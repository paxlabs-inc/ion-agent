package cortex

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/integrity"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	now := clock.now
	clock.now = clock.now.Add(time.Nanosecond)
	return now
}

type testCipher struct{}

func (*testCipher) Encrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

func (*testCipher) Decrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

type cortexHarness struct {
	store   *Cortex
	journal *journal.Journal
	clock   *testClock
	path    string
}

func newTestCortex(t *testing.T) *cortexHarness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.journal")
	source, err := journal.Open(path, &testCipher{})
	if err != nil {
		t.Fatalf("journal.Open: %v", err)
	}
	clock := &testClock{now: baseTime}
	store, err := New(Config{
		Actor:   "test-actor",
		Journal: source,
		Clock:   clock,
	})
	if err != nil {
		_ = source.Close()
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = source.Close()
	})
	return &cortexHarness{
		store:   store,
		journal: source,
		clock:   clock,
		path:    path,
	}
}

func TestCortexCRUDPreservesAllNineTypes(t *testing.T) {
	harness := newTestCortex(t)
	ctx := context.Background()

	for _, memoryType := range memory.Types() {
		stored, err := harness.store.Write(
			ctx,
			memoryType,
			[]byte(`{"kind":"typed"}`),
			"test",
		)
		if err != nil {
			t.Fatalf("Write(%s): %v", memoryType, err)
		}
		if stored.Head.Type != memoryType || stored.Version.Type != memoryType {
			t.Fatalf("Write(%s) returned types %s/%s", memoryType, stored.Head.Type, stored.Version.Type)
		}
		resolved, err := harness.store.Resolve(stored.Head.ID)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", memoryType, err)
		}
		if resolved.Head.Type != memoryType || resolved.Version.Type != memoryType {
			t.Fatalf("Resolve(%s) returned types %s/%s", memoryType, resolved.Head.Type, resolved.Version.Type)
		}
		updated, err := harness.store.Update(
			ctx,
			stored.Head.ID,
			[]byte(`{"kind":"updated"}`),
			"test",
		)
		if err != nil {
			t.Fatalf("Update(%s): %v", memoryType, err)
		}
		if updated.Head.Type != memoryType || updated.Version.Type != memoryType {
			t.Fatalf("Update(%s) changed type", memoryType)
		}
		if ids := harness.store.ListByType(memoryType); len(ids) != 1 || ids[0] != stored.Head.ID {
			t.Fatalf("ListByType(%s) = %v", memoryType, ids)
		}
		if err := harness.store.Tombstone(ctx, stored.Head.ID, "expired", "test"); err != nil {
			t.Fatalf("Tombstone(%s): %v", memoryType, err)
		}
		if _, err := harness.store.Resolve(stored.Head.ID); !errors.Is(err, ErrTombstoned) {
			t.Fatalf("Resolve tombstoned %s error = %v", memoryType, err)
		}
	}
}

func TestCortexRejectsInvalidType(t *testing.T) {
	harness := newTestCortex(t)
	if _, err := harness.store.Write(
		context.Background(),
		memory.Type("0x0a"),
		[]byte(`{}`),
		"test",
	); err == nil {
		t.Fatal("invalid type accepted")
	}
	if harness.store.SMT(memory.Type("0x0a")) != nil {
		t.Fatal("invalid type has an SMT namespace")
	}
}

