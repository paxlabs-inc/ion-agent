package privatecomputer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ProtocolVersion       = "ion.private-computer.v1"
	MaximumRequestBytes   = 64 << 10
	MaximumContractBytes  = 1 << 20
	MaximumRequestTTL     = 5 * time.Minute
	MaximumReasonLength   = 1024
	MaximumCapabilities   = 32
	MaximumArtifactLinks  = 128
	MinimumReplayNonceLen = 16
)

var (
	ErrInvalidContract   = errors.New("private computer: invalid contract")
	ErrUnsupported       = errors.New("private computer: unsupported capability")
	ErrStaleRevision     = errors.New("private computer: stale revision")
	ErrReplayConflict    = errors.New("private computer: idempotency conflict")
	ErrInvalidTransition = errors.New("private computer: invalid lifecycle transition")
	ErrScopeMismatch     = errors.New("private computer: scope mismatch")
	ErrArtifactRequired  = errors.New("private computer: artifact export required")
	ErrBudgetExceeded    = errors.New("private computer: resource budget exceeded")
	ErrOutcomeUnknown    = errors.New("private computer: command outcome unknown")
	ErrSessionNotFound   = errors.New("private computer: session not found")
)

type PersistenceMode string

const (
	ModePersonal PersistenceMode = "personal"
	ModeClean    PersistenceMode = "clean"
)

func (mode PersistenceMode) valid() bool {
	return mode == ModePersonal || mode == ModeClean
}

type Operation string

const (
	OperationProvision Operation = "computer.provision"
	OperationStart     Operation = "computer.start"
	OperationStop      Operation = "computer.stop"
	OperationSuspend   Operation = "computer.suspend"
	OperationResume    Operation = "computer.resume"
	OperationRebuild   Operation = "computer.rebuild"
	OperationDestroy   Operation = "computer.destroy"
	OperationInspect   Operation = "computer.inspect"
	OperationReconcile Operation = "computer.reconcile"
)

func (operation Operation) valid() bool {
	switch operation {
	case OperationProvision, OperationStart, OperationStop, OperationSuspend,
		OperationResume, OperationRebuild, OperationDestroy, OperationInspect,
		OperationReconcile:
		return true
	default:
		return false
	}
}

type State string

const (
	StateStopped      State = "stopped"
	StateUnavailable  State = "unavailable"
	StateProvisioning State = "provisioning"
	StateReady        State = "ready"
	StateActive       State = "active"
	StateNeedsHelp    State = "needs_help"
	StateDisconnected State = "disconnected"
	StateRecovering   State = "recovering"
	StateSuspended    State = "suspended"
	StateFailed       State = "failed"
	StateDestroyed    State = "destroyed"
)

func (state State) valid() bool {
	switch state {
	case StateStopped, StateUnavailable, StateProvisioning, StateReady,
		StateActive, StateNeedsHelp, StateDisconnected, StateRecovering,
		StateSuspended, StateFailed, StateDestroyed:
		return true
	default:
		return false
	}
}

type CapabilityKind string

const (
	CapabilityBrowser             CapabilityKind = "browser"
	CapabilityTerminal            CapabilityKind = "terminal"
	CapabilityFilesystem          CapabilityKind = "filesystem"
	CapabilityScreenshot          CapabilityKind = "screenshot"
	CapabilityPointer             CapabilityKind = "pointer"
	CapabilityKeyboard            CapabilityKind = "keyboard"
	CapabilityClipboard           CapabilityKind = "clipboard"
	CapabilityDesktopStream       CapabilityKind = "desktop_stream"
	CapabilityProtectedSecret     CapabilityKind = "protected_secret_entry"
	CapabilityApprovedApplication CapabilityKind = "approved_application"
)

var capabilityKinds = []CapabilityKind{
	CapabilityBrowser,
	CapabilityTerminal,
	CapabilityFilesystem,
	CapabilityScreenshot,
	CapabilityPointer,
	CapabilityKeyboard,
	CapabilityClipboard,
	CapabilityDesktopStream,
	CapabilityProtectedSecret,
	CapabilityApprovedApplication,
}

