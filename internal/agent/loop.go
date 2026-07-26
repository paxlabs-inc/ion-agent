// Package agent orchestrates the provider-neutral perception and action loop.
//
// The loop integrates premise tracking, prediction-carrying dispatch,
// task DAG convergence, self-model observation, Cassandra doubt control,
// and citation verification through narrow interfaces.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/action"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/belief/selfmodel"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/decision"
	"github.com/paxlabs-inc/ion-agent/internal/reflection/cassandra"
	"github.com/paxlabs-inc/ion-agent/internal/security/circuit"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"lukechampine.com/blake3"
)

const (
	defaultMaxToolCalls          = 128
	defaultIdleTimeout           = 2 * time.Minute
	defaultRepeatedToolLimit     = 4
	defaultContextPrepareTimeout = 2 * time.Second
)

var (
	// ErrToolCallLimit indicates that a provider did not converge within the
	// configured action budget.
	ErrToolCallLimit = errors.New("agent: tool call limit reached")
	// ErrRefutedPremise indicates a premise was refuted, blocking dispatch.
	ErrRefutedPremise = errors.New("agent: premise refuted, replanning required")
	// ErrConvergenceExceeded indicates the task graph convergence window was exceeded.
	ErrConvergenceExceeded = errors.New("agent: convergence window exceeded, revision required")
	// ErrMissingExpectation indicates a prediction-enabled dispatch omitted its
	// mandatory one-line expectation.
	ErrMissingExpectation = errors.New("agent: tool call missing required expect parameter")
	// ErrContextPreparationTimeout indicates that bounded living-context
	// preparation could not finish before the provider request deadline.
	ErrContextPreparationTimeout = errors.New("agent: living context preparation timed out")
	// ErrTextualToolMarkup indicates that provider-internal tool syntax could
	// not be safely normalized and must not escape as an assistant response.
	ErrTextualToolMarkup = errors.New("agent: invalid textual tool markup")
)

const (
	textualToolRepairPrompt     = "The previous provider response used invalid textual tool-call markup. Do not repeat or quote that markup. Either emit a native structured tool call that satisfies the active JSON Schema, including the required expect prediction, or return a normal final answer with no tool-call tags."
	textualToolFreeRepairPrompt = "The previous response used tool-call markup even though tools are disabled for this plan-revision step. Return only the revised plan as ordinary text. Do not emit or quote tool names, tool-call tags, function tags, parameter tags, XML, JSON, or code fences."
	textualToolRepairLimit      = 2
	textualToolFinalPrompt      = "Tool use is now disabled because repeated responses used unsafe tool-call markup. Give the user an honest ordinary-text answer from the available conversation and evidence. State any action you could not complete. Do not emit or quote tool names, tool-call tags, function tags, parameter tags, XML, JSON, or code fences."
	expectationRepairPrompt     = "The previous structured tool call was not executed because at least one call omitted its required non-empty expect prediction. Resubmit the intended native structured tool call, adding a one-line expect prediction to every call, or answer normally without tools."
	expectationRepairLimit      = 2
	expectationFinalPrompt      = "Tool use is now disabled because repeated structured calls omitted the required expect prediction. Give the user an honest ordinary-text answer from the available conversation and evidence, and state any action you could not complete. Do not call tools."
	emptyFinalRepairPrompt      = "The previous response ended without a user-facing answer. Answer the original user request now as ordinary text using the available conversation and tool results. Do not call tools, emit tool-call markup, or return an empty response."
	finalAnswerRepairLimit      = 2
)

// Generator is the narrow provider contract consumed by the basic loop.
type Generator interface {
	Generate(context.Context, protocol.GenerationRequest) (protocol.NormalizedGeneration, error)
}

// ToolManager is the narrow registry contract consumed by the basic loop.
type ToolManager interface {
	Surface(context.Context) []protocol.ToolDefinition
	Execute(context.Context, protocol.NormalizedToolCall) (json.RawMessage, error)
}

// LoopConfig controls the bounded basic loop.
type LoopConfig struct {
	Model                 string
	SystemPrompt          string
	UserID                string
	SessionID             string
	MaxToolCalls          int
	ConvergenceWindow     int
	IdleTimeout           time.Duration
	RepeatedToolLimit     int
	ContextPrepareTimeout time.Duration
	MaxOutputTokens       int
}

// ToolExecution records one action result returned during a turn.
type ToolExecution struct {
	Call   protocol.NormalizedToolCall
	Result json.RawMessage
	Error  string
	Event  *protocol.ToolEvent
	// Streamed indicates that the concrete manager durably emitted lifecycle
	// events while execution was in progress. Coordinators use it to avoid
	// duplicating those events after the turn completes.
	Streamed bool
}

// Response is the completed basic-loop turn.
type Response struct {
	Content           string
	Generation        protocol.NormalizedGeneration
	ToolEvents        []ToolExecution
	ProviderCalls     int
	Usage             protocol.TokenUsage
	Checkpoint        *TurnCheckpoint
	ContentStreamed   bool
	ReasoningStreamed bool
	HonestPartial     bool
}

// TurnCheckpoint is the durable execution cursor required to continue after
// restart without replaying completed tool effects.
type TurnCheckpoint struct {
	Version           int                          `json:"version"`
	UserContent       string                       `json:"user_content"`
	Messages          []protocol.Message           `json:"messages"`
	ToolEvents        []ToolExecution              `json:"tool_events"`
	ProviderCalls     int                          `json:"provider_calls"`
	Usage             protocol.TokenUsage          `json:"usage"`
	ToolCalls         int                          `json:"tool_calls"`
	LastToolSignature string                       `json:"last_tool_signature"`
	RepeatedToolCalls int                          `json:"repeated_tool_calls"`
	PlanFormed        bool                         `json:"plan_formed"`
	RevisionStep      bool                         `json:"revision_step"`
	RevisionTargets   []string                     `json:"revision_targets,omitempty"`
	PendingCall       *protocol.NormalizedToolCall `json:"pending_call,omitempty"`
}

// Loop owns no background goroutines. Provider and tool cancellation is driven
// entirely by the Turn context.
type Loop struct {
	provider          Generator
	tools             ToolManager
	config            LoopConfig
	premises          PremiseLedger
	premiseExtractor  PlanPremiseExtractor
	predictions       PredictionEngine
	predictionRecords PredictionRecordStore
	taskGraph         TaskDAG
	selfModel         SelfModelManager
	cassandra         CassandraController
	citations         CitationVerifier
	events            ToolEventStore
	eventCommitter    ToolEventCommitter
	circuitBreaker    CircuitBreaker
	memoryActivation  MemoryActivation
	behavioral        BehavioralModulator
	decisionPolicy    LivenessDecisionProvider
	contextComposer   ContextComposer
	checkpoints       CheckpointSink
	completionGate    CompletionGate
	clock             Clock
}

// CompletionDecision is a controller-owned convergence result. Ready is true
// only when durable evidence satisfies the active outcome. Stop is reserved
// for a genuine decision, authority, safety, or explicit budget boundary.
type CompletionDecision struct {
	Ready      bool
	Stop       bool
	Reason     string
	NextAction string
}

type CompletionGate interface {
	CheckCompletion(context.Context) (CompletionDecision, error)
}

// Clock abstracts time for deterministic testing.
type Clock interface {
	Now() time.Time
}

// LoopDeps holds the optional epistemic dependencies for the loop.
type LoopDeps struct {
	Premises          PremiseLedger
	PremiseExtractor  PlanPremiseExtractor
	Predictions       PredictionEngine
	PredictionRecords PredictionRecordStore
	TaskGraph         TaskDAG
	SelfModel         SelfModelManager
	Cassandra         CassandraController
	Citations         CitationVerifier
	Events            ToolEventStore
	EventCommitter    ToolEventCommitter
	CircuitBreaker    CircuitBreaker
	MemoryActivation  MemoryActivation
	Behavioral        BehavioralModulator
	DecisionPolicy    LivenessDecisionProvider
	ContextComposer   ContextComposer
	Checkpoints       CheckpointSink
	CompletionGate    CompletionGate
	Clock             Clock
}

