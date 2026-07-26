package project

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const DeliveryContractVersion = "ion.project-delivery.v1"

type EnvironmentScope string

const (
	EnvironmentDevelopment EnvironmentScope = "development"
	EnvironmentTest        EnvironmentScope = "test"
	EnvironmentPreview     EnvironmentScope = "preview"
	EnvironmentStaging     EnvironmentScope = "staging"
	EnvironmentProduction  EnvironmentScope = "production"
)

type ResourceKind string

const (
	ResourceDatabase    ResourceKind = "database"
	ResourceStorage     ResourceKind = "object_storage"
	ResourceAuth        ResourceKind = "authentication"
	ResourceEmail       ResourceKind = "email"
	ResourceQueue       ResourceKind = "queue"
	ResourceSchedule    ResourceKind = "scheduled_job"
	ResourceAnalytics   ResourceKind = "analytics"
	ResourcePayment     ResourceKind = "payment"
	ResourceExternalAPI ResourceKind = "external_api"
)

type ResourceCatalogEntry struct {
	Kind         ResourceKind `json:"kind"`
	Capabilities []string     `json:"capabilities"`
	DataRisks    []string     `json:"data_risks"`
}

type ResourceDesiredState struct {
	ID                    uuid.UUID        `json:"id"`
	ProjectID             uuid.UUID        `json:"project_id"`
	Name                  string           `json:"name"`
	Kind                  ResourceKind     `json:"kind"`
	Provider              string           `json:"provider"`
	Environment           EnvironmentScope `json:"environment"`
	Capabilities          []string         `json:"capabilities"`
	Ownership             string           `json:"ownership"`
	DataRisk              string           `json:"data_risk"`
	Region                string           `json:"region,omitempty"`
	Engine                string           `json:"engine,omitempty"`
	RetentionDays         int              `json:"retention_days,omitempty"`
	MonthlyCostLimitCents int64            `json:"monthly_cost_limit_cents"`
	SecretReferences      []string         `json:"secret_references,omitempty"`
}

type ResourcePlan struct {
	Version            string               `json:"version"`
	ID                 uuid.UUID            `json:"id"`
	ActorID            uuid.UUID            `json:"actor_id"`
	ProjectID          uuid.UUID            `json:"project_id"`
	WorkspaceRevision  uint64               `json:"workspace_revision"`
	Desired            ResourceDesiredState `json:"desired"`
	Actions            []string             `json:"actions"`
	Classification     PolicyClassification `json:"classification"`
	EstimatedCostCents int64                `json:"estimated_cost_cents"`
	PlanSHA256         string               `json:"plan_sha256"`
	CreatedAt          time.Time            `json:"created_at"`
}

type ResourceReceipt struct {
	Version         string               `json:"version"`
	ID              uuid.UUID            `json:"id"`
	PlanID          uuid.UUID            `json:"plan_id"`
	ActorID         uuid.UUID            `json:"actor_id"`
	ProjectID       uuid.UUID            `json:"project_id"`
	ResourceID      uuid.UUID            `json:"resource_id"`
	Provider        string               `json:"provider"`
	Environment     EnvironmentScope     `json:"environment"`
	State           string               `json:"state"`
	ExternalID      string               `json:"external_id,omitempty"`
	Endpoint        string               `json:"endpoint,omitempty"`
	Classification  PolicyClassification `json:"classification"`
	ActualCostCents int64                `json:"actual_cost_cents"`
	Evidence        []EvidenceReference  `json:"evidence"`
	IdempotencyKey  string               `json:"idempotency_key"`
	RequestSHA256   string               `json:"request_sha256"`
	CreatedAt       time.Time            `json:"created_at"`
	ReconciledAt    time.Time            `json:"reconciled_at"`
}

