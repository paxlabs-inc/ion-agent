// Package operatorapp assembles production operator-facing projections without
// exposing domain storage, key material, or internal mutable objects.
package operatorapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/presence/identity"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
	studiocontrol "github.com/paxlabs-inc/ion-agent/internal/studio"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

// RuntimeInfo contains safe process metadata used by operator projections.
// Provider names and models are identifiers; credentials are deliberately
// absent from this type.
type RuntimeInfo struct {
	ProviderName  string
	ProviderModel string
	ProviderUsage func() map[string]any
	DataDirectory string
	StartedAt     time.Time
}

// SurfaceService is a scope-isolated application service for the complete web
// and TUI read matrix.
type SurfaceService struct {
	runtime      RuntimeInfo
	capabilities capabilityProjection
	work         workProjection
	projects     projectProjection
	studio       studioProjection
}

type workProjection interface {
	WorkQuery(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
	WorkCommand(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
}

type projectProjection interface {
	ProjectQuery(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
	ProjectCommand(context.Context, controlplane.Request) (any, error)
}

type studioProjection interface {
	StudioQuery(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
	StudioCommand(context.Context, controlplane.Request) (any, error)
}

type capabilityProjection interface {
	ToolSurface(context.Context) []tools.Status
	ToolReadiness(context.Context) map[string]any
	ToolCommand(context.Context, controlplane.Request) (any, error)
	MemoryCommand(context.Context, controlplane.Request) (any, error)
	SwarmCommand(context.Context, controlplane.Request) (any, error)
	SkillList(context.Context) ([]skills.SkillSummary, error)
	SkillLifecycle(context.Context) (skills.Lifecycle, error)
	SkillCommand(context.Context, controlplane.Operation, json.RawMessage) (any, error)
	PluginList(context.Context) []pluginProjection
	MCPTools(context.Context) []tools.Status
	ChannelList() []channelProjection
	ChannelHealth() map[string]any
	ScheduleState(uuid.UUID) any
	AutomatrixState(uuid.UUID) any
	AutomatrixCommand(context.Context, controlplane.Operation, uuid.UUID, json.RawMessage) (any, error)
	CuriosityState() any
	IntegrityState() any
	PresenceCommand(context.Context, controlplane.Operation, json.RawMessage) (any, error)
	QueryTool(context.Context, controlplane.Scope, string, json.RawMessage) any
	SwarmState(string) map[string]any
	CognitionState(context.Context, uuid.UUID) (cognitionSnapshot, error)
	RecoveryState(context.Context, uuid.UUID) ([]session.TurnState, error)
	PolicyEvents(context.Context) (any, error)
	LivingState(context.Context, controlplane.Scope) (livingProjection, error)
	SoulState(context.Context, uuid.UUID) (identity.Projection, error)
	SoulCommand(context.Context, controlplane.Request) (json.RawMessage, error)
	WorkQuery(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
	WorkCommand(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
	ProjectQuery(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
	ProjectCommand(context.Context, controlplane.Request) (any, error)
	StudioQuery(context.Context, controlplane.Operation, controlplane.Scope, json.RawMessage) (any, error)
	StudioCommand(context.Context, controlplane.Request) (any, error)
}

// NewSurfaceService constructs a bounded runtime projection service.
func NewSurfaceService(
	runtime RuntimeInfo,
	capabilities ...capabilityProjection,
) (*SurfaceService, error) {
	if runtime.StartedAt.IsZero() {
		runtime.StartedAt = time.Now().UTC()
	}
	service := &SurfaceService{runtime: runtime}
	if len(capabilities) > 0 {
		service.capabilities = capabilities[0]
		service.work = capabilities[0]
		service.projects = capabilities[0]
		service.studio = capabilities[0]
	}
	return service, nil
}

// NewSurfaceServiceWithProjectsAndWork constructs the focused browser
// acceptance surface with the real durable Work and supervisor services.
func NewSurfaceServiceWithProjectsAndWork(
	runtime RuntimeInfo,
	projects *projectcontrol.Service,
	studio *studiocontrol.Service,
	work *workcontrol.Service,
	supervisor *workcontrol.SupervisorService,
	clock types.Clock,
) (*SurfaceService, error) {
	service, err := NewSurfaceServiceWithProjects(
		runtime,
		projects,
		studio,
		clock,
	)
	if err != nil {
		return nil, err
	}
	if work == nil || supervisor == nil {
		return nil, fmt.Errorf(
			"operator surface: Work and supervisor services are required",
		)
	}
	service.work = focusedWorkProjection{
		capabilities: &productionCapabilities{
			work: work, supervisor: supervisor,
		},
	}
	return service, nil
}

type focusedWorkProjection struct {
	capabilities *productionCapabilities
}

func (projection focusedWorkProjection) WorkQuery(
	ctx context.Context,
	operation controlplane.Operation,
	scope controlplane.Scope,
	payload json.RawMessage,
) (any, error) {
	return projection.capabilities.WorkQuery(ctx, operation, scope, payload)
}

func (projection focusedWorkProjection) WorkCommand(
	ctx context.Context,
	operation controlplane.Operation,
	scope controlplane.Scope,
	payload json.RawMessage,
) (any, error) {
	return projection.capabilities.WorkCommand(ctx, operation, scope, payload)
}

// NewSurfaceServiceWithProjects constructs the same operator projection with
// the durable project boundary enabled independently. It is used by focused
// production-surface acceptance servers that do not assemble every runtime
// capability.
func NewSurfaceServiceWithProjects(
	runtime RuntimeInfo,
	projects *projectcontrol.Service,
	studio *studiocontrol.Service,
	clock types.Clock,
) (*SurfaceService, error) {
	if projects == nil || studio == nil || clock == nil {
		return nil, fmt.Errorf("operator surface: project, Studio, and clock services are required")
	}
	service, err := NewSurfaceService(runtime)
	if err != nil {
		return nil, err
	}
	service.projects = projectServiceProjection{service: projects, clock: clock}
	service.studio = studioServiceProjection{service: studio}
	return service, nil
}

// Query returns redaction-safe current state for one authenticated scope.
func (service *SurfaceService) Query(
	ctx context.Context,
	request controlplane.Request,
) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(service.projection(
		ctx, request.Operation, request.Scope, request.Payload,
	))
}

// AuthorizeMutation enforces the application boundary before dispatcher audit.
// Domain-specific services may add stricter approval requirements.
func (service *SurfaceService) AuthorizeMutation(
	ctx context.Context,
	request controlplane.Request,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Kind != controlplane.KindCommand {
		return controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "mutation command is required",
		}
	}
	if strings.TrimSpace(request.Scope.Profile) == "forbidden" ||
		strings.TrimSpace(request.Scope.Channel) == "forbidden" {
		return controlplane.ErrUnauthorized
	}
	return nil
}

// Command records safe mutation metadata within the authenticated scope.
func (service *SurfaceService) Command(
	ctx context.Context,
	request controlplane.Request,
) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Operation == controlplane.OperationSoulPropose {
		if service.capabilities == nil {
			return nil, fmt.Errorf("operator surface: SOUL service is unavailable")
		}
		return service.capabilities.SoulCommand(ctx, request)
	}
	if request.Operation == controlplane.OperationRelationshipUpdate {
		if service.capabilities == nil {
			return nil, fmt.Errorf("operator surface: relationship service is unavailable")
		}
		projection, ok := service.capabilities.(interface {
			RelationshipCommand(context.Context, controlplane.Request) (json.RawMessage, error)
		})
		if !ok {
			return nil, fmt.Errorf("operator surface: relationship service is unavailable")
		}
		return projection.RelationshipCommand(ctx, request)
	}
	if request.Operation == controlplane.OperationSkillSave ||
		request.Operation == controlplane.OperationSkillRefine ||
		request.Operation == controlplane.OperationSkillRollback {
		if service.capabilities == nil {
			return nil, fmt.Errorf("operator surface: skill service is unavailable")
		}
		result, err := service.capabilities.SkillCommand(
			ctx, request.Operation, request.Payload,
		)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationToolInvoke {
		if service.capabilities == nil {
			return nil, fmt.Errorf("operator surface: tool service is unavailable")
		}
		result, err := service.capabilities.ToolCommand(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationBrowserWorkflowPause ||
		request.Operation == controlplane.OperationBrowserWorkflowResume ||
		request.Operation == controlplane.OperationBrowserWorkflowCancel ||
		request.Operation == controlplane.OperationBrowserWorkflowHandoff {
		browser, ok := service.capabilities.(interface {
			BrowserWorkflowCommand(context.Context, controlplane.Request) (any, error)
		})
		if !ok {
			return nil, controlplane.PublicError{
				Code: controlplane.ErrorUnavailable, Message: "browser workflow service is unavailable",
			}
		}
		result, err := browser.BrowserWorkflowCommand(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationMemoryPin ||
		request.Operation == controlplane.OperationMemoryRecover {
		if service.capabilities == nil {
			return nil, controlplane.PublicError{
				Code: controlplane.ErrorUnavailable, Message: "memory service is unavailable",
			}
		}
		result, err := service.capabilities.MemoryCommand(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationSwarmAbort {
		if service.capabilities == nil {
			return nil, controlplane.PublicError{
				Code: controlplane.ErrorUnavailable, Message: "swarm service is unavailable",
			}
		}
		result, err := service.capabilities.SwarmCommand(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationIntegrityRun ||
		request.Operation == controlplane.OperationScheduleUpdate {
		if service.capabilities == nil {
			return nil, fmt.Errorf("operator surface: presence service is unavailable")
		}
		result, err := service.capabilities.PresenceCommand(
			ctx, request.Operation, request.Payload,
		)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationAutomatrixApprove ||
		request.Operation == controlplane.OperationAutomatrixReject {
		if service.capabilities == nil {
			return nil, fmt.Errorf("operator surface: Automatrix service is unavailable")
		}
		result, err := service.capabilities.AutomatrixCommand(
			ctx, request.Operation, request.Scope.ActorID, request.Payload,
		)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationWorkContractPut ||
		request.Operation == controlplane.OperationWorkComplete ||
		request.Operation == controlplane.OperationArtifactRecord ||
		request.Operation == controlplane.OperationArtifactVerify ||
		request.Operation == controlplane.OperationAutonomyUpdate ||
		request.Operation == controlplane.OperationWorkflowStart ||
		request.Operation == controlplane.OperationWorkflowAdvance ||
		request.Operation == controlplane.OperationSupervisorStart ||
		request.Operation == controlplane.OperationSupervisorSteer ||
		request.Operation == controlplane.OperationSupervisorCancel {
		if service.work == nil {
			return nil, fmt.Errorf("operator surface: disciplined work service is unavailable")
		}
		result, err := service.work.WorkCommand(
			ctx, request.Operation, request.Scope, request.Payload,
		)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationProjectCreate ||
		request.Operation == controlplane.OperationProjectImport ||
		request.Operation == controlplane.OperationProjectAttach ||
		request.Operation == controlplane.OperationProjectClone ||
		request.Operation == controlplane.OperationProjectIndexRefresh ||
		request.Operation == controlplane.OperationProjectPatchApply ||
		request.Operation == controlplane.OperationProjectPatchRollback ||
		request.Operation == controlplane.OperationProjectGitPreviewStart ||
		request.Operation == controlplane.OperationProjectGitPreviewClose ||
		request.Operation == controlplane.OperationProjectGitReviewComment ||
		request.Operation == controlplane.OperationProjectGitReviewResolve ||
		request.Operation == controlplane.OperationProjectGitBranchCreate ||
		request.Operation == controlplane.OperationProjectGitStage ||
		request.Operation == controlplane.OperationProjectGitStageHunks ||
		request.Operation == controlplane.OperationProjectGitCommit ||
		request.Operation == controlplane.OperationProjectGitCheckpoint ||
		request.Operation == controlplane.OperationProjectGitTagCreate ||
		request.Operation == controlplane.OperationProjectGitSync ||
		request.Operation == controlplane.OperationProjectGitPull ||
		request.Operation == controlplane.OperationProjectGitMerge ||
		request.Operation == controlplane.OperationProjectGitPush ||
		request.Operation == controlplane.OperationProjectGitForceWithLease ||
		request.Operation == controlplane.OperationProjectGitProviderGrant ||
		request.Operation == controlplane.OperationProjectGitProviderDraft ||
		request.Operation == controlplane.OperationProjectRuntimeStart ||
		request.Operation == controlplane.OperationProjectRuntimePhase ||
		request.Operation == controlplane.OperationProjectRuntimeReload ||
		request.Operation == controlplane.OperationProjectRuntimeRestart ||
		request.Operation == controlplane.OperationProjectRuntimeStop ||
		request.Operation == controlplane.OperationProjectRuntimeReport ||
		request.Operation == controlplane.OperationProjectRuntimeAnnotate ||
		request.Operation == controlplane.OperationProjectRuntimeStylePropose ||
		request.Operation == controlplane.OperationProjectVerificationDerive ||
		request.Operation == controlplane.OperationProjectVerificationRun ||
		request.Operation == controlplane.OperationProjectVerificationWaiver ||
		request.Operation == controlplane.OperationProjectResourcePlan ||
		request.Operation == controlplane.OperationProjectResourceApply ||
		request.Operation == controlplane.OperationProjectEnvironmentPut ||
		request.Operation == controlplane.OperationProjectMigrationPlan ||
		request.Operation == controlplane.OperationProjectMigrationApply ||
		request.Operation == controlplane.OperationProjectMigrationRollback ||
		request.Operation == controlplane.OperationProjectDeploymentPlan ||
		request.Operation == controlplane.OperationProjectDeploymentApply ||
		request.Operation == controlplane.OperationProjectDeploymentReconcile ||
		request.Operation == controlplane.OperationProjectDeploymentRollback ||
		request.Operation == controlplane.OperationProjectReleasePrepare ||
		request.Operation == controlplane.OperationProjectPortableExport ||
		request.Operation == controlplane.OperationProjectDependenciesInstall ||
		request.Operation == controlplane.OperationProjectProcessStart ||
		request.Operation == controlplane.OperationProjectTerminalInput ||
		request.Operation == controlplane.OperationProjectTerminalResize ||
		request.Operation == controlplane.OperationProjectTerminalSignal ||
		request.Operation == controlplane.OperationProjectTerminalCancel ||
		request.Operation == controlplane.OperationWorkspaceLifecycle {
		if service.projects == nil {
			return nil, fmt.Errorf("operator surface: project service is unavailable")
		}
		result, err := service.projects.ProjectCommand(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationStudioIntentCompile ||
		request.Operation == controlplane.OperationStudioScopePropose ||
		request.Operation == controlplane.OperationStudioProposalDecide ||
		request.Operation == controlplane.OperationStudioProposalApply ||
		request.Operation == controlplane.OperationStudioCorrelationRecord {
		if service.studio == nil {
			return nil, fmt.Errorf("operator surface: Software Studio is unavailable")
		}
		result, err := service.studio.StudioCommand(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if request.Operation == controlplane.OperationChannelRetry ||
		request.Operation == controlplane.OperationChannelSkip {
		channels, ok := service.capabilities.(interface {
			ChannelCommand(
				context.Context, controlplane.Operation, json.RawMessage,
			) (any, error)
		})
		if !ok {
			return nil, fmt.Errorf("operator surface: channel service is unavailable")
		}
		result, err := channels.ChannelCommand(
			ctx, request.Operation, request.Payload,
		)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	return nil, controlplane.PublicError{
		Code: controlplane.ErrorUnavailable, Message: "operation is not available",
	}
}

func (service *SurfaceService) projection(
	ctx context.Context,
	operation controlplane.Operation,
	scope controlplane.Scope,
	payload json.RawMessage,
) any {
	switch operation {
	case controlplane.OperationSystemHealth:
		return map[string]any{
			"status": "ready", "started_at": service.runtime.StartedAt,
			"uptime_seconds": int64(time.Since(service.runtime.StartedAt).Seconds()),
		}
	case controlplane.OperationSystemMetrics:
		return map[string]any{
			"scope": scopeSummary(scope), "bounded": true,
			"event_stream": "sequenced", "slow_client_policy": "disconnect",
		}
	case controlplane.OperationProviderList:
		if strings.TrimSpace(service.runtime.ProviderName) == "" {
			return []any{}
		}
		return []any{map[string]any{
			"name": service.runtime.ProviderName, "model": service.runtime.ProviderModel,
			"status": "configured", "credentials": "write-only",
		}}
	case controlplane.OperationProviderUsage:
		if service.runtime.ProviderUsage == nil {
			return map[string]any{
				"status": "not_available",
				"reason": "Live model usage accounting is unavailable.",
			}
		}
		return service.runtime.ProviderUsage()
	case controlplane.OperationToolSurface:
		if service.capabilities != nil {
			return service.capabilities.ToolSurface(ctx)
		}
		return unavailableStatus("tool surface is not configured")
	case controlplane.OperationToolReadiness:
		if service.capabilities != nil {
			return service.capabilities.ToolReadiness(ctx)
		}
		return unavailableStatus("tool readiness is not configured")
	case controlplane.OperationBrowserWorkflowList,
		controlplane.OperationBrowserCredentialList:
		browser, ok := service.capabilities.(interface {
			BrowserWorkflowQuery(
				context.Context, controlplane.Operation, controlplane.Scope,
			) (any, error)
		})
		if !ok {
			return unavailableStatus("browser workflow service is unavailable")
		}
		result, err := browser.BrowserWorkflowQuery(ctx, operation, scope)
		if err != nil {
			return unavailableProjection(err)
		}
		return result
	case controlplane.OperationMemorySearch:
		if service.capabilities != nil {
			return service.capabilities.QueryTool(ctx, scope, "memory_search", payload)
		}
		return unavailableStatus("memory search is not configured")
	case controlplane.OperationMemoryGet:
		if service.capabilities != nil {
			return service.capabilities.QueryTool(ctx, scope, "memory_recall", payload)
		}
		return unavailableStatus("memory recall is not configured")
	case controlplane.OperationMemoryGraph:
		return unavailableStatus("memory graph projection is not implemented")
	case controlplane.OperationMemoryActivation:
		return unavailableStatus("memory activation projection is not implemented")
	case controlplane.OperationPremiseList:
		cognition, err := service.scopedCognition(ctx, scope)
		if err != nil {
			return unavailableProjection(err)
		}
		return cognition.Premises.Items
	case controlplane.OperationCitationVerify:
		return unavailableStatus("citation verification projection is not implemented")
	case controlplane.OperationPredictionList:
		cognition, err := service.scopedCognition(ctx, scope)
		if err != nil {
			return unavailableProjection(err)
		}
		return cognition.Predictions
	case controlplane.OperationTaskgraphGet:
		cognition, err := service.scopedCognition(ctx, scope)
		if err != nil {
			return unavailableProjection(err)
		}
		recovery, recoveryErr := service.capabilities.RecoveryState(
			ctx, *scope.SessionID,
		)
		if recoveryErr != nil {
			return unavailableProjection(recoveryErr)
		}
		return map[string]any{
			"graph":    cognition.TaskGraph,
			"recovery": recovery,
		}
	case controlplane.OperationTaskgraphTodo:
		cognition, err := service.scopedCognition(ctx, scope)
		if err != nil {
			return unavailableProjection(err)
		}
		var todos []any
		for _, node := range cognition.TaskGraph.Nodes {
			if node.Type == "subgoal" && node.Status != "completed" {
				todos = append(todos, node)
			}
		}
		return todos
	case controlplane.OperationSwarmList:
		if service.capabilities != nil {
			sessionID := ""
			if scope.SessionID != nil {
				sessionID = scope.SessionID.String()
			}
			return service.capabilities.SwarmState(sessionID)
		}
		return unavailableStatus("swarm projection is not configured")
	case controlplane.OperationAutomatrixList:
		if service.capabilities != nil {
			return service.capabilities.AutomatrixState(scope.ActorID)
		}
		return unavailableStatus("Automatrix projection is not configured")
	case controlplane.OperationCuriosityTargets:
		if service.capabilities != nil {
			return service.capabilities.CuriosityState()
		}
		return unavailableStatus("curiosity projection is not configured")
	case controlplane.OperationDreamweaverBeliefs:
		return unavailableStatus("Dreamweaver projection is not implemented")
	case controlplane.OperationSkillList:
		if service.capabilities != nil {
			found, err := service.capabilities.SkillList(ctx)
			if err != nil {
				return map[string]any{"status": "unavailable", "reason": err.Error()}
			}
			return found
		}
		return unavailableStatus("skill projection is not configured")
	case controlplane.OperationSkillLifecycle:
		if service.capabilities != nil {
			found, err := service.capabilities.SkillLifecycle(ctx)
			if err != nil {
				return map[string]any{"status": "unavailable", "reason": err.Error()}
			}
			return found
		}
		return unavailableStatus("skill lifecycle projection is not configured")
	case controlplane.OperationSkillGet:
		if service.capabilities != nil {
			return service.capabilities.QueryTool(ctx, scope, "skill_view", payload)
		}
		return unavailableStatus("skill detail projection is not configured")
	case controlplane.OperationPluginList:
		if service.capabilities != nil {
			return service.capabilities.PluginList(ctx)
		}
		return unavailableStatus("plugin projection is not configured")
	case controlplane.OperationMCPServers:
		return unavailableStatus("MCP server projection is not implemented")
	case controlplane.OperationMCPTools:
		if service.capabilities != nil {
			return service.capabilities.MCPTools(ctx)
		}
		return unavailableStatus("MCP tool projection is not configured")
	case controlplane.OperationChannelList:
		if service.capabilities != nil {
			return service.capabilities.ChannelList()
		}
		return unavailableStatus("channel projection is not configured")
	case controlplane.OperationChannelHealth:
		if service.capabilities != nil {
			return service.capabilities.ChannelHealth()
		}
		return unavailableStatus("channel health is not configured")
	case controlplane.OperationScheduleList:
		if service.capabilities != nil {
			return service.capabilities.ScheduleState(scope.ActorID)
		}
		return unavailableStatus("schedule projection is not configured")
	case controlplane.OperationPolicyEvents:
		if service.capabilities == nil {
			return unavailableStatus("policy evidence is not configured")
		}
		events, err := service.capabilities.PolicyEvents(ctx)
		if err != nil {
			return unavailableProjection(err)
		}
		return events
	case controlplane.OperationPolicyExplain:
		return unavailableStatus("policy explanation projection is not implemented")
	case controlplane.OperationReceiptList:
		return unavailableStatus("receipt projection is not implemented")
	case controlplane.OperationReceiptVerify:
		return unavailableStatus("receipt verification projection is not implemented")
	case controlplane.OperationIntegrityLatest:
		if service.capabilities != nil {
			return service.capabilities.IntegrityState()
		}
		return unavailableStatus("integrity projection is not configured")
	case controlplane.OperationIntegrityVerify:
		return unavailableStatus("integrity verification projection is not implemented")
	case controlplane.OperationConfigGet:
		return map[string]any{
			"data_directory":  service.runtime.DataDirectory,
			"secret_values":   "write-only",
			"mutation_status": "unavailable",
		}
	case controlplane.OperationSoulGet:
		if service.capabilities == nil {
			return map[string]any{"status": "unavailable"}
		}
		projection, err := service.capabilities.SoulState(ctx, scope.ActorID)
		if err != nil {
			return unavailableProjection(err)
		}
		return projection
	case controlplane.OperationLivenessGet:
		if service.capabilities == nil {
			return map[string]any{"status": "unavailable"}
		}
		projection, err := service.capabilities.LivingState(ctx, scope)
		if err != nil {
			return unavailableProjection(err)
		}
		return projection
	case controlplane.OperationContinuityBrief:
		continuity, ok := service.capabilities.(interface {
			ContinuityBrief(
				context.Context, controlplane.Scope, json.RawMessage,
			) (any, error)
		})
		if !ok {
			return unavailableStatus("continuity brief is not configured")
		}
		projection, err := continuity.ContinuityBrief(ctx, scope, payload)
		if err != nil {
			return unavailableProjection(err)
		}
		return projection
	case controlplane.OperationWorkBrief,
		controlplane.OperationArtifactList,
		controlplane.OperationAutonomyGet,
		controlplane.OperationWorkflowList,
		controlplane.OperationReviewPlan,
		controlplane.OperationSupervisorList,
		controlplane.OperationSupervisorGet:
		if service.work == nil {
			return map[string]any{"status": "unavailable"}
		}
		projection, err := service.work.WorkQuery(ctx, operation, scope, payload)
		if err != nil {
			return unavailableProjection(err)
		}
		return projection
	case controlplane.OperationProjectList,
		controlplane.OperationProjectGet,
		controlplane.OperationProjectIndexGet,
		controlplane.OperationProjectSearch,
		controlplane.OperationProjectCitationVerify,
		controlplane.OperationProjectPatchHistory,
		controlplane.OperationProjectGitGet,
		controlplane.OperationProjectGitBlame,
		controlplane.OperationProjectGitDiff,
		controlplane.OperationProjectGitReviewGet,
		controlplane.OperationProjectGitReviewComments,
		controlplane.OperationProjectGitRestorePlan,
		controlplane.OperationProjectGitProviderRepos,
		controlplane.OperationProjectGitProviderIssues,
		controlplane.OperationProjectGitProviderChanges,
		controlplane.OperationProjectGitProviderReview,
		controlplane.OperationProjectGitProviderChecks,
		controlplane.OperationProjectGitProviderMerge,
		controlplane.OperationProjectRuntimePlan,
		controlplane.OperationProjectRuntimeList,
		controlplane.OperationProjectRuntimeGet,
		controlplane.OperationProjectRuntimeInspect,
		controlplane.OperationProjectRuntimeProblems,
		controlplane.OperationProjectVerificationGet,
		controlplane.OperationProjectVerificationRuns,
		controlplane.OperationProjectVerificationWaivers,
		controlplane.OperationProjectDeliveryGet,
		controlplane.OperationProjectCIPatchPlan,
		controlplane.OperationProjectToolchainGet,
		controlplane.OperationProjectDependenciesPlan,
		controlplane.OperationProjectTerminalReplay,
		controlplane.OperationWorkspaceCapabilities:
		if service.projects == nil {
			return map[string]any{"status": "unavailable"}
		}
		projection, err := service.projects.ProjectQuery(ctx, operation, scope, payload)
		if err != nil {
			return unavailableProjection(err)
		}
		return projection
	case controlplane.OperationStudioIntentList,
		controlplane.OperationStudioIntentGet,
		controlplane.OperationStudioCompletionCheck,
		controlplane.OperationStudioDriftGet,
		controlplane.OperationStudioContextPlan:
		if service.studio == nil {
			return map[string]any{"status": "unavailable"}
		}
		projection, err := service.studio.StudioQuery(ctx, operation, scope, payload)
		if err != nil {
			return unavailableProjection(err)
		}
		return projection
	case controlplane.OperationLogsQuery:
		return unavailableStatus("log projection is not implemented")
	default:
		return unavailableStatus("operation projection is not implemented")
	}
}

func (service *SurfaceService) scopedCognition(
	ctx context.Context,
	scope controlplane.Scope,
) (cognitionSnapshot, error) {
	if service.capabilities == nil || scope.SessionID == nil {
		return cognitionSnapshot{}, fmt.Errorf(
			"operator surface: scoped cognition is not configured",
		)
	}
	return service.capabilities.CognitionState(ctx, *scope.SessionID)
}

func unavailableProjection(err error) map[string]any {
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{
			"status": "not_available",
			"reason": "no cognition checkpoint exists for this session",
		}
	}
	return map[string]any{"status": "unavailable", "reason": err.Error()}
}

func unavailableStatus(reason string) map[string]any {
	return map[string]any{"status": "unavailable", "reason": reason}
}

func scopeKey(scope controlplane.Scope) string {
	sessionID := ""
	if scope.SessionID != nil {
		sessionID = scope.SessionID.String()
	}
	return fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s",
		scope.ActorID, sessionID, scope.Profile, scope.Channel,
	)
}

func scopeSummary(scope controlplane.Scope) map[string]any {
	summary := map[string]any{
		"actor": scope.ActorID.String(), "profile": scope.Profile, "channel": scope.Channel,
	}
	if scope.SessionID != nil {
		summary["session"] = scope.SessionID.String()
	}
	return summary
}
