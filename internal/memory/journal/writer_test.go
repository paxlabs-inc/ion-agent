package journal_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
)

type copyCipher struct{}

func (*copyCipher) Encrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

func (*copyCipher) Decrypt(data []byte) ([]byte, error) {
	return append([]byte(nil), data...), nil
}

func TestWriterGoroutineSerializesConcurrentAppends(t *testing.T) {
	source, err := journal.Open(filepath.Join(t.TempDir(), "writer.journal"), &copyCipher{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	writer, err := journal.NewWriter(source)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	const count = 64
	sequences := make(chan uint64, count)
	errorsFound := make(chan error, count)
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func(value int) {
			defer wait.Done()
			content, err := json.Marshal(map[string]int{"value": value})
			if err != nil {
				errorsFound <- err
				return
			}
			record, err := writer.Append(context.Background(), journal.Entry{
				Type:       journal.Store,
				MemoryID:   uuid.New(),
				MemoryType: memory.Event,
				Content:    content,
				Timestamp:  int64(value + 1),
			})
			if err != nil {
				errorsFound <- err
				return
			}
			sequences <- record.Entry.JournalSeq
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	close(sequences)
	for err := range errorsFound {
		t.Errorf("Append error = %v", err)
	}
	seen := make(map[uint64]bool, count)
	for sequence := range sequences {
		if sequence == 0 || sequence > count || seen[sequence] {
			t.Fatalf("invalid sequence %d", sequence)
		}
		seen[sequence] = true
	}
	if len(seen) != count {
		t.Fatalf("unique sequence count = %d", len(seen))
	}

	var replayed uint64
	if err := source.Replay(context.Background(), func(record journal.Record) error {
		replayed++
		if record.Entry.JournalSeq != replayed {
			t.Fatalf("replay sequence = %d, want %d", record.Entry.JournalSeq, replayed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replayed != count {
		t.Fatalf("replayed = %d", replayed)
	}
}
