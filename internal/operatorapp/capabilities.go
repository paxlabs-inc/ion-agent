package operatorapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/belief/selfmodel"
	nativebrowser "github.com/paxlabs-inc/ion-agent/internal/browser"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	agentmailbox "github.com/paxlabs-inc/ion-agent/internal/mailbox"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/memory/hnsw"
	memoryjournal "github.com/paxlabs-inc/ion-agent/internal/memory/journal"
	"github.com/paxlabs-inc/ion-agent/internal/plugin"
	"github.com/paxlabs-inc/ion-agent/internal/presence/automatrix"
	"github.com/paxlabs-inc/ion-agent/internal/presence/gateway"
	projectcontrol "github.com/paxlabs-inc/ion-agent/internal/project"
	agentscheduler "github.com/paxlabs-inc/ion-agent/internal/scheduler"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
	studiocontrol "github.com/paxlabs-inc/ion-agent/internal/studio"
	"github.com/paxlabs-inc/ion-agent/internal/swarm"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/internal/tools/builtin"
	workcontrol "github.com/paxlabs-inc/ion-agent/internal/work"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

// productionCapabilities is the single authoritative capability composition
// root shared by provider prompts, execution, browser projections, TUI
// projections, plugins, and the MCP-facing manager.
type productionCapabilities struct {
	manager          *tools.Manager
	policy           *policy.Pipeline
	approvals        tools.ApprovalAuthorizer
	auditor          *policy.FileAuditor
	skills           *skills.Store
	pluginHost       *plugin.Host
	pluginSDK        plugin.SDK
	pluginRoot       string
	workspaceRoot    string
	memory           *cortex.Cortex
	memoryJournal    *memoryjournal.Journal
	memoryIndex      *hnsw.Index
	selfModel        *selfmodel.Model
	cognition        *cognitionRegistry
	living           *livingContext
	work             *workcontrol.Service
	supervisor       *workcontrol.SupervisorService
	projects         *projectcontrol.Service
	studio           *studiocontrol.Service
	channels         *channelRuntime
	browser          *nativebrowser.Service
	browserWorkflows *nativebrowser.Supervisor
	previewBrowser   *nativebrowser.Service
	privateDesktop   *privateDesktopHost
	control          *controllease.Service
	mailbox          *agentmailbox.Store
	presence         *presenceSupervisor
	scheduler        *agentscheduler.Service
	sessions         *session.Store
	clock            types.Clock
	lifecycle        tools.LifecycleObserver
	swarmRegistry    *swarm.Registry
	swarmCancel      context.CancelFunc
	swarmStopped     <-chan struct{}
}

