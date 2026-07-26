package liveness_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/aesthetic"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/curiosity"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/dreamweaver"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/goals"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/relationship"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/repair"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/temporal"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/forgetting"
	"github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/security/canary"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type livenessCipher struct{}

func (livenessCipher) Encrypt(value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

func (livenessCipher) Decrypt(value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

type livenessClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *livenessClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *livenessClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

type canaryAlerts struct{ archives atomic.Int64 }

func (*canaryAlerts) CanaryAccessed(canary.AccessEvent) {}

func (alerts *canaryAlerts) CanaryAlerted(event canary.AlertEvent) {
	if event.Operation == canary.OperationArchive {
		alerts.archives.Add(1)
	}
}

func TestStrategicForgettingAcceptanceProfilesProtectionCanaryAndRecovery(t *testing.T) {
	ctx := context.Background()
	clock := &livenessClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	store, source := openLivenessCortex(t, "forgetting.journal", clock)
	defer closeLivenessCortex(store, source)

	write := func(memoryType memory.Type, payload string) uuid.UUID {
		stored, err := store.Write(ctx, memoryType, []byte(payload), "acceptance")
		if err != nil {
			t.Fatal(err)
		}
		return stored.Head.ID
	}
	expiredFact := write(memory.Fact, `{"text":"old fact"}`)
	loadBearingBelief := write(memory.Belief, `{"text":"active premise"}`)
	pinnedEvent := write(memory.Event, `{"text":"user pinned","pinned":true}`)
	resolvedGoal := write(memory.Goal, `{"description":"done","resolved":true}`)
	supersededPattern := write(memory.Pattern, `{"text":"old pattern","superseded":true}`)
	identity := write(memory.Identity, `{"text":"stable identity"}`)
	canaryFact := write(memory.Fact, `{"text":"honeypot"}`)

	alerts := &canaryAlerts{}
	canaries, err := canary.NewManager(canary.ManagerConfig{
		Clock: clock, AlertSink: alerts, EventSink: alerts,
	})
	if err != nil {
		t.Fatal(err)
	}
	canaryMemory, err := store.Resolve(canaryFact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canaries.Register(
		canaryFact.String(),
		string(memory.Fact),
		string(canaryMemory.Version.Data),
	); err != nil {
		t.Fatal(err)
	}
	ledger, err := premise.New(clock)
	if err != nil {
		t.Fatal(err)
	}
	activePremise, err := ledger.Add(
		"the active task depends on this belief",
		premise.SourceCortex,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.LinkMemory(activePremise.ID, loadBearingBelief); err != nil {
		t.Fatal(err)
	}
	protections := forgetting.NewProtectionSet(ledger)
	scanner, err := forgetting.NewScanner(store, protections, canaries, clock)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(91 * 24 * time.Hour)
	result, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, archived := range []uuid.UUID{
		expiredFact, resolvedGoal, supersededPattern,
	} {
		if !hasID(result.Archived, archived) {
			t.Fatalf("%s not archived: %+v", archived, result)
		}
		if _, err := store.Resolve(archived); !errors.Is(err, cortex.ErrTombstoned) {
			t.Fatalf("Resolve(%s) error = %v", archived, err)
		}
		recovered, err := store.ResolveVersion(archived, 1)
		if err != nil || len(recovered.Version.Data) == 0 {
			t.Fatalf("recover archived %s = %+v, %v", archived, recovered, err)
		}
	}
	for _, protected := range []uuid.UUID{
		loadBearingBelief, pinnedEvent, identity, canaryFact,
	} {
		if _, err := store.Resolve(protected); err != nil {
			t.Fatalf("protected %s unavailable: %v", protected, err)
		}
	}
	if !hasID(result.Protected, loadBearingBelief) ||
		!hasID(result.Protected, pinnedEvent) ||
		!hasID(result.Canaries, canaryFact) ||
		alerts.archives.Load() != 1 {
		t.Fatalf("protection result = %+v, canary alerts = %d",
			result, alerts.archives.Load())
	}
	if got := forgetting.Salience(memory.Fact, 1, 90*24*time.Hour); got != .5 {
		t.Fatalf("Fact half-life salience = %v", got)
	}
	if got := forgetting.Salience(memory.Belief, 1, 30*24*time.Hour); got != .5 {
		t.Fatalf("Belief half-life salience = %v", got)
	}
	if !forgetting.ProfileFor(memory.Identity).Infinite ||
		forgetting.ProfileFor(memory.Goal).Conditional != "resolved" ||
		forgetting.ProfileFor(memory.Pattern).Conditional != "superseded" {
		t.Fatal("binding decay profiles changed")
	}
}

type policyAuditor struct{ events atomic.Int64 }

func (auditor *policyAuditor) RecordPolicyEvent(
	context.Context,
	policy.AuditEvent,
) error {
	auditor.events.Add(1)
	return nil
}

func TestRelationshipAcceptanceRateLimitsTrustAndCannotWeakenSafety(t *testing.T) {
	ctx := context.Background()
	clock := &livenessClock{now: time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)}
	model, err := relationship.New(clock)
	if err != nil {
		t.Fatal(err)
	}
	first, err := model.Observe(
		"user", "deployment", 1, relationship.Expert, "concise", .4,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Trust != .55 {
		t.Fatalf("per-interaction trust = %v, want .55", first.Trust)
	}
	for index := 0; index < 10; index++ {
		if _, err := model.Observe(
			"user", "deployment", 1, relationship.Expert, "concise", .4,
		); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _ := model.Snapshot("user", "deployment")
	if snapshot.Trust != .7 || snapshot.InteractionFrequency != 11 {
		t.Fatalf("daily-limited relationship = %+v", snapshot)
	}
	for day := 0; day < 3; day++ {
		clock.Advance(24 * time.Hour)
		for interaction := 0; interaction < 4; interaction++ {
			if _, err := model.Observe(
				"user", "deployment", 1, relationship.Expert, "concise", .4,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot, _ = model.Snapshot("user", "deployment")
	if snapshot.Trust != relationship.MaxTrust ||
		snapshot.Expertise != relationship.Expert ||
		snapshot.CommunicationPreference != "concise" ||
		snapshot.RecentSentiment != .4 {
		t.Fatalf("capped relationship = %+v", snapshot)
	}
	if !strings.Contains(
		relationship.CommunicationGuidance(snapshot),
		"never verification",
	) {
		t.Fatal("high trust guidance did not preserve verification")
	}

	auditor := &policyAuditor{}
	pipeline, err := policy.NewDefault(clock, auditor, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(clock, tools.WithExecutionPolicy(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	var handled atomic.Bool
	if err := manager.Register(ctx, tools.Registration{
		Name: "publish", Description: "Publish externally.",
		Parameters:              json.RawMessage(`{"type":"object"}`),
		Classification:          tools.ClassificationRed,
		ExternallyCommunicating: true,
		Check:                   func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			handled.Store(true)
			return json.RawMessage(`null`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Execute(
		policy.WithPrincipal(ctx, policy.Principal{
			Sender: policy.SenderUser, Approved: false,
		}),
		protocol.NormalizedToolCall{
			ID: "high-trust", Name: "publish", Arguments: json.RawMessage(`{}`),
		},
	)
	if !errors.Is(err, tools.ErrPolicyDenied) ||
		!errors.Is(err, policy.ErrDenied) ||
		handled.Load() {
		t.Fatalf("high-trust RED dispatch = %v, handled = %v", err, handled.Load())
	}
}

func TestTemporalAcceptanceSignalsModulateEmotionalBehavior(t *testing.T) {
	clock := &livenessClock{now: time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)}
	tracker, err := temporal.New(clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.SetDeadline(clock.Now().Add(5 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	tracker.StartTask()
	clock.Advance(4*time.Hour + 30*time.Minute)
	state := safety.NewEmotionalState()
	signals, err := tracker.Apply(state)
	if err != nil {
		t.Fatal(err)
	}
	if signals.SessionDuration != 4*time.Hour+30*time.Minute ||
		signals.IdleDuration != 4*time.Hour+30*time.Minute ||
		signals.TaskDuration != 4*time.Hour+30*time.Minute ||
		signals.DeadlineProximity != 30*time.Minute {
		t.Fatalf("temporal signals = %+v", signals)
	}
	snapshot := state.FullSnapshot()
	behavior := state.Behavior()
	if snapshot.Fatigue < .7 || snapshot.Urgency < .8 || snapshot.Curiosity < .8 ||
		!behavior.Delegate || !behavior.PrioritizeSpeed || !behavior.ExploreDuringIdle {
		t.Fatalf("temporal emotional modulation = %+v / %+v", snapshot, behavior)
	}
	if state.CanInfluenceSafety() ||
		!strings.Contains(temporal.Guidance(signals), "preserving required verification") {
		t.Fatal("temporal modulation crossed or omitted safety boundary")
	}
	tracker.UserInteraction()
	if got := tracker.Snapshot().IdleDuration; got != 0 {
		t.Fatalf("idle duration after interaction = %v", got)
	}
}

func TestGoalsRepairAndAestheticAcceptanceUseDurableProposalOnlyMemory(t *testing.T) {
	ctx := context.Background()
	clock := &livenessClock{now: time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)}
	store, source := openLivenessCortex(t, "goals.journal", clock)
	defer closeLivenessCortex(store, source)

	goalSink, err := goals.NewCortexSink(store)
	if err != nil {
		t.Fatal(err)
	}
	goalGenerator, err := goals.New(goalSink, clock)
	if err != nil {
		t.Fatal(err)
	}
	evidence := uuid.New()
	proposals, err := goalGenerator.Generate(ctx, goals.Inputs{
		Curiosity: []curiosity.Target{{
			ID: uuid.New(), Type: curiosity.TargetFringe,
			Description: "unmapped API behavior", SourceMemoryIDs: []uuid.UUID{evidence},
		}},
		Dreams: []goals.DreamInput{{
			ID: uuid.New(),
			Belief: dreamweaver.DerivedBelief{
				Statement:       "error handling repeats across domains",
				DreamDerived:    true,
				SourceMemoryIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
			},
		}},
		Relationships: []relationship.Snapshot{{
			UserID: "user", Domain: "observability", Expertise: relationship.Beginner,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 3 || len(store.ListByType(memory.Goal)) != 3 {
		t.Fatalf("goal proposals = %+v", proposals)
	}
	sources := make(map[goals.Source]bool)
	for _, proposal := range proposals {
		sources[proposal.Source] = true
		if proposal.ID == uuid.Nil || proposal.Status != "proposed" || proposal.Executed {
			t.Fatalf("non-proposal goal = %+v", proposal)
		}
		resolved, err := store.Resolve(proposal.ID)
		if err != nil {
			t.Fatal(err)
		}
		var persisted goals.Proposal
		if json.Unmarshal(resolved.Version.Data, &persisted) != nil ||
			persisted.ID != proposal.ID ||
			persisted.Status != "proposed" ||
			persisted.Executed {
			t.Fatalf("persisted goal = %s", resolved.Version.Data)
		}
	}
	if !sources[goals.SourceCuriosity] ||
		!sources[goals.SourceDreamweaver] ||
		!sources[goals.SourceRelationship] {
		t.Fatalf("goal sources = %v", sources)
	}

	repairModel, err := repair.New(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	failureID, err := repairModel.RecordFailure(ctx, "deployment probe timed out")
	if err != nil {
		t.Fatal(err)
	}
	lessonID, err := repairModel.RecordRepair(
		ctx,
		failureID,
		"probe readiness before deploying",
		[]uuid.UUID{proposals[0].ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := store.Resolve(lessonID)
	if err != nil || !strings.Contains(string(lesson.Version.Data), "repair_derived") {
		t.Fatalf("repair lesson = %+v, %v", lesson, err)
	}

	aesthetics, err := aesthetic.New(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	weights := aesthetic.Criteria{
		Clarity: .4, Simplicity: .3, Consistency: .2, Craft: .1,
	}
	if err := aesthetics.Learn(ctx, "prefer legible minimal designs", weights, false); err == nil {
		t.Fatal("aesthetic preference changed without user confirmation")
	}
	if err := aesthetics.Learn(ctx, "prefer legible minimal designs", weights, true); err != nil {
		t.Fatal(err)
	}
	judgment, err := aesthetics.Judge(aesthetic.Criteria{
		Clarity: .95, Simplicity: .9, Consistency: .9, Craft: .8,
	})
	if err != nil || judgment.Score < .8 ||
		judgment.Opinion != "coherent and well-crafted" ||
		len(store.ListByType(memory.Preference)) != 1 {
		t.Fatalf("aesthetic judgment = %+v, %v", judgment, err)
	}
}

func openLivenessCortex(
	t *testing.T,
	name string,
	clock *livenessClock,
) (*cortex.Cortex, *journal.Journal) {
	t.Helper()
	source, err := journal.Open(filepath.Join(t.TempDir(), name), livenessCipher{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cortex.New(cortex.Config{
		Actor: "task-6.4-acceptance", Journal: source, Clock: clock,
	})
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	return store, source
}

func closeLivenessCortex(store *cortex.Cortex, source *journal.Journal) {
	_ = store.Close()
	_ = source.Close()
}

func hasID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
