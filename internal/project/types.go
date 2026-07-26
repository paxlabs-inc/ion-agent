// Package project owns Ion's durable engineering-project registry and
// the transport-neutral contract used to operate isolated workspaces. It is a
// capability of the shared Ion runtime, not a separate agent runtime.
package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// WorkspaceHostVersion is negotiated before a project workflow dispatches
	// any privileged operation to a local, container, or remote implementation.
	WorkspaceHostVersion = "ion.workspace-host.v1"
	registryStateKind    = "project_registry_v1"
)

var (
	ErrInvalidEnvelope = errors.New("project: invalid workspace operation envelope")
	ErrStaleRevision   = errors.New("project: stale workspace revision")
	ErrNotFound        = errors.New("project: project not found")
	ErrUnsupported     = errors.New("project: workspace capability is unavailable")
	ErrConflict        = errors.New("project: workspace conflict")
)

// HostKind identifies an implementation without leaking its transport into
// project workflows.
type HostKind string

const (
	HostDirectLocal HostKind = "direct_local"
	HostContainer   HostKind = "container"
	HostRemote      HostKind = "remote_worker"
)

// CapabilityDomain is the complete WorkspaceHost capability namespace.
type CapabilityDomain string

const (
	CapabilityProject     CapabilityDomain = "project"
	CapabilityFile        CapabilityDomain = "file"
	CapabilityProcess     CapabilityDomain = "process"
	CapabilityGit         CapabilityDomain = "git"
	CapabilityPreview     CapabilityDomain = "preview"
	CapabilityArtifact    CapabilityDomain = "artifact"
	CapabilityIntegration CapabilityDomain = "integration"
	CapabilitySystem      CapabilityDomain = "system"
)

var capabilityDomains = []CapabilityDomain{
	CapabilityProject, CapabilityFile, CapabilityProcess, CapabilityGit,
	CapabilityPreview, CapabilityArtifact, CapabilityIntegration, CapabilitySystem,
}

// Capability describes one negotiated domain honestly. Unsupported domains
// stay in the response with an actionable reason instead of disappearing.
type Capability struct {
	Domain    CapabilityDomain `json:"domain"`
	Supported bool             `json:"supported"`
	Features  []string         `json:"features"`
	Reason    string           `json:"reason,omitempty"`
}

// ResourceLimits are hard ceilings supplied to isolated implementations and
// checked before and after direct-local operations.
type ResourceLimits struct {
	CPUMillis      int64 `json:"cpu_millis"`
	MemoryBytes    int64 `json:"memory_bytes"`
	Processes      int64 `json:"processes"`
	DiskBytes      int64 `json:"disk_bytes"`
	WallTimeSecond int64 `json:"wall_time_seconds"`
	OutputBytes    int64 `json:"output_bytes"`
}

// NetworkPolicy is deny-by-default. Allowed destinations are hostnames, not
// credentials or arbitrary command fragments.
type NetworkPolicy struct {
	Mode         string   `json:"mode"`
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
	ExposedPorts []uint16 `json:"exposed_ports,omitempty"`
}

// SecretGrant is a short-lived write-only Vault reference. Secret values are
// intentionally impossible to represent in this contract.
type SecretGrant struct {
	ID        uuid.UUID `json:"id"`
	Reference string    `json:"reference"`
	ExpiresAt time.Time `json:"expires_at"`
	Token     string    `json:"grant_token"`
}

// HostCapabilities is the versioned negotiation response common to every
// implementation.
type HostCapabilities struct {
	Version             string         `json:"version"`
	Kind                HostKind       `json:"kind"`
	Available           bool           `json:"available"`
	Reason              string         `json:"reason,omitempty"`
	Domains             []Capability   `json:"domains"`
	Limits              ResourceLimits `json:"limits"`
	Network             NetworkPolicy  `json:"network"`
	NonRoot             bool           `json:"non_root"`
	RootConfined        bool           `json:"root_confined"`
	AuthorityDisclosure string         `json:"authority_disclosure,omitempty"`
}

// PolicyClassification is carried by every privileged host operation.
type PolicyClassification string

const (
	PolicyGreen  PolicyClassification = "GREEN"
	PolicyYellow PolicyClassification = "YELLOW"
	PolicyRed    PolicyClassification = "RED"
)

// HostOperation is closed and versioned independently of any transport.
type HostOperation string

const (
	HostProvision HostOperation = "workspace.provision"
	HostReadiness HostOperation = "workspace.readiness"
	HostPause     HostOperation = "workspace.pause"
	HostResume    HostOperation = "workspace.resume"
	HostStop      HostOperation = "workspace.stop"
	HostArchive   HostOperation = "workspace.archive"
	HostDestroy   HostOperation = "workspace.destroy"
)

func (operation HostOperation) valid() bool {
	switch operation {
	case HostProvision, HostReadiness, HostPause, HostResume, HostStop, HostArchive, HostDestroy:
		return true
	default:
		return false
	}
}

