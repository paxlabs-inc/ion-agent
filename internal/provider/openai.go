package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// OpenAIAdapter translates OpenAI Chat Completions requests and responses.
type OpenAIAdapter struct{}

// Name returns the stable provider identifier.
func (OpenAIAdapter) Name() string {
	return "openai"
}

// TranslateRequest converts an O1 request into the OpenAI wire format.
func (OpenAIAdapter) TranslateRequest(request protocol.GenerationRequest) (json.RawMessage, error) {
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("provider openai: invalid request: %w", err)
	}
	type openAIFunction struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	type openAITool struct {
		Type     string         `json:"type"`
		Function openAIFunction `json:"function"`
	}
	type openAIRequestMessage struct {
		Role       protocol.MessageRole `json:"role"`
		Content    string               `json:"content"`
		Reasoning  string               `json:"reasoning_content,omitempty"`
		Name       string               `json:"name,omitempty"`
		ToolCallID string               `json:"tool_call_id,omitempty"`
		ToolCalls  []openAIToolCall     `json:"tool_calls,omitempty"`
	}
	tools := make([]openAITool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	messages := make([]openAIRequestMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		translated := openAIRequestMessage{
			Role:       message.Role,
			Content:    message.Content,
			Reasoning:  message.Reasoning,
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
		}
		for _, call := range message.ToolCalls {
			wireCall := openAIToolCall{ID: call.ID, Type: "function"}
			wireCall.Function.Name = call.Name
			wireCall.Function.Arguments = string(call.Arguments)
			translated.ToolCalls = append(translated.ToolCalls, wireCall)
		}
		messages = append(messages, translated)
	}
	payload := struct {
		Model       string                 `json:"model"`
		Messages    []openAIRequestMessage `json:"messages"`
		Tools       []openAITool           `json:"tools,omitempty"`
		MaxTokens   int                    `json:"max_tokens,omitempty"`
		Stream      bool                   `json:"stream"`
		StreamUsage map[string]bool        `json:"stream_options,omitempty"`
	}{
		Model:     request.Model,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: request.MaxOutputTokens,
		Stream:    request.Stream,
	}
	if request.Stream {
		payload.StreamUsage = map[string]bool{"include_usage": true}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("provider openai: encode request: %w", err)
	}
	return encoded, nil
}

// TranslateResponse converts an OpenAI Chat Completions response into O1.
func (OpenAIAdapter) TranslateResponse(raw json.RawMessage) (protocol.NormalizedGeneration, error) {
	var response openAIResponse
	if err := decodeSingleJSON(raw, &response); err != nil {
		return protocol.NormalizedGeneration{}, fmt.Errorf("provider openai: decode response: %w", err)
	}
	if len(response.Choices) == 0 {
		return protocol.NormalizedGeneration{}, fmt.Errorf("provider openai: response has no choices")
	}
	choice := response.Choices[0]
	calls, err := normalizeOpenAIToolCalls(choice.Message.ToolCalls)
	if err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	generation := protocol.NormalizedGeneration{
		Content:      choice.Message.Content,
		Reasoning:    choice.Message.Reasoning,
		ToolCalls:    calls,
		FinishReason: normalizeOpenAIFinish(choice.FinishReason),
		Usage: protocol.TokenUsage{
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
			TotalTokens:      response.Usage.TotalTokens,
			ReasoningTokens:  response.Usage.CompletionDetails.ReasoningTokens,
			CachedTokens:     response.Usage.PromptDetails.CachedTokens,
		},
		Model: response.Model,
	}
	if err := generation.Validate(); err != nil {
		return protocol.NormalizedGeneration{}, fmt.Errorf("provider openai: invalid response: %w", err)
	}
	return generation, nil
}

// TranslateStreamEvent converts one OpenAI SSE data event into O1.
func (OpenAIAdapter) TranslateStreamEvent(event []byte) (protocol.StreamChunk, error) {
	data := trimSSEData(event)
	if bytes.Equal(data, []byte("[DONE]")) {
		return protocol.StreamChunk{Done: true}, nil
	}
	var response openAIStreamResponse
	if err := decodeSingleJSON(data, &response); err != nil {
		return protocol.StreamChunk{}, fmt.Errorf("provider openai: decode stream event: %w", err)
	}
	chunk := protocol.StreamChunk{}
	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		chunk.ContentDelta = choice.Delta.Content
		chunk.ReasoningDelta = choice.Delta.Reasoning
		chunk.FinishReason = normalizeOpenAIFinishOptional(choice.FinishReason)
		for _, source := range choice.Delta.ToolCalls {
			chunk.ToolCallDeltas = append(
				chunk.ToolCallDeltas,
				protocol.StreamToolCallDelta{
					Index: source.Index, ID: source.ID,
					Name:      source.Function.Name,
					Arguments: source.Function.Arguments,
				},
			)
			arguments := json.RawMessage(source.Function.Arguments)
			if len(bytes.TrimSpace(arguments)) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			complete := protocol.NormalizedToolCall{
				ID: source.ID, Name: source.Function.Name, Arguments: arguments,
			}
			if complete.Validate() == nil {
				chunk.ToolCall = &complete
			}
		}
	}
	if response.Usage != nil {
		chunk.Usage = &protocol.TokenUsage{
			PromptTokens:     response.Usage.PromptTokens,
			CompletionTokens: response.Usage.CompletionTokens,
			TotalTokens:      response.Usage.TotalTokens,
			ReasoningTokens:  response.Usage.CompletionDetails.ReasoningTokens,
			CachedTokens:     response.Usage.PromptDetails.CachedTokens,
		}
	}
	return chunk, nil
}

type openAIResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			Reasoning string           `json:"reasoning_content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

type openAIStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content   string           `json:"content"`
			Reasoning string           `json:"reasoning_content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

type openAIToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIUsage struct {
	PromptTokens      int `json:"prompt_tokens"`
	CompletionTokens  int `json:"completion_tokens"`
	TotalTokens       int `json:"total_tokens"`
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func normalizeOpenAIToolCalls(raw []openAIToolCall) ([]protocol.NormalizedToolCall, error) {
	calls := make([]protocol.NormalizedToolCall, 0, len(raw))
	for _, source := range raw {
		arguments := json.RawMessage(source.Function.Arguments)
		if len(bytes.TrimSpace(arguments)) == 0 {
			arguments = json.RawMessage(`{}`)
		}
		call := protocol.NormalizedToolCall{
			ID:        source.ID,
			Name:      source.Function.Name,
			Arguments: arguments,
		}
		if err := call.Validate(); err != nil {
			return nil, fmt.Errorf("provider openai: invalid tool call: %w", err)
		}
		calls = append(calls, call)
	}
	return calls, nil
}

func normalizeOpenAIFinish(reason string) string {
	switch reason {
	case "stop":
		return protocol.FinishStop
	case "tool_calls", "function_call":
		return protocol.FinishToolCalls
	case "length":
		return protocol.FinishLength
	default:
		return protocol.FinishError
	}
}

func normalizeOpenAIFinishOptional(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return ""
	}
	return normalizeOpenAIFinish(reason)
}

func trimSSEData(event []byte) []byte {
	trimmed := bytes.TrimSpace(event)
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		trimmed = bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	}
	return trimmed
}

func decodeSingleJSON[T any](raw []byte, target *T) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
