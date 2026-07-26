//go:build e2e

// Command ion-web-e2e runs the production control plane behind the
// built browser bundle. Playwright uses a deterministic provider by default;
// ION_WEB_PROVIDER_MODE=live selects the configured external provider.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/action"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane/adapters"
	"github.com/paxlabs-inc/ion-agent/internal/operatorapp"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	providerapi "github.com/paxlabs-inc/ion-agent/internal/provider"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	studiocontrol "github.com/paxlabs-inc/ion-agent/internal/studio"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	e2eAddress = "127.0.0.1:4176"
	e2eOrigin  = "http://localhost:4176"
	liveMode   = "live"
)

type boundaryProvider struct {
	mu    sync.Mutex
	calls int
	state *boundaryState
}

type boundaryState struct {
	mu          sync.Mutex
	failedOnce  bool
	publishRuns int
}

type e2ePreviewInspector struct {
	browser *nativebrowser.Service
}

func (inspector e2ePreviewInspector) InspectProjectPreview(
	ctx context.Context,
	rawURL string,
	width int64,
	height int64,
	dark bool,
) (projectcontrol.RuntimeBrowserSnapshot, error) {
	result, err := inspector.browser.InspectPreview(ctx, rawURL, width, height, dark)
	if err != nil {
		return projectcontrol.RuntimeBrowserSnapshot{}, err
	}
	elements := make([]projectcontrol.RuntimeInspectionElement, 0, len(result.Snapshot.Elements))
	for _, element := range result.Snapshot.Elements {
		elements = append(elements, projectcontrol.RuntimeInspectionElement{
			Ref: element.Ref, Tag: element.Tag, Type: element.Type, Text: element.Text,
			Name: element.Name, Placeholder: element.Placeholder, Disabled: element.Disabled,
		})
	}
	accessibility := make(
		[]projectcontrol.RuntimeAccessibilityFinding,
		0,
		len(result.Accessibility),
	)
	for _, finding := range result.Accessibility {
		accessibility = append(accessibility, projectcontrol.RuntimeAccessibilityFinding{
			Ref: finding.Ref, Rule: finding.Rule, Message: finding.Message,
		})
	}
	diagnostics := make([]projectcontrol.RuntimeBrowserReport, 0, len(result.Diagnostics))
	for _, diagnostic := range result.Diagnostics {
		diagnostics = append(diagnostics, projectcontrol.RuntimeBrowserReport{
			Source: diagnostic.Source, Severity: diagnostic.Severity,
			Code: diagnostic.Code, Message: diagnostic.Message, Path: diagnostic.Path,
			Line: diagnostic.Line, Column: diagnostic.Column,
			CausalEvidence: append([]string(nil), diagnostic.Evidence...),
		})
	}
	return projectcontrol.RuntimeBrowserSnapshot{
		URL: result.Snapshot.URL, Title: result.Snapshot.Title, Text: result.Snapshot.Text,
		Elements: elements, Accessibility: accessibility,
		ScreenshotPNG: result.ScreenshotPNG, Diagnostics: diagnostics,
		Width: result.Width, Height: result.Height, DarkMode: result.DarkMode,
	}, nil
}

type transcriptProvider struct {
	inner   agent.Generator
	history []protocol.Message
}

func (provider transcriptProvider) Generate(
	ctx context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	leadingSystem := 0
	for leadingSystem < len(request.Messages) &&
		request.Messages[leadingSystem].Role == protocol.RoleSystem {
		leadingSystem++
	}
	messages := make([]protocol.Message, 0, len(request.Messages)+len(provider.history))
	messages = append(messages, request.Messages[:leadingSystem]...)
	messages = append(messages, provider.history...)
	messages = append(messages, request.Messages[leadingSystem:]...)
	request.Messages = messages
	return provider.inner.Generate(ctx, request)
}

