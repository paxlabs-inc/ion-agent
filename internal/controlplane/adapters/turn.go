package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/action"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/provider"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
)

const (
	maxRecordedTurns        = 256
	defaultTurnStallTimeout = 150 * time.Second
)

// TurnRunner is satisfied by the production agent loop.
type TurnRunner interface {
	Turn(context.Context, string) (agent.Response, error)
}

// TurnResumer is implemented by production runners that can continue a
// durable agent-loop cursor.
type TurnResumer interface {
	Resume(context.Context, string, json.RawMessage) (agent.Response, error)
}

const (
	TurnSurfaceGeneral = "general"
	TurnSurfaceStudio  = "studio"
)

// TurnBinding carries the authoritative workspace surface selected by the
// client. Empty Surface is reserved for legacy turns created before structured
// workspace binding was introduced.
type TurnBinding struct {
	Surface   string
	ProjectID uuid.UUID
}

func (binding TurnBinding) validate() error {
	switch binding.Surface {
	case "":
		if binding.ProjectID != uuid.Nil {
			return fmt.Errorf("controlplane adapters: legacy turn cannot declare a project")
		}
	case TurnSurfaceGeneral:
		if binding.ProjectID != uuid.Nil {
			return fmt.Errorf("controlplane adapters: general turn cannot declare a project")
		}
	case TurnSurfaceStudio:
		if binding.ProjectID == uuid.Nil {
			return fmt.Errorf("controlplane adapters: Studio turn requires a project")
		}
	default:
		return fmt.Errorf("controlplane adapters: unsupported turn surface")
	}
	return nil
}

// TurnRunnerFactory returns a session-scoped production agent loop with an
// explicit workspace binding.
type TurnRunnerFactory interface {
	Runner(uuid.UUID, TurnBinding) (TurnRunner, error)
}

// TurnRunnerFactoryFunc adapts a factory function.
type TurnRunnerFactoryFunc func(uuid.UUID, TurnBinding) (TurnRunner, error)

// Runner invokes the adapted factory.
func (factory TurnRunnerFactoryFunc) Runner(
	sessionID uuid.UUID,
	binding TurnBinding,
) (TurnRunner, error) {
	return factory(sessionID, binding)
}

// TurnLifecycleObserver connects authenticated production turn lifecycle to
// durable liveness state without moving domain logic into the coordinator.
type TurnLifecycleObserver interface {
	Submitted(context.Context, uuid.UUID, uuid.UUID, string) error
	Completed(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		string,
		agent.Response,
	) error
}

// TurnIdentityResolver supplies the explicit actor name used to keep
// user-visible reasoning personal without exposing other relationship state.
type TurnIdentityResolver interface {
	PreferredName(context.Context, uuid.UUID) (string, bool)
}

// TurnRecoveryObserver is an optional production failure/recovery hook. It is
// separate so existing lifecycle observers do not need synthetic no-op repair
// methods.
type TurnRecoveryObserver interface {
	Failed(
		context.Context, uuid.UUID, uuid.UUID, string, string,
	) error
	Recovered(
		context.Context, uuid.UUID, uuid.UUID, string, string, agent.Response,
	) error
}

// TurnSessionStore is the encrypted transcript contract consumed by turns.
type TurnSessionStore interface {
	GetSession(context.Context, uuid.UUID) (session.Session, error)
	AppendMessage(
		context.Context,
		uuid.UUID,
		session.Role,
		session.MemoryType,
		[]byte,
		int,
	) (session.Message, error)
}

type turnMessageStore interface {
	AppendTurnMessage(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		session.Role,
		session.MemoryType,
		[]byte,
		int,
	) (session.Message, error)
}

type durableTurnStore interface {
	CreateTurnState(context.Context, session.TurnState) error
	SaveTurnCheckpoint(context.Context, uuid.UUID, json.RawMessage) error
	SetTurnRecovery(
		context.Context, uuid.UUID, session.TurnStatus, json.RawMessage,
	) error
	SetTurnStatus(context.Context, uuid.UUID, session.TurnStatus) error
	LoadTurnState(context.Context, uuid.UUID) (session.TurnState, error)
	RecoverableTurnStates(context.Context) ([]session.TurnState, error)
}

type durableTurnFailureStore interface {
	SetTurnFailure(
		context.Context, uuid.UUID, session.TurnStatus, string,
	) error
}

type scheduledTurnStore interface {
	CreateScheduledTurn(
		context.Context, session.TurnState, uuid.UUID, int,
	) (session.TurnState, bool, error)
}

type activeTurn struct {
	actorID   uuid.UUID
	sessionID uuid.UUID
	binding   TurnBinding
	cancel    context.CancelFunc
}

type turnRecord struct {
	actorID   uuid.UUID
	sessionID uuid.UUID
	content   string
	binding   TurnBinding
	active    bool
}

type turnGenerationObserver struct {
	emitter     controlplane.EventEmitter
	correlation controlplane.Correlation
	mu          sync.Mutex
	committed   strings.Builder
	attempt     strings.Builder
	reasoning   []string
	attemptNote string
}

func (observer *turnGenerationObserver) ContentDelta(
	ctx context.Context,
	content string,
) error {
	if content == "" {
		return nil
	}
	observer.mu.Lock()
	prefix := ""
	if observer.attempt.Len() == 0 && observer.committed.Len() > 0 {
		prefix = "\n\n"
	}
	observer.attempt.WriteString(prefix)
	observer.attempt.WriteString(content)
	observer.mu.Unlock()
	return observer.emit(
		ctx, controlplane.EventTurnDelta, prefix+content, false, false, "",
	)
}

func (observer *turnGenerationObserver) ReasoningDelta(
	context.Context,
	string,
) error {
	return nil
}

func (observer *turnGenerationObserver) ReasoningProgress(
	ctx context.Context,
) error {
	observer.mu.Lock()
	if observer.attemptNote != "" {
		observer.mu.Unlock()
		return nil
	}
	prefix := ""
	if len(observer.reasoning) > 0 {
		prefix = "\n"
	}
	observer.attemptNote = "Reviewing the request and available context."
	if len(observer.reasoning) > 0 {
		observer.attemptNote = "Reasoning continued with updated turn context."
	}
	content := prefix + observer.attemptNote
	observer.mu.Unlock()
	return observer.emit(
		ctx,
		controlplane.EventReasoningSummary,
		content,
		false,
		false,
		"safe_summary",
	)
}

