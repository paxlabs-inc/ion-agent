package controlplane

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	SessionCookieName = "__Host-ion_session"
	CSRFCookieName    = "__Host-ion_csrf"
	CSRFHeaderName    = "X-Ion-CSRF"
)

// BrowserActor is the authenticated server-side browser identity.
type BrowserActor struct {
	ActorID      uuid.UUID
	csrfHash     [32]byte
	sessionNonce [32]byte
	expiresAt    int64
}

// BrowserAuthenticator validates HttpOnly browser sessions and CSRF proofs.
type BrowserAuthenticator interface {
	Authenticate(*http.Request) (BrowserActor, error)
	ValidateCSRF(*http.Request, BrowserActor) error
}

type browserSession struct {
	ActorID   uuid.UUID `json:"actor_id"`
	ExpiresAt int64     `json:"expires_at"`
	CSRFHash  string    `json:"csrf_hash"`
	Nonce     string    `json:"nonce"`
}

// CookieAuthenticator issues signed, short-lived, HttpOnly browser sessions.
type CookieAuthenticator struct {
	signingKey []byte
	clock      types.Clock
	lifetime   time.Duration

	mu      sync.Mutex
	revoked map[[32]byte]int64
}

// NewCookieAuthenticator constructs a stateless signed-cookie authenticator.
func NewCookieAuthenticator(
	signingKey []byte,
	clock types.Clock,
	lifetime time.Duration,
) (*CookieAuthenticator, error) {
	if len(signingKey) < 32 || clock == nil || lifetime <= 0 ||
		lifetime > 24*time.Hour {
		return nil, fmt.Errorf(
			"controlplane: 32-byte signing key, clock, and browser lifetime up to 24h are required",
		)
	}
	return &CookieAuthenticator{
		signingKey: append([]byte(nil), signingKey...),
		clock:      clock, lifetime: lifetime,
		revoked: make(map[[32]byte]int64),
	}, nil
}

