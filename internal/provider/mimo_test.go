package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestMiMoAdapterNormalizesContentAndReasoningToolDialects(t *testing.T) {
	t.Parallel()
	adapter := &MiMoAdapter{}
	raw, err := adapter.TranslateRequest(protocol.GenerationRequest{
		Model: "mimo-v2.5-pro", Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "write"}},
		Tools: []protocol.ToolDefinition{{Name: "filesystem_write", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request["temperature"] != 0.3 || request["top_p"] != 0.95 {
		t.Fatalf("MiMo agentic sampling = %+v", request)
	}
	generation, err := adapter.TranslateResponse(json.RawMessage(`{
		"model":"mimo-v2.5-pro","choices":[{"message":{
			"content":"I will write it.\n<tool_call>\n<function=filesystem_write>\n<parameter=path>src/app/api/incidents/[id]/route.ts</parameter>\n<parameter=content>export const ok = true</parameter>\n<parameter=expect>the file is written</parameter>\n</function>\n</tool_call>",
			"reasoning_content":""
		},"finish_reason":"stop"}],"usage":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if generation.Content != "I will write it." || len(generation.ToolCalls) != 1 ||
		generation.ToolCalls[0].Name != "filesystem_write" ||
		generation.FinishReason != protocol.FinishToolCalls {
		t.Fatalf("normalized content call = %+v", generation)
	}
	var arguments map[string]any
	if err := json.Unmarshal(generation.ToolCalls[0].Arguments, &arguments); err != nil {
		t.Fatal(err)
	}
	if arguments["path"] != "src/app/api/incidents/[id]/route.ts" ||
		arguments["content"] != "export const ok = true" {
		t.Fatalf("normalized arguments = %+v", arguments)
	}

	reasoning, err := adapter.FinalizeGeneration(protocol.NormalizedGeneration{
		Reasoning:    `<tool_call><function=filesystem_list><parameter=path>.</parameter></function></tool_call>`,
		FinishReason: protocol.FinishStop,
	})
	if err != nil || reasoning.Reasoning != "" || len(reasoning.ToolCalls) != 1 ||
		reasoning.ToolCalls[0].Name != "filesystem_list" {
		t.Fatalf("normalized reasoning call = %+v, %v", reasoning, err)
	}
	matching, err := adapter.FinalizeGeneration(protocol.NormalizedGeneration{
		Content:   `<tool_call><function=echo><parameter=a>1</parameter><parameter=b>true</parameter></function></tool_call>`,
		ToolCalls: []protocol.NormalizedToolCall{{ID: "native", Name: "echo", Arguments: json.RawMessage(`{"b":true,"a":1}`)}},
	})
	if err != nil || len(matching.ToolCalls) != 1 || matching.ToolCalls[0].ID != "native" {
		t.Fatalf("semantically matching native/textual calls = %+v, %v", matching, err)
	}
}

func TestMiMoGeneratorPreflightsMultiTurnAndBuffersTools(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["temperature"] != 0.3 {
			t.Errorf("request temperature = %v", body["temperature"])
		}
		if body["stream"] != false {
			t.Errorf("buffered MiMo request stream = %v", body["stream"])
		}
		writer.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(writer, `{"model":"mimo","choices":[{"message":{"content":"<tool_call><function=ion_capability_echo><parameter=value>READY</parameter><parameter=expect>echo succeeds</parameter></function></tool_call>","reasoning_content":"probe"},"finish_reason":"stop"}],"usage":{}}`)
		case 2:
			fmt.Fprint(writer, `{"model":"mimo","choices":[{"message":{"content":"READY","reasoning_content":""},"finish_reason":"stop"}],"usage":{}}`)
		default:
			fmt.Fprint(writer, `{"model":"mimo","choices":[{"message":{"content":"finished","reasoning_content":""},"finish_reason":"stop"}],"usage":{}}`)
		}
	}))
	defer server.Close()
	adapter := &MiMoAdapter{}
	pool, err := NewPool([]Endpoint{{
		Name: "Xiaomi", URL: server.URL, Model: "mimo-v2.5-pro", Adapter: adapter,
		Credentials:    []Credential{{ID: "test", Secret: "secret"}},
		Authentication: BearerAuthentication(), Client: server.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := NewMiMoGenerator(pool, adapter)
	if err != nil {
		t.Fatal(err)
	}
	var delivered []protocol.StreamChunk
	result, err := generator.GenerateStream(context.Background(), protocol.GenerationRequest{
		Model: "mimo-v2.5-pro", Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "finish"}},
		Tools:  []protocol.ToolDefinition{{Name: "filesystem_list", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Stream: true,
	}, func(chunk protocol.StreamChunk) error {
		delivered = append(delivered, chunk)
		return nil
	})
	if err != nil || result.Content != "finished" {
		t.Fatalf("actual generation = %+v, %v", result, err)
	}
	if len(delivered) != 1 || delivered[0].ContentDelta != "finished" {
		t.Fatalf("buffered delivery = %+v", delivered)
	}
	status := generator.CapabilityStatus()
	if !status.Checked || status.Native || !status.MultiTurn || status.Mode != "textual-buffered" || calls.Load() != 3 {
		t.Fatalf("capability status = %+v calls=%d", status, calls.Load())
	}
}

func TestMiMoGeneratorQuarantinesNoToolMarkupBeforeStreaming(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["stream"] != false {
			t.Errorf("quarantined MiMo request stream = %v", body["stream"])
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"model":"mimo","choices":[{"message":{"content":"<tool_call><function=filesystem_write><parameter=path>leak.txt</parameter></function></tool_call>","reasoning_content":""},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer server.Close()
	adapter := &MiMoAdapter{}
	pool, err := NewPool([]Endpoint{{
		Name: "Xiaomi", URL: server.URL, Model: "mimo-v2.5-pro", Adapter: adapter,
		Credentials:    []Credential{{ID: "test", Secret: "secret"}},
		Authentication: BearerAuthentication(), Client: server.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := NewMiMoGenerator(pool, adapter)
	if err != nil {
		t.Fatal(err)
	}
	deliveries := 0
	_, err = generator.GenerateStream(context.Background(), protocol.GenerationRequest{
		Model: "mimo-v2.5-pro", Stream: true,
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "Revise without tools"}},
	}, func(chunk protocol.StreamChunk) error {
		deliveries++
		return nil
	})
	if !errors.Is(err, ErrToolProtocol) || deliveries != 0 {
		t.Fatalf("no-tool quarantine err=%v deliveries=%d", err, deliveries)
	}
}

func TestMiMoNoToolRevisionFallbackDoesNotPoisonToolStrategy(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		requests++
		var body struct {
			Messages []protocol.Message `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			fmt.Fprint(writer, `{"model":"mimo","choices":[{"message":{"content":"<tool_call><function=filesystem_write><parameter=path>leak.txt</parameter></function></tool_call>","reasoning_content":""},"finish_reason":"stop"}],"usage":{}}`)
			return
		}
		foundCompatibilityPrompt := false
		for _, message := range body.Messages {
			if message.Content == noToolRevisionCompatibilityPrompt {
				foundCompatibilityPrompt = true
				break
			}
		}
		if !foundCompatibilityPrompt {
			t.Error("tools-disabled compatibility prompt is missing")
		}
		fmt.Fprint(writer, `{"model":"mimo","choices":[{"message":{"content":"Use the verified search results to prepare the requested report.","reasoning_content":""},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer server.Close()
	adapter := &MiMoAdapter{}
	pool, err := NewPool([]Endpoint{{
		Name: "Xiaomi", URL: server.URL, Model: "mimo-v2.5-pro", Adapter: adapter,
		Credentials:    []Credential{{ID: "test", Secret: "secret"}},
		Authentication: BearerAuthentication(), Client: server.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := NewMiMoGenerator(pool, adapter)
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.GenerationRequest{
		Model:    "mimo-v2.5-pro",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "Revise without tools"}},
	}
	_, err = generator.Generate(context.Background(), request)
	if !errors.Is(err, ErrToolProtocol) {
		t.Fatalf("first no-tool generation error = %v", err)
	}
	if !generator.AdvanceGenerationStrategy(err.Error()) {
		t.Fatal("no-tool protocol error did not select bounded fallback")
	}
	revised, err := generator.Generate(context.Background(), request)
	if err != nil ||
		revised.Content != "Use the verified search results to prepare the requested report." ||
		len(revised.ToolCalls) != 0 {
		t.Fatalf("quarantined revision fallback = %+v, %v", revised, err)
	}
	status := generator.CapabilityStatus()
	if status.Strategy != 0 || status.Mode != "" || status.Reason != "" {
		t.Fatalf("no-tool recovery poisoned tool capability = %+v", status)
	}
}

func TestMiMoNoToolRevisionFallbackNeverStreamsInternalRecoveryCopy(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"model":"mimo","choices":[{"message":{"content":"<tool_call><function=web_search><parameter=query>Perplexity features</parameter></function></tool_call>","reasoning_content":""},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer server.Close()
	adapter := &MiMoAdapter{}
	pool, err := NewPool([]Endpoint{{
		Name: "Xiaomi", URL: server.URL, Model: "mimo-v2.5-pro", Adapter: adapter,
		Credentials:    []Credential{{ID: "test", Secret: "secret"}},
		Authentication: BearerAuthentication(), Client: server.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := NewMiMoGenerator(pool, adapter)
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.GenerationRequest{
		Model:    "mimo-v2.5-pro",
		Messages: []protocol.Message{{Role: protocol.RoleUser, Content: "Prepare a report"}},
	}
	_, err = generator.Generate(context.Background(), request)
	if !errors.Is(err, ErrToolProtocol) ||
		!generator.AdvanceGenerationStrategy(err.Error()) {
		t.Fatalf("first no-tool recovery err=%v", err)
	}
	deliveries := 0
	_, err = generator.GenerateStream(
		context.Background(),
		request,
		func(protocol.StreamChunk) error {
			deliveries++
			return nil
		},
	)
	if !errors.Is(err, ErrToolProtocol) || deliveries != 0 {
		t.Fatalf("fallback leaked deliveries=%d err=%v", deliveries, err)
	}
	if generator.AdvanceGenerationStrategy(err.Error()) {
		t.Fatal("exhausted no-tool recovery unexpectedly retried")
	}
}
