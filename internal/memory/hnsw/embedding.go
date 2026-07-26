package hnsw

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"

	"lukechampine.com/blake3"
)

const DefaultEmbeddingDimensions = 384

// Embedder generates a stable dense vector for memory content.
type Embedder interface {
	Dimensions() int
	Embed(context.Context, string) ([]float32, error)
}

// HashEmbedder is a deterministic, local feature-hashing embedder. It avoids a
// network dependency during startup and fallback while retaining token and
// adjacent-token similarity. Deployments can supply a model-backed Embedder
// through the same interface without changing indexing code.
type HashEmbedder struct {
	dimensions int
}

// NewHashEmbedder creates a local embedding generator.
func NewHashEmbedder(dimensions int) (*HashEmbedder, error) {
	if dimensions <= 0 {
		return nil, fmt.Errorf("hnsw: embedding dimensions must be positive")
	}
	return &HashEmbedder{dimensions: dimensions}, nil
}

func (embedder *HashEmbedder) Dimensions() int {
	return embedder.dimensions
}

// Embed hashes lowercase word features and adjacent word pairs, then L2
// normalizes the result for cosine search.
func (embedder *HashEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tokens := tokenize(text)
	if len(tokens) == 0 {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("%w: embedding input has no tokens", ErrInvalidVector)
		}
		tokens = []string{"raw:" + text}
	}
	vector := make([]float32, embedder.dimensions)
	for index, token := range tokens {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		addFeature(vector, "u:"+token, 1)
		if index != 0 {
			addFeature(vector, "b:"+tokens[index-1]+"\x00"+token, 0.75)
		}
	}
	var norm float64
	for _, value := range vector {
		norm += float64(value) * float64(value)
	}
	if norm == 0 {
		return nil, fmt.Errorf("%w: embedding magnitude is zero", ErrInvalidVector)
	}
	scale := float32(1 / math.Sqrt(norm))
	for index := range vector {
		vector[index] *= scale
	}
	return vector, nil
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsNumber(character)
	})
}

func addFeature(vector []float32, feature string, weight float32) {
	sum := blake3.Sum256([]byte(feature))
	bucket := uint64(sum[0])<<56 |
		uint64(sum[1])<<48 |
		uint64(sum[2])<<40 |
		uint64(sum[3])<<32 |
		uint64(sum[4])<<24 |
		uint64(sum[5])<<16 |
		uint64(sum[6])<<8 |
		uint64(sum[7])
	sign := weight
	if sum[8]&1 != 0 {
		sign = -sign
	}
	vector[bucket%uint64(len(vector))] += sign
}
