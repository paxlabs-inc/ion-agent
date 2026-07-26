// Package agentaccount implements scoped agent registration, delegation, and recovery.
package agentaccount

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	ProfileVersion            = "https://ion.matrixmcl.com/spec/agent-account/v1"
	AccountTypeAgent          = "agent"
	maximumDelegationLifetime = 24 * time.Hour
	maximumRequestSkew        = 5 * time.Minute
)

type Discovery struct {
	Profile              string   `json:"profile"`
	Origin               string   `json:"origin"`
	RPID                 string   `json:"rp_id"`
	RegistrationEndpoint string   `json:"registration_endpoint"`
	ChallengeEndpoint    string   `json:"challenge_endpoint"`
	RevocationEndpoint   string   `json:"revocation_endpoint"`
	RecoveryEndpoint     string   `json:"recovery_endpoint"`
	SupportedProof       []string `json:"supported_proof"`
	AvailableScopes      []string `json:"available_scopes"`
	HumanHandoff         []string `json:"human_handoff"`
}

type SignupChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	AccountType string `json:"account_type"`
	Description string `json:"description"`
}

type Challenge struct {
	Profile                  string    `json:"profile"`
	ID                       uuid.UUID `json:"id"`
	Origin                   string    `json:"origin"`
	RPID                     string    `json:"rp_id"`
	AccountType              string    `json:"account_type"`
	RequestedScopes          []string  `json:"requested_scopes"`
	TermsVersion             string    `json:"terms_version"`
	IssuedAt                 time.Time `json:"issued_at"`
	ExpiresAt                time.Time `json:"expires_at"`
	Nonce                    string    `json:"nonce"`
	Recovery                 bool      `json:"recovery"`
	PreviousRevocationHandle string    `json:"previous_revocation_handle,omitempty"`
}

type RegistrationProof struct {
	Challenge       Challenge `json:"challenge"`
	HumanCredential string    `json:"human_credential"`
	HumanSignature  string    `json:"human_signature"`
	AgentPublicKey  string    `json:"agent_public_key"`
	AgentSignature  string    `json:"agent_signature"`
}

type Delegation struct {
	ID                    uuid.UUID `json:"id"`
	AccountType           string    `json:"account_type"`
	Origin                string    `json:"origin"`
	RPID                  string    `json:"rp_id"`
	HumanCredential       string    `json:"human_credential"`
	AgentKeyThumbprint    string    `json:"agent_key_thumbprint"`
	AllowedScopes         []string  `json:"allowed_scopes"`
	DeniedScopes          []string  `json:"denied_scopes"`
	TermsVersion          string    `json:"terms_version"`
	IssuedAt              time.Time `json:"issued_at"`
	NotBefore             time.Time `json:"not_before"`
	ExpiresAt             time.Time `json:"expires_at"`
	RevocationHandle      string    `json:"revocation_handle"`
	RecoveryRequiresHuman bool      `json:"recovery_requires_human"`
}

type RequestProof struct {
	RevocationHandle string    `json:"revocation_handle"`
	Origin           string    `json:"origin"`
	Scope            string    `json:"scope"`
	Method           string    `json:"method"`
	Path             string    `json:"path"`
	BodyHash         string    `json:"body_hash"`
	Nonce            string    `json:"nonce"`
	IssuedAt         time.Time `json:"issued_at"`
	Signature        string    `json:"signature"`
}

type Config struct {
	Discovery        Discovery
	HumanCredentials map[string][]byte
	Clock            types.Clock
	Random           io.Reader
}

type challengeRecord struct {
	Challenge Challenge
	Used      bool
}

type delegationRecord struct {
	Delegation Delegation
	AgentKey   []byte
	Revoked    bool
	Nonces     map[string]struct{}
}

type Registry struct {
	discovery Discovery
	humans    map[string][]byte
	clock     types.Clock
	random    io.Reader

	mu          sync.Mutex
	challenges  map[uuid.UUID]challengeRecord
	delegations map[string]delegationRecord
}

func Open(config Config) (*Registry, error) {
	if config.Clock == nil {
		return nil, errors.New("agent account: clock is required")
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	discovery, err := normalizeDiscovery(config.Discovery)
	if err != nil {
		return nil, err
	}
	humans := make(map[string][]byte, len(config.HumanCredentials))
	for id, publicKey := range config.HumanCredentials {
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("agent account: human credential ID is required")
		}
		if _, err := parsePublicKey(publicKey); err != nil {
			return nil, fmt.Errorf("agent account: human credential %q: %w", id, err)
		}
		humans[id] = append([]byte(nil), publicKey...)
	}
	if len(humans) == 0 {
		return nil, errors.New("agent account: at least one human credential is required")
	}
	return &Registry{
		discovery: discovery, humans: humans, clock: config.Clock, random: config.Random,
		challenges:  make(map[uuid.UUID]challengeRecord),
		delegations: make(map[string]delegationRecord),
	}, nil
}

