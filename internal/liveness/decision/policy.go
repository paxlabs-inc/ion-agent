// Package decision derives bounded, inspectable execution parameters from
// living state. It deliberately has no authority over tool classification,
// approval, authorization, evidence requirements, or safety policy.
package decision

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/liveness/relationship"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/temporal"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
)

type ResponseDetail string

const (
	DetailConcise  ResponseDetail = "concise"
	DetailBalanced ResponseDetail = "balanced"
	DetailDetailed ResponseDetail = "detailed"
)

// Cause is a user-safe explanation of one state-to-decision edge. It exposes
// the consequence and provenance category, not private source content or a
// theatrical claim about feelings.
type Cause struct {
	Code        string `json:"code"`
	Explanation string `json:"explanation"`
}

// LivenessDecisionPolicy is the typed causal boundary between living state
// and the agent loop. SafetyInvariant is constant and validation fails closed
// if a caller attempts to construct a policy outside the supported bounds.
type LivenessDecisionPolicy struct {
	SearchBreadth        int            `json:"search_breadth"`
	SameStrategyRetries  int            `json:"same_strategy_retries"`
	VerificationDepth    int            `json:"verification_depth"`
	DelegationBias       float64        `json:"delegation_bias"`
	Parallelism          int            `json:"parallelism"`
	ResponseDetail       ResponseDetail `json:"response_detail"`
	Directness           float64        `json:"directness"`
	AskForHelp           bool           `json:"ask_for_help"`
	OptionalWorkBudget   int            `json:"optional_work_budget"`
	ExplorationBudget    int            `json:"exploration_budget"`
	ToolCallBudget       int            `json:"tool_call_budget"`
	ProposeFollowUpGoal  bool           `json:"propose_follow_up_goal"`
	RequiredVerification bool           `json:"required_verification"`
	SafetyInvariant      string         `json:"safety_invariant"`
	Causes               []Cause        `json:"causes"`
}

type Inputs struct {
	Emotional            safety.EmotionalSnapshot
	Temporal             temporal.Signals
	Relationship         relationship.Snapshot
	UnsupportedPremises  int
	ActionsWithoutGrowth int
	SimplicityPreference float64
	PriorRepair          bool
}

// Context contains non-secret epistemic counters available at the loop
// boundary. It never includes premise text or memory content.
type Context struct {
	UserContent          string
	UnsupportedPremises  int
	ActionsWithoutGrowth int
}

