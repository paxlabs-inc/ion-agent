package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type turnTestStore struct {
	mu       sync.Mutex
	session  session.Session
	messages []string
}

type durableTurnTestStore struct {
	*turnTestStore
	turns map[uuid.UUID]session.TurnState
}

func (store *durableTurnTestStore) CreateTurnState(
	_ context.Context,
	state session.TurnState,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.turns[state.TurnID] = state
	return nil
}

func (store *durableTurnTestStore) SaveTurnCheckpoint(
	_ context.Context,
	turnID uuid.UUID,
	checkpoint json.RawMessage,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	state := store.turns[turnID]
	state.Checkpoint = append(json.RawMessage(nil), checkpoint...)
	store.turns[turnID] = state
	return nil
}

func (store *durableTurnTestStore) SetTurnRecovery(
	_ context.Context,
	turnID uuid.UUID,
	status session.TurnStatus,
	recovery json.RawMessage,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	state := store.turns[turnID]
	state.Status = status
	state.Recovery = append(json.RawMessage(nil), recovery...)
	store.turns[turnID] = state
	return nil
}

func (store *durableTurnTestStore) SetTurnStatus(
	_ context.Context,
	turnID uuid.UUID,
	status session.TurnStatus,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	state := store.turns[turnID]
	state.Status = status
	store.turns[turnID] = state
	return nil
}

func (store *durableTurnTestStore) LoadTurnState(
	_ context.Context,
	turnID uuid.UUID,
) (session.TurnState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, found := store.turns[turnID]
	if !found {
		return session.TurnState{}, errors.New("turn state not found")
	}
	return state, nil
}

func (store *durableTurnTestStore) RecoverableTurnStates(
	context.Context,
) ([]session.TurnState, error) {
	return nil, nil
}

func TestProviderPaymentFailureHasActionableStableCode(t *testing.T) {
	t.Parallel()
	err := errors.New("provider: Xiaomi: provider HTTP status 402")
	if code := classifyTurnFailureCode(err); code != "provider_payment_required" {
		t.Fatalf("failure code = %q", code)
	}
	if class := classifyTurnFailure(err); class != "permanent" {
		t.Fatalf("failure class = %q", class)
	}
}

func (store *turnTestStore) GetSession(
	ctx context.Context,
	id uuid.UUID,
) (session.Session, error) {
	if err := ctx.Err(); err != nil {
		return session.Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if id != store.session.ID {
		return session.Session{}, errors.New("session not found")
	}
	return store.session, nil
}

func (store *turnTestStore) AppendMessage(
	ctx context.Context,
	id uuid.UUID,
	_ session.Role,
	_ session.MemoryType,
	content []byte,
	contextTokens int,
) (session.Message, error) {
	if err := ctx.Err(); err != nil {
		return session.Message{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.messages = append(store.messages, string(content))
	store.session.ContextTokens = contextTokens
	return session.Message{ID: uuid.New(), SessionID: id, Content: content}, nil
}

type turnTestRunner struct {
	waitStarted sync.Once
	waiting     chan struct{}

	mu       sync.Mutex
	attempts map[string]int
}

type stallRecoveryRunner struct {
	resumes int
	mu      sync.Mutex
}

func (runner *stallRecoveryRunner) Turn(ctx context.Context, content string) (agent.Response, error) {
	<-ctx.Done()
	return agent.Response{Checkpoint: &agent.TurnCheckpoint{
		Version: 1, UserContent: content,
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: content}},
	}}, ctx.Err()
}

func (runner *stallRecoveryRunner) Resume(
	context.Context, string, json.RawMessage,
) (agent.Response, error) {
	runner.mu.Lock()
	runner.resumes++
	runner.mu.Unlock()
	return agent.Response{Content: "watchdog recovered from durable work"}, nil
}

func TestTurnWatchdogStartsDurableContinuation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	actorID, sessionID := uuid.New(), uuid.New()
	store := &durableTurnTestStore{
		turnTestStore: &turnTestStore{session: session.Session{ID: sessionID}},
		turns:         make(map[uuid.UUID]session.TurnState),
	}
	runner := &stallRecoveryRunner{}
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 32)}
	coordinator, err := NewTurnCoordinator(
		ctx, store,
		TurnRunnerFactoryFunc(func(uuid.UUID, TurnBinding) (TurnRunner, error) { return runner, nil }),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.stallTimeout = 25 * time.Millisecond
	defer coordinator.Close()
	raw, err := coordinator.handleSubmit(ctx, turnRequest(actorID, sessionID, `{"content":"stall then recover"}`), emitter)
	if err != nil {
		t.Fatal(err)
	}
	turn := decodeTurnResult(t, raw)
	foundRecovery := false
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-emitter.events:
			if event.Correlation.TurnID == nil || *event.Correlation.TurnID != turn.TurnID {
				continue
			}
			if event.Type == controlplane.EventTurnRecovery {
				foundRecovery = true
			}
			if event.Type == controlplane.EventTurnCompleted {
				if !foundRecovery {
					t.Fatal("turn completed without a watchdog recovery event")
				}
				runner.mu.Lock()
				resumes := runner.resumes
				runner.mu.Unlock()
				if resumes != 1 {
					t.Fatalf("watchdog resumes = %d", resumes)
				}
				return
			}
		case <-deadline:
			t.Fatal("watchdog did not recover the stalled turn")
		}
	}
}