func (observer *turnGenerationObserver) Reset(ctx context.Context) error {
	observer.mu.Lock()
	observer.attempt.Reset()
	observer.attemptNote = ""
	content := observer.committed.String()
	reasoning := strings.Join(observer.reasoning, "\n")
	observer.mu.Unlock()
	if err := observer.emit(
		ctx, controlplane.EventTurnDelta, content, false, true, "",
	); err != nil {
		return err
	}
	return observer.emit(
		ctx,
		controlplane.EventReasoningSummary,
		reasoning,
		false,
		true,
		"safe_summary",
	)
}

func (observer *turnGenerationObserver) CommitAttempt(context.Context) error {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.committed.WriteString(observer.attempt.String())
	observer.attempt.Reset()
	if observer.attemptNote != "" {
		observer.reasoning = append(observer.reasoning, observer.attemptNote)
		observer.attemptNote = ""
	}
	return nil
}

func (observer *turnGenerationObserver) Flush(ctx context.Context) error {
	return observer.CommitAttempt(ctx)
}

func (observer *turnGenerationObserver) Content() string {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return strings.TrimSpace(observer.committed.String() + observer.attempt.String())
}

func (observer *turnGenerationObserver) ReasoningSummary() string {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	notes := append([]string(nil), observer.reasoning...)
	if observer.attemptNote != "" {
		notes = append(notes, observer.attemptNote)
	}
	return strings.TrimSpace(strings.Join(notes, "\n"))
}

func (observer *turnGenerationObserver) emit(
	ctx context.Context,
	eventType controlplane.EventType,
	content string,
	reset bool,
	replace bool,
	source string,
) error {
	agent.TouchActivity(ctx)
	values := map[string]any{
		"content": content, "reset": reset, "replace": replace,
	}
	if source != "" {
		values["source"] = source
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = observer.emitter.Emit(
		ctx, eventType, observer.correlation, payload,
	)
	return err
}

// TurnCoordinator owns asynchronous turn goroutines and their cancellation.
type TurnCoordinator struct {
	ctx       context.Context
	cancel    context.CancelFunc
	store     TurnSessionStore
	factory   TurnRunnerFactory
	emitter   controlplane.EventEmitter
	observers []TurnLifecycleObserver
	identity  TurnIdentityResolver
	steering  SteeringResolver

	mu           sync.Mutex
	active       map[uuid.UUID]activeTurn
	turns        map[uuid.UUID]turnRecord
	order        []uuid.UUID
	wg           sync.WaitGroup
	stallTimeout time.Duration
}

// NewTurnCoordinator constructs the production turn application service.
func NewTurnCoordinator(
	parent context.Context,
	store TurnSessionStore,
	factory TurnRunnerFactory,
	emitter controlplane.EventEmitter,
	observers ...TurnLifecycleObserver,
) (*TurnCoordinator, error) {
	if parent == nil || store == nil || factory == nil || emitter == nil {
		return nil, fmt.Errorf("controlplane adapters: turn dependencies are required")
	}
	ctx, cancel := context.WithCancel(parent)
	coordinator := &TurnCoordinator{
		ctx: ctx, cancel: cancel, store: store, factory: factory, emitter: emitter,
		observers:    append([]TurnLifecycleObserver(nil), observers...),
		active:       make(map[uuid.UUID]activeTurn),
		turns:        make(map[uuid.UUID]turnRecord),
		stallTimeout: defaultTurnStallTimeout,
	}
	for _, observer := range observers {
		if resolver, ok := observer.(TurnIdentityResolver); ok {
			coordinator.identity = resolver
			break
		}
	}
	return coordinator, nil
}

// Close cancels and joins every owned background turn.
func (coordinator *TurnCoordinator) Close() {
	coordinator.cancel()
	coordinator.wg.Wait()
}

type submitTurnPayload struct {
	Content   string    `json:"content"`
	Surface   string    `json:"surface,omitempty"`
	ProjectID uuid.UUID `json:"project_id,omitempty"`
}

type submitTurnResult struct {
	TurnID uuid.UUID `json:"turn_id"`
	State  string    `json:"state"`
}

type cancelTurnPayload struct {
	TurnID uuid.UUID `json:"turn_id"`
}

type steerTurnPayload struct {
	TurnID  uuid.UUID   `json:"turn_id"`
	Content string      `json:"content"`
	Target  SteerTarget `json:"target"`
}

type retryTurnPayload struct {
	TurnID uuid.UUID `json:"turn_id"`
}

// RegisterHandlers exposes asynchronous submit, steer, retry, and deterministic
// cancellation.
func (coordinator *TurnCoordinator) RegisterHandlers(
	dispatcher *controlplane.Dispatcher,
) error {
	if dispatcher == nil {
		return fmt.Errorf("controlplane adapters: dispatcher is required")
	}
	if err := dispatcher.Register(
		controlplane.OperationTurnSubmit,
		"Persist input and start one asynchronous production agent turn.",
		controlplane.HandlerFunc(coordinator.handleSubmit),
	); err != nil {
		return err
	}
	if err := dispatcher.Register(
		controlplane.OperationTurnSteer,
		"Cancel one active turn and persist a replacement operator direction.",
		controlplane.HandlerFunc(coordinator.handleSteer),
	); err != nil {
		return err
	}
	if err := dispatcher.Register(
		controlplane.OperationTurnRetry,
		"Retry one terminal turn from its server-recorded operator input.",
		controlplane.HandlerFunc(coordinator.handleRetry),
	); err != nil {
		return err
	}
	return dispatcher.Register(
		controlplane.OperationTurnCancel,
		"Cancel one active turn in the authenticated session scope.",
		controlplane.HandlerFunc(coordinator.handleCancel),
	)
}

func (coordinator *TurnCoordinator) SetSteeringResolver(
	resolver SteeringResolver,
) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.steering = resolver
}

