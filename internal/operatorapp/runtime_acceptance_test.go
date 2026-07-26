package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

type captureGenerator struct {
	request protocol.GenerationRequest
}

type acceptanceClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *acceptanceClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *acceptanceClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func (generator *captureGenerator) Generate(
	_ context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	generator.request = request
	return protocol.NormalizedGeneration{
		Content: "done", FinishReason: protocol.FinishStop,
	}, nil
}

func TestRuntimeRestartPreservesActorReplayIdempotencyAndEncryptedSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	directory := t.TempDir()
	source, err := vault.NewFileKEKSource(filepath.Join(directory, "development.kek"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := vault.NewFileWrappedKeyStore(filepath.Join(directory, "user-key.enc"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := vault.Initialize(ctx, source, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	actorID := uuid.New()
	first, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationSessionCreate,
		Scope:           controlplane.Scope{ActorID: actorID},
		IdempotencyKey:  "restart-create-session",
		Payload:         json.RawMessage(`{}`),
	}
	created := first.dispatcher.Dispatch(ctx, actorID, create)
	if created.Error != nil {
		t.Fatalf("create response = %+v", created)
	}
	var sessionView struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(created.Result, &sessionView); err != nil {
		t.Fatal(err)
	}
	if sessionView.ID == uuid.Nil {
		t.Fatal("session create returned no ID")
	}
	before, err := first.journal.ReplayActor(ctx, actorID, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Events) == 0 {
		t.Fatal("create command produced no durable audit event")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: directory, DevelopmentFileKEK: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	after, err := restarted.journal.ReplayActor(ctx, actorID, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("replay changed across restart:\nbefore=%+v\nafter=%+v", before, after)
	}

	retry := create
	retry.RequestID = uuid.New()
	cached := restarted.dispatcher.Dispatch(ctx, actorID, retry)
	if cached.Error != nil || cached.Revision != created.Revision ||
		string(cached.Result) != string(created.Result) {
		t.Fatalf("restart idempotency response = %+v, want %+v", cached, created)
	}
	afterRetry, err := restarted.journal.ReplayActor(ctx, actorID, 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, afterRetry) {
		t.Fatal("idempotent restart retry appended or changed durable events")
	}

	resume := controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationSessionResume,
		Scope: controlplane.Scope{
			ActorID: actorID, SessionID: &sessionView.ID,
		},
		IdempotencyKey: "restart-resume-session",
		Payload:        json.RawMessage(`{}`),
	}
	resumed := restarted.dispatcher.Dispatch(ctx, actorID, resume)
	if resumed.Error != nil || !json.Valid(resumed.Result) {
		t.Fatalf("encrypted session did not resume after restart: %+v", resumed)
	}
}

func TestProductionRuntimeSharesNonEmptyPolicyBoundCapabilitySurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	workspace := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)

	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	actorID := uuid.New()
	response := runtime.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationToolSurface,
		Scope:           controlplane.Scope{ActorID: actorID},
		Payload:         json.RawMessage(`{}`),
	})
	if response.Error != nil {
		t.Fatalf("tool.surface response = %+v", response)
	}
	var surface []struct {
		Name  string `json:"name"`
		Ready bool   `json:"ready"`
	}
	if err := json.Unmarshal(response.Result, &surface); err != nil {
		t.Fatal(err)
	}
	if len(surface) == 0 {
		t.Fatal("production tool surface is empty")
	}
	foundWrite := false
	for _, tool := range surface {
		if tool.Name == "filesystem_write" && tool.Ready {
			foundWrite = true
		}
	}
	if !foundWrite {
		t.Fatalf("filesystem_write missing from production surface: %+v", surface)
	}
	readinessResponse := runtime.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationToolReadiness,
		Scope:           controlplane.Scope{ActorID: actorID},
		Payload:         json.RawMessage(`{}`),
	})
	if readinessResponse.Error != nil {
		t.Fatalf("tool.readiness response = %+v", readinessResponse)
	}
	var readiness struct {
		Ready       int `json:"ready"`
		Unavailable int `json:"unavailable"`
		Tools       []struct {
			Name   string `json:"name"`
			Ready  bool   `json:"ready"`
			Reason string `json:"reason"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(readinessResponse.Result, &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.Ready != len(surface) || readiness.Unavailable == 0 {
		t.Fatalf("surface/readiness disagree: surface=%d readiness=%+v", len(surface), readiness)
	}
	nativeBrowserFound := false
	for _, status := range readiness.Tools {
		if status.Name == "browser_automation" {
			t.Fatalf("legacy MCP browser placeholder remains registered: %+v", status)
		}
		if status.Name == "browser_navigate" {
			nativeBrowserFound = true
			if !status.Ready && status.Reason == "" {
				t.Fatalf("native browser unavailability was not explained: %+v", status)
			}
		}
	}
	if !nativeBrowserFound {
		t.Fatalf("native browser tools missing from readiness: %+v", readiness.Tools)
	}

	result, err := runtime.capabilityRoot.manager.Execute(
		ctx,
		protocol.NormalizedToolCall{
			ID: "acceptance-write", Name: "filesystem_write",
			Arguments: json.RawMessage(`{"path":"created.txt","content":"production capability"}`),
		},
	)
	if err != nil {
		t.Fatalf("filesystem_write failed through production manager: %v", err)
	}
	if !json.Valid(result) {
		t.Fatalf("filesystem_write returned invalid JSON: %s", result)
	}
	content, err := os.ReadFile(filepath.Join(workspace, "created.txt"))
	if err != nil || string(content) != "production capability" {
		t.Fatalf("workspace effect = %q, %v", content, err)
	}
	audit, err := os.ReadFile(filepath.Join(dataDirectory, "policy", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(audit), `"tool_name":"filesystem_write"`) ||
		!strings.Contains(string(audit), `"decision":"ALLOW"`) {
		t.Fatalf("YELLOW call was not durably audited: %s", audit)
	}
	if strings.Contains(string(audit), "production capability") {
		t.Fatalf("policy audit leaked file content: %s", audit)
	}
}

func TestRuntimeDefaultsPublicResearchToBrowsingMachineSearXNG(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.config.SearchEndpoint != defaultSearchEndpoint {
		t.Fatalf(
			"default search endpoint = %q, want %q",
			runtime.config.SearchEndpoint, defaultSearchEndpoint,
		)
	}
	if runtime.config.SearchEndpoint != "https://browsingmachine.com/" {
		t.Fatalf(
			"public research default = %q",
			runtime.config.SearchEndpoint,
		)
	}
	if runtime.config.TavilySearchEndpoint != defaultTavilySearchEndpoint {
		t.Fatalf(
			"default Tavily endpoint = %q, want %q",
			runtime.config.TavilySearchEndpoint, defaultTavilySearchEndpoint,
		)
	}
	for _, readiness := range runtime.capabilityRoot.manager.Readiness(ctx) {
		if readiness.Name == "web_search" {
			if !readiness.Ready {
				t.Fatalf("default web_search is unavailable: %+v", readiness)
			}
			return
		}
	}
	t.Fatal("web_search is missing from the production surface")
}

func TestProductionRuntimeUsesOnePolicyBoundPathForConsequentialTools(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	actorID := uuid.New()
	surfaceResponse := runtime.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationToolSurface,
		Scope:           controlplane.Scope{ActorID: actorID},
		Payload:         json.RawMessage(`{}`),
	})
	if surfaceResponse.Error != nil {
		t.Fatalf("tool surface = %+v", surfaceResponse)
	}
	if bytes.Contains(surfaceResponse.Result, []byte("core_execute")) {
		t.Fatalf("removed private gateway remains visible: %s", surfaceResponse.Result)
	}

	executions := 0
	const toolName = "acceptance_publish"
	if err := runtime.capabilityRoot.manager.Register(ctx, tools.Registration{
		Name: toolName, Description: "Exercise the production RED path.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationRed,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			executions++
			return json.RawMessage(`{"published":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	call := protocol.NormalizedToolCall{
		ID: "consequential-normal-path", Name: toolName,
		Arguments: json.RawMessage(`{}`),
	}
	unapproved := policy.WithPrincipal(ctx, policy.Principal{
		Sender: policy.SenderUser,
	})
	if _, err := runtime.capabilityRoot.manager.Execute(
		unapproved, call,
	); !errors.Is(err, tools.ErrPolicyDenied) ||
		!errors.Is(err, policy.ErrDenied) {
		t.Fatalf("unapproved RED call error = %v", err)
	}
	if executions != 0 {
		t.Fatalf("unapproved RED handler executions = %d", executions)
	}
	approved := policy.WithPrincipal(ctx, policy.Principal{
		Sender: policy.SenderUser, Approved: true,
	})
	result, err := runtime.capabilityRoot.manager.Execute(approved, call)
	if err != nil || !bytes.Contains(result, []byte(`"published":true`)) {
		t.Fatalf("approved normal-path result = %s, %v", result, err)
	}
	replayed, err := runtime.capabilityRoot.manager.Execute(approved, call)
	if err != nil || string(replayed) != string(result) {
		t.Fatalf("approved idempotent replay = %s, %v", replayed, err)
	}
	if executions != 1 {
		t.Fatalf("approved RED handler executions = %d, want 1", executions)
	}

	evidence := runtime.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationPolicyEvents,
		Scope:           controlplane.Scope{ActorID: actorID},
		Payload:         json.RawMessage(`{}`),
	})
	if evidence.Error != nil {
		t.Fatalf("policy evidence = %+v", evidence)
	}
	var events []policy.AuditEvent
	if err := json.Unmarshal(evidence.Result, &events); err != nil {
		t.Fatal(err)
	}
	denied, allowed := false, false
	for _, event := range events {
		if event.ToolName != toolName {
			continue
		}
		denied = denied || event.Decision == policy.Deny
		allowed = allowed || event.Decision == policy.Allow
	}
	if !denied || !allowed {
		t.Fatalf("operator evidence omitted RED decisions: %+v", events)
	}
}

