package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

const (
	mimoToolOpen       = "<tool_call>"
	mimoToolClose      = "</tool_call>"
	mimoFunctionOpen   = "<function="
	mimoParameterOpen  = "<parameter="
	mimoParameterClose = "</parameter>"
)

// MiMoAdapter is the first-class Xiaomi MiMo compatibility boundary. It
// retains the OpenAI-compatible wire envelope while applying MiMo's agentic
// sampling profile and normalizing tool calls emitted through content or
// reasoning by serving stacks that did not install MiMo's native parser.
type MiMoAdapter struct {
	textualRecoveries atomic.Uint64
}

func (*MiMoAdapter) Name() string { return "mimo" }

func (*MiMoAdapter) TranslateRequest(request protocol.GenerationRequest) (json.RawMessage, error) {
	raw, err := (OpenAIAdapter{}).TranslateRequest(request)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("provider mimo: decode compatible request: %w", err)
	}
	// Xiaomi recommends lower sampling for agentic tool use.
	payload["temperature"] = 0.3
	payload["top_p"] = 0.95
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("provider mimo: encode request: %w", err)
	}
	return encoded, nil
}

func (adapter *MiMoAdapter) TranslateResponse(raw json.RawMessage) (protocol.NormalizedGeneration, error) {
	generation, err := (OpenAIAdapter{}).TranslateResponse(raw)
	if err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	return adapter.FinalizeGeneration(generation)
}

func (*MiMoAdapter) TranslateStreamEvent(event []byte) (protocol.StreamChunk, error) {
	return (OpenAIAdapter{}).TranslateStreamEvent(event)
}

func (adapter *MiMoAdapter) FinalizeGeneration(
	generation protocol.NormalizedGeneration,
) (protocol.NormalizedGeneration, error) {
	content, contentCalls, contentMarkup, err := parseMiMoCalls(generation.Content)
	if err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	reasoning, reasoningCalls, reasoningMarkup, err := parseMiMoCalls(generation.Reasoning)
	if err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	recovered := append(contentCalls, reasoningCalls...)
	if contentMarkup || reasoningMarkup {
		adapter.textualRecoveries.Add(1)
		generation.Content, generation.Reasoning = content, reasoning
	}
	if len(recovered) == 0 {
		return generation, nil
	}
	if len(generation.ToolCalls) == 0 {
		generation.ToolCalls = recovered
	} else if !sameToolCalls(generation.ToolCalls, recovered) {
		return protocol.NormalizedGeneration{}, fmt.Errorf(
			"%w: native and textual MiMo calls disagree", ErrToolProtocol,
		)
	}
	generation.FinishReason = protocol.FinishToolCalls
	return generation, nil
}

func (adapter *MiMoAdapter) TextualRecoveries() uint64 {
	return adapter.textualRecoveries.Load()
}

func parseMiMoCalls(text string) (string, []protocol.NormalizedToolCall, bool, error) {
	if !strings.Contains(text, mimoToolOpen) && !strings.Contains(text, mimoToolClose) {
		return text, nil, false, nil
	}
	var cleaned strings.Builder
	var calls []protocol.NormalizedToolCall
	remainder := text
	for {
		start := strings.Index(remainder, mimoToolOpen)
		if start < 0 {
			if strings.Contains(remainder, mimoToolClose) {
				return "", nil, true, fmt.Errorf("%w: unmatched MiMo tool close tag", ErrToolProtocol)
			}
			cleaned.WriteString(remainder)
			break
		}
		cleaned.WriteString(remainder[:start])
		afterOpen := remainder[start+len(mimoToolOpen):]
		closeAt := strings.Index(afterOpen, mimoToolClose)
		if closeAt < 0 {
			return "", nil, true, fmt.Errorf("%w: incomplete MiMo tool block", ErrToolProtocol)
		}
		rawBlock := remainder[start : start+len(mimoToolOpen)+closeAt+len(mimoToolClose)]
		call, err := parseMiMoBlock(afterOpen[:closeAt], rawBlock)
		if err != nil {
			return "", nil, true, err
		}
		calls = append(calls, call)
		remainder = afterOpen[closeAt+len(mimoToolClose):]
	}
	return strings.TrimSpace(cleaned.String()), calls, true, nil
}

