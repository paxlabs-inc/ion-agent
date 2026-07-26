// Package goals turns liveness observations into user-visible proposals only.
package goals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/liveness/curiosity"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/dreamweaver"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/relationship"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
)

type Source string

const (
	SourceCuriosity    Source = "curiosity"
	SourceDreamweaver  Source = "dreamweaver"
	SourceRelationship Source = "relationship"
)

// Proposal is intentionally non-executable. User acceptance belongs to a
// separate interactive workflow.
type Proposal struct {
	ID          uuid.UUID   `json:"id"`
	ActorID     string      `json:"actor_id"`
	Source      Source      `json:"source"`
	Description string      `json:"description"`
	EvidenceIDs []uuid.UUID `json:"evidence_ids,omitempty"`
	Status      string      `json:"status"`
	Executed    bool        `json:"executed"`
	CreatedAt   time.Time   `json:"created_at"`
}

type DreamInput struct {
	ID     uuid.UUID
	Belief dreamweaver.DerivedBelief
}

type Inputs struct {
	Curiosity     []curiosity.Target
	Dreams        []DreamInput
	Relationships []relationship.Snapshot
}

// Sink persists a proposal without dispatching it.
type Sink interface {
	Propose(context.Context, Proposal) (Proposal, error)
}

type Clock interface{ Now() time.Time }

type Generator struct {
	sink  Sink
	clock Clock
}

func New(sink Sink, clock Clock) (*Generator, error) {
	if sink == nil || clock == nil {
		return nil, fmt.Errorf("goals: sink and clock are required")
	}
	return &Generator{sink: sink, clock: clock}, nil
}

// Generate creates proposals from all three required sources. It has no tool
// executor and cannot silently act on a proposal.
func (generator *Generator) Generate(
	ctx context.Context,
	input Inputs,
) ([]Proposal, error) {
	now := generator.clock.Now().UTC()
	var candidates []Proposal
	for _, target := range input.Curiosity {
		if target.Type != curiosity.TargetFringe {
			continue
		}
		candidates = append(candidates, Proposal{
			ActorID:     "operator",
			Source:      SourceCuriosity,
			Description: "Explore knowledge fringe: " + target.Description,
			EvidenceIDs: append([]uuid.UUID(nil), target.SourceMemoryIDs...),
			Status:      "proposed", CreatedAt: now,
		})
	}
	for _, dream := range input.Dreams {
		if !dream.Belief.DreamDerived ||
			strings.TrimSpace(dream.Belief.Statement) == "" {
			continue
		}
		candidates = append(candidates, Proposal{
			ActorID:     "operator",
			Source:      SourceDreamweaver,
			Description: "Investigate recurring pattern: " + dream.Belief.Statement,
			EvidenceIDs: append(
				[]uuid.UUID{dream.ID},
				dream.Belief.SourceMemoryIDs...,
			),
			Status: "proposed", CreatedAt: now,
		})
	}
	for _, snapshot := range input.Relationships {
		if snapshot.Expertise == relationship.Expert {
			continue
		}
		candidates = append(candidates, Proposal{
			ActorID: snapshot.UserID,
			Source:  SourceRelationship,
			Description: fmt.Sprintf(
				"Offer a user-approved learning goal for %s expertise in %s",
				snapshot.Expertise,
				snapshot.Domain,
			),
			Status: "proposed", CreatedAt: now,
		})
	}
	seen := make(map[string]struct{})
	result := make([]Proposal, 0, len(candidates))
	for _, candidate := range candidates {
		key := string(candidate.Source) + "\x00" +
			strings.TrimSpace(candidate.ActorID) + "\x00" +
			strings.ToLower(strings.TrimSpace(candidate.Description))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		proposed, err := generator.sink.Propose(ctx, candidate)
		if err != nil {
			return result, err
		}
		if proposed.Status != "proposed" || proposed.Executed {
			return result, fmt.Errorf("goals: sink violated proposal-only boundary")
		}
		result = append(result, proposed)
	}
	return result, nil
}

// CortexSink durably stores proposals in the Goal namespace.
type CortexSink struct {
	store interface {
		Write(context.Context, memory.Type, []byte, string) (*cortex.Memory, error)
		Update(context.Context, uuid.UUID, []byte, string) (*cortex.Memory, error)
	}
}

func NewCortexSink(store interface {
	Write(context.Context, memory.Type, []byte, string) (*cortex.Memory, error)
	Update(context.Context, uuid.UUID, []byte, string) (*cortex.Memory, error)
}) (*CortexSink, error) {
	if store == nil {
		return nil, fmt.Errorf("goals: Cortex store is required")
	}
	return &CortexSink{store: store}, nil
}

func (sink *CortexSink) Propose(
	ctx context.Context,
	proposal Proposal,
) (Proposal, error) {
	proposal.ID = uuid.Nil
	proposal.Status = "proposed"
	proposal.Executed = false
	proposal.ActorID = strings.TrimSpace(proposal.ActorID)
	if proposal.ActorID == "" {
		return Proposal{}, fmt.Errorf("goals: proposal actor is required")
	}
	if existing, ok := sink.store.(interface {
		ListByType(memory.Type) []uuid.UUID
		Resolve(uuid.UUID) (*cortex.Memory, error)
	}); ok {
		for _, id := range existing.ListByType(memory.Goal) {
			resolved, err := existing.Resolve(id)
			if err != nil {
				return Proposal{}, err
			}
			var durable Proposal
			if json.Unmarshal(resolved.Version.Data, &durable) == nil &&
				durable.Source == proposal.Source &&
				durable.ActorID == proposal.ActorID &&
				strings.EqualFold(
					strings.TrimSpace(durable.Description),
					strings.TrimSpace(proposal.Description),
				) && !durable.Executed {
				return durable, nil
			}
		}
	}
	payload, err := json.Marshal(proposal)
	if err != nil {
		return Proposal{}, err
	}
	var stored *cortex.Memory
	if scoped, ok := sink.store.(interface {
		WriteForActor(
			context.Context, string, memory.Type, []byte, string,
		) (*cortex.Memory, error)
	}); ok {
		stored, err = scoped.WriteForActor(
			ctx, proposal.ActorID, memory.Goal, payload, "goal-generator",
		)
	} else {
		stored, err = sink.store.Write(
			ctx, memory.Goal, payload, "goal-generator",
		)
	}
	if err != nil {
		return Proposal{}, err
	}
	proposal.ID = stored.Head.ID
	payload, err = json.Marshal(proposal)
	if err != nil {
		return Proposal{}, err
	}
	if _, err := sink.store.Update(
		ctx,
		proposal.ID,
		payload,
		"goal-generator",
	); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}