func SignupChoices() []SignupChoice {
	return []SignupChoice{
		{ID: "sign-in", Label: "Sign in", AccountType: "existing", Description: "Continue with an existing account."},
		{ID: "human", Label: "Create personal account", AccountType: "human", Description: "Create an account for a person."},
		{ID: "agent", Label: "Continue as Agent", AccountType: AccountTypeAgent, Description: "Create an agent account under explicit human or organization delegation."},
	}
}

func (registry *Registry) Discovery() Discovery {
	return cloneDiscovery(registry.discovery)
}

func (registry *Registry) IssueChallenge(
	origin string,
	rpID string,
	scopes []string,
	termsVersion string,
	lifetime time.Duration,
) (Challenge, error) {
	return registry.issueChallenge(origin, rpID, scopes, termsVersion, lifetime, "")
}

func (registry *Registry) BeginRecovery(
	origin string,
	rpID string,
	previousHandle string,
	scopes []string,
	termsVersion string,
	lifetime time.Duration,
) (Challenge, error) {
	registry.mu.Lock()
	record, exists := registry.delegations[strings.TrimSpace(previousHandle)]
	registry.mu.Unlock()
	if !exists || record.Revoked || record.Delegation.AccountType != AccountTypeAgent {
		return Challenge{}, errors.New("agent account: recoverable delegation not found")
	}
	challenge, err := registry.issueChallenge(
		origin, rpID, scopes, termsVersion, lifetime, previousHandle,
	)
	if err != nil {
		return Challenge{}, err
	}
	challenge.Recovery = true
	registry.mu.Lock()
	stored := registry.challenges[challenge.ID]
	stored.Challenge = challenge
	registry.challenges[challenge.ID] = stored
	registry.mu.Unlock()
	return challenge, nil
}

func (registry *Registry) VerifyRegistration(
	proof RegistrationProof,
) (Delegation, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, exists := registry.challenges[proof.Challenge.ID]
	if !exists || record.Used || !sameChallenge(record.Challenge, proof.Challenge) {
		return Delegation{}, errors.New("agent account: challenge is unknown, changed, or already used")
	}
	now := registry.clock.Now().UTC()
	if now.Before(record.Challenge.IssuedAt) || !now.Before(record.Challenge.ExpiresAt) {
		return Delegation{}, errors.New("agent account: challenge expired")
	}
	if record.Challenge.AccountType != AccountTypeAgent {
		return Delegation{}, errors.New("agent account: human and agent account ceremonies are separate")
	}
	humanKey, exists := registry.humans[strings.TrimSpace(proof.HumanCredential)]
	if !exists {
		return Delegation{}, errors.New("agent account: trusted human delegation proof is required")
	}
	agentKey, err := decodePublicKey(proof.AgentPublicKey)
	if err != nil {
		return Delegation{}, err
	}
	if string(agentKey) == string(humanKey) {
		return Delegation{}, errors.New(
			"agent account: agent key must be separate from the human credential",
		)
	}
	digest, err := challengeDigest(record.Challenge)
	if err != nil {
		return Delegation{}, err
	}
	if !verifySignature(humanKey, digest, proof.HumanSignature) {
		return Delegation{}, errors.New("agent account: human delegation proof is invalid")
	}
	if !verifySignature(agentKey, digest, proof.AgentSignature) {
		return Delegation{}, errors.New("agent account: agent proof is invalid")
	}
	if record.Challenge.Recovery {
		previous, exists := registry.delegations[record.Challenge.PreviousRevocationHandle]
		if !exists || previous.Revoked ||
			previous.Delegation.HumanCredential != proof.HumanCredential {
			return Delegation{}, errors.New("agent account: recovery is not controlled by the original human")
		}
		previous.Revoked = true
		registry.delegations[record.Challenge.PreviousRevocationHandle] = previous
	}
	handle, err := registry.randomToken(32)
	if err != nil {
		return Delegation{}, err
	}
	thumbprint := sha256.Sum256(agentKey)
	denied := difference(registry.discovery.AvailableScopes, record.Challenge.RequestedScopes)
	delegation := Delegation{
		ID: uuid.New(), AccountType: AccountTypeAgent,
		Origin: record.Challenge.Origin, RPID: record.Challenge.RPID,
		HumanCredential:    proof.HumanCredential,
		AgentKeyThumbprint: base64.RawURLEncoding.EncodeToString(thumbprint[:]),
		AllowedScopes:      append([]string(nil), record.Challenge.RequestedScopes...),
		DeniedScopes:       denied, TermsVersion: record.Challenge.TermsVersion,
		IssuedAt: now, NotBefore: now, ExpiresAt: record.Challenge.ExpiresAt,
		RevocationHandle: handle, RecoveryRequiresHuman: true,
	}
	record.Used = true
	registry.challenges[proof.Challenge.ID] = record
	registry.delegations[handle] = delegationRecord{
		Delegation: delegation, AgentKey: agentKey, Nonces: make(map[string]struct{}),
	}
	return cloneDelegation(delegation), nil
}

