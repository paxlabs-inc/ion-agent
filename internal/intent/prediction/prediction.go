// Package prediction implements prediction-carrying dispatch. Every tool call
// carries a stated expectation; mismatch between prediction and outcome is a
// first-class belief-update event. Mismatch meters are scoped to one exact
// action within one turn, and N consecutive mismatches force revision.
package prediction

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"lukechampine.com/blake3"
)

const (
	DefaultMismatchThreshold = 3
)

type Clock interface {
	Now() time.Time
}

type MismatchDetector interface {
	DetectMismatch(ctx context.Context, expect string, result json.RawMessage, isError bool) (mismatch bool, decided bool)
}

// Detection is the auditable outcome of one expectation comparison.
type Detection struct {
	Mismatch bool
	Decided  bool
	Method   string
}

// DetailedMismatchDetector reports which comparison rail reached a decision.
type DetailedMismatchDetector interface {
	DetectMismatchDetailed(
		ctx context.Context,
		expect string,
		result json.RawMessage,
		isError bool,
	) Detection
}

// Generator is the normalized, tools-free model boundary used for ambiguous
// semantic comparisons.
type Generator interface {
	Generate(context.Context, protocol.GenerationRequest) (protocol.NormalizedGeneration, error)
}

type DeterministicDetector struct{}

// LayeredDetector uses deterministic comparison first and delegates ambiguous
// natural-language expectations to a model-assisted detector.
type LayeredDetector struct {
	Fallback MismatchDetector
}

func (detector LayeredDetector) DetectMismatch(
	ctx context.Context,
	expect string,
	result json.RawMessage,
	isError bool,
) (bool, bool) {
	detection := detector.DetectMismatchDetailed(ctx, expect, result, isError)
	return detection.Mismatch, detection.Decided
}

// DetectMismatchDetailed uses deterministic rules first and invokes the
// configured semantic comparator only when those rules cannot decide.
func (detector LayeredDetector) DetectMismatchDetailed(
	ctx context.Context,
	expect string,
	result json.RawMessage,
	isError bool,
) Detection {
	mismatch, decided := (DeterministicDetector{}).DetectMismatch(
		ctx, expect, result, isError,
	)
	if decided || detector.Fallback == nil {
		return Detection{
			Mismatch: mismatch,
			Decided:  decided,
			Method:   "deterministic",
		}
	}
	if detailed, ok := detector.Fallback.(DetailedMismatchDetector); ok {
		return detailed.DetectMismatchDetailed(ctx, expect, result, isError)
	}
	mismatch, decided = detector.Fallback.DetectMismatch(
		ctx, expect, result, isError,
	)
	return Detection{
		Mismatch: mismatch,
		Decided:  decided,
		Method:   "model-assisted",
	}
}

var httpStatusPattern = regexp.MustCompile(`(?i)\b([1-5]\d{2})\b`)

func (DeterministicDetector) DetectMismatch(
	_ context.Context,
	expect string,
	result json.RawMessage,
	isError bool,
) (bool, bool) {
	expect = strings.TrimSpace(expect)
	if expect == "" {
		return false, true
	}
	expectLower := strings.ToLower(expect)
	if isError && predictsFailure(expectLower) {
		return false, true
	}
	if isError && !predictsFailure(expectLower) {
		return true, true
	}
	if !isError && predictsFailure(expectLower) {
		return true, true
	}
	expectedStatus := httpStatusPattern.FindString(expectLower)
	if expectedStatus != "" {
		resultStr := string(result)
		if strings.Contains(resultStr, expectedStatus) {
			return false, true
		}
	}
	if predictsNonEmpty(expectLower) {
		trimmed := strings.TrimSpace(string(result))
		if trimmed != "" && trimmed != "null" && trimmed != "{}" && trimmed != "[]" {
			return false, true
		}
		return true, true
	}
	if predictsEmpty(expectLower) {
		trimmed := strings.TrimSpace(string(result))
		if trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "[]" {
			return false, true
		}
		return true, true
	}
	return false, false
}

func predictsFailure(expect string) bool {
	return strings.Contains(expect, "error") ||
		strings.Contains(expect, "fail") ||
		strings.Contains(expect, "reject") ||
		strings.Contains(expect, "denied") ||
		strings.Contains(expect, "not found") ||
		strings.Contains(expect, "404") ||
		strings.Contains(expect, "500") ||
		strings.Contains(expect, "timeout")
}

func predictsEmpty(expect string) bool {
	if strings.Contains(expect, "non-empty") || strings.Contains(expect, "nonempty") {
		return false
	}
	return strings.Contains(expect, "empty") ||
		strings.Contains(expect, "no result") ||
		strings.Contains(expect, "null")
}

func predictsNonEmpty(expect string) bool {
	if predictsEmpty(expect) {
		return false
	}
	return strings.Contains(expect, "non-empty") ||
		strings.Contains(expect, "nonempty") ||
		strings.Contains(expect, "return") ||
		strings.Contains(expect, "found") ||
		strings.Contains(expect, "success")
}