// NewLoop validates and constructs a basic O1 agent loop.
func NewLoop(provider Generator, tools ToolManager, config LoopConfig, deps *LoopDeps) (*Loop, error) {
	if provider == nil || tools == nil {
		return nil, fmt.Errorf("agent: provider and tool manager are required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("agent: model is required")
	}
	if config.MaxToolCalls < 0 {
		return nil, fmt.Errorf("agent: max tool calls cannot be negative")
	}
	if config.IdleTimeout < 0 || config.RepeatedToolLimit < 0 ||
		config.ContextPrepareTimeout < 0 {
		return nil, fmt.Errorf(
			"agent: idle timeout, repeated tool limit, and context preparation timeout cannot be negative",
		)
	}
	if config.MaxToolCalls == 0 {
		config.MaxToolCalls = defaultMaxToolCalls
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = defaultIdleTimeout
	}
	if config.RepeatedToolLimit == 0 {
		config.RepeatedToolLimit = defaultRepeatedToolLimit
	}
	if config.ContextPrepareTimeout == 0 {
		config.ContextPrepareTimeout = defaultContextPrepareTimeout
	}
	loop := &Loop{
		provider: provider,
		tools:    tools,
		config:   config,
	}
	if deps != nil {
		loop.premises = deps.Premises
		loop.premiseExtractor = deps.PremiseExtractor
		loop.predictions = deps.Predictions
		loop.predictionRecords = deps.PredictionRecords
		loop.taskGraph = deps.TaskGraph
		loop.selfModel = deps.SelfModel
		loop.cassandra = deps.Cassandra
		loop.citations = deps.Citations
		loop.events = deps.Events
		loop.eventCommitter = deps.EventCommitter
		loop.circuitBreaker = deps.CircuitBreaker
		loop.memoryActivation = deps.MemoryActivation
		loop.behavioral = deps.Behavioral
		loop.decisionPolicy = deps.DecisionPolicy
		loop.contextComposer = deps.ContextComposer
		loop.checkpoints = deps.Checkpoints
		loop.completionGate = deps.CompletionGate
		loop.clock = deps.Clock
	}
	if loop.memoryActivation != nil &&
		(strings.TrimSpace(config.UserID) == "" ||
			strings.TrimSpace(config.SessionID) == "") {
		return nil, fmt.Errorf(
			"agent: memory activation requires user and session IDs",
		)
	}
	if loop.predictions != nil && (loop.clock == nil || loop.eventCommitter == nil) {
		return nil, fmt.Errorf("agent: predictions require clock and durable ToolEvent committer")
	}
	if loop.selfModel != nil && (loop.clock == nil || loop.eventCommitter == nil) {
		return nil, fmt.Errorf("agent: self-model requires clock and durable ToolEvent committer")
	}
	if loop.eventCommitter != nil && loop.clock == nil {
		return nil, fmt.Errorf("agent: durable ToolEvent committer requires clock")
	}
	return loop, nil
}

// Turn runs perception (message plus ready tool surface), provider intent,
// action dispatch, and the provider's final response.
func (loop *Loop) Turn(ctx context.Context, userContent string) (Response, error) {
	return loop.runTurn(ctx, userContent, nil)
}

// Resume continues a validated durable cursor without replaying its completed
// tool calls.
func (loop *Loop) Resume(
	ctx context.Context,
	userContent string,
	checkpoint TurnCheckpoint,
) (Response, error) {
	if err := checkpoint.validate(userContent); err != nil {
		return Response{}, err
	}
	return loop.runTurn(ctx, userContent, &checkpoint)
}

func (loop *Loop) runTurn(
	ctx context.Context,
	userContent string,
	checkpoint *TurnCheckpoint,
) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(userContent) == "" {
		return Response{}, fmt.Errorf("agent: user message is required")
	}
	if checkpoint == nil && loop.predictions != nil {
		loop.predictions.BeginTurn()
	}
	if err := loop.enforceCircuit("turn", Response{}); err != nil {
		return Response{}, err
	}
	var activePolicy *decision.LivenessDecisionPolicy
	if loop.decisionPolicy != nil {
		policyContext := decision.Context{UserContent: userContent}
		if loop.premises != nil {
			for _, item := range loop.premises.Active() {
				if item != nil && item.Status == premise.Assumption {
					policyContext.UnsupportedPremises++
				}
			}
		}
		if counter, ok := loop.taskGraph.(interface{ ActionsSinceGrowth() int }); ok {
			policyContext.ActionsWithoutGrowth = counter.ActionsSinceGrowth()
		}
		derived, err := loop.decisionPolicy.LivenessDecisionPolicy(ctx, policyContext)
		if err != nil {
			return Response{}, fmt.Errorf("agent: derive liveness decision policy: %w", err)
		}
		if err := derived.Validate(); err != nil {
			return Response{}, fmt.Errorf("agent: validate liveness decision policy: %w", err)
		}
		activePolicy = &derived
	}
	toolCallLimit := loop.config.MaxToolCalls
	if activePolicy != nil && activePolicy.ToolCallBudget < toolCallLimit {
		toolCallLimit = activePolicy.ToolCallBudget
	}

	// A restart-resume remains the same turn, so its edit budget continues.
	if loop.cassandra != nil && checkpoint == nil {
		loop.cassandra.NewTurn()
	}

	messages := []protocol.Message{{Role: protocol.RoleUser, Content: userContent}}

	// Inject expect params into tool schemas.
	toolDefs := loop.tools.Surface(ctx)
	if loop.predictions != nil {
		toolDefs = injectExpectParam(toolDefs)
	}

	request := protocol.GenerationRequest{
		Model:           loop.config.Model,
		Messages:        messages,
		Tools:           toolDefs,
		MaxOutputTokens: loop.config.MaxOutputTokens,
	}
	response := Response{}
	toolCalls := 0
	explorationCalls := 0
	lastToolSignature := ""
	repeatedToolCalls := 0
	textualToolRepairs := 0
	emptyFinalRepairs := 0
	planFormed := false
	revisionStep := false
	revisionTargets := loop.refutedSubgoals()
	var pendingCall *protocol.NormalizedToolCall
	if checkpoint != nil {
		request.Messages = cloneMessages(checkpoint.Messages)
		response.ToolEvents = cloneToolExecutions(checkpoint.ToolEvents)
		response.ProviderCalls = checkpoint.ProviderCalls
		response.Usage = checkpoint.Usage
		toolCalls = checkpoint.ToolCalls
		for _, execution := range checkpoint.ToolEvents {
			if isExplorationTool(execution.Call.Name) {
				explorationCalls++
			}
		}
		lastToolSignature = checkpoint.LastToolSignature
		repeatedToolCalls = checkpoint.RepeatedToolCalls
		planFormed = checkpoint.PlanFormed
		revisionStep = checkpoint.RevisionStep
		revisionTargets = append([]string(nil), checkpoint.RevisionTargets...)
		if checkpoint.PendingCall != nil {
			call := cloneToolCall(*checkpoint.PendingCall)
			pendingCall = &call
		}
	}
	textualToolRepairs = countTextualToolRepairs(request.Messages)
	expectationRepairs := countExpectationRepairs(request.Messages)
	finalAnswerRepairs := 0
	finalRepairReason := ""
	revisionRepairRequested := false
	completionDeferrals := 0
	var completionRepair *CompletionDecision
	if loop.premises != nil && loop.premises.HasRefuted() &&
		len(revisionTargets) == 0 {
		return Response{}, ErrRefutedPremise
	}
	if len(revisionTargets) > 0 {
		if loop.premiseExtractor == nil || loop.taskGraph == nil {
			return Response{}, ErrRefutedPremise
		}
		revisionStep = true
	}
	if pendingCall != nil {
		return response, incomplete(
			"effect_reconciliation",
			pendingCall.Name,
			nil,
			loop.now(),
			"reconcile the pending operation by idempotency key before retry",
			toolCalls,
		)
	}
	if err := loop.saveCheckpoint(
		ctx, userContent, request.Messages, response, toolCalls,
		lastToolSignature, repeatedToolCalls, planFormed,
		revisionStep, revisionTargets, nil,
	); err != nil {
		return response, err
	}
	for {
		if err := loop.enforceCircuit("provider", response); err != nil {
			return response, err
		}
		memoryContext := ""
		if loop.memoryActivation != nil {
			activated, activationErr := loop.memoryActivation.Activate(
				ctx,
				userContent,
				loop.config.UserID,
				loop.config.SessionID,
			)
			if activationErr != nil {
				return response, fmt.Errorf(
					"agent: refresh memory activation: %w",
					activationErr,
				)
			}
			memoryContext = activated
		}
		providerRequest := request
		if revisionStep {
			providerRequest.Tools = nil
			providerRequest.Messages = append(
				cloneMessages(providerRequest.Messages),
				protocol.Message{
					Role: protocol.RoleSystem,
					Content: internalRevisionPrompt(
						revisionTargets, revisionRepairRequested,
					),
				},
			)
		}
		if finalRepairReason != "" {
			providerRequest.Messages = append(
				cloneMessages(providerRequest.Messages),
				protocol.Message{
					Role:    protocol.RoleSystem,
					Content: finalAnswerRepairPrompt(finalRepairReason),
				},
			)
		}
		if completionRepair != nil {
			providerRequest.Messages = append(
				cloneMessages(providerRequest.Messages),
				protocol.Message{
					Role: protocol.RoleSystem,
					Content: completionContinuationPrompt(
						*completionRepair,
					),
				},
			)
		}
		activeRequest, contextErr := loop.withEpistemicActivation(
			ctx, providerRequest, userContent, memoryContext,
		)
		if contextErr != nil {
			return response, contextErr
		}
		providerStarted := time.Now().UTC()
		providerCtx, cancelProvider := context.WithTimeout(ctx, loop.config.IdleTimeout)
		generationCtx := providerCtx
		if revisionStep {
			generationCtx = withoutGenerationObserver(providerCtx)
		}
		generation, streamState, err := loop.generate(generationCtx, activeRequest)
		providerContextErr := providerCtx.Err()
		cancelProvider()
		response.ProviderCalls++
		if errors.Is(providerContextErr, context.DeadlineExceeded) && ctx.Err() == nil {
			response.Checkpoint = loop.makeCheckpoint(
				userContent, request.Messages, response, toolCalls,
				lastToolSignature, repeatedToolCalls, planFormed,
				revisionStep, revisionTargets, nil,
			)
			return response, incomplete(
				"provider",
				"",
				nil,
				providerStarted,
				"respawn over durable turn state",
				response.ProviderCalls,
			)
		}
		if err != nil {
			if adaptive, ok := loop.provider.(interface {
				AdvanceGenerationStrategy(string) bool
			}); ok && adaptive.AdvanceGenerationStrategy(err.Error()) {
				if !revisionStep {
					request.Messages = append(request.Messages, protocol.Message{
						Role:    protocol.RoleSystem,
						Content: "The provider compatibility strategy changed after the previous generation failed. Continue from durable evidence without repeating the same response.",
					})
				}
				if checkpointErr := loop.saveCheckpoint(
					ctx, userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				); checkpointErr != nil {
					return response, checkpointErr
				}
				continue
			}
			return Response{}, fmt.Errorf("agent: generate: %w", err)
		}
		if err := generation.Validate(); err != nil {
			return Response{}, fmt.Errorf("agent: provider returned invalid generation: %w", err)
		}
		mergeResponseTokenUsage(
			&response.Usage,
			generation.Usage,
			response.ProviderCalls == 1,
		)
		if normalizeErr := normalizeTextualToolCalls(
			&generation, request.Tools,
		); normalizeErr != nil {
			if streamState.any() {
				_ = resetGenerationObserver(ctx)
			}
			if adaptive, ok := loop.provider.(interface {
				AdvanceGenerationStrategy(string) bool
			}); ok && adaptive.AdvanceGenerationStrategy(normalizeErr.Error()) {
				request.Messages = append(request.Messages, protocol.Message{
					Role:    protocol.RoleSystem,
					Content: "The runtime changed provider tool compatibility mode. Reissue only the intended action through the active structured tool schema.",
				})
				if err := loop.saveCheckpoint(
					ctx, userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				); err != nil {
					return response, err
				}
				continue
			}
			if textualToolRepairs < textualToolRepairLimit {
				textualToolRepairs++
				repairPrompt := textualToolRepairPrompt
				if revisionStep {
					repairPrompt = textualToolFreeRepairPrompt
				}
				if revisionStep {
					revisionRepairRequested = true
				} else {
					request.Messages = append(request.Messages, protocol.Message{
						Role: protocol.RoleSystem, Content: repairPrompt,
					})
				}
				if err := loop.saveCheckpoint(
					ctx, userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				); err != nil {
					return response, err
				}
				continue
			}
			if toolCalls == 0 &&
				!hasSystemPrompt(request.Messages, textualToolFinalPrompt) {
				request.Tools = nil
				request.Messages = append(request.Messages, protocol.Message{
					Role: protocol.RoleSystem, Content: textualToolFinalPrompt,
				})
				if err := loop.saveCheckpoint(
					ctx, userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				); err != nil {
					return response, err
				}
				continue
			}
			if toolCalls > 0 {
				response.Checkpoint = loop.makeCheckpoint(
					userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				)
				lastTool, lastResult := lastToolEvidence(response.ToolEvents)
				return response, incomplete(
					"provider_tool_markup", lastTool, lastResult, loop.now(),
					"resume over durable tool results and require native structured calls or ordinary prose",
					response.ProviderCalls,
				)
			}
			return Response{}, fmt.Errorf(
				"agent: provider returned unsafe tool markup: %w", normalizeErr,
			)
		}
		toolExpectations := make([]string, len(generation.ToolCalls))
		toolArguments := make([]json.RawMessage, len(generation.ToolCalls))
		if loop.predictions != nil {
			var expectationErr error
			for index, call := range generation.ToolCalls {
				toolExpectations[index], toolArguments[index], expectationErr =
					popExpect(call.Arguments)
				if expectationErr != nil {
					break
				}
			}
			if expectationErr != nil {
				if expectationRepairs >= expectationRepairLimit {
					if hasSystemPrompt(request.Messages, expectationFinalPrompt) {
						return response, expectationErr
					}
					if streamState.any() {
						if resetErr := resetGenerationObserver(ctx); resetErr != nil {
							return Response{}, fmt.Errorf(
								"agent: reset invalid prediction attempt: %w", resetErr,
							)
						}
					}
					request.Tools = nil
					request.Messages = append(request.Messages, protocol.Message{
						Role: protocol.RoleSystem, Content: expectationFinalPrompt,
					})
					if err := loop.saveCheckpoint(
						ctx, userContent, request.Messages, response, toolCalls,
						lastToolSignature, repeatedToolCalls, planFormed,
						revisionStep, revisionTargets, nil,
					); err != nil {
						return response, err
					}
					continue
				}
				if streamState.any() {
					if resetErr := resetGenerationObserver(ctx); resetErr != nil {
						return Response{}, fmt.Errorf(
							"agent: reset invalid prediction attempt: %w", resetErr,
						)
					}
				}
				expectationRepairs++
				request.Messages = append(request.Messages, protocol.Message{
					Role: protocol.RoleSystem, Content: expectationRepairPrompt,
				})
				if err := loop.saveCheckpoint(
					ctx, userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				); err != nil {
					return response, err
				}
				continue
			}
		}
		response.Generation = generation
		if len(generation.ToolCalls) == 0 {
			if strings.TrimSpace(generation.Content) == "" {
				if streamState.any() {
					_ = resetGenerationObserver(ctx)
				}
				if emptyFinalRepairs == 0 {
					emptyFinalRepairs++
					request.Tools = nil
					request.Messages = append(request.Messages, protocol.Message{
						Role: protocol.RoleSystem, Content: emptyFinalRepairPrompt,
					})
					if err := loop.saveCheckpoint(
						ctx, userContent, request.Messages, response, toolCalls,
						lastToolSignature, repeatedToolCalls, planFormed,
						revisionStep, revisionTargets, nil,
					); err != nil {
						return response, err
					}
					continue
				}
				return response, incomplete(
					"provider_finalization",
					"",
					nil,
					loop.now(),
					"retry over durable tool results and require a user-facing answer",
					response.ProviderCalls,
				)
			}
			if revisionStep {
				if err := loop.commitPlan(
					ctx, generation, revisionTargets, true,
				); err != nil {
					return Response{}, err
				}
				planFormed = true
				revisionStep = false
				revisionTargets = nil
				revisionRepairRequested = false
				request.Tools = toolDefs
				if err := loop.saveCheckpoint(
					ctx, userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				); err != nil {
					return response, err
				}
				continue
			}
			if accepted, reason := finalAnswerAddressesRequest(
				userContent, generation.Content, response.ToolEvents,
			); !accepted {
				if streamState.any() {
					if resetErr := resetGenerationObserver(ctx); resetErr != nil {
						return Response{}, fmt.Errorf(
							"agent: reset rejected final answer: %w", resetErr,
						)
					}
				}
				if finalAnswerRepairs < finalAnswerRepairLimit {
					finalAnswerRepairs++
					finalRepairReason = reason
					continue
				}
				generation.Content = finalAnswerHonestFallback(response.ToolEvents)
				generation.ToolCalls = nil
				generation.FinishReason = protocol.FinishStop
				response.Content = generation.Content
				response.Generation = generation
				response.ContentStreamed = false
				response.ReasoningStreamed = false
				response.HonestPartial = true
				if err := loop.saveCheckpoint(
					ctx, userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				); err != nil {
					return response, err
				}
				return response, nil
			}
			finalRepairReason = ""
			if loop.completionGate != nil {
				decision, decisionErr := loop.completionGate.CheckCompletion(ctx)
				if decisionErr != nil {
					return response, fmt.Errorf("agent: check evidence convergence: %w", decisionErr)
				}
				if !decision.Ready {
					if decision.Stop {
						response.Content = strings.TrimSpace(generation.Content)
						if decision.Reason != "" {
							response.Content += "\n\nWork is paused at a real boundary: " + decision.Reason
						}
						response.Generation = generation
						response.ContentStreamed = streamState.content
						response.ReasoningStreamed = streamState.reasoning
						return response, nil
					}
					// A rejected final is internal convergence work. Retract any
					// streamed provider text before either continuing or returning a
					// durable incomplete at the bounded deferral ceiling.
					if streamState.any() {
						_ = resetGenerationObserver(ctx)
					}
					if completionDeferrals >= 8 {
						response.Checkpoint = loop.makeCheckpoint(
							userContent, request.Messages, response, toolCalls,
							lastToolSignature, repeatedToolCalls, planFormed,
							revisionStep, revisionTargets, nil,
						)
						return response, incomplete(
							"evidence_convergence", "", nil, loop.now(),
							"continue from the next durable criterion-linked work item",
							response.ProviderCalls,
						)
					}
					completionDeferrals++
					pending := decision
					completionRepair = &pending
					if err := loop.saveCheckpoint(
						ctx, userContent, request.Messages, response, toolCalls,
						lastToolSignature, repeatedToolCalls, planFormed,
						revisionStep, revisionTargets, nil,
					); err != nil {
						return response, err
					}
					continue
				}
				completionRepair = nil
			}
			if !planFormed &&
				requiresEpistemicCommitment(userContent) {
				if err := loop.commitPlan(
					ctx, generation, nil, true,
				); err != nil {
					return Response{}, err
				}
			}
			response.Content = generation.Content
			response.ContentStreamed = streamState.content
			response.ReasoningStreamed = streamState.reasoning
			if err := loop.saveCheckpoint(
				ctx, userContent, request.Messages, response, toolCalls,
				lastToolSignature, repeatedToolCalls, planFormed,
				revisionStep, revisionTargets, nil,
			); err != nil {
				return response, err
			}
			return response, nil
		}
		// Content and reasoning emitted alongside a valid structured tool call
		// are user-visible progress, not a discarded provider attempt. Keep
		// those deltas in the active turn. Repairs above still reset malformed
		// markup and empty finalization attempts before retrying them.
		if revisionStep {
			return Response{}, ErrConvergenceExceeded
		}
		if err := commitGenerationObserver(ctx); err != nil {
			return response, fmt.Errorf(
				"agent: commit streamed generation: %w", err,
			)
		}
		if !planFormed {
			if err := loop.commitPlan(ctx, generation, nil, true); err != nil {
				return Response{}, err
			}
			planFormed = true
		} else {
			if err := loop.commitPlan(
				ctx,
				generation,
				nil,
				strings.TrimSpace(generation.Content) != "",
			); err != nil {
				return Response{}, err
			}
		}
		request.Messages = append(request.Messages, protocol.Message{
			Role:      protocol.RoleAssistant,
			Content:   generation.Content,
			Reasoning: generation.Reasoning,
			ToolCalls: cloneToolCalls(generation.ToolCalls),
		})
		revisionRequired := false
		cassandraEdited := false
		calls := generation.ToolCalls
		var deferredCalls []protocol.NormalizedToolCall
		if activePolicy != nil && len(calls) > activePolicy.Parallelism {
			deferredCalls = append(
				[]protocol.NormalizedToolCall(nil),
				calls[activePolicy.Parallelism:]...,
			)
			calls = calls[:activePolicy.Parallelism]
		}
		for callIndex, call := range calls {
			toolCalls++
			if toolCalls > toolCallLimit {
				return Response{}, ErrToolCallLimit
			}
			if activePolicy != nil && isExplorationTool(call.Name) {
				explorationCalls++
				if explorationCalls > activePolicy.ExplorationBudget {
					result := json.RawMessage(`{"error":"exploration budget exhausted by enforced liveness decision policy"}`)
					response.ToolEvents = append(response.ToolEvents, ToolExecution{
						Call: cloneToolCall(call), Result: result,
						Error: "exploration budget exhausted",
					})
					request.Messages = append(request.Messages, protocol.Message{
						Role: protocol.RoleTool, Content: string(result),
						Name: call.Name, ToolCallID: call.ID,
					})
					continue
				}
			}

			// Expectation fields were validated for every call before any tool was
			// dispatched, preventing a repaired multi-call response from repeating
			// an effect that had already completed.
			expect := ""
			if loop.predictions != nil {
				expect = toolExpectations[callIndex]
				call.Arguments = append(json.RawMessage(nil), toolArguments[callIndex]...)
			}

			var currentSubgoalID string
			if loop.taskGraph != nil {
				if current := loop.taskGraph.Current(); current != nil {
					currentSubgoalID = current.ID
				}
			}
			if err := loop.enforceCircuit("tool", response); err != nil {
				return response, err
			}

			signature := canonicalToolSignature(call)
			if activePolicy != nil && signature == lastToolSignature &&
				repeatedToolCalls >= activePolicy.SameStrategyRetries+1 {
				response.Checkpoint = loop.makeCheckpoint(
					userContent, request.Messages, response, toolCalls-1,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				)
				return response, incomplete(
					"tool_loop", call.Name, nil, loop.now(),
					"change strategy under the enforced liveness decision policy",
					repeatedToolCalls,
				)
			}
			if signature == lastToolSignature {
				repeatedToolCalls++
			} else {
				lastToolSignature = signature
				repeatedToolCalls = 1
			}
			toolStarted := time.Now().UTC()
			pending := cloneToolCall(call)
			if err := loop.saveCheckpoint(
				ctx, userContent, request.Messages, response, toolCalls,
				lastToolSignature, repeatedToolCalls, planFormed,
				revisionStep, revisionTargets, &pending,
			); err != nil {
				return response, err
			}
			toolCtx, cancelTool := context.WithTimeout(ctx, loop.config.IdleTimeout)
			toolEventID := uuid.New()
			executionBinding := protocol.ToolExecutionBinding{
				ToolEventID: toolEventID,
				AgentID:     "ion",
			}
			if actorID, parseErr := uuid.Parse(
				strings.TrimSpace(loop.config.UserID),
			); parseErr == nil {
				executionBinding.ActorID = actorID
			}
			if sessionID, parseErr := uuid.Parse(
				strings.TrimSpace(loop.config.SessionID),
			); parseErr == nil {
				executionBinding.SessionID = &sessionID
				executionBinding.OutcomeID = &sessionID
			}
			toolCtx = protocol.WithToolExecutionBinding(
				toolCtx,
				executionBinding,
			)
			result, executeErr := loop.tools.Execute(toolCtx, call)
			toolContextErr := toolCtx.Err()
			cancelTool()
			if errors.Is(toolContextErr, context.DeadlineExceeded) && ctx.Err() == nil {
				if executeErr == nil {
					executeErr = toolContextErr
				}
				result = toolErrorResult(executeErr)
				execution := ToolExecution{
					Call: cloneToolCall(call), Result: append(json.RawMessage(nil), result...),
					Error: executeErr.Error(),
					Event: loop.createToolEvent(call, result, executeErr, toolEventID),
				}
				if loop.eventCommitter != nil && execution.Event != nil {
					execution.Event, err = loop.eventCommitter.CommitToolEvent(
						context.WithoutCancel(ctx), *execution.Event,
					)
					if err != nil {
						return Response{}, fmt.Errorf("agent: commit timed-out ToolEvent: %w", err)
					}
				}
				response.ToolEvents = append(response.ToolEvents, execution)
				response.Checkpoint = loop.makeCheckpoint(
					userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, &pending,
				)
				return response, incomplete(
					"tool",
					call.Name,
					result,
					toolStarted,
					"retry or respawn after tool idle timeout",
					toolCalls,
				)
			}
			if ctx.Err() != nil {
				if executeErr == nil {
					executeErr = ctx.Err()
				}
				cancelledResult := toolErrorResult(executeErr)
				cancelledEvent := loop.createToolEvent(
					call, cancelledResult, executeErr, toolEventID,
				)
				if loop.eventCommitter != nil && cancelledEvent != nil {
					if _, commitErr := loop.eventCommitter.CommitToolEvent(
						context.WithoutCancel(ctx), *cancelledEvent,
					); commitErr != nil {
						return Response{}, fmt.Errorf(
							"agent: commit interrupted ToolEvent: %w", commitErr,
						)
					}
				}
				return Response{}, ctx.Err()
			}

			event := ToolExecution{
				Call:   cloneToolCall(call),
				Result: append(json.RawMessage(nil), result...),
			}
			if executeErr != nil {
				event.Error = executeErr.Error()
				result = toolErrorResult(executeErr)
			}

			// Construct the event before comparison so its stable ID binds the
			// PredictionRecord; commit only after Match has been derived.
			toolEvent := loop.createToolEvent(
				call, result, executeErr, toolEventID,
			)
			if toolEvent != nil {
				toolEvent.SubgoalID = currentSubgoalID
			}
			var predictionRecord *protocol.PredictionRecord
			mismatch := false
			if loop.predictions != nil && toolEvent != nil {
				record, observedMismatch := loop.predictions.Observe(
					ctx, toolEvent.ID, call.Name, call.Arguments,
					expect, result, executeErr != nil,
				)
				mismatch = observedMismatch
				matched := !mismatch
				toolEvent.Expect = expect
				toolEvent.Match = &matched
				predictionRecord = &record
			}

			if loop.eventCommitter != nil && toolEvent != nil {
				toolEvent, err = loop.eventCommitter.CommitToolEvent(ctx, *toolEvent)
				if err != nil {
					return Response{}, fmt.Errorf("agent: commit ToolEvent: %w", err)
				}
			}
			event.Event = toolEvent

			if predictionRecord != nil && loop.predictionRecords != nil {
				if err := loop.predictionRecords.RecordPrediction(ctx, *predictionRecord); err != nil {
					return Response{}, fmt.Errorf("agent: commit PredictionRecord: %w", err)
				}
			}

			if mismatch && loop.cassandra != nil && !cassandraEdited &&
				strings.TrimSpace(generation.Content) != "" {
				assistantIndex := len(request.Messages) - 1
				if revised, changed := doubtRevision(generation.Content); changed {
					edit, editErr := loop.cassandra.Edit(
						"assistant:"+generation.ToolCalls[0].ID,
						generation.Content,
						revised,
						cassandra.TriggerPredictionMismatch,
						cassandra.SideDoubt,
						"tool result contradicted the stated prediction",
						"cassandra",
						false,
					)
					if editErr != nil {
						return Response{}, fmt.Errorf(
							"agent: Cassandra mismatch edit: %w", editErr,
						)
					}
					request.Messages[assistantIndex].Content = edit.ResultContent
					cassandraEdited = true
				}
			}

			// A mismatch is explicit new belief state with provenance.
			if mismatch && loop.premises != nil && toolEvent != nil {
				source := premise.SourceAssumption
				if loop.citations != nil && loop.events != nil {
					source = premise.SourceToolEvidence
				}
				item, addErr := loop.premises.Add(
					fmt.Sprintf("Prediction for %s was contradicted by its result", call.Name),
					source,
					toolCalls,
				)
				if addErr != nil {
					return Response{}, fmt.Errorf("agent: record mismatch premise: %w", addErr)
				}
				if currentSubgoalID != "" {
					if attachErr := loop.premises.Attach(
						item.ID, []string{currentSubgoalID},
					); attachErr != nil {
						return Response{}, fmt.Errorf(
							"agent: attach mismatch premise: %w", attachErr,
						)
					}
					if loop.taskGraph != nil {
						if _, graphErr := loop.taskGraph.AddPremise(
							currentSubgoalID, item.Statement,
						); graphErr != nil {
							return Response{}, fmt.Errorf(
								"agent: add mismatch premise to task graph: %w",
								graphErr,
							)
						}
					}
				}
				if toolEvent.MMRLeafHash != ([32]byte{}) &&
					loop.citations != nil && loop.events != nil {
					citation := protocol.Citation{
						ToolEventID:   toolEvent.ID,
						MMRLeafHash:   toolEvent.MMRLeafHash,
						MMRRootAtTime: toolEvent.MMRRootAtTime,
					}
					if citeErr := loop.CitePremise(
						ctx, item.ID, citation,
					); citeErr != nil {
						return Response{}, fmt.Errorf("agent: cite mismatch premise: %w", citeErr)
					}
				}
			}

			// Observe task graph evidence.
			if loop.taskGraph != nil && toolEvent != nil {
				evidenceInput := append([]byte(call.Name+"\x00"), result...)
				evidenceDigest := blake3.Sum256(evidenceInput)
				evidenceKey := fmt.Sprintf("%x", evidenceDigest[:])
				current := loop.taskGraph.Current()
				if current != nil {
					loop.taskGraph.ObserveAction(evidenceKey, current.ID)
					verified := executeErr == nil &&
						toolEvent.MMRLeafHash != ([32]byte{}) &&
						(toolEvent.Match == nil || *toolEvent.Match)
					if verified {
						if err := loop.taskGraph.CompleteSubgoal(current.ID); err != nil {
							return Response{}, fmt.Errorf(
								"agent: complete verified subgoal: %w", err,
							)
						}
					}
				}
			}

			// Observe self-model capability.
			if loop.selfModel != nil && toolEvent != nil {
				if executeErr == nil &&
					(toolEvent.Match == nil || *toolEvent.Match) {
					if err := loop.selfModel.AddCapability(*toolEvent); err != nil {
						return Response{}, fmt.Errorf(
							"agent: update self-model capability: %w", err,
						)
					}
				} else if executeErr != nil {
					loop.selfModel.RecordFailure(executeErr.Error(), call.Name)
				}
			}

			response.ToolEvents = append(response.ToolEvents, event)
			request.Messages = append(request.Messages, protocol.Message{
				Role:       protocol.RoleTool,
				Content:    string(result),
				Name:       call.Name,
				ToolCallID: call.ID,
			})
			if err := loop.saveCheckpoint(
				ctx, userContent, request.Messages, response, toolCalls,
				lastToolSignature, repeatedToolCalls, planFormed,
				revisionStep, revisionTargets, nil,
			); err != nil {
				return response, err
			}
			if repeatedToolCalls >= loop.config.RepeatedToolLimit {
				response.Checkpoint = loop.makeCheckpoint(
					userContent, request.Messages, response, toolCalls,
					lastToolSignature, repeatedToolCalls, planFormed,
					revisionStep, revisionTargets, nil,
				)
				return response, incomplete(
					"tool_loop",
					call.Name,
					result,
					toolStarted,
					"tools-stripped revision or respawn over durable state",
					repeatedToolCalls,
				)
			}
			predictionRevisionRequired := loop.predictions != nil &&
				loop.predictions.ShouldForceRevision()
			if predictionRevisionRequired {
				revisionRequired = true
				if currentSubgoalID != "" {
					revisionTargets = []string{currentSubgoalID}
				}
				for _, skipped := range calls[callIndex+1:] {
					request.Messages = append(
						request.Messages,
						protocol.Message{
							Role: protocol.RoleTool,
							Content: `{"error":"dispatch blocked: ` +
								`plan revision required"}`,
							Name:       skipped.Name,
							ToolCallID: skipped.ID,
						},
					)
				}
				break
			}
		}
		if !revisionRequired {
			for _, deferred := range deferredCalls {
				request.Messages = append(request.Messages, protocol.Message{
					Role:    protocol.RoleTool,
					Content: `{"error":"dispatch deferred: enforced liveness parallelism limit"}`,
					Name:    deferred.Name, ToolCallID: deferred.ID,
				})
			}
		}
		if !revisionRequired && loop.taskGraph != nil &&
			loop.taskGraph.ShouldForceRevision() {
			revisionRequired = true
			if current := loop.taskGraph.Current(); current != nil {
				revisionTargets = []string{current.ID}
			}
		}
		if revisionRequired {
			revisionStep = true
			if err := loop.saveCheckpoint(
				ctx, userContent, request.Messages, response, toolCalls,
				lastToolSignature, repeatedToolCalls, planFormed,
				revisionStep, revisionTargets, nil,
			); err != nil {
				return response, err
			}
		}
	}
}

