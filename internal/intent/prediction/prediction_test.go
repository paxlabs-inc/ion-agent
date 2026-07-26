package prediction

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type modelDetectorGenerator struct {
	requests []protocol.GenerationRequest
}

func (generator *modelDetectorGenerator) Generate(
	_ context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	generator.requests = append(generator.requests, request)
	return protocol.NormalizedGeneration{
		Content:      `{"mismatch":true}`,
		FinishReason: protocol.FinishStop,
	}, nil
}

func Test_InjectExpectParam_AddsToAllSchemas(t *testing.T) {
	schemas := []protocol.ToolDefinition{
		{Name: "read", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
		{Name: "write", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`)},
	}
	injected := InjectExpectParam(schemas)
	if len(injected) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(injected))
	}
	for _, schema := range injected {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(schema.Parameters, &params); err != nil {
			t.Fatalf("unmarshal %s: %v", schema.Name, err)
		}
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(params["properties"], &properties); err != nil {
			t.Fatalf("unmarshal properties: %v", err)
		}
		if _, ok := properties["expect"]; !ok {
			t.Fatalf("schema %s missing expect param", schema.Name)
		}
		var required []string
		if err := json.Unmarshal(params["required"], &required); err != nil {
			t.Fatalf("unmarshal required: %v", err)
		}
		found := false
		for _, r := range required {
			if r == "expect" {
				found = true
			}
		}
		if !found {
			t.Fatalf("schema %s missing expect in required", schema.Name)
		}
	}
	// Original schemas must not be mutated.
	for _, schema := range schemas {
		var params map[string]json.RawMessage
		json.Unmarshal(schema.Parameters, &params)
		var properties map[string]json.RawMessage
		_ = json.Unmarshal(params["properties"], &properties)
		if _, ok := properties["expect"]; ok {
			t.Fatalf("original schema %s was mutated", schema.Name)
		}
	}
}

func Test_PopExpect_RemovesAndReturns(t *testing.T) {
	args := json.RawMessage(`{"path":"/tmp/x","expect":"returns file contents"}`)
	expect, stripped, err := PopExpect(args)
	if err != nil {
		t.Fatalf("PopExpect: %v", err)
	}
	if expect != "returns file contents" {
		t.Fatalf("expected 'returns file contents', got %q", expect)
	}
	var remaining map[string]interface{}
	if err := json.Unmarshal(stripped, &remaining); err != nil {
		t.Fatalf("unmarshal stripped: %v", err)
	}
	if _, ok := remaining["expect"]; ok {
		t.Fatal("expect should have been removed")
	}
	if remaining["path"] != "/tmp/x" {
		t.Fatal("path should be preserved")
	}
}

func Test_StrategyKey_ScopesExactAction(t *testing.T) {
	args1 := json.RawMessage(`{"url":"https://api.example.com/v1/users"}`)
	args2 := json.RawMessage(`{"url":"https://api.example.com/v1/posts"}`)
	key1 := StrategyKey("fetch", args1)
	key2 := StrategyKey("fetch", args2)
	if key1 == key2 {
		t.Fatalf("different exact actions share key %q", key1)
	}
	args3 := json.RawMessage(`{"url": "https://api.example.com/v1/users"}`)
	key3 := StrategyKey("fetch", args3)
	if key1 != key3 {
		t.Fatalf("equivalent JSON actions have keys %q and %q", key1, key3)
	}
}

func Test_StrategyKey_DistinguishesSearchQueriesAndOptions(t *testing.T) {
	first := StrategyKey(
		"web_search",
		json.RawMessage(`{"query":"Perplexity features","limit":10}`),
	)
	same := StrategyKey(
		"web_search",
		json.RawMessage(`{"limit":10,"query":"Perplexity features"}`),
	)
	second := StrategyKey(
		"web_search",
		json.RawMessage(`{"query":"matrixmcl.com","limit":10}`),
	)
	if first != same {
		t.Fatalf("same exact search has keys %q and %q", first, same)
	}
	if first == second {
		t.Fatalf("different search queries share key %q", first)
	}
}

func Test_Meter_AccumulatesMismatches(t *testing.T) {
	meter := NewMeter(3)
	meter.Record("strategy-a", false)
	meter.Record("strategy-a", false)
	if meter.Count("strategy-a") != 2 {
		t.Fatalf("expected count 2, got %d", meter.Count("strategy-a"))
	}
	if meter.ShouldForceRevision() {
		t.Fatal("should not force revision at count 2 with threshold 3")
	}
	meter.Record("strategy-a", false)
	if !meter.ShouldForceRevision() {
		t.Fatal("should force revision at count 3")
	}
}

func Test_Meter_MatchedResetsCount(t *testing.T) {
	meter := NewMeter(3)
	meter.Record("strategy-a", false)
	meter.Record("strategy-a", false)
	meter.Record("strategy-a", true)
	if meter.Count("strategy-a") != 0 {
		t.Fatalf("expected count 0 after match, got %d", meter.Count("strategy-a"))
	}
}

func Test_Meter_BeginTurnClearsPriorRequestMismatches(t *testing.T) {
	meter := NewMeter(3)
	meter.Record("web_search@first", false)
	meter.Record("web_search@first", false)
	meter.BeginTurn()
	if meter.Count("web_search@first") != 0 || meter.ShouldForceRevision() {
		t.Fatal("prior request mismatch state survived BeginTurn")
	}
	meter.Record("web_search@second", false)
	if meter.Count("web_search@second") != 1 || meter.ShouldForceRevision() {
		t.Fatal("new request did not start with an independent mismatch meter")
	}
}

func Test_Meter_StaleMismatchDoesNotPoisonDifferentStrategy(t *testing.T) {
	meter := NewMeter(3)
	meter.Record("filesystem_read", false)
	meter.Record("filesystem_read", false)
	meter.Record("filesystem_read", false)
	if !meter.ShouldForceRevision() {
		t.Fatal("saturated strategy should force revision")
	}
	meter.Record("filesystem_read@research/continuous.go", false)
	if meter.ShouldForceRevision() {
		t.Fatal("stale strategy poisoned a distinct current target")
	}
}

func Test_DeterministicDetector_ErrorMatchesFailureExpect(t *testing.T) {
	detector := DeterministicDetector{}
	mismatch, decided := detector.DetectMismatch(
		context.Background(),
		"expect error or not found",
		json.RawMessage(`{"error":"not found"}`),
		true,
	)
	if !decided {
		t.Fatal("expected decided")
	}
	if mismatch {
		t.Fatal("failure expectation + error = match")
	}
}

func Test_DeterministicDetector_SuccessWhenFailureExpected(t *testing.T) {
	detector := DeterministicDetector{}
	mismatch, decided := detector.DetectMismatch(
		context.Background(),
		"expect error",
		json.RawMessage(`{"result":"ok"}`),
		false,
	)
	if !decided {
		t.Fatal("expected decided")
	}
	if !mismatch {
		t.Fatal("failure expectation + success = mismatch")
	}
}

func Test_DeterministicDetector_EmptyResult(t *testing.T) {
	detector := DeterministicDetector{}
	mismatch, decided := detector.DetectMismatch(
		context.Background(),
		"expect empty result",
		json.RawMessage(`null`),
		false,
	)
	if !decided {
		t.Fatal("expected decided")
	}
	if mismatch {
		t.Fatal("empty expectation + null = match")
	}
}

func Test_Engine_ObserveCreatesRecord(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	engine, err := NewEngine(clock, DeterministicDetector{}, 3)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	eventID := uuid.New()
	record, mismatch := engine.Observe(
		context.Background(),
		eventID,
		"fetch",
		json.RawMessage(`{"url":"https://example.com"}`),
		"returns data",
		json.RawMessage(`{"result":"ok"}`),
		false,
	)
	if record.ToolEventID != eventID {
		t.Fatal("event ID mismatch")
	}
	if mismatch {
		t.Fatal("expected match for success+data")
	}
	if record.ComparisonMethod != "deterministic" {
		t.Fatalf("expected deterministic, got %q", record.ComparisonMethod)
	}
}

func Test_LayeredDetector_UsesModelForSemanticMismatch(t *testing.T) {
	generator := &modelDetectorGenerator{}
	engine, err := NewEngine(
		&testClock{now: time.Now().UTC()},
		LayeredDetector{Fallback: ModelDetector{
			Provider: generator,
			Model:    "semantic-comparator",
		}},
		3,
	)
	if err != nil {
		t.Fatal(err)
	}
	record, mismatch := engine.Observe(
		context.Background(),
		uuid.New(),
		"probe",
		json.RawMessage(`{"query":"status"}`),
		"the deployment version is unchanged",
		json.RawMessage(`{"version":"2.0.0"}`),
		false,
	)
	if !mismatch || record.ComparisonMethod != "model-assisted" {
		t.Fatalf("semantic comparison = mismatch %v, record %+v", mismatch, record)
	}
	if len(generator.requests) != 1 ||
		len(generator.requests[0].Tools) != 0 {
		t.Fatalf("model comparator requests = %+v", generator.requests)
	}
}

func Test_Engine_ForcesRevisionAfterThreshold(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	engine, _ := NewEngine(clock, DeterministicDetector{}, 2)
	for i := 0; i < 2; i++ {
		engine.Observe(
			context.Background(),
			uuid.New(),
			"fetch",
			json.RawMessage(`{"url":"https://example.com"}`),
			"returns data",
			json.RawMessage(`null`),
			true,
		)
	}
	if !engine.ShouldForceRevision() {
		t.Fatal("should force revision after 2 mismatches")
	}
}

func Test_PopExpect_MissingExpectIsRejected(t *testing.T) {
	args := json.RawMessage(`{"path":"/tmp"}`)
	if _, _, err := PopExpect(args); err == nil {
		t.Fatal("missing expectation was accepted")
	}
}

func Test_Engine_NilClockRejected(t *testing.T) {
	_, err := NewEngine(nil, DeterministicDetector{}, 3)
	if err == nil {
		t.Fatal("expected error for nil clock")
	}
}

func Test_Engine_NilDetectorRejected(t *testing.T) {
	clock := &testClock{now: time.Now()}
	_, err := NewEngine(clock, nil, 3)
	if err == nil {
		t.Fatal("expected error for nil detector")
	}
}
