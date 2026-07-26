package controlplane

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type snapshotRecorder struct {
	mu    sync.Mutex
	calls int
}

func (recorder *snapshotRecorder) Snapshot(context.Context, Scope) (json.RawMessage, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.calls++
	return json.RawMessage(`{"pending_approvals":[{"id":"one"}],"access_token":"never"}`), nil
}

func TestDispatcherIdempotencyRevisionRedactionAndAudit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controlplane.db")
	clock := journalClock{at: time.Unix(4_000, 0).UTC()}
	journal, err := OpenJournal(ctx, path, clock, JournalConfig{Retention: 32})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	snapshots := &snapshotRecorder{}
	dispatcher, err := NewDispatcher(journal, clock, snapshots, nil)
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	sessionID := uuid.New()
	handlerCalls := 0
	if err := dispatcher.Register(
		OperationSessionCreate,
		"Create one encrypted session.",
		HandlerFunc(func(
			ctx context.Context,
			request Request,
			emitter EventEmitter,
		) (json.RawMessage, error) {
			handlerCalls++
			if _, emitErr := emitter.Emit(
				ctx,
				EventTurnStarted,
				Correlation{ActorID: request.Scope.ActorID, SessionID: &sessionID},
				json.RawMessage(`{"credential":"hidden","state":"started"}`),
			); emitErr != nil {
				return nil, emitErr
			}
			return json.RawMessage(`{"session_id":"` + sessionID.String() +
				`","refresh_token":"hidden"}`), nil
		}),
	); err != nil {
		t.Fatal(err)
	}

	request := validCommand(actorID, OperationSessionCreate, "create-one", `{}`)
	response := dispatcher.Dispatch(ctx, actorID, request)
	if response.Error != nil || response.Revision != 1 {
		t.Fatalf("first response = %+v", response)
	}
	if handlerCalls != 1 || string(response.Result) !=
		`{"refresh_token":"[REDACTED]","session_id":"`+sessionID.String()+`"}` {
		t.Fatalf("first result=%s calls=%d", response.Result, handlerCalls)
	}

	retry := request
	retry.RequestID = uuid.New()
	cached := dispatcher.Dispatch(ctx, actorID, retry)
	if cached.Error != nil || cached.RequestID != retry.RequestID ||
		cached.Revision != 1 || handlerCalls != 1 {
		t.Fatalf("cached response=%+v calls=%d", cached, handlerCalls)
	}

	conflicting := retry
	conflicting.RequestID = uuid.New()
	conflicting.Payload = json.RawMessage(`{"different":true}`)
	conflict := dispatcher.Dispatch(ctx, actorID, conflicting)
	if conflict.Error == nil || conflict.Error.Code != ErrorConflict || handlerCalls != 1 {
		t.Fatalf("idempotency conflict=%+v calls=%d", conflict, handlerCalls)
	}

	staleRevision := uint64(0)
	stale := validCommand(actorID, OperationSessionCreate, "create-two", `{}`)
	stale.ExpectedRevision = &staleRevision
	staleResponse := dispatcher.Dispatch(ctx, actorID, stale)
	if staleResponse.Error == nil || staleResponse.Error.Code != ErrorConflict ||
		staleResponse.Revision != 1 || handlerCalls != 1 {
		t.Fatalf("stale response=%+v calls=%d", staleResponse, handlerCalls)
	}

	replay, err := journal.Replay(ctx, 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Events) != 2 ||
		replay.Events[0].Type != EventTurnStarted ||
		replay.Events[1].Type != EventPolicyDecision {
		t.Fatalf("durable command events = %+v", replay.Events)
	}
	if string(replay.Events[0].Payload) !=
		`{"credential":"[REDACTED]","state":"started"}` {
		t.Fatalf("event redaction = %s", replay.Events[0].Payload)
	}

	restarted, err := NewDispatcher(journal, clock, snapshots, nil)
	if err != nil {
		t.Fatal(err)
	}
	restartCalls := 0
	if err := restarted.Register(
		OperationSessionCreate,
		"Create one encrypted session.",
		HandlerFunc(func(context.Context, Request, EventEmitter) (json.RawMessage, error) {
			restartCalls++
			return json.RawMessage(`{}`), nil
		}),
	); err != nil {
		t.Fatal(err)
	}
	retry.RequestID = uuid.New()
	afterRestart := restarted.Dispatch(ctx, actorID, retry)
	if afterRestart.Error != nil || afterRestart.Revision != 1 || restartCalls != 0 {
		t.Fatalf("restart idempotency=%+v calls=%d", afterRestart, restartCalls)
	}
}

