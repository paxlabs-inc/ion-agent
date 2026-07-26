package action

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrTransientFailure  = errors.New("action: transient failure")
	ErrRateLimited       = errors.New("action: rate limited")
	ErrRecoveryExhausted = errors.New("action: recovery cascade exhausted")
)

// RecoveryFailure records one failed attempt or recovery transition.
type RecoveryFailure struct {
	Layer   FailureLayer `json:"layer"`
	Attempt int          `json:"attempt"`
	Action  string       `json:"action"`
	Error   string       `json:"error"`
}

// HonestFailure is the terminal, inspectable result of exhausting all six
// recovery layers.
type HonestFailure struct {
	Operation string            `json:"operation"`
	Failures  []RecoveryFailure `json:"failures"`
}

func (failure *HonestFailure) Error() string {
	if failure == nil {
		return ErrRecoveryExhausted.Error()
	}
	parts := make([]string, 0, len(failure.Failures))
	for _, item := range failure.Failures {
		parts = append(parts, fmt.Sprintf(
			"%s[%d] %s: %s",
			item.Layer,
			item.Attempt,
			item.Action,
			item.Error,
		))
	}
	return fmt.Sprintf(
		"%s: %s: %s",
		ErrRecoveryExhausted,
		failure.Operation,
		strings.Join(parts, "; "),
	)
}

func (failure *HonestFailure) Unwrap() error {
	return ErrRecoveryExhausted
}

func (layer FailureLayer) String() string {
	switch layer {
	case LayerRetry:
		return "retry"
	case LayerCredentialRotation:
		return "credential-rotation"
	case LayerAuthProfileRotation:
		return "auth-profile-rotation"
	case LayerModelFallback:
		return "model-fallback"
	case LayerRespawn:
		return "respawn"
	case LayerHonestFailure:
		return "honest-failure"
	default:
		return "unknown"
	}
}

// RecoverySteps are the live transitions exercised by CascadeExecutor.
type RecoverySteps struct {
	Attempt           func(context.Context) (json.RawMessage, error)
	RotateCredential  func(context.Context) error
	RotateAuthProfile func(context.Context) error
	FallbackModel     func(context.Context) error
	Respawn           func(context.Context) (json.RawMessage, error)
}

// Backoff waits before a retry. Injection keeps tests deterministic while the
// default uses bounded exponential delay with cryptographic jitter.
type Backoff func(context.Context, int) error

// CascadeExecutor runs the six recovery layers rather than only describing
// which layer would come next.
type CascadeExecutor struct {
	operation  string
	maxRetries int
	steps      RecoverySteps
	backoff    Backoff
}

func NewCascadeExecutor(
	operation string,
	maxRetries int,
	steps RecoverySteps,
	backoff Backoff,
) (*CascadeExecutor, error) {
	if strings.TrimSpace(operation) == "" ||
		steps.Attempt == nil ||
		steps.RotateCredential == nil ||
		steps.RotateAuthProfile == nil ||
		steps.FallbackModel == nil ||
		steps.Respawn == nil {
		return nil, fmt.Errorf("action: complete recovery cascade is required")
	}
	if maxRetries < 0 {
		return nil, fmt.Errorf("action: max retries cannot be negative")
	}
	if backoff == nil {
		backoff = jitteredBackoff
	}
	return &CascadeExecutor{
		operation: operation, maxRetries: maxRetries,
		steps: steps, backoff: backoff,
	}, nil
}