// OperationEnvelope is mandatory for every privileged WorkspaceHost call.
// The project service constructs it from the authenticated control-plane
// request; clients cannot substitute a different actor or revision.
type OperationEnvelope struct {
	Version              string               `json:"version"`
	Operation            HostOperation        `json:"operation"`
	ActorID              uuid.UUID            `json:"actor_id"`
	ProjectID            uuid.UUID            `json:"project_id"`
	WorkspaceRevision    uint64               `json:"workspace_revision"`
	RequestID            uuid.UUID            `json:"request_id"`
	IdempotencyKey       string               `json:"idempotency_key"`
	PolicyClassification PolicyClassification `json:"policy_classification"`
	Deadline             time.Time            `json:"deadline"`
	CorrelationID        uuid.UUID            `json:"correlation_id"`
	SecretGrants         []SecretGrant        `json:"secret_grants,omitempty"`
	Payload              json.RawMessage      `json:"payload,omitempty"`
}

// Validate rejects incomplete, stale-prone, unbounded, or secret-bearing
// operation envelopes before an implementation is selected.
func (envelope OperationEnvelope) Validate(now time.Time) error {
	if envelope.Version != WorkspaceHostVersion || !envelope.Operation.valid() ||
		envelope.ActorID == uuid.Nil || envelope.ProjectID == uuid.Nil ||
		envelope.RequestID == uuid.Nil || envelope.CorrelationID == uuid.Nil ||
		strings.TrimSpace(envelope.IdempotencyKey) == "" ||
		(envelope.PolicyClassification != PolicyGreen &&
			envelope.PolicyClassification != PolicyYellow &&
			envelope.PolicyClassification != PolicyRed) ||
		envelope.Deadline.IsZero() || !envelope.Deadline.After(now) {
		return ErrInvalidEnvelope
	}
	if len(envelope.IdempotencyKey) > 256 || len(envelope.Payload) > 1<<20 ||
		(len(envelope.Payload) > 0 && !json.Valid(envelope.Payload)) {
		return ErrInvalidEnvelope
	}
	for _, grant := range envelope.SecretGrants {
		if grant.ID == uuid.Nil || strings.TrimSpace(grant.Reference) == "" ||
			strings.TrimSpace(grant.Token) == "" || !grant.ExpiresAt.After(now) ||
			grant.ExpiresAt.After(envelope.Deadline) {
			return fmt.Errorf("%w: invalid secret grant", ErrInvalidEnvelope)
		}
	}
	return nil
}

// SourceKind records how a project entered the registry.
type SourceKind string

const (
	SourceTemplate   SourceKind = "template"
	SourceArchive    SourceKind = "archive"
	SourceDirectory  SourceKind = "directory"
	SourceRepository SourceKind = "repository"
)

// TrustState controls whether discovered repository content may execute.
type TrustState string

const (
	TrustUntrusted TrustState = "untrusted"
	TrustReviewed  TrustState = "reviewed"
	TrustTrusted   TrustState = "trusted"
)

// LifecycleState is durable and reconciled after daemon restart.
type LifecycleState string

const (
	LifecycleProvisioning   LifecycleState = "provisioning"
	LifecycleReady          LifecycleState = "ready"
	LifecyclePaused         LifecycleState = "paused"
	LifecycleStopped        LifecycleState = "stopped"
	LifecycleArchived       LifecycleState = "archived"
	LifecycleDestroyPending LifecycleState = "destroy_pending"
	LifecycleFailed         LifecycleState = "failed"
)

// Project is the redaction-safe durable project record.
type Project struct {
	ID                uuid.UUID      `json:"id"`
	Name              string         `json:"name"`
	Root              string         `json:"root"`
	Source            SourceKind     `json:"source"`
	SourceReference   string         `json:"source_reference,omitempty"`
	DefaultBranch     string         `json:"default_branch,omitempty"`
	StackSignals      []string       `json:"stack_signals"`
	Trust             TrustState     `json:"trust"`
	WorkspaceRevision uint64         `json:"workspace_revision"`
	Host              HostKind       `json:"host"`
	HostReference     string         `json:"host_reference,omitempty"`
	LatestArchive     string         `json:"latest_archive,omitempty"`
	Managed           bool           `json:"managed"`
	Lifecycle         LifecycleState `json:"lifecycle"`
	LastError         string         `json:"last_error,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// HostResult is a bounded, typed lifecycle result. Raw process output is not
// returned through the project registry.
type HostResult struct {
	State         LifecycleState `json:"state"`
	HostReference string         `json:"host_reference,omitempty"`
	Message       string         `json:"message,omitempty"`
}

// WorkspaceHost is the single domain boundary implemented by local,
// container, and future remote workers.
type WorkspaceHost interface {
	Capabilities(context.Context) HostCapabilities
	Execute(context.Context, Project, OperationEnvelope) (HostResult, error)
	Reconcile(context.Context, Project) (HostResult, error)
}
