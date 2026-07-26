package controlplane

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDisplayRegistryConformanceAndStableEncoding(t *testing.T) {
	t.Parallel()
	registry := NewDisplayAdapterRegistry()
	sources := displayTestSources()
	tests := []struct {
		name      string
		tool      string
		arguments string
		result    string
		wantKind  DisplayKind
		special   string
	}{
		{
			name: "search", tool: "filesystem_search",
			arguments: `{"query":"needle"}`,
			result:    `{"matches":[{"path":"main.go","line":7,"text":"needle"}]}`,
			wantKind:  DisplaySearch,
		},
		{
			name: "reader", tool: "web_fetch",
			arguments: `{"url":"https://example.com/read"}`,
			result:    `{"url":"https://example.com/read","status":200,"text":"Article"}`,
			wantKind:  DisplayReader,
		},
		{
			name: "native browser reader", tool: "browser_observe",
			arguments: `{}`,
			result: `{
				"url":"https://example.com/read",
				"title":"Observed article",
				"text":"Article body",
				"truncated":false,
				"untrusted_content":true,
				"elements":[{"ref":"p1","tag":"a","text":"Source link"}]
			}`,
			wantKind: DisplayReader,
		},
		{
			name: "private computer observation", tool: "computer_observe",
			arguments: `{}`,
			result: `{
				"structuredContent":{
					"windows":[{
						"app_name":"Chromium",
						"title":"Example Domains - Chromium",
						"pid":21,
						"window_id":8388611
					}]
				}
			}`,
			wantKind: DisplayDocument,
		},
		{
			name: "private computer action", tool: "computer_interact",
			arguments: `{"action":"click"}`,
			result: `{
				"accepted":true,
				"result":{"content":[{"type":"text","text":"clicked current ref"}]}
			}`,
			wantKind: DisplayDocument,
		},
		{
			name: "navigation", tool: "filesystem_list",
			arguments: `{"path":"src"}`,
			result:    `{"path":"src","entries":[{"name":"main.go","type":"file"}]}`,
			wantKind:  DisplayNavigation,
		},
		{
			name: "repository", tool: "git_status", arguments: `{}`,
			result:   `{"exit_code":0,"output":"M main.go","truncated":false}`,
			wantKind: DisplayRepository,
		},
		{
			name: "code", tool: "filesystem_read",
			arguments: `{"path":"main.go"}`,
			result:    `{"path":"main.go","content":"package main","truncated":false}`,
			wantKind:  DisplayCode,
		},
		{
			name: "terminal", tool: "shell_execute",
			arguments: `{"command":"go","args":["test","./..."]}`,
			result:    `{"exit_code":0,"output":"ok","truncated":false,"timed_out":false}`,
			wantKind:  DisplayTerminal,
		},
		{
			name: "diff", tool: "git_diff", arguments: `{"path":"main.go"}`,
			result:   `{"exit_code":0,"output":"-old\n+new","truncated":false}`,
			wantKind: DisplayDiff,
		},
		{
			name: "process", tool: "process_status", arguments: `{"name":"web"}`,
			result:   `{"name":"web","state":"running","pid":42}`,
			wantKind: DisplayProcess,
		},
		{
			name: "table", tool: "table_render", arguments: `{"name":"builds"}`,
			result:   `{"rows":3,"status":"ready"}`,
			wantKind: DisplayTable,
		},
		{
			name: "chart", tool: "chart_render", arguments: `{"name":"latency"}`,
			result:   `{"series":2,"status":"ready"}`,
			wantKind: DisplayChart,
		},
		{
			name: "document", tool: "document_view", arguments: `{"name":"brief"}`,
			result:   `{"title":"Brief","sections":4}`,
			wantKind: DisplayDocument,
		},
		{
			name: "artifact", tool: "artifact_create", arguments: `{"name":"report"}`,
			result:   `{"name":"report","bytes":128}`,
			wantKind: DisplayArtifact,
		},
		{
			name: "task", tool: "task_list", arguments: `{}`,
			result:   `{"status":"active","count":2}`,
			wantKind: DisplayTask,
		},
		{
			name: "agent", tool: "subagent_delegate", arguments: `{"name":"reviewer"}`,
			result:   `{"agent":"reviewer","status":"running"}`,
			wantKind: DisplayAgent,
		},
		{
			name: "degraded", tool: "unregistered_operation", arguments: `{}`,
			result:   `{"private":"raw result must not be copied"}`,
			wantKind: DisplayDegraded,
			special:  "raw result must not be copied",
		},
	}
	seen := map[DisplayKind]bool{}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			first, firstSources, err := registry.Adapt(
				test.tool,
				json.RawMessage(test.arguments),
				json.RawMessage(test.result),
				sources,
			)
			if err != nil {
				t.Fatal(err)
			}
			second, secondSources, err := registry.Adapt(
				test.tool,
				json.RawMessage(test.arguments),
				json.RawMessage(test.result),
				sources,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) ||
				!displaySourcesEqual(firstSources, secondSources) {
				t.Fatalf("adapter output is not deterministic:\n%s\n%s", first, second)
			}
			model, compatibility, err := ResolveDisplayModel(first, len(firstSources))
			if err != nil {
				t.Fatal(err)
			}
			if compatibility != DisplayCurrent || model.Kind != test.wantKind {
				t.Fatalf("model = %+v, compatibility = %q", model, compatibility)
			}
			if test.special != "" && bytes.Contains(first, []byte(test.special)) {
				t.Fatalf("generic fallback copied raw payload: %s", first)
			}
			seen[model.Kind] = true
		})
	}
	approval, approvalSources, err := registry.Approval("publish", "RED", sources)
	if err != nil {
		t.Fatal(err)
	}
	approvalModel, _, err := ResolveDisplayModel(approval, len(approvalSources))
	if err != nil || approvalModel.Kind != DisplayApproval {
		t.Fatalf("approval model = %+v, err = %v", approvalModel, err)
	}
	seen[DisplayApproval] = true
	failure, failureSources, err := registry.Failure(
		"Action failed", "Tool execution failed.", sources,
	)
	if err != nil {
		t.Fatal(err)
	}
	failureModel, _, err := ResolveDisplayModel(failure, len(failureSources))
	if err != nil || failureModel.Kind != DisplayError {
		t.Fatalf("failure model = %+v, err = %v", failureModel, err)
	}
	seen[DisplayError] = true
	for _, kind := range []DisplayKind{
		DisplaySearch, DisplayReader, DisplayNavigation, DisplayRepository,
		DisplayCode, DisplayTerminal, DisplayDiff, DisplayProcess, DisplayTable,
		DisplayChart, DisplayDocument, DisplayArtifact, DisplayTask,
		DisplayAgent, DisplayApproval, DisplayError, DisplayDegraded,
	} {
		if !seen[kind] {
			t.Fatalf("display kind %q has no conformance fixture", kind)
		}
	}
}

