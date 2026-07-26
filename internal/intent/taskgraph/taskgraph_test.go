package taskgraph

import (
	"strings"
	"testing"
)

func Test_TaskGraph_AddAndRetrieveSubgoal(t *testing.T) {
	graph := New("test goal", 0)
	sub := graph.AddSubgoal("step 1")
	if sub.Text != "step 1" {
		t.Fatalf("expected 'step 1', got %q", sub.Text)
	}
	if sub.Status != StatusPending {
		t.Fatalf("expected pending, got %s", sub.Status)
	}
	current := graph.Current()
	if current == nil {
		t.Fatal("expected non-nil current")
	}
	if current.ID != sub.ID {
		t.Fatal("current should be the added subgoal")
	}
}

func Test_TaskGraph_EvidenceGrowth(t *testing.T) {
	graph := New("test", 4)
	graph.AddSubgoal("step 1")
	current := graph.Current()
	grew := graph.ObserveAction("evidence-1", current.ID)
	if !grew {
		t.Fatal("first evidence should grow the set")
	}
	if graph.EvidenceCount() != 1 {
		t.Fatalf("expected 1 evidence, got %d", graph.EvidenceCount())
	}
	if graph.ActionsSinceGrowth() != 0 {
		t.Fatalf("expected 0 since growth, got %d", graph.ActionsSinceGrowth())
	}
}

func Test_TaskGraph_DuplicateEvidenceNoGrowth(t *testing.T) {
	graph := New("test", 4)
	graph.AddSubgoal("step 1")
	current := graph.Current()
	graph.ObserveAction("evidence-1", current.ID)
	grew := graph.ObserveAction("evidence-1", current.ID)
	if grew {
		t.Fatal("duplicate evidence should not grow")
	}
	if graph.ActionsSinceGrowth() != 1 {
		t.Fatalf("expected 1 since growth, got %d", graph.ActionsSinceGrowth())
	}
}

func Test_TaskGraph_ConvergenceTrips(t *testing.T) {
	graph := New("test", 3)
	graph.AddSubgoal("step 1")
	current := graph.Current()
	graph.ObserveAction("e-initial", current.ID) // growth
	graph.ObserveAction("e-dup", current.ID)     // growth
	graph.ObserveAction("e-dup", current.ID)     // no growth (1)
	graph.ObserveAction("e-dup", current.ID)     // no growth (2)
	graph.ObserveAction("e-dup", current.ID)     // no growth (3)
	if !graph.ShouldForceRevision() {
		t.Fatal("should force revision after 3 consecutive non-growth actions")
	}
}

func Test_TaskGraph_ConvergenceResetsOnGrowth(t *testing.T) {
	graph := New("test", 3)
	graph.AddSubgoal("step 1")
	current := graph.Current()
	graph.ObserveAction("e1", current.ID)
	graph.ObserveAction("e1", current.ID)
	graph.ObserveAction("e2", current.ID) // grows
	graph.ObserveAction("e2", current.ID)
	if graph.ShouldForceRevision() {
		t.Fatal("should not force revision; growth reset the counter")
	}
}

func Test_TaskGraph_TodoProjection(t *testing.T) {
	graph := New("test", 4)
	graph.AddSubgoal("step 1")
	graph.AddSubgoal("step 2")
	todo := graph.TodoProjection()
	if len(todo) != 2 {
		t.Fatalf("expected 2 todo items, got %d", len(todo))
	}
	if todo[0].Text != "step 1" {
		t.Fatalf("expected 'step 1', got %q", todo[0].Text)
	}
}

func Test_TaskGraph_CompleteSubgoal(t *testing.T) {
	graph := New("test", 4)
	sub := graph.AddSubgoal("step 1")
	if err := graph.CompleteSubgoal(sub.ID); err != nil {
		t.Fatalf("CompleteSubgoal: %v", err)
	}
	todo := graph.TodoProjection()
	if todo[0].Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", todo[0].Status)
	}
}

func Test_TaskGraph_Reset(t *testing.T) {
	graph := New("test", 4)
	graph.AddSubgoal("step 1")
	graph.ObserveAction("e1", "x")
	graph.Reset()
	if !graph.IsEmpty() {
		t.Fatal("expected empty after reset")
	}
	if graph.EvidenceCount() != 0 {
		t.Fatal("expected 0 evidence after reset")
	}
}

func Test_TaskGraph_RenderNonEmpty(t *testing.T) {
	graph := New("test goal", 4)
	graph.AddSubgoal("step 1")
	graph.AddSubgoal("step 2")
	rendered := graph.Render()
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
}