// Issue creates the HttpOnly session cookie and a non-secret double-submit
// token cookie. Authentication material is never returned in JSON.
func (authenticator *CookieAuthenticator) Issue(
	actorID uuid.UUID,
) (*http.Cookie, *http.Cookie, error) {
	if actorID == uuid.Nil {
		return nil, nil, fmt.Errorf("controlplane: actor ID is required")
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, nil, err
	}
	nonce, err := randomToken(16)
	if err != nil {
		return nil, nil, err
	}
	csrfHash := sha256.Sum256([]byte(csrf))
	session := browserSession{
		ActorID: actorID, ExpiresAt: authenticator.clock.Now().Add(authenticator.lifetime).Unix(),
		CSRFHash: base64.RawURLEncoding.EncodeToString(csrfHash[:]), Nonce: nonce,
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		return nil, nil, fmt.Errorf("controlplane: encode browser session: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(encoded)
	signature := authenticator.sign(payload)
	value := payload + "." + base64.RawURLEncoding.EncodeToString(signature)
	maxAge := int(authenticator.lifetime.Seconds())
	sessionCookie := &http.Cookie{
		Name: SessionCookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	}
	csrfCookie := &http.Cookie{
		Name: CSRFCookieName, Value: csrf, Path: "/", MaxAge: maxAge,
		HttpOnly: false, Secure: true, SameSite: http.SameSiteStrictMode,
	}
	return sessionCookie, csrfCookie, nil
}

// Authenticate validates signature, expiry, and actor identity.
func (authenticator *CookieAuthenticator) Authenticate(
	request *http.Request,
) (BrowserActor, error) {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil {
		return BrowserActor{}, ErrUnauthorized
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || len(cookie.Value) > 4096 {
		return BrowserActor{}, ErrUnauthorized
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return BrowserActor{}, ErrUnauthorized
	}
	expected := authenticator.sign(parts[0])
	if len(provided) != len(expected) ||
		subtle.ConstantTimeCompare(provided, expected) != 1 {
		return BrowserActor{}, ErrUnauthorized
	}
	encoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return BrowserActor{}, ErrUnauthorized
	}
	var session browserSession
	if err := json.Unmarshal(encoded, &session); err != nil ||
		session.ActorID == uuid.Nil || session.ExpiresAt <= authenticator.clock.Now().Unix() ||
		session.Nonce == "" {
		return BrowserActor{}, ErrUnauthorized
	}
	csrfHash, err := base64.RawURLEncoding.DecodeString(session.CSRFHash)
	if err != nil || len(csrfHash) != sha256.Size {
		return BrowserActor{}, ErrUnauthorized
	}
	nonceHash := sha256.Sum256([]byte(session.Nonce))
	authenticator.mu.Lock()
	authenticator.expireRevocationsLocked()
	_, revoked := authenticator.revoked[nonceHash]
	authenticator.mu.Unlock()
	if revoked {
		return BrowserActor{}, ErrUnauthorized
	}
	var fixed [32]byte
	copy(fixed[:], csrfHash)
	return BrowserActor{
		ActorID: session.ActorID, csrfHash: fixed,
		sessionNonce: nonceHash, expiresAt: session.ExpiresAt,
	}, nil
}

// ValidateCSRF binds the mutation header to the authenticated session.
func (*CookieAuthenticator) ValidateCSRF(
	request *http.Request,
	actor BrowserActor,
) error {
	provided := request.Header.Get(CSRFHeaderName)
	if provided == "" {
		return ErrUnauthorized
	}
	hashed := sha256.Sum256([]byte(provided))
	if subtle.ConstantTimeCompare(hashed[:], actor.csrfHash[:]) != 1 {
		return ErrUnauthorized
	}
	csrfCookie, err := request.Cookie(CSRFCookieName)
	if err != nil || subtle.ConstantTimeCompare(
		[]byte(csrfCookie.Value), []byte(provided),
	) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (authenticator *CookieAuthenticator) sign(payload string) []byte {
	mac := hmac.New(sha256.New, authenticator.signingKey)
	_, _ = mac.Write([]byte(payload)) // hash.Hash Write cannot fail.
	return mac.Sum(nil)
}

// Revoke invalidates one authenticated browser session until its signed expiry.
func (authenticator *CookieAuthenticator) Revoke(actor BrowserActor) error {
	if actor.ActorID == uuid.Nil || actor.expiresAt <= authenticator.clock.Now().Unix() {
		return ErrUnauthorized
	}
	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	authenticator.expireRevocationsLocked()
	if len(authenticator.revoked) >= 10_000 {
		return fmt.Errorf("controlplane: browser session revocation capacity reached")
	}
	authenticator.revoked[actor.sessionNonce] = actor.expiresAt
	return nil
}

func (authenticator *CookieAuthenticator) expireRevocationsLocked() {
	now := authenticator.clock.Now().Unix()
	for nonce, expiresAt := range authenticator.revoked {
		if expiresAt <= now {
			delete(authenticator.revoked, nonce)
		}
	}
}

// ExpireBrowserCookies returns replacement cookies that remove both browser
// authentication proofs from the user agent.
func ExpireBrowserCookies() (*http.Cookie, *http.Cookie) {
	expired := time.Unix(1, 0).UTC()
	sessionCookie := &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: expired, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode,
	}
	csrfCookie := &http.Cookie{
		Name: CSRFCookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: expired, HttpOnly: false, Secure: true,
		SameSite: http.SameSiteStrictMode,
	}
	return sessionCookie, csrfCookie
}

// Ticket is a short-lived, single-use WebSocket credential.
type Ticket struct {
	Value     string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ticketRecord struct {
	actorID   uuid.UUID
	expiresAt time.Time
}

// TicketManager stores only ticket digests and consumes them before upgrade.
type TicketManager struct {
	clock      types.Clock
	lifetime   time.Duration
	maxTickets int

	mu      sync.Mutex
	tickets map[[32]byte]ticketRecord
}

// NewTicketManager constructs a bounded one-time ticket registry.
func NewTicketManager(
	clock types.Clock,
	lifetime time.Duration,
	maxTickets int,
) (*TicketManager, error) {
	if clock == nil || lifetime <= 0 || lifetime > time.Minute ||
		maxTickets <= 0 || maxTickets > 10_000 {
		return nil, fmt.Errorf(
			"controlplane: ticket lifetime up to one minute and bounded capacity are required",
		)
	}
	return &TicketManager{
		clock: clock, lifetime: lifetime, maxTickets: maxTickets,
		tickets: make(map[[32]byte]ticketRecord),
	}, nil
}

// Issue creates a cryptographically random one-time ticket.
func (manager *TicketManager) Issue(actorID uuid.UUID) (Ticket, error) {
	if actorID == uuid.Nil {
		return Ticket{}, fmt.Errorf("controlplane: ticket actor is required")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expireLocked()
	if len(manager.tickets) >= manager.maxTickets {
		return Ticket{}, fmt.Errorf("controlplane: ticket capacity reached")
	}
	value, err := randomToken(32)
	if err != nil {
		return Ticket{}, err
	}
	expiresAt := manager.clock.Now().Add(manager.lifetime)
	manager.tickets[sha256.Sum256([]byte(value))] = ticketRecord{
		actorID: actorID, expiresAt: expiresAt,
	}
	return Ticket{Value: value, ExpiresAt: expiresAt}, nil
}

// Consume atomically invalidates a ticket, including when it has expired.
func (manager *TicketManager) Consume(value string) (uuid.UUID, error) {
	if value == "" || len(value) > 256 {
		return uuid.Nil, ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(value))
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record, exists := manager.tickets[digest]
	delete(manager.tickets, digest)
	if !exists || !manager.clock.Now().Before(record.expiresAt) {
		return uuid.Nil, ErrUnauthorized
	}
	return record.actorID, nil
}

func (manager *TicketManager) expireLocked() {
	now := manager.clock.Now()
	for digest, record := range manager.tickets {
		if !now.Before(record.expiresAt) {
			delete(manager.tickets, digest)
		}
	}
}

// Capability is a short-lived, single-connection local transport credential.
type Capability = Ticket

// CapabilityManager reuses the one-time digest registry semantics for UDS
// connection authentication.
type CapabilityManager struct{ tickets *TicketManager }

// NewCapabilityManager constructs a local capability registry.
func NewCapabilityManager(
	clock types.Clock,
	lifetime time.Duration,
	maxCapabilities int,
) (*CapabilityManager, error) {
	tickets, err := NewTicketManager(clock, lifetime, maxCapabilities)
	if err != nil {
		return nil, err
	}
	return &CapabilityManager{tickets: tickets}, nil
}

// Issue creates a capability for one local connection.
func (manager *CapabilityManager) Issue(actorID uuid.UUID) (Capability, error) {
	return manager.tickets.Issue(actorID)
}

// Consume invalidates and returns the capability's actor.
func (manager *CapabilityManager) Consume(value string) (uuid.UUID, error) {
	return manager.tickets.Consume(value)
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("controlplane: generate credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
