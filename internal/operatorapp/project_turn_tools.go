package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	studiocontrol "github.com/paxlabs-inc/ion-agent/internal/studio"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/internal/tools/builtin"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

var studioProjectReference = regexp.MustCompile(
	`(?i)(?:Software Studio project\s*\(|\bproject\s+)([0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})`,
)

type routedProjectToolManager struct {
	base      agent.ToolManager
	workspace agent.ToolManager
	projects  *projectcontrol.Service
	studio    *studiocontrol.Service
	actorID   uuid.UUID
	projectID uuid.UUID
	routes    map[string]struct{}
}

type studioPlanInput struct {
	Goal                 string                     `json:"goal"`
	Deliverable          string                     `json:"deliverable"`
	VerificationRequired []string                   `json:"verification_required"`
	NextAction           string                     `json:"next_action"`
	MappedRequirements   []string                   `json:"mapped_requirements"`
	Assumptions          []studiocontrol.Assumption `json:"assumptions"`
	Rationale            string                     `json:"rationale"`
	DependencyImpact     []string                   `json:"dependency_impact"`
	Delta                studiocontrol.SpecDelta    `json:"spec_delta"`
}

func (manager routedProjectToolManager) Surface(ctx context.Context) []protocol.ToolDefinition {
	projectTools := manager.workspace.Surface(ctx)
	routes := make(map[string]struct{}, len(projectTools))
	for _, definition := range projectTools {
		routes[definition.Name] = struct{}{}
	}
	combined := make([]protocol.ToolDefinition, 0, len(projectTools)+len(manager.base.Surface(ctx)))
	for _, definition := range manager.base.Surface(ctx) {
		if definition.Name == "work_contract_set" {
			continue
		}
		if _, replaced := routes[definition.Name]; !replaced {
			combined = append(combined, definition)
		}
	}
	combined = append(combined, projectTools...)
	sort.Slice(combined, func(left, right int) bool { return combined[left].Name < combined[right].Name })
	return combined
}

func (manager routedProjectToolManager) Execute(
	ctx context.Context,
	call protocol.NormalizedToolCall,
) (json.RawMessage, error) {
	if call.Name == "work_contract_set" {
		return nil, fmt.Errorf("operator Studio tools: use studio_plan_propose for the bound project's contract, specification, and work-item crosswalk")
	}
	if _, routed := manager.routes[call.Name]; !routed {
		return manager.base.Execute(ctx, call)
	}
	if workspaceMutation(call.Name) {
		intents, _, err := manager.studio.List(ctx, manager.actorID)
		if err != nil {
			return nil, fmt.Errorf("operator Studio tools: check required project plan: %w", err)
		}
		hasPlan := false
		for _, intent := range intents {
			if intent.ProjectID != manager.projectID {
				continue
			}
			for _, proposal := range intent.Proposals {
				if proposal.Status == studiocontrol.ProposalProposed ||
					proposal.Status == studiocontrol.ProposalAccepted {
					hasPlan = true
					break
				}
			}
		}
		if !hasPlan {
			return nil, fmt.Errorf("operator Studio tools: a reviewable project plan is required before mutation; call studio_plan_propose with the complete advertised schema")
		}
	}
	result, err := manager.workspace.Execute(ctx, call)
	if err != nil || !workspaceMutation(call.Name) {
		return result, err
	}
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.ActorID != manager.actorID {
		return result, fmt.Errorf("operator Studio tools: authenticated project scope changed")
	}
	if _, observeErr := manager.projects.ObserveWorkspaceChange(
		context.WithoutCancel(ctx), manager.actorID, manager.projectID,
	); observeErr != nil {
		return result, fmt.Errorf("operator Studio tools: record workspace change: %w", observeErr)
	}
	return result, nil
}

func workspaceMutation(name string) bool {
	switch name {
	case "filesystem_write", "filesystem_patch", "shell_execute":
		return true
	default:
		return false
	}
}

func (capabilities *productionCapabilities) projectToolsForMessages(
	ctx context.Context,
	actorID uuid.UUID,
	messages []protocol.Message,
) (agent.ToolManager, *projectcontrol.Project, error) {
	return capabilities.projectToolsForTurn(ctx, actorID, "", uuid.Nil, messages)
}

