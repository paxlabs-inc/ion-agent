package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/tools"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

type allowPolicy struct{}

func (allowPolicy) Authorize(
	_ context.Context,
	invocation tools.Invocation,
) (protocol.NormalizedToolCall, error) {
	return invocation.Call, nil
}

func TestFilesystemToolsConfineTraversalAndSymlinkEscapes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	manager := newManager(t, ctx, Config{Workspace: workspace})

	for _, arguments := range []string{
		`{"path":"../secret.txt"}`,
		`{"path":"escape/secret.txt"}`,
	} {
		_, err := manager.Execute(ctx, protocol.NormalizedToolCall{
			ID: "escape", Name: "filesystem_read",
			Arguments: json.RawMessage(arguments),
		})
		if err == nil {
			t.Fatalf("filesystem_read(%s) escaped workspace", arguments)
		}
	}
	if _, err := manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "write-escape", Name: "filesystem_write",
		Arguments: json.RawMessage(`{"path":"escape/created.txt","content":"bad"}`),
	}); err == nil {
		t.Fatal("filesystem_write followed an escaping parent symlink")
	}
}

func TestFilesystemWritePatchAndShellAreBoundedProductionHandlers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace := t.TempDir()
	manager := newManager(t, ctx, Config{Workspace: workspace})

	execute := func(name, arguments string) json.RawMessage {
		t.Helper()
		result, err := manager.Execute(ctx, protocol.NormalizedToolCall{
			ID: name, Name: name, Arguments: json.RawMessage(arguments),
		})
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return result
	}
	execute("filesystem_write", `{"path":"note.txt","content":"before"}`)
	execute("filesystem_patch", `{"path":"note.txt","old_text":"before","new_text":"after"}`)
	content, err := os.ReadFile(filepath.Join(workspace, "note.txt"))
	if err != nil || string(content) != "after" {
		t.Fatalf("patched content = %q, %v", content, err)
	}
	result := execute("shell_execute", `{"command":"sh","args":["-c","pwd; printf done"],"timeout_seconds":5}`)
	if !strings.Contains(string(result), workspace) || !strings.Contains(string(result), "done") {
		t.Fatalf("shell result = %s", result)
	}
}

