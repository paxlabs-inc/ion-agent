// Package controlplane defines the single versioned operator protocol used by
// browser, terminal, and future clients.
package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// ProtocolVersion is included in every request and response.
	ProtocolVersion = "ion.controlplane.v1"
	// MaximumPayloadBytes is the protocol-wide decoded JSON payload bound.
	MaximumPayloadBytes = 1 << 20
)

var (
	// ErrInvalidProtocol indicates a malformed or unsupported wire envelope.
	ErrInvalidProtocol = errors.New("controlplane: invalid protocol message")
	// ErrConflict indicates an idempotency or optimistic revision conflict.
	ErrConflict = errors.New("controlplane: conflict")
	// ErrUnauthorized indicates missing or invalid transport authorization.
	ErrUnauthorized = errors.New("controlplane: unauthorized")
)

// RequestKind separates read-only queries from state-changing commands.
type RequestKind string

const (
	KindQuery   RequestKind = "query"
	KindCommand RequestKind = "command"
)

// Operation is a closed command/query name from the shared catalog.
type Operation string

const (
	OperationSystemSnapshot             Operation = "system.snapshot"
	OperationSystemHealth               Operation = "system.health"
	OperationSystemMetrics              Operation = "system.metrics"
	OperationSessionList                Operation = "session.list"
	OperationSessionCreate              Operation = "session.create"
	OperationSessionResume              Operation = "session.resume"
	OperationSessionBranch              Operation = "session.branch"
	OperationSessionClose               Operation = "session.close"
	OperationSessionRename              Operation = "session.rename"
	OperationSessionArchive             Operation = "session.archive"
	OperationSessionDelete              Operation = "session.delete"
	OperationSessionExport              Operation = "session.export"
	OperationTurnSubmit                 Operation = "turn.submit"
	OperationTurnSteer                  Operation = "turn.steer"
	OperationTurnCancel                 Operation = "turn.cancel"
	OperationTurnRetry                  Operation = "turn.retry"
	OperationComputerControlGet         Operation = "computer.control.get"
	OperationComputerControlAcquire     Operation = "computer.control.acquire"
	OperationComputerControlRenew       Operation = "computer.control.renew"
	OperationComputerControlRelease     Operation = "computer.control.release"
	OperationComputerBrowserObserve     Operation = "computer.browser.observe"
	OperationComputerBrowserNavigate    Operation = "computer.browser.navigate"
	OperationComputerBrowserInteract    Operation = "computer.browser.interact"
	OperationComputerBrowserSubmit      Operation = "computer.browser.submit"
	OperationBrowserWorkflowList        Operation = "browser.workflow.list"
	OperationBrowserWorkflowPause       Operation = "browser.workflow.pause"
	OperationBrowserWorkflowResume      Operation = "browser.workflow.resume"
	OperationBrowserWorkflowCancel      Operation = "browser.workflow.cancel"
	OperationBrowserWorkflowHandoff     Operation = "browser.workflow.handoff"
	OperationBrowserCredentialList      Operation = "browser.credential.list"
	OperationApprovalRespond            Operation = "approval.respond"
	OperationProviderList               Operation = "provider.list"
	OperationProviderUsage              Operation = "provider.usage"
	OperationToolSurface                Operation = "tool.surface"
	OperationToolReadiness              Operation = "tool.readiness"
	OperationToolInvoke                 Operation = "tool.invoke"
	OperationMemorySearch               Operation = "memory.search"
	OperationMemoryGet                  Operation = "memory.get"
	OperationMemoryGraph                Operation = "memory.graph"
	OperationMemoryActivation           Operation = "memory.activation"
	OperationMemoryPin                  Operation = "memory.pin"
	OperationMemoryRecover              Operation = "memory.recover"
	OperationPremiseList                Operation = "premise.list"
	OperationCitationVerify             Operation = "citation.verify"
	OperationPredictionList             Operation = "prediction.list"
	OperationTaskgraphGet               Operation = "taskgraph.get"
	OperationTaskgraphTodo              Operation = "taskgraph.todo"
	OperationSwarmList                  Operation = "swarm.list"
	OperationSwarmAbort                 Operation = "swarm.abort"
	OperationAutomatrixList             Operation = "automatrix.list"
	OperationAutomatrixApprove          Operation = "automatrix.approve"
	OperationAutomatrixReject           Operation = "automatrix.reject"
	OperationCuriosityTargets           Operation = "curiosity.targets"
	OperationDreamweaverBeliefs         Operation = "dreamweaver.beliefs"
	OperationSkillList                  Operation = "skill.list"
	OperationSkillLifecycle             Operation = "skill.lifecycle"
	OperationSkillGet                   Operation = "skill.get"
	OperationSkillSave                  Operation = "skill.save"
	OperationSkillRefine                Operation = "skill.refine"
	OperationSkillRollback              Operation = "skill.rollback"
	OperationPluginList                 Operation = "plugin.list"
	OperationPluginLifecycle            Operation = "plugin.lifecycle"
	OperationMCPServers                 Operation = "mcp.servers"
	OperationMCPTools                   Operation = "mcp.tools"
	OperationMCPReload                  Operation = "mcp.reload"
	OperationChannelList                Operation = "channel.list"
	OperationChannelHealth              Operation = "channel.health"
	OperationChannelRetry               Operation = "channel.retry"
	OperationChannelSkip                Operation = "channel.skip"
	OperationScheduleList               Operation = "schedule.list"
	OperationScheduleUpdate             Operation = "schedule.update"
	OperationPolicyEvents               Operation = "policy.events"
	OperationPolicyExplain              Operation = "policy.explain"
	OperationReceiptList                Operation = "receipt.list"
	OperationReceiptVerify              Operation = "receipt.verify"
	OperationIntegrityLatest            Operation = "integrity.latest"
	OperationIntegrityVerify            Operation = "integrity.verify"
	OperationIntegrityRun               Operation = "integrity.run"
	OperationConfigGet                  Operation = "config.get"
	OperationConfigPatch                Operation = "config.patch"
	OperationSoulGet                    Operation = "soul.get"
	OperationSoulPropose                Operation = "soul.propose"
	OperationLivenessGet                Operation = "liveness.get"
	OperationContinuityBrief            Operation = "continuity.brief"
	OperationRelationshipUpdate         Operation = "relationship.update"
	OperationWorkBrief                  Operation = "work.brief"
	OperationWorkContractPut            Operation = "work.contract.put"
	OperationWorkComplete               Operation = "work.contract.complete"
	OperationArtifactList               Operation = "artifact.list"
	OperationArtifactRecord             Operation = "artifact.record"
	OperationArtifactVerify             Operation = "artifact.verify"
	OperationAutonomyGet                Operation = "autonomy.get"
	OperationAutonomyUpdate             Operation = "autonomy.update"
	OperationWorkflowList               Operation = "workflow.list"
	OperationWorkflowStart              Operation = "workflow.start"
	OperationWorkflowAdvance            Operation = "workflow.advance"
	OperationReviewPlan                 Operation = "review.plan"
	OperationSupervisorList             Operation = "supervisor.list"
	OperationSupervisorGet              Operation = "supervisor.get"
	OperationSupervisorStart            Operation = "supervisor.start"
	OperationSupervisorSteer            Operation = "supervisor.steer"
	OperationSupervisorCancel           Operation = "supervisor.cancel"
	OperationProjectList                Operation = "project.list"
	OperationProjectGet                 Operation = "project.get"
	OperationProjectCreate              Operation = "project.create"
	OperationProjectImport              Operation = "project.import"
	OperationProjectAttach              Operation = "project.attach"
	OperationProjectClone               Operation = "project.clone"
	OperationProjectIndexGet            Operation = "project.index.get"
	OperationProjectIndexRefresh        Operation = "project.index.refresh"
	OperationProjectSearch              Operation = "project.search"
	OperationProjectCitationVerify      Operation = "project.citation.verify"
	OperationProjectPatchApply          Operation = "project.patch.apply"
	OperationProjectPatchHistory        Operation = "project.patch.history"
	OperationProjectPatchRollback       Operation = "project.patch.rollback"
	OperationProjectToolchainGet        Operation = "project.toolchain.get"
	OperationProjectDependenciesPlan    Operation = "project.dependencies.plan"
	OperationProjectDependenciesInstall Operation = "project.dependencies.install"
	OperationProjectProcessStart        Operation = "project.process.start"
	OperationProjectTerminalReplay      Operation = "project.terminal.replay"
	OperationProjectTerminalInput       Operation = "project.terminal.input"
	OperationProjectTerminalResize      Operation = "project.terminal.resize"
	OperationProjectTerminalSignal      Operation = "project.terminal.signal"
	OperationProjectTerminalCancel      Operation = "project.terminal.cancel"
	OperationProjectGitGet              Operation = "project.git.get"
	OperationProjectGitBlame            Operation = "project.git.blame"
	OperationProjectGitDiff             Operation = "project.git.diff"
	OperationProjectGitPreviewStart     Operation = "project.git.preview.start"
	OperationProjectGitPreviewClose     Operation = "project.git.preview.close"
	OperationProjectGitReviewGet        Operation = "project.git.review.get"
	OperationProjectGitReviewComments   Operation = "project.git.review.comments"
	OperationProjectGitReviewComment    Operation = "project.git.review.comment"
	OperationProjectGitReviewResolve    Operation = "project.git.review.resolve"
	OperationProjectGitBranchCreate     Operation = "project.git.branch.create"
	OperationProjectGitStage            Operation = "project.git.stage"
	OperationProjectGitStageHunks       Operation = "project.git.stage.hunks"
	OperationProjectGitCommit           Operation = "project.git.commit"
	OperationProjectGitCheckpoint       Operation = "project.git.checkpoint"
	OperationProjectGitTagCreate        Operation = "project.git.tag.create"
	OperationProjectGitRestorePlan      Operation = "project.git.restore.plan"
	OperationProjectGitSync             Operation = "project.git.sync"
	OperationProjectGitPull             Operation = "project.git.pull"
	OperationProjectGitMerge            Operation = "project.git.merge"
	OperationProjectGitPush             Operation = "project.git.push"
	OperationProjectGitForceWithLease   Operation = "project.git.force-with-lease"
	OperationProjectGitProviderGrant    Operation = "project.git.provider.grant"
	OperationProjectGitProviderRepos    Operation = "project.git.provider.repositories"
	OperationProjectGitProviderIssues   Operation = "project.git.provider.issues"
	OperationProjectGitProviderChanges  Operation = "project.git.provider.changes"
	OperationProjectGitProviderReview   Operation = "project.git.provider.review"
	OperationProjectGitProviderChecks   Operation = "project.git.provider.checks"
	OperationProjectGitProviderMerge    Operation = "project.git.provider.mergeability"
	OperationProjectGitProviderDraft    Operation = "project.git.provider.draft"
	OperationProjectRuntimePlan         Operation = "project.runtime.plan"
	OperationProjectRuntimeList         Operation = "project.runtime.list"
	OperationProjectRuntimeGet          Operation = "project.runtime.get"
	OperationProjectRuntimeStart        Operation = "project.runtime.start"
	OperationProjectRuntimePhase        Operation = "project.runtime.phase"
	OperationProjectRuntimeReload       Operation = "project.runtime.reload"
	OperationProjectRuntimeRestart      Operation = "project.runtime.restart"
	OperationProjectRuntimeStop         Operation = "project.runtime.stop"
	OperationProjectRuntimeReport       Operation = "project.runtime.report"
	OperationProjectRuntimeInspect      Operation = "project.runtime.inspect"
	OperationProjectRuntimeProblems     Operation = "project.runtime.problems"
	OperationProjectRuntimeAnnotate     Operation = "project.runtime.annotate"
	OperationProjectRuntimeStylePropose Operation = "project.runtime.style.propose"
	OperationProjectVerificationDerive  Operation = "project.verification.manifest.derive"
	OperationProjectVerificationGet     Operation = "project.verification.manifest.get"
	OperationProjectVerificationRun     Operation = "project.verification.run"
	OperationProjectVerificationRuns    Operation = "project.verification.runs"
	OperationProjectVerificationWaiver  Operation = "project.verification.waiver.create"
	OperationProjectVerificationWaivers Operation = "project.verification.waivers"
	OperationProjectDeliveryGet         Operation = "project.delivery.get"
	OperationProjectResourcePlan        Operation = "project.resource.plan"
	OperationProjectResourceApply       Operation = "project.resource.apply"
	OperationProjectEnvironmentPut      Operation = "project.environment.put"
	OperationProjectMigrationPlan       Operation = "project.migration.plan"
	OperationProjectMigrationApply      Operation = "project.migration.apply"
	OperationProjectMigrationRollback   Operation = "project.migration.rollback"
	OperationProjectDeploymentPlan      Operation = "project.deployment.plan"
	OperationProjectDeploymentApply     Operation = "project.deployment.apply"
	OperationProjectDeploymentReconcile Operation = "project.deployment.reconcile"
	OperationProjectDeploymentRollback  Operation = "project.deployment.rollback"
	OperationProjectCIPatchPlan         Operation = "project.ci.patch.plan"
	OperationProjectReleasePrepare      Operation = "project.release.prepare"
	OperationProjectPortableExport      Operation = "project.portable.export"
	OperationWorkspaceCapabilities      Operation = "workspace.capabilities"
	OperationWorkspaceLifecycle         Operation = "workspace.lifecycle"
	OperationStudioIntentList           Operation = "studio.intent.list"
	OperationStudioIntentGet            Operation = "studio.intent.get"
	OperationStudioIntentCompile        Operation = "studio.intent.compile"
	OperationStudioScopePropose         Operation = "studio.scope.propose"
	OperationStudioProposalDecide       Operation = "studio.proposal.decide"
	OperationStudioProposalApply        Operation = "studio.proposal.apply"
	OperationStudioCorrelationRecord    Operation = "studio.correlation.record"
	OperationStudioCompletionCheck      Operation = "studio.completion.check"
	OperationStudioDriftGet             Operation = "studio.drift.get"
	OperationStudioContextPlan          Operation = "studio.context.plan"
	OperationLogsQuery                  Operation = "logs.query"
	OperationEventsReplay               Operation = "events.replay"
	OperationCommandsCatalog            Operation = "commands.catalog"
	OperationEventsAcknowledge          Operation = "events.acknowledge"
)

