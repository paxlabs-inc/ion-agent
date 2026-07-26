package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DisplayModelVersion        = "ion.display-model.v1"
	displayModelLegacyVersion  = "ion.display-model.v0"
	MaximumDisplayModelBytes   = 64 << 10
	MaximumDisplayInputBytes   = 2 << 20
	MaximumDisplayDatumBytes   = 16 << 10
	MaximumDisplayFields       = 32
	MaximumDisplayBlocks       = 16
	MaximumDisplayItems        = 64
	MaximumDisplayColumns      = 16
	MaximumDisplayRows         = 64
	MaximumDisplayValuesPerRow = 16
)

type DisplayKind string

const (
	DisplaySearch     DisplayKind = "search"
	DisplayReader     DisplayKind = "reader"
	DisplayNavigation DisplayKind = "navigation"
	DisplayRepository DisplayKind = "repository"
	DisplayCode       DisplayKind = "code"
	DisplayTerminal   DisplayKind = "terminal"
	DisplayDiff       DisplayKind = "diff"
	DisplayProcess    DisplayKind = "process"
	DisplayTable      DisplayKind = "table"
	DisplayChart      DisplayKind = "chart"
	DisplayDocument   DisplayKind = "document"
	DisplayArtifact   DisplayKind = "artifact"
	DisplayTask       DisplayKind = "task"
	DisplayAgent      DisplayKind = "agent"
	DisplayApproval   DisplayKind = "approval"
	DisplayError      DisplayKind = "error"
	DisplayDegraded   DisplayKind = "degraded"
)

type DisplayTruth string

const (
	DisplayObserved    DisplayTruth = "observed"
	DisplayGenerated   DisplayTruth = "generated"
	DisplaySummarized  DisplayTruth = "summarized"
	DisplayInferred    DisplayTruth = "inferred"
	DisplayUnavailable DisplayTruth = "unavailable"
)

type DisplayFormat string

const (
	DisplayText         DisplayFormat = "text"
	DisplayURL          DisplayFormat = "url"
	DisplayPath         DisplayFormat = "path"
	DisplayInteger      DisplayFormat = "integer"
	DisplayNumber       DisplayFormat = "number"
	DisplayBoolean      DisplayFormat = "boolean"
	DisplayTimestamp    DisplayFormat = "timestamp"
	DisplayStatus       DisplayFormat = "status"
	DisplayCodeText     DisplayFormat = "code"
	DisplayTerminalText DisplayFormat = "terminal"
	DisplayDiffText     DisplayFormat = "diff"
)

type DisplayBlockKind string

const (
	DisplayBlockText     DisplayBlockKind = "text"
	DisplayBlockList     DisplayBlockKind = "list"
	DisplayBlockCode     DisplayBlockKind = "code"
	DisplayBlockTerminal DisplayBlockKind = "terminal"
	DisplayBlockDiff     DisplayBlockKind = "diff"
	DisplayBlockTable    DisplayBlockKind = "table"
	DisplayBlockChart    DisplayBlockKind = "chart"
	DisplayBlockDocument DisplayBlockKind = "document"
	DisplayBlockEmpty    DisplayBlockKind = "empty"
)

type DisplayDatum struct {
	Value   string        `json:"value"`
	Truth   DisplayTruth  `json:"truth"`
	Format  DisplayFormat `json:"format"`
	Sources []int         `json:"sources"`
}

type DisplayField struct {
	Label string       `json:"label"`
	Value DisplayDatum `json:"value"`
}

type DisplayItem struct {
	Fields []DisplayField `json:"fields"`
}

type DisplayBlock struct {
	Kind     DisplayBlockKind `json:"kind"`
	Label    string           `json:"label,omitempty"`
	Language string           `json:"language,omitempty"`
	Content  *DisplayDatum    `json:"content,omitempty"`
	Fields   []DisplayField   `json:"fields,omitempty"`
	Items    []DisplayItem    `json:"items,omitempty"`
	Columns  []string         `json:"columns,omitempty"`
	Rows     [][]DisplayDatum `json:"rows,omitempty"`
}

type DisplayModel struct {
	ProtocolVersion string         `json:"protocol_version"`
	Kind            DisplayKind    `json:"kind"`
	Title           DisplayDatum   `json:"title"`
	Fields          []DisplayField `json:"fields,omitempty"`
	Blocks          []DisplayBlock `json:"blocks,omitempty"`
}

type DisplayCompatibility string

const (
	DisplayCurrent     DisplayCompatibility = "current"
	DisplayMigrated    DisplayCompatibility = "migrated"
	DisplayUnsupported DisplayCompatibility = "unsupported"
)

type DisplayAdapterRegistry struct {
	adapters map[string]displayAdapter
}

type displayAdapter func(*displayBuilder, json.RawMessage, json.RawMessage) DisplayModel

type displayBuilder struct {
	sources []ComputerSourceReference
}

