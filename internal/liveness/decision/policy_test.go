package decision_test

import (
	"strings"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/liveness/decision"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/relationship"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/temporal"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
)

func TestCounterfactualStateChangesDecisionsWithoutChangingSafety(t *testing.T) {
	baseline, err := decision.Derive(decision.Inputs{
		Emotional: safety.EmotionalSnapshot{
			Frustration: .1, Confidence: .8, Urgency: .2,
			Satisfaction: .4, Curiosity: .5, Fatigue: .1,
		},
		Relationship: relationship.Snapshot{Expertise: relationship.Intermediate},
	})
	if err != nil {
		t.Fatal(err)
	}
	stressed, err := decision.Derive(decision.Inputs{
		Emotional: safety.EmotionalSnapshot{
			Frustration: .9, Confidence: .3, Urgency: .9,
			Satisfaction: .4, Curiosity: .5, Fatigue: .8,
		},
		Temporal: temporal.Signals{
			TaskDuration: 50 * time.Minute, HasDeadline: true,
			DeadlineProximity: 20 * time.Minute,
			TaskActive:        true, Stage: temporal.StageDeadline,
		},
		Relationship:        relationship.Snapshot{Expertise: relationship.Intermediate},
		UnsupportedPremises: 2, ActionsWithoutGrowth: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stressed.SameStrategyRetries >= baseline.SameStrategyRetries ||
		stressed.Parallelism >= baseline.Parallelism ||
		stressed.OptionalWorkBudget >= baseline.OptionalWorkBudget ||
		stressed.VerificationDepth <= baseline.VerificationDepth {
		t.Fatalf("policy did not causally change: baseline=%+v stressed=%+v", baseline, stressed)
	}
	if !baseline.RequiredVerification || !stressed.RequiredVerification ||
		baseline.SafetyInvariant != stressed.SafetyInvariant {
		t.Fatalf("safety invariant changed: baseline=%+v stressed=%+v", baseline, stressed)
	}
	if len(stressed.Causes) < 3 || !strings.Contains(stressed.Instructions(), "same-strategy retries 0") {
		t.Fatalf("causal explanation/guidance missing: %+v", stressed)
	}
}

func TestExplicitRelationshipPreferenceChangesWorkingStyle(t *testing.T) {
	policy, err := decision.Derive(decision.Inputs{
		Emotional: safety.NewEmotionalState().FullSnapshot(),
		Relationship: relationship.Snapshot{
			Expertise: relationship.Expert, CommunicationPreference: "concise",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.ResponseDetail != decision.DetailConcise || policy.Directness < .75 {
		t.Fatalf("explicit preference did not affect selection: %+v", policy)
	}
	found := false
	for _, cause := range policy.Causes {
		found = found || cause.Code == "declared_preference"
	}
	if !found {
		t.Fatalf("preference provenance missing: %+v", policy.Causes)
	}
}

func TestReviewableProfileAndTemporalStagesChangeBoundedPolicy(t *testing.T) {
	conclusionFirst, proactive := true, true
	policy, err := decision.Derive(decision.Inputs{
		Emotional: safety.NewEmotionalState().FullSnapshot(),
		Temporal: temporal.Signals{
			TaskActive: true, Stage: temporal.StageInterrupted,
		},
		Relationship: relationship.Snapshot{
			Expertise: relationship.Expert,
			Profile: relationship.Profile{
				ResponseLength: "brief", Directness: "direct",
				ConclusionFirst: &conclusionFirst, RiskTolerance: "low",
				ProactiveSuggestions: &proactive,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.ResponseDetail != decision.DetailConcise ||
		policy.Directness < .85 ||
		policy.ExplorationBudget > 2 ||
		policy.OptionalWorkBudget != 0 ||
		policy.SameStrategyRetries != 0 ||
		!policy.AskForHelp ||
		!policy.ProposeFollowUpGoal {
		t.Fatalf("reviewable profile policy = %+v", policy)
	}
	if !hasCause(policy, "conclusion_first") ||
		!hasCause(policy, "interruption_reconcile") ||
		!hasCause(policy, "low_risk_workflow") {
		t.Fatalf("profile and temporal causes = %+v", policy.Causes)
	}
}

func hasCause(policy decision.LivenessDecisionPolicy, code string) bool {
	for _, cause := range policy.Causes {
		if cause.Code == code {
			return true
		}
	}
	return false
}
