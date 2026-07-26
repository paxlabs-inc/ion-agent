package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestVerificationManifestRunsEvidenceRepairAndWaiverBoundaries(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "verification")
	writeVerificationScript(t, root, "compile-check", "printf 'src/app.ts:12:4: compile failure 12345\\n'\nexit 1\n")
	writeVerificationScript(t, root, "visual-check", "printf 'visual passed\\n'\nprintf 'screenshot' > screenshot.png\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "verification-attach"),
		AttachInput{Name: "Verification", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	criteria := []VerificationCriterion{
		{ID: "compile.clean", Description: "The project compiles.", Kinds: []string{"compile"}},
		{ID: "visual.current", Description: "The visual result is current.", Kinds: []string{"visual"}},
	}
	manifest, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision, Criteria: criteria,
		Gates: []VerificationGate{
			{ID: "compile", Kind: "compile", Argv: []string{"./compile-check"}, TimeoutSeconds: 10,
				Environment: []string{"CI=1"}, Required: true, Criteria: []string{"compile.clean"},
				EvidenceKinds: []string{"logs"}, Available: true},
			{ID: "visual", Kind: "visual", Argv: []string{"./visual-check"}, TimeoutSeconds: 10,
				Required: true, Criteria: []string{"visual.current"}, EvidenceKinds: []string{"logs", "screenshot"},
				EvidencePaths: []string{"screenshot.png"}, Available: true},
		},
	})
	if err != nil || manifest.Revision != 1 || len(manifest.Gates) != 2 {
		t.Fatalf("manifest = %+v, %v", manifest, err)
	}
	first, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"compile"}, MaxAttempts: 3,
	})
	if err != nil || first.Status != "failed" || first.Repair.State != "diagnose_patch_rerun" ||
		len(first.Results) != 1 || len(first.Results[0].FailureSignature) != 24 {
		t.Fatalf("first targeted run = %+v, %v", first, err)
	}
	second, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"compile"}, MaxAttempts: 3,
	})
	if err != nil || second.Repair.State != "stop_stagnation" ||
		second.Results[0].FailureSignature != first.Results[0].FailureSignature {
		t.Fatalf("stagnation run = %+v, %v", second, err)
	}
	writeVerificationScript(t, root, "compile-check", "test \"$CI\" = \"1\" || exit 2\nprintf 'compile passed\\n'\n")
	flaky, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"compile"}, MaxAttempts: 4,
	})
	if err != nil || flaky.Status != "flaky" || flaky.Repair.State != "stop_flaky" ||
		len(flaky.UncoveredCriteria) != len(criteria) {
		t.Fatalf("flaky run = %+v, %v", flaky, err)
	}
	waiver, err := service.PutVerificationWaiver(ctx, actor, VerificationWaiverInput{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"visual"},
		Criteria: []string{"visual.current"}, Reason: "Browser runner is temporarily unavailable.",
		Risk: "Visual regressions remain possible.", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil || waiver.ID == uuid.Nil {
		t.Fatalf("waiver = %+v, %v", waiver, err)
	}
	waived, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true, MaxAttempts: 10,
	})
	if err != nil || waived.Status != "waived" || len(waived.UncoveredCriteria) != 1 ||
		waived.UncoveredCriteria[0] != "visual.current" || waived.Results[1].Status != "waived" {
		t.Fatalf("waived full run = %+v, %v", waived, err)
	}
	runs, err := service.ListVerificationRuns(ctx, actor, project.ID)
	if err != nil || len(runs) != 4 {
		t.Fatalf("durable runs = %d, %v", len(runs), err)
	}
	if other, err := service.ListVerificationRuns(ctx, uuid.New(), project.ID); err != nil || len(other) != 0 {
		t.Fatalf("cross-actor verification runs = %+v, %v", other, err)
	}
}

