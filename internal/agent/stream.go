package agent

import (
	"context"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// StreamingGenerator is the optional provider contract used when incremental
// delivery is available. Generate remains the compatibility path for providers
// and deterministic tests that only support complete responses.
type StreamingGenerator interface {
	GenerateStream(
		context.Context,
		protocol.GenerationRequest,
		func(protocol.StreamChunk) error,
	) (protocol.NormalizedGeneration, error)
}

// GenerationObserver receives user-visible deltas for the active provider
// attempt. Reset removes only a discarded repair attempt. Valid commentary
// emitted before a structured tool call remains visible while work continues.
type GenerationObserver interface {
	ContentDelta(context.Context, string) error
	ReasoningDelta(context.Context, string) error
	Reset(context.Context) error
}

type safeReasoningProgressObserver interface {
	ReasoningProgress(context.Context) error
}

type generationAttemptCommitter interface {
	CommitAttempt(context.Context) error
}

type generationObserverKey struct{}

// WithGenerationObserver attaches a turn-scoped incremental event sink.
func WithGenerationObserver(
	ctx context.Context,
	observer GenerationObserver,
) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, generationObserverKey{}, observer)
}

func generationObserver(ctx context.Context) GenerationObserver {
	observer, _ := ctx.Value(generationObserverKey{}).(GenerationObserver)
	return observer
}

func withoutGenerationObserver(ctx context.Context) context.Context {
	return context.WithValue(ctx, generationObserverKey{}, struct{}{})
}

type generationStreamState struct {
	content   bool
	reasoning bool
}

func (state generationStreamState) any() bool {
	return state.content || state.reasoning
}

func (loop *Loop) generate(
	ctx context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, generationStreamState, error) {
	streamer, supportsStreaming := loop.provider.(StreamingGenerator)
	observer := generationObserver(ctx)
	if observer == nil {
		generation, err := loop.provider.Generate(ctx, request)
		return generation, generationStreamState{}, err
	}
	if !supportsStreaming {
		generation, err := loop.provider.Generate(ctx, request)
		if err != nil {
			return generation, generationStreamState{}, err
		}
		state := generationStreamState{}
		if generation.Content != "" {
			if err := observer.ContentDelta(ctx, generation.Content); err != nil {
				return protocol.NormalizedGeneration{}, state, err
			}
			state.content = true
		}
		if generation.Reasoning != "" {
			if progress, ok := observer.(safeReasoningProgressObserver); ok {
				if err := progress.ReasoningProgress(ctx); err != nil {
					return protocol.NormalizedGeneration{}, state, err
				}
				state.reasoning = true
			}
		}
		return generation, state, nil
	}
	request.Stream = true
	state := generationStreamState{}
	generation, err := streamer.GenerateStream(
		ctx, request, func(chunk protocol.StreamChunk) error {
			if chunk.ReasoningDelta != "" {
				if progress, ok := observer.(safeReasoningProgressObserver); ok {
					if err := progress.ReasoningProgress(ctx); err != nil {
						return err
					}
					state.reasoning = true
				}
			}
			if chunk.ContentDelta != "" {
				if err := observer.ContentDelta(ctx, chunk.ContentDelta); err != nil {
					return err
				}
				state.content = true
			}
			return nil
		},
	)
	return generation, state, err
}

func commitGenerationObserver(ctx context.Context) error {
	observer := generationObserver(ctx)
	committer, ok := observer.(generationAttemptCommitter)
	if !ok {
		return nil
	}
	return committer.CommitAttempt(ctx)
}

func resetGenerationObserver(ctx context.Context) error {
	observer := generationObserver(ctx)
	if observer == nil {
		return nil
	}
	return observer.Reset(ctx)
}
