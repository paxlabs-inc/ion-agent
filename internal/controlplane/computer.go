package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ComputerEventVersion       = "ion.computer-event.v1"
	MaximumComputerSources     = 8
	MaximumComputerProgress    = 16 << 10
	MaximumComputerErrorLength = 1024
)

// ComputerPhase is the closed semantic lifecycle projected from tools.Manager.
type ComputerPhase string

const (
	ComputerRequested        ComputerPhase = "requested"
	ComputerAwaitingApproval ComputerPhase = "awaiting_approval"
	ComputerStarted          ComputerPhase = "started"
	ComputerProgress         ComputerPhase = "progress"
	ComputerCompleted        ComputerPhase = "completed"
	ComputerFailed           ComputerPhase = "failed"
	ComputerDenied           ComputerPhase = "denied"
	ComputerInterrupted      ComputerPhase = "interrupted"
	ComputerOutcomeUnknown   ComputerPhase = "outcome_unknown"
)

// Terminal reports whether phase is one of the five exclusive outcomes.
func (phase ComputerPhase) Terminal() bool {
	switch phase {
	case ComputerCompleted, ComputerFailed, ComputerDenied,
		ComputerInterrupted, ComputerOutcomeUnknown:
		return true
	default:
		return false
	}
}

// ComputerScope identifies the durable actor and work boundary.
type ComputerScope struct {
	ActorID   uuid.UUID  `json:"actor_id"`
	SessionID *uuid.UUID `json:"session_id,omitempty"`
	TurnID    *uuid.UUID `json:"turn_id,omitempty"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
	OutcomeID *uuid.UUID `json:"outcome_id,omitempty"`
	AgentID   string     `json:"agent_id"`
}

// ComputerSourceReference points to authoritative evidence without embedding
// unrestricted source content in the control-plane journal.
type ComputerSourceReference struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ComputerResultSummary describes availability and size of the real result.
type ComputerResultSummary struct {
	Available bool   `json:"available"`
	Bytes     int    `json:"bytes"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ComputerEventPayload is the versioned payload for every tool lifecycle event.
// Sequence and event identity are assigned by the containing durable Event.
type ComputerEventPayload struct {
	ProtocolVersion  string                    `json:"protocol_version"`
	ToolEventID      uuid.UUID                 `json:"tool_event_id"`
	ProviderCallID   string                    `json:"provider_tool_call_id"`
	Tool             string                    `json:"tool"`
	Operation        string                    `json:"operation"`
	Scope            ComputerScope             `json:"scope"`
	RiskClass        string                    `json:"risk_class"`
	Phase            ComputerPhase             `json:"phase"`
	Timestamp        time.Time                 `json:"timestamp"`
	DisplayKind      string                    `json:"display_kind"`
	SourceReferences []ComputerSourceReference `json:"source_references"`
	TerminalStatus   ComputerPhase             `json:"terminal_status,omitempty"`
	Progress         json.RawMessage           `json:"progress,omitempty"`
	Result           *ComputerResultSummary    `json:"result,omitempty"`
	DisplayModel     json.RawMessage           `json:"display_model,omitempty"`
}

// Validate rejects uncorrelated, unbounded, or internally inconsistent events.
func (payload ComputerEventPayload) Validate() error {
	if payload.ProtocolVersion != ComputerEventVersion ||
		payload.ToolEventID == uuid.Nil ||
		strings.TrimSpace(payload.ProviderCallID) == "" ||
		strings.TrimSpace(payload.Tool) == "" ||
		strings.TrimSpace(payload.Operation) == "" ||
		payload.Scope.ActorID == uuid.Nil ||
		strings.TrimSpace(payload.Scope.AgentID) == "" ||
		payload.Timestamp.IsZero() ||
		strings.TrimSpace(payload.DisplayKind) == "" {
		return fmt.Errorf("%w: incomplete computer event", ErrInvalidProtocol)
	}
	switch payload.RiskClass {
	case "GREEN", "YELLOW", "RED":
	default:
		return fmt.Errorf("%w: invalid computer risk class", ErrInvalidProtocol)
	}
	if payload.Scope.TaskID == nil && payload.Scope.OutcomeID == nil {
		return fmt.Errorf("%w: computer task or outcome is required", ErrInvalidProtocol)
	}
	if !payload.Phase.valid() {
		return fmt.Errorf("%w: invalid computer phase", ErrInvalidProtocol)
	}
	if payload.Phase.Terminal() {
		if payload.TerminalStatus != payload.Phase || payload.Result == nil {
			return fmt.Errorf("%w: invalid computer terminal state", ErrInvalidProtocol)
		}
	} else if payload.TerminalStatus != "" || payload.Result != nil {
		return fmt.Errorf("%w: non-terminal computer event has terminal data", ErrInvalidProtocol)
	}
	if payload.Result != nil {
		if payload.Result.Bytes < 0 ||
			len(payload.Result.Error) > MaximumComputerErrorLength {
			return fmt.Errorf("%w: invalid computer result summary", ErrInvalidProtocol)
		}
	}
	if len(payload.Progress) > 0 {
		if payload.Phase != ComputerProgress ||
			len(payload.Progress) > MaximumComputerProgress ||
			!json.Valid(payload.Progress) {
			return fmt.Errorf("%w: invalid computer progress", ErrInvalidProtocol)
		}
	} else if payload.Phase == ComputerProgress {
		return fmt.Errorf("%w: computer progress payload is required", ErrInvalidProtocol)
	}
	if len(payload.SourceReferences) == 0 ||
		len(payload.SourceReferences) > MaximumComputerSources {
		return fmt.Errorf("%w: invalid computer source references", ErrInvalidProtocol)
	}
	hasToolEvent := false
	for _, reference := range payload.SourceReferences {
		if strings.TrimSpace(reference.Kind) == "" ||
			strings.TrimSpace(reference.ID) == "" ||
			len(reference.Kind) > 64 || len(reference.ID) > 512 {
			return fmt.Errorf("%w: invalid computer source reference", ErrInvalidProtocol)
		}
		if reference.Kind == "tool_event" &&
			reference.ID == payload.ToolEventID.String() {
			hasToolEvent = true
		}
	}
	if !hasToolEvent {
		return fmt.Errorf("%w: authoritative ToolEvent reference is required", ErrInvalidProtocol)
	}
	if len(payload.DisplayModel) > 0 {
		if _, _, err := ResolveDisplayModel(
			payload.DisplayModel, len(payload.SourceReferences),
		); err != nil {
			return err
		}
	}
	return nil
}

// EventType returns the durable catalog entry for phase.
func (phase ComputerPhase) EventType() (EventType, error) {
	switch phase {
	case ComputerRequested:
		return EventToolRequested, nil
	case ComputerAwaitingApproval:
		return EventToolAwaitingApproval, nil
	case ComputerStarted:
		return EventToolStarted, nil
	case ComputerProgress:
		return EventToolDelta, nil
	case ComputerCompleted:
		return EventToolCompleted, nil
	case ComputerFailed:
		return EventToolFailed, nil
	case ComputerDenied:
		return EventToolDenied, nil
	case ComputerInterrupted:
		return EventToolInterrupted, nil
	case ComputerOutcomeUnknown:
		return EventToolOutcomeUnknown, nil
	default:
		return "", fmt.Errorf("%w: invalid computer phase", ErrInvalidProtocol)
	}
}

func (phase ComputerPhase) valid() bool {
	_, err := phase.EventType()
	return err == nil
}

func computerPhaseForEventType(eventType EventType) (ComputerPhase, bool) {
	switch eventType {
	case EventToolRequested:
		return ComputerRequested, true
	case EventToolAwaitingApproval:
		return ComputerAwaitingApproval, true
	case EventToolStarted:
		return ComputerStarted, true
	case EventToolDelta:
		return ComputerProgress, true
	case EventToolCompleted:
		return ComputerCompleted, true
	case EventToolFailed:
		return ComputerFailed, true
	case EventToolDenied:
		return ComputerDenied, true
	case EventToolInterrupted:
		return ComputerInterrupted, true
	case EventToolOutcomeUnknown:
		return ComputerOutcomeUnknown, true
	default:
		return "", false
	}
}
