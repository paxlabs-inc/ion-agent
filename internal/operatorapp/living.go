package operatorapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/aesthetic"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/decision"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/relationship"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/repair"
	"github.com/paxlabs-inc/ion-agent/internal/liveness/temporal"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/presence/identity"
	"github.com/paxlabs-inc/ion-agent/internal/security/circuit"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	relationshipStateKind = "relationship"
	temporalStateKind     = "temporal"
	decisionStateKind     = "liveness_decision"
	aestheticStateKind    = "aesthetic_profile"
	repairStateKind       = "repair_profile"
	livingSessionKind     = "living_session"
	actorIdentityKind     = "actor_identity"
	defaultSoul           = `# Ion SOUL

Ion is an evidence-led operator agent.

- Be candid about uncertainty and incomplete work.
- Preserve the user's agency and require approval for consequential effects.
- Adapt explanation depth without weakening policy, evidence, or verification.
- Treat identity changes as explicit, reviewable, reversible decisions.`
)

type livingContext struct {
	mu                sync.Mutex
	clock             types.Clock
	store             *session.Store
	memory            *cortex.Cortex
	relationships     *relationship.Model
	temporals         map[string]*temporal.Tracker
	emotional         map[string]*safety.EmotionalState
	circuits          map[string]*circuit.Breaker
	decisions         map[string]decision.LivenessDecisionPolicy
	aesthetics        map[string]*aesthetic.Model
	aestheticProfiles map[string]aestheticProfile
	repair            *repair.Model
	repairProfiles    map[string]repairProfile
	actorIdentities   map[string]actorIdentity
	soul              *identity.Service
	emitter           controlplane.EventEmitter
}

type livingProjection struct {
	Relationships []relationship.Snapshot          `json:"relationships"`
	PreferredName string                           `json:"preferred_name,omitempty"`
	Temporal      *temporal.State                  `json:"temporal,omitempty"`
	Signals       *temporal.Signals                `json:"signals,omitempty"`
	Emotional     safety.EmotionalSnapshot         `json:"emotional"`
	SoulVersion   uint64                           `json:"soul_version"`
	SoulHash      string                           `json:"soul_hash"`
	Safety        string                           `json:"safety_boundary"`
	Presence      *presenceProjection              `json:"presence,omitempty"`
	Decision      *decision.LivenessDecisionPolicy `json:"decision,omitempty"`
	Aesthetic     *aestheticProfile                `json:"aesthetic,omitempty"`
	Repair        *repairProfile                   `json:"repair,omitempty"`
}

type aestheticProfile struct {
	Version   int                `json:"version"`
	ActorID   uuid.UUID          `json:"actor_id"`
	Label     string             `json:"label"`
	Weights   aesthetic.Criteria `json:"weights"`
	UpdatedAt time.Time          `json:"updated_at"`
}

type repairProfile struct {
	Version       int       `json:"version"`
	ActorID       uuid.UUID `json:"actor_id"`
	Domain        string    `json:"domain"`
	ToolName      string    `json:"tool_name"`
	FailureID     uuid.UUID `json:"failure_id"`
	LessonID      uuid.UUID `json:"lesson_id"`
	Lesson        string    `json:"lesson"`
	EvidenceCount int       `json:"evidence_count"`
	LearnedAt     time.Time `json:"learned_at"`
}

