package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane/adapters"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type acceptanceClock struct {
	mu sync.Mutex
	at time.Time
}

func (clock *acceptanceClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.at
}

type approvalProvider struct {
	mu    sync.Mutex
	calls int
}

func (provider *approvalProvider) Generate(
	_ context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	if provider.calls == 1 {
		if len(request.Tools) != 1 || request.Tools[0].Name != "approval_probe" {
			return protocol.NormalizedGeneration{}, controlplane.PublicError{
				Code: controlplane.ErrorInternal, Message: "tool surface mismatch",
			}
		}
		return protocol.NormalizedGeneration{
			Content: "I need approval before applying the consequential operation.",
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "approval-call-1", Name: "approval_probe",
				Arguments: json.RawMessage(
					`{"operation":"release","access_token":"must-not-leak"}`,
				),
			}},
			FinishReason: protocol.FinishToolCalls,
			Provider:     "acceptance-boundary", Model: "acceptance",
		}, nil
	}
	return protocol.NormalizedGeneration{
		Content:      "The approved operation completed.",
		FinishReason: protocol.FinishStop,
		Provider:     "acceptance-boundary", Model: "acceptance",
	}, nil
}

func TestTask91HeadlessEncryptedTurnApprovalReconnectAndExactState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	clock := &acceptanceClock{at: time.Unix(20_000, 0).UTC()}
	userKey := bytes.Repeat([]byte{0x42}, vault.KeySize)
	cipher, err := vault.New(userKey)
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	sessionPath := filepath.Join(root, "sessions.db")
	sessionStore, err := session.Open(ctx, sessionPath, cipher, clock, 128*1024)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionStore.Close(ctx)
	journalPath := filepath.Join(root, "controlplane.db")
	journal, err := controlplane.OpenJournal(
		ctx,
		journalPath,
		clock,
		controlplane.JournalConfig{Retention: 256, SubscriberBuffer: 64},
	)
	if err != nil {
		t.Fatal(err)
	}

	var approvalBroker *controlplane.ApprovalBroker
	dispatcher, err := controlplane.NewDispatcher(
		journal,
		clock,
		controlplane.SnapshotFunc(func(
			ctx context.Context,
			scope controlplane.Scope,
		) (json.RawMessage, error) {
			pending := []controlplane.ApprovalRequest{}
			if approvalBroker != nil {
				found, pendingErr := approvalBroker.Pending(ctx, scope.ActorID)
				if pendingErr != nil {
					return nil, pendingErr
				}
				pending = found
			}
			return json.Marshal(map[string]any{
				"pending_approvals": pending,
				"in_progress_turns": []any{},
			})
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	approvalBroker, err = controlplane.NewApprovalBroker(journal, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapters.RegisterSessionHandlers(dispatcher, sessionStore); err != nil {
		t.Fatal(err)
	}
	if err := controlplane.RegisterApprovalHandler(dispatcher, approvalBroker); err != nil {
		t.Fatal(err)
	}
	auditor, err := policy.OpenFileAuditor(filepath.Join(root, "policy.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer auditor.Close()
	var effectApplied atomic.Bool
	factory := adapters.TurnRunnerFactoryFunc(func(
		sessionID uuid.UUID,
		_ adapters.TurnBinding,
	) (adapters.TurnRunner, error) {
		pipeline, pipelineErr := policy.NewDefault(clock, auditor, nil, nil)
		if pipelineErr != nil {
			return nil, pipelineErr
		}
		manager, managerErr := tools.NewManager(
			clock, tools.WithExecutionPolicy(pipeline),
		)
		if managerErr != nil {
			return nil, managerErr
		}
		if managerErr := manager.Register(ctx, tools.Registration{
			Name:        "approval_probe",
			Description: "Apply one exact operation only after durable human approval.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"operation":{"type":"string"},
					"access_token":{"type":"string"}
				},
				"required":["operation","access_token"],
				"additionalProperties":false
			}`),
			Classification: tools.ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(
				toolCtx context.Context,
				arguments json.RawMessage,
			) (json.RawMessage, error) {
				scope, ok := controlplane.ApprovalScopeFromContext(toolCtx)
				if !ok {
					return nil, controlplane.ErrUnauthorized
				}
				if _, requestErr := approvalBroker.Request(
					toolCtx,
					controlplane.ApprovalInput{
						Scope: scope, Operation: "Release production",
						Arguments:   arguments,
						Consequence: "Publishes the accepted release.",
						TTL:         time.Minute,
					},
				); requestErr != nil {
					return nil, requestErr
				}
				effectApplied.Store(true)
				return json.RawMessage(`{"release":"accepted"}`), nil
			},
		}); managerErr != nil {
			return nil, managerErr
		}
		return agent.NewLoop(
			&approvalProvider{},
			manager,
			agent.LoopConfig{
				Model: "acceptance", SessionID: sessionID.String(),
				UserID: "headless-operator",
			},
			nil,
		)
	})
	coordinator, err := adapters.NewTurnCoordinator(
		ctx, sessionStore, factory, dispatcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	if err := coordinator.RegisterHandlers(dispatcher); err != nil {
		t.Fatal(err)
	}

	actorID := uuid.New()
	create := commandRequest(
		actorID, nil, controlplane.OperationSessionCreate, "create-session", `{}`,
	)
	createResponse := dispatcher.Dispatch(ctx, actorID, create)
	if createResponse.Error != nil {
		t.Fatalf("create response = %+v", createResponse)
	}
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(createResponse.Result, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("session create returned no ID")
	}
	subscription, err := journal.SubscribeActor(ctx, actorID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	submit := commandRequest(
		actorID,
		&created.ID,
		controlplane.OperationTurnSubmit,
		"submit-turn",
		`{"content":"Apply the accepted release."}`,
	)
	submitResponse := dispatcher.Dispatch(ctx, actorID, submit)
	if submitResponse.Error != nil {
		t.Fatalf("submit response = %+v", submitResponse)
	}
	var submitted struct {
		TurnID uuid.UUID `json:"turn_id"`
		State  string    `json:"state"`
	}
	if err := json.Unmarshal(submitResponse.Result, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.TurnID == uuid.Nil || submitted.State != "running" {
		t.Fatalf("submitted turn = %+v", submitted)
	}

	requestedEvent := waitForEvent(
		t, subscription.Live, controlplane.EventApprovalRequested, submitted.TurnID,
	)
	var approval controlplane.ApprovalRequest
	if err := json.Unmarshal(requestedEvent.Payload, &approval); err != nil {
		t.Fatal(err)
	}
	if approval.State != controlplane.ApprovalPending ||
		string(approval.Arguments) !=
			`{"access_token":"[REDACTED]","operation":"release"}` ||
		effectApplied.Load() {
		t.Fatalf("pending approval = %+v effect=%t", approval, effectApplied.Load())
	}
	replayAtApproval, err := journal.ReplayActor(ctx, actorID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	recoveryAtApproval, err := dispatcher.Recover(
		ctx,
		controlplane.Scope{ActorID: actorID, SessionID: &created.ID},
		replayAtApproval,
	)
	if err != nil {
		t.Fatal(err)
	}
	var approvalSnapshot struct {
		Pending []controlplane.ApprovalRequest `json:"pending_approvals"`
	}
	if err := json.Unmarshal(recoveryAtApproval.Snapshot, &approvalSnapshot); err != nil {
		t.Fatal(err)
	}
	turnStarted := false
	turnTerminal := false
	for _, event := range recoveryAtApproval.Replay.Events {
		if event.Correlation.TurnID == nil || *event.Correlation.TurnID != submitted.TurnID {
			continue
		}
		if event.Type == controlplane.EventTurnStarted {
			turnStarted = true
		}
		if event.Type == controlplane.EventTurnCompleted ||
			event.Type == controlplane.EventTurnFailed {
			turnTerminal = true
		}
	}
	if len(approvalSnapshot.Pending) != 1 || !turnStarted || turnTerminal {
		t.Fatalf(
			"reconnect state pending=%d started=%t terminal=%t",
			len(approvalSnapshot.Pending), turnStarted, turnTerminal,
		)
	}

	respondPayload, err := json.Marshal(map[string]any{
		"approval_id": approval.ID,
		"decision":    controlplane.DecisionApprove,
	})
	if err != nil {
		t.Fatal(err)
	}
	respond := commandRequest(
		actorID,
		&created.ID,
		controlplane.OperationApprovalRespond,
		"approve-operation",
		string(respondPayload),
	)
	respondResponse := dispatcher.Dispatch(ctx, actorID, respond)
	if respondResponse.Error != nil {
		t.Fatalf("approval response = %+v", respondResponse)
	}
	completed := waitForEvent(
		t, subscription.Live, controlplane.EventTurnCompleted, submitted.TurnID,
	)
	if completed.Sequence <= requestedEvent.Sequence || !effectApplied.Load() {
		t.Fatalf("completed sequence=%d request=%d effect=%t",
			completed.Sequence, requestedEvent.Sequence, effectApplied.Load())
	}

	resume := commandRequest(
		actorID, &created.ID, controlplane.OperationSessionResume, "resume-session", `{}`,
	)
	resumeResponse := dispatcher.Dispatch(ctx, actorID, resume)
	if resumeResponse.Error != nil {
		t.Fatalf("resume response = %+v", resumeResponse)
	}
	var durable struct {
		Messages []struct {
			Role    session.Role `json:"role"`
			Content string       `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(resumeResponse.Result, &durable); err != nil {
		t.Fatal(err)
	}
	if len(durable.Messages) != 2 ||
		durable.Messages[0].Role != session.RoleUser ||
		durable.Messages[1].Role != session.RoleAssistant ||
		durable.Messages[1].Content != "The approved operation completed." {
		t.Fatalf("durable transcript = %+v", durable.Messages)
	}
	cachedApproval := respond
	cachedApproval.RequestID = uuid.New()
	cachedResponse := dispatcher.Dispatch(ctx, actorID, cachedApproval)
	if cachedResponse.Error != nil ||
		string(cachedResponse.Result) != string(respondResponse.Result) {
		t.Fatalf("idempotent approval response = %+v", cachedResponse)
	}
	pending, err := approvalBroker.Pending(ctx, actorID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending approvals after response = %+v, %v", pending, err)
	}

	beforeRestart, err := journal.ReplayActor(ctx, actorID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	for boundary := uint64(0); boundary <= beforeRestart.Latest; boundary++ {
		replay, replayErr := journal.ReplayActor(ctx, actorID, boundary, 256)
		if replayErr != nil {
			t.Fatalf("replay boundary %d: %v", boundary, replayErr)
		}
		previous := boundary
		for _, event := range replay.Events {
			if event.Sequence <= previous {
				t.Fatalf("duplicate or unordered sequence after %d: %d", boundary, event.Sequence)
			}
			previous = event.Sequence
		}
	}
	subscription.Close()
	coordinator.Close()
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := controlplane.OpenJournal(
		ctx, journalPath, clock,
		controlplane.JournalConfig{Retention: 256, SubscriberBuffer: 64},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	afterRestart, err := reopened.ReplayActor(ctx, actorID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.Latest != beforeRestart.Latest ||
		len(afterRestart.Events) != len(beforeRestart.Events) {
		t.Fatalf("restart replay before=%+v after=%+v", beforeRestart, afterRestart)
	}
	for index := range beforeRestart.Events {
		beforeJSON, _ := json.Marshal(beforeRestart.Events[index])
		afterJSON, _ := json.Marshal(afterRestart.Events[index])
		if !bytes.Equal(beforeJSON, afterJSON) {
			t.Fatalf("event %d changed across restart", index)
		}
	}
}

func commandRequest(
	actorID uuid.UUID,
	sessionID *uuid.UUID,
	operation controlplane.Operation,
	idempotencyKey string,
	payload string,
) controlplane.Request {
	return controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand, Operation: operation,
		Scope:          controlplane.Scope{ActorID: actorID, SessionID: sessionID},
		IdempotencyKey: idempotencyKey, Payload: json.RawMessage(payload),
	}
}

func waitForEvent(
	t *testing.T,
	events <-chan controlplane.Event,
	eventType controlplane.EventType,
	turnID uuid.UUID,
) controlplane.Event {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatal("event subscription closed")
			}
			if event.Type == eventType && event.Correlation.TurnID != nil &&
				*event.Correlation.TurnID == turnID {
				return event
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}
