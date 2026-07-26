//go:build integration

package operatorapp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestProductionRuntimeUsesBrowsingMachineBeforePageFetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory:        dataDirectory,
		DevelopmentFileKEK:   true,
		WorkspaceDirectory:   t.TempDir(),
		ProjectWorkspaceRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.config.SearchEndpoint != "https://browsingmachine.com/" {
		t.Fatalf("production search endpoint = %q", runtime.config.SearchEndpoint)
	}
	result, err := runtime.capabilityRoot.manager.Execute(
		ctx,
		protocol.NormalizedToolCall{
			ID:   "production-browsingmachine-news",
			Name: "web_search",
			Arguments: json.RawMessage(
				`{"query":"Virtual crypto network Virtuals Protocol","limit":8}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var normalized struct {
		Source       string   `json:"source"`
		State        string   `json:"state"`
		Category     string   `json:"category"`
		ResultCount  int      `json:"result_count"`
		FallbackFrom string   `json:"fallback_from"`
		Attempted    []string `json:"attempted_categories"`
	}
	if err := json.Unmarshal(result, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized.Source != "searxng" ||
		normalized.State != "ready" ||
		normalized.ResultCount == 0 {
		t.Fatalf("production SearXNG result = %+v", normalized)
	}
	if normalized.Category == "general" {
		if normalized.FallbackFrom != "" ||
			len(normalized.Attempted) != 1 ||
			normalized.Attempted[0] != "general" {
			t.Fatalf("healthy general search metadata = %+v", normalized)
		}
	} else if normalized.Category == "news" {
		if normalized.FallbackFrom != "general" ||
			len(normalized.Attempted) != 2 ||
			normalized.Attempted[0] != "general" ||
			normalized.Attempted[1] != "news" {
			t.Fatalf("news fallback metadata = %+v", normalized)
		}
	} else {
		t.Fatalf("unexpected production category = %+v", normalized)
	}
}