// ResumePending restarts every nonterminal durable turn after the daemon has
// reconstructed its production composition root.
func (coordinator *TurnCoordinator) ResumePending(ctx context.Context) error {
	durable, ok := coordinator.store.(durableTurnStore)
	if !ok {
		return nil
	}
	states, err := durable.RecoverableTurnStates(ctx)
	if err != nil {
		return err
	}
	for _, state := range states {
		found, err := coordinator.store.GetSession(ctx, state.SessionID)
		if err != nil {
			return fmt.Errorf(
				"controlplane adapters: restore turn session %s: %w",
				state.TurnID, err,
			)
		}
		turnCtx, cancel := context.WithCancel(coordinator.ctx)
		turnCtx = controlplane.WithApprovalScope(turnCtx, controlplane.ApprovalScope{
			ActorID:   state.ActorID,
			SessionID: &state.SessionID,
			TurnID:    &state.TurnID,
		})
		if state.Origin == "agent_schedule" {
			turnCtx = policy.WithPrincipal(turnCtx, policy.Principal{
				Sender: policy.SenderScheduler, Profile: "scheduled",
			})
		}
		coordinator.mu.Lock()
		if _, active := coordinator.active[state.TurnID]; active {
			coordinator.mu.Unlock()
			cancel()
			continue
		}
		coordinator.active[state.TurnID] = activeTurn{
			actorID: state.ActorID, sessionID: state.SessionID,
			binding: TurnBinding{Surface: state.Surface, ProjectID: state.ProjectID},
			cancel:  cancel,
		}
		coordinator.turns[state.TurnID] = turnRecord{
			actorID: state.ActorID, sessionID: state.SessionID,
			content: state.Content,
			binding: TurnBinding{Surface: state.Surface, ProjectID: state.ProjectID},
			active:  true,
		}
		coordinator.order = append(coordinator.order, state.TurnID)
		coordinator.pruneTurnsLocked()
		coordinator.mu.Unlock()
		payload, marshalErr := json.Marshal(map[string]any{
			"state": "recovering", "action": "resume_after_restart",
			"had_checkpoint": len(state.Checkpoint) > 0,
		})
		if marshalErr != nil {
			cancel()
			return marshalErr
		}
		if err := durable.SetTurnRecovery(
			ctx, state.TurnID, session.TurnRecovering, payload,
		); err != nil {
			cancel()
			return err
		}
		correlation := controlplane.Correlation{
			ActorID: state.ActorID, SessionID: &state.SessionID,
			TurnID: &state.TurnID,
		}
		if _, err := coordinator.emitter.Emit(
			ctx, controlplane.EventTurnRecovery, correlation, payload,
		); err != nil {
			cancel()
			return err
		}
		coordinator.wg.Add(1)
		go coordinator.run(
			turnCtx, state.TurnID, state.ActorID, state.SessionID,
			state.Content, TurnBinding{Surface: state.Surface, ProjectID: state.ProjectID},
			found.ContextTokens, state.Checkpoint,
		)
	}
	return nil
}

func (coordinator *TurnCoordinator) handleSubmit(
	ctx context.Context,
	request controlplane.Request,
	_ controlplane.EventEmitter,
) (json.RawMessage, error) {
	sessionID, err := scopedSessionID(request)
	if err != nil {
		return nil, err
	}
	var payload submitTurnPayload
	if err := decode(request.Payload, &payload); err != nil {
		return nil, err
	}
	payload.Content = strings.TrimSpace(payload.Content)
	if payload.Content == "" {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "turn content is required",
		}
	}
	binding := TurnBinding{Surface: strings.TrimSpace(payload.Surface), ProjectID: payload.ProjectID}
	if err := binding.validate(); err != nil {
		return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: err.Error()}
	}
	if request.Scope.Channel == "scheduler" {
		return coordinator.startScheduledTurn(
			ctx, request, sessionID, payload.Content, binding,
		)
	}
	return coordinator.startTurn(ctx, request, sessionID, payload.Content, binding)
}

func (coordinator *TurnCoordinator) startScheduledTurn(
	ctx context.Context,
	request controlplane.Request,
	sessionID uuid.UUID,
	content string,
	binding TurnBinding,
) (json.RawMessage, error) {
	principal := policy.PrincipalFromContext(ctx)
	if binding.Surface != TurnSurfaceGeneral || binding.ProjectID != uuid.Nil ||
		request.Scope.ActorID == uuid.Nil ||
		request.Scope.Profile != "scheduled" ||
		principal.Sender != policy.SenderScheduler ||
		principal.Profile != "scheduled" ||
		!strings.HasPrefix(request.IdempotencyKey, "schedule:") {
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "scheduled turns require an authenticated general wake scope",
		}
	}
	durable, ok := coordinator.store.(scheduledTurnStore)
	if !ok {
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorUnavailable,
			Message: "durable scheduled turns are unavailable",
		}
	}
	found, err := coordinator.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf(
			"controlplane adapters: load scheduled session: %w", err,
		)
	}
	identity := request.Scope.ActorID.String() + "\x00" + request.IdempotencyKey
	turnID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("turn\x00"+identity))
	messageID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("message\x00"+identity))
	state, created, err := durable.CreateScheduledTurn(
		ctx,
		session.TurnState{
			TurnID: turnID, ActorID: request.Scope.ActorID,
			SessionID: sessionID, Content: content,
			Surface: binding.Surface, Origin: "agent_schedule",
			ProjectID: binding.ProjectID,
			Status:    session.TurnRunning, UpdatedAt: time.Now().UTC(),
		},
		messageID,
		found.ContextTokens+estimateTokens(content),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"controlplane adapters: persist scheduled turn: %w", err,
		)
	}
	if !created {
		if state.Status == session.TurnFailed &&
			strings.HasPrefix(state.FailureCode, "scheduled_") {
			return nil, fmt.Errorf(
				"controlplane adapters: scheduled turn initialization previously failed",
			)
		}
		return json.Marshal(submitTurnResult{
			TurnID: state.TurnID, State: string(state.Status),
		})
	}
	for _, observer := range coordinator.observers {
		if observer == nil {
			continue
		}
		if err := observer.Submitted(
			ctx, request.Scope.ActorID, sessionID, content,
		); err != nil {
			if failures, ok := coordinator.store.(durableTurnFailureStore); ok {
				_ = failures.SetTurnFailure(
					context.WithoutCancel(ctx), turnID,
					session.TurnFailed, "scheduled_observer_failed",
				)
			}
			return nil, fmt.Errorf(
				"controlplane adapters: prepare scheduled living state: %w", err,
			)
		}
	}
	correlation := controlplane.Correlation{
		ActorID: request.Scope.ActorID, SessionID: &sessionID, TurnID: &turnID,
	}
	startedPayload, err := json.Marshal(map[string]any{
		"state": "running", "content_length": len(content),
		"origin": "agent_schedule",
	})
	if err != nil {
		return nil, err
	}
	if _, err := coordinator.emitter.Emit(
		ctx, controlplane.EventTurnStarted, correlation, startedPayload,
	); err != nil {
		if failures, ok := coordinator.store.(durableTurnFailureStore); ok {
			_ = failures.SetTurnFailure(
				context.WithoutCancel(ctx), turnID,
				session.TurnFailed, "scheduled_start_event_failed",
			)
		}
		return nil, err
	}
	turnCtx, cancel := context.WithCancel(coordinator.ctx)
	turnCtx = controlplane.WithApprovalScope(turnCtx, controlplane.ApprovalScope{
		ActorID: request.Scope.ActorID, SessionID: &sessionID, TurnID: &turnID,
	})
	turnCtx = policy.WithPrincipal(turnCtx, policy.Principal{
		Sender: policy.SenderScheduler, Profile: "scheduled",
	})
	coordinator.mu.Lock()
	coordinator.active[turnID] = activeTurn{
		actorID: request.Scope.ActorID, sessionID: sessionID,
		binding: binding, cancel: cancel,
	}
	coordinator.turns[turnID] = turnRecord{
		actorID: request.Scope.ActorID, sessionID: sessionID,
		content: content, binding: binding, active: true,
	}
	coordinator.order = append(coordinator.order, turnID)
	coordinator.pruneTurnsLocked()
	coordinator.mu.Unlock()
	coordinator.wg.Add(1)
	go coordinator.run(
		turnCtx, turnID, request.Scope.ActorID, sessionID, content, binding,
		found.ContextTokens+estimateTokens(content), nil,
	)
	return json.Marshal(submitTurnResult{TurnID: turnID, State: "running"})
}

