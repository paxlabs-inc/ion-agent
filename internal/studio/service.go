package studio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	stateKind       = "software_intent_v1"
	maxIntents      = 64
	maxProposals    = 128
	maxCorrelations = 4096
	maxInspectFiles = 5000
	maxInspectBytes = int64(32 << 20)
	maxTextLength   = 32 << 10
)

type actorState struct {
	Revision uint64   `json:"revision"`
	Intents  []Intent `json:"intents"`
}

type Service struct {
	mu       sync.Mutex
	store    *session.Store
	clock    types.Clock
	projects *projectcontrol.Service
	work     *workcontrol.Service
}

func NewService(store *session.Store, clock types.Clock, projects *projectcontrol.Service,
	work *workcontrol.Service) (*Service, error) {
	if store == nil || clock == nil || projects == nil || work == nil {
		return nil, fmt.Errorf("studio: encrypted store, clock, projects, and work are required")
	}
	return &Service{store: store, clock: clock, projects: projects, work: work}, nil
}

func (service *Service) Compile(ctx context.Context, actor uuid.UUID, input CompileInput) (Intent, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if actor == uuid.Nil || input.ProjectID == uuid.Nil || input.OutcomeContractID == uuid.Nil ||
		input.WorkspaceRevision == 0 || invalidText(input.Goal) {
		return Intent{}, ErrInvalid
	}
	project, err := service.projects.Get(ctx, actor, input.ProjectID)
	if err != nil {
		return Intent{}, fmt.Errorf("%w: project binding", ErrInvalid)
	}
	if project.WorkspaceRevision != input.WorkspaceRevision {
		return Intent{}, ErrStaleRevision
	}
	portfolio, err := service.work.Get(ctx, actor)
	if err != nil {
		return Intent{}, err
	}
	foundContract := false
	for _, contract := range portfolio.Contracts {
		if contract.ID == input.OutcomeContractID {
			foundContract = true
			break
		}
	}
	if !foundContract {
		return Intent{}, fmt.Errorf("%w: outcome contract binding", ErrInvalid)
	}
	assumptions, err := validateAssumptions(input.Assumptions)
	if err != nil {
		return Intent{}, err
	}
	inspection, err := inspectRepository(project.Root, project.WorkspaceRevision, service.clock.Now().UTC())
	if err != nil {
		return Intent{}, err
	}
	mapped := trimUnique(input.MappedRequirements)
	if len(mapped) == 0 && input.Delta == nil {
		return Intent{}, fmt.Errorf("%w: map existing requirements or propose a spec delta", ErrInvalid)
	}
	now := service.clock.Now().UTC()
	intent := Intent{ID: uuid.New(), Version: StateVersion, ProjectID: project.ID,
		OutcomeContractID: input.OutcomeContractID, BaselineRevision: project.WorkspaceRevision,
		Goal: strings.TrimSpace(input.Goal), MappedRequirements: mapped, Assumptions: assumptions,
		Inspection: inspection, Proposals: []Proposal{}, Correlations: []Correlation{}, CreatedAt: now, UpdatedAt: now}
	if input.Delta != nil {
		proposal, proposalErr := newProposal(1, input.Rationale, input.DependencyImpact, *input.Delta, nil, now)
		if proposalErr != nil {
			return Intent{}, proposalErr
		}
		intent.Proposals = append(intent.Proposals, proposal)
		intent.ActiveProposalID = &intent.Proposals[0].ID
	}
	state, err := service.load(ctx, actor)
	if err != nil {
		return Intent{}, err
	}
	if len(state.Intents) >= maxIntents {
		return Intent{}, fmt.Errorf("studio: intent limit reached")
	}
	state.Intents = append(state.Intents, intent)
	if err := service.save(ctx, actor, &state); err != nil {
		return Intent{}, err
	}
	return intent, nil
}

