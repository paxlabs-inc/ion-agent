package privatecomputer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBrowserContainmentFailsClosedForPersonalState(t *testing.T) {
	config := daemonTestConfig(t, ModePersonal)
	config.BrowserContainment = BrowserCleanHostBoundary
	if err := config.Validate(); err == nil {
		t.Fatal("clean-only degraded browser containment accepted Personal mode")
	}
	config.BrowserContainment = BrowserUnavailable
	if err := config.Validate(); err != nil {
		t.Fatalf("explicit unavailable browser boundary: %v", err)
	}
	config.StartURL = "https://example.com"
	if err := config.Validate(); err == nil {
		t.Fatal("unavailable browser accepted a network start URL")
	}
	config.BrowserContainment = BrowserSandboxed
	if err := config.Validate(); err != nil {
		t.Fatalf("sandboxed browser boundary: %v", err)
	}

	clean := daemonTestConfig(t, ModeClean)
	clean.BrowserContainment = BrowserCleanHostBoundary
	clean.ArtifactPublicKey = bytes.Repeat([]byte{1}, 32)
	if err := clean.Validate(); err == nil {
		t.Fatal("degraded Clean browser accepted a readable daemon auth key")
	}
	clean.AuthKeyIsolated = true
	if err := clean.Validate(); err != nil {
		t.Fatalf("isolated degraded Clean browser: %v", err)
	}
}

func TestDesktopChildEnvironmentExcludesHostAuthentication(t *testing.T) {
	t.Setenv("ION_COMPUTER_AUTH_KEY", "must-not-leak")
	t.Setenv("ION_COMPUTER_AUTH_KEY_FILE", "/must-not-leak")
	for _, entry := range sanitizedDesktopEnvironment() {
		if strings.HasPrefix(entry, "ION_COMPUTER_AUTH_KEY=") ||
			strings.HasPrefix(entry, "ION_COMPUTER_AUTH_KEY_FILE=") {
			t.Fatalf("desktop child environment leaked host authentication: %q", entry)
		}
	}
}

func TestCommandEndpointAuthenticatesValidatesAndPersistsReplay(t *testing.T) {
	config := daemonTestConfig(t, ModePersonal)
	config.BrowserContainment = BrowserUnavailable
	daemon, err := NewDesktopDaemon(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	scope := testScope(ModePersonal)
	envelope := controllerEnvelope(
		now,
		scope,
		OperationProvision,
		1,
		"api-provision",
	)
	envelope.Payload = lifecyclePayload(t, LifecyclePayload{
		Budget: budgetPointer(testBudget()),
	})
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := httptest.NewRequest(
		http.MethodPost,
		"/v1/commands",
		bytes.NewReader(payload),
	)
	unauthenticated.Header.Set("Content-Type", "application/json")
	unauthenticatedResponse := httptest.NewRecorder()
	daemon.Handler().ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated command status = %d", unauthenticatedResponse.Code)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/commands",
		bytes.NewReader(payload),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+config.AuthKey)
	response := httptest.NewRecorder()
	daemon.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("command status = %d: %s", response.Code, response.Body.String())
	}
	var result CommandResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Session.Session.State != StateReady ||
		result.Replay != ReplayNew ||
		result.Event.Correlation.ComputerEventID !=
			envelope.Correlation.ComputerEventID {
		t.Fatalf("command result = %+v", result)
	}

	replayRequest := httptest.NewRequest(
		http.MethodPost,
		"/v1/commands",
		bytes.NewReader(payload),
	)
	replayRequest.Header.Set("Content-Type", "application/json")
	replayRequest.Header.Set("Authorization", "Bearer "+config.AuthKey)
	replayResponse := httptest.NewRecorder()
	daemon.Handler().ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("replay status = %d: %s", replayResponse.Code, replayResponse.Body.String())
	}
	if err := json.Unmarshal(replayResponse.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Replay != ReplayExact {
		t.Fatalf("API replay = %+v", result)
	}
}

