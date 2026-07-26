package privatecomputer

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type DaemonConfig struct {
	AuthKey            string
	AuthKeyIsolated    bool
	ListenAddress      string
	Display            string
	Width              int
	Height             int
	Mode               PersistenceMode
	Home               string
	StateRoot          string
	WorkspaceRoot      string
	StartURL           string
	BrowserContainment BrowserContainment
	ArtifactPublicKey  ed25519.PublicKey
	HostID             uuid.UUID
	HostVersion        string
	ImageDigest        string
	Budget             ResourceBudget
	CuaPython          string
	CuaBridge          string
}

type BrowserContainment string

const (
	BrowserSandboxed         BrowserContainment = "sandboxed"
	BrowserCleanHostBoundary BrowserContainment = "clean_host_boundary"
	BrowserUnavailable       BrowserContainment = "unavailable"
)

func (config DaemonConfig) Validate() error {
	if len(config.AuthKey) < 32 || len(config.AuthKey) > 4096 ||
		strings.TrimSpace(config.ListenAddress) == "" ||
		!strings.HasPrefix(config.Display, ":") ||
		config.Width < 640 || config.Width > 3840 ||
		config.Height < 480 || config.Height > 2160 ||
		!config.Mode.valid() || !filepath.IsAbs(config.Home) ||
		!filepath.IsAbs(config.StateRoot) ||
		!filepath.IsAbs(config.WorkspaceRoot) ||
		config.HostID == uuid.Nil || strings.TrimSpace(config.HostVersion) == "" ||
		!validImageDigest(config.ImageDigest) || config.Budget.Validate() != nil {
		return ErrInvalidContract
	}
	switch config.BrowserContainment {
	case BrowserSandboxed, BrowserUnavailable:
	case BrowserCleanHostBoundary:
		if config.Mode != ModeClean {
			return ErrInvalidContract
		}
	default:
		return ErrInvalidContract
	}
	if config.BrowserContainment == BrowserCleanHostBoundary &&
		!config.AuthKeyIsolated {
		return ErrInvalidContract
	}
	if (config.Mode == ModeClean &&
		len(config.ArtifactPublicKey) != ed25519.PublicKeySize) ||
		(config.Mode == ModePersonal &&
			len(config.ArtifactPublicKey) != 0 &&
			len(config.ArtifactPublicKey) != ed25519.PublicKeySize) {
		return ErrInvalidContract
	}
	parsed, err := url.Parse(config.StartURL)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return ErrInvalidContract
	}
	if config.BrowserContainment == BrowserUnavailable &&
		config.StartURL != "about:blank" {
		return ErrInvalidContract
	}
	if config.StartURL != "about:blank" && parsed.Scheme != "https" {
		return ErrInvalidContract
	}
	return nil
}

type DesktopDaemon struct {
	config         DaemonConfig
	logger         *slog.Logger
	mu             sync.RWMutex
	runtimeMu      sync.Mutex
	processes      map[string]*exec.Cmd
	alive          map[string]bool
	startedAt      time.Time
	ready          bool
	lastError      string
	runContext     context.Context
	controller     *HostController
	desktop        DesktopControl
	desktopFactory func(context.Context) (DesktopControl, error)
	authAttempts   map[string]authAttempt
	inputReplays   map[uuid.UUID]time.Time
}

type authAttempt struct {
	Window time.Time
	Count  int
}

type DaemonState struct {
	ProtocolVersion string          `json:"protocol_version"`
	HostID          uuid.UUID       `json:"host_id"`
	HostVersion     string          `json:"host_version"`
	ImageDigest     string          `json:"image_digest"`
	Mode            PersistenceMode `json:"mode"`
	State           State           `json:"state"`
	Workspace       string          `json:"workspace"`
	Display         string          `json:"display"`
	Width           int             `json:"width"`
	Height          int             `json:"height"`
	StartedAt       time.Time       `json:"started_at"`
	Processes       map[string]bool `json:"processes"`
	Capabilities    []Capability    `json:"capabilities"`
	PendingCommands int             `json:"pending_commands"`
	LastError       string          `json:"last_error,omitempty"`
}

