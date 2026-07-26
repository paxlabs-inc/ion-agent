package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	runtimeStateKind            = "project_runtime_v1"
	runtimePhaseDiagnosticsKind = "project_runtime_phase_diagnostics_v1"
	maxRuntimeLogs              = 1 << 20
)

var runtimeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
var runtimeLocationPattern = regexp.MustCompile(`(?m)([^\s:\n]+\.(?:go|ts|tsx|js|jsx|css|scss|py)):(\d+)(?::(\d+))?[: ]+([^\n]+)`)

type runtimePhaseDiagnostics struct {
	Diagnostics []RuntimeDiagnostic `json:"diagnostics"`
}

type projectRuntime struct {
	mu      sync.Mutex
	state   RuntimeState
	command *exec.Cmd
	output  boundedRuntimeBuffer
}

type RuntimeService struct {
	mu        sync.Mutex
	store     *session.Store
	clock     types.Clock
	projects  *Service
	active    map[string]*projectRuntime
	client    *http.Client
	inspector PreviewInspector
}

type PreviewInspector interface {
	InspectProjectPreview(context.Context, string, int64, int64, bool) (RuntimeBrowserSnapshot, error)
}

type RuntimeEventEmitter interface {
	EmitRuntimeEvent(context.Context, RuntimeEvent) error
}

type boundedRuntimeBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (buffer *boundedRuntimeBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	original := len(payload)
	redacted, _ := redactSecrets("runtime", payload)
	remaining := maxRuntimeLogs - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return original, nil
	}
	if len(redacted) > remaining {
		redacted, buffer.truncated = redacted[:remaining], true
	}
	_, _ = buffer.buffer.Write(redacted)
	return original, nil
}

func (buffer *boundedRuntimeBuffer) snapshot() (string, bool) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String(), buffer.truncated
}

