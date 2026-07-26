package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type projectEventEmitter struct {
	emitter controlplane.EventEmitter
}

type projectServiceProjection struct {
	service *projectcontrol.Service
	clock   types.Clock
}

type projectPreviewInspector struct {
	browser *nativebrowser.Service
}

func (inspector projectPreviewInspector) InspectProjectPreview(ctx context.Context, rawURL string,
	width, height int64, dark bool) (projectcontrol.RuntimeBrowserSnapshot, error) {
	result, err := inspector.browser.InspectPreview(ctx, rawURL, width, height, dark)
	if err != nil {
		return projectcontrol.RuntimeBrowserSnapshot{}, err
	}
	elements := make([]projectcontrol.RuntimeInspectionElement, 0, len(result.Snapshot.Elements))
	for _, element := range result.Snapshot.Elements {
		elements = append(elements, projectcontrol.RuntimeInspectionElement{Ref: element.Ref, Tag: element.Tag,
			Type: element.Type, Text: element.Text, Name: element.Name, Placeholder: element.Placeholder, Disabled: element.Disabled})
	}
	accessibility := make([]projectcontrol.RuntimeAccessibilityFinding, 0, len(result.Accessibility))
	for _, finding := range result.Accessibility {
		accessibility = append(accessibility, projectcontrol.RuntimeAccessibilityFinding{Ref: finding.Ref,
			Rule: finding.Rule, Message: finding.Message})
	}
	diagnostics := make([]projectcontrol.RuntimeBrowserReport, 0, len(result.Diagnostics))
	for _, diagnostic := range result.Diagnostics {
		diagnostics = append(diagnostics, projectcontrol.RuntimeBrowserReport{Source: diagnostic.Source,
			Severity: diagnostic.Severity, Code: diagnostic.Code, Message: diagnostic.Message,
			Path: diagnostic.Path, Line: diagnostic.Line, Column: diagnostic.Column,
			CausalEvidence: append([]string(nil), diagnostic.Evidence...)})
	}
	return projectcontrol.RuntimeBrowserSnapshot{URL: result.Snapshot.URL, Title: result.Snapshot.Title,
		Text: result.Snapshot.Text, Elements: elements, Accessibility: accessibility,
		ScreenshotPNG: result.ScreenshotPNG, Diagnostics: diagnostics, Width: result.Width,
		Height: result.Height, DarkMode: result.DarkMode}, nil
}

