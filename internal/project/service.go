package project

import (
	"archive/zip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	maxProjects       = 128
	maxReceipts       = 512
	maxArchiveFiles   = 20_000
	maxArchiveBytes   = 2 << 30
	maxProjectNameLen = 120
	grantKeyStateKind = "project_grant_authority_v1"
)

// OperationMeta is built only at an authenticated application boundary.
type OperationMeta struct {
	ActorID              uuid.UUID
	RequestID            uuid.UUID
	IdempotencyKey       string
	PolicyClassification PolicyClassification
	Deadline             time.Time
	CorrelationID        uuid.UUID
	ExpectedRevision     *uint64
	SecretGrants         []SecretGrant
}

func (meta OperationMeta) validate(now time.Time) error {
	probe := OperationEnvelope{Version: WorkspaceHostVersion, Operation: HostReadiness,
		ActorID: meta.ActorID, ProjectID: uuid.New(), WorkspaceRevision: 1,
		RequestID: meta.RequestID, IdempotencyKey: meta.IdempotencyKey,
		PolicyClassification: meta.PolicyClassification, Deadline: meta.Deadline,
		CorrelationID: meta.CorrelationID, SecretGrants: meta.SecretGrants}
	return probe.Validate(now)
}

type TemplateInput struct {
	Name     string     `json:"name"`
	Template string     `json:"template"`
	Host     HostKind   `json:"host"`
	Trust    TrustState `json:"trust"`
}

type ArchiveInput struct {
	Name        string     `json:"name"`
	ArchivePath string     `json:"archive_path"`
	Host        HostKind   `json:"host"`
	Trust       TrustState `json:"trust"`
}

type AttachInput struct {
	Name      string     `json:"name"`
	Directory string     `json:"directory"`
	Trust     TrustState `json:"trust"`
}

type CloneInput struct {
	Name                string     `json:"name"`
	RepositoryURL       string     `json:"repository_url"`
	DefaultBranch       string     `json:"default_branch,omitempty"`
	CredentialReference string     `json:"credential_reference,omitempty"`
	Authorized          bool       `json:"authorized"`
	Host                HostKind   `json:"host"`
	Trust               TrustState `json:"trust"`
}

type LifecycleInput struct {
	ProjectID               uuid.UUID     `json:"project_id"`
	Operation               HostOperation `json:"operation"`
	UncommittedWorkDecision string        `json:"uncommitted_work_decision,omitempty"`
}

// LifecycleEvent is converted to a closed control-plane event and durably
// sequenced by the production adapter.
type LifecycleEvent struct {
	State             string        `json:"state"`
	Operation         HostOperation `json:"operation"`
	ActorID           uuid.UUID     `json:"actor_id"`
	ProjectID         uuid.UUID     `json:"project_id"`
	WorkspaceRevision uint64        `json:"workspace_revision"`
	RequestID         uuid.UUID     `json:"request_id"`
	CorrelationID     uuid.UUID     `json:"correlation_id"`
	Message           string        `json:"message,omitempty"`
}

type EventEmitter interface {
	EmitProjectEvent(context.Context, LifecycleEvent) error
}

type ServiceConfig struct {
	WorkspaceRoot       string
	ArchiveRoot         string
	DeliveryRoot        string
	AttachRoots         []string
	ImportRoots         []string
	AllowFileClone      bool
	LocalHost           *LocalHost
	ContainerHost       *ContainerHost
	RepositoryProviders map[string]RepositoryProvider
	ResourceAdapters    map[string]ResourceAdapter
	DeploymentAdapters  map[string]DeploymentAdapter
	GitCredentialBroker GitCredentialBroker
	Control             *controllease.Service
}

type mutationReceipt struct {
	Key       string    `json:"key"`
	Hash      string    `json:"hash"`
	Project   Project   `json:"project"`
	CreatedAt time.Time `json:"created_at"`
}

type registry struct {
	Revision uint64            `json:"revision"`
	Projects []Project         `json:"projects"`
	Receipts []mutationReceipt `json:"receipts"`
}

// Service persists each actor registry through the encrypted session store.
type Service struct {
	mu                  sync.Mutex
	store               *session.Store
	clock               types.Clock
	intelligence        *Intelligence
	editing             *EditingService
	terminals           *TerminalService
	runtimes            *RuntimeService
	verification        *VerificationService
	delivery            *DeliveryService
	gitMu               sync.Mutex
	workspaceRoot       string
	archiveRoot         string
	attachRoots         []string
	importRoots         []string
	allowFileClone      bool
	hosts               map[HostKind]WorkspaceHost
	emitter             EventEmitter
	repositoryProviders map[string]RepositoryProvider
	gitCredentialBroker GitCredentialBroker
}