func Test_TaskGraph_EmptyRender(t *testing.T) {
	graph := New("test", 4)
	// Graph always has a goal node now; render should show just the goal line.
	rendered := graph.Render()
	if rendered == "" {
		t.Fatal("expected non-empty render for goal-only graph")
	}
	if !contains(rendered, "Goal: test") {
		t.Fatalf("rendered should contain goal: %s", rendered)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	return strings.Contains(s, substr)
}

func Test_TaskGraph_HasCycle_NoCycle(t *testing.T) {
	graph := New("test", 4)
	graph.AddSubgoal("step 1")
	graph.AddSubgoal("step 2")
	if graph.HasCycle() {
		t.Fatal("expected no cycle in simple DAG")
	}
}

func Test_TaskGraph_AddPremise(t *testing.T) {
	graph := New("test", 4)
	sub := graph.AddSubgoal("step 1")
	premise, err := graph.AddPremise(sub.ID, "the API returns JSON")
	if err != nil {
		t.Fatalf("AddPremise: %v", err)
	}
	if premise.Type != NodePremise {
		t.Fatalf("expected premise type, got %s", premise.Type)
	}
	// Verify edge exists.
	found := false
	for _, edge := range graph.Edges {
		if edge.From == sub.ID && edge.To == premise.ID && edge.Type == EdgeRequires {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected requires edge from subgoal to premise")
	}
}

func Test_TaskGraph_AddEvidence_BLAKE3Identity(t *testing.T) {
	graph := New("test", 4)
	sub := graph.AddSubgoal("step 1")
	premise, _ := graph.AddPremise(sub.ID, "test premise")

	e1, err := graph.AddEvidence("source-1", "content", []string{premise.ID}, EdgeSupports)
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	// Adding same evidence again should return the same key (idempotent).
	e2, err := graph.AddEvidence("source-1", "content", []string{premise.ID}, EdgeSupports)
	if err != nil {
		t.Fatalf("AddEvidence second: %v", err)
	}
	if e1.Key != e2.Key {
		t.Fatalf("expected same BLAKE3 key, got %s vs %s", e1.Key, e2.Key)
	}
	if len(graph.EvidenceItems) != 1 {
		t.Fatalf("expected 1 evidence item, got %d", len(graph.EvidenceItems))
	}
}

func Test_TaskGraph_EvidenceDifferentContentDifferentKey(t *testing.T) {
	graph := New("test", 4)
	sub := graph.AddSubgoal("step")
	premise, _ := graph.AddPremise(sub.ID, "premise")
	e1, _ := graph.AddEvidence("s1", "content-a", []string{premise.ID}, EdgeSupports)
	e2, _ := graph.AddEvidence("s1", "content-b", []string{premise.ID}, EdgeSupports)
	if e1.Key == e2.Key {
		t.Fatal("different content should produce different BLAKE3 keys")
	}
}

func Test_TaskGraph_RejectsInvalidTypedEdges(t *testing.T) {
	graph := New("test", 4)
	sub := graph.AddSubgoal("step")
	premise, _ := graph.AddPremise(sub.ID, "premise")
	evidence, _ := graph.AddEvidence(
		"source", "content", []string{premise.ID}, EdgeSupports,
	)
	if err := graph.AddEdge(Edge{
		From: evidence.Key, To: premise.ID, Type: EdgeSupports,
	}); err == nil {
		t.Fatal("reverse evidence edge was accepted")
	}
	if graph.HasCycle() {
		t.Fatal("invalid edge mutated the DAG")
	}
}

func Test_TaskGraph_AddPremise_NotFound(t *testing.T) {
	graph := New("test", 4)
	_, err := graph.AddPremise("nonexistent", "statement")
	if err == nil {
		t.Fatal("expected error for nonexistent subgoal")
	}
}

func Test_TaskGraph_ReplanSubtreesPreservesUnrelatedState(t *testing.T) {
	graph := New("goal", 4)
	first := graph.AddSubgoal("first")
	second := graph.AddSubgoal("second")
	firstPremise, _ := graph.AddPremise(first.ID, "first premise")
	secondPremise, _ := graph.AddPremise(second.ID, "second premise")
	_, _ = graph.AddEvidence(
		"first-event", "first evidence",
		[]string{firstPremise.ID}, EdgeRefutes,
	)
	secondEvidence, _ := graph.AddEvidence(
		"second-event", "second evidence",
		[]string{secondPremise.ID}, EdgeSupports,
	)
	graph.ObserveAction("first-action", first.ID)
	graph.ObserveAction("second-action", second.ID)
	if err := graph.CompleteSubgoal(second.ID); err != nil {
		t.Fatal(err)
	}

	if err := graph.ReplanSubtrees([]string{first.ID}); err != nil {
		t.Fatal(err)
	}
	projection := graph.TodoProjection()
	if projection[0].ID != first.ID ||
		projection[0].Status != StatusPending ||
		projection[0].Actions != 0 {
		t.Fatalf("replanned subgoal = %+v", projection[0])
	}
	if projection[1].ID != second.ID ||
		projection[1].Status != StatusCompleted ||
		projection[1].Evidence != 1 ||
		projection[1].Actions != 1 {
		t.Fatalf("unrelated subgoal changed = %+v", projection[1])
	}
	if len(graph.EvidenceItems) != 1 ||
		graph.EvidenceItems[0].Key != secondEvidence.Key {
		t.Fatalf("remaining evidence = %+v", graph.EvidenceItems)
	}
	if graph.EvidenceCount() != 2 {
		t.Fatalf("evidence count = %d, want graph plus action evidence", graph.EvidenceCount())
	}
}
