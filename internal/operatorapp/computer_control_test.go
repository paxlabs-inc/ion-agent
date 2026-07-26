package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestComputerControlDuplicateAcquireIsExactlyOnceAndRevisionBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := types.SystemClock{}
	root := t.TempDir()
	cipher, err := vault.New(bytes.Repeat([]byte{0x72}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(
		ctx, filepath.Join(root, "sessions.db"), cipher, clock, 128<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	defer cipher.Close()
	journal, err := controlplane.OpenJournal(
		ctx,
		filepath.Join(root, "controlplane.db"),
		clock,
		controlplane.JournalConfig{Retention: 128, SubscriberBuffer: 8},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	dispatcher, err := controlplane.NewDispatcher(
		journal,
		clock,
		controlplane.SnapshotFunc(func(
			context.Context,
			controlplane.Scope,
		) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"ready"}`), nil
		}),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, err := controllease.New(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := projectcontrol.NewService(
		store,
		clock,
		projectcontrol.ServiceConfig{
			WorkspaceRoot: filepath.Join(root, "workspaces"),
			ArchiveRoot:   filepath.Join(root, "archives"),
			Control:       control,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer projects.Close()
	browser, err := nativebrowser.New(nativebrowser.Config{
		ProfileRoot: filepath.Join(root, "browser"), Control: control,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	capabilities := &productionCapabilities{
		control: control, browser: browser, projects: projects,
	}
	if err := registerComputerControlHandlers(
		dispatcher, capabilities, journal,
	); err != nil {
		t.Fatal(err)
	}
	actorID, sessionID, turnID, taskID, toolID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	staleEvent := appendComputerControlBrowserEvent(
		t, ctx, journal, actorID, sessionID, turnID, taskID, toolID,
	)
	toolID = uuid.New()
	event := appendComputerControlBrowserEvent(
		t, ctx, journal, actorID, sessionID, turnID, taskID, toolID,
	)
	if _, err := browserOwnerFromJournal(
		ctx,
		journal,
		controlplane.Scope{ActorID: actorID, SessionID: &sessionID},
		staleEvent.Sequence,
	); !publicErrorCode(err, controlplane.ErrorConflict) {
		t.Fatalf("stale browser owner error = %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"resource_kind":   "browser",
		"resource_id":     sessionID,
		"target_revision": event.Sequence,
		"owner": map[string]any{
			"turn_id": turnID, "task_id": taskID,
			"agent_id": "specialist", "tool_event_id": toolID,
			"action": "browser_navigate", "revision": event.Sequence,
		},
		"expected_lease_revision": 0,
		"ttl_seconds":             90,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation:      controlplane.OperationComputerControlAcquire,
		Scope:          controlplane.Scope{ActorID: actorID, SessionID: &sessionID},
		IdempotencyKey: "acquire-browser-once", Payload: payload,
	}
	first := dispatcher.Dispatch(ctx, actorID, request)
	if first.Error != nil {
		t.Fatalf("first acquire error = %+v", first.Error)
	}
	request.RequestID = uuid.New()
	duplicate := dispatcher.Dispatch(ctx, actorID, request)
	if duplicate.Error != nil {
		t.Fatalf("duplicate acquire error = %+v", duplicate.Error)
	}
	var firstLease, duplicateLease controllease.Lease
	if json.Unmarshal(first.Result, &firstLease) != nil ||
		json.Unmarshal(duplicate.Result, &duplicateLease) != nil {
		t.Fatal("decode lease result")
	}
	if firstLease.ID == uuid.Nil || duplicateLease.ID != firstLease.ID ||
		duplicateLease.Revision != firstLease.Revision {
		t.Fatalf("first=%+v duplicate=%+v", firstLease, duplicateLease)
	}
	status, err := control.Status(
		ctx, nativebrowser.ControlTarget(actorID, &sessionID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if status.ID != firstLease.ID || status.Revision != 1 {
		t.Fatalf("durable status after duplicate = %+v", status)
	}
}

func publicErrorCode(err error, code controlplane.ErrorCode) bool {
	var public controlplane.PublicError
	return errors.As(err, &public) && public.Code == code
}

func appendComputerControlBrowserEvent(
	t *testing.T,
	ctx context.Context,
	journal *controlplane.Journal,
	actorID, sessionID, turnID, taskID, toolID uuid.UUID,
) controlplane.Event {
	t.Helper()
	payload := controlplane.ComputerEventPayload{
		ProtocolVersion: controlplane.ComputerEventVersion,
		ToolEventID:     toolID, ProviderCallID: "provider-browser-call",
		Tool: "browser_navigate", Operation: "browser_navigate",
		Scope: controlplane.ComputerScope{
			ActorID: actorID, SessionID: &sessionID, TurnID: &turnID,
			TaskID: &taskID, OutcomeID: &turnID, AgentID: "specialist",
		},
		RiskClass: "YELLOW", Phase: controlplane.ComputerStarted,
		Timestamp: time.Now().UTC(), DisplayKind: "navigation",
		SourceReferences: []controlplane.ComputerSourceReference{
			{Kind: "tool_event", ID: toolID.String()},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	event, err := controlplane.NewEvent(
		controlplane.EventToolStarted,
		controlplane.Correlation{
			ActorID: actorID, SessionID: &sessionID, TurnID: &turnID,
			TaskID: &taskID, ToolID: &toolID,
		},
		raw,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	event, err = journal.Append(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
