package operatorapp

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/decision"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/relationship"
	"github.com/paxlabs-inc/ion-agent/internal/presence/automatrix"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestProductionLivenessPolicyAestheticRepairRestartAndIsolation(t *testing.T) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC),
	}
	directory := t.TempDir()
	initializeRuntimeVault(t, ctx, directory)
	config := RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(), Clock: clock,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	otherActor := uuid.New()
	sessionID := createAcceptanceSession(t, ctx, runtime, actor, "causal-session")
	content := "I prefer simplicity and minimal operational burden. " +
		"Investigate the production failure. deadline: 2026-07-21T08:20:00Z"
	if err := runtime.capabilityRoot.living.Submitted(
		ctx, actor, sessionID, content,
	); err != nil {
		t.Fatal(err)
	}
	emotional, _, err := runtime.capabilityRoot.living.Dependencies(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	emotional.UpdateAll(safety.EmotionalSnapshot{
		Frustration: .9, Confidence: .3, Urgency: .9,
		Satisfaction: .8, Curiosity: .7, Fatigue: .8,
		UpdatedAt: clock.Now(),
	})
	decisionContext := controlplane.WithApprovalScope(
		ctx,
		controlplane.ApprovalScope{ActorID: actor, SessionID: &sessionID},
	)
	policyDecision, err := runtime.capabilityRoot.living.LivenessDecisionPolicy(
		decisionContext,
		decision.Context{UserContent: content, UnsupportedPremises: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if policyDecision.SameStrategyRetries > 1 ||
		policyDecision.OptionalWorkBudget != 0 ||
		policyDecision.Parallelism != 2 ||
		policyDecision.VerificationDepth < 4 ||
		!policyDecision.RequiredVerification {
		t.Fatalf("enforced policy = %+v", policyDecision)
	}
	if !hasDecisionCause(policyDecision, "aesthetic_selection") ||
		!hasDecisionCause(policyDecision, "deadline_scope") {
		t.Fatalf("causal provenance = %+v", policyDecision.Causes)
	}
	if err := runtime.capabilityRoot.living.Recovered(
		ctx, actor, sessionID, content, "provider_timeout",
		agent.Response{Content: "recovered", ProviderCalls: 2},
	); err != nil {
		t.Fatal(err)
	}
	briefSchedule, err := runtime.presence.RunNow(ctx, "morning_brief")
	if err != nil || briefSchedule.Status != "ready" {
		t.Fatalf("morning brief schedule = %+v, %v", briefSchedule, err)
	}
	brief := runtime.presence.Projection(actor).MorningBrief
	if brief == nil || len(brief.Items) == 0 ||
		brief.Sources[0] != "actor-scoped control-plane events" {
		t.Fatalf("typed morning brief = %+v", brief)
	}
	projection, err := runtime.capabilityRoot.living.Projection(
		ctx, controlplane.Scope{ActorID: actor, SessionID: &sessionID},
	)
	if err != nil || projection.Decision == nil || projection.Aesthetic == nil ||
		projection.Repair == nil || projection.Repair.EvidenceCount != 1 {
		t.Fatalf("living projection = %+v, %v", projection, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restored, err := restarted.capabilityRoot.living.Projection(
		ctx, controlplane.Scope{ActorID: actor, SessionID: &sessionID},
	)
	if err != nil || restored.Decision == nil || restored.Aesthetic == nil ||
		restored.Repair == nil {
		t.Fatalf("restart living projection = %+v, %v", restored, err)
	}
	isolated, err := restarted.capabilityRoot.living.Projection(
		ctx, controlplane.Scope{ActorID: otherActor},
	)
	if err != nil {
		t.Fatal(err)
	}
	if isolated.Aesthetic != nil || isolated.Repair != nil || isolated.Decision != nil {
		t.Fatalf("cross-actor living state leaked: %+v", isolated)
	}
}

func TestAutomatrixRequiresExactApprovalExecutesOnceAndReturnsContinuity(t *testing.T) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC),
	}
	directory := t.TempDir()
	initializeRuntimeVault(t, ctx, directory)
	config := RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(), Clock: clock,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	otherActor := uuid.New()
	var calls atomic.Int64
	if err := runtime.capabilityRoot.manager.Register(ctx, tools.Registration{
		Name: "idle_acceptance", Description: "Read local acceptance state.",
		Parameters:     json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			calls.Add(1)
			return json.RawMessage(`{"finding":"verified"}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.presence.Submitted(
		ctx, actor, uuid.New(),
		"We should probably inspect the bounded local acceptance state.",
	); err != nil {
		t.Fatal(err)
	}
	queued := runtime.presence.Automatrix(actor)
	if len(queued) != 1 || queued[0].ApprovedAt != nil {
		t.Fatalf("proposal queue = %+v", queued)
	}
	runtime.presence.runAutomatrix(ctx)
	if calls.Load() != 0 || len(runtime.presence.IdleResults(actor)) != 0 {
		t.Fatal("unapproved Automatrix proposal executed")
	}
	actions := []automatrix.Action{{ToolCall: &protocol.NormalizedToolCall{
		ID: "idle-once", Name: "idle_acceptance", Arguments: json.RawMessage(`{}`),
	}}}
	payload, _ := json.Marshal(map[string]any{
		"item_id": queued[0].ID, "actions": actions,
	})
	defaultDenied := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation:      controlplane.OperationAutomatrixApprove,
		Scope:          controlplane.Scope{ActorID: actor},
		IdempotencyKey: "approve-default-denied", Payload: payload,
	})
	if defaultDenied.Error == nil {
		t.Fatal("suggest-only default allowed background execution approval")
	}
	if _, err := runtime.capabilityRoot.work.UpdateAutonomy(ctx, actor, workcontrol.AutonomySettings{
		Mode: workcontrol.AutonomyApproved, MaxToolCalls: 10, MaxTokens: 64000,
		MaxElapsedSecond: 60, MaxErrors: 2, CooldownSecond: 300,
	}); err != nil {
		t.Fatal(err)
	}
	approved := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation:      controlplane.OperationAutomatrixApprove,
		Scope:          controlplane.Scope{ActorID: actor},
		IdempotencyKey: "approve-idle-once", Payload: payload,
	})
	if approved.Error != nil {
		t.Fatalf("approve response = %+v", approved)
	}
	runtime.presence.runAutomatrix(ctx)
	runtime.presence.runAutomatrix(ctx)
	if calls.Load() != 1 {
		t.Fatalf("approved work executed %d times", calls.Load())
	}
	if err := runtime.presence.Submitted(
		ctx, actor, uuid.New(),
		"We should probably inspect the second bounded local acceptance state.",
	); err != nil {
		t.Fatal(err)
	}
	secondQueued := runtime.presence.Automatrix(actor)
	if len(secondQueued) != 1 {
		t.Fatalf("second proposal queue = %+v", secondQueued)
	}
	secondActions := []automatrix.Action{{ToolCall: &protocol.NormalizedToolCall{
		ID: "idle-after-cooldown", Name: "idle_acceptance", Arguments: json.RawMessage(`{}`),
	}}}
	if _, err := runtime.presence.ApproveAutomatrix(
		ctx, actor, secondQueued[0].ID, secondActions,
	); err != nil {
		t.Fatal(err)
	}
	runtime.presence.runAutomatrix(ctx)
	if calls.Load() != 1 || len(runtime.presence.Automatrix(actor)) != 1 {
		t.Fatalf(
			"cooldown did not hold queued work: calls=%d queue=%+v",
			calls.Load(), runtime.presence.Automatrix(actor),
		)
	}
	projection := runtime.presence.Projection(actor)
	if len(projection.SinceAway) != 1 ||
		projection.SinceAway[0].EvidenceID != queued[0].ID.String() {
		t.Fatalf("continuity projection = %+v", projection.SinceAway)
	}
	if len(runtime.presence.Automatrix(otherActor)) != 0 ||
		len(runtime.presence.IdleResults(otherActor)) != 0 {
		t.Fatal("Automatrix state leaked across actors")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := restarted.presence.Projection(actor).SinceAway; len(got) != 1 {
		t.Fatalf("restart continuity = %+v", got)
	}
}

func TestRelationshipProfileControlsRestartIsolationAndSoulV2Proposal(t *testing.T) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC),
	}
	directory := t.TempDir()
	initializeRuntimeVault(t, ctx, directory)
	config := RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(), Clock: clock,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor, otherActor := uuid.New(), uuid.New()
	sessionID := createAcceptanceSession(
		t, ctx, runtime, actor, "relationship-controls",
	)
	if err := runtime.capabilityRoot.living.Submitted(
		ctx, actor, sessionID, "Help with software architecture.",
	); err != nil {
		t.Fatal(err)
	}
	scope := controlplane.Scope{ActorID: actor, SessionID: &sessionID}
	command := func(key string, payload string) controlplane.Response {
		t.Helper()
		response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
			ProtocolVersion: controlplane.ProtocolVersion,
			RequestID:       uuid.New(), Kind: controlplane.KindCommand,
			Operation: controlplane.OperationRelationshipUpdate,
			Scope:     scope, IdempotencyKey: key,
			Payload: json.RawMessage(payload),
		})
		if response.Error != nil {
			t.Fatalf("relationship command = %+v", response)
		}
		return response
	}
	corrected := command(
		"relationship-correct",
		`{"action":"correct","domain":"software","patch":{`+
			`"response_length":"brief","directness":"direct",`+
			`"conclusion_first":true,"domain_expertise":"expert",`+
			`"preferred_tools":["rg","go test"],"risk_tolerance":"low",`+
			`"proactive_suggestions":true,"notification_cadence":"milestones",`+
			`"dislikes":["hidden work"],`+
			`"constraints":["preserve dirty trees"],`+
			`"project_principles":["prove production behavior"]}}`,
	)
	var snapshot relationship.Snapshot
	if err := json.Unmarshal(corrected.Result, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Profile.ResponseLength != "brief" ||
		snapshot.Profile.Directness != "direct" ||
		snapshot.Expertise != relationship.Expert {
		t.Fatalf("corrected profile = %+v", snapshot)
	}
	command(
		"relationship-pin",
		`{"action":"pin","domain":"software",`+
			`"fields":["response_length","project_principles"]}`,
	)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	projection, err := restarted.capabilityRoot.LivingState(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var restoredProfile *relationship.Snapshot
	for index := range projection.Relationships {
		if projection.Relationships[index].Domain == "software" {
			restoredProfile = &projection.Relationships[index]
		}
	}
	if restoredProfile == nil ||
		len(restoredProfile.Profile.PinnedFields) != 2 {
		t.Fatalf("restart profile = %+v", projection.Relationships)
	}
	isolated, err := restarted.capabilityRoot.LivingState(
		ctx, controlplane.Scope{ActorID: otherActor},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(isolated.Relationships) != 0 {
		t.Fatalf("cross-actor profile leaked = %+v", isolated.Relationships)
	}
	response := restarted.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationRelationshipUpdate,
		Scope:     scope, IdempotencyKey: "relationship-soul-v2",
		Payload: json.RawMessage(
			`{"action":"propose_soul_v2","domain":"software"}`,
		),
	})
	if response.Error != nil {
		t.Fatalf("SOUL v2 proposal = %+v", response)
	}
	var proposal struct {
		Candidate string `json:"candidate"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(response.Result, &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Status != "pending" ||
		!strings.Contains(proposal.Candidate, "Ion SOUL v2") ||
		!strings.Contains(proposal.Candidate, "prove production behavior") {
		t.Fatalf("SOUL v2 proposal = %+v", proposal)
	}
}

func TestProductionTemporalInterruptionReturnAndClosureRestart(t *testing.T) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC),
	}
	directory := t.TempDir()
	initializeRuntimeVault(t, ctx, directory)
	config := RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(), Clock: clock,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	sessionID := createAcceptanceSession(
		t, ctx, runtime, actor, "temporal-restart",
	)
	content := "Investigate and repair the production failure."
	if err := runtime.capabilityRoot.living.Submitted(
		ctx, actor, sessionID, content,
	); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(20 * time.Minute)
	if err := runtime.capabilityRoot.living.Failed(
		ctx, actor, sessionID, content, "provider_timeout",
	); err != nil {
		t.Fatal(err)
	}
	scope := controlplane.Scope{ActorID: actor, SessionID: &sessionID}
	projection, err := runtime.capabilityRoot.LivingState(ctx, scope)
	if err != nil || projection.Signals == nil ||
		projection.Signals.Stage != "interrupted" {
		t.Fatalf("interrupted production signals = %+v, %v", projection.Signals, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(2 * time.Hour)
	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.capabilityRoot.living.Submitted(
		ctx, actor, sessionID, content,
	); err != nil {
		t.Fatal(err)
	}
	projection, err = restarted.capabilityRoot.LivingState(ctx, scope)
	if err != nil || projection.Signals == nil ||
		projection.Signals.Stage != "returned" ||
		projection.Signals.ReturnedAfter != 2*time.Hour {
		t.Fatalf("returned production signals = %+v, %v", projection.Signals, err)
	}
	if err := restarted.capabilityRoot.living.Completed(
		ctx, actor, sessionID, content, agent.Response{Content: "repaired"},
	); err != nil {
		t.Fatal(err)
	}
	projection, err = restarted.capabilityRoot.LivingState(ctx, scope)
	if err != nil || projection.Signals == nil ||
		projection.Signals.Stage != "closure" {
		t.Fatalf("closure production signals = %+v, %v", projection.Signals, err)
	}
}

func TestLongitudinalThreeDayRestartContinuityHasNoUnsupportedClaims(t *testing.T) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC),
	}
	directory := t.TempDir()
	initializeRuntimeVault(t, ctx, directory)
	config := RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(), Clock: clock,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor, otherActor := uuid.New(), uuid.New()
	sessionID := createAcceptanceSession(
		t, ctx, runtime, actor, "three-day-continuity",
	)
	content := "I prefer direct conclusions. Investigate the bounded failure."
	if err := runtime.capabilityRoot.living.Submitted(
		ctx, actor, sessionID, content,
	); err != nil {
		t.Fatal(err)
	}
	appendBriefEvent(
		t, ctx, runtime.journal, actor,
		controlplane.EventTurnCompleted, clock.now.Add(time.Hour), `{}`,
	)
	if err := runtime.capabilityRoot.living.Failed(
		ctx, actor, sessionID, content, "provider_timeout",
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(72 * time.Hour)
	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	brief, err := restarted.presence.ReturnBrief(ctx, actor, "7d")
	if err != nil || briefSectionCount(brief, "completed_work") != 1 ||
		briefSectionCount(brief, "failures") != 0 ||
		briefSectionCount(brief, "repairs") != 0 {
		t.Fatalf("three-day brief = %+v, %v", brief, err)
	}
	empty, err := restarted.presence.ReturnBrief(ctx, otherActor, "7d")
	if err != nil || empty.Status != "no_activity" || len(empty.Sections) != 0 {
		t.Fatalf("cross-actor empty brief = %+v, %v", empty, err)
	}
	if err := restarted.capabilityRoot.living.Submitted(
		ctx, actor, sessionID, content,
	); err != nil {
		t.Fatal(err)
	}
	projection, err := restarted.capabilityRoot.LivingState(
		ctx, controlplane.Scope{ActorID: actor, SessionID: &sessionID},
	)
	if err != nil || projection.Signals == nil ||
		projection.Signals.Stage != "returned" ||
		len(projection.Relationships) == 0 {
		t.Fatalf("three-day return projection = %+v, %v", projection, err)
	}
	isolated, err := restarted.capabilityRoot.LivingState(
		ctx, controlplane.Scope{ActorID: otherActor},
	)
	if err != nil || len(isolated.Relationships) != 0 ||
		isolated.Decision != nil || isolated.Repair != nil {
		t.Fatalf("cross-actor longitudinal projection = %+v, %v", isolated, err)
	}
}

func hasDecisionCause(policy decision.LivenessDecisionPolicy, code string) bool {
	for _, cause := range policy.Causes {
		if cause.Code == code {
			return true
		}
	}
	return false
}