func (provider *boundaryProvider) Generate(
	ctx context.Context,
	request protocol.GenerationRequest,
) (protocol.NormalizedGeneration, error) {
	userContent := ""
	for _, message := range request.Messages {
		if message.Role == protocol.RoleUser {
			userContent = message.Content
		}
	}
	if strings.Contains(userContent, "Inspect the real workspace in Computer") {
		for _, message := range request.Messages {
			if message.Role == protocol.RoleTool {
				return protocol.NormalizedGeneration{
					Content:      "The real workspace file was inspected and is visible in Computer.",
					FinishReason: protocol.FinishStop,
					Provider:     "playwright-external-boundary",
					Model:        "acceptance",
				}, nil
			}
		}
		return protocol.NormalizedGeneration{
			Content: "I will inspect the authoritative specification through the real filesystem tool.",
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "e2e-workspace-read", Name: "filesystem_read",
				Arguments: json.RawMessage(`{"path":"spec/ion_spec/spec.kvx"}`),
			}},
			FinishReason: protocol.FinishToolCalls,
			Provider:     "playwright-external-boundary",
			Model:        "acceptance",
		}, nil
	}
	if strings.Contains(userContent, "Wait for steering") {
		<-ctx.Done()
		return protocol.NormalizedGeneration{}, ctx.Err()
	}
	if strings.Contains(userContent, "Steering correction") {
		return protocol.NormalizedGeneration{
			Content:      "Steering correction applied.",
			FinishReason: protocol.FinishStop,
			Provider:     "playwright-external-boundary",
			Model:        "acceptance",
		}, nil
	}
	if strings.Contains(userContent, "Fail once then retry") {
		provider.state.mu.Lock()
		failed := provider.state.failedOnce
		provider.state.failedOnce = true
		provider.state.mu.Unlock()
		if !failed {
			return protocol.NormalizedGeneration{}, errors.New(
				"simulated permanent provider failure",
			)
		}
		return protocol.NormalizedGeneration{
			Content:      "Retry recovered the failed turn.",
			FinishReason: protocol.FinishStop,
			Provider:     "playwright-external-boundary",
			Model:        "acceptance",
		}, nil
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	if provider.calls == 1 {
		return protocol.NormalizedGeneration{
			Content: "Approval is required before this release can be published.",
			ToolCalls: []protocol.NormalizedToolCall{{
				ID: "e2e-approval", Name: "publish_accepted_release",
				Arguments: json.RawMessage(
					`{"release":"2026.07.19","access_token":"browser-must-not-see"}`,
				),
			}},
			FinishReason: protocol.FinishToolCalls,
			Provider:     "playwright-external-boundary",
			Model:        "acceptance",
		}, nil
	}
	content := "The approved release was published with durable audit evidence."
	for _, message := range request.Messages {
		if message.Role == protocol.RoleTool && strings.Contains(message.Content, "approval denied") {
			content = "The operation was denied and was not published."
		}
	}
	return protocol.NormalizedGeneration{
		Content:      content,
		FinishReason: protocol.FinishStop,
		Provider:     "playwright-external-boundary",
		Model:        "acceptance",
	}, nil
}

func (provider *boundaryProvider) GenerateStream(
	ctx context.Context,
	request protocol.GenerationRequest,
	deliver func(protocol.StreamChunk) error,
) (protocol.NormalizedGeneration, error) {
	userContent := ""
	for _, message := range request.Messages {
		if message.Role == protocol.RoleUser {
			userContent = message.Content
		}
	}
	if !strings.Contains(userContent, "Show a delayed streaming response") {
		return provider.Generate(ctx, request)
	}
	contentChunks := []string{
		"Streaming arrives ",
		"in several visible steps ",
		"without duplicate messages.",
	}
	reasoningChunks := []string{
		"Prepared the first part. ",
		"Checked progressive rendering. ",
		"Finished cleanly.",
	}
	for index := range contentChunks {
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return protocol.NormalizedGeneration{}, ctx.Err()
		case <-timer.C:
		}
		if err := deliver(protocol.StreamChunk{
			ContentDelta:   contentChunks[index],
			ReasoningDelta: reasoningChunks[index],
		}); err != nil {
			return protocol.NormalizedGeneration{}, err
		}
	}
	return protocol.NormalizedGeneration{
		Content:      strings.Join(contentChunks, ""),
		Reasoning:    strings.Join(reasoningChunks, ""),
		FinishReason: protocol.FinishStop,
		Provider:     "playwright-external-boundary", Model: "acceptance",
	}, nil
}

type recoverableBoundaryRunner struct {
	inner *agent.Loop
}

type wideWorkExecutor struct{}

