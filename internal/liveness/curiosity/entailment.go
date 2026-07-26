package curiosity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// Generator is the normalized tools-free provider boundary.
type Generator interface {
	Generate(context.Context, protocol.GenerationRequest) (protocol.NormalizedGeneration, error)
}

// ModelEntailmentScorer performs pairwise consistency checks through O1.
type ModelEntailmentScorer struct {
	Provider Generator
	Model    string
}

// Score returns a strict [0,1] consistency/entailment score.
func (scorer ModelEntailmentScorer) Score(
	ctx context.Context,
	left string,
	right string,
) (float64, error) {
	if scorer.Provider == nil || strings.TrimSpace(scorer.Model) == "" {
		return 0, fmt.Errorf("curiosity: entailment provider and model are required")
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return 0, fmt.Errorf("curiosity: entailment statements are required")
	}
	payload, err := json.Marshal(struct {
		Left  string `json:"left"`
		Right string `json:"right"`
	}{Left: left, Right: right})
	if err != nil {
		return 0, err
	}
	generation, err := scorer.Provider.Generate(ctx, protocol.GenerationRequest{
		Model: scorer.Model,
		Messages: []protocol.Message{
			{
				Role: protocol.RoleSystem,
				Content: "Determine whether both memory statements can simultaneously be true. " +
					"Return only JSON {\"score\":number}, where 0 is incompatible and 1 is fully consistent.",
			},
			{Role: protocol.RoleUser, Content: string(payload)},
		},
		MaxOutputTokens: 32,
	})
	if err != nil {
		return 0, err
	}
	if err := generation.Validate(); err != nil {
		return 0, fmt.Errorf("curiosity: invalid entailment generation: %w", err)
	}
	if len(generation.ToolCalls) != 0 {
		return 0, fmt.Errorf("curiosity: entailment provider attempted a tool call")
	}
	content := strings.TrimSpace(generation.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var response struct {
		Score *float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &response); err != nil ||
		response.Score == nil {
		return 0, fmt.Errorf("curiosity: entailment provider returned invalid JSON")
	}
	if math.IsNaN(*response.Score) || math.IsInf(*response.Score, 0) ||
		*response.Score < 0 || *response.Score > 1 {
		return 0, fmt.Errorf("curiosity: entailment score must be within [0,1]")
	}
	return *response.Score, nil
}
