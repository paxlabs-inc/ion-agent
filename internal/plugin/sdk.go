// Package plugin implements the versioned extension SDK, plugin lifecycle, and
// declarative bundle loading.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/paxlabs-inc/ion-agent/internal/tools"
)

const APIVersion = "v1"

// Domain is one of the binding extension areas from requirement 29.
type Domain string

const (
	DomainChannel  Domain = "channel"
	DomainProvider Domain = "provider"
	DomainTool     Domain = "tool"
	DomainMemory   Domain = "memory"
	DomainSession  Domain = "session"
	DomainSecurity Domain = "security"
)

var domainResources = map[Domain][]string{
	DomainChannel: {
		"accounts", "adapters", "attachments", "capabilities", "commands",
		"delivery", "formatters", "inbound", "mentions", "messages",
		"outbound", "presence", "reactions", "receipts", "routing",
		"scopes", "streams", "threads", "typing", "webhooks",
	},
	DomainProvider: {
		"adapters", "auth", "catalogs", "credentials", "embeddings",
		"fallbacks", "generation", "health", "limits", "models",
		"pricing", "prompts", "routing", "streaming", "telemetry",
		"tokens", "transcription", "translation", "usage", "vision",
	},
	DomainTool: {
		"annotations", "arguments", "availability", "bundles", "calls",
		"cancellation", "classification", "definitions", "discovery", "errors",
		"execution", "handlers", "idempotency", "output", "policy",
		"readiness", "receipts", "registration", "schemas", "timeouts",
	},
	DomainMemory: {
		"activation", "citations", "connections", "dreams", "events",
		"forgetting", "fts", "hooks", "journal", "memories",
		"metadata", "premises", "provenance", "query", "replay",
		"salience", "search", "tombstones", "types", "versions",
	},
	DomainSession: {
		"activation", "children", "compression", "context", "exports",
		"history", "hooks", "identity", "imports", "isolation",
		"messages", "metadata", "origins", "routing", "search",
		"sessions", "splits", "state", "transcript", "usage",
	},
	DomainSecurity: {
		"anomalies", "approvals", "audit", "canaries", "capabilities",
		"classification", "credentials", "decisions", "encryption", "flows",
		"groups", "permissions", "policy", "profiles", "rate_limits",
		"receipts", "sandbox", "senders", "ssrf", "vault",
	},
}

var apiActions = [...]string{"create", "upsert", "get", "list", "delete", "count"}

// API describes one stable, callable SDK operation.
type API struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Domain   Domain `json:"domain"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// Request is the common wire contract for all SDK operations. Create, upsert,
// get, and delete require Key. Create/upsert also require valid JSON Value.
type Request struct {
	Key   string          `json:"key,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// Response is deterministic and safe to cross a process boundary.
type Response struct {
	Value  json.RawMessage            `json:"value,omitempty"`
	Values map[string]json.RawMessage `json:"values,omitempty"`
	Count  int                        `json:"count,omitempty"`
}

var (
	ErrAPINotFound = errors.New("plugin: API not found")
	ErrConflict    = errors.New("plugin: resource already exists")
	ErrNotFound    = errors.New("plugin: resource not found")
)

// Surface owns the complete SDK catalog and extension-scoped resource state.
// Its operations are deliberately uniform so every catalog entry is callable,
// serializable, and testable instead of being a documentation-only export.
type Surface struct {
	mu      sync.RWMutex
	catalog map[string]API
	values  map[string]map[string]json.RawMessage
}

func NewSurface() *Surface {
	surface := &Surface{
		catalog: make(map[string]API, 720),
		values:  make(map[string]map[string]json.RawMessage),
	}
	for _, domain := range []Domain{
		DomainChannel, DomainProvider, DomainTool,
		DomainMemory, DomainSession, DomainSecurity,
	} {
		for _, resource := range domainResources[domain] {
			for _, action := range apiActions {
				api := API{
					Name:    APIVersion + "." + string(domain) + "." + resource + "." + action,
					Version: APIVersion, Domain: domain, Resource: resource, Action: action,
				}
				surface.catalog[api.Name] = api
			}
		}
	}
	return surface
}

// Catalog returns all 720 APIs in stable lexical order.
func (surface *Surface) Catalog() []API {
	surface.mu.RLock()
	defer surface.mu.RUnlock()
	result := make([]API, 0, len(surface.catalog))
	for _, api := range surface.catalog {
		result = append(result, api)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// Invoke executes any catalog operation.
func (surface *Surface) Invoke(
	ctx context.Context,
	name string,
	request Request,
) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	surface.mu.Lock()
	defer surface.mu.Unlock()
	api, ok := surface.catalog[name]
	if !ok {
		return Response{}, fmt.Errorf("%w: %s", ErrAPINotFound, name)
	}
	namespace := string(api.Domain) + "." + api.Resource
	records := surface.values[namespace]
	if records == nil {
		records = make(map[string]json.RawMessage)
		surface.values[namespace] = records
	}
	key := strings.TrimSpace(request.Key)
	switch api.Action {
	case "create":
		if key == "" || !json.Valid(request.Value) {
			return Response{}, fmt.Errorf("plugin: create requires key and valid JSON value")
		}
		if _, exists := records[key]; exists {
			return Response{}, fmt.Errorf("%w: %s", ErrConflict, key)
		}
		records[key] = cloneJSON(request.Value)
		return Response{Value: cloneJSON(request.Value)}, nil
	case "upsert":
		if key == "" || !json.Valid(request.Value) {
			return Response{}, fmt.Errorf("plugin: upsert requires key and valid JSON value")
		}
		records[key] = cloneJSON(request.Value)
		return Response{Value: cloneJSON(request.Value)}, nil
	case "get":
		value, exists := records[key]
		if key == "" || !exists {
			return Response{}, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return Response{Value: cloneJSON(value)}, nil
	case "list":
		values := make(map[string]json.RawMessage, len(records))
		for itemKey, value := range records {
			values[itemKey] = cloneJSON(value)
		}
		return Response{Values: values, Count: len(values)}, nil
	case "delete":
		if _, exists := records[key]; key == "" || !exists {
			return Response{}, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		delete(records, key)
		return Response{}, nil
	case "count":
		return Response{Count: len(records)}, nil
	default:
		panic("plugin: catalog contains an unsupported action")
	}
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

// SDK is the capability object provided to code plugins. Tools is the concrete
// production registry; Surface is the complete versioned extension API.
type SDK struct {
	Surface      *Surface
	Channels     ChannelRegistrar
	Providers    *ProviderRegistry
	Tools        *tools.Manager
	MemoryHooks  *HookBus
	SessionHooks *HookBus
	Security     *SecurityFlow
}

func (sdk SDK) Validate() error {
	if sdk.Surface == nil || sdk.Channels == nil || sdk.Providers == nil ||
		sdk.Tools == nil || sdk.MemoryHooks == nil || sdk.SessionHooks == nil ||
		sdk.Security == nil {
		return fmt.Errorf("plugin: all six SDK domain bridges are required")
	}
	return nil
}
