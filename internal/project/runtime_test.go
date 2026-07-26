package project

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type fakePreviewInspector struct{}

func (fakePreviewInspector) InspectProjectPreview(_ context.Context, rawURL string, width, height int64,
	dark bool) (RuntimeBrowserSnapshot, error) {
	return RuntimeBrowserSnapshot{URL: rawURL, Title: "Live", Text: "Real preview", Width: width, Height: height,
		DarkMode: dark, ScreenshotPNG: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
		Elements:      []RuntimeInspectionElement{{Ref: "p1", Tag: "button", Text: "Save"}},
		Accessibility: []RuntimeAccessibilityFinding{{Ref: "p2", Rule: "interactive-name", Message: "Missing name"}},
		Diagnostics:   []RuntimeBrowserReport{{Source: "network", Severity: "error", Code: "http_404", Message: "/missing.js"}}}, nil
}

type escapingPreviewInspector struct{}

func (escapingPreviewInspector) InspectProjectPreview(_ context.Context, _ string, width, height int64,
	dark bool) (RuntimeBrowserSnapshot, error) {
	return RuntimeBrowserSnapshot{
		URL: "https://attacker.invalid/", Width: width, Height: height, DarkMode: dark,
		ScreenshotPNG: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	}, nil
}

func TestRuntimeStaticPreviewLifecycleDiagnosticsAndRestart(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "runtime-static")
	writeIntelligenceFile(t, root, "index.html", "<!doctype html><title>Live</title><h1>Real preview</h1>")
	writeIntelligenceFile(t, root, "style.css", "button { color: blue; }\n")
	runGit(t, "init", "--initial-branch=main", root)
	runGit(t, "-C", root, "add", ".")
	runGitWithIdentity(t, "-C", root, "commit", "-m", "preview")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "runtime-attach"), AttachInput{Name: "Runtime", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.RuntimePlan(ctx, actor, project.ID)
	if err != nil || plan.Stack != "static" || len(plan.Commands) != 1 || plan.Commands[0].Argv[0] != "python3" {
		t.Fatalf("runtime plan = %+v, %v", plan, err)
	}
	phaseScript := filepath.Join(root, "phase-fail")
	if err := os.WriteFile(phaseScript, []byte("#!/bin/sh\nprintf 'src/app.ts:7:3: type mismatch from build\\n'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	phase, err := service.RunRuntimePhase(ctx, actor, RuntimePhaseRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Kind: "build", Argv: []string{phaseScript}}, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = waitTerminal(t, service, actor, phase.ID, "failed")
	deadline := time.Now().Add(2 * time.Second)
	var phaseProblems []RuntimeDiagnostic
	for time.Now().Before(deadline) {
		phaseProblems, err = service.RuntimeProblems(ctx, actor, project.ID)
		if err == nil && len(phaseProblems) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(phaseProblems) != 1 || phaseProblems[0].Source != "type" || phaseProblems[0].Path != "src/app.ts" ||
		phaseProblems[0].Line != 7 || len(phaseProblems[0].CausalEvidence) == 0 {
		t.Fatalf("phase diagnostics = %+v, %v", phaseProblems, err)
	}
	state, err := service.StartRuntime(ctx, actor, RuntimeStartRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Name: "web", CommandKind: "dev", ReadinessSeconds: 10})
	if err != nil || state.Status != "running" || state.Port == 0 || state.Origin == "" || state.PreviewURL == "" {
		t.Fatalf("runtime start = %+v, %v", state, err)
	}
	if state.NextAction != "Capture and inspect the preview before accepting the result." {
		t.Fatalf("running next action = %q", state.NextAction)
	}
	second, err := service.StartRuntime(ctx, actor, RuntimeStartRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Name: "docs", CommandKind: "dev", ReadinessSeconds: 10})
	if err != nil || second.Status != "running" || second.Port == state.Port {
		t.Fatalf("independent named runtime = %+v, %v", second, err)
	}
	listed, err := service.ListRuntimes(ctx, actor, project.ID)
	if err != nil || len(listed) != 2 || listed[0].Name != "docs" || listed[1].Name != "web" {
		t.Fatalf("named runtime list = %+v, %v", listed, err)
	}
	response, err := http.Get(state.PreviewURL) // #nosec G107 -- URL is allocated by the loopback-only runtime.
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), "Real preview") {
		t.Fatalf("preview body = %q", body)
	}
	service.SetPreviewInspector(fakePreviewInspector{})
	inspection, err := service.InspectRuntime(ctx, actor, RuntimeInspectRequest{ProjectID: project.ID,
		Name: "web", Width: 390, Height: 844, DarkMode: true})
	if err != nil || inspection.ScreenshotSHA256 == "" || len(inspection.Elements) != 1 ||
		len(inspection.Accessibility) != 1 || !inspection.DarkMode {
		t.Fatalf("preview inspection = %+v, %v", inspection, err)
	}
	service.SetPreviewInspector(escapingPreviewInspector{})
	if _, err := service.InspectRuntime(ctx, actor, RuntimeInspectRequest{ProjectID: project.ID,
		Name: "web", Width: 390, Height: 844}); err == nil ||
		!strings.Contains(err.Error(), "escaped the owned runtime origin") {
		t.Fatalf("hostile preview escape error = %v", err)
	}
	service.SetPreviewInspector(fakePreviewInspector{})
	state, err = service.AnnotateRuntime(ctx, actor, RuntimeAnnotationRequest{ProjectID: project.ID,
		Name: "web", ElementRef: "p1", Body: "Increase the contrast."})
	if err != nil || len(state.Annotations) != 1 || len(state.Screenshots) != 1 {
		t.Fatalf("preview annotation = %+v, %v", state, err)
	}
	state, err = service.ProposeRuntimeStyle(ctx, actor, RuntimeStyleProposalRequest{ProjectID: project.ID,
		Name: "web", ElementRef: "p1", Path: "style.css", ExpectedSHA256: testContentHash("button { color: blue; }\n"),
		Declarations: map[string]string{"color": "oklch(0.7 0.2 250)"}})
	if err != nil || len(state.StyleProposals) != 1 || state.StyleProposals[0].Status != "proposed" {
		t.Fatalf("style proposal = %+v, %v", state.StyleProposals, err)
	}
	for index := 0; index < 2; index++ {
		state, err = service.ReportRuntimeDiagnostic(ctx, actor, RuntimeBrowserReport{ProjectID: project.ID,
			Name: "web", Source: "console", Severity: "error", Code: "E_RENDER", Message: "render failed", Path: "app.tsx", Line: 12})
		if err != nil {
			t.Fatal(err)
		}
	}
	var repeated *RuntimeDiagnostic
	for index := range state.Diagnostics {
		if state.Diagnostics[index].Code == "E_RENDER" {
			repeated = &state.Diagnostics[index]
		}
	}
	if len(state.Diagnostics) != 3 || repeated == nil || repeated.Recurrence != 2 || repeated.Signature == "" {
		t.Fatalf("normalized diagnostics = %+v", state.Diagnostics)
	}
	reloaded, err := service.ReloadRuntime(ctx, actor, RuntimeControlRequest{ProjectID: project.ID, Name: "web"})
	if err != nil || reloaded.Reloads != 1 || reloaded.PID != state.PID {
		t.Fatalf("runtime reload = %+v, %v", reloaded, err)
	}
	restarted, err := service.RestartRuntime(ctx, actor, RuntimeControlRequest{ProjectID: project.ID, Name: "web"})
	if err != nil || restarted.Status != "running" || restarted.Restarts != 1 || restarted.PID == state.PID {
		t.Fatalf("runtime restart = %+v, %v", restarted, err)
	}
	stopped, err := service.StopRuntime(ctx, actor, RuntimeControlRequest{ProjectID: project.ID, Name: "web"})
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("runtime stop = %+v, %v", stopped, err)
	}
	if stopped.NextAction != "Start the service when another live preview is needed." {
		t.Fatalf("stopped next action = %q", stopped.NextAction)
	}
	problems, err := service.RuntimeProblems(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		if problem.Code == "process_exit_-1" {
			t.Fatalf("intentional stop was recorded as a crash: %+v", problems)
		}
	}
	if _, err := service.StopRuntime(ctx, actor, RuntimeControlRequest{ProjectID: project.ID, Name: "docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := http.Get(restarted.PreviewURL); err == nil {
		t.Fatal("stopped preview port still accepted traffic")
	}
}