const completionContinuationMarker = "Durable completion gate:"

func completionContinuationPrompt(decision CompletionDecision) string {
	reason := strings.TrimSpace(decision.Reason)
	next := strings.TrimSpace(decision.NextAction)
	return completionContinuationMarker + " the task is not complete from server-verified evidence. " +
		"Do not give a final answer yet. Continue with the next dependency-ready work item. " +
		"Current gap: " + reason + ". Next action: " + next
}

func isExplorationTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "web_search" || name == "web_fetch" ||
		strings.HasPrefix(name, "research_")
}

// requiresEpistemicCommitment keeps every turn on the same perception,
// activation, provider, tool-surface, safety, and liveness path. It suppresses
// only post-response task/premise mutation for a deliberately tiny set of
// unambiguous social acknowledgements. Any ambiguity takes the rigorous path.
func requiresEpistemicCommitment(content string) bool {
	normalized := strings.ToLower(strings.TrimSpace(content))
	normalized = strings.Trim(normalized, " \t\r\n.!?,;:")
	switch normalized {
	case "hi", "hello", "hey", "hello there", "hi there", "hey there",
		"good morning", "good afternoon", "good evening",
		"how are you", "how are you doing", "what's up", "whats up",
		"thanks", "thank you", "ok", "okay", "got it":
		return false
	default:
		return true
	}
}

