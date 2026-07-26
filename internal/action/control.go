// Package action implements the O1 control kernel for action execution with
// typed outcomes, operation manifests, preflight checks, durable run ledger,
// and the six-layer recovery cascade.
package action

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// OperationOutcome is the typed result of an action execution.
type OperationOutcome string

const (
	OutcomeSuccess    OperationOutcome = "success"
	OutcomeFailure    OperationOutcome = "failure"
	OutcomePartial    OperationOutcome = "partial"
	OutcomeIncomplete OperationOutcome = "incomplete"
	OutcomeAborted    OperationOutcome = "aborted"
)

// FailureClass is the closed recovery classification used across the
// agent/control-plane boundary.
type FailureClass string

const (
	FailureTransient  FailureClass = "transient"
	FailureRateLimit  FailureClass = "rate_limit"
	FailureAuth       FailureClass = "authentication"
	FailureValidation FailureClass = "validation"
	FailureTimeout    FailureClass = "timeout"
	FailureLoop       FailureClass = "loop"
	FailurePermanent  FailureClass = "permanent"
)

// FailureLayer identifies which layer in the recovery cascade is handling a failure.
type FailureLayer int

const (
	LayerRetry               FailureLayer = iota // 1. jittered retry
	LayerCredentialRotation                      // 2. credential rotation
	LayerAuthProfileRotation                     // 3. auth-profile rotation
	LayerModelFallback                           // 4. model fallback
	LayerRespawn                                 // 5. fresh-agent respawn
	LayerHonestFailure                           // 6. honest failure
)

// OperationManifest defines what an operation does and its requirements.
type OperationManifest struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	RequiresApproval bool          `json:"requires_approval"`
	IdempotencyKey   string        `json:"idempotency_key,omitempty"`
	Timeout          time.Duration `json:"timeout"`
	MaxRetries       int           `json:"max_retries"`
	Preconditions    []string      `json:"preconditions,omitempty"`
}

// PreflightCheck validates preconditions before execution.
type PreflightCheck func(manifest *OperationManifest) error

// RunEntry is a durable record of one operation execution attempt.
type RunEntry struct {
	ID           string           `json:"id"`
	OperationID  string           `json:"operation_id"`
	Attempt      int              `json:"attempt"`
	Outcome      OperationOutcome `json:"outcome"`
	FailureLayer *FailureLayer    `json:"failure_layer,omitempty"`
	Error        string           `json:"error,omitempty"`
	StartedAt    time.Time        `json:"started_at"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`
	Effect       json.RawMessage  `json:"effect,omitempty"`
	Evidence     json.RawMessage  `json:"evidence,omitempty"`
	Recovery     string           `json:"recovery,omitempty"`
}

// RunLedger is a durable record of all operation executions.
type RunLedger struct {
	mu      sync.Mutex
	entries []RunEntry
}

// NewRunLedger creates a new run ledger.
func NewRunLedger() *RunLedger {
	return &RunLedger{}
}

// Record adds an entry to the ledger.
func (l *RunLedger) Record(entry RunEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

// Entries returns a defensive copy of all entries.
func (l *RunLedger) Entries() []RunEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]RunEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

// RecoveryCascade implements the six-layer recovery cascade:
// 1. Jittered retry
// 2. Credential rotation
// 3. Auth-profile rotation
// 4. Model fallback
// 5. Respawn
// 6. Honest failure
type RecoveryCascade struct {
	mu           sync.Mutex
	maxRetries   int
	currentLayer FailureLayer
	attempt      int
}

// NewRecoveryCascade creates a recovery cascade.
func NewRecoveryCascade(maxRetries int) *RecoveryCascade {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	return &RecoveryCascade{
		maxRetries: maxRetries,
	}
}

// Next returns the next recovery action to try, or LayerHonestFailure if exhausted.
func (rc *RecoveryCascade) Next() (FailureLayer, int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.attempt++

	// Layer 1: jittered retry (up to maxRetries).
	if rc.attempt <= rc.maxRetries {
		return LayerRetry, rc.attempt
	}

	// Layer 2-5: escalate through layers.
	remaining := rc.attempt - rc.maxRetries
	switch {
	case remaining == 1:
		return LayerCredentialRotation, rc.attempt
	case remaining == 2:
		return LayerAuthProfileRotation, rc.attempt
	case remaining == 3:
		return LayerModelFallback, rc.attempt
	case remaining == 4:
		return LayerRespawn, rc.attempt
	default:
		// Layer 6: honest failure.
		return LayerHonestFailure, rc.attempt
	}
}

// Reset resets the cascade for a new operation.
func (rc *RecoveryCascade) Reset() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.attempt = 0
	rc.currentLayer = LayerRetry
}

// Attempt returns the current attempt number.
func (rc *RecoveryCascade) Attempt() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.attempt
}

// ErrIncomplete is returned when an operation cannot complete.
type ErrIncomplete struct {
	Phase      string          `json:"phase"`
	LastTool   string          `json:"last_tool"`
	LastResult json.RawMessage `json:"last_result"`
	StuckSince time.Time       `json:"stuck_since"`
	Recovery   string          `json:"recovery"`
	Attempt    int             `json:"attempt"`
}

func (e *ErrIncomplete) Error() string {
	return fmt.Sprintf("incomplete: phase=%s tool=%s attempt=%d recovery=%s",
		e.Phase, e.LastTool, e.Attempt, e.Recovery)
}

// IdempotencyJoin ensures that uncertain state-changing transport results
// are reconciled before retry.
type IdempotencyJoin struct {
	mu       sync.Mutex
	pending  map[string]RunEntry
	resolved map[string]RunEntry
}

// NewIdempotencyJoin creates a new idempotency join.
func NewIdempotencyJoin() *IdempotencyJoin {
	return &IdempotencyJoin{
		pending:  make(map[string]RunEntry),
		resolved: make(map[string]RunEntry),
	}
}

// Register registers an operation as pending.
func (j *IdempotencyJoin) Register(key string, entry RunEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pending[key] = entry
}

// Resolve marks an operation as resolved.
func (j *IdempotencyJoin) Resolve(key string, entry RunEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.pending, key)
	j.resolved[key] = entry
}

// IsPending checks if an operation is still pending.
func (j *IdempotencyJoin) IsPending(key string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	_, ok := j.pending[key]
	return ok
}

// PendingCount returns the number of pending operations.
func (j *IdempotencyJoin) PendingCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.pending)
}