func TestDispatcherRecoveryReturnsSnapshotAndExplicitGap(t *testing.T) {
	ctx := context.Background()
	clock := journalClock{at: time.Unix(5_000, 0).UTC()}
	journal, err := OpenJournal(ctx, ":memory:", clock, JournalConfig{Retention: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	snapshots := &snapshotRecorder{}
	dispatcher, err := NewDispatcher(journal, clock, snapshots, nil)
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	for index := 0; index < 4; index++ {
		if _, err := journal.Append(ctx, newJournalEvent(t, actorID, index)); err != nil {
			t.Fatal(err)
		}
	}
	request := Request{
		ProtocolVersion: ProtocolVersion, RequestID: uuid.New(),
		Kind: KindQuery, Operation: OperationEventsReplay,
		Scope:   Scope{ActorID: actorID},
		Payload: json.RawMessage(`{"after_sequence":0,"limit":20}`),
	}
	response := dispatcher.Dispatch(ctx, actorID, request)
	if response.Error != nil {
		t.Fatalf("recovery response = %+v", response)
	}
	var recovery Recovery
	if err := json.Unmarshal(response.Result, &recovery); err != nil {
		t.Fatal(err)
	}
	if !recovery.Replay.Gap || recovery.GapMarker == nil ||
		recovery.GapMarker.AvailableFrom != 3 ||
		len(recovery.Snapshot) == 0 || snapshots.calls != 1 {
		t.Fatalf("gap recovery = %+v snapshot calls=%d", recovery, snapshots.calls)
	}
	if string(recovery.Snapshot) !=
		`{"access_token":"[REDACTED]","pending_approvals":[{"id":"one"}]}` {
		t.Fatalf("snapshot redaction = %s", recovery.Snapshot)
	}
}

func TestDispatcherRejectsActorScopeAndCatalogReportsAvailability(t *testing.T) {
	ctx := context.Background()
	clock := journalClock{at: time.Unix(6_000, 0).UTC()}
	journal, err := OpenJournal(ctx, ":memory:", clock, JournalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	dispatcher, err := NewDispatcher(
		journal,
		clock,
		SnapshotFunc(func(context.Context, Scope) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	request := Request{
		ProtocolVersion: ProtocolVersion, RequestID: uuid.New(),
		Kind: KindQuery, Operation: OperationCommandsCatalog,
		Scope: Scope{ActorID: actorID}, Payload: json.RawMessage(`{}`),
	}
	denied := dispatcher.Dispatch(ctx, uuid.New(), request)
	if denied.Error == nil || denied.Error.Code != ErrorUnauthorized {
		t.Fatalf("cross-actor response = %+v", denied)
	}
	response := dispatcher.Dispatch(ctx, actorID, request)
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	var catalog []CommandDescriptor
	if err := json.Unmarshal(response.Result, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != len(Operations()) {
		t.Fatalf("catalog length = %d", len(catalog))
	}
	availability := make(map[Operation]bool, len(catalog))
	for _, descriptor := range catalog {
		availability[descriptor.Operation] = descriptor.Available
	}
	if !availability[OperationSystemSnapshot] ||
		!availability[OperationEventsReplay] ||
		availability[OperationTurnSubmit] {
		t.Fatalf("catalog availability = %+v", availability)
	}
}

func validCommand(actorID uuid.UUID, operation Operation, key string, payload string) Request {
	return Request{
		ProtocolVersion: ProtocolVersion, RequestID: uuid.New(),
		Kind: KindCommand, Operation: operation, Scope: Scope{ActorID: actorID},
		IdempotencyKey: key, Payload: json.RawMessage(payload),
	}
}