func (service *Service) ProposeScopeChange(ctx context.Context, actor uuid.UUID,
	input ScopeChangeInput) (Proposal, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return Proposal{}, err
	}
	intent, index := findIntent(state.Intents, input.IntentID)
	if index < 0 {
		return Proposal{}, ErrNotFound
	}
	if len(intent.Proposals) >= maxProposals {
		return Proposal{}, fmt.Errorf("studio: proposal limit reached")
	}
	var supersedes *uuid.UUID
	if intent.ActiveProposalID != nil {
		for proposalIndex := range intent.Proposals {
			if intent.Proposals[proposalIndex].ID == *intent.ActiveProposalID &&
				intent.Proposals[proposalIndex].Status == ProposalProposed {
				intent.Proposals[proposalIndex].Status = ProposalSuperseded
				now := service.clock.Now().UTC()
				intent.Proposals[proposalIndex].DecidedAt = &now
				supersededID := intent.Proposals[proposalIndex].ID
				supersedes = &supersededID
			}
		}
	}
	proposal, err := newProposal(uint64(len(intent.Proposals)+1), input.Rationale,
		input.DependencyImpact, input.Delta, supersedes, service.clock.Now().UTC())
	if err != nil {
		return Proposal{}, err
	}
	intent.Proposals = append(intent.Proposals, proposal)
	intent.ActiveProposalID = &intent.Proposals[len(intent.Proposals)-1].ID
	intent.UpdatedAt = service.clock.Now().UTC()
	state.Intents[index] = intent
	if err := service.save(ctx, actor, &state); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func (service *Service) DecideProposal(ctx context.Context, actor, intentID, proposalID uuid.UUID,
	accept bool, reason string) (Proposal, error) {
	return service.DecideProposalWithDecisions(ctx, actor, intentID, proposalID, accept, reason, nil)
}

func (service *Service) DecideProposalWithDecisions(ctx context.Context, actor, intentID, proposalID uuid.UUID,
	accept bool, reason string, decisions map[string]string) (Proposal, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if invalidText(reason) {
		return Proposal{}, fmt.Errorf("%w: decision reason is required", ErrInvalid)
	}
	state, err := service.load(ctx, actor)
	if err != nil {
		return Proposal{}, err
	}
	intent, intentIndex := findIntent(state.Intents, intentID)
	if intentIndex < 0 {
		return Proposal{}, ErrNotFound
	}
	proposalIndex := -1
	for index := range intent.Proposals {
		if intent.Proposals[index].ID == proposalID {
			proposalIndex = index
			break
		}
	}
	if proposalIndex < 0 {
		return Proposal{}, ErrNotFound
	}
	proposal := intent.Proposals[proposalIndex]
	if proposal.Status != ProposalProposed {
		return Proposal{}, ErrConflict
	}
	if accept {
		for index := range intent.Assumptions {
			assumption := &intent.Assumptions[index]
			if assumption.Material && assumption.Resolution == "" {
				resolution := strings.TrimSpace(decisions[assumption.ID])
				if invalidText(resolution) {
					return Proposal{}, fmt.Errorf("%w: %s", ErrDecision, assumption.DecisionNeed)
				}
				now := service.clock.Now().UTC()
				assumption.Resolution, assumption.ResolvedAt = resolution, &now
			}
		}
		proposal.Status = ProposalAccepted
	} else {
		proposal.Status = ProposalRejected
	}
	now := service.clock.Now().UTC()
	proposal.DecidedAt, proposal.DecisionReason = &now, strings.TrimSpace(reason)
	intent.Proposals[proposalIndex] = proposal
	intent.ActiveProposalID = nil
	intent.UpdatedAt = now
	state.Intents[intentIndex] = intent
	if err := service.save(ctx, actor, &state); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func (service *Service) Get(ctx context.Context, actor, intentID uuid.UUID) (Intent, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return Intent{}, err
	}
	intent, index := findIntent(state.Intents, intentID)
	if index < 0 {
		return Intent{}, ErrNotFound
	}
	return intent, nil
}

func (service *Service) List(ctx context.Context, actor uuid.UUID) ([]Intent, uint64, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return nil, 0, err
	}
	intents := append([]Intent{}, state.Intents...)
	sort.Slice(intents, func(i, j int) bool { return intents[i].UpdatedAt.After(intents[j].UpdatedAt) })
	return intents, state.Revision, nil
}