func TestDisplayAdaptersPreferStructuredEvidenceAndSanitize(t *testing.T) {
	t.Parallel()
	registry := NewDisplayAdapterRegistry()
	sources := displayTestSources()
	code, codeSources, err := registry.Adapt(
		"filesystem_read",
		json.RawMessage(`{"path":"claimed.go"}`),
		json.RawMessage(`{
			"path":"observed.go",
			"content":"package observed",
			"summary":"This is actually a different file.",
			"truncated":false
		}`),
		sources,
	)
	if err != nil {
		t.Fatal(err)
	}
	codeModel, _, err := ResolveDisplayModel(code, len(codeSources))
	if err != nil {
		t.Fatal(err)
	}
	if codeModel.Title.Value != "observed.go" ||
		codeModel.Title.Truth != DisplayObserved ||
		bytes.Contains(code, []byte("different file")) {
		t.Fatalf("structured result was not authoritative: %s", code)
	}
	if len(codeModel.Title.Sources) != 1 ||
		codeSources[codeModel.Title.Sources[0]].Kind != "workspace_path" {
		t.Fatalf("code title lacks evidence linkage: %+v %+v",
			codeModel.Title, codeSources)
	}

	terminal, terminalSources, err := registry.Adapt(
		"shell_execute",
		json.RawMessage(`{"command":"printf"}`),
		json.RawMessage(`{
			"exit_code":0,
			"output":"\u001b[31m<script>alert(1)</script> api_key=topsecret Bearer abcdefghijk",
			"timed_out":false
		}`),
		sources,
	)
	if err != nil {
		t.Fatal(err)
	}
	terminalModel, _, err := ResolveDisplayModel(terminal, len(terminalSources))
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(terminal)
	for _, forbidden := range []string{
		"\u001b", "<script>", "topsecret", "abcdefghijk",
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("unsafe terminal value %q survived: %s", forbidden, encoded)
		}
	}
	if terminalModel.Blocks[0].Content == nil ||
		terminalModel.Blocks[0].Content.Truth != DisplayObserved ||
		!strings.Contains(terminalModel.Blocks[0].Content.Value, "[REDACTED]") {
		t.Fatalf("terminal sanitization lost truth or redaction: %+v", terminalModel)
	}

	reader, readerSources, err := registry.Adapt(
		"web_fetch",
		json.RawMessage(`{"url":"https://ignored.example/"}`),
		json.RawMessage(`{
			"url":"HTTPS://Example.COM/article?view=full&token=topsecret#tracking",
			"status":200,
			"text":"Observed article"
		}`),
		sources,
	)
	if err != nil {
		t.Fatal(err)
	}
	readerModel, _, err := ResolveDisplayModel(reader, len(readerSources))
	if err != nil {
		t.Fatal(err)
	}
	if readerModel.Fields[0].Value.Value !=
		"https://example.com/article?token=%5BREDACTED%5D&view=full" ||
		readerModel.Fields[0].Value.Truth != DisplayObserved {
		t.Fatalf("reader URL was not normalized from result: %+v", readerModel.Fields)
	}
	if bytes.Contains(reader, []byte("topsecret")) {
		t.Fatalf("reader URL exposed a sensitive query value: %s", reader)
	}
}

