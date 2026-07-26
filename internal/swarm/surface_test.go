package swarm

import "testing"

func TestToolSurfaceIsReducedAndImmutable(t *testing.T) {
	t.Parallel()
	source := []string{"git_diff", "file_read", "git_diff"}
	surface, err := NewToolSurface(source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = "memory_read"
	first := surface.Tools()
	first[0] = "agent_spawn"
	second := surface.Tools()
	if len(second) != 2 || second[0] != "file_read" || second[1] != "git_diff" {
		t.Fatalf("surface mutated: %v", second)
	}
	for _, forbidden := range []string{
		"memory_read",
		"memory_write",
		"memory_delete",
		"memory_export",
		"agent_spawn",
	} {
		if _, err := NewToolSurface([]string{forbidden}); err == nil {
			t.Fatalf("forbidden tool %s accepted", forbidden)
		}
	}
}

func TestSpawnBindsReducedModelAndSurfaceDefensively(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(&testClock{baseTime})
	model := ReducedSelfModel{
		ID: "parent", Capabilities: []string{"read"}, Limitations: []string{"no-vault"},
	}
	surface, err := NewToolSurface([]string{"file_read"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := registry.SpawnReduced("", "session", 1, model, surface)
	if err != nil {
		t.Fatal(err)
	}
	model.Capabilities[0] = "memory_read"
	got := agent.Tools()
	got[0] = "agent_spawn"
	if agent.Model.Capabilities[0] != "read" || agent.Tools()[0] != "file_read" {
		t.Fatalf("spawn authority mutated: model=%+v tools=%v", agent.Model, agent.Tools())
	}
}
