package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/paxlabs-inc/ion-agent/internal/skills"
)

func TestOperatorLibraryDiscoversInstallsAndSelectsEverySourceBundle(
	t *testing.T,
) {
	ctx := context.Background()
	library, err := filepath.Abs(filepath.Join("..", "..", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := skills.DiscoverLibrary(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	sourceCount, err := countSourceSkills(library)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != sourceCount {
		t.Fatalf("discovered bundles = %d, want every source bundle (%d)", len(bundles), sourceCount)
	}
	resourceCount := 0
	for _, bundle := range bundles {
		if bundle.Skill.Name == "" || bundle.Skill.Trigger == "" ||
			bundle.Skill.SourceDigest == "" || bundle.Skill.SourcePath == "" ||
			bundle.Skill.Origin != "library" {
			t.Fatalf("incomplete normalized bundle = %+v", bundle.Skill)
		}
		expectedResources, err := sourceResources(bundle.Directory)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(bundle.Skill.Resources, expectedResources) {
			t.Fatalf(
				"incomplete resources for %q = %+v, want %+v",
				bundle.Skill.Name, bundle.Skill.Resources, expectedResources,
			)
		}
		resourceCount += len(bundle.Skill.Resources)
	}
	if resourceCount == 0 {
		t.Fatal("operator library contained no in-bundle resources")
	}
	normalized := make(map[string]skills.Skill, len(bundles))
	for _, bundle := range bundles {
		normalized[bundle.Skill.Name] = bundle.Skill
	}
	jamdesk := normalized["jamdesk-docs"]
	if jamdesk.Trigger != "jamdesk" ||
		!containsString(jamdesk.Aliases, "docs site") {
		t.Fatalf("block-list triggers were not normalized: %+v", jamdesk)
	}
	computerUseSource := normalized["computer-use"]
	if computerUseSource.Category != "desktop" ||
		!containsString(computerUseSource.Aliases, "automation") {
		t.Fatalf("nested source metadata was not normalized: %+v", computerUseSource)
	}
	openHue := normalized["openhue"]
	if !containsString(openHue.RequiredTools, "shell_execute") {
		t.Fatalf("source command compatibility was not normalized: %+v", openHue)
	}

	store, err := skills.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.ImportLibrary(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if report.Discovered != sourceCount || report.Installed != sourceCount ||
		report.Unchanged != 0 || report.Conflicts != 0 {
		t.Fatalf("first import = %+v", report)
	}
	repeated, err := store.ImportLibrary(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Installed != 0 || repeated.Unchanged != sourceCount ||
		repeated.Conflicts != 0 {
		t.Fatalf("repeated import = %+v", repeated)
	}
	installed, err := store.List(ctx)
	if err != nil || len(installed) != sourceCount {
		t.Fatalf("installed skills = %d, %v", len(installed), err)
	}

	for _, candidate := range installed {
		platform := "linux"
		if len(candidate.Platforms) > 0 {
			platform = candidate.Platforms[0]
		}
		available := make(map[string]struct{})
		for _, tool := range candidate.RequiredTools {
			available[strings.ToLower(tool)] = struct{}{}
		}
		found, err := store.MatchAll(
			ctx,
			"Use the "+candidate.Name+" skill for this request",
			skills.MatchContext{Platform: platform, Tools: available},
			5,
		)
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, item := range found {
			if item.Name == candidate.Name {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("%q was not selected from %+v", candidate.Name, found)
		}
	}

	computerUse, err := store.Load(ctx, "computer-use")
	if err != nil {
		t.Fatal(err)
	}
	if len(computerUse.RequiredTools) != 2 {
		t.Fatalf("computer-use requirements = %+v", computerUse.RequiredTools)
	}
	if _, err := os.Stat(filepath.Join(
		store.BundlePath(computerUse.Name), "SKILL.md",
	)); err != nil {
		t.Fatalf("copied source bundle: %v", err)
	}
	unavailable, err := store.MatchAll(
		ctx, "Use computer-use for this desktop task",
		skills.MatchContext{
			Platform: "linux",
			Tools:    map[string]struct{}{"web_search": {}},
		},
		3,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range unavailable {
		if item.Name == computerUse.Name {
			t.Fatalf("unavailable computer skill matched: %+v", unavailable)
		}
	}
}

func countSourceSkills(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && entry.Name() == "SKILL.md" {
			count++
		}
		return nil
	})
	return count, err
}

func sourceResources(root string) ([]string, error) {
	var resources []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative != "SKILL.md" {
			resources = append(resources, relative)
		}
		return nil
	})
	sort.Strings(resources)
	return resources, err
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