func (coordinator *TurnCoordinator) startTurn(
	ctx context.Context,
	request controlplane.Request,
	sessionID uuid.UUID,
	content string,
	binding TurnBinding,
) (json.RawMessage, error) {
	if err := binding.validate(); err != nil {
		return nil, err
	}
	found, err := coordinator.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("controlplane adapters: load turn session: %w", err)
	}
	for _, observer := range coordinator.observers {
		if observer == nil {
			continue
		}
		if err := observer.Submitted(
			ctx, request.Scope.ActorID, sessionID, content,
		); err != nil {
			return nil, fmt.Errorf(
				"controlplane adapters: prepare living turn state: %w", err,
			)
		}
	}
	contextTokens := found.ContextTokens + estimateTokens(content)
	if _, err := coordinator.store.AppendMessage(
		ctx, sessionID, session.RoleUser, session.MemoryTranscript,
		[]byte(content), contextTokens,
	); err != nil {
		return nil, fmt.Errorf("controlplane adapters: persist user turn: %w", err)
	}
	turnID := uuid.New()
	if durable, ok := coordinator.store.(durableTurnStore); ok {
		if err := durable.CreateTurnState(ctx, session.TurnState{
			TurnID: turnID, ActorID: request.Scope.ActorID,
			SessionID: sessionID, Content: content,
			Surface: binding.Surface, ProjectID: binding.ProjectID,
			Status: session.TurnRunning, UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return nil, fmt.Errorf("controlplane adapters: persist turn state: %w", err)
		}
	}
	correlation := controlplane.Correlation{
		ActorID: request.Scope.ActorID, SessionID: &sessionID, TurnID: &turnID,
	}
	startedPayload, err := json.Marshal(map[string]any{
		"state": "running", "content_length": len(content),
	})
	if err != nil {
		return nil, err
	}
	if _, err := coordinator.emitter.Emit(
		ctx, controlplane.EventTurnStarted, correlation, startedPayload,
	); err != nil {
		return nil, err
	}
	turnCtx, cancel := context.WithCancel(coordinator.ctx)
	turnCtx = controlplane.WithApprovalScope(turnCtx, controlplane.ApprovalScope{
		ActorID: request.Scope.ActorID, SessionID: &sessionID, TurnID: &turnID,
	})
	coordinator.mu.Lock()
	coordinator.active[turnID] = activeTurn{
		actorID: request.Scope.ActorID, sessionID: sessionID,
		binding: binding, cancel: cancel,
	}
	coordinator.turns[turnID] = turnRecord{
		actorID: request.Scope.ActorID, sessionID: sessionID,
		content: content, binding: binding, active: true,
	}
	coordinator.order = append(coordinator.order, turnID)
	coordinator.pruneTurnsLocked()
	coordinator.mu.Unlock()
	coordinator.wg.Add(1)
	go coordinator.run(
		turnCtx, turnID, request.Scope.ActorID, sessionID, content, binding,
		contextTokens, nil,
	)
	return json.Marshal(submitTurnResult{TurnID: turnID, State: "running"})
}

