package agentaccount

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type accountClock struct {
	now time.Time
}

func (clock *accountClock) Now() time.Time {
	return clock.now
}

func TestAgentAccountConformanceHappyPathReplayScopeRevocationAndRecovery(t *testing.T) {
	human := key(t)
	otherHuman := key(t)
	agent := key(t)
	clock := &accountClock{now: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)}
	registry := openRegistry(t, clock, map[string]*ecdsa.PrivateKey{
		"human-primary": human,
		"human-other":   otherHuman,
	})
	challenge, err := registry.IssueChallenge(
		"https://accounts.example", "accounts.example",
		[]string{"read", "draft"}, "terms-7", time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	delegation := register(t, registry, challenge, "human-primary", human, agent)
	if delegation.AccountType != AccountTypeAgent ||
		delegation.Origin != "https://accounts.example" ||
		len(delegation.AllowedScopes) != 2 ||
		!delegation.RecoveryRequiresHuman {
		t.Fatalf("delegation = %+v", delegation)
	}
	if _, err := registry.VerifyRegistration(registrationProof(
		t, challenge, "human-primary", human, agent,
	)); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("challenge replay error = %v", err)
	}
	request := RequestProof{
		RevocationHandle: delegation.RevocationHandle,
		Origin:           "https://accounts.example", Scope: "draft",
		Method: "POST", Path: "/drafts", BodyHash: "sha256:abc",
		Nonce: "request-one", IssuedAt: clock.now,
	}
	request.Signature, err = SignRequest(agent, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Authorize(request); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Authorize(request); err == nil ||
		!strings.Contains(err.Error(), "replayed") {
		t.Fatalf("request replay error = %v", err)
	}
	escalated := request
	escalated.Scope = "submit-with-approval"
	escalated.Nonce = "request-two"
	escalated.Signature, _ = SignRequest(agent, escalated)
	if _, err := registry.Authorize(escalated); err == nil ||
		!strings.Contains(err.Error(), "exceeds delegation") {
		t.Fatalf("scope escalation error = %v", err)
	}
	relayed := request
	relayed.Origin = "https://phishing.example"
	relayed.Nonce = "request-three"
	relayed.Signature, _ = SignRequest(agent, relayed)
	if _, err := registry.Authorize(relayed); err == nil ||
		!strings.Contains(err.Error(), "origin") {
		t.Fatalf("phishing relay error = %v", err)
	}
	recovery, err := registry.BeginRecovery(
		"https://accounts.example", "accounts.example",
		delegation.RevocationHandle, []string{"read"}, "terms-8", time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	recoveredAgent := key(t)
	recovered := register(
		t, registry, recovery, "human-primary", human, recoveredAgent,
	)
	if recovered.RevocationHandle == delegation.RevocationHandle {
		t.Fatal("recovery reused revocation handle")
	}
	afterRecovery := request
	afterRecovery.Nonce = "request-four"
	afterRecovery.Signature, _ = SignRequest(agent, afterRecovery)
	if _, err := registry.Authorize(afterRecovery); err == nil ||
		!strings.Contains(err.Error(), "revoked") {
		t.Fatalf("old delegation after recovery error = %v", err)
	}
	if err := registry.Revoke(recovered.RevocationHandle); err != nil {
		t.Fatal(err)
	}
	recoveredRequest := RequestProof{
		RevocationHandle: recovered.RevocationHandle,
		Origin:           recovered.Origin, Scope: "read", Method: "GET", Path: "/account",
		Nonce: "recovered-one", IssuedAt: clock.now,
	}
	recoveredRequest.Signature, _ = SignRequest(recoveredAgent, recoveredRequest)
	if _, err := registry.Authorize(recoveredRequest); err == nil ||
		!strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked delegation error = %v", err)
	}
}