func NewDesktopDaemon(
	config DaemonConfig,
	logger *slog.Logger,
) (*DesktopDaemon, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	daemon := &DesktopDaemon{
		config:       config,
		logger:       logger,
		processes:    make(map[string]*exec.Cmd),
		alive:        make(map[string]bool),
		authAttempts: make(map[string]authAttempt),
		inputReplays: make(map[uuid.UUID]time.Time),
	}
	var artifactVerifier ArtifactVerifier
	if len(config.ArtifactPublicKey) == ed25519.PublicKeySize {
		verifier, err := NewSignedArtifactVerifier(
			config.ArtifactPublicKey,
			time.Now,
		)
		if err != nil {
			return nil, err
		}
		artifactVerifier = verifier
	}
	controller, err := NewHostController(HostControllerConfig{
		StateRoot:        config.StateRoot,
		WorkspaceRoot:    config.WorkspaceRoot,
		Mode:             config.Mode,
		HostID:           config.HostID,
		HostVersion:      config.HostVersion,
		ImageDigest:      config.ImageDigest,
		Limits:           config.Budget,
		Runtime:          daemon,
		ArtifactVerifier: artifactVerifier,
	})
	if err != nil {
		return nil, err
	}
	daemon.controller = controller
	return daemon, nil
}

func (daemon *DesktopDaemon) Run(ctx context.Context) error {
	daemon.mu.Lock()
	daemon.runContext = ctx
	daemon.mu.Unlock()
	if err := daemon.startDesktop(ctx); err != nil {
		daemon.setFailure(err)
		return err
	}
	defer func() {
		_ = daemon.Stop(context.Background())
	}()

	server := &http.Server{
		Addr:              daemon.config.ListenAddress,
		Handler:           daemon.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	listener, err := net.Listen("tcp", daemon.config.ListenAddress)
	if err != nil {
		return err
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()
	budgetErrors := make(chan error, 1)
	go daemon.monitorBudgets(ctx, budgetErrors)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-budgetErrors:
		return err
	}
}

func (daemon *DesktopDaemon) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", daemon.handleHealth)
	mux.HandleFunc("GET /readyz", daemon.handleReadiness)
	mux.Handle(
		"GET /v1/state",
		daemon.authenticate(http.HandlerFunc(daemon.handleState)),
	)
	mux.Handle(
		"POST /v1/commands",
		daemon.authenticate(http.HandlerFunc(daemon.handleCommand)),
	)
	mux.Handle(
		"GET /v1/desktop/frame",
		daemon.authenticate(http.HandlerFunc(daemon.handleDesktopFrame)),
	)
	mux.Handle(
		"POST /v1/desktop/observe",
		daemon.authenticate(http.HandlerFunc(daemon.handleDesktopObserve)),
	)
	mux.Handle(
		"POST /v1/desktop/input",
		daemon.authenticate(http.HandlerFunc(daemon.handleDesktopInput)),
	)
	mux.Handle(
		"POST /v1/desktop/window-input",
		daemon.authenticate(http.HandlerFunc(daemon.handleDesktopWindowInput)),
	)
	return mux
}

