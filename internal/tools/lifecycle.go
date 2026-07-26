package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

const (
	MaximumProgressBytes        = 16 << 10
	MaximumProgressEvents       = 64
	MaximumLifecycleResultBytes = 2 << 20
	progressInterval            = 100 * time.Millisecond
)

// LifecyclePhase is the canonical manager-time state of one real tool call.
type LifecyclePhase string

const (
	LifecycleRequested        LifecyclePhase = "requested"
	LifecycleAwaitingApproval LifecyclePhase = "awaiting_approval"
	LifecycleStarted          LifecyclePhase = "started"
	LifecycleProgress         LifecyclePhase = "progress"
	LifecycleCompleted        LifecyclePhase = "completed"
	LifecycleFailed           LifecyclePhase = "failed"
	LifecycleDenied           LifecyclePhase = "denied"
	LifecycleInterrupted      LifecyclePhase = "interrupted"
	LifecycleOutcomeUnknown   LifecyclePhase = "outcome_unknown"
)

// Terminal reports whether phase closes one logical execution.
func (phase LifecyclePhase) Terminal() bool {
	switch phase {
	case LifecycleCompleted, LifecycleFailed, LifecycleDenied,
		LifecycleInterrupted, LifecycleOutcomeUnknown:
		return true
	default:
		return false
	}
}

// LifecycleObservation is a bounded, transport-neutral manager observation.
type LifecycleObservation struct {
	Binding        protocol.ToolExecutionBinding
	Call           protocol.NormalizedToolCall
	Description    string
	Classification Classification
	Phase          LifecyclePhase
	OccurredAt     time.Time
	Progress       json.RawMessage
	Result         json.RawMessage
	ResultBytes    int
	ResultOmitted  bool
	Error          error
}

// LifecycleObserver durably projects observations without owning execution.
type LifecycleObserver interface {
	Observe(context.Context, LifecycleObservation) error
}

// LifecycleObserverFunc adapts a function to LifecycleObserver.
type LifecycleObserverFunc func(context.Context, LifecycleObservation) error

func (observer LifecycleObserverFunc) Observe(
	ctx context.Context,
	observation LifecycleObservation,
) error {
	return observer(ctx, observation)
}

// ErrLifecycleProjection indicates that the durable lifecycle boundary failed.
var ErrLifecycleProjection = errors.New("tools: lifecycle projection failed")

// OutcomeUnknownError marks a failed call whose external consequence cannot be
// determined safely.
type OutcomeUnknownError struct {
	Err error
}

func (failure *OutcomeUnknownError) Error() string {
	if failure == nil || failure.Err == nil {
		return "tools: outcome unknown"
	}
	return "tools: outcome unknown: " + failure.Err.Error()
}

func (failure *OutcomeUnknownError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// MarkOutcomeUnknown preserves the underlying error while making uncertainty
// explicit to lifecycle projection and callers.
func MarkOutcomeUnknown(err error) error {
	if err == nil {
		err = errors.New("external effect could not be confirmed")
	}
	return &OutcomeUnknownError{Err: err}
}

// TerminalPhase classifies one returned execution error.
func TerminalPhase(ctx context.Context, err error) LifecyclePhase {
	if err == nil {
		return LifecycleCompleted
	}
	var uncertain *OutcomeUnknownError
	if errors.As(err, &uncertain) {
		return LifecycleOutcomeUnknown
	}
	if errors.Is(err, context.Canceled) ||
		(ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return LifecycleInterrupted
	}
	if errors.Is(err, ErrPolicyDenied) {
		return LifecycleDenied
	}
	return LifecycleFailed
}

type progressReporterKey struct{}

type progressReporter struct {
	mu       sync.Mutex
	clock    func() time.Time
	last     time.Time
	emitted  int
	emit     func(json.RawMessage) error
	terminal bool
}

// ReportProgress emits a bounded coalesced update when the active handler is
// running under a lifecycle-enabled Manager.
func ReportProgress(ctx context.Context, payload json.RawMessage) error {
	reporter, _ := ctx.Value(progressReporterKey{}).(*progressReporter)
	if reporter == nil {
		return nil
	}
	if len(payload) == 0 || len(payload) > MaximumProgressBytes ||
		!json.Valid(payload) {
		return fmt.Errorf("tools: progress must be bounded valid JSON")
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	if reporter.terminal || reporter.emitted >= MaximumProgressEvents {
		return nil
	}
	now := reporter.clock()
	if reporter.emitted > 0 && now.Sub(reporter.last) < progressInterval {
		return nil
	}
	if err := reporter.emit(append(json.RawMessage(nil), payload...)); err != nil {
		return err
	}
	reporter.last = now
	reporter.emitted++
	return nil
}

func lifecycleError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrLifecycleProjection, err)
}
