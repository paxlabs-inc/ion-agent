package automatrix_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/presence/automatrix"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/ssrf"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type runnerClock struct{ now time.Time }

func (clock runnerClock) Now() time.Time { return clock.now }

type runnerAuditor struct {
	events atomic.Int64
}

type executorFunc func(context.Context, protocol.NormalizedToolCall) (json.RawMessage, error)

func (execute executorFunc) Execute(ctx context.Context, call protocol.NormalizedToolCall) (json.RawMessage, error) {
	return execute(ctx, call)
}

func (auditor *runnerAuditor) RecordPolicyEvent(
	context.Context,
	policy.AuditEvent,
) error {
	auditor.events.Add(1)
	return nil
}

func TestCuriosityCycleUsesRealPolicyAndRejectsForbiddenTools(t *testing.T) {
	clock := runnerClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	auditor := &runnerAuditor{}
	pipeline, err := policy.NewDefault(clock, auditor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(clock, tools.WithExecutionPolicy(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	handled := atomic.Int64{}
	registrations := []tools.Registration{
		{
			Name: "idle_red", Description: "Consequential.",
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: tools.ClassificationRed,
		},
		{
			Name: "idle_external", Description: "Communicates externally.",
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: tools.ClassificationGreen, ExternallyCommunicating: true,
		},
	}
	for index := range registrations {
		registrations[index].Check = func(context.Context) error { return nil }
		registrations[index].Handler = func(
			context.Context,
			json.RawMessage,
		) (json.RawMessage, error) {
			handled.Add(1)
			return json.RawMessage(`{"unexpected":true}`), nil
		}
		if err := manager.Register(context.Background(), registrations[index]); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher, err := ssrf.New(ssrf.Config{AllowedHosts: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.CloseIdleConnections()
	queue := automatrix.NewQueue()
	actions := []automatrix.Action{
		{ToolCall: call("red", "idle_red")},
		{ToolCall: call("external", "idle_external")},
	}
	if err := queue.Enqueue(context.Background(), workItem(clock.now, actions)); err != nil {
		t.Fatal(err)
	}
	runner, err := automatrix.NewRunner(queue, manager, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Calls != 2 || len(result.Outcomes) != 2 {
		t.Fatalf("cycle result = %+v", result)
	}
	for _, outcome := range result.Outcomes {
		if !errors.Is(outcome.Err, tools.ErrPolicyDenied) ||
			!errors.Is(outcome.Err, policy.ErrDenied) {
			t.Fatalf("%s error = %v, want real policy denial", outcome.ToolName, outcome.Err)
		}
	}
	if handled.Load() != 0 {
		t.Fatalf("forbidden handlers ran %d times", handled.Load())
	}
	if auditor.events.Load() != 2 {
		t.Fatalf("policy denial audit events = %d, want 2", auditor.events.Load())
	}
}

func TestIdleCycleStopsAtTwentyAndRequeuesRemainder(t *testing.T) {
	clock := runnerClock{now: time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)}
	pipeline, err := policy.NewDefault(clock, &runnerAuditor{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(clock, tools.WithExecutionPolicy(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	handled := atomic.Int64{}
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "idle_local", Description: "Local read.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			handled.Add(1)
			return json.RawMessage(`{"ok":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := ssrf.New(ssrf.Config{AllowedHosts: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.CloseIdleConnections()
	queue := automatrix.NewQueue()
	actions := make([]automatrix.Action, 25)
	for index := range actions {
		actions[index] = automatrix.Action{
			ToolCall: call(string(rune('a'+index)), "idle_local"),
		}
	}
	if err := queue.Enqueue(context.Background(), workItem(clock.now, actions)); err != nil {
		t.Fatal(err)
	}
	runner, err := automatrix.NewRunner(queue, manager, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	first, err := runner.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Calls != automatrix.MaxToolCallsPerCycle || handled.Load() != 20 {
		t.Fatalf("first cycle calls/handled = %d/%d", first.Calls, handled.Load())
	}
	remaining := queue.Snapshot()
	if len(remaining) != 1 || len(remaining[0].Actions) != 5 {
		t.Fatalf("requeued remainder = %+v", remaining)
	}
	second, err := runner.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Calls != 5 || handled.Load() != 25 || len(queue.Snapshot()) != 0 {
		t.Fatalf("second cycle = %+v, handled = %d, queue = %+v",
			second, handled.Load(), queue.Snapshot())
	}
}

func TestEveryIdleFetchUsesSSRFDispatcher(t *testing.T) {
	clock := runnerClock{now: time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)}
	pipeline, err := policy.NewDefault(clock, &runnerAuditor{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(clock, tools.WithExecutionPolicy(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := ssrf.New(ssrf.Config{AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.CloseIdleConnections()
	queue := automatrix.NewQueue()
	item := workItem(clock.now, []automatrix.Action{{
		FetchURL: "https://127.0.0.1/private",
	}})
	if err := queue.Enqueue(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	runner, err := automatrix.NewRunner(queue, manager, dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Calls != 1 || len(result.Outcomes) != 1 ||
		!errors.Is(result.Outcomes[0].Err, ssrf.ErrBlocked) {
		t.Fatalf("SSRF outcome = %+v", result)
	}
}

func TestExecutionGuardKeepsPausedWorkQueuedAndEnforcesBudgets(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	dispatcher, err := ssrf.New(ssrf.Config{AllowedHosts: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.CloseIdleConnections()
	queue := automatrix.NewQueue()
	paused := workItem(now, []automatrix.Action{{ToolCall: call("paused", "read")}})
	paused.Priority = 2
	allowed := workItem(now.Add(time.Second), []automatrix.Action{
		{ToolCall: call("one", "read")},
		{ToolCall: call("two", "read")},
		{ToolCall: call("three", "read")},
	})
	if err := queue.Enqueue(context.Background(), paused); err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(context.Background(), allowed); err != nil {
		t.Fatal(err)
	}
	handled := atomic.Int64{}
	runner, err := automatrix.NewRunner(queue, executorFunc(func(context.Context, protocol.NormalizedToolCall) (json.RawMessage, error) {
		handled.Add(1)
		return json.RawMessage(`{"ok":true}`), nil
	}), dispatcher, automatrix.WithExecutionGuard(func(_ context.Context, item automatrix.WorkItem) (automatrix.ExecutionBudget, error) {
		if item.ID == paused.ID {
			return automatrix.ExecutionBudget{Allowed: false}, nil
		}
		return automatrix.ExecutionBudget{Allowed: true, MaxCalls: 2, MaxErrors: 1, MaxElapsed: time.Minute}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Calls != 2 || handled.Load() != 2 {
		t.Fatalf("calls/handled = %d/%d", result.Calls, handled.Load())
	}
	remaining := queue.Snapshot()
	if len(remaining) != 2 {
		t.Fatalf("remaining items = %+v", remaining)
	}
	var pausedActions, allowedActions int
	for _, item := range remaining {
		if item.ID == paused.ID {
			pausedActions = len(item.Actions)
		}
		if item.ID == allowed.ID {
			allowedActions = len(item.Actions)
		}
	}
	if pausedActions != 1 || allowedActions != 1 {
		t.Fatalf("guarded remainders paused/allowed = %d/%d", pausedActions, allowedActions)
	}
}

func workItem(now time.Time, actions []automatrix.Action) automatrix.WorkItem {
	approved := now
	return automatrix.WorkItem{
		ID: uuid.New(), Source: automatrix.SourceCuriosity,
		Kind: "acceptance", Description: "Exercise restricted idle execution.",
		Priority: 1, Actions: actions, CreatedAt: now, ApprovedAt: &approved,
	}
}

func call(id string, name string) *protocol.NormalizedToolCall {
	return &protocol.NormalizedToolCall{
		ID: id, Name: name, Arguments: json.RawMessage(`{}`),
	}
}
