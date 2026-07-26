package plugin_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/plugin"
	"github.com/paxlabs-inc/ion-agent/internal/presence/gateway"
	"github.com/paxlabs-inc/ion-agent/internal/security/policy"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type lifecyclePlugin struct {
	mu     sync.Mutex
	events []plugin.EventType
}

func (code *lifecyclePlugin) Name() string { return "acceptance-code-plugin" }

func (code *lifecyclePlugin) Start(ctx context.Context, sdk plugin.SDK) error {
	code.record("start")
	if err := sdk.Channels.Register(&testConnector{}); err != nil {
		return err
	}
	if err := sdk.Providers.Register(testAdapter{}); err != nil {
		return err
	}
	if err := sdk.MemoryHooks.Register("acceptance-memory", func(context.Context, json.RawMessage) error {
		code.record("memory_hook")
		return nil
	}); err != nil {
		return err
	}
	if err := sdk.SessionHooks.Register("acceptance-session", func(context.Context, json.RawMessage) error {
		code.record("session_hook")
		return nil
	}); err != nil {
		return err
	}
	if _, err := sdk.Security.Authorize(ctx, tools.Invocation{
		Call: protocol.NormalizedToolCall{
			ID: "security-flow", Name: "plugin_probe", Arguments: json.RawMessage(`{}`),
		},
		Classification: tools.ClassificationGreen,
	}); err != nil {
		return err
	}
	return sdk.Tools.Register(ctx, tools.Registration{
		Name: "plugin_echo", Description: "echo through a code plugin",
		Parameters:     json.RawMessage(`{"type":"object"}`),
		Classification: tools.ClassificationGreen,
		Check:          func(context.Context) error { return nil },
		Handler: func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			return append(json.RawMessage(nil), arguments...), nil
		},
	})
}

func (code *lifecyclePlugin) Hook(_ context.Context, event plugin.Event) error {
	code.record(event.Type)
	return nil
}

func (code *lifecyclePlugin) Stop(context.Context) error {
	code.record("stop")
	return nil
}

func (code *lifecyclePlugin) record(event plugin.EventType) {
	code.mu.Lock()
	defer code.mu.Unlock()
	code.events = append(code.events, event)
}

func (code *lifecyclePlugin) snapshot() []plugin.EventType {
	code.mu.Lock()
	defer code.mu.Unlock()
	return append([]plugin.EventType(nil), code.events...)
}