var (
	displayMarkupPattern     = regexp.MustCompile(`(?is)<\s*(?:script|style|iframe|object|embed|link|meta|[a-z][a-z0-9:-]*)(?:\s[^>]*)?>`)
	displayTagPattern        = regexp.MustCompile(`(?s)<[^>]*>`)
	displayEscapePattern     = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\)|[@-_])`)
	displayCredentialPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|password|passwd|secret)\b\s*[:=]\s*["']?[^\s"',;]{4,}`)
	displayBearerPattern     = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/-]{8,}=*`)
	displayPrivateKeyPattern = regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
)

func (model DisplayModel) Validate(sourceCount int) error {
	if model.ProtocolVersion != DisplayModelVersion || !model.Kind.valid() ||
		sourceCount <= 0 {
		return fmt.Errorf("%w: invalid display model identity", ErrInvalidProtocol)
	}
	if err := model.Title.validate(sourceCount); err != nil {
		return fmt.Errorf("%w: invalid display title", err)
	}
	if len(model.Fields) > MaximumDisplayFields ||
		len(model.Blocks) > MaximumDisplayBlocks {
		return fmt.Errorf("%w: display model exceeds collection bounds", ErrInvalidProtocol)
	}
	for _, field := range model.Fields {
		if err := field.validate(sourceCount); err != nil {
			return err
		}
	}
	for _, block := range model.Blocks {
		if err := block.validate(sourceCount); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(model)
	if err != nil || len(encoded) > MaximumDisplayModelBytes {
		return fmt.Errorf("%w: display model exceeds encoded bound", ErrInvalidProtocol)
	}
	return nil
}

func ResolveDisplayModel(
	raw json.RawMessage,
	sourceCount int,
) (DisplayModel, DisplayCompatibility, error) {
	if len(raw) == 0 || len(raw) > MaximumDisplayModelBytes || !json.Valid(raw) {
		return DisplayModel{}, "", fmt.Errorf(
			"%w: invalid display model envelope", ErrInvalidProtocol,
		)
	}
	var header struct {
		ProtocolVersion string `json:"protocol_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil ||
		strings.TrimSpace(header.ProtocolVersion) == "" ||
		len(header.ProtocolVersion) > 128 {
		return DisplayModel{}, "", fmt.Errorf(
			"%w: invalid display model version", ErrInvalidProtocol,
		)
	}
	switch header.ProtocolVersion {
	case DisplayModelVersion:
		var model DisplayModel
		if err := json.Unmarshal(raw, &model); err != nil {
			return DisplayModel{}, "", fmt.Errorf(
				"%w: decode display model", ErrInvalidProtocol,
			)
		}
		if err := model.Validate(sourceCount); err != nil {
			return DisplayModel{}, "", err
		}
		return model, DisplayCurrent, nil
	case displayModelLegacyVersion:
		var legacy struct {
			ProtocolVersion string      `json:"protocol_version"`
			Kind            DisplayKind `json:"kind"`
			Title           string      `json:"title"`
			Summary         string      `json:"summary,omitempty"`
			Source          int         `json:"source"`
		}
		if err := json.Unmarshal(raw, &legacy); err != nil ||
			legacy.Source < 0 || legacy.Source >= sourceCount {
			return DisplayModel{}, "", fmt.Errorf(
				"%w: invalid legacy display model", ErrInvalidProtocol,
			)
		}
		builder := &displayBuilder{}
		model := DisplayModel{
			ProtocolVersion: DisplayModelVersion,
			Kind:            legacy.Kind,
			Title: builder.datum(
				legacy.Title, DisplayObserved, DisplayText, legacy.Source,
			),
		}
		if strings.TrimSpace(legacy.Summary) != "" {
			model.Blocks = []DisplayBlock{{
				Kind: DisplayBlockText,
				Content: displayDatumPointer(builder.datum(
					legacy.Summary, DisplaySummarized, DisplayText, legacy.Source,
				)),
			}}
		}
		if err := model.Validate(sourceCount); err != nil {
			return DisplayModel{}, "", err
		}
		return model, DisplayMigrated, nil
	default:
		return DisplayModel{}, DisplayUnsupported, nil
	}
}

func NewDisplayAdapterRegistry() *DisplayAdapterRegistry {
	registry := &DisplayAdapterRegistry{adapters: map[string]displayAdapter{}}
	registry.adapters["filesystem_list"] = adaptNavigation
	registry.adapters["filesystem_stat"] = adaptRepository
	registry.adapters["filesystem_read"] = adaptCode
	registry.adapters["filesystem_search"] = adaptSearch
	registry.adapters["filesystem_write"] = adaptArtifact
	registry.adapters["filesystem_patch"] = adaptArtifact
	registry.adapters["shell_execute"] = adaptTerminal
	registry.adapters["git_status"] = adaptRepositoryOutput
	registry.adapters["git_log"] = adaptRepositoryOutput
	registry.adapters["git_show"] = adaptRepositoryOutput
	registry.adapters["git_diff"] = adaptDiff
	registry.adapters["web_fetch"] = adaptReader
	registry.adapters["web_search"] = adaptSearch
	registry.adapters["browser_navigate"] = adaptReader
	registry.adapters["browser_observe"] = adaptReader
	registry.adapters["browser_interact"] = adaptReader
	registry.adapters["browser_submit"] = adaptReader
	registry.adapters["browser_apply_verification"] = adaptReader
	registry.adapters["computer_observe"] = adaptPrivateDesktopObservation
	registry.adapters["computer_interact"] = adaptPrivateDesktopAction
	registry.adapters["computer_submit"] = adaptPrivateDesktopAction
	return registry
}

func (registry *DisplayAdapterRegistry) Adapt(
	tool string,
	arguments json.RawMessage,
	result json.RawMessage,
	sources []ComputerSourceReference,
) (json.RawMessage, []ComputerSourceReference, error) {
	return registry.AdaptResult(
		tool, arguments, result, len(result), sources,
	)
}

func (registry *DisplayAdapterRegistry) AdaptResult(
	tool string,
	arguments json.RawMessage,
	result json.RawMessage,
	resultBytes int,
	sources []ComputerSourceReference,
) (json.RawMessage, []ComputerSourceReference, error) {
	builder := &displayBuilder{
		sources: append([]ComputerSourceReference(nil), sources...),
	}
	resultSource := builder.addSource("tool_result", displayResultReference(sources))
	if resultBytes < 0 || resultBytes > MaximumDisplayInputBytes ||
		len(result) == 0 || len(result) > MaximumDisplayInputBytes ||
		!json.Valid(result) {
		reason := "No structured result was available."
		if resultBytes > MaximumDisplayInputBytes ||
			len(result) > MaximumDisplayInputBytes {
			reason = "The tool result exceeded the safe display limit."
		} else if len(result) > 0 {
			reason = "The tool returned malformed structured data."
		}
		model := builder.degraded(tool, reason, resultSource)
		return builder.encode(model)
	}
	adapter := displayAdapter(nil)
	if registry != nil {
		adapter = registry.adapters[tool]
	}
	if adapter == nil {
		adapter = inferredDisplayAdapter(tool)
	}
	model := adapter(builder, arguments, result)
	return builder.encode(model)
}

