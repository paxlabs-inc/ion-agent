package operatorapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
)

type scopedPreviewInspector struct{ actor uuid.UUID }

func (inspector *scopedPreviewInspector) InspectProjectPreview(
	ctx context.Context,
	rawURL string,
	width, height int64,
	dark bool,
) (projectcontrol.RuntimeBrowserSnapshot, error) {
	scope, _ := controlplane.ApprovalScopeFromContext(ctx)
	inspector.actor = scope.ActorID
	return projectcontrol.RuntimeBrowserSnapshot{
		URL: rawURL, Title: "Scoped preview", Text: "ready",
		ScreenshotPNG: "data:image/png;base64,cG5n",
		Width:         width, Height: height, DarkMode: dark,
	}, nil
}

func TestProjectRegistryProductionControlPlaneRestartIsolationAndEvents(t *testing.T) {
	ctx := context.Background()
	dataRoot, projectRoot, attachedRoot := t.TempDir(), t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	config := RuntimeConfig{DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: attachedRoot, ProjectWorkspaceRoot: projectRoot}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor, other := uuid.New(), uuid.New()
	create := controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationProjectCreate, Scope: controlplane.Scope{ActorID: actor},
		IdempotencyKey: "project-controlplane-create",
		Payload:        json.RawMessage(`{"name":"Operator project","template":"go-cli","host":"direct_local"}`)}
	createdResponse := runtime.dispatcher.Dispatch(ctx, actor, create)
	if createdResponse.Error != nil {
		t.Fatalf("create response = %+v", createdResponse)
	}
	var created projectcontrol.Project
	if err := json.Unmarshal(createdResponse.Result, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == uuid.Nil || created.Lifecycle != projectcontrol.LifecycleReady {
		t.Fatalf("created project = %+v", created)
	}
	replay, err := runtime.journal.ReplayActor(ctx, actor, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	states := []controlplane.EventType{}
	for _, event := range replay.Events {
		switch event.Type {
		case controlplane.EventWorkspaceQueued, controlplane.EventWorkspaceStarted,
			controlplane.EventWorkspaceProgress, controlplane.EventWorkspaceCompleted:
			states = append(states, event.Type)
			var lifecycle projectcontrol.LifecycleEvent
			if err := json.Unmarshal(event.Payload, &lifecycle); err != nil {
				t.Fatal(err)
			}
			if lifecycle.ActorID != actor || lifecycle.ProjectID != created.ID ||
				lifecycle.RequestID != create.RequestID || lifecycle.CorrelationID != create.RequestID {
				t.Fatalf("event envelope = %+v", lifecycle)
			}
		}
	}
	wantStates := []controlplane.EventType{controlplane.EventWorkspaceQueued,
		controlplane.EventWorkspaceStarted, controlplane.EventWorkspaceProgress,
		controlplane.EventWorkspaceCompleted}
	if !equalEventTypes(states, wantStates) {
		t.Fatalf("workspace lifecycle events = %v, want %v", states, wantStates)
	}
	create.RequestID = uuid.New()
	repeated := runtime.dispatcher.Dispatch(ctx, actor, create)
	if repeated.Error != nil || string(repeated.Result) != string(createdResponse.Result) {
		t.Fatalf("idempotent control-plane response = %+v", repeated)
	}
	afterRepeat, err := runtime.journal.ReplayActor(ctx, actor, 0, 128)
	if err != nil || len(afterRepeat.Events) != len(replay.Events) {
		t.Fatalf("duplicate mutation appended events: before=%d after=%d err=%v",
			len(replay.Events), len(afterRepeat.Events), err)
	}

	list := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectList, `{}`)
	var listed struct {
		Revision uint64                   `json:"revision"`
		Projects []projectcontrol.Project `json:"projects"`
	}
	if err := json.Unmarshal(list.Result, &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Revision == 0 || len(listed.Projects) != 1 || listed.Projects[0].ID != created.ID {
		t.Fatalf("project list = %+v", listed)
	}
	otherList := projectQuery(t, ctx, runtime, other, controlplane.OperationProjectList, `{}`)
	if !bytes.Contains(otherList.Result, []byte(`"projects":[]`)) {
		t.Fatalf("cross-actor project list = %s", otherList.Result)
	}
	capabilities := projectQuery(t, ctx, runtime, actor, controlplane.OperationWorkspaceCapabilities, `{}`)
	if !bytes.Contains(capabilities.Result, []byte(projectcontrol.WorkspaceHostVersion)) ||
		!bytes.Contains(capabilities.Result, []byte(`"direct_local"`)) ||
		!bytes.Contains(capabilities.Result, []byte(`"remote_worker"`)) {
		t.Fatalf("workspace capabilities = %s", capabilities.Result)
	}

	wrongRevision := created.WorkspaceRevision - 1
	stalePayload, _ := json.Marshal(map[string]any{"project_id": created.ID,
		"operation": projectcontrol.HostPause, "workspace_revision": wrongRevision})
	stale := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationWorkspaceLifecycle, Scope: controlplane.Scope{ActorID: actor},
		IdempotencyKey: "stale-project-pause", Payload: stalePayload})
	if stale.Error == nil || stale.Error.Code != controlplane.ErrorConflict {
		t.Fatalf("stale project revision = %+v", stale)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := os.ReadFile(filepath.Join(dataRoot, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte("Operator project")) || bytes.Contains(database, []byte(created.Root)) {
		t.Fatal("encrypted project registry leaked plaintext identity or root")
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedList := projectQuery(t, ctx, restarted, actor, controlplane.OperationProjectList, `{}`)
	if !bytes.Contains(restartedList.Result, []byte(created.ID.String())) ||
		!bytes.Contains(restartedList.Result, []byte(`"lifecycle":"ready"`)) {
		t.Fatalf("restart project list = %s", restartedList.Result)
	}
}

func TestProjectGitProductionControlPlaneStagesBranchesAndCommitsExactPaths(t *testing.T) {
	ctx := context.Background()
	dataRoot, projectRoot, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace, ProjectWorkspaceRoot: projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actor := uuid.New()
	createdResponse := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectCreate,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "git-project",
		Payload: json.RawMessage(`{"name":"Git operator acceptance","template":"static-web","host":"direct_local"}`)})
	if createdResponse.Error != nil {
		t.Fatal(createdResponse.Error)
	}
	var project projectcontrol.Project
	if err := json.Unmarshal(createdResponse.Result, &project); err != nil {
		t.Fatal(err)
	}
	gitAcceptance(t, "init", "--initial-branch=main", project.Root)
	gitAcceptance(t, "-C", project.Root, "add", ".")
	gitAcceptance(t, "-C", project.Root, "-c", "user.name=Acceptance", "-c", "user.email=acceptance@example.invalid", "commit", "-m", "baseline")
	projectionPayload, _ := json.Marshal(map[string]any{"project_id": project.ID})
	projectionResponse := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectGitGet, string(projectionPayload))
	var projection projectcontrol.GitProjection
	if err := json.Unmarshal(projectionResponse.Result, &projection); err != nil {
		t.Fatal(err)
	}
	content := "<!doctype html><h1>Exact operator change</h1>\n"
	if err := os.WriteFile(filepath.Join(project.Root, "index.html"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(content))
	expectation := map[string]any{"path": "index.html", "sha256": hex.EncodeToString(digest[:])}
	stagePayload, _ := json.Marshal(map[string]any{"project_id": project.ID, "workspace_revision": project.WorkspaceRevision,
		"expected_head": projection.Head, "paths": []any{expectation}})
	stage := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectGitStage,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "git-stage", Payload: stagePayload})
	if stage.Error != nil {
		t.Fatalf("stage = %+v", stage)
	}
	project, err = runtime.capabilityRoot.projects.Get(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	branchPayload, _ := json.Marshal(map[string]any{"project_id": project.ID, "workspace_revision": project.WorkspaceRevision,
		"expected_head": projection.Head, "name": "review/operator-acceptance"})
	branch := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectGitBranchCreate,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "git-branch", Payload: branchPayload})
	if branch.Error != nil {
		t.Fatalf("branch = %+v", branch)
	}
	project, _ = runtime.capabilityRoot.projects.Get(ctx, actor, project.ID)
	commitPayload, _ := json.Marshal(map[string]any{"project_id": project.ID, "workspace_revision": project.WorkspaceRevision,
		"expected_head": projection.Head, "message": "exact operator change", "author_name": "Acceptance",
		"author_email": "acceptance@example.invalid", "paths": []any{expectation}})
	approved := policy.WithPrincipal(ctx, policy.Principal{Sender: policy.SenderUser, Approved: true})
	commit := runtime.dispatcher.Dispatch(approved, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectGitCommit,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "git-commit", Payload: commitPayload})
	if commit.Error != nil || !bytes.Contains(commit.Result, []byte(`"classification":"RED"`)) {
		t.Fatalf("commit = %+v", commit)
	}
	after := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectGitGet, string(projectionPayload))
	if !bytes.Contains(after.Result, []byte("exact operator change")) || !bytes.Contains(after.Result, []byte("review/operator-acceptance")) {
		t.Fatalf("Git production projection = %s", after.Result)
	}
}

