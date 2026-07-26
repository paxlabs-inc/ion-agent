package automatrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/ssrf"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

const (
	// MaxToolCallsPerCycle is the binding idle-cycle action budget.
	MaxToolCallsPerCycle  = 20
	maxFetchResponseBytes = 2 << 20
)

// ToolExecutor is the real policy-intercepted tool manager boundary.
type ToolExecutor interface {
	Execute(context.Context, protocol.NormalizedToolCall) (json.RawMessage, error)
}

// Runner executes queued work through the idle principal and the sole
// SSRF-checked fetch path.
type Runner struct {
	queue      *Queue
	tools      ToolExecutor
	dispatcher *ssrf.Dispatcher
	guard      ExecutionGuard
}

// ExecutionBudget is a hard per-item ceiling returned immediately before
// unattended execution. Disallowed work remains queued without side effects.
type ExecutionBudget struct {
	Allowed    bool
	MaxCalls   int
	MaxErrors  int
	MaxElapsed time.Duration
}

// ExecutionGuard resolves the owning actor's current autonomy settings.
type ExecutionGuard func(context.Context, WorkItem) (ExecutionBudget, error)

// RunnerOption configures an unattended runner boundary.
type RunnerOption func(*Runner)

// WithExecutionGuard requires a fresh operator budget for each queued item.
func WithExecutionGuard(guard ExecutionGuard) RunnerOption {
	return func(runner *Runner) { runner.guard = guard }
}

// NewRunner constructs the fail-closed idle runner. A concrete SSRF dispatcher
// is required so callers cannot substitute an unrestricted HTTP client.
func NewRunner(queue *Queue, tools ToolExecutor, dispatcher *ssrf.Dispatcher, options ...RunnerOption) (*Runner, error) {
	if queue == nil || tools == nil || dispatcher == nil {
		return nil, fmt.Errorf("automatrix: queue, tool executor, and SSRF dispatcher are required")
	}
	runner := &Runner{queue: queue, tools: tools, dispatcher: dispatcher}
	for _, option := range options {
		if option != nil {
			option(runner)
		}
	}
	return runner, nil
}

