package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/action"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/belief/selfmodel"
	"github.com/paxlabs-inc/ion-agent/internal/intent/prediction"
	"github.com/paxlabs-inc/ion-agent/internal/intent/taskgraph"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/decision"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/relationship"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/provider"
	"github.com/paxlabs-inc/ion-agent/internal/reflection/cassandra"
	"github.com/paxlabs-inc/ion-agent/internal/security/circuit"
	"github.com/paxlabs-inc/ion-agent/internal/security/dashboard"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	"lukechampine.com/blake3"
)

type loopTestCipher struct{}

func (loopTestCipher) Encrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}
func (loopTestCipher) Decrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

type loopTestClock struct{ now time.Time }

type streamTestGenerator struct{}

func (streamTestGenerator) Generate(
	context.Context,
	protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	return protocol.NormalizedGeneration{}, errors.New("blocking path used")
}

type completionGateGenerator struct {
	calls    int
	requests []protocol.GenerationRequest
}

func (generator *completionGateGenerator) Generate(
	_ context.Context, request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	generator.calls++
	generator.requests = append(generator.requests, request)
	content := "premature final"
	if generator.calls > 1 {
		content = "evidence-backed final"
	}
	return protocol.NormalizedGeneration{Content: content, FinishReason: protocol.FinishStop}, nil
}

type sequenceCompletionGate struct{ calls int }

func (gate *sequenceCompletionGate) CheckCompletion(context.Context) (CompletionDecision, error) {
	gate.calls++
	if gate.calls == 1 {
		return CompletionDecision{Reason: "one criterion is unverified", NextAction: "verify it"}, nil
	}
	return CompletionDecision{Ready: true}, nil
}

func TestCompletionGateContinuesPastPrematureModelFinal(t *testing.T) {
	t.Parallel()
	generator := &completionGateGenerator{}
	gate := &sequenceCompletionGate{}
	loop, err := NewLoop(generator, newPolicyManager(t), LoopConfig{Model: "model"}, &LoopDeps{
		CompletionGate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "finish the accepted outcome")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "evidence-backed final" || generator.calls != 2 || gate.calls != 2 {
		t.Fatalf("completion convergence response=%+v provider=%d gate=%d", response, generator.calls, gate.calls)
	}
	if requestContains(generator.requests[1], "premature final") ||
		!requestContains(generator.requests[1], completionContinuationMarker) {
		t.Fatalf("completion continuation polluted transcript history: %+v", generator.requests[1])
	}
}

func (streamTestGenerator) GenerateStream(
	_ context.Context,
	request protocol.GenerationRequest,
	deliver func(protocol.StreamChunk) error,
) (protocol.NormalizedGeneration, error) {
	if !request.Stream {
		return protocol.NormalizedGeneration{}, errors.New("stream flag missing")
	}
	for _, chunk := range []protocol.StreamChunk{
		{ReasoningDelta: "Checked evidence. ", ContentDelta: "Hel"},
		{ReasoningDelta: "Answer ready.", ContentDelta: "lo"},
	} {
		if err := deliver(chunk); err != nil {
			return protocol.NormalizedGeneration{}, err
		}
	}
	return protocol.NormalizedGeneration{
		Content: "Hello", Reasoning: "Checked evidence. Answer ready.",
		FinishReason: protocol.FinishStop,
	}, nil
}

type streamTestObserver struct {
	content   strings.Builder
	reasoning strings.Builder
	resets    int
}

func (observer *streamTestObserver) ContentDelta(
	_ context.Context,
	value string,
) error {
	observer.content.WriteString(value)
	return nil
}

func (observer *streamTestObserver) ReasoningDelta(
	_ context.Context,
	value string,
) error {
	observer.reasoning.WriteString(value)
	return nil
}

func (observer *streamTestObserver) Reset(context.Context) error {
	observer.resets++
	return nil
}

func TestAgentLoopStreamsFinalContentWithoutRawProviderReasoning(t *testing.T) {
	t.Parallel()
	loop, err := NewLoop(
		streamTestGenerator{}, newPolicyManager(t),
		LoopConfig{Model: "stream-model"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	observer := &streamTestObserver{}
	response, err := loop.Turn(
		WithGenerationObserver(context.Background(), observer),
		"say hello",
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Hello" || !response.ContentStreamed ||
		response.ReasoningStreamed || observer.content.String() != "Hello" ||
		observer.reasoning.String() != "" ||
		observer.resets != 0 {
		t.Fatalf("response=%+v observer=%+v", response, observer)
	}
}

type toolStepStreamGenerator struct {
	calls int
}

func (generator *toolStepStreamGenerator) Generate(
	context.Context,
	protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	return protocol.NormalizedGeneration{}, errors.New("blocking path used")
}

func (generator *toolStepStreamGenerator) GenerateStream(
	_ context.Context,
	_ protocol.GenerationRequest,
	deliver func(protocol.StreamChunk) error,
) (protocol.NormalizedGeneration, error) {
	generator.calls++
	if generator.calls == 1 {
		if err := deliver(protocol.StreamChunk{
			ContentDelta:   "Creating the project files now. ",
			ReasoningDelta: "The active workspace is ready. ",
		}); err != nil {
			return protocol.NormalizedGeneration{}, err
		}
		return protocol.NormalizedGeneration{
			Content:   "Creating the project files now. ",
			Reasoning: "The active workspace is ready. ",
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "write-one", Name: "write", Arguments: json.RawMessage(`{"path":"one"}`),
			}},
			FinishReason: protocol.FinishToolCalls,
		}, nil
	}
	if err := deliver(protocol.StreamChunk{
		ContentDelta:   "The first file is complete.",
		ReasoningDelta: "The write result was verified.",
	}); err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	return protocol.NormalizedGeneration{
		Content: "The first file is complete.", Reasoning: "The write result was verified.",
		FinishReason: protocol.FinishStop,
	}, nil
}

func TestAgentLoopKeepsStreamedCommentaryAcrossValidToolSteps(t *testing.T) {
	t.Parallel()
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "write", Description: "Write one project file.",
		Parameters:     json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"written":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	observer := &streamTestObserver{}
	loop, err := NewLoop(
		&toolStepStreamGenerator{}, manager, LoopConfig{Model: "stream-model"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(
		WithGenerationObserver(context.Background(), observer), "build the project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if observer.resets != 0 ||
		observer.content.String() != "Creating the project files now. The first file is complete." ||
		observer.reasoning.String() != "" ||
		response.Content != "The first file is complete." {
		t.Fatalf("response=%+v observer=%+v", response, observer)
	}
}

func (clock *loopTestClock) Now() time.Time {
	now := clock.now
	clock.now = clock.now.Add(time.Nanosecond)
	return now
}

type receiptCommitter struct{}

func (receiptCommitter) CommitToolEvent(
	_ context.Context,
	event protocol.ToolEvent,
) (*protocol.ToolEvent, error) {
	event.MMRLeafHash = [32]byte{1}
	event.MMRRootAtTime = [32]byte{2}
	return &event, nil
}

type recordingSelfModel struct {
	capabilityEvents []protocol.ToolEvent
	failures         []string
	snapshot         selfmodel.SelfModel
}

func (model *recordingSelfModel) AddCapability(event protocol.ToolEvent) error {
	model.capabilityEvents = append(model.capabilityEvents, event)
	return nil
}

func (model *recordingSelfModel) RecordFailure(failureMode, toolName string) {
	model.failures = append(model.failures, toolName+": "+failureMode)
}

func (model *recordingSelfModel) Snapshot() selfmodel.SelfModel {
	return model.snapshot
}

type countingPremiseExtractor struct {
	calls int
}

func (extractor *countingPremiseExtractor) Extract(
	_ context.Context,
	_ premise.Plan,
) ([]premise.Premise, error) {
	extractor.calls++
	return []premise.Premise{{
		Statement: "the requested analysis has load-bearing assumptions",
		Source:    premise.SourceAssumption,
	}}, nil
}

func TestRealAgentLoopCallsToolAndReturnsResponse(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role             protocol.MessageRole `json:"role"`
				Content          string               `json:"content"`
				ReasoningContent string               `json:"reasoning_content"`
				ToolCallID       string               `json:"tool_call_id"`
			} `json:"messages"`
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("Decode(request) error = %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests++
		current := requests
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if current == 1 {
			if len(body.Tools) != 1 {
				t.Errorf("first request tools = %d, want 1", len(body.Tools))
			}
			_, _ = io.WriteString(writer, `{
				"model":"loop-model",
				"choices":[{
					"message":{"content":"","reasoning_content":"Need to calculate.","tool_calls":[{
						"id":"call-add",
						"type":"function",
						"function":{"name":"add","arguments":"{\"a\":2,\"b\":3}"}
					}]},
					"finish_reason":"tool_calls"
				}],
				"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
			}`)
			return
		}
		last := body.Messages[len(body.Messages)-1]
		if last.Role != protocol.RoleTool || last.ToolCallID != "call-add" ||
			last.Content != `{"sum":5}` {
			t.Errorf("tool result message = %+v", last)
		}
		assistant := body.Messages[len(body.Messages)-2]
		if assistant.Role != protocol.RoleAssistant ||
			assistant.ReasoningContent != "Need to calculate." {
			t.Errorf("assistant tool-call message = %+v", assistant)
		}
		_, _ = io.WriteString(writer, `{
			"model":"loop-model",
			"choices":[{"message":{"content":"The sum is 5."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}
		}`)
	}))
	defer server.Close()

	pool, err := provider.NewPool([]provider.Endpoint{{
		Name:           "local-openai",
		URL:            server.URL,
		Model:          "loop-model",
		Adapter:        provider.OpenAIAdapter{},
		Authentication: provider.BearerAuthentication(),
		Credentials:    []provider.Credential{{ID: "test", Secret: "secret"}},
		Client:         server.Client(),
	}})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "add",
		Description:    "Add two integers.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			var input struct {
				A int `json:"a"`
				B int `json:"b"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]int{"sum": input.A + input.B})
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	loop, err := NewLoop(pool, manager, LoopConfig{
		Model:        "loop-model",
		SystemPrompt: "Use tools when helpful.",
	}, nil)
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}
	response, err := loop.Turn(context.Background(), "Add 2 and 3.")
	if err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	if response.Content != "The sum is 5." || response.ProviderCalls != 2 ||
		len(response.ToolEvents) != 1 || response.ToolEvents[0].Error != "" ||
		string(response.ToolEvents[0].Result) != `{"sum":5}` {
		t.Fatalf("response = %+v", response)
	}
}

func TestAgentLoopReturnsToolErrorsToProvider(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call", Name: "fails", Arguments: json.RawMessage(`{}`),
			}},
		},
		{Content: "I could not run it.", FinishReason: protocol.FinishStop},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "fails", Description: "Fails.", Parameters: json.RawMessage(`{}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("expected failure")
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, nil)
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}
	response, err := loop.Turn(context.Background(), "Run it.")
	if err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	if response.Content != "I could not run it." || len(response.ToolEvents) != 1 ||
		response.ToolEvents[0].Error == "" {
		t.Fatalf("response = %+v", response)
	}
	lastRequest := generator.requests[1]
	lastMessage := lastRequest.Messages[len(lastRequest.Messages)-1]
	if lastMessage.Role != protocol.RoleTool ||
		!json.Valid([]byte(lastMessage.Content)) {
		t.Fatalf("tool error message = %+v", lastMessage)
	}
}

