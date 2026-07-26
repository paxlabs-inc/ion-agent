//go:build linux

package operatorapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
)

func TestStudioTransactionalEditingTerminalAndDependencyProductionSurface(t *testing.T) {
	ctx := context.Background()
	dataRoot, projectRoot, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	config := RuntimeConfig{DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace, ProjectWorkspaceRoot: projectRoot}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	project := dispatchStudioProject(t, ctx, runtime, actor, controlplane.OperationProjectCreate,
		"execution-project", map[string]any{"name": "Execution", "template": "empty", "host": "direct_local"})
	if err := os.WriteFile(filepath.Join(project.Root, "main.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Root, "package-lock.json"), []byte(`{"lockfileVersion":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Root, "package.json"),
		[]byte(`{"scripts":{"preinstall":"echo should-not-run"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	patch := projectcontrol.PatchSet{Version: projectcontrol.PatchSetVersion, ID: uuid.New(),
		ProjectID: project.ID, BaselineRevision: project.WorkspaceRevision,
		Criteria: []string{"execution.atomic"}, ValidationPlan: []string{"read exact bytes"},
		Members: []projectcontrol.PatchMember{{Operation: projectcontrol.PatchExact, Path: "main.txt",
			ExpectedSHA256: operatorContentHash("before\n"), OldText: "before", NewText: "after"}}}
	patchResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationProjectPatchApply,
		"apply-execution-patch", patch)
	var receipt projectcontrol.PatchReceipt
	decodeStudioResult(t, patchResponse, &receipt)
	if receipt.Status != "committed" || receipt.WorkspaceRevision != project.WorkspaceRevision+1 {
		t.Fatalf("production patch = %+v", receipt)
	}
	project.WorkspaceRevision = receipt.WorkspaceRevision
	historyResponse := studioQuery(t, ctx, runtime, actor, controlplane.OperationProjectPatchHistory,
		map[string]any{"project_id": project.ID})
	var history []projectcontrol.PatchReceipt
	decodeStudioResult(t, historyResponse, &history)
	if len(history) != 1 || history[0].PatchSetID != receipt.PatchSetID || !history[0].RollbackAvailable {
		t.Fatalf("production patch history = %+v", history)
	}
	rollbackResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationProjectPatchRollback,
		"rollback-execution-patch", projectcontrol.PatchRollbackRequest{ProjectID: project.ID,
			PatchSetID: receipt.PatchSetID, WorkspaceRevision: project.WorkspaceRevision})
	var rollbackReceipt projectcontrol.PatchReceipt
	decodeStudioResult(t, rollbackResponse, &rollbackReceipt)
	project.WorkspaceRevision = rollbackReceipt.WorkspaceRevision
	if content, err := os.ReadFile(filepath.Join(project.Root, "main.txt")); err != nil || string(content) != "before\n" {
		t.Fatalf("production rollback = %q, %v", content, err)
	}

	planResponse := studioQuery(t, ctx, runtime, actor, controlplane.OperationProjectDependenciesPlan,
		projectcontrol.DependencyRequest{ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
			Packages: []string{"untrusted@latest"}})
	var plan projectcontrol.DependencyPlan
	decodeStudioResult(t, planResponse, &plan)
	if !plan.RequiresApproval || plan.Classification != projectcontrol.PolicyRed || !containsOperatorString(plan.Argv, "--ignore-scripts") {
		t.Fatalf("production dependency plan = %+v", plan)
	}
	forged := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectDependenciesInstall,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "forged-dependency-approval",
		Payload: studioJSON(t, map[string]any{"project_id": project.ID, "workspace_revision": project.WorkspaceRevision,
			"packages": []string{"untrusted@latest"}, "network_allowed": true, "approved": true})})
	if forged.Error == nil {
		t.Fatal("payload-forged dependency approval was accepted")
	}

	startResponse := dispatchStudio(t, ctx, runtime, actor, controlplane.OperationProjectProcessStart,
		"start-execution-process", projectcontrol.ProcessRequest{ProjectID: project.ID,
			WorkspaceRevision: project.WorkspaceRevision, Mode: projectcontrol.ProcessOneShot,
			Argv: []string{"/usr/bin/printf", "studio-terminal-output\\n"}, OutputBytes: 4096})
	var terminal projectcontrol.TerminalState
	decodeStudioResult(t, startResponse, &terminal)
	deadline := time.Now().Add(3 * time.Second)
	for {
		replayResponse := studioQuery(t, ctx, runtime, actor, controlplane.OperationProjectTerminalReplay,
			map[string]any{"terminal_id": terminal.ID, "cursor": 0})
		var replay projectcontrol.TerminalReplay
		decodeStudioResult(t, replayResponse, &replay)
		if replay.State.Status == "completed" {
			if !bytes.Contains([]byte(replay.Output), []byte("studio-terminal-output")) {
				t.Fatalf("production terminal replay = %+v", replay)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal did not complete: %+v", replay.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	restarted := studioQuery(t, ctx, runtime, actor, controlplane.OperationProjectTerminalReplay,
		map[string]any{"terminal_id": terminal.ID, "cursor": 0})
	if !bytes.Contains(restarted.Result, []byte(`"status":"completed"`)) {
		t.Fatalf("restart-visible terminal = %s", restarted.Result)
	}
}

func operatorContentHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}
