package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

// SubsystemService is the narrow application-service boundary used to expose
// primary Ion subsystems without giving clients direct storage access.
// Implementations own domain authorization, validation, persistence, and
// secret handling; the dispatcher adds scope, revision, idempotency, redaction,
// durable events, and audit evidence.
type SubsystemService interface {
	Query(context.Context, controlplane.Request) (json.RawMessage, error)
	Command(context.Context, controlplane.Request) (json.RawMessage, error)
}

// MutationAuthorizer applies the production policy boundary before a
// subsystem command is allowed to reach its application service.
type MutationAuthorizer interface {
	AuthorizeMutation(context.Context, controlplane.Request) error
}

// MutationAuthorizerFunc adapts a function to MutationAuthorizer.
type MutationAuthorizerFunc func(context.Context, controlplane.Request) error

// AuthorizeMutation invokes the adapted policy boundary.
func (authorizer MutationAuthorizerFunc) AuthorizeMutation(
	ctx context.Context,
	request controlplane.Request,
) error {
	return authorizer(ctx, request)
}

var subsystemOperations = []controlplane.Operation{
	controlplane.OperationSystemHealth,
	controlplane.OperationSystemMetrics,
	controlplane.OperationProviderList,
	controlplane.OperationProviderUsage,
	controlplane.OperationToolSurface,
	controlplane.OperationToolReadiness,
	controlplane.OperationToolInvoke,
	controlplane.OperationBrowserWorkflowList,
	controlplane.OperationBrowserWorkflowPause,
	controlplane.OperationBrowserWorkflowResume,
	controlplane.OperationBrowserWorkflowCancel,
	controlplane.OperationBrowserWorkflowHandoff,
	controlplane.OperationBrowserCredentialList,
	controlplane.OperationMemorySearch,
	controlplane.OperationMemoryGet,
	controlplane.OperationMemoryGraph,
	controlplane.OperationMemoryActivation,
	controlplane.OperationMemoryPin,
	controlplane.OperationMemoryRecover,
	controlplane.OperationPremiseList,
	controlplane.OperationCitationVerify,
	controlplane.OperationPredictionList,
	controlplane.OperationTaskgraphGet,
	controlplane.OperationTaskgraphTodo,
	controlplane.OperationSwarmList,
	controlplane.OperationSwarmAbort,
	controlplane.OperationAutomatrixList,
	controlplane.OperationAutomatrixApprove,
	controlplane.OperationAutomatrixReject,
	controlplane.OperationCuriosityTargets,
	controlplane.OperationDreamweaverBeliefs,
	controlplane.OperationSkillList,
	controlplane.OperationSkillLifecycle,
	controlplane.OperationSkillGet,
	controlplane.OperationSkillSave,
	controlplane.OperationSkillRefine,
	controlplane.OperationSkillRollback,
	controlplane.OperationPluginList,
	controlplane.OperationMCPServers,
	controlplane.OperationMCPTools,
	controlplane.OperationChannelList,
	controlplane.OperationChannelHealth,
	controlplane.OperationChannelRetry,
	controlplane.OperationChannelSkip,
	controlplane.OperationScheduleList,
	controlplane.OperationScheduleUpdate,
	controlplane.OperationPolicyEvents,
	controlplane.OperationPolicyExplain,
	controlplane.OperationReceiptList,
	controlplane.OperationReceiptVerify,
	controlplane.OperationIntegrityLatest,
	controlplane.OperationIntegrityVerify,
	controlplane.OperationIntegrityRun,
	controlplane.OperationConfigGet,
	controlplane.OperationSoulGet,
	controlplane.OperationSoulPropose,
	controlplane.OperationLivenessGet,
	controlplane.OperationContinuityBrief,
	controlplane.OperationRelationshipUpdate,
	controlplane.OperationWorkBrief,
	controlplane.OperationWorkContractPut,
	controlplane.OperationWorkComplete,
	controlplane.OperationArtifactList,
	controlplane.OperationArtifactRecord,
	controlplane.OperationArtifactVerify,
	controlplane.OperationAutonomyGet,
	controlplane.OperationAutonomyUpdate,
	controlplane.OperationWorkflowList,
	controlplane.OperationWorkflowStart,
	controlplane.OperationWorkflowAdvance,
	controlplane.OperationReviewPlan,
	controlplane.OperationSupervisorList,
	controlplane.OperationSupervisorGet,
	controlplane.OperationSupervisorStart,
	controlplane.OperationSupervisorSteer,
	controlplane.OperationSupervisorCancel,
	controlplane.OperationProjectList,
	controlplane.OperationProjectGet,
	controlplane.OperationProjectCreate,
	controlplane.OperationProjectImport,
	controlplane.OperationProjectAttach,
	controlplane.OperationProjectClone,
	controlplane.OperationProjectIndexGet,
	controlplane.OperationProjectIndexRefresh,
	controlplane.OperationProjectSearch,
	controlplane.OperationProjectCitationVerify,
	controlplane.OperationProjectPatchApply,
	controlplane.OperationProjectPatchHistory,
	controlplane.OperationProjectPatchRollback,
	controlplane.OperationProjectToolchainGet,
	controlplane.OperationProjectDependenciesPlan,
	controlplane.OperationProjectDependenciesInstall,
	controlplane.OperationProjectProcessStart,
	controlplane.OperationProjectTerminalReplay,
	controlplane.OperationProjectTerminalInput,
	controlplane.OperationProjectTerminalResize,
	controlplane.OperationProjectTerminalSignal,
	controlplane.OperationProjectTerminalCancel,
	controlplane.OperationProjectGitGet,
	controlplane.OperationProjectGitBlame,
	controlplane.OperationProjectGitDiff,
	controlplane.OperationProjectGitPreviewStart,
	controlplane.OperationProjectGitPreviewClose,
	controlplane.OperationProjectGitReviewGet,
	controlplane.OperationProjectGitReviewComments,
	controlplane.OperationProjectGitReviewComment,
	controlplane.OperationProjectGitReviewResolve,
	controlplane.OperationProjectGitBranchCreate,
	controlplane.OperationProjectGitStage,
	controlplane.OperationProjectGitStageHunks,
	controlplane.OperationProjectGitCommit,
	controlplane.OperationProjectGitCheckpoint,
	controlplane.OperationProjectGitTagCreate,
	controlplane.OperationProjectGitRestorePlan,
	controlplane.OperationProjectGitSync,
	controlplane.OperationProjectGitPull,
	controlplane.OperationProjectGitMerge,
	controlplane.OperationProjectGitPush,
	controlplane.OperationProjectGitForceWithLease,
	controlplane.OperationProjectGitProviderGrant,
	controlplane.OperationProjectGitProviderRepos,
	controlplane.OperationProjectGitProviderIssues,
	controlplane.OperationProjectGitProviderChanges,
	controlplane.OperationProjectGitProviderReview,
	controlplane.OperationProjectGitProviderChecks,
	controlplane.OperationProjectGitProviderMerge,
	controlplane.OperationProjectGitProviderDraft,
	controlplane.OperationProjectRuntimePlan,
	controlplane.OperationProjectRuntimeList,
	controlplane.OperationProjectRuntimeGet,
	controlplane.OperationProjectRuntimeStart,
	controlplane.OperationProjectRuntimePhase,
	controlplane.OperationProjectRuntimeReload,
	controlplane.OperationProjectRuntimeRestart,
	controlplane.OperationProjectRuntimeStop,
	controlplane.OperationProjectRuntimeReport,
	controlplane.OperationProjectRuntimeInspect,
	controlplane.OperationProjectRuntimeProblems,
	controlplane.OperationProjectRuntimeAnnotate,
	controlplane.OperationProjectRuntimeStylePropose,
	controlplane.OperationProjectVerificationDerive,
	controlplane.OperationProjectVerificationGet,
	controlplane.OperationProjectVerificationRun,
	controlplane.OperationProjectVerificationRuns,
	controlplane.OperationProjectVerificationWaiver,
	controlplane.OperationProjectVerificationWaivers,
	controlplane.OperationProjectDeliveryGet,
	controlplane.OperationProjectResourcePlan,
	controlplane.OperationProjectResourceApply,
	controlplane.OperationProjectEnvironmentPut,
	controlplane.OperationProjectMigrationPlan,
	controlplane.OperationProjectMigrationApply,
	controlplane.OperationProjectMigrationRollback,
	controlplane.OperationProjectDeploymentPlan,
	controlplane.OperationProjectDeploymentApply,
	controlplane.OperationProjectDeploymentReconcile,
	controlplane.OperationProjectDeploymentRollback,
	controlplane.OperationProjectCIPatchPlan,
	controlplane.OperationProjectReleasePrepare,
	controlplane.OperationProjectPortableExport,
	controlplane.OperationWorkspaceCapabilities,
	controlplane.OperationWorkspaceLifecycle,
	controlplane.OperationStudioIntentList,
	controlplane.OperationStudioIntentGet,
	controlplane.OperationStudioIntentCompile,
	controlplane.OperationStudioScopePropose,
	controlplane.OperationStudioProposalDecide,
	controlplane.OperationStudioProposalApply,
	controlplane.OperationStudioCorrelationRecord,
	controlplane.OperationStudioCompletionCheck,
	controlplane.OperationStudioDriftGet,
	controlplane.OperationStudioContextPlan,
	controlplane.OperationLogsQuery,
}

// RegisterSubsystemHandlers binds the complete Task 9.3 catalog to a
// production service and policy boundary. Session, turn, approval, replay,
// snapshot, and catalog operations retain their specialized adapters.
func RegisterSubsystemHandlers(
	dispatcher *controlplane.Dispatcher,
	service SubsystemService,
	authorizer MutationAuthorizer,
) error {
	if dispatcher == nil || service == nil || authorizer == nil {
		return fmt.Errorf(
			"controlplane adapters: dispatcher, subsystem service, and mutation authorizer are required",
		)
	}
	for _, operation := range subsystemOperations {
		kind, _ := operation.Kind()
		handler := controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			if request.Kind == controlplane.KindQuery {
				return service.Query(ctx, request)
			}
			if err := authorizer.AuthorizeMutation(ctx, request); err != nil {
				return nil, err
			}
			return service.Command(ctx, request)
		})
		description := "Read the production " + string(operation) + " boundary."
		if kind == controlplane.KindCommand {
			description = "Mutate " + string(operation) +
				" through production policy, revision, and audit boundaries."
		}
		if err := dispatcher.Register(operation, description, handler); err != nil {
			return err
		}
	}
	return nil
}
