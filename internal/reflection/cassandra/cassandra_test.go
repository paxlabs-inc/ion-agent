package cassandra

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }
func (c *testClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

type testAuditor struct {
	events []Edit
}

func (a *testAuditor) RecordCassandraEvent(edit Edit) error {
	a.events = append(a.events, edit)
	return nil
}

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func Test_Controller_EditProducesDelta(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, err := New(clock, auditor)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Use strings where the edit distance is within 30% of max length.
	// "original content here" -> "original content herf" = 1 edit / 21 chars = ~5%
	edit, err := controller.Edit(
		"msg-1",
		"original content here",
		"original content herf",
		TriggerPredictionMismatch,
		SideDoubt,
		"prediction was wrong",
		"cassandra",
		false,
	)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edit.OriginalContent != "original content here" {
		t.Fatal("original not preserved")
	}
	if edit.ResultContent != "original content herf" {
		t.Fatal("result not set")
	}
	if edit.OriginalHash == "" {
		t.Fatal("original hash not computed")
	}
	if edit.Trigger != TriggerPredictionMismatch {
		t.Fatal("trigger mismatch")
	}
}

func Test_Controller_RateLimitBlocksExcessEdits(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	for i := 0; i < MaxEditsPerTurn; i++ {
		// Each edit uses a unique msgID and stays within change ratio.
		orig := "this is a long enough string for edit " + string(rune('a'+i))
		mod := "this is a long enough string for edif " + string(rune('a'+i))
		_, err := controller.Edit(
			"msg-"+string(rune('0'+i)),
			orig,
			mod,
			TriggerPredictionMismatch,
			SideDoubt,
			"reason",
			"cassandra",
			false,
		)
		if err != nil {
			t.Fatalf("Edit %d: %v", i, err)
		}
	}
	_, err := controller.Edit(
		"msg-extra",
		"this is a long enough string for edit x",
		"this is a long enough string for edif x",
		TriggerPredictionMismatch,
		SideDoubt,
		"reason",
		"cassandra",
		false,
	)
	if err == nil {
		t.Fatal("expected rate limit error")
	}
}

func Test_Controller_CooldownBetweenEdits(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	// First edit: use strings within 30% change ratio.
	controller.Edit("msg-1",
		"this is a long enough test string here",
		"this is a long enough test string herf",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	_, err := controller.Edit("msg-1",
		"this is a long enough test string herf",
		"this is a long enough test string herg",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	if err == nil {
		t.Fatal("expected cooldown error")
	}
	clock.Advance(CooldownBetweenEdits + time.Second)
	_, err = controller.Edit("msg-1",
		"this is a long enough test string herf",
		"this is a long enough test string herg",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	if err != nil {
		t.Fatalf("Edit after cooldown: %v", err)
	}
}

func Test_Controller_ContentChangeRatioExceeded(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	short := "x"
	_, err := controller.Edit("msg-1", long, short, TriggerPredictionMismatch, SideDoubt, "", "", false)
	if err == nil {
		t.Fatal("expected change ratio error")
	}
}

func Test_Controller_UndoRestoresOriginal(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	edit, _ := controller.Edit("msg-1",
		"this is a long enough test string here",
		"this is a long enough test string herf",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	undone, err := controller.Undo(nil)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undone.ID != edit.ID {
		t.Fatal("undo should return the same edit")
	}
	if undone.State != EditUndone {
		t.Fatalf("expected undone state, got %s", undone.State)
	}
	if undone.UndoneAt == nil {
		t.Fatal("expected UndoneAt to be set")
	}
}

func Test_Controller_UndoExpiredWindow(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	controller.Edit("msg-1",
		"this is a long enough test string here",
		"this is a long enough test string herf",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	clock.Advance(UndoWindow + time.Hour)
	_, err := controller.Undo(nil)
	if err == nil {
		t.Fatal("expected undo window expired error")
	}
}

func Test_Controller_UndoSpecificEdit(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	edit1, _ := controller.Edit("msg-1",
		"this is a long enough test string one",
		"this is a long enough test string onf",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	clock.Advance(CooldownBetweenEdits + time.Second)
	edit2, _ := controller.Edit("msg-2",
		"this is a long enough test string two",
		"this is a long enough test string twp",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	undone, err := controller.Undo(&edit2.ID)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undone.ID != edit2.ID {
		t.Fatal("wrong edit undone")
	}
	active := controller.ActiveEdits()
	if len(active) != 1 {
		t.Fatalf("expected 1 active edit, got %d", len(active))
	}
	if active[0].ID != edit1.ID {
		t.Fatal("wrong edit remained active")
	}
	_ = edit1
}

func Test_Controller_NewTurnResetsCounter(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	for i := 0; i < MaxEditsPerTurn; i++ {
		orig := "this is a long enough test string " + string(rune('a'+i))
		mod := "this is a long enough test string " + string(rune('A'+i))
		controller.Edit("msg-"+string(rune('0'+i)), orig, mod, TriggerPredictionMismatch, SideDoubt, "", "", false)
		clock.Advance(CooldownBetweenEdits + time.Second)
	}
	controller.NewTurn()
	_, err := controller.Edit("msg-new",
		"this is a long enough test string z",
		"this is a long enough test string Z",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	if err != nil {
		t.Fatalf("Edit after new turn: %v", err)
	}
}

func Test_Controller_AuditTrailPreserved(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	controller.Edit("msg-1",
		"this is a long enough test string here",
		"this is a long enough test string herf",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	controller.Undo(nil)
	if len(auditor.events) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(auditor.events))
	}
	if auditor.events[0].State != EditActive {
		t.Fatal("first event should be active")
	}
	if auditor.events[1].State != EditUndone {
		t.Fatal("second event should be undone")
	}
}

func Test_IsProtected_KnownTypes(t *testing.T) {
	if !IsProtected("0x01") {
		t.Fatal("Identity should be protected")
	}
	if !IsProtected("0x07") {
		t.Fatal("Constraint should be protected")
	}
	if IsProtected("0x02") {
		t.Fatal("Fact should not be protected")
	}
}

func Test_NeedsApproval_DetectsProtected(t *testing.T) {
	if !NeedsApproval("editing SOUL.md") {
		t.Fatal("SOUL.md should require approval")
	}
	if NeedsApproval("editing Fact memory") {
		t.Fatal("Fact should not require approval")
	}
}

func Test_Edit_ProtectedContentRequiresApproval(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, err := New(clock, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Edit(
		"message",
		"SOUL.md is immutable and safe.",
		"SOUL.md is immutable and very safe.",
		TriggerUserCorrection,
		SideAssurance,
		"correction",
		"agent",
		false,
	); err == nil {
		t.Fatal("protected edit without approval was accepted")
	}
	if len(auditor.events) != 0 {
		t.Fatal("rejected protected edit was audited as committed")
	}
}

func Test_Controller_NilClockRejected(t *testing.T) {
	_, err := New(nil, &testAuditor{})
	if err == nil {
		t.Fatal("expected error for nil clock")
	}
}

func Test_Controller_NilAuditorRejected(t *testing.T) {
	clock := &testClock{now: baseTime}
	_, err := New(clock, nil)
	if err == nil {
		t.Fatal("expected error for nil auditor")
	}
}

func Test_Controller_NoChangeRejected(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	_, err := controller.Edit("msg-1", "same", "same", TriggerPredictionMismatch, SideDoubt, "", "", false)
	if err == nil {
		t.Fatal("expected error for no change")
	}
}

func Test_Controller_UndoByIDNotFound(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	fakeID := uuid.New()
	_, err := controller.Undo(&fakeID)
	if err == nil {
		t.Fatal("expected error for non-existent edit")
	}
}

func Test_Controller_BLAKE3Hash(t *testing.T) {
	clock := &testClock{now: baseTime}
	auditor := &testAuditor{}
	controller, _ := New(clock, auditor)
	edit, _ := controller.Edit("msg-1",
		"this is a long enough test string here",
		"this is a long enough test string herf",
		TriggerPredictionMismatch, SideDoubt, "", "", false)
	// BLAKE3-256 produces 64 hex characters.
	if len(edit.OriginalHash) != 64 {
		t.Fatalf("expected 64 char hex hash, got %d chars: %s", len(edit.OriginalHash), edit.OriginalHash)
	}
	if len(edit.ResultHash) != 64 {
		t.Fatalf("expected 64 char hex hash, got %d chars: %s", len(edit.ResultHash), edit.ResultHash)
	}
	if edit.OriginalHash == edit.ResultHash {
		t.Fatal("different content should produce different hashes")
	}
}

func Test_ContentChangeRatio(t *testing.T) {
	tests := []struct {
		orig, mod string
		maxRatio  float64
	}{
		{"hello world", "hello world", 0.0},
		{"", "", 0.0},
		{"a", "b", 1.0},
		{"aaaa", "xaaa", 0.26},                         // 1 substitution / 4 = 0.25
		{"hello world test", "hello world tess", 0.07}, // 1/16 ~ 0.0625
	}
	for _, tt := range tests {
		ratio := contentChangeRatio(tt.orig, tt.mod)
		if ratio > tt.maxRatio {
			t.Errorf("contentChangeRatio(%q, %q) = %f, want <= %f", tt.orig, tt.mod, ratio, tt.maxRatio)
		}
	}
}