func (daemon *DesktopDaemon) startDesktop(ctx context.Context) error {
	executables := []string{
		"Xvfb", "openbox", "xterm", "xdotool", "scrot",
		daemon.cuaPython(),
	}
	if daemon.config.BrowserContainment != BrowserUnavailable {
		executables = append(executables, "chromium")
	}
	for _, executable := range executables {
		if _, err := exec.LookPath(executable); err != nil {
			return fmt.Errorf("private computer: required executable %s is unavailable", executable)
		}
	}
	if err := os.MkdirAll(daemon.config.Home, 0o700); err != nil {
		return err
	}
	displayNumber := strings.TrimPrefix(daemon.config.Display, ":")
	if _, err := strconv.Atoi(displayNumber); err != nil {
		return ErrInvalidContract
	}
	if err := daemon.startProcess(
		ctx,
		"xvfb",
		"Xvfb",
		daemon.config.Display,
		"-screen", "0",
		fmt.Sprintf("%dx%dx24", daemon.config.Width, daemon.config.Height),
		"-nolisten", "tcp",
		"-noreset",
	); err != nil {
		return err
	}
	socket := filepath.Join("/tmp/.X11-unix", "X"+displayNumber)
	if err := waitForFile(ctx, socket, 10*time.Second); err != nil {
		return err
	}
	if err := daemon.startProcess(ctx, "window_manager", "openbox"); err != nil {
		return err
	}
	if err := daemon.startProcess(
		ctx,
		"terminal",
		"xterm",
		"-geometry", "100x30+24+48",
		"-title", "Ion Terminal",
	); err != nil {
		return err
	}
	if daemon.config.BrowserContainment != BrowserUnavailable {
		chromiumProfile := filepath.Join(daemon.config.Home, ".config/chromium")
		if err := clearChromiumRuntimeLocks(chromiumProfile); err != nil {
			return err
		}
		arguments := []string{
			"--disable-background-networking",
			"--disable-component-update",
			"--disable-default-apps",
			"--disable-sync",
			"--force-renderer-accessibility",
			"--metrics-recording-only",
			"--no-first-run",
			"--password-store=basic",
			"--remote-debugging-address=127.0.0.1",
			"--remote-debugging-port=9222",
			"--user-data-dir=" + chromiumProfile,
			"--window-size=" + strconv.Itoa(daemon.config.Width-80) + "," + strconv.Itoa(daemon.config.Height-100),
		}
		if daemon.config.BrowserContainment == BrowserCleanHostBoundary {
			arguments = append(arguments, "--no-sandbox")
		}
		arguments = append(arguments, daemon.config.StartURL)
		if err := daemon.startProcess(
			ctx,
			"browser",
			"chromium",
			arguments...,
		); err != nil {
			return err
		}
		if err := waitForCommand(
			ctx,
			30*time.Second,
			daemon.config.Display,
			"xdotool",
			"search",
			"--onlyvisible",
			"--class",
			"[Cc]hromium",
		); err != nil {
			return fmt.Errorf("private computer: browser window unavailable: %w", err)
		}
	}
	desktop, err := daemon.startCuaDesktopControl(ctx)
	if err != nil {
		return err
	}
	daemon.mu.Lock()
	daemon.desktop = desktop
	daemon.alive["cua_driver"] = true
	daemon.mu.Unlock()
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	for name := range daemon.processes {
		if !daemon.alive[name] {
			return fmt.Errorf("private computer: %s exited during startup", name)
		}
	}
	daemon.startedAt = time.Now().UTC()
	daemon.ready = true
	return nil
}

func clearChromiumRuntimeLocks(profile string) error {
	if !filepath.IsAbs(profile) {
		return ErrInvalidContract
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return err
	}
	for _, name := range []string{
		"SingletonCookie",
		"SingletonLock",
		"SingletonSocket",
	} {
		path := filepath.Join(profile, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf(
				"private computer: clear stale Chromium lock %s: %w",
				name,
				err,
			)
		}
	}
	return nil
}

func (daemon *DesktopDaemon) startProcess(
	ctx context.Context,
	name string,
	executable string,
	arguments ...string,
) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = append(
		sanitizedDesktopEnvironment(),
		"DISPLAY="+daemon.config.Display,
		"HOME="+daemon.config.Home,
		"XDG_CONFIG_HOME="+filepath.Join(daemon.config.Home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(daemon.config.Home, ".cache"),
	)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	daemon.mu.Lock()
	daemon.processes[name] = command
	daemon.alive[name] = true
	daemon.mu.Unlock()
	go func() {
		err := command.Wait()
		daemon.mu.Lock()
		daemon.alive[name] = false
		if daemon.ready && err != nil {
			daemon.ready = false
			daemon.lastError = name + " stopped unexpectedly"
		}
		daemon.mu.Unlock()
	}()
	return nil
}

