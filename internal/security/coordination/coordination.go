// Package coordination implements the ten closed coordination verbs with
// HMAC-SHA256 authentication, replay protection, and timestamp windows.
package coordination

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Verb is one of the ten closed coordination verbs.
type Verb string

const (
	VerbAttach   Verb = "ATTACH"
	VerbDetach   Verb = "DETACH"
	VerbQuery    Verb = "QUERY"
	VerbMutate   Verb = "MUTATE"
	VerbEscalate Verb = "ESCALATE"
	VerbYield    Verb = "YIELD"
	VerbResume   Verb = "RESUME"
	VerbAbort    Verb = "ABORT"
	VerbInherit  Verb = "INHERIT"
	VerbDelegate Verb = "DELEGATE"
)

// Validate checks if a verb is one of the ten closed coordination verbs.
func Validate(v Verb) error {
	switch v {
	case VerbAttach,
		VerbDetach,
		VerbQuery,
		VerbMutate,
		VerbEscalate,
		VerbYield,
		VerbResume,
		VerbAbort,
		VerbInherit,
		VerbDelegate:
		return nil
	default:
		return fmt.Errorf("coordination: invalid verb %q", v)
	}
}

// Message is an authenticated coordination message.
type Message struct {
	ID        string          `json:"id"`
	Verb      Verb            `json:"verb"`
	Args      json.RawMessage `json:"args"`
	Timestamp int64           `json:"timestamp"`
	Nonce     string          `json:"nonce"`
	HMACValue string          `json:"hmac"` // hex-encoded
}

// Authenticator handles HMAC-SHA256 signing and verification for
// coordination messages.
type Authenticator struct {
	mu              sync.Mutex
	key             []byte
	nonceTracker    map[string]time.Time
	timestampWindow time.Duration
	clock           Clock
	closed          bool
}

// Clock abstracts time for testing.
type Clock interface {
	Now() time.Time
}

// AuthenticatorConfig configures the authenticator.
type AuthenticatorConfig struct {
	Key             []byte
	TimestampWindow time.Duration
	Clock           Clock
}

// NewAuthenticator creates an HMAC-SHA256 authenticator.
func NewAuthenticator(config AuthenticatorConfig) (*Authenticator, error) {
	if len(config.Key) < 32 {
		return nil, fmt.Errorf("coordination: key must contain at least 32 bytes")
	}
	if config.TimestampWindow < 0 {
		return nil, fmt.Errorf("coordination: timestamp window cannot be negative")
	}
	if config.TimestampWindow == 0 {
		config.TimestampWindow = 5 * time.Minute
	}
	if config.Clock == nil {
		return nil, fmt.Errorf("coordination: clock is required")
	}
	// Copy the key to prevent external mutation.
	key := make([]byte, len(config.Key))
	copy(key, config.Key)
	return &Authenticator{
		key:             key,
		nonceTracker:    make(map[string]time.Time),
		timestampWindow: config.TimestampWindow,
		clock:           config.Clock,
	}, nil
}

// Sign creates an authenticated coordination message.
func (a *Authenticator) Sign(verb Verb, args json.RawMessage) (*Message, error) {
	if err := Validate(verb); err != nil {
		return nil, err
	}
	canonicalArgs, err := canonicalJSON(args)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, fmt.Errorf("coordination: authenticator is closed")
	}

	now := a.clock.Now()
	nonce, err := randomNonce()
	if err != nil {
		return nil, err
	}
	msg := &Message{
		ID:        uuid.New().String(),
		Verb:      verb,
		Args:      canonicalArgs,
		Timestamp: now.UnixNano(),
		Nonce:     nonce,
	}

	h := a.computeHMACLocked(msg)
	msg.HMACValue = hex.EncodeToString(h)

	// Nonce tracking is done in Verify, not Sign.
	// This allows a signed message to be verified exactly once.

	return msg, nil
}

// Verify checks an authenticated coordination message. It verifies:
// 1. Verb is one of the ten closed verbs
// 2. HMAC is valid (constant-time comparison)
// 3. Timestamp is within the allowed window
// 4. Nonce has not been seen before (replay protection)
func (a *Authenticator) Verify(msg *Message) error {
	if msg == nil {
		return fmt.Errorf("coordination: nil message")
	}

	// 1. Validate verb.
	if err := Validate(msg.Verb); err != nil {
		return err
	}
	if msg.Timestamp == 0 || msg.Nonce == "" {
		return fmt.Errorf("coordination: timestamp and nonce are required")
	}
	nonce, err := hex.DecodeString(msg.Nonce)
	if err != nil || len(nonce) != 16 {
		return fmt.Errorf("coordination: nonce must be 16 random bytes")
	}

	provided, err := hex.DecodeString(msg.HMACValue)
	if err != nil {
		return fmt.Errorf("coordination: invalid HMAC encoding: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fmt.Errorf("coordination: authenticator is closed")
	}

	// 2. Verify HMAC (constant-time comparison).
	expected := a.computeHMACLocked(msg)
	if subtle.ConstantTimeCompare(expected, provided) != 1 {
		return fmt.Errorf("coordination: HMAC verification failed")
	}
	canonicalArgs, err := canonicalJSON(msg.Args)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonicalArgs, msg.Args) {
		return fmt.Errorf("coordination: args are not canonically encoded")
	}

	// 3. Verify timestamp window.
	now := a.clock.Now()
	msgTime := time.Unix(0, msg.Timestamp)
	age := now.Sub(msgTime)
	if age < 0 {
		age = -age
	}
	if age > a.timestampWindow {
		return fmt.Errorf("coordination: timestamp outside window (age %s, window %s)", age, a.timestampWindow)
	}

	// 4. Verify nonce (replay protection).
	if _, exists := a.nonceTracker[msg.Nonce]; exists {
		return fmt.Errorf("coordination: nonce %q already used (replay attack)", msg.Nonce)
	}
	a.nonceTracker[msg.Nonce] = now

	return nil
}

