package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/coordination"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func Test_Registry_SpawnAndComplete(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	agent, err := r.Spawn("", "session-1", 0)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if agent.State != StateRunning {
		t.Fatalf("expected running, got %s", agent.State)
	}
	if r.ActiveCount() != 1 {
		t.Fatalf("expected 1 active, got %d", r.ActiveCount())
	}

	r.Complete(agent.ID, "session-1")
	if r.ActiveCount() != 0 {
		t.Fatalf("expected 0 active after complete, got %d", r.ActiveCount())
	}
}

func Test_Registry_DepthLimit(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	_, err := r.Spawn("", "s1", MaxDepth+1)
	if err == nil {
		t.Fatal("expected error for exceeding max depth")
	}
}

func Test_Registry_GlobalLaneLimit(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	for i := 0; i < GlobalLaneMax; i++ {
		sessionID := "session-" + string(rune('a'+i))
		_, err := r.Spawn("", sessionID, 0)
		if err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
	}
	_, err := r.Spawn("", "extra-session", 0)
	if err == nil {
		t.Fatal("expected error for global lane full")
	}
}

func Test_Registry_SessionLaneLimit(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	for i := 0; i < SessionLaneMax; i++ {
		_, err := r.Spawn("", "session-limited", 0)
		if err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
	}
	_, err := r.Spawn("", "session-limited", 0)
	if err == nil {
		t.Fatal("expected error for session lane full")
	}
}

func Test_Registry_ParentLaneLimit(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	parent, _ := r.Spawn("", "parent-session", 0)
	for i := 0; i < ParentLaneMax; i++ {
		childSession := "child-session-" + string(rune('a'+i))
		_, err := r.Spawn(parent.ID, childSession, 1)
		if err != nil {
			t.Fatalf("Spawn child %d: %v", i, err)
		}
	}
	_, err := r.Spawn(parent.ID, "extra-child-session", 1)
	if err == nil {
		t.Fatal("expected error for parent lane full")
	}
}

func Test_Registry_ScanOrphans(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	agent, _ := r.Spawn("", "s1", 0)

	// Advance time past orphan threshold.
	clock.now = clock.now.Add(OrphanThreshold + time.Minute)

	orphans := r.ScanOrphans()
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0] != agent.ID {
		t.Fatal("wrong orphan")
	}
}

