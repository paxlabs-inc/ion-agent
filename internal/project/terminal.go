package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const terminalStateKind = "project_terminal_v1"

type terminalRuntime struct {
	mu       sync.Mutex
	state    TerminalState
	command  *exec.Cmd
	pty      *os.File
	output   []byte
	base     uint64
	limit    int
	pending  string
	cancel   context.CancelFunc
	onFinish func(TerminalState, string)
	target   controllease.Target
	owner    controllease.Owner
}

type TerminalService struct {
	mu       sync.Mutex
	store    *session.Store
	clock    types.Clock
	projects *Service
	control  *controllease.Service
	sessions map[uuid.UUID]*terminalRuntime
}

func newTerminalService(
	store *session.Store,
	clock types.Clock,
	projects *Service,
	control *controllease.Service,
) *TerminalService {
	return &TerminalService{store: store, clock: clock, projects: projects,
		control: control, sessions: make(map[uuid.UUID]*terminalRuntime)}
}

func (service *TerminalService) Start(ctx context.Context, actor uuid.UUID, request ProcessRequest) (TerminalState, error) {
	return service.start(ctx, actor, request, nil)
}

func (service *TerminalService) start(ctx context.Context, actor uuid.UUID, request ProcessRequest,
	onFinish func(TerminalState, string)) (TerminalState, error) {
	if actor == uuid.Nil || request.ProjectID == uuid.Nil || request.WorkspaceRevision == 0 ||
		(request.Mode != ProcessOneShot && request.Mode != ProcessPTY) || len(request.Argv) == 0 || len(request.Argv) > 128 {
		return TerminalState{}, fmt.Errorf("project: bounded argv-safe process request is required")
	}
	project, err := service.projects.Get(ctx, actor, request.ProjectID)
	if err != nil {
		return TerminalState{}, err
	}
	if project.WorkspaceRevision != request.WorkspaceRevision {
		return TerminalState{}, ErrStaleRevision
	}
	if project.Lifecycle != LifecycleReady {
		return TerminalState{}, ErrConflict
	}
	directory, err := secureWorkspaceDirectory(project.Root, request.WorkingDirectory)
	if err != nil {
		return TerminalState{}, err
	}
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = 120
	}
	if request.TimeoutSeconds < 1 || request.TimeoutSeconds > 1800 {
		return TerminalState{}, fmt.Errorf("project: process timeout is out of bounds")
	}
	if request.OutputBytes == 0 {
		request.OutputBytes = 1 << 20
	}
	if request.OutputBytes < 4096 || request.OutputBytes > 4<<20 {
		return TerminalState{}, fmt.Errorf("project: process output bound is invalid")
	}
	environment, err := projectProcessEnvironment(request.Environment)
	if err != nil {
		return TerminalState{}, err
	}
	executable, err := exec.LookPath(request.Argv[0])
	if err != nil {
		return TerminalState{}, fmt.Errorf("project: process executable %q is unavailable", request.Argv[0])
	}
	request.Argv[0] = executable
	for index := range environment {
		if strings.HasPrefix(environment[index], "PATH=") {
			environment[index] = "PATH=" + filepath.Dir(executable) + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
		}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	command := exec.Command(request.Argv[0], request.Argv[1:]...)
	command.Dir, command.Env = directory, environment
	runtime := &terminalRuntime{command: command, limit: request.OutputBytes, cancel: cancel,
		onFinish: onFinish,
		state: TerminalState{Version: TerminalVersion, ID: uuid.New(), ActorID: actor,
			ProjectID: project.ID, WorkspaceRevision: project.WorkspaceRevision, Mode: request.Mode,
			Argv: append([]string(nil), request.Argv...), WorkingDirectory: directory,
			Status: "starting", StartedAt: service.clock.Now().UTC()}}
	runtime.target, runtime.owner = terminalControlBinding(
		ctx, actor, runtime.state.ID, runtime.state.WorkspaceRevision,
	)
	runtime.state.ControlSessionID = copyUUID(runtime.target.SessionID)
	runtime.state.ControlOwner = runtime.owner
	var reader io.Reader
	if request.Mode == ProcessPTY {
		master, slave, openErr := openProjectPTY(request.Columns, request.Rows)
		if openErr != nil {
			cancel()
			return TerminalState{}, openErr
		}
		runtime.pty, reader = master, master
		command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
		command.SysProcAttr = projectPTYSysProcAttr()
		defer slave.Close()
	} else {
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Stdout, command.Stderr = runtime, runtime
	}
	if err := command.Start(); err != nil {
		cancel()
		if runtime.pty != nil {
			_ = runtime.pty.Close()
		}
		return TerminalState{}, err
	}
	runtime.state.PID = command.Process.Pid
	runtime.state.ProcessStartToken = linuxProcessStartToken(command.Process.Pid)
	runtime.state.Status = "running"
	service.mu.Lock()
	service.sessions[runtime.state.ID] = runtime
	service.mu.Unlock()
	service.persist(runtime)
	if reader != nil {
		go func() { _, _ = io.Copy(runtime, reader) }()
	}
	go service.wait(runCtx, runtime, time.Duration(request.TimeoutSeconds)*time.Second)
	return runtime.snapshot(), nil
}

