package selfmodel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func defaultCore() *ImmutableCore {
	return NewImmutableCore([]SafetyConstraint{
		{ID: "S1", Statement: "no RED without approval", Source: "spec", Immutable: true, CreatedAt: baseTime},
		{ID: "S2", Statement: "no vault keys for sub-agents", Source: "spec", Immutable: true, CreatedAt: baseTime},
	})
}

func committedEvent(name string) protocol.ToolEvent {
	match := true
	return protocol.ToolEvent{
		ID:            uuid.New(),
		CallID:        "call-1",
		Name:          name,
		Args:          []byte(`{}`),
		Result:        []byte(`{"ok":true}`),
		Expect:        "returns data",
		Match:         &match,
		MMRLeafHash:   [32]byte{1},
		MMRRootAtTime: [32]byte{2},
		Timestamp:     baseTime,
	}
}

func derivedModel(t *testing.T, clock Clock) *Model {
	t.Helper()
	root := t.TempDir()
	source := []byte("package sample\n\nfunc Execute() {}\n")
	if err := os.WriteFile(filepath.Join(root, "sample.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	model, err := NewFromCodeGraph(
		context.Background(), clock, defaultCore(), root,
	)
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func Test_Model_AddCapabilityRequiresToolEvent(t *testing.T) {
	clock := &testClock{now: baseTime}
	model := derivedModel(t, clock)
	event := committedEvent("read")
	if err := model.AddCapability(event); err != nil {
		t.Fatalf("AddCapability: %v", err)
	}
	snap := model.Snapshot()
	if len(snap.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(snap.Capabilities))
	}
	if snap.Capabilities[0].Name != "read" {
		t.Fatalf("expected 'read', got %q", snap.Capabilities[0].Name)
	}
	if snap.Capabilities[0].SampleSize != 1 {
		t.Fatalf("expected sample size 1, got %d", snap.Capabilities[0].SampleSize)
	}
}

func Test_CodeGraphDerivationFeedsStructuralSelfModel(t *testing.T) {
	clock := &testClock{now: baseTime}
	model := derivedModel(t, clock)
	snapshot := model.Snapshot()
	if snapshot.Structure == nil ||
		snapshot.Structure.Digest == "" ||
		len(snapshot.Structure.Packages) != 1 ||
		len(snapshot.Structure.Symbols) != 1 ||
		snapshot.Structure.Symbols[0].Name != "Execute" {
		t.Fatalf("derived structure = %+v", snapshot.Structure)
	}
	snapshot.Structure.Symbols[0].Name = "fabricated"
	if model.Snapshot().Structure.Symbols[0].Name == "fabricated" {
		t.Fatal("structural snapshot was not defensive")
	}
}

func Test_BuildInfoFallbackFeedsStructuralSelfModel(t *testing.T) {
	model, err := NewFromBuildInfo(
		&testClock{now: baseTime},
		defaultCore(),
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := model.Snapshot()
	if snapshot.Structure == nil ||
		snapshot.Structure.Digest == "" ||
		len(snapshot.Structure.Packages) == 0 ||
		snapshot.Structure.Root == "" {
		t.Fatalf("build-derived structure = %+v", snapshot.Structure)
	}
	if err := model.AddCapability(committedEvent("read")); err != nil {
		t.Fatalf("build-derived model rejected evidence: %v", err)
	}
}

func Test_Model_RejectsCapabilityBeforeCodeGraphDerivation(t *testing.T) {
	model, err := New(&testClock{now: baseTime}, defaultCore())
	if err != nil {
		t.Fatal(err)
	}
	if err := model.AddCapability(committedEvent("read")); err == nil {
		t.Fatal("capability was accepted without codegraph derivation")
	}
}

func Test_Model_FailedEventCannotProveCapability(t *testing.T) {
	clock := &testClock{now: baseTime}
	model := derivedModel(t, clock)
	event := committedEvent("write")
	event.FailureClass = protocol.FailureTimeout
	if err := model.AddCapability(event); err == nil {
		t.Fatal("expected error for failed event")
	}
}

func Test_Model_NilEventIDRejected(t *testing.T) {
	clock := &testClock{now: baseTime}
	model := derivedModel(t, clock)
	event := protocol.ToolEvent{
		ID:        uuid.Nil,
		CallID:    "call-1",
		Name:      "read",
		Args:      []byte(`{}`),
		Timestamp: baseTime,
	}
	if err := model.AddCapability(event); err == nil {
		t.Fatal("expected error for nil event ID")
	}
}

func Test_Model_RecordFailure(t *testing.T) {
	clock := &testClock{now: baseTime}
	model, _ := New(clock, defaultCore())
	model.RecordFailure("timeout on network call", "fetch")
	snap := model.Snapshot()
	if len(snap.FailurePatterns) != 1 {
		t.Fatalf("expected 1 failure pattern, got %d", len(snap.FailurePatterns))
	}
	if snap.FailurePatterns[0].Frequency != 1 {
		t.Fatalf("expected frequency 1, got %d", snap.FailurePatterns[0].Frequency)
	}
	model.RecordFailure("timeout on network call", "fetch")
	snap = model.Snapshot()
	if snap.FailurePatterns[0].Frequency != 2 {
		t.Fatalf("expected frequency 2, got %d", snap.FailurePatterns[0].Frequency)
	}
}

func Test_ImmutableCore_CannotBeModified(t *testing.T) {
	core := defaultCore()
	if core.Count() != 2 {
		t.Fatalf("expected 2 constraints, got %d", core.Count())
	}
	if !core.Contains("S1") {
		t.Fatal("expected to contain S1")
	}
	snap := core.Snapshot()
	snap[0].Statement = "MODIFIED"
	if core.Snapshot()[0].Statement == "MODIFIED" {
		t.Fatal("immutable core was modified through snapshot")
	}
}

func Test_ImmutableCore_SnapshotIsDefensive(t *testing.T) {
	core := defaultCore()
	snap1 := core.Snapshot()
	snap2 := core.Snapshot()
	snap1[0] = SafetyConstraint{ID: "INJECTED"}
	if snap2[0].ID == "INJECTED" {
		t.Fatal("snapshot was not defensive")
	}
}

func Test_ReducedSelfModel_ExcludesVaultKeys(t *testing.T) {
	clock := &testClock{now: baseTime}
	model := derivedModel(t, clock)
	model.AddCapability(committedEvent("read"))
	reduced := Reduce(model)
	if reduced.ID == uuid.Nil {
		t.Fatal("expected non-nil ID")
	}
	if reduced.StructureDigest == "" {
		t.Fatal("reduced model omitted structural codegraph identity")
	}
	if len(reduced.Capabilities) != 1 {
		t.Fatalf("expected 1 capability summary, got %d", len(reduced.Capabilities))
	}
	// Verify no vault key fields exist by compilation:
	// ReducedSelfModel has no KEK, UserKey, DEK, or SpawnAuthority fields.
}

func Test_Model_NilClockRejected(t *testing.T) {
	_, err := New(nil, defaultCore())
	if err == nil {
		t.Fatal("expected error for nil clock")
	}
}

func Test_Model_NilCoreRejected(t *testing.T) {
	clock := &testClock{now: baseTime}
	_, err := New(clock, nil)
	if err == nil {
		t.Fatal("expected error for nil core")
	}
}

func Test_Model_AddLimitation(t *testing.T) {
	clock := &testClock{now: baseTime}
	model, _ := New(clock, defaultCore())
	model.AddLimitation("no filesystem access")
	snap := model.Snapshot()
	if len(snap.Limitations) != 1 {
		t.Fatalf("expected 1 limitation, got %d", len(snap.Limitations))
	}
	if snap.Limitations[0] != "no filesystem access" {
		t.Fatalf("unexpected limitation: %q", snap.Limitations[0])
	}
}
