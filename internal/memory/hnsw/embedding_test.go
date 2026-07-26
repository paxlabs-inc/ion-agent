package hnsw

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestHashEmbedderIsDeterministicAndNormalized(t *testing.T) {
	embedder, err := NewHashEmbedder(64)
	if err != nil {
		t.Fatal(err)
	}
	first, err := embedder.Embed(context.Background(), "Durable memory survives restarts")
	if err != nil {
		t.Fatal(err)
	}
	second, err := embedder.Embed(context.Background(), "Durable memory survives restarts")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || len(second) != 64 {
		t.Fatalf("embedding dimensions = %d, %d", len(first), len(second))
	}
	var norm float64
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("embedding differs at %d", index)
		}
		norm += float64(first[index]) * float64(first[index])
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-5 {
		t.Fatalf("embedding norm = %f", math.Sqrt(norm))
	}
}

func TestHashEmbedderRetainsTokenSimilarity(t *testing.T) {
	embedder, _ := NewHashEmbedder(256)
	query, _ := embedder.Embed(context.Background(), "encrypted memory journal")
	related, _ := embedder.Embed(context.Background(), "memory journal replay")
	unrelated, _ := embedder.Embed(context.Background(), "weather forecast sunshine")
	relatedDistance := cosineDistance(query, vectorNorm(query), related)
	unrelatedDistance := cosineDistance(query, vectorNorm(query), unrelated)
	if relatedDistance >= unrelatedDistance {
		t.Fatalf(
			"related distance %f >= unrelated distance %f",
			relatedDistance,
			unrelatedDistance,
		)
	}
}

func TestHashEmbedderSupportsNonWordJSON(t *testing.T) {
	embedder, _ := NewHashEmbedder(16)
	vector, err := embedder.Embed(context.Background(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateVector(vector, 16); err != nil {
		t.Fatal(err)
	}
}

func TestHashEmbedderRejectsEmptyInput(t *testing.T) {
	embedder, _ := NewHashEmbedder(16)
	if _, err := embedder.Embed(context.Background(), " \n\t"); !errors.Is(err, ErrInvalidVector) {
		t.Fatalf("empty input error = %v", err)
	}
}

func TestHashEmbedderHonorsCancellation(t *testing.T) {
	embedder, _ := NewHashEmbedder(16)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := embedder.Embed(ctx, "memory"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled embed error = %v", err)
	}
}

func TestNewHashEmbedderRejectsInvalidDimensions(t *testing.T) {
	if _, err := NewHashEmbedder(0); err == nil {
		t.Fatal("zero dimensions accepted")
	}
}