// ActionOutcome records one attempted idle action.
type ActionOutcome struct {
	ItemID   string          `json:"item_id"`
	ToolName string          `json:"tool_name,omitempty"`
	FetchURL string          `json:"fetch_url,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Err      error           `json:"-"`
}

// CycleResult is inspectable even when individual operations are denied.
type CycleResult struct {
	Calls    int             `json:"calls"`
	Outcomes []ActionOutcome `json:"outcomes"`
}

// RunCycle drains work until the 20-call budget is exhausted. Unattempted
// actions are requeued atomically under the same item ID.
func (runner *Runner) RunCycle(ctx context.Context) (CycleResult, error) {
	var cycle CycleResult
	skipped := make(map[string]struct{})
	for cycle.Calls < MaxToolCallsPerCycle {
		item, found, err := runner.queue.dequeueExcluding(ctx, skipped)
		if err != nil {
			return cycle, err
		}
		if !found {
			break
		}
		if item.ApprovedAt == nil {
			// Proposal-only work remains visible until the owning user approves
			// an exact bounded plan.
			if err := runner.queue.Enqueue(ctx, item); err != nil {
				return cycle, err
			}
			break
		}
		if len(item.Actions) == 0 {
			// Targets can be queued before a later presence planner turns them
			// into actions. Preserve them rather than pretending work occurred.
			if err := runner.queue.Enqueue(ctx, item); err != nil {
				return cycle, err
			}
			break
		}
		budget := ExecutionBudget{Allowed: true, MaxCalls: MaxToolCallsPerCycle,
			MaxErrors: MaxToolCallsPerCycle, MaxElapsed: 24 * time.Hour}
		if runner.guard != nil {
			budget, err = runner.guard(ctx, item)
			if err != nil {
				_ = runner.queue.Enqueue(context.WithoutCancel(ctx), item)
				return cycle, err
			}
		}
		if !budget.Allowed {
			if err := runner.queue.Enqueue(ctx, item); err != nil {
				return cycle, err
			}
			skipped[item.ID.String()] = struct{}{}
			continue
		}
		if budget.MaxCalls < 1 || budget.MaxCalls > MaxToolCallsPerCycle ||
			budget.MaxErrors < 1 || budget.MaxErrors > MaxToolCallsPerCycle ||
			budget.MaxElapsed <= 0 || budget.MaxElapsed > 24*time.Hour {
			_ = runner.queue.Enqueue(context.WithoutCancel(ctx), item)
			return cycle, fmt.Errorf("automatrix: execution guard returned an invalid budget")
		}
		remaining := MaxToolCallsPerCycle - cycle.Calls
		if budget.MaxCalls < remaining {
			remaining = budget.MaxCalls
		}
		attempt := len(item.Actions)
		if attempt > remaining {
			attempt = remaining
		}
		itemCtx, cancelItem := context.WithTimeout(ctx, budget.MaxElapsed)
		executed := 0
		errorsSeen := 0
		for _, action := range item.Actions[:attempt] {
			outcome := runner.execute(itemCtx, item, action)
			cycle.Outcomes = append(cycle.Outcomes, outcome)
			cycle.Calls++
			executed++
			if outcome.Err != nil {
				errorsSeen++
			}
			if errorsSeen >= budget.MaxErrors || itemCtx.Err() != nil {
				break
			}
		}
		cancelItem()
		if executed < len(item.Actions) {
			item.Actions = append([]Action(nil), item.Actions[executed:]...)
			if err := runner.queue.Enqueue(ctx, item); err != nil {
				return cycle, err
			}
			skipped[item.ID.String()] = struct{}{}
		}
	}
	return cycle, nil
}

func (runner *Runner) execute(
	ctx context.Context,
	item WorkItem,
	action Action,
) (outcome ActionOutcome) {
	outcome = ActionOutcome{ItemID: item.ID.String()}
	if action.ToolCall != nil {
		outcome.ToolName = action.ToolCall.Name
		principal := policy.Principal{Sender: policy.SenderAutomatrix}
		if item.Source == SourceCuriosity {
			principal.Sender = policy.SenderCuriosity
		} else if item.Source == SourceDreamweaver {
			principal.Sender = policy.SenderDreamweaver
		}
		result, err := runner.tools.Execute(policy.WithPrincipal(ctx, principal), *action.ToolCall)
		outcome.Result = append(json.RawMessage(nil), result...)
		outcome.Err = err
		return outcome
	}

	outcome.FetchURL = action.FetchURL
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, action.FetchURL, nil)
	if err != nil {
		outcome.Err = fmt.Errorf("automatrix: construct fetch: %w", err)
		return outcome
	}
	response, err := runner.dispatcher.Do(request)
	if err != nil {
		outcome.Err = fmt.Errorf("automatrix: SSRF fetch: %w", err)
		return outcome
	}
	defer func() {
		outcome.Err = errors.Join(outcome.Err, response.Body.Close())
	}()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxFetchResponseBytes+1))
	if err != nil {
		outcome.Err = fmt.Errorf("automatrix: read fetch: %w", err)
		return outcome
	}
	if len(payload) > maxFetchResponseBytes {
		outcome.Err = fmt.Errorf("automatrix: fetch response exceeds size limit")
		return outcome
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		outcome.Err = fmt.Errorf("automatrix: fetch HTTP status %d", response.StatusCode)
		return outcome
	}
	if strings.TrimSpace(string(payload)) == "" {
		payload = []byte("null")
	} else if !json.Valid(payload) {
		encoded, marshalErr := json.Marshal(string(payload))
		if marshalErr != nil {
			outcome.Err = marshalErr
			return outcome
		}
		payload = encoded
	}
	outcome.Result = payload
	return outcome
}
