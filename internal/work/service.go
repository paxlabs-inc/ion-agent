// Package work owns the durable, evidence-bound definition of delegated work.
// It deliberately separates a model's progress narrative from the facts that
// are allowed to satisfy an outcome contract.
package work

import (
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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	portfolioKind        = "work_portfolio_v1"
	maxContracts         = 64
	maxArtifacts         = 256
	maxWorkflowRuns      = 64
	maxWorkItems         = 2048
	maxCriteria          = 32
	maxTextBytes         = 4096
	maxArtifactBytes     = 32 << 20
	defaultToolCallLimit = 20
)

// Status is the closed lifecycle for an outcome contract.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusActive    Status = "active"
	StatusBlocked   Status = "blocked"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
)

// AutonomyMode controls whether initiative may execute. Suggest is the safe
// default: Ion can prepare work but cannot run it unattended.
type AutonomyMode string

const (
	AutonomyOff      AutonomyMode = "off"
	AutonomySuggest  AutonomyMode = "suggest"
	AutonomyApproved AutonomyMode = "approved"
)

// Criterion is one independently provable completion condition.
type Criterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// WorkItemStatus is the durable execution lifecycle for one criterion-linked
// unit. A model turn may stop or be replaced without changing this state.
type WorkItemStatus string

const (
	WorkItemPending   WorkItemStatus = "pending"
	WorkItemReady     WorkItemStatus = "ready"
	WorkItemRunning   WorkItemStatus = "running"
	WorkItemVerifying WorkItemStatus = "verifying"
	WorkItemBlocked   WorkItemStatus = "blocked"
	WorkItemCompleted WorkItemStatus = "completed"
)

// WorkItem is a durable dependency-aware unit owned by an outcome contract.
type WorkItem struct {
	ID           string         `json:"id"`
	ContractID   uuid.UUID      `json:"contract_id"`
	Title        string         `json:"title"`
	Criteria     []string       `json:"criteria"`
	DependsOn    []string       `json:"depends_on,omitempty"`
	Status       WorkItemStatus `json:"status"`
	Attempts     int            `json:"attempts"`
	BlockingNote string         `json:"blocking_note,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type WorkItemInput struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Criteria  []string `json:"criteria"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type WorkItemUpdate struct {
	ContractID uuid.UUID      `json:"contract_id"`
	ItemID     string         `json:"item_id"`
	Status     WorkItemStatus `json:"status"`
	Note       string         `json:"note,omitempty"`
}

// OutcomeContract is the user's durable definition of success.
type OutcomeContract struct {
	ID                   uuid.UUID   `json:"id"`
	SessionID            *uuid.UUID  `json:"session_id,omitempty"`
	Goal                 string      `json:"goal"`
	Deliverable          string      `json:"deliverable"`
	DoneCriteria         []Criterion `json:"done_criteria"`
	VerificationRequired []string    `json:"verification_required"`
	NextAction           string      `json:"next_action"`
	Status               Status      `json:"status"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`
	CompletedAt          *time.Time  `json:"completed_at,omitempty"`
}

// Artifact is a workspace-confined deliverable. Verified fields are written
// only by VerifyArtifact after the server reads and hashes the content.
type Artifact struct {
	ID               uuid.UUID  `json:"id"`
	ContractID       uuid.UUID  `json:"contract_id"`
	Kind             string     `json:"kind"`
	Title            string     `json:"title"`
	Reference        string     `json:"reference"`
	WorkspaceRoot    string     `json:"workspace_root,omitempty"`
	CriteriaCovered  []string   `json:"criteria_covered"`
	SHA256           string     `json:"sha256,omitempty"`
	SizeBytes        int64      `json:"size_bytes,omitempty"`
	Verification     string     `json:"verification,omitempty"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	RecordedAt       time.Time  `json:"recorded_at"`
	VerificationNote string     `json:"verification_note,omitempty"`
}

// AutonomySettings are hard operator ceilings, not planning suggestions.
type AutonomySettings struct {
	Mode             AutonomyMode `json:"mode"`
	Paused           bool         `json:"paused"`
	MaxToolCalls     int          `json:"max_tool_calls"`
	MaxTokens        int          `json:"max_tokens"`
	MaxElapsedSecond int          `json:"max_elapsed_seconds"`
	MaxErrors        int          `json:"max_errors"`
	CooldownSecond   int          `json:"cooldown_seconds"`
	UpdatedAt        time.Time    `json:"updated_at"`
}

// Portfolio is the encrypted actor-scoped durable work state.
type Portfolio struct {
	Revision  uint64            `json:"revision"`
	Contracts []OutcomeContract `json:"contracts"`
	Artifacts []Artifact        `json:"artifacts"`
	Workflows []WorkflowRun     `json:"workflow_runs"`
	Autonomy  AutonomySettings  `json:"autonomy"`
	WorkItems []WorkItem        `json:"work_items,omitempty"`
}

// Brief is a derived projection; it is never persisted independently.
type Brief struct {
	Contract             *OutcomeContract `json:"contract,omitempty"`
	NextAction           string           `json:"next_action,omitempty"`
	VerifiedCriteria     []string         `json:"verified_criteria"`
	UnverifiedCriteria   []string         `json:"unverified_criteria"`
	Deliverables         []Artifact       `json:"deliverables"`
	Autonomy             AutonomySettings `json:"autonomy"`
	BlockingReason       string           `json:"blocking_reason,omitempty"`
	CompletionPercentage int              `json:"completion_percentage"`
	WorkItems            []WorkItem       `json:"work_items"`
}

// ContractInput is accepted from trusted application boundaries.
type ContractInput struct {
	ID                   uuid.UUID   `json:"id,omitempty"`
	SessionID            *uuid.UUID  `json:"session_id,omitempty"`
	Goal                 string      `json:"goal"`
	Deliverable          string      `json:"deliverable"`
	DoneCriteria         []Criterion `json:"done_criteria"`
	VerificationRequired []string    `json:"verification_required"`
	NextAction           string      `json:"next_action"`
	Status               Status      `json:"status,omitempty"`
}

// ArtifactInput intentionally has no verification fields. Clients cannot
// assert that their own output is verified.
type ArtifactInput struct {
	ID              uuid.UUID `json:"id,omitempty"`
	ContractID      uuid.UUID `json:"contract_id"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	Reference       string    `json:"reference"`
	CriteriaCovered []string  `json:"criteria_covered"`
}

// ReviewInput contains task facts used by the deterministic lens router.
type ReviewInput struct {
	Kinds                []string `json:"kinds"`
	Risk                 string   `json:"risk"`
	TouchesAuth          bool     `json:"touches_auth"`
	TouchesSecrets       bool     `json:"touches_secrets"`
	TouchesData          bool     `json:"touches_data"`
	TouchesMigration     bool     `json:"touches_migration"`
	UserFacing           bool     `json:"user_facing"`
	PerformanceSensitive bool     `json:"performance_sensitive"`
	LongRunning          bool     `json:"long_running"`
	ExternalSideEffect   bool     `json:"external_side_effect"`
	ReleaseCandidate     bool     `json:"release_candidate"`
}

// ReviewLens is a concrete question and evidence expectation.
type ReviewLens struct {
	ID               string   `json:"id"`
	Question         string   `json:"question"`
	RequiredEvidence []string `json:"required_evidence"`
}

// ReviewPlan is selected from task facts, not role-play personas.
type ReviewPlan struct {
	Lenses []ReviewLens `json:"lenses"`
}

// RecipeStage makes reusable workflows inspectable and resumable.
type RecipeStage struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Entry        []string `json:"entry_conditions"`
	ExitEvidence []string `json:"exit_evidence"`
	HumanGate    bool     `json:"human_gate"`
}

