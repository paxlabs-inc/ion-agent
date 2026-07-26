package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCascadeExecutorRetriesTransientFailureWithBackoff(t *testing.T) {
	t.Parallel()
	attempts := 0
	waits := 0
	executor, err := NewCascadeExecutor(
		"fetch",
		2,
		RecoverySteps{
			Attempt: func(context.Context) (json.RawMessage, error) {
				attempts++
				if attempts < 2 {
					return nil, fmt.Errorf("%w: connection reset", ErrTransientFailure)
				}
				return json.RawMessage(`{"ok":true}`), nil
			},
			RotateCredential:  func(context.Context) error { return nil },
			RotateAuthProfile: func(context.Context) error { return nil },
			FallbackModel:     func(context.Context) error { return nil },
			Respawn: func(context.Context) (json.RawMessage, error) {
				return nil, errors.New("unexpected respawn")
			},
		},
		func(context.Context, int) error {
			waits++
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background())
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatalf("Execute() = %s, %v", result, err)
	}
	if attempts != 2 || waits != 1 {
		t.Fatalf("attempts = %d, waits = %d", attempts, waits)
	}
}

func TestCascadeExecutorRateLimitRotatesCredentialImmediately(t *testing.T) {
	t.Parallel()
	var order []string
	attempts := 0
	executor, err := NewCascadeExecutor(
		"provider",
		3,
		RecoverySteps{
			Attempt: func(context.Context) (json.RawMessage, error) {
				attempts++
				order = append(order, fmt.Sprintf("attempt-%d", attempts))
				if attempts == 1 {
					return nil, fmt.Errorf("%w: quota", ErrRateLimited)
				}
				return json.RawMessage(`{"provider":"rotated"}`), nil
			},
			RotateCredential: func(context.Context) error {
				order = append(order, "credential")
				return nil
			},
			RotateAuthProfile: func(context.Context) error {
				order = append(order, "auth")
				return nil
			},
			FallbackModel: func(context.Context) error {
				order = append(order, "model")
				return nil
			},
			Respawn: func(context.Context) (json.RawMessage, error) {
				order = append(order, "respawn")
				return nil, errors.New("unexpected")
			},
		},
		func(context.Context, int) error {
			order = append(order, "backoff")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "attempt-1,credential,attempt-2" {
		t.Fatalf("recovery order = %s", got)
	}
}

func TestCascadeExecutorExhaustionReportsEveryLayer(t *testing.T) {
	t.Parallel()
	executor, err := NewCascadeExecutor(
		"deploy",
		1,
		RecoverySteps{
			Attempt: func(context.Context) (json.RawMessage, error) {
				return nil, errors.New("attempt failed")
			},
			RotateCredential: func(context.Context) error {
				return errors.New("no credentials")
			},
			RotateAuthProfile: func(context.Context) error {
				return errors.New("no auth profiles")
			},
			FallbackModel: func(context.Context) error {
				return errors.New("no fallback models")
			},
			Respawn: func(context.Context) (json.RawMessage, error) {
				return nil, errors.New("respawn failed")
			},
		},
		func(context.Context, int) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background())
	var honest *HonestFailure
	if !errors.As(err, &honest) || !errors.Is(err, ErrRecoveryExhausted) {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, layer := range []FailureLayer{
		LayerRetry,
		LayerCredentialRotation,
		LayerAuthProfileRotation,
		LayerModelFallback,
		LayerRespawn,
		LayerHonestFailure,
	} {
		found := false
		for _, failure := range honest.Failures {
			if failure.Layer == layer {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("honest failure omitted %s: %+v", layer, honest.Failures)
		}
	}
	message := honest.Error()
	for _, detail := range []string{
		"attempt failed",
		"no credentials",
		"no auth profiles",
		"no fallback models",
		"respawn failed",
	} {
		if !strings.Contains(message, detail) {
			t.Fatalf("honest failure omitted %q: %s", detail, message)
		}
	}
}

type recoveryContinuation struct {
	state DurableTurnState
}

func (continuation *recoveryContinuation) Continue(
	_ context.Context,
	state DurableTurnState,
) (json.RawMessage, error) {
	continuation.state = state
	return json.RawMessage(`{"continued":true}`), nil
}

type recoveryRespawner struct {
	state        DurableTurnState
	continuation *recoveryContinuation
}

func (respawner *recoveryRespawner) Respawn(
	_ context.Context,
	state DurableTurnState,
) (TurnContinuation, error) {
	respawner.state = state
	respawner.continuation = &recoveryContinuation{}
	return respawner.continuation, nil
}

func TestTaskSupervisorRespawnsAndContinuesDurableIncompleteTurn(t *testing.T) {
	t.Parallel()
	respawner := &recoveryRespawner{}
	supervisor, err := NewTaskSupervisor(respawner)
	if err != nil {
		t.Fatal(err)
	}
	state := DurableTurnState{
		SessionID: "session-a",
		TurnID:    "turn-7",
		Checkpoint: json.RawMessage(
			`{"messages":4,"last_tool":"deploy"}`,
		),
	}
	result, err := supervisor.Recover(
		context.Background(),
		state,
		&ErrIncomplete{
			Phase: "provider", LastTool: "deploy",
			StuckSince: time.Unix(1, 0), Recovery: "respawn", Attempt: 4,
		},
	)
	if err != nil || string(result) != `{"continued":true}` {
		t.Fatalf("Recover() = %s, %v", result, err)
	}
	if respawner.state.SessionID != state.SessionID ||
		respawner.continuation.state.TurnID != state.TurnID ||
		string(respawner.continuation.state.Checkpoint) != string(state.Checkpoint) {
		t.Fatalf(
			"durable state lost: respawn=%+v continuation=%+v",
			respawner.state,
			respawner.continuation.state,
		)
	}
}