func TestAgentLoopNormalizesTextualToolCallWithoutLeakingMarkup(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			Content: "I’ll inspect it now.\n" +
				"<tool_call> <function=filesystem_list> " +
				"<parameter=expect>Files in the workspace " +
				"<parameter=path>research/matrix-core </tool_call>",
			FinishReason: protocol.FinishStop,
		},
		{Content: "The directory contains Cortex.", FinishReason: protocol.FinishStop},
	}}
	manager := newPolicyManager(t)
	var received struct {
		Expect string `json:"expect"`
		Path   string `json:"path"`
	}
	if err := manager.Register(context.Background(), tools.Registration{
		Name:        "filesystem_list",
		Description: "List a workspace directory.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"expect":{"type":"string"},
				"path":{"type":"string"}
			},
			"additionalProperties":false
		}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			if err := json.Unmarshal(arguments, &received); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"entries":["cortex"]}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Inspect Matrix Core.")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "The directory contains Cortex." ||
		len(response.ToolEvents) != 1 ||
		received.Path != "research/matrix-core" ||
		received.Expect != "Files in the workspace" {
		t.Fatalf("response=%+v parameters=%+v", response, received)
	}
	if len(generator.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(generator.requests))
	}
	second := generator.requests[1].Messages
	assistant := second[len(second)-2]
	if len(assistant.ToolCalls) != 1 ||
		strings.Contains(assistant.Content, "<tool_call>") {
		t.Fatalf("normalized assistant message = %+v", assistant)
	}
}