func openProductionCapabilities(
	ctx context.Context,
	clock types.Clock,
	config RuntimeConfig,
	cipher *vault.Vault,
	sessions *session.Store,
	approvalAuthorizer tools.ApprovalAuthorizer,
	lifecycleObserver tools.LifecycleObserver,
	privateDesktop *privateDesktopHost,
) (*productionCapabilities, error) {
	workspace := strings.TrimSpace(config.WorkspaceDirectory)
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("operator capabilities: workspace: %w", err)
		}
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	auditor, err := policy.OpenFileAuditor(
		filepath.Join(config.DataDirectory, "policy", "events.jsonl"),
	)
	if err != nil {
		return nil, err
	}
	success := false
	capabilities := &productionCapabilities{
		auditor: auditor, workspaceRoot: workspace, clock: clock,
		sessions: sessions, approvals: approvalAuthorizer,
		lifecycle: lifecycleObserver, privateDesktop: privateDesktop,
	}
	defer func() {
		if !success {
			_ = capabilities.Close()
		}
	}()
	limiter, err := policy.NewWindowLimiter(120, time.Minute)
	if err != nil {
		return nil, err
	}
	capabilities.policy, err = policy.NewDefault(
		clock, redactingPolicyAuditor{inner: auditor}, limiter, allowAnomalyDetector{},
	)
	if err != nil {
		return nil, err
	}
	capabilities.manager, err = tools.NewManager(
		clock, tools.WithExecutionPolicy(capabilities.policy),
		tools.WithApprovalAuthorizer(approvalAuthorizer),
		tools.WithLifecycleObserver(lifecycleObserver),
	)
	if err != nil {
		return nil, err
	}
	capabilities.skills, err = skills.NewStore(
		filepath.Join(config.DataDirectory, "skills"),
	)
	if err != nil {
		return nil, err
	}
	skillLibrary := strings.TrimSpace(config.SkillLibraryDirectory)
	if skillLibrary == "" {
		skillLibrary = filepath.Join(workspace, "skills")
	}
	if _, err := capabilities.skills.ImportLibrary(ctx, skillLibrary); err != nil {
		return nil, fmt.Errorf("operator capabilities: import skill library: %w", err)
	}
	capabilities.memoryJournal, err = memoryjournal.Open(
		filepath.Join(config.DataDirectory, "cortex", "journal.bin"), cipher,
	)
	if err != nil {
		return nil, err
	}
	capabilities.memory, err = cortex.New(cortex.Config{
		Actor: "operator", Journal: capabilities.memoryJournal, Clock: clock,
	})
	if err != nil {
		return nil, err
	}
	if err := capabilities.openCognition(
		ctx, clock, config, sessions,
	); err != nil {
		return nil, err
	}
	capabilities.living, err = openLivingContext(
		ctx, clock, config, sessions, capabilities.memory,
	)
	if err != nil {
		return nil, err
	}
	capabilities.work, err = workcontrol.NewService(sessions, clock, workspace)
	if err != nil {
		return nil, err
	}
	capabilities.supervisor, err = workcontrol.NewSupervisorService(
		sessions, clock, capabilities.work,
	)
	if err != nil {
		return nil, err
	}
	projectRoot := strings.TrimSpace(config.ProjectWorkspaceRoot)
	if projectRoot == "" {
		projectRoot = filepath.Join(config.DataDirectory, "projects", "workspaces")
	}
	archiveRoot := filepath.Join(config.DataDirectory, "projects", "archives")
	containerHost, err := projectcontrol.NewContainerHost(projectcontrol.ContainerHostConfig{
		WorkspaceRoot: projectRoot, ArchiveRoot: archiveRoot,
		Runtime: config.ContainerRuntime, Image: config.ContainerImage,
		Network: projectcontrol.NetworkPolicy{Mode: "deny"},
	})
	if err != nil {
		return nil, err
	}
	repositoryProviders := map[string]projectcontrol.RepositoryProvider{}
	if config.RepositoryHTTPClient != nil {
		github, providerErr := projectcontrol.NewGitHubProvider(config.GitHubAPIBaseURL,
			config.RepositoryHTTPClient, config.RepositoryProviderAuthorizer)
		if providerErr != nil {
			return nil, providerErr
		}
		gitlab, providerErr := projectcontrol.NewGitLabProvider(config.GitLabAPIBaseURL,
			config.RepositoryHTTPClient, config.RepositoryProviderAuthorizer)
		if providerErr != nil {
			return nil, providerErr
		}
		repositoryProviders[github.Name()], repositoryProviders[gitlab.Name()] = github, gitlab
	}
	capabilities.control, err = controllease.New(sessions, clock)
	if err != nil {
		return nil, err
	}
	capabilities.projects, err = projectcontrol.NewService(sessions, clock, projectcontrol.ServiceConfig{
		WorkspaceRoot: projectRoot, ArchiveRoot: archiveRoot,
		AttachRoots: []string{workspace}, ImportRoots: []string{workspace},
		ContainerHost: containerHost, RepositoryProviders: repositoryProviders,
		GitCredentialBroker: config.RepositoryGitCredentialBroker,
		Control:             capabilities.control,
	})
	if err != nil {
		return nil, err
	}
	capabilities.studio, err = studiocontrol.NewService(
		sessions, clock, capabilities.projects, capabilities.work,
	)
	if err != nil {
		return nil, err
	}
	capabilities.browser, err = nativebrowser.New(nativebrowser.Config{
		ExecutablePath:      config.BrowserExecutable,
		AllowPrivateNetwork: config.BrowserAllowPrivateNetwork,
		Control:             capabilities.control,
		ProfileRoot: filepath.Join(
			config.DataDirectory, "browser", "volatile",
		),
	})
	if err != nil {
		return nil, err
	}
	capabilities.browserWorkflows, err = nativebrowser.OpenSupervisor(
		capabilities.browser,
		filepath.Join(config.DataDirectory, "browser", "workflows.enc"),
		cipher,
		clock,
	)
	if err != nil {
		return nil, err
	}
	capabilities.previewBrowser, err = nativebrowser.New(nativebrowser.Config{
		ExecutablePath: config.BrowserExecutable, AllowPrivateNetwork: true,
		ProfileRoot: filepath.Join(config.DataDirectory, "browser", "preview-volatile"),
	})
	if err != nil {
		return nil, err
	}
	capabilities.projects.SetPreviewInspector(projectPreviewInspector{browser: capabilities.previewBrowser})
	if err := builtin.Register(ctx, capabilities.manager, builtin.Config{
		Workspace: workspace, Skills: capabilities.skills,
		Memory:                capabilities.memory,
		TavilyAPIKey:          config.TavilyAPIKey,
		TavilySearchEndpoint:  config.TavilySearchEndpoint,
		SearXNGSearchEndpoint: config.SearchEndpoint,
	}); err != nil {
		return nil, err
	}
	if err := capabilities.registerWorkTools(ctx); err != nil {
		return nil, err
	}
	for _, registration := range capabilities.browserWorkflows.Registrations() {
		if err := capabilities.manager.Register(ctx, registration); err != nil {
			return nil, fmt.Errorf(
				"operator capabilities: register %s: %w",
				registration.Name, err,
			)
		}
	}
	if err := registerPrivateDesktopTools(
		ctx,
		capabilities.manager,
		capabilities.privateDesktop,
		capabilities.control,
	); err != nil {
		return nil, err
	}
	if config.AgentMailboxSource != nil ||
		strings.TrimSpace(config.AgentEmailAddress) != "" ||
		strings.TrimSpace(config.MachineMailURL) != "" ||
		strings.TrimSpace(config.MachineMailAPIKey) != "" ||
		strings.TrimSpace(config.AgentIMAPAddress) != "" ||
		strings.TrimSpace(config.AgentIMAPUsername) != "" ||
		config.AgentIMAPPassword != "" {
		source := config.AgentMailboxSource
		if source == nil {
			var sourceErr error
			if strings.TrimSpace(config.MachineMailAPIKey) != "" ||
				strings.TrimSpace(config.MachineMailURL) != "" {
				source, sourceErr = agentmailbox.NewMachineMailSource(
					agentmailbox.MachineMailConfig{
						BaseURL:    config.MachineMailURL,
						APIKey:     config.MachineMailAPIKey,
						HTTPClient: config.MachineMailHTTPClient,
					},
				)
			} else {
				source, sourceErr = agentmailbox.NewIMAPSource(agentmailbox.IMAPConfig{
					Address: config.AgentIMAPAddress, Username: config.AgentIMAPUsername,
					Password: config.AgentIMAPPassword,
				})
			}
			if sourceErr != nil {
				return nil, sourceErr
			}
		}
		capabilities.mailbox, err = agentmailbox.Open(
			config.AgentEmailAddress,
			filepath.Join(config.DataDirectory, "mailbox", "state.enc"),
			cipher,
			source,
		)
		if err != nil {
			return nil, err
		}
		for _, registration := range capabilities.mailbox.Registrations(
			capabilities.browser,
		) {
			if err := capabilities.manager.Register(ctx, registration); err != nil {
				return nil, fmt.Errorf(
					"operator capabilities: register %s: %w",
					registration.Name, err,
				)
			}
		}
	}
	capabilities.pluginSDK, err = plugin.NewSDK(
		unconfiguredChannels{}, capabilities.manager, capabilities.policy,
	)
	if err != nil {
		return nil, err
	}
	capabilities.pluginHost, err = plugin.NewHost(capabilities.pluginSDK)
	if err != nil {
		return nil, err
	}
	if err := capabilities.pluginHost.Start(ctx); err != nil {
		return nil, err
	}
	capabilities.pluginRoot = filepath.Join(config.DataDirectory, "plugins")
	if err := os.MkdirAll(capabilities.pluginRoot, 0o700); err != nil {
		return nil, err
	}
	capabilities.swarmRegistry = swarm.NewRegistry(clock)
	swarmCtx, swarmCancel := context.WithCancel(ctx)
	capabilities.swarmCancel = swarmCancel
	capabilities.swarmStopped = capabilities.swarmRegistry.StartOrphanRecovery(swarmCtx)
	success = true
	return capabilities, nil
}