func TestVerificationRejectsFalseGreenStaleAndInjectedFailureModes(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "failure-modes")
	for _, kind := range []string{"compile", "test", "visual", "accessibility", "security", "migration", "performance"} {
		writeVerificationScript(t, root, kind+"-check", "printf '"+kind+" failed\\n'\nexit 1\n")
	}
	writeVerificationScript(t, root, "false-green", "printf 'claimed success without report\\n'\n")
	writeVerificationScript(t, root, "timeout-check", "sleep 2\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "verification-failures"),
		AttachInput{Name: "Failure modes", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	criteria, gates := []VerificationCriterion{}, []VerificationGate{}
	kinds := []string{"compile", "test", "visual", "accessibility", "security", "migration", "performance"}
	for _, kind := range kinds {
		criterion := kind + ".clean"
		criteria = append(criteria, VerificationCriterion{ID: criterion, Description: kind + " gate passes", Kinds: []string{kind}})
		gates = append(gates, VerificationGate{ID: kind, Kind: kind, Argv: []string{"./" + kind + "-check"},
			TimeoutSeconds: 10, Required: true, Criteria: []string{criterion}, EvidenceKinds: []string{"logs"}, Available: true})
	}
	criteria = append(criteria,
		VerificationCriterion{ID: "report.present", Description: "Required report exists.", Kinds: []string{"report"}},
		VerificationCriterion{ID: "timeout.bounded", Description: "Timeout is enforced.", Kinds: []string{"timeout"}},
		VerificationCriterion{ID: "environment.ready", Description: "Environment exists.", Kinds: []string{"environment"}})
	gates = append(gates,
		VerificationGate{ID: "false-green", Kind: "report", Argv: []string{"./false-green"},
			TimeoutSeconds: 10, Required: true, Criteria: []string{"report.present"},
			EvidenceKinds: []string{"logs", "report"}, EvidencePaths: []string{"missing-report.json"}, Available: true},
		VerificationGate{ID: "timeout", Kind: "timeout", Argv: []string{"./timeout-check"},
			TimeoutSeconds: 1, Required: true, Criteria: []string{"timeout.bounded"}, EvidenceKinds: []string{"logs"}, Available: true},
		VerificationGate{ID: "environment", Kind: "environment", TimeoutSeconds: 1, Required: true,
			Criteria: []string{"environment.ready"}, EvidenceKinds: []string{"unavailable"}, Available: false,
			UnavailableReason: "Injected runner outage."})
	manifest, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision, Criteria: criteria, Gates: gates,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true, MaxAttempts: 1,
	})
	if err != nil || run.Status != "failed" || len(run.CriteriaCovered) != 0 {
		t.Fatalf("injected failure run = %+v, %v", run, err)
	}
	statuses := map[string]string{}
	for _, result := range run.Results {
		statuses[result.GateID] = result.Status
	}
	for _, kind := range kinds {
		if statuses[kind] != "failed" {
			t.Fatalf("%s failure was not preserved: %+v", kind, statuses)
		}
	}
	if statuses["false-green"] != "failed" || statuses["timeout"] != "timeout" ||
		statuses["environment"] != "unavailable" {
		t.Fatalf("false-green, timeout, or environment status = %+v", statuses)
	}
	if run.Repair.State != "stop_timeout" {
		t.Fatalf("repair stop = %+v", run.Repair)
	}
	current, err := service.CurrentVerificationManifest(ctx, actor, project.ID)
	if err != nil || current.ID != manifest.ID {
		t.Fatalf("current manifest = %+v, %v", current, err)
	}
	stale := manifest
	stale.WorkspaceRevision++
	if _, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: stale.WorkspaceRevision,
		Criteria: criteria, Gates: gates,
	}); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale manifest error = %v", err)
	}
	database, err := os.ReadFile(filepath.Join(dataRoot, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(database), "Injected runner outage") ||
		strings.Contains(string(database), "claimed success without report") {
		t.Fatal("verification evidence leaked from encrypted durable state")
	}
}

