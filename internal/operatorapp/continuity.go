package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

type returnBrief struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Period      string               `json:"period"`
	Since       time.Time            `json:"since"`
	Until       time.Time            `json:"until"`
	Status      string               `json:"status"`
	Sections    []returnBriefSection `json:"sections"`
	Sources     []string             `json:"sources"`
	Gap         bool                 `json:"retention_gap"`
}

type returnBriefSection struct {
	Kind  string            `json:"kind"`
	Label string            `json:"label"`
	Items []returnBriefItem `json:"items"`
}

type returnBriefItem struct {
	Summary    string    `json:"summary"`
	Status     string    `json:"status"`
	EvidenceID string    `json:"evidence_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (supervisor *presenceSupervisor) ReturnBrief(
	ctx context.Context,
	actorID uuid.UUID,
	period string,
) (returnBrief, error) {
	duration, normalized, err := returnBriefPeriod(period)
	if err != nil {
		return returnBrief{}, err
	}
	now := supervisor.clock.Now().UTC()
	since := now.Add(-duration)
	events, gap, err := supervisor.returnBriefEvents(ctx, actorID)
	if err != nil {
		return returnBrief{}, err
	}
	grouped := make(map[string][]returnBriefItem)
	pending := make(map[uuid.UUID]returnBriefItem)
	for _, event := range events {
		if event.OccurredAt.Before(since) || event.OccurredAt.After(now) {
			continue
		}
		item := returnBriefEventItem(event)
		if event.Type == controlplane.EventApprovalRequested {
			var approval controlplane.ApprovalRequest
			if json.Unmarshal(event.Payload, &approval) == nil && approval.ID != uuid.Nil {
				pending[approval.ID] = returnBriefItem{
					Summary: "Decision waiting for " + safeBriefLabel(approval.Operation),
					Status:  "waiting", EvidenceID: event.EventID.String(),
					OccurredAt: event.OccurredAt,
				}
			}
		}
		if event.Type == controlplane.EventApprovalResolved ||
			event.Type == controlplane.EventApprovalExpired {
			var approval controlplane.ApprovalRequest
			if json.Unmarshal(event.Payload, &approval) == nil {
				delete(pending, approval.ID)
			}
		}
		if item.Summary != "" {
			grouped[item.Status] = append(grouped[item.Status], item)
		}
		if changed, ok := changedFileItem(event); ok {
			grouped["changed_files"] = append(grouped["changed_files"], changed)
		}
	}
	for _, item := range pending {
		grouped["pending_questions"] = append(grouped["pending_questions"], item)
	}
	deadlines, err := supervisor.deadlineCount(ctx, actorID, now)
	if err != nil {
		return returnBrief{}, err
	}
	if deadlines > 0 {
		grouped["deadlines"] = append(grouped["deadlines"], returnBriefItem{
			Summary: fmt.Sprintf("%d explicit deadline(s) are approaching", deadlines),
			Status:  "deadlines", EvidenceID: "temporal-deadlines",
			OccurredAt: now,
		})
	}
	order := []struct {
		Kind  string
		Label string
	}{
		{"completed_work", "Completed work"},
		{"failures", "Failures"},
		{"decisions", "Decisions"},
		{"changed_files", "Changed files"},
		{"pending_questions", "Pending questions"},
		{"discoveries", "Discoveries"},
		{"repairs", "Repairs"},
		{"proposals", "Proposals"},
		{"incomplete_work", "Incomplete work"},
		{"deadlines", "Deadlines"},
	}
	sections := make([]returnBriefSection, 0, len(order))
	for _, definition := range order {
		items := grouped[definition.Kind]
		if len(items) == 0 {
			continue
		}
		sort.SliceStable(items, func(left, right int) bool {
			return items[left].OccurredAt.After(items[right].OccurredAt)
		})
		if len(items) > 50 {
			items = items[:50]
		}
		sections = append(sections, returnBriefSection{
			Kind: definition.Kind, Label: definition.Label, Items: items,
		})
	}
	status := "ready"
	if len(sections) == 0 {
		status = "no_activity"
	}
	if gap {
		status = "partial"
	}
	return returnBrief{
		GeneratedAt: now, Period: normalized, Since: since, Until: now,
		Status: status, Sections: sections, Gap: gap,
		Sources: []string{
			"actor-scoped control-plane events",
			"explicit temporal deadlines",
		},
	}, nil
}

func (supervisor *presenceSupervisor) returnBriefEvents(
	ctx context.Context,
	actorID uuid.UUID,
) ([]controlplane.Event, bool, error) {
	after := uint64(0)
	found := make([]controlplane.Event, 0)
	gap := false
	for pages := 0; pages < 10; pages++ {
		replay, err := supervisor.journal.ReplayActor(ctx, actorID, after, 2000)
		if err != nil {
			return nil, false, err
		}
		gap = gap || replay.Gap
		found = append(found, replay.Events...)
		if replay.Latest >= replay.Head || replay.Latest <= after {
			return found, gap, nil
		}
		after = replay.Latest
	}
	return found, true, nil
}

func returnBriefPeriod(period string) (time.Duration, string, error) {
	switch strings.ToLower(strings.TrimSpace(period)) {
	case "", "24h", "day":
		return 24 * time.Hour, "24h", nil
	case "7d", "week":
		return 7 * 24 * time.Hour, "7d", nil
	case "30d", "month":
		return 30 * 24 * time.Hour, "30d", nil
	default:
		return 0, "", errors.New("return brief: period must be 24h, 7d, or 30d")
	}
}

func returnBriefEventItem(event controlplane.Event) returnBriefItem {
	summary, status := "", ""
	switch event.Type {
	case controlplane.EventTurnCompleted:
		summary, status = "A task completed with a durable result", "completed_work"
	case controlplane.EventAgentCompleted:
		summary, status = "Delegated work completed", "completed_work"
	case controlplane.EventAutomatrixCompleted:
		summary, status = "Approved background work completed", "completed_work"
	case controlplane.EventTurnFailed:
		summary, status = "A task failed", "failures"
	case controlplane.EventToolFailed:
		summary, status = "An action failed", "failures"
	case controlplane.EventWorkspaceFailed:
		summary, status = "A workspace operation failed", "failures"
	case controlplane.EventApprovalRequested:
		summary, status = "A decision was requested", "decisions"
	case controlplane.EventApprovalResolved:
		summary, status = "A decision was resolved", "decisions"
	case controlplane.EventApprovalExpired:
		summary, status = "A decision expired", "decisions"
	case controlplane.EventPredictionMismatched:
		summary, status = "Evidence contradicted an expectation", "discoveries"
	case controlplane.EventCuriosityTargeted:
		summary, status = "A bounded question was identified for investigation", "discoveries"
	case controlplane.EventDreamweaverDerived:
		summary, status = "A possible connection was derived for review", "discoveries"
	case controlplane.EventRepairLearned:
		summary, status = "A failure repair was recorded for future work", "repairs"
	case controlplane.EventAutomatrixQueued:
		summary, status = "Background work was proposed for approval", "proposals"
	case controlplane.EventTurnIncomplete:
		summary, status = "A task remains incomplete", "incomplete_work"
	case controlplane.EventToolInterrupted:
		summary, status = "An action was interrupted", "incomplete_work"
	case controlplane.EventToolOutcomeUnknown:
		summary, status = "An action has an uncertain outcome", "incomplete_work"
	case controlplane.EventWorkspaceCancelled:
		summary, status = "A workspace operation was cancelled", "incomplete_work"
	}
	return returnBriefItem{
		Summary: summary, Status: status, EvidenceID: event.EventID.String(),
		OccurredAt: event.OccurredAt,
	}
}

func changedFileItem(event controlplane.Event) (returnBriefItem, bool) {
	if event.Type != controlplane.EventToolCompleted {
		return returnBriefItem{}, false
	}
	var payload controlplane.ComputerEventPayload
	if json.Unmarshal(event.Payload, &payload) != nil || payload.Validate() != nil {
		return returnBriefItem{}, false
	}
	operation := strings.ToLower(payload.Operation)
	if !strings.Contains(operation, "write") &&
		!strings.Contains(operation, "patch") &&
		!strings.Contains(operation, "edit") &&
		!strings.Contains(operation, "apply") {
		return returnBriefItem{}, false
	}
	var model controlplane.DisplayModel
	if json.Unmarshal(payload.DisplayModel, &model) != nil ||
		model.Validate(len(payload.SourceReferences)) != nil ||
		(model.Kind != controlplane.DisplayCode && model.Kind != controlplane.DisplayDiff) ||
		model.Title.Format != controlplane.DisplayPath ||
		(model.Title.Truth != controlplane.DisplayObserved &&
			model.Title.Truth != controlplane.DisplayGenerated) {
		return returnBriefItem{}, false
	}
	return returnBriefItem{
		Summary: model.Title.Value, Status: "changed_files",
		EvidenceID: event.EventID.String(), OccurredAt: event.OccurredAt,
	}, true
}

func safeBriefLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 120 {
		return "a consequential action"
	}
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return "a consequential action"
		}
	}
	return value
}