// ApplyProposal appends the accepted proposal to the authoritative KVX file,
// then deterministically refreshes Studio-owned regions of generated views.
// Repeating it repairs an interrupted projection write without duplicating the
// authoritative change.
func (service *Service) ApplyProposal(ctx context.Context, actor, intentID, proposalID uuid.UUID) (Intent, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return Intent{}, err
	}
	intent, intentIndex := findIntent(state.Intents, intentID)
	if intentIndex < 0 {
		return Intent{}, ErrNotFound
	}
	proposalIndex := findProposal(intent.Proposals, proposalID)
	if proposalIndex < 0 {
		return Intent{}, ErrNotFound
	}
	proposal := intent.Proposals[proposalIndex]
	if proposal.Status != ProposalAccepted {
		return Intent{}, fmt.Errorf("%w: only an accepted proposal may change the specification", ErrDecision)
	}
	project, err := service.projects.Get(ctx, actor, intent.ProjectID)
	if err != nil {
		return Intent{}, err
	}
	if project.WorkspaceRevision != intent.BaselineRevision {
		return Intent{}, ErrStaleRevision
	}
	current, err := inspectRepository(project.Root, project.WorkspaceRevision, service.clock.Now().UTC())
	if err != nil {
		return Intent{}, err
	}
	markerPresent, err := authoritativeMarkerPresent(project.Root, proposal.ID)
	if err != nil {
		return Intent{}, err
	}
	if proposal.AppliedAt == nil && !markerPresent &&
		(current.AuthoritativeSpecHash != intent.Inspection.AuthoritativeSpecHash ||
			current.ImplementationHash != intent.Inspection.ImplementationHash) {
		return Intent{}, fmt.Errorf("%w: repository changed after proposal inspection", ErrConflict)
	}
	if err := applyAuthoritativeChange(project.Root, intent, proposal); err != nil {
		return Intent{}, err
	}
	inspection, err := inspectRepository(project.Root, project.WorkspaceRevision, service.clock.Now().UTC())
	if err != nil {
		return Intent{}, err
	}
	now := service.clock.Now().UTC()
	proposal.AppliedAt = &now
	proposal.SpecHash = inspection.AuthoritativeSpecHash
	proposal.GeneratedHash = inspection.GeneratedTasksHash
	intent.Proposals[proposalIndex] = proposal
	intent.Inspection = inspection
	intent.UpdatedAt = now
	state.Intents[intentIndex] = intent
	if err := service.save(ctx, actor, &state); err != nil {
		return Intent{}, err
	}
	return intent, nil
}

func (service *Service) RecordCorrelation(ctx context.Context, actor uuid.UUID,
	input CorrelationInput) (Correlation, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if input.IntentID == uuid.Nil || invalidText(input.Reference) || invalidText(input.Description) ||
		!validCorrelationKind(input.Kind) {
		return Correlation{}, ErrInvalid
	}
	state, err := service.load(ctx, actor)
	if err != nil {
		return Correlation{}, err
	}
	intent, intentIndex := findIntent(state.Intents, input.IntentID)
	if intentIndex < 0 {
		return Correlation{}, ErrNotFound
	}
	if len(intent.Correlations) >= maxCorrelations {
		return Correlation{}, fmt.Errorf("studio: correlation limit reached")
	}
	known := acceptedCriteria(intent)
	criteria := trimUnique(input.Criteria)
	if len(criteria) == 0 {
		return Correlation{}, fmt.Errorf("%w: criterion correlation is required", ErrInvalid)
	}
	for _, criterion := range criteria {
		if _, ok := known[criterion]; !ok {
			return Correlation{}, fmt.Errorf("%w: unknown criterion %q", ErrInvalid, criterion)
		}
	}
	if input.Kind == CorrelationArtifact {
		artifactID, parseErr := uuid.Parse(strings.TrimSpace(input.Reference))
		if parseErr != nil || !service.verifiedArtifact(ctx, actor, intent, artifactID, criteria) {
			return Correlation{}, fmt.Errorf("%w: artifact must be server-verified for the same outcome and criteria", ErrInvalid)
		}
	}
	project, err := service.projects.Get(ctx, actor, intent.ProjectID)
	if err != nil {
		return Correlation{}, err
	}
	inspection, err := inspectRepository(project.Root, project.WorkspaceRevision, service.clock.Now().UTC())
	if err != nil {
		return Correlation{}, err
	}
	correlation := Correlation{ID: uuid.New(), Kind: input.Kind,
		Reference: strings.TrimSpace(input.Reference), Criteria: criteria,
		Description: strings.TrimSpace(input.Description), WorkspaceRevision: project.WorkspaceRevision,
		SpecHash: inspection.AuthoritativeSpecHash, GeneratedHash: inspection.GeneratedTasksHash,
		ImplementationHash: inspection.ImplementationHash, CreatedAt: service.clock.Now().UTC()}
	intent.Correlations = append(intent.Correlations, correlation)
	intent.UpdatedAt = correlation.CreatedAt
	state.Intents[intentIndex] = intent
	if err := service.save(ctx, actor, &state); err != nil {
		return Correlation{}, err
	}
	return correlation, nil
}

