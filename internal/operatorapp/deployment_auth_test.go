package operatorapp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

type authTestClock struct {
	mu sync.Mutex
	at time.Time
}

func (clock *authTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.at
}

func TestDeploymentCredentialsRequireCompleteConfigurationAndVerify(t *testing.T) {
	if _, err := newDeploymentAuthenticator("operator", "", ""); err == nil {
		t.Fatal("partial deployment credentials accepted")
	}
	if _, err := newDeploymentAuthenticator("", "long-enough-password", ""); err == nil {
		t.Fatal("password without username accepted")
	}
	if _, err := newDeploymentAuthenticator(
		"operator", "long-enough-password", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$ZGlnaWVzdGRpZ2VzdGRpZ2VzdA",
	); err == nil {
		t.Fatal("ambiguous password configuration accepted")
	}
	credentials, err := newDeploymentAuthenticator(
		"operator",
		"this-is-a-long-password",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.verify("operator", "this-is-a-long-password") {
		t.Fatal("valid deployment credentials rejected")
	}
	if credentials.verify("operator", "this-is-a-wrong-password") ||
		credentials.verify("another-operator", "this-is-a-long-password") {
		t.Fatal("invalid deployment credentials accepted")
	}
}

func TestDeploymentLoginLogoutAndThrottle(t *testing.T) {
	clock := &authTestClock{at: time.Unix(50_000, 0).UTC()}
	credentials, err := newDeploymentAuthenticator(
		"operator",
		"this-is-a-long-password",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := controlplane.NewCookieAuthenticator(
		[]byte("01234567890123456789012345678901"),
		clock,
		12*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := deploymentAuthHandler{
		credentials: credentials,
		sessions:    sessions,
		limiter:     newLoginLimiter(clock),
		origin:      "https://ion.example.com",
		actorID:     uuid.New(),
	}
	login := httptest.NewRequest(
		http.MethodPost,
		"https://ion.example.com/v1/auth/login",
		bytes.NewBufferString(`{"username":"operator","password":"this-is-a-long-password"}`),
	)
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "https://ion.example.com")
	loginResult := httptest.NewRecorder()
	handler.ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", loginResult.Code, loginResult.Body.String())
	}
	cookies := loginResult.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("login cookies = %d", len(cookies))
	}
	var sessionCookie *http.Cookie
	var csrfCookie *http.Cookie
	for _, cookie := range cookies {
		switch cookie.Name {
		case controlplane.SessionCookieName:
			sessionCookie = cookie
		case controlplane.CSRFCookieName:
			csrfCookie = cookie
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatal("login omitted session or CSRF cookie")
	}

	logout := httptest.NewRequest(
		http.MethodPost,
		"https://ion.example.com/v1/auth/logout",
		nil,
	)
	logout.Header.Set("Origin", "https://ion.example.com")
	logout.Header.Set(controlplane.CSRFHeaderName, csrfCookie.Value)
	logout.AddCookie(sessionCookie)
	logout.AddCookie(csrfCookie)
	logoutResult := httptest.NewRecorder()
	handler.ServeHTTP(logoutResult, logout)
	if logoutResult.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d body=%s", logoutResult.Code, logoutResult.Body.String())
	}
	if _, err := sessions.Authenticate(logout); err == nil {
		t.Fatal("logout did not revoke the authenticated session")
	}

	for attempt := 0; attempt < loginFailureLimit; attempt++ {
		failed := httptest.NewRequest(
			http.MethodPost,
			"https://ion.example.com/v1/auth/login",
			bytes.NewBufferString(`{"username":"operator","password":"this-is-a-wrong-password"}`),
		)
		failed.Header.Set("Content-Type", "application/json")
		failed.Header.Set("Origin", "https://ion.example.com")
		handler.ServeHTTP(httptest.NewRecorder(), failed)
	}
	blocked := httptest.NewRequest(
		http.MethodPost,
		"https://ion.example.com/v1/auth/login",
		bytes.NewBufferString(`{"username":"operator","password":"this-is-a-long-password"}`),
	)
	blocked.Header.Set("Content-Type", "application/json")
	blocked.Header.Set("Origin", "https://ion.example.com")
	blockedResult := httptest.NewRecorder()
	handler.ServeHTTP(blockedResult, blocked)
	if blockedResult.Code != http.StatusTooManyRequests ||
		blockedResult.Header().Get("Retry-After") == "" {
		t.Fatalf(
			"blocked login status=%d retry-after=%q",
			blockedResult.Code,
			blockedResult.Header().Get("Retry-After"),
		)
	}
}
