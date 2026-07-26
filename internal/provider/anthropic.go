package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// AnthropicAdapter translates Anthropic Messages API requests and responses.
type AnthropicAdapter struct{}

// Name returns the stable provider identifier.
func (AnthropicAdapter) Name() string {
	return "anthropic"
}

// TranslateRequest converts an O1 request into the Anthropic wire format.
func (AnthropicAdapter) TranslateRequest(request protocol.GenerationRequest) (json.RawMessage, error) {
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("provider anthropic: invalid request: %w", err)
	}
	system, messages := anthropicMessages(request.Messages)
	tools := make([]anthropicTool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
		})
	}
	maxTokens := request.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	payload := struct {
		Model     string             `json:"model"`
		System    string             `json:"system,omitempty"`
		Messages  []anthropicMessage `json:"messages"`
		Tools     []anthropicTool    `json:"tools,omitempty"`
		MaxTokens int                `json:"max_tokens"`
		Stream    bool               `json:"stream"`
	}{
		Model:     request.Model,
		System:    system,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: maxTokens,
		Stream:    request.Stream,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("provider anthropic: encode request: %w", err)
	}
	return encoded, nil
}

// TranslateResponse converts an Anthropic Messages response into O1.
func (AnthropicAdapter) TranslateResponse(raw json.RawMessage) (protocol.NormalizedGeneration, error) {
	var response anthropicResponse
	if err := decodeSingleJSON(raw, &response); err != nil {
		return protocol.NormalizedGeneration{}, fmt.Errorf("provider anthropic: decode response: %w", err)
	}
	var content strings.Builder
	var reasoning strings.Builder
	calls := make([]protocol.NormalizedToolCall, 0)
	for _, block := range response.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		case "tool_use":
			call := protocol.NormalizedToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			}
			if err := call.Validate(); err != nil {
				return protocol.NormalizedGeneration{}, fmt.Errorf(
					"provider anthropic: invalid tool call: %w",
					err,
				)
			}
			calls = append(calls, call)
		}
	}
	total := response.Usage.InputTokens + response.Usage.OutputTokens
	generation := protocol.NormalizedGeneration{
		Content:      content.String(),
		Reasoning:    reasoning.String(),
		ToolCalls:    calls,
		FinishReason: normalizeAnthropicFinish(response.StopReason),
		Usage: protocol.TokenUsage{
			PromptTokens:     response.Usage.InputTokens,
			CompletionTokens: response.Usage.OutputTokens,
			TotalTokens:      total,
			CachedTokens:     response.Usage.CacheReadInputTokens,
		},
		Model: response.Model,
	}
	if err := generation.Validate(); err != nil {
		return protocol.NormalizedGeneration{}, fmt.Errorf("provider anthropic: invalid response: %w", err)
	}
	return generation, nil
}

// TranslateStreamEvent converts one Anthropic SSE data event into O1.
func (AnthropicAdapter) TranslateStreamEvent(event []byte) (protocol.StreamChunk, error) {
	data := trimSSEData(event)
	var response anthropicStreamEvent
	if err := decodeSingleJSON(data, &response); err != nil {
		return protocol.StreamChunk{}, fmt.Errorf("provider anthropic: decode stream event: %w", err)
	}
	chunk := protocol.StreamChunk{}
	switch response.Type {
	case "message_start":
		if response.Message.Usage.InputTokens > 0 {
			chunk.Usage = &protocol.TokenUsage{
				PromptTokens: response.Message.Usage.InputTokens,
				TotalTokens:  response.Message.Usage.InputTokens,
				CachedTokens: response.Message.Usage.CacheReadInputTokens,
			}
		}
	case "content_block_start":
		if response.ContentBlock.Type == "tool_use" {
			arguments := ""
			if input := bytes.TrimSpace(response.ContentBlock.Input); len(input) > 0 &&
				!bytes.Equal(input, []byte(`{}`)) {
				arguments = string(input)
			}
			chunk.ToolCallDeltas = []protocol.StreamToolCallDelta{{
				Index: response.Index, ID: response.ContentBlock.ID,
				Name: response.ContentBlock.Name, Arguments: arguments,
			}}
			input := response.ContentBlock.Input
			if len(bytes.TrimSpace(input)) == 0 {
				input = json.RawMessage(`{}`)
			}
			complete := protocol.NormalizedToolCall{
				ID: response.ContentBlock.ID, Name: response.ContentBlock.Name,
				Arguments: input,
			}
			if complete.Validate() == nil {
				chunk.ToolCall = &complete
			}
		}
	case "content_block_delta":
		chunk.ContentDelta = response.Delta.Text
		chunk.ReasoningDelta = response.Delta.Thinking
		if response.Delta.PartialJSON != "" {
			chunk.ToolCallDeltas = []protocol.StreamToolCallDelta{{
				Index: response.Index, Arguments: response.Delta.PartialJSON,
			}}
		}
	case "message_delta":
		chunk.FinishReason = normalizeAnthropicFinish(response.Delta.StopReason)
		if response.Usage.OutputTokens > 0 {
			chunk.Usage = &protocol.TokenUsage{
				CompletionTokens: response.Usage.OutputTokens,
				TotalTokens:      response.Usage.OutputTokens,
			}
		}
	case "message_stop":
		chunk.Done = true
	}
	return chunk, nil
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicRequestBlock `json:"content"`
}

type anthropicRequestBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthropicResponse struct {
	Model      string                   `json:"model"`
	Content    []anthropicResponseBlock `json:"content"`
	StopReason string                   `json:"stop_reason"`
	Usage      anthropicUsage           `json:"usage"`
}

type anthropicResponseBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
}

type anthropicUsage struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
}

type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index"`
	Message      anthropicResponse      `json:"message"`
	ContentBlock anthropicResponseBlock `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
}

func anthropicMessages(source []protocol.Message) (string, []anthropicMessage) {
	systemParts := make([]string, 0)
	messages := make([]anthropicMessage, 0, len(source))
	for _, message := range source {
		if message.Role == protocol.RoleSystem {
			systemParts = append(systemParts, message.Content)
			continue
		}
		if message.Role == protocol.RoleTool {
			messages = append(messages, anthropicMessage{
				Role: "user",
				Content: []anthropicRequestBlock{{
					Type:      "tool_result",
					ToolUseID: message.ToolCallID,
					Content:   message.Content,
				}},
			})
			continue
		}
		role := string(message.Role)
		blocks := make([]anthropicRequestBlock, 0, len(message.ToolCalls)+1)
		if message.Content != "" {
			blocks = append(blocks, anthropicRequestBlock{
				Type: "text",
				Text: message.Content,
			})
		}
		for _, call := range message.ToolCalls {
			blocks = append(blocks, anthropicRequestBlock{
				Type:  "tool_use",
				ID:    call.ID,
				Name:  call.Name,
				Input: call.Arguments,
			})
		}
		messages = append(messages, anthropicMessage{Role: role, Content: blocks})
	}
	return strings.Join(systemParts, "\n\n"), messages
}

func normalizeAnthropicFinish(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence", "pause_turn":
		return protocol.FinishStop
	case "tool_use":
		return protocol.FinishToolCalls
	case "max_tokens":
		return protocol.FinishLength
	default:
		return protocol.FinishError
	}
}