type ExecutionLayer string

const (
	ExecutionNativeTool     ExecutionLayer = "native_structured_tool"
	ExecutionSearXNG        ExecutionLayer = "searxng_json_search"
	ExecutionSemanticFetch  ExecutionLayer = "semantic_fetch"
	ExecutionNativeChromium ExecutionLayer = "native_chromium_cdp"
	ExecutionPrivateDesktop ExecutionLayer = "private_graphical_computer"
)

func ExecutionHierarchy() []ExecutionLayer {
	return []ExecutionLayer{
		ExecutionNativeTool,
		ExecutionSearXNG,
		ExecutionSemanticFetch,
		ExecutionNativeChromium,
		ExecutionPrivateDesktop,
	}
}

type Scope struct {
	InstallationID    uuid.UUID       `json:"installation_id"`
	ActorID           uuid.UUID       `json:"actor_id"`
	IonSessionID      uuid.UUID       `json:"ion_session_id"`
	TaskID            *uuid.UUID      `json:"task_id,omitempty"`
	OutcomeID         *uuid.UUID      `json:"outcome_id,omitempty"`
	AgentID           string          `json:"agent_id"`
	ComputerSessionID uuid.UUID       `json:"computer_session_id"`
	Mode              PersistenceMode `json:"mode"`
}

func (scope Scope) Validate() error {
	if scope.InstallationID == uuid.Nil || scope.ActorID == uuid.Nil ||
		scope.IonSessionID == uuid.Nil || scope.ComputerSessionID == uuid.Nil ||
		(scope.TaskID == nil && scope.OutcomeID == nil) || !scope.Mode.valid() {
		return ErrInvalidContract
	}
	if strings.TrimSpace(scope.AgentID) == "" || len(scope.AgentID) > 128 {
		return ErrInvalidContract
	}
	if scope.TaskID != nil && *scope.TaskID == uuid.Nil {
		return ErrInvalidContract
	}
	if scope.OutcomeID != nil && *scope.OutcomeID == uuid.Nil {
		return ErrInvalidContract
	}
	return nil
}

func (scope Scope) SameAuthority(other Scope) bool {
	return scope.InstallationID == other.InstallationID &&
		scope.ActorID == other.ActorID &&
		scope.IonSessionID == other.IonSessionID &&
		scope.ComputerSessionID == other.ComputerSessionID &&
		scope.AgentID == other.AgentID &&
		scope.Mode == other.Mode &&
		equalOptionalUUID(scope.TaskID, other.TaskID) &&
		equalOptionalUUID(scope.OutcomeID, other.OutcomeID)
}

type ResourceBudget struct {
	CPUMillis         int64 `json:"cpu_millis"`
	MemoryBytes       int64 `json:"memory_bytes"`
	Processes         int64 `json:"processes"`
	StorageBytes      int64 `json:"storage_bytes"`
	EgressBytes       int64 `json:"egress_bytes"`
	IdleSeconds       int64 `json:"idle_seconds"`
	SessionSeconds    int64 `json:"session_seconds"`
	ScreenshotBytes   int64 `json:"screenshot_bytes"`
	ClipboardBytes    int64 `json:"clipboard_bytes"`
	CostMicrosPerHour int64 `json:"cost_micros_per_hour"`
}

func (budget ResourceBudget) Validate() error {
	values := []int64{
		budget.CPUMillis, budget.MemoryBytes, budget.Processes,
		budget.StorageBytes, budget.EgressBytes, budget.IdleSeconds,
		budget.SessionSeconds, budget.ScreenshotBytes, budget.ClipboardBytes,
		budget.CostMicrosPerHour,
	}
	for _, value := range values {
		if value <= 0 {
			return ErrInvalidContract
		}
	}
	if budget.CPUMillis > 128_000 || budget.MemoryBytes > 1<<50 ||
		budget.Processes > 1_000_000 || budget.StorageBytes > 1<<60 ||
		budget.ScreenshotBytes > 64<<20 || budget.ClipboardBytes > 1<<20 ||
		budget.IdleSeconds > int64((30*24*time.Hour)/time.Second) ||
		budget.SessionSeconds > int64((365*24*time.Hour)/time.Second) {
		return ErrInvalidContract
	}
	return nil
}

