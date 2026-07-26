package operatorapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/swarm"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

type liveSupervisorExecutor struct {
	generator agent.Generator
	model     string
	manager   *tools.Manager
	registry  *swarm.Registry
}

func (executor *liveSupervisorExecutor) Execute(
	ctx context.Context,
	packet workcontrol.TaskPacket,
	attemptID uuid.UUID,
) (workcontrol.WorkerResult, error) {
	if executor == nil || executor.generator == nil || executor.manager == nil ||
		executor.registry == nil {
		return workcontrol.WorkerResult{}, fmt.Errorf(
			"operator supervisor: live specialist executor is unavailable",
		)
	}
	surface, err := swarm.NewToolSurface(packet.Tools)
	if err != nil {
		return workcontrol.WorkerResult{}, err
	}
	reduced := newReducedToolManager(executor.manager, surface.Tools())
	sessionID := packet.SupervisorID.String()
	if packet.SessionID != nil && *packet.SessionID != uuid.Nil {
		sessionID = packet.SessionID.String()
	}
	type execution struct {
		result workcontrol.WorkerResult
		err    error
	}
	finished := make(chan execution, 1)
	startedAt := time.Now()
	agentView, err := executor.registry.SpawnWorker(
		ctx, packet.SupervisorID.String(), sessionID, 1,
		swarm.ReducedSelfModel{
			ID:           string(packet.Specialist),
			Capabilities: surface.Tools(),
			Limitations: append([]string{
				"no durable memory", "no recursive delegation",
				"no authority beyond the task packet",
			}, packet.ParentAuthority...),
			Version: 1,
		},
		surface,
		func(workerCtx context.Context, worker swarm.WorkerContext) (json.RawMessage, error) {
			workerCtx = controlplane.WithApprovalScope(
				workerCtx,
				controlplane.ApprovalScope{
					ActorID: packet.ActorID, SessionID: packet.SessionID,
					OutcomeID: &packet.SupervisorID, AgentID: worker.AgentID,
				},
			)
			childTools := NewScopedAgentToolManager(reduced, worker.AgentID)
			loop, loopErr := agent.NewLoop(
				executor.generator,
				childTools,
				agent.LoopConfig{
					Model:           executor.model,
					SystemPrompt:    supervisorSystemPrompt(packet),
					UserID:          packet.ActorID.String(),
					SessionID:       sessionID,
					MaxToolCalls:    packet.Budget.MaxToolCalls,
					MaxOutputTokens: specialistOutputTokenLimit(packet.Budget),
				},
				nil,
			)
			if loopErr != nil {
				finished <- execution{err: loopErr}
				return nil, loopErr
			}
			payload, marshalErr := json.Marshal(packet)
			if marshalErr != nil {
				finished <- execution{err: marshalErr}
				return nil, marshalErr
			}
			response, turnErr := loop.Turn(
				workerCtx,
				"Complete this accepted specialist packet and return a concise, "+
					"evidence-linked finding set:\n"+string(payload),
			)
			artifacts := verifiedSupervisorArtifacts(response, packet)
			evidence := artifacts
			if len(evidence) == 0 {
				evidence = append([]string(nil), packet.Evidence...)
			}
			result := workcontrol.WorkerResult{
				AttemptID: attemptID, WorkerID: worker.AgentID,
				Status: workcontrol.SpecialistCompleted, Progress: 100,
				Summary:   response.Content,
				Usage:     specialistExecutionUsage(response, startedAt),
				Artifacts: artifacts,
				Findings: []workcontrol.Finding{{
					Kind: string(packet.Specialist), Summary: response.Content,
					Evidence:   evidence,
					Confidence: "worker_reported_pending_parent_verification",
				}},
			}
			if turnErr != nil {
				result.Status = workcontrol.SpecialistBlocked
			}
			finished <- execution{result: result, err: turnErr}
			artifact, _ := json.Marshal(result)
			return artifact, turnErr
		},
	)
	if err != nil {
		return workcontrol.WorkerResult{}, err
	}
	if err := executor.registry.SetAssignment(agentView.ID, packet.Title); err != nil {
		executor.registry.Abort(agentView.ID, sessionID)
		return workcontrol.WorkerResult{}, err
	}
	select {
	case <-ctx.Done():
		_, _ = executor.registry.AbortScoped(
			agentView.ID, sessionID, swarm.StateRunning,
		)
		return workcontrol.WorkerResult{
			AttemptID: attemptID, WorkerID: agentView.ID,
			Status: workcontrol.SpecialistCancelled,
		}, ctx.Err()
	case outcome := <-finished:
		outcome.result.WorkerID = agentView.ID
		return outcome.result, outcome.err
	}
}