func TestAuthenticationFailuresAreRateLimited(t *testing.T) {
	config := daemonTestConfig(t, ModePersonal)
	config.BrowserContainment = BrowserUnavailable
	daemon, err := NewDesktopDaemon(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 20; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
		request.RemoteAddr = "192.0.2.4:4040"
		request.Header.Set("Authorization", "Bearer invalid")
		response := httptest.NewRecorder()
		daemon.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("authentication attempt %d status = %d", attempt, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
	request.RemoteAddr = "192.0.2.4:4040"
	request.Header.Set("Authorization", "Bearer "+config.AuthKey)
	response := httptest.NewRecorder()
	daemon.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests ||
		response.Header().Get("Retry-After") != "60" {
		t.Fatalf("rate-limited status = %d, headers = %v", response.Code, response.Header())
	}
}

func TestReplaceDesktopRecoversLiveButFailingControl(t *testing.T) {
	config := daemonTestConfig(t, ModePersonal)
	config.BrowserContainment = BrowserUnavailable
	daemon, err := NewDesktopDaemon(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	current := &desktopControlStub{available: true}
	replacement := &desktopControlStub{available: true}
	daemon.mu.Lock()
	daemon.desktop = current
	daemon.alive["cua_driver"] = true
	daemon.ready = true
	daemon.runContext = context.Background()
	daemon.mu.Unlock()
	daemon.desktopFactory = func(context.Context) (DesktopControl, error) {
		return replacement, nil
	}

	if recovered := daemon.replaceDesktop(current); recovered != replacement {
		t.Fatalf("recovered desktop = %T, want replacement", recovered)
	}
	if !current.closed || current.available {
		t.Fatalf("failed desktop was not closed: %+v", current)
	}
	state := daemon.State()
	if !state.Processes["cua_driver"] || state.LastError != "" {
		t.Fatalf("recovered daemon state = %+v", state)
	}
}

func TestXdotoolKeyChordAllowsOnlyBoundedKeyboardInputs(t *testing.T) {
	tests := []struct {
		input DesktopInput
		want  string
	}{
		{
			input: DesktopInput{
				Kind: DesktopInputKey,
				Key:  "enter",
			},
			want: "Return",
		},
		{
			input: DesktopInput{
				Kind:      DesktopInputKey,
				Key:       "left",
				Modifiers: []string{"ctrl", "shift"},
			},
			want: "ctrl+shift+Left",
		},
		{
			input: DesktopInput{
				Kind: DesktopInputHotkey,
				Keys: []string{"ctrl", "l"},
			},
			want: "ctrl+l",
		},
	}
	for _, test := range tests {
		chord, err := xdotoolKeyChord(test.input)
		if err != nil || chord != test.want {
			t.Fatalf("keyboard chord = %q, %v, want %q", chord, err, test.want)
		}
	}
	if _, err := xdotoolKeyChord(DesktopInput{
		Kind: DesktopInputHotkey,
		Keys: []string{"ctrl", "XF86Launch9"},
	}); err == nil {
		t.Fatal("unapproved X11 key was accepted")
	}
}

func TestKeyboardCapabilityReportsX11Degradation(t *testing.T) {
	config := daemonTestConfig(t, ModePersonal)
	config.BrowserContainment = BrowserUnavailable
	daemon, err := NewDesktopDaemon(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	daemon.desktop = &desktopControlStub{available: true}
	for _, capability := range daemon.capabilities() {
		if capability.Kind != CapabilityKeyboard {
			continue
		}
		if !capability.Available || !capability.Degraded ||
			!strings.Contains(capability.Reason, "X11") {
			t.Fatalf("keyboard capability = %+v", capability)
		}
		return
	}
	t.Fatal("keyboard capability missing")
}

type desktopControlStub struct {
	available bool
	closed    bool
}

func (control *desktopControlStub) Probe(
	context.Context,
) (DesktopControlProbe, error) {
	return DesktopControlProbe{}, nil
}

func (control *desktopControlStub) Frame(
	context.Context,
) (DesktopFrame, error) {
	return DesktopFrame{}, nil
}

func (control *desktopControlStub) Observe(
	context.Context,
	DesktopObservationRequest,
) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (control *desktopControlStub) Input(
	context.Context,
	DesktopInput,
) error {
	return nil
}

func (control *desktopControlStub) WindowInput(
	context.Context,
	DesktopWindowInput,
) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (control *desktopControlStub) Close(context.Context) error {
	control.closed = true
	control.available = false
	return nil
}

func (control *desktopControlStub) Available() bool {
	return control.available
}

func daemonTestConfig(t *testing.T, mode PersistenceMode) DaemonConfig {
	t.Helper()
	root := t.TempDir()
	return DaemonConfig{
		AuthKey:            strings.Repeat("k", 32),
		ListenAddress:      "127.0.0.1:0",
		Display:            ":99",
		Width:              1280,
		Height:             720,
		Mode:               mode,
		Home:               filepath.Join(root, "home"),
		StateRoot:          filepath.Join(root, "state"),
		WorkspaceRoot:      filepath.Join(root, "workspaces"),
		StartURL:           "about:blank",
		BrowserContainment: BrowserSandboxed,
		HostID:             uuid.New(),
		HostVersion:        "ion-computer/0.1.0",
		ImageDigest:        "sha256:" + strings.Repeat("a", 64),
		Budget:             testBudget(),
	}
}