func (wideWorkExecutor) Execute(
	ctx context.Context,
	packet workcontrol.TaskPacket,
	attemptID uuid.UUID,
) (workcontrol.WorkerResult, error) {
	if strings.HasSuffix(packet.WorkItemID, "-19") {
		return workcontrol.WorkerResult{
			AttemptID: attemptID,
			Status:    workcontrol.SpecialistCompleted,
			Progress:  100,
			Summary:   "Review finished but still needs verified evidence.",
			Usage: workcontrol.BudgetUsage{
				CostKnown: true, ProviderSpendKnown: true,
			},
		}, nil
	}
	<-ctx.Done()
	return workcontrol.WorkerResult{
		AttemptID: attemptID,
		Status:    workcontrol.SpecialistCancelled,
	}, ctx.Err()
}

func (runner recoverableBoundaryRunner) Turn(
	ctx context.Context,
	content string,
) (agent.Response, error) {
	if !strings.Contains(content, "Recover an answer-validation checkpoint") {
		return runner.inner.Turn(ctx, content)
	}
	return agent.Response{
			ProviderCalls: 3,
			Checkpoint: &agent.TurnCheckpoint{
				Version: 1, UserContent: content,
				Messages: []protocol.Message{{
					Role: protocol.RoleUser, Content: content,
				}},
				ProviderCalls: 3,
			},
		}, &action.ErrIncomplete{
			Phase: "answer_validation", StuckSince: time.Now().UTC(),
			Recovery: "resume from durable evidence and produce a direct answer",
			Attempt:  3,
		}
}

func (runner recoverableBoundaryRunner) Resume(
	ctx context.Context,
	content string,
	checkpoint json.RawMessage,
) (agent.Response, error) {
	if !strings.Contains(content, "Recover an answer-validation checkpoint") {
		var cursor agent.TurnCheckpoint
		if err := json.Unmarshal(checkpoint, &cursor); err != nil {
			return agent.Response{}, fmt.Errorf("decode recovery checkpoint: %w", err)
		}
		return runner.inner.Resume(ctx, content, cursor)
	}
	if !json.Valid(checkpoint) {
		return agent.Response{}, fmt.Errorf("invalid recovery checkpoint")
	}
	return agent.Response{
		Content: "The answer-validation checkpoint resumed from durable " +
			"evidence and completed without exposing its intermediate " +
			"recovery event as a failed request.",
		ProviderCalls: 4,
	}, nil
}

type brokeredExecutionPolicy struct {
	broker   *controlplane.ApprovalBroker
	pipeline tools.ExecutionPolicy
	ttl      time.Duration
}

func (executionPolicy brokeredExecutionPolicy) Authorize(
	ctx context.Context,
	invocation tools.Invocation,
) (protocol.NormalizedToolCall, error) {
	if invocation.Classification != tools.ClassificationRed {
		return executionPolicy.pipeline.Authorize(ctx, invocation)
	}
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok {
		return protocol.NormalizedToolCall{}, controlplane.ErrUnauthorized
	}
	_, err := executionPolicy.broker.Request(ctx, controlplane.ApprovalInput{
		Scope: scope, Operation: "Publish accepted release",
		Arguments:   invocation.Call.Arguments,
		Consequence: "Publishes release 2026.07.19 to operators.",
		TTL:         executionPolicy.ttl,
	})
	principal := policy.PrincipalFromContext(ctx)
	if err != nil {
		principal.Approved = false
		_, _ = executionPolicy.pipeline.Authorize(
			policy.WithPrincipal(ctx, principal),
			invocation,
		)
		return protocol.NormalizedToolCall{}, err
	}
	principal.Approved = true
	return executionPolicy.pipeline.Authorize(
		policy.WithPrincipal(ctx, principal),
		invocation,
	)
}