func TestDisplayAdapterMalformedOversizedFallbackAndBounds(t *testing.T) {
	t.Parallel()
	registry := NewDisplayAdapterRegistry()
	sources := displayTestSources()
	tests := []struct {
		name        string
		result      json.RawMessage
		resultBytes int
		wantText    string
	}{
		{
			name: "missing", result: nil, resultBytes: 0,
			wantText: "No structured result was available.",
		},
		{
			name: "malformed", result: json.RawMessage(`{"broken"`),
			resultBytes: 9, wantText: "malformed structured data",
		},
		{
			name: "oversized", result: nil,
			resultBytes: MaximumDisplayInputBytes + 1,
			wantText:    "exceeded the safe display limit",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			raw, foundSources, err := registry.AdaptResult(
				"filesystem_read", json.RawMessage(`{"path":"a.go"}`),
				test.result, test.resultBytes, sources,
			)
			if err != nil {
				t.Fatal(err)
			}
			model, _, err := ResolveDisplayModel(raw, len(foundSources))
			if err != nil {
				t.Fatal(err)
			}
			if model.Kind != DisplayDegraded ||
				len(model.Blocks) != 1 || model.Blocks[0].Content == nil ||
				!strings.Contains(model.Blocks[0].Content.Value, test.wantText) {
				t.Fatalf("fallback = %s", raw)
			}
		})
	}
	unsafe := DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayCode,
		Title: DisplayDatum{
			Value: "<script>unsafe</script>", Truth: DisplayObserved,
			Format: DisplayText, Sources: []int{0},
		},
	}
	if err := unsafe.Validate(len(sources)); err == nil {
		t.Fatal("unsafe display model validated")
	}
	outOfRange := DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayCode,
		Title: DisplayDatum{
			Value: "safe", Truth: DisplayObserved,
			Format: DisplayText, Sources: []int{len(sources)},
		},
	}
	if err := outOfRange.Validate(len(sources)); err == nil {
		t.Fatal("out-of-range evidence reference validated")
	}
}

