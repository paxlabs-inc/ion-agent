package controlplane

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProtocolCatalogAndEnvelopeValidation(t *testing.T) {
	operations := Operations()
	if len(operations) != 202 {
		t.Fatalf("operation count = %d, want 202", len(operations))
	}
	for index := 1; index < len(operations); index++ {
		if operations[index-1] >= operations[index] {
			t.Fatalf("operation catalog is not stable: %q then %q",
				operations[index-1], operations[index])
		}
	}
	if len(EventTypes()) != 64 {
		t.Fatalf("event type count = %d, want 64", len(EventTypes()))
	}

	actorID := uuid.New()
	request := Request{
		ProtocolVersion: ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            KindCommand,
		Operation:       OperationSessionCreate,
		Scope:           Scope{ActorID: actorID},
		IdempotencyKey:  "create-one",
		Payload:         json.RawMessage(`{"title":"acceptance"}`),
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	request.Kind = KindQuery
	if err := request.Validate(); err == nil {
		t.Fatal("mismatched operation kind accepted")
	}
	request.Kind = KindCommand
	request.IdempotencyKey = ""
	if err := request.Validate(); err == nil {
		t.Fatal("command without idempotency key accepted")
	}
	request.IdempotencyKey = "create-one"
	request.ProtocolVersion = "future"
	if err := request.Validate(); err == nil {
		t.Fatal("unsupported protocol accepted")
	}

	event, err := NewEvent(
		EventTurnStarted,
		Correlation{ActorID: actorID},
		json.RawMessage(`{"redacted":true}`),
		time.Unix(100, 5).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.EventID == uuid.Nil || event.Sequence != 0 {
		t.Fatalf("new event identity/sequence = %s/%d", event.EventID, event.Sequence)
	}
}

func TestComputerEventContractRejectsMismatchedLifecycle(t *testing.T) {
	t.Parallel()
	actorID, turnID, toolEventID := uuid.New(), uuid.New(), uuid.New()
	payload := ComputerEventPayload{
		ProtocolVersion: ComputerEventVersion,
		ToolEventID:     toolEventID,
		ProviderCallID:  "provider-call-1",
		Tool:            "filesystem_read",
		Operation:       "filesystem_read",
		Scope: ComputerScope{
			ActorID: actorID, TurnID: &turnID, OutcomeID: &turnID,
			AgentID: "ion",
		},
		RiskClass:   "GREEN",
		Phase:       ComputerCompleted,
		Timestamp:   time.Unix(100, 0).UTC(),
		DisplayKind: "repository",
		SourceReferences: []ComputerSourceReference{{
			Kind: "tool_event", ID: toolEventID.String(),
		}},
		TerminalStatus: ComputerCompleted,
		Result: &ComputerResultSummary{
			Available: true, Bytes: 42,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	event, err := NewEvent(
		EventToolCompleted,
		Correlation{
			ActorID: actorID, TurnID: &turnID, ToolID: &toolEventID,
		},
		encoded,
		payload.Timestamp,
	)
	if err != nil || event.EventID == uuid.Nil {
		t.Fatalf("valid computer event = %+v, %v", event, err)
	}
	payload.TerminalStatus = ComputerFailed
	encoded, _ = json.Marshal(payload)
	if _, err := NewEvent(
		EventToolCompleted,
		Correlation{
			ActorID: actorID, TurnID: &turnID, ToolID: &toolEventID,
		},
		encoded,
		payload.Timestamp,
	); err == nil {
		t.Fatal("mismatched terminal status accepted")
	}
	payload.TerminalStatus = ComputerCompleted
	encoded, _ = json.Marshal(payload)
	wrongTool := uuid.New()
	if _, err := NewEvent(
		EventToolCompleted,
		Correlation{
			ActorID: actorID, TurnID: &turnID, ToolID: &wrongTool,
		},
		encoded,
		payload.Timestamp,
	); err == nil {
		t.Fatal("mismatched ToolEvent correlation accepted")
	}
}

func TestGeneratedTypeScriptIsDeterministicAndCheckedIn(t *testing.T) {
	var first bytes.Buffer
	var second bytes.Buffer
	if err := GenerateTypeScript(&first); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTypeScript(&second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("TypeScript generation is not deterministic")
	}
	checkedIn, err := os.ReadFile(filepath.Join(
		"..", "..", "ui", "shared", "src", "generated", "protocol.ts",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), checkedIn) {
		t.Fatal("checked-in TypeScript contract drifted; run go generate ./internal/controlplane")
	}
}
