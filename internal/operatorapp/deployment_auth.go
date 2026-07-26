package operatorapp

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	"golang.org/x/crypto/argon2"
)

const (
	deploymentPasswordMinimum = 12
	deploymentPasswordMaximum = 1024
	deploymentUsernameMaximum = 128
	loginBodyMaximum          = 8 << 10
	loginFailureLimit         = 5
	loginFailureWindow        = 15 * time.Minute
)

type deploymentAuthenticator struct {
	usernameDigest [sha256.Size]byte
	salt           []byte
	passwordDigest []byte
	memory         uint32
	iterations     uint32
	parallelism    uint8
}

type loginLimiter struct {
	clock  types.Clock
	active chan struct{}

	mu           sync.Mutex
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

type deploymentAuthHandler struct {
	credentials *deploymentAuthenticator
	sessions    *controlplane.CookieAuthenticator
	limiter     *loginLimiter
	origin      string
	actorID     uuid.UUID
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authStatus struct {
	Required      bool `json:"required"`
	Authenticated bool `json:"authenticated"`
}

func newDeploymentAuthenticator(
	rawUsername string,
	password string,
	passwordHash string,
) (*deploymentAuthenticator, error) {
	username := strings.TrimSpace(rawUsername)
	hasUsername := username != ""
	hasPassword := password != ""
	hasHash := strings.TrimSpace(passwordHash) != ""
	if !hasUsername && !hasPassword && !hasHash {
		return nil, nil
	}
	if !hasUsername || hasPassword == hasHash {
		return nil, fmt.Errorf(
			"operator auth: set ION_AUTH_USERNAME and exactly one of ION_AUTH_PASSWORD or ION_AUTH_PASSWORD_HASH",
		)
	}
	if len(username) > deploymentUsernameMaximum ||
		strings.ContainsAny(username, "\r\n\x00") {
		return nil, fmt.Errorf("operator auth: username must be 1-%d safe characters", deploymentUsernameMaximum)
	}

	credentials := &deploymentAuthenticator{
		usernameDigest: sha256.Sum256([]byte(username)),
	}
	if hasPassword {
		if len(password) < deploymentPasswordMinimum ||
			len(password) > deploymentPasswordMaximum {
			return nil, fmt.Errorf(
				"operator auth: ION_AUTH_PASSWORD must be %d-%d characters",
				deploymentPasswordMinimum,
				deploymentPasswordMaximum,
			)
		}
		credentials.memory = 64 * 1024
		credentials.iterations = 3
		credentials.parallelism = 2
		credentials.salt = make([]byte, 16)
		if _, err := rand.Read(credentials.salt); err != nil {
			return nil, fmt.Errorf("operator auth: generate password salt: %w", err)
		}
		passwordBytes := []byte(password)
		credentials.passwordDigest = argon2.IDKey(
			passwordBytes,
			credentials.salt,
			credentials.iterations,
			credentials.memory,
			credentials.parallelism,
			32,
		)
		for index := range passwordBytes {
			passwordBytes[index] = 0
		}
		return credentials, nil
	}

	memory, iterations, parallelism, salt, digest, err :=
		parseArgon2idHash(strings.TrimSpace(passwordHash))
	if err != nil {
		return nil, err
	}
	credentials.memory = memory
	credentials.iterations = iterations
	credentials.parallelism = parallelism
	credentials.salt = salt
	credentials.passwordDigest = digest
	return credentials, nil
}

func parseArgon2idHash(
	encoded string,
) (uint32, uint32, uint8, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != "v=19" {
		return 0, 0, 0, nil, nil, fmt.Errorf(
			"operator auth: ION_AUTH_PASSWORD_HASH must be an Argon2id v=19 PHC string",
		)
	}
	parameters := strings.Split(parts[3], ",")
	if len(parameters) != 3 {
		return 0, 0, 0, nil, nil, fmt.Errorf(
			"operator auth: ION_AUTH_PASSWORD_HASH has unsupported Argon2id parameters",
		)
	}
	memoryValue, memoryErr := strconv.ParseUint(
		strings.TrimPrefix(parameters[0], "m="), 10, 32,
	)
	iterationValue, iterationErr := strconv.ParseUint(
		strings.TrimPrefix(parameters[1], "t="), 10, 32,
	)
	parallelismValue, parallelismErr := strconv.ParseUint(
		strings.TrimPrefix(parameters[2], "p="), 10, 8,
	)
	memory := uint32(memoryValue)
	iterations := uint32(iterationValue)
	parallelism := uint8(parallelismValue)
	if !strings.HasPrefix(parameters[0], "m=") ||
		!strings.HasPrefix(parameters[1], "t=") ||
		!strings.HasPrefix(parameters[2], "p=") ||
		memoryErr != nil || iterationErr != nil || parallelismErr != nil ||
		memory < 16*1024 || memory > 256*1024 ||
		iterations < 1 || iterations > 10 ||
		parallelism < 1 || parallelism > 8 {
		return 0, 0, 0, nil, nil, fmt.Errorf(
			"operator auth: ION_AUTH_PASSWORD_HASH has unsupported Argon2id parameters",
		)
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return 0, 0, 0, nil, nil, fmt.Errorf(
			"operator auth: ION_AUTH_PASSWORD_HASH has an invalid salt",
		)
	}
	digest, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(digest) < 16 || len(digest) > 64 {
		return 0, 0, 0, nil, nil, fmt.Errorf(
			"operator auth: ION_AUTH_PASSWORD_HASH has an invalid digest",
		)
	}
	return memory, iterations, parallelism, salt, digest, nil
}

func (credentials *deploymentAuthenticator) verify(username, password string) bool {
	if credentials == nil ||
		len(username) > deploymentUsernameMaximum ||
		len(password) > deploymentPasswordMaximum {
		return false
	}
	usernameDigest := sha256.Sum256([]byte(username))
	passwordBytes := []byte(password)
	passwordDigest := argon2.IDKey(
		passwordBytes,
		credentials.salt,
		credentials.iterations,
		credentials.memory,
		credentials.parallelism,
		uint32(len(credentials.passwordDigest)),
	)
	usernameMatches := subtle.ConstantTimeCompare(
		usernameDigest[:],
		credentials.usernameDigest[:],
	)
	passwordMatches := subtle.ConstantTimeCompare(
		passwordDigest,
		credentials.passwordDigest,
	)
	for index := range passwordDigest {
		passwordDigest[index] = 0
	}
	for index := range passwordBytes {
		passwordBytes[index] = 0
	}
	return usernameMatches&passwordMatches == 1
}

func newLoginLimiter(clock types.Clock) *loginLimiter {
	return &loginLimiter{clock: clock, active: make(chan struct{}, 2)}
}

func (limiter *loginLimiter) begin() bool {
	select {
	case limiter.active <- struct{}{}:
		return true
	default:
		return false
	}
}

func (limiter *loginLimiter) end() {
	<-limiter.active
}

func (limiter *loginLimiter) retryAfter() time.Duration {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.clock.Now()
	if limiter.blockedUntil.After(now) {
		return limiter.blockedUntil.Sub(now)
	}
	return 0
}

func (limiter *loginLimiter) failed() {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.clock.Now()
	if limiter.windowStart.IsZero() || now.Sub(limiter.windowStart) >= loginFailureWindow {
		limiter.windowStart = now
		limiter.failures = 0
	}
	limiter.failures++
	if limiter.failures >= loginFailureLimit {
		limiter.blockedUntil = now.Add(loginFailureWindow)
		limiter.windowStart = now
		limiter.failures = 0
	}
}

func (limiter *loginLimiter) succeeded() {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.failures = 0
	limiter.windowStart = time.Time{}
	limiter.blockedUntil = time.Time{}
}

func (handler deploymentAuthHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	setAuthSecurityHeaders(writer.Header())
	switch request.URL.Path {
	case "/v1/auth/status":
		handler.serveStatus(writer, request)
	case "/v1/auth/login":
		handler.serveLogin(writer, request)
	case "/v1/auth/logout":
		handler.serveLogout(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (handler deploymentAuthHandler) serveStatus(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, err := handler.sessions.Authenticate(request)
	writeAuthJSON(writer, http.StatusOK, authStatus{
		Required:      handler.credentials != nil,
		Authenticated: err == nil,
	})
}

func (handler deploymentAuthHandler) serveLogin(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if handler.credentials == nil {
		http.NotFound(writer, request)
		return
	}
	if !handler.originAllowed(request) {
		writeAuthError(writer, http.StatusForbidden, "Login was rejected.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAuthError(writer, http.StatusUnsupportedMediaType, "Login was rejected.")
		return
	}
	if retryAfter := handler.limiter.retryAfter(); retryAfter > 0 {
		writer.Header().Set(
			"Retry-After",
			strconv.FormatInt(max(1, int64(retryAfter.Round(time.Second)/time.Second)), 10),
		)
		writeAuthError(writer, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}
	if !handler.limiter.begin() {
		writer.Header().Set("Retry-After", "1")
		writeAuthError(writer, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}
	defer handler.limiter.end()
	request.Body = http.MaxBytesReader(writer, request.Body, loginBodyMaximum)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var supplied loginRequest
	if err := decoder.Decode(&supplied); err != nil || !atJSONEnd(decoder) {
		handler.limiter.failed()
		writeAuthError(writer, http.StatusUnauthorized, "Username or password is incorrect.")
		return
	}
	if !handler.credentials.verify(supplied.Username, supplied.Password) {
		handler.limiter.failed()
		writeAuthError(writer, http.StatusUnauthorized, "Username or password is incorrect.")
		return
	}
	handler.limiter.succeeded()
	sessionCookie, csrfCookie, err := handler.sessions.Issue(handler.actorID)
	if err != nil {
		writeAuthError(writer, http.StatusServiceUnavailable, "Login is temporarily unavailable.")
		return
	}
	http.SetCookie(writer, sessionCookie)
	http.SetCookie(writer, csrfCookie)
	writeAuthJSON(writer, http.StatusOK, authStatus{
		Required: true, Authenticated: true,
	})
}

func (handler deploymentAuthHandler) serveLogout(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, err := handler.sessions.Authenticate(request)
	if err != nil {
		writeAuthError(writer, http.StatusUnauthorized, "Authentication is required.")
		return
	}
	if !handler.originAllowed(request) ||
		handler.sessions.ValidateCSRF(request, actor) != nil {
		writeAuthError(writer, http.StatusForbidden, "Logout was rejected.")
		return
	}
	if err := handler.sessions.Revoke(actor); err != nil {
		writeAuthError(writer, http.StatusServiceUnavailable, "Logout is temporarily unavailable.")
		return
	}
	sessionCookie, csrfCookie := controlplane.ExpireBrowserCookies()
	http.SetCookie(writer, sessionCookie)
	http.SetCookie(writer, csrfCookie)
	writer.WriteHeader(http.StatusNoContent)
}

func (handler deploymentAuthHandler) originAllowed(request *http.Request) bool {
	origin, err := normalizeDeploymentOrigin(request.Header.Get("Origin"))
	return err == nil && origin == handler.origin
}

func normalizeDeploymentOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("operator auth: invalid browser origin")
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), nil
}

func deploymentOriginIsLoopback(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return strings.EqualFold(host, "localhost") ||
		strings.HasSuffix(strings.ToLower(host), ".localhost") ||
		netIPIsLoopback(host)
}

func netIPIsLoopback(host string) bool {
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func atJSONEnd(decoder *json.Decoder) bool {
	var extra any
	return decoder.Decode(&extra) == io.EOF
}

func writeAuthJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAuthError(writer http.ResponseWriter, status int, message string) {
	writeAuthJSON(writer, status, map[string]string{"error": message})
}

func setAuthSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}