func TestToolErrorResultPreservesStructuredValidationPaths(t *testing.T) {
	result := toolErrorResult(fmt.Errorf("wrapped: %w", &tools.ArgumentValidationError{
		Issues: []tools.ArgumentValidationIssue{{Path: "spec_delta.tasks", Message: "is required"}},
	}))
	var decoded struct {
		Details struct {
			Code   string                          `json:"code"`
			Issues []tools.ArgumentValidationIssue `json:"issues"`
		} `json:"details"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Details.Code != "argument_validation_failed" || len(decoded.Details.Issues) != 1 ||
		decoded.Details.Issues[0].Path != "spec_delta.tasks" {
		t.Fatalf("structured tool error = %s", result)
	}
}

func TestAgentLoopRejectsMalformedTextualToolCall(t *testing.T) {
	t.Parallel()
	malformed := protocol.NormalizedGeneration{
		Content:      "<tool_call> <function=filesystem_list> <parameter=path>.",
		FinishReason: protocol.FinishStop,
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		malformed, malformed, malformed, malformed,
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "filesystem_list", Description: "List a workspace directory.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{"path":{"type":"string"}},
			"additionalProperties":false
		}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("malformed textual call reached the tool manager")
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Inspect.")
	if !errors.Is(err, ErrTextualToolMarkup) {
		t.Fatalf("Turn() error = %v, want ErrTextualToolMarkup", err)
	}
	if response.Content != "" || strings.Contains(response.Content, "<tool_call>") {
		t.Fatalf("unsafe response escaped: %+v", response)
	}
	if len(generator.requests) != textualToolRepairLimit+2 ||
		!requestContains(generator.requests[1], textualToolRepairPrompt) {
		t.Fatalf("bounded repair requests = %+v", generator.requests)
	}
}

func TestAgentLoopFallsBackToOrdinaryAnswerAfterTextualRepairExhaustion(t *testing.T) {
	t.Parallel()
	malformed := protocol.NormalizedGeneration{
		Content: "<tool_call><function=read>", FinishReason: protocol.FinishStop,
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		malformed, malformed, malformed,
		{Content: "I could not safely complete the action.", FinishReason: protocol.FinishStop},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "read", Description: "Read data.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("unsafe textual call reached the tool manager")
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Inspect it.")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "I could not safely complete the action." ||
		len(generator.requests) != 4 ||
		!requestContains(generator.requests[3], textualToolFinalPrompt) ||
		len(generator.requests[3].Tools) != 0 {
		t.Fatalf("response=%+v requests=%+v", response, generator.requests)
	}
}

func TestAgentLoopRepairsMalformedTextualToolCallOnFirstAttempt(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			Content:      "<tool_call><function=read>",
			FinishReason: protocol.FinishStop,
		},
		{Content: "Recovered without unsafe markup.", FinishReason: protocol.FinishStop},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "read", Description: "Read data.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("malformed first attempt reached the tool manager")
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Inspect it.")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Recovered without unsafe markup." ||
		len(generator.requests) != 2 ||
		!requestContains(generator.requests[1], textualToolRepairPrompt) {
		t.Fatalf("response=%+v requests=%+v", response, generator.requests)
	}
}

func TestAgentLoopRepairsMalformedTextualToolCallAfterToolWork(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-read", Name: "read", Arguments: json.RawMessage(`{}`),
			}},
		},
		{
			Content:      "<tool_call><function=read>",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "The predecessor contains an agent package.",
			FinishReason: protocol.FinishStop,
		},
	}}
	manager := newPolicyManager(t)
	executions := 0
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "read", Description: "Read predecessor code.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"package":"agent"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Explore your predecessor.")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "The predecessor contains an agent package." ||
		response.ProviderCalls != 3 || len(response.ToolEvents) != 1 ||
		executions != 1 {
		t.Fatalf("response=%+v executions=%d", response, executions)
	}
	if len(generator.requests) != 3 ||
		!requestContains(generator.requests[2], textualToolRepairPrompt) {
		t.Fatalf("repair request = %+v", generator.requests)
	}
}

func TestAgentLoopReturnsResumableIncompleteAfterBoundedTextualRepairs(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-read", Name: "read", Arguments: json.RawMessage(`{}`),
			}},
		},
		{
			Content:      "<tool_call><function=read>",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "<tool_call><function=read>",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "<tool_call><function=read>",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "The predecessor contains an agent package.",
			FinishReason: protocol.FinishStop,
		},
	}}
	manager := newPolicyManager(t)
	executions := 0
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "read", Description: "Read predecessor code.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"package":"agent"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Explore your predecessor.")
	var incompleteErr *action.ErrIncomplete
	if !errors.As(err, &incompleteErr) ||
		incompleteErr.Phase != "provider_tool_markup" {
		t.Fatalf("Turn() error = %v, want provider_tool_markup incomplete", err)
	}
	if len(generator.requests) != 4 || executions != 1 ||
		len(response.ToolEvents) != 1 || response.Checkpoint == nil {
		t.Fatalf(
			"provider requests=%d executions=%d response=%+v",
			len(generator.requests), executions, response,
		)
	}
	if countTextualToolRepairs(response.Checkpoint.Messages) !=
		textualToolRepairLimit {
		t.Fatalf("checkpoint lost bounded repair budget: %+v", response.Checkpoint)
	}
	recovered, err := loop.Resume(
		context.Background(), "Explore your predecessor.", *response.Checkpoint,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Content != "The predecessor contains an agent package." ||
		executions != 1 || len(generator.requests) != 5 {
		t.Fatalf(
			"recovered=%+v executions=%d requests=%d",
			recovered, executions, len(generator.requests),
		)
	}
}

func TestAgentLoopRepairsTextualMarkupDuringToolsDisabledRevision(t *testing.T) {
	t.Parallel()
	clock := &loopTestClock{now: time.Now().UTC()}
	predictor, err := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-probe", Name: "probe",
				Arguments: json.RawMessage(`{"expect":"returns data"}`),
			}},
		},
		{
			Content:      "<tool_call><function=probe>",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "I will inspect one file at a time.",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "The predecessor code inspection is complete.",
			FinishReason: protocol.FinishStop,
		},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "probe", Description: "Probe predecessor code.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator,
		manager,
		LoopConfig{Model: "model"},
		&LoopDeps{
			Predictions:    predictor,
			EventCommitter: receiptCommitter{},
			Clock:          clock,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Explore predecessor code.")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "The predecessor code inspection is complete." ||
		response.ProviderCalls != 4 || len(response.ToolEvents) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if len(generator.requests) != 4 ||
		len(generator.requests[1].Tools) != 0 ||
		len(generator.requests[2].Tools) != 0 ||
		!requestContains(generator.requests[2], textualToolFreeRepairPrompt) ||
		len(generator.requests[3].Tools) == 0 ||
		requestContains(generator.requests[3], "internal plan revision") ||
		requestContains(generator.requests[3], "I will inspect one file at a time.") {
		t.Fatalf("tools-disabled repair requests = %+v", generator.requests)
	}
}

func TestAgentLoopResumePreservesPreRestartTokenUsage(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{{
		Content:      "Resumed with the remaining evidence.",
		FinishReason: protocol.FinishStop,
		Usage: protocol.TokenUsage{
			PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7,
		},
	}}}
	loop, err := NewLoop(
		generator,
		newPolicyManager(t),
		LoopConfig{Model: "model", MaxOutputTokens: 64},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := TurnCheckpoint{
		Version:     1,
		UserContent: "Continue the durable turn.",
		Messages: []protocol.Message{{
			Role: protocol.RoleUser, Content: "Continue the durable turn.",
		}},
		ProviderCalls: 2,
		Usage: protocol.TokenUsage{
			PromptTokens: 6, CompletionTokens: 5, TotalTokens: 11,
		},
	}
	response, err := loop.Resume(
		context.Background(), checkpoint.UserContent, checkpoint,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProviderCalls != 3 ||
		response.Usage.PromptTokens != 9 ||
		response.Usage.CompletionTokens != 9 ||
		response.Usage.TotalTokens != 18 {
		t.Fatalf("resumed usage = %+v", response)
	}
	durable := loop.makeCheckpoint(
		checkpoint.UserContent,
		checkpoint.Messages,
		response,
		0,
		"",
		0,
		false,
		false,
		nil,
		nil,
	)
	if durable.Usage != response.Usage {
		t.Fatalf("durable usage = %+v, want %+v", durable.Usage, response.Usage)
	}
}

func TestAgentLoopAppliesOutputLimitToEveryProviderCall(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-read", Name: "read", Arguments: json.RawMessage(`{}`),
			}},
		},
		{Content: "The evidence is complete.", FinishReason: protocol.FinishStop},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "read",
		Description:    "Read evidence.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"evidence":"verified"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator,
		manager,
		LoopConfig{Model: "model", MaxOutputTokens: 37},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Read and verify the evidence.")
	if err != nil {
		t.Fatal(err)
	}
	if response.ProviderCalls != 2 || len(generator.requests) != 2 {
		t.Fatalf("response=%+v requests=%+v", response, generator.requests)
	}
	for index, request := range generator.requests {
		if request.MaxOutputTokens != 37 {
			t.Fatalf("request %d MaxOutputTokens = %d, want 37", index, request.MaxOutputTokens)
		}
	}
}

func TestAgentLoopFinalizesEmptyResponseAfterToolWork(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-search", Name: "search", Arguments: json.RawMessage(`{}`),
			}},
		},
		{FinishReason: protocol.FinishStop},
		{
			Content:      "I found no stored memories about myself yet.",
			FinishReason: protocol.FinishStop,
		},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "search", Description: "Search memory.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"memories":null}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "What do you remember?")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "I found no stored memories about myself yet." ||
		response.ProviderCalls != 3 || len(response.ToolEvents) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if len(generator.requests) != 3 ||
		len(generator.requests[2].Tools) != 0 ||
		!requestContains(generator.requests[2], emptyFinalRepairPrompt) {
		t.Fatalf("finalization request = %+v", generator.requests)
	}
}

func TestAgentLoopRejectsRepeatedEmptyFinalResponse(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{FinishReason: protocol.FinishStop},
		{FinishReason: protocol.FinishStop},
	}}
	loop, err := NewLoop(
		generator,
		newPolicyManager(t),
		LoopConfig{Model: "model"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Answer me.")
	var incomplete *action.ErrIncomplete
	if !errors.As(err, &incomplete) {
		t.Fatalf("Turn() error = %v, want action.ErrIncomplete", err)
	}
	if response.ProviderCalls != 2 || response.Content != "" ||
		len(generator.requests) != 2 ||
		!requestContains(generator.requests[1], emptyFinalRepairPrompt) {
		t.Fatalf("response=%+v requests=%+v", response, generator.requests)
	}
}

func TestAgentLoopRefreshesMemoryBeforeEveryProviderCall(t *testing.T) {
	t.Parallel()
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call", Name: "read", Arguments: json.RawMessage(`{}`),
			}},
		},
		{Content: "done", FinishReason: protocol.FinishStop},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "read", Description: "Read.", Parameters: json.RawMessage(`{}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	activation := &countingMemoryActivation{}
	loop, err := NewLoop(generator, manager, LoopConfig{
		Model: "model", UserID: "user-a", SessionID: "session-a",
	}, &LoopDeps{MemoryActivation: activation})
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "remember this")
	if err != nil {
		t.Fatal(err)
	}
	if response.ProviderCalls != 2 || activation.calls != 2 {
		t.Fatalf(
			"provider calls = %d, activation calls = %d",
			response.ProviderCalls,
			activation.calls,
		)
	}
	for index, request := range generator.requests {
		want := fmt.Sprintf("activation generation %d", index+1)
		if !requestContains(request, want) {
			t.Fatalf("request %d does not contain %q: %+v", index, want, request)
		}
	}
	if activation.query != "remember this" ||
		activation.userID != "user-a" ||
		activation.sessionID != "session-a" {
		t.Fatalf("activation scope = %+v", activation)
	}
}

func TestAgentLoopRequiresScopeForMemoryActivation(t *testing.T) {
	t.Parallel()
	_, err := NewLoop(
		&sequenceGenerator{},
		newPolicyManager(t),
		LoopConfig{Model: "model"},
		&LoopDeps{MemoryActivation: &countingMemoryActivation{}},
	)
	if err == nil || !strings.Contains(err.Error(), "user and session IDs") {
		t.Fatalf("NewLoop() error = %v", err)
	}
}

