package work

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

var supervisorBaseTime = time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)

type supervisorExecutorFunc func(
	context.Context, TaskPacket, uuid.UUID,
) (WorkerResult, error)

func (function supervisorExecutorFunc) Execute(
	ctx context.Context, packet TaskPacket, attemptID uuid.UUID,
) (WorkerResult, error) {
	return function(ctx, packet, attemptID)
}

func putSupervisorContract(
	t *testing.T, service *Service, actor uuid.UUID, count int, chained bool,
) OutcomeContract {
	t.Helper()
	criteria := make([]Criterion, count)
	items := make([]WorkItemInput, count)
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("item-%02d", index)
		criteria[index] = Criterion{ID: id, Description: "Prove " + id}
		items[index] = WorkItemInput{
			ID: id, Title: "Inspect " + id, Criteria: []string{id},
		}
		if chained && index > 0 {
			items[index].DependsOn = []string{fmt.Sprintf("item-%02d", index-1)}
		}
	}
	contract, _, err := service.PutContractWithWorkItems(
		context.Background(), actor,
		ContractInput{
			Goal: "Complete supervised work", Deliverable: "verified result",
			DoneCriteria: criteria, VerificationRequired: []string{"current evidence"},
			NextAction: "dispatch ready work",
		},
		items,
	)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func waitForSupervisor(
	t *testing.T, service *SupervisorService, actor, runID uuid.UUID,
	predicate func(SupervisorRun) bool,
) SupervisorRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := service.Get(context.Background(), actor, runID)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(run) {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	run, _ := service.Get(context.Background(), actor, runID)
	t.Fatalf("supervisor condition timed out: %+v", run)
	return SupervisorRun{}
}

func createVerifiedSupervisorArtifact(
	service *Service,
	workspace string,
	actor uuid.UUID,
	contractID uuid.UUID,
	criterion string,
) (string, error) {
	reference := filepath.Join("supervisor-evidence", criterion+".txt")
	path := filepath.Join(workspace, reference)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(
		path,
		[]byte("server-verifiable evidence for "+criterion+"\n"),
		0o600,
	); err != nil {
		return "", err
	}
	artifact, err := service.RecordArtifact(
		context.Background(),
		actor,
		ArtifactInput{
			ContractID: contractID,
			Kind:       "supervisor-test",
			Title:      "Evidence for " + criterion,
			Reference:  reference,
			CriteriaCovered: []string{
				criterion,
			},
		},
	)
	if err != nil {
		return "", err
	}
	verified, err := service.VerifyArtifact(
		context.Background(),
		actor,
		artifact.ID,
	)
	if err != nil {
		return "", err
	}
	return verified.ID.String(), nil
}