func (registry *DisplayAdapterRegistry) Approval(
	tool string,
	risk string,
	sources []ComputerSourceReference,
) (json.RawMessage, []ComputerSourceReference, error) {
	builder := &displayBuilder{
		sources: append([]ComputerSourceReference(nil), sources...),
	}
	model := DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayApproval,
		Title: builder.datum(
			"Approval required", DisplayGenerated, DisplayText, 0,
		),
		Fields: []DisplayField{
			builder.field("Action", tool, DisplayGenerated, DisplayText, 1),
			builder.field("Risk", risk, DisplayObserved, DisplayStatus, 0),
		},
	}
	return builder.encode(model)
}

func (registry *DisplayAdapterRegistry) Failure(
	title string,
	message string,
	sources []ComputerSourceReference,
) (json.RawMessage, []ComputerSourceReference, error) {
	builder := &displayBuilder{
		sources: append([]ComputerSourceReference(nil), sources...),
	}
	model := DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayError,
		Title: builder.datum(
			title, DisplayGenerated, DisplayText, 0,
		),
		Blocks: []DisplayBlock{{
			Kind: DisplayBlockText,
			Content: displayDatumPointer(builder.datum(
				message, DisplaySummarized, DisplayText, 0,
			)),
		}},
	}
	return builder.encode(model)
}

func (kind DisplayKind) valid() bool {
	switch kind {
	case DisplaySearch, DisplayReader, DisplayNavigation, DisplayRepository,
		DisplayCode, DisplayTerminal, DisplayDiff, DisplayProcess, DisplayTable,
		DisplayChart, DisplayDocument, DisplayArtifact, DisplayTask,
		DisplayAgent, DisplayApproval, DisplayError, DisplayDegraded:
		return true
	default:
		return false
	}
}

func (truth DisplayTruth) valid() bool {
	switch truth {
	case DisplayObserved, DisplayGenerated, DisplaySummarized, DisplayInferred,
		DisplayUnavailable:
		return true
	default:
		return false
	}
}

func (format DisplayFormat) valid() bool {
	switch format {
	case DisplayText, DisplayURL, DisplayPath, DisplayInteger, DisplayNumber,
		DisplayBoolean, DisplayTimestamp, DisplayStatus, DisplayCodeText,
		DisplayTerminalText, DisplayDiffText:
		return true
	default:
		return false
	}
}

func (kind DisplayBlockKind) valid() bool {
	switch kind {
	case DisplayBlockText, DisplayBlockList, DisplayBlockCode,
		DisplayBlockTerminal, DisplayBlockDiff, DisplayBlockTable,
		DisplayBlockChart, DisplayBlockDocument, DisplayBlockEmpty:
		return true
	default:
		return false
	}
}

func (datum DisplayDatum) validate(sourceCount int) error {
	if !datum.Truth.valid() || !datum.Format.valid() ||
		strings.TrimSpace(datum.Value) == "" ||
		len(datum.Value) > MaximumDisplayDatumBytes ||
		!utf8.ValidString(datum.Value) ||
		!displayTextIsSafe(datum.Value) ||
		len(datum.Sources) == 0 || len(datum.Sources) > MaximumComputerSources {
		return fmt.Errorf("%w: invalid display datum", ErrInvalidProtocol)
	}
	seen := map[int]struct{}{}
	for _, source := range datum.Sources {
		if source < 0 || source >= sourceCount {
			return fmt.Errorf("%w: display datum source is out of range", ErrInvalidProtocol)
		}
		if _, exists := seen[source]; exists {
			return fmt.Errorf("%w: duplicate display datum source", ErrInvalidProtocol)
		}
		seen[source] = struct{}{}
	}
	if datum.Format == DisplayURL {
		normalized, ok := normalizeDisplayURL(datum.Value)
		if !ok || normalized != datum.Value {
			return fmt.Errorf("%w: display URL is not normalized", ErrInvalidProtocol)
		}
	}
	if datum.Format == DisplayPath {
		normalized, ok := normalizeDisplayPath(datum.Value)
		if !ok || normalized != datum.Value {
			return fmt.Errorf("%w: display path is not normalized", ErrInvalidProtocol)
		}
	}
	return nil
}

func (field DisplayField) validate(sourceCount int) error {
	if !displayLabelIsSafe(field.Label) {
		return fmt.Errorf("%w: invalid display field label", ErrInvalidProtocol)
	}
	return field.Value.validate(sourceCount)
}

func (item DisplayItem) validate(sourceCount int) error {
	if len(item.Fields) == 0 || len(item.Fields) > MaximumDisplayFields {
		return fmt.Errorf("%w: invalid display item", ErrInvalidProtocol)
	}
	for _, field := range item.Fields {
		if err := field.validate(sourceCount); err != nil {
			return err
		}
	}
	return nil
}

func (block DisplayBlock) validate(sourceCount int) error {
	if !block.Kind.valid() || (block.Label != "" && !displayLabelIsSafe(block.Label)) ||
		len(block.Language) > 64 || !displayTextIsSafe(block.Language) ||
		len(block.Fields) > MaximumDisplayFields ||
		len(block.Items) > MaximumDisplayItems ||
		len(block.Columns) > MaximumDisplayColumns ||
		len(block.Rows) > MaximumDisplayRows {
		return fmt.Errorf("%w: invalid display block", ErrInvalidProtocol)
	}
	if block.Content != nil {
		if err := block.Content.validate(sourceCount); err != nil {
			return err
		}
	}
	for _, field := range block.Fields {
		if err := field.validate(sourceCount); err != nil {
			return err
		}
	}
	for _, item := range block.Items {
		if err := item.validate(sourceCount); err != nil {
			return err
		}
	}
	for _, column := range block.Columns {
		if !displayLabelIsSafe(column) {
			return fmt.Errorf("%w: invalid display column", ErrInvalidProtocol)
		}
	}
	for _, row := range block.Rows {
		if len(row) == 0 || len(row) > MaximumDisplayValuesPerRow ||
			(len(block.Columns) > 0 && len(row) != len(block.Columns)) {
			return fmt.Errorf("%w: invalid display row", ErrInvalidProtocol)
		}
		for _, datum := range row {
			if err := datum.validate(sourceCount); err != nil {
				return err
			}
		}
	}
	return nil
}