var operationKinds = map[Operation]RequestKind{
	OperationSystemSnapshot: KindQuery, OperationSystemHealth: KindQuery,
	OperationSystemMetrics: KindQuery, OperationSessionList: KindQuery,
	OperationSessionCreate: KindCommand, OperationSessionResume: KindCommand,
	OperationSessionBranch: KindCommand, OperationSessionClose: KindCommand,
	OperationSessionRename: KindCommand, OperationSessionArchive: KindCommand,
	OperationSessionDelete: KindCommand,
	OperationSessionExport: KindQuery, OperationTurnSubmit: KindCommand,
	OperationTurnSteer: KindCommand, OperationTurnCancel: KindCommand,
	OperationTurnRetry: KindCommand, OperationApprovalRespond: KindCommand,
	OperationComputerControlGet:      KindQuery,
	OperationComputerControlAcquire:  KindCommand,
	OperationComputerControlRenew:    KindCommand,
	OperationComputerControlRelease:  KindCommand,
	OperationComputerBrowserObserve:  KindQuery,
	OperationComputerBrowserNavigate: KindCommand,
	OperationComputerBrowserInteract: KindCommand,
	OperationComputerBrowserSubmit:   KindCommand,
	OperationBrowserWorkflowList:     KindQuery,
	OperationBrowserWorkflowPause:    KindCommand,
	OperationBrowserWorkflowResume:   KindCommand,
	OperationBrowserWorkflowCancel:   KindCommand,
	OperationBrowserWorkflowHandoff:  KindCommand,
	OperationBrowserCredentialList:   KindQuery,
	OperationProviderList:            KindQuery, OperationProviderUsage: KindQuery,
	OperationToolSurface: KindQuery, OperationToolReadiness: KindQuery,
	OperationToolInvoke: KindCommand, OperationMemorySearch: KindQuery,
	OperationMemoryGet: KindQuery, OperationMemoryGraph: KindQuery,
	OperationMemoryActivation: KindQuery, OperationMemoryPin: KindCommand,
	OperationMemoryRecover: KindCommand, OperationPremiseList: KindQuery,
	OperationCitationVerify: KindQuery, OperationPredictionList: KindQuery,
	OperationTaskgraphGet: KindQuery, OperationTaskgraphTodo: KindQuery,
	OperationSwarmList: KindQuery, OperationSwarmAbort: KindCommand,
	OperationAutomatrixList: KindQuery, OperationAutomatrixApprove: KindCommand,
	OperationAutomatrixReject: KindCommand, OperationCuriosityTargets: KindQuery,
	OperationDreamweaverBeliefs: KindQuery, OperationSkillList: KindQuery,
	OperationSkillLifecycle: KindQuery, OperationSkillGet: KindQuery,
	OperationSkillSave: KindCommand, OperationSkillRefine: KindCommand,
	OperationSkillRollback: KindCommand, OperationPluginList: KindQuery,
	OperationPluginLifecycle: KindCommand, OperationMCPServers: KindQuery,
	OperationMCPTools: KindQuery, OperationMCPReload: KindCommand,
	OperationChannelList: KindQuery, OperationChannelHealth: KindQuery,
	OperationChannelRetry: KindCommand, OperationChannelSkip: KindCommand,
	OperationScheduleList: KindQuery, OperationScheduleUpdate: KindCommand,
	OperationPolicyEvents: KindQuery, OperationPolicyExplain: KindQuery,
	OperationReceiptList: KindQuery, OperationReceiptVerify: KindQuery,
	OperationIntegrityLatest: KindQuery, OperationIntegrityVerify: KindQuery,
	OperationIntegrityRun: KindCommand, OperationConfigGet: KindQuery,
	OperationConfigPatch: KindCommand, OperationSoulGet: KindQuery,
	OperationSoulPropose: KindCommand, OperationLivenessGet: KindQuery,
	OperationContinuityBrief:    KindQuery,
	OperationRelationshipUpdate: KindCommand,
	OperationWorkBrief:          KindQuery, OperationWorkContractPut: KindCommand,
	OperationWorkComplete: KindCommand, OperationArtifactList: KindQuery,
	OperationArtifactRecord: KindCommand, OperationArtifactVerify: KindCommand,
	OperationAutonomyGet: KindQuery, OperationAutonomyUpdate: KindCommand,
	OperationWorkflowList: KindQuery, OperationReviewPlan: KindQuery,
	OperationWorkflowStart: KindCommand, OperationWorkflowAdvance: KindCommand,
	OperationSupervisorList: KindQuery, OperationSupervisorGet: KindQuery,
	OperationSupervisorStart: KindCommand, OperationSupervisorSteer: KindCommand,
	OperationSupervisorCancel: KindCommand,
	OperationProjectList:      KindQuery, OperationProjectGet: KindQuery,
	OperationProjectCreate: KindCommand, OperationProjectImport: KindCommand,
	OperationProjectAttach: KindCommand, OperationProjectClone: KindCommand,
	OperationProjectIndexGet: KindQuery, OperationProjectIndexRefresh: KindCommand,
	OperationProjectSearch: KindQuery, OperationProjectCitationVerify: KindQuery,
	OperationProjectPatchApply: KindCommand, OperationProjectPatchHistory: KindQuery,
	OperationProjectPatchRollback: KindCommand, OperationProjectToolchainGet: KindQuery,
	OperationProjectDependenciesPlan: KindQuery, OperationProjectDependenciesInstall: KindCommand,
	OperationProjectProcessStart: KindCommand, OperationProjectTerminalReplay: KindQuery,
	OperationProjectTerminalInput: KindCommand, OperationProjectTerminalResize: KindCommand,
	OperationProjectTerminalSignal: KindCommand, OperationProjectTerminalCancel: KindCommand,
	OperationProjectGitGet: KindQuery, OperationProjectGitBlame: KindQuery,
	OperationProjectGitDiff:         KindQuery,
	OperationProjectGitPreviewStart: KindCommand, OperationProjectGitPreviewClose: KindCommand,
	OperationProjectGitReviewGet: KindQuery, OperationProjectGitReviewComments: KindQuery,
	OperationProjectGitReviewComment: KindCommand, OperationProjectGitReviewResolve: KindCommand,
	OperationProjectGitBranchCreate: KindCommand, OperationProjectGitStage: KindCommand,
	OperationProjectGitStageHunks: KindCommand, OperationProjectGitCommit: KindCommand,
	OperationProjectGitCheckpoint: KindCommand, OperationProjectGitTagCreate: KindCommand,
	OperationProjectGitRestorePlan: KindQuery, OperationProjectGitSync: KindCommand,
	OperationProjectGitPull: KindCommand, OperationProjectGitMerge: KindCommand,
	OperationProjectGitPush: KindCommand, OperationProjectGitForceWithLease: KindCommand,
	OperationProjectGitProviderGrant: KindCommand, OperationProjectGitProviderRepos: KindQuery,
	OperationProjectGitProviderIssues: KindQuery, OperationProjectGitProviderChanges: KindQuery,
	OperationProjectGitProviderReview: KindQuery, OperationProjectGitProviderChecks: KindQuery,
	OperationProjectGitProviderMerge: KindQuery, OperationProjectGitProviderDraft: KindCommand,
	OperationProjectRuntimePlan: KindQuery, OperationProjectRuntimeList: KindQuery,
	OperationProjectRuntimeGet: KindQuery, OperationProjectRuntimeStart: KindCommand,
	OperationProjectRuntimePhase:  KindCommand,
	OperationProjectRuntimeReload: KindCommand, OperationProjectRuntimeRestart: KindCommand,
	OperationProjectRuntimeStop: KindCommand, OperationProjectRuntimeReport: KindCommand,
	OperationProjectRuntimeInspect: KindQuery, OperationProjectRuntimeAnnotate: KindCommand,
	OperationProjectRuntimeProblems:     KindQuery,
	OperationProjectRuntimeStylePropose: KindCommand,
	OperationProjectVerificationDerive:  KindCommand,
	OperationProjectVerificationGet:     KindQuery,
	OperationProjectVerificationRun:     KindCommand,
	OperationProjectVerificationRuns:    KindQuery,
	OperationProjectVerificationWaiver:  KindCommand,
	OperationProjectVerificationWaivers: KindQuery,
	OperationProjectDeliveryGet:         KindQuery,
	OperationProjectResourcePlan:        KindCommand,
	OperationProjectResourceApply:       KindCommand,
	OperationProjectEnvironmentPut:      KindCommand,
	OperationProjectMigrationPlan:       KindCommand,
	OperationProjectMigrationApply:      KindCommand,
	OperationProjectMigrationRollback:   KindCommand,
	OperationProjectDeploymentPlan:      KindCommand,
	OperationProjectDeploymentApply:     KindCommand,
	OperationProjectDeploymentReconcile: KindCommand,
	OperationProjectDeploymentRollback:  KindCommand,
	OperationProjectCIPatchPlan:         KindQuery,
	OperationProjectReleasePrepare:      KindCommand,
	OperationProjectPortableExport:      KindCommand,
	OperationWorkspaceCapabilities:      KindQuery, OperationWorkspaceLifecycle: KindCommand,
	OperationStudioIntentList: KindQuery, OperationStudioIntentGet: KindQuery,
	OperationStudioIntentCompile: KindCommand, OperationStudioScopePropose: KindCommand,
	OperationStudioProposalDecide: KindCommand, OperationStudioProposalApply: KindCommand,
	OperationStudioCorrelationRecord: KindCommand, OperationStudioCompletionCheck: KindQuery,
	OperationStudioDriftGet: KindQuery, OperationStudioContextPlan: KindQuery,
	OperationLogsQuery:    KindQuery,
	OperationEventsReplay: KindQuery, OperationCommandsCatalog: KindQuery,
	OperationEventsAcknowledge: KindCommand,
}

