// Package journal implements the encrypted append-only Cortex source of truth.
package journal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
)

const (
	currentVersion = 1
	maxEntrySize   = 64 << 20
)

var (
	// ErrCorrupt indicates a truncated, unauthentic, or invalid journal.
	ErrCorrupt = errors.New("journal: corrupt")
	// ErrClosed indicates use after Close.
	ErrClosed = errors.New("journal: closed")
)

// Cipher is the narrow per-object envelope encryption boundary.
type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

// Mutation is an append-only state transition.
type Mutation string

const (
	Store   Mutation = "store"
	Update  Mutation = "update"
	Archive Mutation = "archive"
)

// Entry is the canonical plaintext payload encrypted into one journal frame.
type Entry struct {
	Version     int             `json:"version"`
	Type        Mutation        `json:"type"`
	MemoryID    uuid.UUID       `json:"memory_id"`
	MemoryType  memory.Type     `json:"memory_type"`
	Content     json.RawMessage `json:"content"`
	PrevVersion *uint64         `json:"prev_version"`
	Timestamp   int64           `json:"timestamp"`
	JournalSeq  uint64          `json:"journal_seq"`
	Actor       string          `json:"actor,omitempty"`
	CreatedBy   string          `json:"created_by,omitempty"`
	SourceTrust *float64        `json:"source_trust,omitempty"`
}

// Record includes the authenticated ciphertext used by integrity structures.
type Record struct {
	Entry      Entry
	Ciphertext []byte
}

// Journal serializes writers and fsyncs every accepted mutation.
type Journal struct {
	mu      sync.Mutex
	file    *os.File
	cipher  Cipher
	nextSeq uint64
}

// Open validates the complete existing journal before accepting appends.
func Open(path string, cipher Cipher) (*Journal, error) {
	if path == "" || cipher == nil {
		return nil, fmt.Errorf("journal: path and cipher are required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("journal: create directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: open: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("journal: secure permissions: %w", err)
	}
	journal := &Journal{file: file, cipher: cipher, nextSeq: 1}
	var last uint64
	if err := journal.scan(context.Background(), func(record Record) error {
		if record.Entry.JournalSeq != last+1 {
			return fmt.Errorf("%w: non-monotonic sequence", ErrCorrupt)
		}
		last = record.Entry.JournalSeq
		return nil
	}); err != nil {
		_ = file.Close()
		return nil, err
	}
	journal.nextSeq = last + 1
	return journal, nil
}

// Append validates, canonicalizes, encrypts, length-prefixes, and syncs one
// transition. JournalSeq is always assigned by the journal.
func (journal *Journal) Append(ctx context.Context, entry Entry) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.file == nil {
		return Record{}, ErrClosed
	}
	entry.Version = currentVersion
	entry.JournalSeq = journal.nextSeq
	canonicalContent, err := canonicalJSON(entry.Content)
	if err != nil {
		return Record{}, err
	}
	entry.Content = canonicalContent
	if err := validateEntry(entry); err != nil {
		return Record{}, err
	}
	plaintext, err := json.Marshal(entry)
	if err != nil {
		return Record{}, fmt.Errorf("journal: encode entry: %w", err)
	}
	envelope, err := journal.cipher.Encrypt(plaintext)
	if err != nil {
		return Record{}, fmt.Errorf("journal: encrypt entry: %w", err)
	}
	if len(envelope) == 0 || len(envelope) > maxEntrySize {
		return Record{}, fmt.Errorf("journal: encrypted entry size is invalid")
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(envelope)))
	frame := make([]byte, 0, len(length)+len(envelope))
	frame = append(frame, length[:]...)
	frame = append(frame, envelope...)
	if _, err := journal.file.Write(frame); err != nil {
		return Record{}, fmt.Errorf("journal: append: %w", err)
	}
	if err := journal.file.Sync(); err != nil {
		return Record{}, fmt.Errorf("journal: sync: %w", err)
	}
	journal.nextSeq++
	return Record{Entry: cloneEntry(entry), Ciphertext: append([]byte(nil), envelope...)}, nil
}

