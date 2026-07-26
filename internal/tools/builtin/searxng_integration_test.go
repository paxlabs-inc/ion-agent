//go:build integration

package builtin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

func TestBrowsingMachineLiveSearXNGNewsPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	manager := newManager(t, ctx, Config{
		Workspace:             t.TempDir(),
		SearXNGSearchEndpoint: "https://browsingmachine.com/",
	})
	result, err := manager.Execute(ctx, protocol.NormalizedToolCall{
		ID:   "browsingmachine-news",
		Name: "web_search",
		Arguments: json.RawMessage(
			`{"query":"Virtual crypto network Virtuals Protocol","limit":8}`,
		),
	})
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
		Results      []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(result, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized.Source != "searxng" ||
		normalized.State != "ready" ||
		normalized.ResultCount == 0 ||
		len(normalized.Results) == 0 ||
		normalized.Results[0].URL == "" ||
		normalized.Results[0].Title == "" {
		t.Fatalf("live SearXNG news result = %+v", normalized)
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
		t.Fatalf("unexpected live category = %+v", normalized)
	}
}
