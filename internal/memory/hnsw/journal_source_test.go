package hnsw

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
)

type journalTestCipher struct{}

func (journalTestCipher) Encrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

func (journalTestCipher) Decrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

func TestCortexJournalSourceReplaysLatestLiveContent(t *testing.T) {
	sourceJournal, err := journal.Open(
		filepath.Join(t.TempDir(), "cortex.journal"),
		journalTestCipher{},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceJournal.Close()
	firstID := uuid.New()
	secondID := uuid.New()
	firstTrust := 1.0
	first, err := sourceJournal.Append(context.Background(), journal.Entry{
		Type:        journal.Store,
		MemoryID:    firstID,
		MemoryType:  memory.Fact,
		Content:     []byte(`{"text":"old memory"}`),
		Timestamp:   1,
		SourceTrust: &firstTrust,
	})
	if err != nil {
		t.Fatal(err)
	}
	previous := uint64(1)
	if _, err := sourceJournal.Append(context.Background(), journal.Entry{
		Type:        journal.Update,
		MemoryID:    firstID,
		MemoryType:  memory.Fact,
		Content:     []byte(`{"text":"new memory"}`),
		PrevVersion: &previous,
		Timestamp:   2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceJournal.Append(context.Background(), journal.Entry{
		Type:       journal.Store,
		MemoryID:   secondID,
		MemoryType: memory.Event,
		Content:    []byte(`{"text":"temporary"}`),
		Timestamp:  3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceJournal.Append(context.Background(), journal.Entry{
		Type:        journal.Archive,
		MemoryID:    secondID,
		MemoryType:  memory.Event,
		Content:     []byte(`{"reason":"expired"}`),
		PrevVersion: &previous,
		Timestamp:   4,
	}); err != nil {
		t.Fatal(err)
	}
	if len(first.Ciphertext) == 0 {
		t.Fatal("journal record was not written")
	}

	embedder, _ := NewHashEmbedder(32)
	source, err := NewCortexJournalSource(sourceJournal, embedder)
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || vectors[0].Key != KeyForMemoryID(firstID) {
		t.Fatalf("vectors = %+v", vectors)
	}
	want, _ := embedder.Embed(context.Background(), `{"text":"new memory"}`)
	for index := range want {
		if vectors[0].Values[index] != want[index] {
			t.Fatalf("latest content embedding differs at %d", index)
		}
	}
}

func TestKeyForMemoryIDIsStableAndDistinct(t *testing.T) {
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	firstKey := KeyForMemoryID(first)
	if firstKey != KeyForMemoryID(first) {
		t.Fatal("key mapping is not stable")
	}
	if firstKey == KeyForMemoryID(second) {
		t.Fatal("distinct fixture UUIDs collided")
	}
}
