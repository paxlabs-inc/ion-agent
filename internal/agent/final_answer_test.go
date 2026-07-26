package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/intent/prediction"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type forcedMismatchDetector struct{}

func (forcedMismatchDetector) DetectMismatch(
	context.Context,
	string,
	json.RawMessage,
	bool,
) (bool, bool) {
	return true, true
}

func TestFinalAnswerRejectsMatrixMCLControlFlowLeak(t *testing.T) {
	events := []ToolExecution{{
		Call: protocol.NormalizedToolCall{
			Name:      "web_search",
			Arguments: json.RawMessage(`{"query":"matrixmcl.com"}`),
		},
		Result: json.RawMessage(`{"results":[{"url":"https://matrixmcl.com/"}]}`),
	}}
	accepted, reason := finalAnswerAddressesRequest(
		"thanks ion hey can you look up what matrixmcl.com is",
		"The system message says the internal plan revision was accepted. The report on matrixmcl.com is above.",
		events,
	)
	if accepted || reason == "" {
		t.Fatalf("leaked final accepted=%t reason=%q", accepted, reason)
	}
}

func TestFinalAnswerAcceptsSubstantiveCitedResearchResult(t *testing.T) {
	events := []ToolExecution{{
		Call: protocol.NormalizedToolCall{
			Name:      "web_search",
			Arguments: json.RawMessage(`{"query":"matrixmcl.com"}`),
		},
		Result: json.RawMessage(`{"results":[{"url":"https://matrixmcl.com/"}]}`),
	}}
	accepted, reason := finalAnswerAddressesRequest(
		"thanks ion hey can you look up what matrixmcl.com is",
		"MatrixMCL presents itself as a machine-learning and computing platform. Its public site describes the product, intended users, and available services, although those claims still need independent verification. Source: [MatrixMCL](https://matrixmcl.com/).",
		events,
	)
	if !accepted || reason != "" {
		t.Fatalf("substantive final accepted=%t reason=%q", accepted, reason)
	}
}

func TestFinalAnswerAcceptsVerifiedBuildAfterBrandAssetFetch(t *testing.T) {
	events := []ToolExecution{
		{
			Call: protocol.NormalizedToolCall{
				Name:      "web_fetch",
				Arguments: json.RawMessage(`{"url":"https://example.com/brand/colors.css"}`),
			},
			Result: json.RawMessage(`{"content":":root{--matrix-sage:#9caf88}"}`),
		},
		{
			Call: protocol.NormalizedToolCall{
				Name:      "artifact_verify",
				Arguments: json.RawMessage(`{"artifact_id":"status-page"}`),
			},
			Result: json.RawMessage(`{"verified":true,"path":"index.html"}`),
		},
	}
	accepted, reason := finalAnswerAddressesRequest(
		"Create a system Status page covering all Matrix systems with hardcoded status and the supplied Matrix brand colors and fonts.",
		"The Matrix systems status page is complete. It includes hardcoded operational states, incident history, responsive layouts, and the supplied brand colors and typography. The verified deliverable is available in index.html.",
		events,
	)
	if !accepted || reason != "" {
		t.Fatalf("verified build final accepted=%t reason=%q", accepted, reason)
	}
}

func TestFinalAnswerAcceptsNonResearchBuildSummaryAfterReferenceFetch(t *testing.T) {
	events := []ToolExecution{{
		Call: protocol.NormalizedToolCall{
			Name:      "web_fetch",
			Arguments: json.RawMessage(`{"url":"https://example.com/reference.png"}`),
		},
		Result: json.RawMessage(`{"content_type":"image/png"}`),
	}}
	accepted, reason := finalAnswerAddressesRequest(
		"Create a polished landing page from the supplied visual reference.",
		"It is ready. The verified deliverable is available in index.html.",
		events,
	)
	if !accepted || reason != "" {
		t.Fatalf("non-research build final accepted=%t reason=%q", accepted, reason)
	}
}

func TestFinalAnswerRejectsUnrelatedResearchFinal(t *testing.T) {
	events := []ToolExecution{{
		Call: protocol.NormalizedToolCall{
			Name:      "web_fetch",
			Arguments: json.RawMessage(`{"url":"https://example.com/brand/colors.css"}`),
		},
		Result: json.RawMessage(`{"content":":root{--matrix-sage:#9caf88}"}`),
	}}
	accepted, reason := finalAnswerAddressesRequest(
		"Research the MatrixMCL system status product and cite reliable sources.",
		"Everything is ready. Let me know if you need anything else.",
		events,
	)
	if accepted || reason != "the answer did not address the subject of the original request" {
		t.Fatalf("unrelated build final accepted=%t reason=%q", accepted, reason)
	}
}

