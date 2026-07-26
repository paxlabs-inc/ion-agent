package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/paxlabs-inc/ion-agent/internal/presence/gateway"
	"github.com/paxlabs-inc/ion-agent/internal/provider"
	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// ChannelRegistrar is implemented by the production multi-channel gateway.
type ChannelRegistrar interface {
	Register(gateway.Connector) error
}

// ProviderRegistry is the mutable adapter catalog used before constructing or
// reloading a provider pool.
type ProviderRegistry struct {
	mu       sync.RWMutex
	adapters map[string]provider.ProviderAdapter
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{adapters: make(map[string]provider.ProviderAdapter)}
}

func (registry *ProviderRegistry) Register(adapter provider.ProviderAdapter) error {
	if adapter == nil || strings.TrimSpace(adapter.Name()) == "" {
		return fmt.Errorf("plugin: named provider adapter is required")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	name := strings.TrimSpace(adapter.Name())
	if _, exists := registry.adapters[name]; exists {
		return fmt.Errorf("plugin: duplicate provider adapter %q", name)
	}
	registry.adapters[name] = adapter
	return nil
}

func (registry *ProviderRegistry) Get(name string) (provider.ProviderAdapter, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	adapter, exists := registry.adapters[name]
	return adapter, exists
}

func (registry *ProviderRegistry) Names() []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	names := make([]string, 0, len(registry.adapters))
	for name := range registry.adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Hook receives a memory or session lifecycle event.
type Hook func(context.Context, json.RawMessage) error

// HookBus is an ordered, concurrency-safe lifecycle hook registry.
type HookBus struct {
	mu    sync.RWMutex
	hooks map[string]Hook
}

func NewHookBus() *HookBus { return &HookBus{hooks: make(map[string]Hook)} }

func (bus *HookBus) Register(name string, hook Hook) error {
	name = strings.TrimSpace(name)
	if name == "" || hook == nil {
		return fmt.Errorf("plugin: named hook is required")
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if _, exists := bus.hooks[name]; exists {
		return fmt.Errorf("plugin: duplicate hook %q", name)
	}
	bus.hooks[name] = hook
	return nil
}

func (bus *HookBus) Emit(ctx context.Context, payload json.RawMessage) error {
	if len(payload) > 0 && !json.Valid(payload) {
		return fmt.Errorf("plugin: hook payload must be valid JSON")
	}
	bus.mu.RLock()
	names := make([]string, 0, len(bus.hooks))
	hooks := make(map[string]Hook, len(bus.hooks))
	for name, hook := range bus.hooks {
		names = append(names, name)
		hooks[name] = hook
	}
	bus.mu.RUnlock()
	sort.Strings(names)
	for _, name := range names {
		if err := hooks[name](ctx, cloneJSON(payload)); err != nil {
			return fmt.Errorf("plugin: hook %s: %w", name, err)
		}
	}
	return nil
}

// SecurityFlow exposes the same authorization boundary used by the concrete
// tool manager; plugins do not receive a bypass.
type SecurityFlow struct {
	policy tools.ExecutionPolicy
}

func NewSecurityFlow(policy tools.ExecutionPolicy) (*SecurityFlow, error) {
	if policy == nil {
		return nil, fmt.Errorf("plugin: security policy is required")
	}
	return &SecurityFlow{policy: policy}, nil
}

func (flow *SecurityFlow) Authorize(
	ctx context.Context,
	invocation tools.Invocation,
) (protocol.NormalizedToolCall, error) {
	return flow.policy.Authorize(ctx, invocation)
}

// NewSDK builds the complete six-domain capability object.
func NewSDK(
	channels ChannelRegistrar,
	toolManager *tools.Manager,
	executionPolicy tools.ExecutionPolicy,
) (SDK, error) {
	security, err := NewSecurityFlow(executionPolicy)
	if err != nil {
		return SDK{}, err
	}
	sdk := SDK{
		Surface: NewSurface(), Channels: channels,
		Providers: NewProviderRegistry(), Tools: toolManager,
		MemoryHooks: NewHookBus(), SessionHooks: NewHookBus(),
		Security: security,
	}
	if err := sdk.Validate(); err != nil {
		return SDK{}, err
	}
	return sdk, nil
}