func (capabilities *productionCapabilities) Close() error {
	if capabilities == nil {
		return nil
	}
	var result error
	if capabilities.swarmCancel != nil {
		capabilities.swarmCancel()
	}
	if capabilities.swarmStopped != nil {
		select {
		case <-capabilities.swarmStopped:
		case <-time.After(5 * time.Second):
			result = errors.Join(result, fmt.Errorf("operator capabilities: swarm shutdown timed out"))
		}
	}
	if capabilities.pluginHost != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result = errors.Join(result, capabilities.pluginHost.Stop(ctx))
		cancel()
	}
	if capabilities.browser != nil {
		result = errors.Join(result, capabilities.browser.Close())
	}
	if capabilities.previewBrowser != nil {
		result = errors.Join(result, capabilities.previewBrowser.Close())
	}
	if capabilities.projects != nil {
		result = errors.Join(result, capabilities.projects.Close())
	}
	if capabilities.memoryIndex != nil {
		result = errors.Join(result, capabilities.memoryIndex.Close())
	}
	if capabilities.memory != nil {
		result = errors.Join(result, capabilities.memory.Close())
	}
	if capabilities.memoryJournal != nil {
		result = errors.Join(result, capabilities.memoryJournal.Close())
	}
	if capabilities.auditor != nil {
		result = errors.Join(result, capabilities.auditor.Close())
	}
	return result
}

func (capabilities *productionCapabilities) enableDelegation(
	ctx context.Context,
	generator agent.Generator,
	model string,
) error {
	if capabilities.swarmRegistry == nil || generator == nil ||
		strings.TrimSpace(model) == "" {
		return fmt.Errorf("operator capabilities: delegation dependencies are required")
	}
	if model == "unconfigured" {
		reason := "configure a live provider to enable bounded sub-agent delegation"
		return capabilities.manager.Register(ctx, tools.Registration{
			Name:           "subagent_delegate",
			Description:    "Delegate one bounded research or inspection task to a reduced sub-agent.",
			Parameters:     json.RawMessage(`{"type":"object","additionalProperties":true}`),
			Classification: tools.ClassificationYellow,
			Check:          func(context.Context) error { return errors.New(reason) },
			Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New(reason)
			},
		})
	}
	allowedNames := []string{
		"filesystem_list", "filesystem_read", "filesystem_search", "filesystem_stat",
		"git_diff", "git_log", "git_show", "git_status",
		"web_fetch", "web_search",
	}
	immutable, err := swarm.NewToolSurface(allowedNames)
	if err != nil {
		return err
	}
	reduced := newReducedToolManager(capabilities.manager, immutable.Tools())
	if capabilities.supervisor != nil {
		capabilities.supervisor.SetExecutor(&liveSupervisorExecutor{
			generator: generator, model: model, manager: capabilities.manager,
			registry: capabilities.swarmRegistry,
		})
	}
	return capabilities.manager.Register(ctx, tools.Registration{
		Name:        "subagent_delegate",
		Description: "Delegate one bounded research or inspection task to a reduced sub-agent.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"required":["task"],
			"properties":{
				"task":{"type":"string","minLength":1,"maxLength":32768},
				"max_tool_calls":{"type":"integer","minimum":1,"maximum":32}
			},
			"additionalProperties":false
		}`),
		Timeout: 2 * time.Minute, Classification: tools.ClassificationYellow,
		Check: func(context.Context) error { return nil },
		Handler: func(runCtx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			var input struct {
				Task         string `json:"task"`
				MaxToolCalls int    `json:"max_tool_calls"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return nil, fmt.Errorf("subagent_delegate: invalid arguments: %w", err)
			}
			input.Task = strings.TrimSpace(input.Task)
			if input.Task == "" {
				return nil, fmt.Errorf("subagent_delegate: task is required")
			}
			if input.MaxToolCalls == 0 {
				input.MaxToolCalls = 16
			}
			scope, ok := controlplane.ApprovalScopeFromContext(runCtx)
			if !ok || scope.SessionID == nil {
				return nil, fmt.Errorf("subagent_delegate: authenticated turn scope is required")
			}
			parentID := "operator"
			if scope.TurnID != nil {
				parentID = scope.TurnID.String()
			}
			type outcome struct {
				content string
				tools   int
				err     error
			}
			finished := make(chan outcome, 1)
			agentView, err := capabilities.swarmRegistry.SpawnWorker(
				runCtx, parentID, scope.SessionID.String(), 1,
				swarm.ReducedSelfModel{
					ID:           "operator-reduced",
					Capabilities: immutable.Tools(),
					Limitations: []string{
						"no writes", "no shell", "no durable memory",
						"no consequential actions", "no recursive delegation",
					},
					Version: 1,
				},
				immutable,
				func(workerCtx context.Context, worker swarm.WorkerContext) (json.RawMessage, error) {
					childTools := NewScopedAgentToolManager(reduced, worker.AgentID)
					loop, loopErr := agent.NewLoop(generator, childTools, agent.LoopConfig{
						Model: model,
						SystemPrompt: "You are a bounded Ion sub-agent. " +
							"Complete only the delegated research or inspection task. " +
							"Do not claim effects outside your immutable tool surface.",
						UserID: "subagent", SessionID: scope.SessionID.String(),
						MaxToolCalls: input.MaxToolCalls,
					}, nil)
					if loopErr != nil {
						finished <- outcome{err: loopErr}
						return nil, loopErr
					}
					response, turnErr := loop.Turn(workerCtx, input.Task)
					finished <- outcome{
						content: response.Content, tools: len(response.ToolEvents),
						err: turnErr,
					}
					artifact, _ := json.Marshal(map[string]any{
						"content":    response.Content,
						"tool_count": len(response.ToolEvents),
					})
					return artifact, turnErr
				},
			)
			if err != nil {
				return nil, err
			}
			if err := capabilities.swarmRegistry.SetAssignment(
				agentView.ID,
				input.Task,
			); err != nil {
				capabilities.swarmRegistry.Abort(
					agentView.ID,
					scope.SessionID.String(),
				)
				return nil, err
			}
			select {
			case <-runCtx.Done():
				capabilities.swarmRegistry.Abort(agentView.ID, scope.SessionID.String())
				return nil, runCtx.Err()
			case result := <-finished:
				if result.err != nil {
					return nil, fmt.Errorf("subagent_delegate: %w", result.err)
				}
				payload, err := json.Marshal(map[string]any{
					"agent_id": agentView.ID, "content": result.content,
					"tool_count": result.tools, "state": "completed",
				})
				return payload, err
			}
		},
	})
}

