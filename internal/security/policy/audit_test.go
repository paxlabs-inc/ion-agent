package policy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileAuditorPersistsPrivateSynchronizedEvents(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "policy.jsonl")
	auditor, err := OpenFileAuditor(path)
	if err != nil {
		t.Fatal(err)
	}
	event := AuditEvent{
		At:         time.Unix(10, 0),
		Layer:      SenderLayer,
		Decision:   Deny,
		Reason:     "not authorized",
		ToolCallID: "call-1",
		ToolName:   "publish",
	}
	if err := auditor.RecordPolicyEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("audit mode = %o, want 600", info.Mode().Perm())
	}
	if err := auditor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := auditor.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("missing audit record: %v", scanner.Err())
	}
	var decoded AuditEvent
	if err := json.Unmarshal(scanner.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Layer != SenderLayer || decoded.Reason != "not authorized" {
		t.Fatalf("decoded event = %+v", decoded)
	}
	if scanner.Scan() {
		t.Fatal("unexpected second audit record")
	}
	if err := auditor.RecordPolicyEvent(context.Background(), event); err == nil {
		t.Fatal("record after close succeeded")
	}
}

func TestAuditorsRejectInvalidPathsAndCancelledWrites(t *testing.T) {
	t.Parallel()
	if _, err := OpenFileAuditor(""); err == nil {
		t.Fatal("empty audit path accepted")
	}
	if _, err := OpenFileAuditor("/dev/null/policy.jsonl"); err == nil {
		t.Fatal("audit path below a file accepted")
	}
	directory := t.TempDir()
	if _, err := OpenFileAuditor(directory); err == nil {
		t.Fatal("directory accepted as audit file")
	}

	auditor, err := OpenFileAuditor(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer auditor.Close()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := auditor.RecordPolicyEvent(cancelled, AuditEvent{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled file audit error = %v", err)
	}
	memory := &MemoryAuditor{}
	if err := memory.RecordPolicyEvent(cancelled, AuditEvent{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled memory audit error = %v", err)
	}
}

func TestMemoryAuditorOwnsEventDataAndReturnsDefensiveSnapshots(t *testing.T) {
	t.Parallel()
	auditor := &MemoryAuditor{}
	event := AuditEvent{Arguments: json.RawMessage(`{"original":true}`)}
	if err := auditor.RecordPolicyEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.Arguments[2] = 'X'
	first := auditor.Events()
	if string(first[0].Arguments) != `{"original":true}` {
		t.Fatalf("stored arguments = %s", first[0].Arguments)
	}
	first[0].Arguments[2] = 'Y'
	second := auditor.Events()
	if string(second[0].Arguments) != `{"original":true}` {
		t.Fatalf("snapshot mutated stored arguments = %s", second[0].Arguments)
	}
}