func sanitizedDesktopEnvironment() []string {
	environment := os.Environ()
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found &&
			(name == "ION_COMPUTER_AUTH_KEY" ||
				name == "ION_COMPUTER_AUTH_KEY_FILE") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (daemon *DesktopDaemon) startCuaDesktopControl(
	ctx context.Context,
) (DesktopControl, error) {
	if daemon.desktopFactory != nil {
		return daemon.desktopFactory(ctx)
	}
	desktopEnvironment := append(
		sanitizedDesktopEnvironment(),
		"DISPLAY="+daemon.config.Display,
		"HOME="+daemon.config.Home,
		"XDG_CONFIG_HOME="+filepath.Join(daemon.config.Home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(daemon.config.Home, ".cache"),
	)
	return StartCuaDesktopControl(
		ctx,
		daemon.cuaPython(),
		daemon.cuaBridge(),
		desktopEnvironment,
		os.Stderr,
	)
}

func (daemon *DesktopDaemon) ensureDesktop() DesktopControl {
	daemon.mu.RLock()
	current := daemon.desktop
	ready := daemon.ready
	daemon.mu.RUnlock()
	if !ready {
		return nil
	}
	if current != nil && current.Available() {
		return current
	}
	daemon.runtimeMu.Lock()
	defer daemon.runtimeMu.Unlock()
	daemon.mu.RLock()
	current = daemon.desktop
	ready = daemon.ready
	runContext := daemon.runContext
	daemon.mu.RUnlock()
	if !ready || runContext == nil || runContext.Err() != nil {
		return nil
	}
	if current != nil && current.Available() {
		return current
	}
	if current != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = current.Close(closeCtx)
		cancel()
	}
	replacement, err := daemon.startCuaDesktopControl(runContext)
	if err != nil {
		daemon.mu.Lock()
		daemon.desktop = nil
		daemon.alive["cua_driver"] = false
		daemon.lastError = "Cua desktop driver recovery failed"
		daemon.mu.Unlock()
		daemon.logger.Error("private computer desktop recovery failed", "error", err)
		return nil
	}
	daemon.mu.Lock()
	daemon.desktop = replacement
	daemon.alive["cua_driver"] = true
	daemon.lastError = ""
	daemon.mu.Unlock()
	daemon.logger.Info("private computer desktop driver recovered")
	return replacement
}

func (daemon *DesktopDaemon) replaceDesktop(
	current DesktopControl,
) DesktopControl {
	if current == nil {
		return nil
	}
	daemon.runtimeMu.Lock()
	defer daemon.runtimeMu.Unlock()
	daemon.mu.Lock()
	if daemon.desktop != current {
		replacement := daemon.desktop
		ready := daemon.ready
		daemon.mu.Unlock()
		if ready && replacement != nil && replacement.Available() {
			return replacement
		}
		return nil
	}
	ready := daemon.ready
	runContext := daemon.runContext
	daemon.desktop = nil
	daemon.alive["cua_driver"] = false
	daemon.mu.Unlock()
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = current.Close(closeCtx)
	cancel()
	if !ready || runContext == nil || runContext.Err() != nil {
		return nil
	}
	replacement, err := daemon.startCuaDesktopControl(runContext)
	if err != nil {
		daemon.mu.Lock()
		daemon.lastError = "Cua desktop driver recovery failed"
		daemon.mu.Unlock()
		daemon.logger.Error("private computer desktop recovery failed", "error", err)
		return nil
	}
	daemon.mu.Lock()
	daemon.desktop = replacement
	daemon.alive["cua_driver"] = true
	daemon.lastError = ""
	daemon.mu.Unlock()
	daemon.logger.Info("private computer desktop driver recovered")
	return replacement
}

func (daemon *DesktopDaemon) Start(ctx context.Context, home string) error {
	daemon.runtimeMu.Lock()
	defer daemon.runtimeMu.Unlock()
	home, err := confinedPath(daemon.config.WorkspaceRoot, home)
	if err != nil {
		return err
	}
	if err := daemon.stopDesktop(ctx); err != nil {
		return err
	}
	daemon.mu.Lock()
	daemon.config.Home = home
	daemon.processes = make(map[string]*exec.Cmd)
	daemon.alive = make(map[string]bool)
	daemon.lastError = ""
	runContext := daemon.runContext
	daemon.mu.Unlock()
	if runContext == nil {
		runContext = ctx
	}
	return daemon.startDesktop(runContext)
}

func (daemon *DesktopDaemon) Stop(ctx context.Context) error {
	daemon.runtimeMu.Lock()
	defer daemon.runtimeMu.Unlock()
	return daemon.stopDesktop(ctx)
}

func (daemon *DesktopDaemon) Suspend(_ context.Context) error {
	daemon.runtimeMu.Lock()
	defer daemon.runtimeMu.Unlock()
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	if !daemon.ready {
		return ErrInvalidTransition
	}
	for name, command := range daemon.processes {
		if command.Process == nil || !daemon.alive[name] {
			return ErrInvalidTransition
		}
		if err := command.Process.Signal(syscall.SIGSTOP); err != nil {
			return err
		}
	}
	daemon.ready = false
	return nil
}

func (daemon *DesktopDaemon) Resume(_ context.Context) error {
	daemon.runtimeMu.Lock()
	defer daemon.runtimeMu.Unlock()
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	for name, command := range daemon.processes {
		if command.Process == nil || !daemon.alive[name] {
			return ErrInvalidTransition
		}
		if err := command.Process.Signal(syscall.SIGCONT); err != nil {
			return err
		}
	}
	daemon.ready = true
	return nil
}

func (daemon *DesktopDaemon) Running() bool {
	daemon.mu.RLock()
	defer daemon.mu.RUnlock()
	if !daemon.ready || len(daemon.processes) == 0 {
		return false
	}
	for name := range daemon.processes {
		if !daemon.alive[name] {
			return false
		}
	}
	return daemon.desktop != nil && daemon.desktop.Available()
}

func (daemon *DesktopDaemon) Workspace() string {
	daemon.mu.RLock()
	defer daemon.mu.RUnlock()
	return daemon.config.Home
}

func (daemon *DesktopDaemon) stopDesktop(ctx context.Context) error {
	daemon.mu.Lock()
	daemon.ready = false
	desktop := daemon.desktop
	daemon.desktop = nil
	daemon.alive["cua_driver"] = false
	processes := make(map[string]*os.Process, len(daemon.processes))
	for name, command := range daemon.processes {
		if command.Process != nil {
			processes[name] = command.Process
		}
	}
	daemon.mu.Unlock()
	if desktop != nil {
		closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_ = desktop.Close(closeCtx)
		cancel()
	}
	for _, process := range processes {
		_ = process.Signal(syscall.SIGTERM)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for len(processes) > 0 {
		select {
		case <-ctx.Done():
			for _, process := range processes {
				_ = process.Kill()
			}
			return ctx.Err()
		case <-deadline.C:
			for _, process := range processes {
				_ = process.Kill()
			}
			return nil
		case <-ticker.C:
			daemon.mu.RLock()
			for name := range processes {
				if !daemon.alive[name] {
					delete(processes, name)
				}
			}
			daemon.mu.RUnlock()
		}
	}
	return nil
}

func (daemon *DesktopDaemon) State() DaemonState {
	pendingCommands := len(daemon.controller.Pending())
	daemon.mu.RLock()
	defer daemon.mu.RUnlock()
	processes := make(map[string]bool, len(daemon.processes))
	for name := range daemon.processes {
		processes[name] = daemon.alive[name]
	}
	if daemon.desktop != nil {
		processes["cua_driver"] = daemon.desktop.Available()
	}
	state := StateUnavailable
	if daemon.ready {
		state = StateReady
	}
	return DaemonState{
		ProtocolVersion: ProtocolVersion,
		HostID:          daemon.config.HostID,
		HostVersion:     daemon.config.HostVersion,
		ImageDigest:     daemon.config.ImageDigest,
		Mode:            daemon.config.Mode,
		State:           state,
		Workspace:       daemon.config.Home,
		Display:         daemon.config.Display,
		Width:           daemon.config.Width,
		Height:          daemon.config.Height,
		StartedAt:       daemon.startedAt,
		Processes:       processes,
		Capabilities:    daemon.capabilities(),
		PendingCommands: pendingCommands,
		LastError:       daemon.lastError,
	}
}

func (daemon *DesktopDaemon) capabilities() []Capability {
	unavailable := map[CapabilityKind]string{
		CapabilityTerminal:        "authenticated terminal transport is not enabled",
		CapabilityFilesystem:      "authenticated filesystem transport is not enabled",
		CapabilityClipboard:       "authenticated clipboard transport is not enabled",
		CapabilityProtectedSecret: "protected secret entry is not enabled in this host task",
	}
	if daemon.desktop == nil || !daemon.desktop.Available() {
		reason := "Cua desktop driver is unavailable"
		unavailable[CapabilityScreenshot] = reason
		unavailable[CapabilityPointer] = reason
		unavailable[CapabilityKeyboard] = reason
		unavailable[CapabilityDesktopStream] = reason
	}
	if daemon.config.BrowserContainment == BrowserUnavailable {
		unavailable[CapabilityBrowser] = "Chromium is unavailable because this host cannot provide a process sandbox for persistent state"
	}
	result := make([]Capability, 0, len(capabilityKinds))
	for _, kind := range capabilityKinds {
		if reason, exists := unavailable[kind]; exists {
			result = append(result, Capability{Kind: kind, Reason: reason})
			continue
		}
		if kind == CapabilityBrowser &&
			daemon.config.BrowserContainment == BrowserCleanHostBoundary {
			result = append(result, Capability{
				Kind:      kind,
				Available: true,
				Degraded:  true,
				Reason:    "Chromium process sandbox is disabled only for this fresh Clean workspace; containment relies on the disposable non-root, capability-dropped, read-only host boundary",
			})
			continue
		}
		if kind == CapabilityKeyboard {
			result = append(result, Capability{
				Kind:      kind,
				Available: true,
				Degraded:  true,
				Reason:    "Text entry is Cua-backed; allowlisted special keys use unprivileged X11 injection because the pinned Linux Cua driver rejects key presses",
			})
			continue
		}
		result = append(result, Capability{Kind: kind, Available: true})
	}
	return result
}

func (daemon *DesktopDaemon) handleHealth(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "alive"})
}