// Operations returns a stable copy of the complete operation catalog.
func Operations() []Operation {
	operations := make([]Operation, 0, len(operationKinds))
	for operation := range operationKinds {
		operations = append(operations, operation)
	}
	sortOperations(operations)
	return operations
}

// Kind reports the binding request kind for an operation.
func (operation Operation) Kind() (RequestKind, bool) {
	kind, exists := operationKinds[operation]
	return kind, exists
}

// Scope binds a request to an authenticated actor and optional session.
type Scope struct {
	ActorID   uuid.UUID  `json:"actor_id"`
	SessionID *uuid.UUID `json:"session_id,omitempty"`
	Profile   string     `json:"profile,omitempty"`
	Channel   string     `json:"channel,omitempty"`
}

// Request is the canonical client-to-server envelope.
type Request struct {
	ProtocolVersion  string          `json:"protocol_version"`
	RequestID        uuid.UUID       `json:"request_id"`
	Kind             RequestKind     `json:"kind"`
	Operation        Operation       `json:"operation"`
	Scope            Scope           `json:"scope"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
	ExpectedRevision *uint64         `json:"expected_revision,omitempty"`
	Payload          json.RawMessage `json:"payload"`
}

// Validate enforces protocol, catalog, scope, mutation, and payload bounds.
func (request Request) Validate() error {
	if request.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version", ErrInvalidProtocol)
	}
	if request.RequestID == uuid.Nil || request.Scope.ActorID == uuid.Nil {
		return fmt.Errorf("%w: request and actor IDs are required", ErrInvalidProtocol)
	}
	expectedKind, exists := request.Operation.Kind()
	if !exists || request.Kind != expectedKind {
		return fmt.Errorf("%w: operation and kind do not match", ErrInvalidProtocol)
	}
	if request.Kind == KindCommand && strings.TrimSpace(request.IdempotencyKey) == "" {
		return fmt.Errorf("%w: command idempotency key is required", ErrInvalidProtocol)
	}
	if len(request.IdempotencyKey) > 256 {
		return fmt.Errorf("%w: idempotency key is too long", ErrInvalidProtocol)
	}
	if len(request.Payload) == 0 {
		request.Payload = json.RawMessage(`{}`)
	}
	if len(request.Payload) > MaximumPayloadBytes || !json.Valid(request.Payload) {
		return fmt.Errorf("%w: payload must be bounded valid JSON", ErrInvalidProtocol)
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Payload))
	decoder.UseNumber()
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: decode payload: %v", ErrInvalidProtocol, err)
	}
	return nil
}

// ErrorCode is a stable machine-readable control-plane error.
type ErrorCode string

const (
	ErrorInvalid      ErrorCode = "invalid"
	ErrorUnauthorized ErrorCode = "unauthorized"
	ErrorForbidden    ErrorCode = "forbidden"
	ErrorNotFound     ErrorCode = "not_found"
	ErrorConflict     ErrorCode = "conflict"
	ErrorRateLimited  ErrorCode = "rate_limited"
	ErrorUnavailable  ErrorCode = "unavailable"
	ErrorInternal     ErrorCode = "internal"
)

// ProtocolError is safe to serialize. Detail must already be redacted.
type ProtocolError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

// Response is the canonical server-to-client RPC envelope.
type Response struct {
	ProtocolVersion string          `json:"protocol_version"`
	RequestID       uuid.UUID       `json:"request_id"`
	Revision        uint64          `json:"revision"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *ProtocolError  `json:"error,omitempty"`
}