func TestAgentLoopAppliesBehavioralDecisionModulation(t *testing.T) {
	t.Parallel()
	emotional := safety.NewEmotionalState()
	emotional.UpdateAll(safety.EmotionalSnapshot{
		Frustration: 0.9,
		Confidence:  0.9,
		Fatigue:     0.7,
	})
	generator := &sequenceGenerator{}
	loop, err := NewLoop(
		generator,
		newPolicyManager(t),
		LoopConfig{Model: "model"},
		&LoopDeps{Behavioral: emotional},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Turn(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	request := generator.requests[0]
	if !requestContains(request, "try an alternative strategy") ||
		!requestContains(request, "never overrides safety") {
		t.Fatalf("behavioral guidance missing from live request: %+v", request)
	}
}

type fixedLivenessPolicy struct {
	policy decision.LivenessDecisionPolicy
}

func (provider fixedLivenessPolicy) LivenessDecisionPolicy(
	context.Context,
	decision.Context,
) (decision.LivenessDecisionPolicy, error) {
	return provider.policy, nil
}

func TestAgentLoopEnforcesLivenessSameStrategyRetryBound(t *testing.T) {
	t.Parallel()
	manager := newPolicyManager(t)
	var calls int
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "bounded_probe", Description: "Run a bounded probe.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"ok":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	policyDecision, err := decision.Derive(decision.Inputs{
		Emotional:    safety.NewEmotionalState().FullSnapshot(),
		Relationship: relationship.Snapshot{Expertise: relationship.Intermediate},
	})
	if err != nil {
		t.Fatal(err)
	}
	policyDecision.SameStrategyRetries = 0
	call := protocol.NormalizedToolCall{
		ID: "same-call", Name: "bounded_probe", Arguments: json.RawMessage(`{}`),
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{FinishReason: protocol.FinishToolCalls, ToolCalls: []protocol.NormalizedToolCall{call}},
		{FinishReason: protocol.FinishToolCalls, ToolCalls: []protocol.NormalizedToolCall{call}},
	}}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"},
		&LoopDeps{DecisionPolicy: fixedLivenessPolicy{policy: policyDecision}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = loop.Turn(context.Background(), "probe")
	var incomplete *action.ErrIncomplete
	if !errors.As(err, &incomplete) ||
		incomplete.Recovery != "change strategy under the enforced liveness decision policy" {
		t.Fatalf("retry bound error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("same strategy executed %d times, want initial attempt only", calls)
	}
}

func TestAgentLoopEnforcesLivenessToolCallBudget(t *testing.T) {
	t.Parallel()
	manager := newPolicyManager(t)
	executions := 0
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "write_part", Description: "Write one distinct required project part.",
		Parameters:     json.RawMessage(`{"type":"object","required":["part"],"properties":{"part":{"type":"integer"}}}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"written":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	policyDecision, err := decision.Derive(decision.Inputs{
		Emotional:    safety.NewEmotionalState().FullSnapshot(),
		Relationship: relationship.Snapshot{Expertise: relationship.Intermediate},
	})
	if err != nil {
		t.Fatal(err)
	}
	policyDecision.ToolCallBudget = 10
	generations := make([]protocol.NormalizedGeneration, 0, 12)
	for index := 0; index < 11; index++ {
		generations = append(generations, protocol.NormalizedGeneration{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: fmt.Sprintf("part-%d", index), Name: "write_part",
				Arguments: json.RawMessage(fmt.Sprintf(`{"part":%d}`, index)),
			}},
		})
	}
	generations = append(generations, protocol.NormalizedGeneration{
		Content: "All required parts were written.", FinishReason: protocol.FinishStop,
	})
	loop, err := NewLoop(
		&sequenceGenerator{generations: generations}, manager,
		LoopConfig{Model: "model", MaxToolCalls: 12},
		&LoopDeps{DecisionPolicy: fixedLivenessPolicy{policy: policyDecision}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = loop.Turn(context.Background(), "build all eleven required parts")
	if !errors.Is(err, ErrToolCallLimit) {
		t.Fatalf("tool-call budget error = %v", err)
	}
	if executions != 10 {
		t.Fatalf("executions=%d, want policy budget 10", executions)
	}
}

func TestSocialTurnUsesLivingContextWithoutManufacturingTaskState(t *testing.T) {
	t.Parallel()
	clock := &loopTestClock{now: time.Now().UTC()}
	ledger, err := premise.New(clock)
	if err != nil {
		t.Fatal(err)
	}
	graph := taskgraph.New("operator turn", 3)
	extractor := &countingPremiseExtractor{}
	activation := &countingMemoryActivation{}
	emotional := safety.NewEmotionalState()
	emotional.UpdateAll(safety.EmotionalSnapshot{
		Frustration: 0.9,
		Confidence:  0.9,
		Fatigue:     0.7,
	})
	model := &recordingSelfModel{snapshot: selfmodel.SelfModel{
		Capabilities: []selfmodel.Capability{{Name: "filesystem_read"}},
	}}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "probe",
		Description:    "Probe the environment.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{Content: "Hello. Good to see you.", FinishReason: protocol.FinishStop},
		{
			Content:      "The analysis depends on verified source evidence.",
			FinishReason: protocol.FinishStop,
		},
	}}
	loop, err := NewLoop(generator, manager, LoopConfig{
		Model: "model", UserID: "operator", SessionID: "session",
	}, &LoopDeps{
		Premises: ledger, PremiseExtractor: extractor,
		TaskGraph: graph, SelfModel: model,
		MemoryActivation: activation, Behavioral: emotional,
		EventCommitter: receiptCommitter{}, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Hello. Good to see you." ||
		response.ProviderCalls != 1 {
		t.Fatalf("social response = %+v", response)
	}
	if extractor.calls != 0 || len(ledger.Active()) != 0 ||
		len(graph.TodoProjection()) != 0 {
		t.Fatalf(
			"social turn mutated task state: extractor=%d premises=%+v tasks=%+v",
			extractor.calls,
			ledger.Active(),
			graph.TodoProjection(),
		)
	}
	socialRequest := generator.requests[0]
	if len(socialRequest.Tools) != 1 ||
		!requestContains(socialRequest, "activation generation 1") ||
		!requestContains(socialRequest, "try an alternative strategy") ||
		!requestContains(socialRequest, "filesystem_read") {
		t.Fatalf("social turn omitted living context: %+v", socialRequest)
	}

	if _, err := loop.Turn(
		context.Background(),
		"Analyze the source and identify its load-bearing assumptions.",
	); err != nil {
		t.Fatal(err)
	}
	if extractor.calls != 1 || len(ledger.Active()) != 1 ||
		len(graph.TodoProjection()) == 0 {
		t.Fatalf(
			"analytical turn did not escalate rigor: extractor=%d premises=%+v tasks=%+v",
			extractor.calls,
			ledger.Active(),
			graph.TodoProjection(),
		)
	}
}

func TestAgentLoopValidationLimitsAndCancellation(t *testing.T) {
	t.Parallel()
	manager := newPolicyManager(t)
	generator := &sequenceGenerator{}
	for _, config := range []LoopConfig{{}, {Model: "model", MaxToolCalls: -1}} {
		if _, err := NewLoop(generator, manager, config, nil); err == nil {
			t.Fatalf("NewLoop(%+v) succeeded", config)
		}
	}
	if _, err := NewLoop(nil, manager, LoopConfig{Model: "model"}, nil); err == nil {
		t.Fatal("NewLoop(nil provider) succeeded")
	}

	limitGenerator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{
				{ID: "one", Name: "missing", Arguments: json.RawMessage(`{}`)},
				{ID: "two", Name: "missing", Arguments: json.RawMessage(`{}`)},
			},
		},
	}}
	loop, err := NewLoop(limitGenerator, manager, LoopConfig{
		Model: "model", MaxToolCalls: 1,
	}, nil)
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}
	if _, err := loop.Turn(context.Background(), "message"); !errors.Is(err, ErrToolCallLimit) {
		t.Fatalf("Turn() error = %v", err)
	}
	if _, err := loop.Turn(context.Background(), " "); err == nil {
		t.Fatal("empty Turn() succeeded")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loop.Turn(cancelled, "message"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Turn() error = %v", err)
	}

	failing := &sequenceGenerator{err: errors.New("provider failed")}
	loop, err = NewLoop(failing, manager, LoopConfig{Model: "model"}, nil)
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}
	if _, err := loop.Turn(context.Background(), "message"); err == nil {
		t.Fatal("provider failure Turn() succeeded")
	}

	invalidGeneration := &sequenceGenerator{generations: []protocol.NormalizedGeneration{{
		FinishReason: "invalid",
	}}}
	loop, err = NewLoop(invalidGeneration, manager, LoopConfig{Model: "model"}, nil)
	if err != nil {
		t.Fatalf("NewLoop() error = %v", err)
	}
	if _, err := loop.Turn(context.Background(), "message"); err == nil {
		t.Fatal("invalid provider generation was accepted")
	}
}

func TestAgentLoopLivingContextPreparationIsBoundedAndCancellationAware(
	t *testing.T,
) {
	t.Parallel()
	composer := blockingContextComposer{}
	loop, err := NewLoop(
		&sequenceGenerator{},
		newPolicyManager(t),
		LoopConfig{
			Model: "model", UserID: "user", SessionID: "session",
			ContextPrepareTimeout: 20 * time.Millisecond,
		},
		&LoopDeps{ContextComposer: composer},
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = loop.Turn(context.Background(), "hello")
	if !errors.Is(err, ErrContextPreparationTimeout) {
		t.Fatalf("bounded context preparation error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded context preparation took %s", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = loop.Turn(ctx, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context preparation error = %v", err)
	}
}

func TestAgentLoopReturnsHonestIncompleteOnProviderAndToolIdle(t *testing.T) {
	t.Parallel()
	manager := newPolicyManager(t)
	providerLoop, err := NewLoop(
		blockingGenerator{},
		manager,
		LoopConfig{Model: "model", IdleTimeout: 10 * time.Millisecond},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = providerLoop.Turn(context.Background(), "wait forever")
	var incomplete *action.ErrIncomplete
	if !errors.As(err, &incomplete) || incomplete.Phase != "provider" ||
		incomplete.Attempt != 1 || incomplete.StuckSince.IsZero() {
		t.Fatalf("provider idle error = %#v, %v", incomplete, err)
	}

	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "blocks",
		Description:    "Block until the turn is idle.",
		Parameters:     json.RawMessage(`{}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	toolLoop, err := NewLoop(
		&sequenceGenerator{generations: []protocol.NormalizedGeneration{{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "blocked", Name: "blocks", Arguments: json.RawMessage(`{}`),
			}},
		}}},
		manager,
		LoopConfig{Model: "model", IdleTimeout: 10 * time.Millisecond},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = toolLoop.Turn(context.Background(), "run blocking tool")
	incomplete = nil
	if !errors.As(err, &incomplete) || incomplete.Phase != "tool" ||
		incomplete.LastTool != "blocks" {
		t.Fatalf("tool idle error = %#v, %v", incomplete, err)
	}
}

func TestAgentLoopDetectsCanonicalRepeatedToolLoop(t *testing.T) {
	t.Parallel()
	manager := newPolicyManager(t)
	executions := 0
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "probe",
		Description:    "Return unchanged evidence.",
		Parameters:     json.RawMessage(`{}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"unchanged":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generations := make([]protocol.NormalizedGeneration, 5)
	arguments := []string{`{"a":1,"b":2}`, `{"b":2,"a":1}`, `{"a":1,"b":2}`, `{"b":2,"a":1}`}
	for index := range arguments {
		generations[index] = protocol.NormalizedGeneration{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID:        fmt.Sprintf("probe-%d", index),
				Name:      "probe",
				Arguments: json.RawMessage(arguments[index]),
			}},
		}
	}
	generations[4] = protocol.NormalizedGeneration{
		Content: "fabricated completion", FinishReason: protocol.FinishStop,
	}
	loop, err := NewLoop(
		&sequenceGenerator{generations: generations},
		manager,
		LoopConfig{Model: "model", RepeatedToolLimit: 4},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "keep probing")
	var incomplete *action.ErrIncomplete
	if !errors.As(err, &incomplete) || incomplete.Phase != "tool_loop" ||
		incomplete.Attempt != 4 || incomplete.LastTool != "probe" {
		t.Fatalf("loop error = %#v, %v", incomplete, err)
	}
	if executions != 4 || len(response.ToolEvents) != 4 ||
		response.Content == "fabricated completion" {
		t.Fatalf("executions = %d, response = %+v", executions, response)
	}
}

func TestAgentLoopEnforcesCircuitBreakerBeforeLiveProviderBoundary(t *testing.T) {
	t.Parallel()
	events, err := dashboard.New(types.SystemClock{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	emotional := safety.NewEmotionalState()
	emotional.Update(0.2, 0.81, 0.2)
	config := circuit.DefaultBreakerConfig()
	config.EventSink = dashboard.CircuitSink{Dashboard: events}
	breaker, err := circuit.NewBreaker(config, emotional, types.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{{
		Content: "must not run", FinishReason: protocol.FinishStop,
	}}}
	loop, err := NewLoop(
		generator,
		newPolicyManager(t),
		LoopConfig{Model: "model"},
		&LoopDeps{CircuitBreaker: breaker},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = loop.Turn(context.Background(), "continue while fatigued")
	var incomplete *circuit.ErrIncomplete
	if !errors.As(err, &incomplete) || incomplete.Phase != "turn" {
		t.Fatalf("circuit error = %#v, %v", incomplete, err)
	}
	if len(generator.requests) != 0 {
		t.Fatal("provider was called after the circuit breaker tripped")
	}
	history := events.History(10, dashboard.EventCircuitBreaker)
	if len(history) != 1 || history[0].Type != dashboard.EventCircuitBreaker {
		t.Fatalf("dashboard history = %+v", history)
	}
}

type blockingGenerator struct{}

func (blockingGenerator) Generate(
	ctx context.Context,
	_ protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	<-ctx.Done()
	return protocol.NormalizedGeneration{}, ctx.Err()
}

func TestAcceptanceRequirement8CodegraphAndExecutionHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &loopTestClock{
		now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
	source, err := journal.Open(
		filepath.Join(t.TempDir(), "events.journal"),
		loopTestCipher{},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cortex.New(cortex.Config{
		Actor: "test-agent", Journal: source, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = source.Close()
	})

	ledger, err := premise.New(clock)
	if err != nil {
		t.Fatal(err)
	}
	predictor, err := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	model, err := selfmodel.NewFromCodeGraph(
		ctx,
		clock,
		selfmodel.NewImmutableCore(nil),
		"../..",
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(ctx, tools.Registration{
		Name: "probe", Description: "Return a value.",
		Parameters: json.RawMessage(
			`{"type":"object","properties":{"value":{"type":"integer"}}}`,
		),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			if string(arguments) != `{"value":1}` {
				t.Fatalf("tool received prediction metadata: %s", arguments)
			}
			return json.RawMessage(`{"value":1}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-probe", Name: "probe",
				Arguments: json.RawMessage(
					`{"value":1,"expect":"returns non-empty data"}`,
				),
			}},
		},
		{Content: "observed", FinishReason: protocol.FinishStop},
	}}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, &LoopDeps{
		Premises:          ledger,
		Predictions:       predictor,
		PredictionRecords: store,
		SelfModel:         model,
		Citations:         store,
		Events:            store,
		EventCommitter:    store,
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(ctx, "probe it")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolEvents) != 1 || response.ToolEvents[0].Event == nil {
		t.Fatalf("ToolEvents = %+v", response.ToolEvents)
	}
	event := response.ToolEvents[0].Event
	if event.MMRLeafHash == ([32]byte{}) ||
		event.MMRRootAtTime == ([32]byte{}) {
		t.Fatal("ToolEvent is not integrity-bound")
	}
	if event.Expect == "" || event.Match == nil || !*event.Match {
		t.Fatalf("prediction fields = expect %q, match %v", event.Expect, event.Match)
	}
	citation := protocol.Citation{
		ToolEventID:   event.ID,
		MMRLeafHash:   event.MMRLeafHash,
		MMRRootAtTime: event.MMRRootAtTime,
		Verified:      true, // Must be ignored and re-derived by CitePremise.
	}
	item, err := ledger.Add("probe returned a value", premise.SourceToolEvidence, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := loop.CitePremise(ctx, item.ID, citation); err != nil {
		t.Fatal(err)
	}
	cited, ok := ledger.Get(item.ID)
	if !ok || cited.Status != premise.Cited || cited.Citation == nil ||
		!cited.Citation.Verified {
		t.Fatalf("cited premise = %+v", cited)
	}
	if capabilities := model.Snapshot().Capabilities; len(capabilities) != 1 ||
		capabilities[0].Name != "probe" {
		t.Fatalf("capabilities = %+v", capabilities)
	}

	var schema map[string]json.RawMessage
	if err := json.Unmarshal(generator.requests[0].Tools[0].Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(schema["properties"], &properties); err != nil {
		t.Fatal(err)
	}
	if _, ok := properties["expect"]; !ok {
		t.Fatal("expect was not inserted into JSON Schema properties")
	}
}

func TestMissingToolExpectationIsRepairedBeforeDispatch(t *testing.T) {
	clock := &loopTestClock{now: time.Now().UTC()}
	predictor, err := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	executions := 0
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "probe", Description: "Probe once.",
		Parameters: json.RawMessage(
			`{"type":"object","properties":{"value":{"type":"integer"}},"additionalProperties":false}`,
		),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			executions++
			if string(arguments) != `{"value":1}` {
				t.Fatalf("tool arguments = %s", arguments)
			}
			return json.RawMessage(`{"value":1}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			Content: "I will inspect it.", FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "missing-expect", Name: "probe",
				Arguments: json.RawMessage(`{"value":1}`),
			}},
		},
		{
			Content: "Inspecting it.", FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "with-expect", Name: "probe",
				Arguments: json.RawMessage(
					`{"value":1,"expect":"returns the requested value"}`,
				),
			}},
		},
		{Content: "Observed it.", FinishReason: protocol.FinishStop},
	}}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"},
		&LoopDeps{
			Predictions: predictor, EventCommitter: receiptCommitter{}, Clock: clock,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	observer := &streamTestObserver{}
	response, err := loop.Turn(
		WithGenerationObserver(context.Background(), observer), "probe it",
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Observed it." || executions != 1 {
		t.Fatalf("response=%+v executions=%d", response, executions)
	}
	if observer.resets != 1 || len(generator.requests) != 3 ||
		!requestContains(generator.requests[1], expectationRepairPrompt) {
		t.Fatalf(
			"resets=%d requests=%d repair_prompt=%v",
			observer.resets, len(generator.requests),
			len(generator.requests) > 1 &&
				requestContains(generator.requests[1], expectationRepairPrompt),
		)
	}
}

func TestMissingToolExpectationFallsBackWithoutDispatch(t *testing.T) {
	clock := &loopTestClock{now: time.Now().UTC()}
	predictor, err := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "probe", Description: "Probe once.",
		Parameters: json.RawMessage(
			`{"type":"object","properties":{"value":{"type":"integer"}},"additionalProperties":false}`,
		),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("tool without an expectation was dispatched")
			return nil, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	missing := protocol.NormalizedGeneration{
		FinishReason: protocol.FinishToolCalls,
		ToolCalls: []protocol.NormalizedToolCall{{
			ID: "missing", Name: "probe", Arguments: json.RawMessage(`{"value":1}`),
		}},
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		missing, missing, missing,
		{Content: "I could not safely run the probe.", FinishReason: protocol.FinishStop},
	}}
	loop, err := NewLoop(
		generator, manager, LoopConfig{Model: "model"},
		&LoopDeps{
			Predictions: predictor, EventCommitter: receiptCommitter{}, Clock: clock,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "probe it")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "I could not safely run the probe." ||
		len(response.ToolEvents) != 0 || len(generator.requests) != 4 ||
		!requestContains(generator.requests[3], expectationFinalPrompt) ||
		len(generator.requests[3].Tools) != 0 {
		t.Fatalf("response=%+v requests=%+v", response, generator.requests)
	}
}

func TestMismatchForcesToolsStrippedRevisionStep(t *testing.T) {
	clock := &loopTestClock{now: time.Now().UTC()}
	model := &recordingSelfModel{}
	predictor, err := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "probe", Description: "Probe.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call", Name: "probe",
				Arguments: json.RawMessage(`{"expect":"returns data"}`),
			}},
		},
		{Content: "I need a different strategy.", FinishReason: protocol.FinishStop},
		{Content: "I completed the task after revising the strategy.", FinishReason: protocol.FinishStop},
	}}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, &LoopDeps{
		Predictions:    predictor,
		SelfModel:      model,
		EventCommitter: receiptCommitter{},
		Clock:          clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "I completed the task after revising the strategy." {
		t.Fatalf("response content = %q", response.Content)
	}
	if len(generator.requests) != 3 ||
		len(generator.requests[1].Tools) != 0 ||
		len(generator.requests[2].Tools) == 0 ||
		requestContains(generator.requests[2], "internal plan revision") ||
		requestContains(generator.requests[2], "I need a different strategy.") {
		t.Fatal("revision request was not tools-stripped")
	}
	if len(model.capabilityEvents) != 0 {
		t.Fatalf(
			"mismatched successful event proved capabilities: %+v",
			model.capabilityEvents,
		)
	}
	if len(model.failures) != 0 {
		t.Fatalf("successful mismatched event recorded failures: %+v", model.failures)
	}
}

func TestIncompleteRevisionRestoresToolsBeforeCompletionContinues(t *testing.T) {
	clock := &loopTestClock{now: time.Now().UTC()}
	predictor, err := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "probe", Description: "Probe.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "mismatch", Name: "probe",
				Arguments: json.RawMessage(`{"expect":"returns data"}`),
			}},
		},
		{Content: "Revise by verifying the remaining live criterion.", FinishReason: protocol.FinishStop},
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "verify", Name: "probe",
				Arguments: json.RawMessage(`{"expect":"returns null"}`),
			}},
		},
		{Content: "Evidence-backed final.", FinishReason: protocol.FinishStop},
		{Content: "Evidence-backed final after completion verification.", FinishReason: protocol.FinishStop},
	}}
	gate := &sequenceCompletionGate{}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, &LoopDeps{
		Predictions:    predictor,
		EventCommitter: receiptCommitter{},
		CompletionGate: gate,
		Clock:          clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "probe and verify")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Evidence-backed final after completion verification." {
		t.Fatalf("response content = %q", response.Content)
	}
	if len(generator.requests) != 5 || len(generator.requests[1].Tools) != 0 {
		t.Fatalf("revision request did not strip tools: %+v", generator.requests)
	}
	if len(generator.requests[2].Tools) == 0 ||
		requestContains(generator.requests[2], "internal plan revision") ||
		requestContains(generator.requests[2], "Evidence-backed final.") {
		t.Fatalf("continuation did not restore tools: %+v", generator.requests[2])
	}
	if !requestContains(generator.requests[4], completionContinuationMarker) {
		t.Fatalf(
			"completion continuation missing after restored tool: %+v",
			generator.requests[4],
		)
	}
}

func TestTaskGraphConvergenceChecksWholeToolBatchBeforeRevision(t *testing.T) {
	t.Parallel()
	clock := &loopTestClock{now: time.Now().UTC()}
	graph := taskgraph.New("inspect lineage", 1)
	subgoal := graph.AddSubgoal("read predecessor files")
	duplicateResult := json.RawMessage(`{"value":"same"}`)
	evidenceInput := append([]byte("read\x00"), duplicateResult...)
	evidenceDigest := blake3.Sum256(evidenceInput)
	graph.ObserveAction(fmt.Sprintf("%x", evidenceDigest[:]), subgoal.ID)

	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{
				{
					ID: "call-duplicate", Name: "read",
					Arguments: json.RawMessage(`{"path":"duplicate"}`),
				},
				{
					ID: "call-fresh", Name: "read",
					Arguments: json.RawMessage(`{"path":"fresh"}`),
				},
			},
		},
		{Content: "Both files were inspected.", FinishReason: protocol.FinishStop},
	}}
	manager := newPolicyManager(t)
	executions := 0
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "read", Description: "Read predecessor code.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"required":["path"],
			"properties":{"path":{"type":"string"}},
			"additionalProperties":false
		}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			executions++
			var input struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			if input.Path == "duplicate" {
				return duplicateResult, nil
			}
			return json.RawMessage(`{"value":"fresh"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(
		generator,
		manager,
		LoopConfig{Model: "model"},
		&LoopDeps{TaskGraph: graph, Clock: clock},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(context.Background(), "Inspect both files.")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Both files were inspected." ||
		len(response.ToolEvents) != 2 || executions != 2 ||
		len(generator.requests) != 2 ||
		len(generator.requests[1].Tools) == 0 ||
		graph.ActionsSinceGrowth() != 0 {
		t.Fatalf(
			"response=%+v executions=%d requests=%d second_tools=%d actions_since_growth=%d",
			response,
			executions,
			len(generator.requests),
			len(generator.requests[1].Tools),
			graph.ActionsSinceGrowth(),
		)
	}
}

func TestPlanOnlyAssistantTurnExtractsPremises(t *testing.T) {
	clock := &loopTestClock{now: time.Now().UTC()}
	ledger, _ := premise.New(clock)
	graph := taskgraph.New("form a plan", 4)
	subgoal := graph.AddSubgoal("inspect source")
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{{
		Content:      "I can inspect the source before changing it.",
		FinishReason: protocol.FinishStop,
	}}}
	loop, err := NewLoop(
		generator,
		newPolicyManager(t),
		LoopConfig{Model: "model"},
		&LoopDeps{
			Premises:         ledger,
			PremiseExtractor: premise.DeterministicExtractor{},
			TaskGraph:        graph,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Turn(context.Background(), "make a plan"); err != nil {
		t.Fatal(err)
	}
	active := ledger.Active()
	if len(active) != 1 ||
		len(active[0].Affected) != 1 ||
		active[0].Affected[0] != subgoal.ID {
		t.Fatalf("plan-only premises = %+v", active)
	}
}

func TestRefutedPremiseWithoutSubtreeBlocksDispatch(t *testing.T) {
	clock := &loopTestClock{now: time.Now().UTC()}
	ledger, _ := premise.New(clock)
	item, _ := ledger.Add("unsafe assumption", premise.SourceAssumption, 0)
	_ = ledger.Refute(item.ID, nil)
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{{
		Content: "should not be reached", FinishReason: protocol.FinishStop,
	}}}
	loop, err := NewLoop(
		generator,
		newPolicyManager(t),
		LoopConfig{Model: "model"},
		&LoopDeps{Premises: ledger},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Turn(
		context.Background(), "continue",
	); !errors.Is(err, ErrRefutedPremise) {
		t.Fatalf("Turn() error = %v", err)
	}
	if len(generator.requests) != 0 {
		t.Fatal("provider was called despite an unrevised refuted premise")
	}
}

func TestAcceptanceRequirement6PlanPremisesAndTargetedRefutation(t *testing.T) {
	ctx := context.Background()
	clock := &loopTestClock{
		now: time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC),
	}
	source, err := journal.Open(
		filepath.Join(t.TempDir(), "plan-events.journal"),
		loopTestCipher{},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cortex.New(cortex.Config{
		Actor: "plan-agent", Journal: source, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = source.Close()
	})

	ledger, _ := premise.New(clock)
	graph := taskgraph.New("audit wave five", 4)
	affected := graph.AddSubgoal("inspect live loop")
	unaffected := graph.AddSubgoal("preserve unrelated work")
	oldAffected, _ := ledger.Add(
		"the old loop plan is sufficient", premise.SourceAssumption, 0,
	)
	_ = ledger.Attach(oldAffected.ID, []string{affected.ID})
	oldUnaffected, _ := ledger.Add(
		"the unrelated subtree remains valid", premise.SourceAssumption, 0,
	)
	_ = ledger.Attach(oldUnaffected.ID, []string{unaffected.ID})
	_, _ = graph.AddPremise(affected.ID, oldAffected.Statement)
	_, _ = graph.AddPremise(unaffected.ID, oldUnaffected.Statement)

	predictor, _ := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 3,
	)
	model, err := selfmodel.NewFromCodeGraph(
		ctx, clock, selfmodel.NewImmutableCore(nil), "../..",
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(ctx, tools.Registration{
		Name: "probe", Description: "Probe current state.",
		Parameters: json.RawMessage(
			`{"type":"object","properties":{"query":{"type":"string"}}}`,
		),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"version":"1.0.0"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			Content:      "I can probe the current endpoint.",
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-plan", Name: "probe",
				Arguments: json.RawMessage(
					`{"query":"version","expect":"returns non-empty version data"}`,
				),
			}},
		},
		{Content: "Version observed.", FinishReason: protocol.FinishStop},
		{
			Content:      "I can use an alternate inspection route.",
			FinishReason: protocol.FinishStop,
		},
	}}
	premiseGenerator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			Content:      `{"premises":["the endpoint exposes version metadata"]}`,
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      `{"premises":["an alternate inspection route is available"]}`,
			FinishReason: protocol.FinishStop,
		},
	}}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, &LoopDeps{
		Premises: ledger,
		PremiseExtractor: premise.LayeredExtractor{
			Model: premise.ModelExtractor{
				Provider: premiseGenerator,
				Model:    "premise-extractor",
			},
		},
		Predictions:       predictor,
		PredictionRecords: store,
		TaskGraph:         graph,
		SelfModel:         model,
		Citations:         store,
		Events:            store,
		EventCommitter:    store,
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := loop.Turn(ctx, "inspect the endpoint")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolEvents) != 1 || first.ToolEvents[0].Event == nil {
		t.Fatalf("first response = %+v", first)
	}
	if !requestContains(
		generator.requests[1],
		"[ASSUMPTION] probe: returns non-empty version data",
	) {
		t.Fatal("freshly extracted premise was absent from the next model step")
	}
	if !requestContains(
		generator.requests[1],
		"[ASSUMPTION] the endpoint exposes version metadata",
	) {
		t.Fatal("model-extracted factual premise was absent from the next model step")
	}
	if len(premiseGenerator.requests) != 1 ||
		len(premiseGenerator.requests[0].Tools) != 0 {
		t.Fatalf("premise extractor requests = %+v", premiseGenerator.requests)
	}

	var planPremise *premise.Premise
	for _, candidate := range ledger.Active() {
		if strings.HasPrefix(candidate.Statement, "probe:") {
			planPremise = candidate
			break
		}
	}
	if planPremise == nil {
		t.Fatalf("plan premises = %+v", ledger.Active())
	}
	event := first.ToolEvents[0].Event
	if err := loop.RefutePremise(ctx, planPremise.ID, protocol.Citation{
		ToolEventID:   event.ID,
		MMRLeafHash:   event.MMRLeafHash,
		MMRRootAtTime: event.MMRRootAtTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Turn(ctx, "continue safely"); err != nil {
		t.Fatal(err)
	}
	revisionRequest := generator.requests[2]
	if len(revisionRequest.Tools) != 0 ||
		!requestContains(revisionRequest, "[REFUTED]") {
		t.Fatalf("revision request = %+v", revisionRequest)
	}

	remaining := ledger.Active()
	foundUnaffected := false
	foundRevision := false
	for _, candidate := range remaining {
		foundUnaffected = foundUnaffected ||
			candidate.ID == oldUnaffected.ID
		foundRevision = foundRevision ||
			strings.Contains(candidate.Statement, "alternate inspection route")
	}
	if !foundUnaffected || !foundRevision {
		t.Fatalf("post-revision premises = %+v", remaining)
	}
	if len(premiseGenerator.requests) != 2 ||
		len(premiseGenerator.requests[1].Tools) != 0 {
		t.Fatalf("revision premise extractor requests = %+v", premiseGenerator.requests)
	}
	projection := graph.TodoProjection()
	if len(projection) != 2 ||
		projection[1].ID != unaffected.ID ||
		projection[1].Text != "preserve unrelated work" {
		t.Fatalf("task projection = %+v", projection)
	}
}

func TestAcceptanceRequirement9CassandraLiveEditAndDualRecord(t *testing.T) {
	ctx := context.Background()
	clock := &loopTestClock{
		now: time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC),
	}
	source, err := journal.Open(
		filepath.Join(t.TempDir(), "cassandra-events.journal"),
		loopTestCipher{},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cortex.New(cortex.Config{
		Actor: "cassandra-agent", Journal: source, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = source.Close()
	})
	auditor, err := cassandra.NewJournalAuditor(store, "cassandra")
	if err != nil {
		t.Fatal(err)
	}
	controller, err := cassandra.New(clock, auditor)
	if err != nil {
		t.Fatal(err)
	}
	predictor, _ := prediction.NewEngine(
		clock, prediction.DeterministicDetector{}, 3,
	)
	manager := newPolicyManager(t)
	if err := manager.Register(ctx, tools.Registration{
		Name: "probe", Description: "Probe.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			Content:      "The probe will return data.",
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-cassandra", Name: "probe",
				Arguments: json.RawMessage(
					`{"expect":"returns non-empty data"}`,
				),
			}},
		},
		{Content: "Revised.", FinishReason: protocol.FinishStop},
	}}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, &LoopDeps{
		Predictions:       predictor,
		PredictionRecords: store,
		Cassandra:         controller,
		EventCommitter:    store,
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Turn(ctx, "probe"); err != nil {
		t.Fatal(err)
	}
	if !requestContains(
		generator.requests[1], "The probe may return data.",
	) {
		t.Fatalf("second request = %+v", generator.requests[1])
	}
	edits := controller.Edits()
	if len(edits) != 1 ||
		edits[0].Trigger != cassandra.TriggerPredictionMismatch ||
		edits[0].OriginalContent != "The probe will return data." {
		t.Fatalf("Cassandra edits = %+v", edits)
	}
	undone, err := controller.Undo(&edits[0].ID)
	if err != nil || undone.State != cassandra.EditUndone ||
		undone.OriginalContent != "The probe will return data." {
		t.Fatalf("undo = %+v, %v", undone, err)
	}
	prior := protocol.Message{
		Role:    protocol.RoleAssistant,
		Content: "The deployment version is one point zero.",
	}
	correction, err := loop.ApplyUserCorrection(
		"prior-message",
		&prior,
		"The deployment version is two point zero.",
		"user supplied the current version",
		false,
	)
	if err != nil ||
		prior.Content != "The deployment version is two point zero." {
		t.Fatalf("user correction = %+v, message %+v, error %v", correction, prior, err)
	}
	if _, err := loop.UndoCassandra(&correction.ID, &prior); err != nil ||
		prior.Content != "The deployment version is one point zero." {
		t.Fatalf("user correction undo = message %+v, error %v", prior, err)
	}
	if events := store.ListByType(memory.Event); len(events) != 6 {
		t.Fatalf(
			"durable Event records = %d, want tool, prediction, and two edit/undo pairs",
			len(events),
		)
	}
}