type grantKeyState struct {
	Key []byte `json:"key"`
}

// IssueSecretGrant creates an actor-scoped, short-lived capability signed by
// key material that is itself stored only through Vault-encrypted living
// state. The grant carries a reference, never the referenced secret value.
func (service *Service) IssueSecretGrant(ctx context.Context, actor uuid.UUID,
	reference string, ttl time.Duration) (SecretGrant, error) {
	if actor == uuid.Nil || strings.TrimSpace(reference) == "" || ttl <= 0 || ttl > 15*time.Minute {
		return SecretGrant{}, fmt.Errorf("project: bounded actor-scoped secret grant is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	key, err := service.grantKey(ctx, actor)
	if err != nil {
		return SecretGrant{}, err
	}
	grant := SecretGrant{ID: uuid.New(), Reference: strings.TrimSpace(reference),
		ExpiresAt: service.clock.Now().UTC().Add(ttl)}
	grant.Token = signGrant(key, actor, grant)
	for index := range key {
		key[index] = 0
	}
	return grant, nil
}

func (service *Service) grantKey(ctx context.Context, actor uuid.UUID) ([]byte, error) {
	raw, err := service.store.LoadLivingState(ctx, grantKeyStateKind, actor.String())
	if err == nil {
		var state grantKeyState
		if json.Unmarshal(raw, &state) != nil || len(state.Key) != 32 {
			return nil, fmt.Errorf("project: invalid encrypted grant authority")
		}
		return append([]byte(nil), state.Key...), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(grantKeyState{Key: key})
	if err != nil {
		return nil, err
	}
	if err := service.store.SaveLivingState(ctx, grantKeyStateKind, actor.String(), encoded); err != nil {
		for index := range key {
			key[index] = 0
		}
		return nil, err
	}
	return key, nil
}

func signGrant(key []byte, actor uuid.UUID, grant SecretGrant) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(actor.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(grant.ID.String()))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(grant.Reference))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(grant.ExpiresAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (service *Service) verifyGrant(ctx context.Context, actor uuid.UUID, grant SecretGrant) error {
	if grant.ID == uuid.Nil || strings.TrimSpace(grant.Token) == "" ||
		!grant.ExpiresAt.After(service.clock.Now()) {
		return fmt.Errorf("project: secret grant is invalid or expired")
	}
	service.mu.Lock()
	key, err := service.grantKey(ctx, actor)
	service.mu.Unlock()
	if err != nil {
		return err
	}
	expected := signGrant(key, actor, grant)
	for index := range key {
		key[index] = 0
	}
	provided, err := hex.DecodeString(grant.Token)
	if err != nil || !hmac.Equal([]byte(expected), []byte(hex.EncodeToString(provided))) {
		return fmt.Errorf("project: secret grant signature is invalid")
	}
	return nil
}

func NewService(store *session.Store, clock types.Clock, config ServiceConfig) (*Service, error) {
	if store == nil || clock == nil {
		return nil, fmt.Errorf("project: encrypted store and clock are required")
	}
	root, archives, err := hostRoots(config.WorkspaceRoot, config.ArchiveRoot)
	if err != nil {
		return nil, err
	}
	attachRoots, err := resolveAllowedRoots(config.AttachRoots)
	if err != nil {
		return nil, err
	}
	importRoots, err := resolveAllowedRoots(config.ImportRoots)
	if err != nil {
		return nil, err
	}
	local := config.LocalHost
	if local == nil {
		local, err = NewLocalHost(LocalHostConfig{WorkspaceRoot: root, ArchiveRoot: archives})
		if err != nil {
			return nil, err
		}
	}
	hosts := map[HostKind]WorkspaceHost{HostDirectLocal: local}
	if config.ContainerHost != nil {
		hosts[HostContainer] = config.ContainerHost
	}
	providers := map[string]RepositoryProvider{}
	for name, provider := range config.RepositoryProviders {
		if provider == nil || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("project: repository provider registration is invalid")
		}
		providers[strings.ToLower(strings.TrimSpace(name))] = provider
	}
	service := &Service{store: store, clock: clock, workspaceRoot: root, archiveRoot: archives,
		attachRoots: attachRoots, importRoots: importRoots, allowFileClone: config.AllowFileClone,
		hosts: hosts, repositoryProviders: providers, gitCredentialBroker: config.GitCredentialBroker}
	service.intelligence = newIntelligence(store, clock, service)
	service.editing = newEditingService(store, clock, service)
	service.terminals = newTerminalService(store, clock, service, config.Control)
	service.runtimes = newRuntimeService(store, clock, service)
	service.verification = newVerificationService(store, clock, service)
	deliveryRoot := strings.TrimSpace(config.DeliveryRoot)
	if deliveryRoot == "" {
		deliveryRoot = filepath.Join(archives, "delivery")
	}
	service.delivery, err = newDeliveryService(store, clock, service, deliveryRoot,
		config.ResourceAdapters, config.DeploymentAdapters)
	if err != nil {
		return nil, err
	}
	return service, nil
}

func (service *Service) SetEmitter(emitter EventEmitter) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.emitter = emitter
}

// Close releases host-local monitors. Durable workspace state and containers
// remain available for restart reconciliation.
func (service *Service) Close() error {
	service.mu.Lock()
	terminals, runtimes, delivery := service.terminals, service.runtimes, service.delivery
	hosts := make([]WorkspaceHost, 0, len(service.hosts))
	for _, host := range service.hosts {
		hosts = append(hosts, host)
	}
	service.mu.Unlock()
	var result error
	if terminals != nil {
		result = errors.Join(result, terminals.Close())
	}
	if runtimes != nil {
		result = errors.Join(result, runtimes.Close())
	}
	if delivery != nil {
		result = errors.Join(result, delivery.Close())
	}
	for _, host := range hosts {
		if closer, ok := host.(interface{ Close() error }); ok {
			result = errors.Join(result, closer.Close())
		}
	}
	return result
}

func (service *Service) Capabilities(ctx context.Context) []HostCapabilities {
	service.mu.Lock()
	hosts := make([]WorkspaceHost, 0, len(service.hosts))
	for _, host := range service.hosts {
		hosts = append(hosts, host)
	}
	service.mu.Unlock()
	result := make([]HostCapabilities, 0, len(hosts)+1)
	for _, host := range hosts {
		result = append(result, host.Capabilities(ctx))
	}
	if _, ok := service.hosts[HostRemote]; !ok {
		result = append(result, HostCapabilities{Version: WorkspaceHostVersion,
			Kind: HostRemote, Available: false, Reason: "no authorized remote worker is configured",
			Domains: capabilities(nil), Network: NetworkPolicy{Mode: "deny"}, RootConfined: true, NonRoot: true})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	return result
}

func (service *Service) List(ctx context.Context, actor uuid.UUID) ([]Project, uint64, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return nil, 0, err
	}
	projects := append([]Project{}, state.Projects...)
	sort.Slice(projects, func(i, j int) bool { return projects[i].UpdatedAt.After(projects[j].UpdatedAt) })
	return projects, state.Revision, nil
}

func (service *Service) Get(ctx context.Context, actor, projectID uuid.UUID) (Project, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return Project{}, err
	}
	project, _, ok := findProject(state.Projects, projectID)
	if !ok {
		return Project{}, ErrNotFound
	}
	return project, nil
}