type reducedToolManager struct {
	parent  *tools.Manager
	allowed map[string]struct{}
}

func newReducedToolManager(
	parent *tools.Manager,
	names []string,
) *reducedToolManager {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	return &reducedToolManager{parent: parent, allowed: allowed}
}

func (manager *reducedToolManager) Surface(
	ctx context.Context,
) []protocol.ToolDefinition {
	var result []protocol.ToolDefinition
	for _, definition := range manager.parent.Surface(ctx) {
		if _, ok := manager.allowed[definition.Name]; ok {
			result = append(result, definition)
		}
	}
	return result
}

func (manager *reducedToolManager) Execute(
	ctx context.Context,
	call protocol.NormalizedToolCall,
) (json.RawMessage, error) {
	if _, ok := manager.allowed[call.Name]; !ok {
		return nil, fmt.Errorf("subagent: tool %q is outside immutable surface", call.Name)
	}
	return manager.parent.Execute(ctx, call)
}

func (capabilities *productionCapabilities) ToolSurface(
	ctx context.Context,
) []tools.Status {
	statuses := capabilities.manager.Readiness(ctx)
	result := statuses[:0]
	for _, status := range statuses {
		if status.Ready {
			result = append(result, status)
		}
	}
	return result
}

func (capabilities *productionCapabilities) ToolReadiness(
	ctx context.Context,
) map[string]any {
	statuses := capabilities.manager.Readiness(ctx)
	ready := 0
	unavailable := 0
	for _, status := range statuses {
		if status.Ready {
			ready++
		} else {
			unavailable++
		}
	}
	return map[string]any{
		"ready": ready, "unavailable": unavailable,
		"cache": "30 second bounded probes", "tools": statuses,
	}
}

func (capabilities *productionCapabilities) ToolCommand(
	ctx context.Context,
	request controlplane.Request,
) (any, error) {
	if request.Operation != controlplane.OperationToolInvoke {
		return nil, fmt.Errorf("operator capabilities: unsupported tool operation %q", request.Operation)
	}
	var input struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := decodeStrictJSON(request.Payload, &input); err != nil {
		return nil, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return nil, fmt.Errorf("operator capabilities: tool name is required")
	}
	if len(input.Arguments) == 0 {
		input.Arguments = json.RawMessage(`{}`)
	}
	return capabilities.executeControlTool(
		ctx,
		request,
		input.Name,
		input.Arguments,
	)
}

func (capabilities *productionCapabilities) BrowserWorkflowQuery(
	ctx context.Context,
	operation controlplane.Operation,
	scope controlplane.Scope,
) (any, error) {
	if capabilities.browserWorkflows == nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "browser workflow service is unavailable",
		}
	}
	runCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: scope.ActorID, SessionID: scope.SessionID,
	})
	switch operation {
	case controlplane.OperationBrowserWorkflowList:
		return capabilities.browserWorkflows.List(runCtx)
	case controlplane.OperationBrowserCredentialList:
		return capabilities.browserWorkflows.Credentials(runCtx)
	default:
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "browser workflow query is unavailable",
		}
	}
}

func (capabilities *productionCapabilities) BrowserWorkflowCommand(
	ctx context.Context,
	request controlplane.Request,
) (any, error) {
	name := ""
	switch request.Operation {
	case controlplane.OperationBrowserWorkflowPause:
		name = "browser_workflow_pause"
	case controlplane.OperationBrowserWorkflowResume:
		name = "browser_workflow_resume"
	case controlplane.OperationBrowserWorkflowCancel:
		name = "browser_workflow_cancel"
	case controlplane.OperationBrowserWorkflowHandoff:
		name = "browser_request_handoff"
	default:
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "browser workflow operation is unavailable",
		}
	}
	return capabilities.executeControlTool(ctx, request, name, request.Payload)
}

func (capabilities *productionCapabilities) ContinuityBrief(
	ctx context.Context,
	scope controlplane.Scope,
	payload json.RawMessage,
) (any, error) {
	if capabilities.presence == nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "continuity service is unavailable",
		}
	}
	var input struct {
		Period string `json:"period"`
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if err := decodeStrictJSON(payload, &input); err != nil {
		return nil, err
	}
	return capabilities.presence.ReturnBrief(ctx, scope.ActorID, input.Period)
}