func (builder *displayBuilder) addSource(kind string, id string) int {
	kind = strings.TrimSpace(kind)
	id = sanitizeDisplayText(id, 512)
	for index, source := range builder.sources {
		if source.Kind == kind && source.ID == id {
			return index
		}
	}
	if len(builder.sources) >= MaximumComputerSources {
		return 0
	}
	builder.sources = append(builder.sources, ComputerSourceReference{
		Kind: kind,
		ID:   id,
	})
	return len(builder.sources) - 1
}

func (builder *displayBuilder) datum(
	value string,
	truth DisplayTruth,
	format DisplayFormat,
	sources ...int,
) DisplayDatum {
	switch format {
	case DisplayURL:
		if normalized, ok := normalizeDisplayURL(value); ok {
			value = normalized
		} else {
			value, truth, format = "Unavailable", DisplayUnavailable, DisplayText
		}
	case DisplayPath:
		if normalized, ok := normalizeDisplayPath(value); ok {
			value = normalized
		} else {
			value, truth, format = "Unavailable", DisplayUnavailable, DisplayText
		}
	default:
		value = sanitizeDisplayText(value, MaximumDisplayDatumBytes)
	}
	if strings.TrimSpace(value) == "" {
		value, truth, format = "Unavailable", DisplayUnavailable, DisplayText
	}
	if len(sources) == 0 {
		sources = []int{0}
	}
	unique := make([]int, 0, len(sources))
	seen := map[int]struct{}{}
	for _, source := range sources {
		if source < 0 || source >= len(builder.sources) {
			source = 0
		}
		if _, exists := seen[source]; exists {
			continue
		}
		seen[source] = struct{}{}
		unique = append(unique, source)
	}
	return DisplayDatum{
		Value: value, Truth: truth, Format: format, Sources: unique,
	}
}

func (builder *displayBuilder) field(
	label string,
	value string,
	truth DisplayTruth,
	format DisplayFormat,
	sources ...int,
) DisplayField {
	return DisplayField{
		Label: sanitizeDisplayLabel(label),
		Value: builder.datum(value, truth, format, sources...),
	}
}

func (builder *displayBuilder) encode(
	model DisplayModel,
) (json.RawMessage, []ComputerSourceReference, error) {
	if err := model.Validate(len(builder.sources)); err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, nil, err
	}
	return encoded, append([]ComputerSourceReference(nil), builder.sources...), nil
}

func (builder *displayBuilder) degraded(
	tool string,
	reason string,
	source int,
) DisplayModel {
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayDegraded,
		Title: builder.datum(
			displayToolTitle(tool), DisplayGenerated, DisplayText, 1,
		),
		Blocks: []DisplayBlock{{
			Kind: DisplayBlockEmpty,
			Content: displayDatumPointer(builder.datum(
				reason, DisplayUnavailable, DisplayText, source,
			)),
		}},
	}
}

func inferredDisplayAdapter(tool string) displayAdapter {
	lower := strings.ToLower(tool)
	switch {
	case strings.Contains(lower, "task"), strings.Contains(lower, "work_"):
		return adaptTask
	case strings.Contains(lower, "agent"), strings.Contains(lower, "delegate"):
		return adaptAgent
	case strings.Contains(lower, "process"), strings.Contains(lower, "runtime"):
		return adaptProcess
	case strings.Contains(lower, "chart"):
		return adaptChart
	case strings.Contains(lower, "table"):
		return adaptTable
	case strings.Contains(lower, "document"), strings.Contains(lower, "skill"):
		return adaptDocument
	case strings.Contains(lower, "artifact"):
		return adaptArtifact
	case strings.Contains(lower, "git"), strings.Contains(lower, "repository"):
		return adaptRepository
	case strings.Contains(lower, "search"):
		return adaptSearch
	case strings.Contains(lower, "read"), strings.Contains(lower, "fetch"):
		return adaptReader
	case strings.Contains(lower, "list"), strings.Contains(lower, "tree"):
		return adaptNavigation
	case strings.Contains(lower, "diff"), strings.Contains(lower, "patch"):
		return adaptDiff
	case strings.Contains(lower, "shell"), strings.Contains(lower, "terminal"):
		return adaptTerminal
	default:
		return func(
			builder *displayBuilder,
			_ json.RawMessage,
			_ json.RawMessage,
		) DisplayModel {
			return builder.degraded(
				tool,
				"The result is available, but no structured display adapter is registered.",
				2,
			)
		}
	}
}

func adaptNavigation(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	resultObject, ok := displayObject(result)
	if !ok {
		return builder.degraded("navigation", "The navigation result was malformed.", 2)
	}
	pathValue, _ := displayString(resultObject["path"])
	pathSource := builder.addSource("workspace_path", pathValue)
	title := builder.datum("Workspace files", DisplayGenerated, DisplayText, 1)
	fields := []DisplayField{}
	if pathValue != "" {
		fields = append(fields, builder.field(
			"Path", pathValue, DisplayObserved, DisplayPath, pathSource,
		))
	}
	var entries []map[string]json.RawMessage
	_ = json.Unmarshal(resultObject["entries"], &entries)
	items := make([]DisplayItem, 0, minInt(len(entries), MaximumDisplayItems))
	for _, entry := range entries {
		if len(items) >= MaximumDisplayItems {
			break
		}
		name, nameOK := displayString(entry["name"])
		kind, kindOK := displayString(entry["type"])
		if !nameOK || !kindOK {
			continue
		}
		fullPath := name
		if pathValue != "" && pathValue != "." {
			fullPath = path.Join(pathValue, name)
		}
		entrySource := builder.addSource("workspace_path", fullPath)
		items = append(items, DisplayItem{Fields: []DisplayField{
			builder.field("Name", name, DisplayObserved, DisplayText, entrySource),
			builder.field("Type", kind, DisplayObserved, DisplayStatus, entrySource),
		}})
	}
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayNavigation,
		Title:           title,
		Fields:          append(fields, argumentField(builder, arguments, "path", "Requested path")...),
		Blocks: []DisplayBlock{{
			Kind: DisplayBlockList, Label: "Entries", Items: items,
		}},
	}
}