func TestShellExecuteNormalizesQuotedCommandAndStringArguments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager := newManager(t, ctx, Config{Workspace: t.TempDir()})
	execute := func(id string, arguments string) string {
		t.Helper()
		result, err := manager.Execute(ctx, protocol.NormalizedToolCall{
			ID: id, Name: "shell_execute", Arguments: json.RawMessage(arguments),
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(result)
	}
	quoted := execute(
		"quoted-command",
		`{"command":"printf '%s:%s' 'hello world' done"}`,
	)
	if !strings.Contains(quoted, "hello world:done") {
		t.Fatalf("quoted command result = %s", quoted)
	}
	stringArgs := execute(
		"string-args",
		`{"command":"printf","args":"'%s/%s' left right"}`,
	)
	if !strings.Contains(stringArgs, "left/right") {
		t.Fatalf("string args result = %s", stringArgs)
	}
	_, err := manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "bad-quotes", Name: "shell_execute",
		Arguments: json.RawMessage(`{"command":"printf 'unterminated"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "command line is invalid") {
		t.Fatalf("invalid quoted command error = %v", err)
	}
}

func TestWorkspaceToolsRepairCommonPathAndShellIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace := t.TempDir()
	manager := newManager(t, ctx, Config{Workspace: workspace})
	execute := func(id, name, arguments string) string {
		t.Helper()
		result, err := manager.Execute(ctx, protocol.NormalizedToolCall{
			ID: id, Name: name, Arguments: json.RawMessage(arguments),
		})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return string(result)
	}
	execute("nested", "filesystem_write", `{"path":"src/app/api/incidents/[id]/route.ts","content":"ok"}`)
	if content, err := os.ReadFile(filepath.Join(workspace, "src/app/api/incidents/[id]/route.ts")); err != nil || string(content) != "ok" {
		t.Fatalf("nested write = %q, %v", content, err)
	}
	absoluteDirectory := filepath.Join(workspace, "src", "app")
	absArgs, _ := json.Marshal(map[string]any{
		"command": "pwd", "working_directory": absoluteDirectory,
	})
	if result := execute("absolute", "shell_execute", string(absArgs)); !strings.Contains(result, absoluteDirectory) {
		t.Fatalf("absolute workdir result = %s", result)
	}
	if result := execute("cd-chain", "shell_execute", `{"command":"cd src/app && pwd"}`); !strings.Contains(result, absoluteDirectory) {
		t.Fatalf("cd normalization result = %s", result)
	}
	if result := execute("cd-only", "shell_execute", `{"command":"cd src/app"}`); !strings.Contains(result, "corrected_intent") {
		t.Fatalf("cd correction result = %s", result)
	}
	if result := execute("missing", "shell_execute", `{"command":"definitely-not-a-real-executable"}`); !strings.Contains(result, "executable_not_found") {
		t.Fatalf("missing executable result = %s", result)
	}
	execute("source", "filesystem_write", `{"path":"source.txt","content":"workspace only"}`)
	outside := filepath.Join(t.TempDir(), "escaped.txt")
	escapeArgs, _ := json.Marshal(map[string]any{
		"command": "cp", "args": []string{"source.txt", outside},
	})
	result, err := manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "reject-process-escape", Name: "shell_execute", Arguments: escapeArgs,
	})
	if err == nil || !strings.Contains(err.Error(), "outside the registered workspace") {
		t.Fatalf("absolute process escape = %s, %v", result, err)
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("process wrote outside the workspace: %v", err)
	}
}

func TestMemorySaveSchemaNamesEveryMemoryType(t *testing.T) {
	t.Parallel()
	var parameters json.RawMessage
	for _, registration := range memoryTools(nil) {
		if registration.Name == "memory_save" {
			parameters = registration.Parameters
			break
		}
	}
	if len(parameters) == 0 {
		t.Fatal("memory_save registration is missing")
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(parameters, &schema); err != nil {
		t.Fatal(err)
	}
	description := schema.Properties["type"].Description
	for _, required := range []string{"0x01 Identity", "0x02 Fact", "0x09 Pattern"} {
		if !strings.Contains(description, required) {
			t.Fatalf("memory type description %q does not contain %q", description, required)
		}
	}
}

func TestMemorySearchAllowsBoundedOverviewWithoutQuery(t *testing.T) {
	t.Parallel()
	var parameters json.RawMessage
	for _, registration := range memoryTools(nil) {
		if registration.Name == "memory_search" {
			parameters = registration.Parameters
			break
		}
	}
	if len(parameters) == 0 {
		t.Fatal("memory_search registration is missing")
	}
	if err := tools.ValidateArguments(
		parameters, json.RawMessage(`{"limit":24}`),
	); err != nil {
		t.Fatalf("memory overview arguments rejected: %v", err)
	}
	if err := tools.ValidateArguments(
		parameters, json.RawMessage(`{"query":"","limit":24}`),
	); err == nil {
		t.Fatal("explicitly empty search query was accepted")
	}
}

func TestNormalizeSearXNGResultsReturnsBoundedRankedMetadata(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"query":"latest agent news",
		"unresponsive_engines":[["startpage","CAPTCHA"]],
		"results":[
			{
				"url":"https://example.com/first",
				"title":" First result ",
				"content":" Useful source snippet. ",
				"publishedDate":"2026-07-24",
				"engine":"brave",
				"engines":["brave","bing"],
				"score":4.5
			},
			{
				"url":"javascript:alert(1)",
				"title":"Unsafe result",
				"content":"must be excluded",
				"score":4
			},
			{
				"url":"https://example.org/second",
				"title":"Second result",
				"content":"Second snippet",
				"engine":"google",
				"score":3
			}
		]
	}`)
	normalized, err := normalizeSearXNGResults(
		body, "fallback query", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if normalized["query"] != "latest agent news" ||
		normalized["source"] != "searxng" ||
		normalized["result_count"] != 2 ||
		normalized["state"] != "ready" {
		t.Fatalf("normalized metadata = %+v", normalized)
	}
	results, ok := normalized["results"].([]rankedSearchResult)
	if !ok || len(results) != 2 {
		t.Fatalf("normalized results = %#v", normalized["results"])
	}
	if results[0].Title != "First result" ||
		results[0].Snippet != "Useful source snippet." ||
		results[1].URL != "https://example.org/second" {
		t.Fatalf("ranked results = %+v", results)
	}
	degraded, err := normalizeSearXNGResults(
		[]byte(`{
			"query":"latest agent news",
			"results":[],
			"unresponsive_engines":[
				["brave","Suspended: too many requests"],
				["duckduckgo","CAPTCHA"]
			]
		}`),
		"fallback query", 8,
	)
	if err != nil {
		t.Fatal(err)
	}
	if degraded["state"] != "degraded" ||
		degraded["result_count"] != 0 {
		t.Fatalf("degraded search metadata = %+v", degraded)
	}
}

func TestNormalizeTavilyResultsReturnsResearchMetadata(t *testing.T) {
	t.Parallel()
	normalized, err := normalizeTavilyResults([]byte(`{
		"query":"agent research",
		"answer":"A source-backed overview.",
		"response_time":"0.81",
		"request_id":"request-1",
		"results":[
			{
				"url":"https://example.com/research",
				"title":"Primary research",
				"content":"Observed source excerpt.",
				"published_date":"2026-07-26",
				"score":0.91
			},
			{
				"url":"javascript:alert(1)",
				"title":"Unsafe",
				"content":"Excluded",
				"score":1
			}
		]
	}`), "fallback query", 8)
	if err != nil {
		t.Fatal(err)
	}
	if normalized["query"] != "agent research" ||
		normalized["answer"] != "A source-backed overview." ||
		normalized["provider"] != "tavily" ||
		normalized["result_count"] != 1 ||
		normalized["state"] != "ready" {
		t.Fatalf("normalized Tavily metadata = %+v", normalized)
	}
	results, ok := normalized["results"].([]rankedSearchResult)
	if !ok || len(results) != 1 ||
		results[0].Source != "example.com" ||
		results[0].PublishedDate != "2026-07-26" {
		t.Fatalf("normalized Tavily results = %#v", normalized["results"])
	}
}

func TestSearchCategoryRoutesCurrentResearchThroughSearXNGNews(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		"latest AI and agent news",
		"recent model releases",
		"AI news today",
	} {
		if got := searchCategory(query, ""); got != "news" {
			t.Fatalf("searchCategory(%q) = %q, want news", query, got)
		}
	}
	if got := searchCategory("OpenAI API documentation", ""); got != "general" {
		t.Fatalf("ordinary research category = %q, want general", got)
	}
	if got := searchCategory("latest AI news", "general"); got != "general" {
		t.Fatalf("explicit general override = %q", got)
	}
}

func TestSearchFallbackCategoryRepairsUnavailableGeneralEngines(t *testing.T) {
	t.Parallel()
	if category, ok := searchFallbackCategory("general", "", 0); !ok ||
		category != "news" {
		t.Fatalf("general fallback = %q, %t", category, ok)
	}
	for _, test := range []struct {
		category string
		explicit string
		count    int
	}{
		{category: "general", explicit: "general", count: 0},
		{category: "general", count: 1},
		{category: "news", count: 0},
	} {
		if category, ok := searchFallbackCategory(
			test.category, test.explicit, test.count,
		); ok || category != "" {
			t.Fatalf(
				"unexpected fallback for %+v = %q, %t",
				test, category, ok,
			)
		}
	}
}

func TestWebFetchRejectsLoopbackBeforeConnecting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	manager := newManager(t, ctx, Config{Workspace: t.TempDir()})
	_, err := manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "ssrf", Name: "web_fetch",
		Arguments: json.RawMessage(`{"url":"http://127.0.0.1:8080/private"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("web_fetch loopback error = %v", err)
	}
}

func TestShellCancellationTerminatesOwnedProcessGroup(t *testing.T) {
	t.Parallel()
	manager := newManager(t, context.Background(), Config{Workspace: t.TempDir()})
	// Start the cancellation budget at execution, not while the parallel test
	// is still constructing and registering its manager under race load.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := manager.Execute(ctx, protocol.NormalizedToolCall{
		ID: "cancel", Name: "shell_execute",
		Arguments: json.RawMessage(`{"command":"sh","args":["-c","sleep 30"],"timeout_seconds":30}`),
	})
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, tools.ErrTimeout)) {
		t.Fatalf("shell cancellation error = %v", err)
	}
}

func newManager(t *testing.T, ctx context.Context, config Config) *tools.Manager {
	t.Helper()
	manager, err := tools.NewManager(
		types.SystemClock{}, tools.WithExecutionPolicy(allowPolicy{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(ctx, manager, config); err != nil {
		t.Fatal(err)
	}
	return manager
}
