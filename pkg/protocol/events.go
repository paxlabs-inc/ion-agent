package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FailureClass categorizes tool execution failures for structured handling.
type FailureClass string

const (
	FailureNone           FailureClass = ""
	FailureTimeout        FailureClass = "timeout"
	FailureAuth           FailureClass = "auth"
	FailureRateLimit      FailureClass = "rate_limit"
	FailureValidation     FailureClass = "validation"
	FailureExecution      FailureClass = "execution"
	FailureDenied         FailureClass = "denied"
	FailureInterrupted    FailureClass = "interrupted"
	FailureOutcomeUnknown FailureClass = "outcome_unknown"
)

// ToolEvent is the immutable record of one tool execution, including its
// prediction, outcome, and provenance bindings. It is persisted through the
// encrypted journal as an Event (0x05) memory.
type ToolEvent struct {
	ID            uuid.UUID       `json:"id"`
	CallID        string          `json:"call_id"`
	Name          string          `json:"name"`
	Args          json.RawMessage `json:"args"`
	Result        json.RawMessage `json:"result"`
	FailureClass  FailureClass    `json:"failure_class"`
	Expect        string          `json:"expect"`
	Match         *bool           `json:"match,omitempty"`
	SubgoalID     string          `json:"subgoal_id,omitempty"`
	MMRLeafIndex  uint64          `json:"mmr_leaf_index"`
	MMRLeafHash   [32]byte        `json:"mmr_leaf_hash"`
	MMRRootAtTime [32]byte        `json:"mmr_root_at_time"`
	Timestamp     time.Time       `json:"timestamp"`
}

// Validate rejects malformed events at construction.
func (event ToolEvent) Validate() error {
	if event.ID == uuid.Nil {
		return fmt.Errorf("protocol: tool event ID is required")
	}
	if strings.TrimSpace(event.CallID) == "" {
		return fmt.Errorf("protocol: tool event call ID is required")
	}
	if strings.TrimSpace(event.Name) == "" {
		return fmt.Errorf("protocol: tool event tool name is required")
	}
	if len(event.Args) == 0 || !json.Valid(event.Args) {
		return fmt.Errorf("protocol: tool event args must be valid JSON")
	}
	if event.Timestamp.IsZero() {
		return fmt.Errorf("protocol: tool event timestamp is required")
	}
	return nil
}

// Citation cryptographically binds a factual claim to a ToolEvent in the MMR.
// Verified is always derived, never trusted from decoded caller input.
type Citation struct {
	ToolEventID   uuid.UUID `json:"tool_event_id"`
	MMRLeafHash   [32]byte  `json:"mmr_leaf_hash"`
	MMRRootAtTime [32]byte  `json:"mmr_root_at_time"`
	Verified      bool      `json:"verified"`
}

// Validate rejects malformed citations.
func (citation Citation) Validate() error {
	if citation.ToolEventID == uuid.Nil {
		return fmt.Errorf("protocol: citation tool event ID is required")
	}
	if citation.MMRLeafHash == ([32]byte{}) {
		return fmt.Errorf("protocol: citation MMR leaf hash is required")
	}
	if citation.MMRRootAtTime == ([32]byte{}) {
		return fmt.Errorf("protocol: citation MMR root is required")
	}
	return nil
}

// PredictionRecord captures one expectation-outcome comparison.
type PredictionRecord struct {
	ToolEventID          uuid.UUID `json:"tool_event_id"`
	StrategyKey          string    `json:"strategy_key"`
	Expectation          string    `json:"expectation"`
	ObservedResultDigest string    `json:"observed_result_digest"`
	Matched              bool      `json:"matched"`
	ComparisonMethod     string    `json:"comparison_method"`
	ConsecutiveMismatch  int       `json:"consecutive_mismatch"`
	Timestamp            time.Time `json:"timestamp"`
}
