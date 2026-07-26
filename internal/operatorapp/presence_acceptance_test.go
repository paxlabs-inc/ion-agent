package operatorapp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/presence/integrity"
)

func TestProductionPresenceRunsForgettingGoalsHeartbeatAndRestartSchedules(
	t *testing.T,
) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC),
	}
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(), Clock: clock,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	sessionID := createAcceptanceSession(t, ctx, runtime, actor, "presence-session")
	submitAcceptanceTurn(t, ctx, runtime, actor, sessionID, "hello", "presence-hello")

	protected, err := runtime.capabilityRoot.memory.Write(
		ctx, memory.Fact, []byte(`{"text":"load-bearing fact"}`), "presence-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	expiring, err := runtime.capabilityRoot.memory.Write(
		ctx, memory.Event, []byte(`{"text":"old event"}`), "presence-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := runtime.sessions.LoadCognitionState(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var cognition cognitionSnapshot
	if err := json.Unmarshal(raw, &cognition); err != nil {
		t.Fatal(err)
	}
	cognition.Premises.Items = append(cognition.Premises.Items, &premise.Premise{
		ID: uuid.New(), Statement: "load-bearing fact", Status: premise.Cited,
		Source: premise.SourceCortex, Load: 1, CreatedAt: clock.Now(),
		PlanID: cognition.Premises.PlanID, MemoryID: &protected.Head.ID,
	})
	raw, err = json.Marshal(cognition)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.sessions.SaveCognitionState(ctx, sessionID, raw); err != nil {
		t.Fatal(err)
	}

	clock.Advance(100 * 24 * time.Hour)
	forgettingSchedule, err := runtime.presence.RunNow(ctx, "strategic_forgetting")
	if err != nil || forgettingSchedule.Status != "ready" {
		t.Fatalf("forgetting schedule = %+v, %v", forgettingSchedule, err)
	}
	if !containsMemoryID(
		runtime.capabilityRoot.memory.ListByType(memory.Fact), protected.Head.ID,
	) {
		t.Fatal("load-bearing fact was archived")
	}
	if containsMemoryID(
		runtime.capabilityRoot.memory.ListByType(memory.Event), expiring.Head.ID,
	) {
		t.Fatal("expired unprotected event remained live")
	}
	livenessSchedule, err := runtime.presence.RunNow(ctx, "liveness_cognition")
	if err != nil || livenessSchedule.Status != "ready" {
		t.Fatalf("liveness schedule = %+v, %v", livenessSchedule, err)
	}
	goalIDs := runtime.capabilityRoot.memory.ListByType(memory.Goal)
	if len(goalIDs) != 1 {
		t.Fatalf("goal proposals = %v", goalIDs)
	}
	resolvedGoal, err := runtime.capabilityRoot.memory.Resolve(goalIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if resolvedGoal.Head.Actor != actor.String() {
		t.Fatalf("relationship goal owner = %q, want %q",
			resolvedGoal.Head.Actor, actor)
	}
	var proposal struct {
		Status   string `json:"status"`
		Executed bool   `json:"executed"`
	}
	if json.Unmarshal(resolvedGoal.Version.Data, &proposal) != nil ||
		proposal.Status != "proposed" || proposal.Executed {
		t.Fatalf("goal proposal = %s", resolvedGoal.Version.Data)
	}
	integritySchedule, err := runtime.presence.RunNow(ctx, "weekly_integrity")
	if err != nil || integritySchedule.Status != "ready" {
		t.Fatalf("integrity schedule = %+v, %v", integritySchedule, err)
	}
	latest := runtime.presence.LatestIntegrity()
	latestMap, ok := latest.(map[string]any)
	if !ok || latestMap["verified"] != true {
		t.Fatalf("integrity projection = %+v", latest)
	}
	waitForTelegram(t, time.Second, func() bool {
		return runtime.presence.Projection(actor).LastBeat != nil
	})
	before := runtime.presence.Projection(actor)
	if before.HeartbeatInterval != "1m0s" || before.LastArchived != 1 ||
		before.LastProtected == 0 || before.GoalProposals != 1 {
		t.Fatalf("presence projection = %+v", before)
	}
	report, ok := latestMap["report"].(integrity.Report)
	if !ok || !integrity.Verify(report) {
		t.Fatal("weekly integrity report is absent or invalid")
	}
	runtime.presence.mu.Lock()
	interrupted := runtime.presence.state.Schedules["morning_brief"]
	interrupted.Status = "running"
	interrupted.NextDue = clock.Now().Add(24 * time.Hour)
	runtime.presence.state.Schedules["morning_brief"] = interrupted
	if err := runtime.presence.saveLocked(ctx); err != nil {
		runtime.presence.mu.Unlock()
		t.Fatal(err)
	}
	runtime.presence.mu.Unlock()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	after := restarted.presence.Projection(actor)
	if after.LastBeat == nil || after.LastArchived != before.LastArchived ||
		after.LastProtected != before.LastProtected ||
		after.GoalProposals != before.GoalProposals {
		t.Fatalf("presence restart projection = %+v, before %+v", after, before)
	}
	var recovered scheduleState
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, schedule := range restarted.presence.Schedules() {
			if schedule.Name == "Morning brief" {
				recovered = schedule
			}
		}
		if recovered.Status != "running" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	recoveredPending := recovered.Status == "degraded" &&
		recovered.LastError == "interrupted by daemon restart; retry is due" &&
		!recovered.NextDue.After(clock.Now())
	recoveredCompleted := recovered.Status == "ready" &&
		recovered.LastSuccess != nil && recovered.LastError == "" &&
		recovered.NextDue.After(clock.Now())
	if !recoveredPending && !recoveredCompleted {
		t.Fatalf("interrupted schedule recovery = %+v", recovered)
	}
	if _, err := restarted.presence.RunNow(ctx, "liveness_cognition"); err != nil {
		t.Fatal(err)
	}
	if got := restarted.capabilityRoot.memory.ListByType(memory.Goal); len(got) != 1 {
		t.Fatalf("restart duplicated goal proposal: %v", got)
	}
}

func TestForgettingRebuildsProtectionsAcrossAllSessionsAndRemovesRefuted(
	t *testing.T,
) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC),
	}
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(), Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	protected, err := runtime.capabilityRoot.memory.Write(
		ctx, memory.Fact, []byte(`{"text":"old protected fact"}`), "presence-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	oldest, err := runtime.sessions.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cognitionSnapshot{}
	snapshot.Premises.Items = []*premise.Premise{{
		ID: uuid.New(), Statement: "old protected fact", Status: premise.Cited,
		Source: premise.SourceCortex, Load: 1, CreatedAt: clock.Now(),
		MemoryID: &protected.Head.ID,
	}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.sessions.SaveCognitionState(ctx, oldest.ID, raw); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 100; index++ {
		clock.Advance(time.Millisecond)
		if _, err := runtime.sessions.CreateSession(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(100 * 24 * time.Hour)
	if _, err := runtime.presence.RunNow(ctx, "strategic_forgetting"); err != nil {
		t.Fatal(err)
	}
	if !containsMemoryID(
		runtime.capabilityRoot.memory.ListByType(memory.Fact), protected.Head.ID,
	) {
		t.Fatal("premise in session 101 was omitted from protection rebuild")
	}
	snapshot.Premises.Items[0].Status = premise.Refuted
	raw, _ = json.Marshal(snapshot)
	if err := runtime.sessions.SaveCognitionState(ctx, oldest.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.presence.RunNow(ctx, "strategic_forgetting"); err != nil {
		t.Fatal(err)
	}
	if containsMemoryID(
		runtime.capabilityRoot.memory.ListByType(memory.Fact), protected.Head.ID,
	) {
		t.Fatal("refuted premise retained obsolete forgetting protection")
	}
}

func containsMemoryID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
