package journal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/integrity"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/memory/mmr"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
)

func TestEncryptedAppendReplayAndDeterministicIntegrity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cortex.journal")
	cipher, err := vault.New(make([]byte, vault.KeySize))
	if err != nil {
		t.Fatalf("vault.New() error = %v", err)
	}
	defer cipher.Close()
	source, err := journal.Open(path, cipher)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	memoryID := uuid.New()
	first, err := source.Append(context.Background(), journal.Entry{
		Type:       journal.Store,
		MemoryID:   memoryID,
		MemoryType: memory.Fact,
		Content:    json.RawMessage(`{"z":1,"a":"first"}`),
		Timestamp:  100,
	})
	if err != nil {
		t.Fatalf("Append(store) error = %v", err)
	}
	previous := uint64(1)
	second, err := source.Append(context.Background(), journal.Entry{
		Type:        journal.Update,
		MemoryID:    memoryID,
		MemoryType:  memory.Fact,
		Content:     json.RawMessage(`{"a":"second"}`),
		PrevVersion: &previous,
		Timestamp:   101,
	})
	if err != nil {
		t.Fatalf("Append(update) error = %v", err)
	}
	if first.Entry.JournalSeq != 1 || second.Entry.JournalSeq != 2 {
		t.Fatalf("sequences = %d, %d", first.Entry.JournalSeq, second.Entry.JournalSeq)
	}
	if string(first.Entry.Content) != `{"a":"first","z":1}` {
		t.Fatalf("canonical content = %s", first.Entry.Content)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, first.Entry.Content) || len(raw) == 0 {
		t.Fatal("journal exposed plaintext")
	}

	firstState, err := integrity.Replay(context.Background(), source)
	if err != nil {
		t.Fatalf("integrity.Replay() error = %v", err)
	}
	secondState, err := integrity.Replay(context.Background(), source)
	if err != nil {
		t.Fatalf("second integrity.Replay() error = %v", err)
	}
	if firstState.MMR.Root() != secondState.MMR.Root() ||
		firstState.Forest.Root() != secondState.Forest.Root() {
		t.Fatal("replay was not byte-deterministic")
	}

	persisted, err := mmr.Open(filepath.Join(t.TempDir(), "mmr.dat"))
	if err != nil {
		t.Fatal(err)
	}
	defer persisted.Close()
	for index := uint64(0); index < firstState.MMR.LeafCount(); index++ {
		leaf, _ := firstState.MMR.Leaf(index)
		if _, _, err := persisted.AppendHash(leaf); err != nil {
			t.Fatal(err)
		}
	}
	if err := firstState.VerifyMMR(persisted); err != nil {
		t.Fatalf("VerifyMMR() error = %v", err)
	}
}

func TestJournalRejectsTamperingAndInvalidVersionChains(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cortex.journal")
	cipher, err := vault.New(make([]byte, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	source, err := journal.Open(path, cipher)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Append(context.Background(), journal.Entry{
		Type:       journal.Update,
		MemoryID:   uuid.New(),
		MemoryType: memory.Belief,
		Content:    json.RawMessage(`{"invalid":true}`),
		Timestamp:  1,
	})
	if err == nil {
		t.Fatal("update without previous version accepted")
	}
	if _, err := source.Append(context.Background(), journal.Entry{
		Type:       journal.Store,
		MemoryID:   uuid.New(),
		MemoryType: memory.Belief,
		Content:    json.RawMessage(`{"valid":true}`),
		Timestamp:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)-1] ^= 1
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Open(path, cipher); !errors.Is(err, journal.ErrCorrupt) {
		t.Fatalf("journal.Open(tampered) error = %v", err)
	}
}

func TestMemoryTaxonomyIsClosed(t *testing.T) {
	t.Parallel()
	for _, memoryType := range []memory.Type{
		memory.Identity,
		memory.Fact,
		memory.Preference,
		memory.Belief,
		memory.Event,
		memory.Goal,
		memory.Constraint,
		memory.Capability,
		memory.Pattern,
	} {
		if err := memoryType.Validate(); err != nil {
			t.Errorf("%s invalid: %v", memoryType, err)
		}
	}
	if err := memory.Type("0x10").Validate(); err == nil {
		t.Fatal("invented memory type accepted")
	}
}