func parseMiMoBlock(body, rawBlock string) (protocol.NormalizedToolCall, error) {
	body = strings.TrimSpace(body)
	name := ""
	remainder := ""
	if strings.HasPrefix(body, mimoFunctionOpen) {
		end := strings.IndexByte(body, '>')
		if end < len(mimoFunctionOpen) {
			return protocol.NormalizedToolCall{}, fmt.Errorf("%w: malformed MiMo function tag", ErrToolProtocol)
		}
		name = strings.TrimSpace(body[len(mimoFunctionOpen):end])
		remainder = body[end+1:]
	} else if strings.HasPrefix(body, "<function>") {
		end := strings.Index(body, "</function>")
		if end < 0 {
			return protocol.NormalizedToolCall{}, fmt.Errorf("%w: malformed MiMo function name", ErrToolProtocol)
		}
		name = strings.TrimSpace(body[len("<function>"):end])
		remainder = body[end+len("</function>"):]
	} else {
		return protocol.NormalizedToolCall{}, fmt.Errorf("%w: MiMo function tag is missing", ErrToolProtocol)
	}
	if !validMiMoName(name) {
		return protocol.NormalizedToolCall{}, fmt.Errorf("%w: invalid MiMo function name", ErrToolProtocol)
	}
	if end := strings.LastIndex(remainder, "</function>"); end >= 0 {
		remainder = remainder[:end]
	}
	parameters := map[string]any{}
	for strings.TrimSpace(remainder) != "" {
		remainder = strings.TrimSpace(remainder)
		if !strings.HasPrefix(remainder, mimoParameterOpen) {
			return protocol.NormalizedToolCall{}, fmt.Errorf("%w: malformed MiMo parameter", ErrToolProtocol)
		}
		tagEnd := strings.IndexByte(remainder, '>')
		if tagEnd < len(mimoParameterOpen) {
			return protocol.NormalizedToolCall{}, fmt.Errorf("%w: malformed MiMo parameter tag", ErrToolProtocol)
		}
		parameterName := strings.TrimSpace(remainder[len(mimoParameterOpen):tagEnd])
		if !validMiMoName(parameterName) {
			return protocol.NormalizedToolCall{}, fmt.Errorf("%w: invalid MiMo parameter name", ErrToolProtocol)
		}
		if _, duplicate := parameters[parameterName]; duplicate {
			return protocol.NormalizedToolCall{}, fmt.Errorf("%w: duplicate MiMo parameter", ErrToolProtocol)
		}
		valueAndRest := remainder[tagEnd+1:]
		closeAt := strings.Index(valueAndRest, mimoParameterClose)
		if closeAt >= 0 {
			parameters[parameterName] = mimoParameterValue(valueAndRest[:closeAt])
			remainder = valueAndRest[closeAt+len(mimoParameterClose):]
			continue
		}
		next := strings.Index(valueAndRest, mimoParameterOpen)
		if next < 0 {
			parameters[parameterName] = mimoParameterValue(valueAndRest)
			remainder = ""
		} else {
			parameters[parameterName] = mimoParameterValue(valueAndRest[:next])
			remainder = valueAndRest[next:]
		}
	}
	arguments, err := json.Marshal(parameters)
	if err != nil {
		return protocol.NormalizedToolCall{}, fmt.Errorf("%w: encode MiMo parameters", ErrToolProtocol)
	}
	digest := sha256.Sum256([]byte(rawBlock))
	return protocol.NormalizedToolCall{
		ID: fmt.Sprintf("mimo-%x", digest[:12]), Name: name, Arguments: arguments,
	}, nil
}

func mimoParameterValue(raw string) any {
	value := strings.TrimSpace(raw)
	if value == "true" || value == "false" {
		parsed, _ := strconv.ParseBool(value)
		return parsed
	}
	if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
		return integer
	}
	if strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") {
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			return decoded
		}
	}
	return value
}

func validMiMoName(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, current := range value {
		if (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') || current == '_' || current == '-' || current == '.' {
			continue
		}
		return false
	}
	return true
}

func sameToolCalls(left, right []protocol.NormalizedToolCall) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name ||
			!sameJSONValue(left[index].Arguments, right[index].Arguments) {
			return false
		}
	}
	return true
}