type Envelope struct {
	Version           string          `json:"version"`
	RequestID         uuid.UUID       `json:"request_id"`
	Operation         Operation       `json:"operation"`
	Scope             Scope           `json:"scope"`
	Resource          string          `json:"resource"`
	PolicyDecisionID  uuid.UUID       `json:"policy_decision_id"`
	RiskClass         string          `json:"risk_class"`
	Correlation       Correlation     `json:"correlation"`
	AuthorityRevision uint64          `json:"authority_revision"`
	SessionRevision   uint64          `json:"session_revision"`
	ExpiresAt         time.Time       `json:"expires_at"`
	IdempotencyKey    string          `json:"idempotency_key"`
	ReplayNonce       string          `json:"replay_nonce"`
	Payload           json.RawMessage `json:"payload,omitempty"`
}

func (envelope Envelope) Validate(now time.Time) error {
	if envelope.Version != ProtocolVersion || envelope.RequestID == uuid.Nil ||
		!envelope.Operation.valid() || envelope.Scope.Validate() != nil ||
		envelope.PolicyDecisionID == uuid.Nil || !validRiskClass(envelope.RiskClass) ||
		envelope.Correlation.Validate() != nil ||
		envelope.Correlation.PolicyDecisionID != envelope.PolicyDecisionID ||
		envelope.AuthorityRevision == 0 || envelope.SessionRevision == 0 ||
		envelope.ExpiresAt.IsZero() || !envelope.ExpiresAt.After(now) ||
		envelope.ExpiresAt.After(now.Add(MaximumRequestTTL)) {
		return ErrInvalidContract
	}
	resource := strings.TrimSpace(envelope.Resource)
	idempotencyKey := strings.TrimSpace(envelope.IdempotencyKey)
	replayNonce := strings.TrimSpace(envelope.ReplayNonce)
	if resource == "" || len(resource) > 256 ||
		idempotencyKey == "" || len(idempotencyKey) > 256 ||
		len(replayNonce) < MinimumReplayNonceLen || len(replayNonce) > 256 ||
		len(envelope.Payload) > MaximumRequestBytes ||
		(len(envelope.Payload) > 0 && !json.Valid(envelope.Payload)) {
		return ErrInvalidContract
	}
	return nil
}

