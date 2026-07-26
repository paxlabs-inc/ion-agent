package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// Bundle is the safe declarative plugin form. It composes existing tool names;
// it cannot inject handlers or bypass the tool manager's policy pipeline.
type Bundle struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Tools       []string `json:"tools"`
	Providers   []string `json:"providers,omitempty"`
	Channels    []string `json:"channels,omitempty"`
	sdk         SDK
}

// LoadBundle parses the intentionally small YAML manifest schema and verifies
// every composed tool against the live readiness-filtered registry.
func LoadBundle(ctx context.Context, path string, sdk SDK) (*Bundle, error) {
	if err := sdk.Validate(); err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("plugin: read bundle: %w", err)
	}
	bundle, err := decodeBundle(string(payload))
	if err != nil {
		return nil, err
	}
	available := make(map[string]struct{})
	for _, definition := range sdk.Tools.Surface(ctx) {
		available[definition.Name] = struct{}{}
	}
	for _, name := range bundle.Tools {
		if _, exists := available[name]; !exists {
			return nil, fmt.Errorf("plugin: bundle tool %q is not ready", name)
		}
	}
	bundle.sdk = sdk
	return &bundle, nil
}

// Surface returns only the selected live tool definitions.
func (bundle *Bundle) Surface(ctx context.Context) []protocol.ToolDefinition {
	selected := make(map[string]struct{}, len(bundle.Tools))
	for _, name := range bundle.Tools {
		selected[name] = struct{}{}
	}
	var result []protocol.ToolDefinition
	for _, definition := range bundle.sdk.Tools.Surface(ctx) {
		if _, ok := selected[definition.Name]; ok {
			result = append(result, definition)
		}
	}
	return result
}

// Execute routes through the real registry, including readiness, policy, rail,
// timeout, and output validation.
func (bundle *Bundle) Execute(
	ctx context.Context,
	call protocol.NormalizedToolCall,
) (json.RawMessage, error) {
	allowed := false
	for _, name := range bundle.Tools {
		if call.Name == name {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("plugin: tool %q is outside bundle %q", call.Name, bundle.Name)
	}
	return bundle.sdk.Tools.Execute(ctx, call)
}

func decodeBundle(payload string) (Bundle, error) {
	var bundle Bundle
	scanner := bufio.NewScanner(strings.NewReader(payload))
	var list *[]string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") && list != nil {
			value, err := parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			if err != nil {
				return Bundle{}, err
			}
			*list = append(*list, value)
			continue
		}
		list = nil
		key, raw, ok := strings.Cut(trimmed, ":")
		if !ok {
			return Bundle{}, fmt.Errorf("plugin: invalid bundle YAML line %q", line)
		}
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		scalar := func() (string, error) {
			value, scalarErr := parseYAMLScalar(raw)
			if scalarErr != nil {
				return "", fmt.Errorf("plugin: bundle field %s: %w", key, scalarErr)
			}
			return value, nil
		}
		var scalarErr error
		switch key {
		case "name":
			bundle.Name, scalarErr = scalar()
		case "version":
			bundle.Version, scalarErr = scalar()
		case "description":
			bundle.Description, scalarErr = scalar()
		case "tools":
			list = &bundle.Tools
		case "providers":
			list = &bundle.Providers
		case "channels":
			list = &bundle.Channels
		default:
			return Bundle{}, fmt.Errorf("plugin: unknown bundle field %q", key)
		}
		if scalarErr != nil {
			return Bundle{}, scalarErr
		}
	}
	if err := scanner.Err(); err != nil {
		return Bundle{}, err
	}
	bundle.Name = strings.TrimSpace(bundle.Name)
	bundle.Version = strings.TrimSpace(bundle.Version)
	bundle.Tools = uniqueStrings(bundle.Tools)
	bundle.Providers = uniqueStrings(bundle.Providers)
	bundle.Channels = uniqueStrings(bundle.Channels)
	if bundle.Name == "" || bundle.Version == "" || len(bundle.Tools) == 0 {
		return Bundle{}, fmt.Errorf("plugin: bundle name, version, and tools are required")
	}
	return bundle, nil
}

func parseYAMLScalar(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if raw[0] == '"' || raw[0] == '\'' {
		if raw[0] == '\'' {
			if len(raw) < 2 || raw[len(raw)-1] != '\'' {
				return "", fmt.Errorf("plugin: invalid quoted YAML scalar")
			}
			return strings.ReplaceAll(raw[1:len(raw)-1], "''", "'"), nil
		}
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("plugin: invalid YAML scalar: %w", err)
		}
		return value, nil
	}
	return raw, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
