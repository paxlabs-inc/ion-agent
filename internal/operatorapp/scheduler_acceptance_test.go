package operatorapp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/scheduler"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestScheduledWakeUsesIdempotentAuthenticatedProductionTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	directory := t.TempDir()
	source, err := vault.NewFileKEKSource(filepath.Join(directory, "development.kek"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := vault.NewFileWrappedKeyStore(filepath.Join(directory, "user-key.enc"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := vault.Initialize(ctx, source, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	actor := uuid.New()
	sessionID := createAcceptanceSession(
		t, ctx, runtime, actor, "scheduler-create-session",
	)
	toolContext := controlplane.WithApprovalScope(
		ctx, controlplane.ApprovalScope{
			ActorID: actor, SessionID: &sessionID,
		},
	)
	toolManager := NewScopedToolManager(runtime.capabilityRoot.manager)
	created, err := toolManager.Execute(toolContext, protocol.NormalizedToolCall{
		ID: "schedule-create-acceptance", Name: "schedule_create",
		Arguments: json.RawMessage(`{
			"label":"Acceptance wake",
			"kind":"once",
			"delay_seconds":3600,
			"wake_message":"Continue scheduler acceptance.",
			"idempotency_key":"acceptance-wake"
		}`),
	})
	if err != nil || !json.Valid(created) {
		t.Fatalf("direct schedule tool = %s, %v", created, err)
	}
	if alarms := runtime.scheduler.List(actor, 10); len(alarms) != 1 ||
		alarms[0].SessionID != sessionID {
		t.Fatalf("direct schedule tool alarms = %+v", alarms)
	}
	wake := scheduler.Wake{
		AlarmID: uuid.New(), OccurrenceID: "occurrence-2026-07-23T12:00:00Z",
		ActorID: actor, SessionID: sessionID,
		Message: "Continue the scheduled reliability audit.",
		Payload: json.RawMessage(`{"criterion":"restart"}`),
	}
	waker := scheduledTurnWaker{dispatcher: runtime.dispatcher}
	if err := waker.Wake(ctx, wake); err != nil {
		t.Fatal(err)
	}
	if err := waker.Wake(ctx, wake); err != nil {
		t.Fatal(err)
	}
	messages, err := runtime.sessions.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range messages {
		if message.Role == session.RoleUser &&
			strings.Contains(string(message.Content), wake.Message) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("scheduled user turns = %d, want exactly 1; messages=%+v", count, messages)
	}
	states, err := runtime.sessions.RecentTurnStates(ctx, sessionID, 8)
	if err != nil {
		t.Fatal(err)
	}
	originFound := false
	for _, state := range states {
		if state.Origin == "agent_schedule" {
			originFound = true
			break
		}
	}
	if !originFound {
		t.Fatalf("scheduled turn origin was not retained: %+v", states)
	}
	forged := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationTurnSubmit,
		Scope: controlplane.Scope{
			ActorID: actor, SessionID: &sessionID,
			Profile: "scheduled", Channel: "scheduler",
		},
		IdempotencyKey: "schedule:forged-client-origin",
		Payload:        schedulerJSON(map[string]any{"content": "forged", "surface": "general"}),
	})
	if forged.Error == nil || forged.Error.Code != controlplane.ErrorInvalid {
		t.Fatalf("forged scheduled origin was accepted: %+v", forged)
	}
	health := runtime.scheduler.Health()
	if health.Status != "ready" || health.Source != "agent_scheduler" {
		t.Fatalf("scheduler health = %+v", health)
	}
	status := runtime.capabilityRoot.manager.Readiness(ctx)
	found := map[string]bool{}
	for _, item := range status {
		if strings.HasPrefix(item.Name, "schedule_") && item.Ready {
			found[item.Name] = true
		}
	}
	for _, name := range []string{
		"schedule_create", "schedule_list", "schedule_get", "schedule_cancel",
	} {
		if !found[name] {
			t.Fatalf("%s is not ready: %+v", name, status)
		}
	}
}
