package securememory

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
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type alertRecorder struct {
	events []canary.AlertEvent
}

func (recorder *alertRecorder) CanaryAlerted(event canary.AlertEvent) {
	recorder.events = append(recorder.events, event)
}

func Test_Cortex_RealCanaryCannotBeReadModifiedOrArchived(t *testing.T) {
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
		filepath.Join(t.TempDir(), "cortex.journal"),
		envelope,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer journalStore.Close()
	store, err := cortex.New(cortex.Config{
		Actor:   "security-test",
		Journal: journalStore,
		Clock:   types.SystemClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	alerts := &alertRecorder{}
	canaries, err := canary.NewManager(canary.ManagerConfig{
		Clock:     types.SystemClock{},
		EventSink: alerts,
	})
	if err != nil {
		t.Fatal(err)
	}
	guarded, err := New(store, canaries)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	honeypot, err := guarded.SeedCanary(
		ctx,
		memory.Fact,
		[]byte(`{"fact":"trusted"}`),
		"seed",
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := guarded.Resolve(honeypot.Head.ID, "adversarial-reader"); err == nil {
		t.Fatal("canary read succeeded")
	}
	if _, err := guarded.Update(
		ctx,
		honeypot.Head.ID,
		[]byte(`{"fact":"poisoned"}`),
		"adversarial-writer",
	); !errors.Is(err, canary.ErrCanaryMutation) {
		t.Fatalf("canary update error = %v", err)
	}
	if err := guarded.Archive(
		ctx,
		honeypot.Head.ID,
		"decay",
		"adversarial-archiver",
	); !errors.Is(err, canary.ErrCanaryArchive) {
		t.Fatalf("canary archive error = %v", err)
	}
	if len(alerts.events) != 3 {
		t.Fatalf("alerts = %+v", alerts.events)
	}

	resolved, err := store.Resolve(honeypot.Head.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resolved.Version.Data, []byte(`{"fact":"trusted"}`)) ||
		resolved.Head.Tombstoned != nil {
		t.Fatalf("canary changed despite guard: %+v", resolved)
	}

	normal, err := guarded.Write(
		ctx,
		memory.Fact,
		[]byte(`{"fact":"normal"}`),
		"writer",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guarded.Resolve(normal.Head.ID, "reader"); err != nil {
		t.Fatalf("normal resolve: %v", err)
	}
	if _, err := guarded.Update(
		ctx,
		normal.Head.ID,
		[]byte(`{"fact":"updated"}`),
		"writer",
	); err != nil {
		t.Fatalf("normal update: %v", err)
	}
	if err := guarded.Archive(
		ctx,
		normal.Head.ID,
		"expired",
		"archiver",
	); err != nil {
		t.Fatalf("normal archive: %v", err)
	}
	if _, err := guarded.SeedCanary(
		ctx,
		memory.Type("invalid"),
		[]byte(`{}`),
		"seed",
	); err == nil {
		t.Fatal("invalid canary type accepted")
	}
}

func Test_Cortex_RequiresDependencies(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("nil dependencies accepted")
	}
}