func (service *TerminalService) wait(ctx context.Context, runtime *terminalRuntime, timeout time.Duration) {
	wait := make(chan error, 1)
	go func() { wait <- runtime.command.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var err error
	select {
	case err = <-wait:
	case <-timer.C:
		runtime.mu.Lock()
		runtime.state.TimedOut = true
		runtime.mu.Unlock()
		_ = killProjectProcess(runtime, syscall.SIGKILL)
		err = <-wait
	case <-ctx.Done():
		_ = killProjectProcess(runtime, syscall.SIGKILL)
		err = <-wait
	}
	if runtime.pty != nil {
		_ = runtime.pty.Close()
	}
	runtime.flushPending()
	now := service.clock.Now().UTC()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	runtime.mu.Lock()
	runtime.state.ExitCode, runtime.state.FinishedAt = &exitCode, &now
	if runtime.state.TimedOut {
		runtime.state.Status = "timed_out"
	} else if ctx.Err() != nil {
		runtime.state.Status = "cancelled"
	} else if exitCode == 0 {
		runtime.state.Status = "completed"
	} else {
		runtime.state.Status = "failed"
	}
	runtime.mu.Unlock()
	service.persist(runtime)
	service.reconcileStoppedControl(runtime, "terminal_finished_executor_stopped")
	if runtime.onFinish != nil {
		runtime.mu.Lock()
		state, output := runtime.state, string(runtime.output)
		runtime.mu.Unlock()
		runtime.onFinish(state, output)
	}
}

func (runtime *terminalRuntime) Write(payload []byte) (int, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.pending += string(payload)
	for {
		lineEnd := strings.IndexByte(runtime.pending, '\n')
		if lineEnd < 0 {
			break
		}
		line := runtime.pending[:lineEnd+1]
		runtime.pending = runtime.pending[lineEnd+1:]
		redacted, _ := redactSecrets("terminal", []byte(line))
		runtime.appendOutput(redacted)
	}
	return len(payload), nil
}

func (runtime *terminalRuntime) flushPending() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.pending != "" {
		redacted, _ := redactSecrets("terminal", []byte(runtime.pending))
		runtime.appendOutput(redacted)
		runtime.pending = ""
	}
}

func (runtime *terminalRuntime) appendOutput(payload []byte) {
	runtime.output = append(runtime.output, payload...)
	runtime.state.OutputCursor += uint64(len(payload))
	if len(runtime.output) > runtime.limit {
		dropped := len(runtime.output) - runtime.limit
		runtime.output = append([]byte(nil), runtime.output[dropped:]...)
		runtime.base += uint64(dropped)
		runtime.state.DroppedBytes += uint64(dropped)
		runtime.state.Truncated = true
	}
}