func (loop *Loop) saveCheckpoint(
	ctx context.Context,
	userContent string,
	messages []protocol.Message,
	response Response,
	toolCalls int,
	lastToolSignature string,
	repeatedToolCalls int,
	planFormed bool,
	revisionStep bool,
	revisionTargets []string,
	pendingCall *protocol.NormalizedToolCall,
) error {
	checkpoint := loop.makeCheckpoint(
		userContent, messages, response, toolCalls, lastToolSignature,
		repeatedToolCalls, planFormed, revisionStep, revisionTargets, pendingCall,
	)
	response.Checkpoint = checkpoint
	if loop.checkpoints == nil {
		return nil
	}
	if err := loop.checkpoints.SaveCheckpoint(ctx, *checkpoint); err != nil {
		return fmt.Errorf("agent: persist turn checkpoint: %w", err)
	}
	return nil
}

func (loop *Loop) makeCheckpoint(
	userContent string,
	messages []protocol.Message,
	response Response,
	toolCalls int,
	lastToolSignature string,
	repeatedToolCalls int,
	planFormed bool,
	revisionStep bool,
	revisionTargets []string,
	pendingCall *protocol.NormalizedToolCall,
) *TurnCheckpoint {
	checkpoint := &TurnCheckpoint{
		Version: 1, UserContent: userContent,
		Messages:      cloneMessages(messages),
		ToolEvents:    cloneToolExecutions(response.ToolEvents),
		ProviderCalls: response.ProviderCalls, ToolCalls: toolCalls,
		Usage:             response.Usage,
		LastToolSignature: lastToolSignature,
		RepeatedToolCalls: repeatedToolCalls,
		PlanFormed:        planFormed, RevisionStep: revisionStep,
		RevisionTargets: append([]string(nil), revisionTargets...),
	}
	if pendingCall != nil {
		call := cloneToolCall(*pendingCall)
		checkpoint.PendingCall = &call
	}
	return checkpoint
}

