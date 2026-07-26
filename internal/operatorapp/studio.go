package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
	studiocontrol "github.com/paxlabs-inc/ion-agent/internal/studio"
)

type studioServiceProjection struct {
	service *studiocontrol.Service
}

func (capabilities *productionCapabilities) StudioQuery(ctx context.Context,
	operation controlplane.Operation, scope controlplane.Scope, payload json.RawMessage) (any, error) {
	if operation == controlplane.OperationStudioContextPlan {
		return capabilities.planStudioContext(ctx, scope, payload)
	}
	return queryStudio(ctx, capabilities.studio, operation, scope, payload)
}

func (projection studioServiceProjection) StudioQuery(ctx context.Context,
	operation controlplane.Operation, scope controlplane.Scope, payload json.RawMessage) (any, error) {
	return queryStudio(ctx, projection.service, operation, scope, payload)
}

func (capabilities *productionCapabilities) planStudioContext(ctx context.Context,
	scope controlplane.Scope, payload json.RawMessage) (any, error) {
	if capabilities.projects == nil || capabilities.studio == nil || capabilities.work == nil || capabilities.skills == nil {
		return nil, fmt.Errorf("operator capabilities: Studio context dependencies are unavailable")
	}
	var input struct {
		IntentID              uuid.UUID `json:"intent_id"`
		WorkspaceRevision     uint64    `json:"workspace_revision"`
		ExpectedIndexRevision uint64    `json:"expected_index_revision,omitempty"`
		Task                  string    `json:"task"`
		PathScope             string    `json:"path_scope,omitempty"`
		MaxBytes              int       `json:"max_bytes,omitempty"`
		Mismatch              string    `json:"mismatch,omitempty"`
		Expand                []string  `json:"expand,omitempty"`
	}
	if err := decodeStrictJSON(payload, &input); err != nil {
		return nil, err
	}
	if input.IntentID == uuid.Nil || input.WorkspaceRevision == 0 || strings.TrimSpace(input.Task) == "" {
		return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid,
			Message: "intent_id, workspace_revision, and task are required"}
	}
	intent, err := capabilities.studio.Get(ctx, scope.ActorID, input.IntentID)
	if err != nil {
		return nil, studioPublicError(err)
	}
	projectState, err := capabilities.projects.Get(ctx, scope.ActorID, intent.ProjectID)
	if err != nil {
		return nil, projectPublicError(err)
	}
	if projectState.WorkspaceRevision != input.WorkspaceRevision {
		return nil, projectPublicError(projectcontrol.ErrStaleRevision)
	}

	sources := make([]projectcontrol.ContextSource, 0, 8)
	intentContent, err := json.Marshal(map[string]any{
		"intent_id": intent.ID, "goal": intent.Goal, "mapped_requirements": intent.MappedRequirements,
		"assumptions": intent.Assumptions, "proposals": intent.Proposals,
		"active_proposal_id": intent.ActiveProposalID, "baseline_workspace_revision": intent.BaselineRevision,
	})
	if err != nil {
		return nil, err
	}
	sources = append(sources, projectcontrol.ContextSource{Kind: "studio_intent",
		Title: "Compiled Software Studio intent", Content: string(intentContent), Priority: 100, Verified: true})

	brief, err := capabilities.work.Brief(ctx, scope.ActorID, scope.SessionID)
	if err != nil {
		return nil, err
	}
	briefContent, err := json.Marshal(brief)
	if err != nil {
		return nil, err
	}
	sources = append(sources, projectcontrol.ContextSource{Kind: "work_brief",
		Title: "Current Work Brief", Content: string(briefContent), Priority: 90, Verified: true})
	for _, artifact := range brief.Deliverables {
		if artifact.VerifiedAt == nil || artifact.SHA256 == "" || artifact.Verification != "server_sha256" {
			continue
		}
		content, marshalErr := json.Marshal(map[string]any{"title": artifact.Title, "kind": artifact.Kind,
			"reference": artifact.Reference, "criteria": artifact.CriteriaCovered,
			"sha256": artifact.SHA256, "verified_at": artifact.VerifiedAt})
		if marshalErr != nil {
			return nil, marshalErr
		}
		sources = append(sources, projectcontrol.ContextSource{Kind: "verified_evidence",
			Title: artifact.Title, Content: string(content), Priority: 92, Verified: true,
			Citation: projectcontrol.Citation{Path: artifact.Reference, SHA256: artifact.SHA256,
				Source: "verified_artifact"}})
	}

	installed, err := capabilities.skills.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(installed, func(left, right int) bool {
		return studioSkillScore(installed[left], input.Task) > studioSkillScore(installed[right], input.Task)
	})
	for i, skill := range installed {
		if i >= 5 || studioSkillScore(skill, input.Task) == 0 {
			break
		}
		content, marshalErr := json.Marshal(map[string]any{"name": skill.Name, "trigger": skill.Trigger,
			"steps": skill.Steps, "pitfalls": skill.Pitfalls, "verification": skill.Verification,
			"revision": skill.Revision})
		if marshalErr != nil {
			return nil, marshalErr
		}
		sources = append(sources, projectcontrol.ContextSource{Kind: "skill", Title: skill.Name,
			Content: string(content), Priority: 60, Verified: true})
	}

	pack, err := capabilities.projects.PlanProjectContext(ctx, scope.ActorID, projectcontrol.ContextPlanRequest{
		ProjectID: intent.ProjectID, WorkspaceRevision: input.WorkspaceRevision,
		ExpectedIndexRevision: input.ExpectedIndexRevision, Task: input.Task, PathScope: input.PathScope,
		MaxBytes: input.MaxBytes, Mismatch: input.Mismatch, Expand: input.Expand, Sources: sources,
	})
	if err != nil {
		return nil, projectPublicError(err)
	}
	return pack, nil
}