func (capabilities *productionCapabilities) MemoryCommand(
	ctx context.Context,
	request controlplane.Request,
) (any, error) {
	name := ""
	switch request.Operation {
	case controlplane.OperationMemoryPin:
		name = "memory_pin"
	case controlplane.OperationMemoryRecover:
		name = "memory_recover"
	default:
		return nil, controlplane.PublicError{
			Code:    controlplane.ErrorUnavailable,
			Message: "memory operation is not available",
		}
	}
	return capabilities.executeControlTool(ctx, request, name, request.Payload)
}

func (capabilities *productionCapabilities) executeControlTool(
	ctx context.Context,
	request controlplane.Request,
	name string,
	arguments json.RawMessage,
) (any, error) {
	if capabilities.manager == nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "tool service is unavailable",
			Retryable: true,
		}
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	runCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: request.Scope.ActorID, SessionID: request.Scope.SessionID,
	})
	runCtx = policy.WithPrincipal(runCtx, policy.Principal{
		Sender: policy.SenderUser, Profile: request.Scope.Profile,
	})
	runCtx = tools.WithIdempotencyScope(runCtx, scopeKey(request.Scope))
	callID := request.IdempotencyKey
	if strings.TrimSpace(callID) == "" {
		callID = request.RequestID.String()
	}
	result, err := capabilities.manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: callID, Name: name, Arguments: arguments,
	})
	if err != nil {
		return nil, controlToolPublicError(err)
	}
	return result, nil
}

func controlToolPublicError(err error) error {
	var validation *tools.ArgumentValidationError
	switch {
	case errors.As(err, &validation):
		return controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "operation payload is invalid",
		}
	case errors.Is(err, tools.ErrPolicyDenied):
		return controlplane.PublicError{
			Code: controlplane.ErrorForbidden, Message: "operation was denied by policy",
		}
	case errors.Is(err, tools.ErrIdempotencyConflict):
		return controlplane.PublicError{
			Code: controlplane.ErrorConflict, Message: "idempotency key conflicts with a prior operation",
		}
	case errors.Is(err, tools.ErrNotFound), errors.Is(err, tools.ErrUnavailable):
		return controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "operation is not available",
			Retryable: errors.Is(err, tools.ErrUnavailable),
		}
	case errors.Is(err, tools.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "operation timed out",
			Retryable: true,
		}
	default:
		return err
	}
}

func (capabilities *productionCapabilities) SwarmCommand(
	ctx context.Context,
	request controlplane.Request,
) (any, error) {
	if request.Operation != controlplane.OperationSwarmAbort ||
		capabilities.swarmRegistry == nil || capabilities.policy == nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorUnavailable, Message: "swarm operation is not available",
		}
	}
	if request.Scope.SessionID == nil || *request.Scope.SessionID == uuid.Nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "swarm abort requires a session scope",
		}
	}
	callID := request.IdempotencyKey
	if strings.TrimSpace(callID) == "" {
		callID = request.RequestID.String()
	}
	runCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: request.Scope.ActorID, SessionID: request.Scope.SessionID,
	})
	runCtx = policy.WithPrincipal(runCtx, policy.Principal{
		Sender: policy.SenderUser, Profile: request.Scope.Profile,
	})
	authorized, err := capabilities.policy.Authorize(runCtx, tools.Invocation{
		Call: protocol.NormalizedToolCall{
			ID: callID, Name: "swarm_abort",
			Arguments: append(json.RawMessage(nil), request.Payload...),
		},
		Description:    "Abort one active helper in the authenticated session.",
		Classification: tools.ClassificationYellow,
	})
	if err != nil {
		return nil, controlToolPublicError(
			fmt.Errorf("%w: %v", tools.ErrPolicyDenied, err),
		)
	}
	var input struct {
		AgentID       string           `json:"agent_id"`
		ExpectedState swarm.AgentState `json:"expected_state"`
	}
	if err := decodeStrictJSON(authorized.Arguments, &input); err != nil {
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "swarm abort payload is invalid",
		}
	}
	aborted, err := capabilities.swarmRegistry.AbortScoped(
		input.AgentID,
		request.Scope.SessionID.String(),
		input.ExpectedState,
	)
	switch {
	case errors.Is(err, swarm.ErrAgentNotFound):
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorNotFound, Message: "helper was not found",
		}
	case errors.Is(err, swarm.ErrAgentSessionMismatch):
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorUnauthorized, Message: "helper is outside this session",
		}
	case errors.Is(err, swarm.ErrAgentStateConflict):
		return nil, controlplane.PublicError{
			Code: controlplane.ErrorConflict, Message: "helper state changed before abort",
		}
	case err != nil:
		return nil, err
	default:
		return aborted, nil
	}
}

func (capabilities *productionCapabilities) SkillList(
	ctx context.Context,
) ([]skills.SkillSummary, error) {
	return capabilities.skills.Summaries(ctx)
}

func (capabilities *productionCapabilities) SkillLifecycle(
	ctx context.Context,
) (skills.Lifecycle, error) {
	return capabilities.skills.Lifecycle(ctx)
}

func (capabilities *productionCapabilities) SkillCommand(
	ctx context.Context,
	operation controlplane.Operation,
	payload json.RawMessage,
) (any, error) {
	switch operation {
	case controlplane.OperationSkillSave:
		var skill skills.Skill
		if err := decodeStrictJSON(payload, &skill); err != nil {
			return nil, err
		}
		path, err := capabilities.skills.Save(ctx, skill)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"saved": true, "bundle": filepath.Base(filepath.Dir(path)),
		}, nil
	case controlplane.OperationSkillRefine:
		var input struct {
			Name         string            `json:"name"`
			Steps        []string          `json:"steps"`
			Pitfalls     []string          `json:"pitfalls"`
			Verification []string          `json:"verification"`
			BodyNote     string            `json:"body_note"`
			Evidence     []skills.Evidence `json:"evidence"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.skills.Propose(ctx, input.Name, skills.Refinement{
			Steps: input.Steps, Pitfalls: input.Pitfalls,
			Verification: input.Verification, BodyNote: input.BodyNote,
		}, input.Evidence)
	case controlplane.OperationSkillRollback:
		var input struct {
			Name     string `json:"name"`
			Revision int    `json:"revision"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.skills.Rollback(ctx, input.Name, input.Revision)
	default:
		return nil, fmt.Errorf("operator capabilities: unsupported skill operation %q", operation)
	}
}

