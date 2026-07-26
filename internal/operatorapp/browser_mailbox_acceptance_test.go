package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	agentmailbox "github.com/paxlabs-inc/ion-agent/internal/mailbox"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type acceptanceMailboxSource struct {
	messages []agentmailbox.Message
}

func (source acceptanceMailboxSource) FetchRecent(
	context.Context,
	int,
) ([]agentmailbox.Message, error) {
	return append([]agentmailbox.Message(nil), source.messages...), nil
}

func TestProductionRuntimeCompletesBrowserMailboxVerificationWorkflow(
	t *testing.T,
) {
	var (
		submitMu sync.Mutex
		email    string
		code     string
		submits  int
	)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/signup":
			_, _ = fmt.Fprint(writer, `<!doctype html><html><head><title>Create agent account</title></head>
				<body><form action="/complete" method="get">
				<input name="email" aria-label="Agent email">
				<input name="verification_code" autocomplete="one-time-code" aria-label="Verification code">
				<button type="submit">Create account</button>
				</form></body></html>`)
		case "/complete":
			submitMu.Lock()
			submits++
			email = request.URL.Query().Get("email")
			code = request.URL.Query().Get("verification_code")
			submitMu.Unlock()
			_, _ = fmt.Fprint(writer, "<html><body>Account request complete</body></html>")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	executable := acceptanceChromium(t)
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	source := acceptanceMailboxSource{messages: []agentmailbox.Message{{
		UID: 71, From: "Accounts <accounts@example.test>",
		Subject:    "Agent account verification",
		ReceivedAt: time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC),
		Raw: []byte("From: accounts@example.test\r\n" +
			"Subject: Agent account verification\r\n" +
			"Content-Type: text/plain\r\n\r\nVerification code: 641209\r\n"),
	}}}
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
		BrowserExecutable:  executable, BrowserAllowPrivateNetwork: true,
		AgentEmailAddress:  "ion-agent@example.test",
		AgentMailboxSource: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actorID := uuid.New()
	sessionID := uuid.New()
	turnID := uuid.New()
	base := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: actorID, SessionID: &sessionID, TurnID: &turnID,
	})
	user := policy.WithPrincipal(base, policy.Principal{
		Sender: policy.SenderUser,
	})
	approved := policy.WithPrincipal(base, policy.Principal{
		Sender: policy.SenderUser, Approved: true,
	})
	execute := func(
		runCtx context.Context,
		id string,
		name string,
		arguments string,
	) (json.RawMessage, error) {
		t.Helper()
		return runtime.capabilityRoot.manager.Execute(
			runCtx,
			protocol.NormalizedToolCall{
				ID: id, Name: name, Arguments: json.RawMessage(arguments),
			},
		)
	}
	executeWithApproval := func(
		id string,
		name string,
		arguments string,
		decision controlplane.ApprovalDecision,
	) (json.RawMessage, error) {
		t.Helper()
		type outcome struct {
			result json.RawMessage
			err    error
		}
		finished := make(chan outcome, 1)
		go func() {
			result, runErr := execute(user, id, name, arguments)
			finished <- outcome{result: result, err: runErr}
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			pending, pendingErr := runtime.approvals.Pending(ctx, actorID)
			if pendingErr != nil {
				t.Fatal(pendingErr)
			}
			for _, request := range pending {
				if request.Operation != name {
					continue
				}
				if _, respondErr := runtime.approvals.Respond(
					ctx, actorID, request.ID, decision,
				); respondErr != nil {
					t.Fatal(respondErr)
				}
				select {
				case found := <-finished:
					return found.result, found.err
				case <-time.After(5 * time.Second):
					t.Fatal("approved tool did not resume")
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("approval for %s was not published", name)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	navigated, err := execute(
		user, "browser-nav", "browser_navigate",
		fmt.Sprintf(`{"url":%q}`, server.URL+"/signup"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var browserSnapshot nativebrowser.Snapshot
	if err := json.Unmarshal(navigated, &browserSnapshot); err != nil {
		t.Fatal(err)
	}
	emailRef := browserElementReference(t, browserSnapshot, "input", "email", "")
	filled, err := execute(
		user, "browser-email", "browser_interact",
		fmt.Sprintf(`{"action":"fill","ref":%q,"value":"ion-agent@example.test"}`, emailRef),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(filled, &browserSnapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(
		user, "mailbox-sync", "agent_mailbox_sync", `{}`,
	); err != nil {
		t.Fatal(err)
	}
	listed, err := execute(
		user, "mailbox-list", "agent_mailbox_list", `{"limit":10}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Verifications []agentmailbox.Metadata `json:"verifications"`
	}
	if err := json.Unmarshal(listed, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Verifications) != 1 ||
		bytes.Contains(listed, []byte("641209")) {
		t.Fatalf("redacted mailbox list = %s", listed)
	}
	codeRef := browserElementReference(t, browserSnapshot, "input", "verification_code", "")
	verificationArguments := fmt.Sprintf(
		`{"verification_id":%q,"expected_domain":%q,"ref":%q}`,
		list.Verifications[0].ID.String(), target.Hostname(), codeRef,
	)
	if _, err := executeWithApproval(
		"apply-verification", "browser_apply_verification",
		verificationArguments, controlplane.DecisionDeny,
	); !errors.Is(err, tools.ErrPolicyDenied) {
		t.Fatalf("denied verification error = %v", err)
	}
	applied, err := execute(
		approved, "apply-verification", "browser_apply_verification",
		verificationArguments,
	)
	if err != nil || bytes.Contains(applied, []byte("641209")) {
		t.Fatalf("private verification result = %s, %v", applied, err)
	}
	if err := json.Unmarshal(applied, &browserSnapshot); err != nil {
		t.Fatal(err)
	}
	submitRef := browserElementReference(t, browserSnapshot, "button", "", "Create account")
	submitArguments := fmt.Sprintf(`{"ref":%q}`, submitRef)
	result, err := executeWithApproval(
		"submit-account", "browser_submit", submitArguments,
		controlplane.DecisionApprove,
	)
	if err != nil || !bytes.Contains(result, []byte("Account request complete")) {
		t.Fatalf("approved account submit = %s, %v", result, err)
	}
	replayed, err := execute(
		approved, "submit-account", "browser_submit", submitArguments,
	)
	if err != nil || string(replayed) != string(result) {
		t.Fatalf("idempotent browser replay = %s, %v", replayed, err)
	}
	submitMu.Lock()
	gotEmail, gotCode, gotSubmits := email, code, submits
	submitMu.Unlock()
	if gotEmail != "ion-agent@example.test" ||
		gotCode != "641209" || gotSubmits != 1 {
		t.Fatalf(
			"submitted email=%q code=%q count=%d",
			gotEmail, gotCode, gotSubmits,
		)
	}
	state, err := os.ReadFile(filepath.Join(
		dataDirectory, "mailbox", "state.enc",
	))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(state, []byte("641209")) ||
		bytes.Contains(state, []byte("Agent account verification")) {
		t.Fatal("encrypted mailbox state contains plaintext")
	}
}

func acceptanceChromium(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(
		os.Getenv("ION_BROWSER_EXECUTABLE"),
	); configured != "" {
		return configured
	}
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(
		home, ".cache", "ms-playwright", "chromium-*", "chrome-linux64", "chrome",
	))
	if len(matches) == 0 {
		t.Skip("Chromium is unavailable for native browser acceptance")
	}
	return matches[len(matches)-1]
}

func browserElementReference(
	t *testing.T,
	snapshot nativebrowser.Snapshot,
	tag string,
	name string,
	text string,
) string {
	t.Helper()
	for _, element := range snapshot.Elements {
		if (tag == "" || element.Tag == tag) &&
			(name == "" || element.Name == name) &&
			(text == "" || element.Text == text) {
			return element.Ref
		}
	}
	t.Fatalf(
		"browser element tag=%q name=%q text=%q not found in %+v",
		tag,
		name,
		text,
		snapshot.Elements,
	)
	return ""
}