func (service *Service) Completion(ctx context.Context, actor, intentID uuid.UUID) (Completion, error) {
	intent, err := service.Get(ctx, actor, intentID)
	if err != nil {
		return Completion{}, err
	}
	result := Completion{BlockingReasons: []string{}, UncoveredCriteria: []string{},
		UnverifiedCriteria: []string{}, MissingCorrelationKind: []CorrelationKind{},
		CorrelationsByKind: make(map[CorrelationKind]int)}
	known := acceptedCriteria(intent)
	covered := make(map[string]struct{})
	for _, correlation := range intent.Correlations {
		result.CorrelationsByKind[correlation.Kind]++
		for _, criterion := range correlation.Criteria {
			covered[criterion] = struct{}{}
		}
	}
	for criterion := range known {
		if _, ok := covered[criterion]; !ok {
			result.UncoveredCriteria = append(result.UncoveredCriteria, criterion)
		}
	}
	portfolio, err := service.work.Get(ctx, actor)
	if err != nil {
		return Completion{}, err
	}
	verified := make(map[string]struct{})
	for _, artifact := range portfolio.Artifacts {
		if artifact.ContractID != intent.OutcomeContractID || artifact.VerifiedAt == nil ||
			artifact.SHA256 == "" || artifact.Verification != "server_sha256" {
			continue
		}
		for _, criterion := range artifact.CriteriaCovered {
			verified[criterion] = struct{}{}
		}
	}
	for criterion := range known {
		if _, ok := verified[criterion]; !ok {
			result.UnverifiedCriteria = append(result.UnverifiedCriteria, criterion)
		}
	}
	for _, kind := range []CorrelationKind{CorrelationTask, CorrelationPatch, CorrelationTool,
		CorrelationReview, CorrelationVerification, CorrelationArtifact} {
		if result.CorrelationsByKind[kind] == 0 {
			result.MissingCorrelationKind = append(result.MissingCorrelationKind, kind)
		}
	}
	result.Drift, err = service.completionDrift(ctx, actor, intent)
	if err != nil {
		return Completion{}, err
	}
	if len(known) == 0 {
		result.BlockingReasons = append(result.BlockingReasons, "no accepted applied proposal defines completion criteria")
	}
	if len(result.UncoveredCriteria) > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "one or more criteria have no implementation correlation")
	}
	if len(result.UnverifiedCriteria) > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "one or more criteria lack server-verified artifact evidence")
	}
	if len(result.MissingCorrelationKind) > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "required work stages are not correlated")
	}
	if result.Drift.CompletionBlocked {
		result.BlockingReasons = append(result.BlockingReasons, "specification, generated view, implementation, or revision drift requires reconciliation")
	}
	sort.Strings(result.UncoveredCriteria)
	sort.Strings(result.UnverifiedCriteria)
	result.Ready = len(result.BlockingReasons) == 0
	return result, nil
}

func (service *Service) DetectDrift(ctx context.Context, actor, intentID uuid.UUID) (Drift, error) {
	intent, err := service.Get(ctx, actor, intentID)
	if err != nil {
		return Drift{}, err
	}
	project, err := service.projects.Get(ctx, actor, intent.ProjectID)
	if err != nil {
		return Drift{}, err
	}
	current, err := inspectRepository(project.Root, project.WorkspaceRevision, service.clock.Now().UTC())
	if err != nil {
		return Drift{}, err
	}
	expected := intent.Inspection
	for index := len(intent.Correlations) - 1; index >= 0; index-- {
		correlation := intent.Correlations[index]
		if correlation.Kind == CorrelationPatch || correlation.Kind == CorrelationArtifact {
			expected.WorkspaceRevision = correlation.WorkspaceRevision
			expected.AuthoritativeSpecHash = correlation.SpecHash
			expected.GeneratedTasksHash = correlation.GeneratedHash
			expected.ImplementationHash = correlation.ImplementationHash
			break
		}
	}
	drift := Drift{
		AuthoritativeSpecChanged: current.AuthoritativeSpecHash != expected.AuthoritativeSpecHash,
		GeneratedViewChanged:     current.GeneratedTasksHash != expected.GeneratedTasksHash,
		ImplementationChanged:    current.ImplementationHash != expected.ImplementationHash,
	}
	drift.CompletionBlocked = drift.AuthoritativeSpecChanged || drift.GeneratedViewChanged ||
		drift.ImplementationChanged || current.WorkspaceRevision != expected.WorkspaceRevision
	return drift, nil
}