func Test_Registry_Heartbeat(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	agent, _ := r.Spawn("", "s1", 0)
	clock.now = clock.now.Add(OrphanThreshold - time.Minute)
	parent, exists := r.ParentEndpoint(agent.ID)
	if !exists {
		t.Fatal("missing authenticated parent endpoint")
	}
	message, err := parent.Sign(coordination.VerbQuery, json.RawMessage(`{"status":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.DeliverToSubAgent(agent.ID, message); err != nil {
		t.Fatal(err)
	}

	// Should not be orphaned yet.
	orphans := r.ScanOrphans()
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans after heartbeat, got %d", len(orphans))
	}
}

func Test_Registry_RejectsTamperedReplayedAndCrossSpawnVerbs(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)
	first, _ := r.Spawn("", "s1", 0)
	second, _ := r.Spawn("", "s2", 0)
	parent, _ := r.ParentEndpoint(first.ID)
	message, err := parent.Sign(coordination.VerbYield, json.RawMessage(`{"reason":"wait"}`))
	if err != nil {
		t.Fatal(err)
	}
	tampered := *message
	tampered.Verb = coordination.VerbAbort
	if err := r.DeliverToSubAgent(first.ID, &tampered); err == nil {
		t.Fatal("tampered verb reached registry lifecycle")
	}
	if err := r.DeliverToSubAgent(second.ID, message); err == nil {
		t.Fatal("cross-spawn verb reached registry lifecycle")
	}
	if err := r.DeliverToSubAgent(first.ID, message); err != nil {
		t.Fatal(err)
	}
	if first.State != StateYielded || first.LastVerb != string(coordination.VerbYield) {
		t.Fatalf("first state = %+v", first)
	}
	if err := r.DeliverToSubAgent(first.ID, message); err == nil {
		t.Fatal("replayed verb reached registry lifecycle")
	}
	if first.State != StateYielded {
		t.Fatalf("replay changed state to %s", first.State)
	}
}

func Test_Registry_AuthenticatedAbortClosesSpawnChannel(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)
	agent, _ := r.Spawn("", "s1", 0)
	parent, _ := r.ParentEndpoint(agent.ID)
	message, err := parent.Sign(coordination.VerbAbort, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.DeliverToSubAgent(agent.ID, message); err != nil {
		t.Fatal(err)
	}
	if agent.State != StateAborted || r.ActiveCount() != 0 {
		t.Fatalf("agent = %+v, active = %d", agent, r.ActiveCount())
	}
	if _, exists := r.ParentEndpoint(agent.ID); exists {
		t.Fatal("aborted spawn retained authentication channel")
	}
}

func Test_Registry_Abort(t *testing.T) {
	clock := &testClock{baseTime}
	r := NewRegistry(clock)

	agent, _ := r.Spawn("", "s1", 0)
	r.Abort(agent.ID, "s1")
	if r.ActiveCount() != 0 {
		t.Fatalf("expected 0 active after abort, got %d", r.ActiveCount())
	}
}

func TestRegistryAbortScopedIsAtomicAndSessionBound(t *testing.T) {
	t.Parallel()
	clock := &testClock{baseTime}
	registry := NewRegistry(clock)
	first, err := registry.Spawn("", "session-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Spawn("", "session-b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AbortScoped(
		"missing", "session-a", StateRunning,
	); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("missing abort error = %v", err)
	}
	if _, err := registry.AbortScoped(
		first.ID, "session-b", StateRunning,
	); !errors.Is(err, ErrAgentSessionMismatch) {
		t.Fatalf("cross-session abort error = %v", err)
	}
	if _, err := registry.AbortScoped(
		first.ID, "session-a", StateYielded,
	); !errors.Is(err, ErrAgentStateConflict) {
		t.Fatalf("stale-state abort error = %v", err)
	}
	if registry.ActiveCount() != 2 || first.State != StateRunning {
		t.Fatalf("rejected abort changed state: active=%d first=%s", registry.ActiveCount(), first.State)
	}
	aborted, err := registry.AbortScoped(
		first.ID, "session-a", StateRunning,
	)
	if err != nil {
		t.Fatal(err)
	}
	if aborted.State != StateAborted ||
		aborted.LastVerb != string(coordination.VerbAbort) ||
		registry.ActiveCount() != 1 ||
		registry.SessionCount("session-a") != 0 {
		t.Fatalf("aborted = %+v, active = %d", aborted, registry.ActiveCount())
	}
	if _, err := registry.AbortScoped(
		first.ID, "session-a", StateRunning,
	); !errors.Is(err, ErrAgentStateConflict) {
		t.Fatalf("terminal abort error = %v", err)
	}
	registry.Complete(second.ID, "session-b")
	if _, err := registry.AbortScoped(
		second.ID, "session-b", StateRunning,
	); !errors.Is(err, ErrAgentStateConflict) {
		t.Fatalf("completed abort error = %v", err)
	}
}

func Test_ReducedSelfModel_Fields(t *testing.T) {
	model := ReducedSelfModel{
		ID:           "test",
		Capabilities: []string{"read", "write"},
		Limitations:  []string{"no-vault"},
		Version:      1,
	}
	if model.ID != "test" {
		t.Fatal("ID mismatch")
	}
	if len(model.Capabilities) != 2 {
		t.Fatal("capabilities mismatch")
	}
}

func TestRegistrySpawnWorkerInheritsReducedModelAndPushesCompletion(t *testing.T) {
	clock := &testClock{baseTime}
	registry := NewRegistry(clock)
	surface, err := NewToolSurface([]string{"read", "search"})
	if err != nil {
		t.Fatal(err)
	}
	model := ReducedSelfModel{
		ID: "parent-model", Capabilities: []string{"analysis"},
		Limitations: []string{"no-vault"}, Version: 3,
	}
	agent, err := registry.SpawnWorker(
		context.Background(),
		"parent",
		"session",
		1,
		model,
		surface,
		func(_ context.Context, inherited WorkerContext) (json.RawMessage, error) {
			if inherited.Model.ID != "parent-model" ||
				len(inherited.Model.Capabilities) != 1 ||
				len(inherited.Tools) != 2 {
				return nil, errors.New("reduced inheritance mismatch")
			}
			inherited.Model.Capabilities[0] = "mutated"
			inherited.Tools[0] = "memory_read"
			return json.RawMessage(`{"artifact":"preserved"}`), nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case completion := <-registry.Completions():
		if completion.AgentID != agent.ID ||
			completion.State != StateCompleted ||
			string(completion.Artifact) != `{"artifact":"preserved"}` ||
			completion.Error != "" {
			t.Fatalf("completion = %+v", completion)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not push completion")
	}
	if registry.ActiveCount() != 0 {
		t.Fatalf("worker lane was not released: %d", registry.ActiveCount())
	}
	if agent.Model.Capabilities[0] != "analysis" ||
		agent.Tools()[0] == "memory_read" {
		t.Fatalf("worker mutated inherited authority: %+v %v", agent.Model, agent.Tools())
	}
	artifact, exists := registry.Artifact(agent.ID)
	if !exists || string(artifact) != `{"artifact":"preserved"}` {
		t.Fatalf("preserved artifact = %s, %v", artifact, exists)
	}
}

func TestRegistrySnapshotIsSessionScopedAndExcludesWorkerContent(t *testing.T) {
	clock := &testClock{baseTime}
	registry := NewRegistry(clock)
	first, err := registry.SpawnReduced(
		"parent-a", "session-a", 1,
		ReducedSelfModel{ID: "model-a"},
		ToolSurface{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAssignment(first.ID, "Inspect the bounded repository"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SpawnReduced(
		"parent-b", "session-b", 1,
		ReducedSelfModel{ID: "model-b"},
		ToolSurface{},
	); err != nil {
		t.Fatal(err)
	}
	found := registry.Snapshot("session-a")
	if len(found) != 1 || found[0].ID != first.ID ||
		found[0].Assignment != "Inspect the bounded repository" ||
		found[0].ParentID != "parent-a" {
		t.Fatalf("session snapshot = %+v", found)
	}
	encoded, err := json.Marshal(found)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"model-a", "session-a", "workerError"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("operator snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRegistryBackgroundOrphanRecoveryAuthenticatesAbortAndPreservesArtifact(t *testing.T) {
	clock := &testClock{baseTime}
	registry := NewRegistry(clock)
	registry.scanInterval = 5 * time.Millisecond
	registry.abortTimeout = 100 * time.Millisecond
	workerStarted := make(chan struct{})
	agent, err := registry.SpawnWorker(
		context.Background(),
		"",
		"session",
		0,
		ReducedSelfModel{},
		ToolSurface{},
		func(ctx context.Context, _ WorkerContext) (json.RawMessage, error) {
			close(workerStarted)
			<-ctx.Done()
			return json.RawMessage(`{"partial":"saved"}`), ctx.Err()
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	<-workerStarted
	clock.now = clock.now.Add(OrphanThreshold + time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := registry.StartOrphanRecovery(ctx)
	select {
	case completion := <-registry.Completions():
		if completion.AgentID != agent.ID ||
			completion.State != StateOrphaned ||
			string(completion.Artifact) != `{"partial":"saved"}` {
			t.Fatalf("orphan completion = %+v", completion)
		}
	case <-time.After(time.Second):
		t.Fatal("background orphan recovery did not complete")
	}
	cancel()
	<-stopped
	agent.mu.Lock()
	state, lastVerb := agent.State, agent.LastVerb
	agent.mu.Unlock()
	if state != StateOrphaned || lastVerb != string(coordination.VerbAbort) ||
		registry.ActiveCount() != 0 {
		t.Fatalf(
			"orphan state = %s, verb = %s, active = %d",
			state,
			lastVerb,
			registry.ActiveCount(),
		)
	}
}
