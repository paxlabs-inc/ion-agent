package studio

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestIntentProposalRestartIsolationDecisionAndDrift(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, 7, 22, 7, 0, 0, 0, time.UTC)}
	cipher, err := vault.New(bytes.Repeat([]byte{0x41}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"), cipher, clock, 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	workspaceRoot := t.TempDir()
	projects, err := projectcontrol.NewService(store, clock, projectcontrol.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		ArchiveRoot:   filepath.Join(t.TempDir(), "archives"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer projects.Close()
	work, err := workcontrol.NewService(store, clock, workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	actor, other := uuid.New(), uuid.New()
	project, err := projects.CreateTemplate(ctx, operationMeta(actor, "studio-project"), projectcontrol.TemplateInput{
		Name: "Studio project", Template: "empty", Host: projectcontrol.HostDirectLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeRepositoryFixture(t, project.Root)
	contract, err := work.PutContract(ctx, actor, workcontrol.ContractInput{
		Goal: "Add a visible status page", Deliverable: "working status page",
		DoneCriteria:         []workcontrol.Criterion{{ID: "status.visible", Description: "status is visible"}},
		VerificationRequired: []string{"go test ./..."}, NextAction: "review proposed specification",
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, clock, projects, work)
	if err != nil {
		t.Fatal(err)
	}
	delta := validDelta()
	intent, err := service.Compile(ctx, actor, CompileInput{
		ProjectID: project.ID, OutcomeContractID: contract.ID, WorkspaceRevision: project.WorkspaceRevision,
		Goal: "Add a visible status page", Rationale: "No existing requirement covers the status page",
		DependencyImpact: []string{"status route precedes browser acceptance"}, Delta: &delta,
		Assumptions: []Assumption{{ID: "route", Statement: "Use the existing router", Reversible: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.Inspection.AuthoritativeSpecPath != "spec/spec.kvx" ||
		intent.Inspection.GeneratedTasksPath != "spec/tasks.md" ||
		len(intent.Inspection.InstructionFiles) != 1 || intent.Inspection.InstructionFiles[0] != "AGENTS.md" ||
		len(intent.Proposals) != 1 || intent.Proposals[0].Status != ProposalProposed {
		t.Fatalf("compiled intent = %+v", intent)
	}
	if _, err := service.Get(ctx, other, intent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-actor read = %v", err)
	}
	restarted, err := NewService(store, clock, projects, work)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := restarted.Get(ctx, actor, intent.ID)
	if err != nil || resumed.Goal != intent.Goal {
		t.Fatalf("restart resume = %+v, %v", resumed, err)
	}
	rejected, err := restarted.DecideProposal(ctx, actor, intent.ID, intent.Proposals[0].ID, false,
		"The user rejected the first behavior")
	if err != nil || rejected.Status != ProposalRejected {
		t.Fatalf("rejected proposal = %+v, %v", rejected, err)
	}
	change, err := restarted.ProposeScopeChange(ctx, actor, ScopeChangeInput{IntentID: intent.ID,
		Rationale:        "Repository inspection found an authenticated route",
		DependencyImpact: []string{"authentication must precede the page"}, Delta: delta})
	if err != nil || change.Version != 2 || change.Status != ProposalProposed {
		t.Fatalf("scope proposal = %+v, %v", change, err)
	}
	replacement, err := restarted.ProposeScopeChange(ctx, actor, ScopeChangeInput{IntentID: intent.ID,
		Rationale:        "The authenticated route also requires a recovery state",
		DependencyImpact: []string{"recovery follows authentication"}, Delta: delta})
	if err != nil || replacement.Supersedes == nil || *replacement.Supersedes != change.ID {
		t.Fatalf("superseding proposal = %+v, %v", replacement, err)
	}
	accepted, err := restarted.DecideProposal(ctx, actor, intent.ID, replacement.ID, true, "Approved by the user")
	if err != nil || accepted.Status != ProposalAccepted {
		t.Fatalf("accepted proposal = %+v, %v", accepted, err)
	}
	applied, err := restarted.ApplyProposal(ctx, actor, intent.ID, accepted.ID)
	if err != nil || applied.Proposals[len(applied.Proposals)-1].AppliedAt == nil {
		t.Fatalf("applied proposal = %+v, %v", applied, err)
	}
	if _, err := restarted.ApplyProposal(ctx, actor, intent.ID, accepted.ID); err != nil {
		t.Fatalf("idempotent apply = %v", err)
	}
	specification, err := os.ReadFile(filepath.Join(project.Root, "spec", "spec.kvx"))
	if err != nil || bytes.Count(specification, []byte("BEGIN ION STUDIO CHANGE "+accepted.ID.String())) != 1 {
		t.Fatalf("authoritative change = %s, %v", specification, err)
	}
	initialCompletion, err := restarted.Completion(ctx, actor, intent.ID)
	if err != nil || initialCompletion.Ready {
		t.Fatalf("false completion was allowed = %+v, %v", initialCompletion, err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "acceptance.txt"), []byte("verified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := work.RecordArtifact(ctx, actor, workcontrol.ArtifactInput{ContractID: contract.ID,
		Kind: "report", Title: "acceptance", Reference: "acceptance.txt", CriteriaCovered: []string{"status.visible"}})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err = work.VerifyArtifact(ctx, actor, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, correlation := range []CorrelationInput{
		{IntentID: intent.ID, Kind: CorrelationTask, Reference: "status.route", Criteria: []string{"status.visible"}, Description: "implementation task"},
		{IntentID: intent.ID, Kind: CorrelationPatch, Reference: "patch-1", Criteria: []string{"status.visible"}, Description: "atomic patch"},
		{IntentID: intent.ID, Kind: CorrelationTool, Reference: "tool-event-1", Criteria: []string{"status.visible"}, Description: "tool dispatch"},
		{IntentID: intent.ID, Kind: CorrelationReview, Reference: "review-1", Criteria: []string{"status.visible"}, Description: "independent review"},
		{IntentID: intent.ID, Kind: CorrelationVerification, Reference: "verification-1", Criteria: []string{"status.visible"}, Description: "verification run"},
		{IntentID: intent.ID, Kind: CorrelationArtifact, Reference: artifact.ID.String(), Criteria: []string{"status.visible"}, Description: "verified evidence"},
	} {
		if _, err := restarted.RecordCorrelation(ctx, actor, correlation); err != nil {
			t.Fatalf("record correlation %s: %v", correlation.Kind, err)
		}
	}
	complete, err := restarted.Completion(ctx, actor, intent.ID)
	if err != nil || !complete.Ready {
		t.Fatalf("evidence-bound completion = %+v, %v", complete, err)
	}
	if err := os.WriteFile(filepath.Join(project.Root, "main.go"), []byte("package main\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	drift, err := restarted.DetectDrift(ctx, actor, intent.ID)
	if err != nil || !drift.ImplementationChanged || !drift.CompletionBlocked {
		t.Fatalf("drift = %+v, %v", drift, err)
	}
	if err := os.WriteFile(filepath.Join(project.Root, "spec", "tasks.md"), []byte("hand edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	drift, err = restarted.DetectDrift(ctx, actor, intent.ID)
	if err != nil || !drift.GeneratedViewChanged || !drift.CompletionBlocked {
		t.Fatalf("generated view drift = %+v, %v", drift, err)
	}
}

func TestCompileFailsClosedForBindingsAmbiguityAndCoverage(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, 7, 22, 7, 30, 0, 0, time.UTC)}
	cipher, _ := vault.New(bytes.Repeat([]byte{0x42}, vault.KeySize))
	store, err := session.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"), cipher, clock, 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	root := t.TempDir()
	projects, _ := projectcontrol.NewService(store, clock, projectcontrol.ServiceConfig{
		WorkspaceRoot: root, ArchiveRoot: filepath.Join(t.TempDir(), "archives")})
	defer projects.Close()
	work, _ := workcontrol.NewService(store, clock, root)
	actor := uuid.New()
	project, err := projects.CreateTemplate(ctx, operationMeta(actor, "create"), projectcontrol.TemplateInput{
		Name: "Bound", Template: "empty", Host: projectcontrol.HostDirectLocal})
	if err != nil {
		t.Fatal(err)
	}
	contract, _ := work.PutContract(ctx, actor, workcontrol.ContractInput{Goal: "goal", Deliverable: "result",
		DoneCriteria:         []workcontrol.Criterion{{ID: "done", Description: "done"}},
		VerificationRequired: []string{"test"}, NextAction: "compile"})
	service, _ := NewService(store, clock, projects, work)
	delta := validDelta()
	if _, err := service.Compile(ctx, actor, CompileInput{ProjectID: project.ID,
		OutcomeContractID: contract.ID, WorkspaceRevision: project.WorkspaceRevision + 1,
		Goal: "goal", Rationale: "new behavior", Delta: &delta}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale revision = %v", err)
	}
	intent, err := service.Compile(ctx, actor, CompileInput{ProjectID: project.ID,
		OutcomeContractID: contract.ID, WorkspaceRevision: project.WorkspaceRevision,
		Goal: "goal", Rationale: "new behavior", Delta: &delta,
		Assumptions: []Assumption{{ID: "billing", Statement: "Charge the customer", Material: true,
			DecisionNeed: "Choose whether billing is enabled"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideProposal(ctx, actor, intent.ID, intent.Proposals[0].ID, true,
		"Proceed"); !errors.Is(err, ErrDecision) {
		t.Fatalf("material ambiguity = %v", err)
	}
	invalid := validDelta()
	invalid.Tasks[0].Criteria = []string{"unknown"}
	if _, err := service.ProposeScopeChange(ctx, actor, ScopeChangeInput{IntentID: intent.ID,
		Rationale: "invalid coverage", Delta: invalid}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown criterion = %v", err)
	}
}

func validDelta() SpecDelta {
	return SpecDelta{
		UserVisibleBehavior: []string{"A status page shows current state"},
		NonGoals:            []string{"No deployment"}, Constraints: []string{"Use the shared runtime"},
		Criteria:           []Criterion{{ID: "status.visible", Description: "The current state is visible"}},
		SecurityBoundaries: []string{"Authenticated actor only"}, DataBoundaries: []string{"Project-scoped state"},
		Migration: []string{"No data migration"}, Rollback: []string{"Remove the route"},
		Verification: []string{"go test ./..."},
		Tasks:        []PlannedTask{{ID: "status.route", Title: "Build status route", Criteria: []string{"status.visible"}}},
	}
}

func operationMeta(actor uuid.UUID, key string) projectcontrol.OperationMeta {
	return projectcontrol.OperationMeta{ActorID: actor, RequestID: uuid.New(), IdempotencyKey: key,
		PolicyClassification: projectcontrol.PolicyYellow, Deadline: time.Date(2030, 7, 23, 0, 0, 0, 0, time.UTC),
		CorrelationID: uuid.New()}
}

func writeRepositoryFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "spec"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"AGENTS.md": "Follow repository instructions.\n", "spec/spec.kvx": "[req.1]\n",
		"spec/tasks.md": "# Tasks\n", "main.go": "package main\n", ".env": "SECRET=must-not-index\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
