package privatecomputer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const hostStateVersion = 1

type DesktopRuntime interface {
	Start(context.Context, string) error
	Stop(context.Context) error
	Suspend(context.Context) error
	Resume(context.Context) error
	Running() bool
	Workspace() string
}

type ArtifactVerifier interface {
	VerifyExport(context.Context, Scope, string, ArtifactExportReceipt) error
}

type HostControllerConfig struct {
	StateRoot        string
	WorkspaceRoot    string
	Mode             PersistenceMode
	HostID           uuid.UUID
	HostVersion      string
	ImageDigest      string
	Limits           ResourceBudget
	Runtime          DesktopRuntime
	ArtifactVerifier ArtifactVerifier
	Clock            func() time.Time
	Random           io.Reader
}

type HostController struct {
	config HostControllerConfig
	mu     sync.Mutex
	state  durableHostState
}

type LifecyclePayload struct {
	Budget              *ResourceBudget         `json:"budget,omitempty"`
	ProducedArtifactIDs []uuid.UUID             `json:"produced_artifact_ids,omitempty"`
	ExportedArtifactIDs []uuid.UUID             `json:"exported_artifact_ids,omitempty"`
	ArtifactReceipts    []ArtifactExportReceipt `json:"artifact_receipts,omitempty"`
}