// EventType is the closed streamed-event catalog.
type EventType string

const (
	EventConnectionReady         EventType = "connection.ready"
	EventConnectionDegraded      EventType = "connection.degraded"
	EventConnectionRecovered     EventType = "connection.recovered"
	EventTurnStarted             EventType = "turn.started"
	EventTurnDelta               EventType = "turn.delta"
	EventTurnCompleted           EventType = "turn.completed"
	EventTurnIncomplete          EventType = "turn.incomplete"
	EventTurnRecovery            EventType = "turn.recovery"
	EventTurnFailed              EventType = "turn.failed"
	EventReasoningSummary        EventType = "reasoning.summary"
	EventToolRequested           EventType = "tool.requested"
	EventToolAwaitingApproval    EventType = "tool.awaiting_approval"
	EventToolStarted             EventType = "tool.started"
	EventToolDelta               EventType = "tool.delta"
	EventToolCompleted           EventType = "tool.completed"
	EventToolFailed              EventType = "tool.failed"
	EventToolDenied              EventType = "tool.denied"
	EventToolInterrupted         EventType = "tool.interrupted"
	EventToolOutcomeUnknown      EventType = "tool.outcome_unknown"
	EventComputerControlAcquired EventType = "computer.control.acquired"
	EventComputerControlRenewed  EventType = "computer.control.renewed"
	EventComputerControlReleased EventType = "computer.control.released"
	EventApprovalRequested       EventType = "approval.requested"
	EventApprovalResolved        EventType = "approval.resolved"
	EventApprovalExpired         EventType = "approval.expired"
	EventPremiseCreated          EventType = "premise.created"
	EventPremiseCited            EventType = "premise.cited"
	EventPremiseRefuted          EventType = "premise.refuted"
	EventPredictionCreated       EventType = "prediction.created"
	EventPredictionMatched       EventType = "prediction.matched"
	EventPredictionMismatched    EventType = "prediction.mismatched"
	EventTaskChanged             EventType = "task.changed"
	EventConvergenceWarning      EventType = "convergence.warning"
	EventAgentSpawned            EventType = "agent.spawned"
	EventAgentProgress           EventType = "agent.progress"
	EventAgentCompleted          EventType = "agent.completed"
	EventAgentAborted            EventType = "agent.aborted"
	EventMemoryCommitted         EventType = "memory.committed"
	EventMemoryTombstoned        EventType = "memory.tombstoned"
	EventMemoryRecovered         EventType = "memory.recovered"
	EventEmotionalChanged        EventType = "emotional.changed"
	EventRelationshipChanged     EventType = "relationship.changed"
	EventTemporalChanged         EventType = "temporal.changed"
	EventLivenessDecision        EventType = "liveness.decision"
	EventRepairLearned           EventType = "repair.learned"
	EventAestheticChanged        EventType = "aesthetic.changed"
	EventSoulChanged             EventType = "soul.changed"
	EventCircuitOpened           EventType = "circuit.opened"
	EventCircuitClosed           EventType = "circuit.closed"
	EventHeartbeatPulse          EventType = "heartbeat.pulse"
	EventAutomatrixQueued        EventType = "automatrix.queued"
	EventAutomatrixCompleted     EventType = "automatrix.completed"
	EventCuriosityTargeted       EventType = "curiosity.targeted"
	EventDreamweaverDerived      EventType = "dreamweaver.derived"
	EventPolicyDecision          EventType = "policy.decision"
	EventSecurityAlert           EventType = "security.alert"
	EventIntegrityGenerated      EventType = "integrity.generated"
	EventWorkspaceQueued         EventType = "workspace.operation.queued"
	EventWorkspaceStarted        EventType = "workspace.operation.started"
	EventWorkspaceProgress       EventType = "workspace.operation.progress"
	EventWorkspaceCompleted      EventType = "workspace.operation.completed"
	EventWorkspaceFailed         EventType = "workspace.operation.failed"
	EventWorkspaceCancelled      EventType = "workspace.operation.cancelled"
	EventJournalGap              EventType = "journal.gap"
)

