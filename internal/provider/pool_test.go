package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/ssrf"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestPoolStreamsContentReasoningAndFragmentedToolCalls(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream {
			t.Error("streaming request was not enabled")
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Check \",\"content\":\"Hel\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"done.\",\"content\":\"lo\",\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()
	pool, err := NewPool([]Endpoint{{
		Name: "stream", URL: server.URL, Model: "stream-model",
		Adapter: OpenAIAdapter{}, Authentication: BearerAuthentication(),
		Credentials: []Credential{{ID: "key", Secret: "secret"}},
		Client:      server.Client(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []protocol.StreamChunk
	generation, err := pool.GenerateStream(
		context.Background(), adapterRequest(),
		func(chunk protocol.StreamChunk) error {
			chunks = append(chunks, chunk)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || generation.Content != "Hello" ||
		generation.Reasoning != "Check done." ||
		generation.FinishReason != protocol.FinishToolCalls ||
		len(generation.ToolCalls) != 1 ||
		string(generation.ToolCalls[0].Arguments) != `{"value":1}` ||
		generation.Usage.TotalTokens != 5 {
		t.Fatalf("chunks=%+v generation=%+v", chunks, generation)
	}
}

func TestPoolRotatesCredentialsOn429(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var credentials []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		credential := request.Header.Get("Authorization")
		mu.Lock()
		credentials = append(credentials, credential)
		mu.Unlock()
		if credential == "Bearer rate-limited" {
			writer.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeOpenAISuccess(t, writer, "rotated-model")
	}))
	defer server.Close()

	pool, err := NewPool([]Endpoint{{
		Name:           "primary",
		URL:            server.URL,
		Model:          "configured-model",
		Adapter:        OpenAIAdapter{},
		Authentication: BearerAuthentication(),
		Client:         server.Client(),
		Credentials: []Credential{
			{ID: "first", Secret: "rate-limited"},
			{ID: "second", Secret: "working"},
		},
	}})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	generation, err := pool.Generate(context.Background(), adapterRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if generation.Provider != "primary" || generation.Model != "rotated-model" {
		t.Fatalf("generation = %+v", generation)
	}
	mu.Lock()
	gotCredentials := append([]string(nil), credentials...)
	mu.Unlock()
	if len(gotCredentials) != 2 || gotCredentials[0] != "Bearer rate-limited" ||
		gotCredentials[1] != "Bearer working" {
		t.Fatalf("credentials = %q", gotCredentials)
	}
	usage := pool.Usage()
	if len(usage) != 2 || usage[0].RateLimits != 1 || usage[0].Failures != 1 ||
		usage[1].Requests != 1 || usage[1].TotalTokens != 5 {
		t.Fatalf("usage = %+v", usage)
	}

	if _, err := pool.Generate(context.Background(), adapterRequest()); err != nil {
		t.Fatalf("second Generate() error = %v", err)
	}
	mu.Lock()
	lastCredential := credentials[len(credentials)-1]
	mu.Unlock()
	if lastCredential != "Bearer working" {
		t.Fatalf("successful credential did not remain active: %q", lastCredential)
	}
}

func TestPoolFallsBackAcrossProviders(t *testing.T) {
	t.Parallel()
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("x-api-key") != "anthropic-secret" ||
			request.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("Anthropic headers = %v", request.Header)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"model":"claude-fallback",
			"content":[{"type":"text","text":"fallback worked"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":2,"output_tokens":2}
		}`)
	}))
	defer secondary.Close()

	pool, err := NewPool([]Endpoint{
		{
			Name:           "openai-primary",
			URL:            primary.URL,
			Model:          "gpt-primary",
			Adapter:        OpenAIAdapter{},
			Authentication: BearerAuthentication(),
			Credentials:    []Credential{{ID: "openai", Secret: "secret"}},
			Client:         primary.Client(),
		},
		{
			Name:           "anthropic-fallback",
			URL:            secondary.URL,
			Model:          "claude-configured",
			Adapter:        AnthropicAdapter{},
			Authentication: HeaderAuthentication("x-api-key"),
			Headers:        AnthropicHeaders(),
			Credentials:    []Credential{{ID: "anthropic", Secret: "anthropic-secret"}},
			Client:         secondary.Client(),
		},
	})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	generation, err := pool.Generate(context.Background(), adapterRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if generation.Provider != "anthropic-fallback" ||
		generation.Model != "claude-fallback" ||
		generation.Content != "fallback worked" {
		t.Fatalf("generation = %+v", generation)
	}
	usage := pool.Usage()
	if usage[0].Failures != 1 || usage[1].TotalTokens != 4 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestPoolAllRateLimitedAndFailures(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	pool, err := NewPool([]Endpoint{{
		Name:           "limited",
		URL:            server.URL,
		Model:          "model",
		Adapter:        OpenAIAdapter{},
		Authentication: BearerAuthentication(),
		Credentials:    []Credential{{ID: "only", Secret: "secret"}},
		Client:         server.Client(),
	}})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	_, err = pool.Generate(context.Background(), adapterRequest())
	if !errors.Is(err, ErrNoProvider) || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Generate() error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Generate(cancelled, adapterRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Generate() error = %v", err)
	}
}

func TestPoolValidation(t *testing.T) {
	t.Parallel()
	valid := Endpoint{
		Name:           "provider",
		URL:            "https://example.invalid",
		Model:          "model",
		Adapter:        OpenAIAdapter{},
		Authentication: BearerAuthentication(),
		Credentials:    []Credential{{ID: "key", Secret: "secret"}},
	}
	tests := []struct {
		name      string
		endpoints []Endpoint
	}{
		{name: "empty"},
		{name: "identity", endpoints: []Endpoint{{}}},
		{name: "adapter", endpoints: []Endpoint{{
			Name: "x", URL: "x", Model: "x", Credentials: valid.Credentials,
			Authentication: valid.Authentication,
		}}},
		{name: "credentials", endpoints: []Endpoint{{
			Name: "x", URL: "x", Model: "x", Adapter: OpenAIAdapter{},
			Authentication: valid.Authentication,
		}}},
		{name: "authentication", endpoints: []Endpoint{{
			Name: "x", URL: "x", Model: "x", Adapter: OpenAIAdapter{},
			Credentials: valid.Credentials,
		}}},
		{name: "credential identity", endpoints: []Endpoint{{
			Name: "x", URL: "x", Model: "x", Adapter: OpenAIAdapter{},
			Authentication: valid.Authentication,
			Credentials:    []Credential{{Secret: "x"}},
		}}},
		{name: "negative timeout", endpoints: []Endpoint{func() Endpoint {
			endpoint := valid
			endpoint.RequestTimeout = -time.Second
			return endpoint
		}()}},
		{name: "endpoint duplicate", endpoints: []Endpoint{valid, valid}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewPool(test.endpoints); err == nil {
				t.Fatal("NewPool() succeeded")
			}
		})
	}
	duplicateCredential := valid
	duplicateCredential.Credentials = []Credential{
		{ID: "same", Secret: "a"},
		{ID: "same", Secret: "b"},
	}
	if _, err := NewPool([]Endpoint{duplicateCredential}); err == nil {
		t.Fatal("duplicate credential accepted")
	}
}

func TestPoolResponseAndTransportErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "invalid JSON", handler: func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, `{`)
		}},
		{name: "too large", handler: func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, strings.Repeat("x", maxProviderResponseBytes+1))
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(test.handler)
			defer server.Close()
			pool, err := NewPool([]Endpoint{{
				Name:           "bad",
				URL:            server.URL,
				Model:          "model",
				Adapter:        OpenAIAdapter{},
				Authentication: BearerAuthentication(),
				Credentials:    []Credential{{ID: "key", Secret: "secret"}},
				Client:         server.Client(),
			}})
			if err != nil {
				t.Fatalf("NewPool() error = %v", err)
			}
			if _, err := pool.Generate(context.Background(), adapterRequest()); err == nil {
				t.Fatal("Generate() succeeded")
			}
		})
	}
	_, err := NewPool([]Endpoint{{
		Name:           "network",
		URL:            "://invalid",
		Model:          "model",
		Adapter:        OpenAIAdapter{},
		Authentication: BearerAuthentication(),
		Credentials:    []Credential{{ID: "key", Secret: "secret"}},
	}}, WithClock(fixedClock{now: time.Unix(1, 0)}))
	if err == nil {
		t.Fatal("invalid endpoint URL accepted")
	}
}

func TestPoolDefaultTransportUsesSSRFDispatcher(t *testing.T) {
	t.Parallel()
	pool, err := NewPool([]Endpoint{{
		Name:           "private",
		URL:            "https://127.0.0.1/v1/messages",
		Model:          "model",
		Adapter:        OpenAIAdapter{},
		Authentication: BearerAuthentication(),
		Credentials:    []Credential{{ID: "key", Secret: "secret"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Generate(
		context.Background(),
		adapterRequest(),
	); !errors.Is(err, ssrf.ErrBlocked) {
		t.Fatalf("Generate() error = %v, want SSRF denial", err)
	}
}

func TestAccountTokenCostUsesConfiguredTariffAndNeverAssumesUnknownSpend(t *testing.T) {
	t.Parallel()
	usage := protocol.TokenUsage{
		PromptTokens: 10, CachedTokens: 4, CompletionTokens: 5,
		TotalTokens: 15,
	}
	unknown := accountTokenCost(usage, nil)
	if unknown.ModelCostKnown || unknown.ProviderSpendKnown ||
		unknown.ModelCostMicrocents != 0 ||
		unknown.ProviderSpendMicrocents != 0 {
		t.Fatalf("unknown pricing became zero spend: %+v", unknown)
	}
	accounted := accountTokenCost(usage, &TokenPricing{
		InputMicrocentsPerToken:       100,
		CachedInputMicrocentsPerToken: 25,
		OutputMicrocentsPerToken:      400,
		ProviderSurchargeBPS:          500,
	})
	if accounted.ModelCostMicrocents != 2_700 ||
		accounted.ProviderSpendMicrocents != 2_835 ||
		!accounted.ModelCostKnown ||
		!accounted.ProviderSpendKnown {
		t.Fatalf("accounted usage = %+v", accounted)
	}
}

func writeOpenAISuccess(t *testing.T, writer http.ResponseWriter, model string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	payload, err := json.Marshal(map[string]interface{}{
		"model": model,
		"choices": []map[string]interface{}{{
			"message":       map[string]string{"content": "ok"},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     2,
			"completion_tokens": 3,
			"total_tokens":      5,
		},
	})
	if err != nil {
		t.Fatalf("Marshal(response) error = %v", err)
	}
	_, _ = writer.Write(payload)
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}