func (checkpoint TurnCheckpoint) validate(userContent string) error {
	if checkpoint.Version != 1 ||
		strings.TrimSpace(checkpoint.UserContent) == "" ||
		checkpoint.UserContent != userContent ||
		len(checkpoint.Messages) == 0 ||
		checkpoint.ProviderCalls < 0 || checkpoint.ToolCalls < 0 ||
		checkpoint.Usage.PromptTokens < 0 ||
		checkpoint.Usage.CompletionTokens < 0 ||
		checkpoint.Usage.TotalTokens < 0 ||
		checkpoint.Usage.ReasoningTokens < 0 ||
		checkpoint.Usage.CachedTokens < 0 ||
		checkpoint.Usage.ModelCostMicrocents < 0 ||
		checkpoint.Usage.ProviderSpendMicrocents < 0 ||
		checkpoint.RepeatedToolCalls < 0 {
		return fmt.Errorf("agent: invalid durable turn checkpoint")
	}
	return nil
}

func mergeResponseTokenUsage(
	current *protocol.TokenUsage,
	update protocol.TokenUsage,
	firstProviderCall bool,
) {
	current.PromptTokens += update.PromptTokens
	current.CompletionTokens += update.CompletionTokens
	current.TotalTokens += update.TotalTokens
	current.ReasoningTokens += update.ReasoningTokens
	current.CachedTokens += update.CachedTokens
	current.ModelCostMicrocents += update.ModelCostMicrocents
	current.ProviderSpendMicrocents += update.ProviderSpendMicrocents
	if firstProviderCall {
		current.ModelCostKnown = update.ModelCostKnown
		current.ProviderSpendKnown = update.ProviderSpendKnown
		return
	}
	current.ModelCostKnown = current.ModelCostKnown && update.ModelCostKnown
	current.ProviderSpendKnown =
		current.ProviderSpendKnown && update.ProviderSpendKnown
}

