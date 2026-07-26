package provider

import (
	"encoding/json"
	"testing"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestAnthropicAdapterRoundTripTranslation(t *testing.T) {
	t.Parallel()
	adapter := AnthropicAdapter{}
	request := adapterRequest()
	request.Messages = []protocol.Message{
		{Role: protocol.RoleSystem, Content: "First."},
		{Role: protocol.RoleSystem, Content: "Second."},
		{Role: protocol.RoleUser, Content: "Run it."},
		{
			Role:    protocol.RoleAssistant,
			Content: "Calling.",
			ToolCalls: []protocol.NormalizedToolCall{{
				ID:        "call-1",
				Name:      "weather",
				Arguments: json.RawMessage(`{"city":"Berlin"}`),
			}},
		},
		{Role: protocol.RoleTool, ToolCallID: "call-1", Content: `{"temp":20}`},
	}
	rawRequest, err := adapter.TranslateRequest(request)
	if err != nil {
		t.Fatalf("TranslateRequest() error = %v", err)
	}
	var encoded struct {
		System    string `json:"system"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rawRequest, &encoded); err != nil {
		t.Fatalf("Unmarshal(request) error = %v", err)
	}
	if encoded.System != "First.\n\nSecond." || encoded.MaxTokens != 256 ||
		len(encoded.Messages) != 3 ||
		encoded.Messages[1].Content[1].Type != "tool_use" ||
		encoded.Messages[2].Content[0].Type != "tool_result" {
		t.Fatalf("translated request = %+v", encoded)
	}

	rawResponse := json.RawMessage(`{
		"id":"msg-1",
		"model":"claude-test",
		"content":[
			{"type":"thinking","thinking":"Need data."},
			{"type":"text","text":"Checking."},
			{"type":"tool_use","id":"call-1","name":"weather","input":{"city":"Berlin"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":9,"output_tokens":4,"cache_read_input_tokens":2}
	}`)
	generation, err := adapter.TranslateResponse(rawResponse)
	if err != nil {
		t.Fatalf("TranslateResponse() error = %v", err)
	}
	if generation.Content != "Checking." || generation.Reasoning != "Need data." ||
		generation.Model != "claude-test" ||
		generation.FinishReason != protocol.FinishToolCalls ||
		len(generation.ToolCalls) != 1 ||
		string(generation.ToolCalls[0].Arguments) != `{"city":"Berlin"}` ||
		generation.Usage.TotalTokens != 13 || generation.Usage.CachedTokens != 2 {
		t.Fatalf("generation = %+v", generation)
	}
}

func TestAnthropicStreamTranslation(t *testing.T) {
	t.Parallel()
	adapter := AnthropicAdapter{}
	start, err := adapter.TranslateStreamEvent([]byte(
		`data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"call","name":"echo","input":{}}}`,
	))
	if err != nil || start.ToolCall == nil || start.ToolCall.Name != "echo" {
		t.Fatalf("start = %+v, error = %v", start, err)
	}
	delta, err := adapter.TranslateStreamEvent([]byte(
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello","thinking":"why"}}`,
	))
	if err != nil || delta.ContentDelta != "hello" || delta.ReasoningDelta != "why" {
		t.Fatalf("delta = %+v, error = %v", delta, err)
	}
	finished, err := adapter.TranslateStreamEvent([]byte(
		`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":7}}`,
	))
	if err != nil || finished.FinishReason != protocol.FinishLength ||
		finished.Usage == nil || finished.Usage.TotalTokens != 7 {
		t.Fatalf("finished = %+v, error = %v", finished, err)
	}
	done, err := adapter.TranslateStreamEvent([]byte(`{"type":"message_stop"}`))
	if err != nil || !done.Done {
		t.Fatalf("done = %+v, error = %v", done, err)
	}
}

func TestAnthropicAdapterValidationAndFinishReasons(t *testing.T) {
	t.Parallel()
	adapter := AnthropicAdapter{}
	request := adapterRequest()
	request.MaxOutputTokens = 0
	raw, err := adapter.TranslateRequest(request)
	if err != nil {
		t.Fatalf("TranslateRequest() error = %v", err)
	}
	if !containsJSONNumber(raw, "max_tokens", 4096) {
		t.Fatalf("default max_tokens missing from %s", raw)
	}
	request.Model = ""
	if _, err := adapter.TranslateRequest(request); err == nil {
		t.Fatal("invalid request accepted")
	}
	for _, rawResponse := range []string{
		`{`,
		`{"content":[{"type":"tool_use","id":"","name":"x","input":{}}],"stop_reason":"tool_use"}`,
	} {
		if _, err := adapter.TranslateResponse(json.RawMessage(rawResponse)); err == nil {
			t.Fatalf("TranslateResponse(%q) succeeded", rawResponse)
		}
	}
	if _, err := adapter.TranslateStreamEvent([]byte(`{`)); err == nil {
		t.Fatal("invalid stream event accepted")
	}
	expected := map[string]string{
		"end_turn":      protocol.FinishStop,
		"stop_sequence": protocol.FinishStop,
		"pause_turn":    protocol.FinishStop,
		"tool_use":      protocol.FinishToolCalls,
		"max_tokens":    protocol.FinishLength,
		"refusal":       protocol.FinishError,
	}
	for input, want := range expected {
		if got := normalizeAnthropicFinish(input); got != want {
			t.Fatalf("normalizeAnthropicFinish(%q) = %q, want %q", input, got, want)
		}
	}
}

func containsJSONNumber(raw json.RawMessage, key string, expected int) bool {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	var actual int
	return json.Unmarshal(values[key], &actual) == nil && actual == expected
}
