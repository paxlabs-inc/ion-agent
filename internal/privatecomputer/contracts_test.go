package privatecomputer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnvelopeValidationAndReplayAreScopeBound(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	envelope := testEnvelope(now, testScope(ModePersonal))
	if err := envelope.Validate(now); err != nil {
		t.Fatalf("valid envelope: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{"protocol", func(candidate *Envelope) { candidate.Version = "old" }},
		{"actor", func(candidate *Envelope) { candidate.Scope.ActorID = uuid.Nil }},
		{"work", func(candidate *Envelope) {
			candidate.Scope.TaskID = nil
			candidate.Scope.OutcomeID = nil
		}},
		{"authority", func(candidate *Envelope) { candidate.AuthorityRevision = 0 }},
		{"session revision", func(candidate *Envelope) { candidate.SessionRevision = 0 }},
		{"expired", func(candidate *Envelope) { candidate.ExpiresAt = now }},
		{"ttl", func(candidate *Envelope) {
			candidate.ExpiresAt = now.Add(MaximumRequestTTL + time.Second)
		}},
		{"resource", func(candidate *Envelope) { candidate.Resource = " " }},
		{"policy", func(candidate *Envelope) { candidate.PolicyDecisionID = uuid.Nil }},
		{"risk", func(candidate *Envelope) { candidate.RiskClass = "UNKNOWN" }},
		{"idempotency", func(candidate *Envelope) { candidate.IdempotencyKey = "" }},
		{"nonce", func(candidate *Envelope) { candidate.ReplayNonce = "short" }},
		{"payload", func(candidate *Envelope) { candidate.Payload = json.RawMessage("{") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := envelope
			test.mutate(&candidate)
			if !errors.Is(candidate.Validate(now), ErrInvalidContract) {
				t.Fatalf("invalid envelope accepted: %+v", candidate)
			}
		})
	}

	if disposition, err := ClassifyReplay(now, envelope, nil); err != nil ||
		disposition != ReplayNew {
		t.Fatalf("new replay classification = %q, %v", disposition, err)
	}
	fingerprint, err := envelope.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	record := &ReplayRecord{
		IdempotencyKey:    envelope.IdempotencyKey,
		Fingerprint:       fingerprint,
		AuthorityRevision: envelope.AuthorityRevision,
		SessionRevision:   envelope.SessionRevision,
		ReceiptID:         uuid.New(),
	}
	if disposition, err := ClassifyReplay(now, envelope, record); err != nil ||
		disposition != ReplayExact {
		t.Fatalf("exact replay classification = %q, %v", disposition, err)
	}
	conflict := envelope
	conflict.Resource = "different-resource"
	if _, err := ClassifyReplay(now, conflict, record); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("same key with different request = %v", err)
	}
	stale := *record
	stale.AuthorityRevision++
	if _, err := ClassifyReplay(now, envelope, &stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale authority replay = %v", err)
	}
	invalid := envelope
	invalid.ExpiresAt = now
	if _, err := ClassifyReplay(now, invalid, nil); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid replay envelope = %v", err)
	}
}

func TestHostNegotiationRequiresCompleteUnprivilegedOffer(t *testing.T) {
	offer := testHostOffer()
	compatibility, err := Negotiate([]string{ProtocolVersion}, offer)
	if err != nil {
		t.Fatal(err)
	}
	if !compatibility.Ready || compatibility.State != StateReady {
		t.Fatalf("compatibility = %+v", compatibility)
	}

	unavailable := testHostOffer()
	unavailable.Available = false
	unavailable.Reason = "desktop display is unavailable"
	compatibility, err = Negotiate([]string{ProtocolVersion}, unavailable)
	if err != nil || compatibility.Ready ||
		compatibility.State != StateUnavailable ||
		compatibility.Reason != unavailable.Reason {
		t.Fatalf("unavailable compatibility = %+v, %v", compatibility, err)
	}

	if compatibility, err = Negotiate([]string{"ion.private-computer.v2"}, offer); !errors.Is(err, ErrUnsupported) ||
		compatibility.State != StateUnavailable {
		t.Fatalf("incompatible protocol = %+v, %v", compatibility, err)
	}

	tests := []struct {
		name   string
		mutate func(*HostOffer)
	}{
		{"root", func(candidate *HostOffer) { candidate.NonRoot = false }},
		{"privileged", func(candidate *HostOffer) { candidate.Privileged = true }},
		{"public port", func(candidate *HostOffer) { candidate.PublicControlPort = true }},
		{"unpinned image", func(candidate *HostOffer) { candidate.ImageDigest = "latest" }},
		{"missing capability", func(candidate *HostOffer) {
			candidate.Capabilities = candidate.Capabilities[:len(candidate.Capabilities)-1]
		}},
		{"duplicate capability", func(candidate *HostOffer) {
			candidate.Capabilities[1].Kind = candidate.Capabilities[0].Kind
		}},
		{"silent degradation", func(candidate *HostOffer) {
			candidate.Capabilities[0].Degraded = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := testHostOffer()
			test.mutate(&candidate)
			if !errors.Is(candidate.Validate(), ErrInvalidContract) {
				t.Fatalf("unsafe host offer accepted: %+v", candidate)
			}
		})
	}
}