func studioSkillScore(skill skills.Skill, task string) int {
	haystack := strings.ToLower(skill.Name + " " + skill.Trigger + " " + strings.Join(skill.Steps, " "))
	seen, score := map[string]struct{}{}, 0
	for _, token := range strings.FieldsFunc(strings.ToLower(task), func(character rune) bool {
		return character < 'a' || character > 'z'
	}) {
		if len(token) < 3 {
			continue
		}
		if _, duplicate := seen[token]; !duplicate && strings.Contains(haystack, token) {
			seen[token] = struct{}{}
			score++
		}
	}
	return score
}

func queryStudio(ctx context.Context, service *studiocontrol.Service,
	operation controlplane.Operation, scope controlplane.Scope, payload json.RawMessage) (any, error) {
	switch operation {
	case controlplane.OperationStudioIntentList:
		intents, revision, err := service.List(ctx, scope.ActorID)
		if err != nil {
			return nil, studioPublicError(err)
		}
		return map[string]any{"revision": revision, "intents": intents}, nil
	case controlplane.OperationStudioIntentGet:
		intentID, err := decodeIntentID(payload)
		if err != nil {
			return nil, err
		}
		intent, err := service.Get(ctx, scope.ActorID, intentID)
		return intent, studioPublicError(err)
	case controlplane.OperationStudioCompletionCheck:
		intentID, err := decodeIntentID(payload)
		if err != nil {
			return nil, err
		}
		completion, err := service.Completion(ctx, scope.ActorID, intentID)
		return completion, studioPublicError(err)
	case controlplane.OperationStudioDriftGet:
		intentID, err := decodeIntentID(payload)
		if err != nil {
			return nil, err
		}
		drift, err := service.DetectDrift(ctx, scope.ActorID, intentID)
		return drift, studioPublicError(err)
	default:
		return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "unsupported Studio query"}
	}
}

func (capabilities *productionCapabilities) StudioCommand(ctx context.Context,
	request controlplane.Request) (any, error) {
	return commandStudio(ctx, capabilities.studio, request)
}

func (projection studioServiceProjection) StudioCommand(ctx context.Context,
	request controlplane.Request) (any, error) {
	return commandStudio(ctx, projection.service, request)
}

func commandStudio(ctx context.Context, service *studiocontrol.Service,
	request controlplane.Request) (any, error) {
	var result any
	var err error
	switch request.Operation {
	case controlplane.OperationStudioIntentCompile:
		var input studiocontrol.CompileInput
		if err = decodeStrictJSON(request.Payload, &input); err == nil {
			result, err = service.Compile(ctx, request.Scope.ActorID, input)
		}
	case controlplane.OperationStudioScopePropose:
		var input studiocontrol.ScopeChangeInput
		if err = decodeStrictJSON(request.Payload, &input); err == nil {
			result, err = service.ProposeScopeChange(ctx, request.Scope.ActorID, input)
		}
	case controlplane.OperationStudioProposalDecide:
		var input struct {
			IntentID            uuid.UUID         `json:"intent_id"`
			ProposalID          uuid.UUID         `json:"proposal_id"`
			Accept              bool              `json:"accept"`
			Reason              string            `json:"reason"`
			AssumptionDecisions map[string]string `json:"assumption_decisions,omitempty"`
		}
		if err = decodeStrictJSON(request.Payload, &input); err == nil {
			result, err = service.DecideProposalWithDecisions(ctx, request.Scope.ActorID,
				input.IntentID, input.ProposalID, input.Accept, input.Reason, input.AssumptionDecisions)
		}
	case controlplane.OperationStudioProposalApply:
		var input struct {
			IntentID   uuid.UUID `json:"intent_id"`
			ProposalID uuid.UUID `json:"proposal_id"`
		}
		if err = decodeStrictJSON(request.Payload, &input); err == nil {
			result, err = service.ApplyProposal(ctx, request.Scope.ActorID,
				input.IntentID, input.ProposalID)
		}
	case controlplane.OperationStudioCorrelationRecord:
		var input studiocontrol.CorrelationInput
		if err = decodeStrictJSON(request.Payload, &input); err == nil {
			result, err = service.RecordCorrelation(ctx, request.Scope.ActorID, input)
		}
	default:
		return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "unsupported Studio command"}
	}
	if err != nil {
		return nil, studioPublicError(err)
	}
	return result, nil
}

func decodeIntentID(payload json.RawMessage) (uuid.UUID, error) {
	var input struct {
		IntentID uuid.UUID `json:"intent_id"`
	}
	if err := decodeStrictJSON(payload, &input); err != nil || input.IntentID == uuid.Nil {
		return uuid.Nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid intent_id is required"}
	}
	return input.IntentID, nil
}

func studioPublicError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, studiocontrol.ErrNotFound):
		return controlplane.PublicError{Code: controlplane.ErrorNotFound, Message: "Studio intent or proposal was not found"}
	case errors.Is(err, studiocontrol.ErrStaleRevision), errors.Is(err, studiocontrol.ErrConflict):
		return controlplane.PublicError{Code: controlplane.ErrorConflict, Message: "project or specification state changed; review and retry"}
	case errors.Is(err, studiocontrol.ErrDecision):
		return controlplane.PublicError{Code: controlplane.ErrorConflict, Message: err.Error()}
	default:
		return controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: err.Error()}
	}
}