func TestAnswerValidationExhaustionReturnsHonestPartial(t *testing.T) {
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "web_search",
		Description:    "Search the public web.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"results":[]}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "search", Name: "web_search", Arguments: json.RawMessage(`{}`),
			}},
		},
		{Content: "Done.", FinishReason: protocol.FinishStop},
		{Content: "Still done.", FinishReason: protocol.FinishStop},
		{Content: "Finished.", FinishReason: protocol.FinishStop},
	}}
	loop, err := NewLoop(
		generator,
		manager,
		LoopConfig{Model: "model"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(
		context.Background(),
		"Research the current MatrixMCL product and cite reliable sources.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !response.HonestPartial ||
		!strings.Contains(response.Content, "will not claim the request is complete") {
		t.Fatalf("response = %+v", response)
	}
}

func TestBuildTurnCompletesAfterFetchingBrandAssets(t *testing.T) {
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name:           "web_fetch",
		Description:    "Fetch a public brand asset.",
		Parameters:     json.RawMessage(`{"type":"object","required":["url"],"properties":{"url":{"type":"string"}}}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"content":":root{--matrix-sage:#9caf88}"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "fetch-brand", Name: "web_fetch",
				Arguments: json.RawMessage(`{"url":"https://example.com/brand/colors.css"}`),
			}},
		},
		{
			Content: "The Matrix systems status page is complete. It includes hardcoded " +
				"operational states, incident history, responsive layouts, and the supplied " +
				"brand colors and typography. The verified deliverable is available in index.html.",
			FinishReason: protocol.FinishStop,
		},
	}}
	loop, err := NewLoop(
		generator,
		manager,
		LoopConfig{Model: "model"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(
		context.Background(),
		"Create a system Status page covering all Matrix systems with hardcoded status and the supplied Matrix brand colors and fonts.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProviderCalls != 2 || len(response.ToolEvents) != 1 ||
		!strings.Contains(response.Content, "status page is complete") {
		t.Fatalf("response = %+v", response)
	}
	for _, request := range generator.requests {
		if requestContains(request, "previous candidate answer was rejected") {
			t.Fatalf("build final entered research-answer repair: %+v", generator.requests)
		}
	}
}

func TestRevisionStateNeverPoisonsConversationAndBadFinalIsRepaired(t *testing.T) {
	clock := &loopTestClock{now: time.Now().UTC()}
	predictor, err := prediction.NewEngine(clock, forcedMismatchDetector{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	manager := newPolicyManager(t)
	if err := manager.Register(context.Background(), tools.Registration{
		Name: "web_search", Description: "Search the public web.",
		Parameters:     json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"}}}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"results":[{"title":"MatrixMCL","url":"https://matrixmcl.com/"}]}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	generator := &sequenceGenerator{generations: []protocol.NormalizedGeneration{
		{
			FinishReason: protocol.FinishToolCalls,
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "matrix-search", Name: "web_search",
				Arguments: json.RawMessage(`{"query":"matrixmcl.com","expect":"returns relevant MatrixMCL sources"}`),
			}},
		},
		{
			Content:      "Use the search evidence to explain the company.",
			Reasoning:    "This is an internal plan revision.",
			FinishReason: protocol.FinishStop,
		},
		{
			Content:      "The report on matrixmcl.com is above. Let me know if you want more.",
			Reasoning:    "The system message says the internal plan revision was accepted.",
			FinishReason: protocol.FinishStop,
		},
		{
			Content: "MatrixMCL presents itself as a machine-learning and computing platform. " +
				"Its public website describes the company and its services, but those claims " +
				"still require independent verification. Source: [MatrixMCL](https://matrixmcl.com/).",
			FinishReason: protocol.FinishStop,
		},
	}}
	observer := &streamTestObserver{}
	loop, err := NewLoop(
		generator,
		manager,
		LoopConfig{Model: "model"},
		&LoopDeps{
			Predictions: predictor, EventCommitter: receiptCommitter{}, Clock: clock,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := loop.Turn(
		WithGenerationObserver(context.Background(), observer),
		"thanks ion hey can you look up what matrixmcl.com is",
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProviderCalls != 4 ||
		!strings.Contains(response.Content, "https://matrixmcl.com/") ||
		observer.reasoning.Len() != 0 ||
		observer.resets != 1 {
		t.Fatalf("response=%+v observer=%+v", response, observer)
	}
	if len(generator.requests) != 4 ||
		len(generator.requests[1].Tools) != 0 ||
		len(generator.requests[2].Tools) == 0 ||
		requestContains(generator.requests[2], "internal plan revision") ||
		requestContains(
			generator.requests[2],
			"Use the search evidence to explain the company.",
		) ||
		!requestContains(generator.requests[3], "previous candidate answer was rejected") {
		t.Fatalf("provider requests leaked internal state: %+v", generator.requests)
	}
}