func verifiedSupervisorArtifacts(
	response agent.Response,
	packet workcontrol.TaskPacket,
) []string {
	found := make(map[string]struct{})
	var covered []string
	var result []string
	for _, event := range response.ToolEvents {
		if event.Error != "" || event.Call.Name != "artifact_verify" {
			continue
		}
		var artifact struct {
			ID              uuid.UUID  `json:"id"`
			ContractID      uuid.UUID  `json:"contract_id"`
			CriteriaCovered []string   `json:"criteria_covered"`
			SHA256          string     `json:"sha256"`
			Verification    string     `json:"verification"`
			VerifiedAt      *time.Time `json:"verified_at"`
		}
		if err := json.Unmarshal(event.Result, &artifact); err != nil ||
			artifact.ID == uuid.Nil ||
			artifact.ContractID != packet.ContractID ||
			artifact.VerifiedAt == nil ||
			artifact.SHA256 == "" ||
			artifact.Verification != "server_sha256" {
			continue
		}
		id := artifact.ID.String()
		if _, exists := found[id]; exists {
			continue
		}
		found[id] = struct{}{}
		result = append(result, id)
		covered = append(covered, artifact.CriteriaCovered...)
	}
	if !criteriaCovered(packet.Criteria, covered) {
		return nil
	}
	return result
}

func criteriaCovered(wanted, covered []string) bool {
	available := make(map[string]struct{}, len(covered))
	for _, criterion := range covered {
		available[criterion] = struct{}{}
	}
	for _, criterion := range wanted {
		if _, ok := available[criterion]; !ok {
			return false
		}
	}
	return len(wanted) > 0
}

func specialistExecutionUsage(
	response agent.Response, startedAt time.Time,
) workcontrol.BudgetUsage {
	usage := workcontrol.BudgetUsage{
		Tokens:             int64(response.Usage.TotalTokens),
		CostCents:          microcentsToCents(response.Usage.ModelCostMicrocents),
		CostKnown:          response.Usage.ModelCostKnown,
		ToolCalls:          len(response.ToolEvents),
		WallSeconds:        int(time.Since(startedAt).Round(time.Second) / time.Second),
		ProviderCents:      microcentsToCents(response.Usage.ProviderSpendMicrocents),
		ProviderSpendKnown: response.Usage.ProviderSpendKnown,
	}
	for _, event := range response.ToolEvents {
		switch event.Call.Name {
		case "shell_execute":
			usage.Processes++
		case "web_fetch", "web_search":
			usage.NetworkBytes += int64(len(event.Result))
		}
	}
	return usage
}

func microcentsToCents(value int64) int64 {
	if value <= 0 {
		return 0
	}
	const microcentsPerCent = int64(1_000_000)
	result := value / microcentsPerCent
	if value%microcentsPerCent != 0 {
		result++
	}
	return result
}

func specialistOutputTokenLimit(budget workcontrol.SpecialistBudget) int {
	calls := budget.MaxToolCalls + 1
	if calls < 1 {
		calls = 1
	}
	limit := int(budget.MaxTokens) / calls
	if limit < 1 {
		return 1
	}
	if limit > 8192 {
		return 8192
	}
	return limit
}

func supervisorSystemPrompt(packet workcontrol.TaskPacket) string {
	authority := "No external effects are authorized."
	if packet.Scope.ExternalEffects {
		authority = "External effects remain subject to the parent's exact approval boundary."
	}
	return strings.Join([]string{
		"You are one bounded Ion specialist.",
		"Work only on the supplied task packet and use only the immutable tool surface.",
		"Do not commit, publish, deploy, spend provider funds, or communicate externally unless the packet explicitly carries that parent authority.",
		authority,
		"Distinguish observations, changes, tests, limitations, and evidence. Never claim verified completion from narrative alone.",
		"Before returning completion, record and independently verify workspace artifacts that cover every packet criterion. Use the returned verified artifact IDs as the evidence boundary.",
	}, " ")
}

var _ workcontrol.SupervisorExecutor = (*liveSupervisorExecutor)(nil)