// computeHMACLocked computes the exact binding payload required by
// STANDARD-SEC-020: verb_type || args || timestamp || nonce.
func (a *Authenticator) computeHMACLocked(msg *Message) []byte {
	mac := hmac.New(sha256.New, a.key)

	_, _ = mac.Write([]byte(msg.Verb))
	_, _ = mac.Write(msg.Args)

	// Timestamp as big-endian int64 for determinism.
	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], uint64(msg.Timestamp))
	_, _ = mac.Write(tsBuf[:])

	_, _ = mac.Write([]byte(msg.Nonce))

	return mac.Sum(nil)
}

// PurgeStale removes nonces older than 2x the timestamp window to prevent
// unbounded memory growth.
func (a *Authenticator) PurgeStale() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := a.clock.Now().Add(-2 * a.timestampWindow)
	removed := 0
	for nonce, t := range a.nonceTracker {
		if t.Before(cutoff) {
			delete(a.nonceTracker, nonce)
			removed++
		}
	}
	return removed
}

// ZeroizeKey clears the key material from memory.
func (a *Authenticator) ZeroizeKey() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.key {
		a.key[i] = 0
	}
	a.closed = true
}

// NonceCount returns the number of tracked nonces (for testing).
func (a *Authenticator) NonceCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.nonceTracker)
}

// CreateKey creates a new random key of the specified size (minimum 32 bytes).
func CreateKey(size int) ([]byte, error) {
	if size < 32 {
		size = 32 // minimum 256 bits
	}
	key := make([]byte, size)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("coordination: generate key: %w", err)
	}
	return key, nil
}

// Canonicalize serializes the canonical form for external verification.
func (msg *Message) Canonicalize() ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("coordination: nil message")
	}
	if err := Validate(msg.Verb); err != nil {
		return nil, err
	}
	canonicalArgs, err := canonicalJSON(msg.Args)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonicalArgs, msg.Args) {
		return nil, fmt.Errorf("coordination: args are not canonically encoded")
	}
	payload := make([]byte, 0, len(msg.Verb)+len(msg.Args)+8+len(msg.Nonce))
	payload = append(payload, msg.Verb...)
	payload = append(payload, msg.Args...)
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(msg.Timestamp))
	payload = append(payload, timestamp[:]...)
	payload = append(payload, msg.Nonce...)
	return payload, nil
}

func randomNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("coordination: generate nonce: %w", err)
	}
	return hex.EncodeToString(nonce[:]), nil
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("coordination: args must be valid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("coordination: args must be valid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("coordination: args must contain one JSON value")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("coordination: canonicalize args: %w", err)
	}
	return canonical, nil
}

// Endpoint is one side of a spawn-scoped authenticated channel. It exposes no
// session key and therefore cannot leak key material through coordination APIs.
type Endpoint struct {
	auth *Authenticator
}

func (endpoint *Endpoint) Sign(verb Verb, args json.RawMessage) (*Message, error) {
	return endpoint.auth.Sign(verb, args)
}

func (endpoint *Endpoint) Verify(message *Message) error {
	return endpoint.auth.Verify(message)
}

// SpawnSession owns a fresh CSPRNG key and two isolated replay windows: one
// for the parent endpoint and one for the sub-agent endpoint.
type SpawnSession struct {
	parent   *Endpoint
	subAgent *Endpoint
}

// NewSpawnSession creates authenticated endpoints for one sub-agent spawn.
func NewSpawnSession(clock Clock, timestampWindow time.Duration) (*SpawnSession, error) {
	key, err := CreateKey(32)
	if err != nil {
		return nil, err
	}
	defer func() {
		for index := range key {
			key[index] = 0
		}
	}()
	parentAuth, err := NewAuthenticator(AuthenticatorConfig{
		Key:             key,
		TimestampWindow: timestampWindow,
		Clock:           clock,
	})
	if err != nil {
		return nil, err
	}
	subAgentAuth, err := NewAuthenticator(AuthenticatorConfig{
		Key:             key,
		TimestampWindow: timestampWindow,
		Clock:           clock,
	})
	if err != nil {
		parentAuth.ZeroizeKey()
		return nil, err
	}
	return &SpawnSession{
		parent:   &Endpoint{auth: parentAuth},
		subAgent: &Endpoint{auth: subAgentAuth},
	}, nil
}

func (session *SpawnSession) Parent() *Endpoint {
	return session.parent
}

func (session *SpawnSession) SubAgent() *Endpoint {
	return session.subAgent
}

// Close zeroizes both endpoint key copies.
func (session *SpawnSession) Close() {
	if session == nil {
		return
	}
	session.parent.auth.ZeroizeKey()
	session.subAgent.auth.ZeroizeKey()
}