func adaptSearch(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	query := displayArgumentString(arguments, "query")
	fields := []DisplayField{}
	if query != "" {
		fields = append(fields, builder.field(
			"Query", query, DisplayGenerated, DisplayText, 1,
		))
	}
	object, _ := displayObject(result)
	for _, pair := range []struct {
		key    string
		label  string
		format DisplayFormat
	}{
		{key: "provider", label: "Provider", format: DisplayStatus},
		{key: "category", label: "Topic", format: DisplayStatus},
		{key: "state", label: "State", format: DisplayStatus},
		{key: "response_time", label: "Response time", format: DisplayText},
	} {
		if value, valid := displayString(object[pair.key]); valid {
			fields = append(fields, builder.field(
				pair.label, value, DisplayObserved, pair.format, 2,
			))
		}
	}
	if count, valid := displayInteger(object["result_count"]); valid {
		fields = append(fields, builder.field(
			"Results", strconv.FormatInt(count, 10),
			DisplayObserved, DisplayInteger, 2,
		))
	}
	blocks := []DisplayBlock{}
	if answer, valid := displayString(object["answer"]); valid &&
		strings.TrimSpace(answer) != "" {
		blocks = append(blocks, DisplayBlock{
			Kind: DisplayBlockText, Label: "Research overview",
			Content: displayDatumPointer(builder.datum(
				answer, DisplaySummarized, DisplayText, 2,
			)),
		})
	}
	items := extractSearchItems(builder, result)
	if len(items) == 0 {
		blocks = append(blocks, DisplayBlock{
			Kind: DisplayBlockEmpty,
			Content: displayDatumPointer(builder.datum(
				"No structured matches were available.",
				DisplayUnavailable, DisplayText, 2,
			)),
		})
		return DisplayModel{
			ProtocolVersion: DisplayModelVersion,
			Kind:            DisplaySearch,
			Title: builder.datum(
				"Research results", DisplayGenerated, DisplayText, 1,
			),
			Fields: fields,
			Blocks: blocks,
		}
	}
	blocks = append(blocks, DisplayBlock{
		Kind: DisplayBlockList, Label: "Ranked sources", Items: items,
	})
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplaySearch,
		Title: builder.datum(
			"Research results", DisplayGenerated, DisplayText, 1,
		),
		Fields: fields,
		Blocks: blocks,
	}
}

func adaptReader(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded("reader", "The reader result was malformed.", 2)
	}
	target, _ := displayString(object["url"])
	if target == "" {
		target = displayArgumentString(arguments, "url")
	}
	urlSource := 2
	if normalized, valid := normalizeDisplayURL(target); valid {
		target = normalized
		urlSource = builder.addSource("url", target)
	}
	textValue, _ := displayString(object["text"])
	if textValue == "" {
		textValue, _ = displayString(object["content"])
	}
	titleValue, _ := displayString(object["title"])
	fields := []DisplayField{}
	if target != "" {
		fields = append(fields, builder.field(
			"URL", target, DisplayObserved, DisplayURL, urlSource,
		))
	}
	if status, valid := displayInteger(object["status"]); valid {
		fields = append(fields, builder.field(
			"Status", strconv.FormatInt(status, 10),
			DisplayObserved, DisplayInteger, 2,
		))
	}
	if truncated, valid := displayBoolean(object["truncated"]); valid {
		fields = append(fields, builder.field(
			"Truncated", strconv.FormatBool(truncated),
			DisplayObserved, DisplayBoolean, 2,
		))
	}
	if untrusted, valid := displayBoolean(object["untrusted_content"]); valid {
		fields = append(fields, builder.field(
			"External content", strconv.FormatBool(untrusted),
			DisplayObserved, DisplayBoolean, 2,
		))
	}
	title := builder.datum(
		"Reader", DisplayGenerated, DisplayText, 1,
	)
	if titleValue != "" {
		title = builder.datum(
			titleValue, DisplayObserved, DisplayText, 2,
		)
	}
	blocks := []DisplayBlock{{
		Kind: DisplayBlockDocument, Label: "Content",
		Content: displayDatumPointer(builder.datum(
			textValue, DisplayObserved, DisplayText, 2,
		)),
	}}
	if elements := extractBrowserElements(builder, object["elements"]); len(elements) > 0 {
		blocks = append(blocks, DisplayBlock{
			Kind: DisplayBlockList, Label: "Page navigation",
			Items: elements,
		})
	}
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayReader,
		Title:           title,
		Fields:          fields,
		Blocks:          blocks,
	}
}

