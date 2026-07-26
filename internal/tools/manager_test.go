package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestReadinessCacheGraceAndRecovery(t *testing.T) {
	t.Parallel()
	clock := newManualClock(time.Unix(100, 0))
	manager, err := NewManager(clock)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	var mu sync.Mutex
	available := true
	checks := 0
	err = manager.Register(context.Background(), Registration{
		Name:           "ready",
		Description:    "A readiness test.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: ClassificationGreen,
		Check: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			checks++
			if !available {
				return errors.New("down")
			}
			return nil
		},
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			return arguments, nil
		},
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	assertSurfaceNames(t, manager.Surface(context.Background()), "ready")
	assertSurfaceNames(t, manager.Surface(context.Background()), "ready")
	mu.Lock()
	if checks != 1 {
		t.Fatalf("checks = %d, want 1", checks)
	}
	available = false
	mu.Unlock()

	clock.Advance(31 * time.Second)
	assertSurfaceNames(t, manager.Surface(context.Background()), "ready")
	clock.Advance(30 * time.Second)
	assertSurfaceNames(t, manager.Surface(context.Background()))
	mu.Lock()
	if checks != 3 {
		t.Fatalf("checks = %d, want 3", checks)
	}
	available = true
	mu.Unlock()
	if err := manager.InvalidateReadiness(context.Background(), "ready"); err != nil {
		t.Fatalf("InvalidateReadiness() error = %v", err)
	}
	assertSurfaceNames(t, manager.Surface(context.Background()), "ready")
}

