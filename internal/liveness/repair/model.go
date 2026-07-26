// Package repair turns verified recovery from failure into explicit learning
// without rewriting protected Identity memory.
package repair

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
)

type Store interface {
	Write(context.Context, memory.Type, []byte, string) (*cortex.Memory, error)
	Resolve(uuid.UUID) (*cortex.Memory, error)
}

type scopedStore interface {
	WriteForActor(context.Context, string, memory.Type, []byte, string) (*cortex.Memory, error)
}

type Clock interface{ Now() time.Time }

type Model struct {
	store Store
	clock Clock
}

func New(store Store, clock Clock) (*Model, error) {
	if store == nil || clock == nil {
		return nil, fmt.Errorf("repair: store and clock are required")
	}
	return &Model{store: store, clock: clock}, nil
}

type Failure struct {
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type Lesson struct {
	Statement       string      `json:"statement"`
	RepairDerived   bool        `json:"repair_derived"`
	SourceFailureID uuid.UUID   `json:"source_failure_id"`
	EvidenceIDs     []uuid.UUID `json:"evidence_ids"`
	LearnedAt       time.Time   `json:"learned_at"`
}

func (model *Model) RecordFailure(
	ctx context.Context,
	description string,
) (uuid.UUID, error) {
	return model.RecordFailureFor(ctx, "operator", description)
}

func (model *Model) RecordFailureFor(
	ctx context.Context,
	actorID string,
	description string,
) (uuid.UUID, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return uuid.Nil, fmt.Errorf("repair: failure description is required")
	}
	payload, err := json.Marshal(Failure{
		Kind: "repair_failure", Description: description,
		OccurredAt: model.clock.Now().UTC(),
	})
	if err != nil {
		return uuid.Nil, err
	}
	stored, err := model.write(ctx, actorID, memory.Event, payload)
	if err != nil {
		return uuid.Nil, err
	}
	return stored.Head.ID, nil
}

func (model *Model) RecordRepair(
	ctx context.Context,
	failureID uuid.UUID,
	lesson string,
	evidenceIDs []uuid.UUID,
) (uuid.UUID, error) {
	return model.RecordRepairFor(ctx, "operator", failureID, lesson, evidenceIDs)
}

func (model *Model) RecordRepairFor(
	ctx context.Context,
	actorID string,
	failureID uuid.UUID,
	lesson string,
	evidenceIDs []uuid.UUID,
) (uuid.UUID, error) {
	failure, err := model.store.Resolve(failureID)
	if err != nil || failure.Head.Type != memory.Event ||
		!strings.Contains(string(failure.Version.Data), "repair_failure") {
		return uuid.Nil, fmt.Errorf("repair: committed failure evidence is required")
	}
	lesson = strings.TrimSpace(lesson)
	evidenceIDs = uniqueIDs(evidenceIDs)
	if lesson == "" || len(evidenceIDs) == 0 {
		return uuid.Nil, fmt.Errorf("repair: lesson and repair evidence are required")
	}
	payload, err := json.Marshal(Lesson{
		Statement: lesson, RepairDerived: true, SourceFailureID: failureID,
		EvidenceIDs: evidenceIDs, LearnedAt: model.clock.Now().UTC(),
	})
	if err != nil {
		return uuid.Nil, err
	}
	stored, err := model.write(ctx, actorID, memory.Belief, payload)
	if err != nil {
		return uuid.Nil, err
	}
	return stored.Head.ID, nil
}

func (model *Model) write(
	ctx context.Context,
	actorID string,
	memoryType memory.Type,
	payload []byte,
) (*cortex.Memory, error) {
	if scoped, ok := model.store.(scopedStore); ok {
		return scoped.WriteForActor(
			ctx, strings.TrimSpace(actorID), memoryType, payload, "repair-model",
		)
	}
	return model.store.Write(ctx, memoryType, payload, "repair-model")
}

func uniqueIDs(values []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{})
	var result []uuid.UUID
	for _, id := range values {
		if id == uuid.Nil {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