type Workspace struct {
	ID                  string          `json:"id"`
	Mode                PersistenceMode `json:"mode"`
	Path                string          `json:"path"`
	InstallationID      uuid.UUID       `json:"installation_id"`
	ActorID             uuid.UUID       `json:"actor_id"`
	ComputerSessionID   uuid.UUID       `json:"computer_session_id"`
	FreshScopeDigest    string          `json:"fresh_scope_digest,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	DestructionDeadline *time.Time      `json:"destruction_deadline,omitempty"`
	DestroyedAt         *time.Time      `json:"destroyed_at,omitempty"`
}

type ResourceUsage struct {
	StorageBytes    int64     `json:"storage_bytes"`
	EgressBytes     int64     `json:"egress_bytes"`
	ScreenshotBytes int64     `json:"screenshot_bytes"`
	ClipboardBytes  int64     `json:"clipboard_bytes"`
	ActiveSeconds   int64     `json:"active_seconds"`
	IdleSeconds     int64     `json:"idle_seconds"`
	CostMicros      int64     `json:"cost_micros"`
	ObservedAt      time.Time `json:"observed_at"`
}

type CleanupEvidence struct {
	ID                  uuid.UUID       `json:"id"`
	SessionID           uuid.UUID       `json:"session_id"`
	WorkspaceID         string          `json:"workspace_id"`
	Mode                PersistenceMode `json:"mode"`
	ExportedArtifactIDs []uuid.UUID     `json:"exported_artifact_ids,omitempty"`
	CompletedAt         time.Time       `json:"completed_at"`
	WorkspaceRemoved    bool            `json:"workspace_removed"`
	Partial             bool            `json:"partial"`
	Reason              string          `json:"reason,omitempty"`
}

type ComputerEvent struct {
	ID                uuid.UUID   `json:"id"`
	RequestID         uuid.UUID   `json:"request_id"`
	SessionID         uuid.UUID   `json:"session_id"`
	Operation         Operation   `json:"operation"`
	From              State       `json:"from"`
	To                State       `json:"to"`
	SessionRevision   uint64      `json:"session_revision"`
	AuthorityRevision uint64      `json:"authority_revision"`
	OccurredAt        time.Time   `json:"occurred_at"`
	Correlation       Correlation `json:"correlation"`
	CleanupEvidenceID *uuid.UUID  `json:"cleanup_evidence_id,omitempty"`
}

type ManagedSession struct {
	Session             Session       `json:"session"`
	Workspace           Workspace     `json:"workspace"`
	ProducedArtifactIDs []uuid.UUID   `json:"produced_artifact_ids,omitempty"`
	Usage               ResourceUsage `json:"usage"`
	LastActivityAt      time.Time     `json:"last_activity_at"`
	ActiveSince         *time.Time    `json:"active_since,omitempty"`
}

type CommandResult struct {
	Receipt         Receipt           `json:"receipt"`
	Session         ManagedSession    `json:"session"`
	Event           ComputerEvent     `json:"event"`
	CleanupEvidence *CleanupEvidence  `json:"cleanup_evidence,omitempty"`
	Replay          ReplayDisposition `json:"replay"`
}

type durableReplay struct {
	Record  ReplayRecord  `json:"record"`
	Receipt Receipt       `json:"receipt"`
	Event   ComputerEvent `json:"event"`
}

type pendingCommand struct {
	Envelope    Envelope  `json:"envelope"`
	Fingerprint string    `json:"fingerprint"`
	PreparedAt  time.Time `json:"prepared_at"`
}

type durableHostState struct {
	Version         int                        `json:"version"`
	Sessions        map[string]ManagedSession  `json:"sessions"`
	Replays         map[string]durableReplay   `json:"replays"`
	ReplayNonces    map[string]string          `json:"replay_nonces"`
	Pending         map[string]pendingCommand  `json:"pending"`
	CleanupEvidence map[string]CleanupEvidence `json:"cleanup_evidence"`
	Events          []ComputerEvent            `json:"events"`
	ActiveSessionID string                     `json:"active_session_id,omitempty"`
}

func SessionResource(id uuid.UUID) string {
	return "computer-session:" + id.String()
}

func NewHostController(config HostControllerConfig) (*HostController, error) {
	if !filepath.IsAbs(config.StateRoot) ||
		!filepath.IsAbs(config.WorkspaceRoot) ||
		!config.Mode.valid() ||
		config.HostID == uuid.Nil ||
		strings.TrimSpace(config.HostVersion) == "" ||
		!validImageDigest(config.ImageDigest) ||
		config.Limits.Validate() != nil {
		return nil, ErrInvalidContract
	}
	stateRoot, err := filepath.Abs(filepath.Clean(config.StateRoot))
	if err != nil {
		return nil, err
	}
	workspaceRoot, err := filepath.Abs(filepath.Clean(config.WorkspaceRoot))
	if err != nil {
		return nil, err
	}
	if stateRoot == string(filepath.Separator) ||
		workspaceRoot == string(filepath.Separator) {
		return nil, ErrInvalidContract
	}
	config.StateRoot = stateRoot
	config.WorkspaceRoot = workspaceRoot
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if err := secureDirectory(config.StateRoot); err != nil {
		return nil, err
	}
	if err := secureDirectory(config.WorkspaceRoot); err != nil {
		return nil, err
	}
	controller := &HostController{config: config}
	state, err := controller.load()
	if err != nil {
		return nil, err
	}
	controller.state = state
	return controller, nil
}

func (controller *HostController) Execute(
	ctx context.Context,
	envelope Envelope,
) (CommandResult, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	now := controller.config.Clock().UTC()
	if err := envelope.Validate(now); err != nil {
		return CommandResult{}, err
	}
	if envelope.Resource != SessionResource(envelope.Scope.ComputerSessionID) {
		return CommandResult{}, ErrScopeMismatch
	}
	fingerprint, err := envelope.Fingerprint()
	if err != nil {
		return CommandResult{}, err
	}
	if replay, exists := controller.state.Replays[envelope.IdempotencyKey]; exists {
		disposition, classifyErr := ClassifyReplay(now, envelope, &replay.Record)
		if classifyErr != nil {
			return CommandResult{}, classifyErr
		}
		if disposition == ReplayExact {
			session, exists := controller.state.Sessions[envelope.Scope.ComputerSessionID.String()]
			if !exists {
				return CommandResult{}, ErrOutcomeUnknown
			}
			result := CommandResult{
				Receipt: replay.Receipt,
				Session: session,
				Event:   replay.Event,
				Replay:  ReplayExact,
			}
			if replay.Event.CleanupEvidenceID != nil {
				evidence := controller.state.CleanupEvidence[replay.Event.CleanupEvidenceID.String()]
				result.CleanupEvidence = &evidence
			}
			return result, nil
		}
	}
	if pending, exists := controller.state.Pending[envelope.IdempotencyKey]; exists {
		if pending.Fingerprint == fingerprint {
			return CommandResult{}, ErrOutcomeUnknown
		}
		return CommandResult{}, ErrReplayConflict
	}
	if existing, exists := controller.state.ReplayNonces[envelope.ReplayNonce]; exists &&
		existing != fingerprint {
		return CommandResult{}, ErrReplayConflict
	}
	payload, err := decodeLifecyclePayload(envelope.Payload)
	if err != nil {
		return CommandResult{}, err
	}
	current, err := controller.validateCommand(ctx, now, envelope, payload)
	if err != nil {
		return CommandResult{}, err
	}
	controller.state.Pending[envelope.IdempotencyKey] = pendingCommand{
		Envelope:    envelope,
		Fingerprint: fingerprint,
		PreparedAt:  now,
	}
	controller.state.ReplayNonces[envelope.ReplayNonce] = fingerprint
	if err := controller.save(); err != nil {
		delete(controller.state.Pending, envelope.IdempotencyKey)
		delete(controller.state.ReplayNonces, envelope.ReplayNonce)
		return CommandResult{}, err
	}

	updated, cleanup, from, err := controller.apply(
		ctx,
		now,
		current,
		envelope,
		payload,
	)
	if err != nil {
		return CommandResult{}, err
	}
	if envelope.Operation == OperationReconcile {
		for key, pending := range controller.state.Pending {
			if pending.Envelope.Scope.ComputerSessionID ==
				envelope.Scope.ComputerSessionID {
				delete(controller.state.Pending, key)
			}
		}
	}
	updated.LastActivityAt = now
	usage, err := controller.measureUsage(now, updated)
	if err != nil {
		return CommandResult{}, err
	}
	updated.Usage = usage
	if usage.StorageBytes > updated.Session.Budget.StorageBytes {
		return CommandResult{}, ErrBudgetExceeded
	}
	event := ComputerEvent{
		ID:                uuid.New(),
		RequestID:         envelope.RequestID,
		SessionID:         envelope.Scope.ComputerSessionID,
		Operation:         envelope.Operation,
		From:              from,
		To:                updated.Session.State,
		SessionRevision:   updated.Session.Revision,
		AuthorityRevision: updated.Session.AuthorityRevision,
		OccurredAt:        now,
		Correlation:       envelope.Correlation,
	}
	if cleanup != nil {
		event.CleanupEvidenceID = &cleanup.ID
		controller.state.CleanupEvidence[cleanup.ID.String()] = *cleanup
	}
	receipt := Receipt{
		ProtocolVersion:    ProtocolVersion,
		RequestID:          envelope.RequestID,
		IdempotencyKey:     envelope.IdempotencyKey,
		RequestFingerprint: fingerprint,
		HostID:             controller.config.HostID,
		HostVersion:        controller.config.HostVersion,
		SessionID:          envelope.Scope.ComputerSessionID,
		SessionRevision:    updated.Session.Revision,
		State:              updated.Session.State,
		ObservedAt:         now,
		Correlation:        envelope.Correlation,
	}
	if err := receipt.ValidateFor(envelope); err != nil {
		return CommandResult{}, err
	}
	controller.state.Sessions[envelope.Scope.ComputerSessionID.String()] = updated
	controller.state.Events = appendBoundedEvent(controller.state.Events, event)
	controller.state.Replays[envelope.IdempotencyKey] = durableReplay{
		Record: ReplayRecord{
			IdempotencyKey:    envelope.IdempotencyKey,
			Fingerprint:       fingerprint,
			AuthorityRevision: envelope.AuthorityRevision,
			SessionRevision:   envelope.SessionRevision,
			ReceiptID:         event.ID,
		},
		Receipt: receipt,
		Event:   event,
	}
	delete(controller.state.Pending, envelope.IdempotencyKey)
	if err := controller.save(); err != nil {
		return CommandResult{}, ErrOutcomeUnknown
	}
	return CommandResult{
		Receipt:         receipt,
		Session:         updated,
		Event:           event,
		CleanupEvidence: cleanup,
		Replay:          ReplayNew,
	}, nil
}

func (controller *HostController) Inspect(
	sessionID uuid.UUID,
) (ManagedSession, bool) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	session, exists := controller.state.Sessions[sessionID.String()]
	return session, exists
}

func (controller *HostController) Pending() []Envelope {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	result := make([]Envelope, 0, len(controller.state.Pending))
	for _, pending := range controller.state.Pending {
		result = append(result, pending.Envelope)
	}
	return result
}

func (controller *HostController) EnforceBudgets(ctx context.Context) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.config.Clock().UTC()
	changed := false
	for key, managed := range controller.state.Sessions {
		if managed.Session.State != StateActive &&
			managed.Session.State != StateReady &&
			managed.Session.State != StateNeedsHelp {
			continue
		}
		usage, err := controller.measureUsage(now, managed)
		if err != nil {
			return err
		}
		managed.Usage = usage
		reason := budgetViolation(managed)
		if reason == "" {
			controller.state.Sessions[key] = managed
			continue
		}
		if controller.config.Runtime != nil &&
			controller.state.ActiveSessionID == key {
			if err := controller.config.Runtime.Stop(ctx); err != nil {
				return err
			}
			controller.state.ActiveSessionID = ""
		}
		managed.Session.State = StateUnavailable
		managed.Session.UnavailableReason = reason
		managed.Session.Revision++
		managed.Session.UpdatedAt = now
		managed.ActiveSince = nil
		controller.state.Sessions[key] = managed
		changed = true
	}
	if changed {
		return controller.save()
	}
	return nil
}

func (controller *HostController) validateCommand(
	ctx context.Context,
	now time.Time,
	envelope Envelope,
	payload LifecyclePayload,
) (ManagedSession, error) {
	if envelope.Scope.Mode != controller.config.Mode {
		return ManagedSession{}, ErrScopeMismatch
	}
	key := envelope.Scope.ComputerSessionID.String()
	current, exists := controller.state.Sessions[key]
	if envelope.Operation == OperationProvision {
		if exists || envelope.SessionRevision != 1 || payload.Budget == nil {
			return ManagedSession{}, ErrInvalidContract
		}
		if err := validateBudgetWithin(*payload.Budget, controller.config.Limits); err != nil {
			return ManagedSession{}, err
		}
		return ManagedSession{}, nil
	}
	if !exists {
		return ManagedSession{}, ErrSessionNotFound
	}
	if !current.Session.Scope.SameAuthority(envelope.Scope) {
		return ManagedSession{}, ErrScopeMismatch
	}
	if current.Session.AuthorityRevision != envelope.AuthorityRevision ||
		current.Session.Revision != envelope.SessionRevision {
		return ManagedSession{}, ErrStaleRevision
	}
	if current.Session.State == StateDestroyed {
		return ManagedSession{}, ErrInvalidTransition
	}
	if payload.Budget != nil {
		return ManagedSession{}, ErrInvalidContract
	}
	var target State
	switch envelope.Operation {
	case OperationStart:
		target = StateActive
		if controller.config.Runtime == nil {
			return ManagedSession{}, ErrUnsupported
		}
	case OperationStop:
		target = StateStopped
	case OperationSuspend:
		target = StateSuspended
		if controller.config.Runtime == nil ||
			controller.state.ActiveSessionID != key {
			return ManagedSession{}, ErrInvalidTransition
		}
	case OperationResume:
		target = StateRecovering
		if controller.config.Runtime == nil ||
			controller.state.ActiveSessionID != key {
			return ManagedSession{}, ErrInvalidTransition
		}
	case OperationRebuild:
		target = StateProvisioning
	case OperationDestroy:
		target = StateDestroyed
	case OperationInspect:
		target = current.Session.State
	case OperationReconcile:
		target = current.Session.State
	default:
		return ManagedSession{}, ErrUnsupported
	}
	if !operationAllows(envelope.Operation, current.Session.State, target) {
		return ManagedSession{}, ErrInvalidTransition
	}
	if envelope.Operation == OperationDestroy &&
		current.Session.Scope.Mode == ModeClean {
		produced := mergeUUIDs(
			current.ProducedArtifactIDs,
			payload.ProducedArtifactIDs,
		)
		if !containsAll(payload.ExportedArtifactIDs, produced) {
			return ManagedSession{}, ErrArtifactRequired
		}
		for _, artifactID := range produced {
			if controller.config.ArtifactVerifier == nil {
				return ManagedSession{}, ErrArtifactRequired
			}
			receipt, exists := artifactReceiptByID(
				payload.ArtifactReceipts,
				artifactID,
			)
			if !exists ||
				!containsUUID(envelope.Correlation.ArtifactIDs, artifactID) {
				return ManagedSession{}, ErrArtifactRequired
			}
			if err := controller.config.ArtifactVerifier.VerifyExport(
				ctx,
				current.Session.Scope,
				current.Workspace.Path,
				receipt,
			); err != nil {
				return ManagedSession{}, ErrArtifactRequired
			}
		}
	}
	if err := controller.checkBudget(now, current); err != nil &&
		envelope.Operation != OperationStop &&
		envelope.Operation != OperationDestroy &&
		envelope.Operation != OperationInspect &&
		envelope.Operation != OperationReconcile {
		return ManagedSession{}, err
	}
	return current, nil
}

func (controller *HostController) apply(
	ctx context.Context,
	now time.Time,
	current ManagedSession,
	envelope Envelope,
	payload LifecyclePayload,
) (ManagedSession, *CleanupEvidence, State, error) {
	if envelope.Operation == OperationProvision {
		workspace, err := controller.createWorkspace(now, envelope.Scope)
		if err != nil {
			return ManagedSession{}, nil, "", err
		}
		session := Session{
			ID:                envelope.Scope.ComputerSessionID,
			Scope:             envelope.Scope,
			State:             StateReady,
			Revision:          envelope.SessionRevision + 1,
			AuthorityRevision: envelope.AuthorityRevision,
			HostID:            controller.config.HostID,
			HostVersion:       controller.config.HostVersion,
			ImageDigest:       controller.config.ImageDigest,
			Budget:            *payload.Budget,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := session.Validate(); err != nil {
			_ = controller.removeWorkspace(workspace)
			return ManagedSession{}, nil, "", err
		}
		return ManagedSession{
			Session:        session,
			Workspace:      workspace,
			LastActivityAt: now,
		}, nil, StateStopped, nil
	}

	from := current.Session.State
	transition := Transition{
		Envelope:            envelope,
		From:                from,
		ProducedArtifactIDs: payload.ProducedArtifactIDs,
		ExportedArtifactIDs: payload.ExportedArtifactIDs,
	}
	switch envelope.Operation {
	case OperationStart:
		if controller.state.ActiveSessionID != "" &&
			controller.state.ActiveSessionID != current.Session.ID.String() {
			return ManagedSession{}, nil, from, ErrScopeMismatch
		}
		if controller.config.Runtime == nil {
			return ManagedSession{}, nil, from, ErrUnsupported
		}
		if err := controller.config.Runtime.Start(ctx, current.Workspace.Path); err != nil {
			return ManagedSession{}, nil, from, err
		}
		transition.To = StateActive
		controller.state.ActiveSessionID = current.Session.ID.String()
		current.ActiveSince = timePointer(now)
	case OperationStop:
		if controller.config.Runtime != nil &&
			controller.state.ActiveSessionID == current.Session.ID.String() {
			if err := controller.config.Runtime.Stop(ctx); err != nil {
				return ManagedSession{}, nil, from, err
			}
			controller.state.ActiveSessionID = ""
		}
		transition.To = StateStopped
		current.ActiveSince = nil
	case OperationSuspend:
		if controller.config.Runtime == nil ||
			controller.state.ActiveSessionID != current.Session.ID.String() {
			return ManagedSession{}, nil, from, ErrInvalidTransition
		}
		if err := controller.config.Runtime.Suspend(ctx); err != nil {
			return ManagedSession{}, nil, from, err
		}
		transition.To = StateSuspended
	case OperationResume:
		if controller.config.Runtime == nil ||
			controller.state.ActiveSessionID != current.Session.ID.String() {
			return ManagedSession{}, nil, from, ErrInvalidTransition
		}
		if err := controller.config.Runtime.Resume(ctx); err != nil {
			return ManagedSession{}, nil, from, err
		}
		transition.To = StateRecovering
	case OperationRebuild:
		if controller.config.Runtime != nil &&
			controller.state.ActiveSessionID == current.Session.ID.String() {
			if err := controller.config.Runtime.Stop(ctx); err != nil {
				return ManagedSession{}, nil, from, err
			}
			controller.state.ActiveSessionID = ""
		}
		transition.To = StateProvisioning
	case OperationDestroy:
		if controller.config.Runtime != nil &&
			controller.state.ActiveSessionID == current.Session.ID.String() {
			if err := controller.config.Runtime.Stop(ctx); err != nil {
				return ManagedSession{}, nil, from, err
			}
			controller.state.ActiveSessionID = ""
		}
		return controller.destroy(
			ctx,
			now,
			current,
			transition,
			payload.ArtifactReceipts,
		)
	case OperationInspect:
		transition.To = from
	case OperationReconcile:
		return controller.reconcile(ctx, now, current, transition)
	default:
		return ManagedSession{}, nil, from, ErrUnsupported
	}
	updated, err := ApplyTransition(now, current.Session, transition)
	if err != nil {
		return ManagedSession{}, nil, from, err
	}
	current.Session = updated
	if envelope.Operation == OperationResume {
		current.Session.State = StateActive
		current.Session.Revision++
		current.Session.UpdatedAt = now
	}
	if envelope.Operation == OperationRebuild {
		if current.Session.Scope.Mode == ModeClean {
			if err := controller.removeWorkspace(current.Workspace); err != nil {
				return ManagedSession{}, nil, from, err
			}
			workspace, err := controller.createWorkspace(now, current.Session.Scope)
			if err != nil {
				return ManagedSession{}, nil, from, err
			}
			current.Workspace = workspace
			current.ProducedArtifactIDs = nil
		}
		current.Session.State = StateReady
		current.Session.Revision++
		current.Session.UpdatedAt = now
	}
	current.ProducedArtifactIDs = mergeUUIDs(
		current.ProducedArtifactIDs,
		payload.ProducedArtifactIDs,
	)
	return current, nil, from, nil
}

func (controller *HostController) destroy(
	ctx context.Context,
	now time.Time,
	current ManagedSession,
	transition Transition,
	artifactReceipts []ArtifactExportReceipt,
) (ManagedSession, *CleanupEvidence, State, error) {
	from := current.Session.State
	produced := mergeUUIDs(
		current.ProducedArtifactIDs,
		transition.ProducedArtifactIDs,
	)
	transition.ProducedArtifactIDs = produced
	if current.Session.Scope.Mode == ModeClean {
		if !containsAll(transition.ExportedArtifactIDs, produced) {
			return ManagedSession{}, nil, from, ErrArtifactRequired
		}
		for _, artifactID := range produced {
			if controller.config.ArtifactVerifier == nil {
				return ManagedSession{}, nil, from, ErrArtifactRequired
			}
			receipt, exists := artifactReceiptByID(
				artifactReceipts,
				artifactID,
			)
			if !exists {
				return ManagedSession{}, nil, from, ErrArtifactRequired
			}
			if err := controller.config.ArtifactVerifier.VerifyExport(
				ctx,
				current.Session.Scope,
				current.Workspace.Path,
				receipt,
			); err != nil {
				return ManagedSession{}, nil, from, ErrArtifactRequired
			}
		}
	}
	evidence := &CleanupEvidence{
		ID:                  uuid.New(),
		SessionID:           current.Session.ID,
		WorkspaceID:         current.Workspace.ID,
		Mode:                current.Session.Scope.Mode,
		ExportedArtifactIDs: append([]uuid.UUID(nil), transition.ExportedArtifactIDs...),
		CompletedAt:         now,
	}
	if current.Session.Scope.Mode == ModeClean {
		if err := controller.removeWorkspace(current.Workspace); err != nil {
			evidence.Partial = true
			evidence.Reason = boundedReason(err.Error())
			current.Session.State = StateRecovering
			current.Session.Revision++
			current.Session.UpdatedAt = now
			current.Session.ExportedArtifactIDs = append(
				[]uuid.UUID(nil),
				transition.ExportedArtifactIDs...,
			)
			current.Session.CleanupEvidenceID = &evidence.ID
			current.Session.DegradedReasons = appendBoundedReason(
				current.Session.DegradedReasons,
				"Clean workspace cleanup is partial and requires reconciliation",
			)
			current.ProducedArtifactIDs = produced
			current.ActiveSince = nil
			return current, evidence, from, nil
		}
		evidence.WorkspaceRemoved = true
		current.Workspace.DestroyedAt = timePointer(now)
	}
	transition.To = StateDestroyed
	transition.CleanupEvidenceID = &evidence.ID
	updated, err := ApplyTransition(now, current.Session, transition)
	if err != nil {
		return ManagedSession{}, evidence, from, err
	}
	current.Session = updated
	current.ProducedArtifactIDs = produced
	current.ActiveSince = nil
	return current, evidence, from, nil
}

func (controller *HostController) reconcile(
	ctx context.Context,
	now time.Time,
	current ManagedSession,
	transition Transition,
) (ManagedSession, *CleanupEvidence, State, error) {
	from := current.Session.State
	if current.Session.Scope.Mode == ModeClean &&
		current.Session.CleanupEvidenceID != nil {
		evidence, exists := controller.state.CleanupEvidence[current.Session.CleanupEvidenceID.String()]
		if exists && evidence.Partial {
			if err := controller.removeWorkspace(current.Workspace); err != nil {
				evidence.CompletedAt = now
				evidence.Reason = boundedReason(err.Error())
				transition.To = from
				updated, applyErr := ApplyTransition(
					now,
					current.Session,
					transition,
				)
				if applyErr != nil {
					return ManagedSession{}, nil, from, applyErr
				}
				current.Session = updated
				return current, &evidence, from, nil
			}
			evidence.CompletedAt = now
			evidence.WorkspaceRemoved = true
			evidence.Partial = false
			evidence.Reason = ""
			transition.To = StateDestroyed
			transition.ProducedArtifactIDs = append(
				[]uuid.UUID(nil),
				current.ProducedArtifactIDs...,
			)
			transition.ExportedArtifactIDs = append(
				[]uuid.UUID(nil),
				current.Session.ExportedArtifactIDs...,
			)
			transition.CleanupEvidenceID = &evidence.ID
			updated, applyErr := ApplyTransition(
				now,
				current.Session,
				transition,
			)
			if applyErr != nil {
				return ManagedSession{}, nil, from, applyErr
			}
			current.Session = updated
			current.Workspace.DestroyedAt = timePointer(now)
			return current, &evidence, from, nil
		}
	}
	info, err := os.Stat(current.Workspace.Path)
	if err != nil || !info.IsDir() {
		transition.To = StateUnavailable
		updated, applyErr := ApplyTransition(now, current.Session, transition)
		if applyErr != nil {
			return ManagedSession{}, nil, from, applyErr
		}
		updated.UnavailableReason = "private computer workspace is unavailable"
		current.Session = updated
		return current, nil, from, nil
	}
	transition.To = from
	recovered := false
	if from == StateActive &&
		(controller.config.Runtime == nil ||
			!controller.config.Runtime.Running() ||
			filepath.Clean(controller.config.Runtime.Workspace()) !=
				filepath.Clean(current.Workspace.Path)) {
		transition.To = StateRecovering
		if controller.config.Runtime != nil {
			if err := controller.config.Runtime.Start(
				ctx,
				current.Workspace.Path,
			); err != nil {
				return ManagedSession{}, nil, from, err
			}
			controller.state.ActiveSessionID = current.Session.ID.String()
			recovered = true
		}
	}
	updated, err := ApplyTransition(now, current.Session, transition)
	if err != nil {
		return ManagedSession{}, nil, from, err
	}
	current.Session = updated
	if recovered {
		current.Session.State = StateActive
		current.Session.Revision++
		current.Session.UpdatedAt = now
	}
	return current, nil, from, nil
}

func (controller *HostController) createWorkspace(
	now time.Time,
	scope Scope,
) (Workspace, error) {
	var directory string
	workspace := Workspace{
		Mode:              scope.Mode,
		InstallationID:    scope.InstallationID,
		ActorID:           scope.ActorID,
		ComputerSessionID: scope.ComputerSessionID,
		CreatedAt:         now,
	}
	if scope.Mode == ModePersonal {
		workspace.ID = "personal:" + scope.InstallationID.String() + ":" + scope.ActorID.String()
		directory = filepath.Join(
			controller.config.WorkspaceRoot,
			"personal",
			scope.InstallationID.String(),
			scope.ActorID.String(),
			"home",
		)
	} else {
		randomScope := make([]byte, 32)
		if _, err := io.ReadFull(controller.config.Random, randomScope); err != nil {
			return Workspace{}, err
		}
		scopeName := hex.EncodeToString(randomScope)
		digest := sha256.Sum256(randomScope)
		workspace.ID = "clean:" + scope.ComputerSessionID.String() + ":" + scopeName
		workspace.FreshScopeDigest = hex.EncodeToString(digest[:])
		directory = filepath.Join(
			controller.config.WorkspaceRoot,
			"clean",
			scope.ComputerSessionID.String(),
			scopeName,
			"home",
		)
		deadline := now.Add(time.Duration(controller.config.Limits.SessionSeconds) * time.Second)
		workspace.DestructionDeadline = &deadline
	}
	directory, err := confinedPath(controller.config.WorkspaceRoot, directory)
	if err != nil {
		return Workspace{}, err
	}
	if err := secureDirectory(directory); err != nil {
		return Workspace{}, err
	}
	workspace.Path = directory
	return workspace, nil
}

func (controller *HostController) removeWorkspace(workspace Workspace) error {
	if workspace.Mode != ModeClean {
		return nil
	}
	sessionRoot := filepath.Dir(filepath.Dir(workspace.Path))
	sessionRoot, err := confinedPath(controller.config.WorkspaceRoot, sessionRoot)
	if err != nil {
		return err
	}
	cleanRoot := filepath.Join(controller.config.WorkspaceRoot, "clean")
	relative, err := filepath.Rel(cleanRoot, sessionRoot)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return ErrScopeMismatch
	}
	return os.RemoveAll(sessionRoot)
}

func (controller *HostController) checkBudget(
	now time.Time,
	managed ManagedSession,
) error {
	usage, err := controller.measureUsage(now, managed)
	if err != nil {
		return err
	}
	managed.Usage = usage
	if budgetViolation(managed) != "" {
		return ErrBudgetExceeded
	}
	return nil
}

func (controller *HostController) measureUsage(
	now time.Time,
	managed ManagedSession,
) (ResourceUsage, error) {
	storage, err := directorySize(managed.Workspace.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ResourceUsage{}, err
	}
	usage := managed.Usage
	usage.StorageBytes = storage
	usage.ObservedAt = now
	usage.IdleSeconds = max(0, int64(now.Sub(managed.LastActivityAt)/time.Second))
	if managed.ActiveSince != nil {
		usage.ActiveSeconds = max(0, int64(now.Sub(*managed.ActiveSince)/time.Second))
	}
	usage.CostMicros = usage.ActiveSeconds *
		managed.Session.Budget.CostMicrosPerHour / 3600
	return usage, nil
}

func (controller *HostController) load() (durableHostState, error) {
	path := filepath.Join(controller.config.StateRoot, "host-state.json")
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyDurableHostState(), nil
	}
	if err != nil {
		return durableHostState{}, err
	}
	if len(payload) > 16<<20 {
		return durableHostState{}, ErrInvalidContract
	}
	var state durableHostState
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return durableHostState{}, ErrInvalidContract
	}
	if state.Version != hostStateVersion ||
		state.Sessions == nil ||
		state.Replays == nil ||
		state.ReplayNonces == nil ||
		state.Pending == nil ||
		state.CleanupEvidence == nil {
		return durableHostState{}, ErrInvalidContract
	}
	return state, nil
}

func (controller *HostController) save() error {
	payload, err := json.Marshal(controller.state)
	if err != nil {
		return err
	}
	if len(payload) > 16<<20 {
		return ErrInvalidContract
	}
	path := filepath.Join(controller.config.StateRoot, "host-state.json")
	file, err := os.CreateTemp(controller.config.StateRoot, ".host-state-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	directory, err := os.Open(controller.config.StateRoot)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func emptyDurableHostState() durableHostState {
	return durableHostState{
		Version:         hostStateVersion,
		Sessions:        make(map[string]ManagedSession),
		Replays:         make(map[string]durableReplay),
		ReplayNonces:    make(map[string]string),
		Pending:         make(map[string]pendingCommand),
		CleanupEvidence: make(map[string]CleanupEvidence),
	}
}

func decodeLifecyclePayload(payload json.RawMessage) (LifecyclePayload, error) {
	if len(payload) == 0 {
		return LifecyclePayload{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var result LifecyclePayload
	if err := decoder.Decode(&result); err != nil {
		return LifecyclePayload{}, ErrInvalidContract
	}
	if !validUUIDs(result.ProducedArtifactIDs) ||
		!validUUIDs(result.ExportedArtifactIDs) ||
		len(result.ProducedArtifactIDs) > MaximumArtifactLinks ||
		len(result.ExportedArtifactIDs) > MaximumArtifactLinks ||
		len(result.ArtifactReceipts) > MaximumArtifactLinks {
		return LifecyclePayload{}, ErrInvalidContract
	}
	seenReceipts := make(map[uuid.UUID]struct{}, len(result.ArtifactReceipts))
	for _, receipt := range result.ArtifactReceipts {
		if receipt.ArtifactID == uuid.Nil {
			return LifecyclePayload{}, ErrInvalidContract
		}
		if _, exists := seenReceipts[receipt.ArtifactID]; exists {
			return LifecyclePayload{}, ErrInvalidContract
		}
		seenReceipts[receipt.ArtifactID] = struct{}{}
	}
	return result, nil
}

func validateBudgetWithin(budget, limits ResourceBudget) error {
	if budget.Validate() != nil || limits.Validate() != nil {
		return ErrInvalidContract
	}
	requested := []int64{
		budget.CPUMillis,
		budget.MemoryBytes,
		budget.Processes,
		budget.StorageBytes,
		budget.EgressBytes,
		budget.IdleSeconds,
		budget.SessionSeconds,
		budget.ScreenshotBytes,
		budget.ClipboardBytes,
		budget.CostMicrosPerHour,
	}
	available := []int64{
		limits.CPUMillis,
		limits.MemoryBytes,
		limits.Processes,
		limits.StorageBytes,
		limits.EgressBytes,
		limits.IdleSeconds,
		limits.SessionSeconds,
		limits.ScreenshotBytes,
		limits.ClipboardBytes,
		limits.CostMicrosPerHour,
	}
	for index := range requested {
		if requested[index] > available[index] {
			return ErrBudgetExceeded
		}
	}
	return nil
}

func budgetViolation(managed ManagedSession) string {
	if managed.Usage.StorageBytes > managed.Session.Budget.StorageBytes {
		return "private computer storage budget exceeded"
	}
	if managed.Usage.EgressBytes > managed.Session.Budget.EgressBytes {
		return "private computer egress budget exceeded"
	}
	if managed.Usage.ScreenshotBytes > managed.Session.Budget.ScreenshotBytes {
		return "private computer screenshot budget exceeded"
	}
	if managed.Usage.ClipboardBytes > managed.Session.Budget.ClipboardBytes {
		return "private computer clipboard budget exceeded"
	}
	if managed.Usage.IdleSeconds > managed.Session.Budget.IdleSeconds {
		return "private computer idle budget exceeded"
	}
	if managed.Usage.ActiveSeconds > managed.Session.Budget.SessionSeconds {
		return "private computer session duration budget exceeded"
	}
	maxCost := managed.Session.Budget.CostMicrosPerHour *
		managed.Session.Budget.SessionSeconds / 3600
	if managed.Usage.CostMicros > maxCost {
		return "private computer cost budget exceeded"
	}
	return ""
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			total += info.Size()
		}
		if total < 0 {
			return ErrBudgetExceeded
		}
		return nil
	})
	return total, err
}

func secureDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return ErrInvalidContract
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func confinedPath(root, candidate string) (string, error) {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." ||
		relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrScopeMismatch
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrScopeMismatch
		}
	}
	return candidate, nil
}

func mergeUUIDs(left, right []uuid.UUID) []uuid.UUID {
	result := append([]uuid.UUID(nil), left...)
	seen := make(map[uuid.UUID]struct{}, len(result))
	for _, id := range result {
		seen[id] = struct{}{}
	}
	for _, id := range right {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func artifactReceiptByID(
	receipts []ArtifactExportReceipt,
	id uuid.UUID,
) (ArtifactExportReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.ArtifactID == id {
			return receipt, true
		}
	}
	return ArtifactExportReceipt{}, false
}

func containsUUID(ids []uuid.UUID, target uuid.UUID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func appendBoundedEvent(events []ComputerEvent, event ComputerEvent) []ComputerEvent {
	const maximumEvents = 4096
	if len(events) >= maximumEvents {
		copy(events, events[len(events)-maximumEvents+1:])
		events = events[:maximumEvents-1]
	}
	return append(events, event)
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func boundedReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > MaximumReasonLength {
		return reason[:MaximumReasonLength]
	}
	return reason
}