func (daemon *DesktopDaemon) handleReadiness(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	state := daemon.State()
	if state.State != StateReady {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
			"status": "unavailable",
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (daemon *DesktopDaemon) handleState(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	_ = daemon.ensureDesktop()
	writeJSON(writer, http.StatusOK, daemon.State())
}

func (daemon *DesktopDaemon) handleCommand(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !strings.HasPrefix(
		strings.ToLower(request.Header.Get("Content-Type")),
		"application/json",
	) {
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{
			"error": "application/json is required",
		})
		return
	}
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		MaximumContractBytes,
	)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{
			"error": "invalid command envelope",
		})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{
			"error": "invalid command envelope",
		})
		return
	}
	result, err := daemon.controller.Execute(request.Context(), envelope)
	if err != nil {
		writeJSON(writer, commandErrorStatus(err), map[string]string{
			"error": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (daemon *DesktopDaemon) handleDesktopFrame(
	writer http.ResponseWriter,
	request *http.Request,
) {
	desktop := daemon.ensureDesktop()
	if desktop == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
			"error": "desktop stream unavailable",
		})
		return
	}
	frameCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frame, err := desktop.Frame(frameCtx)
	if err != nil {
		if replacement := daemon.replaceDesktop(desktop); replacement != nil {
			retryCtx, retryCancel := context.WithTimeout(
				request.Context(),
				6*time.Second,
			)
			frame, err = replacement.Frame(retryCtx)
			retryCancel()
		}
	}
	if err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
			"error": "desktop frame unavailable",
		})
		return
	}
	if int64(len(frame.Data)) > daemon.config.Budget.ScreenshotBytes {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "desktop frame exceeds screenshot budget",
		})
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", frame.MIMEType)
	writer.Header().Set("X-Ion-Frame-Sequence", strconv.FormatUint(frame.Sequence, 10))
	writer.Header().Set("X-Ion-Frame-Digest", frame.Digest)
	writer.Header().Set("X-Ion-Frame-Width", strconv.Itoa(frame.Width))
	writer.Header().Set("X-Ion-Frame-Height", strconv.Itoa(frame.Height))
	writer.Header().Set("X-Ion-Frame-Captured-At", frame.CapturedAt.Format(time.RFC3339Nano))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(frame.Data)
}