func configuredLiveProvider() (
	agent.Generator,
	string,
	string,
	bool,
	error,
) {
	if !strings.EqualFold(
		strings.TrimSpace(os.Getenv("ION_WEB_PROVIDER_MODE")),
		liveMode,
	) {
		return nil, "", "", false, nil
	}
	name := strings.TrimSpace(os.Getenv("PROVIDER_NAME"))
	baseURL := strings.TrimSpace(os.Getenv("PROVIDER_BASE_URL"))
	apiKey := os.Getenv("PROVIDER_API_KEY")
	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if name == "" || baseURL == "" || apiKey == "" || model == "" {
		return nil, "", "", false, fmt.Errorf(
			"live provider requires PROVIDER_NAME, PROVIDER_BASE_URL, " +
				"PROVIDER_API_KEY, and LLM_MODEL",
		)
	}
	var adapter providerapi.ProviderAdapter = providerapi.OpenAIAdapter{}
	authentication := providerapi.BearerAuthentication()
	headers := map[string]string{}
	endpointURL := providerEndpoint(baseURL, "chat/completions")
	normalizedName := strings.ToLower(name)
	if strings.Contains(normalizedName, "anthropic") ||
		strings.Contains(normalizedName, "claude") {
		adapter = providerapi.AnthropicAdapter{}
		authentication = providerapi.HeaderAuthentication("x-api-key")
		headers = providerapi.AnthropicHeaders()
		endpointURL = providerEndpoint(baseURL, "messages")
	}
	pool, err := providerapi.NewPool([]providerapi.Endpoint{{
		Name: name, URL: endpointURL, Model: model, Adapter: adapter,
		Credentials: []providerapi.Credential{{
			ID: "environment-primary", Secret: apiKey,
		}},
		Authentication: authentication,
		Headers:        headers,
		RequestTimeout: 90 * time.Second,
	}})
	if err != nil {
		return nil, "", "", false, err
	}
	const systemPrompt = `You are Ion, an operator agent. Give concise,
accurate answers and clearly distinguish observations from assumptions. You
have one RED tool, publish_accepted_release. Call it only when the user
explicitly asks to publish the accepted release; its effect requires an exact
human approval in the operator UI. Never claim an external effect occurred
unless its tool result confirms it.`
	return pool, model, systemPrompt, true, nil
}

func providerEndpoint(baseURL string, suffix string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(strings.ToLower(trimmed), "/"+strings.ToLower(suffix)) {
		return trimmed
	}
	return trimmed + "/" + suffix
}

