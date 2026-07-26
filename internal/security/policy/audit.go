package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileAuditor writes one canonical JSON event per line and syncs before
// returning, so a denial cannot disappear after the caller observes it.
type FileAuditor struct {
	mu   sync.Mutex
	file *os.File
	path string
}

// OpenFileAuditor opens a private append-only policy journal.
func OpenFileAuditor(path string) (*FileAuditor, error) {
	if path == "" {
		return nil, fmt.Errorf("policy: audit path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("policy: create audit directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("policy: open audit journal: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("policy: secure audit journal: %w", err)
	}
	return &FileAuditor{file: file, path: path}, nil
}

func (auditor *FileAuditor) RecordPolicyEvent(ctx context.Context, event AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("policy: encode audit event: %w", err)
	}
	encoded = append(encoded, '\n')

	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if auditor.file == nil {
		return fmt.Errorf("policy: audit journal is closed")
	}
	if _, err := auditor.file.Write(encoded); err != nil {
		return fmt.Errorf("policy: append audit event: %w", err)
	}
	if err := auditor.file.Sync(); err != nil {
		return fmt.Errorf("policy: sync audit event: %w", err)
	}
	return nil
}

// Events returns the newest bounded, redacted policy records in journal order.
// The caller controls redaction before writes; this reader never exposes
// anything beyond the canonical durable AuditEvent schema.
func (auditor *FileAuditor) Events(
	ctx context.Context,
	limit int,
) ([]AuditEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 256 {
		limit = 256
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if auditor.file == nil {
		return nil, fmt.Errorf("policy: audit journal is closed")
	}
	payload, err := os.ReadFile(auditor.path)
	if err != nil {
		return nil, fmt.Errorf("policy: read audit journal: %w", err)
	}
	lines := bytes.Split(bytes.TrimSpace(payload), []byte{'\n'})
	if len(lines) == 1 && len(lines[0]) == 0 {
		return []AuditEvent{}, nil
	}
	start := 0
	if len(lines) > limit {
		start = len(lines) - limit
	}
	events := make([]AuditEvent, 0, len(lines)-start)
	for index, line := range lines[start:] {
		var event AuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf(
				"policy: decode audit event %d: %w", start+index, err,
			)
		}
		events = append(events, event)
	}
	return events, nil
}

// Close flushes and closes the audit journal.
func (auditor *FileAuditor) Close() error {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	if auditor.file == nil {
		return nil
	}
	err := auditor.file.Close()
	auditor.file = nil
	return err
}

// MemoryAuditor is a concurrency-safe auditor useful for embedded consumers
// that persist events elsewhere after evaluation.
type MemoryAuditor struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (auditor *MemoryAuditor) RecordPolicyEvent(
	ctx context.Context,
	event AuditEvent,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	auditor.events = append(auditor.events, cloneAuditEvent(event))
	return nil
}

// Events returns a defensive snapshot.
func (auditor *MemoryAuditor) Events() []AuditEvent {
	auditor.mu.Lock()
	defer auditor.mu.Unlock()
	events := make([]AuditEvent, 0, len(auditor.events))
	for _, event := range auditor.events {
		events = append(events, cloneAuditEvent(event))
	}
	return events
}

func cloneAuditEvent(event AuditEvent) AuditEvent {
	event.Arguments = append(json.RawMessage(nil), event.Arguments...)
	return event
}