type actorIdentity struct {
	Version       int       `json:"version"`
	ActorID       uuid.UUID `json:"actor_id"`
	PreferredName string    `json:"preferred_name,omitempty"`
	NamePrompted  bool      `json:"name_prompted"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (living *livingContext) relationshipSnapshots() []relationship.Snapshot {
	living.mu.Lock()
	defer living.mu.Unlock()
	return living.relationships.AllSnapshots()
}

func openLivingContext(
	ctx context.Context,
	clock types.Clock,
	config RuntimeConfig,
	store *session.Store,
	memoryStore *cortex.Cortex,
) (*livingContext, error) {
	if clock == nil || store == nil || memoryStore == nil {
		return nil, fmt.Errorf("operator living context: dependencies are required")
	}
	states, err := store.ListLivingStates(ctx, relationshipStateKind)
	if err != nil {
		return nil, err
	}
	durableRelationships := make([]relationship.State, 0, len(states))
	for _, stored := range states {
		var state relationship.State
		if err := json.Unmarshal(stored.State, &state); err != nil {
			return nil, fmt.Errorf(
				"operator living context: decode relationship: %w", err,
			)
		}
		expectedScope := relationshipScope(
			state.Snapshot.UserID, state.Snapshot.Domain,
		)
		if stored.Scope != expectedScope {
			return nil, fmt.Errorf(
				"operator living context: relationship scope mismatch",
			)
		}
		durableRelationships = append(durableRelationships, state)
	}
	model, err := relationship.Restore(clock, durableRelationships)
	if err != nil {
		return nil, err
	}
	identityStates, err := store.ListLivingStates(ctx, actorIdentityKind)
	if err != nil {
		return nil, err
	}
	actorIdentities := make(map[string]actorIdentity, len(identityStates))
	for _, stored := range identityStates {
		var state actorIdentity
		if err := json.Unmarshal(stored.State, &state); err != nil ||
			state.Version != 1 || state.ActorID == uuid.Nil ||
			stored.Scope != state.ActorID.String() ||
			!state.NamePrompted || state.UpdatedAt.IsZero() {
			return nil, fmt.Errorf(
				"operator living context: invalid actor identity state",
			)
		}
		if state.PreferredName != "" {
			name, valid := relationship.PreferredNameFromDeclaration(
				state.PreferredName,
			)
			if !valid || name != state.PreferredName {
				return nil, fmt.Errorf(
					"operator living context: invalid preferred name",
				)
			}
		}
		actorIdentities[state.ActorID.String()] = state
	}
	file, err := identity.Bootstrap(
		config.DataDirectory+"/presence", defaultSoul,
	)
	if err != nil {
		return nil, err
	}
	soul, err := identity.NewService(ctx, file, store, clock)
	if err != nil {
		return nil, err
	}
	repairModel, err := repair.New(memoryStore, clock)
	if err != nil {
		return nil, err
	}
	return &livingContext{
		clock: clock, store: store, memory: memoryStore, relationships: model,
		temporals:         make(map[string]*temporal.Tracker),
		emotional:         make(map[string]*safety.EmotionalState),
		circuits:          make(map[string]*circuit.Breaker),
		decisions:         make(map[string]decision.LivenessDecisionPolicy),
		aesthetics:        make(map[string]*aesthetic.Model),
		aestheticProfiles: make(map[string]aestheticProfile),
		repair:            repairModel, repairProfiles: make(map[string]repairProfile),
		actorIdentities: actorIdentities,
		soul:            soul,
	}, nil
}

func (living *livingContext) SetEmitter(emitter controlplane.EventEmitter) {
	living.mu.Lock()
	defer living.mu.Unlock()
	living.emitter = emitter
}

func (living *livingContext) PreferredName(
	_ context.Context,
	actorID uuid.UUID,
) (string, bool) {
	living.mu.Lock()
	defer living.mu.Unlock()
	state, exists := living.actorIdentities[actorID.String()]
	if !exists || state.PreferredName == "" {
		return "", false
	}
	return state.PreferredName, true
}

func (living *livingContext) preparePreferredNameTurn(
	ctx context.Context,
	actorID uuid.UUID,
	content string,
) (string, bool, error) {
	if actorID == uuid.Nil {
		return "", false, fmt.Errorf(
			"operator living context: authenticated actor is required",
		)
	}
	if principal := policy.PrincipalFromContext(ctx); principal.Sender ==
		policy.SenderScheduler {
		return "", false, nil
	}
	living.mu.Lock()
	defer living.mu.Unlock()
	key := actorID.String()
	state, exists := living.actorIdentities[key]
	if !exists {
		state = actorIdentity{
			Version: 1, ActorID: actorID, NamePrompted: true,
			UpdatedAt: living.clock.Now().UTC(),
		}
		if err := living.saveActorIdentityLocked(ctx, state); err != nil {
			return "", false, err
		}
		living.actorIdentities[key] = state
		return "Before we begin, what should I call you?", true, nil
	}
	if state.PreferredName != "" {
		return "", false, nil
	}
	name, valid := relationship.PreferredNameFromDeclaration(content)
	if !valid {
		return "I didn't catch a name. What should I call you?", true, nil
	}
	state.PreferredName = name
	state.UpdatedAt = living.clock.Now().UTC()
	if err := living.saveActorIdentityLocked(ctx, state); err != nil {
		return "", false, err
	}
	living.actorIdentities[key] = state
	return fmt.Sprintf(
		"Thanks, %s. What should we work on?", name,
	), true, nil
}

func (living *livingContext) Submitted(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	content string,
) error {
	if actorID == uuid.Nil || sessionID == uuid.Nil {
		return fmt.Errorf("operator living context: authenticated scope is required")
	}
	userID := actorID.String()
	domain := relationshipDomain(content)
	expertise, expertiseDeclared := declaredExpertise(content)
	preference, preferenceDeclared := declaredPreference(content)

	living.mu.Lock()
	if err := living.ensureSessionOwnerLocked(
		ctx, actorID, sessionID, true,
	); err != nil {
		living.mu.Unlock()
		return err
	}
	snapshot, err := living.relationships.Prepare(
		userID, domain, expertise, preference,
		expertiseDeclared, preferenceDeclared,
	)
	if err != nil {
		living.mu.Unlock()
		return err
	}
	if err := living.saveRelationshipLocked(ctx, userID, domain); err != nil {
		living.mu.Unlock()
		return err
	}
	var changedAesthetic *aestheticProfile
	if label, weights, declared := declaredAesthetic(content); declared {
		model, profile, modelErr := living.aestheticLocked(ctx, actorID)
		if modelErr != nil {
			living.mu.Unlock()
			return modelErr
		}
		if profile == nil || profile.Label != label || profile.Weights != weights {
			if modelErr := model.LearnForActor(
				ctx, actorID.String(), label, weights, true,
			); modelErr != nil {
				living.mu.Unlock()
				return modelErr
			}
			updated := aestheticProfile{
				Version: 1, ActorID: actorID, Label: label,
				Weights: model.Snapshot(), UpdatedAt: living.clock.Now().UTC(),
			}
			if modelErr := living.saveAestheticLocked(ctx, updated); modelErr != nil {
				living.mu.Unlock()
				return modelErr
			}
			changedAesthetic = &updated
		}
	}
	tracker, err := living.temporalLocked(ctx, actorID, sessionID)
	if err != nil {
		living.mu.Unlock()
		return err
	}
	tracker.UserInteraction()
	if requiresLivingTask(content) {
		tracker.StartTask()
	}
	if deadline, ok := explicitDeadline(content); ok {
		if err := tracker.SetDeadline(deadline); err != nil {
			living.mu.Unlock()
			return err
		}
	}
	if err := living.saveTemporalLocked(ctx, actorID, sessionID, tracker); err != nil {
		living.mu.Unlock()
		return err
	}
	emitter := living.emitter
	signals := tracker.Snapshot()
	living.mu.Unlock()

	correlation := controlplane.Correlation{
		ActorID: actorID, SessionID: &sessionID,
	}
	if err := emitLiving(
		ctx, emitter, controlplane.EventRelationshipChanged, correlation,
		map[string]any{
			"domain": snapshot.Domain, "trust": snapshot.Trust,
			"expertise":             snapshot.Expertise,
			"interaction_frequency": snapshot.InteractionFrequency,
		},
	); err != nil {
		return err
	}
	if changedAesthetic != nil {
		if err := emitLiving(
			ctx, emitter, controlplane.EventAestheticChanged, correlation,
			map[string]any{
				"label": changedAesthetic.Label, "confirmed": true,
				"effect": "solution selection",
			},
		); err != nil {
			return err
		}
	}
	return emitLiving(
		ctx, emitter, controlplane.EventTemporalChanged, correlation,
		map[string]any{
			"signals": signals, "task_active": !tracker.State().TaskStarted.IsZero(),
		},
	)
}

func (living *livingContext) Completed(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	content string,
	response agent.Response,
) error {
	verified := 0
	failed := 0
	for _, execution := range response.ToolEvents {
		if execution.Error != "" {
			failed++
			continue
		}
		if execution.Event == nil {
			continue
		}
		event := execution.Event
		if event.MMRRootAtTime != [32]byte{} &&
			(event.Match == nil || *event.Match) {
			verified++
		}
	}
	userID := actorID.String()
	domain := relationshipDomain(content)
	living.mu.Lock()
	snapshot, err := living.relationships.RecordCompleted(userID, domain, 0)
	if err == nil && verified > 0 {
		snapshot, err = living.relationships.AdjustTrust(userID, domain, 0.01)
	}
	if err == nil {
		err = living.saveRelationshipLocked(ctx, userID, domain)
	}
	if err == nil {
		tracker, trackerErr := living.temporalLocked(ctx, actorID, sessionID)
		if trackerErr == nil {
			tracker.CompleteTask()
			trackerErr = living.saveTemporalLocked(
				ctx, actorID, sessionID, tracker,
			)
		}
		err = trackerErr
	}
	emotional, emotionalErr := living.emotionalLocked(ctx, actorID)
	if err == nil {
		err = emotionalErr
	}
	if err == nil {
		snapshot := emotional.FullSnapshot()
		switch {
		case verified > 0:
			snapshot.Confidence += 0.02
			snapshot.Satisfaction += 0.02
			snapshot.Frustration -= 0.02
		case failed > 0:
			snapshot.Confidence -= 0.02
			snapshot.Satisfaction -= 0.02
			snapshot.Frustration += 0.05
		}
		snapshot.UpdatedAt = living.clock.Now().UTC()
		emotional.UpdateAll(snapshot)
		err = living.store.SaveEmotionalState(
			ctx, actorID.String(), emotional,
		)
	}
	emotionalSnapshot := safety.EmotionalSnapshot{}
	if emotional != nil {
		emotionalSnapshot = emotional.FullSnapshot()
	}
	emitter := living.emitter
	living.mu.Unlock()
	if err != nil {
		return err
	}
	if err := emitLiving(
		ctx, emitter, controlplane.EventRelationshipChanged,
		controlplane.Correlation{ActorID: actorID, SessionID: &sessionID},
		map[string]any{
			"domain": domain, "trust": snapshot.Trust,
			"verified_outcomes":     verified,
			"interaction_frequency": snapshot.InteractionFrequency,
		},
	); err != nil {
		return err
	}
	cause := "relationship_completed"
	if failed > 0 {
		cause = "tool_failure"
	} else if verified > 0 {
		cause = "verified_outcome"
	}
	if err := living.recordVerifiedRepair(
		ctx, actorID, domain, response, emitter, sessionID,
	); err != nil {
		return err
	}
	return emitLiving(
		ctx, emitter, controlplane.EventEmotionalChanged,
		controlplane.Correlation{ActorID: actorID, SessionID: &sessionID},
		map[string]any{
			"cause": cause, "verified_outcomes": verified,
			"failed_outcomes": failed, "state": emotionalSnapshot,
		},
	)
}

// Failed records a durable failure event even when no recovery succeeds.
func (living *livingContext) Failed(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	content string,
	failureCode string,
) error {
	_, err := living.repair.RecordFailureFor(
		ctx, actorID.String(), fmt.Sprintf(
			"turn failed in %s work with classified code %s",
			relationshipDomain(content), safeFailureCode(failureCode),
		),
	)
	if err != nil {
		return err
	}
	living.mu.Lock()
	tracker, temporalErr := living.temporalLocked(ctx, actorID, sessionID)
	if temporalErr == nil {
		tracker.InterruptTask()
		temporalErr = living.saveTemporalLocked(
			ctx, actorID, sessionID, tracker,
		)
	}
	emotional, stateErr := living.emotionalLocked(ctx, actorID)
	if stateErr == nil {
		stateErr = temporalErr
	}
	if stateErr == nil {
		snapshot := emotional.FullSnapshot()
		snapshot.Confidence -= .03
		snapshot.Frustration += .05
		snapshot.UpdatedAt = living.clock.Now().UTC()
		emotional.UpdateAll(snapshot)
		stateErr = living.store.SaveEmotionalState(ctx, actorID.String(), emotional)
	}
	living.mu.Unlock()
	return stateErr
}

// Recovered binds the classified failure, attempted production recovery, and
// committed recovery evidence into a reusable prevention lesson.
func (living *livingContext) Recovered(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	content string,
	failureCode string,
	response agent.Response,
) error {
	domain := relationshipDomain(content)
	code := safeFailureCode(failureCode)
	failureID, err := living.repair.RecordFailureFor(
		ctx, actorID.String(), fmt.Sprintf(
			"turn encountered classified failure %s during %s work", code, domain,
		),
	)
	if err != nil {
		return err
	}
	evidencePayload, err := json.Marshal(map[string]any{
		"kind": "verified_turn_recovery", "actor_id": actorID,
		"session_id": sessionID, "failure_code": code,
		"provider_calls": response.ProviderCalls,
		"tool_events":    len(response.ToolEvents),
		"verified_at":    living.clock.Now().UTC(),
	})
	if err != nil {
		return err
	}
	evidence, err := living.memory.WriteForActor(
		ctx, actorID.String(), memory.Event, evidencePayload, "turn-recovery",
	)
	if err != nil {
		return err
	}
	lesson := fmt.Sprintf(
		"When %s recurs in %s work, change approach and require committed recovery evidence before claiming completion.",
		code, domain,
	)
	lessonID, err := living.repair.RecordRepairFor(
		ctx, actorID.String(), failureID, lesson, []uuid.UUID{evidence.Head.ID},
	)
	if err != nil {
		return err
	}
	profile := repairProfile{
		Version: 1, ActorID: actorID, Domain: domain, ToolName: "turn",
		FailureID: failureID, LessonID: lessonID, Lesson: lesson,
		EvidenceCount: 1, LearnedAt: living.clock.Now().UTC(),
	}
	if err := living.saveRepairProfile(ctx, profile); err != nil {
		return err
	}
	return emitLiving(
		ctx, living.emitter, controlplane.EventRepairLearned,
		controlplane.Correlation{ActorID: actorID, SessionID: &sessionID},
		map[string]any{
			"domain": domain, "failure_code": code, "evidence_count": 1,
			"effect": "future same-strategy retries reduced",
		},
	)
}

// Owner returns the authenticated actor bound to a production session.
func (living *livingContext) Owner(
	ctx context.Context,
	sessionID uuid.UUID,
) (uuid.UUID, error) {
	if sessionID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("operator living context: session is required")
	}
	living.mu.Lock()
	defer living.mu.Unlock()
	return living.sessionOwnerLocked(ctx, sessionID)
}

// AuthorizedSessions returns only sessions durably bound to the supplied
// authenticated actor. It is the cross-session activation allowlist.
func (living *livingContext) AuthorizedSessions(
	ctx context.Context,
	userID string,
	current uuid.UUID,
) ([]uuid.UUID, error) {
	actorID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil || actorID == uuid.Nil || current == uuid.Nil {
		return nil, fmt.Errorf(
			"operator living context: valid activation scope is required",
		)
	}
	living.mu.Lock()
	defer living.mu.Unlock()
	currentOwner, err := living.sessionOwnerLocked(ctx, current)
	if err != nil {
		return nil, err
	}
	if currentOwner != actorID {
		return nil, fmt.Errorf(
			"operator living context: cross-actor activation denied",
		)
	}
	states, err := living.store.ListLivingStates(ctx, livingSessionKind)
	if err != nil {
		return nil, err
	}
	result := make([]uuid.UUID, 0, len(states))
	for _, stored := range states {
		owner, sessionID, decodeErr := decodeLivingSession(stored.Scope, stored.State)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if owner == actorID {
			result = append(result, sessionID)
		}
	}
	if len(result) == 0 || len(result) > 64 {
		return nil, fmt.Errorf(
			"operator living context: authorized session count is outside bounds",
		)
	}
	return result, nil
}

// Dependencies returns the per-actor emotional state and the circuit breaker
// that observes that same mutable object.
func (living *livingContext) Dependencies(
	ctx context.Context,
	sessionID uuid.UUID,
) (*safety.EmotionalState, *circuit.Breaker, error) {
	living.mu.Lock()
	defer living.mu.Unlock()
	actorID, err := living.sessionOwnerLocked(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	emotional, err := living.emotionalLocked(ctx, actorID)
	if err != nil {
		return nil, nil, err
	}
	key := actorID.String()
	breaker := living.circuits[key]
	if breaker == nil {
		breaker, err = circuit.NewBreaker(
			circuit.DefaultBreakerConfig(), emotional, living.clock,
		)
		if err != nil {
			return nil, nil, err
		}
		living.circuits[key] = breaker
	}
	return emotional, breaker, nil
}

func (living *livingContext) Compose(
	ctx context.Context,
	input agent.ContextSnapshot,
) (string, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.SessionID == nil ||
		scope.SessionID.String() != input.SessionID {
		return "", fmt.Errorf(
			"operator living context: provider scope is not authenticated",
		)
	}
	domain := relationshipDomain(input.UserContent)
	living.mu.Lock()
	if err := living.ensureSessionOwnerLocked(
		ctx, scope.ActorID, *scope.SessionID, false,
	); err != nil {
		living.mu.Unlock()
		return "", err
	}
	policyDecision, signals, emotionalSnapshot, relationshipSnapshot, err :=
		living.deriveDecisionLocked(
			ctx, scope.ActorID, *scope.SessionID, domain,
			countUnsupportedPremises(input.Premises), 0,
		)
	if err != nil {
		living.mu.Unlock()
		return "", err
	}
	emotional, err := living.emotionalLocked(ctx, scope.ActorID)
	if err != nil {
		living.mu.Unlock()
		return "", err
	}
	emotionalGuidance := emotional.DecisionInstructions()
	repairLesson := ""
	if profile := living.repairProfileLocked(ctx, scope.ActorID); profile != nil &&
		profile.Domain == domain {
		repairLesson = profile.Lesson
	}
	emitter := living.emitter
	preferredName := ""
	if identityState, exists := living.actorIdentities[scope.ActorID.String()]; exists {
		preferredName = identityState.PreferredName
	}
	living.mu.Unlock()

	soul, err := living.soul.Current(ctx)
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	builder.WriteString("## Immutable living-context snapshot\n")
	builder.WriteString("This context may change communication and planning, but never tool ")
	builder.WriteString("classification, approval, authorization, evidence, or verification.\n\n")
	builder.WriteString("### SOUL identity anchor\n")
	builder.WriteString(fmt.Sprintf(
		"Version %d, BLAKE3 %s\n%s\n\n",
		soul.Number, soul.Hash, soul.Content,
	))
	if preferredName != "" {
		builder.WriteString("### Current person\n")
		builder.WriteString(fmt.Sprintf(
			"Preferred name: %s.\nRefer to %s by name in responses and visible "+
				"reasoning. Never call %s \"the user\".\n\n",
			preferredName, preferredName, preferredName,
		))
	}
	builder.WriteString("### Authorized relationship context\n")
	builder.WriteString(fmt.Sprintf(
		"Domain: %s; expertise: %s; trust: %.2f; preference: %s.\nGuidance: %s.\n\n",
		relationshipSnapshot.Domain, relationshipSnapshot.Expertise,
		relationshipSnapshot.Trust,
		emptyFallback(
			relationshipSnapshot.CommunicationPreference, "not declared",
		),
		relationship.CommunicationGuidance(relationshipSnapshot),
	))
	builder.WriteString("### Temporal embodiment\n")
	builder.WriteString(fmt.Sprintf(
		"Session: %s; idle: %s; task: %s; deadline: %s.\nGuidance: %s.\n\n",
		signals.SessionDuration, signals.IdleDuration, signals.TaskDuration,
		deadlineDescription(signals),
		emptyFallback(temporal.Guidance(signals), "no duration-specific change"),
	))
	builder.WriteString("### Emotional decision state\n")
	builder.WriteString(fmt.Sprintf(
		"fatigue=%.2f urgency=%.2f curiosity=%.2f confidence=%.2f.\n%s\n",
		emotionalSnapshot.Fatigue, emotionalSnapshot.Urgency,
		emotionalSnapshot.Curiosity, emotionalSnapshot.Confidence,
		emptyFallback(emotionalGuidance, "No additional behavioral modulation."),
	))
	builder.WriteString("\n### Enforced liveness decision policy\n")
	builder.WriteString(policyDecision.Instructions())
	builder.WriteByte('\n')
	for _, cause := range policyDecision.Causes {
		builder.WriteString("- ")
		builder.WriteString(cause.Explanation)
		builder.WriteByte('\n')
	}
	appendContextSection(&builder, "Applicable verified repair lesson", repairLesson)
	appendContextSection(
		&builder, "Relevant encrypted memory activation",
		emptyFallback(input.Memory, "No authorized memory matched this provider step."),
	)
	appendContextSection(&builder, "Observed self-model", input.SelfModel)
	appendContextSection(&builder, "Active premises", input.Premises)
	appendContextSection(&builder, "Task state", input.TaskGraph)
	composed := builder.String()
	if len(composed) > 192<<10 {
		return "", fmt.Errorf("operator living context: composed snapshot exceeds bound")
	}
	correlation := controlplane.Correlation{
		ActorID: scope.ActorID, SessionID: scope.SessionID, TurnID: scope.TurnID,
	}
	if err := emitLiving(
		ctx, emitter, controlplane.EventEmotionalChanged, correlation,
		map[string]any{
			"cause": "temporal_apply", "state": emotionalSnapshot,
		},
	); err != nil {
		return "", err
	}
	if err := emitLiving(
		ctx, emitter, controlplane.EventLivenessDecision, correlation,
		policyDecision,
	); err != nil {
		return "", err
	}
	return composed, nil
}

// LivenessDecisionPolicy derives the same typed policy later included in the
// provider snapshot. The agent loop consumes and enforces this result before
// it admits tool batches or retries.
func (living *livingContext) LivenessDecisionPolicy(
	ctx context.Context,
	input decision.Context,
) (decision.LivenessDecisionPolicy, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok || scope.SessionID == nil || scope.ActorID == uuid.Nil {
		return decision.LivenessDecisionPolicy{}, fmt.Errorf(
			"operator living context: decision scope is not authenticated",
		)
	}
	domain := relationshipDomain(input.UserContent)
	living.mu.Lock()
	defer living.mu.Unlock()
	if err := living.ensureSessionOwnerLocked(
		ctx, scope.ActorID, *scope.SessionID, false,
	); err != nil {
		return decision.LivenessDecisionPolicy{}, err
	}
	policyDecision, _, _, _, err := living.deriveDecisionLocked(
		ctx, scope.ActorID, *scope.SessionID, domain,
		input.UnsupportedPremises, input.ActionsWithoutGrowth,
	)
	return policyDecision, err
}

func (living *livingContext) deriveDecisionLocked(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	domain string,
	unsupportedPremises int,
	actionsWithoutGrowth int,
) (
	decision.LivenessDecisionPolicy,
	temporal.Signals,
	safety.EmotionalSnapshot,
	relationship.Snapshot,
	error,
) {
	tracker, err := living.temporalLocked(ctx, actorID, sessionID)
	if err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	emotional, err := living.emotionalLocked(ctx, actorID)
	if err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	if err := emotional.Decay(living.clock.Now().UTC()); err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	signals, err := tracker.Apply(emotional)
	if err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	relationshipSnapshot, exists := living.relationships.Snapshot(
		actorID.String(), domain,
	)
	if !exists {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, fmt.Errorf(
				"operator living context: relationship was not prepared",
			)
	}
	emotionalSnapshot := emotional.FullSnapshot()
	policyDecision, err := decision.Derive(decision.Inputs{
		Emotional: emotionalSnapshot, Temporal: signals,
		Relationship:         relationshipSnapshot,
		UnsupportedPremises:  unsupportedPremises,
		ActionsWithoutGrowth: actionsWithoutGrowth,
		SimplicityPreference: living.simplicityPreferenceLocked(ctx, actorID),
		PriorRepair:          living.hasRepairLocked(ctx, actorID, domain),
	})
	if err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	if err := living.saveTemporalLocked(ctx, actorID, sessionID, tracker); err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	if err := living.store.SaveEmotionalState(
		ctx, actorID.String(), emotional,
	); err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	raw, err := json.Marshal(policyDecision)
	if err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	key := temporalScope(actorID, sessionID)
	if err := living.store.SaveLivingState(
		ctx, decisionStateKind, key, raw,
	); err != nil {
		return decision.LivenessDecisionPolicy{}, temporal.Signals{},
			safety.EmotionalSnapshot{}, relationship.Snapshot{}, err
	}
	living.decisions[key] = policyDecision
	return policyDecision, signals, emotionalSnapshot, relationshipSnapshot, nil
}

func (living *livingContext) Projection(
	ctx context.Context,
	scope controlplane.Scope,
) (livingProjection, error) {
	if scope.ActorID == uuid.Nil {
		return livingProjection{}, fmt.Errorf(
			"operator living context: authenticated actor is required",
		)
	}
	living.mu.Lock()
	relationships := living.relationships.All(scope.ActorID.String())
	sort.Slice(relationships, func(left int, right int) bool {
		return relationships[left].Domain < relationships[right].Domain
	})
	emotional, err := living.emotionalLocked(ctx, scope.ActorID)
	if err != nil {
		living.mu.Unlock()
		return livingProjection{}, err
	}
	projection := livingProjection{
		Relationships: relationships, Emotional: emotional.FullSnapshot(),
		Safety: "relationship, temporal, and emotion never alter policy or approval",
	}
	if state, exists := living.actorIdentities[scope.ActorID.String()]; exists {
		projection.PreferredName = state.PreferredName
	}
	if scope.SessionID != nil {
		if err := living.ensureSessionOwnerLocked(
			ctx, scope.ActorID, *scope.SessionID, false,
		); err != nil {
			living.mu.Unlock()
			return livingProjection{}, err
		}
		tracker, trackerErr := living.temporalLocked(
			ctx, scope.ActorID, *scope.SessionID,
		)
		if trackerErr != nil {
			living.mu.Unlock()
			return livingProjection{}, trackerErr
		}
		state := tracker.State()
		signals := tracker.Snapshot()
		projection.Temporal = &state
		projection.Signals = &signals
		key := temporalScope(scope.ActorID, *scope.SessionID)
		if current, exists := living.decisions[key]; exists {
			copy := current
			projection.Decision = &copy
		} else if raw, loadErr := living.store.LoadLivingState(
			ctx, decisionStateKind, key,
		); loadErr == nil {
			var restored decision.LivenessDecisionPolicy
			if json.Unmarshal(raw, &restored) == nil && restored.Validate() == nil {
				living.decisions[key] = restored
				projection.Decision = &restored
			}
		}
		if profile := living.aestheticProfileLocked(ctx, scope.ActorID); profile != nil {
			copy := *profile
			projection.Aesthetic = &copy
		}
		if profile := living.repairProfileLocked(ctx, scope.ActorID); profile != nil {
			copy := *profile
			projection.Repair = &copy
		}
	}
	living.mu.Unlock()
	soul, err := living.soul.Current(ctx)
	if err != nil {
		return livingProjection{}, err
	}
	projection.SoulVersion = soul.Number
	projection.SoulHash = soul.Hash
	return projection, nil
}

func (living *livingContext) temporalLocked(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
) (*temporal.Tracker, error) {
	scope := temporalScope(actorID, sessionID)
	if tracker := living.temporals[scope]; tracker != nil {
		return tracker, nil
	}
	raw, err := living.store.LoadLivingState(ctx, temporalStateKind, scope)
	var tracker *temporal.Tracker
	if errors.Is(err, sql.ErrNoRows) {
		tracker, err = temporal.New(living.clock)
	} else if err == nil {
		var state temporal.State
		if decodeErr := json.Unmarshal(raw, &state); decodeErr != nil {
			return nil, fmt.Errorf(
				"operator living context: decode temporal state: %w", decodeErr,
			)
		}
		tracker, err = temporal.Restore(living.clock, state)
	}
	if err != nil {
		return nil, err
	}
	living.temporals[scope] = tracker
	return tracker, nil
}

func (living *livingContext) emotionalLocked(
	ctx context.Context,
	actorID uuid.UUID,
) (*safety.EmotionalState, error) {
	key := actorID.String()
	if emotional := living.emotional[key]; emotional != nil {
		return emotional, nil
	}
	emotional, err := living.store.LoadEmotionalState(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		emotional = safety.NewEmotionalState()
		err = nil
	}
	if err != nil {
		return nil, err
	}
	living.emotional[key] = emotional
	return emotional, nil
}

func (living *livingContext) aestheticLocked(
	ctx context.Context,
	actorID uuid.UUID,
) (*aesthetic.Model, *aestheticProfile, error) {
	key := actorID.String()
	if model := living.aesthetics[key]; model != nil {
		profile := living.aestheticProfiles[key]
		if profile.Version == 0 {
			return model, nil, nil
		}
		copy := profile
		return model, &copy, nil
	}
	model, err := aesthetic.New(living.memory, living.clock)
	if err != nil {
		return nil, nil, err
	}
	raw, err := living.store.LoadLivingState(ctx, aestheticStateKind, key)
	if err == nil {
		var profile aestheticProfile
		if json.Unmarshal(raw, &profile) != nil || profile.Version != 1 ||
			profile.ActorID != actorID || strings.TrimSpace(profile.Label) == "" ||
			profile.UpdatedAt.IsZero() {
			return nil, nil, fmt.Errorf("operator living context: invalid aesthetic profile")
		}
		if err := model.Restore(profile.Weights); err != nil {
			return nil, nil, err
		}
		living.aesthetics[key] = model
		living.aestheticProfiles[key] = profile
		copy := profile
		return model, &copy, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	living.aesthetics[key] = model
	return model, nil, nil
}

func (living *livingContext) saveAestheticLocked(
	ctx context.Context,
	profile aestheticProfile,
) error {
	raw, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	if err := living.store.SaveLivingState(
		ctx, aestheticStateKind, profile.ActorID.String(), raw,
	); err != nil {
		return err
	}
	living.aestheticProfiles[profile.ActorID.String()] = profile
	return nil
}

func (living *livingContext) aestheticProfileLocked(
	ctx context.Context,
	actorID uuid.UUID,
) *aestheticProfile {
	_, profile, err := living.aestheticLocked(ctx, actorID)
	if err != nil {
		return nil
	}
	return profile
}

func (living *livingContext) simplicityPreferenceLocked(
	ctx context.Context,
	actorID uuid.UUID,
) float64 {
	profile := living.aestheticProfileLocked(ctx, actorID)
	if profile == nil {
		return 0
	}
	return profile.Weights.Simplicity
}

func (living *livingContext) repairProfileLocked(
	ctx context.Context,
	actorID uuid.UUID,
) *repairProfile {
	key := actorID.String()
	if profile := living.repairProfiles[key]; profile.Version != 0 {
		copy := profile
		return &copy
	}
	raw, err := living.store.LoadLivingState(ctx, repairStateKind, key)
	if err != nil {
		return nil
	}
	var profile repairProfile
	if json.Unmarshal(raw, &profile) != nil || profile.Version != 1 ||
		profile.ActorID != actorID || profile.FailureID == uuid.Nil ||
		profile.LessonID == uuid.Nil || strings.TrimSpace(profile.Lesson) == "" ||
		profile.LearnedAt.IsZero() {
		return nil
	}
	living.repairProfiles[key] = profile
	copy := profile
	return &copy
}

func (living *livingContext) hasRepairLocked(
	ctx context.Context,
	actorID uuid.UUID,
	domain string,
) bool {
	profile := living.repairProfileLocked(ctx, actorID)
	return profile != nil && profile.Domain == domain
}

func (living *livingContext) saveRepairProfile(
	ctx context.Context,
	profile repairProfile,
) error {
	raw, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	living.mu.Lock()
	defer living.mu.Unlock()
	if err := living.store.SaveLivingState(
		ctx, repairStateKind, profile.ActorID.String(), raw,
	); err != nil {
		return err
	}
	living.repairProfiles[profile.ActorID.String()] = profile
	return nil
}

func (living *livingContext) recordVerifiedRepair(
	ctx context.Context,
	actorID uuid.UUID,
	domain string,
	response agent.Response,
	emitter controlplane.EventEmitter,
	sessionID uuid.UUID,
) error {
	var failedTool string
	var evidenceIDs []uuid.UUID
	for _, execution := range response.ToolEvents {
		if execution.Error != "" && failedTool == "" {
			failedTool = strings.TrimSpace(execution.Call.Name)
			continue
		}
		if failedTool == "" || execution.Event == nil {
			continue
		}
		event := execution.Event
		if event.MMRRootAtTime != [32]byte{} &&
			(event.Match == nil || *event.Match) {
			evidenceIDs = append(evidenceIDs, event.ID)
		}
	}
	if failedTool == "" {
		return nil
	}
	failureID, err := living.repair.RecordFailureFor(
		ctx, actorID.String(),
		fmt.Sprintf("%s operation failed during %s work", failedTool, domain),
	)
	if err != nil {
		return err
	}
	if len(evidenceIDs) == 0 {
		return nil
	}
	lesson := fmt.Sprintf(
		"Change approach after %s fails; accept recovery only after committed evidence verifies the new result.",
		failedTool,
	)
	lessonID, err := living.repair.RecordRepairFor(
		ctx, actorID.String(), failureID, lesson, evidenceIDs,
	)
	if err != nil {
		return err
	}
	profile := repairProfile{
		Version: 1, ActorID: actorID, Domain: domain, ToolName: failedTool,
		FailureID: failureID, LessonID: lessonID, Lesson: lesson,
		EvidenceCount: len(evidenceIDs), LearnedAt: living.clock.Now().UTC(),
	}
	if err := living.saveRepairProfile(ctx, profile); err != nil {
		return err
	}
	return emitLiving(
		ctx, emitter, controlplane.EventRepairLearned,
		controlplane.Correlation{ActorID: actorID, SessionID: &sessionID},
		map[string]any{
			"domain": domain, "tool": failedTool,
			"evidence_count": len(evidenceIDs),
			"effect":         "future same-strategy retries reduced",
		},
	)
}

func (living *livingContext) saveRelationshipLocked(
	ctx context.Context,
	userID string,
	domain string,
) error {
	state, ok := living.relationships.State(userID, domain)
	if !ok {
		return fmt.Errorf("operator living context: relationship state is missing")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return living.store.SaveLivingState(
		ctx, relationshipStateKind, relationshipScope(userID, domain), raw,
	)
}

func (living *livingContext) saveActorIdentityLocked(
	ctx context.Context,
	state actorIdentity,
) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return living.store.SaveLivingState(
		ctx, actorIdentityKind, state.ActorID.String(), raw,
	)
}

func (living *livingContext) saveTemporalLocked(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	tracker *temporal.Tracker,
) error {
	raw, err := json.Marshal(tracker.State())
	if err != nil {
		return err
	}
	return living.store.SaveLivingState(
		ctx, temporalStateKind, temporalScope(actorID, sessionID), raw,
	)
}

func (living *livingContext) ensureSessionOwnerLocked(
	ctx context.Context,
	actorID uuid.UUID,
	sessionID uuid.UUID,
	create bool,
) error {
	scope := sessionID.String()
	raw, err := living.store.LoadLivingState(ctx, livingSessionKind, scope)
	if errors.Is(err, sql.ErrNoRows) {
		if !create {
			return fmt.Errorf(
				"operator living context: session has no authenticated living-state owner",
			)
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"version": 1, "actor_id": actorID,
			"session_id": sessionID,
		})
		if marshalErr != nil {
			return marshalErr
		}
		return living.store.SaveLivingState(
			ctx, livingSessionKind, scope, payload,
		)
	}
	if err != nil {
		return err
	}
	owner, restoredSession, err := decodeLivingSession(scope, raw)
	if err != nil {
		return err
	}
	if restoredSession != sessionID {
		return fmt.Errorf("operator living context: invalid session owner state")
	}
	if owner != actorID {
		return fmt.Errorf(
			"operator living context: cross-actor session access denied",
		)
	}
	return nil
}

func (living *livingContext) sessionOwnerLocked(
	ctx context.Context,
	sessionID uuid.UUID,
) (uuid.UUID, error) {
	raw, err := living.store.LoadLivingState(
		ctx, livingSessionKind, sessionID.String(),
	)
	if err != nil {
		return uuid.Nil, err
	}
	owner, restoredSession, err := decodeLivingSession(sessionID.String(), raw)
	if err != nil {
		return uuid.Nil, err
	}
	if restoredSession != sessionID {
		return uuid.Nil, fmt.Errorf(
			"operator living context: invalid session owner state",
		)
	}
	return owner, nil
}

func decodeLivingSession(
	scope string,
	raw json.RawMessage,
) (uuid.UUID, uuid.UUID, error) {
	var state struct {
		Version   int       `json:"version"`
		ActorID   uuid.UUID `json:"actor_id"`
		SessionID uuid.UUID `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &state); err != nil ||
		state.Version != 1 || state.ActorID == uuid.Nil ||
		state.SessionID == uuid.Nil || scope != state.SessionID.String() {
		return uuid.Nil, uuid.Nil, fmt.Errorf(
			"operator living context: invalid session owner state",
		)
	}
	return state.ActorID, state.SessionID, nil
}

