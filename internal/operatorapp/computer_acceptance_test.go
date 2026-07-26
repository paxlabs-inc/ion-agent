package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestComputerLifecycleDurablyProjectsRealManagerOutcomes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "computer.db")
	clock := types.SystemClock{}
	journal, err := controlplane.OpenJournal(
		ctx, path, clock,
		controlplane.JournalConfig{Retention: 256, SubscriberBuffer: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := controlplane.NewDispatcher(
		journal, clock,
		controlplane.SnapshotFunc(func(
			context.Context,
			controlplane.Scope,
		) (json.RawMessage, error) {
			return json.RawMessage(`{"computer":"durable"}`), nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	approval := &computerApprovalAuthorizer{
		entered: make(chan struct{}, 1), release: make(chan error, 1),
	}
	manager, err := tools.NewManager(
		clock,
		tools.WithExecutionPolicy(computerPassthroughPolicy{}),
		tools.WithApprovalAuthorizer(approval),
		tools.WithLifecycleObserver(computerLifecycleObserver{emitter: dispatcher}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var successfulExecutions atomic.Int32
	interruptStarted := make(chan struct{}, 1)
	registrations := []tools.Registration{
		{
			Name: "success", Description: "Return a verified result.",
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: tools.ClassificationYellow,
			Check:          func(context.Context) error { return nil },
			Handler: func(runCtx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				successfulExecutions.Add(1)
				if err := tools.ReportProgress(
					runCtx, json.RawMessage(`{"records":1}`),
				); err != nil {
					return nil, err
				}
				return json.RawMessage(`{"verified":true}`), nil
			},
		},
		{
			Name: "failure", Description: "Return a known failure.",
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: tools.ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New("known failure")
			},
		},
		{
			Name: "invalid", Description: "Require one exact field.",
			Parameters: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["verification_required"],"properties":{"verification_required":{"type":"array"}}}`,
			),
			Classification: tools.ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				t.Fatal("invalid arguments reached the handler")
				return nil, nil
			},
		},
		{
			Name: "uncertain", Description: "Lose an external receipt.",
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: tools.ClassificationYellow,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return nil, tools.MarkOutcomeUnknown(errors.New("receipt unavailable"))
			},
		},
		{
			Name: "interrupt", Description: "Wait for cancellation.",
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: tools.ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(runCtx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				interruptStarted <- struct{}{}
				<-runCtx.Done()
				return nil, runCtx.Err()
			},
		},
		{
			Name: "approval", Description: "Wait for an exact decision.",
			Parameters:     json.RawMessage(`{"type":"object"}`),
			Classification: tools.ClassificationRed,
			Check:          func(context.Context) error { return nil },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.RawMessage(`{"approved":true}`), nil
			},
		},
	}
	for _, registration := range registrations {
		if err := manager.Register(ctx, registration); err != nil {
			t.Fatal(err)
		}
	}

	actorID, sessionID, turnID := uuid.New(), uuid.New(), uuid.New()
	execute := func(
		runCtx context.Context,
		name string,
		toolEventID uuid.UUID,
	) (json.RawMessage, error) {
		bound := protocol.WithToolExecutionBinding(
			tools.WithIdempotencyScope(runCtx, turnID.String()),
			protocol.ToolExecutionBinding{
				ToolEventID: toolEventID, ActorID: actorID,
				SessionID: &sessionID, TurnID: &turnID, OutcomeID: &turnID,
				AgentID: "ion",
			},
		)
		return manager.Execute(bound, protocol.NormalizedToolCall{
			ID:   "provider-" + name + "-" + toolEventID.String(),
			Name: name, Arguments: json.RawMessage(`{}`),
		})
	}

	successID := uuid.New()
	first, err := execute(ctx, "success", successID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := execute(ctx, "success", successID)
	if err != nil || string(first) != string(second) ||
		successfulExecutions.Load() != 1 {
		t.Fatalf("idempotent execution first=%s second=%s count=%d err=%v",
			first, second, successfulExecutions.Load(), err)
	}
	if _, err := execute(ctx, "failure", uuid.New()); err == nil {
		t.Fatal("known failure succeeded")
	}
	if _, err := execute(ctx, "invalid", uuid.New()); err == nil {
		t.Fatal("invalid arguments succeeded")
	}
	if _, err := execute(ctx, "uncertain", uuid.New()); err == nil {
		t.Fatal("uncertain outcome succeeded")
	}
	interruptCtx, cancel := context.WithCancel(ctx)
	interruptResult := make(chan error, 1)
	go func() {
		_, executeErr := execute(interruptCtx, "interrupt", uuid.New())
		interruptResult <- executeErr
	}()
	select {
	case <-interruptStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("interrupt handler did not start")
	}
	cancel()
	if err := <-interruptResult; err == nil {
		t.Fatal("interrupted call succeeded")
	}

	approvalID := uuid.New()
	approvalResult := make(chan error, 1)
	go func() {
		_, executeErr := execute(ctx, "approval", approvalID)
		approvalResult <- executeErr
	}()
	select {
	case <-approval.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("approval boundary was not reached")
	}
	replay, err := journal.ReplayActor(ctx, actorID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	assertComputerPhases(
		t, replay.Events, approvalID,
		controlplane.ComputerRequested,
		controlplane.ComputerAwaitingApproval,
	)
	approval.release <- nil
	select {
	case err := <-approvalResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approved execution did not finish")
	}

	deniedID := uuid.New()
	approval.release <- controlplane.ErrApprovalDenied
	if _, err := execute(ctx, "approval", deniedID); err == nil {
		t.Fatal("denied approval succeeded")
	}

	replay, err = journal.ReplayActor(ctx, actorID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	assertComputerPhases(
		t, replay.Events, successID,
		controlplane.ComputerRequested, controlplane.ComputerStarted,
		controlplane.ComputerProgress, controlplane.ComputerCompleted,
	)
	assertComputerTerminal(t, replay.Events, "failure", controlplane.ComputerFailed)
	assertComputerFailureSummary(
		t, replay.Events, "invalid", "invalid_arguments",
		"Tool input is missing required field: verification_required.",
	)
	assertComputerTerminal(t, replay.Events, "uncertain", controlplane.ComputerOutcomeUnknown)
	assertComputerTerminal(t, replay.Events, "interrupt", controlplane.ComputerInterrupted)
	assertComputerPhases(
		t, replay.Events, approvalID,
		controlplane.ComputerRequested,
		controlplane.ComputerAwaitingApproval,
		controlplane.ComputerStarted,
		controlplane.ComputerCompleted,
	)
	assertComputerPhases(
		t, replay.Events, deniedID,
		controlplane.ComputerRequested,
		controlplane.ComputerAwaitingApproval,
		controlplane.ComputerDenied,
	)

	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := controlplane.OpenJournal(
		ctx, path, clock,
		controlplane.JournalConfig{Retention: 256, SubscriberBuffer: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	afterRestart, err := restarted.ReplayActor(ctx, actorID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	assertComputerPhases(
		t, afterRestart.Events, successID,
		controlplane.ComputerRequested, controlplane.ComputerStarted,
		controlplane.ComputerProgress, controlplane.ComputerCompleted,
	)
}

func TestComputerLifecycleSlowClientGapAndConcurrentActors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "computer-gap.db")
	clock := types.SystemClock{}
	journal, err := controlplane.OpenJournal(
		ctx, path, clock,
		controlplane.JournalConfig{Retention: 12, SubscriberBuffer: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	dispatcher, err := controlplane.NewDispatcher(
		journal, clock,
		controlplane.SnapshotFunc(func(
			context.Context,
			controlplane.Scope,
		) (json.RawMessage, error) {
			return json.RawMessage(`{"gap_boundary":true}`), nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(
		clock,
		tools.WithExecutionPolicy(computerPassthroughPolicy{}),
		tools.WithLifecycleObserver(computerLifecycleObserver{emitter: dispatcher}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(ctx, tools.Registration{
		Name: "inspect", Description: "Inspect one bounded item.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"observed":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	actorA, actorB := uuid.New(), uuid.New()
	subscription, err := journal.SubscribeActor(ctx, actorA, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		for _, actorID := range []uuid.UUID{actorA, actorB} {
			wait.Add(1)
			go func(index int, actorID uuid.UUID) {
				defer wait.Done()
				toolEventID := uuid.New()
				outcomeID := uuid.New()
				bound := protocol.WithToolExecutionBinding(
					ctx,
					protocol.ToolExecutionBinding{
						ToolEventID: toolEventID, ActorID: actorID,
						OutcomeID: &outcomeID, AgentID: actorID.String(),
					},
				)
				_, executeErr := manager.Execute(
					bound,
					protocol.NormalizedToolCall{
						ID:   fmt.Sprintf("%s-%d", actorID, index),
						Name: "inspect", Arguments: json.RawMessage(`{}`),
					},
				)
				if executeErr != nil {
					t.Errorf("execute actor=%s index=%d: %v", actorID, index, executeErr)
				}
			}(index, actorID)
		}
	}
	wait.Wait()
	select {
	case _, open := <-subscription.Live:
		if open {
			select {
			case _, open = <-subscription.Live:
				if open {
					t.Fatal("slow subscription remained open after overflowing")
				}
			case <-time.After(time.Second):
				t.Fatal("slow subscription did not close")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("slow subscription produced no state")
	}
	for _, actorID := range []uuid.UUID{actorA, actorB} {
		replay, err := journal.ReplayActor(ctx, actorID, 0, 256)
		if err != nil {
			t.Fatal(err)
		}
		if !replay.Gap || replay.Earliest == 0 {
			t.Fatalf("actor %s replay did not expose retention gap: %+v", actorID, replay)
		}
		for _, event := range replay.Events {
			if event.Correlation.ActorID != actorID {
				t.Fatalf("actor %s received cross-scope event %+v", actorID, event)
			}
		}
		recovery, err := dispatcher.Recover(
			ctx, controlplane.Scope{ActorID: actorID}, replay,
		)
		if err != nil || recovery.GapMarker == nil ||
			string(recovery.Snapshot) != `{"gap_boundary":true}` {
			t.Fatalf("actor %s recovery = %+v, %v", actorID, recovery, err)
		}
	}
}

type computerPassthroughPolicy struct{}

func (computerPassthroughPolicy) Authorize(
	_ context.Context,
	invocation tools.Invocation,
) (protocol.NormalizedToolCall, error) {
	return invocation.Call, nil
}

type computerApprovalAuthorizer struct {
	entered chan struct{}
	release chan error
}

func (authorizer *computerApprovalAuthorizer) AuthorizeTool(
	ctx context.Context,
	_ tools.Invocation,
) (context.Context, error) {
	select {
	case authorizer.entered <- struct{}{}:
	default:
	}
	select {
	case err := <-authorizer.release:
		return ctx, err
	case <-ctx.Done():
		return ctx, ctx.Err()
	}
}

func assertComputerPhases(
	t *testing.T,
	events []controlplane.Event,
	toolEventID uuid.UUID,
	want ...controlplane.ComputerPhase,
) {
	t.Helper()
	got := make([]controlplane.ComputerPhase, 0, len(want))
	terminal := 0
	for _, event := range events {
		if event.Correlation.ToolID == nil ||
			*event.Correlation.ToolID != toolEventID {
			continue
		}
		var payload controlplane.ComputerEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if err := payload.Validate(); err != nil {
			t.Fatal(err)
		}
		if payload.Phase == controlplane.ComputerAwaitingApproval ||
			payload.Phase.Terminal() {
			model, compatibility, err := controlplane.ResolveDisplayModel(
				payload.DisplayModel, len(payload.SourceReferences),
			)
			if err != nil {
				t.Fatal(err)
			}
			if compatibility != controlplane.DisplayCurrent ||
				model.ProtocolVersion != controlplane.DisplayModelVersion {
				t.Fatalf(
					"tool %s display compatibility = %q, model = %+v",
					toolEventID, compatibility, model,
				)
			}
		}
		got = append(got, payload.Phase)
		if payload.Phase.Terminal() {
			terminal++
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tool %s phases = %v, want %v", toolEventID, got, want)
	}
	wantTerminal := 0
	if len(want) > 0 && want[len(want)-1].Terminal() {
		wantTerminal = 1
	}
	if terminal != wantTerminal {
		t.Fatalf(
			"tool %s terminal count = %d, want %d",
			toolEventID, terminal, wantTerminal,
		)
	}
}

func assertComputerTerminal(
	t *testing.T,
	events []controlplane.Event,
	providerSuffix string,
	want controlplane.ComputerPhase,
) {
	t.Helper()
	var ids []uuid.UUID
	for _, event := range events {
		var payload controlplane.ComputerEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if strings.HasPrefix(
			payload.ProviderCallID,
			"provider-"+providerSuffix+"-",
		) &&
			payload.Phase.Terminal() {
			if payload.Phase != want {
				t.Fatalf("%s terminal = %s, want %s", providerSuffix, payload.Phase, want)
			}
			ids = append(ids, payload.ToolEventID)
		}
	}
	if len(ids) != 1 {
		t.Fatalf("%s terminal count = %d", providerSuffix, len(ids))
	}
}

func assertComputerFailureSummary(
	t *testing.T,
	events []controlplane.Event,
	providerSuffix string,
	code string,
	message string,
) {
	t.Helper()
	for _, event := range events {
		var payload controlplane.ComputerEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if strings.HasPrefix(
			payload.ProviderCallID,
			"provider-"+providerSuffix+"-",
		) && payload.Phase.Terminal() {
			if payload.Result == nil || payload.Result.ErrorCode != code ||
				payload.Result.Error != message {
				t.Fatalf("%s failure summary = %+v", providerSuffix, payload.Result)
			}
			return
		}
	}
	t.Fatalf("%s terminal failure was not found", providerSuffix)
}