// Execute returns on the first successful attempt. Rate limits immediately
// enter credential rotation; other failures first consume jittered retries.
func (executor *CascadeExecutor) Execute(
	ctx context.Context,
) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	failures := make([]RecoveryFailure, 0, executor.maxRetries+6)
	attempt := 1
	result, err := executor.steps.Attempt(ctx)
	if err == nil {
		return validRecoveryResult(result)
	}
	var incomplete *ErrIncomplete
	if errors.As(err, &incomplete) {
		return nil, incomplete
	}
	failures = appendRecoveryFailure(
		failures, LayerRetry, attempt, "initial attempt", err,
	)

	if !errors.Is(err, ErrRateLimited) {
		for retry := 1; retry <= executor.maxRetries; retry++ {
			if waitErr := executor.backoff(ctx, retry); waitErr != nil {
				return nil, waitErr
			}
			attempt++
			result, err = executor.steps.Attempt(ctx)
			if err == nil {
				return validRecoveryResult(result)
			}
			if errors.As(err, &incomplete) {
				return nil, incomplete
			}
			failures = appendRecoveryFailure(
				failures, LayerRetry, attempt, "jittered retry", err,
			)
			if errors.Is(err, ErrRateLimited) {
				break
			}
		}
	}

	transitions := []struct {
		layer  FailureLayer
		action string
		run    func(context.Context) error
	}{
		{LayerCredentialRotation, "rotate credential", executor.steps.RotateCredential},
		{LayerAuthProfileRotation, "rotate auth profile", executor.steps.RotateAuthProfile},
		{LayerModelFallback, "select fallback model", executor.steps.FallbackModel},
	}
	for _, transition := range transitions {
		if transitionErr := transition.run(ctx); transitionErr != nil {
			failures = appendRecoveryFailure(
				failures,
				transition.layer,
				attempt,
				transition.action,
				transitionErr,
			)
			continue
		}
		attempt++
		result, err = executor.steps.Attempt(ctx)
		if err == nil {
			return validRecoveryResult(result)
		}
		if errors.As(err, &incomplete) {
			return nil, incomplete
		}
		failures = appendRecoveryFailure(
			failures,
			transition.layer,
			attempt,
			transition.action+" then attempt",
			err,
		)
	}

	attempt++
	result, err = executor.steps.Respawn(ctx)
	if err == nil {
		return validRecoveryResult(result)
	}
	if errors.As(err, &incomplete) {
		return nil, incomplete
	}
	failures = appendRecoveryFailure(
		failures,
		LayerRespawn,
		attempt,
		"respawn over durable state",
		err,
	)
	failures = appendRecoveryFailure(
		failures,
		LayerHonestFailure,
		attempt,
		"report complete recovery context",
		ErrRecoveryExhausted,
	)
	return nil, &HonestFailure{
		Operation: executor.operation,
		Failures:  failures,
	}
}

func validRecoveryResult(result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	if !json.Valid(result) {
		return nil, fmt.Errorf("action: recovery attempt returned invalid JSON")
	}
	return append(json.RawMessage(nil), result...), nil
}

func appendRecoveryFailure(
	failures []RecoveryFailure,
	layer FailureLayer,
	attempt int,
	action string,
	err error,
) []RecoveryFailure {
	return append(failures, RecoveryFailure{
		Layer: layer, Attempt: attempt, Action: action, Error: err.Error(),
	})
}

func jitteredBackoff(ctx context.Context, attempt int) error {
	if attempt < 1 {
		attempt = 1
	}
	exponent := attempt - 1
	if exponent > 5 {
		exponent = 5
	}
	base := 50 * time.Millisecond * time.Duration(1<<exponent)
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("action: recovery jitter entropy: %w", err)
	}
	// Jitter range is [50%, 150%) of the exponential base.
	fraction := float64(binary.BigEndian.Uint64(random[:])) / float64(^uint64(0))
	delay := time.Duration(float64(base) * (0.5 + fraction))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// DurableTurnState is the minimum checkpoint handed to a fresh agent.
type DurableTurnState struct {
	SessionID  string          `json:"session_id"`
	TurnID     string          `json:"turn_id"`
	Checkpoint json.RawMessage `json:"checkpoint"`
}

// TurnContinuation continues one durable checkpoint.
type TurnContinuation interface {
	Continue(context.Context, DurableTurnState) (json.RawMessage, error)
}

// AgentRespawner creates a fresh agent without losing durable turn state.
type AgentRespawner interface {
	Respawn(context.Context, DurableTurnState) (TurnContinuation, error)
}

// TaskSupervisor converts ErrIncomplete into an actual respawn and
// continuation over durable state.
type TaskSupervisor struct {
	respawner AgentRespawner
}

func NewTaskSupervisor(respawner AgentRespawner) (*TaskSupervisor, error) {
	if respawner == nil {
		return nil, fmt.Errorf("action: respawner is required")
	}
	return &TaskSupervisor{respawner: respawner}, nil
}

func (supervisor *TaskSupervisor) Recover(
	ctx context.Context,
	state DurableTurnState,
	cause error,
) (json.RawMessage, error) {
	var incomplete *ErrIncomplete
	if !errors.As(cause, &incomplete) {
		return nil, fmt.Errorf("action: supervisor requires ErrIncomplete")
	}
	if strings.TrimSpace(state.SessionID) == "" ||
		strings.TrimSpace(state.TurnID) == "" ||
		!json.Valid(state.Checkpoint) {
		return nil, fmt.Errorf("action: complete durable turn state is required")
	}
	continuation, err := supervisor.respawner.Respawn(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("action: respawn after %s: %w", incomplete.Phase, err)
	}
	if continuation == nil {
		return nil, fmt.Errorf("action: respawner returned nil continuation")
	}
	result, err := continuation.Continue(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("action: continue respawned turn: %w", err)
	}
	return validRecoveryResult(result)
}