func (capabilities *productionCapabilities) projectToolsForTurn(
	ctx context.Context,
	actorID uuid.UUID,
	surface string,
	projectID uuid.UUID,
	messages []protocol.Message,
) (agent.ToolManager, *projectcontrol.Project, error) {
	switch surface {
	case "":
		projectID = referencedStudioProject(messages)
	case "general":
		if projectID != uuid.Nil {
			return nil, nil, fmt.Errorf("operator Studio tools: general turn declared a project")
		}
		return capabilities.manager, nil, nil
	case "studio":
		if projectID == uuid.Nil {
			return nil, nil, fmt.Errorf("operator Studio tools: Studio turn requires a project")
		}
	default:
		return nil, nil, fmt.Errorf("operator Studio tools: unsupported turn surface")
	}
	if projectID == uuid.Nil {
		return capabilities.manager, nil, nil
	}
	project, err := capabilities.projects.Get(ctx, actorID, projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("operator Studio tools: load bound project: %w", err)
	}
	workspace, err := tools.NewManager(
		capabilities.clock,
		tools.WithExecutionPolicy(capabilities.policy),
		tools.WithApprovalAuthorizer(capabilities.approvals),
		tools.WithLifecycleObserver(capabilities.lifecycle),
	)
	if err != nil {
		return nil, nil, err
	}
	if err := builtin.RegisterWorkspace(ctx, workspace, project.Root); err != nil {
		return nil, nil, fmt.Errorf("operator Studio tools: bind project workspace: %w", err)
	}
	if err := capabilities.registerBoundStudioTools(ctx, workspace, project); err != nil {
		return nil, nil, err
	}
	routes := make(map[string]struct{})
	for _, definition := range workspace.Surface(ctx) {
		routes[definition.Name] = struct{}{}
	}
	return routedProjectToolManager{
		base: capabilities.manager, workspace: workspace,
		projects: capabilities.projects, studio: capabilities.studio,
		actorID: actorID, projectID: project.ID,
		routes: routes,
	}, &project, nil
}

func referencedStudioProject(messages []protocol.Message) uuid.UUID {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Role != protocol.RoleUser {
			continue
		}
		match := studioProjectReference.FindStringSubmatch(message.Content)
		if len(match) != 2 {
			continue
		}
		projectID, err := uuid.Parse(strings.TrimSpace(match[1]))
		if err == nil {
			return projectID
		}
	}
	return uuid.Nil
}

func studioProjectPrompt(project projectcontrol.Project) string {
	return fmt.Sprintf(`

Active Software Studio project: %s (%s).
Registered absolute workspace root: %s
Report this exact absolute root before the first edit. The filesystem, process, and Git tools are rooted directly at this registered project's WorkspaceHost. Use paths relative to that root. Never create or prefix a second directory named after the project. Inspect the existing project first. If the project has no reviewable Studio plan, call studio_plan_propose before implementation; use it again for material scope changes. Keep the project plan, Work Brief, files, revision, diagnostics, and verification evidence synchronized as work proceeds, and do not claim completion from file creation alone.`, project.Name, project.ID, project.Root)
}