func (service *Service) completionDrift(ctx context.Context, actor uuid.UUID, intent Intent) (Drift, error) {
	project, err := service.projects.Get(ctx, actor, intent.ProjectID)
	if err != nil {
		return Drift{}, err
	}
	current, err := inspectRepository(project.Root, project.WorkspaceRevision, service.clock.Now().UTC())
	if err != nil {
		return Drift{}, err
	}
	expected := intent.Inspection
	for index := len(intent.Correlations) - 1; index >= 0; index-- {
		if intent.Correlations[index].Kind == CorrelationArtifact {
			correlation := intent.Correlations[index]
			expected.WorkspaceRevision = correlation.WorkspaceRevision
			expected.ImplementationHash = correlation.ImplementationHash
			break
		}
	}
	for index := len(intent.Proposals) - 1; index >= 0; index-- {
		proposal := intent.Proposals[index]
		if proposal.Status == ProposalAccepted && proposal.AppliedAt != nil {
			expected.AuthoritativeSpecHash = proposal.SpecHash
			expected.GeneratedTasksHash = proposal.GeneratedHash
			break
		}
	}
	drift := Drift{AuthoritativeSpecChanged: current.AuthoritativeSpecHash != expected.AuthoritativeSpecHash,
		GeneratedViewChanged:  current.GeneratedTasksHash != expected.GeneratedTasksHash,
		ImplementationChanged: current.ImplementationHash != expected.ImplementationHash}
	drift.CompletionBlocked = drift.AuthoritativeSpecChanged || drift.GeneratedViewChanged ||
		drift.ImplementationChanged || current.WorkspaceRevision != expected.WorkspaceRevision
	return drift, nil
}

func (service *Service) load(ctx context.Context, actor uuid.UUID) (actorState, error) {
	if actor == uuid.Nil {
		return actorState{}, ErrInvalid
	}
	raw, err := service.store.LoadLivingState(ctx, stateKind, actor.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return actorState{Intents: []Intent{}}, nil
		}
		return actorState{}, err
	}
	var state actorState
	if err := json.Unmarshal(raw, &state); err != nil {
		return actorState{}, fmt.Errorf("studio: decode state: %w", err)
	}
	return state, nil
}

func (service *Service) save(ctx context.Context, actor uuid.UUID, state *actorState) error {
	state.Revision++
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, stateKind, actor.String(), raw)
}

func newProposal(version uint64, rationale string, impact []string, delta SpecDelta,
	supersedes *uuid.UUID, now time.Time) (Proposal, error) {
	if invalidText(rationale) {
		return Proposal{}, fmt.Errorf("%w: proposal rationale is required", ErrInvalid)
	}
	if err := validateDelta(delta); err != nil {
		return Proposal{}, err
	}
	return Proposal{ID: uuid.New(), Version: version, Status: ProposalProposed,
		Rationale: strings.TrimSpace(rationale), DependencyImpact: trimUnique(impact),
		Delta: normalizeDelta(delta), Supersedes: supersedes, CreatedAt: now}, nil
}

func validateDelta(delta SpecDelta) error {
	if len(trimUnique(delta.UserVisibleBehavior)) == 0 || len(delta.Criteria) == 0 ||
		len(trimUnique(delta.SecurityBoundaries)) == 0 || len(trimUnique(delta.DataBoundaries)) == 0 ||
		len(trimUnique(delta.Verification)) == 0 || len(delta.Tasks) == 0 {
		return fmt.Errorf("%w: spec delta requires behavior, criteria, boundaries, verification, and tasks", ErrInvalid)
	}
	criteria := make(map[string]struct{}, len(delta.Criteria))
	for _, criterion := range delta.Criteria {
		if invalidIdentifier(criterion.ID) || invalidText(criterion.Description) {
			return fmt.Errorf("%w: invalid acceptance criterion", ErrInvalid)
		}
		if _, duplicate := criteria[criterion.ID]; duplicate {
			return fmt.Errorf("%w: duplicate acceptance criterion", ErrInvalid)
		}
		criteria[criterion.ID] = struct{}{}
	}
	tasks := make(map[string]struct{}, len(delta.Tasks))
	covered := make(map[string]struct{}, len(criteria))
	for _, task := range delta.Tasks {
		if invalidIdentifier(task.ID) || invalidText(task.Title) || len(task.Criteria) == 0 {
			return fmt.Errorf("%w: invalid dependency task", ErrInvalid)
		}
		if _, duplicate := tasks[task.ID]; duplicate {
			return fmt.Errorf("%w: duplicate dependency task", ErrInvalid)
		}
		tasks[task.ID] = struct{}{}
		for _, criterion := range trimUnique(task.Criteria) {
			if _, exists := criteria[criterion]; !exists {
				return fmt.Errorf("%w: task references unknown criterion", ErrInvalid)
			}
			covered[criterion] = struct{}{}
		}
	}
	for _, task := range delta.Tasks {
		for _, dependency := range trimUnique(task.DependsOn) {
			if dependency == task.ID {
				return fmt.Errorf("%w: task cannot depend on itself", ErrInvalid)
			}
			if _, exists := tasks[dependency]; !exists {
				return fmt.Errorf("%w: task references unknown dependency", ErrInvalid)
			}
		}
	}
	if len(covered) != len(criteria) {
		return fmt.Errorf("%w: every criterion must be assigned to a task", ErrInvalid)
	}
	return nil
}

