package dreamweaver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// Generator is the normalized provider boundary used for pattern discovery.
type Generator interface {
	Generate(context.Context, protocol.GenerationRequest) (protocol.NormalizedGeneration, error)
}

// ModelPatternGenerator finds structural similarity through a tools-free O1
// request. Engine-side validation remains authoritative.
type ModelPatternGenerator struct {
	Provider Generator
	Model    string
}

// Generate returns strict JSON candidate insights.
func (generator ModelPatternGenerator) Generate(
	ctx context.Context,
	candidates []Candidate,
	limit int,
) ([]ProposedInsight, error) {
	if generator.Provider == nil || strings.TrimSpace(generator.Model) == "" {
		return nil, fmt.Errorf("dreamweaver: provider and model are required")
	}
	if len(candidates) < MinSupportingMemories || limit <= 0 {
		return nil, nil
	}
	payload, err := json.Marshal(struct {
		Candidates  []Candidate `json:"candidates"`
		MaxInsights int         `json:"max_insights"`
	}{
		Candidates:  append([]Candidate(nil), candidates...),
		MaxInsights: min(limit, MaxBeliefsPerCycle),
	})
	if err != nil {
		return nil, err
	}
	generation, err := generator.Provider.Generate(ctx, protocol.GenerationRequest{
		Model: generator.Model,
		Messages: []protocol.Message{
			{
				Role: protocol.RoleSystem,
				Content: "Find meaningful structural patterns across unrelated memory domains. " +
					"Return only JSON {\"insights\":[{\"statement\":string," +
					"\"source_memory_ids\":[uuid,...]}]}. Every insight must cite at least " +
					"three supplied memories spanning at least two domains. Do not call tools.",
			},
			{Role: protocol.RoleUser, Content: string(payload)},
		},
		MaxOutputTokens: 1024,
	})
	if err != nil {
		return nil, err
	}
	if err := generation.Validate(); err != nil {
		return nil, fmt.Errorf("dreamweaver: invalid generation: %w", err)
	}
	if len(generation.ToolCalls) != 0 {
		return nil, fmt.Errorf("dreamweaver: provider attempted a tool call")
	}
	content := strings.TrimSpace(generation.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var response struct {
		Insights []ProposedInsight `json:"insights"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &response); err != nil {
		return nil, fmt.Errorf("dreamweaver: invalid insight JSON: %w", err)
	}
	return response.Insights, nil
}