// ModelDetector asks an O1 provider for a strict semantic comparison when
// deterministic status/error/emptiness rules do not apply.
type ModelDetector struct {
	Provider Generator
	Model    string
}

// DetectMismatch implements MismatchDetector.
func (detector ModelDetector) DetectMismatch(
	ctx context.Context,
	expect string,
	result json.RawMessage,
	isError bool,
) (bool, bool) {
	detection := detector.DetectMismatchDetailed(ctx, expect, result, isError)
	return detection.Mismatch, detection.Decided
}

// DetectMismatchDetailed performs a real normalized provider call with no tool
// surface and accepts only a strict {"mismatch":bool} response.
func (detector ModelDetector) DetectMismatchDetailed(
	ctx context.Context,
	expect string,
	result json.RawMessage,
	isError bool,
) Detection {
	undecided := Detection{Method: "model-assisted"}
	if detector.Provider == nil || strings.TrimSpace(detector.Model) == "" ||
		strings.TrimSpace(expect) == "" || !json.Valid(result) {
		return undecided
	}
	payload, err := json.Marshal(struct {
		Expectation string          `json:"expectation"`
		Result      json.RawMessage `json:"result"`
		IsError     bool            `json:"is_error"`
	}{
		Expectation: expect,
		Result:      result,
		IsError:     isError,
	})
	if err != nil {
		return undecided
	}
	generation, err := detector.Provider.Generate(ctx, protocol.GenerationRequest{
		Model: detector.Model,
		Messages: []protocol.Message{
			{
				Role: protocol.RoleSystem,
				Content: "Compare the stated expectation with the observed tool result. " +
					"Return only JSON in the form {\"mismatch\":true} or " +
					"{\"mismatch\":false}.",
			},
			{Role: protocol.RoleUser, Content: string(payload)},
		},
		MaxOutputTokens: 32,
	})
	if err != nil || generation.Validate() != nil ||
		len(generation.ToolCalls) != 0 {
		return undecided
	}
	var decoded struct {
		Mismatch *bool `json:"mismatch"`
	}
	content := strings.TrimSpace(generation.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decoded); err != nil ||
		decoded.Mismatch == nil {
		return undecided
	}
	return Detection{
		Mismatch: *decoded.Mismatch,
		Decided:  true,
		Method:   "model-assisted",
	}
}

func StrategyKey(toolName string, args json.RawMessage) string {
	normalized := append(json.RawMessage(nil), args...)
	var parsed any
	if json.Unmarshal(args, &parsed) == nil {
		if canonical, err := json.Marshal(parsed); err == nil {
			normalized = canonical
		}
	}
	digest := blake3.Sum256(normalized)
	return strings.TrimSpace(toolName) + "@" + fmt.Sprintf("%x", digest[:16])
}

type Meter struct {
	mu              sync.Mutex
	counts          map[string]int
	threshold       int
	lastStrategyKey string
}

// State is the durable prediction mismatch meter.
type State struct {
	Counts    map[string]int `json:"counts"`
	Threshold int            `json:"threshold"`
}

func NewMeter(threshold int) *Meter {
	if threshold <= 0 {
		threshold = DefaultMismatchThreshold
	}
	return &Meter{
		counts:    make(map[string]int),
		threshold: threshold,
	}
}

// BeginTurn prevents a mismatch from one user request from influencing a later
// request while retaining same-turn recovery across provider attempts.
func (meter *Meter) BeginTurn() {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	meter.counts = make(map[string]int)
	meter.lastStrategyKey = ""
}

func (meter *Meter) Record(strategyKey string, matched bool) int {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	meter.lastStrategyKey = strategyKey
	if matched {
		meter.counts[strategyKey] = 0
		return 0
	}
	meter.counts[strategyKey]++
	return meter.counts[strategyKey]
}

func (meter *Meter) ShouldForceRevision() bool {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if meter.lastStrategyKey == "" {
		return false
	}
	return meter.counts[meter.lastStrategyKey] >= meter.threshold
}

func (meter *Meter) Count(strategyKey string) int {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	return meter.counts[strategyKey]
}

type Engine struct {
	clock     Clock
	detector  MismatchDetector
	meter     *Meter
	threshold int
}

func NewEngine(clock Clock, detector MismatchDetector, threshold int) (*Engine, error) {
	if clock == nil {
		return nil, fmt.Errorf("prediction: clock is required")
	}
	if detector == nil {
		return nil, fmt.Errorf("prediction: detector is required")
	}
	if threshold <= 0 {
		threshold = DefaultMismatchThreshold
	}
	return &Engine{
		clock:     clock,
		detector:  detector,
		meter:     NewMeter(threshold),
		threshold: threshold,
	}, nil
}