// ValidateSpecDelta applies the same complete crosswalk validation used by
// Compile and ProposeScopeChange without mutating Studio state.
func ValidateSpecDelta(delta SpecDelta) error {
	return validateDelta(delta)
}

func normalizeDelta(delta SpecDelta) SpecDelta {
	delta.UserVisibleBehavior = trimUnique(delta.UserVisibleBehavior)
	delta.NonGoals = trimUnique(delta.NonGoals)
	delta.Constraints = trimUnique(delta.Constraints)
	delta.Risks = trimUnique(delta.Risks)
	delta.SecurityBoundaries = trimUnique(delta.SecurityBoundaries)
	delta.DataBoundaries = trimUnique(delta.DataBoundaries)
	delta.Migration = trimUnique(delta.Migration)
	delta.Rollback = trimUnique(delta.Rollback)
	delta.Verification = trimUnique(delta.Verification)
	for index := range delta.Criteria {
		delta.Criteria[index].ID = strings.TrimSpace(delta.Criteria[index].ID)
		delta.Criteria[index].Description = strings.TrimSpace(delta.Criteria[index].Description)
	}
	for index := range delta.Tasks {
		delta.Tasks[index].ID = strings.TrimSpace(delta.Tasks[index].ID)
		delta.Tasks[index].Title = strings.TrimSpace(delta.Tasks[index].Title)
		delta.Tasks[index].Criteria = trimUnique(delta.Tasks[index].Criteria)
		delta.Tasks[index].DependsOn = trimUnique(delta.Tasks[index].DependsOn)
	}
	return delta
}

func validateAssumptions(input []Assumption) ([]Assumption, error) {
	result := make([]Assumption, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, assumption := range input {
		assumption.ID = strings.TrimSpace(assumption.ID)
		assumption.Statement = strings.TrimSpace(assumption.Statement)
		assumption.Consequence = strings.TrimSpace(assumption.Consequence)
		assumption.DecisionNeed = strings.TrimSpace(assumption.DecisionNeed)
		if invalidIdentifier(assumption.ID) || invalidText(assumption.Statement) {
			return nil, fmt.Errorf("%w: invalid assumption", ErrInvalid)
		}
		if assumption.Material && assumption.DecisionNeed == "" {
			return nil, fmt.Errorf("%w: material assumption requires a decision", ErrInvalid)
		}
		if !assumption.Material && !assumption.Reversible {
			return nil, fmt.Errorf("%w: autonomous assumptions must be reversible", ErrInvalid)
		}
		if _, duplicate := seen[assumption.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate assumption", ErrInvalid)
		}
		seen[assumption.ID] = struct{}{}
		result = append(result, assumption)
	}
	return result, nil
}

func inspectRepository(root string, revision uint64, now time.Time) (RepositoryInspection, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return RepositoryInspection{}, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return RepositoryInspection{}, fmt.Errorf("studio: project root is unavailable")
	}
	result := RepositoryInspection{Root: root, InspectedAt: now, WorkspaceRevision: revision,
		InstructionFiles: []string{}}
	implementation := sha256.New()
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if protectedDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || protectedFile(entry.Name()) {
			return nil
		}
		if result.FilesInspected >= maxInspectFiles || result.BytesInspected >= maxInspectBytes {
			result.Truncated = true
			return nil
		}
		fileInfo, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if fileInfo.Size() > maxInspectBytes-result.BytesInspected {
			result.Truncated = true
			return nil
		}
		digest, digestErr := digestFile(path)
		if digestErr != nil {
			return digestErr
		}
		relative = filepath.ToSlash(relative)
		result.FilesInspected++
		result.BytesInspected += fileInfo.Size()
		switch relative {
		case "spec/spec.kvx":
			result.AuthoritativeSpecPath, result.AuthoritativeSpecHash = relative, digest
		case "spec/tasks.md":
			result.GeneratedTasksPath, result.GeneratedTasksHash = relative, digest
		case "spec/requirements.md", "spec/design.md":
		default:
			_, _ = io.WriteString(implementation, relative+"\x00"+digest+"\n")
		}
		if entry.Name() == "AGENTS.md" {
			result.InstructionFiles = append(result.InstructionFiles, relative)
		}
		return nil
	})
	if err != nil {
		return RepositoryInspection{}, fmt.Errorf("studio: inspect repository: %w", err)
	}
	sort.Strings(result.InstructionFiles)
	result.ImplementationHash = hex.EncodeToString(implementation.Sum(nil))
	return result, nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func protectedDirectory(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "target", "dist", ".cache":
		return true
	default:
		return false
	}
}