func newRuntimeService(store *session.Store, clock types.Clock, projects *Service) *RuntimeService {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &RuntimeService{store: store, clock: clock, projects: projects, active: map[string]*projectRuntime{},
		client: &http.Client{Transport: transport, Timeout: 750 * time.Millisecond,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (service *RuntimeService) Plan(ctx context.Context, actor, projectID uuid.UUID) (RuntimePlan, error) {
	project, err := service.projects.Get(ctx, actor, projectID)
	if err != nil {
		return RuntimePlan{}, err
	}
	plan := RuntimePlan{Version: RuntimeContractVersion, ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, WorkingDirectory: project.Root,
		DefaultService: "web", ReadinessPath: "/", Inferred: true}
	packagePath := filepath.Join(project.Root, "package.json")
	if raw, readErr := os.ReadFile(packagePath); readErr == nil {
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(raw, &manifest) != nil {
			return RuntimePlan{}, fmt.Errorf("project: package.json is invalid")
		}
		manager := "npm"
		if regularFile(filepath.Join(project.Root, "pnpm-lock.yaml")) {
			manager = "pnpm"
		} else if regularFile(filepath.Join(project.Root, "yarn.lock")) {
			manager = "yarn"
		}
		plan.Stack = "node"
		if regularFile(filepath.Join(project.Root, "pnpm-lock.yaml")) {
			plan.Commands = append(plan.Commands, RuntimeCommand{Kind: "install", Argv: []string{"pnpm", "install", "--frozen-lockfile"}, Description: "Install locked dependencies", Network: true})
		} else if regularFile(filepath.Join(project.Root, "package-lock.json")) {
			plan.Commands = append(plan.Commands, RuntimeCommand{Kind: "install", Argv: []string{"npm", "ci"}, Description: "Install locked dependencies", Network: true})
		}
		for _, kind := range []string{"build", "dev", "test", "start"} {
			if _, ok := manifest.Scripts[kind]; ok {
				plan.Commands = append(plan.Commands, RuntimeCommand{Kind: kind, Argv: []string{manager, "run", kind}, Description: "Run package script " + kind})
			}
		}
		hasRunnable := false
		for _, command := range plan.Commands {
			if command.Kind == "dev" || command.Kind == "start" {
				hasRunnable = true
				break
			}
		}
		if !hasRunnable && regularFile(filepath.Join(project.Root, "index.html")) {
			plan.Stack = "node + static"
			plan.Commands = append(plan.Commands, RuntimeCommand{
				Kind:        "dev",
				Argv:        []string{"python3", "-m", "http.server", "${PORT}", "--bind", "127.0.0.1"},
				Description: "Serve the static entry point while retaining package verification commands",
			})
		}
		return plan, nil
	}
	if regularFile(filepath.Join(project.Root, "go.mod")) {
		plan.Stack = "go"
		plan.Commands = []RuntimeCommand{
			{Kind: "build", Argv: []string{"go", "build", "./..."}, Description: "Build all Go packages"},
			{Kind: "test", Argv: []string{"go", "test", "./..."}, Description: "Test all Go packages"},
			{Kind: "dev", Argv: []string{"go", "run", "."}, Description: "Run the Go application"},
		}
		return plan, nil
	}
	if regularFile(filepath.Join(project.Root, "index.html")) {
		plan.Stack = "static"
		plan.Commands = []RuntimeCommand{{Kind: "dev", Argv: []string{"python3", "-m", "http.server", "${PORT}", "--bind", "127.0.0.1"}, Description: "Serve static files"}}
		return plan, nil
	}
	plan.Stack = "unknown"
	plan.Warnings = []string{"No safe start command was inferred. Configure an exact argv to start a preview."}
	return plan, nil
}

func (service *RuntimeService) RunPhase(ctx context.Context, actor uuid.UUID, request RuntimePhaseRequest, authorized bool) (TerminalState, error) {
	if request.Kind != "install" && request.Kind != "build" && request.Kind != "test" {
		return TerminalState{}, fmt.Errorf("project: runtime phase must be install, build, or test")
	}
	if request.Kind == "install" && !authorized {
		return TerminalState{}, fmt.Errorf("project: dependency installation requires explicit approval")
	}
	plan, err := service.Plan(ctx, actor, request.ProjectID)
	if err != nil {
		return TerminalState{}, err
	}
	argv := append([]string(nil), request.Argv...)
	if len(argv) == 0 {
		for _, command := range plan.Commands {
			if command.Kind == request.Kind {
				argv = append([]string(nil), command.Argv...)
			}
		}
	}
	if len(argv) == 0 {
		return TerminalState{}, fmt.Errorf("project: no safe %s command was inferred; configure exact argv", request.Kind)
	}
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = 900
	}
	return service.projects.terminals.start(ctx, actor, ProcessRequest{ProjectID: request.ProjectID,
		WorkspaceRevision: request.WorkspaceRevision, Mode: ProcessOneShot, Argv: argv,
		WorkingDirectory: request.WorkingDirectory, TimeoutSeconds: request.TimeoutSeconds, OutputBytes: 4 << 20},
		func(state TerminalState, output string) {
			_ = service.savePhaseDiagnostics(context.Background(), actor, request.ProjectID,
				normalizePhaseOutput(request.Kind, state, output))
		})
}

func (service *RuntimeService) Start(ctx context.Context, actor uuid.UUID, request RuntimeStartRequest) (RuntimeState, error) {
	if actor == uuid.Nil || request.ProjectID == uuid.Nil || request.WorkspaceRevision == 0 ||
		!runtimeNamePattern.MatchString(request.Name) {
		return RuntimeState{}, fmt.Errorf("project: valid named runtime request is required")
	}
	project, err := service.projects.Get(ctx, actor, request.ProjectID)
	if err != nil {
		return RuntimeState{}, err
	}
	if project.WorkspaceRevision != request.WorkspaceRevision {
		return RuntimeState{}, ErrStaleRevision
	}
	if existing, loadErr := service.Get(ctx, actor, request.ProjectID, request.Name); loadErr == nil {
		if existing.Status == "running" || existing.Status == "starting" {
			return RuntimeState{}, fmt.Errorf("project: named runtime already exists; restart or stop it first")
		}
		if err := service.store.DeleteLivingState(ctx, runtimeStateKind, runtimeScope(actor, request.ProjectID, request.Name)); err != nil {
			return RuntimeState{}, err
		}
	} else if !errors.Is(loadErr, sql.ErrNoRows) {
		return RuntimeState{}, loadErr
	}
	plan, err := service.Plan(ctx, actor, request.ProjectID)
	if err != nil {
		return RuntimeState{}, err
	}
	argv := append([]string(nil), request.Argv...)
	if len(argv) == 0 {
		for _, command := range plan.Commands {
			if command.Kind == request.CommandKind {
				argv = append([]string(nil), command.Argv...)
			}
		}
	}
	if len(argv) == 0 || len(argv) > 128 || (request.CommandKind != "dev" && request.CommandKind != "start") {
		return RuntimeState{}, fmt.Errorf("project: exact dev or start argv is required")
	}
	directory, err := secureWorkspaceDirectory(project.Root, request.WorkingDirectory)
	if err != nil {
		return RuntimeState{}, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return RuntimeState{}, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	templateArgv := append([]string(nil), argv...)
	for index, argument := range argv {
		argv[index] = strings.ReplaceAll(argument, "${PORT}", strconv.Itoa(port))
		if strings.ContainsRune(argv[index], 0) {
			_ = listener.Close()
			return RuntimeState{}, fmt.Errorf("project: runtime argv contains invalid bytes")
		}
	}
	executable, err := exec.LookPath(argv[0])
	if err != nil {
		_ = listener.Close()
		return RuntimeState{}, fmt.Errorf("project: runtime executable %q is unavailable", argv[0])
	}
	argv[0] = executable
	readinessPath := strings.TrimSpace(request.ReadinessPath)
	if readinessPath == "" {
		readinessPath = plan.ReadinessPath
	}
	if !strings.HasPrefix(readinessPath, "/") || strings.ContainsAny(readinessPath, "\r\n") {
		_ = listener.Close()
		return RuntimeState{}, fmt.Errorf("project: readiness path must be root-relative")
	}
	if request.ReadinessSeconds == 0 {
		request.ReadinessSeconds = 20
	}
	if request.ReadinessSeconds < 1 || request.ReadinessSeconds > 120 {
		_ = listener.Close()
		return RuntimeState{}, fmt.Errorf("project: readiness timeout is out of bounds")
	}
	key := runtimeScope(actor, request.ProjectID, request.Name)
	service.mu.Lock()
	if _, active := service.active[key]; active {
		service.mu.Unlock()
		_ = listener.Close()
		return RuntimeState{}, ErrConflict
	}
	runtime := &projectRuntime{state: RuntimeState{Version: RuntimeContractVersion, ID: uuid.New(), ActorID: actor,
		ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision, Name: request.Name,
		CommandKind: request.CommandKind, Argv: argv, CommandTemplate: templateArgv, WorkingDirectory: directory, Host: "127.0.0.1",
		Port: uint16(port), PreviewURL: fmt.Sprintf("http://127.0.0.1:%d/", port),
		Origin: fmt.Sprintf("http://127.0.0.1:%d", port), ReadinessPath: readinessPath,
		Status: "starting", StartedAt: service.clock.Now().UTC()}}
	service.active[key] = runtime
	service.mu.Unlock()
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = directory
	cacheRoot := filepath.Join(service.projects.archiveRoot, "runtime-cache", actor.String(), request.ProjectID.String(), request.Name)
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		service.removeActive(key)
		return RuntimeState{}, err
	}
	runtimePath := filepath.Dir(executable) + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	command.Env = []string{"PATH=" + runtimePath, "HOME=" + cacheRoot, "GOCACHE=" + filepath.Join(cacheRoot, "go-build"),
		"GOTOOLCHAIN=local", "npm_config_cache=" + filepath.Join(cacheRoot, "npm"), "LANG=C.UTF-8", "CI=1", "HOST=127.0.0.1",
		"PORT=" + strconv.Itoa(port), "BROWSER=none", "NO_UPDATE_NOTIFIER=1"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout, command.Stderr = &runtime.output, &runtime.output
	runtime.command = command
	_ = listener.Close()
	if err := command.Start(); err != nil {
		service.removeActive(key)
		return RuntimeState{}, err
	}
	runtime.mu.Lock()
	runtime.state.PID, runtime.state.ProcessStartToken = command.Process.Pid, linuxProcessStartToken(command.Process.Pid)
	runtime.mu.Unlock()
	if err := service.persist(runtime); err != nil {
		_ = killRuntimeProcess(runtime, syscall.SIGKILL)
		service.removeActive(key)
		return RuntimeState{}, err
	}
	service.emit(runtime, "starting")
	go service.wait(runtime, key)
	deadline := time.Now().Add(time.Duration(request.ReadinessSeconds) * time.Second)
	for time.Now().Before(deadline) {
		if service.ready(ctx, runtime) {
			now := service.clock.Now().UTC()
			runtime.mu.Lock()
			runtime.state.Status, runtime.state.ReadyAt = "running", &now
			runtime.mu.Unlock()
			_ = service.persist(runtime)
			service.emit(runtime, "ready")
			return runtime.snapshot(), nil
		}
		runtime.mu.Lock()
		stopped := runtime.state.StoppedAt != nil
		runtime.mu.Unlock()
		if stopped {
			break
		}
		select {
		case <-ctx.Done():
			_ = service.stopRuntime(runtime)
			return RuntimeState{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	runtime.mu.Lock()
	if runtime.state.Status != "crashed" {
		runtime.state.Status = "failed"
	}
	runtime.mu.Unlock()
	_ = service.stopRuntime(runtime)
	runtime.mu.Lock()
	runtime.state.Status = "failed"
	runtime.state.LastError = "readiness endpoint did not become available"
	service.addDiagnosticLocked(&runtime.state, RuntimeBrowserReport{Source: "readiness", Severity: "error", Code: "runtime_not_ready", Message: runtime.state.LastError})
	runtime.mu.Unlock()
	_ = service.persist(runtime)
	service.emit(runtime, "readiness_failed")
	return runtime.snapshot(), fmt.Errorf("project: runtime failed readiness; inspect normalized diagnostics and logs")
}

func (service *RuntimeService) ready(ctx context.Context, runtime *projectRuntime) bool {
	runtime.mu.Lock()
	endpoint := strings.TrimRight(runtime.state.PreviewURL, "/") + runtime.state.ReadinessPath
	runtime.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	response, err := service.client.Do(request)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 500
}

func (service *RuntimeService) wait(runtime *projectRuntime, key string) {
	err := runtime.command.Wait()
	exit := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = exitErr.ExitCode()
		} else {
			exit = -1
		}
	}
	now := service.clock.Now().UTC()
	logs, truncated := runtime.output.snapshot()
	runtime.mu.Lock()
	runtime.state.Logs, runtime.state.LogsTruncated, runtime.state.ExitCode, runtime.state.StoppedAt = logs, truncated, &exit, &now
	if runtime.state.Status != "stopped" && runtime.state.Status != "failed" {
		if exit == 0 {
			runtime.state.Status = "stopped"
		} else {
			runtime.state.Status, runtime.state.LastError = "crashed", "runtime process exited unexpectedly"
			service.addDiagnosticLocked(&runtime.state, RuntimeBrowserReport{Source: "process", Severity: "error", Code: "process_exit_" + strconv.Itoa(exit), Message: runtime.state.LastError})
		}
	}
	runtime.mu.Unlock()
	_ = service.persist(runtime)
	service.emit(runtime, runtime.snapshot().Status)
	service.removeActive(key)
}

func (service *RuntimeService) Get(ctx context.Context, actor, projectID uuid.UUID, name string) (RuntimeState, error) {
	key := runtimeScope(actor, projectID, name)
	service.mu.Lock()
	runtime := service.active[key]
	service.mu.Unlock()
	if runtime != nil {
		return runtime.snapshot(), nil
	}
	raw, err := service.store.LoadLivingState(ctx, runtimeStateKind, key)
	if err != nil {
		return RuntimeState{}, err
	}
	var state RuntimeState
	if json.Unmarshal(raw, &state) != nil || state.Version != RuntimeContractVersion || state.ActorID != actor || state.ProjectID != projectID {
		return RuntimeState{}, fmt.Errorf("project: invalid encrypted runtime state")
	}
	state.NextAction = runtimeNextAction(state)
	return state, nil
}

func (service *RuntimeService) List(ctx context.Context, actor, projectID uuid.UUID) ([]RuntimeState, error) {
	if _, err := service.projects.Get(ctx, actor, projectID); err != nil {
		return nil, err
	}
	states, err := service.store.ListLivingStates(ctx, runtimeStateKind)
	if err != nil {
		return nil, err
	}
	result := []RuntimeState{}
	for _, encrypted := range states {
		var state RuntimeState
		if json.Unmarshal(encrypted.State, &state) != nil {
			return nil, fmt.Errorf("project: invalid encrypted runtime state")
		}
		if state.ActorID == actor && state.ProjectID == projectID {
			state.NextAction = runtimeNextAction(state)
			result = append(result, state)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (service *RuntimeService) Reload(ctx context.Context, actor uuid.UUID, request RuntimeControlRequest) (RuntimeState, error) {
	runtime, err := service.activeRuntime(actor, request.ProjectID, request.Name)
	if err != nil {
		return RuntimeState{}, err
	}
	if err := ctx.Err(); err != nil {
		return RuntimeState{}, err
	}
	runtime.mu.Lock()
	runtime.state.Reloads++
	runtime.mu.Unlock()
	_ = service.persist(runtime)
	service.emit(runtime, "reload")
	return runtime.snapshot(), nil
}

func (service *RuntimeService) Stop(ctx context.Context, actor uuid.UUID, request RuntimeControlRequest) (RuntimeState, error) {
	runtime, err := service.activeRuntime(actor, request.ProjectID, request.Name)
	if err != nil {
		state, loadErr := service.Get(ctx, actor, request.ProjectID, request.Name)
		if loadErr != nil || state.Status == "stopped" || state.Status == "failed" || state.Status == "crashed" {
			return state, loadErr
		}
		return RuntimeState{}, err
	}
	runtime.mu.Lock()
	previousStatus := runtime.state.Status
	runtime.state.Status = "stopped"
	runtime.mu.Unlock()
	if err := service.stopRuntime(runtime); err != nil {
		runtime.mu.Lock()
		runtime.state.Status = previousStatus
		runtime.mu.Unlock()
		return RuntimeState{}, err
	}
	_ = service.persist(runtime)
	service.emit(runtime, "stopped")
	return runtime.snapshot(), nil
}

func (service *RuntimeService) Restart(ctx context.Context, actor uuid.UUID, request RuntimeControlRequest) (RuntimeState, error) {
	previous, err := service.Get(ctx, actor, request.ProjectID, request.Name)
	if err != nil {
		return RuntimeState{}, err
	}
	if previous.Status == "running" || previous.Status == "starting" {
		if _, err := service.Stop(ctx, actor, request); err != nil {
			return RuntimeState{}, err
		}
	}
	current, err := service.projects.Get(ctx, actor, request.ProjectID)
	if err != nil {
		return RuntimeState{}, err
	}
	relativeDirectory, err := filepath.Rel(current.Root, previous.WorkingDirectory)
	if err != nil || strings.HasPrefix(relativeDirectory, "..") {
		return RuntimeState{}, ErrProtectedPath
	}
	started, err := service.Start(ctx, actor, RuntimeStartRequest{ProjectID: request.ProjectID,
		WorkspaceRevision: current.WorkspaceRevision, Name: request.Name, CommandKind: previous.CommandKind,
		Argv: previous.CommandTemplate, WorkingDirectory: filepath.ToSlash(relativeDirectory), ReadinessPath: previous.ReadinessPath})
	if err != nil {
		return started, err
	}
	key := runtimeScope(actor, request.ProjectID, request.Name)
	service.mu.Lock()
	runtime := service.active[key]
	service.mu.Unlock()
	if runtime != nil {
		runtime.mu.Lock()
		runtime.state.Restarts = previous.Restarts + 1
		runtime.mu.Unlock()
		_ = service.persist(runtime)
		started = runtime.snapshot()
	}
	return started, nil
}

func (service *RuntimeService) emit(runtime *projectRuntime, phase string) {
	service.projects.mu.Lock()
	emitter, ok := service.projects.emitter.(RuntimeEventEmitter)
	service.projects.mu.Unlock()
	if ok {
		_ = emitter.EmitRuntimeEvent(context.Background(), RuntimeEvent{Phase: phase, State: runtime.snapshot()})
	}
}

func (service *RuntimeService) Report(ctx context.Context, actor uuid.UUID, report RuntimeBrowserReport) (RuntimeState, error) {
	runtime, err := service.activeRuntime(actor, report.ProjectID, report.Name)
	if err != nil {
		return RuntimeState{}, err
	}
	if len(strings.TrimSpace(report.Message)) == 0 || len(report.Message) > 16<<10 ||
		(report.Source != "console" && report.Source != "network" && report.Source != "browser" && report.Source != "accessibility") {
		return RuntimeState{}, fmt.Errorf("project: bounded browser diagnostic is required")
	}
	runtime.mu.Lock()
	service.addDiagnosticLocked(&runtime.state, report)
	runtime.mu.Unlock()
	_ = service.persist(runtime)
	return runtime.snapshot(), nil
}

func (service *RuntimeService) Inspect(ctx context.Context, actor uuid.UUID, request RuntimeInspectRequest) (RuntimeInspection, error) {
	runtime, err := service.activeRuntime(actor, request.ProjectID, request.Name)
	if err != nil {
		return RuntimeInspection{}, err
	}
	service.mu.Lock()
	inspector := service.inspector
	service.mu.Unlock()
	if inspector == nil {
		return RuntimeInspection{}, fmt.Errorf("project: isolated preview browser is unavailable")
	}
	runtime.mu.Lock()
	previewURL := runtime.state.PreviewURL
	runtime.mu.Unlock()
	snapshot, err := inspector.InspectProjectPreview(ctx, previewURL, request.Width, request.Height, request.DarkMode)
	if err != nil {
		return RuntimeInspection{}, err
	}
	if snapshot.URL == "" || !strings.HasPrefix(snapshot.URL, previewURL) {
		return RuntimeInspection{}, fmt.Errorf("project: preview browser escaped the owned runtime origin")
	}
	encoded := strings.TrimPrefix(snapshot.ScreenshotPNG, "data:image/png;base64,")
	png, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(png) == 0 || len(png) > 16<<20 {
		return RuntimeInspection{}, fmt.Errorf("project: invalid bounded preview screenshot")
	}
	digest := sha256.Sum256(png)
	hash := hex.EncodeToString(digest[:])
	now, id := service.clock.Now().UTC(), uuid.New()
	directory := filepath.Join(service.projects.archiveRoot, "preview-evidence", actor.String(), request.ProjectID.String())
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return RuntimeInspection{}, err
	}
	path := filepath.Join(directory, id.String()+".png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		return RuntimeInspection{}, err
	}
	runtime.mu.Lock()
	if len(runtime.state.Screenshots) >= 32 {
		oldest := runtime.state.Screenshots[0]
		_ = os.Remove(oldest.Path)
		runtime.state.Screenshots = append([]RuntimeScreenshot(nil), runtime.state.Screenshots[1:]...)
	}
	runtime.state.Screenshots = append(runtime.state.Screenshots, RuntimeScreenshot{ID: id, SHA256: hash,
		Path: path, Width: snapshot.Width, Height: snapshot.Height, DarkMode: snapshot.DarkMode, CreatedAt: now})
	for _, diagnostic := range snapshot.Diagnostics {
		service.addDiagnosticLocked(&runtime.state, diagnostic)
	}
	for _, finding := range snapshot.Accessibility {
		service.addDiagnosticLocked(&runtime.state, RuntimeBrowserReport{ProjectID: request.ProjectID, Name: request.Name,
			Source: "accessibility", Severity: "warning", Code: finding.Rule, Message: finding.Message})
	}
	runtime.mu.Unlock()
	_ = service.persist(runtime)
	return RuntimeInspection{URL: snapshot.URL, Title: snapshot.Title, Text: snapshot.Text,
		Elements: snapshot.Elements, Accessibility: snapshot.Accessibility, ScreenshotPNG: snapshot.ScreenshotPNG,
		ScreenshotSHA256: hash, Width: snapshot.Width, Height: snapshot.Height, DarkMode: snapshot.DarkMode, CapturedAt: now}, nil
}

func (service *RuntimeService) Annotate(ctx context.Context, actor uuid.UUID, request RuntimeAnnotationRequest) (RuntimeState, error) {
	if strings.TrimSpace(request.Body) == "" || len(request.Body) > 8<<10 ||
		(request.ElementRef == "" && (request.X < 0 || request.X > 1 || request.Y < 0 || request.Y > 1)) {
		return RuntimeState{}, fmt.Errorf("project: bounded element or screenshot annotation is required")
	}
	runtime, err := service.activeRuntime(actor, request.ProjectID, request.Name)
	if err != nil {
		return RuntimeState{}, err
	}
	runtime.mu.Lock()
	if len(runtime.state.Annotations) >= 256 {
		runtime.mu.Unlock()
		return RuntimeState{}, fmt.Errorf("project: preview annotation limit reached")
	}
	runtime.state.Annotations = append(runtime.state.Annotations, RuntimeAnnotation{ID: uuid.New(), ElementRef: request.ElementRef,
		X: request.X, Y: request.Y, Body: strings.TrimSpace(request.Body), CreatedAt: service.clock.Now().UTC()})
	runtime.mu.Unlock()
	_ = service.persist(runtime)
	return runtime.snapshot(), nil
}

func (service *RuntimeService) ProposeStyle(ctx context.Context, actor uuid.UUID,
	request RuntimeStyleProposalRequest) (RuntimeState, error) {
	if request.ElementRef == "" || len(request.Declarations) == 0 || len(request.Declarations) > 32 {
		return RuntimeState{}, fmt.Errorf("project: selected element and bounded style declarations are required")
	}
	project, root, err := service.projects.gitProject(ctx, actor, request.ProjectID)
	if err != nil {
		return RuntimeState{}, err
	}
	path, err := securePatchPath(root, request.Path, false)
	if err != nil {
		return RuntimeState{}, err
	}
	hash, _, _, err := snapshotPath(path)
	if err != nil || hash != request.ExpectedSHA256 {
		return RuntimeState{}, errors.Join(ErrStalePreimage, err)
	}
	for property, value := range request.Declarations {
		if strings.TrimSpace(property) == "" || len(property) > 100 || len(value) > 1000 || strings.ContainsAny(property+value, "{}\x00") {
			return RuntimeState{}, fmt.Errorf("project: style proposal contains unsupported declarations")
		}
	}
	runtime, err := service.activeRuntime(actor, request.ProjectID, request.Name)
	if err != nil {
		return RuntimeState{}, err
	}
	runtime.mu.Lock()
	if len(runtime.state.StyleProposals) >= 128 {
		runtime.mu.Unlock()
		return RuntimeState{}, fmt.Errorf("project: style proposal limit reached")
	}
	runtime.state.StyleProposals = append(runtime.state.StyleProposals, RuntimeStyleProposal{ID: uuid.New(),
		WorkspaceRevision: project.WorkspaceRevision, ElementRef: request.ElementRef, Path: cleanRelativePath(request.Path),
		ExpectedSHA256: request.ExpectedSHA256, Declarations: request.Declarations, Status: "proposed", CreatedAt: service.clock.Now().UTC()})
	runtime.mu.Unlock()
	_ = service.persist(runtime)
	return runtime.snapshot(), nil
}

func (service *RuntimeService) addDiagnosticLocked(state *RuntimeState, report RuntimeBrowserReport) {
	message := strings.TrimSpace(report.Message)
	digest := sha256.Sum256([]byte(report.Source + "\x00" + report.Code + "\x00" + report.Path + "\x00" + message))
	signature := hex.EncodeToString(digest[:12])
	now := service.clock.Now().UTC()
	for index := range state.Diagnostics {
		if state.Diagnostics[index].Signature == signature {
			state.Diagnostics[index].Recurrence++
			state.Diagnostics[index].LastSeen = now
			return
		}
	}
	if len(state.Diagnostics) >= 256 {
		state.Diagnostics = append([]RuntimeDiagnostic(nil), state.Diagnostics[1:]...)
	}
	state.Diagnostics = append(state.Diagnostics, RuntimeDiagnostic{ID: signature, Source: report.Source,
		Severity: report.Severity, Code: report.Code, Message: message, Path: report.Path,
		Line: report.Line, Column: report.Column, Signature: signature, Recurrence: 1,
		CausalEvidence: append([]string(nil), report.CausalEvidence...), FirstSeen: now, LastSeen: now})
}

func normalizePhaseOutput(kind string, state TerminalState, output string) []RuntimeBrowserReport {
	reports := []RuntimeBrowserReport{}
	for _, match := range runtimeLocationPattern.FindAllStringSubmatch(output, 256) {
		line, _ := strconv.Atoi(match[2])
		column, _ := strconv.Atoi(match[3])
		source, lower := kind, strings.ToLower(match[4])
		if strings.Contains(lower, "type") {
			source = "type"
		} else if strings.Contains(lower, "lint") {
			source = "lint"
		}
		reports = append(reports, RuntimeBrowserReport{Source: source, Severity: "error", Code: kind + "_diagnostic",
			Message: strings.TrimSpace(match[4]), Path: cleanRelativePath(match[1]), Line: line, Column: column,
			CausalEvidence: []string{"terminal:" + state.ID.String(), "exit_status:" + state.Status}})
	}
	if state.ExitCode != nil && *state.ExitCode != 0 && len(reports) == 0 {
		message := strings.TrimSpace(output)
		if len(message) > 2000 {
			message = message[len(message)-2000:]
		}
		reports = append(reports, RuntimeBrowserReport{Source: kind, Severity: "error",
			Code: kind + "_exit_" + strconv.Itoa(*state.ExitCode), Message: message,
			CausalEvidence: []string{"terminal:" + state.ID.String(), "exit_status:" + state.Status}})
	}
	return reports
}

func (service *RuntimeService) savePhaseDiagnostics(ctx context.Context, actor, projectID uuid.UUID,
	reports []RuntimeBrowserReport) error {
	if len(reports) == 0 {
		return nil
	}
	scope := patchScope(actor, projectID)
	service.mu.Lock()
	defer service.mu.Unlock()
	state := runtimePhaseDiagnostics{Diagnostics: []RuntimeDiagnostic{}}
	if raw, err := service.store.LoadLivingState(ctx, runtimePhaseDiagnosticsKind, scope); err == nil {
		if json.Unmarshal(raw, &state) != nil {
			return fmt.Errorf("project: invalid encrypted phase diagnostics")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	container := RuntimeState{Diagnostics: state.Diagnostics}
	for _, report := range reports {
		service.addDiagnosticLocked(&container, report)
	}
	state.Diagnostics = container.Diagnostics
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, runtimePhaseDiagnosticsKind, scope, raw)
}

func (service *RuntimeService) Problems(ctx context.Context, actor, projectID uuid.UUID) ([]RuntimeDiagnostic, error) {
	runtimes, err := service.List(ctx, actor, projectID)
	if err != nil {
		return nil, err
	}
	all := []RuntimeDiagnostic{}
	for _, runtime := range runtimes {
		all = append(all, runtime.Diagnostics...)
	}
	raw, err := service.store.LoadLivingState(ctx, runtimePhaseDiagnosticsKind, patchScope(actor, projectID))
	if err == nil {
		var phases runtimePhaseDiagnostics
		if json.Unmarshal(raw, &phases) != nil {
			return nil, fmt.Errorf("project: invalid encrypted phase diagnostics")
		}
		all = append(all, phases.Diagnostics...)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	bySignature := map[string]RuntimeDiagnostic{}
	for _, diagnostic := range all {
		if existing, ok := bySignature[diagnostic.Signature]; ok {
			existing.Recurrence += diagnostic.Recurrence
			if diagnostic.LastSeen.After(existing.LastSeen) {
				existing.LastSeen = diagnostic.LastSeen
			}
			bySignature[diagnostic.Signature] = existing
		} else {
			bySignature[diagnostic.Signature] = diagnostic
		}
	}
	result := make([]RuntimeDiagnostic, 0, len(bySignature))
	for _, diagnostic := range bySignature {
		result = append(result, diagnostic)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastSeen.After(result[j].LastSeen) })
	return result, nil
}

func (service *RuntimeService) activeRuntime(actor, projectID uuid.UUID, name string) (*projectRuntime, error) {
	key := runtimeScope(actor, projectID, name)
	service.mu.Lock()
	defer service.mu.Unlock()
	runtime := service.active[key]
	if runtime == nil {
		return nil, fmt.Errorf("project: named runtime is not active")
	}
	return runtime, nil
}

func (runtime *projectRuntime) snapshot() RuntimeState {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	state := runtime.state
	state.Argv = append([]string(nil), state.Argv...)
	state.CommandTemplate = append([]string(nil), state.CommandTemplate...)
	state.Diagnostics = append([]RuntimeDiagnostic(nil), state.Diagnostics...)
	state.Screenshots = append([]RuntimeScreenshot(nil), state.Screenshots...)
	state.Annotations = append([]RuntimeAnnotation(nil), state.Annotations...)
	state.StyleProposals = append([]RuntimeStyleProposal(nil), state.StyleProposals...)
	if logs, truncated := runtime.output.snapshot(); logs != "" {
		state.Logs, state.LogsTruncated = logs, truncated
	}
	state.NextAction = runtimeNextAction(state)
	return state
}

func runtimeNextAction(state RuntimeState) string {
	switch state.Status {
	case "starting":
		return "Wait for the readiness check to finish."
	case "running":
		if len(state.Diagnostics) > 0 {
			return "Review Problems, then capture the preview again after the fix."
		}
		return "Capture and inspect the preview before accepting the result."
	case "crashed":
		return "Review the crash evidence in Problems, then restart the service."
	case "failed":
		return "Review the readiness evidence and logs, correct the command, then start again."
	case "stopped":
		return "Start the service when another live preview is needed."
	default:
		return "Inspect the service state before continuing."
	}
}

func (service *RuntimeService) persist(runtime *projectRuntime) error {
	state := runtime.snapshot()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(context.Background(), runtimeStateKind,
		runtimeScope(state.ActorID, state.ProjectID, state.Name), raw)
}

func (service *RuntimeService) stopRuntime(runtime *projectRuntime) error {
	if err := killRuntimeProcess(runtime, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.mu.Lock()
		stopped := runtime.state.StoppedAt != nil
		runtime.mu.Unlock()
		if stopped {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return killRuntimeProcess(runtime, syscall.SIGKILL)
}

func killRuntimeProcess(runtime *projectRuntime, signal syscall.Signal) error {
	runtime.mu.Lock()
	pid, token := runtime.state.PID, runtime.state.ProcessStartToken
	runtime.mu.Unlock()
	if pid <= 1 || token == "" || linuxProcessStartToken(pid) != token {
		return os.ErrProcessDone
	}
	return syscall.Kill(-pid, signal)
}

func (service *RuntimeService) removeActive(key string) {
	service.mu.Lock()
	delete(service.active, key)
	service.mu.Unlock()
}

func (service *RuntimeService) ReconcileAll(ctx context.Context) error {
	states, err := service.store.ListLivingStates(ctx, runtimeStateKind)
	if err != nil {
		return err
	}
	for _, encrypted := range states {
		var state RuntimeState
		if json.Unmarshal(encrypted.State, &state) != nil || state.Version != RuntimeContractVersion {
			return fmt.Errorf("project: invalid encrypted runtime state")
		}
		if state.Status != "running" && state.Status != "starting" {
			continue
		}
		if state.PID > 1 && state.ProcessStartToken != "" && linuxProcessStartToken(state.PID) == state.ProcessStartToken {
			runtime := &projectRuntime{state: state}
			service.mu.Lock()
			service.active[encrypted.Scope] = runtime
			service.mu.Unlock()
			continue
		}
		now := service.clock.Now().UTC()
		state.Status, state.StoppedAt, state.LastError = "crashed", &now, "runtime process was not alive after restart"
		service.addDiagnosticLocked(&state, RuntimeBrowserReport{Source: "process", Severity: "error", Code: "restart_process_missing", Message: state.LastError})
		raw, _ := json.Marshal(state)
		if err := service.store.SaveLivingState(ctx, runtimeStateKind, encrypted.Scope, raw); err != nil {
			return err
		}
	}
	return nil
}

func (service *RuntimeService) Close() error {
	service.mu.Lock()
	runtimes := make([]*projectRuntime, 0, len(service.active))
	for _, runtime := range service.active {
		runtimes = append(runtimes, runtime)
	}
	service.mu.Unlock()
	var result error
	for _, runtime := range runtimes {
		result = errors.Join(result, service.stopRuntime(runtime))
	}
	result = errors.Join(result, removeRuntimeCache(service.projects.archiveRoot))
	return result
}

func removeRuntimeCache(archiveRoot string) error {
	cacheRoot := filepath.Join(archiveRoot, "runtime-cache")
	if err := filepath.Walk(cacheRoot, func(path string, info os.FileInfo, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("project: make runtime cache removable: %w", err)
	}
	if err := os.RemoveAll(cacheRoot); err != nil {
		return fmt.Errorf("project: remove runtime cache: %w", err)
	}
	return nil
}

func runtimeScope(actor, projectID uuid.UUID, name string) string {
	return actor.String() + ":" + projectID.String() + ":" + strings.TrimSpace(name)
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (service *Service) RuntimePlan(ctx context.Context, actor, projectID uuid.UUID) (RuntimePlan, error) {
	return service.runtimes.Plan(ctx, actor, projectID)
}

func (service *Service) StartRuntime(ctx context.Context, actor uuid.UUID, request RuntimeStartRequest) (RuntimeState, error) {
	return service.runtimes.Start(ctx, actor, request)
}

func (service *Service) RunRuntimePhase(ctx context.Context, actor uuid.UUID, request RuntimePhaseRequest, authorized bool) (TerminalState, error) {
	return service.runtimes.RunPhase(ctx, actor, request, authorized)
}

func (service *Service) ListRuntimes(ctx context.Context, actor, projectID uuid.UUID) ([]RuntimeState, error) {
	return service.runtimes.List(ctx, actor, projectID)
}

func (service *Service) GetRuntime(ctx context.Context, actor, projectID uuid.UUID, name string) (RuntimeState, error) {
	return service.runtimes.Get(ctx, actor, projectID, name)
}

func (service *Service) ReloadRuntime(ctx context.Context, actor uuid.UUID, request RuntimeControlRequest) (RuntimeState, error) {
	return service.runtimes.Reload(ctx, actor, request)
}

func (service *Service) RestartRuntime(ctx context.Context, actor uuid.UUID, request RuntimeControlRequest) (RuntimeState, error) {
	return service.runtimes.Restart(ctx, actor, request)
}

func (service *Service) StopRuntime(ctx context.Context, actor uuid.UUID, request RuntimeControlRequest) (RuntimeState, error) {
	return service.runtimes.Stop(ctx, actor, request)
}

func (service *Service) ReportRuntimeDiagnostic(ctx context.Context, actor uuid.UUID, report RuntimeBrowserReport) (RuntimeState, error) {
	return service.runtimes.Report(ctx, actor, report)
}

func (service *Service) RuntimeProblems(ctx context.Context, actor, projectID uuid.UUID) ([]RuntimeDiagnostic, error) {
	return service.runtimes.Problems(ctx, actor, projectID)
}

func (service *Service) SetPreviewInspector(inspector PreviewInspector) {
	service.runtimes.mu.Lock()
	service.runtimes.inspector = inspector
	service.runtimes.mu.Unlock()
}

func (service *Service) InspectRuntime(ctx context.Context, actor uuid.UUID, request RuntimeInspectRequest) (RuntimeInspection, error) {
	return service.runtimes.Inspect(ctx, actor, request)
}

func (service *Service) AnnotateRuntime(ctx context.Context, actor uuid.UUID, request RuntimeAnnotationRequest) (RuntimeState, error) {
	return service.runtimes.Annotate(ctx, actor, request)
}

func (service *Service) ProposeRuntimeStyle(ctx context.Context, actor uuid.UUID, request RuntimeStyleProposalRequest) (RuntimeState, error) {
	return service.runtimes.ProposeStyle(ctx, actor, request)
}