type ResourcePlanInput struct {
	ProjectID         uuid.UUID            `json:"project_id"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	Desired           ResourceDesiredState `json:"desired"`
}

type ResourceApplyInput struct {
	ProjectID      uuid.UUID     `json:"project_id"`
	PlanID         uuid.UUID     `json:"plan_id"`
	SecretGrants   []SecretGrant `json:"secret_grants,omitempty"`
	IdempotencyKey string        `json:"idempotency_key"`
}

type EnvironmentVariable struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Reference   string `json:"reference"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type EnvironmentSchema struct {
	Version      string                `json:"version"`
	Revision     uint64                `json:"revision"`
	ActorID      uuid.UUID             `json:"actor_id"`
	ProjectID    uuid.UUID             `json:"project_id"`
	Environment  EnvironmentScope      `json:"environment"`
	Variables    []EnvironmentVariable `json:"variables"`
	SchemaSHA256 string                `json:"schema_sha256"`
	CreatedAt    time.Time             `json:"created_at"`
}

type EnvironmentSchemaInput struct {
	ProjectID   uuid.UUID             `json:"project_id"`
	Environment EnvironmentScope      `json:"environment"`
	Variables   []EnvironmentVariable `json:"variables"`
}

type MigrationStep struct {
	ID       string `json:"id"`
	SQL      string `json:"sql"`
	Rollback string `json:"rollback"`
}

type MigrationPlanInput struct {
	ProjectID         uuid.UUID        `json:"project_id"`
	WorkspaceRevision uint64           `json:"workspace_revision"`
	Environment       EnvironmentScope `json:"environment"`
	DatabasePath      string           `json:"database_path"`
	Steps             []MigrationStep  `json:"steps"`
}

type MigrationPlan struct {
	Version             string               `json:"version"`
	ID                  uuid.UUID            `json:"id"`
	ActorID             uuid.UUID            `json:"actor_id"`
	ProjectID           uuid.UUID            `json:"project_id"`
	WorkspaceRevision   uint64               `json:"workspace_revision"`
	Environment         EnvironmentScope     `json:"environment"`
	DatabasePath        string               `json:"database_path"`
	Steps               []MigrationStep      `json:"steps"`
	SchemaBefore        []string             `json:"schema_before"`
	SchemaAfter         []string             `json:"schema_after"`
	DestructiveFindings []string             `json:"destructive_findings"`
	DryRunPassed        bool                 `json:"dry_run_passed"`
	Classification      PolicyClassification `json:"classification"`
	PlanSHA256          string               `json:"plan_sha256"`
	CreatedAt           time.Time            `json:"created_at"`
}

type MigrationReceipt struct {
	Version          string              `json:"version"`
	ID               uuid.UUID           `json:"id"`
	PlanID           uuid.UUID           `json:"plan_id"`
	ActorID          uuid.UUID           `json:"actor_id"`
	ProjectID        uuid.UUID           `json:"project_id"`
	Environment      EnvironmentScope    `json:"environment"`
	State            string              `json:"state"`
	BackupPath       string              `json:"backup_path"`
	BackupSHA256     string              `json:"backup_sha256"`
	SchemaAfter      []string            `json:"schema_after"`
	RollbackEvidence []EvidenceReference `json:"rollback_evidence"`
	AppliedAt        time.Time           `json:"applied_at"`
	RolledBackAt     *time.Time          `json:"rolled_back_at,omitempty"`
}

type MigrationApplyInput struct {
	ProjectID uuid.UUID `json:"project_id"`
	PlanID    uuid.UUID `json:"plan_id"`
}

type MigrationRollbackInput struct {
	ProjectID uuid.UUID `json:"project_id"`
	ReceiptID uuid.UUID `json:"receipt_id"`
}

type Artifact struct {
	ID                uuid.UUID `json:"id"`
	ProjectID         uuid.UUID `json:"project_id"`
	WorkspaceRevision uint64    `json:"workspace_revision"`
	Path              string    `json:"path"`
	SHA256            string    `json:"sha256"`
	SizeBytes         int64     `json:"size_bytes"`
	CreatedAt         time.Time `json:"created_at"`
}

type DeploymentPlanInput struct {
	ProjectID           uuid.UUID        `json:"project_id"`
	WorkspaceRevision   uint64           `json:"workspace_revision"`
	Environment         EnvironmentScope `json:"environment"`
	Provider            string           `json:"provider"`
	HealthPath          string           `json:"health_path"`
	Domain              string           `json:"domain,omitempty"`
	Version             string           `json:"version"`
	EnvironmentRevision uint64           `json:"environment_revision,omitempty"`
	CostLimitCents      int64            `json:"cost_limit_cents"`
}

type DeploymentPlan struct {
	Version             string               `json:"contract_version"`
	ID                  uuid.UUID            `json:"id"`
	ActorID             uuid.UUID            `json:"actor_id"`
	ProjectID           uuid.UUID            `json:"project_id"`
	WorkspaceRevision   uint64               `json:"workspace_revision"`
	Environment         EnvironmentScope     `json:"environment"`
	Provider            string               `json:"provider"`
	Artifact            Artifact             `json:"artifact"`
	HealthPath          string               `json:"health_path"`
	Domain              string               `json:"domain,omitempty"`
	ReleaseVersion      string               `json:"release_version"`
	EnvironmentRevision uint64               `json:"environment_revision,omitempty"`
	CostLimitCents      int64                `json:"cost_limit_cents"`
	Classification      PolicyClassification `json:"classification"`
	Actions             []string             `json:"actions"`
	PlanSHA256          string               `json:"plan_sha256"`
	CreatedAt           time.Time            `json:"created_at"`
}

type DeploymentReceipt struct {
	Version         string               `json:"version"`
	ID              uuid.UUID            `json:"id"`
	PlanID          uuid.UUID            `json:"plan_id"`
	ActorID         uuid.UUID            `json:"actor_id"`
	ProjectID       uuid.UUID            `json:"project_id"`
	Environment     EnvironmentScope     `json:"environment"`
	Provider        string               `json:"provider"`
	ReleaseVersion  string               `json:"release_version"`
	ArtifactSHA256  string               `json:"artifact_sha256"`
	State           string               `json:"state"`
	URL             string               `json:"url,omitempty"`
	Health          string               `json:"health"`
	Logs            string               `json:"logs,omitempty"`
	RollbackHandle  string               `json:"rollback_handle,omitempty"`
	PreviousReceipt *uuid.UUID           `json:"previous_receipt,omitempty"`
	Classification  PolicyClassification `json:"classification"`
	ActualCostCents int64                `json:"actual_cost_cents"`
	IdempotencyKey  string               `json:"idempotency_key"`
	RequestSHA256   string               `json:"request_sha256"`
	Evidence        []EvidenceReference  `json:"evidence"`
	CreatedAt       time.Time            `json:"created_at"`
	ReconciledAt    time.Time            `json:"reconciled_at"`
	RolledBackAt    *time.Time           `json:"rolled_back_at,omitempty"`
}

type DeploymentApplyInput struct {
	ProjectID      uuid.UUID     `json:"project_id"`
	PlanID         uuid.UUID     `json:"plan_id"`
	SecretGrants   []SecretGrant `json:"secret_grants,omitempty"`
	IdempotencyKey string        `json:"idempotency_key"`
}

type DeploymentReconcileInput struct {
	ProjectID uuid.UUID `json:"project_id"`
	ReceiptID uuid.UUID `json:"receipt_id"`
}

type DeploymentRollbackInput struct {
	ProjectID uuid.UUID `json:"project_id"`
	ReceiptID uuid.UUID `json:"receipt_id"`
}

type EvidenceReference struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256,omitempty"`
}

