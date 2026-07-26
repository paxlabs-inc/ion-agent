package coordination

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func newTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	key, err := CreateKey(32)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	auth, err := NewAuthenticator(AuthenticatorConfig{
		Key:             key,
		TimestampWindow: 5 * time.Minute,
		Clock:           &testClock{baseTime},
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return auth
}

func Test_AllTenVerbs(t *testing.T) {
	verbs := []Verb{
		VerbAttach, VerbDetach, VerbQuery, VerbMutate,
		VerbEscalate, VerbYield, VerbResume, VerbAbort,
		VerbInherit, VerbDelegate,
	}
	for _, v := range verbs {
		if err := Validate(v); err != nil {
			t.Errorf("Validate(%s) = %v", v, err)
		}
	}
}

func Test_InvalidVerb(t *testing.T) {
	if err := Validate(Verb("INVALID")); err == nil {
		t.Fatal("expected error for invalid verb")
	}
	if err := Validate(VerbAttach); err != nil {
		t.Fatalf("expected no error for valid verb, got %v", err)
	}
}

func Test_SignAndVerify(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()

	args := json.RawMessage(`{"target":"sub-agent-1"}`)
	msg, err := auth.Sign(VerbAttach, args)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if msg.HMACValue == "" {
		t.Fatal("HMAC not set")
	}
	if err := auth.Verify(msg); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func Test_Verify_RejectsTamperedHMAC(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()

	msg, _ := auth.Sign(VerbAttach, json.RawMessage(`{}`))
	msg.HMACValue = "000000000000000000000000000000000000000000000000000000000000000000"
	if err := auth.Verify(msg); err == nil {
		t.Fatal("expected error for tampered HMAC")
	}
}

func Test_Verify_RejectsReplay(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()

	msg, _ := auth.Sign(VerbAttach, json.RawMessage(`{}`))
	// First verify succeeds.
	if err := auth.Verify(msg); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	// Replay should fail.
	if err := auth.Verify(msg); err == nil {
		t.Fatal("expected error for replayed message")
	}
}

func Test_Verify_RejectsExpiredTimestamp(t *testing.T) {
	clock := &testClock{baseTime}
	key, _ := CreateKey(32)
	auth, _ := NewAuthenticator(AuthenticatorConfig{
		Key: key, TimestampWindow: 1 * time.Minute, Clock: clock,
	})
	defer auth.ZeroizeKey()

	msg, _ := auth.Sign(VerbAttach, json.RawMessage(`{}`))

	// Advance time past the window.
	clock.now = clock.now.Add(2 * time.Minute)

	if err := auth.Verify(msg); err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

func Test_Verify_RejectsInvalidVerb(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()

	msg, _ := auth.Sign(VerbAttach, json.RawMessage(`{}`))
	msg.Verb = Verb("INVALID")
	if err := auth.Verify(msg); err == nil {
		t.Fatal("expected error for invalid verb")
	}
}

func Test_PurgeStale(t *testing.T) {
	clock := &testClock{baseTime}
	key, _ := CreateKey(32)
	auth, _ := NewAuthenticator(AuthenticatorConfig{
		Key: key, TimestampWindow: 1 * time.Minute, Clock: clock,
	})
	defer auth.ZeroizeKey()

	// Create some nonces via Verify (which tracks them).
	msg1, _ := auth.Sign(VerbAttach, json.RawMessage(`{}`))
	msg2, _ := auth.Sign(VerbDetach, json.RawMessage(`{}`))
	auth.Verify(msg1)
	auth.Verify(msg2)
	if auth.NonceCount() != 2 {
		t.Fatalf("expected 2 nonces, got %d", auth.NonceCount())
	}

	// Advance time and purge.
	clock.now = clock.now.Add(3 * time.Minute)
	removed := auth.PurgeStale()
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	if auth.NonceCount() != 0 {
		t.Fatalf("expected 0 nonces after purge, got %d", auth.NonceCount())
	}
}

func Test_ZeroizeKey(t *testing.T) {
	auth := newTestAuth(t)
	auth.ZeroizeKey()
	if _, err := auth.Sign(VerbAttach, json.RawMessage(`{}`)); err == nil {
		t.Fatal("Sign succeeded after key zeroization")
	}
}

func Test_NilMessageRejected(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()
	if err := auth.Verify(nil); err == nil {
		t.Fatal("expected error for nil message")
	}
}

func Test_CreateKey_MinSize(t *testing.T) {
	key, err := CreateKey(16)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if len(key) < 32 {
		t.Fatalf("expected minimum 32 bytes, got %d", len(key))
	}
}

func Test_HMAC_UsesExactSpecifiedPayload(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	auth, err := NewAuthenticator(AuthenticatorConfig{
		Key:             key,
		TimestampWindow: time.Minute,
		Clock:           &testClock{baseTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer auth.ZeroizeKey()
	message, err := auth.Sign(VerbDelegate, json.RawMessage(`{"task":"audit"}`))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := message.Canonicalize()
	if err != nil {
		t.Fatal(err)
	}
	wantPayload := append([]byte{}, []byte(VerbDelegate)...)
	wantPayload = append(wantPayload, message.Args...)
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(message.Timestamp))
	wantPayload = append(wantPayload, timestamp[:]...)
	wantPayload = append(wantPayload, message.Nonce...)
	if !bytes.Equal(canonical, wantPayload) {
		t.Fatalf("canonical payload = %x, want %x", canonical, wantPayload)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(wantPayload)
	if message.HMACValue != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("HMAC = %s, want %x", message.HMACValue, mac.Sum(nil))
	}
}

func Test_Sign_CanonicalizesJSONArgumentsDeterministically(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()
	message, err := auth.Sign(
		VerbMutate,
		json.RawMessage(` { "z": 1.0, "a": 9007199254740993 } `),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(message.Args) != `{"a":9007199254740993,"z":1.0}` {
		t.Fatalf("canonical args = %s", message.Args)
	}
	if err := auth.Verify(message); err != nil {
		t.Fatalf("canonical message rejected: %v", err)
	}
}

func Test_Verify_AtomicallyRejectsConcurrentReplay(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()
	message, _ := auth.Sign(VerbQuery, json.RawMessage(`{"id":"x"}`))
	var successes int
	var mu sync.Mutex
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if auth.Verify(message) == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wait.Wait()
	if successes != 1 {
		t.Fatalf("successful concurrent verifications = %d, want 1", successes)
	}
}

func Test_SpawnSession_AuthenticatesBothDirectionsAndIsolatesKeys(t *testing.T) {
	clock := &testClock{baseTime}
	first, err := NewSpawnSession(clock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewSpawnSession(clock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	delegation, err := first.Parent().Sign(
		VerbDelegate,
		json.RawMessage(`{"task":"red-team"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SubAgent().Verify(delegation); err != nil {
		t.Fatalf("sub-agent rejected parent message: %v", err)
	}
	if err := second.SubAgent().Verify(delegation); err == nil {
		t.Fatal("different spawn session accepted delegation")
	}

	yielded, err := first.SubAgent().Sign(
		VerbYield,
		json.RawMessage(`{"result":"complete"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Parent().Verify(yielded); err != nil {
		t.Fatalf("parent rejected sub-agent message: %v", err)
	}
	first.Close()
	if _, err := first.Parent().Sign(VerbAbort, json.RawMessage(`{}`)); err == nil {
		t.Fatal("closed spawn session retained signing authority")
	}
}

func Test_SignAndVerify_RejectMalformedFields(t *testing.T) {
	auth := newTestAuth(t)
	defer auth.ZeroizeKey()
	if _, err := auth.Sign(VerbAttach, nil); err == nil {
		t.Fatal("nil args accepted")
	}
	message, _ := auth.Sign(VerbAttach, json.RawMessage(`{}`))
	message.Nonce = ""
	if err := auth.Verify(message); err == nil {
		t.Fatal("empty nonce accepted")
	}
	if _, err := (*Message)(nil).Canonicalize(); err == nil {
		t.Fatal("nil message canonicalized")
	}
}
