package hnsw

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T, dimensions int) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(
		context.Background(),
		filepath.Join(t.TempDir(), "vectors.db"),
		dimensions,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteStoreUpsertAndSnapshotRoundTrip(t *testing.T) {
	store := newTestStore(t, 3)
	ctx := context.Background()
	if err := store.Upsert(ctx, Vector{Key: 9, Values: []float32{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(ctx, Vector{Key: 9, Values: []float32{3, 2, 1}}); err != nil {
		t.Fatal(err)
	}
	vectors, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || vectors[0].Key != 9 || vectors[0].Values[0] != 3 {
		t.Fatalf("snapshot = %+v", vectors)
	}
}

func TestSQLiteStoreDeleteReportsExistence(t *testing.T) {
	store := newTestStore(t, 2)
	ctx := context.Background()
	if err := store.Upsert(ctx, Vector{Key: 1, Values: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}
	removed, err := store.Delete(ctx, 1)
	if err != nil || !removed {
		t.Fatalf("first delete = %v, %v", removed, err)
	}
	removed, err = store.Delete(ctx, 1)
	if err != nil || removed {
		t.Fatalf("second delete = %v, %v", removed, err)
	}
}

func TestSQLiteStoreExactCosineSearchOrdersAndBreaksTies(t *testing.T) {
	store := newTestStore(t, 2)
	ctx := context.Background()
	for _, vector := range []Vector{
		{Key: 8, Values: []float32{1, 0}},
		{Key: 3, Values: []float32{2, 0}},
		{Key: 5, Values: []float32{0, 1}},
		{Key: 7, Values: []float32{-1, 0}},
	} {
		if err := store.Upsert(ctx, vector); err != nil {
			t.Fatal(err)
		}
	}
	matches, err := store.Search(ctx, []float32{1, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{3, 8, 5}
	for index := range want {
		if matches[index].Key != want[index] {
			t.Fatalf("matches = %+v", matches)
		}
	}
}

func TestSQLiteStoreSearchKZeroIsEmpty(t *testing.T) {
	store := newTestStore(t, 2)
	matches, err := store.Search(context.Background(), []float32{1, 0}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if matches == nil || len(matches) != 0 {
		t.Fatalf("matches = %#v", matches)
	}
}

func TestSQLiteStoreReplaceIsComplete(t *testing.T) {
	store := newTestStore(t, 2)
	ctx := context.Background()
	_ = store.Upsert(ctx, Vector{Key: 99, Values: []float32{1, 0}})
	replacement := []Vector{
		{Key: 1, Values: []float32{1, 0}},
		{Key: 2, Values: []float32{0, 1}},
	}
	if err := store.Replace(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	count, err := store.Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("count = %d, %v", count, err)
	}
	vectors, _ := store.Snapshot(ctx)
	if len(vectors) != 2 || vectors[0].Key != 1 || vectors[1].Key != 2 {
		t.Fatalf("vectors = %+v", vectors)
	}
}

func TestSQLiteStoreRejectsInvalidVectorsWithoutMutation(t *testing.T) {
	store := newTestStore(t, 2)
	ctx := context.Background()
	for _, values := range [][]float32{
		{1},
		{0, 0},
		{float32(math.NaN()), 1},
		{float32(math.Inf(1)), 1},
	} {
		if err := store.Upsert(ctx, Vector{Key: 1, Values: values}); !errors.Is(err, ErrInvalidVector) {
			t.Fatalf("Upsert(%v) error = %v", values, err)
		}
	}
	count, _ := store.Count(ctx)
	if count != 0 {
		t.Fatalf("count = %d", count)
	}
}

func TestSQLiteStorePreservesFullUint64Keys(t *testing.T) {
	store := newTestStore(t, 2)
	ctx := context.Background()
	key := ^uint64(0)
	if err := store.Upsert(ctx, Vector{Key: key, Values: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}
	vectors, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || vectors[0].Key != key {
		t.Fatalf("snapshot = %+v", vectors)
	}
}
