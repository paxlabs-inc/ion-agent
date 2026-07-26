package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// scopedToolManager adds authenticated turn identity and activity tracking.
// Lifecycle emission itself is owned by the shared tools.Manager boundary.
type scopedToolManager struct {
	manager agent.ToolManager
	agentID string
}

// NewScopedToolManager binds authenticated operator turn identity before the
// shared manager executes a model-visible call.
func NewScopedToolManager(manager agent.ToolManager) agent.ToolManager {
	return scopedToolManager{manager: manager}
}

// NewScopedAgentToolManager binds a delegated agent identity while retaining
// the authenticated actor, session, turn, task, and outcome scope.
func NewScopedAgentToolManager(
	manager agent.ToolManager,
	agentID string,
) agent.ToolManager {
	return scopedToolManager{
		manager: manager,
		agentID: strings.TrimSpace(agentID),
	}
}

func (manager scopedToolManager) Surface(
	ctx context.Context,
) []protocol.ToolDefinition {
	return manager.manager.Surface(ctx)
}

func (manager scopedToolManager) Execute(
	ctx context.Context,
	call protocol.NormalizedToolCall,
) (json.RawMessage, error) {
	agent.TouchActivity(ctx)
	scope, scoped := controlplane.ApprovalScopeFromContext(ctx)
	if scoped {
		if manager.agentID != "" {
			scope.AgentID = manager.agentID
			ctx = controlplane.WithApprovalScope(ctx, scope)
		}
		idempotencyScope := ""
		if scope.TurnID != nil {
			idempotencyScope = scope.TurnID.String()
		} else if scope.SessionID != nil {
			idempotencyScope = scope.SessionID.String()
		}
		ctx = tools.WithIdempotencyScope(ctx, idempotencyScope)
		binding := protocol.ToolExecutionBinding{
			ActorID: scope.ActorID, SessionID: scope.SessionID,
			TurnID: scope.TurnID, TaskID: scope.TaskID,
			OutcomeID: scope.OutcomeID, AgentID: scope.AgentID,
		}
		if binding.OutcomeID == nil {
			binding.OutcomeID = scope.TurnID
		}
		if strings.TrimSpace(binding.AgentID) == "" {
			binding.AgentID = "ion"
		}
		ctx = protocol.WithToolExecutionBinding(ctx, binding)
	}
	result, err := manager.manager.Execute(ctx, call)
	agent.TouchActivity(ctx)
	return result, err
}

type computerLifecycleObserver struct {
	emitter controlplane.EventEmitter
	display *controlplane.DisplayAdapterRegistry
}

// NewComputerLifecycleObserver returns the production durable projection used
// by operator runtimes and real-client acceptance composition.
func NewComputerLifecycleObserver(
	emitter controlplane.EventEmitter,
) tools.LifecycleObserver {
	return computerLifecycleObserver{
		emitter: emitter,
		display: controlplane.NewDisplayAdapterRegistry(),
	}
}

