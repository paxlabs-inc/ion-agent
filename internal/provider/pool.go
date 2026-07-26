package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/ssrf"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const maxProviderResponseBytes = 8 << 20

// Credential identifies a secret without exposing it in usage reports.
type Credential struct {
	ID     string
	Secret string
}

// Authentication describes how a provider credential is placed on a request.
type Authentication struct {
	Header string
	Scheme string
}

// Endpoint configures one entry in the ordered provider fallback chain.
type Endpoint struct {
	Name           string
	URL            string
	Model          string
	Adapter        ProviderAdapter
	Credentials    []Credential
	Authentication Authentication
	Headers        map[string]string
	RequestTimeout time.Duration
	Pricing        *TokenPricing
	// Client is only for controlled external boundaries such as httptest.
	// Production callers leave it nil so the SSRF dispatcher is installed.
	Client *http.Client
}

// TokenPricing is the operator-confirmed billing tariff for one endpoint.
// A nil tariff means cost is unknown; the supervisor must not treat unknown
// spend as zero.
type TokenPricing struct {
	InputMicrocentsPerToken       int64
	CachedInputMicrocentsPerToken int64
	OutputMicrocentsPerToken      int64
	ProviderSurchargeBPS          int64
}

// CredentialUsage is a snapshot of per-key accounting.
type CredentialUsage struct {
	Provider         string
	CredentialID     string
	Requests         uint64
	Failures         uint64
	RateLimits       uint64
	PromptTokens     uint64
	CompletionTokens uint64
	TotalTokens      uint64
}

type credentialState struct {
	config Credential
	usage  CredentialUsage
}

type endpointState struct {
	config      Endpoint
	credentials []credentialState
	active      int
}

// Pool owns the ordered fallback chain and synchronized credential state.
type Pool struct {
	mu        sync.Mutex
	clock     types.Clock
	endpoints []*endpointState
}

// PoolOption customizes provider pool construction.
type PoolOption func(*Pool)

// WithClock injects the clock used for provider duration measurement.
func WithClock(clock types.Clock) PoolOption {
	return func(pool *Pool) {
		if clock != nil {
			pool.clock = clock
		}
	}
}

// NewPool validates and copies an ordered provider fallback chain.
func NewPool(endpoints []Endpoint, options ...PoolOption) (*Pool, error) {
	pool := &Pool{clock: types.SystemClock{}}
	for _, option := range options {
		option(pool)
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("provider: at least one endpoint is required")
	}
	seenNames := make(map[string]struct{}, len(endpoints))
	seenCredentialIDs := make(map[string]struct{})
	for index, endpoint := range endpoints {
		if err := validateEndpoint(endpoint); err != nil {
			return nil, fmt.Errorf("provider: endpoint %d: %w", index, err)
		}
		if _, exists := seenNames[endpoint.Name]; exists {
			return nil, fmt.Errorf("provider: duplicate endpoint name %q", endpoint.Name)
		}
		seenNames[endpoint.Name] = struct{}{}
		prepared, err := cloneEndpoint(endpoint)
		if err != nil {
			return nil, fmt.Errorf("provider: endpoint %d: %w", index, err)
		}
		state := &endpointState{config: prepared}
		for _, credential := range endpoint.Credentials {
			key := endpoint.Name + "\x00" + credential.ID
			if _, exists := seenCredentialIDs[key]; exists {
				return nil, fmt.Errorf(
					"provider: duplicate credential ID %q for %q",
					credential.ID,
					endpoint.Name,
				)
			}
			seenCredentialIDs[key] = struct{}{}
			state.credentials = append(state.credentials, credentialState{
				config: credential,
				usage: CredentialUsage{
					Provider:     endpoint.Name,
					CredentialID: credential.ID,
				},
			})
		}
		pool.endpoints = append(pool.endpoints, state)
	}
	return pool, nil
}

