package swarm

import (
	"fmt"
	"sort"
	"strings"
)

var forbiddenSubAgentTools = map[string]struct{}{
	"memory_read":   {},
	"memory_write":  {},
	"memory_delete": {},
	"memory_export": {},
	"agent_spawn":   {},
}

// ToolSurface is immutable after construction and excludes authority-bearing
// tools. Tools returns a defensive copy, so a child cannot expand its surface.
type ToolSurface struct {
	names []string
}

func NewToolSurface(names []string) (ToolSurface, error) {
	seen := make(map[string]struct{}, len(names))
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return ToolSurface{}, fmt.Errorf("swarm: tool name is required")
		}
		if _, forbidden := forbiddenSubAgentTools[name]; forbidden {
			return ToolSurface{}, fmt.Errorf("swarm: tool %s is forbidden to sub-agents", name)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		allowed = append(allowed, name)
	}
	sort.Strings(allowed)
	return ToolSurface{names: allowed}, nil
}

func (surface ToolSurface) Tools() []string {
	return append([]string(nil), surface.names...)
}