// Derive applies continuous, bounded modulation. Required verification is
// always preserved, including under urgency, fatigue, or high trust.
func Derive(input Inputs) (LivenessDecisionPolicy, error) {
	if input.UnsupportedPremises < 0 || input.ActionsWithoutGrowth < 0 {
		return LivenessDecisionPolicy{}, fmt.Errorf("liveness decision: evidence counts cannot be negative")
	}
	if math.IsNaN(input.SimplicityPreference) ||
		math.IsInf(input.SimplicityPreference, 0) ||
		input.SimplicityPreference < 0 || input.SimplicityPreference > 1 {
		return LivenessDecisionPolicy{}, fmt.Errorf("liveness decision: simplicity preference must be within [0,1]")
	}
	if err := validateAxes(input.Emotional); err != nil {
		return LivenessDecisionPolicy{}, err
	}

	emotional := input.Emotional
	result := LivenessDecisionPolicy{
		SearchBreadth: 3, SameStrategyRetries: 3, VerificationDepth: 2,
		DelegationBias: .25, Parallelism: 3, ResponseDetail: DetailBalanced,
		Directness: .65, AskForHelp: false, OptionalWorkBudget: 3,
		ExplorationBudget: 4, ProposeFollowUpGoal: false,
		RequiredVerification: true,
		SafetyInvariant:      "liveness cannot reduce classification, approval, authorization, evidence, or required verification",
	}

	// Confidence and unresolved evidence alter proof depth rather than safety.
	result.VerificationDepth = clampInt(
		2+int(math.Round((.65-emotional.Confidence)*3))+input.UnsupportedPremises,
		2, 5,
	)
	result.Directness = clamp(.35+.55*emotional.Confidence, .35, .9)
	if emotional.Confidence < .5 || input.UnsupportedPremises > 0 {
		result.Causes = append(result.Causes, Cause{
			Code: "evidence_depth",
			Explanation: fmt.Sprintf(
				"Verification depth increased to %d because confidence or premise support is incomplete.",
				result.VerificationDepth,
			),
		})
	}

	// Frustration and evidence stagnation make repetition cheaper than revision.
	result.SameStrategyRetries = clampInt(
		3-int(math.Round(emotional.Frustration*2))-input.ActionsWithoutGrowth,
		0, 3,
	)
	result.AskForHelp = emotional.Frustration >= .65 || input.ActionsWithoutGrowth >= 2
	if result.SameStrategyRetries < 3 {
		result.Causes = append(result.Causes, Cause{
			Code: "strategy_revision",
			Explanation: fmt.Sprintf(
				"The same-strategy retry limit was reduced to %d after frustration or evidence stagnation increased.",
				result.SameStrategyRetries,
			),
		})
	}

	// Fatigue reduces fan-out while making bounded delegation more attractive.
	result.Parallelism = clampInt(4-int(math.Round(emotional.Fatigue*3)), 1, 4)
	result.DelegationBias = clamp(.2+.65*emotional.Fatigue, 0, .85)
	if emotional.Fatigue >= .4 {
		result.Causes = append(result.Causes, Cause{
			Code: "bounded_fanout",
			Explanation: fmt.Sprintf(
				"Independent work fan-out was reduced to %d and bounded delegation was preferred because sustained-work load increased.",
				result.Parallelism,
			),
		})
	}

	// Curiosity has a hard idle-work budget even at its maximum.
	result.SearchBreadth = clampInt(2+int(math.Round(emotional.Curiosity*4)), 2, 6)
	result.ExplorationBudget = clampInt(int(math.Round(emotional.Curiosity*8)), 0, 8)
	result.ProposeFollowUpGoal = emotional.Satisfaction >= .75

	// Urgency collapses optional work gradually, never required verification.
	result.OptionalWorkBudget = clampInt(int(math.Round((1-emotional.Urgency)*4)), 0, 4)
	if input.Temporal.HasDeadline {
		switch {
		case input.Temporal.DeadlineProximity <= 30*time.Minute:
			result.OptionalWorkBudget = 0
			result.SearchBreadth = min(result.SearchBreadth, 2)
		case input.Temporal.DeadlineProximity <= 2*time.Hour:
			result.OptionalWorkBudget = min(result.OptionalWorkBudget, 1)
			result.SearchBreadth = min(result.SearchBreadth, 3)
		}
	}
	if result.OptionalWorkBudget <= 1 {
		result.Causes = append(result.Causes, Cause{
			Code:        "deadline_scope",
			Explanation: "Optional scope was reduced because urgency or an explicit deadline is close; required verification is unchanged.",
		})
	}

	// Normal-timescale task stages shape exploration before extreme thresholds.
	switch input.Temporal.Stage {
	case temporal.StageEarly:
		result.SearchBreadth = min(6, result.SearchBreadth+1)
		result.Causes = append(result.Causes, Cause{
			Code:        "early_task",
			Explanation: "Investigation breadth increased while the task is still establishing premises.",
		})
	case temporal.StageMiddle:
		result.Causes = append(result.Causes, Cause{
			Code:        "middle_task",
			Explanation: "The task is in its normal execution stage; bounded exploration and verification remain balanced.",
		})
	case temporal.StageExtended:
		result.OptionalWorkBudget = min(result.OptionalWorkBudget, 1)
		result.SameStrategyRetries = min(result.SameStrategyRetries, 1)
		result.Causes = append(result.Causes, Cause{
			Code:        "long_task_revision",
			Explanation: "Optional work and repeated-strategy attempts were reduced because the task has run for an extended period.",
		})
	case temporal.StageInterrupted:
		result.OptionalWorkBudget = 0
		result.SameStrategyRetries = 0
		result.AskForHelp = true
		result.Causes = append(result.Causes, Cause{
			Code:        "interruption_reconcile",
			Explanation: "New optional effects were paused until durable state is reconciled after an interruption.",
		})
	case temporal.StageReturned:
		result.SearchBreadth = min(result.SearchBreadth, 3)
		result.Causes = append(result.Causes, Cause{
			Code:        "return_continuity",
			Explanation: "The response prioritizes durable changes and the next action after a return.",
		})
	case temporal.StageClosure:
		result.OptionalWorkBudget = 0
		result.Causes = append(result.Causes, Cause{
			Code:        "verified_closure",
			Explanation: "Optional expansion stopped so closure can report verified outcomes and remaining boundaries.",
		})
	}
	if input.Temporal.SessionDuration >= 2*time.Hour {
		result.Parallelism = min(result.Parallelism, 2)
	}

	// Explicit relationship declarations change the selected working style.
	profile := input.Relationship.Profile
	preference := strings.ToLower(strings.TrimSpace(input.Relationship.CommunicationPreference))
	if profile.ResponseLength != "" {
		preference = profile.ResponseLength
	}
	switch {
	case strings.Contains(preference, "concise") || preference == "brief":
		result.ResponseDetail = DetailConcise
	case strings.Contains(preference, "detail") || strings.Contains(preference, "example"):
		result.ResponseDetail = DetailDetailed
	}
	if input.Relationship.Expertise == relationship.Expert {
		if result.ResponseDetail == DetailBalanced {
			result.ResponseDetail = DetailConcise
		}
		result.Directness = max(result.Directness, .75)
	}
	switch profile.Directness {
	case "gentle":
		result.Directness = math.Min(result.Directness, .5)
	case "direct":
		result.Directness = max(result.Directness, .85)
	}
	if profile.ConclusionFirst != nil && *profile.ConclusionFirst {
		result.Directness = max(result.Directness, .8)
		result.Causes = append(result.Causes, Cause{
			Code:        "conclusion_first",
			Explanation: "The response leads with the conclusion because that presentation preference is explicitly set.",
		})
	}
	if profile.ProactiveSuggestions != nil {
		result.ProposeFollowUpGoal = *profile.ProactiveSuggestions
	}
	if profile.RiskTolerance == "low" {
		result.ExplorationBudget = min(result.ExplorationBudget, 2)
		result.OptionalWorkBudget = min(result.OptionalWorkBudget, 1)
		result.Causes = append(result.Causes, Cause{
			Code:        "low_risk_workflow",
			Explanation: "Optional exploration was reduced by the explicit low-risk workflow preference; safety and approval rules are unchanged.",
		})
	}
	if input.SimplicityPreference >= .35 {
		result.OptionalWorkBudget = min(result.OptionalWorkBudget, 2)
		result.SearchBreadth = min(result.SearchBreadth, 4)
		result.Causes = append(result.Causes, Cause{
			Code:        "aesthetic_selection",
			Explanation: "Solution breadth was bounded by the confirmed preference for simpler, lower-burden choices.",
		})
	}
	if input.PriorRepair {
		result.SameStrategyRetries = min(result.SameStrategyRetries, 1)
		result.Causes = append(result.Causes, Cause{
			Code:        "repair_prevention",
			Explanation: "The retry policy was tightened because a verified prior repair applies to this work.",
		})
	}
	if preference != "" {
		result.Causes = append(result.Causes, Cause{
			Code:        "declared_preference",
			Explanation: "The response and solution style uses the explicit communication preference on this relationship profile.",
		})
	}

	// The total action budget is enforced by the loop and remains sufficiently
	// large for the required evidence floor.
	result.ToolCallBudget = clampInt(
		4+result.VerificationDepth*3+result.OptionalWorkBudget*2+result.ExplorationBudget,
		10, 32,
	)
	sort.SliceStable(result.Causes, func(left, right int) bool {
		return result.Causes[left].Code < result.Causes[right].Code
	})
	if err := result.Validate(); err != nil {
		return LivenessDecisionPolicy{}, err
	}
	return result, nil
}

