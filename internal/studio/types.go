// Package studio owns the durable, spec-driven boundary for Ion's
// embedded Software Studio. It binds engineering work to the same project,
// outcome, identity, and evidence state used by the general-purpose agent.
package studio

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const StateVersion = "ion.software-intent.v1"

var (
	ErrInvalid       = errors.New("studio: invalid intent")
	ErrNotFound      = errors.New("studio: intent or proposal not found")
	ErrStaleRevision = errors.New("studio: stale project revision")
	ErrDecision      = errors.New("studio: material human decision is required")
	ErrConflict      = errors.New("studio: state conflict")
)

type ProposalStatus string

const (
	ProposalProposed   ProposalStatus = "proposed"
	ProposalAccepted   ProposalStatus = "accepted"
	ProposalRejected   ProposalStatus = "rejected"
	ProposalSuperseded ProposalStatus = "superseded"
)

type Assumption struct {
	ID           string     `json:"id"`
	Statement    string     `json:"statement"`
	Reversible   bool       `json:"reversible"`
	Material     bool       `json:"material"`
	Consequence  string     `json:"consequence,omitempty"`
	DecisionNeed string     `json:"decision_needed,omitempty"`
	Resolution   string     `json:"resolution,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

type Criterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type PlannedTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Criteria  []string `json:"criteria"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type SpecDelta struct {
	UserVisibleBehavior []string      `json:"user_visible_behavior"`
	NonGoals            []string      `json:"non_goals"`
	Constraints         []string      `json:"constraints"`
	Risks               []string      `json:"risks"`
	Criteria            []Criterion   `json:"acceptance_criteria"`
	SecurityBoundaries  []string      `json:"security_boundaries"`
	DataBoundaries      []string      `json:"data_boundaries"`
	Migration           []string      `json:"migration"`
	Rollback            []string      `json:"rollback"`
	Verification        []string      `json:"verification_commands"`
	Tasks               []PlannedTask `json:"tasks"`
}

type RepositoryInspection struct {
	Root                  string    `json:"root"`
	InspectedAt           time.Time `json:"inspected_at"`
	WorkspaceRevision     uint64    `json:"workspace_revision"`
	InstructionFiles      []string  `json:"instruction_files"`
	AuthoritativeSpecPath string    `json:"authoritative_spec_path,omitempty"`
	AuthoritativeSpecHash string    `json:"authoritative_spec_sha256,omitempty"`
	GeneratedTasksPath    string    `json:"generated_tasks_path,omitempty"`
	GeneratedTasksHash    string    `json:"generated_tasks_sha256,omitempty"`
	ImplementationHash    string    `json:"implementation_sha256"`
	FilesInspected        int       `json:"files_inspected"`
	BytesInspected        int64     `json:"bytes_inspected"`
	Truncated             bool      `json:"truncated"`
}

type Proposal struct {
	ID               uuid.UUID      `json:"id"`
	Version          uint64         `json:"version"`
	Status           ProposalStatus `json:"status"`
	Rationale        string         `json:"rationale"`
	DependencyImpact []string       `json:"dependency_impact"`
	Delta            SpecDelta      `json:"delta"`
	Supersedes       *uuid.UUID     `json:"supersedes,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	DecidedAt        *time.Time     `json:"decided_at,omitempty"`
	DecisionReason   string         `json:"decision_reason,omitempty"`
	AppliedAt        *time.Time     `json:"applied_at,omitempty"`
	SpecHash         string         `json:"spec_sha256,omitempty"`
	GeneratedHash    string         `json:"generated_tasks_sha256,omitempty"`
}

type CorrelationKind string

const (
	CorrelationTask         CorrelationKind = "task"
	CorrelationPatch        CorrelationKind = "patch"
	CorrelationTool         CorrelationKind = "tool"
	CorrelationReview       CorrelationKind = "review"
	CorrelationVerification CorrelationKind = "verification"
	CorrelationArtifact     CorrelationKind = "artifact"
)

type Correlation struct {
	ID                 uuid.UUID       `json:"id"`
	Kind               CorrelationKind `json:"kind"`
	Reference          string          `json:"reference"`
	Criteria           []string        `json:"criteria"`
	Description        string          `json:"description"`
	WorkspaceRevision  uint64          `json:"workspace_revision"`
	SpecHash           string          `json:"spec_sha256"`
	GeneratedHash      string          `json:"generated_tasks_sha256"`
	ImplementationHash string          `json:"implementation_sha256"`
	CreatedAt          time.Time       `json:"created_at"`
}

type CorrelationInput struct {
	IntentID    uuid.UUID       `json:"intent_id"`
	Kind        CorrelationKind `json:"kind"`
	Reference   string          `json:"reference"`
	Criteria    []string        `json:"criteria"`
	Description string          `json:"description"`
}

type Completion struct {
	Ready                  bool                    `json:"ready"`
	BlockingReasons        []string                `json:"blocking_reasons"`
	UncoveredCriteria      []string                `json:"uncovered_criteria"`
	UnverifiedCriteria     []string                `json:"unverified_criteria"`
	MissingCorrelationKind []CorrelationKind       `json:"missing_correlation_kinds"`
	CorrelationsByKind     map[CorrelationKind]int `json:"correlations_by_kind"`
	Drift                  Drift                   `json:"drift"`
}

type Intent struct {
	ID                 uuid.UUID            `json:"id"`
	Version            string               `json:"version"`
	ProjectID          uuid.UUID            `json:"project_id"`
	OutcomeContractID  uuid.UUID            `json:"outcome_contract_id"`
	BaselineRevision   uint64               `json:"baseline_workspace_revision"`
	Goal               string               `json:"goal"`
	MappedRequirements []string             `json:"mapped_requirements"`
	Assumptions        []Assumption         `json:"assumptions"`
	Inspection         RepositoryInspection `json:"inspection"`
	Proposals          []Proposal           `json:"proposals"`
	Correlations       []Correlation        `json:"correlations"`
	ActiveProposalID   *uuid.UUID           `json:"active_proposal_id,omitempty"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

type CompileInput struct {
	ProjectID          uuid.UUID    `json:"project_id"`
	OutcomeContractID  uuid.UUID    `json:"outcome_contract_id"`
	WorkspaceRevision  uint64       `json:"workspace_revision"`
	Goal               string       `json:"goal"`
	MappedRequirements []string     `json:"mapped_requirements,omitempty"`
	Assumptions        []Assumption `json:"assumptions,omitempty"`
	Rationale          string       `json:"rationale,omitempty"`
	DependencyImpact   []string     `json:"dependency_impact,omitempty"`
	Delta              *SpecDelta   `json:"spec_delta,omitempty"`
}

type ScopeChangeInput struct {
	IntentID         uuid.UUID `json:"intent_id"`
	Rationale        string    `json:"rationale"`
	DependencyImpact []string  `json:"dependency_impact"`
	Delta            SpecDelta `json:"spec_delta"`
}

type Drift struct {
	AuthoritativeSpecChanged bool `json:"authoritative_spec_changed"`
	GeneratedViewChanged     bool `json:"generated_view_changed"`
	ImplementationChanged    bool `json:"implementation_changed"`
	CompletionBlocked        bool `json:"completion_blocked"`
}