func TestCortexJournalIsAppendOnlyForEveryMutation(t *testing.T) {
	harness := newTestCortex(t)
	ctx := context.Background()
	stored, err := harness.store.Write(ctx, memory.Fact, []byte(`{"value":1}`), "writer")
	if err != nil {
		t.Fatal(err)
	}
	afterStore, err := os.ReadFile(harness.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.store.Update(ctx, stored.Head.ID, []byte(`{"value":2}`), "writer"); err != nil {
		t.Fatal(err)
	}
	afterUpdate, err := os.ReadFile(harness.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterUpdate) <= len(afterStore) || !bytes.Equal(afterUpdate[:len(afterStore)], afterStore) {
		t.Fatal("update modified existing journal bytes")
	}
	if err := harness.store.Tombstone(ctx, stored.Head.ID, "obsolete", "writer"); err != nil {
		t.Fatal(err)
	}
	afterArchive, err := os.ReadFile(harness.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterArchive) <= len(afterUpdate) || !bytes.Equal(afterArchive[:len(afterUpdate)], afterUpdate) {
		t.Fatal("archive modified existing journal bytes")
	}

	var mutations []journal.Mutation
	if err := harness.journal.Replay(ctx, func(record journal.Record) error {
		mutations = append(mutations, record.Entry.Type)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []journal.Mutation{journal.Store, journal.Update, journal.Archive}
	if len(mutations) != len(want) {
		t.Fatalf("journal mutations = %v", mutations)
	}
	for index := range want {
		if mutations[index] != want[index] {
			t.Fatalf("journal mutations = %v", mutations)
		}
	}
	if harness.store.MMRState().LeafCount() != uint64(len(want)) {
		t.Fatalf("MMR leaves = %d", harness.store.MMRState().LeafCount())
	}
}

func TestCortexVersionChainAndSourceTrustAreImmutable(t *testing.T) {
	harness := newTestCortex(t)
	ctx := context.Background()
	stored, err := harness.store.WriteWithTrust(
		ctx,
		memory.Belief,
		[]byte(`{"claim":"first"}`),
		"observer",
		TrustLow,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := harness.store.UpdateWithTrust(
		ctx,
		stored.Head.ID,
		[]byte(`{"claim":"second"}`),
		"corroborator",
		TrustHigh,
	)
	if err != nil {
		t.Fatal(err)
	}
	third, err := harness.store.UpdateSourceTrust(
		ctx,
		stored.Head.ID,
		TrustVerified,
		"verifier",
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version.Version != 2 || third.Version.Version != 3 {
		t.Fatalf("versions = %d, %d", second.Version.Version, third.Version.Version)
	}
	if !bytes.Equal(second.Version.Data, third.Version.Data) {
		t.Fatal("trust-only update changed content")
	}

	chain, err := harness.store.Versions(stored.Head.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain length = %d", len(chain))
	}
	for index, version := range chain {
		wantVersion := uint64(index + 1)
		if version.Version != wantVersion {
			t.Fatalf("chain[%d].Version = %d", index, version.Version)
		}
		if index == 0 {
			if version.PreviousVersion != 0 || version.PreviousHash != ([32]byte{}) {
				t.Fatal("first version has a predecessor")
			}
			continue
		}
		if version.PreviousVersion != chain[index-1].Version ||
			version.PreviousHash != chain[index-1].Hash {
			t.Fatalf("chain[%d] is not linked to its predecessor", index)
		}
		if version.Hash == chain[index-1].Hash {
			t.Fatalf("chain[%d] reused predecessor hash", index)
		}
	}
	if chain[0].SourceTrust != float64(TrustLow) ||
		chain[1].SourceTrust != float64(TrustHigh) ||
		chain[2].SourceTrust != float64(TrustVerified) {
		t.Fatalf("trust history = %v, %v, %v", chain[0].SourceTrust, chain[1].SourceTrust, chain[2].SourceTrust)
	}
	trust, err := harness.store.SourceTrust(stored.Head.ID)
	if err != nil || trust != TrustVerified {
		t.Fatalf("SourceTrust = %v, %v", trust, err)
	}

	chain[0].Data[0] ^= 1
	historical, err := harness.store.ResolveVersion(stored.Head.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(historical.Version.Data, []byte(`{"claim":"first"}`)) {
		t.Fatal("caller mutated stored version data")
	}
	if historical.Version.Hash != stored.Version.Hash {
		t.Fatal("historical version hash changed")
	}
}

func TestCortexRejectsInvalidSourceTrustWithoutJournalMutation(t *testing.T) {
	harness := newTestCortex(t)
	ctx := context.Background()
	before, err := os.Stat(harness.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, trust := range []TrustLevel{-0.01, 1.01, TrustLevel(mathNaN())} {
		if _, err := harness.store.WriteWithTrust(
			ctx,
			memory.Fact,
			[]byte(`{}`),
			"writer",
			trust,
		); !errors.Is(err, ErrInvalidSourceTrust) {
			t.Fatalf("trust %v error = %v", trust, err)
		}
	}
	after, err := os.Stat(harness.path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatal("invalid trust mutation reached journal")
	}
}

func TestCortexReplayRestoresVersionsTrustTombstonesAndIntegrity(t *testing.T) {
	harness := newTestCortex(t)
	ctx := context.Background()
	live, err := harness.store.WriteWithTrust(
		ctx,
		memory.Fact,
		[]byte(`{"z":1,"a":"first"}`),
		"writer",
		TrustMedium,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.store.UpdateSourceTrust(ctx, live.Head.ID, TrustHigh, "reviewer"); err != nil {
		t.Fatal(err)
	}
	archived, err := harness.store.Write(ctx, memory.Event, []byte(`{"event":"past"}`), "writer")
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.store.Tombstone(ctx, archived.Head.ID, "complete", "writer"); err != nil {
		t.Fatal(err)
	}
	liveRoot := harness.store.MMRState().Root()
	liveCount := harness.store.MMRState().LeafCount()
	if err := harness.store.Close(); err != nil {
		t.Fatal(err)
	}

	replayed, err := New(Config{
		Actor:   "test-actor",
		Journal: harness.journal,
		Clock:   harness.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer replayed.Close()
	if replayed.MMRState().Root() != liveRoot || replayed.MMRState().LeafCount() != liveCount {
		t.Fatal("replayed MMR differs from live MMR")
	}
	resolved, err := replayed.Resolve(live.Head.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Version.Version != 2 ||
		resolved.Head.SourceTrust != float64(TrustHigh) ||
		!bytes.Equal(resolved.Version.Data, []byte(`{"a":"first","z":1}`)) {
		t.Fatalf("replayed memory = %+v", resolved)
	}
	versions, err := replayed.Versions(live.Head.ID)
	if err != nil || len(versions) != 2 || versions[1].PreviousHash != versions[0].Hash {
		t.Fatalf("replayed versions = %+v, %v", versions, err)
	}
	if _, err := replayed.Resolve(archived.Head.ID); !errors.Is(err, ErrTombstoned) {
		t.Fatalf("archived Resolve error = %v", err)
	}
	archivedVersion, err := replayed.ResolveVersion(archived.Head.ID, 1)
	if err != nil || !bytes.Equal(archivedVersion.Version.Data, []byte(`{"event":"past"}`)) {
		t.Fatalf("archived version = %+v, %v", archivedVersion, err)
	}

	derived, err := integrity.Replay(ctx, harness.journal)
	if err != nil {
		t.Fatal(err)
	}
	if derived.MMR.Root() != liveRoot {
		t.Fatal("Cortex live root does not match journal-derived integrity root")
	}
}

func TestHistoricalJournalTamperingChangesMMRRootAndReplayIsDeterministic(t *testing.T) {
	harness := newTestCortex(t)
	ctx := context.Background()
	if _, err := harness.store.Write(
		ctx,
		memory.Fact,
		[]byte(`{"fact":"first"}`),
		"writer",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.store.Write(
		ctx,
		memory.Fact,
		[]byte(`{"fact":"second"}`),
		"writer",
	); err != nil {
		t.Fatal(err)
	}
	firstReplay, err := integrity.Replay(ctx, harness.journal)
	if err != nil {
		t.Fatal(err)
	}
	secondReplay, err := integrity.Replay(ctx, harness.journal)
	if err != nil {
		t.Fatal(err)
	}
	if firstReplay.MMR.Root() != secondReplay.MMR.Root() ||
		firstReplay.Forest.Root() != secondReplay.Forest.Root() {
		t.Fatal("replay from identical journal bytes is not deterministic")
	}

	raw, err := os.ReadFile(harness.path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(`"first"`), []byte(`"other"`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("test did not locate historical content")
	}
	if err := os.WriteFile(harness.path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	tamperedReplay, err := integrity.Replay(ctx, harness.journal)
	if err != nil {
		t.Fatalf("replay structurally valid tampering: %v", err)
	}
	if tamperedReplay.MMR.Root() == firstReplay.MMR.Root() {
		t.Fatal("historical tampering did not change the MMR root")
	}
}

func TestCortexActorAndDependencies(t *testing.T) {
	harness := newTestCortex(t)
	if harness.store.Actor() != "test-actor" {
		t.Fatalf("Actor = %q", harness.store.Actor())
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
	if _, err := harness.store.Resolve(uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Resolve error = %v", err)
	}
}

func mathNaN() float64 {
	var zero float64
	return zero / zero
}
