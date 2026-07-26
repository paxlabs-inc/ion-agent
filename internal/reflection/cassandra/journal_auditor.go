package cassandra

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// EventWriter is the Cortex-compatible boundary used for encrypted,
// append-only Cassandra audit records.
type EventWriter interface {
	WriteEvent(context.Context, []byte, string) error
}

// JournalAuditor persists every edit and compensating undo as a Cortex event.
type JournalAuditor struct {
	writer EventWriter
	actor  string
}

// NewJournalAuditor constructs a durable Cassandra auditor.
func NewJournalAuditor(writer EventWriter, actor string) (*JournalAuditor, error) {
	if writer == nil {
		return nil, fmt.Errorf("cassandra: event writer is required")
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("cassandra: audit actor is required")
	}
	return &JournalAuditor{writer: writer, actor: actor}, nil
}

// RecordCassandraEvent commits the full dual record below Cortex encryption.
func (auditor *JournalAuditor) RecordCassandraEvent(edit Edit) error {
	encoded, err := json.Marshal(edit)
	if err != nil {
		return fmt.Errorf("cassandra: encode audit event: %w", err)
	}
	if err := auditor.writer.WriteEvent(
		context.Background(), encoded, auditor.actor,
	); err != nil {
		return fmt.Errorf("cassandra: write audit event: %w", err)
	}
	return nil
}