func protectedFile(name string) bool {
	lower := strings.ToLower(name)
	return lower == ".env" || strings.HasPrefix(lower, ".env.") ||
		strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") ||
		strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx")
}

func findIntent(intents []Intent, id uuid.UUID) (Intent, int) {
	for index := range intents {
		if intents[index].ID == id {
			return intents[index], index
		}
	}
	return Intent{}, -1
}

func findProposal(proposals []Proposal, id uuid.UUID) int {
	for index := range proposals {
		if proposals[index].ID == id {
			return index
		}
	}
	return -1
}

func validCorrelationKind(kind CorrelationKind) bool {
	switch kind {
	case CorrelationTask, CorrelationPatch, CorrelationTool, CorrelationReview,
		CorrelationVerification, CorrelationArtifact:
		return true
	default:
		return false
	}
}

func acceptedCriteria(intent Intent) map[string]struct{} {
	result := make(map[string]struct{})
	for _, proposal := range intent.Proposals {
		if proposal.Status != ProposalAccepted || proposal.AppliedAt == nil {
			continue
		}
		for _, criterion := range proposal.Delta.Criteria {
			result[criterion.ID] = struct{}{}
		}
	}
	return result
}

func (service *Service) verifiedArtifact(ctx context.Context, actor uuid.UUID, intent Intent,
	artifactID uuid.UUID, criteria []string) bool {
	portfolio, err := service.work.Get(ctx, actor)
	if err != nil {
		return false
	}
	wanted := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		wanted[criterion] = struct{}{}
	}
	for _, artifact := range portfolio.Artifacts {
		if artifact.ID != artifactID || artifact.ContractID != intent.OutcomeContractID ||
			artifact.VerifiedAt == nil || artifact.SHA256 == "" || artifact.Verification != "server_sha256" {
			continue
		}
		covered := make(map[string]struct{}, len(artifact.CriteriaCovered))
		for _, criterion := range artifact.CriteriaCovered {
			covered[criterion] = struct{}{}
		}
		for criterion := range wanted {
			if _, ok := covered[criterion]; !ok {
				return false
			}
		}
		return true
	}
	return false
}