func (capabilities *productionCapabilities) registerBoundStudioTools(
	ctx context.Context,
	manager *tools.Manager,
	project projectcontrol.Project,
) error {
	const planSchema = `{
		"type":"object",
		"additionalProperties":false,
		"required":["goal","deliverable","verification_required","next_action","rationale","spec_delta"],
		"properties":{
			"goal":{"type":"string","minLength":1,"maxLength":4096},
			"deliverable":{"type":"string","minLength":1,"maxLength":4096},
			"verification_required":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":4096}},
			"next_action":{"type":"string","minLength":1,"maxLength":4096},
			"mapped_requirements":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
			"assumptions":{"type":"array","maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["id","statement","reversible","material"],"properties":{"id":{"type":"string","minLength":1,"maxLength":128},"statement":{"type":"string","minLength":1,"maxLength":4096},"reversible":{"type":"boolean"},"material":{"type":"boolean"},"consequence":{"type":"string","maxLength":4096},"decision_needed":{"type":"string","maxLength":4096},"resolution":{"type":"string","maxLength":4096}}}},
			"rationale":{"type":"string","minLength":1,"maxLength":32768},
			"dependency_impact":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
			"spec_delta":{"type":"object","additionalProperties":false,"required":["user_visible_behavior","acceptance_criteria","security_boundaries","data_boundaries","verification_commands","tasks"],"properties":{
				"user_visible_behavior":{"type":"array","minItems":1,"maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"non_goals":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"constraints":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"risks":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"acceptance_criteria":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["id","description"],"properties":{"id":{"type":"string","minLength":1,"maxLength":128},"description":{"type":"string","minLength":1,"maxLength":4096}}}},
				"security_boundaries":{"type":"array","minItems":1,"maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"data_boundaries":{"type":"array","minItems":1,"maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"migration":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"rollback":{"type":"array","maxItems":64,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"verification_commands":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":4096}},
				"tasks":{"type":"array","minItems":1,"maxItems":256,"items":{"type":"object","additionalProperties":false,"required":["id","title","criteria"],"properties":{"id":{"type":"string","minLength":1,"maxLength":128},"title":{"type":"string","minLength":1,"maxLength":4096},"criteria":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":128}},"depends_on":{"type":"array","maxItems":256,"items":{"type":"string","minLength":1,"maxLength":128}}}}}
			}}
		}
	}`
	registration := workRegistration(
		"studio_plan_propose",
		"Create or revise the reviewable Software Studio plan and its outcome contract for the active project before implementation. spec_delta.acceptance_criteria is the single criterion source and is copied into the Work Brief; every task criterion must reference one of those IDs.",
		planSchema,
		tools.ClassificationYellow,
		func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			actor, sessionID, err := authenticatedWorkScope(runCtx)
			if err != nil || sessionID == nil {
				return nil, fmt.Errorf("operator Studio tools: authenticated session scope is required")
			}
			var input studioPlanInput
			if err := decodeStrictJSON(raw, &input); err != nil {
				return nil, err
			}
			if err := validateStudioPlanCrosswalk(input); err != nil {
				return nil, err
			}
			intents, _, err := capabilities.studio.List(runCtx, actor)
			if err != nil {
				return nil, err
			}
			var existing *studiocontrol.Intent
			for index := range intents {
				if intents[index].ProjectID == project.ID {
					item := intents[index]
					existing = &item
					break
				}
			}
			contractInput := workcontrol.ContractInput{
				SessionID: sessionID, Goal: input.Goal, Deliverable: input.Deliverable,
				DoneCriteria:         studioWorkCriteria(input.Delta.Criteria),
				VerificationRequired: input.VerificationRequired,
				NextAction:           input.NextAction, Status: workcontrol.StatusActive,
			}
			if existing != nil {
				contractInput.ID = existing.OutcomeContractID
			}
			plannedItems := make([]workcontrol.WorkItemInput, 0, len(input.Delta.Tasks))
			for _, task := range input.Delta.Tasks {
				plannedItems = append(plannedItems, workcontrol.WorkItemInput{
					ID: task.ID, Title: task.Title, Criteria: task.Criteria,
					DependsOn: task.DependsOn,
				})
			}
			before, err := capabilities.work.Get(runCtx, actor)
			if err != nil {
				return nil, err
			}
			if existing == nil {
				for index := range before.Contracts {
					candidate := before.Contracts[index]
					if candidate.SessionID == nil || *candidate.SessionID != *sessionID ||
						candidate.Status == workcontrol.StatusCompleted ||
						candidate.Status == workcontrol.StatusCancelled {
						continue
					}
					if contractInput.ID == uuid.Nil {
						contractInput.ID = candidate.ID
					}
				}
			}
			current, err := capabilities.projects.Get(runCtx, actor, project.ID)
			if err != nil {
				return nil, err
			}
			contract, workItems, err := capabilities.work.PutContractWithWorkItems(
				runCtx, actor, contractInput, plannedItems,
			)
			if err != nil {
				return nil, err
			}
			compensate := func(writeErr error) error {
				if writeErr == nil {
					return nil
				}
				restoreErr := capabilities.work.RestorePortfolio(
					context.WithoutCancel(runCtx), actor, before.Revision+1, before,
				)
				if restoreErr != nil {
					return errors.Join(writeErr, fmt.Errorf("operator Studio tools: compensate Work Brief: %w", restoreErr))
				}
				return writeErr
			}
			if existing != nil {
				proposal, proposeErr := capabilities.studio.ProposeScopeChange(
					runCtx, actor, studiocontrol.ScopeChangeInput{
						IntentID: existing.ID, Rationale: input.Rationale,
						DependencyImpact: input.DependencyImpact, Delta: input.Delta,
					},
				)
				return marshalWorkResult(map[string]any{
					"contract": contract, "work_items": workItems,
					"intent_id": existing.ID, "proposal": proposal,
				}, compensate(proposeErr))
			}
			intent, err := capabilities.studio.Compile(runCtx, actor, studiocontrol.CompileInput{
				ProjectID: project.ID, OutcomeContractID: contract.ID,
				WorkspaceRevision: current.WorkspaceRevision, Goal: input.Goal,
				MappedRequirements: input.MappedRequirements, Assumptions: input.Assumptions,
				Rationale: input.Rationale, DependencyImpact: input.DependencyImpact,
				Delta: &input.Delta,
			})
			return marshalWorkResult(map[string]any{
				"contract": contract, "work_items": workItems, "intent": intent,
			}, compensate(err))
		},
	)
	if err := manager.Register(ctx, registration); err != nil {
		return fmt.Errorf("operator Studio tools: register %s: %w", registration.Name, err)
	}
	if err := capabilities.registerBoundArtifactTools(ctx, manager, project); err != nil {
		return err
	}
	return nil
}