// ObserveWorkspaceChange advances the authoritative project revision after a
// project-bound agent tool mutates the WorkspaceHost root. Generic agent tools
// previously wrote successfully without informing the registry, leaving stack
// discovery, optimistic revisions, and every Studio projection stale.
func (service *Service) ObserveWorkspaceChange(
	ctx context.Context,
	actor uuid.UUID,
	projectID uuid.UUID,
) (Project, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, actor)
	if err != nil {
		return Project{}, err
	}
	project, index, ok := findProject(state.Projects, projectID)
	if !ok {
		return Project{}, ErrNotFound
	}
	if err := validateExistingRoot(project.Root); err != nil {
		return Project{}, err
	}
	project.WorkspaceRevision++
	project.StackSignals = discoverStack(project.Root)
	project.DefaultBranch = discoverBranch(ctx, project.Root)
	project.UpdatedAt = service.clock.Now().UTC()
	project.LastError = ""
	state.Projects[index] = project
	if err := service.save(ctx, actor, &state); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (service *Service) CreateTemplate(ctx context.Context, meta OperationMeta, input TemplateInput) (Project, error) {
	input.Template = strings.TrimSpace(input.Template)
	if input.Template == "" {
		input.Template = "empty"
	}
	if input.Template != "empty" && input.Template != "go-cli" && input.Template != "static-web" {
		return Project{}, fmt.Errorf("project: unknown curated template")
	}
	return service.createManaged(ctx, meta, "template", input.Name, input.Host, input.Trust,
		SourceTemplate, input.Template, func(root string) error { return writeTemplate(root, input.Template) })
}

func (service *Service) ImportArchive(ctx context.Context, meta OperationMeta, input ArchiveInput) (Project, error) {
	archive, err := service.allowedExistingFile(input.ArchivePath, service.importRoots)
	if err != nil {
		return Project{}, err
	}
	if strings.ToLower(filepath.Ext(archive)) != ".zip" {
		return Project{}, fmt.Errorf("project: only bounded zip imports are currently supported")
	}
	return service.createManaged(ctx, meta, "archive", input.Name, input.Host, input.Trust,
		SourceArchive, archive, func(root string) error { return extractZip(ctx, archive, root) })
}

