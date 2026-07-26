package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestOpenAIAdapterRoundTripTranslation(t *testing.T) {
	t.Parallel()
	adapter := OpenAIAdapter{}
	request := adapterRequest()
	request.Stream = true
	request.Messages = append(request.Messages, protocol.Message{
		Role:      protocol.RoleAssistant,
		Content:   "Calling.",
		Reasoning: "Need a tool.",
		ToolCalls: []protocol.NormalizedToolCall{{
			ID:        "call-1",
			Name:      "weather",
			Arguments: json.RawMessage(`{"city":"Berlin"}`),
		}},
	})
	rawRequest, err := adapter.TranslateRequest(request)
	if err != nil {
		t.Fatalf("TranslateRequest() error = %v", err)
	}
	var encoded struct {
		Model         string `json:"model"`
		MaxTokens     int    `json:"max_tokens"`
		Stream        bool   `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Reasoning string `json:"reasoning_content"`
			ToolCalls []struct {
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rawRequest, &encoded); err != nil {
		t.Fatalf("Unmarshal(request) error = %v", err)
	}
	if encoded.Model != "test-model" || encoded.MaxTokens != 256 ||
		!encoded.Stream || !encoded.StreamOptions.IncludeUsage ||
		len(encoded.Tools) != 1 || encoded.Tools[0].Type != "function" ||
		encoded.Tools[0].Function.Name != "weather" ||
		len(encoded.Messages[1].ToolCalls) != 1 ||
		encoded.Messages[1].Reasoning != "Need a tool." ||
		encoded.Messages[1].ToolCalls[0].Type != "function" ||
		encoded.Messages[1].ToolCalls[0].Function.Name != "weather" ||
		encoded.Messages[1].ToolCalls[0].Function.Arguments != `{"city":"Berlin"}` {
		t.Fatalf("translated request = %+v", encoded)
	}

	rawResponse := json.RawMessage(`{
		"id":"chatcmpl-1",
		"model":"gpt-test",
		"choices":[{
			"message":{
				"content":"Checking.",
				"reasoning_content":"Need a tool.",
				"tool_calls":[{
					"id":"call-1",
					"type":"function",
					"function":{"name":"weather","arguments":"{\"city\":\"Berlin\"}"}
				}]
			},
			"finish_reason":"tool_calls"
		}],
		"usage":{
			"prompt_tokens":10,
			"completion_tokens":5,
			"total_tokens":15,
			"completion_tokens_details":{"reasoning_tokens":2},
			"prompt_tokens_details":{"cached_tokens":3}
		}
	}`)
	generation, err := adapter.TranslateResponse(rawResponse)
	if err != nil {
		t.Fatalf("TranslateResponse() error = %v", err)
	}
	if generation.Content != "Checking." || generation.Reasoning != "Need a tool." ||
		generation.Model != "gpt-test" || generation.FinishReason != protocol.FinishToolCalls ||
		len(generation.ToolCalls) != 1 ||
		string(generation.ToolCalls[0].Arguments) != `{"city":"Berlin"}` ||
		generation.Usage.ReasoningTokens != 2 || generation.Usage.CachedTokens != 3 {
		t.Fatalf("generation = %+v", generation)
	}
}

func TestOpenAIStreamTranslation(t *testing.T) {
	t.Parallel()
	adapter := OpenAIAdapter{}
	event := []byte(`data: {"choices":[{"delta":{"content":"Hi","reasoning_content":"R","tool_calls":[{"id":"call","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	chunk, err := adapter.TranslateStreamEvent(event)
	if err != nil {
		t.Fatalf("TranslateStreamEvent() error = %v", err)
	}
	if chunk.ContentDelta != "Hi" || chunk.ReasoningDelta != "R" ||
		chunk.FinishReason != protocol.FinishToolCalls || chunk.ToolCall == nil ||
		chunk.ToolCall.Name != "echo" || chunk.Usage == nil ||
		chunk.Usage.TotalTokens != 3 {
		t.Fatalf("chunk = %+v", chunk)
	}
	done, err := adapter.TranslateStreamEvent([]byte("data: [DONE]\n\n"))
	if err != nil || !done.Done {
		t.Fatalf("done = %+v, error = %v", done, err)
	}
}

func TestOpenAIAdapterRejectsInvalidWireData(t *testing.T) {
	t.Parallel()
	adapter := OpenAIAdapter{}
	invalidRequest := adapterRequest()
	invalidRequest.Model = ""
	if _, err := adapter.TranslateRequest(invalidRequest); err == nil {
		t.Fatal("invalid request accepted")
	}
	for _, raw := range []string{
		`{`,
		`{"choices":[]}`,
		`{"choices":[{"message":{"tool_calls":[{"id":"call","function":{"name":"x","arguments":"[]"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[{"message":{},"finish_reason":"stop"}]} {}`,
	} {
		if _, err := adapter.TranslateResponse(json.RawMessage(raw)); err == nil {
			t.Fatalf("TranslateResponse(%q) succeeded", raw)
		}
	}
	if _, err := adapter.TranslateStreamEvent([]byte(`data: {`)); err == nil {
		t.Fatal("invalid stream event accepted")
	}
}

func TestOpenAIFinishNormalization(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"stop":           protocol.FinishStop,
		"tool_calls":     protocol.FinishToolCalls,
		"function_call":  protocol.FinishToolCalls,
		"length":         protocol.FinishLength,
		"content_filter": protocol.FinishError,
	}
	for input, expected := range tests {
		if actual := normalizeOpenAIFinish(input); actual != expected {
			t.Fatalf("normalizeOpenAIFinish(%q) = %q, want %q", input, actual, expected)
		}
	}
	if normalizeOpenAIFinishOptional(" ") != "" {
		t.Fatal("empty optional finish reason was normalized")
	}
	if !strings.Contains(string(trimSSEData([]byte(" data: {} "))), "{}") {
		t.Fatal("SSE data prefix not removed")
	}
}

func adapterRequest() protocol.GenerationRequest {
	return protocol.GenerationRequest{
		Model: "test-model",
		Messages: []protocol.Message{{
			Role:    protocol.RoleUser,
			Content: "What is the weather?",
		}},
		Tools: []protocol.ToolDefinition{{
			Name:        "weather",
			Description: "Get weather.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		MaxOutputTokens: 256,
	}
}