func (observer computerLifecycleObserver) Observe(
	ctx context.Context,
	observation tools.LifecycleObservation,
) error {
	if observer.emitter == nil {
		return fmt.Errorf("operator runtime: computer event emitter is required")
	}
	binding := observation.Binding
	if binding.ToolEventID == [16]byte{} || binding.ActorID == [16]byte{} {
		return fmt.Errorf("operator runtime: computer event binding is incomplete")
	}
	phase, eventType, err := computerPhase(observation.Phase)
	if err != nil {
		return err
	}
	outcomeID := binding.OutcomeID
	if outcomeID == nil {
		outcomeID = binding.TurnID
	}
	payload := controlplane.ComputerEventPayload{
		ProtocolVersion: controlplane.ComputerEventVersion,
		ToolEventID:     binding.ToolEventID,
		ProviderCallID:  observation.Call.ID,
		Tool:            observation.Call.Name,
		Operation:       observation.Call.Name,
		Scope: controlplane.ComputerScope{
			ActorID: binding.ActorID, SessionID: binding.SessionID,
			TurnID: binding.TurnID, TaskID: binding.TaskID,
			OutcomeID: outcomeID, AgentID: binding.AgentID,
		},
		RiskClass:   string(observation.Classification),
		Phase:       phase,
		Timestamp:   observation.OccurredAt,
		DisplayKind: computerDisplayKind(observation.Call.Name),
		SourceReferences: []controlplane.ComputerSourceReference{
			{Kind: "tool_event", ID: binding.ToolEventID.String()},
			{Kind: "provider_tool_call", ID: observation.Call.ID},
		},
	}
	if phase == controlplane.ComputerProgress {
		payload.Progress = append(json.RawMessage(nil), observation.Progress...)
	}
	display := observer.display
	if display == nil {
		display = controlplane.NewDisplayAdapterRegistry()
	}
	if phase == controlplane.ComputerAwaitingApproval {
		payload.DisplayModel, payload.SourceReferences, err = display.Approval(
			observation.Call.Name,
			string(observation.Classification),
			payload.SourceReferences,
		)
		if err != nil {
			return fmt.Errorf("operator runtime: build approval display: %w", err)
		}
	}
	if phase.Terminal() {
		payload.TerminalStatus = phase
		payload.Result = &controlplane.ComputerResultSummary{
			Available: observation.Error == nil,
			Bytes:     observation.ResultBytes,
			ErrorCode: computerErrorCode(observation.Error),
			Error:     safeToolError(observation.Error),
		}
		if phase == controlplane.ComputerCompleted {
			payload.DisplayModel, payload.SourceReferences, err = display.AdaptResult(
				observation.Call.Name,
				observation.Call.Arguments,
				observation.Result,
				observation.ResultBytes,
				payload.SourceReferences,
			)
		} else {
			payload.DisplayModel, payload.SourceReferences, err = display.Failure(
				computerFailureTitle(phase),
				safeToolError(observation.Error),
				payload.SourceReferences,
			)
		}
		if err != nil {
			return fmt.Errorf("operator runtime: build terminal display: %w", err)
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("operator runtime: encode computer event: %w", err)
	}
	correlation := controlplane.Correlation{
		ActorID: binding.ActorID, SessionID: binding.SessionID,
		TurnID: binding.TurnID, TaskID: binding.TaskID,
		ToolID: &binding.ToolEventID,
	}
	emitCtx := ctx
	if ctx.Err() != nil {
		emitCtx = context.WithoutCancel(ctx)
	}
	if _, err := observer.emitter.Emit(
		emitCtx, eventType, correlation, encoded,
	); err != nil {
		return fmt.Errorf("operator runtime: record computer event: %w", err)
	}
	return nil
}

func computerFailureTitle(phase controlplane.ComputerPhase) string {
	switch phase {
	case controlplane.ComputerDenied:
		return "Action denied"
	case controlplane.ComputerInterrupted:
		return "Action interrupted"
	case controlplane.ComputerOutcomeUnknown:
		return "Outcome unknown"
	default:
		return "Action failed"
	}
}

func computerPhase(
	phase tools.LifecyclePhase,
) (controlplane.ComputerPhase, controlplane.EventType, error) {
	mapped := controlplane.ComputerPhase(phase)
	eventType, err := mapped.EventType()
	if err != nil {
		return "", "", err
	}
	return mapped, eventType, nil
}

func computerDisplayKind(name string) string {
	switch {
	case strings.Contains(name, "browser"), strings.Contains(name, "search"):
		return "research"
	case strings.Contains(name, "filesystem"), strings.Contains(name, "git"),
		strings.Contains(name, "project"):
		return "repository"
	case strings.Contains(name, "shell"), strings.Contains(name, "process"):
		return "terminal"
	case strings.Contains(name, "artifact"), strings.Contains(name, "document"):
		return "artifact"
	case strings.Contains(name, "work_"), strings.Contains(name, "task"):
		return "task"
	default:
		return "generic"
	}
}

func computerErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case isArgumentValidationError(err):
		return "invalid_arguments"
	case errors.Is(err, tools.ErrPolicyDenied):
		return "denied"
	case errors.Is(err, tools.ErrTimeout):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "interrupted"
	default:
		var uncertain *tools.OutcomeUnknownError
		if errors.As(err, &uncertain) {
			return "outcome_unknown"
		}
		return "execution_failed"
	}
}

func safeToolError(err error) string {
	if err == nil {
		return ""
	}
	switch computerErrorCode(err) {
	case "invalid_arguments":
		return safeArgumentValidationError(err)
	case "denied":
		return "Operation was denied."
	case "timeout":
		return "Tool execution timed out."
	case "interrupted":
		return "Tool execution was interrupted."
	case "outcome_unknown":
		return "The external outcome could not be confirmed."
	default:
		return "Tool execution failed."
	}
}

func isArgumentValidationError(err error) bool {
	var validation *tools.ArgumentValidationError
	return errors.As(err, &validation)
}

func safeArgumentValidationError(err error) string {
	var validation *tools.ArgumentValidationError
	if !errors.As(err, &validation) || len(validation.Issues) == 0 {
		return "Tool input does not match the required format."
	}
	issue := validation.Issues[0]
	switch {
	case strings.Contains(issue.Message, "task references unknown criterion"):
		return "Tool input has a task criterion that is not declared in spec_delta.acceptance_criteria."
	case strings.Contains(issue.Message, "task references unknown dependency"):
		return "Tool input has a task dependency that is not declared in spec_delta.tasks."
	case strings.Contains(issue.Message, "every criterion must be assigned"):
		return "Tool input must assign every acceptance criterion to a task."
	case strings.Contains(issue.Message, "missing properties"):
		fields := safeQuotedIdentifiers(issue.Message)
		if len(fields) == 1 {
			return "Tool input is missing required field: " + fields[0] + "."
		}
		if len(fields) > 1 {
			return "Tool input is missing required fields: " +
				strings.Join(fields, ", ") + "."
		}
	}
	path := safeValidationPath(issue.Path)
	if path == "" || path == "$" {
		return "Tool input does not match the required format."
	}
	return "Tool input is invalid at " + path + "."
}

func safeQuotedIdentifiers(message string) []string {
	fields := make([]string, 0, 4)
	for _, part := range strings.Split(message, "'") {
		if safeValidationPath(part) == part && part != "" && part != "$" {
			fields = append(fields, part)
			if len(fields) == 4 {
				break
			}
		}
	}
	return fields
}

func safeValidationPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > 160 {
		return ""
	}
	for _, character := range path {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("_.$-[]", character) {
			continue
		}
		return ""
	}
	return path
}