func (daemon *DesktopDaemon) handleDesktopInput(
	writer http.ResponseWriter,
	request *http.Request,
) {
	leaseID, requestID, desktop, ready, accepted := daemon.desktopInputIdentity(
		writer, request,
	)
	if !accepted {
		return
	}
	if !ready || desktop == nil || !desktop.Available() {
		desktop = daemon.ensureDesktop()
		if desktop == nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
				"error": "desktop input unavailable",
			})
			return
		}
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input DesktopInput
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{
			"error": "invalid desktop input",
		})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{
			"error": "invalid desktop input",
		})
		return
	}
	inputCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	if input.Kind == DesktopInputKey || input.Kind == DesktopInputHotkey {
		err = daemon.x11KeyboardInput(inputCtx, input)
	} else {
		err = desktop.Input(inputCtx, input)
	}
	if err != nil {
		writeJSON(writer, commandErrorStatus(err), map[string]string{
			"error": "desktop input rejected",
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"accepted": true,
		"input_id": requestID,
		"lease_id": leaseID,
	})
}

func (daemon *DesktopDaemon) x11KeyboardInput(
	ctx context.Context,
	input DesktopInput,
) error {
	if err := input.Validate(daemon.config.Width, daemon.config.Height); err != nil {
		return err
	}
	chord, err := xdotoolKeyChord(input)
	if err != nil {
		return err
	}
	command := exec.CommandContext(
		ctx,
		"xdotool",
		"key",
		"--clearmodifiers",
		chord,
	)
	command.Env = append(
		sanitizedDesktopEnvironment(),
		"DISPLAY="+daemon.config.Display,
	)
	if err := command.Run(); err != nil {
		return fmt.Errorf("private computer: X11 keyboard input: %w", err)
	}
	return nil
}

