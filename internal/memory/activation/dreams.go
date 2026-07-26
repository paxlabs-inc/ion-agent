package activation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/dreamweaver"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
)

// DreamBeliefs loads Dreamweaver-derived Beliefs into the insertion tier
// between Timeline and Recent.
type DreamBeliefs struct {
	store *cortex.Cortex
}

// NewDreamBeliefs constructs a Cortex-backed Dreams tier source.
func NewDreamBeliefs(store *cortex.Cortex) (*DreamBeliefs, error) {
	if store == nil {
		return nil, fmt.Errorf("activation: Cortex is required for dream beliefs")
	}
	return &DreamBeliefs{store: store}, nil
}

// Entries implements TierSource.
func (source *DreamBeliefs) Entries(
	ctx context.Context,
	_ Request,
) ([]Entry, error) {
	var entries []Entry
	for _, id := range source.store.ListByType(memory.Belief) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved, err := source.store.Resolve(id)
		if err != nil {
			return nil, err
		}
		var belief dreamweaver.DerivedBelief
		if json.Unmarshal(resolved.Version.Data, &belief) != nil ||
			!belief.DreamDerived ||
			strings.TrimSpace(belief.Statement) == "" ||
			len(belief.SourceMemoryIDs) < dreamweaver.MinSupportingMemories {
			continue
		}
		entries = append(entries, Entry{
			Tier: TierDreams,
			Content: fmt.Sprintf(
				"[dream-derived belief %s] %s (sources: %s)",
				id,
				strings.TrimSpace(belief.Statement),
				joinUUIDs(belief.SourceMemoryIDs),
			),
			Salience: 0.7,
		})
	}
	return entries, nil
}

func joinUUIDs(ids []uuid.UUID) string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = id.String()
	}
	return strings.Join(values, ",")
}