func (capabilities *productionCapabilities) registerBoundArtifactTools(
	ctx context.Context,
	manager *tools.Manager,
	project projectcontrol.Project,
) error {
	registrations := []tools.Registration{
		workRegistration(
			"artifact_record",
			"Register a deliverable relative to the active project's workspace as unverified evidence for completion criteria.",
			`{"type":"object","additionalProperties":false,"required":["contract_id","kind","title","reference","criteria_covered"],"properties":{"contract_id":{"type":"string","format":"uuid"},"kind":{"type":"string","minLength":1,"maxLength":4096},"title":{"type":"string","minLength":1,"maxLength":4096},"reference":{"type":"string","minLength":1,"maxLength":4096},"criteria_covered":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":128}}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input workcontrol.ArtifactInput
				if err := decodeStrictJSON(raw, &input); err != nil {
					return nil, err
				}
				if err := capabilities.requireProjectContract(runCtx, actor, project.ID, input.ContractID); err != nil {
					return nil, err
				}
				artifact, err := capabilities.work.RecordArtifactInWorkspace(
					runCtx, actor, input, project.Root,
				)
				return marshalWorkResult(artifact, err)
			},
		),
		workRegistration(
			"artifact_verify",
			"Independently read and hash a registered deliverable inside the active project's workspace. The client cannot supply the digest or verification root.",
			`{"type":"object","additionalProperties":false,"required":["artifact_id"],"properties":{"artifact_id":{"type":"string","format":"uuid"}}}`,
			tools.ClassificationYellow,
			func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
				actor, _, err := authenticatedWorkScope(runCtx)
				if err != nil {
					return nil, err
				}
				var input struct {
					ArtifactID uuid.UUID `json:"artifact_id"`
				}
				if err := decodeStrictJSON(raw, &input); err != nil || input.ArtifactID == uuid.Nil {
					return nil, fmt.Errorf("operator Studio tools: valid artifact_id is required")
				}
				portfolio, err := capabilities.work.Get(runCtx, actor)
				if err != nil {
					return nil, err
				}
				contractID := uuid.Nil
				for _, artifact := range portfolio.Artifacts {
					if artifact.ID == input.ArtifactID {
						contractID = artifact.ContractID
						break
					}
				}
				if contractID == uuid.Nil {
					return nil, fmt.Errorf("operator Studio tools: artifact not found")
				}
				if err := capabilities.requireProjectContract(runCtx, actor, project.ID, contractID); err != nil {
					return nil, err
				}
				artifact, err := capabilities.work.VerifyArtifactInWorkspace(
					runCtx, actor, input.ArtifactID, project.Root,
				)
				return marshalWorkResult(artifact, err)
			},
		),
	}
	for _, registration := range registrations {
		if err := manager.Register(ctx, registration); err != nil {
			return fmt.Errorf("operator Studio tools: register %s: %w", registration.Name, err)
		}
	}
	return nil
}

func (capabilities *productionCapabilities) requireProjectContract(
	ctx context.Context,
	actor, projectID, contractID uuid.UUID,
) error {
	if contractID == uuid.Nil {
		return fmt.Errorf("operator Studio tools: valid contract_id is required")
	}
	intents, _, err := capabilities.studio.List(ctx, actor)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if intent.ProjectID == projectID && intent.OutcomeContractID == contractID {
			return nil
		}
	}
	return fmt.Errorf("operator Studio tools: outcome contract is not bound to the active project")
}

func validateStudioPlanCrosswalk(input studioPlanInput) error {
	if err := studiocontrol.ValidateSpecDelta(input.Delta); err != nil {
		return &tools.ArgumentValidationError{Issues: []tools.ArgumentValidationIssue{{
			Path: "spec_delta", Message: err.Error(),
		}}}
	}
	return nil
}

func studioWorkCriteria(criteria []studiocontrol.Criterion) []workcontrol.Criterion {
	result := make([]workcontrol.Criterion, 0, len(criteria))
	for _, criterion := range criteria {
		result = append(result, workcontrol.Criterion{
			ID:          strings.TrimSpace(criterion.ID),
			Description: strings.TrimSpace(criterion.Description),
		})
	}
	return result
}