var eventTypes = []EventType{
	EventConnectionReady, EventConnectionDegraded, EventConnectionRecovered,
	EventTurnStarted, EventTurnDelta, EventTurnCompleted, EventTurnIncomplete,
	EventTurnRecovery, EventTurnFailed,
	EventReasoningSummary, EventToolRequested, EventToolAwaitingApproval,
	EventToolStarted, EventToolDelta, EventToolCompleted, EventToolFailed,
	EventToolDenied, EventToolInterrupted, EventToolOutcomeUnknown,
	EventComputerControlAcquired, EventComputerControlRenewed,
	EventComputerControlReleased,
	EventApprovalRequested,
	EventApprovalResolved, EventApprovalExpired, EventPremiseCreated,
	EventPremiseCited, EventPremiseRefuted, EventPredictionCreated,
	EventPredictionMatched, EventPredictionMismatched, EventTaskChanged,
	EventConvergenceWarning, EventAgentSpawned, EventAgentProgress,
	EventAgentCompleted, EventAgentAborted, EventMemoryCommitted,
	EventMemoryTombstoned, EventMemoryRecovered, EventEmotionalChanged,
	EventRelationshipChanged, EventTemporalChanged, EventLivenessDecision,
	EventRepairLearned, EventAestheticChanged, EventSoulChanged,
	EventCircuitOpened, EventCircuitClosed, EventHeartbeatPulse,
	EventAutomatrixQueued, EventAutomatrixCompleted, EventCuriosityTargeted,
	EventDreamweaverDerived, EventPolicyDecision, EventSecurityAlert,
	EventIntegrityGenerated, EventWorkspaceQueued, EventWorkspaceStarted,
	EventWorkspaceProgress, EventWorkspaceCompleted, EventWorkspaceFailed,
	EventWorkspaceCancelled, EventJournalGap,
}

