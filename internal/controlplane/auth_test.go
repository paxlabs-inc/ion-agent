package controlplane

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type mutableClock struct {
	mu sync.Mutex
	at time.Time
}

func (clock *mutableClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.at
}

func (clock *mutableClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.at = clock.at.Add(duration)
}

func TestCookieAuthenticatorAndCSRFBindToSignedSession(t *testing.T) {
	clock := &mutableClock{at: time.Unix(7_000, 0).UTC()}
	authenticator, err := NewCookieAuthenticator(
		[]byte("01234567890123456789012345678901"),
		clock,
		30*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	sessionCookie, csrfCookie, err := authenticator.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	if !sessionCookie.HttpOnly || !sessionCookie.Secure ||
		sessionCookie.SameSite != http.SameSiteStrictMode ||
		csrfCookie.HttpOnly || !csrfCookie.Secure {
		t.Fatalf("cookie flags session=%+v csrf=%+v", sessionCookie, csrfCookie)
	}
	request := httptest.NewRequest(http.MethodPost, "https://operator.example/v1/rpc", nil)
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set(CSRFHeaderName, csrfCookie.Value)
	actor, err := authenticator.Authenticate(request)
	if err != nil || actor.ActorID != actorID {
		t.Fatalf("authentication = %+v, %v", actor, err)
	}
	if err := authenticator.ValidateCSRF(request, actor); err != nil {
		t.Fatalf("valid CSRF rejected: %v", err)
	}
	if err := authenticator.Revoke(actor); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	if _, err := authenticator.Authenticate(request); err == nil {
		t.Fatal("revoked session accepted")
	}
	request.Header.Set(CSRFHeaderName, csrfCookie.Value+"tampered")
	if err := authenticator.ValidateCSRF(request, actor); err == nil {
		t.Fatal("tampered CSRF accepted")
	}

	tampered := *sessionCookie
	tampered.Value = strings.Replace(sessionCookie.Value, ".", "A.", 1)
	tamperedRequest := httptest.NewRequest(http.MethodGet, "https://operator.example", nil)
	tamperedRequest.AddCookie(&tampered)
	if _, err := authenticator.Authenticate(tamperedRequest); err == nil {
		t.Fatal("tampered session accepted")
	}
	clock.Advance(31 * time.Minute)
	if _, err := authenticator.Authenticate(request); err == nil {
		t.Fatal("expired session accepted")
	}
}

func TestTicketAndCapabilityAreBoundedSingleUseAndExpire(t *testing.T) {
	clock := &mutableClock{at: time.Unix(8_000, 0).UTC()}
	manager, err := NewTicketManager(clock, 30*time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	ticket, err := manager.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := manager.Consume(ticket.Value)
	if err != nil || consumed != actorID {
		t.Fatalf("ticket consume = %s, %v", consumed, err)
	}
	if _, err := manager.Consume(ticket.Value); err == nil {
		t.Fatal("replayed ticket accepted")
	}
	expired, err := manager.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, err := manager.Consume(expired.Value); err == nil {
		t.Fatal("expired ticket accepted")
	}
	capabilities, err := NewCapabilityManager(clock, 20*time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := capabilities.Issue(actorID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capabilities.Issue(actorID); err == nil {
		t.Fatal("capability registry exceeded its bound")
	}
	if consumed, err := capabilities.Consume(capability.Value); err != nil ||
		consumed != actorID {
		t.Fatalf("capability consume = %s, %v", consumed, err)
	}
}