func TestInitialReadinessFailureVanishes(t *testing.T) {
	t.Parallel()
	manager, err := NewManager(newManualClock(time.Unix(1, 0)))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Register(context.Background(), Registration{
		Name:           "missing",
		Description:    "Unavailable.",
		Parameters:     json.RawMessage(`{}`),
		Classification: ClassificationGreen,
		Check:          func(context.Context) error { return errors.New("missing") },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	assertSurfaceNames(t, manager.Surface(context.Background()))
	_, err = manager.Execute(context.Background(), protocol.NormalizedToolCall{
		ID:        "call",
		Name:      "missing",
		Arguments: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestExecuteReportsDeadlineWhenReadinessContextExpires(t *testing.T) {
	t.Parallel()
	manager, err := NewManager(newManualClock(time.Unix(1, 0)),
		WithExecutionPolicy(passthroughExecutionPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(context.Background(), Registration{
		Name: "deadline", Description: "Deadline classification.",
		Parameters: json.RawMessage(`{"type":"object"}`), Classification: ClassificationGreen,
		Check: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("handler must not run")
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "deadline", Name: "deadline", Arguments: json.RawMessage(`{}`),
	})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestManagerExecutionAndErrors(t *testing.T) {
	t.Parallel()
	manager, err := NewManager(
		newManualClock(time.Unix(1, 0)),
		WithExecutionPolicy(passthroughExecutionPolicy{}),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	registrations := []Registration{
		{
			Name: "echo", Description: "Echo.", Parameters: json.RawMessage(`{}`),
			Classification: ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
				arguments[0] = '{'
				return json.RawMessage(`{"ok":true}`), nil
			},
		},
		{
			Name: "timeout", Description: "Timeout.", Parameters: json.RawMessage(`{}`),
			Classification: ClassificationGreen,
			Timeout:        10 * time.Millisecond,
			Check:          func(context.Context) error { return nil },
			Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
		{
			Name: "failure", Description: "Failure.", Parameters: json.RawMessage(`{}`),
			Classification: ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New("handler failed")
			},
		},
		{
			Name: "invalid-json", Description: "Invalid.", Parameters: json.RawMessage(`{}`),
			Classification: ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{`), nil
			},
		},
		{
			Name: "empty", Description: "Empty.", Parameters: json.RawMessage(`{}`),
			Classification: ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return nil, nil
			},
		},
	}
	for _, registration := range registrations {
		if err := manager.Register(context.Background(), registration); err != nil {
			t.Fatalf("Register(%s) error = %v", registration.Name, err)
		}
	}
	result, err := manager.Execute(context.Background(), toolCall("echo"))
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatalf("Execute(echo) = %s, %v", result, err)
	}
	if _, err := manager.Execute(context.Background(), toolCall("timeout")); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Execute(timeout) error = %v", err)
	}
	if _, err := manager.Execute(context.Background(), toolCall("failure")); err == nil {
		t.Fatal("Execute(failure) succeeded")
	}
	if _, err := manager.Execute(context.Background(), toolCall("invalid-json")); err == nil {
		t.Fatal("Execute(invalid-json) succeeded")
	}
	result, err = manager.Execute(context.Background(), toolCall("empty"))
	if err != nil || string(result) != "null" {
		t.Fatalf("Execute(empty) = %s, %v", result, err)
	}
	if _, err := manager.Execute(context.Background(), toolCall("unknown")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Execute(unknown) error = %v", err)
	}
	invalid := toolCall("echo")
	invalid.Arguments = json.RawMessage(`[]`)
	if _, err := manager.Execute(context.Background(), invalid); err == nil {
		t.Fatal("invalid call accepted")
	}
}

func TestManagerInterceptsCallBeforeHandler(t *testing.T) {
	t.Parallel()
	policy := &testExecutionPolicy{}
	manager, err := NewManager(
		newManualClock(time.Unix(1, 0)),
		WithExecutionPolicy(policy),
	)
	if err != nil {
		t.Fatal(err)
	}
	var handled json.RawMessage
	if err := manager.Register(context.Background(), Registration{
		Name:                    "publish",
		Description:             "Publish.",
		Parameters:              json.RawMessage(`{}`),
		Classification:          ClassificationRed,
		ExternallyCommunicating: true,
		Check:                   func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			handled = append(json.RawMessage(nil), arguments...)
			return json.RawMessage(`{"ok":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Execute(context.Background(), toolCall("publish"))
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatalf("Execute() = %s, %v", result, err)
	}
	if string(handled) != `{"policy_modified":true}` {
		t.Fatalf("handler arguments = %s", handled)
	}
	if policy.seen.Classification != ClassificationRed ||
		!policy.seen.ExternallyCommunicating {
		t.Fatalf("policy invocation = %+v", policy.seen)
	}

	policy.deny = true
	handled = nil
	if _, err := manager.Execute(context.Background(), toolCall("publish")); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("denied Execute() error = %v", err)
	}
	if handled != nil {
		t.Fatal("handler ran after policy denial")
	}
}

func TestManagerFailsClosedWithoutPolicy(t *testing.T) {
	t.Parallel()
	manager, err := NewManager(newManualClock(time.Unix(1, 0)))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := manager.Register(context.Background(), Registration{
		Name:           "green",
		Description:    "A classified tool.",
		Parameters:     json.RawMessage(`{}`),
		Classification: ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			called = true
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Execute(context.Background(), toolCall("green"))
	if !errors.Is(err, ErrPolicyDenied) || !errors.Is(err, ErrPolicyRequired) {
		t.Fatalf("Execute() error = %v", err)
	}
	if called {
		t.Fatal("handler ran without a policy")
	}
}

func TestManagerRegistrationValidation(t *testing.T) {
	t.Parallel()
	if _, err := NewManager(nil); err == nil {
		t.Fatal("NewManager(nil) succeeded")
	}
	manager, err := NewManager(newManualClock(time.Unix(1, 0)),
		WithReadinessTiming(time.Second, 2*time.Second))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	valid := Registration{
		Name: "valid", Description: "Valid.", Parameters: json.RawMessage(`{}`),
		Classification: ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`null`), nil
		},
	}
	tests := []func(*Registration){
		func(value *Registration) { value.Name = "" },
		func(value *Registration) { value.Description = "" },
		func(value *Registration) { value.Parameters = json.RawMessage(`[]`) },
		func(value *Registration) { value.Check = nil },
		func(value *Registration) { value.Handler = nil },
		func(value *Registration) { value.Timeout = -1 },
		func(value *Registration) { value.Classification = "" },
		func(value *Registration) { value.Classification = "BLUE" },
	}
	for _, mutate := range tests {
		registration := valid
		mutate(&registration)
		if err := manager.Register(context.Background(), registration); err == nil {
			t.Fatalf("Register(%+v) succeeded", registration)
		}
	}
	if err := manager.Register(context.Background(), valid); err != nil {
		t.Fatalf("Register(valid) error = %v", err)
	}
	if err := manager.Register(context.Background(), valid); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate Register() error = %v", err)
	}
	if err := manager.InvalidateReadiness(context.Background(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("InvalidateReadiness() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Register(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Register() error = %v", err)
	}
	if err := manager.InvalidateReadiness(cancelled, "valid"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled InvalidateReadiness() error = %v", err)
	}
}

func TestConsequentialCallsAreIdempotentWithinTurnScope(t *testing.T) {
	t.Parallel()
	manager, err := NewManager(
		newManualClock(time.Unix(1, 0)),
		WithExecutionPolicy(passthroughExecutionPolicy{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	if err := manager.Register(context.Background(), Registration{
		Name: "publish", Description: "Publish once.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: ClassificationRed,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"published":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	call := protocol.NormalizedToolCall{
		ID: "stable-call", Name: "publish", Arguments: json.RawMessage(`{}`),
	}
	scope := WithIdempotencyScope(context.Background(), "turn-a")
	first, err := manager.Execute(scope, call)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Execute(scope, call)
	if err != nil || string(second) != string(first) || executions != 1 {
		t.Fatalf(
			"idempotent replay first=%s second=%s executions=%d err=%v",
			first, second, executions, err,
		)
	}
	conflict := call
	conflict.Arguments = json.RawMessage(`{"changed":true}`)
	if _, err := manager.Execute(
		scope, conflict,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error = %v", err)
	}
	if _, err := manager.Execute(
		WithIdempotencyScope(context.Background(), "turn-b"), call,
	); err != nil || executions != 2 {
		t.Fatalf("isolated turn execution count=%d err=%v", executions, err)
	}
}

func TestManagerProjectsCanonicalLifecycleAndSuppressesIdempotentReplay(t *testing.T) {
	t.Parallel()
	clock := newManualClock(time.Unix(100, 0).UTC())
	observer := &recordingLifecycleObserver{}
	approval := &recordingApprovalAuthorizer{}
	manager, err := NewManager(
		clock,
		WithExecutionPolicy(passthroughExecutionPolicy{}),
		WithApprovalAuthorizer(approval),
		WithLifecycleObserver(observer),
	)
	if err != nil {
		t.Fatal(err)
	}
	executions := 0
	if err := manager.Register(context.Background(), Registration{
		Name: "publish", Description: "Publish one durable result.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: ClassificationRed,
		Check:          func(context.Context) error { return nil },
		Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			executions++
			if err := ReportProgress(ctx, json.RawMessage(`{"items":1}`)); err != nil {
				return nil, err
			}
			clock.Advance(progressInterval)
			if err := ReportProgress(ctx, json.RawMessage(`{"items":2}`)); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"published":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	actorID, toolEventID := uuid.New(), uuid.New()
	ctx := protocol.WithToolExecutionBinding(
		WithIdempotencyScope(context.Background(), "turn-one"),
		protocol.ToolExecutionBinding{
			ToolEventID: toolEventID, ActorID: actorID,
			OutcomeID: &actorID, AgentID: "ion",
		},
	)
	call := protocol.NormalizedToolCall{
		ID: "provider-call", Name: "publish", Arguments: json.RawMessage(`{}`),
	}
	first, err := manager.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Execute(ctx, call)
	if err != nil || string(first) != string(second) || executions != 1 {
		t.Fatalf("replay first=%s second=%s executions=%d err=%v",
			first, second, executions, err)
	}
	phases := observer.Phases()
	want := []LifecyclePhase{
		LifecycleRequested, LifecycleAwaitingApproval, LifecycleStarted,
		LifecycleProgress, LifecycleProgress, LifecycleCompleted,
	}
	if fmt.Sprint(phases) != fmt.Sprint(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	if approval.calls != 2 {
		t.Fatalf("approval calls = %d, want policy reauthorization on replay", approval.calls)
	}
	terminal := 0
	for _, observation := range observer.Observations() {
		if observation.Phase.Terminal() {
			terminal++
			if observation.Binding.ToolEventID != toolEventID ||
				observation.Call.ID != call.ID ||
				observation.ResultBytes != len(first) {
				t.Fatalf("terminal observation = %+v", observation)
			}
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal events = %d, want exactly one", terminal)
	}
}

func TestManagerClassifiesEveryTerminalOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		err   error
		phase LifecyclePhase
	}{
		{name: "failure", err: errors.New("handler failed"), phase: LifecycleFailed},
		{name: "uncertain", err: MarkOutcomeUnknown(errors.New("receipt lost")), phase: LifecycleOutcomeUnknown},
		{name: "interrupted", err: context.Canceled, phase: LifecycleInterrupted},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observer := &recordingLifecycleObserver{}
			manager, err := NewManager(
				newManualClock(time.Unix(1, 0).UTC()),
				WithExecutionPolicy(passthroughExecutionPolicy{}),
				WithLifecycleObserver(observer),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Register(context.Background(), Registration{
				Name: test.name, Description: "Return one classified result.",
				Parameters:     json.RawMessage(`{"type":"object"}`),
				Classification: ClassificationGreen,
				Check:          func(context.Context) error { return nil },
				Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
					return nil, test.err
				},
			}); err != nil {
				t.Fatal(err)
			}
			actorID, toolEventID := uuid.New(), uuid.New()
			ctx := protocol.WithToolExecutionBinding(
				context.Background(),
				protocol.ToolExecutionBinding{
					ToolEventID: toolEventID, ActorID: actorID,
					OutcomeID: &actorID, AgentID: "ion",
				},
			)
			_, _ = manager.Execute(ctx, protocol.NormalizedToolCall{
				ID: test.name, Name: test.name, Arguments: json.RawMessage(`{}`),
			})
			phases := observer.Phases()
			if len(phases) != 3 || phases[2] != test.phase ||
				!phases[2].Terminal() {
				t.Fatalf("phases = %v, want terminal %s", phases, test.phase)
			}
		})
	}
}

func TestManagerDenialNeverStartsHandler(t *testing.T) {
	t.Parallel()
	observer := &recordingLifecycleObserver{}
	manager, err := NewManager(
		newManualClock(time.Unix(1, 0).UTC()),
		WithExecutionPolicy(&denyingExecutionPolicy{}),
		WithLifecycleObserver(observer),
	)
	if err != nil {
		t.Fatal(err)
	}
	handled := false
	if err := manager.Register(context.Background(), Registration{
		Name: "denied", Description: "A denied operation.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: ClassificationYellow,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			handled = true
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	actorID, toolEventID := uuid.New(), uuid.New()
	ctx := protocol.WithToolExecutionBinding(
		context.Background(),
		protocol.ToolExecutionBinding{
			ToolEventID: toolEventID, ActorID: actorID,
			OutcomeID: &actorID, AgentID: "ion",
		},
	)
	_, err = manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "denied", Name: "denied", Arguments: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrPolicyDenied) || handled {
		t.Fatalf("denied error=%v handled=%t", err, handled)
	}
	if phases := observer.Phases(); fmt.Sprint(phases) !=
		fmt.Sprint([]LifecyclePhase{LifecycleRequested, LifecycleDenied}) {
		t.Fatalf("phases = %v", phases)
	}
}

func TestManagerBoundsLifecycleResultWithoutChangingExecutionResult(t *testing.T) {
	t.Parallel()
	observer := &recordingLifecycleObserver{}
	manager, err := NewManager(
		newManualClock(time.Unix(1, 0).UTC()),
		WithExecutionPolicy(passthroughExecutionPolicy{}),
		WithLifecycleObserver(observer),
	)
	if err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(
		`"` + strings.Repeat("x", MaximumLifecycleResultBytes+1) + `"`,
	)
	if err := manager.Register(context.Background(), Registration{
		Name: "large", Description: "Return a result larger than the display boundary.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return result, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	actorID, toolEventID := uuid.New(), uuid.New()
	ctx := protocol.WithToolExecutionBinding(
		context.Background(),
		protocol.ToolExecutionBinding{
			ToolEventID: toolEventID, ActorID: actorID,
			OutcomeID: &actorID, AgentID: "ion",
		},
	)
	returned, err := manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "large", Name: "large", Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(returned) || len(returned) != len(result) {
		t.Fatalf("execution result changed: bytes=%d, want=%d", len(returned), len(result))
	}
	observations := observer.Observations()
	terminal := observations[len(observations)-1]
	if terminal.Phase != LifecycleCompleted ||
		!terminal.ResultOmitted || len(terminal.Result) != 0 ||
		terminal.ResultBytes != len(result) {
		t.Fatalf("bounded lifecycle terminal = %+v", terminal)
	}
}

func toolCall(name string) protocol.NormalizedToolCall {
	return protocol.NormalizedToolCall{
		ID:        "call-" + name,
		Name:      name,
		Arguments: json.RawMessage(`{"input":"value"}`),
	}
}

type testExecutionPolicy struct {
	seen Invocation
	deny bool
}

type passthroughExecutionPolicy struct{}

type denyingExecutionPolicy struct{}

func (passthroughExecutionPolicy) Authorize(
	_ context.Context,
	invocation Invocation,
) (protocol.NormalizedToolCall, error) {
	return invocation.Call, nil
}

func (*denyingExecutionPolicy) Authorize(
	context.Context,
	Invocation,
) (protocol.NormalizedToolCall, error) {
	return protocol.NormalizedToolCall{}, errors.New("not authorized")
}

type recordingApprovalAuthorizer struct {
	mu    sync.Mutex
	calls int
}

func (authorizer *recordingApprovalAuthorizer) AuthorizeTool(
	ctx context.Context,
	_ Invocation,
) (context.Context, error) {
	authorizer.mu.Lock()
	authorizer.calls++
	authorizer.mu.Unlock()
	return ctx, nil
}

type recordingLifecycleObserver struct {
	mu           sync.Mutex
	observations []LifecycleObservation
}

func (observer *recordingLifecycleObserver) Observe(
	_ context.Context,
	observation LifecycleObservation,
) error {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observation.Progress = append(json.RawMessage(nil), observation.Progress...)
	observation.Result = append(json.RawMessage(nil), observation.Result...)
	observer.observations = append(observer.observations, observation)
	return nil
}

func (observer *recordingLifecycleObserver) Observations() []LifecycleObservation {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]LifecycleObservation(nil), observer.observations...)
}

func (observer *recordingLifecycleObserver) Phases() []LifecyclePhase {
	observations := observer.Observations()
	phases := make([]LifecyclePhase, len(observations))
	for index, observation := range observations {
		phases[index] = observation.Phase
	}
	return phases
}

func (policy *testExecutionPolicy) Authorize(
	_ context.Context,
	invocation Invocation,
) (protocol.NormalizedToolCall, error) {
	policy.seen = invocation
	if policy.deny {
		return protocol.NormalizedToolCall{}, errors.New("denied")
	}
	call := invocation.Call
	call.Arguments = json.RawMessage(`{"policy_modified":true}`)
	return call, nil
}

func assertSurfaceNames(t *testing.T, surface []protocol.ToolDefinition, expected ...string) {
	t.Helper()
	if len(surface) != len(expected) {
		t.Fatalf("surface = %+v, want names %q", surface, expected)
	}
	for index, name := range expected {
		if surface[index].Name != name {
			t.Fatalf("surface[%d].Name = %q, want %q", index, surface[index].Name, name)
		}
	}
}

func TestManagerEnforcesCompleteJSONSchemaBeforeHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	handled := false
	manager, err := NewManager(
		newManualClock(time.Now()),
		WithExecutionPolicy(passthroughExecutionPolicy{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Register(ctx, Registration{
		Name: "schema", Description: "Exercise complete schema enforcement.",
		Parameters: json.RawMessage(`{
			"type":"object","required":["id","mode","nested"],
			"properties":{
				"id":{"type":"string","format":"uuid"},
				"mode":{"type":"string","enum":["safe"]},
				"count":{"type":"integer","minimum":1,"maximum":3},
				"nested":{"type":"object","required":["label"],
					"properties":{"label":{"type":"string","minLength":2,"maxLength":4}},
					"additionalProperties":false}
			},"additionalProperties":false
		}`),
		Classification: ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			handled = true
			return json.RawMessage(`{"ok":true}`), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, arguments := range []string{
		`{"mode":"safe","nested":{"label":"ok"}}`,
		`{"id":"not-a-uuid","mode":"safe","nested":{"label":"ok"}}`,
		`{"id":"00000000-0000-4000-8000-000000000001","mode":"unsafe","nested":{"label":"ok"}}`,
		`{"id":"00000000-0000-4000-8000-000000000001","mode":"safe","count":4,"nested":{"label":"ok"}}`,
		`{"id":"00000000-0000-4000-8000-000000000001","mode":"safe","nested":{"label":"x","extra":true}}`,
	} {
		handled = false
		_, err := manager.Execute(ctx, protocol.NormalizedToolCall{
			ID: fmt.Sprintf("invalid-%d", index), Name: "schema",
			Arguments: json.RawMessage(arguments),
		})
		if err == nil || handled {
			t.Fatalf("arguments %s reached handler: err=%v handled=%v",
				arguments, err, handled)
		}
	}
	_, err = manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "valid", Name: "schema",
		Arguments: json.RawMessage(
			`{"id":"00000000-0000-4000-8000-000000000001","mode":"safe","count":2,"nested":{"label":"okay"}}`,
		),
	})
	if err != nil || !handled {
		t.Fatalf("valid schema call err=%v handled=%v", err, handled)
	}
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now}
}

func (clock *manualClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *manualClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}