func (loop *Loop) now() time.Time {
	if loop.clock != nil {
		return loop.clock.Now()
	}
	return time.Now().UTC()
}

func (loop *Loop) withEpistemicActivation(
	ctx context.Context,
	request protocol.GenerationRequest,
	userContent string,
	memoryContext string,
) (protocol.GenerationRequest, error) {
	active := request
	messages := make([]protocol.Message, 0, len(request.Messages)+5)
	if loop.config.SystemPrompt != "" {
		messages = append(messages, protocol.Message{
			Role: protocol.RoleSystem, Content: loop.config.SystemPrompt,
		})
	}
	if loop.contextComposer != nil {
		composeCtx, cancelCompose := context.WithTimeout(
			ctx, loop.config.ContextPrepareTimeout,
		)
		composed, err := loop.contextComposer.Compose(composeCtx, ContextSnapshot{
			UserID: loop.config.UserID, SessionID: loop.config.SessionID,
			UserContent: userContent, Memory: memoryContext,
			SelfModel: renderOptionalSelfModel(loop.selfModel),
			Premises:  renderOptionalPremises(loop.premises),
			TaskGraph: renderOptionalTaskGraph(loop.taskGraph),
		})
		composeContextErr := composeCtx.Err()
		cancelCompose()
		if errors.Is(composeContextErr, context.DeadlineExceeded) &&
			ctx.Err() == nil {
			return protocol.GenerationRequest{}, ErrContextPreparationTimeout
		}
		if err != nil {
			return protocol.GenerationRequest{}, fmt.Errorf(
				"agent: compose living context: %w", err,
			)
		}
		if strings.TrimSpace(composed) != "" {
			messages = append(messages, protocol.Message{
				Role: protocol.RoleSystem, Content: composed,
			})
		}
	} else {
		if memoryContext != "" {
			messages = append(messages, protocol.Message{
				Role: protocol.RoleSystem, Content: memoryContext,
			})
		}
		if loop.behavioral != nil {
			if guidance := loop.behavioral.DecisionInstructions(); guidance != "" {
				messages = append(messages, protocol.Message{
					Role: protocol.RoleSystem, Content: guidance,
				})
			}
		}
		for _, rendered := range []string{
			renderOptionalSelfModel(loop.selfModel),
			renderOptionalPremises(loop.premises),
			renderOptionalTaskGraph(loop.taskGraph),
		} {
			if rendered == "" {
				continue
			}
			messages = append(messages, protocol.Message{
				Role: protocol.RoleSystem, Content: rendered,
			})
		}
	}
	messages = append(messages, request.Messages...)
	active.Messages = messages
	return active, nil
}

func renderOptionalSelfModel(model SelfModelManager) string {
	if model == nil {
		return ""
	}
	return renderSelfModel(model.Snapshot())
}

