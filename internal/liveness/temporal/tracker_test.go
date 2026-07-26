package temporal

import (
	"testing"
	"time"
)

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time { return clock.now }

func TestRestorePreservesAbsoluteMarkersAndSocialTaskAbsence(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)}
	tracker, err := New(clock)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(30 * time.Minute)
	tracker.UserInteraction()
	if tracker.Snapshot().TaskDuration != 0 {
		t.Fatal("social interaction manufactured task duration")
	}
	tracker.StartTask()
	deadline := clock.now.Add(2 * time.Hour)
	if err := tracker.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	state := tracker.State()
	clock.now = clock.now.Add(45 * time.Minute)
	restored, err := Restore(clock, state)
	if err != nil {
		t.Fatal(err)
	}
	signals := restored.Snapshot()
	if signals.SessionDuration != 75*time.Minute ||
		signals.IdleDuration != 45*time.Minute ||
		signals.TaskDuration != 45*time.Minute ||
		signals.DeadlineProximity != 75*time.Minute {
		t.Fatalf("restored signals = %+v", signals)
	}
}

func TestNormalStagesInterruptionReturnAndClosureAreDurable(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)}
	tracker, err := New(clock)
	if err != nil {
		t.Fatal(err)
	}
	tracker.StartTask()
	if got := tracker.Snapshot().Stage; got != StageEarly {
		t.Fatalf("early stage = %q", got)
	}
	clock.now = clock.now.Add(20 * time.Minute)
	if got := tracker.Snapshot().Stage; got != StageMiddle {
		t.Fatalf("middle stage = %q", got)
	}
	tracker.InterruptTask()
	if signals := tracker.Snapshot(); !signals.Interrupted ||
		signals.Stage != StageInterrupted {
		t.Fatalf("interrupted signals = %+v", signals)
	}
	state := tracker.State()
	clock.now = clock.now.Add(2 * time.Hour)
	restored, err := Restore(clock, state)
	if err != nil {
		t.Fatal(err)
	}
	restored.UserInteraction()
	if signals := restored.Snapshot(); !signals.RecentlyReturned ||
		signals.ReturnedAfter != 2*time.Hour ||
		signals.Stage != StageReturned {
		t.Fatalf("return signals = %+v", signals)
	}
	restored.CompleteTask()
	if signals := restored.Snapshot(); !signals.RecentlyCompleted ||
		signals.Stage != StageClosure {
		t.Fatalf("closure signals = %+v", signals)
	}
}