func (runtime *terminalRuntime) snapshot() TerminalState {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.state
}

func (service *TerminalService) Replay(ctx context.Context, actor, id uuid.UUID, cursor uint64) (TerminalReplay, error) {
	runtime, err := service.runtime(actor, id)
	if err != nil {
		if !errors.Is(err, ErrTerminalNotFound) {
			return TerminalReplay{}, err
		}
		raw, loadErr := service.store.LoadLivingState(ctx, terminalStateKind, terminalScope(actor, id))
		if loadErr != nil {
			if errors.Is(loadErr, sql.ErrNoRows) {
				return TerminalReplay{}, ErrTerminalNotFound
			}
			return TerminalReplay{}, loadErr
		}
		var state TerminalState
		if json.Unmarshal(raw, &state) != nil || state.ActorID != actor || state.ID != id {
			return TerminalReplay{}, fmt.Errorf("project: invalid encrypted terminal state")
		}
		return TerminalReplay{State: state, FromCursor: state.OutputCursor,
			NextCursor: state.OutputCursor, Gap: cursor < state.OutputCursor}, nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return TerminalReplay{}, err
	}
	gap := cursor < runtime.base
	if gap {
		cursor = runtime.base
	}
	if cursor > runtime.state.OutputCursor {
		return TerminalReplay{}, fmt.Errorf("project: terminal cursor is ahead of durable state")
	}
	offset := int(cursor - runtime.base)
	return TerminalReplay{State: runtime.state, FromCursor: cursor,
		NextCursor: runtime.state.OutputCursor, Output: string(runtime.output[offset:]), Gap: gap}, nil
}

func (service *TerminalService) Input(ctx context.Context, actor, id uuid.UUID, input []byte) error {
	release, err := service.beginAutomation(ctx, actor, id)
	if err != nil {
		return err
	}
	defer release()
	return service.input(ctx, actor, id, input)
}

func (service *TerminalService) input(ctx context.Context, actor, id uuid.UUID, input []byte) error {
	if len(input) == 0 || len(input) > 64<<10 {
		return fmt.Errorf("project: bounded terminal input is required")
	}
	runtime, err := service.runtime(actor, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.state.Status != "running" || runtime.pty == nil {
		return ErrConflict
	}
	_, err = runtime.pty.Write(input)
	return err
}

func (service *TerminalService) Resize(ctx context.Context, actor, id uuid.UUID, columns, rows uint16) error {
	release, err := service.beginAutomation(ctx, actor, id)
	if err != nil {
		return err
	}
	defer release()
	return service.resize(ctx, actor, id, columns, rows)
}

func (service *TerminalService) resize(ctx context.Context, actor, id uuid.UUID, columns, rows uint16) error {
	if columns < 20 || rows < 5 || columns > 500 || rows > 300 {
		return fmt.Errorf("project: terminal dimensions are out of bounds")
	}
	runtime, err := service.runtime(actor, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.pty == nil || runtime.state.Status != "running" {
		return ErrConflict
	}
	return resizeProjectPTY(runtime.pty, columns, rows)
}

func (service *TerminalService) Signal(ctx context.Context, actor, id uuid.UUID, signal syscall.Signal) error {
	release, err := service.beginAutomation(ctx, actor, id)
	if err != nil {
		return err
	}
	defer release()
	return service.signal(ctx, actor, id, signal)
}

func (service *TerminalService) signal(ctx context.Context, actor, id uuid.UUID, signal syscall.Signal) error {
	if signal != syscall.SIGINT && signal != syscall.SIGTERM && signal != syscall.SIGKILL && signal != syscall.SIGHUP {
		return fmt.Errorf("project: signal is not allowed")
	}
	runtime, err := service.runtime(actor, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return killProjectProcess(runtime, signal)
}

func (service *TerminalService) Cancel(ctx context.Context, actor, id uuid.UUID) error {
	release, err := service.beginAutomation(ctx, actor, id)
	if err != nil {
		return err
	}
	defer release()
	return service.cancelRuntime(ctx, actor, id)
}

func (service *TerminalService) cancelRuntime(ctx context.Context, actor, id uuid.UUID) error {
	runtime, err := service.runtime(actor, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.cancel()
	return nil
}

func (service *TerminalService) InputWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
	input []byte,
) error {
	release, err := service.beginOperator(ctx, actor, id, leaseID, revision)
	if err != nil {
		return err
	}
	defer release()
	return service.input(ctx, actor, id, input)
}

func (service *TerminalService) ResizeWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
	columns, rows uint16,
) error {
	release, err := service.beginOperator(ctx, actor, id, leaseID, revision)
	if err != nil {
		return err
	}
	defer release()
	return service.resize(ctx, actor, id, columns, rows)
}

func (service *TerminalService) SignalWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
	signal syscall.Signal,
) error {
	release, err := service.beginOperator(ctx, actor, id, leaseID, revision)
	if err != nil {
		return err
	}
	defer release()
	return service.signal(ctx, actor, id, signal)
}

func (service *TerminalService) CancelWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
) error {
	release, err := service.beginOperator(ctx, actor, id, leaseID, revision)
	if err != nil {
		return err
	}
	defer release()
	return service.cancelRuntime(ctx, actor, id)
}