func renderOptionalPremises(ledger PremiseLedger) string {
	if ledger == nil {
		return ""
	}
	return ledger.Render()
}

func renderOptionalTaskGraph(graph TaskDAG) string {
	if graph == nil {
		return ""
	}
	return graph.Render()
}

func renderSelfModel(model selfmodel.SelfModel) string {
	var builder strings.Builder
	builder.WriteString("## Observed self-model\n")
	if model.Structure != nil && model.Structure.Digest != "" {
		builder.WriteString("- codegraph: ")
		builder.WriteString(model.Structure.Digest)
		builder.WriteByte('\n')
	}
	for _, capability := range model.Capabilities {
		builder.WriteString(fmt.Sprintf(
			"- capability %s: reliability %.2f from %d committed ToolEvents\n",
			capability.Name,
			capability.Reliability,
			capability.SampleSize,
		))
	}
	for _, failure := range model.FailurePatterns {
		builder.WriteString(fmt.Sprintf(
			"- failure pattern %s: observed %d times\n",
			failure.Pattern,
			failure.Frequency,
		))
	}
	if builder.Len() == len("## Observed self-model\n") {
		return ""
	}
	return builder.String()
}

func (loop *Loop) commitPlan(
	ctx context.Context,
	generation protocol.NormalizedGeneration,
	affected []string,
	replacePlan bool,
) error {
	if loop.premises == nil || loop.premiseExtractor == nil {
		return nil
	}
	extracted, err := loop.premiseExtractor.Extract(ctx, premise.Plan{
		Text:      generation.Content,
		ToolCalls: cloneToolCalls(generation.ToolCalls),
	})
	if err != nil {
		return fmt.Errorf("agent: extract plan premises: %w", err)
	}
	if loop.taskGraph == nil {
		if replacePlan {
			loop.premises.PlanChanged()
		}
		for index, candidate := range extracted {
			source := candidate.Source
			if source != premise.SourceAssumption && candidate.Citation == nil {
				source = premise.SourceAssumption
			}
			if _, err := loop.premises.Add(
				candidate.Statement, source, index,
			); err != nil {
				return fmt.Errorf("agent: add plan premise: %w", err)
			}
		}
		return nil
	}

	targets := uniqueStrings(affected)
	if len(targets) == 0 {
		if current := loop.taskGraph.Current(); current != nil {
			targets = []string{current.ID}
		}
	}
	if len(targets) == 0 {
		text := strings.TrimSpace(generation.Content)
		if text == "" && len(generation.ToolCalls) > 0 {
			text = "Execute " + generation.ToolCalls[0].Name
		}
		if text == "" {
			text = "Revise current plan"
		}
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[:newline]
		}
		if len(text) > 160 {
			text = text[:160]
		}
		targets = []string{loop.taskGraph.AddSubgoal(text).ID}
	}

	if replacePlan {
		if err := loop.taskGraph.ReplanSubtrees(targets); err != nil {
			return fmt.Errorf("agent: replan task subtree: %w", err)
		}
		loop.premises.ReviseAffected(targets)
	}

	existing := make(map[string]struct{})
	for _, item := range loop.premises.Active() {
		existing[strings.ToLower(strings.TrimSpace(item.Statement))] = struct{}{}
	}
	for index, candidate := range extracted {
		key := strings.ToLower(strings.TrimSpace(candidate.Statement))
		if _, duplicate := existing[key]; duplicate {
			continue
		}
		source := candidate.Source
		if source != premise.SourceAssumption && candidate.Citation == nil {
			source = premise.SourceAssumption
		}
		item, err := loop.premises.Add(candidate.Statement, source, index)
		if err != nil {
			return fmt.Errorf("agent: add plan premise: %w", err)
		}
		if err := loop.premises.Attach(item.ID, targets); err != nil {
			return fmt.Errorf("agent: attach plan premise: %w", err)
		}
		for _, target := range targets {
			if _, err := loop.taskGraph.AddPremise(
				target, candidate.Statement,
			); err != nil {
				return fmt.Errorf("agent: add task-DAG premise: %w", err)
			}
		}
		existing[key] = struct{}{}
	}
	return nil
}

func (loop *Loop) refutedSubgoals() []string {
	if loop.premises == nil {
		return nil
	}
	var targets []string
	for _, item := range loop.premises.UnrevisedRefuted() {
		targets = append(targets, item.Affected...)
	}
	if len(targets) == 0 && loop.premises.HasRefuted() &&
		loop.taskGraph != nil {
		if current := loop.taskGraph.Current(); current != nil {
			targets = append(targets, current.ID)
		}
	}
	return uniqueStrings(targets)
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (loop *Loop) enforceCircuit(phase string, response Response) error {
	if loop.circuitBreaker == nil {
		return nil
	}
	context := circuit.IncompleteContext{
		Phase:      phase,
		StuckSince: time.Now().UTC(),
		Recovery:   "human clearance or respawn over durable state",
		Attempt:    response.ProviderCalls,
	}
	if len(response.ToolEvents) > 0 {
		last := response.ToolEvents[len(response.ToolEvents)-1]
		context.LastTool = last.Call.Name
		context.LastResult = string(last.Result)
	}
	return loop.circuitBreaker.Enforce(context)
}

func canonicalToolSignature(call protocol.NormalizedToolCall) string {
	var value any
	arguments := call.Arguments
	if json.Unmarshal(call.Arguments, &value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			arguments = encoded
		}
	}
	digest := blake3.New(32, nil)
	_, _ = digest.Write([]byte(call.Name))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(arguments)
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func incomplete(
	phase string,
	lastTool string,
	lastResult json.RawMessage,
	stuckSince time.Time,
	recovery string,
	attempt int,
) *action.ErrIncomplete {
	return &action.ErrIncomplete{
		Phase:      phase,
		LastTool:   lastTool,
		LastResult: append(json.RawMessage(nil), lastResult...),
		StuckSince: stuckSince,
		Recovery:   recovery,
		Attempt:    attempt,
	}
}

func countTextualToolRepairs(messages []protocol.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role != protocol.RoleSystem {
			continue
		}
		if message.Content == textualToolRepairPrompt ||
			message.Content == textualToolFreeRepairPrompt {
			count++
		}
	}
	return count
}

func countExpectationRepairs(messages []protocol.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == protocol.RoleSystem &&
			message.Content == expectationRepairPrompt {
			count++
		}
	}
	return count
}

func hasSystemPrompt(messages []protocol.Message, prompt string) bool {
	for _, message := range messages {
		if message.Role == protocol.RoleSystem && message.Content == prompt {
			return true
		}
	}
	return false
}

func lastToolEvidence(events []ToolExecution) (string, json.RawMessage) {
	if len(events) == 0 {
		return "", nil
	}
	last := events[len(events)-1]
	return last.Call.Name, append(json.RawMessage(nil), last.Result...)
}

func doubtRevision(content string) (string, bool) {
	// Prediction mismatch should revise the claim's epistemic force, not merely
	// its punctuation. Keep replacements small enough for Cassandra's bounded
	// edit contract while changing what the sentence asserts.
	replacements := []struct {
		from string
		to   string
	}{
		{" confirmed ", " uncertain "},
		{" verified ", " disputed "},
		{" certainly ", " possibly "},
		{" definitely ", " possibly "},
		{" will ", " may "},
	}
	lower := strings.ToLower(content)
	for _, replacement := range replacements {
		index := strings.Index(lower, replacement.from)
		if index < 0 {
			continue
		}
		return content[:index] + replacement.to +
			content[index+len(replacement.from):], true
	}
	return content, false
}

// createToolEvent constructs a ToolEvent from an execution result.
func (loop *Loop) createToolEvent(
	call protocol.NormalizedToolCall,
	result json.RawMessage,
	execErr error,
	eventID uuid.UUID,
) *protocol.ToolEvent {
	if loop.clock == nil {
		return nil
	}
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}
	event := &protocol.ToolEvent{
		ID:        eventID,
		CallID:    call.ID,
		Name:      call.Name,
		Args:      append(json.RawMessage(nil), call.Arguments...),
		Result:    append(json.RawMessage(nil), result...),
		Timestamp: loop.clock.Now(),
	}
	if execErr != nil {
		switch tools.TerminalPhase(context.Background(), execErr) {
		case tools.LifecycleDenied:
			event.FailureClass = protocol.FailureDenied
		case tools.LifecycleInterrupted:
			event.FailureClass = protocol.FailureInterrupted
		case tools.LifecycleOutcomeUnknown:
			event.FailureClass = protocol.FailureOutcomeUnknown
		default:
			if errors.Is(execErr, tools.ErrTimeout) ||
				errors.Is(execErr, context.DeadlineExceeded) {
				event.FailureClass = protocol.FailureTimeout
			} else {
				event.FailureClass = protocol.FailureExecution
			}
		}
	}
	return event
}

