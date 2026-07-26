package skills_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/paxlabs-inc/ion-agent/internal/skills"
)

func TestSkillAcceptanceSaveLoadRecurrenceAndRefinement(t *testing.T) {
	ctx := context.Background()
	store, err := skills.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.Save(ctx, skills.Skill{
		Name:    "Repair flaky integration tests",
		Trigger: "flaky integration test",
		Steps: []string{
			"Reproduce with a fixed seed",
			"Inspect the real external boundary",
		},
		Pitfalls: []string{
			"Do not replace the failing boundary with a detector stub",
		},
		Verification: []string{
			"Run the test repeatedly with the race detector",
		},
		Body: "Captured after resolving a nondeterministic provider test.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/SKILL.md") {
		t.Fatalf("skill path = %q", path)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"name:", "trigger:", "steps:", "pitfalls:", "verification:",
	} {
		if !strings.Contains(string(payload), field) {
			t.Fatalf("SKILL.md missing %s:\n%s", field, payload)
		}
	}

	loaded, err := store.Load(ctx, "Repair flaky integration tests")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 || loaded.Uses != 0 || len(loaded.Steps) != 2 {
		t.Fatalf("loaded skill = %+v", loaded)
	}
	matched, err := store.Match(
		ctx,
		"The flaky integration test returned after the dependency restart",
	)
	if err != nil {
		t.Fatal(err)
	}
	if matched == nil || matched.Name != loaded.Name || matched.Uses != 1 {
		t.Fatalf("recurrence match = %+v", matched)
	}
	refined, err := store.Refine(ctx, loaded.Name, skills.Refinement{
		Steps:        []string{"Restart the dependency between attempts"},
		Pitfalls:     []string{"A cached readiness result can hide recovery"},
		Verification: []string{"Prove restart replay from durable state"},
		BodyNote:     "Refined after the second successful use.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if refined.Revision != 2 || refined.Uses != 1 ||
		len(refined.Steps) != 3 ||
		len(refined.Pitfalls) != 2 ||
		len(refined.Verification) != 2 ||
		!strings.Contains(refined.Body, "second successful use") {
		t.Fatalf("refined skill = %+v", refined)
	}
	reloaded, err := store.Load(ctx, loaded.Name)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != refined.Revision ||
		reloaded.Uses != refined.Uses ||
		len(reloaded.Steps) != len(refined.Steps) {
		t.Fatalf("reloaded refinement = %+v, want %+v", reloaded, refined)
	}
}