func (service *TerminalService) ControlBinding(
	ctx context.Context,
	actor, id uuid.UUID,
) (controllease.Target, controllease.Owner, error) {
	runtime, err := service.runtime(actor, id)
	if err != nil {
		if !errors.Is(err, ErrTerminalNotFound) {
			return controllease.Target{}, controllease.Owner{}, err
		}
		raw, loadErr := service.store.LoadLivingState(
			ctx, terminalStateKind, terminalScope(actor, id),
		)
		if loadErr != nil {
			if errors.Is(loadErr, sql.ErrNoRows) {
				return controllease.Target{}, controllease.Owner{}, ErrTerminalNotFound
			}
			return controllease.Target{}, controllease.Owner{}, loadErr
		}
		var state TerminalState
		if json.Unmarshal(raw, &state) != nil ||
			state.Version != TerminalVersion ||
			state.ActorID != actor || state.ID != id {
			return controllease.Target{}, controllease.Owner{},
				fmt.Errorf("project: invalid encrypted terminal state")
		}
		return controllease.Target{
			ActorID: actor, SessionID: copyUUID(state.ControlSessionID),
			Kind: controllease.ResourceTerminal, ResourceID: id.String(),
		}, state.ControlOwner, nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.target, runtime.owner, nil
}

func (service *TerminalService) ControlRunning(actor, id uuid.UUID) bool {
	runtime, err := service.runtime(actor, id)
	if err != nil {
		return false
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.state.Status == "running"
}

func (service *TerminalService) beginAutomation(
	ctx context.Context,
	actor, id uuid.UUID,
) (func(), error) {
	if service.control == nil {
		return func() {}, nil
	}
	runtime, err := service.runtime(actor, id)
	if err != nil {
		return nil, err
	}
	return service.control.BeginAutomation(ctx, runtime.target)
}

func (service *TerminalService) beginOperator(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
) (func(), error) {
	if service.control == nil {
		return nil, fmt.Errorf("project: terminal control is unavailable")
	}
	runtime, err := service.runtime(actor, id)
	if err != nil {
		return nil, err
	}
	release, _, err := service.control.BeginOperator(
		ctx, runtime.target, leaseID, revision,
	)
	return release, err
}

func (service *TerminalService) runtime(actor, id uuid.UUID) (*terminalRuntime, error) {
	service.mu.Lock()
	runtime := service.sessions[id]
	service.mu.Unlock()
	if runtime == nil || runtime.state.ActorID != actor {
		return nil, ErrTerminalNotFound
	}
	return runtime, nil
}

func killProjectProcess(runtime *terminalRuntime, signal syscall.Signal) error {
	runtime.mu.Lock()
	pid, status := runtime.state.PID, runtime.state.Status
	runtime.mu.Unlock()
	if pid <= 0 || status != "running" {
		return ErrConflict
	}
	err := syscall.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (service *TerminalService) persist(runtime *terminalRuntime) {
	state := runtime.snapshot()
	raw, err := json.Marshal(state)
	if err == nil {
		_ = service.store.SaveLivingState(context.Background(), terminalStateKind,
			terminalScope(state.ActorID, state.ID), raw)
	}
}

func (service *TerminalService) reconcileStoppedControl(
	runtime *terminalRuntime,
	reason string,
) {
	if service.control == nil {
		return
	}
	_ = service.control.ReconcileStopped(
		context.Background(), runtime.target, reason,
	)
}

func (service *TerminalService) ReconcileAll(ctx context.Context) error {
	states, err := service.store.ListLivingStates(ctx, terminalStateKind)
	if err != nil {
		return err
	}
	for _, persisted := range states {
		var state TerminalState
		if json.Unmarshal(persisted.State, &state) != nil || state.Version != TerminalVersion {
			return fmt.Errorf("project: invalid encrypted terminal state")
		}
		if state.Status == "running" || state.Status == "starting" {
			if state.PID > 0 && state.ProcessStartToken != "" && linuxProcessStartToken(state.PID) == state.ProcessStartToken {
				_ = syscall.Kill(-state.PID, syscall.SIGKILL)
			}
			now, exit := service.clock.Now().UTC(), -1
			state.Status, state.ExitCode, state.FinishedAt = "recovered_stopped", &exit, &now
			raw, _ := json.Marshal(state)
			if err := service.store.SaveLivingState(ctx, terminalStateKind, persisted.Scope, raw); err != nil {
				return err
			}
			if service.control != nil {
				target := controllease.Target{
					ActorID:    state.ActorID,
					SessionID:  copyUUID(state.ControlSessionID),
					Kind:       controllease.ResourceTerminal,
					ResourceID: state.ID.String(),
				}
				if err := service.control.ReconcileStopped(
					ctx, target, "daemon_restart_terminal_stopped",
				); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (service *TerminalService) Close() error {
	service.mu.Lock()
	sessions := make([]*terminalRuntime, 0, len(service.sessions))
	for _, runtime := range service.sessions {
		sessions = append(sessions, runtime)
	}
	service.mu.Unlock()
	for _, runtime := range sessions {
		runtime.cancel()
	}
	return nil
}

func secureWorkspaceDirectory(root, relative string) (string, error) {
	if strings.TrimSpace(relative) == "" {
		relative = "."
	}
	absolute := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	if !pathWithin(root, absolute) {
		return "", ErrProtectedPath
	}
	current := root
	resolved, err := filepath.Rel(root, absolute)
	if err != nil {
		return "", err
	}
	for _, part := range strings.Split(resolved, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.Join(ErrProtectedPath, err)
		}
	}
	return absolute, nil
}

func projectProcessEnvironment(input []string) ([]string, error) {
	values := map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin", "LANG": "C.UTF-8", "HOME": "/nonexistent"}
	allowed := map[string]bool{"LANG": true, "LC_ALL": true, "TERM": true, "CI": true, "NODE_ENV": true, "PORT": true}
	for _, entry := range input {
		name, value, found := strings.Cut(entry, "=")
		if !found || !allowed[name] || len(value) > 4096 || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("project: process environment contains a denied entry")
		}
		values[name] = value
	}
	result := make([]string, 0, len(values))
	for _, name := range []string{"PATH", "HOME", "LANG", "LC_ALL", "TERM", "CI", "NODE_ENV", "PORT"} {
		if value, ok := values[name]; ok {
			result = append(result, name+"="+value)
		}
	}
	return result, nil
}

func linuxProcessStartToken(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return ""
	}
	closing := strings.LastIndexByte(string(data), ')')
	if closing < 0 {
		return ""
	}
	fields := strings.Fields(string(data)[closing+1:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

func terminalScope(actor, id uuid.UUID) string { return actor.String() + ":" + id.String() }

func terminalControlBinding(
	ctx context.Context,
	actor, terminalID uuid.UUID,
	revision uint64,
) (controllease.Target, controllease.Owner) {
	var sessionID, turnID, taskID *uuid.UUID
	agentID := "operator"
	if scope, ok := controlplane.ApprovalScopeFromContext(ctx); ok {
		sessionID = copyUUID(scope.SessionID)
		turnID = copyUUID(scope.TurnID)
		taskID = copyUUID(scope.TaskID)
		if strings.TrimSpace(scope.AgentID) != "" {
			agentID = strings.TrimSpace(scope.AgentID)
		}
	}
	if taskID == nil {
		taskID = copyUUID(turnID)
	}
	if taskID == nil {
		taskID = copyUUID(&terminalID)
	}
	return controllease.Target{
			ActorID: actor, SessionID: sessionID,
			Kind: controllease.ResourceTerminal, ResourceID: terminalID.String(),
		}, controllease.Owner{
			TurnID: turnID, TaskID: taskID, AgentID: agentID,
			Action: "project.process.start", Revision: revision,
		}
}

func copyUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (service *Service) StartProcess(ctx context.Context, actor uuid.UUID, request ProcessRequest) (TerminalState, error) {
	return service.terminals.Start(ctx, actor, request)
}

func (service *Service) ReplayTerminal(ctx context.Context, actor, id uuid.UUID, cursor uint64) (TerminalReplay, error) {
	return service.terminals.Replay(ctx, actor, id, cursor)
}

func (service *Service) WriteTerminal(ctx context.Context, actor, id uuid.UUID, input []byte) error {
	return service.terminals.Input(ctx, actor, id, input)
}

func (service *Service) ResizeTerminal(ctx context.Context, actor, id uuid.UUID, columns, rows uint16) error {
	return service.terminals.Resize(ctx, actor, id, columns, rows)
}

func (service *Service) SignalTerminal(ctx context.Context, actor, id uuid.UUID, signal syscall.Signal) error {
	return service.terminals.Signal(ctx, actor, id, signal)
}

func (service *Service) CancelTerminal(ctx context.Context, actor, id uuid.UUID) error {
	return service.terminals.Cancel(ctx, actor, id)
}

func (service *Service) TerminalControlBinding(
	ctx context.Context,
	actor, id uuid.UUID,
) (controllease.Target, controllease.Owner, error) {
	return service.terminals.ControlBinding(ctx, actor, id)
}

func (service *Service) TerminalControlRunning(
	actor, id uuid.UUID,
) bool {
	return service.terminals.ControlRunning(actor, id)
}

func (service *Service) TerminalControlRequired() bool {
	return service != nil && service.terminals != nil &&
		service.terminals.control != nil
}

func (service *Service) WriteTerminalWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
	input []byte,
) error {
	return service.terminals.InputWithLease(
		ctx, actor, id, leaseID, revision, input,
	)
}

func (service *Service) ResizeTerminalWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
	columns, rows uint16,
) error {
	return service.terminals.ResizeWithLease(
		ctx, actor, id, leaseID, revision, columns, rows,
	)
}

func (service *Service) SignalTerminalWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
	signal syscall.Signal,
) error {
	return service.terminals.SignalWithLease(
		ctx, actor, id, leaseID, revision, signal,
	)
}

func (service *Service) CancelTerminalWithLease(
	ctx context.Context,
	actor, id, leaseID uuid.UUID,
	revision uint64,
) error {
	return service.terminals.CancelWithLease(
		ctx, actor, id, leaseID, revision,
	)
}
