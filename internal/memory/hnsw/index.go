package hnsw

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
)

const rebuildDivergence = 0.01

// Index combines the fast remote graph with a durable exact SQLite fallback.
// The fallback store is updated first, so an accepted mutation survives a
// service crash and can be replayed into USearch later.
type Index struct {
	remote     Remote
	store      VectorStore
	source     VectorSource
	dimensions int

	degraded         atomic.Bool
	rebuiltOnStartup atomic.Bool
}

// Config wires the Rust service, SQLite Cortex index, and optional
// journal-authoritative rebuild source.
type Config struct {
	Remote     Remote
	Store      VectorStore
	Source     VectorSource
	Dimensions int
}

// NewIndex refreshes SQLite from Source when provided, then checks the remote
// vector count. A down service is not a startup failure: the returned index is
// marked degraded and immediately serves exact SQLite searches.
func NewIndex(ctx context.Context, config Config) (*Index, error) {
	if config.Remote == nil || config.Store == nil {
		return nil, fmt.Errorf("hnsw: remote and store are required")
	}
	if config.Dimensions <= 0 {
		return nil, fmt.Errorf("hnsw: dimensions must be positive")
	}
	index := &Index{
		remote:     config.Remote,
		store:      config.Store,
		source:     config.Source,
		dimensions: config.Dimensions,
	}
	rebuilt, err := index.Reconcile(ctx)
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			index.degraded.Store(true)
			return index, nil
		}
		return nil, err
	}
	index.rebuiltOnStartup.Store(rebuilt)
	return index, nil
}

// Upsert durably updates fallback before attempting the HNSW graph.
func (index *Index) Upsert(
	ctx context.Context,
	key uint64,
	values []float32,
) error {
	if err := validateVector(values, index.dimensions); err != nil {
		return err
	}
	vector := Vector{Key: key, Values: append([]float32(nil), values...)}
	if err := index.store.Upsert(ctx, vector); err != nil {
		return err
	}
	if err := index.remote.Insert(ctx, key, values); err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			index.degraded.Store(true)
			return nil
		}
		return err
	}
	return nil
}

// Search uses USearch while healthy and transparently falls back to exact
// cosine search on a crash or broken socket.
func (index *Index) Search(
	ctx context.Context,
	query []float32,
	k int,
) ([]Match, error) {
	if err := validateVector(query, index.dimensions); err != nil {
		return nil, err
	}
	if k < 0 {
		return nil, fmt.Errorf("hnsw: k must not be negative")
	}
	matches, err := index.remote.Search(ctx, query, k)
	if err == nil {
		return matches, nil
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		return nil, err
	}
	index.degraded.Store(true)
	return index.store.Search(ctx, query, k)
}

// Delete durably removes fallback first. Remote unavailability leaves a
// detectable divergence that Reconcile repairs from durable state.
func (index *Index) Delete(ctx context.Context, key uint64) (bool, error) {
	removed, err := index.store.Delete(ctx, key)
	if err != nil {
		return false, err
	}
	if _, err := index.remote.Delete(ctx, key); err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			index.degraded.Store(true)
			return removed, nil
		}
		return false, err
	}
	return removed, nil
}

// Reconcile refreshes fallback from the Cortex journal when configured and
// rebuilds USearch only when vector counts diverge by strictly more than 1%.
func (index *Index) Reconcile(ctx context.Context) (bool, error) {
	var vectors []Vector
	var err error
	if index.source != nil {
		vectors, err = index.source.Snapshot(ctx)
		if err != nil {
			return false, err
		}
		if err := index.store.Replace(ctx, vectors); err != nil {
			return false, err
		}
	} else {
		vectors, err = index.store.Snapshot(ctx)
		if err != nil {
			return false, err
		}
	}
	remoteCount, err := index.remote.Count(ctx)
	if err != nil {
		index.degraded.Store(true)
		return false, err
	}
	localCount := uint64(len(vectors))
	if !countsDiverged(remoteCount, localCount) {
		index.degraded.Store(false)
		return false, nil
	}
	if err := index.remote.Reset(ctx); err != nil {
		index.degraded.Store(true)
		return false, err
	}
	for _, vector := range vectors {
		if err := index.remote.Insert(ctx, vector.Key, vector.Values); err != nil {
			index.degraded.Store(true)
			return false, fmt.Errorf("hnsw: rebuild key %d: %w", vector.Key, err)
		}
	}
	index.degraded.Store(false)
	return true, nil
}

// Degraded reports whether a service failure or incomplete synchronization has
// occurred since the last successful reconciliation.
func (index *Index) Degraded() bool {
	return index.degraded.Load()
}

// RebuiltOnStartup reports whether NewIndex detected >1% divergence and
// rebuilt the remote graph.
func (index *Index) RebuiltOnStartup() bool {
	return index.rebuiltOnStartup.Load()
}

// Close closes both owned client-side resources.
func (index *Index) Close() error {
	return errors.Join(index.remote.Close(), index.store.Close())
}

func countsDiverged(remote, local uint64) bool {
	if remote == local {
		return false
	}
	denominator := math.Max(float64(local), 1)
	return math.Abs(float64(remote)-float64(local))/denominator > rebuildDivergence
}