func (registry *Registry) Revoke(handle string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, exists := registry.delegations[strings.TrimSpace(handle)]
	if !exists {
		return errors.New("agent account: delegation not found")
	}
	record.Revoked = true
	registry.delegations[handle] = record
	return nil
}

func (registry *Registry) Authorize(proof RequestProof) (Delegation, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, exists := registry.delegations[strings.TrimSpace(proof.RevocationHandle)]
	if !exists || record.Revoked {
		return Delegation{}, errors.New("agent account: delegation is revoked or unavailable")
	}
	now := registry.clock.Now().UTC()
	if now.Before(record.Delegation.NotBefore) || !now.Before(record.Delegation.ExpiresAt) ||
		proof.IssuedAt.Before(now.Add(-maximumRequestSkew)) ||
		proof.IssuedAt.After(now.Add(maximumRequestSkew)) {
		return Delegation{}, errors.New("agent account: delegation or request proof expired")
	}
	if proof.Origin != record.Delegation.Origin ||
		!contains(record.Delegation.AllowedScopes, proof.Scope) {
		return Delegation{}, errors.New("agent account: origin or scope exceeds delegation")
	}
	if strings.TrimSpace(proof.Nonce) == "" {
		return Delegation{}, errors.New("agent account: request nonce is required")
	}
	if _, replayed := record.Nonces[proof.Nonce]; replayed {
		return Delegation{}, errors.New("agent account: request proof replayed")
	}
	digest, err := requestDigest(proof)
	if err != nil || !verifySignature(record.AgentKey, digest, proof.Signature) {
		return Delegation{}, errors.New("agent account: request proof is invalid")
	}
	record.Nonces[proof.Nonce] = struct{}{}
	registry.delegations[proof.RevocationHandle] = record
	return cloneDelegation(record.Delegation), nil
}

func (registry *Registry) issueChallenge(
	origin string,
	rpID string,
	scopes []string,
	termsVersion string,
	lifetime time.Duration,
	previousHandle string,
) (Challenge, error) {
	origin, err := normalizeOrigin(origin)
	if err != nil || rpID != hostname(origin) {
		return Challenge{}, errors.New("agent account: exact HTTPS origin and RP ID are required")
	}
	if origin != registry.discovery.Origin || rpID != registry.discovery.RPID {
		return Challenge{}, errors.New("agent account: origin or RP ID does not match discovery")
	}
	scopes = uniqueSorted(scopes)
	if len(scopes) == 0 {
		return Challenge{}, errors.New("agent account: at least one scope is required")
	}
	for _, scope := range scopes {
		if !contains(registry.discovery.AvailableScopes, scope) {
			return Challenge{}, errors.New("agent account: requested scope is unavailable")
		}
	}
	if strings.TrimSpace(termsVersion) == "" ||
		lifetime <= 0 || lifetime > maximumDelegationLifetime {
		return Challenge{}, errors.New("agent account: terms and bounded lifetime are required")
	}
	nonce, err := registry.randomToken(32)
	if err != nil {
		return Challenge{}, err
	}
	now := registry.clock.Now().UTC()
	challenge := Challenge{
		Profile: ProfileVersion, ID: uuid.New(), Origin: origin, RPID: rpID,
		AccountType: AccountTypeAgent, RequestedScopes: scopes,
		TermsVersion: strings.TrimSpace(termsVersion), IssuedAt: now,
		ExpiresAt: now.Add(lifetime), Nonce: nonce,
		PreviousRevocationHandle: previousHandle,
	}
	registry.mu.Lock()
	registry.challenges[challenge.ID] = challengeRecord{Challenge: challenge}
	registry.mu.Unlock()
	return challenge, nil
}