func TestDisplayModelMigrationAndUnsupportedVersion(t *testing.T) {
	t.Parallel()
	legacy := json.RawMessage(`{
		"protocol_version":"ion.display-model.v0",
		"kind":"document",
		"title":"Historical brief",
		"summary":"Retained evidence",
		"source":0
	}`)
	model, compatibility, err := ResolveDisplayModel(legacy, 2)
	if err != nil {
		t.Fatal(err)
	}
	if compatibility != DisplayMigrated ||
		model.ProtocolVersion != DisplayModelVersion ||
		model.Kind != DisplayDocument ||
		model.Title.Truth != DisplayObserved ||
		model.Blocks[0].Content == nil ||
		model.Blocks[0].Content.Truth != DisplaySummarized {
		t.Fatalf("legacy migration = %+v, %q", model, compatibility)
	}
	future := json.RawMessage(`{
		"protocol_version":"ion.display-model.v99",
		"kind":"future-native-view",
		"opaque":{"must_not_be_reinterpreted":true}
	}`)
	model, compatibility, err = ResolveDisplayModel(future, 2)
	if err != nil {
		t.Fatal(err)
	}
	if compatibility != DisplayUnsupported || model.ProtocolVersion != "" {
		t.Fatalf("future compatibility = %q, model = %+v", compatibility, model)
	}
}

func TestComputerEventPreservesUnsupportedDisplayVersionExplicitly(t *testing.T) {
	t.Parallel()
	toolEventID := uuid.MustParse(
		"7f50fe55-bf37-4ec1-91bd-1584eeab05be",
	)
	actorID := uuid.New()
	outcomeID := uuid.New()
	payload := ComputerEventPayload{
		ProtocolVersion: ComputerEventVersion,
		ToolEventID:     toolEventID,
		ProviderCallID:  "provider-call",
		Tool:            "future_tool",
		Operation:       "future_tool",
		Scope: ComputerScope{
			ActorID: actorID, OutcomeID: &outcomeID, AgentID: "ion",
		},
		RiskClass:      "GREEN",
		Phase:          ComputerCompleted,
		Timestamp:      time.Now().UTC(),
		DisplayKind:    "degraded",
		TerminalStatus: ComputerCompleted,
		Result:         &ComputerResultSummary{Available: true, Bytes: 2},
		SourceReferences: []ComputerSourceReference{{
			Kind: "tool_event", ID: toolEventID.String(),
		}},
		DisplayModel: json.RawMessage(`{
			"protocol_version":"ion.display-model.v99",
			"kind":"future-native-view"
		}`),
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("unsupported retained display version was rejected: %v", err)
	}
	payload.DisplayModel = json.RawMessage(`{
		"protocol_version":"ion.display-model.v1",
		"kind":"code",
		"title":{
			"value":"<script>unsafe</script>",
			"truth":"observed",
			"format":"text",
			"sources":[0]
		}
	}`)
	if err := payload.Validate(); err == nil {
		t.Fatal("unsafe current display model validated")
	}
}

func displayTestSources() []ComputerSourceReference {
	return []ComputerSourceReference{
		{
			Kind: "tool_event",
			ID:   "7f50fe55-bf37-4ec1-91bd-1584eeab05be",
		},
		{Kind: "provider_tool_call", ID: "provider-call"},
	}
}

func displaySourcesEqual(
	left []ComputerSourceReference,
	right []ComputerSourceReference,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