func (runner *turnTestRunner) Turn(
	ctx context.Context,
	content string,
) (agent.Response, error) {
	switch content {
	case "wait for steering":
		runner.waitStarted.Do(func() { close(runner.waiting) })
		<-ctx.Done()
		return agent.Response{}, ctx.Err()
	case "fail once":
		runner.mu.Lock()
		runner.attempts[content]++
		attempt := runner.attempts[content]
		runner.mu.Unlock()
		if attempt == 1 {
			return agent.Response{}, errors.New("permanent failure")
		}
		return agent.Response{Content: "retry recovered"}, nil
	case "transient once":
		runner.mu.Lock()
		runner.attempts[content]++
		attempt := runner.attempts[content]
		runner.mu.Unlock()
		if attempt == 1 {
			return agent.Response{}, errors.New("transient failure")
		}
		return agent.Response{Content: "transient recovered"}, nil
	case "blank final":
		return agent.Response{ProviderCalls: 2}, nil
	default:
		return agent.Response{Content: content + " completed"}, nil
	}
}

type turnTestEmitter struct {
	mu       sync.Mutex
	sequence uint64
	events   chan controlplane.Event
}

func (emitter *turnTestEmitter) Emit(
	ctx context.Context,
	eventType controlplane.EventType,
	correlation controlplane.Correlation,
	payload json.RawMessage,
) (controlplane.Event, error) {
	if err := ctx.Err(); err != nil {
		return controlplane.Event{}, err
	}
	emitter.mu.Lock()
	emitter.sequence++
	sequence := emitter.sequence
	emitter.mu.Unlock()
	event, err := controlplane.NewEvent(
		eventType, correlation, payload, time.Unix(int64(sequence), 0).UTC(),
	)
	if err != nil {
		return controlplane.Event{}, err
	}
	event.Sequence = sequence
	emitter.events <- event
	return event, nil
}