func adaptPrivateDesktopObservation(
	builder *displayBuilder,
	_ json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded(
			"private computer",
			"The private computer observation was malformed.",
			2,
		)
	}
	structured, ok := displayObject(object["structuredContent"])
	if !ok {
		return builder.degraded(
			"private computer",
			"No structured private computer observation was available.",
			2,
		)
	}
	model := DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayDocument,
		Title: builder.datum(
			"Private computer",
			DisplayGenerated,
			DisplayText,
			1,
		),
	}
	var windows []map[string]json.RawMessage
	_ = json.Unmarshal(structured["windows"], &windows)
	if len(windows) > 0 {
		items := make([]DisplayItem, 0, minInt(len(windows), MaximumDisplayItems))
		for _, window := range windows {
			if len(items) >= MaximumDisplayItems {
				break
			}
			app, _ := displayString(window["app_name"])
			title, _ := displayString(window["title"])
			fields := []DisplayField{}
			if app != "" {
				fields = append(fields, builder.field(
					"Application", app, DisplayObserved, DisplayText, 2,
				))
			}
			if title != "" {
				fields = append(fields, builder.field(
					"Window", title, DisplayObserved, DisplayText, 2,
				))
			}
			if pid, valid := displayInteger(window["pid"]); valid {
				fields = append(fields, builder.field(
					"Process", strconv.FormatInt(pid, 10),
					DisplayObserved, DisplayInteger, 2,
				))
			}
			if len(fields) > 0 {
				items = append(items, DisplayItem{Fields: fields})
			}
		}
		model.Blocks = []DisplayBlock{{
			Kind: DisplayBlockList, Label: "Visible windows", Items: items,
		}}
		return model
	}
	browser, browserOK := displayObject(object["browser"])
	browserState, stateOK := displayObject(browser["structuredContent"])
	if browserOK && stateOK {
		page, _ := displayObject(browserState["page"])
		title, _ := displayString(page["title"])
		target, _ := displayString(page["url"])
		if title != "" {
			model.Title = builder.datum(
				title, DisplayObserved, DisplayText, 2,
			)
		}
		if normalized, valid := normalizeDisplayURL(target); valid {
			source := builder.addSource("url", normalized)
			model.Fields = append(model.Fields, builder.field(
				"URL", normalized, DisplayObserved, DisplayURL, source,
			))
		}
		outline, _ := displayString(browserState["outline"])
		if strings.TrimSpace(outline) == "" {
			outline = "No semantic page content is currently visible."
		}
		model.Kind = DisplayReader
		model.Blocks = []DisplayBlock{{
			Kind:  DisplayBlockDocument,
			Label: "Current page",
			Content: displayDatumPointer(builder.datum(
				outline, DisplayObserved, DisplayText, 2,
			)),
		}}
		return model
	}
	var elements []map[string]json.RawMessage
	_ = json.Unmarshal(structured["elements"], &elements)
	items := make([]DisplayItem, 0, minInt(len(elements), MaximumDisplayItems))
	for _, element := range elements {
		if len(items) >= MaximumDisplayItems {
			break
		}
		role, _ := displayString(element["role"])
		label, _ := displayString(element["label"])
		fields := []DisplayField{}
		if role != "" {
			fields = append(fields, builder.field(
				"Role", role, DisplayObserved, DisplayStatus, 2,
			))
		}
		if label != "" {
			fields = append(fields, builder.field(
				"Label", label, DisplayObserved, DisplayText, 2,
			))
		}
		if len(fields) > 0 {
			items = append(items, DisplayItem{Fields: fields})
		}
	}
	if len(items) == 0 {
		model.Blocks = []DisplayBlock{{
			Kind: DisplayBlockEmpty,
			Content: displayDatumPointer(builder.datum(
				"No structured elements are currently visible.",
				DisplayUnavailable, DisplayText, 2,
			)),
		}}
	} else {
		model.Blocks = []DisplayBlock{{
			Kind: DisplayBlockList, Label: "Visible elements", Items: items,
		}}
	}
	return model
}

func adaptPrivateDesktopAction(
	builder *displayBuilder,
	_ json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded(
			"private computer action",
			"The private computer action result was malformed.",
			2,
		)
	}
	accepted, _ := displayBoolean(object["accepted"])
	model := DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayDocument,
		Title: builder.datum(
			"Private computer action",
			DisplayGenerated,
			DisplayText,
			1,
		),
		Fields: []DisplayField{builder.field(
			"Accepted", strconv.FormatBool(accepted),
			DisplayObserved, DisplayBoolean, 2,
		)},
	}
	nested, _ := displayObject(object["result"])
	var content []map[string]json.RawMessage
	_ = json.Unmarshal(nested["content"], &content)
	if len(content) > 0 {
		text, _ := displayString(content[0]["text"])
		if text != "" {
			model.Blocks = []DisplayBlock{{
				Kind: DisplayBlockText,
				Content: displayDatumPointer(builder.datum(
					text, DisplayObserved, DisplayText, 2,
				)),
			}}
		}
	}
	return model
}

func extractBrowserElements(
	builder *displayBuilder,
	raw json.RawMessage,
) []DisplayItem {
	var elements []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &elements) != nil {
		return nil
	}
	items := make([]DisplayItem, 0, minInt(len(elements), MaximumDisplayItems))
	for _, element := range elements {
		if len(items) >= MaximumDisplayItems {
			break
		}
		ref, refOK := displayString(element["ref"])
		tag, tagOK := displayString(element["tag"])
		if !refOK || !tagOK {
			continue
		}
		fields := []DisplayField{
			builder.field("Reference", ref, DisplayObserved, DisplayText, 2),
			builder.field("Element", tag, DisplayObserved, DisplayStatus, 2),
		}
		for _, candidate := range []struct {
			key   string
			label string
		}{
			{key: "text", label: "Label"},
			{key: "name", label: "Name"},
			{key: "placeholder", label: "Placeholder"},
		} {
			if value, ok := displayString(element[candidate.key]); ok &&
				strings.TrimSpace(value) != "" {
				fields = append(fields, builder.field(
					candidate.label, value, DisplayObserved, DisplayText, 2,
				))
			}
		}
		items = append(items, DisplayItem{Fields: fields})
	}
	return items
}

func adaptCode(
	builder *displayBuilder,
	_ json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded("code", "The file result was malformed.", 2)
	}
	pathValue, _ := displayString(object["path"])
	content, _ := displayString(object["content"])
	pathSource := builder.addSource("workspace_path", pathValue)
	language := inferLanguage(pathValue)
	fields := []DisplayField{
		builder.field("Path", pathValue, DisplayObserved, DisplayPath, pathSource),
	}
	if truncated, valid := displayBoolean(object["truncated"]); valid {
		fields = append(fields, builder.field(
			"Truncated", strconv.FormatBool(truncated),
			DisplayObserved, DisplayBoolean, 2,
		))
	}
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayCode,
		Title: builder.datum(
			pathValue, DisplayObserved, DisplayPath, pathSource,
		),
		Fields: fields,
		Blocks: []DisplayBlock{{
			Kind: DisplayBlockCode, Language: language,
			Content: displayDatumPointer(builder.datum(
				content, DisplayObserved, DisplayCodeText, 2,
			)),
		}},
	}
}

func adaptTerminal(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded("terminal", "The command result was malformed.", 2)
	}
	command := displayArgumentString(arguments, "command")
	output, _ := displayString(object["output"])
	fields := []DisplayField{}
	if command != "" {
		commandSource := builder.addSource("command", command)
		fields = append(fields, builder.field(
			"Command", command, DisplayGenerated, DisplayCodeText, commandSource,
		))
	}
	if exitCode, valid := displayInteger(object["exit_code"]); valid {
		fields = append(fields, builder.field(
			"Exit code", strconv.FormatInt(exitCode, 10),
			DisplayObserved, DisplayInteger, 2,
		))
	}
	if timedOut, valid := displayBoolean(object["timed_out"]); valid {
		fields = append(fields, builder.field(
			"Timed out", strconv.FormatBool(timedOut),
			DisplayObserved, DisplayBoolean, 2,
		))
	}
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayTerminal,
		Title: builder.datum(
			"Command output", DisplayGenerated, DisplayText, 1,
		),
		Fields: fields,
		Blocks: []DisplayBlock{{
			Kind: DisplayBlockTerminal,
			Content: displayDatumPointer(builder.datum(
				output, DisplayObserved, DisplayTerminalText, 2,
			)),
		}},
	}
}