func (coordinator *TurnCoordinator) run(
	ctx context.Context,
	turnID uuid.UUID,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	content string,
	binding TurnBinding,
	contextTokens int,
	checkpoint json.RawMessage,
) {
	defer coordinator.wg.Done()
	defer func() {
		coordinator.mu.Lock()
		active := coordinator.active[turnID]
		delete(coordinator.active, turnID)
		record := coordinator.turns[turnID]
		record.active = false
		coordinator.turns[turnID] = record
		coordinator.pruneTurnsLocked()
		coordinator.mu.Unlock()
		active.cancel()
	}()
	correlation := controlplane.Correlation{
		ActorID: actorID, SessionID: &sessionID, TurnID: &turnID,
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	var lastActivity atomic.Int64
	var stalled atomic.Bool
	touch := func() { lastActivity.Store(time.Now().UnixNano()) }
	touch()
	runCtx = agent.WithActivityObserver(runCtx, touch)
	watchdogDone := make(chan struct{})
	defer close(watchdogDone)
	go func() {
		interval := coordinator.stallTimeout / 10
		if interval < 5*time.Millisecond {
			interval = 5 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-runCtx.Done():
				return
			case <-ticker.C:
				last := time.Unix(0, lastActivity.Load())
				if time.Since(last) < coordinator.stallTimeout {
					continue
				}
				stalled.Store(true)
				payload, _ := json.Marshal(map[string]any{
					"state": "recovering", "action": "stall_watchdog",
					"phase": "no_runtime_activity", "stalled_for_seconds": int(coordinator.stallTimeout.Seconds()),
				})
				_, _ = coordinator.emitter.Emit(
					context.WithoutCancel(runCtx), controlplane.EventTurnRecovery,
					correlation, payload,
				)
				cancelRun()
				return
			}
		}
	}()
	ctx = runCtx
	generationEvents := &turnGenerationObserver{
		emitter: coordinator.emitter, correlation: correlation,
	}
	defer func() {
		_ = generationEvents.Flush(context.WithoutCancel(ctx))
	}()
	ctx = agent.WithGenerationObserver(ctx, generationEvents)
	runner, err := coordinator.factory.Runner(sessionID, binding)
	if err != nil {
		if statusErr := coordinator.setTurnStatus(
			context.WithoutCancel(ctx), turnID, session.TurnFailed,
		); statusErr != nil {
			coordinator.fail(
				context.WithoutCancel(ctx), correlation, "turn_state_persistence",
			)
			return
		}
		coordinator.fail(ctx, correlation, "runner_unavailable")
		return
	}
	var response agent.Response
	if len(checkpoint) > 0 {
		response, err = resumeRunner(ctx, runner, content, checkpoint)
	} else {
		response, err = runner.Turn(ctx, content)
	}
	initialErr := err
	if stalled.Load() && err != nil {
		err = &action.ErrIncomplete{
			Phase: "no_runtime_activity", StuckSince: time.Now().UTC(),
			Recovery: "start a fresh continuation from the durable checkpoint",
			Attempt:  response.ProviderCalls,
		}
		ctx = context.WithoutCancel(ctx)
		initialErr = err
	}
	if err != nil {
		var incomplete *action.ErrIncomplete
		if errors.As(err, &incomplete) {
			recovered, recoveryErr := coordinator.recoverIncomplete(
				ctx, correlation, content, binding, response, incomplete,
			)
			if recoveryErr == nil {
				response = recovered
				err = nil
			} else {
				var finalIncomplete *action.ErrIncomplete
				if errors.As(recoveryErr, &finalIncomplete) {
					if recordErr := coordinator.recordIncomplete(
						context.WithoutCancel(ctx), correlation,
						finalIncomplete, true,
					); recordErr != nil {
						coordinator.fail(
							context.WithoutCancel(ctx), correlation,
							"incomplete_persistence",
						)
					}
					return
				}
				coordinator.fail(
					context.WithoutCancel(ctx), correlation,
					string(classifyTurnFailure(recoveryErr)),
				)
				return
			}
		}
		if err != nil {
			failureClass := classifyTurnFailure(err)
			if len(response.ToolEvents) == 0 && cascadeEligible(failureClass) {
				recovered, recoveryErr := coordinator.recoverClassified(
					ctx, correlation, runner, content, binding, failureClass,
				)
				if recoveryErr == nil {
					response = recovered
					err = nil
				} else {
					err = recoveryErr
				}
			}
		}
		if err != nil {
			var recoveredIncomplete *action.ErrIncomplete
			if errors.As(err, &recoveredIncomplete) {
				recovered, recoveryErr := coordinator.recoverIncomplete(
					ctx, correlation, content, binding, response, recoveredIncomplete,
				)
				if recoveryErr == nil {
					response = recovered
					err = nil
				} else {
					var finalIncomplete *action.ErrIncomplete
					if errors.As(recoveryErr, &finalIncomplete) {
						if recordErr := coordinator.recordIncomplete(
							context.WithoutCancel(ctx), correlation,
							finalIncomplete, true,
						); recordErr != nil {
							coordinator.fail(
								context.WithoutCancel(ctx), correlation,
								"incomplete_persistence",
							)
						}
						return
					}
					err = recoveryErr
				}
			}
		}
		if err != nil {
			if errors.Is(err, context.Canceled) && coordinator.ctx.Err() != nil {
				if statusErr := coordinator.setTurnStatus(
					context.WithoutCancel(ctx), turnID, session.TurnInterrupted,
				); statusErr != nil {
					coordinator.fail(
						context.WithoutCancel(ctx), correlation,
						"turn_state_persistence",
					)
				}
				return
			}
			errorClass := classifyTurnFailure(err)
			status := session.TurnFailed
			if errors.Is(err, context.Canceled) {
				errorClass = "cancelled"
				status = session.TurnCancelled
			}
			failureCode := classifyTurnFailureCode(err)
			for _, observer := range coordinator.observers {
				recoveryObserver, ok := observer.(TurnRecoveryObserver)
				if !ok || recoveryObserver == nil {
					continue
				}
				if observeErr := recoveryObserver.Failed(
					context.WithoutCancel(ctx), actorID, sessionID,
					content, failureCode,
				); observeErr != nil {
					coordinator.fail(
						context.WithoutCancel(ctx), correlation,
						"repair_state_persistence",
					)
					return
				}
			}
			if statusErr := coordinator.setTurnFailure(
				context.WithoutCancel(ctx), turnID, status, failureCode,
			); statusErr != nil {
				coordinator.fail(
					context.WithoutCancel(ctx), correlation,
					"turn_state_persistence",
				)
				return
			}
			coordinator.failWithCode(
				context.WithoutCancel(ctx), correlation,
				string(errorClass), failureCode,
			)
			return
		}
	}
	if initialErr != nil {
		failureCode := classifyTurnFailureCode(initialErr)
		for _, observer := range coordinator.observers {
			recoveryObserver, ok := observer.(TurnRecoveryObserver)
			if !ok || recoveryObserver == nil {
				continue
			}
			if observeErr := recoveryObserver.Recovered(
				context.WithoutCancel(ctx), actorID, sessionID,
				content, failureCode, response,
			); observeErr != nil {
				coordinator.fail(
					context.WithoutCancel(ctx), correlation,
					"repair_state_persistence",
				)
				return
			}
		}
	}
	if flushErr := generationEvents.Flush(ctx); flushErr != nil {
		coordinator.fail(ctx, correlation, "reasoning_event_delivery")
		return
	}
	if strings.TrimSpace(response.Content) == "" {
		incomplete := &action.ErrIncomplete{
			Phase:      "provider_finalization",
			StuckSince: time.Now().UTC(),
			Recovery: "retry over durable tool results and require a " +
				"user-facing answer",
			Attempt: response.ProviderCalls,
		}
		if recordErr := coordinator.recordIncomplete(
			context.WithoutCancel(ctx), correlation, incomplete, true,
		); recordErr != nil {
			coordinator.fail(
				context.WithoutCancel(ctx), correlation,
				"incomplete_persistence",
			)
		}
		return
	}
	if !response.ContentStreamed {
		deltaContent := response.Content
		if generationEvents.Content() != "" {
			deltaContent = "\n\n" + strings.TrimSpace(response.Content)
		}
		delta, marshalErr := json.Marshal(map[string]any{
			"content": deltaContent, "reset": false,
		})
		if marshalErr != nil {
			coordinator.fail(ctx, correlation, "turn_event_encoding")
			return
		}
		if _, emitErr := coordinator.emitter.Emit(
			ctx, controlplane.EventTurnDelta, correlation, delta,
		); emitErr != nil {
			return
		}
	}
	durableContent := generationEvents.Content()
	if !response.ContentStreamed {
		durableContent = joinVisibleTurnContent(
			durableContent, response.Content,
		)
	}
	if durableContent == "" {
		durableContent = response.Content
	}
	contextTokens += estimateTokens(durableContent)
	appendTranscript := func() error {
		if durable, ok := coordinator.store.(turnMessageStore); ok {
			_, err := durable.AppendTurnMessage(
				ctx,
				sessionID,
				turnID,
				session.RoleAssistant,
				session.MemoryTranscript,
				[]byte(durableContent),
				contextTokens,
			)
			return err
		}
		_, err := coordinator.store.AppendMessage(
			ctx,
			sessionID,
			session.RoleAssistant,
			session.MemoryTranscript,
			[]byte(durableContent),
			contextTokens,
		)
		return err
	}
	if appendErr := appendTranscript(); appendErr != nil {
		coordinator.fail(ctx, correlation, "assistant_persistence")
		return
	}
	if summary := generationEvents.ReasoningSummary(); summary != "" {
		if durable, ok := coordinator.store.(turnMessageStore); ok {
			if _, appendErr := durable.AppendTurnMessage(
				ctx,
				sessionID,
				turnID,
				session.RoleAssistant,
				session.MemorySummary,
				[]byte(summary),
				contextTokens,
			); appendErr != nil {
				coordinator.fail(ctx, correlation, "reasoning_persistence")
				return
			}
		}
	}
	if !response.HonestPartial {
		for _, observer := range coordinator.observers {
			if observer == nil {
				continue
			}
			if observeErr := observer.Completed(
				context.WithoutCancel(ctx), actorID, sessionID, content, response,
			); observeErr != nil {
				coordinator.fail(
					context.WithoutCancel(ctx), correlation,
					"living_state_persistence",
				)
				return
			}
		}
	}
	outcome := "completed"
	if response.HonestPartial {
		outcome = "partial"
	}
	completed, err := json.Marshal(map[string]any{
		"state": "completed", "provider_calls": response.ProviderCalls,
		"tool_count": len(response.ToolEvents), "outcome": outcome,
	})
	if err != nil {
		coordinator.fail(ctx, correlation, "turn_event_encoding")
		return
	}
	if err := coordinator.setTurnStatus(
		context.WithoutCancel(ctx), turnID, session.TurnCompleted,
	); err != nil {
		coordinator.fail(
			context.WithoutCancel(ctx), correlation, "turn_state_persistence",
		)
		return
	}
	_, _ = coordinator.emitter.Emit(
		ctx, controlplane.EventTurnCompleted, correlation, completed,
	)
}

