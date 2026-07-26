package canary

import (
	"errors"
	"testing"
	"time"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type testAlertSink struct {
	events []AccessEvent
	alerts []AlertEvent
}

func (s *testAlertSink) CanaryAccessed(event AccessEvent) {
	s.events = append(s.events, event)
}

func (s *testAlertSink) CanaryAlerted(event AlertEvent) {
	s.alerts = append(s.alerts, event)
}

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func Test_Manager_SeedAndDetect(t *testing.T) {
	clock := &testClock{baseTime}
	sink := &testAlertSink{}
	mgr, err := NewManager(ManagerConfig{
		Clock:     clock,
		AlertSink: sink,
		EventSink: sink,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	canary := mgr.Seed("0x01", "secret-identity-data")
	if canary.ID == "" {
		t.Fatal("expected non-empty canary ID")
	}

	// TrapAccess should detect the canary.
	detected := mgr.TrapAccess(canary.ID, "test-probe")
	if !detected {
		t.Fatal("expected canary to be detected")
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 alert event, got %d", len(sink.events))
	}
	if sink.events[0].Source != "test-probe" {
		t.Fatalf("expected source test-probe, got %s", sink.events[0].Source)
	}
	if len(sink.alerts) != 1 || sink.alerts[0].Operation != OperationRead {
		t.Fatalf("alerts = %+v", sink.alerts)
	}
}

func Test_Manager_NonCanaryNotDetected(t *testing.T) {
	clock := &testClock{baseTime}
	mgr, _ := NewManager(ManagerConfig{Clock: clock})
	detected := mgr.TrapAccess("real-memory-id", "normal-read")
	if detected {
		t.Fatal("non-canary should not be detected")
	}
}

func Test_Manager_IsCanary(t *testing.T) {
	clock := &testClock{baseTime}
	mgr, _ := NewManager(ManagerConfig{Clock: clock})
	canary := mgr.Seed("0x02", "honeypot")

	if !mgr.IsCanary(canary.ID) {
		t.Fatal("expected IsCanary to return true")
	}
	if mgr.IsCanary("nonexistent") {
		t.Fatal("expected IsCanary to return false for non-canary")
	}
}

func Test_Manager_AccessCount(t *testing.T) {
	clock := &testClock{baseTime}
	mgr, _ := NewManager(ManagerConfig{Clock: clock})
	canary := mgr.Seed("0x01", "data")

	mgr.TrapAccess(canary.ID, "probe-1")
	mgr.TrapAccess(canary.ID, "probe-2")
	mgr.TrapAccess("nonexistent", "probe-3")

	if mgr.AccessCount() != 2 {
		t.Fatalf("expected 2 accesses, got %d", mgr.AccessCount())
	}
}

func Test_Manager_SeedDefault(t *testing.T) {
	clock := &testClock{baseTime}
	mgr, _ := NewManager(ManagerConfig{Clock: clock})
	mgr.SeedDefault()

	canaries := mgr.Canaries()
	if len(canaries) != 9 {
		t.Fatalf("expected 9 default canaries, got %d", len(canaries))
	}
	mgr.SeedDefault()
	if got := len(mgr.Canaries()); got != 9 {
		t.Fatalf("default seeding was not idempotent: %d", got)
	}
}

func Test_Manager_BlocksMutationAndArchiveWithAlerts(t *testing.T) {
	clock := &testClock{baseTime}
	sink := &testAlertSink{}
	mgr, _ := NewManager(ManagerConfig{Clock: clock, EventSink: sink})
	canary := mgr.Seed("0x02", "honeypot")

	if err := mgr.ProtectMutation(canary.ID, "cortex-update"); !errors.Is(err, ErrCanaryMutation) {
		t.Fatalf("ProtectMutation() error = %v", err)
	}
	if err := mgr.ProtectArchive(canary.ID, "decay-worker"); !errors.Is(err, ErrCanaryArchive) {
		t.Fatalf("ProtectArchive() error = %v", err)
	}
	if len(sink.alerts) != 2 ||
		sink.alerts[0].Operation != OperationModify ||
		sink.alerts[1].Operation != OperationArchive {
		t.Fatalf("alerts = %+v", sink.alerts)
	}
	if err := mgr.ProtectMutation("normal-memory", "cortex-update"); err != nil {
		t.Fatalf("normal mutation blocked: %v", err)
	}
}

func Test_Manager_DetectsContentTampering(t *testing.T) {
	clock := &testClock{baseTime}
	sink := &testAlertSink{}
	mgr, _ := NewManager(ManagerConfig{Clock: clock, EventSink: sink})
	canary := mgr.Seed("0x07", "immutable constraint")
	if err := mgr.VerifyContent(canary.ID, "immutable constraint", "integrity-scan"); err != nil {
		t.Fatalf("valid content rejected: %v", err)
	}
	if err := mgr.VerifyContent(canary.ID, "tampered", "integrity-scan"); !errors.Is(err, ErrCanaryTampered) {
		t.Fatalf("tampered content error = %v", err)
	}
	if len(sink.alerts) != 1 || sink.alerts[0].Operation != OperationIntegrity {
		t.Fatalf("alerts = %+v", sink.alerts)
	}
}

func Test_Manager_ReturnsDefensiveCanaryCopies(t *testing.T) {
	clock := &testClock{baseTime}
	mgr, _ := NewManager(ManagerConfig{Clock: clock})
	seeded := mgr.Seed("0x01", "original")
	seeded.Content = "mutated"
	if err := mgr.VerifyContent(seeded.ID, "original", "scan"); err != nil {
		t.Fatalf("seed return mutated manager state: %v", err)
	}
	mgr.TrapAccess(seeded.ID, "read")
	first := mgr.Canaries()
	*first[0].LastAccess = first[0].LastAccess.Add(time.Hour)
	second := mgr.Canaries()
	if second[0].LastAccess.Equal(*first[0].LastAccess) {
		t.Fatal("LastAccess pointer was shared through snapshot")
	}
}

func Test_Manager_NilClockRejected(t *testing.T) {
	_, err := NewManager(ManagerConfig{})
	if err == nil {
		t.Fatal("expected error for nil clock")
	}
}

func Test_Manager_InvalidMemoryTypeRejected(t *testing.T) {
	manager, _ := NewManager(ManagerConfig{Clock: &testClock{baseTime}})
	if canary := manager.Seed("invented", "data"); canary != nil {
		t.Fatalf("invalid canary seeded: %+v", canary)
	}
}

func Test_Canary_ContentHash(t *testing.T) {
	clock := &testClock{baseTime}
	mgr, _ := NewManager(ManagerConfig{Clock: clock})
	canary := mgr.Seed("0x01", "test-content")

	if canary.ContentHash == [32]byte{} {
		t.Fatal("expected non-zero content hash")
	}
}