func adaptDiff(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded("diff", "The diff result was malformed.", 2)
	}
	output, _ := displayString(object["output"])
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayDiff,
		Title: builder.datum(
			"Changes", DisplayGenerated, DisplayText, 1,
		),
		Fields: argumentField(builder, arguments, "path", "Path"),
		Blocks: []DisplayBlock{{
			Kind: DisplayBlockDiff,
			Content: displayDatumPointer(builder.datum(
				output, DisplayObserved, DisplayDiffText, 2,
			)),
		}},
	}
}

func adaptRepositoryOutput(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded("repository", "The repository result was malformed.", 2)
	}
	output, _ := displayString(object["output"])
	fields := argumentField(builder, arguments, "revision", "Revision")
	if exitCode, valid := displayInteger(object["exit_code"]); valid {
		fields = append(fields, builder.field(
			"Exit code", strconv.FormatInt(exitCode, 10),
			DisplayObserved, DisplayInteger, 2,
		))
	}
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            DisplayRepository,
		Title: builder.datum(
			"Repository", DisplayGenerated, DisplayText, 1,
		),
		Fields: fields,
		Blocks: []DisplayBlock{{
			Kind: DisplayBlockText,
			Content: displayDatumPointer(builder.datum(
				output, DisplayObserved, DisplayText, 2,
			)),
		}},
	}
}

func adaptRepository(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(
		builder, arguments, result, DisplayRepository, "Repository",
	)
}

func adaptProcess(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(builder, arguments, result, DisplayProcess, "Process")
}

func adaptTable(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(builder, arguments, result, DisplayTable, "Table")
}

func adaptChart(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(builder, arguments, result, DisplayChart, "Chart")
}

func adaptDocument(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(builder, arguments, result, DisplayDocument, "Document")
}

func adaptArtifact(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(builder, arguments, result, DisplayArtifact, "Artifact")
}

func adaptTask(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(builder, arguments, result, DisplayTask, "Task")
}

func adaptAgent(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
) DisplayModel {
	return adaptStructuredKind(builder, arguments, result, DisplayAgent, "Agent")
}

func adaptStructuredKind(
	builder *displayBuilder,
	arguments json.RawMessage,
	result json.RawMessage,
	kind DisplayKind,
	title string,
) DisplayModel {
	object, ok := displayObject(result)
	if !ok {
		return builder.degraded(strings.ToLower(title), "The structured result was malformed.", 2)
	}
	fields := displayScalarFields(builder, object, 2)
	if len(fields) == 0 {
		fields = append(fields, builder.field(
			"Result", "No displayable structured fields were available.",
			DisplayUnavailable, DisplayText, 2,
		))
	}
	if name := displayArgumentString(arguments, "name"); name != "" {
		fields = append([]DisplayField{builder.field(
			"Requested name", name, DisplayGenerated, DisplayText, 1,
		)}, fields...)
	}
	return DisplayModel{
		ProtocolVersion: DisplayModelVersion,
		Kind:            kind,
		Title: builder.datum(
			title, DisplayGenerated, DisplayText, 1,
		),
		Fields: fields,
	}
}

func extractSearchItems(
	builder *displayBuilder,
	result json.RawMessage,
) []DisplayItem {
	object, objectOK := displayObject(result)
	if objectOK {
		if matches, exists := object["matches"]; exists {
			return displaySearchArray(builder, matches)
		}
		if results, exists := object["results"]; exists {
			if items := displaySearchArray(builder, results); len(items) > 0 {
				return items
			}
			if nested, ok := displayObject(results); ok {
				for _, key := range []string{"items", "results", "matches"} {
					if items := displaySearchArray(builder, nested[key]); len(items) > 0 {
						return items
					}
				}
			}
		}
	}
	return displaySearchArray(builder, result)
}

func displaySearchArray(
	builder *displayBuilder,
	raw json.RawMessage,
) []DisplayItem {
	var entries []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	items := make([]DisplayItem, 0, minInt(len(entries), MaximumDisplayItems))
	for _, entry := range entries {
		if len(items) >= MaximumDisplayItems {
			break
		}
		fields := []DisplayField{}
		source := 2
		if target, valid := displayString(entry["url"]); valid {
			if normalized, ok := normalizeDisplayURL(target); ok {
				source = builder.addSource("url", normalized)
				fields = append(fields, builder.field(
					"URL", normalized, DisplayObserved, DisplayURL, source,
				))
			}
		}
		if pathValue, valid := displayString(entry["path"]); valid {
			pathSource := builder.addSource("workspace_path", pathValue)
			source = pathSource
			fields = append(fields, builder.field(
				"Path", pathValue, DisplayObserved, DisplayPath, pathSource,
			))
		}
		for _, pair := range []struct {
			key   string
			label string
		}{
			{key: "title", label: "Title"},
			{key: "name", label: "Name"},
			{key: "snippet", label: "Snippet"},
			{key: "text", label: "Text"},
			{key: "source", label: "Source"},
			{key: "published_date", label: "Published"},
			{key: "engine", label: "Engine"},
		} {
			if value, valid := displayString(entry[pair.key]); valid {
				fields = append(fields, builder.field(
					pair.label, value, DisplayObserved, DisplayText, source,
				))
			}
		}
		if line, valid := displayInteger(entry["line"]); valid {
			fields = append(fields, builder.field(
				"Line", strconv.FormatInt(line, 10),
				DisplayObserved, DisplayInteger, source,
			))
		}
		if score, valid := displayNumber(entry["score"]); valid {
			fields = append(fields, builder.field(
				"Relevance", score, DisplayObserved, DisplayNumber, source,
			))
		}
		if len(fields) > 0 {
			items = append(items, DisplayItem{Fields: fields})
		}
	}
	return items
}