func TestSupervisorDispatchesTwentyIndependentSpecialistsWithLiveProgress(t *testing.T) {
	workService, store, workspace := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract := putSupervisorContract(t, workService, actor, LaunchParallelMinimum, false)
	supervisor, err := NewSupervisorService(store, fixedClock{now: supervisorBaseTime}, workService)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	started := make(chan struct{}, LaunchParallelMinimum)
	var active, peak atomic.Int32
	supervisor.SetExecutor(supervisorExecutorFunc(func(
		ctx context.Context, packet TaskPacket, attemptID uuid.UUID,
	) (WorkerResult, error) {
		now := active.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-ctx.Done():
			active.Add(-1)
			return WorkerResult{}, ctx.Err()
		case <-release:
		}
		active.Add(-1)
		artifactID, err := createVerifiedSupervisorArtifact(
			workService, workspace, actor, contract.ID, packet.Criteria[0],
		)
		if err != nil {
			return WorkerResult{}, err
		}
		return WorkerResult{
			AttemptID: attemptID, WorkerID: "worker-" + packet.ID,
			Status: SpecialistCompleted, Progress: 100,
			Summary: "inspected " + packet.ID,
			Usage: BudgetUsage{
				CostKnown: true, ProviderSpendKnown: true,
			},
			Artifacts: []string{artifactID},
			Findings:  []Finding{{Kind: "evidence", Summary: packet.ID}},
		}, nil
	}))
	run, err := supervisor.Start(context.Background(), actor, SupervisorStartInput{
		ContractID: contract.ID,
		ProjectID:  func() *uuid.UUID { id := uuid.New(); return &id }(),
		Budget: SupervisorBudget{
			SpecialistBudget: defaultSupervisorBudget().SpecialistBudget,
			MaxParallel:      LaunchParallelMinimum,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < LaunchParallelMinimum; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d specialists started", index)
		}
	}
	live, err := supervisor.Get(context.Background(), actor, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	running := 0
	for _, task := range live.Tasks {
		if task.Status == SpecialistRunning && task.Progress == 5 &&
			len(task.Attempts) == 1 {
			running++
		}
	}
	if running != LaunchParallelMinimum || peak.Load() != LaunchParallelMinimum {
		t.Fatalf("running=%d peak=%d", running, peak.Load())
	}
	close(release)
	completed := waitForSupervisor(t, supervisor, actor, run.ID,
		func(found SupervisorRun) bool { return found.Status == SupervisorCompleted })
	if len(completed.Synthesis) != LaunchParallelMinimum {
		t.Fatalf("deterministic synthesis = %+v", completed.Synthesis)
	}
	for index := 1; index < len(completed.Synthesis); index++ {
		if completed.Synthesis[index-1].Summary > completed.Synthesis[index].Summary {
			t.Fatal("result synthesis is not deterministic")
		}
	}
}

func TestSupervisorRestartWithLiveExecutorResumesOnce(t *testing.T) {
	workService, store, workspace := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract := putSupervisorContract(t, workService, actor, 1, false)
	first, err := NewSupervisorService(
		store,
		fixedClock{now: supervisorBaseTime},
		workService,
	)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan uuid.UUID, 1)
	first.SetExecutor(supervisorExecutorFunc(func(
		ctx context.Context,
		_ TaskPacket,
		attemptID uuid.UUID,
	) (WorkerResult, error) {
		started <- attemptID
		<-ctx.Done()
		return WorkerResult{}, ctx.Err()
	}))
	run, err := first.Start(
		context.Background(),
		actor,
		SupervisorStartInput{
			ContractID: contract.ID,
			Budget:     defaultSupervisorBudget(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstAttemptID := <-started
	second, err := NewSupervisorService(
		store,
		fixedClock{now: supervisorBaseTime.Add(time.Minute)},
		workService,
	)
	if err != nil {
		t.Fatal(err)
	}
	var resumed atomic.Int32
	second.SetExecutor(supervisorExecutorFunc(func(
		_ context.Context,
		packet TaskPacket,
		attemptID uuid.UUID,
	) (WorkerResult, error) {
		resumed.Add(1)
		artifactID, artifactErr := createVerifiedSupervisorArtifact(
			workService, workspace, actor, contract.ID, packet.Criteria[0],
		)
		if artifactErr != nil {
			return WorkerResult{}, artifactErr
		}
		return WorkerResult{
			AttemptID: attemptID,
			Status:    SpecialistCompleted,
			Progress:  100,
			Usage: BudgetUsage{
				CostKnown: true, ProviderSpendKnown: true,
			},
			Artifacts: []string{artifactID},
		}, nil
	}))
	if _, err := second.Get(context.Background(), actor, run.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitForSupervisor(
		t,
		second,
		actor,
		run.ID,
		func(found SupervisorRun) bool {
			return found.Status == SupervisorCompleted
		},
	)
	if resumed.Load() != 1 ||
		len(completed.Tasks[0].Attempts) != 2 ||
		completed.Tasks[0].Attempts[0].ID != firstAttemptID ||
		completed.Reconciliations[0].RecoveredTasks != 1 {
		t.Fatalf(
			"restart resumed=%d run=%+v",
			resumed.Load(),
			completed,
		)
	}
	first.mu.Lock()
	first.running[firstAttemptID]()
	first.mu.Unlock()
}

func TestSupervisorEnforcesDependenciesLeasesRetriesBudgetsAndCancellation(t *testing.T) {
	workService, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract := putSupervisorContract(t, workService, actor, 2, false)
	supervisor, err := NewSupervisorService(store, fixedClock{now: supervisorBaseTime}, workService)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var calls atomic.Int32
	supervisor.SetExecutor(supervisorExecutorFunc(func(
		ctx context.Context, packet TaskPacket, attemptID uuid.UUID,
	) (WorkerResult, error) {
		call := calls.Add(1)
		if call == 1 {
			return WorkerResult{
				AttemptID: attemptID, Status: SpecialistRetrying,
				Usage: BudgetUsage{ToolCalls: 1},
			}, errors.New("injected child failure")
		}
		select {
		case <-ctx.Done():
			return WorkerResult{}, ctx.Err()
		case <-release:
		}
		return WorkerResult{
			AttemptID: attemptID, Status: SpecialistCompleted, Progress: 100,
			Usage: BudgetUsage{ToolCalls: 1},
		}, nil
	}))
	overrides := []TaskOverride{
		{WorkItemID: "item-00", Specialist: SpecialistImplementation,
			Scope:  AuthorityScope{WriteFiles: []string{"shared.go"}},
			Tools:  []string{"filesystem_write"},
			Budget: SpecialistBudget{MaxToolCalls: 2, MaxWallSeconds: 30, MaxRetries: 1}},
		{WorkItemID: "item-01", Specialist: SpecialistImplementation,
			Scope:  AuthorityScope{WriteFiles: []string{"shared.go"}},
			Tools:  []string{"filesystem_write"},
			Budget: SpecialistBudget{MaxToolCalls: 2, MaxWallSeconds: 30, MaxRetries: 1}},
	}
	budget := defaultSupervisorBudget()
	budget.MaxParallel = 2
	run, err := supervisor.Start(context.Background(), actor, SupervisorStartInput{
		ContractID: contract.ID, Budget: budget, Overrides: overrides,
	})
	if err != nil {
		t.Fatal(err)
	}
	live := waitForSupervisor(t, supervisor, actor, run.ID, func(found SupervisorRun) bool {
		return calls.Load() >= 2
	})
	running, ready := 0, 0
	for _, task := range live.Tasks {
		switch task.Status {
		case SpecialistRunning:
			running++
		case SpecialistReady:
			ready++
		}
	}
	if running != 1 || ready != 1 {
		t.Fatalf("conflicting write lease failed: running=%d ready=%d %+v", running, ready, live.Leases)
	}
	steered, err := supervisor.Steer(context.Background(), actor, run.ID, "Prefer the minimal compatible edit")
	if err != nil || len(steered.Steering) != 1 {
		t.Fatalf("steering = %+v, %v", steered, err)
	}
	cancelled, err := supervisor.Cancel(context.Background(), actor, run.ID)
	if err != nil || cancelled.Status != SupervisorCancelled ||
		len(cancelled.Leases) != 0 {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	close(release)
}

func TestSupervisorRestartReconcilesBeforeRetryAndPreservesUncertainEffects(t *testing.T) {
	workService, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract := putSupervisorContract(t, workService, actor, 1, false)
	first, err := NewSupervisorService(store, fixedClock{now: supervisorBaseTime}, workService)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan uuid.UUID, 1)
	first.SetExecutor(supervisorExecutorFunc(func(
		ctx context.Context, _ TaskPacket, attemptID uuid.UUID,
	) (WorkerResult, error) {
		started <- attemptID
		<-ctx.Done()
		return WorkerResult{}, ctx.Err()
	}))
	budget := defaultSupervisorBudget()
	run, err := first.Start(context.Background(), actor, SupervisorStartInput{
		ContractID: contract.ID, Budget: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	attemptID := <-started
	second, err := NewSupervisorService(store, fixedClock{now: supervisorBaseTime.Add(time.Minute)}, workService)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := second.Get(context.Background(), actor, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Tasks[0].Status != SpecialistReady ||
		len(recovered.Reconciliations) != 1 ||
		recovered.Reconciliations[0].RecoveredTasks != 1 ||
		!strings.HasPrefix(recovered.Reconciliations[0].FileSystem, "inspected_") ||
		recovered.Reconciliations[0].Git == "" ||
		recovered.Reconciliations[0].Processes != "no_recorded_processes" {
		t.Fatalf("restart reconciliation = %+v", recovered)
	}
	first.mu.Lock()
	first.running[attemptID]()
	first.mu.Unlock()

	document, err := second.load(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	index := findSupervisorRun(document.Runs, run.ID)
	now := supervisorBaseTime.Add(2 * time.Minute)
	document.Runs[index].Tasks[0].Status = SpecialistRunning
	document.Runs[index].Tasks[0].Attempts = append(
		document.Runs[index].Tasks[0].Attempts,
		Attempt{ID: uuid.New(), Status: SpecialistRunning, StartedAt: now,
			ExternalEffects: []ExternalEffect{{
				Kind: "deploy", Target: "production", IdempotencyKey: "deploy-1",
				State: "started",
			}}},
	)
	if err := second.save(context.Background(), actor, &document); err != nil {
		t.Fatal(err)
	}
	third, err := NewSupervisorService(store, fixedClock{now: now.Add(time.Minute)}, workService)
	if err != nil {
		t.Fatal(err)
	}
	uncertain, err := third.Get(context.Background(), actor, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if uncertain.Status != SupervisorOutcomeUnknown ||
		uncertain.Tasks[0].Status != SpecialistOutcomeUnknown ||
		uncertain.Tasks[0].Attempts[len(uncertain.Tasks[0].Attempts)-1].
			ExternalEffects[0].State != "outcome_unknown" {
		t.Fatalf("uncertain external effect was retried: %+v", uncertain)
	}
}

func TestSupervisorRejectsOverBudgetAndDuplicateCompletion(t *testing.T) {
	workService, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract := putSupervisorContract(t, workService, actor, 1, false)
	supervisor, err := NewSupervisorService(store, fixedClock{now: supervisorBaseTime}, workService)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan uuid.UUID, 1)
	supervisor.SetExecutor(supervisorExecutorFunc(func(
		_ context.Context, _ TaskPacket, attemptID uuid.UUID,
	) (WorkerResult, error) {
		finished <- attemptID
		return WorkerResult{
			AttemptID: attemptID, Status: SpecialistCompleted, Progress: 100,
			Usage: BudgetUsage{ToolCalls: 2},
		}, nil
	}))
	budget := defaultSupervisorBudget()
	budget.MaxToolCalls = 1
	run, err := supervisor.Start(context.Background(), actor, SupervisorStartInput{
		ContractID: contract.ID, Budget: budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	attemptID := <-finished
	paused := waitForSupervisor(t, supervisor, actor, run.ID,
		func(found SupervisorRun) bool { return found.Status == SupervisorPaused })
	if paused.Usage.ToolCalls != 2 || paused.Tasks[0].Status != SpecialistBlocked ||
		paused.Tasks[0].BlockingReason == "" {
		t.Fatalf("budget pause = %+v", paused)
	}
	supervisor.finishAttempt(actor, run.ID, "item-00", attemptID,
		WorkerResult{AttemptID: attemptID, Status: SpecialistCompleted,
			Usage: BudgetUsage{ToolCalls: 99}}, nil)
	after, err := supervisor.Get(context.Background(), actor, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Usage.ToolCalls != 2 || len(after.Tasks[0].Attempts) != 1 {
		t.Fatalf("duplicate completion mutated state: %+v", after)
	}
}

func TestSupervisorProjectBudgetStopsAggregateOversubscription(t *testing.T) {
	workService, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	projectID := uuid.New()
	firstContract := putSupervisorContract(t, workService, actor, 1, false)
	secondContract := putSupervisorContract(t, workService, actor, 1, false)
	supervisor, err := NewSupervisorService(
		store,
		fixedClock{now: supervisorBaseTime},
		workService,
	)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var started atomic.Int32
	supervisor.SetExecutor(supervisorExecutorFunc(func(
		ctx context.Context,
		_ TaskPacket,
		_ uuid.UUID,
	) (WorkerResult, error) {
		started.Add(1)
		select {
		case <-ctx.Done():
			return WorkerResult{}, ctx.Err()
		case <-release:
			return WorkerResult{
				Status: SpecialistWaitingEvidence,
			}, nil
		}
	}))
	outcomeBudget := defaultSupervisorBudget()
	outcomeBudget.MaxParallel = 1
	outcomeBudget.MaxTokens = 100
	projectBudget := outcomeBudget
	first, err := supervisor.Start(
		context.Background(),
		actor,
		SupervisorStartInput{
			ContractID:    firstContract.ID,
			ProjectID:     &projectID,
			Budget:        outcomeBudget,
			ProjectBudget: &projectBudget,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForSupervisor(
		t,
		supervisor,
		actor,
		first.ID,
		func(found SupervisorRun) bool {
			return started.Load() == 1 &&
				found.ProjectUsage.Tokens == projectBudget.MaxTokens
		},
	)
	second, err := supervisor.Start(
		context.Background(),
		actor,
		SupervisorStartInput{
			ContractID:    secondContract.ID,
			ProjectID:     &projectID,
			Budget:        outcomeBudget,
			ProjectBudget: &projectBudget,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	current, err := supervisor.Get(
		context.Background(),
		actor,
		second.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if started.Load() != 1 ||
		current.Status != SupervisorWaiting ||
		current.Tasks[0].Status != SpecialistReady ||
		current.ProjectUsage.Tokens != projectBudget.MaxTokens {
		t.Fatalf(
			"project budget started=%d run=%+v",
			started.Load(),
			current,
		)
	}
	if _, err := supervisor.Cancel(
		context.Background(),
		actor,
		first.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Cancel(
		context.Background(),
		actor,
		second.ID,
	); err != nil {
		t.Fatal(err)
	}
	close(release)
}

func TestSupervisorRejectsNarrativeCompletionWithoutVerifiedArtifacts(t *testing.T) {
	workService, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract := putSupervisorContract(t, workService, actor, 1, false)
	supervisor, err := NewSupervisorService(
		store,
		fixedClock{now: supervisorBaseTime},
		workService,
	)
	if err != nil {
		t.Fatal(err)
	}
	supervisor.SetExecutor(supervisorExecutorFunc(func(
		_ context.Context,
		_ TaskPacket,
		attemptID uuid.UUID,
	) (WorkerResult, error) {
		return WorkerResult{
			AttemptID: attemptID,
			Status:    SpecialistCompleted,
			Progress:  100,
			Summary:   "I finished everything.",
			Usage: BudgetUsage{
				CostKnown: true, ProviderSpendKnown: true,
			},
			Findings: []Finding{{
				Kind: "claim", Summary: "All criteria pass.",
			}},
		}, nil
	}))
	run, err := supervisor.Start(
		context.Background(),
		actor,
		SupervisorStartInput{
			ContractID: contract.ID,
			Budget:     defaultSupervisorBudget(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	blocked := waitForSupervisor(
		t,
		supervisor,
		actor,
		run.ID,
		func(found SupervisorRun) bool {
			return found.Status == SupervisorBlocked
		},
	)
	portfolio, err := workService.Get(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Tasks[0].Status != SpecialistWaitingEvidence ||
		blocked.Tasks[0].Progress >= 100 ||
		portfolio.Contracts[0].Status == StatusCompleted {
		t.Fatalf(
			"narrative completion crossed evidence gate: run=%+v contract=%+v",
			blocked,
			portfolio.Contracts[0],
		)
	}
}

func TestSupervisorValidatesLeastAuthorityAndParallelBounds(t *testing.T) {
	workService, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract := putSupervisorContract(t, workService, actor, 1, false)
	supervisor, err := NewSupervisorService(store, fixedClock{now: supervisorBaseTime}, workService)
	if err != nil {
		t.Fatal(err)
	}
	budget := defaultSupervisorBudget()
	budget.MaxParallel = MaxParallelSpecialists + 1
	if _, err := supervisor.Start(context.Background(), actor, SupervisorStartInput{
		ContractID: contract.ID, Budget: budget,
	}); err == nil {
		t.Fatal("unsafe parallel budget accepted")
	}
	packet := compileTaskPacket(WorkItem{
		ID: "release", Title: "Deploy release", Criteria: []string{"live"},
	}, defaultSupervisorBudget(), TaskOverride{})
	if packet.Specialist != SpecialistOperations || packet.Scope.ExternalEffects ||
		len(packet.ParentAuthority) == 0 {
		t.Fatalf("operations authority was not least-privilege: %+v", packet)
	}
}

var _ SupervisorExecutor = supervisorExecutorFunc(nil)