func (envelope Envelope) Fingerprint() (string, error) {
	encoded, err := MarshalBounded(envelope, MaximumContractBytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type Capability struct {
	Kind      CapabilityKind `json:"kind"`
	Available bool           `json:"available"`
	Degraded  bool           `json:"degraded"`
	Reason    string         `json:"reason,omitempty"`
}

func (capability Capability) validate() error {
	validKind := false
	for _, candidate := range capabilityKinds {
		if capability.Kind == candidate {
			validKind = true
			break
		}
	}
	if !validKind || len(capability.Reason) > MaximumReasonLength {
		return ErrInvalidContract
	}
	if (!capability.Available || capability.Degraded) &&
		strings.TrimSpace(capability.Reason) == "" {
		return ErrInvalidContract
	}
	if capability.Available && !capability.Degraded &&
		strings.TrimSpace(capability.Reason) != "" {
		return ErrInvalidContract
	}
	return nil
}

type HostOffer struct {
	ProtocolVersion   string         `json:"protocol_version"`
	HostID            uuid.UUID      `json:"host_id"`
	HostVersion       string         `json:"host_version"`
	ImageDigest       string         `json:"image_digest"`
	Available         bool           `json:"available"`
	Reason            string         `json:"reason,omitempty"`
	NonRoot           bool           `json:"non_root"`
	Privileged        bool           `json:"privileged"`
	PublicControlPort bool           `json:"public_control_port"`
	Capabilities      []Capability   `json:"capabilities"`
	Limits            ResourceBudget `json:"limits"`
}

func (offer HostOffer) Validate() error {
	if offer.ProtocolVersion != ProtocolVersion || offer.HostID == uuid.Nil ||
		strings.TrimSpace(offer.HostVersion) == "" ||
		len(offer.HostVersion) > 128 || !validImageDigest(offer.ImageDigest) ||
		!offer.NonRoot || offer.Privileged || offer.PublicControlPort ||
		len(offer.Capabilities) != len(capabilityKinds) ||
		len(offer.Capabilities) > MaximumCapabilities ||
		offer.Limits.Validate() != nil || len(offer.Reason) > MaximumReasonLength {
		return ErrInvalidContract
	}
	if !offer.Available && strings.TrimSpace(offer.Reason) == "" {
		return ErrInvalidContract
	}
	if offer.Available && strings.TrimSpace(offer.Reason) != "" {
		return ErrInvalidContract
	}
	seen := make(map[CapabilityKind]struct{}, len(offer.Capabilities))
	for _, capability := range offer.Capabilities {
		if capability.validate() != nil {
			return ErrInvalidContract
		}
		if _, duplicate := seen[capability.Kind]; duplicate {
			return ErrInvalidContract
		}
		seen[capability.Kind] = struct{}{}
	}
	for _, kind := range capabilityKinds {
		if _, exists := seen[kind]; !exists {
			return ErrInvalidContract
		}
	}
	return nil
}

type Compatibility struct {
	ProtocolVersion string `json:"protocol_version"`
	HostVersion     string `json:"host_version"`
	Ready           bool   `json:"ready"`
	State           State  `json:"state"`
	Reason          string `json:"reason,omitempty"`
}

func Negotiate(supportedProtocolVersions []string, offer HostOffer) (Compatibility, error) {
	if err := offer.Validate(); err != nil {
		return Compatibility{}, err
	}
	supported := false
	for _, version := range supportedProtocolVersions {
		if version == offer.ProtocolVersion {
			supported = true
			break
		}
	}
	if !supported {
		return Compatibility{
			ProtocolVersion: offer.ProtocolVersion,
			HostVersion:     offer.HostVersion,
			State:           StateUnavailable,
			Reason:          "private computer protocol version is incompatible",
		}, ErrUnsupported
	}
	if !offer.Available {
		return Compatibility{
			ProtocolVersion: offer.ProtocolVersion,
			HostVersion:     offer.HostVersion,
			State:           StateUnavailable,
			Reason:          offer.Reason,
		}, nil
	}
	return Compatibility{
		ProtocolVersion: offer.ProtocolVersion,
		HostVersion:     offer.HostVersion,
		Ready:           true,
		State:           StateReady,
	}, nil
}

type Correlation struct {
	ComputerEventID  uuid.UUID   `json:"computer_event_id"`
	ToolEventID      uuid.UUID   `json:"tool_event_id"`
	PolicyDecisionID uuid.UUID   `json:"policy_decision_id"`
	LeaseID          *uuid.UUID  `json:"lease_id,omitempty"`
	ArtifactIDs      []uuid.UUID `json:"artifact_ids,omitempty"`
	EvidenceIDs      []uuid.UUID `json:"evidence_ids"`
}

func (correlation Correlation) Validate() error {
	if correlation.ComputerEventID == uuid.Nil ||
		correlation.ToolEventID == uuid.Nil ||
		correlation.PolicyDecisionID == uuid.Nil ||
		len(correlation.EvidenceIDs) == 0 ||
		len(correlation.ArtifactIDs) > MaximumArtifactLinks ||
		len(correlation.EvidenceIDs) > MaximumArtifactLinks {
		return ErrInvalidContract
	}
	if correlation.LeaseID != nil && *correlation.LeaseID == uuid.Nil {
		return ErrInvalidContract
	}
	for _, id := range append(
		append([]uuid.UUID(nil), correlation.ArtifactIDs...),
		correlation.EvidenceIDs...,
	) {
		if id == uuid.Nil {
			return ErrInvalidContract
		}
	}
	return nil
}

type Receipt struct {
	ProtocolVersion    string      `json:"protocol_version"`
	RequestID          uuid.UUID   `json:"request_id"`
	IdempotencyKey     string      `json:"idempotency_key"`
	RequestFingerprint string      `json:"request_fingerprint"`
	HostID             uuid.UUID   `json:"host_id"`
	HostVersion        string      `json:"host_version"`
	SessionID          uuid.UUID   `json:"session_id"`
	SessionRevision    uint64      `json:"session_revision"`
	State              State       `json:"state"`
	ObservedAt         time.Time   `json:"observed_at"`
	Message            string      `json:"message,omitempty"`
	Correlation        Correlation `json:"correlation"`
}

func (receipt Receipt) ValidateFor(envelope Envelope) error {
	fingerprint, err := envelope.Fingerprint()
	if err != nil {
		return err
	}
	if receipt.ProtocolVersion != ProtocolVersion ||
		receipt.RequestID != envelope.RequestID ||
		receipt.IdempotencyKey != envelope.IdempotencyKey ||
		receipt.RequestFingerprint != fingerprint ||
		receipt.HostID == uuid.Nil || strings.TrimSpace(receipt.HostVersion) == "" ||
		receipt.SessionID != envelope.Scope.ComputerSessionID ||
		receipt.SessionRevision < envelope.SessionRevision ||
		!receipt.State.valid() || receipt.ObservedAt.IsZero() ||
		len(receipt.Message) > MaximumReasonLength ||
		receipt.Correlation.Validate() != nil ||
		!correlationsEqual(receipt.Correlation, envelope.Correlation) {
		return ErrInvalidContract
	}
	return nil
}

type Host interface {
	Capabilities(context.Context) HostOffer
	Execute(context.Context, Session, Envelope) (Receipt, error)
	Observe(context.Context, Session) (Observation, error)
}

type ReplayDisposition string

const (
	ReplayNew   ReplayDisposition = "new"
	ReplayExact ReplayDisposition = "exact_replay"
)

type ReplayRecord struct {
	IdempotencyKey    string    `json:"idempotency_key"`
	Fingerprint       string    `json:"fingerprint"`
	AuthorityRevision uint64    `json:"authority_revision"`
	SessionRevision   uint64    `json:"session_revision"`
	ReceiptID         uuid.UUID `json:"receipt_id"`
}

func ClassifyReplay(
	now time.Time,
	envelope Envelope,
	existing *ReplayRecord,
) (ReplayDisposition, error) {
	if err := envelope.Validate(now); err != nil {
		return "", err
	}
	fingerprint, err := envelope.Fingerprint()
	if err != nil {
		return "", err
	}
	if existing == nil {
		return ReplayNew, nil
	}
	if existing.IdempotencyKey != envelope.IdempotencyKey {
		return ReplayNew, nil
	}
	if existing.ReceiptID == uuid.Nil || existing.Fingerprint == "" {
		return "", ErrInvalidContract
	}
	if existing.AuthorityRevision != envelope.AuthorityRevision ||
		existing.SessionRevision != envelope.SessionRevision {
		return "", ErrStaleRevision
	}
	if existing.Fingerprint != fingerprint {
		return "", ErrReplayConflict
	}
	return ReplayExact, nil
}

func MarshalBounded(value any, maximum int) ([]byte, error) {
	if maximum <= 0 || maximum > MaximumContractBytes {
		return nil, ErrInvalidContract
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidContract, err)
	}
	if len(encoded) > maximum {
		return nil, ErrInvalidContract
	}
	return encoded, nil
}

func validImageDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil
}

func validRiskClass(riskClass string) bool {
	return riskClass == "GREEN" || riskClass == "YELLOW" || riskClass == "RED"
}

func equalOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func correlationsEqual(left, right Correlation) bool {
	return left.ComputerEventID == right.ComputerEventID &&
		left.ToolEventID == right.ToolEventID &&
		left.PolicyDecisionID == right.PolicyDecisionID &&
		equalOptionalUUID(left.LeaseID, right.LeaseID) &&
		slices.Equal(left.ArtifactIDs, right.ArtifactIDs) &&
		slices.Equal(left.EvidenceIDs, right.EvidenceIDs)
}