func decodeStrictJSON(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("operator capabilities: invalid payload: %w", err)
	}
	return nil
}

func (capabilities *productionCapabilities) WorkQuery(
	ctx context.Context,
	operation controlplane.Operation,
	scope controlplane.Scope,
	payload json.RawMessage,
) (any, error) {
	if capabilities.work == nil {
		return nil, fmt.Errorf("operator capabilities: disciplined work is unavailable")
	}
	switch operation {
	case controlplane.OperationWorkBrief:
		return capabilities.work.Brief(ctx, scope.ActorID, scope.SessionID)
	case controlplane.OperationArtifactList:
		portfolio, err := capabilities.work.Get(ctx, scope.ActorID)
		if err != nil {
			return nil, err
		}
		return portfolio.Artifacts, nil
	case controlplane.OperationAutonomyGet:
		portfolio, err := capabilities.work.Get(ctx, scope.ActorID)
		if err != nil {
			return nil, err
		}
		return portfolio.Autonomy, nil
	case controlplane.OperationWorkflowList:
		portfolio, err := capabilities.work.Get(ctx, scope.ActorID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"recipes": workcontrol.Recipes(), "runs": portfolio.Workflows}, nil
	case controlplane.OperationReviewPlan:
		var input workcontrol.ReviewInput
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return workcontrol.Review(input), nil
	case controlplane.OperationSupervisorList:
		if capabilities.supervisor == nil {
			return nil, fmt.Errorf("operator capabilities: supervisor is unavailable")
		}
		return capabilities.supervisor.List(ctx, scope.ActorID)
	case controlplane.OperationSupervisorGet:
		if capabilities.supervisor == nil {
			return nil, fmt.Errorf("operator capabilities: supervisor is unavailable")
		}
		var input struct {
			RunID uuid.UUID `json:"run_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.supervisor.Get(ctx, scope.ActorID, input.RunID)
	default:
		return nil, fmt.Errorf("operator capabilities: unsupported work query %q", operation)
	}
}

func (capabilities *productionCapabilities) WorkCommand(
	ctx context.Context,
	operation controlplane.Operation,
	scope controlplane.Scope,
	payload json.RawMessage,
) (any, error) {
	if capabilities.work == nil {
		return nil, fmt.Errorf("operator capabilities: disciplined work is unavailable")
	}
	switch operation {
	case controlplane.OperationWorkContractPut:
		var input workcontrol.ContractInput
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		if input.SessionID != nil && (scope.SessionID == nil || *input.SessionID != *scope.SessionID) {
			return nil, fmt.Errorf("operator capabilities: contract session does not match request scope")
		}
		if input.SessionID == nil {
			input.SessionID = scope.SessionID
		}
		return capabilities.work.PutContract(ctx, scope.ActorID, input)
	case controlplane.OperationWorkComplete:
		var input struct {
			ContractID uuid.UUID `json:"contract_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ContractID == uuid.Nil {
			return nil, fmt.Errorf("operator capabilities: valid contract_id is required")
		}
		return capabilities.work.CompleteContract(ctx, scope.ActorID, input.ContractID)
	case controlplane.OperationArtifactRecord:
		var input workcontrol.ArtifactInput
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.work.RecordArtifact(ctx, scope.ActorID, input)
	case controlplane.OperationArtifactVerify:
		var input struct {
			ArtifactID uuid.UUID `json:"artifact_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil || input.ArtifactID == uuid.Nil {
			return nil, fmt.Errorf("operator capabilities: valid artifact_id is required")
		}
		return capabilities.work.VerifyArtifact(ctx, scope.ActorID, input.ArtifactID)
	case controlplane.OperationAutonomyUpdate:
		var input workcontrol.AutonomySettings
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.work.UpdateAutonomy(ctx, scope.ActorID, input)
	case controlplane.OperationWorkflowStart:
		var input struct {
			RecipeID   string    `json:"recipe_id"`
			ContractID uuid.UUID `json:"contract_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.work.StartWorkflow(ctx, scope.ActorID, input.RecipeID, input.ContractID)
	case controlplane.OperationWorkflowAdvance:
		var input workcontrol.WorkflowAdvanceInput
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.work.AdvanceWorkflow(ctx, scope.ActorID, input)
	case controlplane.OperationSupervisorStart:
		if capabilities.supervisor == nil {
			return nil, fmt.Errorf("operator capabilities: supervisor is unavailable")
		}
		var input workcontrol.SupervisorStartInput
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		input.SessionID = scope.SessionID
		return capabilities.supervisor.Start(ctx, scope.ActorID, input)
	case controlplane.OperationSupervisorSteer:
		if capabilities.supervisor == nil {
			return nil, fmt.Errorf("operator capabilities: supervisor is unavailable")
		}
		var input struct {
			RunID       uuid.UUID `json:"run_id"`
			Instruction string    `json:"instruction"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.supervisor.Steer(
			ctx, scope.ActorID, input.RunID, input.Instruction,
		)
	case controlplane.OperationSupervisorCancel:
		if capabilities.supervisor == nil {
			return nil, fmt.Errorf("operator capabilities: supervisor is unavailable")
		}
		var input struct {
			RunID uuid.UUID `json:"run_id"`
		}
		if err := decodeStrictJSON(payload, &input); err != nil {
			return nil, err
		}
		return capabilities.supervisor.Cancel(ctx, scope.ActorID, input.RunID)
	default:
		return nil, fmt.Errorf("operator capabilities: unsupported work command %q", operation)
	}
}

type pluginProjection struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Version string `json:"version,omitempty"`
}

func (capabilities *productionCapabilities) PluginList(
	ctx context.Context,
) []pluginProjection {
	entries, err := os.ReadDir(capabilities.pluginRoot)
	if err != nil {
		return []pluginProjection{{
			Name: "installed plugins", Status: "unavailable", Reason: err.Error(),
		}}
	}
	result := make([]pluginProjection, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			break
		}
		if entry.IsDir() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension != ".yaml" && extension != ".yml" {
			continue
		}
		bundle, err := plugin.LoadBundle(
			ctx, filepath.Join(capabilities.pluginRoot, entry.Name()),
			capabilities.pluginSDK,
		)
		if err != nil {
			result = append(result, pluginProjection{
				Name: entry.Name(), Status: "unavailable", Reason: err.Error(),
			})
			continue
		}
		result = append(result, pluginProjection{
			Name: bundle.Name, Version: bundle.Version, Status: "ready",
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func (capabilities *productionCapabilities) MCPTools(
	ctx context.Context,
) []tools.Status {
	var result []tools.Status
	for _, status := range capabilities.manager.Readiness(ctx) {
		if strings.HasPrefix(status.Name, "mcp_") && status.Name != "mcp_invoke" {
			result = append(result, status)
		}
	}
	return result
}

func (capabilities *productionCapabilities) ChannelList() []channelProjection {
	if capabilities.channels == nil {
		return []channelProjection{{
			Name: "Telegram", Priority: "first_class", Status: "starting",
			Transport: "HTTPS long polling",
			Session:   "shared production agent path",
		}}
	}
	return capabilities.channels.List()
}

func (capabilities *productionCapabilities) ChannelHealth() map[string]any {
	if capabilities.channels == nil {
		return map[string]any{
			"configured": 0, "healthy": 0,
			"primary_external_channel": "telegram", "status": "starting",
		}
	}
	return capabilities.channels.Health()
}

func (capabilities *productionCapabilities) ChannelCommand(
	ctx context.Context,
	operation controlplane.Operation,
	payload json.RawMessage,
) (any, error) {
	if capabilities.channels == nil {
		return nil, fmt.Errorf("operator capabilities: channels are unavailable")
	}
	var input struct {
		UpdateID int64 `json:"update_id"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.UpdateID < 0 {
		return nil, fmt.Errorf("operator capabilities: valid update_id is required")
	}
	switch operation {
	case controlplane.OperationChannelRetry:
		state, err := capabilities.channels.RetryTelegramUpdate(ctx, input.UpdateID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"update_id": state.UpdateID, "status": state.Status,
			"attempts": state.Attempts, "failure_code": state.FailureCode,
		}, nil
	case controlplane.OperationChannelSkip:
		state, err := capabilities.channels.SkipTelegramUpdate(ctx, input.UpdateID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"update_id": state.UpdateID, "status": state.Status,
			"attempts": state.Attempts,
		}, nil
	default:
		return nil, fmt.Errorf("operator capabilities: unsupported channel command")
	}
}

func (capabilities *productionCapabilities) ScheduleState(
	actorID uuid.UUID,
) any {
	result := make([]any, 0)
	if capabilities.presence != nil {
		for _, schedule := range capabilities.presence.Schedules() {
			result = append(result, map[string]any{
				"name": schedule.Name, "interval": schedule.Interval,
				"status": schedule.Status, "last_attempt": schedule.LastAttempt,
				"last_success": schedule.LastSuccess, "next_due": schedule.NextDue,
				"last_error": schedule.LastError, "summary": schedule.Summary,
				"source": "internal_presence",
			})
		}
	}
	if capabilities.scheduler != nil {
		result = append(result, capabilities.scheduler.Health())
		for _, alarm := range capabilities.scheduler.Projections(actorID) {
			result = append(result, alarm)
		}
	}
	if len(result) == 0 {
		return []any{}
	}
	return result
}

func (capabilities *productionCapabilities) AutomatrixState(
	actorID uuid.UUID,
) any {
	if capabilities.presence == nil {
		return []any{}
	}
	return capabilities.presence.Automatrix(actorID)
}

func (capabilities *productionCapabilities) AutomatrixCommand(
	ctx context.Context,
	operation controlplane.Operation,
	actorID uuid.UUID,
	payload json.RawMessage,
) (any, error) {
	if capabilities.presence == nil {
		return nil, fmt.Errorf("operator capabilities: Automatrix is unavailable")
	}
	var input struct {
		ItemID  uuid.UUID           `json:"item_id"`
		Actions []automatrix.Action `json:"actions"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.ItemID == uuid.Nil {
		return nil, fmt.Errorf("operator capabilities: valid Automatrix item_id is required")
	}
	switch operation {
	case controlplane.OperationAutomatrixApprove:
		return capabilities.presence.ApproveAutomatrix(
			ctx, actorID, input.ItemID, input.Actions,
		)
	case controlplane.OperationAutomatrixReject:
		if len(input.Actions) != 0 {
			return nil, fmt.Errorf("operator capabilities: rejection cannot include actions")
		}
		if err := capabilities.presence.RejectAutomatrix(
			ctx, actorID, input.ItemID,
		); err != nil {
			return nil, err
		}
		return map[string]any{"item_id": input.ItemID, "status": "rejected"}, nil
	default:
		return nil, fmt.Errorf("operator capabilities: unsupported Automatrix command")
	}
}

func (capabilities *productionCapabilities) CuriosityState() any {
	if capabilities.presence == nil {
		return []any{}
	}
	return capabilities.presence.Curiosity()
}

func (capabilities *productionCapabilities) IntegrityState() any {
	if capabilities.presence == nil {
		return map[string]any{"status": "starting"}
	}
	return capabilities.presence.LatestIntegrity()
}

func (capabilities *productionCapabilities) PresenceCommand(
	ctx context.Context,
	operation controlplane.Operation,
	payload json.RawMessage,
) (any, error) {
	if capabilities.presence == nil {
		return nil, fmt.Errorf("operator presence: production supervisor is unavailable")
	}
	name := ""
	switch operation {
	case controlplane.OperationIntegrityRun:
		name = "weekly_integrity"
	case controlplane.OperationScheduleUpdate:
		var request struct {
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		if err := json.Unmarshal(payload, &request); err != nil ||
			request.Action != "run_now" {
			return nil, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "schedule.update requires name and action=run_now",
			}
		}
		name = request.Name
	default:
		return nil, fmt.Errorf("operator presence: unsupported command")
	}
	return capabilities.presence.RunNow(ctx, name)
}

func (capabilities *productionCapabilities) SwarmState(sessionID string) map[string]any {
	if capabilities.swarmRegistry == nil {
		return map[string]any{"status": "unavailable", "active": 0}
	}
	return map[string]any{
		"status": "ready", "active": capabilities.swarmRegistry.ActiveCount(),
		"global_limit": swarm.GlobalLaneMax, "session_limit": swarm.SessionLaneMax,
		"parent_limit": swarm.ParentLaneMax, "recursive_spawning": false,
		"agents": capabilities.swarmRegistry.Snapshot(sessionID),
	}
}

func (capabilities *productionCapabilities) CognitionState(
	ctx context.Context,
	sessionID uuid.UUID,
) (cognitionSnapshot, error) {
	if capabilities.sessions == nil || sessionID == uuid.Nil {
		return cognitionSnapshot{}, fmt.Errorf(
			"operator capabilities: scoped cognition is unavailable",
		)
	}
	raw, err := capabilities.sessions.LoadCognitionState(ctx, sessionID)
	if err != nil {
		return cognitionSnapshot{}, err
	}
	var snapshot cognitionSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return cognitionSnapshot{}, fmt.Errorf(
			"operator capabilities: decode cognition projection: %w", err,
		)
	}
	return snapshot, nil
}

func (capabilities *productionCapabilities) RecoveryState(
	ctx context.Context,
	sessionID uuid.UUID,
) ([]session.TurnState, error) {
	if capabilities.sessions == nil {
		return nil, fmt.Errorf("operator capabilities: turn recovery is unavailable")
	}
	return capabilities.sessions.RecentTurnStates(ctx, sessionID, 24)
}

func (capabilities *productionCapabilities) PolicyEvents(
	ctx context.Context,
) (any, error) {
	if capabilities.auditor == nil {
		return nil, fmt.Errorf("operator capabilities: policy evidence is unavailable")
	}
	return capabilities.auditor.Events(ctx, 256)
}

func (capabilities *productionCapabilities) QueryTool(
	ctx context.Context,
	scope controlplane.Scope,
	name string,
	arguments json.RawMessage,
) any {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	runCtx := controlplane.WithApprovalScope(ctx, controlplane.ApprovalScope{
		ActorID: scope.ActorID, SessionID: scope.SessionID,
	})
	runCtx = policy.WithPrincipal(runCtx, policy.Principal{
		Sender: policy.SenderUser, Profile: scope.Profile,
	})
	result, err := capabilities.manager.Execute(runCtx, protocol.NormalizedToolCall{
		ID: "operator-query-" + name, Name: name, Arguments: arguments,
	})
	if err != nil {
		return map[string]any{"status": "unavailable", "reason": err.Error()}
	}
	return result
}

func (capabilities *productionCapabilities) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"workspace": capabilities.workspaceRoot,
		"tools":     capabilities.manager.Readiness(context.Background()),
	})
}

type allowAnomalyDetector struct{}

func (allowAnomalyDetector) Observe(context.Context, policy.Request) error { return nil }

type redactingPolicyAuditor struct {
	inner policy.Auditor
}

func (auditor redactingPolicyAuditor) RecordPolicyEvent(
	ctx context.Context,
	event policy.AuditEvent,
) error {
	event.Arguments = redactToolArguments(event.ToolName, event.Arguments)
	return auditor.inner.RecordPolicyEvent(ctx, event)
}

func redactToolArguments(toolName string, arguments json.RawMessage) json.RawMessage {
	if toolName == "shell_execute" {
		return json.RawMessage(`{"arguments":"redacted","reason":"command arguments may contain secrets"}`)
	}
	if toolName == "browser_navigate" {
		return json.RawMessage(`{"url":"[REDACTED]"}`)
	}
	var value any
	if err := json.Unmarshal(arguments, &value); err != nil {
		return json.RawMessage(`{"arguments":"redacted"}`)
	}
	if toolName == "browser_interact" {
		if object, ok := value.(map[string]any); ok {
			if _, exists := object["value"]; exists {
				object["value"] = "[REDACTED]"
			}
		}
	}
	redactJSONValue(value)
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > 16<<10 {
		return json.RawMessage(`{"arguments":"redacted"}`)
	}
	return payload
}

func redactJSONValue(value any) {
	switch found := value.(type) {
	case map[string]any:
		for key, child := range found {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "content" || normalized == "old_text" ||
				normalized == "new_text" || strings.Contains(normalized, "secret") ||
				strings.Contains(normalized, "password") ||
				strings.Contains(normalized, "token") ||
				strings.Contains(normalized, "api_key") ||
				normalized == "authorization" {
				found[key] = "[REDACTED]"
				continue
			}
			redactJSONValue(child)
		}
	case []any:
		for _, child := range found {
			redactJSONValue(child)
		}
	}
}

// unconfiguredChannels is a real SDK boundary that reports the absence of
// configured external connectors instead of routing messages anywhere.
type unconfiguredChannels struct{}

func (unconfiguredChannels) Register(gateway.Connector) error {
	return fmt.Errorf("operator capabilities: external channels are not configured")
}
