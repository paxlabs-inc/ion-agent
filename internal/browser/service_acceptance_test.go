package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	sessionstore "github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

func TestNativeBrowserCompletesPolicySeparatedWorkflow(t *testing.T) {
	var submitted string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/":
			_, _ = fmt.Fprint(writer, `<!doctype html><html><head><title>Agent signup</title></head>
				<body>
				<form action="/done" method="get">
					<label>Email <input name="email" placeholder="agent email"
						oninput="document.getElementById('input-state').textContent=this.value==='agent@example.test'?'registered':'missing'"></label>
					<span id="input-state">missing</span>
					<label>Code <input name="code" autocomplete="one-time-code"></label>
					<button type="button" onclick="document.body.dataset.checked='yes'">Check name</button>
					<button type="submit">Create account</button>
				</form>
				</body></html>`)
		case "/done":
			submitted = request.URL.Query().Get("email")
			_, _ = fmt.Fprint(writer, "<html><body>Account request received</body></html>")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	service, err := New(Config{
		ExecutablePath: findAcceptanceChromium(t),
		ProfileRoot:    t.TempDir(), AllowPrivateNetwork: true,
		DisableSandbox: os.Geteuid() == 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	sessionID := uuid.New()
	ctx := controlplane.WithApprovalScope(
		context.Background(),
		controlplane.ApprovalScope{
			ActorID: uuid.New(), SessionID: &sessionID,
		},
	)
	snapshot, err := service.Navigate(ctx, server.URL+"?token=must-not-leak")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Title != "Agent signup" || len(snapshot.Elements) != 4 {
		t.Fatalf("unexpected signup snapshot: %+v", snapshot)
	}
	if strings.Contains(snapshot.URL, "token=") {
		t.Fatalf("snapshot leaked URL query: %q", snapshot.URL)
	}
	emailRef := ""
	for _, element := range snapshot.Elements {
		if element.Placeholder == "agent email" {
			emailRef = element.Ref
			break
		}
	}
	if emailRef == "" {
		t.Fatal("email field reference is missing")
	}
	snapshot, err = service.Interact(ctx, "fill", emailRef, "agent@example.test")
	if err != nil || !strings.Contains(snapshot.Text, "registered") {
		t.Fatalf("fill = %+v, %v", snapshot, err)
	}
	codeRef := ""
	for _, element := range snapshot.Elements {
		if element.Name == "code" {
			codeRef = element.Ref
			break
		}
	}
	if codeRef == "" {
		t.Fatal("verification field reference is missing")
	}
	submitRef := elementRef(t, snapshot, "Create account")
	if _, err := service.Interact(ctx, "fill", codeRef, "123456"); err == nil ||
		!strings.Contains(err.Error(), "sensitive field") {
		t.Fatalf("sensitive fill error = %v", err)
	}
	if _, err := service.Interact(ctx, "click", submitRef, ""); err == nil ||
		!strings.Contains(err.Error(), "browser_submit") {
		t.Fatalf("consequential click error = %v", err)
	}
	snapshot, err = service.Submit(ctx, submitRef)
	if err != nil {
		t.Fatal(err)
	}
	if submitted != "agent@example.test" ||
		!strings.Contains(snapshot.Text, "Account request received") {
		t.Fatalf("submit = %q, %+v", submitted, snapshot)
	}
}

func TestBrowserBlocksPrivateDestinationInProductionMode(t *testing.T) {
	service, err := New(Config{
		ExecutablePath: findAcceptanceChromium(t),
		ProfileRoot:    t.TempDir(),
		DisableSandbox: os.Geteuid() == 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := controlplane.WithApprovalScope(
		context.Background(),
		controlplane.ApprovalScope{ActorID: uuid.New()},
	)
	if _, err := service.Navigate(ctx, "http://127.0.0.1:8080"); err == nil ||
		!strings.Contains(err.Error(), "private or local") {
		t.Fatalf("private navigation error = %v", err)
	}
}

func TestBrowserTakeoverLeaseBlocksAutomationAndDrivesTheSameBrowser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = fmt.Fprint(writer, "<html><head><title>Controlled browser</title></head><body>lease-bound page</body></html>")
	}))
	defer server.Close()
	ctx := context.Background()
	cipher, err := vault.New(bytes.Repeat([]byte{0x62}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := sessionstore.Open(
		ctx, filepath.Join(t.TempDir(), "sessions.db"), cipher,
		types.SystemClock{}, 128<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	defer cipher.Close()
	control, err := controllease.New(store, types.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Config{
		ExecutablePath: findAcceptanceChromium(t),
		ProfileRoot:    t.TempDir(), AllowPrivateNetwork: true, Control: control,
		DisableSandbox: os.Geteuid() == 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	actorID, sessionID, turnID := uuid.New(), uuid.New(), uuid.New()
	runCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: actorID, SessionID: &sessionID, TurnID: &turnID,
		AgentID: "ion",
	})
	target := ControlTarget(actorID, &sessionID)
	owner := controllease.Owner{
		TurnID: &turnID, TaskID: &turnID, AgentID: "ion",
		Action: "browser_navigate", Revision: 9,
	}
	lease, err := control.Acquire(
		ctx, target, owner, 0, controllease.MinimumLeaseTTL,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Navigate(runCtx, server.URL); !errors.Is(
		err, controllease.ErrHeld,
	) {
		t.Fatalf("automation during browser takeover error = %v", err)
	}
	snapshot, err := service.NavigateWithLease(
		runCtx, lease.ID, lease.Revision, server.URL,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Title != "Controlled browser" {
		t.Fatalf("controlled snapshot = %+v", snapshot)
	}
	lease, err = control.Release(
		ctx, target, lease.ID, lease.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Authority != controllease.AuthorityExecutor {
		t.Fatalf("released lease = %+v", lease)
	}
	if _, err := service.Observe(runCtx); err != nil {
		t.Fatalf("automation after release = %v", err)
	}
}

func TestPreviewInspectionCapturesMobileDarkEvidenceAndBlocksOriginEscape(t *testing.T) {
	var escaped atomic.Bool
	privateTarget := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		escaped.Store(true)
		_, _ = writer.Write([]byte("should not load"))
	}))
	defer privateTarget.Close()
	preview := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/missing" {
			http.NotFound(writer, request)
			return
		}
		_, _ = fmt.Fprintf(writer, `<!doctype html><html><head><title>Preview matrix</title></head><body>
			<main>mobile dark preview</main><button></button><script>
			console.error('preview-console-correlation'); fetch('/missing'); fetch(%q).catch(()=>{});
			</script></body></html>`, privateTarget.URL)
	}))
	defer preview.Close()
	service, err := New(Config{
		ExecutablePath: findAcceptanceChromium(t),
		ProfileRoot:    t.TempDir(), AllowPrivateNetwork: true,
		DisableSandbox: os.Geteuid() == 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := controlplane.WithApprovalScope(context.Background(), controlplane.ApprovalScope{ActorID: uuid.New()})
	inspection, err := service.InspectPreview(ctx, preview.URL, 390, 844, true)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Width != 390 || inspection.Height != 844 || !inspection.DarkMode ||
		!strings.HasPrefix(inspection.ScreenshotPNG, "data:image/png;base64,") || len(inspection.ScreenshotPNG) < 1000 ||
		inspection.Snapshot.Title != "Preview matrix" || len(inspection.Accessibility) == 0 {
		t.Fatalf("preview inspection = %+v", inspection)
	}
	foundConsole, foundNetwork := false, false
	for _, diagnostic := range inspection.Diagnostics {
		foundConsole = foundConsole || (diagnostic.Source == "console" && strings.Contains(diagnostic.Message, "preview-console-correlation"))
		foundNetwork = foundNetwork || diagnostic.Source == "network"
	}
	if !foundConsole || !foundNetwork || escaped.Load() {
		t.Fatalf("diagnostics console=%v network=%v escaped=%v: %+v", foundConsole, foundNetwork, escaped.Load(), inspection.Diagnostics)
	}
}

func findAcceptanceChromium(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("ION_BROWSER_EXECUTABLE"); configured != "" {
		return configured
	}
	found, err := findExecutable("")
	if err != nil {
		t.Skipf("Chromium unavailable: %v", err)
	}
	return found
}