func TestLiveEncryptedOperatorTurnUsesFilesystemEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	workspace := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	if err := os.WriteFile(
		filepath.Join(workspace, "evidence.txt"),
		[]byte("live production evidence"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := 0
	firstRequestHadTool := false
	firstRequestHadExpectSchema := false
	firstRequestHadSelfModel := false
	secondRequestHadEvidence := false
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		if bytes.Contains(payload, []byte("Extract only load-bearing factual premises")) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{
				"id":"premise-1","model":"test-model",
				"choices":[{"message":{"content":"{\"premises\":[\"evidence.txt exists in the workspace\"]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`)
			return
		}
		mu.Lock()
		requests++
		current := requests
		if current == 1 {
			firstRequestHadTool = bytes.Contains(payload, []byte(`"name":"filesystem_read"`))
			firstRequestHadExpectSchema = bytes.Contains(
				payload,
				[]byte(`"expect"`),
			)
			firstRequestHadSelfModel = bytes.Contains(
				payload,
				[]byte("## Observed self-model"),
			)
		} else {
			secondRequestHadEvidence = bytes.Contains(payload, []byte("live production evidence"))
		}
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if current == 1 {
			_, _ = io.WriteString(writer, `{
				"id":"turn-1","model":"test-model",
				"choices":[{
					"message":{"content":"","tool_calls":[{
						"id":"read-evidence","type":"function",
						"function":{"name":"filesystem_read","arguments":"{\"path\":\"evidence.txt\",\"expect\":\"return non-empty file content\"}"}
					}]},
					"finish_reason":"tool_calls"
				}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`)
			return
		}
		_, _ = io.WriteString(writer, `{
			"id":"turn-2","model":"test-model",
			"choices":[{"message":{"content":"Verified live production evidence."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer server.Close()

	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
		ProviderName:       "openai-test", ProviderBaseURL: server.URL,
		ProviderAPIKey: "test-only", ProviderModel: "test-model",
		ProviderHTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	actorID := uuid.New()
	created := runtime.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationSessionCreate,
		Scope:           controlplane.Scope{ActorID: actorID},
		IdempotencyKey:  "live-tool-session",
		Payload:         json.RawMessage(`{}`),
	})
	if created.Error != nil {
		t.Fatalf("session create = %+v", created)
	}
	var sessionView struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(created.Result, &sessionView); err != nil {
		t.Fatal(err)
	}
	onboardAcceptanceActor(
		t, ctx, runtime, actorID, sessionView.ID, "live-tool-onboarding",
	)
	beforeMessages, err := runtime.sessions.ListMessages(ctx, sessionView.ID)
	if err != nil {
		t.Fatal(err)
	}
	submitted := runtime.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationTurnSubmit,
		Scope: controlplane.Scope{
			ActorID: actorID, SessionID: &sessionView.ID,
		},
		IdempotencyKey: "live-tool-turn",
		Payload:        json.RawMessage(`{"content":"Read evidence.txt and report its contents."}`),
	})
	if submitted.Error != nil {
		t.Fatalf("turn submit = %+v", submitted)
	}
	var turnView struct {
		TurnID uuid.UUID `json:"turn_id"`
	}
	if err := json.Unmarshal(submitted.Result, &turnView); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	var messages []session.Message
	for time.Now().Before(deadline) {
		messages, err = runtime.sessions.ListMessages(ctx, sessionView.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) >= len(beforeMessages)+2 &&
			messages[len(messages)-1].Role == session.RoleAssistant {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(messages) < 2 ||
		!strings.Contains(string(messages[len(messages)-1].Content), "Verified live production evidence") {
		t.Fatalf("live turn did not persist final answer: %+v", messages)
	}
	mu.Lock()
	gotRequests := requests
	gotTool := firstRequestHadTool
	gotExpectSchema := firstRequestHadExpectSchema
	gotSelfModel := firstRequestHadSelfModel
	gotEvidence := secondRequestHadEvidence
	mu.Unlock()
	if gotRequests != 2 || !gotTool || !gotExpectSchema ||
		!gotSelfModel || !gotEvidence {
		t.Fatalf(
			"provider flow requests=%d tool=%v expect=%v self_model=%v evidence=%v",
			gotRequests, gotTool, gotExpectSchema, gotSelfModel, gotEvidence,
		)
	}
	var replay controlplane.Replay
	var requestedEvents int
	var requestedSequence, completedSequence uint64
	var requestedComputer, completedComputer controlplane.ComputerEventPayload
	var toolTerminalEvents int
	for time.Now().Before(deadline) {
		replay, err = runtime.journal.ReplayActor(ctx, actorID, 0, 256)
		if err != nil {
			t.Fatal(err)
		}
		requestedEvents = 0
		requestedSequence = 0
		completedSequence = 0
		toolTerminalEvents = 0
		for _, event := range replay.Events {
			if event.Correlation.TurnID == nil ||
				*event.Correlation.TurnID != turnView.TurnID {
				continue
			}
			switch event.Type {
			case controlplane.EventToolRequested:
				requestedEvents++
				requestedSequence = event.Sequence
				if err := json.Unmarshal(event.Payload, &requestedComputer); err != nil {
					t.Fatal(err)
				}
			case controlplane.EventToolCompleted:
				toolTerminalEvents++
				if err := json.Unmarshal(event.Payload, &completedComputer); err != nil {
					t.Fatal(err)
				}
			case controlplane.EventToolFailed, controlplane.EventToolDenied,
				controlplane.EventToolInterrupted,
				controlplane.EventToolOutcomeUnknown:
				toolTerminalEvents++
			case controlplane.EventTurnCompleted:
				completedSequence = event.Sequence
			}
		}
		if completedSequence != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requestedEvents != 1 || requestedSequence == 0 ||
		completedSequence == 0 || requestedSequence >= completedSequence ||
		toolTerminalEvents != 1 {
		t.Fatalf("tool lifecycle was not streamed once before completion: %+v", replay.Events)
	}
	var committed *protocol.ToolEvent
	for _, id := range runtime.capabilityRoot.memory.ListByType(memory.Event) {
		if found, ok := runtime.capabilityRoot.memory.GetToolEvent(id); ok {
			committed = found
			break
		}
	}
	if committed == nil || committed.Name != "filesystem_read" ||
		committed.MMRRootAtTime == [32]byte{} {
		t.Fatalf("tool evidence was not committed to encrypted Cortex: %+v", committed)
	}
	if requestedComputer.ToolEventID != committed.ID ||
		completedComputer.ToolEventID != committed.ID ||
		requestedComputer.ProviderCallID == "" ||
		requestedComputer.ProviderCallID != completedComputer.ProviderCallID ||
		completedComputer.Phase != controlplane.ComputerCompleted ||
		completedComputer.TerminalStatus != controlplane.ComputerCompleted ||
		completedComputer.Result == nil ||
		!completedComputer.Result.Available {
		t.Fatalf(
			"computer lifecycle was not bound to authoritative evidence: requested=%+v completed=%+v evidence=%+v",
			requestedComputer, completedComputer, committed,
		)
	}
	displayModel, compatibility, err := controlplane.ResolveDisplayModel(
		completedComputer.DisplayModel,
		len(completedComputer.SourceReferences),
	)
	if err != nil || compatibility != controlplane.DisplayCurrent ||
		displayModel.Kind != controlplane.DisplayCode ||
		len(displayModel.Blocks) != 1 ||
		displayModel.Blocks[0].Content == nil ||
		displayModel.Blocks[0].Content.Truth != controlplane.DisplayObserved {
		t.Fatalf(
			"production tool result did not produce a validated code display: model=%+v compatibility=%q err=%v",
			displayModel, compatibility, err,
		)
	}
	self := runtime.capabilityRoot.selfModel.Snapshot()
	if len(self.Capabilities) != 1 ||
		self.Capabilities[0].Name != "filesystem_read" ||
		len(self.Capabilities[0].VerifiedBy) != 1 ||
		self.Capabilities[0].VerifiedBy[0] != committed.ID {
		t.Fatalf("committed evidence did not evolve self-model: %+v", self)
	}
	verified, err := runtime.capabilityRoot.memory.VerifyCitation(
		ctx,
		protocol.Citation{
			ToolEventID: committed.ID, MMRLeafHash: committed.MMRLeafHash,
			MMRRootAtTime: committed.MMRRootAtTime, Verified: true,
		},
		*committed,
	)
	if err != nil || !verified {
		t.Fatalf("tool evidence citation did not verify: %v, %v", verified, err)
	}
	database, err := os.ReadFile(filepath.Join(dataDirectory, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte("Verified live production evidence")) ||
		bytes.Contains(database, []byte("Read evidence.txt")) {
		t.Fatal("encrypted session database exposed transcript plaintext")
	}
}

func initializeRuntimeVault(t *testing.T, ctx context.Context, directory string) {
	t.Helper()
	source, err := vault.NewFileKEKSource(filepath.Join(directory, "development.kek"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := vault.NewFileWrappedKeyStore(filepath.Join(directory, "user-key.enc"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := vault.Initialize(ctx, source, keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionMemoryToolsIsolateDashboardAndTelegramActors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	dashboard := uuid.New()
	telegramOne := uuid.New()
	telegramTwo := uuid.New()
	scoped := func(actor uuid.UUID) context.Context {
		return controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
			ActorID: actor,
		})
	}
	execute := func(
		actor uuid.UUID,
		id string,
		name string,
		arguments string,
	) (json.RawMessage, error) {
		t.Helper()
		return runtime.capabilityRoot.manager.Execute(
			scoped(actor),
			protocol.NormalizedToolCall{
				ID: id, Name: name, Arguments: json.RawMessage(arguments),
			},
		)
	}
	saved, err := execute(
		telegramOne, "save-one", "memory_save",
		`{"type":"0x02","content":"telegram-one private memory"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		ID uuid.UUID `json:"id"`
	}
	if json.Unmarshal(saved, &first) != nil || first.ID == uuid.Nil {
		t.Fatalf("saved memory = %s", saved)
	}
	for label, actor := range map[string]uuid.UUID{
		"dashboard": dashboard, "telegram-two": telegramTwo,
	} {
		search, err := execute(
			actor, "search-"+label, "memory_search",
			`{"query":"telegram-one private"}`,
		)
		if err != nil || strings.Contains(string(search), first.ID.String()) ||
			strings.Contains(string(search), "telegram-one private") {
			t.Fatalf("%s cross-actor search = %s, %v", label, search, err)
		}
		if recalled, err := execute(
			actor, "recall-"+label, "memory_recall",
			fmt.Sprintf(`{"id":%q}`, first.ID.String()),
		); err == nil || len(recalled) != 0 {
			t.Fatalf("%s cross-actor recall = %s, %v", label, recalled, err)
		}
	}
	own, err := execute(
		telegramOne, "recall-own", "memory_recall",
		fmt.Sprintf(`{"id":%q}`, first.ID.String()),
	)
	if err != nil || !strings.Contains(string(own), "telegram-one private memory") {
		t.Fatalf("owner recall = %s, %v", own, err)
	}
	resolved, err := runtime.capabilityRoot.memory.Resolve(first.ID)
	if err != nil || resolved.Head.Actor != telegramOne.String() {
		t.Fatalf("durable memory owner = %+v, %v", resolved, err)
	}
}

func TestCompletedTranscriptRecallsAcrossSessionsWithoutCortexWriteback(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)

	var captureMu sync.Mutex
	var providerBodies [][]byte
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
		writer.Header().Set("Content-Type", "application/json")
		if bytes.Contains(body, []byte("Extract only load-bearing factual premises")) {
			_, _ = io.WriteString(writer, `{
				"id":"premises","model":"recall-test",
				"choices":[{"message":{"content":"{\"premises\":[]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`)
			return
		}
		captureMu.Lock()
		providerBodies = append(providerBodies, append([]byte(nil), body...))
		captureMu.Unlock()
		content := "I can use the encrypted transcript activation."
		if bytes.Contains(body, []byte("trace my predecessor lineage")) {
			content = "My prior lineage synthesis identified Cortex, the agent loop, " +
				"and operator surfaces."
		}
		encoded, marshalErr := json.Marshal(map[string]any{
			"id": "recall-turn", "model": "recall-test",
			"choices": []map[string]any{{
				"message":       map[string]string{"content": content},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{
				"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2,
			},
		})
		if marshalErr != nil {
			t.Error(marshalErr)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = writer.Write(encoded)
	}))
	defer server.Close()

	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
		ProviderName:       "openai-test", ProviderBaseURL: server.URL,
		ProviderAPIKey: "test-only", ProviderModel: "recall-test",
		ProviderHTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	actor := uuid.New()
	firstSession := createAcceptanceSession(
		t, ctx, runtime, actor, "recall-first-session",
	)
	submitAcceptanceTurn(
		t, ctx, runtime, actor, firstSession,
		"trace my predecessor lineage", "recall-first-turn",
	)

	search, err := runtime.capabilityRoot.manager.Execute(
		controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
			ActorID: actor,
		}),
		protocol.NormalizedToolCall{
			ID: "recall-cortex-search", Name: "memory_search",
			Arguments: json.RawMessage(`{"query":"prior lineage synthesis"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(search, []byte("prior lineage synthesis")) ||
		bytes.Contains(search, []byte("operator surfaces")) {
		t.Fatalf("completed transcript was silently copied into Cortex: %s", search)
	}

	secondSession := createAcceptanceSession(
		t, ctx, runtime, actor, "recall-second-session",
	)
	secondPrompt := "what can you remember about your predecessor lineage"
	submitAcceptanceTurn(
		t, ctx, runtime, actor, secondSession, secondPrompt, "recall-second-turn",
	)
	secondBody := primaryBodyContaining(
		t, &captureMu, providerBodies, secondPrompt,
	)
	for _, fragment := range []string{
		"Relevant encrypted memory activation",
		"My prior lineage synthesis identified Cortex",
		fmt.Sprintf("session:%s memory:transcript", firstSession),
	} {
		if !bytes.Contains(secondBody, []byte(fragment)) {
			t.Fatalf("cross-session transcript activation missing %q: %s", fragment, secondBody)
		}
	}
}

func TestProductionMemoryTodosAndSkillsSurviveRestartAndInfluenceTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	workspace := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
	}
	first, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	execute := func(name, arguments string) json.RawMessage {
		t.Helper()
		result, executeErr := first.capabilityRoot.manager.Execute(
			ctx, protocol.NormalizedToolCall{
				ID: name, Name: name, Arguments: json.RawMessage(arguments),
			},
		)
		if executeErr != nil {
			t.Fatalf("%s failed: %v", name, executeErr)
		}
		return result
	}
	memoryResult := execute(
		"memory_save",
		`{"type":"0x02","content":"restart-only durable memory","pinned":true}`,
	)
	var savedMemory struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(memoryResult, &savedMemory); err != nil {
		t.Fatal(err)
	}
	execute("todo_add", `{"content":"restart-only durable todo"}`)
	execute("skill_save", `{
		"name":"Restart procedure",
		"trigger":"restart-procedure",
		"steps":["inspect durable state"],
		"pitfalls":["do not assume"],
		"verification":["confirm prompt context"]
	}`)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	journalPayload, err := os.ReadFile(filepath.Join(dataDirectory, "cortex", "journal.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(journalPayload), "restart-only durable memory") {
		t.Fatal("Cortex journal exposed memory plaintext")
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	search, err := restarted.capabilityRoot.manager.Execute(
		ctx, protocol.NormalizedToolCall{
			ID: "memory-search", Name: "memory_search",
			Arguments: json.RawMessage(`{"query":"restart-only"}`),
		},
	)
	if err != nil || !strings.Contains(string(search), savedMemory.ID.String()) {
		t.Fatalf("memory did not survive restart: %s, %v", search, err)
	}
	todos, err := restarted.capabilityRoot.manager.Execute(
		ctx, protocol.NormalizedToolCall{
			ID: "todo-list", Name: "todo_list", Arguments: json.RawMessage(`{}`),
		},
	)
	if err != nil || !strings.Contains(string(todos), "restart-only durable todo") {
		t.Fatalf("todo did not survive restart: %s, %v", todos, err)
	}
	installed, err := restarted.capabilityRoot.skills.List(ctx)
	if err != nil || len(installed) != 1 || installed[0].Name != "Restart procedure" {
		t.Fatalf("skill did not survive restart: %+v, %v", installed, err)
	}
	generator := &captureGenerator{}
	runner := skillAwareTurnRunner{
		generator: generator, manager: restarted.capabilityRoot.manager,
		config: agent.LoopConfig{
			Model: "acceptance", SystemPrompt: "base prompt",
			UserID: "operator", SessionID: uuid.NewString(),
		},
		skills: restarted.capabilityRoot.skills,
	}
	if _, err := runner.Turn(ctx, "please use restart-procedure"); err != nil {
		t.Fatal(err)
	}
	if len(generator.request.Messages) == 0 ||
		!strings.Contains(generator.request.Messages[0].Content, "Restart procedure") {
		t.Fatalf("matched skill did not influence prompt: %+v", generator.request.Messages)
	}

	createdSession, err := restarted.sessions.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	createdActor := uuid.New()
	if err := restarted.capabilityRoot.living.Submitted(
		ctx, createdActor, createdSession.ID, "memory activation acceptance",
	); err != nil {
		t.Fatal(err)
	}
	deps, err := restarted.capabilityRoot.loopDeps(
		ctx, createdSession.ID, generator, "unconfigured", restarted.sessions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if deps.Premises == nil || deps.PremiseExtractor == nil ||
		deps.Predictions == nil || deps.PredictionRecords == nil ||
		deps.TaskGraph == nil || deps.SelfModel == nil || deps.Cassandra == nil ||
		deps.CircuitBreaker == nil || deps.MemoryActivation == nil ||
		deps.Behavioral == nil || deps.ContextComposer == nil {
		t.Fatalf("production cognition was only partially composed: %+v", deps)
	}
	activated, err := deps.MemoryActivation.Activate(
		ctx, "restart-only durable memory", createdActor.String(),
		createdSession.ID.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(activated, "restart-only durable memory") {
		t.Fatalf("durable memory did not enter provider activation: %s", activated)
	}
}

func TestReducedSubAgentSurfaceCannotExpandOrWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDirectory := t.TempDir()
	workspace := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	reduced := newReducedToolManager(
		runtime.capabilityRoot.manager,
		[]string{"filesystem_read", "filesystem_search"},
	)
	for _, definition := range reduced.Surface(ctx) {
		if definition.Name != "filesystem_read" &&
			definition.Name != "filesystem_search" {
			t.Fatalf("reduced surface expanded to %q", definition.Name)
		}
	}
	_, err = reduced.Execute(ctx, protocol.NormalizedToolCall{
		ID: "forbidden-write", Name: "filesystem_write",
		Arguments: json.RawMessage(`{"path":"forbidden.txt","content":"no"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "outside immutable surface") {
		t.Fatalf("reduced manager write error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "forbidden.txt")); !os.IsNotExist(err) {
		t.Fatalf("reduced manager created forbidden file: %v", err)
	}
}

func TestOpenRuntimeIncompleteRestartRestoresCognitionAndDoesNotRepeatTool(
	t *testing.T,
) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	workspace := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)

	var mu sync.Mutex
	normalRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		if bytes.Contains(payload, []byte("Extract only load-bearing factual premises")) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{
				"id":"premise","model":"recovery-model",
				"choices":[{"message":{"content":"{\"premises\":[\"recovery.txt must be written once\"]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`)
			return
		}
		mu.Lock()
		normalRequests++
		current := normalRequests
		mu.Unlock()
		if current == 2 || current == 3 {
			<-request.Context().Done()
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if current == 1 {
			_, _ = io.WriteString(writer, `{
				"id":"tool","model":"recovery-model",
				"choices":[{"message":{"content":"I will persist the recovery marker.","tool_calls":[{
					"id":"write-once","type":"function",
					"function":{"name":"filesystem_write","arguments":"{\"path\":\"recovery.txt\",\"content\":\"single completed effect\",\"expect\":\"return successful write metadata\"}"}
				}]},"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`)
			return
		}
		_, _ = io.WriteString(writer, `{
			"id":"complete","model":"recovery-model",
			"choices":[{"message":{"content":"Recovered from the durable checkpoint."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer server.Close()

	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
		ProviderName:       "openai-recovery", ProviderBaseURL: server.URL,
		ProviderAPIKey: "test-only", ProviderModel: "recovery-model",
		ProviderHTTPClient: server.Client(), TurnIdleTimeout: 500 * time.Millisecond,
	}
	actorID := uuid.New()
	first, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	created := first.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation:      controlplane.OperationSessionCreate,
		Scope:          controlplane.Scope{ActorID: actorID},
		IdempotencyKey: "recovery-session", Payload: json.RawMessage(`{}`),
	})
	if created.Error != nil {
		t.Fatalf("session create = %+v", created)
	}
	var sessionView struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(created.Result, &sessionView); err != nil {
		t.Fatal(err)
	}
	onboardAcceptanceActor(
		t, ctx, first, actorID, sessionView.ID, "recovery-onboarding",
	)
	submitted := first.dispatcher.Dispatch(ctx, actorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation:      controlplane.OperationTurnSubmit,
		Scope:          controlplane.Scope{ActorID: actorID, SessionID: &sessionView.ID},
		IdempotencyKey: "recovery-turn",
		Payload:        json.RawMessage(`{"content":"Write recovery.txt exactly once, then report."}`),
	})
	if submitted.Error != nil {
		t.Fatalf("turn submit = %+v", submitted)
	}
	var turnView struct {
		TurnID uuid.UUID `json:"turn_id"`
	}
	if err := json.Unmarshal(submitted.Result, &turnView); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var incomplete session.TurnState
	for time.Now().Before(deadline) {
		incomplete, err = first.sessions.LoadTurnState(ctx, turnView.TurnID)
		if err == nil && incomplete.Status == session.TurnIncomplete {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if incomplete.Status != session.TurnIncomplete ||
		len(incomplete.Checkpoint) == 0 || len(incomplete.Recovery) == 0 {
		t.Fatalf("typed incomplete was not durable: %+v, %v", incomplete, err)
	}
	var recovery map[string]any
	if err := json.Unmarshal(incomplete.Recovery, &recovery); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"phase", "last_tool", "last_result", "attempt",
		"recovery", "failure_class", "final_honest_partial",
	} {
		if _, exists := recovery[field]; !exists {
			t.Fatalf("durable recovery omitted %s: %+v", field, recovery)
		}
	}
	cognitionBefore, err := first.sessions.LoadCognitionState(ctx, sessionView.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	deadline = time.Now().Add(15 * time.Second)
	var messages []session.Message
	for time.Now().Before(deadline) {
		messages, err = restarted.sessions.ListMessages(ctx, sessionView.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) >= 2 &&
			messages[len(messages)-1].Role == session.RoleAssistant {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(messages) < 2 || !strings.Contains(
		string(messages[len(messages)-1].Content),
		"Recovered from the durable checkpoint",
	) {
		state, stateErr := restarted.sessions.LoadTurnState(ctx, turnView.TurnID)
		events, replayErr := restarted.journal.ReplayActor(ctx, actorID, 0, 512)
		mu.Lock()
		requestCount := normalRequests
		mu.Unlock()
		t.Fatalf(
			"restart did not finish the durable turn: messages=%+v state=%+v state_err=%v requests=%d events=%+v replay_err=%v",
			messages, state, stateErr, requestCount, events.Events, replayErr,
		)
	}
	effect, err := os.ReadFile(filepath.Join(workspace, "recovery.txt"))
	if err != nil || string(effect) != "single completed effect" {
		t.Fatalf("completed effect changed across recovery: %q, %v", effect, err)
	}
	replay, err := restarted.journal.ReplayActor(ctx, actorID, 0, 512)
	if err != nil {
		t.Fatal(err)
	}
	toolRequests := 0
	incompleteEvents := 0
	recoveryEvents := 0
	for _, event := range replay.Events {
		switch event.Type {
		case controlplane.EventToolRequested:
			toolRequests++
		case controlplane.EventTurnIncomplete:
			incompleteEvents++
		case controlplane.EventTurnRecovery:
			recoveryEvents++
		}
	}
	if toolRequests != 1 || incompleteEvents < 1 || recoveryEvents < 2 {
		t.Fatalf(
			"recovery events tool=%d incomplete=%d recovery=%d",
			toolRequests, incompleteEvents, recoveryEvents,
		)
	}
	cognitionAfter, err := restarted.sessions.LoadCognitionState(ctx, sessionView.ID)
	if err != nil {
		t.Fatal(err)
	}
	var beforeState, afterState cognitionSnapshot
	if err := json.Unmarshal(cognitionBefore, &beforeState); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cognitionAfter, &afterState); err != nil {
		t.Fatal(err)
	}
	if len(beforeState.Premises.Items) == 0 ||
		len(afterState.Premises.Items) == 0 ||
		len(afterState.TaskGraph.Nodes) < len(beforeState.TaskGraph.Nodes) ||
		len(afterState.TaskGraph.ActionLog) != len(beforeState.TaskGraph.ActionLog) {
		t.Fatalf(
			"epistemic continuity failed:\nbefore=%+v\nafter=%+v",
			beforeState, afterState,
		)
	}
	mu.Lock()
	gotRequests := normalRequests
	mu.Unlock()
	if gotRequests != 4 {
		t.Fatalf("normal provider requests = %d, want 4", gotRequests)
	}
	database, err := os.ReadFile(filepath.Join(dataDirectory, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte("recovery.txt must be written once")) ||
		bytes.Contains(database, []byte("single completed effect")) {
		t.Fatal("durable recovery or cognition state was stored in plaintext")
	}
}

func TestOpenRuntimeLivingContextSoulRelationshipTemporalRestartAndIsolation(
	t *testing.T,
) {
	ctx := context.Background()
	clock := &acceptanceClock{
		now: time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC),
	}
	dataDirectory := t.TempDir()
	workspace := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)

	var captureMu sync.Mutex
	var providerBodies [][]byte
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
		writer.Header().Set("Content-Type", "application/json")
		if bytes.Contains(body, []byte("Extract only load-bearing factual premises")) {
			_, _ = io.WriteString(writer, `{
				"id":"premises","model":"living-test",
				"choices":[{"message":{"content":"{\"premises\":[\"the requested explanation concerns code\"]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`)
			return
		}
		captureMu.Lock()
		providerBodies = append(providerBodies, append([]byte(nil), body...))
		captureMu.Unlock()
		_, _ = io.WriteString(writer, `{
			"id":"living-turn","model":"living-test",
			"choices":[{"message":{"content":"Living context observed."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer server.Close()

	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
		ProviderName:       "openai-test", ProviderBaseURL: server.URL,
		ProviderAPIKey: "test-only", ProviderModel: "living-test",
		ProviderHTTPClient: server.Client(),
		Clock:              clock,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	sessionID := createAcceptanceSession(t, ctx, runtime, actor, "living-a")
	submitAcceptanceTurn(t, ctx, runtime, actor, sessionID, "hello", "living-hello")

	captureMu.Lock()
	if len(providerBodies) != 1 {
		captureMu.Unlock()
		t.Fatalf("social primary provider calls = %d, want 1", len(providerBodies))
	}
	helloBody := append([]byte(nil), providerBodies[0]...)
	captureMu.Unlock()
	for _, fragment := range [][]byte{
		[]byte("Immutable living-context snapshot"),
		[]byte("SOUL identity anchor"),
		[]byte("Authorized relationship context"),
		[]byte("Temporal embodiment"),
		[]byte("Emotional decision state"),
		[]byte("Relevant encrypted memory activation"),
		[]byte("Observed self-model"),
		[]byte(`"name":"filesystem_read"`),
		[]byte("never tool classification"),
	} {
		if !bytes.Contains(helloBody, fragment) {
			t.Fatalf("social provider request missing %q", fragment)
		}
	}
	firstLiving := queryLivingState(t, ctx, runtime, actor, sessionID)
	if len(firstLiving.Relationships) != 1 ||
		firstLiving.Relationships[0].Domain != "general" ||
		firstLiving.Temporal == nil ||
		!firstLiving.Temporal.TaskStarted.IsZero() {
		t.Fatalf("social turn manufactured task/domain state: %+v", firstLiving)
	}

	clock.Advance(4 * time.Hour)
	expertContent := "I am an expert; keep it concise. Analyze this code.\n" +
		"deadline: " + clock.Now().Add(30*time.Minute).Format(time.RFC3339)
	submitAcceptanceTurn(
		t, ctx, runtime, actor, sessionID, expertContent, "living-expert",
	)
	expertMarker := "I am an expert; keep it concise. Analyze this code."
	expertBody := primaryBodyContaining(t, &captureMu, providerBodies, expertMarker)
	if !bytes.Contains(expertBody, []byte("be concise and assume domain terminology")) {
		t.Fatalf("expert guidance did not reach provider: %s", expertBody)
	}
	for _, guidance := range []string{
		"suggest a break or bounded delegation",
		"skip optional steps while preserving required verification",
		"prioritize required steps and defer optional work",
	} {
		if !bytes.Contains(expertBody, []byte(guidance)) {
			t.Fatalf("temporal guidance %q did not reach provider: %s", guidance, expertBody)
		}
	}
	secondLiving := queryLivingState(t, ctx, runtime, actor, sessionID)
	if secondLiving.Temporal == nil || secondLiving.Temporal.LastCompleted.IsZero() ||
		!secondLiving.Temporal.TaskStarted.IsZero() ||
		len(secondLiving.Relationships) != 2 {
		t.Fatalf("substantive turn did not close isolated domain state: %+v", secondLiving)
	}
	redHandled := false
	if err := runtime.capabilityRoot.manager.Register(ctx, tools.Registration{
		Name: "acceptance_publish", Description: "Consequential acceptance operation.",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationRed,
		Check:          func(context.Context) error { return nil },
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			redHandled = true
			return json.RawMessage(`{"published":true}`), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	assertRedDenied := func(label string) {
		t.Helper()
		_, executeErr := runtime.capabilityRoot.manager.Execute(
			policy.WithPrincipal(ctx, policy.Principal{
				Sender: policy.SenderUser, Approved: false,
			}),
			protocol.NormalizedToolCall{
				ID: label, Name: "acceptance_publish",
				Arguments: json.RawMessage(`{}`),
			},
		)
		if !errors.Is(executeErr, tools.ErrPolicyDenied) || redHandled {
			t.Fatalf("%s RED operation = %v, handled=%v", label, executeErr, redHandled)
		}
	}
	assertRedDenied("normal-trust")
	runtime.capabilityRoot.living.mu.Lock()
	for day := 0; day < 2; day++ {
		for adjustment := 0; adjustment < 4; adjustment++ {
			if _, err := runtime.capabilityRoot.living.relationships.AdjustTrust(
				actor.String(), "software", .05,
			); err != nil {
				runtime.capabilityRoot.living.mu.Unlock()
				t.Fatal(err)
			}
		}
		clock.Advance(24 * time.Hour)
	}
	if err := runtime.capabilityRoot.living.saveRelationshipLocked(
		ctx, actor.String(), "software",
	); err != nil {
		runtime.capabilityRoot.living.mu.Unlock()
		t.Fatal(err)
	}
	runtime.capabilityRoot.living.mu.Unlock()
	highTrustContent := "Analyze this code with the established context."
	submitAcceptanceTurn(
		t, ctx, runtime, actor, sessionID, highTrustContent, "living-high-trust",
	)
	highTrustBody := primaryBodyContaining(
		t, &captureMu, providerBodies, highTrustContent,
	)
	if !bytes.Contains(
		highTrustBody,
		[]byte("reduce conversational hedging, never verification"),
	) {
		t.Fatalf("high-trust communication guidance missing: %s", highTrustBody)
	}
	assertRedDenied("high-trust")

	beginner := uuid.New()
	beginnerSession := createAcceptanceSession(
		t, ctx, runtime, beginner, "living-b",
	)
	beginnerContent := "I am a beginner. Explain this code."
	submitAcceptanceTurn(
		t, ctx, runtime, beginner, beginnerSession,
		beginnerContent, "living-beginner",
	)
	beginnerBody := primaryBodyContaining(
		t, &captureMu, providerBodies, beginnerContent,
	)
	if !bytes.Contains(beginnerBody, []byte("explain terminology and use concrete examples")) ||
		!bytes.Contains(beginnerBody, []byte(`"name":"filesystem_read"`)) {
		t.Fatalf("beginner guidance or normal tools missing: %s", beginnerBody)
	}
	if bytes.Contains(beginnerBody, []byte(expertMarker)) {
		t.Fatal("cross-actor transcript leaked through production activation")
	}
	crossActor := runtime.dispatcher.Dispatch(ctx, beginner, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationLivenessGet,
		Scope: controlplane.Scope{
			ActorID: beginner, SessionID: &sessionID,
		},
		Payload: json.RawMessage(`{}`),
	})
	if crossActor.Error != nil ||
		!bytes.Contains(crossActor.Result, []byte("cross-actor session access denied")) ||
		bytes.Contains(crossActor.Result, []byte(`"domain":"general"`)) {
		t.Fatalf("cross-actor living projection did not fail closed: %+v", crossActor)
	}

	soulBefore := querySoulState(t, ctx, runtime, actor, sessionID)
	propose := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationSoulPropose,
		Scope: controlplane.Scope{
			ActorID: actor, SessionID: &sessionID,
		},
		IdempotencyKey: "soul-propose-living",
		Payload: json.RawMessage(`{
			"action":"propose",
			"candidate":"# Ion SOUL\n\nBe precise, candid, and evidence-led."
		}`),
	})
	if propose.Error != nil {
		t.Fatalf("SOUL proposal = %+v", propose)
	}
	var proposal struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(propose.Result, &proposal); err != nil {
		t.Fatal(err)
	}
	approvePayload, err := json.Marshal(map[string]any{
		"action": "approve", "proposal_id": proposal.ID, "confirm": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	approve := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationSoulPropose,
		Scope: controlplane.Scope{
			ActorID: actor, SessionID: &sessionID,
		},
		IdempotencyKey: "soul-approve-living",
		Payload:        approvePayload,
	})
	if approve.Error != nil {
		t.Fatalf("SOUL approval = %+v", approve)
	}
	soulAfter := querySoulState(t, ctx, runtime, actor, sessionID)
	if soulAfter.Current.Number != soulBefore.Current.Number+1 ||
		len(soulAfter.History) != len(soulBefore.History)+1 {
		t.Fatalf("SOUL history did not advance: before=%+v after=%+v", soulBefore, soulAfter)
	}
	submitAcceptanceTurn(
		t, ctx, runtime, actor, sessionID, "thank you", "living-new-soul",
	)
	revisedBody := primaryBodyContaining(
		t, &captureMu, providerBodies, "thank you",
	)
	if !bytes.Contains(revisedBody, []byte("Be precise, candid, and evidence-led.")) {
		t.Fatal("approved SOUL revision was not observed by the next interaction")
	}

	temporalBefore := *queryLivingState(
		t, ctx, runtime, actor, sessionID,
	).Temporal
	emotionalBefore := queryLivingState(
		t, ctx, runtime, actor, sessionID,
	).Emotional
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restoredLiving := queryLivingState(t, ctx, restarted, actor, sessionID)
	restoredSoul := querySoulState(t, ctx, restarted, actor, sessionID)
	if restoredLiving.Temporal == nil ||
		restoredLiving.Temporal.SessionStarted != temporalBefore.SessionStarted ||
		restoredLiving.Temporal.TaskStarted != temporalBefore.TaskStarted ||
		len(restoredLiving.Relationships) != 2 ||
		restoredLiving.Emotional != emotionalBefore ||
		restoredSoul.Current.Number != soulAfter.Current.Number {
		t.Fatalf(
			"living restart continuity failed: living=%+v soul=%+v",
			restoredLiving, restoredSoul,
		)
	}
	database, err := os.ReadFile(filepath.Join(dataDirectory, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte("Be precise, candid, and evidence-led.")) ||
		bytes.Contains(database, []byte(`"expertise":"expert"`)) {
		t.Fatal("encrypted living-state database exposed identity or relationship plaintext")
	}
}

type acceptanceLivingProjection struct {
	Relationships []struct {
		Domain string `json:"domain"`
	} `json:"relationships"`
	Temporal *struct {
		SessionStarted time.Time `json:"session_started"`
		TaskStarted    time.Time `json:"task_started"`
		LastCompleted  time.Time `json:"last_completed"`
	} `json:"temporal"`
	Emotional struct {
		Frustration  float64 `json:"frustration"`
		Confidence   float64 `json:"confidence"`
		Urgency      float64 `json:"urgency"`
		Satisfaction float64 `json:"satisfaction"`
		Curiosity    float64 `json:"curiosity"`
		Fatigue      float64 `json:"fatigue"`
	} `json:"emotional"`
}

type acceptanceSoulProjection struct {
	Current struct {
		Number uint64 `json:"number"`
	} `json:"current"`
	History []json.RawMessage `json:"history"`
}

func createAcceptanceSession(
	t *testing.T,
	ctx context.Context,
	runtime *Runtime,
	actor uuid.UUID,
	key string,
) uuid.UUID {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationSessionCreate,
		Scope:           controlplane.Scope{ActorID: actor},
		IdempotencyKey:  key,
		Payload:         json.RawMessage(`{}`),
	})
	if response.Error != nil {
		t.Fatalf("create session = %+v", response)
	}
	var result struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}

func submitAcceptanceTurn(
	t *testing.T,
	ctx context.Context,
	runtime *Runtime,
	actor uuid.UUID,
	sessionID uuid.UUID,
	content string,
	key string,
) {
	t.Helper()
	onboardAcceptanceActor(t, ctx, runtime, actor, sessionID, key)
	submitAcceptanceTurnRaw(t, ctx, runtime, actor, sessionID, content, key)
}

func onboardAcceptanceActor(
	t *testing.T,
	ctx context.Context,
	runtime *Runtime,
	actor uuid.UUID,
	sessionID uuid.UUID,
	key string,
) {
	t.Helper()
	if _, named := runtime.capabilityRoot.living.PreferredName(ctx, actor); named {
		return
	}
	submitAcceptanceTurnRaw(
		t, ctx, runtime, actor, sessionID,
		"hello", key+"-name-prompt",
	)
	submitAcceptanceTurnRaw(
		t, ctx, runtime, actor, sessionID,
		"Acceptance Operator", key+"-name-answer",
	)
}

func submitAcceptanceTurnRaw(
	t *testing.T,
	ctx context.Context,
	runtime *Runtime,
	actor uuid.UUID,
	sessionID uuid.UUID,
	content string,
	key string,
) {
	t.Helper()
	before, err := runtime.sessions.ListMessages(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindCommand,
		Operation:       controlplane.OperationTurnSubmit,
		Scope: controlplane.Scope{
			ActorID: actor, SessionID: &sessionID,
		},
		IdempotencyKey: key,
		Payload:        mustJSON(t, map[string]string{"content": content}),
	})
	if response.Error != nil {
		t.Fatalf("submit turn = %+v", response)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		messages, listErr := runtime.sessions.ListMessages(ctx, sessionID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(messages) >= len(before)+2 &&
			messages[len(messages)-1].Role == session.RoleAssistant {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	states, _ := runtime.sessions.RecentTurnStates(ctx, sessionID, 8)
	replay, _ := runtime.journal.ReplayActor(ctx, actor, 0, 512)
	t.Fatalf("turn %q did not complete: states=%+v events=%+v", content, states, replay.Events)
}

func queryLivingState(
	t *testing.T,
	ctx context.Context,
	runtime *Runtime,
	actor uuid.UUID,
	sessionID uuid.UUID,
) acceptanceLivingProjection {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationLivenessGet,
		Scope: controlplane.Scope{
			ActorID: actor, SessionID: &sessionID,
		},
		Payload: json.RawMessage(`{}`),
	})
	if response.Error != nil {
		t.Fatalf("liveness query = %+v", response)
	}
	var result acceptanceLivingProjection
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func querySoulState(
	t *testing.T,
	ctx context.Context,
	runtime *Runtime,
	actor uuid.UUID,
	sessionID uuid.UUID,
) acceptanceSoulProjection {
	t.Helper()
	response := runtime.dispatcher.Dispatch(ctx, actor, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(),
		Kind:            controlplane.KindQuery,
		Operation:       controlplane.OperationSoulGet,
		Scope: controlplane.Scope{
			ActorID: actor, SessionID: &sessionID,
		},
		Payload: json.RawMessage(`{}`),
	})
	if response.Error != nil {
		t.Fatalf("SOUL query = %+v", response)
	}
	var result acceptanceSoulProjection
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func primaryBodyContaining(
	t *testing.T,
	mu *sync.Mutex,
	bodies [][]byte,
	content string,
) []byte {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	for _, body := range bodies {
		if bytes.Contains(body, []byte(content)) {
			return append([]byte(nil), body...)
		}
	}
	t.Fatalf("no primary provider request contained %q", content)
	return nil
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
