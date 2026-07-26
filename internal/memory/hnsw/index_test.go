package hnsw

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestIndex(
	t *testing.T,
	service *testUDSService,
	dimensions int,
	source VectorSource,
) (*Index, *SQLiteStore) {
	t.Helper()
	store, err := OpenSQLiteStore(
		context.Background(),
		filepath.Join(t.TempDir(), "cortex-vectors.db"),
		dimensions,
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(service.path, dimensions, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	index, err := NewIndex(context.Background(), Config{
		Remote:     client,
		Store:      store,
		Source:     source,
		Dimensions: dimensions,
	})
	if err != nil {
		_ = client.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return index, store
}

func TestIndexFallsBackToBruteForceAfterServiceCrash(t *testing.T) {
	service := startTestUDSService(t)
	index, _ := newTestIndex(t, service, 3, nil)
	ctx := context.Background()
	if err := index.Upsert(ctx, 1, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(ctx, 2, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	service.crash()
	matches, err := index.Search(ctx, []float32{1, 0, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Key != 1 {
		t.Fatalf("fallback matches = %+v", matches)
	}
	if !index.Degraded() {
		t.Fatal("index did not report degraded mode")
	}
}

func TestIndexAcceptsDurableUpsertWhileServiceIsDown(t *testing.T) {
	service := startTestUDSService(t)
	index, store := newTestIndex(t, service, 2, nil)
	service.crash()
	if err := index.Upsert(context.Background(), 7, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	count, err := store.Count(context.Background())
	if err != nil || count != 1 || !index.Degraded() {
		t.Fatalf("fallback count/degraded = %d/%v, %v", count, index.Degraded(), err)
	}
}

func TestIndexRebuildsWhenCountDivergesByMoreThanOnePercent(t *testing.T) {
	service := startTestUDSService(t)
	vectors := deterministicVectors(200, 8)
	index, store := newTestIndex(t, service, 8, staticVectorSource(vectors))
	if !index.RebuiltOnStartup() {
		t.Fatal("startup rebuild not reported")
	}
	if service.resets() != 1 || service.vectorCount() != 200 {
		t.Fatalf("service resets/count = %d/%d", service.resets(), service.vectorCount())
	}
	count, err := store.Count(context.Background())
	if err != nil || count != 200 {
		t.Fatalf("fallback count = %d, %v", count, err)
	}
}

func TestIndexDoesNotRebuildAtExactlyOnePercentDivergence(t *testing.T) {
	service := startTestUDSService(t)
	client, _ := NewClient(service.path, 4, time.Second)
	for _, vector := range deterministicVectors(99, 4) {
		if err := client.Insert(context.Background(), vector.Key, vector.Values); err != nil {
			t.Fatal(err)
		}
	}
	_ = client.Close()
	index, _ := newTestIndex(t, service, 4, staticVectorSource(deterministicVectors(100, 4)))
	if index.RebuiltOnStartup() || service.resets() != 0 {
		t.Fatalf("unexpected rebuild at 1%% divergence: resets=%d", service.resets())
	}
}

func TestIndexReconcileRepairsMissedWritesAfterRestart(t *testing.T) {
	first := startTestUDSService(t)
	index, store := newTestIndex(t, first, 2, nil)
	first.crash()
	if err := index.Upsert(context.Background(), 55, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}

	second := startTestUDSService(t)
	client, _ := NewClient(second.path, 2, time.Second)
	restarted, err := NewIndex(context.Background(), Config{
		Remote:     client,
		Store:      store,
		Dimensions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Prevent both Index values from closing the shared store.
	index.store = noCloseStore{VectorStore: store}
	defer restarted.Close()
	if !restarted.RebuiltOnStartup() || second.vectorCount() != 1 {
		t.Fatalf(
			"restart rebuilt/count = %v/%d",
			restarted.RebuiltOnStartup(),
			second.vectorCount(),
		)
	}
}

func TestNewIndexStartsDegradedWhenServiceIsAbsent(t *testing.T) {
	store := newTestStore(t, 2)
	client, _ := NewClient(filepath.Join(t.TempDir(), "absent.sock"), 2, 20*time.Millisecond)
	index, err := NewIndex(context.Background(), Config{
		Remote:     client,
		Store:      noCloseStore{VectorStore: store},
		Dimensions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if !index.Degraded() {
		t.Fatal("absent service did not activate degraded mode")
	}
}

func TestCountsDivergedUsesStrictOnePercentThreshold(t *testing.T) {
	for _, testCase := range []struct {
		remote, local uint64
		want          bool
	}{
		{100, 100, false},
		{99, 100, false},
		{101, 100, false},
		{98, 100, true},
		{0, 1, true},
		{1, 0, true},
	} {
		if got := countsDiverged(testCase.remote, testCase.local); got != testCase.want {
			t.Fatalf(
				"countsDiverged(%d,%d) = %v",
				testCase.remote,
				testCase.local,
				got,
			)
		}
	}
}

type staticVectorSource []Vector

func (source staticVectorSource) Snapshot(context.Context) ([]Vector, error) {
	vectors := make([]Vector, len(source))
	for index, vector := range source {
		vectors[index] = Vector{
			Key:    vector.Key,
			Values: append([]float32(nil), vector.Values...),
		}
	}
	return vectors, nil
}

func deterministicVectors(count, dimensions int) []Vector {
	vectors := make([]Vector, count)
	for index := range vectors {
		values := make([]float32, dimensions)
		values[index%dimensions] = 1
		values[(index+1)%dimensions] = float32(index+1) / float32(count+1)
		vectors[index] = Vector{Key: uint64(index + 1), Values: values}
	}
	return vectors
}

type noCloseStore struct {
	VectorStore
}

func (store noCloseStore) Close() error { return nil }

type failingSource struct{}

func (failingSource) Snapshot(context.Context) ([]Vector, error) {
	return nil, errors.New("journal unavailable")
}

func TestNewIndexDoesNotHideJournalSourceFailure(t *testing.T) {
	service := startTestUDSService(t)
	store := newTestStore(t, 2)
	client, _ := NewClient(service.path, 2, time.Second)
	_, err := NewIndex(context.Background(), Config{
		Remote:     client,
		Store:      noCloseStore{VectorStore: store},
		Source:     failingSource{},
		Dimensions: 2,
	})
	_ = client.Close()
	if err == nil || err.Error() != "journal unavailable" {
		t.Fatalf("source error = %v", err)
	}
}