func transcriptMessages(messages []session.Message) []protocol.Message {
	if len(messages) > 0 && messages[len(messages)-1].Role == session.RoleUser {
		// startTurn persists the current input before constructing the runner;
		// Loop.Turn appends that same current input itself.
		messages = messages[:len(messages)-1]
	}
	history := make([]protocol.Message, 0, len(messages))
	for _, message := range messages {
		if message.MemoryType != session.MemoryTranscript {
			continue
		}
		var role protocol.MessageRole
		switch message.Role {
		case session.RoleSystem:
			role = protocol.RoleSystem
		case session.RoleUser:
			role = protocol.RoleUser
		case session.RoleAssistant:
			role = protocol.RoleAssistant
		default:
			continue
		}
		history = append(history, protocol.Message{
			Role: role, Content: string(message.Content),
		})
	}
	return history
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer stop()
	root, err := os.MkdirTemp("", "ion-web-e2e-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	key := make([]byte, vault.KeySize)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	cipher, err := vault.New(key)
	for index := range key {
		key[index] = 0
	}
	if err != nil {
		return err
	}
	defer cipher.Close()
	clock := types.SystemClock{}
	sessionStore, err := session.Open(
		ctx, filepath.Join(root, "sessions.db"), cipher, clock, 128*1024,
	)
	if err != nil {
		return err
	}
	defer sessionStore.Close(context.WithoutCancel(ctx))
	journal, err := controlplane.OpenJournal(
		ctx,
		filepath.Join(root, "controlplane.db"),
		clock,
		controlplane.JournalConfig{Retention: 2_000, SubscriberBuffer: 256},
	)
	if err != nil {
		return err
	}
	defer journal.Close()
	var broker *controlplane.ApprovalBroker
	dispatcher, err := controlplane.NewDispatcher(
		journal,
		clock,
		controlplane.SnapshotFunc(func(
			ctx context.Context,
			scope controlplane.Scope,
		) (json.RawMessage, error) {
			pending := []controlplane.ApprovalRequest{}
			if broker != nil {
				found, pendingErr := broker.Pending(ctx, scope.ActorID)
				if pendingErr != nil {
					return nil, pendingErr
				}
				pending = found
			}
			return json.Marshal(map[string]any{
				"health":            map[string]string{"status": "ready"},
				"heartbeat":         map[string]string{"state": "alive"},
				"providers":         map[string]string{"acceptance": "ready"},
				"pending_approvals": pending,
			})
		}),
		nil,
	)
	if err != nil {
		return err
	}
	broker, err = controlplane.NewApprovalBroker(journal, clock, nil)
	if err != nil {
		return err
	}
	if err := adapters.RegisterSessionHandlers(dispatcher, sessionStore); err != nil {
		return err
	}
	if err := controlplane.RegisterApprovalHandler(dispatcher, broker); err != nil {
		return err
	}
	projectRoot := filepath.Join(root, "projects", "workspaces")
	containerHost, err := projectcontrol.NewContainerHost(projectcontrol.ContainerHostConfig{
		WorkspaceRoot: projectRoot,
		ArchiveRoot:   filepath.Join(root, "projects", "archives"),
		Runtime:       strings.TrimSpace(os.Getenv("ION_CONTAINER_RUNTIME")),
		Image:         strings.TrimSpace(os.Getenv("ION_CONTAINER_IMAGE")),
		Network:       projectcontrol.NetworkPolicy{Mode: "deny"},
	})
	if err != nil {
		return err
	}
	projectService, err := projectcontrol.NewService(sessionStore, clock, projectcontrol.ServiceConfig{
		WorkspaceRoot: projectRoot,
		ArchiveRoot:   filepath.Join(root, "projects", "archives"),
		AttachRoots:   []string{root},
		ImportRoots:   []string{root},
		ContainerHost: containerHost,
	})
	if err != nil {
		_ = containerHost.Close()
		return err
	}
	defer projectService.Close()
	previewBrowser, err := nativebrowser.New(nativebrowser.Config{
		ExecutablePath:      strings.TrimSpace(os.Getenv("ION_BROWSER_EXECUTABLE")),
		ProfileRoot:         filepath.Join(root, "preview-browser"),
		AllowPrivateNetwork: true,
		DisableSandbox:      true,
	})
	if err != nil {
		return err
	}
	if err := previewBrowser.Ready(); err != nil {
		_ = previewBrowser.Close()
		return err
	}
	defer previewBrowser.Close()
	projectService.SetPreviewInspector(e2ePreviewInspector{browser: previewBrowser})
	workService, err := workcontrol.NewService(sessionStore, clock, root)
	if err != nil {
		return err
	}
	supervisorService, err := workcontrol.NewSupervisorService(
		sessionStore,
		clock,
		workService,
	)
	if err != nil {
		return err
	}
	supervisorService.SetExecutor(wideWorkExecutor{})
	studioService, err := studiocontrol.NewService(sessionStore, clock, projectService, workService)
	if err != nil {
		return err
	}
	surfaceService, err := operatorapp.NewSurfaceServiceWithProjectsAndWork(operatorapp.RuntimeInfo{
		ProviderName:  strings.TrimSpace(os.Getenv("PROVIDER_NAME")),
		ProviderModel: strings.TrimSpace(os.Getenv("LLM_MODEL")),
		DataDirectory: root,
		StartedAt:     time.Now().UTC(),
	}, projectService, studioService, workService, supervisorService, clock)
	if err != nil {
		return err
	}
	if err := adapters.RegisterSubsystemHandlers(
		dispatcher, surfaceService, surfaceService,
	); err != nil {
		return err
	}
	auditor, err := policy.OpenFileAuditor(filepath.Join(root, "policy.jsonl"))
	if err != nil {
		return err
	}
	defer auditor.Close()
	liveProvider, liveModel, livePrompt, usingLiveProvider, err :=
		configuredLiveProvider()
	if err != nil {
		return err
	}
	sessionStates := make(map[uuid.UUID]*boundaryState)
	var sessionStatesMu sync.Mutex
	factory := adapters.TurnRunnerFactoryFunc(func(
		sessionID uuid.UUID,
		_ adapters.TurnBinding,
	) (adapters.TurnRunner, error) {
		sessionStatesMu.Lock()
		state := sessionStates[sessionID]
		if state == nil {
			state = &boundaryState{}
			sessionStates[sessionID] = state
		}
		sessionStatesMu.Unlock()
		pipeline, err := policy.NewDefault(clock, auditor, nil, nil)
		if err != nil {
			return nil, err
		}
		manager, err := tools.NewManager(
			clock,
			tools.WithExecutionPolicy(brokeredExecutionPolicy{
				broker: broker, pipeline: pipeline, ttl: 2 * time.Minute,
			}),
			tools.WithLifecycleObserver(
				operatorapp.NewComputerLifecycleObserver(dispatcher),
			),
		)
		if err != nil {
			return nil, err
		}
		if err := manager.Register(ctx, tools.Registration{
			Name:        "publish_accepted_release",
			Description: "Publish exactly one accepted release.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"release":{"type":"string"},
					"access_token":{"type":"string"}
				},
				"required":["release","access_token"],
				"additionalProperties":false
			}`),
			Classification: tools.ClassificationRed,
			Check:          func(context.Context) error { return nil },
			Handler: func(
				_ context.Context,
				arguments json.RawMessage,
			) (json.RawMessage, error) {
				state.mu.Lock()
				state.publishRuns++
				state.mu.Unlock()
				return json.RawMessage(`{"published":true}`), nil
			},
		}); err != nil {
			return nil, err
		}
		if err := manager.Register(ctx, tools.Registration{
			Name:        "filesystem_read",
			Description: "Read one bounded file from the real Ion workspace.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"path":{"type":"string"}},
				"required":["path"],
				"additionalProperties":false
			}`),
			Classification: tools.ClassificationGreen,
			Check:          func(context.Context) error { return nil },
			Handler: func(
				_ context.Context,
				arguments json.RawMessage,
			) (json.RawMessage, error) {
				var input struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(arguments, &input); err != nil {
					return nil, err
				}
				const acceptedPath = "spec/ion_spec/spec.kvx"
				if input.Path != acceptedPath {
					return nil, fmt.Errorf("workspace path is not allowed")
				}
				content, err := os.ReadFile(acceptedPath)
				if err != nil {
					return nil, err
				}
				truncated := len(content) > 4096
				if truncated {
					content = content[:4096]
				}
				return json.Marshal(map[string]any{
					"path": acceptedPath, "content": string(content),
					"truncated": truncated,
				})
			},
		}); err != nil {
			return nil, err
		}
		generator := agent.Generator(&boundaryProvider{state: state})
		model := "acceptance"
		systemPrompt := ""
		if usingLiveProvider {
			messages, historyErr := sessionStore.ListMessages(ctx, sessionID)
			if historyErr != nil {
				return nil, historyErr
			}
			generator = transcriptProvider{
				inner: liveProvider, history: transcriptMessages(messages),
			}
			model = liveModel
			systemPrompt = livePrompt
		}
		loop, err := agent.NewLoop(
			generator,
			operatorapp.NewScopedToolManager(manager),
			agent.LoopConfig{
				Model: model, SystemPrompt: systemPrompt, UserID: "web-operator",
				SessionID: sessionID.String(),
			},
			nil,
		)
		if err != nil {
			return nil, err
		}
		return recoverableBoundaryRunner{inner: loop}, nil
	})
	coordinator, err := adapters.NewTurnCoordinator(
		ctx, sessionStore, factory, dispatcher,
	)
	if err != nil {
		return err
	}
	defer coordinator.Close()
	coordinator.SetSteeringResolver(
		adapters.NewJournalSteeringResolver(journal),
	)
	if err := coordinator.RegisterHandlers(dispatcher); err != nil {
		return err
	}
	signingKey := make([]byte, 32)
	if _, err := rand.Read(signingKey); err != nil {
		return err
	}
	authenticator, err := controlplane.NewCookieAuthenticator(
		signingKey, clock, time.Hour,
	)
	for index := range signingKey {
		signingKey[index] = 0
	}
	if err != nil {
		return err
	}
	tickets, err := controlplane.NewTicketManager(clock, 30*time.Second, 64)
	if err != nil {
		return err
	}
	actorID := uuid.New()
	if configuredActor := strings.TrimSpace(os.Getenv("ION_WEB_ACTOR_ID")); configuredActor != "" {
		actorID, err = uuid.Parse(configuredActor)
		if err != nil {
			return fmt.Errorf("invalid ION_WEB_ACTOR_ID: %w", err)
		}
	}
	allowedOrigins := []string{e2eOrigin}
	if devOrigin := strings.TrimSpace(os.Getenv("ION_WEB_DEV_ORIGIN")); devOrigin != "" {
		allowedOrigins = append(allowedOrigins, devOrigin)
	}
	browser, err := controlplane.NewBrowserServer(
		dispatcher,
		journal,
		authenticator,
		tickets,
		clock,
		controlplane.BrowserServerConfig{
			AllowedOrigins: allowedOrigins,
			MaxConnections: 8, MaxPayloadBytes: 64 << 10,
			// The browser matrix deliberately creates many fresh pages for the
			// same actor in under a minute. Transport rate-limit behavior has
			// dedicated lower-bound tests; this harness must not make unrelated
			// UX journeys fail because earlier test pages used the shared budget.
			RequestsPerMinute: 2_000, PingInterval: 5 * time.Second,
			PongTimeout: 15 * time.Second,
		},
	)
	if err != nil {
		return err
	}
	seededProject, err := projectService.CreateTemplate(ctx, projectcontrol.OperationMeta{
		ActorID: actorID, RequestID: uuid.New(), IdempotencyKey: "browser-studio-project",
		PolicyClassification: projectcontrol.PolicyYellow, Deadline: time.Now().Add(time.Minute),
		CorrelationID: uuid.New(),
	}, projectcontrol.TemplateInput{Name: "Welcome project", Template: "static-web", Host: projectcontrol.HostDirectLocal})
	if err != nil {
		return err
	}
	if err := initializeFixtureRepository(ctx, seededProject.Root); err != nil {
		return err
	}
	baseline := []byte("<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><link rel=\"icon\" href=\"data:,\"><title>New project</title><h1>Built with Ion</h1>\n")
	baselineHash := sha256.Sum256(baseline)
	patchReceipt, err := projectService.ApplyPatchSet(ctx, actorID, projectcontrol.PatchSet{
		Version:          projectcontrol.PatchSetVersion,
		ID:               uuid.New(),
		ProjectID:        seededProject.ID,
		BaselineRevision: seededProject.WorkspaceRevision,
		Criteria:         []string{"welcome.visible"},
		ValidationPlan:   []string{"npm test", "live preview"},
		Members: []projectcontrol.PatchMember{{
			Operation:      projectcontrol.PatchExact,
			Path:           "index.html",
			ExpectedSHA256: fmt.Sprintf("%x", baselineHash),
			OldText:        "<h1>Built with Ion</h1>",
			NewText:        "<main><h1>Built with Ion</h1><p>A calm first project is ready to review.</p></main>",
		}},
	})
	if err != nil {
		return err
	}
	seededProject.WorkspaceRevision = patchReceipt.WorkspaceRevision
	seededContract, err := workService.PutContract(ctx, actorID, workcontrol.ContractInput{
		Goal: "Add a welcoming project page", Deliverable: "reviewed page specification",
		DoneCriteria:         []workcontrol.Criterion{{ID: "welcome.visible", Description: "The welcome message is visible"}},
		VerificationRequired: []string{"npm test", "live preview"}, NextAction: "Review the proposed specification",
	})
	if err != nil {
		return err
	}
	seedDelta := studiocontrol.SpecDelta{
		UserVisibleBehavior: []string{"A clear welcome message appears on the project page"},
		NonGoals:            []string{"No deployment"}, Constraints: []string{"Use the shared Ion runtime"},
		Risks:              []string{"Existing page hierarchy could change"},
		Criteria:           []studiocontrol.Criterion{{ID: "welcome.visible", Description: "The welcome message is visible"}},
		SecurityBoundaries: []string{"Authenticated project members"}, DataBoundaries: []string{"Project files only"},
		Migration: []string{"No migration"}, Rollback: []string{"Remove the generated page change"},
		Verification: []string{"npm test", "live preview"},
		Tasks:        []studiocontrol.PlannedTask{{ID: "welcome.page", Title: "Build the welcome page", Criteria: []string{"welcome.visible"}}},
	}
	if _, err := studioService.Compile(ctx, actorID, studiocontrol.CompileInput{
		ProjectID: seededProject.ID, OutcomeContractID: seededContract.ID,
		WorkspaceRevision: seededProject.WorkspaceRevision, Goal: seededContract.Goal,
		Assumptions: []studiocontrol.Assumption{{ID: "layout", Statement: "Keep the existing page hierarchy", Reversible: true}},
		Rationale:   "No existing requirement covers the welcome page", Delta: &seedDelta,
	}); err != nil {
		return err
	}
	sessionCookie, csrfCookie, err := authenticator.Issue(actorID)
	if err != nil {
		return err
	}
	webRoot := filepath.Join("ui", "web", "dist")
	index, err := os.ReadFile(filepath.Join(webRoot, "index.html"))
	if err != nil {
		return fmt.Errorf("build ui/web before e2e: %w", err)
	}
	index = bytes.Replace(
		index,
		[]byte("<head>"),
		[]byte(`<head><meta name="ion-actor-id" content="`+
			actorID.String()+`">`),
		1,
	)
	mux := http.NewServeMux()
	mux.Handle("/v1/", browser)
	mux.HandleFunc("/e2e/wide-work", func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		run, seedErr := seedWideWork(
			request.Context(),
			workService,
			supervisorService,
			actorID,
			seededProject.ID,
		)
		if seedErr != nil {
			http.Error(writer, seedErr.Error(), http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if encodeErr := json.NewEncoder(writer).Encode(run); encodeErr != nil {
			return
		}
	})
	mux.Handle("/assets/", http.FileServer(http.Dir(webRoot)))
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		http.SetCookie(writer, sessionCookie)
		http.SetCookie(writer, csrfCookie)
		writer.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; connect-src 'self' ws://localhost:4176; "+
				"font-src 'self' data:; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"frame-src 'self' http://127.0.0.1:* http://localhost:*; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
		)
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write(index) // Client disconnect is the only write failure.
	})
	server := &http.Server{
		Addr: e2eAddress, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if usingLiveProvider {
		fmt.Printf(
			"ION_WEB_E2E_READY %s provider=%s model=%s\n",
			e2eOrigin,
			strings.TrimSpace(os.Getenv("PROVIDER_NAME")),
			liveModel,
		)
	} else {
		fmt.Printf("ION_WEB_E2E_READY %s provider=deterministic\n", e2eOrigin)
	}
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func seedWideWork(
	ctx context.Context,
	workService *workcontrol.Service,
	supervisorService *workcontrol.SupervisorService,
	actorID uuid.UUID,
	projectID uuid.UUID,
) (workcontrol.SupervisorRun, error) {
	prefix := uuid.NewString()[:8]
	criteria := make([]workcontrol.Criterion, workcontrol.LaunchParallelMinimum)
	items := make([]workcontrol.WorkItemInput, workcontrol.LaunchParallelMinimum)
	overrides := make([]workcontrol.TaskOverride, workcontrol.LaunchParallelMinimum)
	for index := range criteria {
		id := fmt.Sprintf("%s-%02d", prefix, index)
		criteria[index] = workcontrol.Criterion{
			ID: id, Description: "Prove " + id,
		}
		items[index] = workcontrol.WorkItemInput{
			ID: id, Title: fmt.Sprintf("Wide Work stream %02d", index+1),
			Criteria: []string{id},
		}
		overrides[index] = workcontrol.TaskOverride{
			WorkItemID: id,
			Specialist: workcontrol.SpecialistImplementation,
			Scope: workcontrol.AuthorityScope{
				ReadFiles:  []string{"workspace"},
				WriteFiles: []string{"wide-work/" + id},
			},
			Tools: []string{
				"filesystem_read", "filesystem_write",
				"artifact_record", "artifact_verify",
			},
		}
	}
	contract, _, err := workService.PutContractWithWorkItems(
		ctx,
		actorID,
		workcontrol.ContractInput{
			Goal:                 "Exercise live Wide Work controls",
			Deliverable:          "Visible specialist progress",
			DoneCriteria:         criteria,
			VerificationRequired: []string{"server-verified artifacts"},
			NextAction:           "supervise active workstreams",
		},
		items,
	)
	if err != nil {
		return workcontrol.SupervisorRun{}, err
	}
	budget := workcontrol.SupervisorBudget{
		SpecialistBudget: workcontrol.SpecialistBudget{
			MaxTokens: 400_000, MaxCostCents: 5_000,
			MaxToolCalls: 640, MaxWallSeconds: 7_200,
			MaxProcesses: 32, MaxStorageBytes: 2 << 30,
			MaxNetworkBytes: 1 << 30, MaxProviderCents: 5_000,
			MaxRetries: 2,
		},
		MaxParallel: workcontrol.LaunchParallelMinimum,
	}
	return supervisorService.Start(
		ctx,
		actorID,
		workcontrol.SupervisorStartInput{
			ContractID: contract.ID,
			ProjectID:  &projectID,
			Budget:     budget,
			Overrides:  overrides,
		},
	)
}

func initializeFixtureRepository(ctx context.Context, root string) error {
	commands := [][]string{
		{"init", "-b", "main"},
		{"add", "--", "."},
		{"-c", "user.name=Ion Acceptance", "-c", "user.email=ion@localhost", "commit", "-m", "Initial Studio project"},
	}
	for _, arguments := range commands {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, arguments...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if output, runErr := command.CombinedOutput(); runErr != nil {
			return fmt.Errorf(
				"initialize Studio acceptance repository: git %s: %w: %s",
				strings.Join(arguments, " "),
				runErr,
				strings.TrimSpace(string(output)),
			)
		}
	}
	return nil
}