func xdotoolKeyChord(input DesktopInput) (string, error) {
	keys := append([]string(nil), input.Keys...)
	if input.Kind == DesktopInputKey {
		keys = append(append([]string(nil), input.Modifiers...), input.Key)
	} else if input.Kind != DesktopInputHotkey {
		return "", ErrInvalidContract
	}
	if len(keys) == 0 {
		return "", ErrInvalidContract
	}
	mapped := make([]string, 0, len(keys))
	for _, key := range keys {
		value, exists := xdotoolKeyName(strings.ToLower(strings.TrimSpace(key)))
		if !exists {
			return "", ErrInvalidContract
		}
		mapped = append(mapped, value)
	}
	return strings.Join(mapped, "+"), nil
}

func xdotoolKeyName(key string) (string, bool) {
	keys := map[string]string{
		"alt":       "alt",
		"backspace": "BackSpace",
		"ctrl":      "ctrl",
		"delete":    "Delete",
		"down":      "Down",
		"end":       "End",
		"enter":     "Return",
		"esc":       "Escape",
		"home":      "Home",
		"left":      "Left",
		"meta":      "super",
		"pagedown":  "Page_Down",
		"pageup":    "Page_Up",
		"right":     "Right",
		"shift":     "shift",
		"space":     "space",
		"tab":       "Tab",
		"up":        "Up",
	}
	if value, exists := keys[key]; exists {
		return value, true
	}
	if len(key) == 1 &&
		((key[0] >= 'a' && key[0] <= 'z') ||
			(key[0] >= '0' && key[0] <= '9')) {
		return key, true
	}
	return "", false
}

func (daemon *DesktopDaemon) handleDesktopObserve(
	writer http.ResponseWriter,
	request *http.Request,
) {
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input DesktopObservationRequest
	if decoder.Decode(&input) != nil || input.Validate() != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{
			"error": "invalid desktop observation",
		})
		return
	}
	desktop := daemon.ensureDesktop()
	if desktop == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
			"error": "desktop observation unavailable",
		})
		return
	}
	observeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := desktop.Observe(observeCtx, input)
	if err != nil {
		if replacement := daemon.replaceDesktop(desktop); replacement != nil {
			retryCtx, retryCancel := context.WithTimeout(
				request.Context(),
				6*time.Second,
			)
			result, err = replacement.Observe(retryCtx, input)
			retryCancel()
		}
	}
	if err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
			"error": "desktop observation unavailable",
		})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(result)
}

func (daemon *DesktopDaemon) handleDesktopWindowInput(
	writer http.ResponseWriter,
	request *http.Request,
) {
	leaseID, requestID, desktop, ready, accepted := daemon.desktopInputIdentity(
		writer, request,
	)
	if !accepted {
		return
	}
	if !ready || desktop == nil || !desktop.Available() {
		desktop = daemon.ensureDesktop()
		if desktop == nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
				"error": "desktop input unavailable",
			})
			return
		}
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input DesktopWindowInput
	if decoder.Decode(&input) != nil || input.Validate() != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{
			"error": "invalid desktop input",
		})
		return
	}
	inputCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	result, err := desktop.WindowInput(inputCtx, input)
	if err != nil {
		writeJSON(writer, commandErrorStatus(err), map[string]string{
			"error": "desktop input rejected",
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"accepted": true,
		"input_id": requestID,
		"lease_id": leaseID,
		"result":   result,
	})
}

func (daemon *DesktopDaemon) desktopInputIdentity(
	writer http.ResponseWriter,
	request *http.Request,
) (uuid.UUID, uuid.UUID, DesktopControl, bool, bool) {
	leaseID, leaseErr := uuid.Parse(strings.TrimSpace(
		request.Header.Get("X-Ion-Control-Lease"),
	))
	requestID, requestErr := uuid.Parse(strings.TrimSpace(
		request.Header.Get("X-Ion-Input-ID"),
	))
	if leaseErr != nil || requestErr != nil || leaseID == uuid.Nil ||
		requestID == uuid.Nil {
		writeJSON(writer, http.StatusForbidden, map[string]string{
			"error": "exact desktop control lease and input identity are required",
		})
		return uuid.Nil, uuid.Nil, nil, false, false
	}
	daemon.mu.Lock()
	now := time.Now().UTC()
	for id, observedAt := range daemon.inputReplays {
		if now.Sub(observedAt) > MaximumRequestTTL {
			delete(daemon.inputReplays, id)
		}
	}
	if _, replay := daemon.inputReplays[requestID]; replay {
		daemon.mu.Unlock()
		writeJSON(writer, http.StatusConflict, map[string]string{
			"error": "desktop input replay rejected",
		})
		return uuid.Nil, uuid.Nil, nil, false, false
	}
	desktop := daemon.desktop
	ready := daemon.ready
	daemon.inputReplays[requestID] = now
	daemon.mu.Unlock()
	return leaseID, requestID, desktop, ready, true
}