// Replay verifies and emits every record in sequence.
func (journal *Journal) Replay(
	ctx context.Context,
	consume func(Record) error,
) error {
	if consume == nil {
		return fmt.Errorf("journal: replay consumer is required")
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.file == nil {
		return ErrClosed
	}
	return journal.scan(ctx, consume)
}

func (journal *Journal) scan(
	ctx context.Context,
	consume func(Record) error,
) error {
	if _, err := journal.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("journal: seek: %w", err)
	}
	reader := bufio.NewReader(journal.file)
	expectedSeq := uint64(1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var length [4]byte
		_, err := io.ReadFull(reader, length[:])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: truncated length prefix", ErrCorrupt)
		}
		size := binary.BigEndian.Uint32(length[:])
		if size == 0 || size > maxEntrySize {
			return fmt.Errorf("%w: invalid entry length", ErrCorrupt)
		}
		envelope := make([]byte, size)
		if _, err := io.ReadFull(reader, envelope); err != nil {
			return fmt.Errorf("%w: truncated entry", ErrCorrupt)
		}
		plaintext, err := journal.cipher.Decrypt(envelope)
		if err != nil {
			return fmt.Errorf("%w: authentication failed", ErrCorrupt)
		}
		entry, err := decodeEntry(plaintext)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if entry.JournalSeq != expectedSeq {
			return fmt.Errorf("%w: expected sequence %d", ErrCorrupt, expectedSeq)
		}
		expectedSeq++
		if err := consume(Record{
			Entry:      cloneEntry(entry),
			Ciphertext: append([]byte(nil), envelope...),
		}); err != nil {
			return err
		}
	}
}

// Close closes the journal after syncing prior writes.
func (journal *Journal) Close() error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.file == nil {
		return nil
	}
	err := journal.file.Close()
	journal.file = nil
	return err
}

func decodeEntry(plaintext []byte) (Entry, error) {
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var entry Entry
	if err := decoder.Decode(&entry); err != nil {
		return Entry{}, fmt.Errorf("decode entry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Entry{}, fmt.Errorf("multiple JSON values")
	}
	if err := validateEntry(entry); err != nil {
		return Entry{}, err
	}
	canonical, err := json.Marshal(entry)
	if err != nil || !bytes.Equal(canonical, plaintext) {
		return Entry{}, fmt.Errorf("entry is not canonical")
	}
	return entry, nil
}

func validateEntry(entry Entry) error {
	if entry.Version != currentVersion {
		return fmt.Errorf("journal: unsupported version %d", entry.Version)
	}
	switch entry.Type {
	case Store:
		if entry.PrevVersion != nil {
			return fmt.Errorf("journal: store cannot have a previous version")
		}
	case Update, Archive:
		if entry.PrevVersion == nil {
			return fmt.Errorf("journal: %s requires a previous version", entry.Type)
		}
	default:
		return fmt.Errorf("journal: invalid mutation %q", entry.Type)
	}
	if entry.MemoryID == uuid.Nil {
		return fmt.Errorf("journal: memory ID is required")
	}
	if err := entry.MemoryType.Validate(); err != nil {
		return err
	}
	if entry.JournalSeq == 0 {
		return fmt.Errorf("journal: sequence must be positive")
	}
	if entry.Timestamp <= 0 {
		return fmt.Errorf("journal: timestamp must be positive")
	}
	if len(entry.Content) == 0 || !json.Valid(entry.Content) {
		return fmt.Errorf("journal: content must be valid JSON")
	}
	if entry.SourceTrust != nil &&
		(math.IsNaN(*entry.SourceTrust) ||
			math.IsInf(*entry.SourceTrust, 0) ||
			*entry.SourceTrust < 0 ||
			*entry.SourceTrust > 1) {
		return fmt.Errorf("journal: source trust must be between 0 and 1")
	}
	return nil
}

func canonicalJSON(content json.RawMessage) (json.RawMessage, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("journal: content is required")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("journal: invalid content: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("journal: content contains multiple values")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("journal: canonicalize content: %w", err)
	}
	return canonical, nil
}

func cloneEntry(entry Entry) Entry {
	entry.Content = append(json.RawMessage(nil), entry.Content...)
	if entry.PrevVersion != nil {
		previous := *entry.PrevVersion
		entry.PrevVersion = &previous
	}
	if entry.SourceTrust != nil {
		trust := *entry.SourceTrust
		entry.SourceTrust = &trust
	}
	return entry
}
