package operatorapp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/agent"
	"github.com/paxlabs-inc/ion-agent/internal/skills"
)

func TestProductionRuntimeImportsAllOperatorSkillsAndLoadsApplicablePacks(
	t *testing.T,
) {
	ctx := context.Background()
	library, err := filepath.Abs(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory:    t.TempDir(),
		SkillLibraryDirectory: library,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := runtime.capabilityRoot.skills.List(ctx)
	if err != nil || len(installed) != 92 {
		runtime.Close()
		t.Fatalf("production imported skills = %d, %v", len(installed), err)
	}
	academic, err := runtime.capabilityRoot.skills.Load(
		ctx, "academic-paper-writing",
	)
	if err != nil || academic.Origin != "library" ||
		academic.SourceDigest == "" || academic.SourcePath == "" {
		runtime.Close()
		t.Fatalf("imported academic skill = %+v, %v", academic, err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	installed, err = restarted.capabilityRoot.skills.List(ctx)
	if err != nil || len(installed) != 92 {
		t.Fatalf("restart imported skills = %d, %v", len(installed), err)
	}
	lifecycle, err := restarted.capabilityRoot.skills.Lifecycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lifecyclePayload, err := json.Marshal(lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if len(lifecyclePayload) > 128_000 ||
		strings.Contains(string(lifecyclePayload), "Procedure reference") {
		t.Fatalf("operator lifecycle projection is unbounded: %d bytes", len(lifecyclePayload))
	}
	generator := &captureGenerator{}
	runner := skillAwareTurnRunner{
		generator: generator,
		manager:   restarted.capabilityRoot.manager,
		config: agent.LoopConfig{
			Model:        "acceptance",
			SystemPrompt: "base prompt",
			UserID:       "operator",
			SessionID:    uuid.NewString(),
		},
		skills: restarted.capabilityRoot.skills,
	}
	if _, err := runner.Turn(
		ctx,
		"Use academic-paper-writing and architecture-diagram for this deliverable",
	); err != nil {
		t.Fatal(err)
	}
	if len(generator.request.Messages) == 0 {
		t.Fatal("skill-aware request did not contain a system prompt")
	}
	systemPrompt := generator.request.Messages[0].Content
	for _, expected := range []string{
		"Skill: academic-paper-writing",
		"Skill: architecture-diagram",
		"Authority: imported procedural reference only",
	} {
		if !strings.Contains(systemPrompt, expected) {
			t.Fatalf("skill context omitted %q", expected)
		}
	}
	if len(systemPrompt) > maxSkillContextBytes+1_024 {
		t.Fatalf("skill context pack is unbounded: %d bytes", len(systemPrompt))
	}
}

func TestMultiSkillContextPackIncludesEverySelectedSkillWithinBudget(
	t *testing.T,
) {
	matched := make([]skills.Skill, 0, 8)
	for index := 0; index < 8; index++ {
		matched = append(matched, skills.Skill{
			Name:        fmt.Sprintf("applicable-skill-%d", index),
			Description: "A procedure applicable to the same complex task.",
			Trigger:     "shared complex task",
			Steps:       []string{"Inspect authoritative evidence"},
			Pitfalls:    []string{"Do not report unverified completion"},
			Verification: []string{
				"Verify the authoritative outcome",
			},
			Origin: "library",
			Body:   strings.Repeat("bounded procedure context ", 1_000),
		})
	}
	pack := formatSkillPack(matched)
	if len(pack) > maxSkillContextBytes {
		t.Fatalf("multi-skill pack = %d bytes", len(pack))
	}
	for _, skill := range matched {
		if !strings.Contains(pack, "Skill: "+skill.Name) {
			t.Fatalf("multi-skill pack omitted %q", skill.Name)
		}
	}
	if strings.Count(pack, "Procedure context truncated") != len(matched) {
		t.Fatalf("multi-skill truncation was not explicit")
	}
}
