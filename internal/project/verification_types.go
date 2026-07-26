package project

import (
	"time"

	"github.com/google/uuid"
)

const VerificationContractVersion = "ion.project-verification.v1"

type VerificationCriterion struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Kinds       []string `json:"kinds"`
}

type VerificationGate struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"`
	Argv              []string `json:"argv,omitempty"`
	WorkingDirectory  string   `json:"working_directory,omitempty"`
	Environment       []string `json:"environment"`
	TimeoutSeconds    int      `json:"timeout_seconds"`
	Required          bool     `json:"required"`
	Criteria          []string `json:"criteria"`
	EvidenceKinds     []string `json:"evidence_kinds"`
	EvidencePaths     []string `json:"evidence_paths,omitempty"`
	Available         bool     `json:"available"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type VerificationManifest struct {
	Version           string                  `json:"version"`
	ID                uuid.UUID               `json:"id"`
	ActorID           uuid.UUID               `json:"actor_id"`
	ProjectID         uuid.UUID               `json:"project_id"`
	WorkspaceRevision uint64                  `json:"workspace_revision"`
	InducingPatchID   *uuid.UUID              `json:"inducing_patch_id,omitempty"`
	Revision          uint64                  `json:"revision"`
	Criteria          []VerificationCriterion `json:"criteria"`
	Gates             []VerificationGate      `json:"gates"`
	CreatedAt         time.Time               `json:"created_at"`
}

type VerificationManifestInput struct {
	ProjectID         uuid.UUID               `json:"project_id"`
	WorkspaceRevision uint64                  `json:"workspace_revision"`
	Criteria          []VerificationCriterion `json:"criteria"`
	Gates             []VerificationGate      `json:"gates,omitempty"`
}

type VerificationEvidence struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type VerificationGateResult struct {
	GateID            string                 `json:"gate_id"`
	Kind              string                 `json:"kind"`
	Status            string                 `json:"status"`
	TerminalID        *uuid.UUID             `json:"terminal_id,omitempty"`
	ExitCode          *int                   `json:"exit_code,omitempty"`
	DurationMillis    int64                  `json:"duration_millis"`
	Logs              string                 `json:"logs,omitempty"`
	LogsTruncated     bool                   `json:"logs_truncated"`
	FailureSignature  string                 `json:"failure_signature,omitempty"`
	CriteriaCovered   []string               `json:"criteria_covered"`
	Evidence          []VerificationEvidence `json:"evidence"`
	UnavailableReason string                 `json:"unavailable_reason,omitempty"`
	WaiverID          *uuid.UUID             `json:"waiver_id,omitempty"`
}

type VerificationRepairDecision struct {
	State             string   `json:"state"`
	Reason            string   `json:"reason"`
	Attempts          int      `json:"attempts"`
	MaxAttempts       int      `json:"max_attempts"`
	FailureSignatures []string `json:"failure_signatures"`
}

type VerificationRun struct {
	Version           string                     `json:"version"`
	ID                uuid.UUID                  `json:"id"`
	ActorID           uuid.UUID                  `json:"actor_id"`
	ProjectID         uuid.UUID                  `json:"project_id"`
	ManifestID        uuid.UUID                  `json:"manifest_id"`
	ManifestRevision  uint64                     `json:"manifest_revision"`
	WorkspaceRevision uint64                     `json:"workspace_revision"`
	InducingPatchID   *uuid.UUID                 `json:"inducing_patch_id,omitempty"`
	Mode              string                     `json:"mode"`
	Status            string                     `json:"status"`
	Results           []VerificationGateResult   `json:"results"`
	CriteriaCovered   []string                   `json:"criteria_covered"`
	UncoveredCriteria []string                   `json:"uncovered_criteria"`
	Repair            VerificationRepairDecision `json:"repair"`
	StartedAt         time.Time                  `json:"started_at"`
	FinishedAt        time.Time                  `json:"finished_at"`
}

type VerificationRunRequest struct {
	ProjectID   uuid.UUID `json:"project_id"`
	ManifestID  uuid.UUID `json:"manifest_id"`
	GateIDs     []string  `json:"gate_ids,omitempty"`
	Full        bool      `json:"full"`
	MaxAttempts int       `json:"max_attempts,omitempty"`
}

type VerificationWaiver struct {
	ID         uuid.UUID  `json:"id"`
	ActorID    uuid.UUID  `json:"actor_id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	ManifestID uuid.UUID  `json:"manifest_id"`
	GateIDs    []string   `json:"gate_ids"`
	Criteria   []string   `json:"criteria"`
	Reason     string     `json:"reason"`
	Risk       string     `json:"risk"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type VerificationWaiverInput struct {
	ProjectID  uuid.UUID `json:"project_id"`
	ManifestID uuid.UUID `json:"manifest_id"`
	GateIDs    []string  `json:"gate_ids"`
	Criteria   []string  `json:"criteria"`
	Reason     string    `json:"reason"`
	Risk       string    `json:"risk"`
	ExpiresAt  time.Time `json:"expires_at"`
}
