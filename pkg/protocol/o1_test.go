package protocol

import (
	"encoding/json"
	"testing"
)

func TestGenerationRequestValidation(t *testing.T) {
	t.Parallel()
	valid := GenerationRequest{
		Model: "model",
		Messages: []Message{{
			Role:    RoleUser,
			Content: "hello",
		}},
		Tools: []ToolDefinition{{
			Name:        "echo",
			Description: "Echo input.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*GenerationRequest)
	}{
		{name: "model", mutate: func(request *GenerationRequest) { request.Model = "" }},
		{name: "messages", mutate: func(request *GenerationRequest) { request.Messages = nil }},
		{name: "role", mutate: func(request *GenerationRequest) {
			request.Messages[0].Role = MessageRole("invalid")
		}},
		{name: "tool message ID", mutate: func(request *GenerationRequest) {
			request.Messages[0].Role = RoleTool
		}},
		{name: "tool name", mutate: func(request *GenerationRequest) {
			request.Tools[0].Name = ""
		}},
		{name: "tool schema", mutate: func(request *GenerationRequest) {
			request.Tools[0].Parameters = json.RawMessage(`[]`)
		}},
		{name: "tokens", mutate: func(request *GenerationRequest) {
			request.MaxOutputTokens = -1
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := valid
			request.Messages = append([]Message(nil), valid.Messages...)
			request.Tools = append([]ToolDefinition(nil), valid.Tools...)
			test.mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestNormalizedGenerationValidation(t *testing.T) {
	t.Parallel()
	valid := NormalizedGeneration{
		FinishReason: FinishToolCalls,
		ToolCalls: []NormalizedToolCall{{
			ID:        "call-1",
			Name:      "echo",
			Arguments: json.RawMessage(`{"value":"hello"}`),
		}},
		Usage: TokenUsage{
			PromptTokens:     2,
			CompletionTokens: 3,
			TotalTokens:      5,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	invalidFinish := valid
	invalidFinish.FinishReason = "unknown"
	if err := invalidFinish.Validate(); err == nil {
		t.Fatal("invalid finish reason accepted")
	}
	invalidCall := valid
	invalidCall.ToolCalls = []NormalizedToolCall{{
		ID:        "",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	}}
	if err := invalidCall.Validate(); err == nil {
		t.Fatal("invalid tool call accepted")
	}
	invalidUsage := valid
	invalidUsage.Usage.TotalTokens = 1
	if err := invalidUsage.Validate(); err == nil {
		t.Fatal("inconsistent usage accepted")
	}
}

func TestToolCallAndUsageValidation(t *testing.T) {
	t.Parallel()
	for _, call := range []NormalizedToolCall{
		{Name: "echo", Arguments: json.RawMessage(`{}`)},
		{ID: "call", Arguments: json.RawMessage(`{}`)},
		{ID: "call", Name: "echo", Arguments: json.RawMessage(`null`)},
		{ID: "call", Name: "echo", Arguments: json.RawMessage(`{`)},
	} {
		if err := call.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded", call)
		}
	}
	if err := (TokenUsage{PromptTokens: -1}).Validate(); err == nil {
		t.Fatal("negative usage accepted")
	}
	if err := (TokenUsage{}).Validate(); err != nil {
		t.Fatalf("zero usage error = %v", err)
	}
}

func TestMessageRoles(t *testing.T) {
	t.Parallel()
	for _, role := range []MessageRole{RoleSystem, RoleUser, RoleAssistant, RoleTool} {
		if !role.Valid() {
			t.Fatalf("%q is invalid", role)
		}
	}
	if MessageRole("").Valid() {
		t.Fatal("empty role is valid")
	}
}