// VerifyCitation verifies a citation against the MMR and committed ToolEvent.
func (loop *Loop) VerifyCitation(ctx context.Context, citation protocol.Citation) (bool, error) {
	if loop.citations == nil {
		return false, fmt.Errorf("agent: citation verifier not configured")
	}
	if loop.events == nil {
		return false, fmt.Errorf("agent: event store not configured")
	}

	// Retrieve the committed ToolEvent.
	event, ok := loop.events.GetToolEvent(citation.ToolEventID)
	if !ok {
		return false, fmt.Errorf("agent: tool event not found: %s", citation.ToolEventID)
	}

	// Verify against the MMR.
	return loop.citations.VerifyCitation(ctx, citation, *event)
}

// CitePremise verifies caller-supplied provenance before the ledger can render
// it as cited. The Verified bit is derived here and is never trusted on input.
func (loop *Loop) CitePremise(
	ctx context.Context,
	premiseID uuid.UUID,
	citation protocol.Citation,
) error {
	if loop.premises == nil {
		return fmt.Errorf("agent: premise ledger not configured")
	}
	citation.Verified = false
	verified, err := loop.VerifyCitation(ctx, citation)
	if err != nil {
		return err
	}
	if !verified {
		return fmt.Errorf("agent: citation verification failed")
	}
	citation.Verified = true
	return loop.premises.Cite(premiseID, citation)
}

// RefutePremise verifies evidence before marking a premise refuted. The next
// turn will tools-strip and revise only its attached task-DAG subtrees.
func (loop *Loop) RefutePremise(
	ctx context.Context,
	premiseID uuid.UUID,
	citation protocol.Citation,
) error {
	if loop.premises == nil {
		return fmt.Errorf("agent: premise ledger not configured")
	}
	citation.Verified = false
	verified, err := loop.VerifyCitation(ctx, citation)
	if err != nil {
		return err
	}
	if !verified {
		return fmt.Errorf("agent: citation verification failed")
	}
	citation.Verified = true
	return loop.premises.Refute(premiseID, &citation)
}

// ApplyUserCorrection edits a prior assistant message in place and records the
// dual record through Cassandra's configured auditor.
func (loop *Loop) ApplyUserCorrection(
	messageID string,
	message *protocol.Message,
	correctedContent string,
	reason string,
	approved bool,
) (*cassandra.Edit, error) {
	if loop.cassandra == nil {
		return nil, fmt.Errorf("agent: Cassandra controller not configured")
	}
	if message == nil || message.Role != protocol.RoleAssistant {
		return nil, fmt.Errorf("agent: Cassandra can edit only assistant messages")
	}
	edit, err := loop.cassandra.Edit(
		messageID,
		message.Content,
		correctedContent,
		cassandra.TriggerUserCorrection,
		cassandra.SideDoubt,
		reason,
		"cassandra",
		approved,
	)
	if err != nil {
		return nil, err
	}
	message.Content = edit.ResultContent
	return edit, nil
}

// UndoCassandra restores the original assistant content for an active edit.
func (loop *Loop) UndoCassandra(
	editID *uuid.UUID,
	message *protocol.Message,
) (*cassandra.Edit, error) {
	if loop.cassandra == nil {
		return nil, fmt.Errorf("agent: Cassandra controller not configured")
	}
	if message == nil || message.Role != protocol.RoleAssistant {
		return nil, fmt.Errorf("agent: Cassandra can undo only assistant messages")
	}
	edit, err := loop.cassandra.Undo(editID)
	if err != nil {
		return nil, err
	}
	message.Content = edit.OriginalContent
	return edit, nil
}

func toolErrorResult(err error) json.RawMessage {
	var structured interface{ StructuredToolError() any }
	if errors.As(err, &structured) {
		encoded, marshalErr := json.Marshal(struct {
			Error   string `json:"error"`
			Details any    `json:"details"`
		}{Error: err.Error(), Details: structured.StructuredToolError()})
		if marshalErr == nil {
			return encoded
		}
	}
	encoded, marshalErr := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
	if marshalErr != nil {
		return json.RawMessage(`{"error":"tool execution failed"}`)
	}
	return encoded
}

func cloneToolCalls(source []protocol.NormalizedToolCall) []protocol.NormalizedToolCall {
	cloned := make([]protocol.NormalizedToolCall, 0, len(source))
	for _, call := range source {
		cloned = append(cloned, cloneToolCall(call))
	}
	return cloned
}

func cloneMessages(source []protocol.Message) []protocol.Message {
	cloned := make([]protocol.Message, 0, len(source))
	for _, message := range source {
		message.ToolCalls = cloneToolCalls(message.ToolCalls)
		cloned = append(cloned, message)
	}
	return cloned
}

func cloneToolExecutions(source []ToolExecution) []ToolExecution {
	cloned := make([]ToolExecution, 0, len(source))
	for _, execution := range source {
		execution.Call = cloneToolCall(execution.Call)
		execution.Result = append(json.RawMessage(nil), execution.Result...)
		if execution.Event != nil {
			event := *execution.Event
			event.Args = append(json.RawMessage(nil), execution.Event.Args...)
			event.Result = append(json.RawMessage(nil), execution.Event.Result...)
			execution.Event = &event
		}
		cloned = append(cloned, execution)
	}
	return cloned
}

func cloneToolCall(call protocol.NormalizedToolCall) protocol.NormalizedToolCall {
	call.Arguments = append(json.RawMessage(nil), call.Arguments...)
	return call
}

// injectExpectParam adds the expect parameter to tool schemas.
func injectExpectParam(schemas []protocol.ToolDefinition) []protocol.ToolDefinition {
	result := make([]protocol.ToolDefinition, len(schemas))
	copy(result, schemas)
	for i := range result {
		if err := injectOneExpectParam(&result[i]); err != nil {
			continue
		}
	}
	return result
}

func injectOneExpectParam(def *protocol.ToolDefinition) error {
	var m map[string]json.RawMessage
	p := &m
	if err := json.Unmarshal(def.Parameters, p); err != nil {
		return err
	}
	if m == nil {
		m = make(map[string]json.RawMessage)
	}
	var properties map[string]json.RawMessage
	if raw, ok := m["properties"]; ok {
		if err := json.Unmarshal(raw, &properties); err != nil {
			return fmt.Errorf("agent: tool properties must be an object: %w", err)
		}
	}
	if properties == nil {
		properties = make(map[string]json.RawMessage)
	}
	properties["expect"] = json.RawMessage(`{"type":"string","minLength":1,"description":"One-line prediction of what this tool will return"}`)
	encodedProperties, err := json.Marshal(properties)
	if err != nil {
		return err
	}
	m["properties"] = encodedProperties
	if required, ok := m["required"]; ok {
		var list []string
		if err := json.Unmarshal(required, &list); err == nil {
			has := false
			for _, r := range list {
				if r == "expect" {
					has = true
					break
				}
			}
			if !has {
				list = append(list, "expect")
				m["required"], _ = json.Marshal(list)
			}
		}
	} else {
		list := []string{"expect"}
		m["required"], _ = json.Marshal(list)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	def.Parameters = out
	return nil
}

// popExpect extracts the expect parameter from tool call arguments.
func popExpect(args json.RawMessage) (string, json.RawMessage, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(args, &parsed); err != nil {
		return "", args, fmt.Errorf("%w: invalid arguments: %v", ErrMissingExpectation, err)
	}
	expectRaw, hasExpect := parsed["expect"]
	delete(parsed, "expect")
	stripped, err := json.Marshal(parsed)
	if err != nil {
		return "", args, fmt.Errorf("%w: %v", ErrMissingExpectation, err)
	}
	if !hasExpect {
		return "", stripped, ErrMissingExpectation
	}
	var expect string
	if err := json.Unmarshal(expectRaw, &expect); err != nil {
		return "", stripped, ErrMissingExpectation
	}
	expect = strings.TrimSpace(expect)
	if expect == "" {
		return "", stripped, ErrMissingExpectation
	}
	return expect, stripped, nil
}