// Recipe is a curated workflow definition.
type Recipe struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Stages      []RecipeStage `json:"stages"`
}

// WorkflowStageResult is durable evidence that one recipe stage exited.
type WorkflowStageResult struct {
	StageID     string      `json:"stage_id"`
	ArtifactIDs []uuid.UUID `json:"artifact_ids"`
	Confirmed   bool        `json:"human_confirmed"`
	CompletedAt time.Time   `json:"completed_at"`
}

// WorkflowRun is an actor-scoped resumable cursor through a curated recipe.
type WorkflowRun struct {
	ID                uuid.UUID             `json:"id"`
	RecipeID          string                `json:"recipe_id"`
	ContractID        uuid.UUID             `json:"contract_id"`
	CurrentStageIndex int                   `json:"current_stage_index"`
	Status            string                `json:"status"`
	CompletedStages   []WorkflowStageResult `json:"completed_stages"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
}

// WorkflowAdvanceInput supplies only server-checkable artifact references and
// an explicit human confirmation bit for stages declared as human gates.
type WorkflowAdvanceInput struct {
	RunID       uuid.UUID   `json:"run_id"`
	StageID     string      `json:"stage_id"`
	ArtifactIDs []uuid.UUID `json:"artifact_ids"`
	Confirmed   bool        `json:"confirmed"`
}

// Service persists work state through the encrypted session store.
type Service struct {
	mu        sync.Mutex
	store     *session.Store
	clock     types.Clock
	workspace string
}

// NewService constructs a workspace-confined durable work service.
func NewService(store *session.Store, clock types.Clock, workspace string) (*Service, error) {
	if store == nil || clock == nil {
		return nil, fmt.Errorf("work: session store and clock are required")
	}
	abs, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("work: valid workspace is required")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("work: resolve workspace: %w", err)
	}
	return &Service{store: store, clock: clock, workspace: resolved}, nil
}

func defaultAutonomy(now time.Time) AutonomySettings {
	return AutonomySettings{Mode: AutonomySuggest, MaxToolCalls: defaultToolCallLimit,
		MaxTokens: 64000, MaxElapsedSecond: 1800, MaxErrors: 3,
		CooldownSecond: 300, UpdatedAt: now.UTC()}
}

func (service *Service) load(ctx context.Context, actor uuid.UUID) (Portfolio, error) {
	if actor == uuid.Nil {
		return Portfolio{}, fmt.Errorf("work: actor is required")
	}
	raw, err := service.store.LoadLivingState(ctx, portfolioKind, actor.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Portfolio{Autonomy: defaultAutonomy(service.clock.Now())}, nil
		}
		return Portfolio{}, err
	}
	var portfolio Portfolio
	if err := json.Unmarshal(raw, &portfolio); err != nil {
		return Portfolio{}, fmt.Errorf("work: decode portfolio: %w", err)
	}
	if portfolio.Autonomy.Mode == "" {
		portfolio.Autonomy = defaultAutonomy(service.clock.Now())
	}
	return portfolio, nil
}

func (service *Service) save(ctx context.Context, actor uuid.UUID, portfolio Portfolio) error {
	portfolio.Revision++
	raw, err := json.Marshal(portfolio)
	if err != nil {
		return fmt.Errorf("work: encode portfolio: %w", err)
	}
	return service.store.SaveLivingState(ctx, portfolioKind, actor.String(), raw)
}

// Get returns an isolated copy of one actor's portfolio.
func (service *Service) Get(ctx context.Context, actor uuid.UUID) (Portfolio, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.load(ctx, actor)
}

// PutContract validates and atomically creates or replaces a contract.
func (service *Service) PutContract(ctx context.Context, actor uuid.UUID, input ContractInput) (OutcomeContract, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := validateContractInput(input); err != nil {
		return OutcomeContract{}, err
	}
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return OutcomeContract{}, err
	}
	contract, err := putContract(&portfolio, input, service.clock.Now().UTC())
	if err != nil {
		return OutcomeContract{}, err
	}
	if err := service.save(ctx, actor, portfolio); err != nil {
		return OutcomeContract{}, err
	}
	return contract, nil
}

// PutContractWithWorkItems validates and writes the outcome contract and its
// complete criterion-to-work-item crosswalk in one encrypted portfolio update.
// A rejected plan therefore cannot leave behind a partial Work Brief.
func (service *Service) PutContractWithWorkItems(
	ctx context.Context,
	actor uuid.UUID,
	input ContractInput,
	items []WorkItemInput,
) (OutcomeContract, []WorkItem, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := validateContractInput(input); err != nil {
		return OutcomeContract{}, nil, err
	}
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return OutcomeContract{}, nil, err
	}
	contract, err := putContract(&portfolio, input, service.clock.Now().UTC())
	if err != nil {
		return OutcomeContract{}, nil, err
	}
	planned, err := syncWorkItems(&portfolio, contract, items, service.clock.Now().UTC())
	if err != nil {
		return OutcomeContract{}, nil, err
	}
	if err := service.save(ctx, actor, portfolio); err != nil {
		return OutcomeContract{}, nil, err
	}
	return contract, planned, nil
}

func putContract(portfolio *Portfolio, input ContractInput, now time.Time) (OutcomeContract, error) {
	contract := OutcomeContract{ID: input.ID, SessionID: input.SessionID,
		Goal: strings.TrimSpace(input.Goal), Deliverable: strings.TrimSpace(input.Deliverable),
		DoneCriteria: normalizeCriteria(input.DoneCriteria), VerificationRequired: trimUnique(input.VerificationRequired),
		NextAction: strings.TrimSpace(input.NextAction), Status: input.Status,
		CreatedAt: now, UpdatedAt: now}
	if contract.ID == uuid.Nil {
		contract.ID = uuid.New()
	}
	if contract.Status == "" {
		contract.Status = StatusActive
	}
	if contract.Status == StatusCompleted {
		return OutcomeContract{}, fmt.Errorf("work: completion must use evidence-bound completion")
	}
	found := false
	for index := range portfolio.Contracts {
		if portfolio.Contracts[index].ID != contract.ID {
			continue
		}
		if portfolio.Contracts[index].Status == StatusCompleted || portfolio.Contracts[index].Status == StatusCancelled {
			return OutcomeContract{}, fmt.Errorf("work: terminal contract cannot be replaced")
		}
		contract.CreatedAt = portfolio.Contracts[index].CreatedAt
		portfolio.Contracts[index] = contract
		found = true
		break
	}
	if !found {
		if len(portfolio.Contracts) >= maxContracts {
			return OutcomeContract{}, fmt.Errorf("work: contract limit reached")
		}
		portfolio.Contracts = append(portfolio.Contracts, contract)
	}
	portfolio.WorkItems = ensureCriterionWorkItems(
		portfolio.WorkItems, contract, now,
	)
	return contract, nil
}

// SyncWorkItems replaces one contract's planned execution units while
// preserving compatible progress. It is used by the Studio spec compiler.
func (service *Service) SyncWorkItems(
	ctx context.Context, actor, contractID uuid.UUID, input []WorkItemInput,
) ([]WorkItem, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return nil, err
	}
	contract, found := findContract(portfolio.Contracts, contractID)
	if !found || contract.Status == StatusCompleted || contract.Status == StatusCancelled {
		return nil, fmt.Errorf("work: active contract not found")
	}
	items, err := syncWorkItems(&portfolio, contract, input, service.clock.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := service.save(ctx, actor, portfolio); err != nil {
		return nil, err
	}
	return append([]WorkItem(nil), items...), nil
}

func syncWorkItems(
	portfolio *Portfolio,
	contract OutcomeContract,
	input []WorkItemInput,
	now time.Time,
) ([]WorkItem, error) {
	if len(input) == 0 || len(input) > maxWorkItems {
		return nil, fmt.Errorf("work: one or more bounded work items are required")
	}
	knownCriteria := make(map[string]struct{}, len(contract.DoneCriteria))
	for _, criterion := range contract.DoneCriteria {
		knownCriteria[criterion.ID] = struct{}{}
	}
	knownItems := make(map[string]struct{}, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item.ID)
		if id == "" || invalidText(item.Title) || len(item.Criteria) == 0 {
			return nil, fmt.Errorf("work: work item ID, title, and criteria are required")
		}
		if _, duplicate := knownItems[id]; duplicate {
			return nil, fmt.Errorf("work: duplicate work item ID")
		}
		knownItems[id] = struct{}{}
		for _, criterion := range trimUnique(item.Criteria) {
			if _, exists := knownCriteria[criterion]; !exists {
				return nil, fmt.Errorf("work: work item references unknown criterion %q", criterion)
			}
		}
	}
	for _, item := range input {
		for _, dependency := range trimUnique(item.DependsOn) {
			if dependency == strings.TrimSpace(item.ID) {
				return nil, fmt.Errorf("work: work item cannot depend on itself")
			}
			if _, exists := knownItems[dependency]; !exists {
				return nil, fmt.Errorf("work: work item references unknown dependency %q", dependency)
			}
		}
	}
	existing := make(map[string]WorkItem)
	retained := portfolio.WorkItems[:0]
	for _, item := range portfolio.WorkItems {
		if item.ContractID == contract.ID {
			existing[item.ID] = item
			continue
		}
		retained = append(retained, item)
	}
	items := make([]WorkItem, 0, len(input))
	for _, source := range input {
		item := WorkItem{ID: strings.TrimSpace(source.ID), ContractID: contract.ID,
			Title: strings.TrimSpace(source.Title), Criteria: trimUnique(source.Criteria),
			DependsOn: trimUnique(source.DependsOn), Status: WorkItemPending, UpdatedAt: now}
		if previous, ok := existing[item.ID]; ok &&
			slices.Equal(previous.Criteria, item.Criteria) &&
			slices.Equal(previous.DependsOn, item.DependsOn) {
			item.Status, item.Attempts = previous.Status, previous.Attempts
			if previous.Status != WorkItemCompleted {
				item.BlockingNote = previous.BlockingNote
			}
		}
		items = append(items, item)
	}
	refreshReadyWorkItems(items)
	portfolio.WorkItems = append(retained, items...)
	if len(portfolio.WorkItems) > maxWorkItems {
		return nil, fmt.Errorf("work: work item limit reached")
	}
	return items, nil
}

// RestorePortfolio compensates a failed cross-service Studio write only when
// no other work mutation has advanced the portfolio in the meantime.
func (service *Service) RestorePortfolio(
	ctx context.Context,
	actor uuid.UUID,
	expectedRevision uint64,
	snapshot Portfolio,
) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	current, err := service.load(ctx, actor)
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision || snapshot.Revision+1 != expectedRevision {
		return fmt.Errorf("work: portfolio changed before compensation")
	}
	return service.save(ctx, actor, snapshot)
}

// UpdateWorkItem advances one durable unit. Completion is accepted only after
// server-verified artifact coverage exists for every criterion on the item.
func (service *Service) UpdateWorkItem(
	ctx context.Context, actor uuid.UUID, input WorkItemUpdate,
) (WorkItem, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return WorkItem{}, err
	}
	index := -1
	for candidate := range portfolio.WorkItems {
		if portfolio.WorkItems[candidate].ContractID == input.ContractID &&
			portfolio.WorkItems[candidate].ID == strings.TrimSpace(input.ItemID) {
			index = candidate
			break
		}
	}
	if index < 0 {
		return WorkItem{}, fmt.Errorf("work: work item not found")
	}
	item := portfolio.WorkItems[index]
	if input.Status != WorkItemRunning && input.Status != WorkItemVerifying &&
		input.Status != WorkItemBlocked && input.Status != WorkItemCompleted {
		return WorkItem{}, fmt.Errorf("work: unsupported work item transition")
	}
	if item.Status == WorkItemCompleted {
		return item, nil
	}
	if input.Status == WorkItemRunning {
		if !dependenciesCompleted(portfolio.WorkItems, item) {
			return WorkItem{}, fmt.Errorf("work: dependencies are not complete")
		}
		item.Attempts++
	}
	if input.Status == WorkItemCompleted {
		covered := verifiedCoverage(portfolio.Artifacts, item.ContractID)
		for _, criterion := range item.Criteria {
			if _, ok := covered[criterion]; !ok {
				return WorkItem{}, fmt.Errorf("work: criterion %q lacks verified evidence", criterion)
			}
		}
	}
	item.Status = input.Status
	item.BlockingNote = ""
	if input.Status == WorkItemBlocked {
		item.BlockingNote = strings.TrimSpace(input.Note)
		if item.BlockingNote == "" {
			return WorkItem{}, fmt.Errorf("work: blocked item requires a reason")
		}
	}
	item.UpdatedAt = service.clock.Now().UTC()
	portfolio.WorkItems[index] = item
	refreshReadyWorkItemsForContract(portfolio.WorkItems, item.ContractID)
	if err := service.save(ctx, actor, portfolio); err != nil {
		return WorkItem{}, err
	}
	return item, nil
}

// RecordArtifact stores only an unverified artifact claim.
func (service *Service) RecordArtifact(ctx context.Context, actor uuid.UUID, input ArtifactInput) (Artifact, error) {
	return service.RecordArtifactInWorkspace(ctx, actor, input, service.workspace)
}

// RecordArtifactInWorkspace binds an unverified artifact claim to one
// authenticated workspace root while keeping its public reference relative.
func (service *Service) RecordArtifactInWorkspace(
	ctx context.Context,
	actor uuid.UUID,
	input ArtifactInput,
	workspace string,
) (Artifact, error) {
	root, err := resolveWorkspaceRoot(workspace)
	if err != nil {
		return Artifact{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if input.ContractID == uuid.Nil || strings.TrimSpace(input.Reference) == "" ||
		invalidText(input.Kind) || invalidText(input.Title) || len(input.CriteriaCovered) == 0 {
		return Artifact{}, fmt.Errorf("work: valid contract, artifact, reference, and criterion coverage are required")
	}
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return Artifact{}, err
	}
	contract, found := findContract(portfolio.Contracts, input.ContractID)
	if !found || contract.Status == StatusCompleted || contract.Status == StatusCancelled {
		return Artifact{}, fmt.Errorf("work: active contract not found")
	}
	coverage := trimUnique(input.CriteriaCovered)
	if !criteriaSubset(contract.DoneCriteria, coverage) {
		return Artifact{}, fmt.Errorf("work: artifact references an unknown completion criterion")
	}
	reference, err := cleanReference(input.Reference)
	if err != nil {
		return Artifact{}, err
	}
	artifact := Artifact{ID: input.ID, ContractID: input.ContractID,
		Kind: strings.TrimSpace(input.Kind), Title: strings.TrimSpace(input.Title),
		Reference: reference, WorkspaceRoot: root,
		CriteriaCovered: coverage, RecordedAt: service.clock.Now().UTC()}
	if artifact.ID == uuid.Nil {
		artifact.ID = uuid.New()
	}
	for _, existing := range portfolio.Artifacts {
		if existing.ID == artifact.ID {
			return Artifact{}, fmt.Errorf("work: artifact ID already exists")
		}
	}
	if len(portfolio.Artifacts) >= maxArtifacts {
		return Artifact{}, fmt.Errorf("work: artifact limit reached")
	}
	portfolio.Artifacts = append(portfolio.Artifacts, artifact)
	if err := service.save(ctx, actor, portfolio); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// VerifyArtifact reads a workspace-confined regular file and stores its digest.
func (service *Service) VerifyArtifact(ctx context.Context, actor, artifactID uuid.UUID) (Artifact, error) {
	return service.verifyArtifact(ctx, actor, artifactID, "", false)
}

// VerifyArtifactInWorkspace verifies only against the workspace selected by
// the authenticated caller. Legacy unbound artifacts acquire that binding
// after a successful verification.
func (service *Service) VerifyArtifactInWorkspace(
	ctx context.Context,
	actor, artifactID uuid.UUID,
	workspace string,
) (Artifact, error) {
	root, err := resolveWorkspaceRoot(workspace)
	if err != nil {
		return Artifact{}, err
	}
	return service.verifyArtifact(ctx, actor, artifactID, root, true)
}

func (service *Service) verifyArtifact(
	ctx context.Context,
	actor, artifactID uuid.UUID,
	root string,
	enforceWorkspace bool,
) (Artifact, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if artifactID == uuid.Nil {
		return Artifact{}, fmt.Errorf("work: artifact is required")
	}
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return Artifact{}, err
	}
	index := -1
	for i := range portfolio.Artifacts {
		if portfolio.Artifacts[i].ID == artifactID {
			index = i
			break
		}
	}
	if index < 0 {
		return Artifact{}, fmt.Errorf("work: artifact not found")
	}
	boundRoot := strings.TrimSpace(portfolio.Artifacts[index].WorkspaceRoot)
	if enforceWorkspace && boundRoot != "" && boundRoot != root {
		return Artifact{}, fmt.Errorf("work: artifact belongs to a different workspace")
	}
	if boundRoot != "" {
		root = boundRoot
	} else if root == "" {
		root = service.workspace
	}
	root, err = resolveWorkspaceRoot(root)
	if err != nil {
		return Artifact{}, err
	}
	digest, size, err := hashReference(ctx, root, portfolio.Artifacts[index].Reference)
	if err != nil {
		return Artifact{}, err
	}
	now := service.clock.Now().UTC()
	portfolio.Artifacts[index].WorkspaceRoot = root
	portfolio.Artifacts[index].SHA256 = digest
	portfolio.Artifacts[index].SizeBytes = size
	portfolio.Artifacts[index].Verification = "server_sha256"
	portfolio.Artifacts[index].VerifiedAt = &now
	portfolio.Artifacts[index].VerificationNote = "regular workspace file read and hashed by Ion"
	reconcileVerifiedWorkItems(portfolio.WorkItems, portfolio.Artifacts, portfolio.Artifacts[index].ContractID, now)
	if err := service.save(ctx, actor, portfolio); err != nil {
		return Artifact{}, err
	}
	return portfolio.Artifacts[index], nil
}

// CompleteContract closes a contract only when server-verified artifacts cover
// every criterion. This is the central anti-false-completion invariant.
func (service *Service) CompleteContract(ctx context.Context, actor, contractID uuid.UUID) (OutcomeContract, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return OutcomeContract{}, err
	}
	index := -1
	for i := range portfolio.Contracts {
		if portfolio.Contracts[i].ID == contractID {
			index = i
			break
		}
	}
	if index < 0 {
		return OutcomeContract{}, fmt.Errorf("work: contract not found")
	}
	contract := portfolio.Contracts[index]
	if contract.Status == StatusCompleted {
		return contract, nil
	}
	covered := verifiedCoverage(portfolio.Artifacts, contract.ID)
	for _, criterion := range contract.DoneCriteria {
		if _, ok := covered[criterion.ID]; !ok {
			return OutcomeContract{}, fmt.Errorf("work: completion criterion %q lacks verified evidence", criterion.ID)
		}
	}
	now := service.clock.Now().UTC()
	reconcileVerifiedWorkItems(portfolio.WorkItems, portfolio.Artifacts, contract.ID, now)
	contract.Status, contract.UpdatedAt, contract.CompletedAt = StatusCompleted, now, &now
	contract.NextAction = ""
	portfolio.Contracts[index] = contract
	if err := service.save(ctx, actor, portfolio); err != nil {
		return OutcomeContract{}, err
	}
	return contract, nil
}

// Brief derives the current task-first projection for an actor and session.
func (service *Service) Brief(ctx context.Context, actor uuid.UUID, sessionID *uuid.UUID) (Brief, error) {
	portfolio, err := service.Get(ctx, actor)
	if err != nil {
		return Brief{}, err
	}
	brief := Brief{Autonomy: portfolio.Autonomy, VerifiedCriteria: []string{},
		UnverifiedCriteria: []string{}, Deliverables: []Artifact{}, WorkItems: []WorkItem{}}
	var chosen *OutcomeContract
	for i := range portfolio.Contracts {
		candidate := &portfolio.Contracts[i]
		if candidate.Status == StatusCompleted || candidate.Status == StatusCancelled {
			continue
		}
		if sessionID != nil && (candidate.SessionID == nil || *candidate.SessionID != *sessionID) {
			continue
		}
		if chosen == nil || candidate.UpdatedAt.After(chosen.UpdatedAt) {
			chosen = candidate
		}
	}
	if chosen == nil && sessionID != nil {
		return brief, nil
	}
	if chosen == nil {
		brief.BlockingReason = "No active outcome contract"
		return brief, nil
	}
	contractCopy := *chosen
	brief.Contract, brief.NextAction = &contractCopy, chosen.NextAction
	coverage := verifiedCoverage(portfolio.Artifacts, chosen.ID)
	for _, criterion := range chosen.DoneCriteria {
		if _, ok := coverage[criterion.ID]; ok {
			brief.VerifiedCriteria = append(brief.VerifiedCriteria, criterion.ID)
		} else {
			brief.UnverifiedCriteria = append(brief.UnverifiedCriteria, criterion.ID)
		}
	}
	for _, artifact := range portfolio.Artifacts {
		if artifact.ContractID == chosen.ID {
			brief.Deliverables = append(brief.Deliverables, artifact)
		}
	}
	for _, item := range portfolio.WorkItems {
		if item.ContractID == chosen.ID {
			brief.WorkItems = append(brief.WorkItems, item)
		}
	}
	if len(chosen.DoneCriteria) > 0 {
		brief.CompletionPercentage = 100 * len(brief.VerifiedCriteria) / len(chosen.DoneCriteria)
	}
	if portfolio.Autonomy.Paused {
		brief.BlockingReason = "Autonomous work is paused"
	} else if portfolio.Autonomy.Mode != AutonomyApproved {
		brief.BlockingReason = "Autonomy is " + string(portfolio.Autonomy.Mode)
	} else if chosen.Status == StatusBlocked {
		brief.BlockingReason = "Outcome contract is blocked"
	}
	return brief, nil
}

// UpdateAutonomy atomically applies validated operator ceilings.
func (service *Service) UpdateAutonomy(ctx context.Context, actor uuid.UUID, settings AutonomySettings) (AutonomySettings, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if settings.Mode != AutonomyOff && settings.Mode != AutonomySuggest && settings.Mode != AutonomyApproved {
		return AutonomySettings{}, fmt.Errorf("work: autonomy mode is invalid")
	}
	if settings.MaxToolCalls < 1 || settings.MaxToolCalls > 32 ||
		settings.MaxTokens < 256 || settings.MaxTokens > 1_000_000 ||
		settings.MaxElapsedSecond < 1 || settings.MaxElapsedSecond > 86400 ||
		settings.MaxErrors < 1 || settings.MaxErrors > 20 ||
		settings.CooldownSecond < 0 || settings.CooldownSecond > 86400 {
		return AutonomySettings{}, fmt.Errorf("work: autonomy budget is outside safe bounds")
	}
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return AutonomySettings{}, err
	}
	settings.UpdatedAt = service.clock.Now().UTC()
	portfolio.Autonomy = settings
	if err := service.save(ctx, actor, portfolio); err != nil {
		return AutonomySettings{}, err
	}
	return settings, nil
}

// StartWorkflow binds a curated recipe to an active outcome contract.
func (service *Service) StartWorkflow(
	ctx context.Context,
	actor uuid.UUID,
	recipeID string,
	contractID uuid.UUID,
) (WorkflowRun, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	recipe, found := findRecipe(recipeID)
	if !found || len(recipe.Stages) == 0 || contractID == uuid.Nil {
		return WorkflowRun{}, fmt.Errorf("work: valid recipe and contract are required")
	}
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return WorkflowRun{}, err
	}
	contract, found := findContract(portfolio.Contracts, contractID)
	if !found || (contract.Status != StatusActive && contract.Status != StatusBlocked) {
		return WorkflowRun{}, fmt.Errorf("work: workflow requires an active outcome contract")
	}
	for _, existing := range portfolio.Workflows {
		if existing.RecipeID == recipe.ID && existing.ContractID == contractID && existing.Status == "active" {
			return existing, nil
		}
	}
	if len(portfolio.Workflows) >= maxWorkflowRuns {
		return WorkflowRun{}, fmt.Errorf("work: workflow run limit reached")
	}
	now := service.clock.Now().UTC()
	run := WorkflowRun{ID: uuid.New(), RecipeID: recipe.ID, ContractID: contractID,
		Status: "active", CompletedStages: []WorkflowStageResult{}, CreatedAt: now, UpdatedAt: now}
	portfolio.Workflows = append(portfolio.Workflows, run)
	if err := service.save(ctx, actor, portfolio); err != nil {
		return WorkflowRun{}, err
	}
	return run, nil
}

// AdvanceWorkflow proves the current stage exit and atomically advances its
// durable cursor. A human-gated stage cannot be crossed by artifact evidence
// alone, and non-contract stages require server-verified artifacts.
func (service *Service) AdvanceWorkflow(
	ctx context.Context,
	actor uuid.UUID,
	input WorkflowAdvanceInput,
) (WorkflowRun, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if input.RunID == uuid.Nil || strings.TrimSpace(input.StageID) == "" {
		return WorkflowRun{}, fmt.Errorf("work: workflow run and stage are required")
	}
	portfolio, err := service.load(ctx, actor)
	if err != nil {
		return WorkflowRun{}, err
	}
	index := -1
	for i := range portfolio.Workflows {
		if portfolio.Workflows[i].ID == input.RunID {
			index = i
			break
		}
	}
	if index < 0 || portfolio.Workflows[index].Status != "active" {
		return WorkflowRun{}, fmt.Errorf("work: active workflow run not found")
	}
	run := portfolio.Workflows[index]
	recipe, found := findRecipe(run.RecipeID)
	if !found || run.CurrentStageIndex < 0 || run.CurrentStageIndex >= len(recipe.Stages) {
		return WorkflowRun{}, fmt.Errorf("work: workflow cursor is invalid")
	}
	stage := recipe.Stages[run.CurrentStageIndex]
	if strings.TrimSpace(input.StageID) != stage.ID {
		return WorkflowRun{}, fmt.Errorf("work: only the current workflow stage can advance")
	}
	if stage.HumanGate && !input.Confirmed {
		return WorkflowRun{}, fmt.Errorf("work: workflow stage requires explicit human confirmation")
	}
	artifactIDs := uniqueUUIDs(input.ArtifactIDs)
	if run.CurrentStageIndex > 0 && len(artifactIDs) == 0 {
		return WorkflowRun{}, fmt.Errorf("work: workflow stage requires verified artifact evidence")
	}
	for _, artifactID := range artifactIDs {
		artifact, found := findArtifact(portfolio.Artifacts, artifactID)
		if !found || artifact.ContractID != run.ContractID || artifact.VerifiedAt == nil ||
			artifact.SHA256 == "" || artifact.Verification != "server_sha256" {
			return WorkflowRun{}, fmt.Errorf("work: workflow evidence is not a verified artifact for this contract")
		}
	}
	now := service.clock.Now().UTC()
	run.CompletedStages = append(run.CompletedStages, WorkflowStageResult{
		StageID: stage.ID, ArtifactIDs: artifactIDs, Confirmed: input.Confirmed, CompletedAt: now,
	})
	run.CurrentStageIndex++
	run.UpdatedAt = now
	if run.CurrentStageIndex == len(recipe.Stages) {
		run.Status = "completed"
	}
	portfolio.Workflows[index] = run
	if err := service.save(ctx, actor, portfolio); err != nil {
		return WorkflowRun{}, err
	}
	return run, nil
}

// Review deterministically selects rigorous lenses from task and risk facts.
func Review(input ReviewInput) ReviewPlan {
	selected := map[string]ReviewLens{}
	add := func(lens ReviewLens) { selected[lens.ID] = lens }
	add(ReviewLens{ID: "correctness", Question: "Does the result satisfy every stated criterion under realistic failure conditions?", RequiredEvidence: []string{"targeted tests", "acceptance result"}})
	add(ReviewLens{ID: "evidence", Question: "Is every completion claim traceable to current, independently checked evidence?", RequiredEvidence: []string{"artifact digest", "verification output"}})
	kinds := strings.ToLower(strings.Join(input.Kinds, " "))
	if input.TouchesAuth || input.TouchesSecrets || input.ExternalSideEffect || strings.Contains(kinds, "security") || strings.EqualFold(input.Risk, "high") {
		add(ReviewLens{ID: "security", Question: "Can untrusted input cross an authorization, secret, network, or side-effect boundary?", RequiredEvidence: []string{"denial-path test", "redaction or authorization evidence"}})
	}
	if input.UserFacing || strings.Contains(kinds, "ui") || strings.Contains(kinds, "tui") {
		add(ReviewLens{ID: "usability", Question: "Can the user understand the current outcome, next action, and consequence without internal jargon?", RequiredEvidence: []string{"user journey", "accessibility check"}})
		add(ReviewLens{ID: "accessibility", Question: "Can people use the changed experience with keyboard, assistive technology, reduced motion, and required contrast?", RequiredEvidence: []string{"automated accessibility result", "keyboard or assistive-technology journey"}})
	}
	if input.LongRunning || strings.Contains(kinds, "service") || strings.Contains(kinds, "migration") {
		add(ReviewLens{ID: "operability", Question: "Does the work survive restart, partial failure, retry, and observation in production?", RequiredEvidence: []string{"restart result", "health or recovery evidence"}})
	}
	if input.TouchesData || strings.Contains(kinds, "data") || strings.Contains(kinds, "database") {
		add(ReviewLens{ID: "data", Question: "Are data definitions, integrity constraints, isolation, retention, and recovery behavior correct?", RequiredEvidence: []string{"data-quality or integrity result", "isolation and recovery evidence"}})
	}
	if input.TouchesMigration || strings.Contains(kinds, "migration") || strings.Contains(kinds, "schema") {
		add(ReviewLens{ID: "migration", Question: "Can the change migrate and roll back without silent loss, partial state, or incompatible readers?", RequiredEvidence: []string{"migration dry run", "rollback or compatibility result"}})
	}
	if input.PerformanceSensitive || strings.Contains(kinds, "performance") || strings.Contains(kinds, "latency") {
		add(ReviewLens{ID: "performance", Question: "Does measured resource use and latency remain within explicit budgets at representative load?", RequiredEvidence: []string{"repeatable benchmark", "budget comparison"}})
	}
	if input.ReleaseCandidate || strings.Contains(kinds, "release") || strings.Contains(kinds, "deploy") {
		add(ReviewLens{ID: "release", Question: "Does the exact candidate have complete current gates, resolved findings, rollback evidence, and no false-green waiver?", RequiredEvidence: []string{"revision-bound full gate", "release and rollback evidence"}})
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	plan := ReviewPlan{Lenses: make([]ReviewLens, 0, len(ids))}
	for _, id := range ids {
		plan.Lenses = append(plan.Lenses, selected[id])
	}
	return plan
}

// Recipes returns a bounded built-in library. The definitions are data only;
// execution still traverses normal policy and outcome-contract boundaries.
func Recipes() []Recipe {
	return []Recipe{
		{ID: "production-change", Title: "Production-ready change", Description: "Implement and prove a bounded production change.", Stages: []RecipeStage{
			{ID: "contract", Title: "Agree on the outcome", Entry: []string{"authenticated request"}, ExitEvidence: []string{"active outcome contract"}},
			{ID: "implement", Title: "Build the smallest complete slice", Entry: []string{"active contract"}, ExitEvidence: []string{"recorded deliverables"}},
			{ID: "review", Title: "Run relevant review lenses", Entry: []string{"deliverables recorded"}, ExitEvidence: []string{"review findings resolved or explicit"}},
			{ID: "verify", Title: "Verify completion evidence", Entry: []string{"review complete"}, ExitEvidence: []string{"server-verified artifacts", "acceptance results"}},
			{ID: "release", Title: "Apply consequential release", Entry: []string{"all criteria covered"}, ExitEvidence: []string{"live health and restart evidence"}, HumanGate: true},
		}},
		{ID: "research-decision", Title: "Research and decide", Description: "Turn bounded research into a cited decision without confusing hypotheses for facts.", Stages: []RecipeStage{
			{ID: "question", Title: "Frame the decision", Entry: []string{"decision owner"}, ExitEvidence: []string{"outcome contract", "decision criteria"}},
			{ID: "collect", Title: "Collect primary evidence", Entry: []string{"bounded scope"}, ExitEvidence: []string{"source artifacts"}},
			{ID: "challenge", Title: "Challenge the leading option", Entry: []string{"candidate conclusion"}, ExitEvidence: []string{"counterevidence review"}},
			{ID: "decide", Title: "Record the decision", Entry: []string{"criteria evaluated"}, ExitEvidence: []string{"verified decision artifact"}, HumanGate: true},
		}},
		{ID: "incident-recovery", Title: "Incident recovery", Description: "Diagnose, contain, repair, and prove recovery.", Stages: []RecipeStage{
			{ID: "contain", Title: "Bound impact", Entry: []string{"incident signal"}, ExitEvidence: []string{"impact and containment evidence"}, HumanGate: true},
			{ID: "diagnose", Title: "Find the causal fault", Entry: []string{"bounded system"}, ExitEvidence: []string{"reproduction", "causal evidence"}},
			{ID: "repair", Title: "Apply and test repair", Entry: []string{"causal fault"}, ExitEvidence: []string{"regression test", "repair artifact"}},
			{ID: "recover", Title: "Verify live recovery", Entry: []string{"tested repair"}, ExitEvidence: []string{"live health", "restart evidence"}, HumanGate: true},
		}},
	}
}

func validateContractInput(input ContractInput) error {
	if invalidText(input.Goal) || invalidText(input.Deliverable) || invalidText(input.NextAction) ||
		len(input.DoneCriteria) == 0 || len(input.DoneCriteria) > maxCriteria ||
		len(input.VerificationRequired) == 0 || len(input.VerificationRequired) > maxCriteria {
		return fmt.Errorf("work: bounded goal, deliverable, criteria, verification, and next action are required")
	}
	if input.Status != "" && input.Status != StatusDraft && input.Status != StatusActive && input.Status != StatusBlocked && input.Status != StatusCancelled {
		return fmt.Errorf("work: contract status is invalid")
	}
	seen := map[string]struct{}{}
	for _, criterion := range input.DoneCriteria {
		criterion.ID = strings.TrimSpace(criterion.ID)
		if criterion.ID == "" || len(criterion.ID) > 128 || invalidText(criterion.Description) {
			return fmt.Errorf("work: criterion ID and description are required")
		}
		if _, exists := seen[criterion.ID]; exists {
			return fmt.Errorf("work: criterion IDs must be unique")
		}
		seen[criterion.ID] = struct{}{}
	}
	for _, item := range input.VerificationRequired {
		if invalidText(item) {
			return fmt.Errorf("work: verification requirement is invalid")
		}
	}
	return nil
}

func invalidText(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || len(trimmed) > maxTextBytes
}

func trimUnique(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeCriteria(criteria []Criterion) []Criterion {
	result := make([]Criterion, len(criteria))
	for index, criterion := range criteria {
		result[index] = Criterion{ID: strings.TrimSpace(criterion.ID), Description: strings.TrimSpace(criterion.Description)}
	}
	return result
}

func findContract(contracts []OutcomeContract, id uuid.UUID) (OutcomeContract, bool) {
	for _, contract := range contracts {
		if contract.ID == id {
			return contract, true
		}
	}
	return OutcomeContract{}, false
}

func findArtifact(artifacts []Artifact, id uuid.UUID) (Artifact, bool) {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return Artifact{}, false
}

func uniqueUUIDs(values []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func findRecipe(id string) (Recipe, bool) {
	id = strings.TrimSpace(id)
	for _, recipe := range Recipes() {
		if recipe.ID == id {
			return recipe, true
		}
	}
	return Recipe{}, false
}

func criteriaSubset(criteria []Criterion, coverage []string) bool {
	known := map[string]struct{}{}
	for _, criterion := range criteria {
		known[criterion.ID] = struct{}{}
	}
	for _, id := range coverage {
		if _, ok := known[id]; !ok {
			return false
		}
	}
	return true
}

func ensureCriterionWorkItems(items []WorkItem, contract OutcomeContract, now time.Time) []WorkItem {
	retained := make([]WorkItem, 0, len(items)+len(contract.DoneCriteria))
	covered := make(map[string]struct{})
	for _, item := range items {
		if item.ContractID != contract.ID {
			retained = append(retained, item)
			continue
		}
		validCriteria := true
		for _, criterion := range item.Criteria {
			if !criterionKnown(contract.DoneCriteria, criterion) {
				validCriteria = false
				break
			}
			covered[criterion] = struct{}{}
		}
		if validCriteria {
			retained = append(retained, item)
		}
	}
	for _, criterion := range contract.DoneCriteria {
		if _, exists := covered[criterion.ID]; exists {
			continue
		}
		retained = append(retained, WorkItem{
			ID: "criterion:" + criterion.ID, ContractID: contract.ID,
			Title: criterion.Description, Criteria: []string{criterion.ID},
			Status: WorkItemReady, UpdatedAt: now,
		})
	}
	return retained
}

func criterionKnown(criteria []Criterion, wanted string) bool {
	for _, criterion := range criteria {
		if criterion.ID == wanted {
			return true
		}
	}
	return false
}

func dependenciesCompleted(items []WorkItem, item WorkItem) bool {
	for _, dependency := range item.DependsOn {
		completed := false
		for _, candidate := range items {
			if candidate.ContractID == item.ContractID && candidate.ID == dependency {
				completed = candidate.Status == WorkItemCompleted
				break
			}
		}
		if !completed {
			return false
		}
	}
	return true
}

func refreshReadyWorkItems(items []WorkItem) {
	for index := range items {
		if items[index].Status == WorkItemPending && dependenciesCompleted(items, items[index]) {
			items[index].Status = WorkItemReady
		}
	}
}

func refreshReadyWorkItemsForContract(items []WorkItem, contractID uuid.UUID) {
	for index := range items {
		if items[index].ContractID == contractID && items[index].Status == WorkItemPending &&
			dependenciesCompleted(items, items[index]) {
			items[index].Status = WorkItemReady
		}
	}
}

func reconcileVerifiedWorkItems(
	items []WorkItem, artifacts []Artifact, contractID uuid.UUID, now time.Time,
) {
	covered := verifiedCoverage(artifacts, contractID)
	for index := range items {
		if items[index].ContractID != contractID || items[index].Status == WorkItemCompleted {
			continue
		}
		complete := len(items[index].Criteria) > 0
		for _, criterion := range items[index].Criteria {
			if _, ok := covered[criterion]; !ok {
				complete = false
				break
			}
		}
		if complete {
			items[index].Status = WorkItemCompleted
			items[index].BlockingNote = ""
			items[index].UpdatedAt = now
		}
	}
	refreshReadyWorkItemsForContract(items, contractID)
}

func verifiedCoverage(artifacts []Artifact, contractID uuid.UUID) map[string]struct{} {
	covered := map[string]struct{}{}
	for _, artifact := range artifacts {
		if artifact.ContractID != contractID || artifact.VerifiedAt == nil || artifact.SHA256 == "" || artifact.Verification != "server_sha256" {
			continue
		}
		for _, id := range artifact.CriteriaCovered {
			covered[id] = struct{}{}
		}
	}
	return covered
}

func cleanReference(reference string) (string, error) {
	reference = filepath.Clean(strings.TrimSpace(reference))
	if reference == "." || filepath.IsAbs(reference) || reference == ".." || strings.HasPrefix(reference, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("work: artifact reference must remain workspace-relative")
	}
	return filepath.ToSlash(reference), nil
}

func resolveWorkspaceRoot(workspace string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || strings.TrimSpace(workspace) == "" {
		return "", fmt.Errorf("work: valid workspace is required")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("work: resolve workspace: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("work: workspace must be an existing directory")
	}
	return resolved, nil
}

func hashReference(ctx context.Context, workspace, reference string) (string, int64, error) {
	reference, err := cleanReference(reference)
	if err != nil {
		return "", 0, err
	}
	path := filepath.Join(workspace, filepath.FromSlash(reference))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", 0, fmt.Errorf("work: resolve artifact: %w", err)
	}
	relative, err := filepath.Rel(workspace, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", 0, fmt.Errorf("work: artifact escapes workspace")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", 0, fmt.Errorf("work: open artifact: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("work: inspect artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxArtifactBytes {
		return "", 0, fmt.Errorf("work: artifact must be a bounded regular file")
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(&contextReader{ctx: ctx, reader: file}, maxArtifactBytes+1))
	if err != nil {
		return "", 0, fmt.Errorf("work: hash artifact: %w", err)
	}
	if written > maxArtifactBytes {
		return "", 0, fmt.Errorf("work: artifact exceeds size limit")
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
