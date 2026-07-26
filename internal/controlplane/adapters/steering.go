package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

type SteerTargetKind string

const (
	SteerTargetTurn SteerTargetKind = "turn"
	SteerTargetTool SteerTargetKind = "tool"
)

type SteerTarget struct {
	Kind           SteerTargetKind `json:"kind"`
	TaskID         uuid.UUID       `json:"task_id"`
	AgentID        string          `json:"agent_id"`
	ToolEventID    *uuid.UUID      `json:"tool_event_id,omitempty"`
	ToolAction     string          `json:"tool_action"`
	TargetRevision uint64          `json:"target_revision"`
}

func (target SteerTarget) validate(turnID uuid.UUID) error {
	target.AgentID = strings.TrimSpace(target.AgentID)
	target.ToolAction = strings.TrimSpace(target.ToolAction)
	if target.TaskID == uuid.Nil || target.AgentID == "" ||
		target.ToolAction == "" || target.TargetRevision == 0 {
		return controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "steering requires an explicit task, agent, action, and revision",
		}
	}
	switch target.Kind {
	case SteerTargetTurn:
		if target.TaskID != turnID || target.AgentID != "ion" ||
			target.ToolAction != "turn.run" || target.ToolEventID != nil {
			return controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "turn steering target is contradictory",
			}
		}
	case SteerTargetTool:
		if target.ToolEventID == nil || *target.ToolEventID == uuid.Nil {
			return controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "tool steering requires an explicit tool event",
			}
		}
	default:
		return controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "steering target kind must be turn or tool",
		}
	}
	return nil
}

type SteeringResolver interface {
	Resolve(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		SteerTarget,
	) error
}

type JournalSteeringResolver struct {
	journal *controlplane.Journal
}

func NewJournalSteeringResolver(
	journal *controlplane.Journal,
) *JournalSteeringResolver {
	return &JournalSteeringResolver{journal: journal}
}

func (resolver *JournalSteeringResolver) Resolve(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	turnID uuid.UUID,
	target SteerTarget,
) error {
	if resolver == nil || resolver.journal == nil {
		return fmt.Errorf("controlplane adapters: steering journal is unavailable")
	}
	if err := target.validate(turnID); err != nil {
		return err
	}
	var exact *controlplane.Event
	var latest uint64
	var after uint64
	for {
		replay, err := resolver.journal.ReplayActor(ctx, actorID, after, 2_000)
		if err != nil {
			return err
		}
		for index := range replay.Events {
			event := replay.Events[index]
			if !steeringEventInScope(event, sessionID, turnID) {
				continue
			}
			relevant, err := steeringEventRelevant(event, target)
			if err != nil {
				return err
			}
			if !relevant {
				continue
			}
			if event.Sequence > latest {
				latest = event.Sequence
			}
			if event.Sequence == target.TargetRevision {
				copied := event
				exact = &copied
			}
		}
		if replay.Latest >= replay.Head || len(replay.Events) == 0 {
			break
		}
		after = replay.Latest
	}
	if exact == nil {
		return controlplane.PublicError{
			Code:    controlplane.ErrorNotFound,
			Message: "the targeted execution revision is not retained",
		}
	}
	if latest != target.TargetRevision {
		return controlplane.PublicError{
			Code:    controlplane.ErrorConflict,
			Message: "the targeted execution revision is stale",
		}
	}
	if target.Kind == SteerTargetTool {
		var payload controlplane.ComputerEventPayload
		if err := json.Unmarshal(exact.Payload, &payload); err != nil {
			return fmt.Errorf("controlplane adapters: decode steering target: %w", err)
		}
		if payload.Phase.Terminal() {
			return controlplane.PublicError{
				Code:    controlplane.ErrorConflict,
				Message: "the targeted tool action is already terminal",
			}
		}
	}
	return nil
}

func steeringEventInScope(
	event controlplane.Event,
	sessionID uuid.UUID,
	turnID uuid.UUID,
) bool {
	return event.Correlation.SessionID != nil &&
		*event.Correlation.SessionID == sessionID &&
		event.Correlation.TurnID != nil &&
		*event.Correlation.TurnID == turnID
}

func steeringEventRelevant(
	event controlplane.Event,
	target SteerTarget,
) (bool, error) {
	if target.Kind == SteerTargetTurn {
		return true, nil
	}
	if event.Correlation.ToolID == nil ||
		target.ToolEventID == nil ||
		*event.Correlation.ToolID != *target.ToolEventID {
		return false, nil
	}
	var payload controlplane.ComputerEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return false, fmt.Errorf("controlplane adapters: decode tool steering target: %w", err)
	}
	taskID := payload.Scope.TaskID
	if taskID == nil {
		taskID = payload.Scope.OutcomeID
	}
	if taskID == nil {
		taskID = payload.Scope.TurnID
	}
	if taskID == nil ||
		*taskID != target.TaskID ||
		payload.Scope.AgentID != strings.TrimSpace(target.AgentID) ||
		payload.Operation != strings.TrimSpace(target.ToolAction) ||
		payload.ToolEventID != *target.ToolEventID {
		return false, controlplane.PublicError{
			Code:    controlplane.ErrorConflict,
			Message: "the steering target contradicts authoritative execution",
		}
	}
	return true, nil
}