// Generate tries credentials on rate limits, then providers in configured
// fallback order. Provider-specific failures never change the O1 response
// contract observed by callers.
func (pool *Pool) Generate(
	ctx context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	if err := ctx.Err(); err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	var failures []error
	for _, endpoint := range pool.endpoints {
		generation, err := pool.generateEndpoint(ctx, endpoint, request)
		if err == nil {
			return generation, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", endpoint.config.Name, err))
		if ctx.Err() != nil {
			return protocol.NormalizedGeneration{}, ctx.Err()
		}
	}
	return protocol.NormalizedGeneration{}, errors.Join(
		append([]error{ErrNoProvider}, failures...)...,
	)
}

// GenerateStream delivers provider content and reasoning deltas as they arrive
// while still returning one fully assembled, validated generation. A provider
// fallback is only attempted before any delta has escaped to the caller.
func (pool *Pool) GenerateStream(
	ctx context.Context,
	request protocol.GenerationRequest,
	deliver func(protocol.StreamChunk) error,
) (protocol.NormalizedGeneration, error) {
	if err := ctx.Err(); err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	if deliver == nil {
		return protocol.NormalizedGeneration{}, fmt.Errorf(
			"provider: stream callback is required",
		)
	}
	request.Stream = true
	var failures []error
	for _, endpoint := range pool.endpoints {
		generation, emitted, err := pool.generateEndpointStream(
			ctx, endpoint, request, deliver,
		)
		if err == nil {
			return generation, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", endpoint.config.Name, err))
		if emitted || ctx.Err() != nil {
			if ctx.Err() != nil {
				return protocol.NormalizedGeneration{}, ctx.Err()
			}
			return protocol.NormalizedGeneration{}, errors.Join(
				append([]error{ErrNoProvider}, failures...)...,
			)
		}
	}
	return protocol.NormalizedGeneration{}, errors.Join(
		append([]error{ErrNoProvider}, failures...)...,
	)
}

// Usage returns an immutable snapshot without credential secrets.
func (pool *Pool) Usage() []CredentialUsage {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	var reports []CredentialUsage
	for _, endpoint := range pool.endpoints {
		for _, credential := range endpoint.credentials {
			reports = append(reports, credential.usage)
		}
	}
	return reports
}

func (pool *Pool) generateEndpoint(
	ctx context.Context,
	endpoint *endpointState,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	request.Model = endpoint.config.Model
	body, err := endpoint.config.Adapter.TranslateRequest(request)
	if err != nil {
		return protocol.NormalizedGeneration{}, err
	}
	start := pool.clock.Now()
	for attempts := 0; attempts < len(endpoint.credentials); attempts++ {
		index, credential := pool.credentialForAttempt(endpoint)
		status, response, callErr := callProvider(
			ctx,
			endpoint.config,
			credential.Secret,
			body,
		)
		if callErr != nil {
			pool.markFailure(endpoint, index)
			return protocol.NormalizedGeneration{}, callErr
		}
		if status == http.StatusTooManyRequests {
			pool.markRateLimit(endpoint, index)
			continue
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			pool.markFailure(endpoint, index)
			return protocol.NormalizedGeneration{}, fmt.Errorf(
				"provider HTTP status %d",
				status,
			)
		}
		generation, translateErr := endpoint.config.Adapter.TranslateResponse(response)
		if translateErr != nil {
			pool.markFailure(endpoint, index)
			return protocol.NormalizedGeneration{}, translateErr
		}
		generation.Provider = endpoint.config.Name
		if generation.Model == "" {
			generation.Model = endpoint.config.Model
		}
		generation.Duration = pool.clock.Now().Sub(start)
		generation.Usage = accountTokenCost(
			generation.Usage,
			endpoint.config.Pricing,
		)
		pool.markSuccess(endpoint, index, generation.Usage)
		return generation, nil
	}
	return protocol.NormalizedGeneration{}, ErrRateLimited
}

type streamedToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

func (pool *Pool) generateEndpointStream(
	ctx context.Context,
	endpoint *endpointState,
	request protocol.GenerationRequest,
	deliver func(protocol.StreamChunk) error,
) (protocol.NormalizedGeneration, bool, error) {
	request.Model = endpoint.config.Model
	request.Stream = true
	body, err := endpoint.config.Adapter.TranslateRequest(request)
	if err != nil {
		return protocol.NormalizedGeneration{}, false, err
	}
	start := pool.clock.Now()
	for attempts := 0; attempts < len(endpoint.credentials); attempts++ {
		index, credential := pool.credentialForAttempt(endpoint)
		status, response, callErr := openProviderResponse(
			ctx, endpoint.config, credential.Secret, body,
		)
		if callErr != nil {
			pool.markFailure(endpoint, index)
			return protocol.NormalizedGeneration{}, false, callErr
		}
		if status == http.StatusTooManyRequests {
			_ = response.Body.Close()
			pool.markRateLimit(endpoint, index)
			continue
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			pool.markFailure(endpoint, index)
			return protocol.NormalizedGeneration{}, false, fmt.Errorf(
				"provider HTTP status %d", status,
			)
		}
		if !strings.Contains(
			strings.ToLower(response.Header.Get("Content-Type")),
			"text/event-stream",
		) {
			payload, readErr := io.ReadAll(io.LimitReader(
				response.Body, maxProviderResponseBytes+1,
			))
			_ = response.Body.Close()
			if readErr != nil {
				pool.markFailure(endpoint, index)
				return protocol.NormalizedGeneration{}, false, fmt.Errorf(
					"provider: read response: %w", readErr,
				)
			}
			if len(payload) > maxProviderResponseBytes {
				pool.markFailure(endpoint, index)
				return protocol.NormalizedGeneration{}, false, fmt.Errorf(
					"provider: response exceeds size limit",
				)
			}
			generation, translateErr := endpoint.config.Adapter.TranslateResponse(payload)
			if translateErr != nil {
				pool.markFailure(endpoint, index)
				return protocol.NormalizedGeneration{}, false, translateErr
			}
			generation.Provider = endpoint.config.Name
			if generation.Model == "" {
				generation.Model = endpoint.config.Model
			}
			generation.Duration = pool.clock.Now().Sub(start)
			generation.Usage = accountTokenCost(
				generation.Usage,
				endpoint.config.Pricing,
			)
			emitted := false
			if generation.Content != "" || generation.Reasoning != "" {
				if err := deliver(protocol.StreamChunk{
					ContentDelta:   generation.Content,
					ReasoningDelta: generation.Reasoning,
				}); err != nil {
					pool.markFailure(endpoint, index)
					return protocol.NormalizedGeneration{}, false, err
				}
				emitted = true
			}
			pool.markSuccess(endpoint, index, generation.Usage)
			return generation, emitted, nil
		}

		var content strings.Builder
		var reasoning strings.Builder
		toolsByIndex := make(map[int]*streamedToolCall)
		toolOrder := make([]int, 0)
		finishReason := ""
		usage := protocol.TokenUsage{}
		emitted := false
		streamErr := readSSE(response.Body, func(event []byte) error {
			chunk, translateErr := endpoint.config.Adapter.TranslateStreamEvent(event)
			if translateErr != nil {
				return translateErr
			}
			content.WriteString(chunk.ContentDelta)
			reasoning.WriteString(chunk.ReasoningDelta)
			if chunk.FinishReason != "" {
				finishReason = chunk.FinishReason
			}
			if chunk.Usage != nil {
				mergeStreamUsage(&usage, *chunk.Usage)
			}
			for _, delta := range chunk.ToolCallDeltas {
				call, exists := toolsByIndex[delta.Index]
				if !exists {
					call = &streamedToolCall{}
					toolsByIndex[delta.Index] = call
					toolOrder = append(toolOrder, delta.Index)
				}
				if delta.ID != "" {
					call.id = delta.ID
				}
				if delta.Name != "" {
					call.name = delta.Name
				}
				call.arguments.WriteString(delta.Arguments)
			}
			if chunk.ToolCall != nil && len(chunk.ToolCallDeltas) == 0 {
				call, exists := toolsByIndex[len(toolOrder)]
				if !exists {
					call = &streamedToolCall{}
					toolsByIndex[len(toolOrder)] = call
					toolOrder = append(toolOrder, len(toolOrder))
				}
				call.id = chunk.ToolCall.ID
				call.name = chunk.ToolCall.Name
				call.arguments.Write(chunk.ToolCall.Arguments)
			}
			if chunk.ContentDelta != "" || chunk.ReasoningDelta != "" {
				if err := deliver(chunk); err != nil {
					return err
				}
				emitted = true
			}
			return nil
		})
		_ = response.Body.Close()
		if streamErr != nil {
			pool.markFailure(endpoint, index)
			return protocol.NormalizedGeneration{}, emitted, streamErr
		}
		if finishReason == "" {
			finishReason = protocol.FinishError
		}
		calls := make([]protocol.NormalizedToolCall, 0, len(toolOrder))
		for _, toolIndex := range toolOrder {
			source := toolsByIndex[toolIndex]
			arguments := strings.TrimSpace(source.arguments.String())
			if arguments == "" {
				arguments = "{}"
			}
			call := protocol.NormalizedToolCall{
				ID: source.id, Name: source.name,
				Arguments: json.RawMessage(arguments),
			}
			if err := call.Validate(); err != nil {
				pool.markFailure(endpoint, index)
				return protocol.NormalizedGeneration{}, emitted, fmt.Errorf(
					"provider: invalid streamed tool call %d: %w", toolIndex, err,
				)
			}
			calls = append(calls, call)
		}
		generation := protocol.NormalizedGeneration{
			Content: content.String(), Reasoning: reasoning.String(),
			ToolCalls: calls, FinishReason: finishReason, Usage: usage,
			Provider: endpoint.config.Name, Model: endpoint.config.Model,
			Duration: pool.clock.Now().Sub(start),
		}
		if finalizer, ok := endpoint.config.Adapter.(GenerationFinalizer); ok {
			generation, streamErr = finalizer.FinalizeGeneration(generation)
			if streamErr != nil {
				pool.markFailure(endpoint, index)
				return protocol.NormalizedGeneration{}, emitted, streamErr
			}
		}
		if err := generation.Validate(); err != nil {
			pool.markFailure(endpoint, index)
			return protocol.NormalizedGeneration{}, emitted, err
		}
		generation.Usage = accountTokenCost(
			generation.Usage,
			endpoint.config.Pricing,
		)
		pool.markSuccess(endpoint, index, generation.Usage)
		return generation, emitted, nil
	}
	return protocol.NormalizedGeneration{}, false, ErrRateLimited
}

func mergeStreamUsage(current *protocol.TokenUsage, update protocol.TokenUsage) {
	if update.PromptTokens != 0 {
		current.PromptTokens = update.PromptTokens
	}
	if update.CompletionTokens != 0 {
		current.CompletionTokens = update.CompletionTokens
	}
	if update.TotalTokens != 0 {
		current.TotalTokens = update.TotalTokens
	}
	if update.ReasoningTokens != 0 {
		current.ReasoningTokens = update.ReasoningTokens
	}
	if update.CachedTokens != 0 {
		current.CachedTokens = update.CachedTokens
	}
	if update.ModelCostKnown {
		current.ModelCostMicrocents = update.ModelCostMicrocents
		current.ModelCostKnown = true
	}
	if update.ProviderSpendKnown {
		current.ProviderSpendMicrocents = update.ProviderSpendMicrocents
		current.ProviderSpendKnown = true
	}
	if current.TotalTokens < current.PromptTokens+current.CompletionTokens {
		current.TotalTokens = current.PromptTokens + current.CompletionTokens
	}
}

func accountTokenCost(
	usage protocol.TokenUsage,
	pricing *TokenPricing,
) protocol.TokenUsage {
	if pricing == nil {
		return usage
	}
	cached := max(int64(usage.CachedTokens), 0)
	prompt := max(int64(usage.PromptTokens), 0)
	if cached > prompt {
		cached = prompt
	}
	uncached := prompt - cached
	completion := max(int64(usage.CompletionTokens), 0)
	modelCost := sumTokenCosts(
		[2]int64{uncached, pricing.InputMicrocentsPerToken},
		[2]int64{cached, pricing.CachedInputMicrocentsPerToken},
		[2]int64{completion, pricing.OutputMicrocentsPerToken},
	)
	providerSpend := modelCost
	if pricing.ProviderSurchargeBPS > 0 {
		if modelCost > math.MaxInt64/
			(10_000+pricing.ProviderSurchargeBPS) {
			providerSpend = math.MaxInt64
		} else {
			providerSpend = divideRoundUp(
				modelCost*(10_000+pricing.ProviderSurchargeBPS),
				10_000,
			)
		}
	}
	usage.ModelCostMicrocents = modelCost
	usage.ProviderSpendMicrocents = providerSpend
	usage.ModelCostKnown = true
	usage.ProviderSpendKnown = true
	return usage
}

func sumTokenCosts(terms ...[2]int64) int64 {
	var total int64
	for _, term := range terms {
		count, rate := term[0], term[1]
		if count == 0 || rate == 0 {
			continue
		}
		if count > math.MaxInt64/rate {
			return math.MaxInt64
		}
		cost := count * rate
		if total > math.MaxInt64-cost {
			return math.MaxInt64
		}
		total += cost
	}
	return total
}

func divideRoundUp(value, divisor int64) int64 {
	if value <= 0 {
		return 0
	}
	result := value / divisor
	if value%divisor != 0 {
		result++
	}
	return result
}

func readSSE(body io.Reader, consume func([]byte) error) error {
	scanner := bufio.NewScanner(io.LimitReader(body, maxProviderResponseBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxProviderResponseBytes)
	var data bytes.Buffer
	total := 0
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		event := append([]byte(nil), data.Bytes()...)
		data.Reset()
		return consume(event)
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		total += len(line) + 1
		if total > maxProviderResponseBytes {
			return fmt.Errorf("provider: response exceeds size limit")
		}
		if len(line) == 0 {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.Write(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:"))))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("provider: read stream: %w", err)
	}
	return flush()
}

func (pool *Pool) credentialForAttempt(endpoint *endpointState) (int, Credential) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	index := endpoint.active
	endpoint.credentials[index].usage.Requests++
	return index, endpoint.credentials[index].config
}

func (pool *Pool) markRateLimit(endpoint *endpointState, index int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	endpoint.credentials[index].usage.RateLimits++
	endpoint.credentials[index].usage.Failures++
	endpoint.active = (index + 1) % len(endpoint.credentials)
}

func (pool *Pool) markFailure(endpoint *endpointState, index int) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	endpoint.credentials[index].usage.Failures++
}

func (pool *Pool) markSuccess(
	endpoint *endpointState,
	index int,
	usage protocol.TokenUsage,
) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	endpoint.active = index
	record := &endpoint.credentials[index].usage
	record.PromptTokens += uint64(usage.PromptTokens)
	record.CompletionTokens += uint64(usage.CompletionTokens)
	record.TotalTokens += uint64(usage.TotalTokens)
}

func validateEndpoint(endpoint Endpoint) error {
	if strings.TrimSpace(endpoint.Name) == "" ||
		strings.TrimSpace(endpoint.URL) == "" ||
		strings.TrimSpace(endpoint.Model) == "" {
		return fmt.Errorf("name, URL, and model are required")
	}
	if endpoint.Adapter == nil {
		return fmt.Errorf("adapter is required")
	}
	if endpoint.RequestTimeout < 0 {
		return fmt.Errorf("request timeout cannot be negative")
	}
	if endpoint.Pricing != nil &&
		(endpoint.Pricing.InputMicrocentsPerToken < 0 ||
			endpoint.Pricing.InputMicrocentsPerToken > 1_000_000_000 ||
			endpoint.Pricing.CachedInputMicrocentsPerToken < 0 ||
			endpoint.Pricing.CachedInputMicrocentsPerToken > 1_000_000_000 ||
			endpoint.Pricing.OutputMicrocentsPerToken < 0 ||
			endpoint.Pricing.OutputMicrocentsPerToken > 1_000_000_000 ||
			endpoint.Pricing.ProviderSurchargeBPS < 0 ||
			endpoint.Pricing.ProviderSurchargeBPS > 100_000) {
		return fmt.Errorf("provider pricing is outside safe bounds")
	}
	if len(endpoint.Credentials) == 0 {
		return fmt.Errorf("at least one credential is required")
	}
	if strings.TrimSpace(endpoint.Authentication.Header) == "" {
		return fmt.Errorf("authentication header is required")
	}
	for _, credential := range endpoint.Credentials {
		if strings.TrimSpace(credential.ID) == "" || credential.Secret == "" {
			return fmt.Errorf("credential ID and secret are required")
		}
	}
	return nil
}

func cloneEndpoint(endpoint Endpoint) (Endpoint, error) {
	cloned := endpoint
	if endpoint.Pricing != nil {
		pricing := *endpoint.Pricing
		cloned.Pricing = &pricing
	}
	cloned.Credentials = append([]Credential(nil), endpoint.Credentials...)
	cloned.Headers = make(map[string]string, len(endpoint.Headers))
	for key, value := range endpoint.Headers {
		cloned.Headers[key] = value
	}
	if cloned.Client == nil {
		target, err := url.Parse(endpoint.URL)
		if err != nil || target.Hostname() == "" {
			return Endpoint{}, fmt.Errorf("invalid endpoint URL")
		}
		dispatcher, err := ssrf.New(ssrf.Config{
			AllowedHosts:   []string{target.Hostname()},
			RequestTimeout: endpoint.RequestTimeout,
		})
		if err != nil {
			return Endpoint{}, fmt.Errorf("configure SSRF dispatcher: %w", err)
		}
		cloned.Client = dispatcher.Client()
	}
	return cloned, nil
}

func callProvider(
	ctx context.Context,
	endpoint Endpoint,
	secret string,
	body json.RawMessage,
) (int, json.RawMessage, error) {
	status, response, err := openProviderResponse(ctx, endpoint, secret, body)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		return 0, nil, fmt.Errorf("provider: read response: %w", err)
	}
	if len(payload) > maxProviderResponseBytes {
		return 0, nil, fmt.Errorf("provider: response exceeds size limit")
	}
	return status, payload, nil
}

func openProviderResponse(
	ctx context.Context,
	endpoint Endpoint,
	secret string,
	body json.RawMessage,
) (int, *http.Response, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint.URL,
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, nil, fmt.Errorf("provider: construct request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(
		endpoint.Authentication.Header,
		endpoint.Authentication.Scheme+secret,
	)
	for key, value := range endpoint.Headers {
		request.Header.Set(key, value)
	}
	response, err := endpoint.Client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("provider: request failed: %w", err)
	}
	return response.StatusCode, response, nil
}

// BearerAuthentication configures an Authorization: Bearer header.
func BearerAuthentication() Authentication {
	return Authentication{Header: "Authorization", Scheme: "Bearer "}
}

// HeaderAuthentication configures a provider-specific raw API key header.
func HeaderAuthentication(header string) Authentication {
	return Authentication{Header: header}
}

// AnthropicHeaders returns the mandatory version header for Messages API.
func AnthropicHeaders() map[string]string {
	return map[string]string{"anthropic-version": "2023-06-01"}
}