type ReleaseReadiness struct {
	Version        string              `json:"version"`
	ActorID        uuid.UUID           `json:"actor_id"`
	ProjectID      uuid.UUID           `json:"project_id"`
	ReleaseVersion string              `json:"release_version"`
	Notes          []string            `json:"notes"`
	Changelog      []string            `json:"changelog"`
	Domain         string              `json:"domain,omitempty"`
	DNSState       string              `json:"dns_state"`
	ProviderChecks []EvidenceReference `json:"provider_checks"`
	PortableExport string              `json:"portable_export,omitempty"`
	Ready          bool                `json:"ready"`
	Unmet          []string            `json:"unmet"`
	CreatedAt      time.Time           `json:"created_at"`
}

type CIPatchPlan struct {
	Version           string               `json:"version"`
	ProjectID         uuid.UUID            `json:"project_id"`
	WorkspaceRevision uint64               `json:"workspace_revision"`
	Path              string               `json:"path"`
	ExpectedSHA256    string               `json:"expected_sha256"`
	Content           string               `json:"content"`
	Classification    PolicyClassification `json:"classification"`
	ReviewRequired    bool                 `json:"review_required"`
}

type ReleaseInput struct {
	ProjectID      uuid.UUID `json:"project_id"`
	ReleaseVersion string    `json:"release_version"`
	Notes          []string  `json:"notes"`
	Changelog      []string  `json:"changelog"`
	Domain         string    `json:"domain,omitempty"`
}

type DeliverySnapshot struct {
	Version      string                 `json:"version"`
	Catalog      []ResourceCatalogEntry `json:"catalog"`
	Resources    []ResourceReceipt      `json:"resources"`
	Environments []EnvironmentSchema    `json:"environments"`
	Migrations   []MigrationReceipt     `json:"migrations"`
	Deployments  []DeploymentReceipt    `json:"deployments"`
	Release      *ReleaseReadiness      `json:"release,omitempty"`
}

type ResourceAdapter interface {
	Name() string
	Capabilities(context.Context) []ResourceKind
	Plan(context.Context, ResourceDesiredState) ([]string, int64, error)
	Apply(context.Context, ResourcePlan) (ResourceReceipt, error)
	Reconcile(context.Context, ResourceReceipt) (ResourceReceipt, error)
	Export(context.Context, ResourceReceipt, string) ([]EvidenceReference, error)
}

type DeploymentAdapter interface {
	Name() string
	Plan(context.Context, DeploymentPlan) ([]string, error)
	Apply(context.Context, DeploymentPlan) (DeploymentReceipt, error)
	Reconcile(context.Context, DeploymentReceipt, Artifact, string) (DeploymentReceipt, error)
	Rollback(context.Context, DeploymentReceipt, DeploymentReceipt, Artifact, string) (DeploymentReceipt, error)
	Close() error
}

type deliveryState struct {
	Revision           uint64                     `json:"revision"`
	ResourcePlans      []ResourcePlan             `json:"resource_plans"`
	Resources          []ResourceReceipt          `json:"resources"`
	Environments       []EnvironmentSchema        `json:"environments"`
	MigrationPlans     []MigrationPlan            `json:"migration_plans"`
	Migrations         []MigrationReceipt         `json:"migrations"`
	DeploymentPlans    []DeploymentPlan           `json:"deployment_plans"`
	Deployments        []DeploymentReceipt        `json:"deployments"`
	Release            *ReleaseReadiness          `json:"release,omitempty"`
	ResourceRequests   map[string]json.RawMessage `json:"resource_requests"`
	DeploymentRequests map[string]json.RawMessage `json:"deployment_requests"`
}
