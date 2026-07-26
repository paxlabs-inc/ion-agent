package protocol

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// ToolExecutionBinding carries durable execution identity through the shared
// manager boundary without coupling the tools package to an operator transport.
type ToolExecutionBinding struct {
	ToolEventID uuid.UUID
	ActorID     uuid.UUID
	SessionID   *uuid.UUID
	TurnID      *uuid.UUID
	TaskID      *uuid.UUID
	OutcomeID   *uuid.UUID
	AgentID     string
}

type toolExecutionBindingKey struct{}

// WithToolExecutionBinding merges non-zero execution identity into ctx.
func WithToolExecutionBinding(
	ctx context.Context,
	binding ToolExecutionBinding,
) context.Context {
	current, _ := ToolExecutionBindingFromContext(ctx)
	if binding.ToolEventID != uuid.Nil {
		current.ToolEventID = binding.ToolEventID
	}
	if binding.ActorID != uuid.Nil {
		current.ActorID = binding.ActorID
	}
	if binding.SessionID != nil {
		current.SessionID = cloneExecutionUUID(binding.SessionID)
	}
	if binding.TurnID != nil {
		current.TurnID = cloneExecutionUUID(binding.TurnID)
	}
	if binding.TaskID != nil {
		current.TaskID = cloneExecutionUUID(binding.TaskID)
	}
	if binding.OutcomeID != nil {
		current.OutcomeID = cloneExecutionUUID(binding.OutcomeID)
	}
	if value := strings.TrimSpace(binding.AgentID); value != "" {
		current.AgentID = value
	}
	return context.WithValue(ctx, toolExecutionBindingKey{}, current)
}

// ToolExecutionBindingFromContext returns a detached execution binding.
func ToolExecutionBindingFromContext(
	ctx context.Context,
) (ToolExecutionBinding, bool) {
	binding, ok := ctx.Value(toolExecutionBindingKey{}).(ToolExecutionBinding)
	if !ok {
		return ToolExecutionBinding{}, false
	}
	binding.SessionID = cloneExecutionUUID(binding.SessionID)
	binding.TurnID = cloneExecutionUUID(binding.TurnID)
	binding.TaskID = cloneExecutionUUID(binding.TaskID)
	binding.OutcomeID = cloneExecutionUUID(binding.OutcomeID)
	return binding, binding.ToolEventID != uuid.Nil || binding.ActorID != uuid.Nil
}

func cloneExecutionUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