func TestRuntimeNodeVerificationManifestRetainsStaticPreview(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(
		store,
		types.SystemClock{},
		ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "verified-static")
	writeIntelligenceFile(t, root, "index.html", "<h1>Verified static preview</h1>")
	writeIntelligenceFile(t, root, "package.json", `{"scripts":{"test":"node --test"}}`)
	actor := uuid.New()
	project, err := service.AttachDirectory(
		ctx,
		testMeta(actor, "verified-static"),
		AttachInput{Name: "Verified static", Directory: root},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.RuntimePlan(ctx, actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var hasTest, hasPreview bool
	for _, command := range plan.Commands {
		hasTest = hasTest || command.Kind == "test"
		hasPreview = hasPreview || command.Kind == "dev" && len(command.Argv) > 0 &&
			command.Argv[0] == "python3"
	}
	if plan.Stack != "node + static" || !hasTest || !hasPreview {
		t.Fatalf("runtime plan = %+v", plan)
	}
}

func TestRuntimeCrashIsPersistedAsActionableDiagnostic(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	root := filepath.Join(attachRoot, "runtime-crash")
	writeIntelligenceFile(t, root, "index.html", "crash test")
	actor := uuid.New()
	project, err := service.AttachDirectory(ctx, testMeta(actor, "runtime-crash-attach"), AttachInput{Name: "Runtime crash", Directory: root})
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.StartRuntime(ctx, actor, RuntimeStartRequest{ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, Name: "web", CommandKind: "dev", ReadinessSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-state.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err = service.GetRuntime(ctx, actor, project.ID, "web")
		if err == nil && state.Status == "crashed" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if state.Status != "crashed" || len(state.Diagnostics) != 1 || state.Diagnostics[0].Code == "" || state.StoppedAt == nil {
		t.Fatalf("crash state = %+v", state)
	}
	repaired, err := service.RestartRuntime(ctx, actor, RuntimeControlRequest{ProjectID: project.ID, Name: "web"})
	if err != nil || repaired.Status != "running" || repaired.Restarts != 1 || repaired.PID == state.PID {
		t.Fatalf("crash repair = %+v, %v", repaired, err)
	}
	if _, err := service.StopRuntime(ctx, actor, RuntimeControlRequest{ProjectID: project.ID, Name: "web"}); err != nil {
		t.Fatal(err)
	}
	if process, findErr := os.FindProcess(state.PID); findErr == nil {
		_ = process.Release()
	}
}

func TestRuntimeRepresentativeStaticFrontendAPIAndFullStackMatrix(t *testing.T) {
	ctx := context.Background()
	dataRoot, workspaceRoot, attachRoot := t.TempDir(), t.TempDir(), t.TempDir()
	manager, store := openProjectStore(t, ctx, dataRoot)
	defer manager.Close()
	defer store.Close(ctx)
	service, err := NewService(store, types.SystemClock{}, ServiceConfig{WorkspaceRoot: workspaceRoot, AttachRoots: []string{attachRoot}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	actor := uuid.New()
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "static", files: map[string]string{"index.html": "<h1>static-matrix</h1>"}, want: "static-matrix"},
		{name: "frontend", files: map[string]string{
			"package.json": `{"scripts":{"build":"node --check server.js","test":"node --test","dev":"node server.js","start":"node server.js"}}`,
			"server.js":    `const http=require('http');http.createServer((_q,r)=>{r.setHeader('content-type','text/html');r.end('<main>frontend-matrix</main>')}).listen(Number(process.env.PORT),'127.0.0.1')`,
		}, want: "frontend-matrix"},
		{name: "api", files: map[string]string{
			"go.mod":  "module example.invalid/runtime-api\n\ngo 1.24\n",
			"main.go": "package main\nimport (\"fmt\";\"net/http\";\"os\")\nfunc main(){http.HandleFunc(\"/\",func(w http.ResponseWriter,r *http.Request){fmt.Fprint(w,\"api-matrix\")}); _=http.ListenAndServe(\"127.0.0.1:\"+os.Getenv(\"PORT\"),nil)}\n",
		}, want: "api-matrix"},
		{name: "fullstack", files: map[string]string{
			"package.json": `{"scripts":{"dev":"node server.js"}}`,
			"server.js":    `const http=require('http');http.createServer((q,r)=>{r.setHeader('content-type',q.url==='/api'?'application/json':'text/html');r.end(q.url==='/api'?'{"ok":true}':'<main>fullstack-matrix</main>')}).listen(Number(process.env.PORT),'127.0.0.1')`,
		}, want: "fullstack-matrix"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := filepath.Join(attachRoot, testCase.name)
			for path, content := range testCase.files {
				writeIntelligenceFile(t, root, path, content)
			}
			project, err := service.AttachDirectory(ctx, testMeta(actor, "matrix-"+testCase.name), AttachInput{Name: testCase.name, Directory: root})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := service.RuntimePlan(ctx, actor, project.ID)
			if err != nil || len(plan.Commands) == 0 {
				t.Fatalf("%s plan = %+v, %v", testCase.name, plan, err)
			}
			state, err := service.StartRuntime(ctx, actor, RuntimeStartRequest{ProjectID: project.ID,
				WorkspaceRevision: project.WorkspaceRevision, Name: "web", CommandKind: "dev", ReadinessSeconds: 60})
			if err != nil {
				t.Fatalf("%s start = %+v, %v", testCase.name, state, err)
			}
			response, err := http.Get(state.PreviewURL) // #nosec G107 -- loopback URL is allocated by the runtime.
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if !strings.Contains(string(body), testCase.want) {
				t.Fatalf("%s body = %q", testCase.name, body)
			}
			if _, err := service.StopRuntime(ctx, actor, RuntimeControlRequest{ProjectID: project.ID, Name: "web"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
