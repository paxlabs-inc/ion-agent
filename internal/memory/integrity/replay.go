// Package integrity derives MMR and SMT state from the encrypted journal.
package integrity

import (
	"context"
	"fmt"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/memory/mmr"
	"github.com/paxlabs-inc/ion-agent/internal/memory/smt"
)

// State is byte-deterministic derived state. The journal remains authoritative.
type State struct {
	MMR    *mmr.MMR
	Forest *smt.Forest
}

// Replay rebuilds all integrity state from authenticated journal ciphertext.
func Replay(ctx context.Context, source *journal.Journal) (*State, error) {
	if source == nil {
		return nil, fmt.Errorf("integrity: journal is required")
	}
	state := &State{MMR: mmr.New(), Forest: smt.NewForest()}
	for _, memoryType := range memory.Types() {
		if _, err := state.Forest.Update(string(memoryType), smt.Hash{}, nil); err != nil {
			return nil, fmt.Errorf("integrity: initialize namespace %s: %w", memoryType, err)
		}
	}
	err := source.Replay(ctx, func(record journal.Record) error {
		key := smt.Key(record.Entry.MemoryID[:])
		if _, err := state.Forest.Update(
			string(record.Entry.MemoryType),
			key,
			record.Ciphertext,
		); err != nil {
			return err
		}
		forestRoot := state.Forest.Root()
		var commitment mmr.Hash
		copy(commitment[:], forestRoot[:])
		if _, _, err := state.MMR.AppendEntry(record.Ciphertext, commitment); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("integrity: replay: %w", err)
	}
	return state, nil
}

// VerifyMMR checks every persisted leaf, not just the final root.
func (state *State) VerifyMMR(persisted *mmr.MMR) error {
	if state == nil || state.MMR == nil || persisted == nil {
		return fmt.Errorf("integrity: both MMRs are required")
	}
	if state.MMR.LeafCount() != persisted.LeafCount() {
		return fmt.Errorf("integrity: MMR leaf count mismatch")
	}
	for index := uint64(0); index < state.MMR.LeafCount(); index++ {
		expected, _ := state.MMR.Leaf(index)
		actual, _ := persisted.Leaf(index)
		if expected != actual {
			return fmt.Errorf("integrity: MMR leaf %d mismatch", index)
		}
	}
	if state.MMR.Root() != persisted.Root() {
		return fmt.Errorf("integrity: MMR root mismatch")
	}
	return nil
}