func gitAcceptance(t *testing.T, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func TestProjectRuntimeProductionControlPlaneStartsReloadsRestartsAndCleansPreview(t *testing.T) {
	ctx := context.Background()
	dataRoot, projectRoot, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace, ProjectWorkspaceRoot: projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actor := uuid.New()
	createdResponse := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectCreate,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "runtime-project",
		Payload: json.RawMessage(`{"name":"Live operator preview","template":"static-web","host":"direct_local"}`)})
	if createdResponse.Error != nil {
		t.Fatal(createdResponse.Error)
	}
	var project projectcontrol.Project
	if err := json.Unmarshal(createdResponse.Result, &project); err != nil {
		t.Fatal(err)
	}
	planPayload, _ := json.Marshal(map[string]any{"project_id": project.ID})
	plan := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectRuntimePlan, string(planPayload))
	if !bytes.Contains(plan.Result, []byte(`"stack":"node + static"`)) ||
		!bytes.Contains(plan.Result, []byte(`"kind":"test"`)) ||
		!bytes.Contains(plan.Result, []byte(`"kind":"dev"`)) {
		t.Fatalf("runtime plan = %s", plan.Result)
	}
	startPayload, _ := json.Marshal(map[string]any{"project_id": project.ID, "workspace_revision": project.WorkspaceRevision,
		"name": "web", "command_kind": "dev", "readiness_seconds": 10})
	started := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectRuntimeStart,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "runtime-start", Payload: startPayload})
	if started.Error != nil {
		t.Fatalf("runtime start = %+v", started)
	}
	var state projectcontrol.RuntimeState
	if err := json.Unmarshal(started.Result, &state); err != nil || state.Status != "running" || state.Port == 0 {
		t.Fatalf("runtime state = %+v, %v", state, err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(state.PreviewURL) // #nosec G107 -- production runtime returned a loopback-only owned URL.
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(body, []byte("Built with Ion")) {
		t.Fatalf("preview body = %q", body)
	}
	inspector := &scopedPreviewInspector{}
	runtime.capabilityRoot.projects.SetPreviewInspector(inspector)
	inspectPayload, _ := json.Marshal(map[string]any{"project_id": project.ID, "name": "web",
		"width": 390, "height": 844, "dark_mode": true})
	inspected := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectRuntimeInspect, string(inspectPayload))
	if inspector.actor != actor || !bytes.Contains(inspected.Result, []byte(`"width":390`)) ||
		!bytes.Contains(inspected.Result, []byte(`"dark_mode":true`)) {
		t.Fatalf("actor-scoped runtime inspection = %s, actor %s", inspected.Result, inspector.actor)
	}
	controlPayload, _ := json.Marshal(map[string]any{"project_id": project.ID, "name": "web"})
	for _, operation := range []controlplane.Operation{controlplane.OperationProjectRuntimeReload, controlplane.OperationProjectRuntimeRestart} {
		result := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
			RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: operation,
			Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: string(operation) + "-acceptance", Payload: controlPayload})
		if result.Error != nil {
			t.Fatalf("%s = %+v", operation, result)
		}
	}
	stopped := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{ProtocolVersion: controlplane.ProtocolVersion,
		RequestID: uuid.New(), Kind: controlplane.KindCommand, Operation: controlplane.OperationProjectRuntimeStop,
		Scope: controlplane.Scope{ActorID: actor}, IdempotencyKey: "runtime-stop", Payload: controlPayload})
	if stopped.Error != nil || !bytes.Contains(stopped.Result, []byte(`"status":"stopped"`)) {
		t.Fatalf("runtime stop = %+v", stopped)
	}
	if _, err := client.Get(state.PreviewURL); err == nil {
		t.Fatal("stopped production runtime retained its owned port")
	}
}