func TestExecutionHierarchyAndBoundedCorrelation(t *testing.T) {
	got := ExecutionHierarchy()
	want := []ExecutionLayer{
		ExecutionNativeTool,
		ExecutionSearXNG,
		ExecutionSemanticFetch,
		ExecutionNativeChromium,
		ExecutionPrivateDesktop,
	}
	if len(got) != len(want) {
		t.Fatalf("execution hierarchy = %v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("execution hierarchy[%d] = %q", index, got[index])
		}
	}
	got[0] = ExecutionPrivateDesktop
	if ExecutionHierarchy()[0] != ExecutionNativeTool {
		t.Fatal("execution hierarchy returned shared mutable state")
	}

	correlation := testCorrelation()
	if err := correlation.Validate(); err != nil {
		t.Fatal(err)
	}
	correlation.EvidenceIDs = nil
	if !errors.Is(correlation.Validate(), ErrInvalidContract) {
		t.Fatal("correlation without authoritative evidence was accepted")
	}

	if _, err := MarshalBounded(strings.Repeat("x", 1024), 16); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("oversized contract = %v", err)
	}
}

func TestReceiptReferencesExistingPolicyAndEvidenceTruth(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	envelope := testEnvelope(now, testScope(ModePersonal))
	fingerprint, err := envelope.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	correlation := testCorrelation()
	correlation.PolicyDecisionID = envelope.PolicyDecisionID
	envelope.Correlation = correlation
	fingerprint, err = envelope.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	receipt := Receipt{
		ProtocolVersion:    ProtocolVersion,
		RequestID:          envelope.RequestID,
		IdempotencyKey:     envelope.IdempotencyKey,
		RequestFingerprint: fingerprint,
		HostID:             uuid.New(),
		HostVersion:        "ion-computer/0.1.0",
		SessionID:          envelope.Scope.ComputerSessionID,
		SessionRevision:    envelope.SessionRevision + 1,
		State:              StateActive,
		ObservedAt:         now.Add(time.Second),
		Correlation:        envelope.Correlation,
	}
	if err := receipt.ValidateFor(envelope); err != nil {
		t.Fatal(err)
	}
	receipt.Correlation.PolicyDecisionID = uuid.New()
	if !errors.Is(receipt.ValidateFor(envelope), ErrInvalidContract) {
		t.Fatal("receipt with substituted policy decision was accepted")
	}
}

func testScope(mode PersistenceMode) Scope {
	taskID := uuid.New()
	return Scope{
		InstallationID:    uuid.New(),
		ActorID:           uuid.New(),
		IonSessionID:      uuid.New(),
		TaskID:            &taskID,
		AgentID:           "ion",
		ComputerSessionID: uuid.New(),
		Mode:              mode,
	}
}

func testBudget() ResourceBudget {
	return ResourceBudget{
		CPUMillis:         2_000,
		MemoryBytes:       4 << 30,
		Processes:         512,
		StorageBytes:      20 << 30,
		EgressBytes:       2 << 30,
		IdleSeconds:       900,
		SessionSeconds:    8 * 60 * 60,
		ScreenshotBytes:   8 << 20,
		ClipboardBytes:    64 << 10,
		CostMicrosPerHour: 500_000,
	}
}

func testEnvelope(now time.Time, scope Scope) Envelope {
	policyDecisionID := uuid.New()
	correlation := testCorrelation()
	correlation.PolicyDecisionID = policyDecisionID
	return Envelope{
		Version:           ProtocolVersion,
		RequestID:         uuid.New(),
		Operation:         OperationStart,
		Scope:             scope,
		Resource:          "desktop",
		PolicyDecisionID:  policyDecisionID,
		RiskClass:         "YELLOW",
		Correlation:       correlation,
		AuthorityRevision: 4,
		SessionRevision:   3,
		ExpiresAt:         now.Add(time.Minute),
		IdempotencyKey:    "start-once",
		ReplayNonce:       "0123456789abcdef",
		Payload:           json.RawMessage(`{"reason":"operator request"}`),
	}
}

func testHostOffer() HostOffer {
	capabilities := make([]Capability, 0, len(capabilityKinds))
	for _, kind := range capabilityKinds {
		capabilities = append(capabilities, Capability{
			Kind:      kind,
			Available: true,
		})
	}
	return HostOffer{
		ProtocolVersion: ProtocolVersion,
		HostID:          uuid.New(),
		HostVersion:     "ion-computer/0.1.0",
		ImageDigest:     "sha256:" + strings.Repeat("a", 64),
		Available:       true,
		NonRoot:         true,
		Capabilities:    capabilities,
		Limits:          testBudget(),
	}
}

func testCorrelation() Correlation {
	return Correlation{
		ComputerEventID:  uuid.New(),
		ToolEventID:      uuid.New(),
		PolicyDecisionID: uuid.New(),
		EvidenceIDs:      []uuid.UUID{uuid.New()},
	}
}
