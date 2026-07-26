package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// EventType is the closed lifecycle event set delivered to every code plugin.
type EventType string

const (
	EventSessionStart EventType = "on_session_start"
	EventToolCall     EventType = "on_tool_call"
	EventMessage      EventType = "on_message"
	EventError        EventType = "on_error"
)

// Event is safe to serialize to process plugins.
type Event struct {
	Type      EventType       `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func (event Event) Validate() error {
	switch event.Type {
	case EventSessionStart, EventToolCall, EventMessage, EventError:
	default:
		return fmt.Errorf("plugin: invalid lifecycle event %q", event.Type)
	}
	if len(event.Payload) > 0 && !json.Valid(event.Payload) {
		return fmt.Errorf("plugin: lifecycle payload must be valid JSON")
	}
	return nil
}

// CodePlugin is implemented by in-process Go plugins and by ProcessPlugin for
// Python or other executable modules.
type CodePlugin interface {
	Name() string
	Start(context.Context, SDK) error
	Hook(context.Context, Event) error
	Stop(context.Context) error
}

type pluginState struct {
	plugin  CodePlugin
	started bool
}

// Host owns plugin lifecycle. Calls are serialized per host so hook ordering is
// exactly registration order and Stop is the reverse of Start.
type Host struct {
	mu      sync.Mutex
	sdk     SDK
	plugins map[string]*pluginState
	order   []string
	started bool
}

func NewHost(sdk SDK) (*Host, error) {
	if err := sdk.Validate(); err != nil {
		return nil, err
	}
	return &Host{sdk: sdk, plugins: make(map[string]*pluginState)}, nil
}

func (host *Host) Register(plugin CodePlugin) error {
	if plugin == nil || strings.TrimSpace(plugin.Name()) == "" {
		return fmt.Errorf("plugin: named code plugin is required")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.started {
		return fmt.Errorf("plugin: cannot register after host start")
	}
	name := strings.TrimSpace(plugin.Name())
	if _, exists := host.plugins[name]; exists {
		return fmt.Errorf("plugin: duplicate code plugin %q", name)
	}
	host.plugins[name] = &pluginState{plugin: plugin}
	host.order = append(host.order, name)
	sort.Strings(host.order)
	return nil
}

func (host *Host) Start(ctx context.Context) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.started {
		return nil
	}
	var started []string
	for _, name := range host.order {
		state := host.plugins[name]
		if err := state.plugin.Start(ctx, host.sdk); err != nil {
			rollbackErr := host.stopLocked(ctx, started)
			return errors.Join(fmt.Errorf("plugin: start %s: %w", name, err), rollbackErr)
		}
		state.started = true
		started = append(started, name)
	}
	host.started = true
	return nil
}

func (host *Host) Dispatch(ctx context.Context, event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if !host.started {
		return fmt.Errorf("plugin: host is not started")
	}
	for _, name := range host.order {
		if err := host.plugins[name].plugin.Hook(ctx, cloneEvent(event)); err != nil {
			return fmt.Errorf("plugin: hook %s: %w", name, err)
		}
	}
	return nil
}

func (host *Host) Stop(ctx context.Context) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	if !host.started {
		return nil
	}
	err := host.stopLocked(ctx, host.order)
	host.started = false
	return err
}

func (host *Host) stopLocked(ctx context.Context, names []string) error {
	var failures []error
	for index := len(names) - 1; index >= 0; index-- {
		state := host.plugins[names[index]]
		if !state.started {
			continue
		}
		if err := state.plugin.Stop(ctx); err != nil {
			failures = append(failures, fmt.Errorf("plugin: stop %s: %w", names[index], err))
		}
		state.started = false
	}
	return errors.Join(failures...)
}

func cloneEvent(event Event) Event {
	event.Payload = cloneJSON(event.Payload)
	return event
}