func sameJSONValue(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// MiMoCapability is the durable-runtime projection of the harmless tool
// preflight. It is intentionally secret-free.
type MiMoCapability struct {
	Checked   bool      `json:"checked"`
	Mode      string    `json:"mode"`
	Native    bool      `json:"native_tool_calls"`
	MultiTurn bool      `json:"multi_turn"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Strategy  int       `json:"strategy"`
}

// MiMoGenerator runs one harmless multi-turn tool canary before exposing tools
// to a real task. Tool-enabled requests are buffered so raw provider markup can
// never leak into the operator stream before normalization.
type MiMoGenerator struct {
	inner   *Pool
	adapter *MiMoAdapter

	mu         sync.Mutex
	probing    bool
	probeDone  chan struct{}
	capability MiMoCapability
	// noToolRevisionFallback is a one-shot, request-local recovery for MiMo
	// serving stacks that insist on serializing a tool call during an internal
	// tools-stripped plan revision. It must not advance the global tool-bearing
	// compatibility strategy or contaminate later project turns.
	noToolRevisionFallback bool
}

const noToolRevisionCompatibilityPrompt = "This request is an internal tools-disabled planning or finalization step. Return only ordinary prose that directly satisfies the latest system instruction. Do not call, name, describe, or serialize any tool, function, parameter, XML tag, JSON tool object, or tool-call markup."

func NewMiMoGenerator(inner *Pool, adapter *MiMoAdapter) (*MiMoGenerator, error) {
	if inner == nil || adapter == nil {
		return nil, fmt.Errorf("provider mimo: pool and adapter are required")
	}
	return &MiMoGenerator{inner: inner, adapter: adapter}, nil
}

func (generator *MiMoGenerator) Generate(
	ctx context.Context, request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	hasTools := len(request.Tools) > 0
	request.Stream = false
	noToolFallback := false
	if len(request.Tools) > 0 {
		generator.ensureCapability(ctx, request.Model)
		request = generator.applyStrategy(request)
		// This path is intentionally buffered. Sending stream=true through the
		// non-stream decoder makes a valid SSE response look like malformed JSON
		// (it begins with "data:") and was the live MiMo failure mode this
		// compatibility boundary is designed to prevent.
	} else {
		generator.mu.Lock()
		noToolFallback = generator.noToolRevisionFallback
		generator.mu.Unlock()
		if noToolFallback {
			request.Messages = append(
				[]protocol.Message{{
					Role:    protocol.RoleSystem,
					Content: noToolRevisionCompatibilityPrompt,
				}},
				request.Messages...,
			)
		}
	}
	generation, err := generator.inner.Generate(ctx, request)
	if err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	if !hasTools && len(generation.ToolCalls) > 0 {
		return protocol.NormalizedGeneration{}, fmt.Errorf(
			"%w: MiMo emitted a tool call while the active request exposed no tools (compatibility_retry=%t)",
			ErrToolProtocol, noToolFallback,
		)
	}
	if !hasTools {
		generator.mu.Lock()
		generator.noToolRevisionFallback = false
		generator.mu.Unlock()
	}
	return generation, nil
}

func (generator *MiMoGenerator) GenerateStream(
	ctx context.Context,
	request protocol.GenerationRequest,
	deliver func(protocol.StreamChunk) error,
) (protocol.NormalizedGeneration, error) {
	// Buffer every MiMo generation. Serving stacks may serialize a tool call
	// through content/reasoning even when the active recovery request exposes
	// no tools, so no delta is safe to display before final normalization.
	generation, err := generator.Generate(ctx, request)
	if err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	if generation.Reasoning != "" {
		if err := deliver(protocol.StreamChunk{ReasoningDelta: generation.Reasoning}); err != nil {
			return protocol.NormalizedGeneration{}, err
		}
	}
	if generation.Content != "" {
		if err := deliver(protocol.StreamChunk{ContentDelta: generation.Content}); err != nil {
			return protocol.NormalizedGeneration{}, err
		}
	}
	return generation, nil
}

func (generator *MiMoGenerator) Usage() []CredentialUsage { return generator.inner.Usage() }

func (generator *MiMoGenerator) CapabilityStatus() MiMoCapability {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	return generator.capability
}

// AdvanceGenerationStrategy guarantees that a retry changes the provider
// request. Once all compatibility strategies are exhausted it returns false.
func (generator *MiMoGenerator) AdvanceGenerationStrategy(reason string) bool {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	if strings.Contains(reason, "active request exposed no tools") {
		if generator.noToolRevisionFallback {
			generator.noToolRevisionFallback = false
			return false
		}
		generator.noToolRevisionFallback = true
		return true
	}
	if generator.capability.Strategy >= 2 {
		return false
	}
	generator.capability.Strategy++
	generator.capability.Mode = "compatibility"
	generator.capability.Reason = strings.TrimSpace(reason)
	return true
}

func (generator *MiMoGenerator) applyStrategy(request protocol.GenerationRequest) protocol.GenerationRequest {
	generator.mu.Lock()
	strategy := generator.capability.Strategy
	mode := generator.capability.Mode
	generator.mu.Unlock()
	if strategy == 0 && mode != "compatibility" {
		return request
	}
	instruction := "MiMo tool compatibility mode is active. Emit exactly one complete native function call when an action is needed. Do not mix prose after a tool call."
	if strategy >= 2 {
		instruction += " Keep the call minimal and use only parameters from the supplied schema."
	}
	request.Messages = append([]protocol.Message{{Role: protocol.RoleSystem, Content: instruction}}, request.Messages...)
	return request
}

func (generator *MiMoGenerator) ensureCapability(ctx context.Context, model string) {
	generator.mu.Lock()
	if generator.capability.Checked {
		generator.mu.Unlock()
		return
	}
	if generator.probing {
		done := generator.probeDone
		generator.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
		}
		return
	}
	generator.probing = true
	generator.probeDone = make(chan struct{})
	done := generator.probeDone
	generator.mu.Unlock()

	capability := generator.runCapabilityProbe(ctx, model)
	generator.mu.Lock()
	generator.capability = capability
	generator.probing = false
	close(done)
	generator.mu.Unlock()
}

func (generator *MiMoGenerator) runCapabilityProbe(ctx context.Context, model string) MiMoCapability {
	result := MiMoCapability{Checked: true, Mode: "compatibility", CheckedAt: time.Now().UTC()}
	definition := protocol.ToolDefinition{
		Name:        "ion_capability_echo",
		Description: "Harmless provider tool-call capability probe; it has no external effect.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["value","expect"],"properties":{"value":{"type":"string"},"expect":{"type":"string"}}}`),
	}
	before := generator.adapter.TextualRecoveries()
	first, err := generator.inner.Generate(ctx, protocol.GenerationRequest{
		Model: model,
		Messages: []protocol.Message{{Role: protocol.RoleUser,
			Content: "Call ion_capability_echo exactly once with value READY and a short expect prediction."}},
		Tools: []protocol.ToolDefinition{definition},
	})
	if err != nil || len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != definition.Name {
		result.Reason = "native canary did not produce one recoverable tool call"
		return result
	}
	result.Native = generator.adapter.TextualRecoveries() == before
	if result.Native {
		result.Mode = "native-buffered"
	} else {
		result.Mode = "textual-buffered"
	}
	secondMessages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "Call ion_capability_echo exactly once with value READY and a short expect prediction."},
		{Role: protocol.RoleAssistant, Content: first.Content, Reasoning: first.Reasoning, ToolCalls: first.ToolCalls},
		{Role: protocol.RoleTool, Name: definition.Name, ToolCallID: first.ToolCalls[0].ID, Content: `{"value":"READY"}`},
		{Role: protocol.RoleUser, Content: "Reply with READY as ordinary text and do not call a tool."},
	}
	second, err := generator.inner.Generate(ctx, protocol.GenerationRequest{Model: model, Messages: secondMessages, Tools: []protocol.ToolDefinition{definition}})
	if err != nil || len(second.ToolCalls) != 0 || !strings.Contains(strings.ToUpper(second.Content), "READY") {
		result.Mode = "compatibility"
		result.Reason = "multi-turn tool continuation canary failed; compatibility prompting enabled"
		result.Strategy = 1
		return result
	}
	result.MultiTurn = true
	return result
}