func (service *Service) AttachDirectory(ctx context.Context, meta OperationMeta, input AttachInput) (Project, error) {
	if err := meta.validate(service.clock.Now()); err != nil {
		return Project{}, err
	}
	root, err := service.allowedExistingDirectory(input.Directory, service.attachRoots)
	if err != nil {
		return Project{}, err
	}
	name, trust, err := normalizeIdentity(input.Name, input.Trust)
	if err != nil {
		return Project{}, err
	}
	hash := requestHash("attach", input)
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := service.load(ctx, meta.ActorID)
	if err != nil {
		return Project{}, err
	}
	if found, err := receiptResult(state, meta.IdempotencyKey, hash); found.ID != uuid.Nil || err != nil {
		return found, err
	}
	if err := service.assertRootUnclaimed(ctx, meta.ActorID, root); err != nil {
		return Project{}, err
	}
	now := service.clock.Now().UTC()
	project := Project{ID: uuid.New(), Name: name, Root: root, Source: SourceDirectory,
		SourceReference: root, StackSignals: discoverStack(root), Trust: trust,
		WorkspaceRevision: 1, Host: HostDirectLocal, Managed: false,
		Lifecycle: LifecycleReady, CreatedAt: now, UpdatedAt: now,
		DefaultBranch: discoverBranch(ctx, root)}
	if err := appendProject(&state, project); err != nil {
		return Project{}, err
	}
	appendReceipt(&state, meta.IdempotencyKey, hash, project, now)
	if err := service.save(ctx, meta.ActorID, &state); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (service *Service) CloneRepository(ctx context.Context, meta OperationMeta, input CloneInput) (Project, error) {
	if !input.Authorized {
		return Project{}, fmt.Errorf("project: repository clone requires explicit authorization")
	}
	repository, err := service.validateRepositoryURL(input.RepositoryURL)
	if err != nil {
		return Project{}, err
	}
	if strings.TrimSpace(input.CredentialReference) != "" {
		validGrant := false
		for _, grant := range meta.SecretGrants {
			if grant.Reference == input.CredentialReference && service.verifyGrant(ctx, meta.ActorID, grant) == nil {
				validGrant = true
			}
		}
		if !validGrant {
			return Project{}, fmt.Errorf("project: repository credential requires a matching short-lived grant")
		}
	}
	branch := strings.TrimSpace(input.DefaultBranch)
	return service.createManaged(ctx, meta, "clone", input.Name, input.Host, input.Trust,
		SourceRepository, repository, func(root string) error {
			arguments := []string{"clone", "--no-tags", "--", repository, root}
			if branch != "" {
				arguments = []string{"clone", "--no-tags", "--branch", branch, "--single-branch", "--", repository, root}
			}
			command := exec.CommandContext(ctx, "git", arguments...)
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1"}
			output, cloneErr := command.CombinedOutput()
			if len(output) > 64<<10 {
				output = output[:64<<10]
			}
			if cloneErr != nil {
				return fmt.Errorf("project: authorized clone failed: %w: %s", cloneErr, strings.TrimSpace(string(output)))
			}
			return nil
		})
}

func (service *Service) Lifecycle(ctx context.Context, meta OperationMeta, input LifecycleInput) (Project, error) {
	if err := meta.validate(service.clock.Now()); err != nil {
		return Project{}, err
	}
	if input.ProjectID == uuid.Nil || !input.Operation.valid() || input.Operation == HostProvision {
		return Project{}, ErrInvalidEnvelope
	}
	hash := requestHash("lifecycle", input)
	service.mu.Lock()
	state, err := service.load(ctx, meta.ActorID)
	if err != nil {
		service.mu.Unlock()
		return Project{}, err
	}
	if found, receiptErr := receiptResult(state, meta.IdempotencyKey, hash); found.ID != uuid.Nil || receiptErr != nil {
		service.mu.Unlock()
		return found, receiptErr
	}
	project, index, ok := findProject(state.Projects, input.ProjectID)
	if !ok {
		service.mu.Unlock()
		return Project{}, ErrNotFound
	}
	if meta.ExpectedRevision != nil && *meta.ExpectedRevision != project.WorkspaceRevision {
		service.mu.Unlock()
		return Project{}, ErrStaleRevision
	}
	host := service.hosts[project.Host]
	emitter := service.emitter
	service.mu.Unlock()
	if host == nil {
		return Project{}, ErrUnsupported
	}
	runCtx, envelope, cancel, err := service.envelope(ctx, meta, project, input.Operation, input)
	if err != nil {
		return Project{}, err
	}
	defer cancel()
	if err := emitLifecycle(ctx, emitter, envelope, "queued", "operation accepted"); err != nil {
		return Project{}, err
	}
	if err := emitLifecycle(ctx, emitter, envelope, "started", "workspace host operation started"); err != nil {
		return Project{}, err
	}
	result, executeErr := host.Execute(runCtx, project, envelope)
	if executeErr != nil {
		stateName := "failed"
		if errors.Is(executeErr, context.Canceled) || errors.Is(executeErr, context.DeadlineExceeded) {
			stateName = "cancelled"
		}
		_ = emitLifecycle(context.Background(), emitter, envelope, stateName, executeErr.Error())
		return Project{}, executeErr
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err = service.load(ctx, meta.ActorID)
	if err != nil {
		return Project{}, err
	}
	latest, latestIndex, ok := findProject(state.Projects, input.ProjectID)
	if !ok || latest.WorkspaceRevision != project.WorkspaceRevision || latestIndex != index {
		return Project{}, ErrStaleRevision
	}
	latest.WorkspaceRevision++
	latest.Lifecycle = result.State
	if input.Operation == HostArchive {
		latest.LatestArchive = result.HostReference
		latest.HostReference = project.HostReference
	} else if result.HostReference != "" {
		latest.HostReference = result.HostReference
	} else {
		latest.HostReference = project.HostReference
	}
	latest.UpdatedAt = service.clock.Now().UTC()
	latest.LastError = ""
	if input.Operation == HostDestroy {
		state.Projects = append(state.Projects[:latestIndex], state.Projects[latestIndex+1:]...)
	} else {
		state.Projects[latestIndex] = latest
	}
	appendReceipt(&state, meta.IdempotencyKey, hash, latest, latest.UpdatedAt)
	if err := service.save(ctx, meta.ActorID, &state); err != nil {
		return Project{}, err
	}
	if err := emitLifecycle(ctx, emitter, envelope, "completed", result.Message); err != nil {
		return Project{}, err
	}
	return latest, nil
}

func (service *Service) createManaged(ctx context.Context, meta OperationMeta, action, requestedName string,
	hostKind HostKind, requestedTrust TrustState, source SourceKind, reference string,
	populate func(string) error) (Project, error) {
	if err := meta.validate(service.clock.Now()); err != nil {
		return Project{}, err
	}
	if hostKind == "" {
		hostKind = HostDirectLocal
	}
	name, trust, err := normalizeIdentity(requestedName, requestedTrust)
	if err != nil {
		return Project{}, err
	}
	hash := requestHash(action, map[string]any{"name": name, "host": hostKind, "trust": trust,
		"source": source, "reference": reference})
	service.mu.Lock()
	state, err := service.load(ctx, meta.ActorID)
	if err != nil {
		service.mu.Unlock()
		return Project{}, err
	}
	if found, receiptErr := receiptResult(state, meta.IdempotencyKey, hash); found.ID != uuid.Nil || receiptErr != nil {
		service.mu.Unlock()
		return found, receiptErr
	}
	host := service.hosts[hostKind]
	if host == nil {
		service.mu.Unlock()
		return Project{}, ErrUnsupported
	}
	projectID := uuid.New()
	root := filepath.Join(service.workspaceRoot, meta.ActorID.String(), projectID.String())
	now := service.clock.Now().UTC()
	project := Project{ID: projectID, Name: name, Root: root, Source: source,
		SourceReference: safeSourceReference(source, reference), Trust: trust,
		WorkspaceRevision: 1, Host: hostKind, Managed: true,
		Lifecycle: LifecycleProvisioning, CreatedAt: now, UpdatedAt: now}
	if err := appendProject(&state, project); err != nil {
		service.mu.Unlock()
		return Project{}, err
	}
	if err := service.save(ctx, meta.ActorID, &state); err != nil {
		service.mu.Unlock()
		return Project{}, err
	}
	emitter := service.emitter
	service.mu.Unlock()

	runCtx, envelope, cancel, err := service.envelope(ctx, meta, project, HostProvision, map[string]any{"source": source})
	if err != nil {
		return Project{}, err
	}
	defer cancel()
	if err := emitLifecycle(ctx, emitter, envelope, "queued", "project creation accepted"); err != nil {
		return Project{}, err
	}
	if err := emitLifecycle(ctx, emitter, envelope, "started", "preparing managed workspace"); err != nil {
		return Project{}, err
	}
	staging := root + ".staging-" + meta.RequestID.String()
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return Project{}, service.failCreate(ctx, meta.ActorID, project, envelope, emitter, err)
	}
	if err := os.Mkdir(staging, 0o700); err != nil {
		return Project{}, service.failCreate(ctx, meta.ActorID, project, envelope, emitter, err)
	}
	if err := populate(staging); err != nil {
		_ = os.RemoveAll(staging)
		return Project{}, service.failCreate(ctx, meta.ActorID, project, envelope, emitter, err)
	}
	if err := os.Rename(staging, root); err != nil {
		_ = os.RemoveAll(staging)
		return Project{}, service.failCreate(ctx, meta.ActorID, project, envelope, emitter, err)
	}
	project.StackSignals = discoverStack(root)
	project.DefaultBranch = discoverBranch(ctx, root)
	if err := emitLifecycle(ctx, emitter, envelope, "progress", "workspace content prepared"); err != nil {
		return Project{}, err
	}
	result, err := host.Execute(runCtx, project, envelope)
	if err != nil {
		return Project{}, service.failCreate(ctx, meta.ActorID, project, envelope, emitter, err)
	}
	project.Lifecycle, project.HostReference = result.State, result.HostReference
	project.WorkspaceRevision++
	project.UpdatedAt = service.clock.Now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err = service.load(ctx, meta.ActorID)
	if err != nil {
		return Project{}, err
	}
	_, index, ok := findProject(state.Projects, project.ID)
	if !ok || state.Projects[index].WorkspaceRevision != 1 {
		return Project{}, ErrStaleRevision
	}
	state.Projects[index] = project
	appendReceipt(&state, meta.IdempotencyKey, hash, project, project.UpdatedAt)
	if err := service.save(ctx, meta.ActorID, &state); err != nil {
		return Project{}, err
	}
	if err := emitLifecycle(ctx, emitter, envelope, "completed", result.Message); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (service *Service) failCreate(ctx context.Context, actor uuid.UUID, project Project,
	envelope OperationEnvelope, emitter EventEmitter, failure error) error {
	service.mu.Lock()
	state, loadErr := service.load(context.Background(), actor)
	if loadErr == nil {
		if _, index, ok := findProject(state.Projects, project.ID); ok {
			state.Projects[index].Lifecycle = LifecycleFailed
			state.Projects[index].LastError = safeError(failure)
			state.Projects[index].WorkspaceRevision++
			state.Projects[index].UpdatedAt = service.clock.Now().UTC()
			loadErr = service.save(context.Background(), actor, &state)
		}
	}
	service.mu.Unlock()
	stateName := "failed"
	if errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded) {
		stateName = "cancelled"
	}
	_ = emitLifecycle(context.Background(), emitter, envelope, stateName, safeError(failure))
	return errors.Join(failure, loadErr)
}

func (service *Service) ReconcileAll(ctx context.Context) error {
	if err := service.reconcileGitMutations(ctx); err != nil {
		return err
	}
	if err := service.editing.ReconcileAll(ctx); err != nil {
		return err
	}
	if err := service.terminals.ReconcileAll(ctx); err != nil {
		return err
	}
	if err := service.runtimes.ReconcileAll(ctx); err != nil {
		return err
	}
	if err := service.reconcileGitPreviews(ctx); err != nil {
		return err
	}
	states, err := service.store.ListLivingStates(ctx, registryStateKind)
	if err != nil {
		return err
	}
	for _, encrypted := range states {
		actor, parseErr := uuid.Parse(encrypted.Scope)
		if parseErr != nil {
			return fmt.Errorf("project: invalid registry actor key")
		}
		var state registry
		if err := json.Unmarshal(encrypted.State, &state); err != nil {
			return fmt.Errorf("project: decode registry for reconciliation: %w", err)
		}
		changed := false
		for index, project := range state.Projects {
			host := service.hosts[project.Host]
			if host == nil {
				state.Projects[index].Lifecycle = LifecycleFailed
				state.Projects[index].LastError = "configured workspace host is unavailable"
				changed = true
				continue
			}
			result, reconcileErr := host.Reconcile(ctx, project)
			if reconcileErr != nil {
				state.Projects[index].Lifecycle = LifecycleFailed
				state.Projects[index].LastError = safeError(reconcileErr)
				changed = true
				continue
			}
			if result.State != project.Lifecycle || result.HostReference != "" && result.HostReference != project.HostReference {
				state.Projects[index].Lifecycle = result.State
				if result.HostReference != "" {
					state.Projects[index].HostReference = result.HostReference
				}
				state.Projects[index].WorkspaceRevision++
				state.Projects[index].UpdatedAt = service.clock.Now().UTC()
				state.Projects[index].LastError = ""
				changed = true
			}
		}
		if changed {
			if err := service.save(ctx, actor, &state); err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *Service) envelope(ctx context.Context, meta OperationMeta, project Project,
	operation HostOperation, payload any) (context.Context, OperationEnvelope, context.CancelFunc, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, OperationEnvelope{}, nil, err
	}
	envelope := OperationEnvelope{Version: WorkspaceHostVersion, Operation: operation,
		ActorID: meta.ActorID, ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		RequestID: meta.RequestID, IdempotencyKey: meta.IdempotencyKey,
		PolicyClassification: meta.PolicyClassification, Deadline: meta.Deadline,
		CorrelationID: meta.CorrelationID, SecretGrants: append([]SecretGrant(nil), meta.SecretGrants...), Payload: raw}
	if err := envelope.Validate(service.clock.Now()); err != nil {
		return nil, OperationEnvelope{}, nil, err
	}
	deadline := meta.Deadline
	if existing, ok := ctx.Deadline(); ok && existing.Before(deadline) {
		deadline = existing
	}
	runCtx, cancel := context.WithDeadline(ctx, deadline)
	return runCtx, envelope, cancel, nil
}

func emitLifecycle(ctx context.Context, emitter EventEmitter, envelope OperationEnvelope, state, message string) error {
	if emitter == nil {
		return nil
	}
	return emitter.EmitProjectEvent(ctx, LifecycleEvent{State: state, Operation: envelope.Operation,
		ActorID: envelope.ActorID, ProjectID: envelope.ProjectID,
		WorkspaceRevision: envelope.WorkspaceRevision, RequestID: envelope.RequestID,
		CorrelationID: envelope.CorrelationID, Message: message})
}

func (service *Service) load(ctx context.Context, actor uuid.UUID) (registry, error) {
	if actor == uuid.Nil {
		return registry{}, fmt.Errorf("project: actor is required")
	}
	raw, err := service.store.LoadLivingState(ctx, registryStateKind, actor.String())
	if errors.Is(err, sql.ErrNoRows) {
		return registry{}, nil
	}
	if err != nil {
		return registry{}, err
	}
	var state registry
	if err := json.Unmarshal(raw, &state); err != nil {
		return registry{}, fmt.Errorf("project: decode registry: %w", err)
	}
	return state, nil
}

func (service *Service) save(ctx context.Context, actor uuid.UUID, state *registry) error {
	state.Revision++
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, registryStateKind, actor.String(), raw)
}

func appendProject(state *registry, project Project) error {
	if len(state.Projects) >= maxProjects {
		return fmt.Errorf("project: project limit reached")
	}
	state.Projects = append(state.Projects, project)
	return nil
}

func findProject(projects []Project, id uuid.UUID) (Project, int, bool) {
	for index := range projects {
		if projects[index].ID == id {
			return projects[index], index, true
		}
	}
	return Project{}, -1, false
}

func requestHash(operation string, input any) string {
	raw, _ := json.Marshal(input)
	digest := sha256.Sum256(append([]byte(operation+"\x00"), raw...))
	return hex.EncodeToString(digest[:])
}

func receiptResult(state registry, key, hash string) (Project, error) {
	for _, receipt := range state.Receipts {
		if receipt.Key != key {
			continue
		}
		if receipt.Hash != hash {
			return Project{}, ErrConflict
		}
		return receipt.Project, nil
	}
	return Project{}, nil
}

func appendReceipt(state *registry, key, hash string, project Project, now time.Time) {
	state.Receipts = append(state.Receipts, mutationReceipt{Key: key, Hash: hash, Project: project, CreatedAt: now})
	if len(state.Receipts) > maxReceipts {
		state.Receipts = append([]mutationReceipt(nil), state.Receipts[len(state.Receipts)-maxReceipts:]...)
	}
}

func normalizeIdentity(name string, trust TrustState) (string, TrustState, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxProjectNameLen || strings.ContainsAny(name, "\x00\r\n") {
		return "", "", fmt.Errorf("project: bounded project name is required")
	}
	if trust == "" {
		trust = TrustUntrusted
	}
	if trust != TrustUntrusted && trust != TrustReviewed && trust != TrustTrusted {
		return "", "", fmt.Errorf("project: invalid trust state")
	}
	return name, trust, nil
}

func resolveAllowedRoots(paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		root, err := secureRoot(path)
		if err != nil {
			return nil, err
		}
		result = append(result, root)
	}
	return result, nil
}