func joinVisibleTurnContent(previous string, current string) string {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	switch {
	case previous == "":
		return current
	case current == "":
		return previous
	default:
		return previous + "\n\n" + current
	}
}

func (coordinator *TurnCoordinator) recoverClassified(
	ctx context.Context,
	correlation controlplane.Correlation,
	runner TurnRunner,
	content string,
	binding TurnBinding,
	failureClass action.FailureClass,
) (agent.Response, error) {
	if correlation.TurnID == nil || correlation.SessionID == nil {
		return agent.Response{}, fmt.Errorf("controlplane adapters: recovery correlation is incomplete")
	}
	payload, err := json.Marshal(map[string]any{
		"state": "recovering", "action": "recovery_cascade",
		"failure_class": failureClass,
	})
	if err != nil {
		return agent.Response{}, err
	}
	if durable, ok := coordinator.store.(durableTurnStore); ok {
		if err := durable.SetTurnRecovery(
			context.WithoutCancel(ctx), *correlation.TurnID,
			session.TurnRecovering, payload,
		); err != nil {
			return agent.Response{}, err
		}
	}
	if _, err := coordinator.emitter.Emit(
		context.WithoutCancel(ctx), controlplane.EventTurnRecovery,
		correlation, payload,
	); err != nil {
		return agent.Response{}, err
	}
	attempt := func(runCtx context.Context) (json.RawMessage, error) {
		response, runErr := runner.Turn(runCtx, content)
		if failureClass == action.FailureRateLimit && runErr != nil {
			runErr = fmt.Errorf("%w: %v", action.ErrRateLimited, runErr)
		}
		if runErr != nil {
			return nil, runErr
		}
		return json.Marshal(response)
	}
	executor, err := action.NewCascadeExecutor(
		"production turn", 1,
		action.RecoverySteps{
			Attempt: attempt,
			RotateCredential: func(context.Context) error {
				return fmt.Errorf(
					"credential pool rotation was exhausted inside provider generation",
				)
			},
			RotateAuthProfile: func(context.Context) error {
				return fmt.Errorf("no alternate production auth profile is configured")
			},
			FallbackModel: func(context.Context) error {
				return fmt.Errorf(
					"provider fallback chain was exhausted inside provider generation",
				)
			},
			Respawn: func(runCtx context.Context) (json.RawMessage, error) {
				fresh, freshErr := coordinator.factory.Runner(*correlation.SessionID, binding)
				if freshErr != nil {
					return nil, freshErr
				}
				response, turnErr := fresh.Turn(runCtx, content)
				if turnErr != nil {
					return nil, turnErr
				}
				return json.Marshal(response)
			},
		},
		nil,
	)
	if err != nil {
		return agent.Response{}, err
	}
	raw, err := executor.Execute(ctx)
	if err != nil {
		return agent.Response{}, err
	}
	var recovered agent.Response
	if err := json.Unmarshal(raw, &recovered); err != nil {
		return agent.Response{}, fmt.Errorf(
			"controlplane adapters: decode cascade result: %w", err,
		)
	}
	return recovered, nil
}

func cascadeEligible(failureClass action.FailureClass) bool {
	switch failureClass {
	case action.FailureTransient, action.FailureRateLimit, action.FailureAuth:
		return true
	default:
		return false
	}
}

func (coordinator *TurnCoordinator) recoverIncomplete(
	ctx context.Context,
	correlation controlplane.Correlation,
	content string,
	binding TurnBinding,
	response agent.Response,
	incomplete *action.ErrIncomplete,
) (agent.Response, error) {
	if correlation.TurnID == nil || correlation.SessionID == nil {
		return agent.Response{}, fmt.Errorf("controlplane adapters: recovery correlation is incomplete")
	}
	checkpoint, err := coordinator.durableCheckpoint(
		ctx, *correlation.TurnID, response,
	)
	if err != nil {
		return agent.Response{}, err
	}
	if err := coordinator.recordIncomplete(
		context.WithoutCancel(ctx), correlation, incomplete, false,
	); err != nil {
		return agent.Response{}, err
	}
	recoveryPayload, err := json.Marshal(map[string]any{
		"state": "recovering", "action": "respawn",
		"phase": incomplete.Phase, "attempt": incomplete.Attempt,
		"failure_class": classifyTurnFailure(incomplete),
	})
	if err != nil {
		return agent.Response{}, err
	}
	if durable, ok := coordinator.store.(durableTurnStore); ok {
		if err := durable.SetTurnRecovery(
			context.WithoutCancel(ctx), *correlation.TurnID,
			session.TurnRecovering, recoveryPayload,
		); err != nil {
			return agent.Response{}, err
		}
	}
	if _, err := coordinator.emitter.Emit(
		context.WithoutCancel(ctx), controlplane.EventTurnRecovery,
		correlation, recoveryPayload,
	); err != nil {
		return agent.Response{}, err
	}
	supervisor, err := action.NewTaskSupervisor(coordinatorRespawner{
		factory:   coordinator.factory,
		sessionID: *correlation.SessionID,
		content:   content,
		binding:   binding,
	})
	if err != nil {
		return agent.Response{}, err
	}
	raw, err := supervisor.Recover(ctx, action.DurableTurnState{
		SessionID:  correlation.SessionID.String(),
		TurnID:     correlation.TurnID.String(),
		Checkpoint: checkpoint,
	}, incomplete)
	if err != nil {
		return agent.Response{}, err
	}
	var recovered agent.Response
	if err := json.Unmarshal(raw, &recovered); err != nil {
		return agent.Response{}, fmt.Errorf(
			"controlplane adapters: decode recovered turn: %w", err,
		)
	}
	return recovered, nil
}