func TestPluginAcceptanceAPISurfaceLifecycleAndBundleComposition(t *testing.T) {
	ctx := context.Background()
	sdk, _ := newRuntime(t)
	surface := sdk.Surface
	catalog := surface.Catalog()
	if len(catalog) != 720 {
		t.Fatalf("SDK API count = %d, want 720", len(catalog))
	}
	domains := map[plugin.Domain]bool{}
	for _, api := range catalog {
		domains[api.Domain] = true
	}
	for _, domain := range []plugin.Domain{
		plugin.DomainChannel, plugin.DomainProvider, plugin.DomainTool,
		plugin.DomainMemory, plugin.DomainSession, plugin.DomainSecurity,
	} {
		if !domains[domain] {
			t.Fatalf("SDK catalog missing %s domain", domain)
		}
		name := "v1." + string(domain) + "." + firstResource(domain) + ".create"
		if _, err := surface.Invoke(ctx, name, plugin.Request{
			Key: "acceptance", Value: json.RawMessage(`{"enabled":true}`),
		}); err != nil {
			t.Fatalf("invoke %s: %v", name, err)
		}
	}

	host, err := plugin.NewHost(sdk)
	if err != nil {
		t.Fatal(err)
	}
	code := &lifecyclePlugin{}
	if err := host.Register(code); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, exists := sdk.Providers.Get("acceptance-provider"); !exists {
		t.Fatal("code plugin provider was not registered")
	}
	if err := sdk.MemoryHooks.Emit(ctx, json.RawMessage(`{"memory":"stored"}`)); err != nil {
		t.Fatal(err)
	}
	if err := sdk.SessionHooks.Emit(ctx, json.RawMessage(`{"session":"started"}`)); err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []plugin.EventType{
		plugin.EventSessionStart, plugin.EventMessage,
		plugin.EventToolCall, plugin.EventError,
	} {
		if err := host.Dispatch(ctx, plugin.Event{
			Type: eventType, SessionID: "session-a", Payload: json.RawMessage(`{"ok":true}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	gotEvents := code.snapshot()
	if len(gotEvents) != 8 || gotEvents[0] != "start" ||
		gotEvents[len(gotEvents)-1] != "stop" {
		t.Fatalf("lifecycle events = %v", gotEvents)
	}

	manifest := t.TempDir() + "/plugin.yaml"
	if err := os.WriteFile(manifest, []byte(
		"name: acceptance-bundle\n"+
			"version: \"1.0.0\"\n"+
			"tools:\n"+
			"  - plugin_echo\n"+
			"channels:\n"+
			"  - telegram\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := plugin.LoadBundle(ctx, manifest, sdk)
	if err != nil {
		t.Fatal(err)
	}
	if got := bundle.Surface(ctx); len(got) != 1 || got[0].Name != "plugin_echo" {
		t.Fatalf("bundle surface = %+v", got)
	}
	result, err := bundle.Execute(ctx, protocol.NormalizedToolCall{
		ID: "bundle-call", Name: "plugin_echo", Arguments: json.RawMessage(`{"value":7}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"value":7}` {
		t.Fatalf("bundle result = %s", result)
	}
	if _, err := bundle.Execute(ctx, protocol.NormalizedToolCall{
		ID: "escape", Name: "not_selected", Arguments: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("bundle allowed a tool outside its manifest")
	}
}

func TestProcessCodePluginReceivesRealLifecycle(t *testing.T) {
	if os.Getenv("ION_PROCESS_PLUGIN_HELPER") == "1" {
		runProcessPluginHelper()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sdk, _ := newRuntime(t)
	host, err := plugin.NewHost(sdk)
	if err != nil {
		t.Fatal(err)
	}
	process := &plugin.ProcessPlugin{
		PluginName: "external-code-plugin",
		Command:    []string{os.Args[0], "-test.run=TestProcessCodePluginReceivesRealLifecycle"},
		Env:        []string{"ION_PROCESS_PLUGIN_HELPER=1"},
	}
	if err := host.Register(process); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Dispatch(ctx, plugin.Event{
		Type: plugin.EventMessage, SessionID: "external-session",
		Payload: json.RawMessage(`{"text":"hello"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func runProcessPluginHelper() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			Method string        `json:"method"`
			Event  *plugin.Event `json:"event"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			_ = encoder.Encode(map[string]any{"ok": false, "error": "invalid request"})
			continue
		}
		ok := request.Method == "start" || request.Method == "stop" ||
			(request.Method == "hook" && request.Event != nil &&
				request.Event.Type == plugin.EventMessage)
		_ = encoder.Encode(map[string]any{"ok": ok})
		if request.Method == "stop" {
			return
		}
	}
}

func firstResource(domain plugin.Domain) string {
	switch domain {
	case plugin.DomainChannel:
		return "accounts"
	case plugin.DomainProvider:
		return "adapters"
	case plugin.DomainTool:
		return "annotations"
	case plugin.DomainMemory:
		return "activation"
	case plugin.DomainSession:
		return "activation"
	case plugin.DomainSecurity:
		return "anomalies"
	default:
		panic(fmt.Sprintf("unknown domain %s", domain))
	}
}

type noOpCore struct{}

func (noOpCore) Respond(context.Context, gateway.Turn) (string, error) {
	return "ok", nil
}

type staticSoul struct{}

func (staticSoul) Load(context.Context) (string, error) { return "# SOUL", nil }

type testConnector struct{}

func (*testConnector) Platform() gateway.Platform                   { return gateway.Telegram }
func (*testConnector) Send(context.Context, gateway.Outbound) error { return nil }

type testAdapter struct{}

func (testAdapter) Name() string { return "acceptance-provider" }
func (testAdapter) TranslateRequest(protocol.GenerationRequest) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (testAdapter) TranslateResponse(json.RawMessage) (protocol.NormalizedGeneration, error) {
	return protocol.NormalizedGeneration{}, nil
}
func (testAdapter) TranslateStreamEvent([]byte) (protocol.StreamChunk, error) {
	return protocol.StreamChunk{}, nil
}

func newRuntime(t *testing.T) (plugin.SDK, *tools.Manager) {
	t.Helper()
	allow := func(name policy.LayerName) policy.Layer {
		return policy.LayerFunc{
			LayerName: name,
			EvaluateFunc: func(context.Context, policy.Request) (policy.Result, error) {
				return policy.Result{Decision: policy.Allow}, nil
			},
		}
	}
	pipeline, err := policy.New(
		types.SystemClock{}, &policy.MemoryAuditor{},
		allow(policy.SandboxLayer), allow(policy.ProfileLayer),
		allow(policy.ProviderLayer), allow(policy.SenderLayer),
		allow(policy.GroupLayer),
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tools.NewManager(types.SystemClock{}, tools.WithExecutionPolicy(pipeline))
	if err != nil {
		t.Fatal(err)
	}
	channelGateway, err := gateway.New(
		noOpCore{}, []byte("0123456789abcdef0123456789abcdef"), staticSoul{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := plugin.NewSDK(channelGateway, manager, pipeline)
	if err != nil {
		t.Fatal(err)
	}
	return sdk, manager
}

func TestSDKCatalogNamesAreUniqueAndStable(t *testing.T) {
	catalog := plugin.NewSurface().Catalog()
	seen := make(map[string]struct{}, len(catalog))
	for _, api := range catalog {
		if !strings.HasPrefix(api.Name, "v1.") {
			t.Fatalf("unversioned API: %s", api.Name)
		}
		if _, exists := seen[api.Name]; exists {
			t.Fatalf("duplicate API: %s", api.Name)
		}
		seen[api.Name] = struct{}{}
	}
}
