package operatorapp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

func TestPreferredNameFirstContactRestartContextAndReasoningEnforcement(
	t *testing.T,
) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	var requestMu sync.Mutex
	var providerRequests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		requestMu.Lock()
		providerRequests = append(
			providerRequests, append([]byte(nil), body...),
		)
		requestMu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"id":"preferred-name","model":"identity-test",
			"choices":[{"message":{
				"content":"Hello, Andrew.",
				"reasoning_content":"The user is simply saying hello."
			},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer server.Close()
	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
		ProviderName:       "openai-test", ProviderBaseURL: server.URL,
		ProviderAPIKey: "test-only", ProviderModel: "identity-test",
		ProviderHTTPClient: server.Client(),
	}
	first, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	sessionID := createAcceptanceSession(
		t, ctx, first, actor, "identity-first-session",
	)
	submitAcceptanceTurnRaw(
		t, ctx, first, actor, sessionID, "hello", "identity-name-prompt",
	)
	messages, err := first.sessions.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(messages[len(messages)-1].Content); got !=
		"Before we begin, what should I call you?" {
		t.Fatalf("first response = %q", got)
	}
	submitAcceptanceTurnRaw(
		t, ctx, first, actor, sessionID,
		"My name is Andrew.", "identity-name-answer",
	)
	messages, err = first.sessions.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(messages[len(messages)-1].Content); got !=
		"Thanks, Andrew. What should we work on?" {
		t.Fatalf("name acknowledgement = %q", got)
	}
	requestMu.Lock()
	if len(providerRequests) != 0 {
		t.Fatalf("provider ran before name capture: %d requests", len(providerRequests))
	}
	requestMu.Unlock()
	if name, exists := first.capabilityRoot.living.PreferredName(
		ctx, actor,
	); !exists || name != "Andrew" {
		t.Fatalf("preferred name = %q, %v", name, exists)
	}
	otherActor := uuid.New()
	otherSession := createAcceptanceSession(
		t, ctx, first, otherActor, "identity-isolated-session",
	)
	submitAcceptanceTurnRaw(
		t, ctx, first, otherActor, otherSession,
		"hello", "identity-isolated-prompt",
	)
	if _, exists := first.capabilityRoot.living.PreferredName(
		ctx, otherActor,
	); exists {
		t.Fatal("preferred name crossed actor scope")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedSession := createAcceptanceSession(
		t, ctx, restarted, actor, "identity-restarted-session",
	)
	submitAcceptanceTurnRaw(
		t, ctx, restarted, actor, restartedSession,
		"hello", "identity-restarted-turn",
	)
	requestMu.Lock()
	if len(providerRequests) != 1 {
		requestMu.Unlock()
		t.Fatalf("provider requests after restart = %d, want 1", len(providerRequests))
	}
	providerRequest := append([]byte(nil), providerRequests[0]...)
	requestMu.Unlock()
	for _, expected := range [][]byte{
		[]byte("Preferred name: Andrew."),
		[]byte("Refer to Andrew by name"),
		[]byte("Never call Andrew"),
	} {
		if !bytes.Contains(providerRequest, expected) {
			t.Fatalf("provider context missing %q: %s", expected, providerRequest)
		}
	}
	replay, err := restarted.journal.ReplayActor(ctx, actor, 0, 512)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range replay.Events {
		if event.Type == controlplane.EventReasoningSummary {
			t.Fatalf("raw provider reasoning event escaped: %+v", event)
		}
	}
	database, err := os.ReadFile(filepath.Join(dataDirectory, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte(`"preferred_name":"Andrew"`)) {
		t.Fatal("actor identity was stored as plaintext")
	}
}
