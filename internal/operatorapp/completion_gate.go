package operatorapp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

// studioCompletionGate keeps an accepted Studio outcome alive independently
// of any one model response. It relies only on encrypted contract state and
// server-verified evidence, never on the model's completion claim.
type studioCompletionGate struct {
	work      *workcontrol.Service
	actorID   uuid.UUID
	sessionID uuid.UUID
}

func (gate studioCompletionGate) CheckCompletion(
	ctx context.Context,
) (agent.CompletionDecision, error) {
	if gate.work == nil || gate.actorID == uuid.Nil || gate.sessionID == uuid.Nil {
		return agent.CompletionDecision{}, fmt.Errorf("operator completion gate: bound work scope is required")
	}
	portfolio, err := gate.work.Get(ctx, gate.actorID)
	if err != nil {
		return agent.CompletionDecision{}, err
	}
	contracts := append([]workcontrol.OutcomeContract(nil), portfolio.Contracts...)
	sort.Slice(contracts, func(left, right int) bool {
		return contracts[left].UpdatedAt.After(contracts[right].UpdatedAt)
	})
	var contract *workcontrol.OutcomeContract
	for index := range contracts {
		if contracts[index].SessionID != nil && *contracts[index].SessionID == gate.sessionID {
			contract = &contracts[index]
			break
		}
	}
	// Ordinary Studio questions do not become long-running jobs merely because
	// the completion gate is installed. The gate activates when a contract does.
	if contract == nil {
		return agent.CompletionDecision{Ready: true}, nil
	}
	switch contract.Status {
	case workcontrol.StatusCompleted:
		return agent.CompletionDecision{Ready: true}, nil
	case workcontrol.StatusCancelled:
		return agent.CompletionDecision{Stop: true, Reason: "the outcome was explicitly cancelled"}, nil
	case workcontrol.StatusBlocked:
		return agent.CompletionDecision{Stop: true, Reason: "the outcome contract records a required blocker"}, nil
	}
	brief, err := gate.work.Brief(ctx, gate.actorID, &gate.sessionID)
	if err != nil {
		return agent.CompletionDecision{}, err
	}
	for _, item := range brief.WorkItems {
		if item.Status == workcontrol.WorkItemBlocked {
			reason := strings.TrimSpace(item.BlockingNote)
			if reason == "" {
				reason = item.Title
			}
			return agent.CompletionDecision{Stop: true, Reason: reason}, nil
		}
	}
	if len(brief.UnverifiedCriteria) == 0 {
		return agent.CompletionDecision{
			Reason:     "all criteria have evidence but the outcome is not closed",
			NextAction: "call work_contract_complete after the final evidence review",
		}, nil
	}
	next := strings.TrimSpace(brief.NextAction)
	foundNext := false
	for _, wanted := range []workcontrol.WorkItemStatus{
		workcontrol.WorkItemRunning, workcontrol.WorkItemVerifying, workcontrol.WorkItemReady,
	} {
		for _, item := range brief.WorkItems {
			if item.Status == wanted {
				next = item.Title
				foundNext = true
				break
			}
		}
		if foundNext {
			break
		}
	}
	return agent.CompletionDecision{
		Reason:     fmt.Sprintf("%d completion criteria still lack server-verified evidence", len(brief.UnverifiedCriteria)),
		NextAction: next,
	}, nil
}
