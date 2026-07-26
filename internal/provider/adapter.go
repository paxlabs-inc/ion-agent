// Package provider implements the O1 provider abstraction, concrete provider
// adapters, credential rotation, and provider fallback.
package provider

import (
	"encoding/json"
	"errors"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

var (
	// ErrNoProvider indicates that every configured provider failed.
	ErrNoProvider = errors.New("provider: no provider succeeded")
	// ErrRateLimited indicates that all credentials for a provider returned 429.
	ErrRateLimited = errors.New("provider: credentials rate limited")
	// ErrToolProtocol indicates that a provider returned tool intent in a
	// provider-specific form that could not be normalized safely.
	ErrToolProtocol = errors.New("provider: incompatible tool protocol")
)

// ProviderAdapter translates one provider's wire format to and from O1.
type ProviderAdapter interface {
	Name() string
	TranslateRequest(protocol.GenerationRequest) (json.RawMessage, error)
	TranslateResponse(json.RawMessage) (protocol.NormalizedGeneration, error)
	TranslateStreamEvent([]byte) (protocol.StreamChunk, error)
}

// GenerationFinalizer lets an adapter normalize a fully assembled streaming
// generation. This is required by providers whose serving layer can emit tool
// syntax through content or reasoning instead of structured deltas.
type GenerationFinalizer interface {
	FinalizeGeneration(protocol.NormalizedGeneration) (protocol.NormalizedGeneration, error)
}