func (emitter projectEventEmitter) EmitProjectEvent(ctx context.Context, event projectcontrol.LifecycleEvent) error {
	eventType := map[string]controlplane.EventType{
		"queued": controlplane.EventWorkspaceQueued, "started": controlplane.EventWorkspaceStarted,
		"progress": controlplane.EventWorkspaceProgress, "completed": controlplane.EventWorkspaceCompleted,
		"failed": controlplane.EventWorkspaceFailed, "cancelled": controlplane.EventWorkspaceCancelled,
	}[event.State]
	if eventType == "" || emitter.emitter == nil {
		return fmt.Errorf("operator projects: invalid lifecycle event")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	correlation := event.CorrelationID
	_, err = emitter.emitter.Emit(ctx, eventType, controlplane.Correlation{
		ActorID: event.ActorID, TaskID: &correlation,
	}, payload)
	return err
}

func (emitter projectEventEmitter) EmitRuntimeEvent(ctx context.Context, event projectcontrol.RuntimeEvent) error {
	if emitter.emitter == nil {
		return fmt.Errorf("operator projects: runtime event emitter is unavailable")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = emitter.emitter.Emit(ctx, controlplane.EventWorkspaceProgress, controlplane.Correlation{
		ActorID: event.State.ActorID, TaskID: &event.State.ID,
	}, payload)
	return err
}

func (capabilities *productionCapabilities) ProjectQuery(ctx context.Context, operation controlplane.Operation,
	scope controlplane.Scope, payload json.RawMessage) (any, error) {
	return queryProjects(ctx, capabilities.projects, operation, scope, payload)
}

func (projection projectServiceProjection) ProjectQuery(ctx context.Context, operation controlplane.Operation,
	scope controlplane.Scope, payload json.RawMessage) (any, error) {
	return queryProjects(ctx, projection.service, operation, scope, payload)
}

func queryProjects(ctx context.Context, projects *projectcontrol.Service, operation controlplane.Operation,
	scope controlplane.Scope, payload json.RawMessage) (any, error) {
	switch operation {
	case controlplane.OperationProjectList:
		found, revision, err := projects.List(ctx, scope.ActorID)
		if err != nil {
			return nil, projectPublicError(err)
		}
		return map[string]any{"revision": revision, "projects": found}, nil
	case controlplane.OperationProjectGet:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		project, err := projects.Get(ctx, scope.ActorID, input.ProjectID)
		if err != nil {
			return nil, projectPublicError(err)
		}
		return project, nil
	case controlplane.OperationProjectIndexGet:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		index, err := projects.ProjectIndex(ctx, scope.ActorID, input.ProjectID)
		if err != nil {
			return nil, projectPublicError(err)
		}
		return index, nil
	case controlplane.OperationProjectSearch:
		var input projectcontrol.SearchRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.SearchProject(ctx, scope.ActorID, input)
		if err != nil {
			return nil, projectPublicError(err)
		}
		return result, nil
	case controlplane.OperationProjectCitationVerify:
		var input struct {
			Citation projectcontrol.Citation `json:"citation"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		if err := projects.VerifyProjectCitation(ctx, scope.ActorID, input.Citation); err != nil {
			return nil, projectPublicError(err)
		}
		return map[string]any{"valid": true, "citation": input.Citation}, nil
	case controlplane.OperationProjectPatchHistory:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.PatchHistory(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectToolchainGet:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.DiscoverToolchain(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectDependenciesPlan:
		var input projectcontrol.DependencyRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PlanDependencies(ctx, scope.ActorID, input)
		return result, projectPublicError(err)
	case controlplane.OperationProjectTerminalReplay:
		var input struct {
			TerminalID uuid.UUID `json:"terminal_id"`
			Cursor     uint64    `json:"cursor"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.TerminalID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid terminal_id is required"}
		}
		result, err := projects.ReplayTerminal(ctx, scope.ActorID, input.TerminalID, input.Cursor)
		return result, projectPublicError(err)
	case controlplane.OperationProjectGitGet:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.GitProjection(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectGitBlame:
		var input projectcontrol.GitBlameRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.GitBlame(ctx, scope.ActorID, input)
		return result, projectPublicError(err)
	case controlplane.OperationProjectGitDiff:
		var input projectcontrol.GitDiffRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.GitUnstagedDiff(ctx, scope.ActorID, input)
		return result, projectPublicError(err)
	case controlplane.OperationProjectGitReviewGet:
		var input projectcontrol.GitReviewRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.BuildGitReview(ctx, scope.ActorID, input)
		return result, projectPublicError(err)
	case controlplane.OperationProjectGitReviewComments:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.ListGitReviewComments(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectGitRestorePlan:
		var input projectcontrol.GitRestorePlanRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PlanGitRestore(ctx, scope.ActorID, input)
		return result, projectPublicError(err)
	case controlplane.OperationProjectGitProviderRepos,
		controlplane.OperationProjectGitProviderIssues,
		controlplane.OperationProjectGitProviderChanges,
		controlplane.OperationProjectGitProviderReview,
		controlplane.OperationProjectGitProviderChecks,
		controlplane.OperationProjectGitProviderMerge:
		var input projectcontrol.ProviderQueryRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		kind := map[controlplane.Operation]string{
			controlplane.OperationProjectGitProviderRepos:   "repositories",
			controlplane.OperationProjectGitProviderIssues:  "issues",
			controlplane.OperationProjectGitProviderChanges: "changes",
			controlplane.OperationProjectGitProviderReview:  "review",
			controlplane.OperationProjectGitProviderChecks:  "checks",
			controlplane.OperationProjectGitProviderMerge:   "mergeability",
		}[operation]
		result, err := projects.ProviderQuery(ctx, scope.ActorID, input, kind)
		return result, projectPublicError(err)
	case controlplane.OperationProjectRuntimePlan:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.RuntimePlan(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectRuntimeList:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.ListRuntimes(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectRuntimeGet:
		var input projectcontrol.RuntimeControlRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.GetRuntime(ctx, scope.ActorID, input.ProjectID, input.Name)
		return result, projectPublicError(err)
	case controlplane.OperationProjectRuntimeInspect:
		var input projectcontrol.RuntimeInspectRequest
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		// Runtime inspection enters the actor-isolated native browser. Queries
		// carry authenticated scope in the request envelope, but unlike turn
		// execution they do not already have an ApprovalScope in context.
		// Propagate the exact actor/session boundary so Studio preview capture
		// cannot fall back to an unscoped browser profile.
		inspectCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
			ActorID: scope.ActorID, SessionID: scope.SessionID,
		})
		result, err := projects.InspectRuntime(inspectCtx, scope.ActorID, input)
		return result, projectPublicError(err)
	case controlplane.OperationProjectRuntimeProblems:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.RuntimeProblems(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectVerificationGet:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.CurrentVerificationManifest(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectVerificationRuns:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.ListVerificationRuns(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectVerificationWaivers:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.ListVerificationWaivers(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectDeliveryGet:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.DeliverySnapshot(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationProjectCIPatchPlan:
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		result, err := projects.PrepareCIPatch(ctx, scope.ActorID, input.ProjectID)
		return result, projectPublicError(err)
	case controlplane.OperationWorkspaceCapabilities:
		return map[string]any{"contract_version": projectcontrol.WorkspaceHostVersion,
			"hosts": projects.Capabilities(ctx)}, nil
	default:
		return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "unsupported project query"}
	}
}

type projectOperationFields struct {
	Deadline          *time.Time                   `json:"deadline,omitempty"`
	CorrelationID     uuid.UUID                    `json:"correlation_id,omitempty"`
	WorkspaceRevision *uint64                      `json:"workspace_revision,omitempty"`
	SecretGrants      []projectcontrol.SecretGrant `json:"secret_grants,omitempty"`
}

func (capabilities *productionCapabilities) ProjectCommand(ctx context.Context,
	request controlplane.Request) (any, error) {
	return commandProjects(ctx, capabilities.projects, capabilities.clock, request)
}

func (projection projectServiceProjection) ProjectCommand(ctx context.Context,
	request controlplane.Request) (any, error) {
	return commandProjects(ctx, projection.service, projection.clock, request)
}

func commandProjects(ctx context.Context, projects *projectcontrol.Service, clock types.Clock,
	request controlplane.Request) (any, error) {
	if _, scoped := controlplane.ApprovalScopeFromContext(ctx); !scoped &&
		(request.Operation == controlplane.OperationProjectProcessStart ||
			request.Operation == controlplane.OperationProjectVerificationRun ||
			request.Operation == controlplane.OperationProjectTerminalInput ||
			request.Operation == controlplane.OperationProjectTerminalResize ||
			request.Operation == controlplane.OperationProjectTerminalSignal ||
			request.Operation == controlplane.OperationProjectTerminalCancel) {
		ctx = controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
			ActorID: request.Scope.ActorID, SessionID: request.Scope.SessionID,
			AgentID: "operator",
		})
	}
	if request.Operation == controlplane.OperationProjectIndexRefresh {
		var input projectcontrol.RefreshInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		index, err := projects.RefreshIndex(ctx, request.Scope.ActorID, input)
		if err != nil {
			return nil, projectPublicError(err)
		}
		return index, nil
	}
	if request.Operation == controlplane.OperationProjectPatchApply {
		var input projectcontrol.PatchSet
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		principal := policy.PrincipalFromContext(ctx)
		result, err := projects.ApplyPatchSetApproved(ctx, request.Scope.ActorID, input, principal.Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectPatchRollback {
		var input projectcontrol.PatchRollbackRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.RollbackPatchSet(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectVerificationDerive {
		var input projectcontrol.VerificationManifestInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.DeriveVerificationManifest(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectVerificationRun {
		var input projectcontrol.VerificationRunRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.RunVerification(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectVerificationWaiver {
		var input projectcontrol.VerificationWaiverInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PutVerificationWaiver(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectResourcePlan {
		var input projectcontrol.ResourcePlanInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PlanResource(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectResourceApply {
		var input projectcontrol.ResourceApplyInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		input.IdempotencyKey = request.IdempotencyKey
		result, err := projects.ApplyResource(ctx, request.Scope.ActorID, input,
			policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectEnvironmentPut {
		var input projectcontrol.EnvironmentSchemaInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PutEnvironmentSchema(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectMigrationPlan {
		var input projectcontrol.MigrationPlanInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PlanMigration(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectMigrationApply {
		var input projectcontrol.MigrationApplyInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.ApplyMigration(ctx, request.Scope.ActorID, input,
			policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectMigrationRollback {
		var input projectcontrol.MigrationRollbackInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.RollbackMigration(ctx, request.Scope.ActorID, input,
			policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectDeploymentPlan {
		var input projectcontrol.DeploymentPlanInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PlanDeployment(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectDeploymentApply {
		var input projectcontrol.DeploymentApplyInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		input.IdempotencyKey = request.IdempotencyKey
		result, err := projects.ApplyDeployment(ctx, request.Scope.ActorID, input,
			policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectDeploymentReconcile {
		var input projectcontrol.DeploymentReconcileInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.ReconcileDeployment(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectDeploymentRollback {
		var input projectcontrol.DeploymentRollbackInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.RollbackDeployment(ctx, request.Scope.ActorID, input,
			policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectReleasePrepare {
		var input projectcontrol.ReleaseInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PrepareRelease(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectPortableExport {
		var input struct {
			ProjectID uuid.UUID `json:"project_id"`
		}
		if err := decodeStrictJSON(request.Payload, &input); err != nil || input.ProjectID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid project_id is required"}
		}
		path, err := projects.PortableExport(ctx, request.Scope.ActorID, input.ProjectID)
		if err != nil {
			return nil, projectPublicError(err)
		}
		return map[string]any{"path": path}, nil
	}
	if request.Operation == controlplane.OperationProjectGitPreviewStart {
		var input projectcontrol.GitPreviewRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.StartGitPreview(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitPreviewClose {
		var input projectcontrol.GitPreviewCloseRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.CloseGitPreview(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitReviewComment {
		var input projectcontrol.GitReviewCommentInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.AddGitReviewComment(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitReviewResolve {
		var input projectcontrol.GitReviewResolveInput
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.ResolveGitReviewComment(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitBranchCreate {
		var input projectcontrol.GitBranchCreateRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.CreateGitBranch(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitStage {
		var input projectcontrol.GitStageRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.StageGitPaths(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitStageHunks {
		var input projectcontrol.GitStageHunksRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.StageGitHunks(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitCommit {
		var input projectcontrol.GitCommitRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.CommitGitPaths(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitCheckpoint {
		var input projectcontrol.GitCheckpointRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.CreateGitCheckpoint(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitTagCreate {
		var input projectcontrol.GitTagRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.CreateGitTag(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitSync {
		var input projectcontrol.GitSyncRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.SyncGitRemote(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitPull {
		var input projectcontrol.GitPullRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PullGitRemote(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitMerge {
		var input projectcontrol.GitMergeRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.MergeGitRevision(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitPush ||
		request.Operation == controlplane.OperationProjectGitForceWithLease {
		var input projectcontrol.GitPushRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.PushGitRemote(ctx, request.Scope.ActorID, input,
			request.Operation == controlplane.OperationProjectGitForceWithLease, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectGitProviderGrant {
		var input projectcontrol.RepositoryGrantRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		grant, err := projects.IssueRepositoryGrant(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		if err != nil {
			return nil, projectPublicError(err)
		}
		return map[string]any{"permission_grant": grant, "expires_in_seconds": input.TTLSeconds}, nil
	}
	if request.Operation == controlplane.OperationProjectGitProviderDraft {
		var input projectcontrol.ProviderDraftRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.CreateProviderDraft(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectRuntimeStart {
		var input projectcontrol.RuntimeStartRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.StartRuntime(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectRuntimePhase {
		var input projectcontrol.RuntimePhaseRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.RunRuntimePhase(ctx, request.Scope.ActorID, input, policy.PrincipalFromContext(ctx).Approved)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectRuntimeReload ||
		request.Operation == controlplane.OperationProjectRuntimeRestart ||
		request.Operation == controlplane.OperationProjectRuntimeStop {
		var input projectcontrol.RuntimeControlRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		var result projectcontrol.RuntimeState
		var err error
		switch request.Operation {
		case controlplane.OperationProjectRuntimeReload:
			result, err = projects.ReloadRuntime(ctx, request.Scope.ActorID, input)
		case controlplane.OperationProjectRuntimeRestart:
			result, err = projects.RestartRuntime(ctx, request.Scope.ActorID, input)
		case controlplane.OperationProjectRuntimeStop:
			result, err = projects.StopRuntime(ctx, request.Scope.ActorID, input)
		}
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectRuntimeReport {
		var input projectcontrol.RuntimeBrowserReport
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.ReportRuntimeDiagnostic(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectRuntimeAnnotate {
		var input projectcontrol.RuntimeAnnotationRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.AnnotateRuntime(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectRuntimeStylePropose {
		var input projectcontrol.RuntimeStyleProposalRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.ProposeRuntimeStyle(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectProcessStart {
		var input projectcontrol.ProcessRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		result, err := projects.StartProcess(ctx, request.Scope.ActorID, input)
		return result, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectDependenciesInstall {
		var input projectcontrol.DependencyRequest
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		principal := policy.PrincipalFromContext(ctx)
		terminal, plan, err := projects.InstallDependencies(ctx, request.Scope.ActorID, input, principal.Approved)
		return map[string]any{"terminal": terminal, "plan": plan}, projectPublicError(err)
	}
	if request.Operation == controlplane.OperationProjectTerminalInput ||
		request.Operation == controlplane.OperationProjectTerminalResize ||
		request.Operation == controlplane.OperationProjectTerminalSignal ||
		request.Operation == controlplane.OperationProjectTerminalCancel {
		var input struct {
			TerminalID    uuid.UUID `json:"terminal_id"`
			LeaseID       uuid.UUID `json:"lease_id,omitempty"`
			LeaseRevision uint64    `json:"lease_revision,omitempty"`
			Input         string    `json:"input,omitempty"`
			Columns       uint16    `json:"columns,omitempty"`
			Rows          uint16    `json:"rows,omitempty"`
			Signal        string    `json:"signal,omitempty"`
		}
		if err := decodeStrictJSON(request.Payload, &input); err != nil || input.TerminalID == uuid.Nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "a valid terminal_id is required"}
		}
		var err error
		requiresLease := projects.TerminalControlRequired()
		if requiresLease && (input.LeaseID == uuid.Nil || input.LeaseRevision == 0) {
			return nil, controlplane.PublicError{
				Code:    controlplane.ErrorUnauthorized,
				Message: "acquire the exact terminal control lease before sending input",
			}
		}
		switch request.Operation {
		case controlplane.OperationProjectTerminalInput:
			if requiresLease {
				err = projects.WriteTerminalWithLease(
					ctx, request.Scope.ActorID, input.TerminalID,
					input.LeaseID, input.LeaseRevision, []byte(input.Input),
				)
			} else {
				err = projects.WriteTerminal(ctx, request.Scope.ActorID, input.TerminalID, []byte(input.Input))
			}
		case controlplane.OperationProjectTerminalResize:
			if requiresLease {
				err = projects.ResizeTerminalWithLease(
					ctx, request.Scope.ActorID, input.TerminalID,
					input.LeaseID, input.LeaseRevision, input.Columns, input.Rows,
				)
			} else {
				err = projects.ResizeTerminal(ctx, request.Scope.ActorID, input.TerminalID, input.Columns, input.Rows)
			}
		case controlplane.OperationProjectTerminalCancel:
			if requiresLease {
				err = projects.CancelTerminalWithLease(
					ctx, request.Scope.ActorID, input.TerminalID,
					input.LeaseID, input.LeaseRevision,
				)
			} else {
				err = projects.CancelTerminal(ctx, request.Scope.ActorID, input.TerminalID)
			}
		case controlplane.OperationProjectTerminalSignal:
			signal := map[string]syscall.Signal{"INT": syscall.SIGINT, "TERM": syscall.SIGTERM,
				"KILL": syscall.SIGKILL, "HUP": syscall.SIGHUP}[strings.ToUpper(strings.TrimPrefix(input.Signal, "SIG"))]
			if signal == 0 {
				return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "signal must be INT, TERM, KILL, or HUP"}
			}
			if requiresLease {
				err = projects.SignalTerminalWithLease(
					ctx, request.Scope.ActorID, input.TerminalID,
					input.LeaseID, input.LeaseRevision, signal,
				)
			} else {
				err = projects.SignalTerminal(ctx, request.Scope.ActorID, input.TerminalID, signal)
			}
		}
		if err != nil {
			return nil, projectPublicError(err)
		}
		return map[string]any{"accepted": true, "terminal_id": input.TerminalID}, nil
	}
	now := clock.Now().UTC()
	classification := projectcontrol.PolicyYellow
	var fields projectOperationFields
	var execute func(projectcontrol.OperationMeta) (projectcontrol.Project, error)
	switch request.Operation {
	case controlplane.OperationProjectCreate:
		var input struct {
			projectOperationFields
			Name     string                    `json:"name"`
			Template string                    `json:"template"`
			Host     projectcontrol.HostKind   `json:"host"`
			Trust    projectcontrol.TrustState `json:"trust"`
		}
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		fields = input.projectOperationFields
		execute = func(meta projectcontrol.OperationMeta) (projectcontrol.Project, error) {
			return projects.CreateTemplate(ctx, meta, projectcontrol.TemplateInput{
				Name: input.Name, Template: input.Template, Host: input.Host, Trust: input.Trust})
		}
	case controlplane.OperationProjectImport:
		var input struct {
			projectOperationFields
			Name        string                    `json:"name"`
			ArchivePath string                    `json:"archive_path"`
			Host        projectcontrol.HostKind   `json:"host"`
			Trust       projectcontrol.TrustState `json:"trust"`
		}
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		fields = input.projectOperationFields
		execute = func(meta projectcontrol.OperationMeta) (projectcontrol.Project, error) {
			return projects.ImportArchive(ctx, meta, projectcontrol.ArchiveInput{
				Name: input.Name, ArchivePath: input.ArchivePath, Host: input.Host, Trust: input.Trust})
		}
	case controlplane.OperationProjectAttach:
		var input struct {
			projectOperationFields
			Name      string                    `json:"name"`
			Directory string                    `json:"directory"`
			Trust     projectcontrol.TrustState `json:"trust"`
		}
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		fields = input.projectOperationFields
		execute = func(meta projectcontrol.OperationMeta) (projectcontrol.Project, error) {
			return projects.AttachDirectory(ctx, meta, projectcontrol.AttachInput{
				Name: input.Name, Directory: input.Directory, Trust: input.Trust})
		}
	case controlplane.OperationProjectClone:
		var input struct {
			projectOperationFields
			Name                string                    `json:"name"`
			RepositoryURL       string                    `json:"repository_url"`
			DefaultBranch       string                    `json:"default_branch,omitempty"`
			CredentialReference string                    `json:"credential_reference,omitempty"`
			Authorized          bool                      `json:"authorized"`
			Host                projectcontrol.HostKind   `json:"host"`
			Trust               projectcontrol.TrustState `json:"trust"`
		}
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		fields = input.projectOperationFields
		execute = func(meta projectcontrol.OperationMeta) (projectcontrol.Project, error) {
			return projects.CloneRepository(ctx, meta, projectcontrol.CloneInput{
				Name: input.Name, RepositoryURL: input.RepositoryURL, DefaultBranch: input.DefaultBranch,
				CredentialReference: input.CredentialReference, Authorized: input.Authorized,
				Host: input.Host, Trust: input.Trust})
		}
	case controlplane.OperationWorkspaceLifecycle:
		var input struct {
			projectOperationFields
			ProjectID               uuid.UUID                    `json:"project_id"`
			Operation               projectcontrol.HostOperation `json:"operation"`
			UncommittedWorkDecision string                       `json:"uncommitted_work_decision,omitempty"`
		}
		if err := decodeStrictJSON(request.Payload, &input); err != nil {
			return nil, err
		}
		fields = input.projectOperationFields
		if fields.WorkspaceRevision == nil {
			return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid,
				Message: "workspace_revision is required for lifecycle mutations"}
		}
		if input.Operation == projectcontrol.HostDestroy {
			classification = projectcontrol.PolicyRed
		}
		execute = func(meta projectcontrol.OperationMeta) (projectcontrol.Project, error) {
			return projects.Lifecycle(ctx, meta, projectcontrol.LifecycleInput{
				ProjectID: input.ProjectID, Operation: input.Operation,
				UncommittedWorkDecision: input.UncommittedWorkDecision})
		}
	default:
		return nil, controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "unsupported project command"}
	}
	deadline := now.Add(30 * time.Minute)
	if fields.Deadline != nil {
		deadline = fields.Deadline.UTC()
	}
	correlation := fields.CorrelationID
	if correlation == uuid.Nil {
		correlation = request.RequestID
	}
	project, err := execute(projectcontrol.OperationMeta{ActorID: request.Scope.ActorID,
		RequestID: request.RequestID, IdempotencyKey: request.IdempotencyKey,
		PolicyClassification: classification, Deadline: deadline,
		CorrelationID: correlation, ExpectedRevision: fields.WorkspaceRevision,
		SecretGrants: fields.SecretGrants})
	if err != nil {
		return nil, projectPublicError(err)
	}
	return project, nil
}

func projectPublicError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, projectcontrol.ErrNotFound):
		return controlplane.PublicError{Code: controlplane.ErrorNotFound, Message: "project was not found"}
	case errors.Is(err, projectcontrol.ErrStaleRevision), errors.Is(err, projectcontrol.ErrConflict):
		return controlplane.PublicError{Code: controlplane.ErrorConflict, Message: "project revision or idempotency conflict"}
	case errors.Is(err, projectcontrol.ErrStaleIndex), errors.Is(err, projectcontrol.ErrStaleCitation):
		return controlplane.PublicError{Code: controlplane.ErrorConflict, Message: "project index or citation is stale; refresh the index before continuing"}
	case errors.Is(err, projectcontrol.ErrIndexNotFound):
		return controlplane.PublicError{Code: controlplane.ErrorNotFound, Message: "project index is not available; refresh it first"}
	case errors.Is(err, projectcontrol.ErrProtectedPath):
		return controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: "project path is protected from indexing and model context"}
	case errors.Is(err, projectcontrol.ErrStalePreimage), errors.Is(err, projectcontrol.ErrPatchPending):
		return controlplane.PublicError{Code: controlplane.ErrorConflict, Message: err.Error()}
	case errors.Is(err, projectcontrol.ErrTerminalNotFound):
		return controlplane.PublicError{Code: controlplane.ErrorNotFound, Message: "terminal session was not found"}
	case errors.Is(err, controllease.ErrStale):
		return controlplane.PublicError{Code: controlplane.ErrorConflict, Message: "terminal control lease is stale"}
	case errors.Is(err, controllease.ErrHeld), errors.Is(err, controllease.ErrConflict):
		return controlplane.PublicError{Code: controlplane.ErrorConflict, Message: "terminal authority changed before this action"}
	case errors.Is(err, controllease.ErrUnauthorized), errors.Is(err, controllease.ErrNotFound):
		return controlplane.PublicError{Code: controlplane.ErrorUnauthorized, Message: "terminal control lease is not authorized"}
	case errors.Is(err, projectcontrol.ErrUnsupported):
		return controlplane.PublicError{Code: controlplane.ErrorUnavailable, Message: err.Error(), Retryable: false}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return controlplane.PublicError{Code: controlplane.ErrorUnavailable, Message: "workspace operation was cancelled or timed out", Retryable: true}
	default:
		return controlplane.PublicError{Code: controlplane.ErrorInvalid, Message: err.Error()}
	}
}