func TestAgentAccountRejectsOriginConfusionExpiryProofAndHumanAccountMixing(t *testing.T) {
	human := key(t)
	agent := key(t)
	clock := &accountClock{now: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)}
	registry := openRegistry(t, clock, map[string]*ecdsa.PrivateKey{
		"human-primary": human,
	})
	if _, err := registry.IssueChallenge(
		"https://other.example", "other.example", []string{"read"}, "terms", time.Hour,
	); err == nil {
		t.Fatal("origin confusion challenge succeeded")
	}
	if _, err := registry.IssueChallenge(
		"https://accounts.example", "example", []string{"read"}, "terms", time.Hour,
	); err == nil {
		t.Fatal("RP confusion challenge succeeded")
	}
	if _, err := registry.IssueChallenge(
		"https://accounts.example", "accounts.example", []string{"admin"}, "terms", time.Hour,
	); err == nil {
		t.Fatal("unavailable scope challenge succeeded")
	}
	challenge, err := registry.IssueChallenge(
		"https://accounts.example", "accounts.example", []string{"read"}, "terms", time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	mixed := challenge
	mixed.AccountType = "human"
	if _, err := registry.VerifyRegistration(registrationProof(
		t, mixed, "human-primary", human, agent,
	)); err == nil {
		t.Fatal("human account ceremony was accepted as agent delegation")
	}
	clock.now = clock.now.Add(2 * time.Minute)
	if _, err := registry.VerifyRegistration(registrationProof(
		t, challenge, "human-primary", human, agent,
	)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired challenge error = %v", err)
	}
	fresh, err := registry.IssueChallenge(
		"https://accounts.example", "accounts.example", []string{"read"}, "terms", time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	proof := registrationProof(t, fresh, "human-primary", human, agent)
	proof.AgentSignature = proof.HumanSignature
	if _, err := registry.VerifyRegistration(proof); err == nil ||
		!strings.Contains(err.Error(), "agent proof") {
		t.Fatalf("missing distinct agent proof error = %v", err)
	}
}

func TestAgentAccountDiscoveryAndThirdSignupChoice(t *testing.T) {
	choices := SignupChoices()
	if len(choices) != 3 || choices[2].ID != "agent" ||
		choices[2].AccountType != AccountTypeAgent ||
		!strings.Contains(choices[2].Description, "explicit") {
		t.Fatalf("signup choices = %+v", choices)
	}
	human := key(t)
	clock := &accountClock{now: time.Now().UTC()}
	discovery := testDiscovery()
	discovery.RevocationEndpoint = "https://phishing.example/revoke"
	if _, err := Open(Config{
		Discovery:        discovery,
		HumanCredentials: map[string][]byte{"human": publicBytes(human)},
		Clock:            clock,
	}); err == nil || !strings.Contains(err.Error(), "same-origin") {
		t.Fatalf("cross-origin discovery error = %v", err)
	}
}

func TestPublishedCompatibilityVectors(t *testing.T) {
	payload, err := os.ReadFile("../../docs/agent-account-profile-v1-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Profile       string   `json:"profile"`
		SignupChoices []string `json:"signup_choices"`
		Cases         []struct {
			Name   string   `json:"name"`
			Origin string   `json:"origin"`
			RPID   string   `json:"rp_id"`
			Scopes []string `json:"scopes"`
			Valid  bool     `json:"valid"`
		} `json:"challenge_cases"`
	}
	if err := json.Unmarshal(payload, &vectors); err != nil {
		t.Fatal(err)
	}
	if vectors.Profile != ProfileVersion || len(vectors.SignupChoices) != 3 {
		t.Fatalf("vector header = %+v", vectors)
	}
	human := key(t)
	clock := &accountClock{now: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)}
	registry := openRegistry(t, clock, map[string]*ecdsa.PrivateKey{"human": human})
	for _, vector := range vectors.Cases {
		t.Run(vector.Name, func(t *testing.T) {
			_, err := registry.IssueChallenge(
				vector.Origin, vector.RPID, vector.Scopes, "terms-vector", time.Hour,
			)
			if vector.Valid && err != nil {
				t.Fatal(err)
			}
			if !vector.Valid && err == nil {
				t.Fatal("invalid compatibility vector was accepted")
			}
		})
	}
}

func openRegistry(
	t *testing.T,
	clock *accountClock,
	humans map[string]*ecdsa.PrivateKey,
) *Registry {
	t.Helper()
	publicKeys := make(map[string][]byte, len(humans))
	for id, privateKey := range humans {
		publicKeys[id] = publicBytes(privateKey)
	}
	registry, err := Open(Config{
		Discovery: testDiscovery(), HumanCredentials: publicKeys, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func testDiscovery() Discovery {
	return Discovery{
		Profile: ProfileVersion, Origin: "https://accounts.example",
		RPID:                 "accounts.example",
		RegistrationEndpoint: "https://accounts.example/agent/register",
		ChallengeEndpoint:    "https://accounts.example/agent/challenge",
		RevocationEndpoint:   "https://accounts.example/agent/revoke",
		RecoveryEndpoint:     "https://accounts.example/agent/recover",
		SupportedProof:       []string{"webauthn-delegation-v1"},
		AvailableScopes:      []string{"read", "draft", "submit-with-approval"},
		HumanHandoff:         []string{"terms", "payment", "identity", "recovery"},
	}
}

func register(
	t *testing.T,
	registry *Registry,
	challenge Challenge,
	humanID string,
	human *ecdsa.PrivateKey,
	agent *ecdsa.PrivateKey,
) Delegation {
	t.Helper()
	delegation, err := registry.VerifyRegistration(
		registrationProof(t, challenge, humanID, human, agent),
	)
	if err != nil {
		t.Fatal(err)
	}
	return delegation
}

func registrationProof(
	t *testing.T,
	challenge Challenge,
	humanID string,
	human *ecdsa.PrivateKey,
	agent *ecdsa.PrivateKey,
) RegistrationProof {
	t.Helper()
	humanSignature, err := SignChallenge(human, challenge)
	if err != nil {
		t.Fatal(err)
	}
	agentSignature, err := SignChallenge(agent, challenge)
	if err != nil {
		t.Fatal(err)
	}
	return RegistrationProof{
		Challenge: challenge, HumanCredential: humanID,
		HumanSignature: humanSignature,
		AgentPublicKey: EncodePublicKey(&agent.PublicKey),
		AgentSignature: agentSignature,
	}
}

func key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}

func publicBytes(privateKey *ecdsa.PrivateKey) []byte {
	return elliptic.Marshal(
		elliptic.P256(), privateKey.PublicKey.X, privateKey.PublicKey.Y,
	)
}
