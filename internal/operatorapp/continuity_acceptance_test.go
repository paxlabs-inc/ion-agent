package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	sessionstore "github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestReturnBriefAllowlistPeriodsPendingDecisionsAndActorIsolation(t *testing.T) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
	}
	cipher, err := vault.New(bytes.Repeat([]byte{0x75}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	store, err := sessionstore.Open(
		ctx, filepath.Join(t.TempDir(), "sessions.db"), cipher,
		clock, 128<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	journal, err := controlplane.OpenJournal(
		ctx, ":memory:", clock, controlplane.JournalConfig{Retention: 256},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	supervisor := &presenceSupervisor{clock: clock, store: store, journal: journal}
	actor, other := uuid.New(), uuid.New()
	appendBriefEvent(t, ctx, journal, actor, controlplane.EventTurnCompleted, clock.now.Add(-time.Hour), `{}`)
	appendBriefEvent(t, ctx, journal, actor, controlplane.EventTurnFailed, clock.now.Add(-2*time.Hour), `{}`)
	appendBriefEvent(t, ctx, journal, actor, controlplane.EventRepairLearned, clock.now.Add(-3*time.Hour), `{}`)
	appendBriefEvent(t, ctx, journal, actor, controlplane.EventCuriosityTargeted, clock.now.Add(-8*24*time.Hour), `{}`)
	appendBriefEvent(t, ctx, journal, other, controlplane.EventTurnCompleted, clock.now.Add(-time.Hour), `{}`)
	pending := controlplane.ApprovalRequest{
		ID: uuid.New(), ActorID: actor, Operation: "browser_submit",
		Consequence: "Submit the form", ExpiresAt: clock.now.Add(time.Hour),
		State: controlplane.ApprovalPending,
	}
	pendingPayload, _ := json.Marshal(pending)
	appendBriefEvent(
		t, ctx, journal, actor, controlplane.EventApprovalRequested,
		clock.now.Add(-30*time.Minute), string(pendingPayload),
	)
	resolved := pending
	resolved.ID = uuid.New()
	resolved.State = controlplane.ApprovalResolved
	resolvedPayload, _ := json.Marshal(resolved)
	appendBriefEvent(
		t, ctx, journal, actor, controlplane.EventApprovalRequested,
		clock.now.Add(-25*time.Minute), string(resolvedPayload),
	)
	appendBriefEvent(
		t, ctx, journal, actor, controlplane.EventApprovalResolved,
		clock.now.Add(-20*time.Minute), string(resolvedPayload),
	)

	brief, err := supervisor.ReturnBrief(ctx, actor, "24h")
	if err != nil {
		t.Fatal(err)
	}
	if brief.Status != "ready" ||
		briefSectionCount(brief, "completed_work") != 1 ||
		briefSectionCount(brief, "failures") != 1 ||
		briefSectionCount(brief, "repairs") != 1 ||
		briefSectionCount(brief, "discoveries") != 0 ||
		briefSectionCount(brief, "pending_questions") != 1 {
		t.Fatalf("24 hour brief = %+v", brief)
	}
	week, err := supervisor.ReturnBrief(ctx, actor, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if briefSectionCount(week, "discoveries") != 0 {
		t.Fatalf("week brief included event older than seven days: %+v", week)
	}
	month, err := supervisor.ReturnBrief(ctx, actor, "30d")
	if err != nil || briefSectionCount(month, "discoveries") != 1 {
		t.Fatalf("month brief = %+v, %v", month, err)
	}
	isolated, err := supervisor.ReturnBrief(ctx, uuid.New(), "24h")
	if err != nil || isolated.Status != "no_activity" || len(isolated.Sections) != 0 {
		t.Fatalf("isolated brief = %+v, %v", isolated, err)
	}
}

func TestReturnBriefIncludesOnlyValidatedCompletedFileMutations(t *testing.T) {
	actor, toolID, taskID := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	model := controlplane.DisplayModel{
		ProtocolVersion: controlplane.DisplayModelVersion,
		Kind:            controlplane.DisplayDiff,
		Title: controlplane.DisplayDatum{
			Value: "internal/service.go", Truth: controlplane.DisplayObserved,
			Format: controlplane.DisplayPath, Sources: []int{0},
		},
	}
	modelPayload, _ := json.Marshal(model)
	payload := controlplane.ComputerEventPayload{
		ProtocolVersion: controlplane.ComputerEventVersion,
		ToolEventID:     toolID, ProviderCallID: "call-one",
		Tool: "file_patch", Operation: "file_patch_apply",
		Scope: controlplane.ComputerScope{
			ActorID: actor, TaskID: &taskID, AgentID: "ion",
		},
		RiskClass: "YELLOW", Phase: controlplane.ComputerCompleted,
		Timestamp: now, DisplayKind: "diff",
		SourceReferences: []controlplane.ComputerSourceReference{{
			Kind: "tool_event", ID: toolID.String(),
		}},
		TerminalStatus: controlplane.ComputerCompleted,
		Result:         &controlplane.ComputerResultSummary{Available: true, Bytes: 32},
		DisplayModel:   modelPayload,
	}
	raw, _ := json.Marshal(payload)
	event, err := controlplane.NewEvent(
		controlplane.EventToolCompleted,
		controlplane.Correlation{
			ActorID: actor, TaskID: &taskID, ToolID: &toolID,
		},
		raw, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := changedFileItem(event)
	if !ok || item.Summary != "internal/service.go" {
		t.Fatalf("changed file item = %+v, %v", item, ok)
	}
	payload.Operation = "file_read"
	raw, _ = json.Marshal(payload)
	event, err = controlplane.NewEvent(
		controlplane.EventToolCompleted,
		controlplane.Correlation{
			ActorID: actor, TaskID: &taskID, ToolID: &toolID,
		},
		raw, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := changedFileItem(event); ok {
		t.Fatal("read-only file event was reported as a change")
	}
}

func appendBriefEvent(
	t *testing.T,
	ctx context.Context,
	journal *controlplane.Journal,
	actor uuid.UUID,
	eventType controlplane.EventType,
	at time.Time,
	payload string,
) {
	t.Helper()
	event, err := controlplane.NewEvent(
		eventType, controlplane.Correlation{ActorID: actor},
		json.RawMessage(payload), at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Append(ctx, event); err != nil {
		t.Fatal(err)
	}
}

func briefSectionCount(brief returnBrief, kind string) int {
	for _, section := range brief.Sections {
		if section.Kind == kind {
			return len(section.Items)
		}
	}
	return 0
}

var _ types.Clock = (*acceptanceClock)(nil)
