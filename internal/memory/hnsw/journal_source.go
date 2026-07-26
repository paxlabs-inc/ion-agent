package hnsw

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"lukechampine.com/blake3"

	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
)

// VectorSource reconstructs the live vector set from an authoritative source.
type VectorSource interface {
	Snapshot(context.Context) ([]Vector, error)
}

// CortexJournalSource replays latest live Cortex content and generates its
// embeddings. Archive records remove entries, so rebuild never resurrects a
// tombstoned memory.
type CortexJournalSource struct {
	journal  *journal.Journal
	embedder Embedder
}

func NewCortexJournalSource(
	source *journal.Journal,
	embedder Embedder,
) (*CortexJournalSource, error) {
	if source == nil || embedder == nil {
		return nil, fmt.Errorf("hnsw: journal and embedder are required")
	}
	return &CortexJournalSource{journal: source, embedder: embedder}, nil
}

func (source *CortexJournalSource) Snapshot(ctx context.Context) ([]Vector, error) {
	type liveMemory struct {
		id      uuid.UUID
		content []byte
	}
	live := make(map[uuid.UUID]liveMemory)
	if err := source.journal.Replay(ctx, func(record journal.Record) error {
		switch record.Entry.Type {
		case journal.Store, journal.Update:
			live[record.Entry.MemoryID] = liveMemory{
				id:      record.Entry.MemoryID,
				content: append([]byte(nil), record.Entry.Content...),
			}
		case journal.Archive:
			delete(live, record.Entry.MemoryID)
		default:
			return fmt.Errorf("hnsw: unsupported journal mutation %q", record.Entry.Type)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("hnsw: replay vector journal source: %w", err)
	}

	memories := make([]liveMemory, 0, len(live))
	for _, memory := range live {
		memories = append(memories, memory)
	}
	sort.Slice(memories, func(left, right int) bool {
		return string(memories[left].id[:]) < string(memories[right].id[:])
	})
	vectors := make([]Vector, 0, len(memories))
	keys := make(map[uint64]uuid.UUID, len(memories))
	for _, memory := range memories {
		values, err := source.embedder.Embed(ctx, string(memory.content))
		if err != nil {
			return nil, fmt.Errorf("hnsw: embed memory %s: %w", memory.id, err)
		}
		key := KeyForMemoryID(memory.id)
		if prior, exists := keys[key]; exists && prior != memory.id {
			return nil, fmt.Errorf(
				"hnsw: memory key collision between %s and %s",
				prior,
				memory.id,
			)
		}
		keys[key] = memory.id
		vectors = append(vectors, Vector{Key: key, Values: values})
	}
	sort.Slice(vectors, func(left, right int) bool {
		return vectors[left].Key < vectors[right].Key
	})
	return vectors, nil
}

// KeyForMemoryID deterministically maps a Cortex UUID into USearch's u64 key
// space. CortexJournalSource detects the vanishingly unlikely collision before
// any derived index is replaced.
func KeyForMemoryID(id uuid.UUID) uint64 {
	hash := blake3.Sum256(id[:])
	return binary.BigEndian.Uint64(hash[:8])
}