func relationshipScope(userID string, domain string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(domain)
}

func temporalScope(actorID uuid.UUID, sessionID uuid.UUID) string {
	return actorID.String() + "\x00" + sessionID.String()
}

func relationshipDomain(content string) string {
	normalized := strings.ToLower(content)
	domains := []struct {
		name    string
		markers []string
	}{
		{"software", []string{"code", "compile", "test", "bug", "repository", "file", "api"}},
		{"operations", []string{"deploy", "server", "production", "incident", "dashboard"}},
		{"security", []string{"security", "policy", "approval", "vulnerability", "credential"}},
		{"research", []string{"research", "paper", "study", "analyze", "evidence"}},
	}
	for _, domain := range domains {
		for _, marker := range domain.markers {
			if strings.Contains(normalized, marker) {
				return domain.name
			}
		}
	}
	return "general"
}

func declaredExpertise(content string) (relationship.Expertise, bool) {
	normalized := strings.ToLower(content)
	for _, marker := range []string{
		"i am a beginner", "i'm a beginner", "new to this", "explain from scratch",
	} {
		if strings.Contains(normalized, marker) {
			return relationship.Beginner, true
		}
	}
	for _, marker := range []string{
		"i am an expert", "i'm an expert", "assume expert", "i work professionally",
	} {
		if strings.Contains(normalized, marker) {
			return relationship.Expert, true
		}
	}
	return relationship.Intermediate, false
}