func TestVerificationDerivesRequiredRepositoryGateKindsWithRunnableEvidence(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "derived-gates")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	packageJSON := `{"scripts":{
		"format":"true","lint":"true","typecheck":"true","build":"true",
		"test:unit":"true","test:integration":"true","test:e2e":"true",
		"test:a11y":"true","test:security":"true","check:dependencies":"true",
		"check:licenses":"true","test:migration":"true","test:performance":"true",
		"package":"true"
	}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "derived-gates-attach"),
		AttachInput{Name: "Derived gates", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"format", "lint", "type", "compile", "unit", "integration", "end-to-end",
		"accessibility", "security", "dependency", "license", "migration", "performance", "package"}
	criteria := make([]VerificationCriterion, 0, len(kinds))
	for _, kind := range kinds {
		criteria = append(criteria, VerificationCriterion{
			ID: kind + ".required", Description: kind + " evidence is required.", Kinds: []string{kind},
		})
	}
	manifest, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision, Criteria: criteria,
	})
	if err != nil {
		t.Fatal(err)
	}
	derivedKinds := map[string]VerificationGate{}
	for _, gate := range manifest.Gates {
		if !gate.Available || len(gate.Argv) != 3 || gate.Argv[0] != "npm" ||
			gate.Argv[1] != "run" || !containsVerificationString(gate.EvidenceKinds, "logs") ||
			!containsVerificationString(gate.Environment, "CI=1") ||
			!containsVerificationString(gate.Environment, "HOME=/nonexistent") ||
			!containsVerificationString(gate.Environment, "PATH=/usr/local/bin:/usr/bin:/bin") {
			t.Fatalf("derived gate is not runnable with exact retained evidence: %+v", gate)
		}
		derivedKinds[gate.Kind] = gate
	}
	for _, kind := range kinds {
		if _, ok := derivedKinds[kind]; !ok {
			t.Fatalf("required repository gate kind %q was not derived: %+v", kind, manifest.Gates)
		}
	}
}

func TestVerificationRepairStopsOnRegressionAndBudgetAndPrioritizesFailure(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "repair-stops")
	writeVerificationScript(t, root, "compile-check", "printf 'compile failure\\n'\nexit 1\n")
	writeVerificationScript(t, root, "test-check", "printf 'tests passed\\n'\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "repair-stops-attach"),
		AttachInput{Name: "Repair stops", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Criteria: []VerificationCriterion{
			{ID: "compile.clean", Description: "The project compiles.", Kinds: []string{"compile"}},
			{ID: "tests.clean", Description: "The tests pass.", Kinds: []string{"test"}},
		},
		Gates: []VerificationGate{
			{ID: "compile", Kind: "compile", Argv: []string{"./compile-check"}, TimeoutSeconds: 10,
				Required: true, Criteria: []string{"compile.clean"}, EvidenceKinds: []string{"logs"}, Available: true},
			{ID: "test", Kind: "test", Argv: []string{"./test-check"}, TimeoutSeconds: 10,
				Required: true, Criteria: []string{"tests.clean"}, EvidenceKinds: []string{"logs"}, Available: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true, MaxAttempts: 3,
	})
	if err != nil || first.Repair.State != "diagnose_patch_rerun" {
		t.Fatalf("initial repair decision = %+v, %v", first.Repair, err)
	}
	writeVerificationScript(t, root, "test-check", "printf 'test regression\\n'\nexit 1\n")
	regression, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true, MaxAttempts: 3,
	})
	if err != nil || regression.Repair.State != "stop_regression" {
		t.Fatalf("regression repair decision = %+v, %v", regression.Repair, err)
	}
	budget, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true, MaxAttempts: 3,
	})
	if err != nil || budget.Repair.State != "stop_budget" || budget.Repair.Attempts != 3 {
		t.Fatalf("budget repair decision = %+v, %v", budget.Repair, err)
	}
	status := verificationRunStatus(VerificationRun{Results: []VerificationGateResult{
		{GateID: "waived", Status: "waived"},
		{GateID: "failed", Status: "failed"},
	}}, manifest, true)
	if status != "failed" {
		t.Fatalf("failure did not outrank waiver: %q", status)
	}
	sharedCriterion := VerificationManifest{
		Criteria: []VerificationCriterion{{ID: "release.ready"}},
		Gates: []VerificationGate{
			{ID: "compile", Required: true, Criteria: []string{"release.ready"}},
			{ID: "test", Required: true, Criteria: []string{"release.ready"}},
		},
	}
	covered, uncovered := verificationCoverage(sharedCriterion, []VerificationGateResult{
		{GateID: "compile", Status: "passed", CriteriaCovered: []string{"release.ready"}},
	})
	if len(covered) != 0 || len(uncovered) != 1 {
		t.Fatalf("partial multi-gate evidence covered a criterion: covered=%v uncovered=%v", covered, uncovered)
	}
}

func TestVerificationWaiverExpiresAndRemainsUncovered(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	clock := &verificationClock{now: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)}
	service, err := NewService(store, clock, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "expired-waiver")
	writeVerificationScript(t, root, "security-check", "printf 'security failure\\n'\nexit 1\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "expired-waiver-attach"),
		AttachInput{Name: "Expired waiver", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Criteria: []VerificationCriterion{
			{ID: "security.clean", Description: "The security gate passes.", Kinds: []string{"security"}},
		},
		Gates: []VerificationGate{
			{ID: "security", Kind: "security", Argv: []string{"./security-check"}, TimeoutSeconds: 10,
				Required: true, Criteria: []string{"security.clean"}, EvidenceKinds: []string{"logs"}, Available: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waiver, err := service.PutVerificationWaiver(ctx, actor, VerificationWaiverInput{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"security"},
		Criteria: []string{"security.clean"}, Reason: "Temporary scanner outage.",
		Risk: "A security regression may remain.", ExpiresAt: clock.now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	run, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true, MaxAttempts: 3,
	})
	if err != nil || run.Status != "failed" || run.Results[0].Status != "failed" ||
		len(run.UncoveredCriteria) != 1 || run.UncoveredCriteria[0] != "security.clean" {
		t.Fatalf("expired waiver run = %+v, %v", run, err)
	}
	waivers, err := service.ListVerificationWaivers(ctx, actor, project.ID)
	if err != nil || len(waivers) != 1 || waivers[0].ID != waiver.ID {
		t.Fatalf("visible expired waivers = %+v, %v", waivers, err)
	}
}

func TestVerificationStableRepairHistorySurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(attachRoot, "restart-repair")
	writeVerificationScript(t, root, "compile-check", "printf 'compile failure\\n'\nexit 1\n")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "restart-repair-attach"),
		AttachInput{Name: "Restart repair", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.DeriveVerificationManifest(ctx, actor, VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Criteria: []VerificationCriterion{
			{ID: "compile.clean", Description: "The project compiles.", Kinds: []string{"compile"}},
		},
		Gates: []VerificationGate{
			{ID: "compile", Kind: "compile", Argv: []string{"./compile-check"}, TimeoutSeconds: 10,
				Required: true, Criteria: []string{"compile.clean"}, EvidenceKinds: []string{"logs"}, Available: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"compile"}, MaxAttempts: 4,
	}); err != nil {
		t.Fatal(err)
	}
	writeVerificationScript(t, root, "compile-check", "printf 'compile passed\\n'\n")
	flaky, err := service.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"compile"}, MaxAttempts: 4,
	})
	if err != nil || flaky.Status != "flaky" || flaky.Repair.State != "stop_flaky" {
		t.Fatalf("first repair pass = %+v, %v", flaky, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, store = reopenProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	restarted, err := NewService(store, types.SystemClock{}, ServiceConfig{
		WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	current, err := restarted.CurrentVerificationManifest(ctx, actor, project.ID)
	if err != nil || current.ID != manifest.ID {
		t.Fatalf("restarted manifest = %+v, %v", current, err)
	}
	stable, err := restarted.RunVerification(ctx, actor, VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"compile"}, MaxAttempts: 4,
	})
	if err != nil || stable.Status != "targeted_passed" || stable.Repair.State != "complete" {
		t.Fatalf("stable repair after restart = %+v, %v", stable, err)
	}
}

type verificationClock struct{ now time.Time }

func (clock *verificationClock) Now() time.Time { return clock.now }

func writeVerificationScript(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
}
