// Package aesthetic implements an explicit, user-grounded quality preference
// model rather than prompt-only taste.
package aesthetic

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
)

type Clock interface{ Now() time.Time }

type PreferenceStore interface {
	Write(context.Context, memory.Type, []byte, string) (*cortex.Memory, error)
}

type scopedPreferenceStore interface {
	WriteForActor(context.Context, string, memory.Type, []byte, string) (*cortex.Memory, error)
}

// Criteria is a normalized quality vector.
type Criteria struct {
	Clarity     float64 `json:"clarity"`
	Simplicity  float64 `json:"simplicity"`
	Consistency float64 `json:"consistency"`
	Craft       float64 `json:"craft"`
}

type Judgment struct {
	Score   float64 `json:"score"`
	Opinion string  `json:"opinion"`
}

type Model struct {
	mu      sync.RWMutex
	store   PreferenceStore
	clock   Clock
	weights Criteria
}

func New(store PreferenceStore, clock Clock) (*Model, error) {
	if store == nil || clock == nil {
		return nil, fmt.Errorf("aesthetic: preference store and clock are required")
	}
	return &Model{
		store: store, clock: clock,
		weights: Criteria{Clarity: .3, Simplicity: .3, Consistency: .25, Craft: .15},
	}, nil
}

// Snapshot returns the confirmed selection weights without exposing the
// underlying preference memory.
func (model *Model) Snapshot() Criteria {
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.weights
}

// Restore applies previously confirmed encrypted state during production
// composition. It does not create a new memory because the original user
// confirmation is already durable.
func (model *Model) Restore(weights Criteria) error {
	if err := validate(weights); err != nil {
		return err
	}
	sum := weights.Clarity + weights.Simplicity + weights.Consistency + weights.Craft
	if sum == 0 {
		return fmt.Errorf("aesthetic: at least one weight is required")
	}
	weights.Clarity /= sum
	weights.Simplicity /= sum
	weights.Consistency /= sum
	weights.Craft /= sum
	model.mu.Lock()
	model.weights = weights
	model.mu.Unlock()
	return nil
}

// Judge produces a stable opinion from explicit criteria.
func (model *Model) Judge(criteria Criteria) (Judgment, error) {
	if err := validate(criteria); err != nil {
		return Judgment{}, err
	}
	model.mu.RLock()
	weights := model.weights
	model.mu.RUnlock()
	score := criteria.Clarity*weights.Clarity +
		criteria.Simplicity*weights.Simplicity +
		criteria.Consistency*weights.Consistency +
		criteria.Craft*weights.Craft
	opinion := "needs refinement"
	if score >= .8 {
		opinion = "coherent and well-crafted"
	} else if score >= .6 {
		opinion = "sound with visible rough edges"
	}
	return Judgment{Score: score, Opinion: opinion}, nil
}

// Learn changes taste only from explicit user confirmation and journals the
// resulting Preference memory.
func (model *Model) Learn(
	ctx context.Context,
	label string,
	weights Criteria,
	userConfirmed bool,
) error {
	return model.LearnForActor(ctx, "user", label, weights, userConfirmed)
}

// LearnForActor keeps confirmed taste private to the authenticated actor when
// the production store supports scoped memory writes.
func (model *Model) LearnForActor(
	ctx context.Context,
	actorID string,
	label string,
	weights Criteria,
	userConfirmed bool,
) error {
	if !userConfirmed {
		return fmt.Errorf("aesthetic: user confirmation is required")
	}
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("aesthetic: preference label is required")
	}
	if err := validate(weights); err != nil {
		return err
	}
	sum := weights.Clarity + weights.Simplicity + weights.Consistency + weights.Craft
	if sum == 0 {
		return fmt.Errorf("aesthetic: at least one weight is required")
	}
	weights.Clarity /= sum
	weights.Simplicity /= sum
	weights.Consistency /= sum
	weights.Craft /= sum
	payload, err := json.Marshal(struct {
		Kind      string    `json:"kind"`
		Label     string    `json:"label"`
		Weights   Criteria  `json:"weights"`
		Confirmed bool      `json:"user_confirmed"`
		LearnedAt time.Time `json:"learned_at"`
	}{
		Kind: "aesthetic_preference", Label: strings.TrimSpace(label),
		Weights: weights, Confirmed: true, LearnedAt: model.clock.Now().UTC(),
	})
	if err != nil {
		return err
	}
	var writeErr error
	if scoped, ok := model.store.(scopedPreferenceStore); ok {
		_, writeErr = scoped.WriteForActor(
			ctx, strings.TrimSpace(actorID), memory.Preference, payload, "user",
		)
	} else {
		_, writeErr = model.store.Write(ctx, memory.Preference, payload, "user")
	}
	if writeErr != nil {
		return writeErr
	}
	model.mu.Lock()
	model.weights = weights
	model.mu.Unlock()
	return nil
}

func validate(criteria Criteria) error {
	for _, value := range []float64{
		criteria.Clarity, criteria.Simplicity, criteria.Consistency, criteria.Craft,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("aesthetic: criteria must be within [0,1]")
		}
	}
	return nil
}
