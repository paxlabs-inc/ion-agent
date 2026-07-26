package premise

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type testClock struct {
	now time.Time
}

func (clock *testClock) Now() time.Time { return clock.now }

func Test_Premise_AddAndRetrieve(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, err := New(clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	premise, err := ledger.Add("test premise", SourceToolEvidence, 0)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if premise.Status != Assumption {
		t.Fatalf("expected Assumption, got %s", premise.Status)
	}
	if premise.Statement != "test premise" {
		t.Fatalf("expected 'test premise', got %q", premise.Statement)
	}
	got, ok := ledger.Get(premise.ID)
	if !ok {
		t.Fatal("Get returned false")
	}
	if got.ID != premise.ID {
		t.Fatalf("ID mismatch")
	}
}

func Test_Premise_CiteWithCitation(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, err := New(clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	premise, _ := ledger.Add("test", SourceAssumption, 0)
	citation := protocol.Citation{
		ToolEventID:   uuid.New(),
		MMRLeafHash:   [32]byte{1},
		MMRRootAtTime: [32]byte{2},
		Verified:      true,
	}
	if err := ledger.Cite(premise.ID, citation); err != nil {
		t.Fatalf("Cite: %v", err)
	}
	got, _ := ledger.Get(premise.ID)
	if got.Status != Cited {
		t.Fatalf("expected Cited, got %s", got.Status)
	}
	if got.Citation == nil {
		t.Fatal("expected citation to be set")
	}
}

func Test_Premise_RefuteBlocksDispatch(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, err := New(clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	premise, _ := ledger.Add("test", SourceAssumption, 0)
	if ledger.HasRefuted() {
		t.Fatal("should not have refuted premises yet")
	}
	if err := ledger.Refute(premise.ID, nil); err != nil {
		t.Fatalf("Refute: %v", err)
	}
	if !ledger.HasRefuted() {
		t.Fatal("should have refuted premises")
	}
	got, _ := ledger.Get(premise.ID)
	if got.Status != Refuted {
		t.Fatalf("expected Refuted, got %s", got.Status)
	}
	if got.RefutedAt == nil {
		t.Fatal("expected RefutedAt to be set")
	}
}

func Test_Premise_ActiveExcludesRefuted(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, err := New(clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ledger.Add("a", SourceAssumption, 0)
	b, _ := ledger.Add("b", SourceAssumption, 1)
	ledger.Add("c", SourceAssumption, 2)
	ledger.Refute(b.ID, nil)
	active := ledger.Active()
	if len(active) != 2 {
		t.Fatalf("expected 2 active, got %d", len(active))
	}
}

func Test_Premise_PlanChangedClearsLedger(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, err := New(clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ledger.Add("old premise", SourceAssumption, 0)
	ledger.PlanChanged()
	active := ledger.Active()
	if len(active) != 0 {
		t.Fatalf("expected 0 active after plan change, got %d", len(active))
	}
}

func Test_Premise_RenderIncludesStatuses(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, err := New(clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ledger.Add("assumed fact", SourceAssumption, 0)
	cited, _ := ledger.Add("cited fact", SourceToolEvidence, 1)
	ledger.Cite(cited.ID, protocol.Citation{
		ToolEventID:   uuid.New(),
		MMRLeafHash:   [32]byte{1},
		MMRRootAtTime: [32]byte{2},
		Verified:      true,
	})
	rendered := ledger.Render()
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
}

func Test_Premise_DeterministicExtractor(t *testing.T) {
	extractor := DeterministicExtractor{}
	premises, err := extractor.Extract(context.Background(), Plan{
		Text: "I can read files and I have access to the network",
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(premises) == 0 {
		t.Fatal("expected at least one premise from self-referential text")
	}
}

func Test_Premise_EmptyStatementRejected(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, _ := New(clock)
	_, err := ledger.Add("", SourceAssumption, 0)
	if err == nil {
		t.Fatal("expected error for empty statement")
	}
}

func Test_Premise_NilClockRejected(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error for nil clock")
	}
}

func Test_Premise_RefuteIdempotent(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, _ := New(clock)
	p, _ := ledger.Add("test", SourceAssumption, 0)
	ledger.Refute(p.ID, nil)
	ledger.Refute(p.ID, nil)
	refuted := ledger.UnrevisedRefuted()
	if len(refuted) != 1 {
		t.Fatalf("expected 1 refuted, got %d", len(refuted))
	}
}

func Test_Premise_ReviseAffectedPreservesOtherSubtrees(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)}
	ledger, _ := New(clock)
	first, _ := ledger.Add("first", SourceAssumption, 0)
	second, _ := ledger.Add("second", SourceAssumption, 1)
	if err := ledger.Attach(first.ID, []string{"subgoal-a"}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Attach(second.ID, []string{"subgoal-b"}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Refute(first.ID, nil); err != nil {
		t.Fatal(err)
	}
	ledger.ReviseAffected([]string{"subgoal-a"})
	active := ledger.Active()
	if len(active) != 1 || active[0].ID != second.ID {
		t.Fatalf("unaffected premises = %+v", active)
	}
}

func Test_Premise_ExtractorReadsToolExpectations(t *testing.T) {
	extractor := DeterministicExtractor{}
	extracted, err := extractor.Extract(context.Background(), Plan{
		ToolCalls: []protocol.NormalizedToolCall{{
			ID: "call", Name: "probe",
			Arguments: json.RawMessage(
				`{"query":"status","expect":"version remains one"}`,
			),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(extracted) != 1 ||
		extracted[0].Statement != "probe: version remains one" ||
		extracted[0].Source != SourceAssumption {
		t.Fatalf("extracted premises = %+v", extracted)
	}
}