func (coordinator *TurnCoordinator) durableCheckpoint(
	ctx context.Context,
	turnID uuid.UUID,
	response agent.Response,
) (json.RawMessage, error) {
	if response.Checkpoint != nil {
		return json.Marshal(response.Checkpoint)
	}
	durable, ok := coordinator.store.(durableTurnStore)
	if !ok {
		return nil, fmt.Errorf("controlplane adapters: durable turn store is required")
	}
	state, err := durable.LoadTurnState(ctx, turnID)
	if err != nil {
		return nil, err
	}
	if len(state.Checkpoint) == 0 {
		return nil, fmt.Errorf("controlplane adapters: turn checkpoint is missing")
	}
	return state.Checkpoint, nil
}

func (coordinator *TurnCoordinator) recordIncomplete(
	ctx context.Context,
	correlation controlplane.Correlation,
	incomplete *action.ErrIncomplete,
	final bool,
) error {
	payload, err := json.Marshal(map[string]any{
		"state": "incomplete", "phase": incomplete.Phase,
		"last_tool":            incomplete.LastTool,
		"last_result":          incomplete.LastResult,
		"stuck_since":          incomplete.StuckSince,
		"recovery":             incomplete.Recovery,
		"attempt":              incomplete.Attempt,
		"failure_class":        classifyTurnFailure(incomplete),
		"final_honest_partial": final,
	})
	if err != nil {
		return err
	}
	if correlation.TurnID != nil {
		if durable, ok := coordinator.store.(durableTurnStore); ok {
			if err := durable.SetTurnRecovery(
				ctx, *correlation.TurnID, session.TurnIncomplete, payload,
			); err != nil {
				return err
			}
		}
	}
	_, err = coordinator.emitter.Emit(
		ctx, controlplane.EventTurnIncomplete, correlation, payload,
	)
	return err
}

func (coordinator *TurnCoordinator) setTurnStatus(
	ctx context.Context,
	turnID uuid.UUID,
	status session.TurnStatus,
) error {
	if durable, ok := coordinator.store.(durableTurnStore); ok {
		return durable.SetTurnStatus(ctx, turnID, status)
	}
	return nil
}

func (coordinator *TurnCoordinator) setTurnFailure(
	ctx context.Context,
	turnID uuid.UUID,
	status session.TurnStatus,
	code string,
) error {
	if durable, ok := coordinator.store.(durableTurnFailureStore); ok {
		return durable.SetTurnFailure(ctx, turnID, status, code)
	}
	return coordinator.setTurnStatus(ctx, turnID, status)
}

type coordinatorRespawner struct {
	factory   TurnRunnerFactory
	sessionID uuid.UUID
	content   string
	binding   TurnBinding
}

func (respawner coordinatorRespawner) Respawn(
	_ context.Context,
	_ action.DurableTurnState,
) (action.TurnContinuation, error) {
	runner, err := respawner.factory.Runner(respawner.sessionID, respawner.binding)
	if err != nil {
		return nil, err
	}
	return coordinatorContinuation{
		runner: runner, content: respawner.content,
	}, nil
}

type coordinatorContinuation struct {
	runner  TurnRunner
	content string
}

func (continuation coordinatorContinuation) Continue(
	ctx context.Context,
	state action.DurableTurnState,
) (json.RawMessage, error) {
	response, err := resumeRunner(
		ctx, continuation.runner, continuation.content, state.Checkpoint,
	)
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func resumeRunner(
	ctx context.Context,
	runner TurnRunner,
	content string,
	checkpoint json.RawMessage,
) (agent.Response, error) {
	resumer, ok := runner.(TurnResumer)
	if !ok {
		return agent.Response{}, fmt.Errorf(
			"controlplane adapters: production runner cannot resume durable state",
		)
	}
	return resumer.Resume(ctx, content, checkpoint)
}

func classifyTurnFailure(err error) action.FailureClass {
	switch {
	case errors.Is(err, provider.ErrRateLimited),
		errors.Is(err, action.ErrRateLimited):
		return action.FailureRateLimit
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, tools.ErrTimeout):
		return action.FailureTimeout
	case errors.Is(err, agent.ErrToolCallLimit),
		errors.Is(err, agent.ErrConvergenceExceeded),
		errors.Is(err, agent.ErrRefutedPremise):
		return action.FailureLoop
	case errors.Is(err, agent.ErrMissingExpectation):
		return action.FailureValidation
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "401"),
		strings.Contains(lower, "403"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "authentication"):
		return action.FailureAuth
	case strings.Contains(lower, "validation"),
		strings.Contains(lower, "invalid"):
		return action.FailureValidation
	case strings.Contains(lower, "timeout"),
		strings.Contains(lower, "deadline"):
		return action.FailureTimeout
	case strings.Contains(lower, "temporary"),
		strings.Contains(lower, "transient"),
		strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "connection refused"):
		return action.FailureTransient
	default:
		return action.FailurePermanent
	}
}

