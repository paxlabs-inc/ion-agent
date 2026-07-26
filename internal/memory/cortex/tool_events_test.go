package cortex

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestToolEventCommitProducesVerifiableHistoricalCitation(t *testing.T) {
	harness := newTestCortex(t)
	event := protocol.ToolEvent{
		ID:        uuid.New(),
		CallID:    "call-read",
		Name:      "read",
		Args:      []byte(`{"path":"spec/spec.kvx"}`),
		Result:    []byte(`{"bytes":42}`),
		Expect:    "returns non-empty data",
		Timestamp: baseTime,
	}
	committed, err := harness.store.CommitToolEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if committed.MMRLeafHash == ([32]byte{}) ||
		committed.MMRRootAtTime == ([32]byte{}) {
		t.Fatal("commit did not produce an MMR receipt")
	}
	citation := protocol.Citation{
		ToolEventID:   committed.ID,
		MMRLeafHash:   committed.MMRLeafHash,
		MMRRootAtTime: committed.MMRRootAtTime,
	}
	verified, err := harness.store.VerifyCitation(
		context.Background(), citation, *committed,
	)
	if err != nil || !verified {
		t.Fatalf("VerifyCitation() = %v, %v", verified, err)
	}

	// Later leaves must not invalidate a proof against the historical root.
	if _, err := harness.store.Write(
		context.Background(),
		"0x02",
		[]byte(`{"later":true}`),
		"test",
	); err != nil {
		t.Fatal(err)
	}
	verified, err = harness.store.VerifyCitation(
		context.Background(), citation, *committed,
	)
	if err != nil || !verified {
		t.Fatalf("historical VerifyCitation() = %v, %v", verified, err)
	}

	fabricated := citation
	fabricated.MMRLeafHash[0] ^= 0xff
	if verified, err = harness.store.VerifyCitation(
		context.Background(), fabricated, *committed,
	); err != nil || verified {
		t.Fatalf("fabricated VerifyCitation() = %v, %v", verified, err)
	}
}

func TestToolEventReceiptReconstructedOnReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.journal")
	source, err := journal.Open(path, &testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: baseTime}
	first, err := New(Config{Actor: "actor", Journal: source, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	event := protocol.ToolEvent{
		ID: uuid.New(), CallID: "call", Name: "probe",
		Args: []byte(`{}`), Result: []byte(`null`), Timestamp: baseTime,
	}
	committed, err := first.CommitToolEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	replayed, err := New(Config{Actor: "actor", Journal: source, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = replayed.Close()
		_ = source.Close()
	})
	got, ok := replayed.GetToolEvent(event.ID)
	if !ok || got.MMRLeafHash != committed.MMRLeafHash ||
		got.MMRRootAtTime != committed.MMRRootAtTime {
		t.Fatalf("replayed event receipt = %+v, ok=%v", got, ok)
	}
}