// RestoreEngine reconstructs prediction state while retaining the live
// detector boundary supplied by the production composition root.
func RestoreEngine(
	clock Clock,
	detector MismatchDetector,
	state State,
) (*Engine, error) {
	engine, err := NewEngine(clock, detector, state.Threshold)
	if err != nil {
		return nil, err
	}
	for key, count := range state.Counts {
		if strings.TrimSpace(key) == "" || count < 0 {
			return nil, fmt.Errorf("prediction: invalid durable meter state")
		}
		engine.meter.counts[key] = count
	}
	return engine, nil
}

// Snapshot returns a defensive copy of the mismatch meter.
func (engine *Engine) Snapshot() State {
	engine.meter.mu.Lock()
	defer engine.meter.mu.Unlock()
	counts := make(map[string]int, len(engine.meter.counts))
	for key, count := range engine.meter.counts {
		counts[key] = count
	}
	return State{Counts: counts, Threshold: engine.threshold}
}

func InjectExpectParam(schemas []protocol.ToolDefinition) []protocol.ToolDefinition {
	result := make([]protocol.ToolDefinition, len(schemas))
	for i, schema := range schemas {
		result[i] = schema
		var params map[string]json.RawMessage
		if err := json.Unmarshal(schema.Parameters, &params); err != nil {
			continue
		}
		if params == nil {
			params = make(map[string]json.RawMessage)
		}
		var properties map[string]json.RawMessage
		if raw, ok := params["properties"]; ok {
			if err := json.Unmarshal(raw, &properties); err != nil {
				continue
			}
		}
		if properties == nil {
			properties = make(map[string]json.RawMessage)
		}
		properties["expect"] = json.RawMessage(`{"type":"string","minLength":1,"description":"One-line prediction of what this tool will return"}`)
		encodedProperties, err := json.Marshal(properties)
		if err != nil {
			continue
		}
		params["properties"] = encodedProperties
		if required, ok := params["required"]; ok {
			var list []string
			if err := json.Unmarshal(required, &list); err == nil {
				hasExpect := false
				for _, r := range list {
					if r == "expect" {
						hasExpect = true
						break
					}
				}
				if !hasExpect {
					list = append(list, "expect")
					params["required"], _ = json.Marshal(list)
				}
			}
		} else {
			list := []string{"expect"}
			params["required"], _ = json.Marshal(list)
		}
		modified, err := json.Marshal(params)
		if err != nil {
			continue
		}
		result[i].Parameters = modified
	}
	return result
}

func PopExpect(args json.RawMessage) (string, json.RawMessage, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(args, &parsed); err != nil {
		return "", args, fmt.Errorf("prediction: parse args: %w", err)
	}
	expectRaw, hasExpect := parsed["expect"]
	delete(parsed, "expect")
	stripped, err := json.Marshal(parsed)
	if err != nil {
		return "", args, fmt.Errorf("prediction: strip expect: %w", err)
	}
	if !hasExpect {
		return "", stripped, fmt.Errorf("prediction: expect parameter is required")
	}
	var expect string
	if err := json.Unmarshal(expectRaw, &expect); err != nil {
		return "", stripped, fmt.Errorf("prediction: expect parameter must be a string")
	}
	expect = strings.TrimSpace(expect)
	if expect == "" {
		return "", stripped, fmt.Errorf("prediction: expect parameter cannot be empty")
	}
	return expect, stripped, nil
}

func (engine *Engine) Observe(
	ctx context.Context,
	eventID uuid.UUID,
	toolName string,
	args json.RawMessage,
	expect string,
	result json.RawMessage,
	isError bool,
) (protocol.PredictionRecord, bool) {
	strategyKey := StrategyKey(toolName, args)
	detection := Detection{Method: "deterministic"}
	if detailed, ok := engine.detector.(DetailedMismatchDetector); ok {
		detection = detailed.DetectMismatchDetailed(
			ctx, expect, result, isError,
		)
	} else {
		detection.Mismatch, detection.Decided =
			engine.detector.DetectMismatch(ctx, expect, result, isError)
	}
	mismatch, decided := detection.Mismatch, detection.Decided
	if !decided {
		// An unclassified semantic result must not be silently promoted to a
		// successful prediction.
		mismatch = true
	}
	matched := !mismatch
	count := engine.meter.Record(strategyKey, matched)
	method := detection.Method
	if method == "" {
		method = "deterministic"
	}
	if !decided {
		method = "undetermined"
	}
	record := protocol.PredictionRecord{
		ToolEventID:         eventID,
		StrategyKey:         strategyKey,
		Expectation:         expect,
		Matched:             matched,
		ComparisonMethod:    method,
		ConsecutiveMismatch: count,
		Timestamp:           engine.clock.Now(),
	}
	digest := blake3.Sum256(result)
	record.ObservedResultDigest = fmt.Sprintf("%x", digest[:])
	return record, mismatch
}

func (engine *Engine) ShouldForceRevision() bool {
	return engine.meter.ShouldForceRevision()
}

func (engine *Engine) BeginTurn() {
	engine.meter.BeginTurn()
}

func (engine *Engine) MeterCount(strategyKey string) int {
	return engine.meter.Count(strategyKey)
}