func applyAuthoritativeChange(root string, intent Intent, proposal Proposal) error {
	specDirectory := filepath.Join(root, "spec")
	if info, err := os.Lstat(specDirectory); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("studio: specification directory must not be a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(specDirectory, 0o700); err != nil {
		return fmt.Errorf("studio: create spec directory: %w", err)
	}
	for _, name := range []string{"spec.kvx", "requirements.md", "tasks.md"} {
		path := filepath.Join(specDirectory, name)
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("studio: specification path must not be a symlink")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	specPath := filepath.Join(specDirectory, "spec.kvx")
	spec, err := os.ReadFile(specPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	marker := "# BEGIN ION STUDIO CHANGE " + proposal.ID.String()
	if !strings.Contains(string(spec), marker) {
		if len(spec) == 0 {
			spec = []byte("# spec.kvx — authoritative project specification\n")
		}
		if spec[len(spec)-1] != '\n' {
			spec = append(spec, '\n')
		}
		spec = append(spec, renderKVXChange(intent, proposal)...)
	}
	if err := writeAtomic(specPath, spec, 0o600); err != nil {
		return err
	}
	for _, projection := range []struct {
		name, heading string
	}{
		{name: "requirements.md", heading: "# Requirements\n"},
		{name: "tasks.md", heading: "# Tasks\n"},
	} {
		path := filepath.Join(specDirectory, projection.name)
		current, readErr := os.ReadFile(path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		generated := renderProjection(current, projection.heading, intent, proposal, projection.name)
		if err := writeAtomic(path, generated, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func authoritativeMarkerPresent(root string, proposalID uuid.UUID) (bool, error) {
	content, err := os.ReadFile(filepath.Join(root, "spec", "spec.kvx"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Contains(content, []byte("# BEGIN ION STUDIO CHANGE "+proposalID.String())), nil
}

func renderKVXChange(intent Intent, proposal Proposal) []byte {
	var builder strings.Builder
	builder.WriteString("\n# BEGIN ION STUDIO CHANGE " + proposal.ID.String() + "\n")
	builder.WriteString("[studio_change." + proposal.ID.String() + "]\n")
	writeKVXString(&builder, "intent_id", intent.ID.String())
	writeKVXString(&builder, "outcome_contract_id", intent.OutcomeContractID.String())
	writeKVXString(&builder, "status", string(proposal.Status))
	writeKVXString(&builder, "goal", intent.Goal)
	writeKVXString(&builder, "rationale", proposal.Rationale)
	writeKVXList(&builder, "behavior", proposal.Delta.UserVisibleBehavior)
	writeKVXList(&builder, "non_goals", proposal.Delta.NonGoals)
	writeKVXList(&builder, "constraints", proposal.Delta.Constraints)
	writeKVXList(&builder, "risks", proposal.Delta.Risks)
	writeKVXList(&builder, "security_boundaries", proposal.Delta.SecurityBoundaries)
	writeKVXList(&builder, "data_boundaries", proposal.Delta.DataBoundaries)
	writeKVXList(&builder, "migration", proposal.Delta.Migration)
	writeKVXList(&builder, "rollback", proposal.Delta.Rollback)
	writeKVXList(&builder, "verification", proposal.Delta.Verification)
	for index, criterion := range proposal.Delta.Criteria {
		writeKVXString(&builder, fmt.Sprintf("criterion_%d", index+1), criterion.ID+" | "+criterion.Description)
	}
	for index, task := range proposal.Delta.Tasks {
		encoded, _ := json.Marshal(task)
		writeKVXString(&builder, fmt.Sprintf("task_%d", index+1), string(encoded))
	}
	builder.WriteString("# END ION STUDIO CHANGE " + proposal.ID.String() + "\n")
	return []byte(builder.String())
}

func writeKVXString(builder *strings.Builder, key, value string) {
	encoded, _ := json.Marshal(value)
	fmt.Fprintf(builder, "%s = %s\n", key, encoded)
}

func writeKVXList(builder *strings.Builder, key string, values []string) {
	encoded, _ := json.Marshal(values)
	fmt.Fprintf(builder, "%s = %s\n", key, encoded)
}

func renderProjection(current []byte, heading string, intent Intent, proposal Proposal, name string) []byte {
	begin := "<!-- BEGIN ION STUDIO CHANGE " + proposal.ID.String() + " -->"
	end := "<!-- END ION STUDIO CHANGE " + proposal.ID.String() + " -->"
	text := string(current)
	if text == "" {
		text = "<!-- GENERATED from spec/spec.kvx — DO NOT EDIT -->\n\n" + heading
	}
	if start := strings.Index(text, begin); start >= 0 {
		if finish := strings.Index(text[start:], end); finish >= 0 {
			finish += start + len(end)
			text = strings.TrimRight(text[:start], "\n") + text[finish:]
		}
	}
	var builder strings.Builder
	builder.WriteString(strings.TrimRight(text, "\n"))
	builder.WriteString("\n\n" + begin + "\n")
	if name == "requirements.md" {
		builder.WriteString("## Accepted Software Intent: " + intent.Goal + "\n\n")
		builder.WriteString("**Rationale:** " + proposal.Rationale + "\n\n")
		for _, criterion := range proposal.Delta.Criteria {
			builder.WriteString("- **" + criterion.ID + ":** " + criterion.Description + "\n")
		}
	} else {
		builder.WriteString("## Accepted Software Work: " + intent.Goal + "\n\n")
		for _, task := range proposal.Delta.Tasks {
			builder.WriteString("- [ ] " + task.ID + " " + task.Title + "\n")
			builder.WriteString("  - Criteria: " + strings.Join(task.Criteria, ", ") + "\n")
			if len(task.DependsOn) > 0 {
				builder.WriteString("  - Depends on: " + strings.Join(task.DependsOn, ", ") + "\n")
			}
		}
	}
	builder.WriteString(end + "\n")
	return []byte(builder.String())
}

func writeAtomic(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".ion-spec-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func trimUnique(input []string) []string {
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, value := range input {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxTextLength {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func invalidText(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || len(value) > maxTextLength || strings.ContainsRune(value, '\x00')
}

func invalidIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n\t ")
}