func (service *Service) allowedExistingDirectory(path string, roots []string) (string, error) {
	resolved, err := resolveExisting(path)
	if err != nil {
		return "", err
	}
	if err := validateExistingRoot(resolved); err != nil {
		return "", err
	}
	for _, root := range roots {
		if pathWithin(root, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("project: directory is outside configured attach roots")
}

func (service *Service) allowedExistingFile(path string, roots []string) (string, error) {
	resolved, err := resolveExisting(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxArchiveBytes {
		return "", fmt.Errorf("project: import archive is unavailable or oversized")
	}
	for _, root := range roots {
		if pathWithin(root, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("project: archive is outside configured import roots")
}

func resolveExisting(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("project: valid path is required")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func (service *Service) assertRootUnclaimed(ctx context.Context, actor uuid.UUID, root string) error {
	states, err := service.store.ListLivingStates(ctx, registryStateKind)
	if err != nil {
		return err
	}
	for _, encrypted := range states {
		var state registry
		if err := json.Unmarshal(encrypted.State, &state); err != nil {
			return err
		}
		for _, existing := range state.Projects {
			if existing.Root == root {
				if encrypted.Scope == actor.String() {
					return fmt.Errorf("project: directory is already attached")
				}
				return fmt.Errorf("project: directory belongs to another actor")
			}
		}
	}
	return nil
}

func (service *Service) validateRepositoryURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("project: repository URL must not contain credentials or fragments")
	}
	switch parsed.Scheme {
	case "file":
		if !service.allowFileClone {
			return "", fmt.Errorf("project: local repository clone is not enabled")
		}
		resolved, err := resolveExisting(parsed.Path)
		if err != nil {
			return "", err
		}
		return (&url.URL{Scheme: "file", Path: resolved}).String(), nil
	case "https":
		host := strings.TrimSpace(parsed.Hostname())
		if host == "" || strings.EqualFold(host, "localhost") {
			return "", fmt.Errorf("project: repository host is not allowed")
		}
		if address := net.ParseIP(host); address != nil &&
			(address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified()) {
			return "", fmt.Errorf("project: private repository address requires an integration broker")
		}
		return parsed.String(), nil
	default:
		return "", fmt.Errorf("project: only HTTPS authorized repositories are supported")
	}
}

func safeSourceReference(kind SourceKind, reference string) string {
	if kind != SourceRepository {
		return reference
	}
	parsed, err := url.Parse(reference)
	if err != nil {
		return "repository"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	return parsed.String()
}

func writeTemplate(root, name string) error {
	files := map[string]string{}
	switch name {
	case "empty":
		files["README.md"] = "# New project\n"
	case "go-cli":
		files["go.mod"] = "module example.invalid/project\n\ngo 1.24\n"
		files["main.go"] = "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"Hello from Ion\") }\n"
	case "static-web":
		files["index.html"] = "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><link rel=\"icon\" href=\"data:,\"><title>New project</title><h1>Built with Ion</h1>\n"
		files["package.json"] = "{\"private\":true,\"scripts\":{\"test\":\"node --test test.mjs\"}}\n"
		files["test.mjs"] = "import assert from 'node:assert/strict'\nimport { readFile } from 'node:fs/promises'\n\nconst page = await readFile(new URL('./index.html', import.meta.url), 'utf8')\nassert.match(page, /<meta name=\"viewport\"/)\nassert.match(page, /Built with Ion/)\n"
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func extractZip(ctx context.Context, archive, root string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	if len(reader.File) > maxArchiveFiles {
		return fmt.Errorf("project: archive file-count limit exceeded")
	}
	var total uint64
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if file.Mode()&os.ModeSymlink != 0 || file.UncompressedSize64 > maxArchiveBytes {
			return fmt.Errorf("project: archive contains a symlink or oversized entry")
		}
		total += file.UncompressedSize64
		if total > maxArchiveBytes {
			return fmt.Errorf("project: archive expansion limit exceeded")
		}
		clean := filepath.Clean(filepath.FromSlash(file.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("project: archive traversal rejected")
		}
		target := filepath.Join(root, clean)
		if !pathWithin(root, target) {
			return fmt.Errorf("project: archive traversal rejected")
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if !file.Mode().IsRegular() {
			return fmt.Errorf("project: archive contains an unsupported entry")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = source.Close()
			return err
		}
		_, copyErr := io.Copy(destination, io.LimitReader(source, int64(file.UncompressedSize64)+1))
		closeDestinationErr := destination.Close()
		closeSourceErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeDestinationErr != nil {
			return closeDestinationErr
		}
		if closeSourceErr != nil {
			return closeSourceErr
		}
	}
	return nil
}

func discoverStack(root string) []string {
	signals := map[string]string{"go.mod": "go", "package.json": "node",
		"pyproject.toml": "python", "requirements.txt": "python", "Cargo.toml": "rust",
		"pom.xml": "java-maven", "build.gradle": "java-gradle", "Dockerfile": "container"}
	result := []string{}
	for file, signal := range signals {
		if info, err := os.Stat(filepath.Join(root, file)); err == nil && info.Mode().IsRegular() {
			result = append(result, signal)
		}
	}
	sort.Strings(result)
	return result
}

func discoverBranch(ctx context.Context, root string) string {
	command := exec.CommandContext(ctx, "git", "-C", root, "symbolic-ref", "--quiet", "--short", "HEAD")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	output, err := command.Output()
	if err != nil || len(output) > 256 {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
