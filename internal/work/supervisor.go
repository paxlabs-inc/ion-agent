package work

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
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	supervisorKind         = "work_supervisor_v1"
	MaxParallelSpecialists = 32
	LaunchParallelMinimum  = 20
)

// Specialist identifies a bounded professional workstream. It is data, not a
// role-play prompt: authority is carried separately by Scope and Tools.
type Specialist string

const (
	SpecialistDiscovery      Specialist = "discovery"
	SpecialistExploration    Specialist = "exploration"
	SpecialistImplementation Specialist = "implementation"
	SpecialistTest           Specialist = "test"
	SpecialistSecurity       Specialist = "security"
	SpecialistData           Specialist = "data"
	SpecialistFrontend       Specialist = "frontend"
	SpecialistPerformance    Specialist = "performance"
	SpecialistOperations     Specialist = "operations"
	SpecialistReview         Specialist = "review"
)

var validSpecialists = map[Specialist]struct{}{
	SpecialistDiscovery: {}, SpecialistExploration: {},
	SpecialistImplementation: {}, SpecialistTest: {},
	SpecialistSecurity: {}, SpecialistData: {}, SpecialistFrontend: {},
	SpecialistPerformance: {}, SpecialistOperations: {}, SpecialistReview: {},
}

type SupervisorStatus string

const (
	SupervisorQueued         SupervisorStatus = "queued"
	SupervisorWorking        SupervisorStatus = "working"
	SupervisorWaiting        SupervisorStatus = "waiting"
	SupervisorBlocked        SupervisorStatus = "blocked"
	SupervisorPaused         SupervisorStatus = "paused"
	SupervisorCancelled      SupervisorStatus = "cancelled"
	SupervisorCompleted      SupervisorStatus = "completed"
	SupervisorOutcomeUnknown SupervisorStatus = "outcome_unknown"
)

type SpecialistStatus string

const (
	SpecialistPending         SpecialistStatus = "pending"
	SpecialistReady           SpecialistStatus = "ready"
	SpecialistRunning         SpecialistStatus = "running"
	SpecialistWaitingEvidence SpecialistStatus = "waiting_evidence"
	SpecialistRetrying        SpecialistStatus = "retrying"
	SpecialistBlocked         SpecialistStatus = "blocked"
	SpecialistCancelled       SpecialistStatus = "cancelled"
	SpecialistCompleted       SpecialistStatus = "completed"
	SpecialistOutcomeUnknown  SpecialistStatus = "outcome_unknown"
)

// AuthorityScope is the complete least-authority boundary for one specialist.
// External effects are denied unless an accepted parent plan sets them true.
type AuthorityScope struct {
	ReadFiles       []string `json:"read_files,omitempty"`
	WriteFiles      []string `json:"write_files,omitempty"`
	Services        []string `json:"services,omitempty"`
	EnvironmentKeys []string `json:"environment_keys,omitempty"`
	NetworkHosts    []string `json:"network_hosts,omitempty"`
	ExternalEffects bool     `json:"external_effects"`
}

type SpecialistBudget struct {
	MaxTokens        int64 `json:"max_tokens"`
	MaxCostCents     int64 `json:"max_cost_cents"`
	MaxToolCalls     int   `json:"max_tool_calls"`
	MaxWallSeconds   int   `json:"max_wall_seconds"`
	MaxProcesses     int   `json:"max_processes"`
	MaxStorageBytes  int64 `json:"max_storage_bytes"`
	MaxNetworkBytes  int64 `json:"max_network_bytes"`
	MaxProviderCents int64 `json:"max_provider_cents"`
	MaxRetries       int   `json:"max_retries"`
}

type SupervisorBudget struct {
	SpecialistBudget
	MaxParallel int `json:"max_parallel"`
}

type BudgetUsage struct {
	Tokens             int64 `json:"tokens"`
	CostCents          int64 `json:"cost_cents"`
	CostKnown          bool  `json:"cost_known"`
	ToolCalls          int   `json:"tool_calls"`
	WallSeconds        int   `json:"wall_seconds"`
	Processes          int   `json:"processes"`
	StorageBytes       int64 `json:"storage_bytes"`
	NetworkBytes       int64 `json:"network_bytes"`
	ProviderCents      int64 `json:"provider_cents"`
	ProviderSpendKnown bool  `json:"provider_spend_known"`
	Retries            int   `json:"retries"`
}

type TaskOverride struct {
	WorkItemID string           `json:"work_item_id"`
	Specialist Specialist       `json:"specialist"`
	Scope      AuthorityScope   `json:"scope"`
	Tools      []string         `json:"tools"`
	Budget     SpecialistBudget `json:"budget"`
}

// TaskPacket is the immutable contract passed to one specialist attempt.
type TaskPacket struct {
	ID              string           `json:"id"`
	SupervisorID    uuid.UUID        `json:"supervisor_id"`
	ActorID         uuid.UUID        `json:"actor_id"`
	SessionID       *uuid.UUID       `json:"session_id,omitempty"`
	ContractID      uuid.UUID        `json:"contract_id"`
	WorkItemID      string           `json:"work_item_id"`
	Title           string           `json:"title"`
	Criteria        []string         `json:"criteria"`
	DependsOn       []string         `json:"depends_on,omitempty"`
	Specialist      Specialist       `json:"specialist"`
	Scope           AuthorityScope   `json:"scope"`
	Tools           []string         `json:"tools"`
	Budget          SpecialistBudget `json:"budget"`
	Deliverables    []string         `json:"deliverables"`
	Evidence        []string         `json:"evidence_required"`
	ParentAuthority []string         `json:"parent_authority_required,omitempty"`
}

type Attempt struct {
	ID              uuid.UUID        `json:"id"`
	Number          int              `json:"number"`
	Status          SpecialistStatus `json:"status"`
	StartedAt       time.Time        `json:"started_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
	WorkerID        string           `json:"worker_id,omitempty"`
	Summary         string           `json:"summary,omitempty"`
	Error           string           `json:"error,omitempty"`
	Usage           BudgetUsage      `json:"usage"`
	Artifacts       []string         `json:"artifacts,omitempty"`
	Findings        []Finding        `json:"findings,omitempty"`
	ExternalEffects []ExternalEffect `json:"external_effects,omitempty"`
	ProcessIDs      []int            `json:"process_ids,omitempty"`
}

type Finding struct {
	Kind       string   `json:"kind"`
	Summary    string   `json:"summary"`
	Evidence   []string `json:"evidence,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
}