func declaredPreference(content string) (string, bool) {
	normalized := strings.ToLower(content)
	for _, marker := range []string{"keep it concise", "be concise", "brief answers"} {
		if strings.Contains(normalized, marker) {
			return "concise", true
		}
	}
	for _, marker := range []string{
		"explain in detail", "show the steps", "use examples",
	} {
		if strings.Contains(normalized, marker) {
			return "detailed", true
		}
	}
	return "", false
}

func declaredAesthetic(content string) (string, aesthetic.Criteria, bool) {
	normalized := strings.ToLower(content)
	for _, marker := range []string{
		"i prefer simple", "i prefer simplicity", "keep solutions simple",
		"prefer low operational burden", "minimal operational burden",
	} {
		if strings.Contains(normalized, marker) {
			return "simple, coherent, low-burden solutions", aesthetic.Criteria{
				Clarity: .25, Simplicity: .45, Consistency: .2, Craft: .1,
			}, true
		}
	}
	for _, marker := range []string{
		"i prefer clarity", "prioritize clarity", "make clarity the priority",
	} {
		if strings.Contains(normalized, marker) {
			return "clarity-first solutions", aesthetic.Criteria{
				Clarity: .5, Simplicity: .2, Consistency: .2, Craft: .1,
			}, true
		}
	}
	return "", aesthetic.Criteria{}, false
}

func safeFailureCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_' ||
			character == '-' {
			builder.WriteRune(character)
		}
		if builder.Len() == 64 {
			break
		}
	}
	if builder.Len() == 0 {
		return "unclassified"
	}
	return builder.String()
}

func explicitDeadline(content string) (time.Time, bool) {
	normalized := strings.ToLower(content)
	index := strings.Index(normalized, "deadline:")
	if index < 0 {
		return time.Time{}, false
	}
	value := strings.TrimSpace(content[index+len("deadline:"):])
	if space := strings.IndexAny(value, "\r\n"); space >= 0 {
		value = strings.TrimSpace(value[:space])
	}
	deadline, err := time.Parse(time.RFC3339, value)
	return deadline.UTC(), err == nil
}

func requiresLivingTask(content string) bool {
	normalized := strings.ToLower(strings.TrimSpace(content))
	normalized = strings.Trim(normalized, " \t\r\n.!?,;:")
	switch normalized {
	case "hi", "hello", "hey", "hello there", "hi there", "hey there",
		"good morning", "good afternoon", "good evening",
		"how are you", "how are you doing", "what's up", "whats up",
		"thanks", "thank you", "ok", "okay", "got it":
		return false
	default:
		return true
	}
}

func appendContextSection(builder *strings.Builder, title string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	builder.WriteString("\n### ")
	builder.WriteString(title)
	builder.WriteByte('\n')
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func deadlineDescription(signals temporal.Signals) string {
	if !signals.HasDeadline {
		return "none declared"
	}
	return signals.DeadlineProximity.String()
}

func countUnsupportedPremises(rendered string) int {
	normalized := strings.ToLower(rendered)
	count := strings.Count(normalized, "status=assumption") +
		strings.Count(normalized, `"status":"assumption"`)
	if count > 3 {
		return 3
	}
	return count
}

func emptyFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func emitLiving(
	ctx context.Context,
	emitter controlplane.EventEmitter,
	eventType controlplane.EventType,
	correlation controlplane.Correlation,
	value any,
) error {
	if emitter == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = emitter.Emit(ctx, eventType, correlation, raw)
	return err
}

func (capabilities *productionCapabilities) LivingState(
	ctx context.Context,
	scope controlplane.Scope,
) (livingProjection, error) {
	if capabilities == nil || capabilities.living == nil {
		return livingProjection{}, fmt.Errorf(
			"operator living context: production service is unavailable",
		)
	}
	projection, err := capabilities.living.Projection(ctx, scope)
	if err != nil {
		return livingProjection{}, err
	}
	if capabilities.presence != nil {
		presence := capabilities.presence.Projection(scope.ActorID)
		projection.Presence = &presence
	}
	return projection, nil
}

func (capabilities *productionCapabilities) RelationshipCommand(
	ctx context.Context,
	request controlplane.Request,
) (json.RawMessage, error) {
	if capabilities == nil || capabilities.living == nil {
		return nil, fmt.Errorf(
			"operator relationship: production service is unavailable",
		)
	}
	if request.Scope.ActorID == uuid.Nil {
		return nil, controlplane.ErrUnauthorized
	}
	var payload struct {
		Action string                    `json:"action"`
		Domain string                    `json:"domain"`
		Patch  relationship.ProfilePatch `json:"patch"`
		Fields []string                  `json:"fields"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(request.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "relationship profile command is invalid",
		}
	}
	payload.Action = strings.ToLower(strings.TrimSpace(payload.Action))
	payload.Domain = strings.ToLower(strings.TrimSpace(payload.Domain))
	if payload.Domain == "" || len(payload.Domain) > 128 {
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "a bounded relationship domain is required",
		}
	}
	living := capabilities.living
	var result any
	eventType := controlplane.EventRelationshipChanged
	event := map[string]any{
		"action": payload.Action, "domain": payload.Domain,
		"source": "explicit user control",
	}
	if payload.Action == "propose_soul_v2" {
		living.mu.Lock()
		snapshot, exists := living.relationships.Snapshot(
			request.Scope.ActorID.String(), payload.Domain,
		)
		living.mu.Unlock()
		if !exists {
			return nil, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "relationship profile does not exist",
			}
		}
		proposal, err := living.soul.Propose(
			ctx,
			request.Scope.ActorID,
			soulV2Candidate(snapshot),
		)
		if err != nil {
			return nil, err
		}
		result = proposal
		eventType = controlplane.EventSoulChanged
		event = map[string]any{
			"action": "proposed", "proposal_id": proposal.ID,
			"base_version":   proposal.BaseVersion,
			"candidate_hash": proposal.CandidateHash,
			"source":         "reviewed relationship profile",
		}
	} else {
		living.mu.Lock()
		if _, exists := living.relationships.Snapshot(
			request.Scope.ActorID.String(), payload.Domain,
		); !exists {
			if _, err := living.relationships.Prepare(
				request.Scope.ActorID.String(), payload.Domain,
				relationship.Intermediate, "", false, false,
			); err != nil {
				living.mu.Unlock()
				return nil, err
			}
		}
		var (
			snapshot relationship.Snapshot
			err      error
		)
		switch payload.Action {
		case "correct":
			if profilePatchEmpty(payload.Patch) {
				err = fmt.Errorf("relationship profile correction is empty")
			} else {
				snapshot, err = living.relationships.CorrectProfile(
					request.Scope.ActorID.String(), payload.Domain,
					payload.Patch,
				)
			}
		case "pin":
			snapshot, err = living.relationships.PinProfileFields(
				request.Scope.ActorID.String(), payload.Domain,
				payload.Fields, true,
			)
		case "unpin":
			snapshot, err = living.relationships.PinProfileFields(
				request.Scope.ActorID.String(), payload.Domain,
				payload.Fields, false,
			)
		case "remove":
			snapshot, err = living.relationships.RemoveProfileFields(
				request.Scope.ActorID.String(), payload.Domain,
				payload.Fields,
			)
		default:
			err = fmt.Errorf(
				"relationship profile action must be correct, pin, unpin, remove, or propose_soul_v2",
			)
		}
		if err == nil {
			err = living.saveRelationshipLocked(
				ctx, request.Scope.ActorID.String(), payload.Domain,
			)
		}
		living.mu.Unlock()
		if err != nil {
			return nil, controlplane.PublicError{
				Code: controlplane.ErrorInvalid, Message: err.Error(),
			}
		}
		result = snapshot
		event["pinned_fields"] = snapshot.Profile.PinnedFields
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := emitLiving(
		ctx, living.emitter, eventType,
		controlplane.Correlation{
			ActorID: request.Scope.ActorID, SessionID: request.Scope.SessionID,
		},
		event,
	); err != nil {
		return nil, err
	}
	return raw, nil
}

func (capabilities *productionCapabilities) SoulState(
	ctx context.Context,
	actorID uuid.UUID,
) (identity.Projection, error) {
	if capabilities == nil || capabilities.living == nil ||
		capabilities.living.soul == nil {
		return identity.Projection{}, fmt.Errorf(
			"operator identity: production service is unavailable",
		)
	}
	return capabilities.living.soul.Projection(ctx, actorID)
}

func profilePatchEmpty(patch relationship.ProfilePatch) bool {
	return patch.ResponseLength == nil &&
		patch.Directness == nil &&
		patch.ConclusionFirst == nil &&
		patch.DomainExpertise == nil &&
		patch.PreferredTools == nil &&
		patch.RiskTolerance == nil &&
		patch.ProactiveSuggestions == nil &&
		patch.NotificationCadence == nil &&
		patch.Dislikes == nil &&
		patch.Constraints == nil &&
		patch.ProjectPrinciples == nil
}

func soulV2Candidate(snapshot relationship.Snapshot) string {
	var builder strings.Builder
	builder.WriteString("# Ion SOUL v2\n\n")
	builder.WriteString("Be precise, candid, evidence-led, and accountable for uncertainty.\n\n")
	builder.WriteString("## Durable operating principles\n\n")
	principles := snapshot.Profile.ProjectPrinciples
	if len(principles) == 0 {
		principles = []string{
			"Preserve user work and intent.",
			"Prefer verified outcomes over plausible completion claims.",
			"Keep consequential action under explicit user authority.",
		}
	}
	for _, principle := range principles {
		builder.WriteString("- ")
		builder.WriteString(principle)
		builder.WriteByte('\n')
	}
	builder.WriteString("\n## Relationship and safety boundary\n\n")
	builder.WriteString("- Apply explicit communication and workflow preferences when they are relevant.\n")
	builder.WriteString("- Never use trust, urgency, emotion, aesthetics, or risk tolerance to weaken classification, approval, authorization, evidence, or verification.\n")
	builder.WriteString("- Keep preferences inspectable, correctable, removable, and subordinate to the user's current instruction.\n")
	if len(snapshot.Profile.Constraints) > 0 {
		builder.WriteString("\n## Explicit working constraints\n\n")
		for _, constraint := range snapshot.Profile.Constraints {
			builder.WriteString("- ")
			builder.WriteString(constraint)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func (capabilities *productionCapabilities) SoulCommand(
	ctx context.Context,
	request controlplane.Request,
) (json.RawMessage, error) {
	if capabilities == nil || capabilities.living == nil ||
		capabilities.living.soul == nil {
		return nil, fmt.Errorf("operator identity: production service is unavailable")
	}
	var payload struct {
		Action     string    `json:"action"`
		Candidate  string    `json:"candidate"`
		ProposalID uuid.UUID `json:"proposal_id"`
		Version    uint64    `json:"version"`
		Confirm    bool      `json:"confirm"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(request.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "SOUL command is invalid",
		}
	}
	var (
		result any
		event  map[string]any
	)
	switch strings.TrimSpace(payload.Action) {
	case "propose":
		proposal, err := capabilities.living.soul.Propose(
			ctx, request.Scope.ActorID, payload.Candidate,
		)
		if err != nil {
			return nil, err
		}
		result = proposal
		event = map[string]any{
			"action": "proposed", "proposal_id": proposal.ID,
			"base_version":   proposal.BaseVersion,
			"candidate_hash": proposal.CandidateHash,
		}
	case "approve":
		if !payload.Confirm {
			return nil, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "explicit SOUL approval confirmation is required",
			}
		}
		if err := capabilities.auditSoulApproval(
			ctx, request, "soul.approve", payload.ProposalID.String(),
		); err != nil {
			return nil, err
		}
		version, err := capabilities.living.soul.Resolve(
			ctx, request.Scope.ActorID, payload.ProposalID, true,
		)
		if err != nil {
			return nil, err
		}
		result = version
		event = map[string]any{
			"action": "approved", "proposal_id": payload.ProposalID,
			"version": version.Number, "hash": version.Hash,
			"classification": "RED",
		}
	case "deny":
		version, err := capabilities.living.soul.Resolve(
			ctx, request.Scope.ActorID, payload.ProposalID, false,
		)
		if err != nil {
			return nil, err
		}
		result = map[string]any{
			"status": "denied", "current_version": version.Number,
			"current_hash": version.Hash,
		}
		event = map[string]any{
			"action": "denied", "proposal_id": payload.ProposalID,
		}
	case "rollback":
		if !payload.Confirm {
			return nil, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "explicit SOUL rollback confirmation is required",
			}
		}
		if err := capabilities.auditSoulApproval(
			ctx, request, "soul.rollback", fmt.Sprintf("%d", payload.Version),
		); err != nil {
			return nil, err
		}
		version, err := capabilities.living.soul.Rollback(
			ctx, request.Scope.ActorID, payload.Version,
		)
		if err != nil {
			return nil, err
		}
		result = version
		event = map[string]any{
			"action": "rollback", "target_version": payload.Version,
			"version": version.Number, "hash": version.Hash,
			"classification": "RED",
		}
	default:
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorInvalid,
			Message: "SOUL action must be propose, approve, deny, or rollback",
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := emitLiving(
		ctx, capabilities.living.emitter, controlplane.EventSoulChanged,
		controlplane.Correlation{
			ActorID:   request.Scope.ActorID,
			SessionID: request.Scope.SessionID,
		},
		event,
	); err != nil {
		return nil, err
	}
	return raw, nil
}

func (capabilities *productionCapabilities) auditSoulApproval(
	ctx context.Context,
	request controlplane.Request,
	action string,
	target string,
) error {
	if capabilities.auditor == nil {
		return fmt.Errorf("operator identity: policy auditor is unavailable")
	}
	arguments, err := json.Marshal(map[string]string{"target": target})
	if err != nil {
		return err
	}
	return capabilities.auditor.RecordPolicyEvent(ctx, policy.AuditEvent{
		At: capabilities.clock.Now(), Layer: policy.SandboxLayer,
		Decision:   policy.Allow,
		Reason:     "explicit authenticated confirmation for consequential identity change",
		ToolCallID: request.RequestID.String(), ToolName: action,
		Arguments: arguments, Classification: tools.ClassificationRed,
		Sender: policy.SenderUser, Profile: request.Scope.Profile,
	})
}
