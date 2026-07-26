package adapters

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestJournalSteeringResolverRejectsStaleContradictoryAndCrossSessionTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	journal, err := controlplane.OpenJournal(
		ctx,
		filepath.Join(t.TempDir(), "controlplane.db"),
		types.SystemClock{},
		controlplane.JournalConfig{Retention: 64, SubscriberBuffer: 4},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	actorID, sessionID, turnID, taskID, toolID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	requested := appendSteeringToolEvent(
		t, ctx, journal, actorID, sessionID, turnID, taskID, toolID,
		controlplane.ComputerRequested,
	)
	started := appendSteeringToolEvent(
		t, ctx, journal, actorID, sessionID, turnID, taskID, toolID,
		controlplane.ComputerStarted,
	)
	resolver := NewJournalSteeringResolver(journal)
	target := SteerTarget{
		Kind: SteerTargetTool, TaskID: taskID, AgentID: "specialist",
		ToolEventID: &toolID, ToolAction: "browser_navigate",
		TargetRevision: requested.Sequence,
	}
	if err := resolver.Resolve(
		ctx, actorID, sessionID, turnID, target,
	); !hasPublicCode(err, controlplane.ErrorConflict) {
		t.Fatalf("stale target error = %v", err)
	}
	target.TargetRevision = started.Sequence
	if err := resolver.Resolve(ctx, actorID, sessionID, turnID, target); err != nil {
		t.Fatalf("current target error = %v", err)
	}
	target.AgentID = "other-agent"
	if err := resolver.Resolve(
		ctx, actorID, sessionID, turnID, target,
	); !hasPublicCode(err, controlplane.ErrorConflict) {
		t.Fatalf("contradictory target error = %v", err)
	}
	target.AgentID = "specialist"
	if err := resolver.Resolve(
		ctx, actorID, uuid.New(), turnID, target,
	); !hasPublicCode(err, controlplane.ErrorNotFound) {
		t.Fatalf("cross-session target error = %v", err)
	}
	completed := appendSteeringToolEvent(
		t, ctx, journal, actorID, sessionID, turnID, taskID, toolID,
		controlplane.ComputerCompleted,
	)
	target.TargetRevision = completed.Sequence
	if err := resolver.Resolve(
		ctx, actorID, sessionID, turnID, target,
	); !hasPublicCode(err, controlplane.ErrorConflict) {
		t.Fatalf("terminal target error = %v", err)
	}
}

func appendSteeringToolEvent(
	t *testing.T,
	ctx context.Context,
	journal *controlplane.Journal,
	actorID, sessionID, turnID, taskID, toolID uuid.UUID,
	phase controlplane.ComputerPhase,
) controlplane.Event {
	t.Helper()
	payload := controlplane.ComputerEventPayload{
		ProtocolVersion: controlplane.ComputerEventVersion,
		ToolEventID:     toolID, ProviderCallID: "provider-call",
		Tool: "browser_navigate", Operation: "browser_navigate",
		Scope: controlplane.ComputerScope{
			ActorID: actorID, SessionID: &sessionID, TurnID: &turnID,
			TaskID: &taskID, OutcomeID: &turnID, AgentID: "specialist",
		},
		RiskClass: "YELLOW", Phase: phase, Timestamp: time.Now().UTC(),
		DisplayKind: "navigation",
		SourceReferences: []controlplane.ComputerSourceReference{
			{Kind: "tool_event", ID: toolID.String()},
		},
	}
	if phase.Terminal() {
		payload.TerminalStatus = phase
		payload.Result = &controlplane.ComputerResultSummary{
			Available: true, Bytes: 16,
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	eventType, err := phase.EventType()
	if err != nil {
		t.Fatal(err)
	}
	event, err := controlplane.NewEvent(
		eventType,
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
