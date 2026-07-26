package adversarial

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/security/canary"
	"github.com/paxlabs-inc/ion-agent/internal/security/memoryguard"
	"github.com/paxlabs-inc/ion-agent/internal/security/securememory"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type canaryEvents struct {
	events []canary.AlertEvent
}

func (sink *canaryEvents) CanaryAlerted(event canary.AlertEvent) {
	sink.events = append(sink.events, event)
}

type protectedEvents struct {
	events []memoryguard.Event
}

func (sink *protectedEvents) ProtectedMemoryTargeted(event memoryguard.Event) {
	sink.events = append(sink.events, event)
}

func Test_MemoryPoisoning_CanaryMutationAndArchiveAreBlocked(t *testing.T) {
	sink := &canaryEvents{}
	manager, err := canary.NewManager(canary.ManagerConfig{
		Clock:     types.SystemClock{},
		EventSink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, vault.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.New(key)
	for index := range key {
		key[index] = 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer envelope.Close()
	journalStore, err := journal.Open(
		filepath.Join(t.TempDir(), "adversarial.journal"),
		envelope,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journalStore.Close()
	store, err := cortex.New(cortex.Config{
		Actor:   "red-team",
		Journal: journalStore,
		Clock:   types.SystemClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	guarded, err := securememory.New(store, manager)
	if err != nil {
		t.Fatal(err)
	}
	honeypot, err := guarded.SeedCanary(
		context.Background(),
		memory.Fact,
		[]byte(`{"fact":"trusted operational fact"}`),
		"red-team-seed",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guarded.Update(
		context.Background(),
		honeypot.Head.ID,
		[]byte(`{"fact":"poisoned operational fact"}`),
		"adversarial-memory-writer",
	); !errors.Is(err, canary.ErrCanaryMutation) {
		t.Fatalf("mutation error = %v", err)
	}
	if err := guarded.Archive(
		context.Background(),
		honeypot.Head.ID,
		"decay",
		"adversarial-decay-worker",
	); !errors.Is(err, canary.ErrCanaryArchive) {
		t.Fatalf("archive error = %v", err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("canary alerts = %+v", sink.events)
	}
	resolved, err := store.Resolve(honeypot.Head.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resolved.Version.Data, []byte(`{"fact":"trusted operational fact"}`)) ||
		resolved.Head.Tombstoned != nil {
		t.Fatalf("honeypot changed after attack: %+v", resolved)
	}
}

func Test_MemoryPoisoning_DreamweaverCannotRewriteBindingTypes(t *testing.T) {
	sink := &protectedEvents{}
	guard, err := memoryguard.New(types.SystemClock{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	for _, memoryType := range []memory.Type{
		memory.Identity,
		memory.Preference,
		memory.Constraint,
	} {
		if err := guard.AuthorizeDreamweaverMutation(
			"target",
			memoryType,
			"adversarial-reorganize",
		); !errors.Is(err, memoryguard.ErrProtectedType) {
			t.Fatalf("%s rewrite error = %v", memoryType, err)
		}
	}
	if len(sink.events) != 3 {
		t.Fatalf("protected-memory alerts = %+v", sink.events)
	}
}
