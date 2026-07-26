package premise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

const (
	modelExtractionMaxTokens = 2048
	defaultExtractionTimeout = 30 * time.Second
)

// ErrSemanticExtractionIncomplete marks a bounded model response that could
// not produce a complete tools-free premise set. Layered extraction preserves
// deterministic premises and records this limitation explicitly.
var ErrSemanticExtractionIncomplete = errors.New(
	"premise: semantic extraction incomplete",
)

// Generator is the normalized, tools-free provider boundary used for semantic
// premise extraction.
type Generator interface {
	Generate(context.Context, protocol.GenerationRequest) (protocol.NormalizedGeneration, error)
}

// ModelExtractor extracts factual, load-bearing assumptions that deterministic
// syntax rules cannot recognize.
type ModelExtractor struct {
	Provider Generator
	Model    string
	Timeout  time.Duration
}

// Extract asks the configured model for strict JSON and treats every returned
// statement as an assumption until separately verified by a citation.
func (extractor ModelExtractor) Extract(
	ctx context.Context,
	plan Plan,
) ([]Premise, error) {
	if extractor.Provider == nil {
		return nil, fmt.Errorf("premise: model extractor provider is required")
	}
	if strings.TrimSpace(extractor.Model) == "" {
		return nil, fmt.Errorf("premise: model extractor model is required")
	}
	timeout := extractor.Timeout
	if timeout <= 0 {
		timeout = defaultExtractionTimeout
	}
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("premise: encode plan: %w", err)
	}
	extractionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	generation, err := extractor.Provider.Generate(extractionCtx, protocol.GenerationRequest{
		Model: extractor.Model,
		Messages: []protocol.Message{
			{
				Role: protocol.RoleSystem,
				Content: "Extract only load-bearing factual premises required for this plan to work. " +
					"Do not include goals, commands, or predictions already present in tool expect fields. " +
					"Return only JSON: {\"premises\":[\"fact one\",\"fact two\"]}. " +
					"Use an empty array when no factual premise exists.",
			},
			{Role: protocol.RoleUser, Content: string(encodedPlan)},
		},
		MaxOutputTokens: modelExtractionMaxTokens,
	})
	if err != nil {
		if errors.Is(extractionCtx.Err(), context.DeadlineExceeded) &&
			ctx.Err() == nil {
			return nil, fmt.Errorf(
				"%w: auxiliary extraction exceeded %s",
				ErrSemanticExtractionIncomplete,
				timeout,
			)
		}
		return nil, fmt.Errorf("premise: model extraction: %w", err)
	}
	if err := generation.Validate(); err != nil {
		return nil, fmt.Errorf("premise: invalid model extraction: %w", err)
	}
	if generation.FinishReason != protocol.FinishStop ||
		len(generation.ToolCalls) != 0 {
		return nil, fmt.Errorf(
			"%w: finish_reason=%q tool_calls=%d",
			ErrSemanticExtractionIncomplete,
			generation.FinishReason,
			len(generation.ToolCalls),
		)
	}
	content := strings.TrimSpace(generation.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var decoded struct {
		Premises []string `json:"premises"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decoded); err != nil {
		return nil, fmt.Errorf("premise: decode model extraction: %w", err)
	}
	result := make([]Premise, 0, len(decoded.Premises))
	seen := make(map[string]struct{})
	for _, statement := range decoded.Premises {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		key := strings.ToLower(statement)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, Premise{
			Statement: statement,
			Source:    SourceAssumption,
		})
	}
	return result, nil
}

// LayeredExtractor combines deterministic tool/self claims with semantic model
// extraction and de-duplicates the resulting assumption set.
type LayeredExtractor struct {
	Deterministic Extractor
	Model         Extractor
}

// Extract runs both rails. A model failure is returned because silently
// omitting load-bearing premises would make the ledger incomplete.
func (extractor LayeredExtractor) Extract(
	ctx context.Context,
	plan Plan,
) ([]Premise, error) {
	deterministic := extractor.Deterministic
	if deterministic == nil {
		deterministic = DeterministicExtractor{}
	}
	base, err := deterministic.Extract(ctx, plan)
	if err != nil {
		return nil, err
	}
	if extractor.Model == nil {
		return base, nil
	}
	semantic, err := extractor.Model.Extract(ctx, plan)
	if err != nil {
		if !errors.Is(err, ErrSemanticExtractionIncomplete) {
			return nil, err
		}
		semantic = []Premise{{
			Statement: "Semantic premise extraction was incomplete; " +
				"additional load-bearing assumptions may remain unrecorded",
			Source: SourceAssumption,
		}}
	}
	result := make([]Premise, 0, len(base)+len(semantic))
	seen := make(map[string]struct{})
	for _, candidate := range append(base, semantic...) {
		key := strings.ToLower(strings.TrimSpace(candidate.Statement))
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, candidate)
	}
	return result, nil
}