type ExternalEffect struct {
	Kind           string `json:"kind"`
	Target         string `json:"target"`
	IdempotencyKey string `json:"idempotency_key"`
	State          string `json:"state"`
}

type SpecialistTask struct {
	Packet         TaskPacket       `json:"packet"`
	Status         SpecialistStatus `json:"status"`
	Progress       int              `json:"progress"`
	Attempts       []Attempt        `json:"attempts,omitempty"`
	BlockingReason string           `json:"blocking_reason,omitempty"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type ScopeLease struct {
	ID        uuid.UUID `json:"id"`
	TaskID    string    `json:"task_id"`
	Resource  string    `json:"resource"`
	Mode      string    `json:"mode"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Reconciliation struct {
	At              time.Time `json:"at"`
	FileSystem      string    `json:"file_system"`
	Git             string    `json:"git"`
	Processes       string    `json:"processes"`
	Provider        string    `json:"provider"`
	EventHistory    string    `json:"event_history"`
	ExternalEffects string    `json:"external_effects"`
	RecoveredTasks  int       `json:"recovered_tasks"`
	UncertainTasks  int       `json:"uncertain_tasks"`
}

type SteeringRecord struct {
	At          time.Time `json:"at"`
	Instruction string    `json:"instruction"`
}

type SupervisorRun struct {
	ID              uuid.UUID        `json:"id"`
	ActorID         uuid.UUID        `json:"actor_id"`
	SessionID       *uuid.UUID       `json:"session_id,omitempty"`
	ContractID      uuid.UUID        `json:"contract_id"`
	ProjectID       *uuid.UUID       `json:"project_id,omitempty"`
	Status          SupervisorStatus `json:"status"`
	Revision        uint64           `json:"revision"`
	Tasks           []SpecialistTask `json:"tasks"`
	Leases          []ScopeLease     `json:"leases,omitempty"`
	Budget          SupervisorBudget `json:"budget"`
	Usage           BudgetUsage      `json:"usage"`
	Projection      BudgetUsage      `json:"projected_usage"`
	ProjectBudget   SupervisorBudget `json:"project_budget"`
	ProjectUsage    BudgetUsage      `json:"project_usage"`
	Steering        []SteeringRecord `json:"steering,omitempty"`
	Reconciliations []Reconciliation `json:"reconciliations,omitempty"`
	Synthesis       []Finding        `json:"synthesis,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
}

type SupervisorDocument struct {
	Revision uint64          `json:"revision"`
	Runs     []SupervisorRun `json:"runs"`
}

type SupervisorStartInput struct {
	ContractID    uuid.UUID         `json:"contract_id"`
	SessionID     *uuid.UUID        `json:"session_id,omitempty"`
	ProjectID     *uuid.UUID        `json:"project_id,omitempty"`
	Budget        SupervisorBudget  `json:"budget"`
	ProjectBudget *SupervisorBudget `json:"project_budget,omitempty"`
	Overrides     []TaskOverride    `json:"overrides,omitempty"`
}

type WorkerResult struct {
	AttemptID       uuid.UUID        `json:"attempt_id"`
	WorkerID        string           `json:"worker_id,omitempty"`
	Status          SpecialistStatus `json:"status"`
	Progress        int              `json:"progress"`
	Summary         string           `json:"summary"`
	Usage           BudgetUsage      `json:"usage"`
	Artifacts       []string         `json:"artifacts,omitempty"`
	Findings        []Finding        `json:"findings,omitempty"`
	ExternalEffects []ExternalEffect `json:"external_effects,omitempty"`
	ProcessIDs      []int            `json:"process_ids,omitempty"`
}

type SupervisorExecutor interface {
	Execute(context.Context, TaskPacket, uuid.UUID) (WorkerResult, error)
}

type SupervisorReconciler interface {
	Reconcile(
		context.Context, SupervisorRun, SpecialistTask, Attempt,
	) (Reconciliation, bool, error)
}

type SupervisorService struct {
	mu         sync.Mutex
	store      *session.Store
	clock      types.Clock
	work       *Service
	executor   SupervisorExecutor
	reconciler SupervisorReconciler
	running    map[uuid.UUID]context.CancelFunc
}

func NewSupervisorService(
	store *session.Store, clock types.Clock, work *Service,
) (*SupervisorService, error) {
	if store == nil || clock == nil || work == nil {
		return nil, fmt.Errorf("work supervisor: store, clock, and work service are required")
	}
	return &SupervisorService{
		store: store, clock: clock, work: work,
		reconciler: &localSupervisorReconciler{workspace: work.workspace},
		running:    make(map[uuid.UUID]context.CancelFunc),
	}, nil
}

func (service *SupervisorService) SetExecutor(executor SupervisorExecutor) {
	service.mu.Lock()
	service.executor = executor
	service.mu.Unlock()
}

func (service *SupervisorService) SetReconciler(reconciler SupervisorReconciler) {
	service.mu.Lock()
	service.reconciler = reconciler
	service.mu.Unlock()
}

func defaultSupervisorBudget() SupervisorBudget {
	return SupervisorBudget{SpecialistBudget: SpecialistBudget{
		MaxTokens: 400_000, MaxCostCents: 5_000, MaxToolCalls: 640,
		MaxWallSeconds: 7200, MaxProcesses: 32, MaxStorageBytes: 2 << 30,
		MaxNetworkBytes: 1 << 30, MaxProviderCents: 5_000, MaxRetries: 2,
	}, MaxParallel: LaunchParallelMinimum}
}

func normalizeSupervisorBudget(input SupervisorBudget) (SupervisorBudget, error) {
	if input.MaxParallel == 0 {
		input = defaultSupervisorBudget()
	}
	if input.MaxParallel < 1 || input.MaxParallel > MaxParallelSpecialists ||
		input.MaxTokens < 1 || input.MaxCostCents < 0 ||
		input.MaxToolCalls < 1 || input.MaxWallSeconds < 1 ||
		input.MaxProcesses < 1 || input.MaxStorageBytes < 0 ||
		input.MaxNetworkBytes < 0 || input.MaxProviderCents < 0 ||
		input.MaxRetries < 0 || input.MaxRetries > 10 {
		return SupervisorBudget{}, fmt.Errorf("work supervisor: budget is outside safe bounds")
	}
	return input, nil
}

func (service *SupervisorService) load(
	ctx context.Context, actor uuid.UUID,
) (SupervisorDocument, error) {
	raw, err := service.store.LoadLivingState(ctx, supervisorKind, actor.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SupervisorDocument{}, nil
		}
		return SupervisorDocument{}, err
	}
	var document SupervisorDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return SupervisorDocument{}, fmt.Errorf("work supervisor: decode state: %w", err)
	}
	return document, nil
}

func (service *SupervisorService) save(
	ctx context.Context, actor uuid.UUID, document *SupervisorDocument,
) error {
	document.Revision++
	raw, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("work supervisor: encode state: %w", err)
	}
	return service.store.SaveLivingState(ctx, supervisorKind, actor.String(), raw)
}

func (service *SupervisorService) Start(
	ctx context.Context, actor uuid.UUID, input SupervisorStartInput,
) (SupervisorRun, error) {
	if actor == uuid.Nil || input.ContractID == uuid.Nil {
		return SupervisorRun{}, fmt.Errorf("work supervisor: actor and contract are required")
	}
	budget, err := normalizeSupervisorBudget(input.Budget)
	if err != nil {
		return SupervisorRun{}, err
	}
	projectBudget := budget
	if input.ProjectBudget != nil {
		projectBudget, err = normalizeSupervisorBudget(*input.ProjectBudget)
		if err != nil {
			return SupervisorRun{}, fmt.Errorf("work supervisor: project %w", err)
		}
	}
	portfolio, err := service.work.Get(ctx, actor)
	if err != nil {
		return SupervisorRun{}, err
	}
	var contract *OutcomeContract
	for index := range portfolio.Contracts {
		if portfolio.Contracts[index].ID == input.ContractID {
			contract = &portfolio.Contracts[index]
			break
		}
	}
	if contract == nil || contract.Status == StatusCompleted ||
		contract.Status == StatusCancelled {
		return SupervisorRun{}, fmt.Errorf("work supervisor: active outcome contract not found")
	}
	items := make([]WorkItem, 0)
	for _, item := range portfolio.WorkItems {
		if item.ContractID == contract.ID && item.Status != WorkItemCompleted {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return SupervisorRun{}, fmt.Errorf("work supervisor: contract has no unfinished work")
	}
	overrides := make(map[string]TaskOverride, len(input.Overrides))
	for _, override := range input.Overrides {
		id := strings.TrimSpace(override.WorkItemID)
		if id == "" {
			return SupervisorRun{}, fmt.Errorf("work supervisor: override work item is required")
		}
		if _, ok := validSpecialists[override.Specialist]; !ok {
			return SupervisorRun{}, fmt.Errorf("work supervisor: specialist %q is invalid", override.Specialist)
		}
		overrides[id] = override
	}
	now := service.clock.Now().UTC()
	run := SupervisorRun{
		ID: uuid.New(), ActorID: actor, SessionID: input.SessionID,
		ContractID: input.ContractID, ProjectID: input.ProjectID,
		Status: SupervisorQueued, Budget: budget, ProjectBudget: projectBudget,
		CreatedAt: now, UpdatedAt: now,
	}
	allocationBudget := budget
	allocationBudget.MaxParallel = min(budget.MaxParallel, len(items))
	for _, item := range items {
		packet := compileTaskPacket(item, allocationBudget, overrides[item.ID])
		packet.SupervisorID, packet.ActorID, packet.SessionID, packet.ContractID =
			run.ID, actor, input.SessionID, input.ContractID
		status := SpecialistPending
		if item.Status == WorkItemReady {
			status = SpecialistReady
		}
		run.Tasks = append(run.Tasks, SpecialistTask{
			Packet: packet, Status: status, UpdatedAt: now,
		})
	}
	sort.SliceStable(run.Tasks, func(left, right int) bool {
		return run.Tasks[left].Packet.ID < run.Tasks[right].Packet.ID
	})
	service.mu.Lock()
	defer service.mu.Unlock()
	document, err := service.load(ctx, actor)
	if err != nil {
		return SupervisorRun{}, err
	}
	for _, existing := range document.Runs {
		if existing.ContractID == run.ContractID && !supervisorTerminal(existing.Status) {
			return SupervisorRun{}, fmt.Errorf("work supervisor: contract already has an active supervisor")
		}
	}
	document.Runs = append(document.Runs, run)
	if err := service.save(ctx, actor, &document); err != nil {
		return SupervisorRun{}, err
	}
	service.scheduleLocked(context.WithoutCancel(ctx), actor, &document, len(document.Runs)-1)
	return cloneSupervisorRun(document.Runs[len(document.Runs)-1]), nil
}

func compileTaskPacket(
	item WorkItem, parent SupervisorBudget, override TaskOverride,
) TaskPacket {
	role := inferSpecialist(item)
	scope, tools := defaultSpecialistAuthority(role)
	budget := specialistBudgetFromParent(parent)
	if override.Specialist != "" {
		role = override.Specialist
		scope = normalizeAuthorityScope(override.Scope)
		tools = trimUnique(override.Tools)
		budget = overlaySpecialistBudget(budget, override.Budget)
	}
	packet := TaskPacket{
		ID: item.ID, WorkItemID: item.ID, Title: item.Title,
		Criteria:   append([]string(nil), item.Criteria...),
		DependsOn:  append([]string(nil), item.DependsOn...),
		Specialist: role, Scope: scope, Tools: tools, Budget: budget,
		Deliverables: []string{"structured result", "artifact references"},
		Evidence:     []string{"current observations", "criterion-linked evidence"},
	}
	if role == SpecialistOperations {
		packet.ParentAuthority = []string{
			"commit", "publish", "deploy", "provider spend", "external communication",
		}
	}
	return packet
}

func specialistBudgetFromParent(parent SupervisorBudget) SpecialistBudget {
	lanes := max(int64(parent.MaxParallel), 1)
	return SpecialistBudget{
		MaxTokens:        min(parent.MaxTokens/lanes, 64_000),
		MaxCostCents:     min(parent.MaxCostCents/lanes, 1_000),
		MaxToolCalls:     min(parent.MaxToolCalls, 32),
		MaxWallSeconds:   min(parent.MaxWallSeconds/int(lanes), 1800),
		MaxProcesses:     max(min(parent.MaxProcesses/int(lanes), 4), 1),
		MaxStorageBytes:  min(parent.MaxStorageBytes/lanes, 256<<20),
		MaxNetworkBytes:  min(parent.MaxNetworkBytes/lanes, 128<<20),
		MaxProviderCents: min(parent.MaxProviderCents/lanes, 1_000),
		MaxRetries:       parent.MaxRetries,
	}
}

func overlaySpecialistBudget(
	base SpecialistBudget, override SpecialistBudget,
) SpecialistBudget {
	if override.MaxTokens > 0 {
		base.MaxTokens = override.MaxTokens
	}
	if override.MaxCostCents > 0 {
		base.MaxCostCents = override.MaxCostCents
	}
	if override.MaxToolCalls > 0 {
		base.MaxToolCalls = min(override.MaxToolCalls, 32)
	}
	if override.MaxWallSeconds > 0 {
		base.MaxWallSeconds = override.MaxWallSeconds
	}
	if override.MaxProcesses > 0 {
		base.MaxProcesses = override.MaxProcesses
	}
	if override.MaxStorageBytes > 0 {
		base.MaxStorageBytes = override.MaxStorageBytes
	}
	if override.MaxNetworkBytes > 0 {
		base.MaxNetworkBytes = override.MaxNetworkBytes
	}
	if override.MaxProviderCents > 0 {
		base.MaxProviderCents = override.MaxProviderCents
	}
	if override.MaxRetries > 0 {
		base.MaxRetries = override.MaxRetries
	}
	return base
}

func inferSpecialist(item WorkItem) Specialist {
	value := strings.ToLower(item.ID + " " + item.Title + " " + strings.Join(item.Criteria, " "))
	for _, candidate := range []struct {
		needles []string
		role    Specialist
	}{
		{[]string{"security", "auth", "secret"}, SpecialistSecurity},
		{[]string{"frontend", " ui", "browser", "accessib"}, SpecialistFrontend},
		{[]string{"database", " data", "migration", "schema"}, SpecialistData},
		{[]string{"performance", "latency", "benchmark"}, SpecialistPerformance},
		{[]string{"deploy", "operation", "release", "runtime"}, SpecialistOperations},
		{[]string{"test", "verify", "acceptance"}, SpecialistTest},
		{[]string{"review", "audit"}, SpecialistReview},
		{[]string{"research", "explore"}, SpecialistExploration},
		{[]string{"discover", "inspect", "inventory"}, SpecialistDiscovery},
	} {
		for _, needle := range candidate.needles {
			if strings.Contains(value, needle) {
				return candidate.role
			}
		}
	}
	return SpecialistImplementation
}

func defaultSpecialistAuthority(role Specialist) (AuthorityScope, []string) {
	read := []string{"workspace"}
	evidenceTools := []string{
		"work_brief", "artifact_record", "artifact_verify",
	}
	withEvidence := func(base []string) []string {
		return append(base, evidenceTools...)
	}
	switch role {
	case SpecialistImplementation, SpecialistFrontend, SpecialistData:
		return AuthorityScope{ReadFiles: read, WriteFiles: []string{"workspace"}},
			withEvidence([]string{
				"filesystem_list", "filesystem_read", "filesystem_search",
				"filesystem_stat", "filesystem_write", "git_diff", "git_status",
			})
	case SpecialistTest, SpecialistPerformance:
		return AuthorityScope{ReadFiles: read, Services: []string{"workspace-process"}},
			withEvidence([]string{
				"filesystem_list", "filesystem_read", "filesystem_search",
				"filesystem_stat", "git_diff", "git_status", "shell_execute",
			})
	case SpecialistExploration:
		return AuthorityScope{ReadFiles: read, NetworkHosts: []string{"approved"}},
			withEvidence([]string{
				"filesystem_list", "filesystem_read", "filesystem_search",
				"filesystem_stat", "git_diff", "git_log", "git_show",
				"web_fetch", "web_search",
			})
	default:
		return AuthorityScope{ReadFiles: read},
			withEvidence([]string{
				"filesystem_list", "filesystem_read", "filesystem_search",
				"filesystem_stat", "git_diff", "git_log", "git_show", "git_status",
			})
	}
}

func normalizeAuthorityScope(scope AuthorityScope) AuthorityScope {
	scope.ReadFiles = trimUnique(scope.ReadFiles)
	scope.WriteFiles = trimUnique(scope.WriteFiles)
	scope.Services = trimUnique(scope.Services)
	scope.EnvironmentKeys = trimUnique(scope.EnvironmentKeys)
	scope.NetworkHosts = trimUnique(scope.NetworkHosts)
	return scope
}

func (service *SupervisorService) Get(
	ctx context.Context, actor uuid.UUID, runID uuid.UUID,
) (SupervisorRun, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	document, err := service.load(ctx, actor)
	if err != nil {
		return SupervisorRun{}, err
	}
	changed, err := reconcileRestart(
		ctx, &document, runID, service.clock.Now().UTC(), service.running,
		service.reconciler,
	)
	if err != nil {
		return SupervisorRun{}, err
	}
	if changed {
		if err := service.save(ctx, actor, &document); err != nil {
			return SupervisorRun{}, err
		}
		if service.executor != nil {
			for index := range document.Runs {
				if (runID == uuid.Nil || document.Runs[index].ID == runID) &&
					!supervisorTerminal(document.Runs[index].Status) &&
					document.Runs[index].Status != SupervisorOutcomeUnknown {
					service.scheduleLocked(
						context.WithoutCancel(ctx), actor, &document, index,
					)
				}
			}
		}
	}
	for index := range document.Runs {
		if runID == uuid.Nil || document.Runs[index].ID == runID {
			return cloneSupervisorRun(document.Runs[index]), nil
		}
	}
	return SupervisorRun{}, fmt.Errorf("work supervisor: run not found")
}

func (service *SupervisorService) List(
	ctx context.Context, actor uuid.UUID,
) ([]SupervisorRun, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	document, err := service.load(ctx, actor)
	if err != nil {
		return nil, err
	}
	changed, err := reconcileRestart(
		ctx, &document, uuid.Nil, service.clock.Now().UTC(), service.running,
		service.reconciler,
	)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := service.save(ctx, actor, &document); err != nil {
			return nil, err
		}
		if service.executor != nil {
			for index := range document.Runs {
				if !supervisorTerminal(document.Runs[index].Status) &&
					document.Runs[index].Status != SupervisorOutcomeUnknown {
					service.scheduleLocked(
						context.WithoutCancel(ctx), actor, &document, index,
					)
				}
			}
		}
	}
	result := append([]SupervisorRun(nil), document.Runs...)
	sort.Slice(result, func(left, right int) bool {
		return result[left].CreatedAt.After(result[right].CreatedAt)
	})
	for index := range result {
		result[index] = cloneSupervisorRun(result[index])
	}
	return result, nil
}

func reconcileRestart(
	ctx context.Context,
	document *SupervisorDocument,
	only uuid.UUID,
	now time.Time,
	local map[uuid.UUID]context.CancelFunc,
	reconciler SupervisorReconciler,
) (bool, error) {
	changed := false
	for runIndex := range document.Runs {
		run := &document.Runs[runIndex]
		if only != uuid.Nil && run.ID != only || supervisorTerminal(run.Status) {
			continue
		}
		reconciliation := Reconciliation{At: now}
		for taskIndex := range run.Tasks {
			task := &run.Tasks[taskIndex]
			if task.Status != SpecialistRunning {
				continue
			}
			if len(task.Attempts) > 0 {
				attempt := task.Attempts[len(task.Attempts)-1]
				if local[attempt.ID] != nil {
					continue
				}
			}
			if reconciler == nil {
				return false, fmt.Errorf(
					"work supervisor: restart reconciler is unavailable",
				)
			}
			uncertain := false
			var attempt Attempt
			if len(task.Attempts) > 0 {
				attempt = task.Attempts[len(task.Attempts)-1]
				for effectIndex := range attempt.ExternalEffects {
					if attempt.ExternalEffects[effectIndex].State == "started" ||
						attempt.ExternalEffects[effectIndex].State == "outcome_unknown" {
						attempt.ExternalEffects[effectIndex].State = "outcome_unknown"
						uncertain = true
					}
				}
			}
			inspected, inspectedUncertain, err := reconciler.Reconcile(
				ctx, *run, *task, attempt,
			)
			if err != nil {
				return false, fmt.Errorf(
					"work supervisor: reconcile %s: %w", task.Packet.ID, err,
				)
			}
			inspected.At = now
			reconciliation = mergeReconciliation(reconciliation, inspected)
			uncertain = uncertain || inspectedUncertain
			if len(task.Attempts) > 0 {
				persisted := &task.Attempts[len(task.Attempts)-1]
				persisted.ExternalEffects = append(
					[]ExternalEffect(nil), attempt.ExternalEffects...,
				)
				persisted.Status = SpecialistRetrying
				persisted.Error = "supervisor restarted before terminal worker evidence"
				persisted.FinishedAt = &now
			}
			if uncertain || task.Packet.Scope.ExternalEffects {
				task.Status = SpecialistOutcomeUnknown
				task.BlockingReason = "external effect requires provider reconciliation before retry"
				reconciliation.UncertainTasks++
				run.Status = SupervisorOutcomeUnknown
			} else {
				task.Status = SpecialistReady
				task.BlockingReason = ""
				reconciliation.RecoveredTasks++
			}
			task.UpdatedAt = now
			changed = true
		}
		if reconciliation.RecoveredTasks > 0 || reconciliation.UncertainTasks > 0 {
			run.Leases = nil
			run.Reconciliations = append(run.Reconciliations, reconciliation)
			run.UpdatedAt = now
		}
	}
	return changed, nil
}

func (service *SupervisorService) scheduleLocked(
	ctx context.Context, actor uuid.UUID, document *SupervisorDocument, runIndex int,
) {
	run := &document.Runs[runIndex]
	if service.executor == nil {
		run.Status = SupervisorWaiting
		run.UpdatedAt = service.clock.Now().UTC()
		_ = service.save(ctx, actor, document)
		return
	}
	active := 0
	refreshProjectUsage(document, runIndex)
	for _, task := range run.Tasks {
		if task.Status == SpecialistRunning {
			active++
		}
	}
	for taskIndex := range run.Tasks {
		if active >= run.Budget.MaxParallel ||
			budgetExceeded(run.Usage, run.Budget) ||
			budgetExceeded(run.ProjectUsage, run.ProjectBudget) ||
			projectBudgetAtCapacity(run.ProjectUsage, run.ProjectBudget) {
			break
		}
		task := &run.Tasks[taskIndex]
		if task.Status != SpecialistReady || !supervisorDependenciesComplete(run.Tasks, task.Packet) {
			continue
		}
		leases, ok := acquireTaskLeases(run.Leases, task.Packet, service.clock.Now().UTC())
		if !ok {
			continue
		}
		run.Leases = leases
		now := service.clock.Now().UTC()
		attempt := Attempt{
			ID: uuid.New(), Number: len(task.Attempts) + 1,
			Status: SpecialistRunning, StartedAt: now,
		}
		task.Attempts = append(task.Attempts, attempt)
		task.Status, task.Progress, task.UpdatedAt = SpecialistRunning, 5, now
		run.Status, run.UpdatedAt = SupervisorWorking, now
		run.Projection = projectedSupervisorUsage(*run)
		refreshProjectUsage(document, runIndex)
		run.Revision++
		if err := service.save(ctx, actor, document); err != nil {
			task.Status = SpecialistReady
			run.Leases = releaseTaskLeases(run.Leases, task.Packet.ID)
			continue
		}
		runID, taskID, attemptID := run.ID, task.Packet.ID, attempt.ID
		packet := cloneTaskPacket(task.Packet)
		workerCtx, cancel := context.WithTimeout(
			context.Background(), time.Duration(packet.Budget.MaxWallSeconds)*time.Second,
		)
		service.running[attemptID] = cancel
		executor := service.executor
		go func() {
			result, executeErr := executor.Execute(workerCtx, packet, attemptID)
			cancel()
			service.finishAttempt(actor, runID, taskID, attemptID, result, executeErr)
		}()
		active++
	}
	if active == 0 && run.Status == SupervisorQueued {
		run.Status = SupervisorWaiting
	}
	run.Projection = projectedSupervisorUsage(*run)
	refreshProjectUsage(document, runIndex)
	_ = service.save(ctx, actor, document)
}

func (service *SupervisorService) finishAttempt(
	actor, runID uuid.UUID, taskID string, attemptID uuid.UUID,
	result WorkerResult, executeErr error,
) {
	service.mu.Lock()
	defer service.mu.Unlock()
	delete(service.running, attemptID)
	ctx := context.Background()
	document, err := service.load(ctx, actor)
	if err != nil {
		return
	}
	runIndex, taskIndex, attemptIndex := findSupervisorAttempt(&document, runID, taskID, attemptID)
	if runIndex < 0 {
		return
	}
	run := &document.Runs[runIndex]
	task := &run.Tasks[taskIndex]
	attempt := &task.Attempts[attemptIndex]
	if attempt.Status != SpecialistRunning {
		return
	}
	now := service.clock.Now().UTC()
	attempt.FinishedAt, attempt.WorkerID = &now, strings.TrimSpace(result.WorkerID)
	attempt.Summary = boundedText(result.Summary)
	attempt.Usage, attempt.Artifacts = result.Usage, trimUnique(result.Artifacts)
	attempt.Findings = append([]Finding(nil), result.Findings...)
	attempt.ExternalEffects = append([]ExternalEffect(nil), result.ExternalEffects...)
	attempt.ProcessIDs = append([]int(nil), result.ProcessIDs...)
	task.Progress = clampProgress(result.Progress)
	run.Usage = addUsage(run.Usage, result.Usage)
	run.Leases = releaseTaskLeases(run.Leases, taskID)
	taskBudgetExceeded := specialistBudgetExceeded(result.Usage, task.Packet.Budget)
	accountingMissing := result.Status == SpecialistCompleted &&
		(task.Packet.Budget.MaxCostCents > 0 && !result.Usage.CostKnown ||
			task.Packet.Budget.MaxProviderCents > 0 &&
				!result.Usage.ProviderSpendKnown)
	verifiedEvidence := false
	if !taskBudgetExceeded && !accountingMissing && executeErr == nil &&
		result.Status == SpecialistCompleted {
		verifiedEvidence, err = service.workerResultHasVerifiedEvidence(
			ctx, actor, *run, *task, result,
		)
		if err != nil {
			executeErr = err
		}
	}
	switch {
	case taskBudgetExceeded:
		attempt.Status = SpecialistBlocked
		attempt.Error = "specialist budget exhausted before verified completion"
		task.Status = SpecialistBlocked
		task.BlockingReason = attempt.Error
	case accountingMissing:
		attempt.Status = SpecialistBlocked
		attempt.Error = "authoritative model cost or provider spend is unavailable"
		task.Status = SpecialistBlocked
		task.BlockingReason = attempt.Error
	case executeErr != nil && len(task.Attempts) <= task.Packet.Budget.MaxRetries &&
		!hasUncertainEffect(result.ExternalEffects) && !budgetExceeded(run.Usage, run.Budget):
		attempt.Status, attempt.Error = SpecialistRetrying, boundedText(executeErr.Error())
		task.Status, task.Progress = SpecialistReady, 0
		run.Usage.Retries++
	case executeErr != nil:
		attempt.Status, attempt.Error = SpecialistBlocked, boundedText(executeErr.Error())
		task.Status, task.BlockingReason = SpecialistBlocked, attempt.Error
	case result.Status == SpecialistOutcomeUnknown || hasUncertainEffect(result.ExternalEffects):
		attempt.Status = SpecialistOutcomeUnknown
		task.Status = SpecialistOutcomeUnknown
		task.BlockingReason = "external effect outcome must be reconciled"
	case result.Status == SpecialistCompleted && verifiedEvidence:
		attempt.Status = SpecialistCompleted
		task.Status, task.Progress = SpecialistCompleted, 100
	case result.Status == SpecialistCompleted:
		attempt.Status = SpecialistWaitingEvidence
		attempt.Error = "specialist completion lacks server-verified criterion coverage"
		task.Status = SpecialistWaitingEvidence
		task.BlockingReason = attempt.Error
		task.Progress = min(task.Progress, 99)
	default:
		attempt.Status = SpecialistWaitingEvidence
		task.Status = SpecialistWaitingEvidence
		task.BlockingReason = "specialist result lacks terminal evidence"
	}
	task.UpdatedAt, run.UpdatedAt = now, now
	refreshSupervisorReady(run)
	finalizeSupervisorRun(run, now)
	if run.Status == SupervisorCompleted {
		if _, completionErr := service.work.CompleteContract(
			ctx, actor, run.ContractID,
		); completionErr != nil {
			attempt.Status = SpecialistWaitingEvidence
			attempt.Error = boundedText(completionErr.Error())
			task.Status = SpecialistWaitingEvidence
			task.BlockingReason = "outcome completion lacks server-verified criterion coverage"
			task.Progress = min(task.Progress, 99)
			run.FinishedAt = nil
			finalizeSupervisorRun(run, now)
		}
	}
	if taskBudgetExceeded {
		run.Status = SupervisorPaused
	}
	if accountingMissing {
		run.Status = SupervisorPaused
	}
	run.Projection = projectedSupervisorUsage(*run)
	refreshProjectUsage(&document, runIndex)
	run.Revision++
	if err := service.save(ctx, actor, &document); err != nil {
		return
	}
	if !supervisorTerminal(run.Status) && run.Status != SupervisorPaused &&
		run.Status != SupervisorOutcomeUnknown {
		service.scheduleLocked(ctx, actor, &document, runIndex)
	}
}

func (service *SupervisorService) Steer(
	ctx context.Context, actor, runID uuid.UUID, instruction string,
) (SupervisorRun, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" || len(instruction) > maxTextBytes {
		return SupervisorRun{}, fmt.Errorf("work supervisor: bounded steering instruction is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	document, err := service.load(ctx, actor)
	if err != nil {
		return SupervisorRun{}, err
	}
	index := findSupervisorRun(document.Runs, runID)
	if index < 0 || supervisorTerminal(document.Runs[index].Status) {
		return SupervisorRun{}, fmt.Errorf("work supervisor: active run not found")
	}
	now := service.clock.Now().UTC()
	document.Runs[index].Steering = append(document.Runs[index].Steering,
		SteeringRecord{At: now, Instruction: instruction})
	document.Runs[index].UpdatedAt = now
	if err := service.save(ctx, actor, &document); err != nil {
		return SupervisorRun{}, err
	}
	return cloneSupervisorRun(document.Runs[index]), nil
}

func (service *SupervisorService) Cancel(
	ctx context.Context, actor, runID uuid.UUID,
) (SupervisorRun, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	document, err := service.load(ctx, actor)
	if err != nil {
		return SupervisorRun{}, err
	}
	index := findSupervisorRun(document.Runs, runID)
	if index < 0 || supervisorTerminal(document.Runs[index].Status) {
		return SupervisorRun{}, fmt.Errorf("work supervisor: active run not found")
	}
	now := service.clock.Now().UTC()
	run := &document.Runs[index]
	for taskIndex := range run.Tasks {
		task := &run.Tasks[taskIndex]
		if task.Status == SpecialistRunning && len(task.Attempts) > 0 {
			attempt := &task.Attempts[len(task.Attempts)-1]
			if cancel := service.running[attempt.ID]; cancel != nil {
				cancel()
				delete(service.running, attempt.ID)
			}
			attempt.Status, attempt.FinishedAt, attempt.Error =
				SpecialistCancelled, &now, "cancelled by parent supervisor"
		}
		if task.Status != SpecialistCompleted {
			task.Status, task.UpdatedAt = SpecialistCancelled, now
		}
	}
	run.Status, run.Leases, run.UpdatedAt, run.FinishedAt =
		SupervisorCancelled, nil, now, &now
	run.Projection = run.Usage
	refreshProjectUsage(&document, index)
	if err := service.save(ctx, actor, &document); err != nil {
		return SupervisorRun{}, err
	}
	return cloneSupervisorRun(*run), nil
}

func supervisorDependenciesComplete(tasks []SpecialistTask, packet TaskPacket) bool {
	for _, dependency := range packet.DependsOn {
		found := false
		for _, task := range tasks {
			if task.Packet.WorkItemID == dependency && task.Status == SpecialistCompleted {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func refreshSupervisorReady(run *SupervisorRun) {
	for index := range run.Tasks {
		task := &run.Tasks[index]
		if task.Status == SpecialistPending &&
			supervisorDependenciesComplete(run.Tasks, task.Packet) {
			task.Status = SpecialistReady
		}
	}
}

func finalizeSupervisorRun(run *SupervisorRun, now time.Time) {
	completed, active, blocked, uncertain := 0, 0, 0, 0
	var findings []Finding
	for _, task := range run.Tasks {
		switch task.Status {
		case SpecialistCompleted:
			completed++
		case SpecialistRunning, SpecialistReady, SpecialistRetrying:
			active++
		case SpecialistOutcomeUnknown:
			uncertain++
		case SpecialistBlocked, SpecialistWaitingEvidence:
			blocked++
		}
		for _, attempt := range task.Attempts {
			findings = append(findings, attempt.Findings...)
		}
	}
	run.Synthesis = deterministicFindings(findings)
	switch {
	case uncertain > 0:
		run.Status = SupervisorOutcomeUnknown
	case budgetExceeded(run.Usage, run.Budget):
		run.Status = SupervisorPaused
	case completed == len(run.Tasks):
		run.Status, run.FinishedAt = SupervisorCompleted, &now
	case active > 0:
		run.Status = SupervisorWorking
	case blocked > 0:
		run.Status = SupervisorBlocked
	default:
		run.Status = SupervisorWaiting
	}
}

func acquireTaskLeases(
	current []ScopeLease, packet TaskPacket, now time.Time,
) ([]ScopeLease, bool) {
	requested := make([]ScopeLease, 0)
	for _, resource := range packet.Scope.ReadFiles {
		requested = append(requested, ScopeLease{
			ID: uuid.New(), TaskID: packet.ID, Resource: "file:" + resource,
			Mode: "read", ExpiresAt: now.Add(time.Hour),
		})
	}
	for _, resource := range packet.Scope.WriteFiles {
		requested = append(requested, ScopeLease{
			ID: uuid.New(), TaskID: packet.ID, Resource: "file:" + resource,
			Mode: "write", ExpiresAt: now.Add(time.Hour),
		})
	}
	for _, resource := range packet.Scope.Services {
		requested = append(requested, ScopeLease{
			ID: uuid.New(), TaskID: packet.ID, Resource: "service:" + resource,
			Mode: "write", ExpiresAt: now.Add(time.Hour),
		})
	}
	for _, wanted := range requested {
		for _, held := range current {
			if held.ExpiresAt.After(now) && held.TaskID != packet.ID &&
				held.Resource == wanted.Resource &&
				(held.Mode == "write" || wanted.Mode == "write") {
				return current, false
			}
		}
	}
	return append(current, requested...), true
}

func releaseTaskLeases(leases []ScopeLease, taskID string) []ScopeLease {
	result := leases[:0]
	for _, lease := range leases {
		if lease.TaskID != taskID {
			result = append(result, lease)
		}
	}
	return result
}

func budgetExceeded(usage BudgetUsage, budget SupervisorBudget) bool {
	return usage.Tokens > budget.MaxTokens || usage.CostCents > budget.MaxCostCents ||
		usage.ToolCalls > budget.MaxToolCalls || usage.WallSeconds > budget.MaxWallSeconds ||
		usage.Processes > budget.MaxProcesses || usage.StorageBytes > budget.MaxStorageBytes ||
		usage.NetworkBytes > budget.MaxNetworkBytes ||
		usage.ProviderCents > budget.MaxProviderCents ||
		usage.Retries > budget.MaxRetries
}

func projectBudgetAtCapacity(usage BudgetUsage, budget SupervisorBudget) bool {
	return budget.MaxTokens > 0 && usage.Tokens >= budget.MaxTokens ||
		budget.MaxCostCents > 0 && usage.CostCents >= budget.MaxCostCents ||
		budget.MaxToolCalls > 0 && usage.ToolCalls >= budget.MaxToolCalls ||
		budget.MaxWallSeconds > 0 && usage.WallSeconds >= budget.MaxWallSeconds ||
		budget.MaxProcesses > 0 && usage.Processes >= budget.MaxProcesses ||
		budget.MaxStorageBytes > 0 && usage.StorageBytes >= budget.MaxStorageBytes ||
		budget.MaxNetworkBytes > 0 && usage.NetworkBytes >= budget.MaxNetworkBytes ||
		budget.MaxProviderCents > 0 && usage.ProviderCents >= budget.MaxProviderCents ||
		budget.MaxRetries > 0 && usage.Retries >= budget.MaxRetries
}

func addUsage(left, right BudgetUsage) BudgetUsage {
	result := BudgetUsage{
		Tokens: left.Tokens + right.Tokens, CostCents: left.CostCents + right.CostCents,
		ToolCalls:     left.ToolCalls + right.ToolCalls,
		WallSeconds:   left.WallSeconds + right.WallSeconds,
		Processes:     left.Processes + right.Processes,
		StorageBytes:  left.StorageBytes + right.StorageBytes,
		NetworkBytes:  left.NetworkBytes + right.NetworkBytes,
		ProviderCents: left.ProviderCents + right.ProviderCents,
		Retries:       left.Retries + right.Retries,
	}
	switch {
	case !budgetUsageObserved(left):
		result.CostKnown = right.CostKnown
		result.ProviderSpendKnown = right.ProviderSpendKnown
	case !budgetUsageObserved(right):
		result.CostKnown = left.CostKnown
		result.ProviderSpendKnown = left.ProviderSpendKnown
	default:
		result.CostKnown = left.CostKnown && right.CostKnown
		result.ProviderSpendKnown =
			left.ProviderSpendKnown && right.ProviderSpendKnown
	}
	return result
}

func budgetUsageObserved(usage BudgetUsage) bool {
	return usage.Tokens != 0 ||
		usage.CostCents != 0 ||
		usage.CostKnown ||
		usage.ToolCalls != 0 ||
		usage.WallSeconds != 0 ||
		usage.Processes != 0 ||
		usage.StorageBytes != 0 ||
		usage.NetworkBytes != 0 ||
		usage.ProviderCents != 0 ||
		usage.ProviderSpendKnown ||
		usage.Retries != 0
}

func projectedSupervisorUsage(run SupervisorRun) BudgetUsage {
	projected := run.Usage
	for _, task := range run.Tasks {
		if task.Status != SpecialistRunning {
			continue
		}
		projected = addUsage(projected, BudgetUsage{
			Tokens:        task.Packet.Budget.MaxTokens,
			CostCents:     task.Packet.Budget.MaxCostCents,
			ToolCalls:     task.Packet.Budget.MaxToolCalls,
			WallSeconds:   task.Packet.Budget.MaxWallSeconds,
			Processes:     task.Packet.Budget.MaxProcesses,
			StorageBytes:  task.Packet.Budget.MaxStorageBytes,
			NetworkBytes:  task.Packet.Budget.MaxNetworkBytes,
			ProviderCents: task.Packet.Budget.MaxProviderCents,
		})
	}
	return projected
}

func specialistBudgetExceeded(usage BudgetUsage, budget SpecialistBudget) bool {
	return usage.Tokens > budget.MaxTokens ||
		usage.CostCents > budget.MaxCostCents ||
		usage.ToolCalls > budget.MaxToolCalls ||
		usage.WallSeconds > budget.MaxWallSeconds ||
		usage.Processes > budget.MaxProcesses ||
		usage.StorageBytes > budget.MaxStorageBytes ||
		usage.NetworkBytes > budget.MaxNetworkBytes ||
		usage.ProviderCents > budget.MaxProviderCents ||
		usage.Retries > budget.MaxRetries
}

func refreshProjectUsage(document *SupervisorDocument, runIndex int) {
	run := &document.Runs[runIndex]
	usage := BudgetUsage{}
	for index := range document.Runs {
		candidate := &document.Runs[index]
		sameProject := run.ProjectID != nil && candidate.ProjectID != nil &&
			*run.ProjectID == *candidate.ProjectID
		if run.ProjectID == nil {
			sameProject = candidate.ID == run.ID
		}
		if sameProject {
			usage = addUsage(usage, candidate.Usage)
			usage = addUsage(
				usage,
				subtractUsage(candidate.Projection, candidate.Usage),
			)
		}
	}
	run.ProjectUsage = usage
}

func (service *SupervisorService) workerResultHasVerifiedEvidence(
	ctx context.Context,
	actor uuid.UUID,
	run SupervisorRun,
	task SpecialistTask,
	result WorkerResult,
) (bool, error) {
	if len(task.Packet.Criteria) == 0 || len(result.Artifacts) == 0 {
		return false, nil
	}
	wanted := make(map[uuid.UUID]struct{}, len(result.Artifacts))
	for _, reference := range result.Artifacts {
		id, err := uuid.Parse(strings.TrimSpace(reference))
		if err != nil || id == uuid.Nil {
			return false, nil
		}
		wanted[id] = struct{}{}
	}
	portfolio, err := service.work.Get(ctx, actor)
	if err != nil {
		return false, fmt.Errorf("work supervisor: inspect verified artifacts: %w", err)
	}
	covered := make(map[string]struct{}, len(task.Packet.Criteria))
	for _, artifact := range portfolio.Artifacts {
		if _, ok := wanted[artifact.ID]; !ok ||
			artifact.ContractID != run.ContractID ||
			artifact.VerifiedAt == nil ||
			artifact.SHA256 == "" ||
			artifact.Verification != "server_sha256" {
			continue
		}
		for _, criterion := range artifact.CriteriaCovered {
			covered[criterion] = struct{}{}
		}
	}
	for _, criterion := range task.Packet.Criteria {
		if _, ok := covered[criterion]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func subtractUsage(left, right BudgetUsage) BudgetUsage {
	return BudgetUsage{
		Tokens:             max(left.Tokens-right.Tokens, 0),
		CostCents:          max(left.CostCents-right.CostCents, 0),
		ToolCalls:          max(left.ToolCalls-right.ToolCalls, 0),
		WallSeconds:        max(left.WallSeconds-right.WallSeconds, 0),
		Processes:          max(left.Processes-right.Processes, 0),
		StorageBytes:       max(left.StorageBytes-right.StorageBytes, 0),
		NetworkBytes:       max(left.NetworkBytes-right.NetworkBytes, 0),
		ProviderCents:      max(left.ProviderCents-right.ProviderCents, 0),
		CostKnown:          left.CostKnown,
		ProviderSpendKnown: left.ProviderSpendKnown,
		Retries:            max(left.Retries-right.Retries, 0),
	}
}

func hasUncertainEffect(effects []ExternalEffect) bool {
	for _, effect := range effects {
		if effect.State == "started" || effect.State == "outcome_unknown" {
			return true
		}
	}
	return false
}

func deterministicFindings(input []Finding) []Finding {
	result := append([]Finding(nil), input...)
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Kind != result[right].Kind {
			return result[left].Kind < result[right].Kind
		}
		return result[left].Summary < result[right].Summary
	})
	return result
}

func findSupervisorRun(runs []SupervisorRun, id uuid.UUID) int {
	for index := range runs {
		if runs[index].ID == id {
			return index
		}
	}
	return -1
}

func findSupervisorAttempt(
	document *SupervisorDocument, runID uuid.UUID, taskID string, attemptID uuid.UUID,
) (int, int, int) {
	runIndex := findSupervisorRun(document.Runs, runID)
	if runIndex < 0 {
		return -1, -1, -1
	}
	for taskIndex := range document.Runs[runIndex].Tasks {
		task := &document.Runs[runIndex].Tasks[taskIndex]
		if task.Packet.ID != taskID {
			continue
		}
		for attemptIndex := range task.Attempts {
			if task.Attempts[attemptIndex].ID == attemptID {
				return runIndex, taskIndex, attemptIndex
			}
		}
	}
	return -1, -1, -1
}

func supervisorTerminal(status SupervisorStatus) bool {
	return status == SupervisorCancelled || status == SupervisorCompleted
}

func clampProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func boundedText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxTextBytes {
		return value[:maxTextBytes]
	}
	return value
}

func cloneTaskPacket(packet TaskPacket) TaskPacket {
	raw, _ := json.Marshal(packet)
	var result TaskPacket
	_ = json.Unmarshal(raw, &result)
	return result
}

func cloneSupervisorRun(run SupervisorRun) SupervisorRun {
	raw, _ := json.Marshal(run)
	var result SupervisorRun
	_ = json.Unmarshal(raw, &result)
	return result
}