// EventTypes returns a stable copy of the event catalog.
func EventTypes() []EventType {
	return append([]EventType(nil), eventTypes...)
}

// Valid reports whether an event type belongs to the closed catalog.
func (eventType EventType) Valid() bool {
	for _, candidate := range eventTypes {
		if eventType == candidate {
			return true
		}
	}
	return false
}

// Correlation groups optional entity IDs carried by every event.
type Correlation struct {
	ActorID   uuid.UUID  `json:"actor_id"`
	SessionID *uuid.UUID `json:"session_id,omitempty"`
	TurnID    *uuid.UUID `json:"turn_id,omitempty"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
	ToolID    *uuid.UUID `json:"tool_id,omitempty"`
}

// Event is immutable after the journal assigns its sequence.
type Event struct {
	Sequence    uint64          `json:"sequence"`
	EventID     uuid.UUID       `json:"event_id"`
	Type        EventType       `json:"type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Correlation Correlation     `json:"correlation"`
	Payload     json.RawMessage `json:"payload"`
}

// NewEvent validates a redacted payload before it reaches the journal.
func NewEvent(
	eventType EventType,
	correlation Correlation,
	payload json.RawMessage,
	occurredAt time.Time,
) (Event, error) {
	if !eventType.Valid() || correlation.ActorID == uuid.Nil || occurredAt.IsZero() {
		return Event{}, fmt.Errorf("%w: event type, actor, and timestamp are required", ErrInvalidProtocol)
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if len(payload) > MaximumPayloadBytes || !json.Valid(payload) {
		return Event{}, fmt.Errorf("%w: event payload must be bounded valid JSON", ErrInvalidProtocol)
	}
	if phase, computer := computerPhaseForEventType(eventType); computer {
		var computerPayload ComputerEventPayload
		if err := json.Unmarshal(payload, &computerPayload); err != nil {
			return Event{}, fmt.Errorf("%w: decode computer event: %v", ErrInvalidProtocol, err)
		}
		if err := computerPayload.Validate(); err != nil {
			return Event{}, err
		}
		if computerPayload.Phase != phase ||
			computerPayload.Scope.ActorID != correlation.ActorID ||
			correlation.ToolID == nil ||
			*correlation.ToolID != computerPayload.ToolEventID {
			return Event{}, fmt.Errorf("%w: mismatched computer event correlation", ErrInvalidProtocol)
		}
	}
	return Event{
		EventID: uuid.New(), Type: eventType, OccurredAt: occurredAt,
		Correlation: cloneCorrelation(correlation),
		Payload:     append(json.RawMessage(nil), payload...),
	}, nil
}

// Replay is returned for cursor-based event recovery.
type Replay struct {
	AfterSequence uint64 `json:"after_sequence"`
	Earliest      uint64 `json:"earliest_sequence"`
	// Latest is the highest sequence the client may acknowledge after applying
	// this page. It never advances beyond the portion of the journal actually
	// scanned for the authenticated actor.
	Latest uint64 `json:"latest_sequence"`
	// Head is the journal head captured for this replay. When Latest is below
	// Head the transport must request another bounded page before switching to
	// the live feed.
	Head   uint64  `json:"head_sequence"`
	Gap    bool    `json:"gap"`
	Events []Event `json:"events"`
}

// GapMarker makes retention loss explicit instead of silently jumping cursors.
type GapMarker struct {
	RequestedAfter uint64 `json:"requested_after"`
	AvailableFrom  uint64 `json:"available_from"`
	Latest         uint64 `json:"latest"`
}

// Recovery combines replay with a fresh snapshot when retention was exceeded.
type Recovery struct {
	Replay    Replay          `json:"replay"`
	GapMarker *GapMarker      `json:"gap_marker,omitempty"`
	Snapshot  json.RawMessage `json:"snapshot,omitempty"`
}

// CommandDescriptor drives both browser and TUI command discovery.
type CommandDescriptor struct {
	Operation   Operation   `json:"operation"`
	Kind        RequestKind `json:"kind"`
	Available   bool        `json:"available"`
	Description string      `json:"description"`
}

func cloneCorrelation(correlation Correlation) Correlation {
	cloned := correlation
	cloned.SessionID = cloneUUID(correlation.SessionID)
	cloned.TurnID = cloneUUID(correlation.TurnID)
	cloned.TaskID = cloneUUID(correlation.TaskID)
	cloned.ToolID = cloneUUID(correlation.ToolID)
	return cloned
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sortOperations(operations []Operation) {
	for index := 1; index < len(operations); index++ {
		for cursor := index; cursor > 0 && operations[cursor] < operations[cursor-1]; cursor-- {
			operations[cursor], operations[cursor-1] = operations[cursor-1], operations[cursor]
		}
	}
}