func TestTurnCoordinatorSteerAndRetryUseScopedRecordedInput(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	actorID := uuid.New()
	sessionID := uuid.New()
	store := &turnTestStore{session: session.Session{ID: sessionID}}
	runner := &turnTestRunner{
		waiting: make(chan struct{}), attempts: make(map[string]int),
	}
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 32)}
	coordinator, err := NewTurnCoordinator(
		ctx,
		store,
		TurnRunnerFactoryFunc(func(uuid.UUID, TurnBinding) (TurnRunner, error) {
			return runner, nil
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	journal, err := controlplane.OpenJournal(
		ctx,
		filepath.Join(t.TempDir(), "steering.db"),
		types.SystemClock{},
		controlplane.JournalConfig{Retention: 64, SubscriberBuffer: 4},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	coordinator.SetSteeringResolver(NewJournalSteeringResolver(journal))

	initial := turnRequest(actorID, sessionID, `{"content":"wait for steering"}`)
	initialResult, err := coordinator.handleSubmit(ctx, initial, emitter)
	if err != nil {
		t.Fatal(err)
	}
	initialTurn := decodeTurnResult(t, initialResult)
	targetEvent, err := controlplane.NewEvent(
		controlplane.EventTurnStarted,
		controlplane.Correlation{
			ActorID: actorID, SessionID: &sessionID, TurnID: &initialTurn.TurnID,
		},
		json.RawMessage(`{"state":"running"}`),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	targetEvent, err = journal.Append(ctx, targetEvent)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.waiting:
	case <-time.After(time.Second):
		t.Fatal("initial turn did not start")
	}

	if _, err := coordinator.handleRetry(
		ctx,
		turnRequestWithPayload(actorID, sessionID, map[string]any{
			"turn_id": initialTurn.TurnID,
		}),
		emitter,
	); !hasPublicCode(err, controlplane.ErrorConflict) {
		t.Fatalf("retry active turn error = %v", err)
	}
	otherActor := turnRequestWithPayload(uuid.New(), sessionID, map[string]any{
		"turn_id": initialTurn.TurnID, "content": "unauthorized steering",
	})
	if _, err := coordinator.handleSteer(ctx, otherActor, emitter); !errors.Is(
		err,
		controlplane.ErrUnauthorized,
	) {
		t.Fatalf("cross-actor steer error = %v", err)
	}

	steeredRaw, err := coordinator.handleSteer(
		ctx,
		turnRequestWithPayload(actorID, sessionID, map[string]any{
			"turn_id": initialTurn.TurnID, "content": "bounded correction",
			"target": map[string]any{
				"kind": "turn", "task_id": initialTurn.TurnID,
				"agent_id": "ion", "tool_action": "turn.run",
				"target_revision": targetEvent.Sequence,
			},
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	steered := decodeTurnResult(t, steeredRaw)
	waitForTurnEvents(t, emitter.events, map[uuid.UUID]controlplane.EventType{
		initialTurn.TurnID: controlplane.EventTurnFailed,
		steered.TurnID:     controlplane.EventTurnCompleted,
	})

	failedRaw, err := coordinator.handleSubmit(
		ctx,
		turnRequest(actorID, sessionID, `{"content":"fail once"}`),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	failed := decodeTurnResult(t, failedRaw)
	waitForTurnEvent(t, emitter.events, controlplane.EventTurnFailed, failed.TurnID)
	waitForInactiveRecord(t, coordinator, failed.TurnID)

	retriedRaw, err := coordinator.handleRetry(
		ctx,
		turnRequestWithPayload(actorID, sessionID, map[string]any{
			"turn_id": failed.TurnID,
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	retried := decodeTurnResult(t, retriedRaw)
	waitForTurnEvent(t, emitter.events, controlplane.EventTurnCompleted, retried.TurnID)

	store.mu.Lock()
	defer store.mu.Unlock()
	want := []string{
		"wait for steering",
		"bounded correction",
		"bounded correction completed",
		"fail once",
		"fail once",
		"retry recovered",
	}
	if len(store.messages) != len(want) {
		t.Fatalf("durable messages = %q, want %q", store.messages, want)
	}
	for index := range want {
		if store.messages[index] != want[index] {
			t.Fatalf("durable messages = %q, want %q", store.messages, want)
		}
	}
}

func TestTurnRetryReconstructsTerminalDurableStateAfterRestart(t *testing.T) {
	ctx := context.Background()
	actorID := uuid.New()
	sessionID := uuid.New()
	failedTurnID := uuid.New()
	store := &durableTurnTestStore{
		turnTestStore: &turnTestStore{session: session.Session{ID: sessionID}},
		turns: map[uuid.UUID]session.TurnState{
			failedTurnID: {
				TurnID: failedTurnID, ActorID: actorID, SessionID: sessionID,
				Content: "recover after restart", Status: session.TurnFailed,
				UpdatedAt: time.Now().UTC(),
			},
		},
	}
	runner := &turnTestRunner{
		waiting: make(chan struct{}), attempts: make(map[string]int),
	}
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 16)}
	coordinator, err := NewTurnCoordinator(
		ctx, store,
		TurnRunnerFactoryFunc(func(uuid.UUID, TurnBinding) (TurnRunner, error) {
			return runner, nil
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()

	retriedRaw, err := coordinator.handleRetry(
		ctx,
		turnRequestWithPayload(actorID, sessionID, map[string]any{
			"turn_id": failedTurnID,
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	retried := decodeTurnResult(t, retriedRaw)
	waitForTurnEvent(t, emitter.events, controlplane.EventTurnCompleted, retried.TurnID)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.messages) != 2 ||
		store.messages[0] != "recover after restart" ||
		store.messages[1] != "recover after restart completed" {
		t.Fatalf("restart retry messages = %+v", store.messages)
	}
}

func TestTurnStructuredStudioBindingSurvivesFailureAndRetry(t *testing.T) {
	ctx := context.Background()
	actorID, sessionID, projectID := uuid.New(), uuid.New(), uuid.New()
	store := &durableTurnTestStore{
		turnTestStore: &turnTestStore{session: session.Session{ID: sessionID}},
		turns:         make(map[uuid.UUID]session.TurnState),
	}
	runner := &turnTestRunner{
		waiting: make(chan struct{}), attempts: make(map[string]int),
	}
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 16)}
	var bindingsMu sync.Mutex
	bindings := make([]TurnBinding, 0, 2)
	coordinator, err := NewTurnCoordinator(
		ctx, store,
		TurnRunnerFactoryFunc(func(_ uuid.UUID, binding TurnBinding) (TurnRunner, error) {
			bindingsMu.Lock()
			bindings = append(bindings, binding)
			bindingsMu.Unlock()
			return runner, nil
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()

	failedRaw, err := coordinator.handleSubmit(
		ctx,
		turnRequestWithPayload(actorID, sessionID, map[string]any{
			"content": "fail once", "surface": TurnSurfaceStudio,
			"project_id": projectID,
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	failed := decodeTurnResult(t, failedRaw)
	waitForTurnEvent(t, emitter.events, controlplane.EventTurnFailed, failed.TurnID)
	waitForInactiveRecord(t, coordinator, failed.TurnID)
	state, err := store.LoadTurnState(ctx, failed.TurnID)
	if err != nil || state.Surface != TurnSurfaceStudio || state.ProjectID != projectID {
		t.Fatalf("durable binding = %+v, %v", state, err)
	}

	retriedRaw, err := coordinator.handleRetry(
		ctx,
		turnRequestWithPayload(actorID, sessionID, map[string]any{"turn_id": failed.TurnID}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	retried := decodeTurnResult(t, retriedRaw)
	waitForTurnEvent(t, emitter.events, controlplane.EventTurnCompleted, retried.TurnID)
	bindingsMu.Lock()
	defer bindingsMu.Unlock()
	if len(bindings) != 2 {
		t.Fatalf("runner bindings = %+v", bindings)
	}
	for _, binding := range bindings {
		if binding.Surface != TurnSurfaceStudio || binding.ProjectID != projectID {
			t.Fatalf("runner binding = %+v", binding)
		}
	}
}

func TestTurnRejectsContradictoryStructuredWorkspaceBinding(t *testing.T) {
	ctx := context.Background()
	actorID, sessionID := uuid.New(), uuid.New()
	store := &turnTestStore{session: session.Session{ID: sessionID}}
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 4)}
	coordinator, err := NewTurnCoordinator(
		ctx, store,
		TurnRunnerFactoryFunc(func(uuid.UUID, TurnBinding) (TurnRunner, error) {
			return &turnTestRunner{waiting: make(chan struct{}), attempts: make(map[string]int)}, nil
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	for _, payload := range []map[string]any{
		{"content": "unsafe", "surface": TurnSurfaceStudio},
		{"content": "unsafe", "surface": TurnSurfaceGeneral, "project_id": uuid.New()},
		{"content": "unsafe", "surface": "surprise"},
	} {
		if _, err := coordinator.handleSubmit(
			ctx, turnRequestWithPayload(actorID, sessionID, payload), emitter,
		); !hasPublicCode(err, controlplane.ErrorInvalid) {
			t.Fatalf("binding payload %+v error = %v", payload, err)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.messages) != 0 {
		t.Fatalf("rejected binding persisted messages = %+v", store.messages)
	}
}

func TestTurnCoordinatorTransientFailureUsesRecoveryCascade(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	actorID := uuid.New()
	sessionID := uuid.New()
	store := &turnTestStore{session: session.Session{ID: sessionID}}
	runner := &turnTestRunner{
		waiting: make(chan struct{}), attempts: make(map[string]int),
	}
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 16)}
	coordinator, err := NewTurnCoordinator(
		ctx,
		store,
		TurnRunnerFactoryFunc(func(uuid.UUID, TurnBinding) (TurnRunner, error) {
			return runner, nil
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()

	raw, err := coordinator.handleSubmit(
		ctx,
		turnRequest(actorID, sessionID, `{"content":"transient once"}`),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	turn := decodeTurnResult(t, raw)
	waitForTurnEvents(t, emitter.events, map[uuid.UUID]controlplane.EventType{
		turn.TurnID: controlplane.EventTurnCompleted,
	})

	runner.mu.Lock()
	attempts := runner.attempts["transient once"]
	runner.mu.Unlock()
	if attempts != 2 {
		t.Fatalf("transient attempts = %d, want 2", attempts)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	want := []string{"transient once", "transient recovered"}
	if len(store.messages) != len(want) {
		t.Fatalf("durable messages = %q, want %q", store.messages, want)
	}
	for index := range want {
		if store.messages[index] != want[index] {
			t.Fatalf("durable messages = %q, want %q", store.messages, want)
		}
	}
}

func TestTurnCoordinatorRejectsBlankSuccessfulResponse(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	actorID := uuid.New()
	sessionID := uuid.New()
	store := &turnTestStore{session: session.Session{ID: sessionID}}
	runner := &turnTestRunner{
		waiting: make(chan struct{}), attempts: make(map[string]int),
	}
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 16)}
	coordinator, err := NewTurnCoordinator(
		ctx,
		store,
		TurnRunnerFactoryFunc(func(uuid.UUID, TurnBinding) (TurnRunner, error) {
			return runner, nil
		}),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()

	raw, err := coordinator.handleSubmit(
		ctx,
		turnRequest(actorID, sessionID, `{"content":"blank final"}`),
		emitter,
	)
	if err != nil {
		t.Fatal(err)
	}
	turn := decodeTurnResult(t, raw)
	event := waitForTurnEvent(
		t, emitter.events, controlplane.EventTurnIncomplete, turn.TurnID,
	)
	var payload struct {
		State              string `json:"state"`
		Phase              string `json:"phase"`
		Attempt            int    `json:"attempt"`
		FinalHonestPartial bool   `json:"final_honest_partial"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.State != "incomplete" ||
		payload.Phase != "provider_finalization" ||
		payload.Attempt != 2 ||
		!payload.FinalHonestPartial {
		t.Fatalf("incomplete payload = %+v", payload)
	}
	waitForInactiveRecord(t, coordinator, turn.TurnID)
	store.mu.Lock()
	messages := append([]string(nil), store.messages...)
	store.mu.Unlock()
	if len(messages) != 1 || messages[0] != "blank final" {
		t.Fatalf("durable messages = %q, want only the user turn", messages)
	}
	select {
	case event := <-emitter.events:
		if event.Type == controlplane.EventTurnCompleted &&
			event.Correlation.TurnID != nil &&
			*event.Correlation.TurnID == turn.TurnID {
			t.Fatalf("blank response emitted completion: %+v", event)
		}
	default:
	}
}

func TestProviderReasoningIsNeverPublished(t *testing.T) {
	t.Parallel()
	emitter := &turnTestEmitter{events: make(chan controlplane.Event, 1)}
	observer := &turnGenerationObserver{emitter: emitter}
	if err := observer.ReasoningDelta(
		context.Background(),
		"The user is simply saying hello.",
	); err != nil {
		t.Fatal(err)
	}
	if err := observer.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-emitter.events:
		t.Fatalf("provider reasoning was published: %+v", event)
	default:
	}
}

func turnRequest(
	actorID uuid.UUID,
	sessionID uuid.UUID,
	payload string,
) controlplane.Request {
	return controlplane.Request{
		Scope:   controlplane.Scope{ActorID: actorID, SessionID: &sessionID},
		Payload: json.RawMessage(payload),
	}
}

func turnRequestWithPayload(
	actorID uuid.UUID,
	sessionID uuid.UUID,
	payload map[string]any,
) controlplane.Request {
	encoded, _ := json.Marshal(payload)
	return turnRequest(actorID, sessionID, string(encoded))
}

func decodeTurnResult(t *testing.T, raw json.RawMessage) submitTurnResult {
	t.Helper()
	var result submitTurnResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.TurnID == uuid.Nil || result.State != "running" {
		t.Fatalf("turn result = %+v", result)
	}
	return result
}

func waitForTurnEvent(
	t *testing.T,
	events <-chan controlplane.Event,
	eventType controlplane.EventType,
	turnID uuid.UUID,
) controlplane.Event {
	t.Helper()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == eventType && event.Correlation.TurnID != nil &&
				*event.Correlation.TurnID == turnID {
				return event
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s on %s", eventType, turnID)
		}
	}
}

func waitForTurnEvents(
	t *testing.T,
	events <-chan controlplane.Event,
	expected map[uuid.UUID]controlplane.EventType,
) {
	t.Helper()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for len(expected) > 0 {
		select {
		case event := <-events:
			if event.Correlation.TurnID == nil {
				continue
			}
			eventType, exists := expected[*event.Correlation.TurnID]
			if exists && event.Type == eventType {
				delete(expected, *event.Correlation.TurnID)
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for turn events: %+v", expected)
		}
	}
}

func waitForInactiveRecord(
	t *testing.T,
	coordinator *TurnCoordinator,
	turnID uuid.UUID,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		active := coordinator.turns[turnID].active
		coordinator.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("turn %s remained active", turnID)
}

func hasPublicCode(err error, code controlplane.ErrorCode) bool {
	var public controlplane.PublicError
	return errors.As(err, &public) && public.Code == code
}