func (daemon *DesktopDaemon) cuaPython() string {
	if value := strings.TrimSpace(daemon.config.CuaPython); value != "" {
		return value
	}
	return "python3"
}

func (daemon *DesktopDaemon) cuaBridge() string {
	if value := strings.TrimSpace(daemon.config.CuaBridge); value != "" {
		return value
	}
	return "/usr/local/libexec/ion-cua-bridge.py"
}

func (daemon *DesktopDaemon) authenticate(next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(daemon.config.AuthKey))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		remote := request.RemoteAddr
		if host, _, err := net.SplitHostPort(request.RemoteAddr); err == nil {
			remote = host
		}
		if !daemon.authenticationAllowed(remote, time.Now().UTC()) {
			writer.Header().Set("Retry-After", "60")
			writeJSON(writer, http.StatusTooManyRequests, map[string]string{
				"error": "authentication rate limit exceeded",
			})
			return
		}
		const prefix = "Bearer "
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, prefix) {
			daemon.recordAuthenticationFailure(remote, time.Now().UTC())
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(writer, http.StatusUnauthorized, map[string]string{
				"error": "authentication required",
			})
			return
		}
		actual := sha256.Sum256([]byte(strings.TrimPrefix(authorization, prefix)))
		if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			daemon.recordAuthenticationFailure(remote, time.Now().UTC())
			writer.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(writer, http.StatusUnauthorized, map[string]string{
				"error": "authentication required",
			})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (daemon *DesktopDaemon) authenticationAllowed(
	remote string,
	now time.Time,
) bool {
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	attempt := daemon.authAttempts[remote]
	if attempt.Window.IsZero() || now.Sub(attempt.Window) >= time.Minute {
		delete(daemon.authAttempts, remote)
		return true
	}
	return attempt.Count < 20
}

func (daemon *DesktopDaemon) recordAuthenticationFailure(
	remote string,
	now time.Time,
) {
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	attempt := daemon.authAttempts[remote]
	if attempt.Window.IsZero() || now.Sub(attempt.Window) >= time.Minute {
		attempt = authAttempt{Window: now}
	}
	attempt.Count++
	daemon.authAttempts[remote] = attempt
}

func (daemon *DesktopDaemon) monitorBudgets(
	ctx context.Context,
	errorsChannel chan<- error,
) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := daemon.controller.EnforceBudgets(ctx); err != nil {
				select {
				case errorsChannel <- err:
				default:
				}
				return
			}
		}
	}
}

func commandErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrInvalidContract),
		errors.Is(err, ErrInvalidTransition):
		return http.StatusBadRequest
	case errors.Is(err, ErrScopeMismatch):
		return http.StatusForbidden
	case errors.Is(err, ErrSessionNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrStaleRevision),
		errors.Is(err, ErrReplayConflict),
		errors.Is(err, ErrOutcomeUnknown):
		return http.StatusConflict
	case errors.Is(err, ErrArtifactRequired):
		return http.StatusPreconditionFailed
	case errors.Is(err, ErrBudgetExceeded):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrUnsupported):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (daemon *DesktopDaemon) setFailure(err error) {
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	daemon.ready = false
	daemon.lastError = err.Error()
}

func waitForFile(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("private computer: timed out waiting for %s", filepath.Base(path))
		case <-ticker.C:
			if _, err := os.Stat(path); err == nil {
				return nil
			}
		}
	}
}

func waitForCommand(
	ctx context.Context,
	timeout time.Duration,
	display string,
	executable string,
	arguments ...string,
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastError error
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastError == nil {
				return errors.New("command did not succeed")
			}
			return lastError
		case <-ticker.C:
			command := exec.CommandContext(ctx, executable, arguments...)
			command.Env = append(
				os.Environ(),
				"DISPLAY="+display,
			)
			if err := command.Run(); err == nil {
				return nil
			} else {
				lastError = err
			}
		}
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		http.Error(writer, "response unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}