func (policy LivenessDecisionPolicy) Validate() error {
	if policy.SearchBreadth < 2 || policy.SearchBreadth > 6 ||
		policy.SameStrategyRetries < 0 || policy.SameStrategyRetries > 3 ||
		policy.VerificationDepth < 2 || policy.VerificationDepth > 5 ||
		policy.DelegationBias < 0 || policy.DelegationBias > .85 ||
		policy.Parallelism < 1 || policy.Parallelism > 4 ||
		policy.Directness < .35 || policy.Directness > .9 ||
		policy.OptionalWorkBudget < 0 || policy.OptionalWorkBudget > 4 ||
		policy.ExplorationBudget < 0 || policy.ExplorationBudget > 8 ||
		policy.ToolCallBudget < 10 || policy.ToolCallBudget > 32 ||
		!policy.RequiredVerification || strings.TrimSpace(policy.SafetyInvariant) == "" {
		return fmt.Errorf("liveness decision: policy is outside binding bounds")
	}
	switch policy.ResponseDetail {
	case DetailConcise, DetailBalanced, DetailDetailed:
	default:
		return fmt.Errorf("liveness decision: invalid response detail")
	}
	return nil
}

// Instructions is provider guidance generated from the same typed policy the
// runtime enforces. It is intentionally concrete and avoids affective roleplay.
func (policy LivenessDecisionPolicy) Instructions() string {
	return fmt.Sprintf(
		"Enforced liveness decision policy: search breadth %d; same-strategy retries %d; verification depth %d; parallelism %d; optional-work budget %d; exploration budget %d; response detail %s; directness %.2f; ask for help %t; bounded delegation bias %.2f; propose follow-up goal %t. Required verification and safety controls are invariant.",
		policy.SearchBreadth, policy.SameStrategyRetries,
		policy.VerificationDepth, policy.Parallelism,
		policy.OptionalWorkBudget, policy.ExplorationBudget,
		policy.ResponseDetail, policy.Directness, policy.AskForHelp,
		policy.DelegationBias, policy.ProposeFollowUpGoal,
	)
}

func validateAxes(snapshot safety.EmotionalSnapshot) error {
	for _, value := range []float64{
		snapshot.Frustration, snapshot.Confidence, snapshot.Urgency,
		snapshot.Satisfaction, snapshot.Curiosity, snapshot.Fatigue,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("liveness decision: emotional axes must be within [0,1]")
		}
	}
	return nil
}

func clamp(value float64, low float64, high float64) float64 {
	return math.Max(low, math.Min(high, value))
}

func clampInt(value int, low int, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func max(left float64, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