func displayScalarFields(
	builder *displayBuilder,
	object map[string]json.RawMessage,
	source int,
) []DisplayField {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sortStrings(keys)
	fields := make([]DisplayField, 0, minInt(len(keys), MaximumDisplayFields))
	for _, key := range keys {
		if len(fields) >= MaximumDisplayFields {
			break
		}
		raw := object[key]
		switch {
		case displayIsNull(raw):
			continue
		case displayJSONType(raw) == "string":
			value, _ := displayString(raw)
			format := DisplayText
			if strings.Contains(strings.ToLower(key), "path") {
				format = DisplayPath
			} else if strings.Contains(strings.ToLower(key), "url") {
				format = DisplayURL
			}
			fields = append(fields, builder.field(
				displayFieldLabel(key), value, DisplayObserved, format, source,
			))
		case displayJSONType(raw) == "boolean":
			value, _ := displayBoolean(raw)
			fields = append(fields, builder.field(
				displayFieldLabel(key), strconv.FormatBool(value),
				DisplayObserved, DisplayBoolean, source,
			))
		case displayJSONType(raw) == "number":
			value := strings.TrimSpace(string(raw))
			format := DisplayNumber
			if !strings.ContainsAny(value, ".eE") {
				format = DisplayInteger
			}
			fields = append(fields, builder.field(
				displayFieldLabel(key), value, DisplayObserved, format, source,
			))
		}
	}
	return fields
}

func displayObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var value map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

func displayString(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func displayInteger(raw json.RawMessage) (int64, bool) {
	var value int64
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, true
}

func displayNumber(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || displayJSONType(raw) != "number" {
		return "", false
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", false
	}
	return value, true
}

func displayBoolean(raw json.RawMessage) (bool, bool) {
	var value bool
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return false, false
	}
	return value, true
}

func displayArgumentString(arguments json.RawMessage, key string) string {
	object, ok := displayObject(arguments)
	if !ok {
		return ""
	}
	value, _ := displayString(object[key])
	return value
}

func argumentField(
	builder *displayBuilder,
	arguments json.RawMessage,
	key string,
	label string,
) []DisplayField {
	value := displayArgumentString(arguments, key)
	if value == "" {
		return nil
	}
	format := DisplayText
	if key == "path" {
		format = DisplayPath
	}
	return []DisplayField{
		builder.field(label, value, DisplayGenerated, format, 1),
	}
}

func displayDatumPointer(value DisplayDatum) *DisplayDatum {
	return &value
}

func displayResultReference(sources []ComputerSourceReference) string {
	for _, source := range sources {
		if source.Kind == "tool_event" {
			return source.ID + ":result"
		}
	}
	return "tool-result"
}

func displayToolTitle(tool string) string {
	tool = strings.TrimSpace(strings.ReplaceAll(tool, "_", " "))
	if tool == "" {
		return "Tool result"
	}
	return strings.ToUpper(tool[:1]) + tool[1:]
}

func displayFieldLabel(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "_", " ")
	if value == "" {
		return "Value"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func inferLanguage(pathValue string) string {
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(pathValue)), ".")
	switch extension {
	case "go", "js", "jsx", "ts", "tsx", "json", "css", "html", "md",
		"py", "rb", "rs", "java", "kt", "c", "h", "cpp", "hpp", "sh",
		"yaml", "yml", "toml", "sql", "xml":
		return extension
	default:
		return "text"
	}
}

func normalizeDisplayURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 ||
		strings.HasPrefix(strings.ToLower(value), "data:") {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" {
		return "", false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		if displaySensitiveQueryKey(key) {
			query.Set(key, "[REDACTED]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), true
}

func displaySensitiveQueryKey(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, fragment := range []string{
		"api_key", "apikey", "access_token", "token", "secret", "password",
		"passwd", "authorization", "signature", "sig",
	} {
		if value == fragment || strings.HasSuffix(value, "_"+fragment) {
			return true
		}
	}
	return false
}

func normalizeDisplayPath(value string) (string, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" || strings.ContainsRune(value, 0) {
		return "", false
	}
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") ||
		strings.HasPrefix(clean, "/") {
		return "", false
	}
	return clean, true
}

func sanitizeDisplayLabel(value string) string {
	value = sanitizeDisplayText(value, 128)
	if strings.TrimSpace(value) == "" {
		return "Value"
	}
	return value
}

func sanitizeDisplayText(value string, limit int) string {
	value = displayPrivateKeyPattern.ReplaceAllString(value, "[REDACTED]")
	value = displayBearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = displayCredentialPattern.ReplaceAllString(value, "$1 [REDACTED]")
	value = displayEscapePattern.ReplaceAllString(value, "")
	if displayMarkupPattern.MatchString(value) {
		value = displayTagPattern.ReplaceAllString(value, " ")
		value = html.UnescapeString(value)
		value = displayTagPattern.ReplaceAllString(value, " ")
	}
	var builder strings.Builder
	builder.Grow(minInt(len(value), limit))
	for _, runeValue := range value {
		if runeValue == '\n' || runeValue == '\t' ||
			(!unicode.IsControl(runeValue) && runeValue != '\u2028' &&
				runeValue != '\u2029') {
			builder.WriteRune(runeValue)
		}
		if builder.Len() >= limit {
			break
		}
	}
	value = strings.TrimSpace(builder.String())
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func displayTextIsSafe(value string) bool {
	credentialCheck := strings.ReplaceAll(value, "[REDACTED]", "")
	if !utf8.ValidString(value) || bytes.IndexByte([]byte(value), 0) >= 0 ||
		displayEscapePattern.MatchString(value) ||
		displayMarkupPattern.MatchString(value) ||
		displayPrivateKeyPattern.MatchString(value) ||
		displayBearerPattern.MatchString(credentialCheck) ||
		displayCredentialPattern.MatchString(credentialCheck) {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) && runeValue != '\n' && runeValue != '\t' {
			return false
		}
	}
	return true
}

func displayLabelIsSafe(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 128 &&
		displayTextIsSafe(value)
}

func displayJSONType(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	switch raw[0] {
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	case '{':
		return "object"
	case '[':
		return "array"
	default:
		return "number"
	}
}

func displayIsNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current] < values[current-1]; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
