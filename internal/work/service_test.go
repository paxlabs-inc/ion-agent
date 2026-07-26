package work

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func openTestService(t *testing.T) (*Service, *session.Store, string) {
	t.Helper()
	directory := t.TempDir()
	cipher, err := vault.New(bytes.Repeat([]byte{0x73}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(context.Background(), filepath.Join(directory, "sessions.db"), cipher,
		fixedClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}, 8192)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, fixedClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}, directory)
	if err != nil {
		store.Close(context.Background())
		t.Fatal(err)
	}
	return service, store, directory
}

func TestCompletionRequiresServerVerifiedCoverage(t *testing.T) {
	service, store, workspace := openTestService(t)
	defer store.Close(context.Background())
	actor := uuid.New()
	contract, err := service.PutContract(context.Background(), actor, ContractInput{
		Goal: "Ship exact recovery", Deliverable: "recovery report",
		DoneCriteria:         []Criterion{{ID: "tests", Description: "reconnect tests pass"}, {ID: "report", Description: "report exists"}},
		VerificationRequired: []string{"targeted test", "server digest"}, NextAction: "run tests",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteContract(context.Background(), actor, contract.ID); err == nil {
		t.Fatal("completion succeeded without evidence")
	}
	if err := os.WriteFile(filepath.Join(workspace, "report.txt"), []byte("verified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := service.RecordArtifact(context.Background(), actor, ArtifactInput{
		ContractID: contract.ID, Kind: "report", Title: "acceptance", Reference: "report.txt",
		CriteriaCovered: []string{"tests", "report"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteContract(context.Background(), actor, contract.ID); err == nil {
		t.Fatal("completion succeeded on unverified client record")
	}
	verified, err := service.VerifyArtifact(context.Background(), actor, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.SHA256) != 64 || verified.VerifiedAt == nil || verified.Verification != "server_sha256" {
		t.Fatalf("artifact was not server verified: %+v", verified)
	}
	completed, err := service.CompleteContract(context.Background(), actor, contract.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleted || completed.CompletedAt == nil {
		t.Fatalf("unexpected completed contract: %+v", completed)
	}
}

func TestCriterionWorkItemsPersistDependenciesAndRequireEvidence(t *testing.T) {
	service, store, workspace := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	contract, err := service.PutContract(context.Background(), actor, ContractInput{
		Goal: "Build safely", Deliverable: "app",
		DoneCriteria:         []Criterion{{ID: "api", Description: "API works"}, {ID: "ui", Description: "UI works"}},
		VerificationRequired: []string{"tests"}, NextAction: "implement API",
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.SyncWorkItems(context.Background(), actor, contract.ID, []WorkItemInput{
		{ID: "api", Title: "Implement API routes", Criteria: []string{"api"}},
		{ID: "ui", Title: "Verify frontend", Criteria: []string{"ui"}, DependsOn: []string{"api"}},
	})
	if err != nil || items[0].Status != WorkItemReady || items[1].Status != WorkItemPending {
		t.Fatalf("initial work items = %+v, %v", items, err)
	}
	if _, err := service.UpdateWorkItem(context.Background(), actor, WorkItemUpdate{
		ContractID: contract.ID, ItemID: "ui", Status: WorkItemRunning,
	}); err == nil {
		t.Fatal("dependent item started before its dependency")
	}
	if _, err := service.UpdateWorkItem(context.Background(), actor, WorkItemUpdate{
		ContractID: contract.ID, ItemID: "api", Status: WorkItemRunning,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "api.txt"), []byte("passed"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := service.RecordArtifact(context.Background(), actor, ArtifactInput{
		ContractID: contract.ID, Kind: "test", Title: "API test", Reference: "api.txt", CriteriaCovered: []string{"api"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyArtifact(context.Background(), actor, artifact.ID); err != nil {
		t.Fatal(err)
	}
	brief, err := service.Brief(context.Background(), actor, nil)
	if err != nil || brief.WorkItems[0].Status != WorkItemCompleted || brief.WorkItems[1].Status != WorkItemReady {
		t.Fatalf("evidence reconciliation = %+v, %v", brief.WorkItems, err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	cipher, err := vault.New(bytes.Repeat([]byte{0x73}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err = session.Open(context.Background(), filepath.Join(workspace, "sessions.db"), cipher,
		fixedClock{now: time.Date(2026, 7, 21, 12, 1, 0, 0, time.UTC)}, 8192)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(store,
		fixedClock{now: time.Date(2026, 7, 21, 12, 1, 0, 0, time.UTC)}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	brief, err = restarted.Brief(context.Background(), actor, nil)
	if err != nil || len(brief.WorkItems) != 2 || brief.WorkItems[0].Status != WorkItemCompleted || brief.WorkItems[1].Status != WorkItemReady {
		t.Fatalf("restart work items = %+v, %v", brief.WorkItems, err)
	}
}

func TestPutContractWithWorkItemsRejectsInvalidPlanWithoutPartialContract(t *testing.T) {
	service, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor := uuid.New()
	input := ContractInput{
		Goal: "Build safely", Deliverable: "verified app",
		DoneCriteria:         []Criterion{{ID: "app.runs", Description: "The app runs"}},
		VerificationRequired: []string{"npm test"}, NextAction: "build it",
	}
	if _, _, err := service.PutContractWithWorkItems(
		context.Background(), actor, input, nil,
	); err == nil {
		t.Fatal("plan without work items succeeded")
	}
	portfolio, err := service.Get(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if portfolio.Revision != 0 || len(portfolio.Contracts) != 0 || len(portfolio.WorkItems) != 0 {
		t.Fatalf("rejected plan mutated portfolio: %+v", portfolio)
	}
	contract, items, err := service.PutContractWithWorkItems(
		context.Background(), actor, input, []WorkItemInput{{
			ID: "app.build", Title: "Build and verify", Criteria: []string{"app.runs"},
		}},
	)
	if err != nil || contract.ID == uuid.Nil || len(items) != 1 || items[0].ContractID != contract.ID {
		t.Fatalf("atomic plan = contract %+v items %+v err %v", contract, items, err)
	}
}

func TestBriefDoesNotLeakActiveContractIntoAnotherSession(t *testing.T) {
	service, store, _ := openTestService(t)
	defer func() { _ = store.Close(context.Background()) }()
	actor, firstSession, secondSession := uuid.New(), uuid.New(), uuid.New()
	if _, err := service.PutContract(context.Background(), actor, ContractInput{
		SessionID: &firstSession, Goal: "First project", Deliverable: "first app",
		DoneCriteria:         []Criterion{{ID: "first", Description: "First app works"}},
		VerificationRequired: []string{"test first app"}, NextAction: "build first app",
	}); err != nil {
		t.Fatal(err)
	}
	first, err := service.Brief(context.Background(), actor, &firstSession)
	if err != nil || first.Contract == nil || first.Contract.SessionID == nil ||
		*first.Contract.SessionID != firstSession {
		t.Fatalf("first session brief = %+v, %v", first, err)
	}
	second, err := service.Brief(context.Background(), actor, &secondSession)
	if err != nil {
		t.Fatal(err)
	}
	if second.Contract != nil || len(second.WorkItems) != 0 || second.NextAction != "" {
		t.Fatalf("second session inherited first contract: %+v", second)
	}
}

func TestArtifactVerificationRejectsTraversalAndSymlinkEscape(t *testing.T) {
	service, store, workspace := openTestService(t)
	defer store.Close(context.Background())
	actor := uuid.New()
	contract, err := service.PutContract(context.Background(), actor, ContractInput{
		Goal: "Bound artifacts", Deliverable: "safe file",
		DoneCriteria:         []Criterion{{ID: "safe", Description: "file stays inside workspace"}},
		VerificationRequired: []string{"digest"}, NextAction: "record file",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordArtifact(context.Background(), actor, ArtifactInput{
		ContractID: contract.ID, Kind: "file", Title: "escape", Reference: "../secret", CriteriaCovered: []string{"safe"},
	}); err == nil {
		t.Fatal("traversal reference was accepted")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	artifact, err := service.RecordArtifact(context.Background(), actor, ArtifactInput{
		ContractID: contract.ID, Kind: "file", Title: "link", Reference: "escape", CriteriaCovered: []string{"safe"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyArtifact(context.Background(), actor, artifact.ID); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("symlink escape result = %v", err)
	}
}

func TestArtifactVerificationUsesDurableWorkspaceBinding(t *testing.T) {
	service, store, _ := openTestService(t)
	defer store.Close(context.Background())
	actor := uuid.New()
	contract, err := service.PutContract(context.Background(), actor, ContractInput{
		Goal: "Verify a project artifact", Deliverable: "project page",
		DoneCriteria:         []Criterion{{ID: "page", Description: "page exists"}},
		VerificationRequired: []string{"digest"}, NextAction: "record page",
	})
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()
	otherRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "index.html"), []byte("project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherRoot, "index.html"), []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := service.RecordArtifactInWorkspace(context.Background(), actor, ArtifactInput{
		ContractID: contract.ID, Kind: "web_page", Title: "page", Reference: "index.html",
		CriteriaCovered: []string{"page"},
	}, projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.WorkspaceRoot != projectRoot {
		t.Fatalf("artifact root = %q, want %q", artifact.WorkspaceRoot, projectRoot)
	}
	if _, err := service.VerifyArtifactInWorkspace(
		context.Background(), actor, artifact.ID, otherRoot,
	); err == nil || !strings.Contains(err.Error(), "different workspace") {
		t.Fatalf("cross-workspace verification result = %v", err)
	}
	verified, err := service.VerifyArtifact(context.Background(), actor, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.SHA256 == "" || verified.VerifiedAt == nil {
		t.Fatalf("project artifact was not verified: %+v", verified)
	}
}

func TestPortfolioIsEncryptedRestartSafeAndActorIsolated(t *testing.T) {
	service, store, workspace := openTestService(t)
	actorA, actorB := uuid.New(), uuid.New()
	secretGoal := "private launch outcome"
	if _, err := service.PutContract(context.Background(), actorA, ContractInput{
		Goal: secretGoal, Deliverable: "release", DoneCriteria: []Criterion{{ID: "live", Description: "live health passes"}},
		VerificationRequired: []string{"restart"}, NextAction: "deploy",
	}); err != nil {
		t.Fatal(err)
	}
	other, err := service.Brief(context.Background(), actorB, nil)
	if err != nil {
		t.Fatal(err)
	}
	if other.Contract != nil {
		t.Fatalf("cross-actor contract leaked: %+v", other.Contract)
	}
	databaseBytes, err := os.ReadFile(filepath.Join(workspace, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, []byte(secretGoal)) {
		t.Fatal("plaintext contract leaked into database")
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Reopen with the same encryption key and verify the durable projection.
	cipher, err := vault.New(bytes.Repeat([]byte{0x73}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := session.Open(context.Background(), filepath.Join(workspace, "sessions.db"), cipher,
		fixedClock{now: time.Date(2026, 7, 21, 12, 1, 0, 0, time.UTC)}, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	restarted, err := NewService(reopened, fixedClock{now: time.Date(2026, 7, 21, 12, 1, 0, 0, time.UTC)}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := restarted.Brief(context.Background(), actorA, nil)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Contract == nil || brief.Contract.Goal != secretGoal {
		t.Fatalf("restart brief = %+v", brief)
	}
}

func TestReviewRouterAndAutonomyDefaults(t *testing.T) {
	service, store, _ := openTestService(t)
	defer store.Close(context.Background())
	brief, err := service.Brief(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Autonomy.Mode != AutonomySuggest || brief.Autonomy.MaxToolCalls != defaultToolCallLimit {
		t.Fatalf("unsafe default autonomy: %+v", brief.Autonomy)
	}
	plan := Review(ReviewInput{TouchesAuth: true, TouchesData: true, TouchesMigration: true,
		UserFacing: true, PerformanceSensitive: true, LongRunning: true, ReleaseCandidate: true})
	want := map[string]bool{"correctness": false, "evidence": false, "security": false,
		"usability": false, "accessibility": false, "operability": false, "data": false,
		"migration": false, "performance": false, "release": false}
	for _, lens := range plan.Lenses {
		want[lens.ID] = true
	}
	for id, selected := range want {
		if !selected {
			t.Fatalf("lens %q not selected: %+v", id, plan)
		}
	}
}

func TestWorkflowRunResumesAndRequiresVerifiedEvidenceAndHumanGate(t *testing.T) {
	service, store, workspace := openTestService(t)
	actor := uuid.New()
	contract, err := service.PutContract(context.Background(), actor, ContractInput{
		Goal: "Ship safely", Deliverable: "release.txt",
		DoneCriteria:         []Criterion{{ID: "release", Description: "release evidence exists"}},
		VerificationRequired: []string{"digest"}, NextAction: "follow production recipe",
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartWorkflow(context.Background(), actor, "production-change", contract.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.CurrentStageIndex != 0 || run.Status != "active" {
		t.Fatalf("started run = %+v", run)
	}
	run, err = service.AdvanceWorkflow(context.Background(), actor, WorkflowAdvanceInput{RunID: run.ID, StageID: "contract"})
	if err != nil || run.CurrentStageIndex != 1 {
		t.Fatalf("contract advance = %+v, %v", run, err)
	}
	if _, err := service.AdvanceWorkflow(context.Background(), actor, WorkflowAdvanceInput{RunID: run.ID, StageID: "implement"}); err == nil {
		t.Fatal("implementation advanced without verified evidence")
	}
	if err := os.WriteFile(filepath.Join(workspace, "release.txt"), []byte("verified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := service.RecordArtifact(context.Background(), actor, ArtifactInput{ContractID: contract.ID,
		Kind: "release", Title: "release evidence", Reference: "release.txt", CriteriaCovered: []string{"release"}})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err = service.VerifyArtifact(context.Background(), actor, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, stageID := range []string{"implement", "review", "verify"} {
		run, err = service.AdvanceWorkflow(context.Background(), actor, WorkflowAdvanceInput{
			RunID: run.ID, StageID: stageID, ArtifactIDs: []uuid.UUID{artifact.ID},
		})
		if err != nil {
			t.Fatalf("advance %s: %v", stageID, err)
		}
	}
	if _, err := service.AdvanceWorkflow(context.Background(), actor, WorkflowAdvanceInput{
		RunID: run.ID, StageID: "release", ArtifactIDs: []uuid.UUID{artifact.ID},
	}); err == nil {
		t.Fatal("human-gated release advanced without confirmation")
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	cipher, err := vault.New(bytes.Repeat([]byte{0x73}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := session.Open(context.Background(), filepath.Join(workspace, "sessions.db"), cipher,
		fixedClock{now: time.Date(2026, 7, 21, 12, 2, 0, 0, time.UTC)}, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	restarted, err := NewService(reopened, fixedClock{now: time.Date(2026, 7, 21, 12, 2, 0, 0, time.UTC)}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	portfolio, err := restarted.Get(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(portfolio.Workflows) != 1 || portfolio.Workflows[0].CurrentStageIndex != 4 {
		t.Fatalf("restart run = %+v", portfolio.Workflows)
	}
	completed, err := restarted.AdvanceWorkflow(context.Background(), actor, WorkflowAdvanceInput{
		RunID: run.ID, StageID: "release", ArtifactIDs: []uuid.UUID{artifact.ID}, Confirmed: true,
	})
	if err != nil || completed.Status != "completed" {
		t.Fatalf("confirmed completion = %+v, %v", completed, err)
	}
}
