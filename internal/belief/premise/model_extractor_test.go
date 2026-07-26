package premise

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type extractorGeneratorFunc func(
	context.Context,
	protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error)

func (generate extractorGeneratorFunc) Generate(
	ctx context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	return generate(ctx, request)
}

type extractorFunc func(context.Context, Plan) ([]Premise, error)

func (extract extractorFunc) Extract(
	ctx context.Context,
	plan Plan,
) ([]Premise, error) {
	return extract(ctx, plan)
}

func TestModelExtractorReservesReasoningBudget(t *testing.T) {
	t.Parallel()
	var observed protocol.GenerationRequest
	extractor := ModelExtractor{
		Model: "reasoning-model",
		Provider: extractorGeneratorFunc(func(
			_ context.Context,
			request protocol.GenerationRequest,
		) (protocol.NormalizedGeneration, error) {
			observed = request
			return protocol.NormalizedGeneration{
				Content:      `{"premises":["README.md exists in the workspace"]}`,
				FinishReason: protocol.FinishStop,
			}, nil
		}),
	}

	extracted, err := extractor.Extract(context.Background(), Plan{
		Text: "Inspect README.md.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.MaxOutputTokens != modelExtractionMaxTokens {
		t.Fatalf(
			"max output tokens = %d, want %d",
			observed.MaxOutputTokens,
			modelExtractionMaxTokens,
		)
	}
	if len(observed.Tools) != 0 {
		t.Fatalf("model extraction exposed %d tools", len(observed.Tools))
	}
	if len(extracted) != 1 ||
		extracted[0].Statement != "README.md exists in the workspace" {
		t.Fatalf("extracted premises = %+v", extracted)
	}
}

func TestLayeredExtractorRecordsBoundedSemanticPartial(t *testing.T) {
	t.Parallel()
	extractor := LayeredExtractor{
		Deterministic: DeterministicExtractor{},
		Model: extractorFunc(func(
			context.Context,
			Plan,
		) ([]Premise, error) {
			return nil, fmt.Errorf(
				"%w: finish_reason=%q",
				ErrSemanticExtractionIncomplete,
				protocol.FinishLength,
			)
		}),
	}

	extracted, err := extractor.Extract(context.Background(), Plan{
		ToolCalls: []protocol.NormalizedToolCall{{
			ID:   "call",
			Name: "memory_save",
			Arguments: []byte(
				`{"expect":"the memory will be stored"}`,
			),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(extracted) != 2 {
		t.Fatalf("extracted premises = %+v", extracted)
	}
	if extracted[0].Statement != "memory_save: the memory will be stored" ||
		extracted[1].Statement !=
			"Semantic premise extraction was incomplete; additional load-bearing assumptions may remain unrecorded" {
		t.Fatalf("extracted premises = %+v", extracted)
	}
}

func TestModelExtractorBoundsAuxiliaryCognition(t *testing.T) {
	t.Parallel()
	extractor := ModelExtractor{
		Model:   "reasoning-model",
		Timeout: 10 * time.Millisecond,
		Provider: extractorGeneratorFunc(func(
			ctx context.Context,
			_ protocol.GenerationRequest,
		) (protocol.NormalizedGeneration, error) {
			<-ctx.Done()
			return protocol.NormalizedGeneration{}, ctx.Err()
		}),
	}
	_, err := extractor.Extract(context.Background(), Plan{Text: "Inspect source."})
	if !errors.Is(err, ErrSemanticExtractionIncomplete) {
		t.Fatalf("Extract() error = %v, want bounded semantic partial", err)
	}
}

func TestLayeredExtractorDoesNotMaskProviderFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("provider unavailable")
	extractor := LayeredExtractor{
		Model: extractorFunc(func(
			context.Context,
			Plan,
		) ([]Premise, error) {
			return nil, want
		}),
	}
	if _, err := extractor.Extract(context.Background(), Plan{}); !errors.Is(err, want) {
		t.Fatalf("Extract() error = %v, want %v", err, want)
	}
}