func TestAcceptanceRequirement7ModelAssistedMismatchForcesRevision(t *testing.T) {
	ctx := context.Background()
	clock := &loopTestClock{
		now: time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC),
	}
	source, err := journal.Open(
		filepath.Join(t.TempDir(), "model-mismatch.journal"),
		loopTestCipher{},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cortex.New(cortex.Config{
		Actor: "prediction-agent", Journal: source, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = source.Close()
	})
	comparator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{{
		Content: `{"mismatch":true}`, FinishReason: protocol.FinishStop,
	}}}
	predictor, err := prediction.NewEngine(
		clock,
		prediction.LayeredDetector{Fallback: prediction.ModelDetector{
			Provider: comparator,
			Model:    "semantic-comparator",
		}},
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(ctx, tools.Registration{
		Name: "version", Description: "Return deployed version.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"version":"2.0.0"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "call-version", Name: "version",
				Arguments: json.RawMessage(
					`{"expect":"the deployed version remains 1.0.0"}`,
				),
			}},
		},
		{
			Content:      "The deployment changed; revise the strategy.",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "The verified deployed version is 2.0.0.",
			FinishReason: protocol.FinishStop,
		},
	}}
	loop, err := NewLoop(generator, manager, LoopConfig{Model: "model"}, &LoopDeps{
		Predictions:       predictor,
		PredictionRecords: store,
		EventCommitter:    store,
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(ctx, "check version")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "The verified deployed version is 2.0.0." {
		t.Fatalf("response = %+v", response)
	}
	if len(comparator.requests) != 1 ||
		len(comparator.requests[0].Tools) != 0 {
		t.Fatalf("comparator requests = %+v", comparator.requests)
	}
	if len(generator.requests) != 3 ||
		len(generator.requests[1].Tools) != 0 ||
		len(generator.requests[2].Tools) == 0 ||
		requestContains(generator.requests[2], "internal plan revision") ||
		requestContains(generator.requests[2], "The deployment changed; revise the strategy.") {
		t.Fatalf("revision requests = %+v", generator.requests)
	}
	events := store.ListByType(memory.Event)
	if len(events) != 2 {
		t.Fatalf("durable prediction path wrote %d events, want two", len(events))
	}
	foundModelRecord := false
	for _, id := range events {
		stored, resolveErr := store.Resolve(id)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		var record protocol.PredictionRecord
		if json.Unmarshal(stored.Version.Data, &record) == nil &&
			record.ComparisonMethod == "model-assisted" {
			foundModelRecord = true
		}
	}
	if !foundModelRecord {
		t.Fatal("durable model-assisted PredictionRecord was not found")
	}
}

func requestContains(request protocol.GenerationRequest, needle string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

type sequenceGenerator struct {
	mu          sync.Mutex
	generations []protocol.NormalizedGeneration
	requests    []protocol.GenerationRequest
	err         error
}

type countingMemoryActivation struct {
	calls     int
	query     string
	userID    string
	sessionID string
}

type blockingContextComposer struct{}

func (blockingContextComposer) Compose(
	ctx context.Context,
	_ ContextSnapshot,
) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (activation *countingMemoryActivation) Activate(
	_ context.Context,
	query string,
	userID string,
	sessionID string,
) (string, error) {
	activation.calls++
	activation.query = query
	activation.userID = userID
	activation.sessionID = sessionID
	return fmt.Sprintf("activation generation %d", activation.calls), nil
}

func newPolicyManager(t *testing.T) *tools.Manager {
	t.Helper()
	pipeline, err := policy.NewDefault(
		types.SystemClock{},
		&policy.MemoryAuditor{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("policy.NewDefault() error = %v", err)
	}
	manager, err := tools.NewManager(
		types.SystemClock{},
		tools.WithExecutionPolicy(pipeline),
	)
	if err != nil {
		t.Fatalf("tools.NewManager() error = %v", err)
	}
	return manager
}

func TestTextualToolCompatibilityAllowsLiteralCodeAndRejectsMissingRequired(
	t *testing.T,
) {
	t.Parallel()
	definition := protocol.ToolDefinition{
		Name: "read",
		Parameters: json.RawMessage(`{
			"type":"object","required":["path"],
			"properties":{"path":{"type":"string","minLength":1}},
			"additionalProperties":false
		}`),
	}
	literal := protocol.NormalizedGeneration{
		Content:      "The literal `<tool_call>` and `<parameter=path>` are provider syntax.",
		FinishReason: protocol.FinishStop,
	}
	if err := normalizeTextualToolCalls(
		&literal, []protocol.ToolDefinition{definition},
	); err != nil {
		t.Fatalf("literal Markdown code was rejected: %v", err)
	}
	if literal.Content == "" || len(literal.ToolCalls) != 0 {
		t.Fatalf("literal prose was normalized: %+v", literal)
	}
	missing := protocol.NormalizedGeneration{
		Content:      "<tool_call><function=read></tool_call>",
		FinishReason: protocol.FinishStop,
	}
	if err := normalizeTextualToolCalls(
		&missing, []protocol.ToolDefinition{definition},
	); !errors.Is(err, ErrTextualToolMarkup) {
		t.Fatalf("missing required argument error = %v", err)
	}
}

func TestTextualToolCompatibilityNormalizesMultipleCalls(t *testing.T) {
	t.Parallel()
	definition := protocol.ToolDefinition{
		Name: "read",
		Parameters: json.RawMessage(`{
			"type":"object","required":["path"],
			"properties":{"path":{"type":"string","minLength":1}},
			"additionalProperties":false
		}`),
	}
	generation := protocol.NormalizedGeneration{
		Content: "Checking both.\n" +
			"<tool_call><function=read><parameter=path>one</tool_call>\n" +
			"<tool_call><function=read><parameter=path>two</tool_call>",
		FinishReason: protocol.FinishStop,
	}
	if err := normalizeTextualToolCalls(
		&generation, []protocol.ToolDefinition{definition},
	); err != nil {
		t.Fatal(err)
	}
	if generation.Content != "Checking both." || len(generation.ToolCalls) != 2 {
		t.Fatalf("multiple normalized calls = %+v", generation)
	}
}

func (generator *sequenceGenerator) Generate(
	_ context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	generator.requests = append(generator.requests, request)
	if generator.err != nil {
		return protocol.NormalizedGeneration{}, generator.err
	}
	if len(generator.generations) == 0 {
		return protocol.NormalizedGeneration{
			Content: "done", FinishReason: protocol.FinishStop,
		}, nil
	}
	next := generator.generations[0]
	generator.generations = generator.generations[1:]
	return next, nil
}