func normalizeDiscovery(discovery Discovery) (Discovery, error) {
	origin, err := normalizeOrigin(discovery.Origin)
	if err != nil || discovery.Profile != ProfileVersion ||
		discovery.RPID != hostname(origin) {
		return Discovery{}, errors.New("agent account: valid versioned HTTPS discovery is required")
	}
	discovery.Origin = origin
	for _, endpoint := range []string{
		discovery.RegistrationEndpoint, discovery.ChallengeEndpoint,
		discovery.RevocationEndpoint, discovery.RecoveryEndpoint,
	} {
		normalized, endpointErr := normalizeOrigin(endpoint)
		if endpointErr != nil || normalized != origin {
			return Discovery{}, errors.New("agent account: discovery endpoints must be same-origin HTTPS URLs")
		}
	}
	if !contains(discovery.SupportedProof, "webauthn-delegation-v1") {
		return Discovery{}, errors.New("agent account: WebAuthn-compatible dual proof is required")
	}
	discovery.AvailableScopes = uniqueSorted(discovery.AvailableScopes)
	if len(discovery.AvailableScopes) == 0 {
		return Discovery{}, errors.New("agent account: available scopes are required")
	}
	discovery.SupportedProof = uniqueSorted(discovery.SupportedProof)
	discovery.HumanHandoff = uniqueSorted(discovery.HumanHandoff)
	return discovery, nil
}

func normalizeOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("agent account: HTTPS origin is required")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func hostname(origin string) string {
	parsed, _ := url.Parse(origin)
	return parsed.Hostname()
}

func challengeDigest(challenge Challenge) ([]byte, error) {
	payload, err := json.Marshal(challenge)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	return digest[:], nil
}

func requestDigest(proof RequestProof) ([]byte, error) {
	proof.Signature = ""
	payload, err := json.Marshal(proof)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	return digest[:], nil
}

func SignChallenge(privateKey *ecdsa.PrivateKey, challenge Challenge) (string, error) {
	digest, err := challengeDigest(challenge)
	if err != nil {
		return "", err
	}
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(signature), nil
}

func SignRequest(privateKey *ecdsa.PrivateKey, proof RequestProof) (string, error) {
	digest, err := requestDigest(proof)
	if err != nil {
		return "", err
	}
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(signature), nil
}

func EncodePublicKey(publicKey *ecdsa.PublicKey) string {
	ecdhKey, err := publicKey.ECDH()
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(ecdhKey.Bytes())
}

func decodePublicKey(encoded string) ([]byte, error) {
	publicKey, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, errors.New("agent account: public key encoding is invalid")
	}
	if _, err := parsePublicKey(publicKey); err != nil {
		return nil, err
	}
	return publicKey, nil
}

func parsePublicKey(encoded []byte) (*ecdsa.PublicKey, error) {
	if len(encoded) != 65 || encoded[0] != 4 {
		return nil, errors.New("P-256 public key is invalid")
	}
	if _, err := ecdh.P256().NewPublicKey(encoded); err != nil {
		return nil, errors.New("P-256 public key is invalid")
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(encoded[1:33]),
		Y:     new(big.Int).SetBytes(encoded[33:]),
	}, nil
}

func verifySignature(publicKey []byte, digest []byte, encoded string) bool {
	key, err := parsePublicKey(publicKey)
	if err != nil {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	return err == nil && ecdsa.VerifyASN1(key, digest, signature)
}

func (registry *Registry) randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(registry.random, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func sameChallenge(left, right Challenge) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	found := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			found[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(found))
	for value := range found {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func difference(all, selected []string) []string {
	result := make([]string, 0)
	for _, value := range all {
		if !contains(selected, value) {
			result = append(result, value)
		}
	}
	return result
}

func cloneDiscovery(value Discovery) Discovery {
	value.SupportedProof = append([]string(nil), value.SupportedProof...)
	value.AvailableScopes = append([]string(nil), value.AvailableScopes...)
	value.HumanHandoff = append([]string(nil), value.HumanHandoff...)
	return value
}

func cloneDelegation(value Delegation) Delegation {
	value.AllowedScopes = append([]string(nil), value.AllowedScopes...)
	value.DeniedScopes = append([]string(nil), value.DeniedScopes...)
	return value
}