func classifyTurnFailureCode(err error) string {
	switch {
	case strings.Contains(strings.ToLower(err.Error()), "http status 402"):
		return "provider_payment_required"
	case errors.Is(err, agent.ErrTextualToolMarkup):
		return "provider_textual_tool_markup"
	case errors.Is(err, agent.ErrMissingExpectation):
		return "missing_tool_expectation"
	case errors.Is(err, agent.ErrToolCallLimit):
		return "tool_call_limit"
	case errors.Is(err, agent.ErrConvergenceExceeded):
		return "convergence_limit"
	case errors.Is(err, agent.ErrRefutedPremise):
		return "refuted_premise"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return string(classifyTurnFailure(err))
}

func (coordinator *TurnCoordinator) pruneTurnsLocked() {
	for len(coordinator.turns) > maxRecordedTurns {
		terminalIndex := -1
		for index, turnID := range coordinator.order {
			if !coordinator.turns[turnID].active {
				terminalIndex = index
				break
			}
		}
		if terminalIndex < 0 {
			return
		}
		turnID := coordinator.order[terminalIndex]
		delete(coordinator.turns, turnID)
		coordinator.order = append(
			coordinator.order[:terminalIndex],
			coordinator.order[terminalIndex+1:]...,
		)
	}
}

func (coordinator *TurnCoordinator) fail(
	ctx context.Context,
	correlation controlplane.Correlation,
	errorClass string,
) {
	coordinator.failWithCode(ctx, correlation, errorClass, errorClass)
}

func (coordinator *TurnCoordinator) failWithCode(
	ctx context.Context,
	correlation controlplane.Correlation,
	errorClass string,
	errorCode string,
) {
	payload, err := json.Marshal(map[string]string{
		"state": "failed", "error_class": errorClass, "error_code": errorCode,
	})
	if err != nil {
		return
	}
	if _, err := coordinator.emitter.Emit(
		ctx, controlplane.EventTurnFailed, correlation, payload,
	); err != nil {
		// The journal failure is already the terminal condition for this turn.
		return
	}
}

func (coordinator *TurnCoordinator) handleSteer(
	ctx context.Context,
	request controlplane.Request,
	_ controlplane.EventEmitter,
) (json.RawMessage, error) {
	sessionID, err := scopedSessionID(request)
	if err != nil {
		return nil, err
	}
	var payload steerTurnPayload
	if err := decode(request.Payload, &payload); err != nil {
		return nil, err
	}
	payload.Content = strings.TrimSpace(payload.Content)
	if payload.TurnID == uuid.Nil || payload.Content == "" {
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "active turn and steering content are required",
		}
	}
	coordinator.mu.Lock()
	active, exists := coordinator.active[payload.TurnID]
	if exists && (active.actorID != request.Scope.ActorID ||
		active.sessionID != sessionID) {
		coordinator.mu.Unlock()
		return nil, controlplane.ErrUnauthorized
	}
	resolver := coordinator.steering
	coordinator.mu.Unlock()
	if !exists {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorNotFound, Message: "active turn not found",
		}
	}
	if err := payload.Target.validate(payload.TurnID); err != nil {
		return nil, err
	}
	if resolver == nil {
		return nil, controlplane.PublicError{
			Code:      controlplane.ErrorUnavailable,
			Message:   "revision-bound steering is unavailable",
			Retryable: true,
		}
	}
	if err := resolver.Resolve(
		ctx, request.Scope.ActorID, sessionID, payload.TurnID, payload.Target,
	); err != nil {
		return nil, err
	}
	coordinator.mu.Lock()
	current, stillActive := coordinator.active[payload.TurnID]
	if !stillActive || current.actorID != request.Scope.ActorID ||
		current.sessionID != sessionID {
		coordinator.mu.Unlock()
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorConflict,
			Message: "the targeted turn changed before steering was applied",
		}
	}
	current.cancel()
	coordinator.mu.Unlock()
	return coordinator.startTurn(ctx, request, sessionID, payload.Content, active.binding)
}

func (coordinator *TurnCoordinator) handleRetry(
	ctx context.Context,
	request controlplane.Request,
	_ controlplane.EventEmitter,
) (json.RawMessage, error) {
	sessionID, err := scopedSessionID(request)
	if err != nil {
		return nil, err
	}
	var payload retryTurnPayload
	if err := decode(request.Payload, &payload); err != nil {
		return nil, err
	}
	if payload.TurnID == uuid.Nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "turn to retry is required",
		}
	}
	coordinator.mu.Lock()
	record, exists := coordinator.turns[payload.TurnID]
	coordinator.mu.Unlock()
	if !exists {
		durable, ok := coordinator.store.(durableTurnStore)
		if !ok {
			return nil, controlplane.PublicError{
				Code: controlplane.ErrorNotFound, Message: "turn to retry was not found",
			}
		}
		state, loadErr := durable.LoadTurnState(ctx, payload.TurnID)
		if errors.Is(loadErr, sql.ErrNoRows) {
			return nil, controlplane.PublicError{
				Code: controlplane.ErrorNotFound, Message: "turn to retry was not found",
			}
		}
		if loadErr != nil {
			return nil, fmt.Errorf(
				"controlplane adapters: load durable turn for retry: %w", loadErr,
			)
		}
		if state.ActorID != request.Scope.ActorID || state.SessionID != sessionID {
			return nil, controlplane.ErrUnauthorized
		}
		if state.Status == session.TurnRunning ||
			state.Status == session.TurnRecovering {
			return nil, controlplane.PublicError{
				Code:    controlplane.ErrorConflict,
				Message: "active turn cannot be retried",
			}
		}
		record = turnRecord{
			actorID: state.ActorID, sessionID: state.SessionID,
			content: state.Content,
			binding: TurnBinding{Surface: state.Surface, ProjectID: state.ProjectID},
		}
		coordinator.mu.Lock()
		if current, found := coordinator.turns[payload.TurnID]; found {
			record = current
		} else {
			coordinator.turns[payload.TurnID] = record
			coordinator.order = append(coordinator.order, payload.TurnID)
			coordinator.pruneTurnsLocked()
		}
		coordinator.mu.Unlock()
	}
	if record.actorID != request.Scope.ActorID || record.sessionID != sessionID {
		return nil, controlplane.ErrUnauthorized
	}
	if record.active {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorConflict, Message: "active turn cannot be retried",
		}
	}
	return coordinator.startTurn(ctx, request, sessionID, record.content, record.binding)
}

func (coordinator *TurnCoordinator) handleCancel(
	_ context.Context,
	request controlplane.Request,
	_ controlplane.EventEmitter,
) (json.RawMessage, error) {
	sessionID, err := scopedSessionID(request)
	if err != nil {
		return nil, err
	}
	var payload cancelTurnPayload
	if err := decode(request.Payload, &payload); err != nil {
		return nil, err
	}
	coordinator.mu.Lock()
	active, exists := coordinator.active[payload.TurnID]
	coordinator.mu.Unlock()
	if !exists {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorNotFound, Message: "active turn not found",
		}
	}
	if active.actorID != request.Scope.ActorID || active.sessionID != sessionID {
		return nil, controlplane.ErrUnauthorized
	}
	active.cancel()
	return json.Marshal(map[string]any{
		"turn_id": payload.TurnID, "state": "cancelling",
	})
}

func estimateTokens(content string) int {
	tokens := len([]rune(content)) / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}