func TestProjectVerificationProductionControlPlaneDerivesRunsAndListsWaivers(t *testing.T) {
	ctx := context.Background()
	dataRoot, projectRoot, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	initializeRuntimeVault(t, ctx, dataRoot)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{DataDirectory: dataRoot, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace, ProjectWorkspaceRoot: projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actor, other, sessionID := uuid.New(), uuid.New(), uuid.New()
	createdResponse := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationProjectCreate, Scope: controlplane.Scope{ActorID: actor, SessionID: &sessionID},
		IdempotencyKey: "verification-project",
		Payload:        json.RawMessage(`{"name":"Verification operator acceptance","template":"static-web","host":"direct_local"}`),
	})
	if createdResponse.Error != nil {
		t.Fatal(createdResponse.Error)
	}
	var project projectcontrol.Project
	if err := json.Unmarshal(createdResponse.Result, &project); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(project.Root, "verify.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'verified\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	derivePayload, _ := json.Marshal(projectcontrol.VerificationManifestInput{
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision,
		Criteria: []projectcontrol.VerificationCriterion{
			{ID: "release.ready", Description: "The release gate passes.", Kinds: []string{"test"}},
		},
		Gates: []projectcontrol.VerificationGate{
			{ID: "test", Kind: "test", Argv: []string{"./verify.sh"}, TimeoutSeconds: 10,
				Required: true, Criteria: []string{"release.ready"}, EvidenceKinds: []string{"logs"}, Available: true},
		},
	})
	derived := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationProjectVerificationDerive,
		Scope:     controlplane.Scope{ActorID: actor, SessionID: &sessionID}, IdempotencyKey: "verification-derive",
		Payload: derivePayload,
	})
	if derived.Error != nil {
		t.Fatalf("derive verification manifest = %+v", derived)
	}
	var manifest projectcontrol.VerificationManifest
	if err := json.Unmarshal(derived.Result, &manifest); err != nil || manifest.ID == uuid.Nil {
		t.Fatalf("manifest = %+v, %v", manifest, err)
	}
	projectPayload, _ := json.Marshal(map[string]any{"project_id": project.ID})
	current := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectVerificationGet, string(projectPayload))
	if !bytes.Contains(current.Result, []byte(manifest.ID.String())) {
		t.Fatalf("current verification manifest = %s", current.Result)
	}
	runPayload, _ := json.Marshal(projectcontrol.VerificationRunRequest{
		ProjectID: project.ID, ManifestID: manifest.ID, Full: true, MaxAttempts: 3,
	})
	runResponse := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationProjectVerificationRun,
		Scope:     controlplane.Scope{ActorID: actor, SessionID: &sessionID}, IdempotencyKey: "verification-run",
		Payload: runPayload,
	})
	var run projectcontrol.VerificationRun
	if runResponse.Error != nil || json.Unmarshal(runResponse.Result, &run) != nil ||
		run.Status != "passed" || len(run.CriteriaCovered) != 1 {
		t.Fatalf("verification run = %+v, decoded %+v", runResponse, run)
	}
	runs := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectVerificationRuns, string(projectPayload))
	if !bytes.Contains(runs.Result, []byte(run.ID.String())) {
		t.Fatalf("verification runs = %s", runs.Result)
	}
	waiverPayload, _ := json.Marshal(projectcontrol.VerificationWaiverInput{
		ProjectID: project.ID, ManifestID: manifest.ID, GateIDs: []string{"test"},
		Criteria: []string{"release.ready"}, Reason: "Acceptance waiver boundary.",
		Risk: "Release evidence remains uncovered.", ExpiresAt: time.Now().Add(time.Hour),
	})
	waived := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationProjectVerificationWaiver,
		Scope:     controlplane.Scope{ActorID: actor, SessionID: &sessionID}, IdempotencyKey: "verification-waiver",
		Payload: waiverPayload,
	})
	if waived.Error != nil {
		t.Fatalf("verification waiver = %+v", waived)
	}
	waivers := projectQuery(t, ctx, runtime, actor, controlplane.OperationProjectVerificationWaivers, string(projectPayload))
	if !bytes.Contains(waivers.Result, []byte(`"risk":"Release evidence remains uncovered."`)) {
		t.Fatalf("verification waivers = %s", waivers.Result)
	}
	crossActor := runtime.dispatcher.Dispatch(ctx, other, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindQuery,
		Operation: controlplane.OperationProjectVerificationGet, Scope: controlplane.Scope{ActorID: other},
		Payload: projectPayload,
	})
	if crossActor.Error != nil || !bytes.Contains(crossActor.Result, []byte(`"status":"unavailable"`)) ||
		!bytes.Contains(crossActor.Result, []byte(`"reason":"project was not found"`)) {
		t.Fatalf("cross-actor verification manifest = %+v", crossActor)
	}
}

func projectQuery(t *testing.T, ctx context.Context, runtime *Runtime, actor uuid.UUID,
	operation controlplane.Operation, payload string) controlplane.Response {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion, RequestID: uuid.New(), Kind: controlplane.KindQuery,
		Operation: operation, Scope: controlplane.Scope{ActorID: actor}, Payload: json.RawMessage(payload)})
	if response.Error != nil {
		t.Fatalf("%s response = %+v", operation, response)
	}
	return response
}

func equalEventTypes(left, right []controlplane.EventType) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
