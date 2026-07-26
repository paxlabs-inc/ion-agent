package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultContextBytes = 64 << 10
	maxContextBytes     = 256 << 10
	minContextBytes     = 8 << 10
)

func planProjectContext(ctx context.Context, project Project, index ProjectIndex,
	request ContextPlanRequest, now time.Time) (ContextPack, error) {
	request.Task = strings.TrimSpace(request.Task)
	if request.Task == "" || len(request.Task) > 16<<10 {
		return ContextPack{}, fmt.Errorf("project: a bounded task is required for context planning")
	}
	if request.PathScope != "" {
		request.PathScope = cleanRelativePath(request.PathScope)
		if request.PathScope == "" {
			return ContextPack{}, fmt.Errorf("project: invalid context path scope")
		}
	}
	if request.MaxBytes == 0 {
		request.MaxBytes = defaultContextBytes
	}
	if request.MaxBytes < minContextBytes || request.MaxBytes > maxContextBytes {
		return ContextPack{}, fmt.Errorf("project: context byte limit is out of bounds")
	}
	pack := ContextPack{Version: IntelligenceVersion, ProjectID: project.ID,
		WorkspaceRevision: index.WorkspaceRevision, IndexRevision: index.IndexRevision,
		Task: request.Task, Items: []ContextItem{}, Omissions: append([]Omission(nil), index.Omissions...),
		CreatedAt: now, ExpandedForMismatch: strings.TrimSpace(request.Mismatch) != "" || len(request.Expand) > 0}
	pack.Instructions = resolveInstructions(index.Instructions, request.PathScope)
	add := func(source ContextSource) {
		content, findings := redactSecrets(source.Title, []byte(source.Content))
		if len(findings) > 0 {
			pack.Omissions = append(pack.Omissions, Omission{Class: "secret_redaction", Reason: "secret-like values were removed from context", Count: len(findings)})
		}
		content = []byte(strings.TrimSpace(string(content)))
		if len(content) == 0 {
			return
		}
		if len(content) > 32<<10 {
			content = content[:32<<10]
			pack.Truncated = true
			pack.Omissions = append(pack.Omissions, Omission{Class: "source_limit", Reason: source.Title + " was truncated"})
		}
		if source.Citation.ProjectID == uuid.Nil {
			source.Citation.ProjectID = project.ID
		}
		if source.Citation.WorkspaceRevision == 0 {
			source.Citation.WorkspaceRevision = index.WorkspaceRevision
		}
		if source.Citation.IndexRevision == 0 {
			source.Citation.IndexRevision = index.IndexRevision
		}
		if source.Citation.Path == "" {
			source.Citation.Path = "context/" + safeContextName(source.Kind+"-"+source.Title)
		}
		if source.Citation.Source == "" {
			source.Citation.Source = source.Kind
		}
		if source.Citation.SHA256 == "" {
			digest := sha256.Sum256(content)
			source.Citation.SHA256 = hex.EncodeToString(digest[:])
		}
		size := len(content) + len(source.Title) + len(source.Citation.Path) + 128
		if pack.Bytes+size > request.MaxBytes {
			pack.Truncated = true
			pack.Omissions = append(pack.Omissions, Omission{Path: source.Citation.Path, Class: "budget", Reason: "context byte budget exhausted"})
			return
		}
		pack.Items = append(pack.Items, ContextItem{Kind: source.Kind, Title: source.Title,
			Content: string(content), Citation: source.Citation, Verified: source.Verified,
			Bytes: size, Priority: source.Priority})
		pack.Bytes += size
	}
	sources := append([]ContextSource(nil), request.Sources...)
	sort.SliceStable(sources, func(left, right int) bool { return sources[left].Priority > sources[right].Priority })
	for _, source := range sources {
		add(source)
	}
	for _, specPath := range []string{"spec/spec.kvx", ".ion/spec.kvx", "spec/tasks.md", "tasks.md"} {
		record, ok := fileByPath(index.Files, specPath)
		if !ok || record.Class != ContentSource {
			continue
		}
		content, err := readIndexedFile(project, record)
		if err != nil {
			return ContextPack{}, err
		}
		add(ContextSource{Kind: "authoritative_spec", Title: specPath, Content: string(content),
			Priority: 98, Verified: true,
			Citation: Citation{ProjectID: project.ID, WorkspaceRevision: index.WorkspaceRevision,
				IndexRevision: index.IndexRevision, Path: specPath, SHA256: record.SHA256,
				Source: "repository"}})
		break
	}
	for _, instruction := range pack.Instructions.Instructions {
		add(ContextSource{Kind: "repository_instruction", Title: instruction.Path,
			Content: instruction.Content, Priority: 95, Verified: true,
			Citation: Citation{ProjectID: project.ID, WorkspaceRevision: index.WorkspaceRevision,
				IndexRevision: index.IndexRevision, Path: instruction.Path, SHA256: instruction.SHA256,
				Source: "repository_instruction"}})
	}
	addHistoryContext(index, request.Task, add)
	addDiagnosticContext(index, request.Task+" "+request.Mismatch, request.PathScope, add)
	searchBudget := request.MaxBytes - pack.Bytes
	if searchBudget >= 1024 {
		if searchBudget > 48<<10 {
			searchBudget = 48 << 10
		}
		response, err := searchProject(ctx, project, index, SearchRequest{ProjectID: project.ID,
			WorkspaceRevision: index.WorkspaceRevision, ExpectedIndexRevision: index.IndexRevision,
			Kind: SearchSemantic, Query: request.Task, PathPrefix: request.PathScope, Limit: 32,
			MaxResultBytes: max(1024, searchBudget)})
		if err != nil {
			return ContextPack{}, err
		}
		for _, match := range response.Matches {
			add(ContextSource{Kind: "repository", Title: match.Path, Content: match.Snippet,
				Citation: match.Citation, Verified: true, Priority: 70})
		}
		pack.Omissions = append(pack.Omissions, response.Omissions...)
		if response.Truncated {
			pack.Truncated = true
		}
	}
	if pack.ExpandedForMismatch {
		expansionTerms := append([]string{}, request.Expand...)
		if strings.TrimSpace(request.Mismatch) != "" {
			expansionTerms = append(expansionTerms, request.Mismatch)
		}
		for _, term := range expansionTerms {
			term = strings.TrimSpace(term)
			if term == "" || pack.Bytes >= request.MaxBytes {
				continue
			}
			for _, kind := range []SearchKind{SearchFilename, SearchSymbol, SearchDependency, SearchSemantic} {
				remaining := request.MaxBytes - pack.Bytes
				if remaining < 1024 {
					break
				}
				response, err := searchProject(ctx, project, index, SearchRequest{ProjectID: project.ID,
					WorkspaceRevision: index.WorkspaceRevision, ExpectedIndexRevision: index.IndexRevision,
					Kind: kind, Query: term, PathPrefix: request.PathScope, Limit: 8,
					MaxResultBytes: max(1024, min(remaining, 16<<10))})
				if err != nil {
					return ContextPack{}, err
				}
				for _, match := range response.Matches {
					add(ContextSource{Kind: "mismatch_expansion", Title: match.Path, Content: match.Snippet,
						Citation: match.Citation, Verified: true, Priority: 80})
				}
			}
		}
	}
	classCounts := map[string]int{}
	for _, record := range index.Files {
		if record.Class != ContentSource {
			classCounts[string(record.Class)]++
		}
	}
	for class, count := range classCounts {
		pack.Omissions = append(pack.Omissions, Omission{Class: class, Reason: "excluded from model context by default", Count: count})
	}
	pack.Items = deduplicateContextItems(pack.Items)
	pack.Bytes = 0
	for _, item := range pack.Items {
		pack.Bytes += item.Bytes
	}
	sort.SliceStable(pack.Omissions, func(left, right int) bool {
		if pack.Omissions[left].Class == pack.Omissions[right].Class {
			return pack.Omissions[left].Path < pack.Omissions[right].Path
		}
		return pack.Omissions[left].Class < pack.Omissions[right].Class
	})
	return pack, nil
}

func resolveInstructions(instructions []Instruction, target string) InstructionResolution {
	resolution := InstructionResolution{TargetPath: target,
		Precedence:          "immutable safety and user authority, then nearest scoped repository instruction",
		ImmutableSafetyWins: true, UserAuthorityWins: true, Instructions: []Instruction{}}
	for _, instruction := range instructions {
		if instruction.Scope == "" || target == "" || target == instruction.Scope || strings.HasPrefix(target, instruction.Scope+"/") {
			resolution.Instructions = append(resolution.Instructions, instruction)
		}
	}
	sort.SliceStable(resolution.Instructions, func(left, right int) bool {
		if resolution.Instructions[left].Precedence == resolution.Instructions[right].Precedence {
			return resolution.Instructions[left].Path < resolution.Instructions[right].Path
		}
		return resolution.Instructions[left].Precedence < resolution.Instructions[right].Precedence
	})
	return resolution
}

func addHistoryContext(index ProjectIndex, task string, add func(ContextSource)) {
	query := semanticTokens(task)
	for _, entry := range index.History {
		haystack := entry.Subject + " " + strings.Join(entry.Paths, " ")
		if len(query) > 0 && semanticScore(task, haystack) == 0 {
			continue
		}
		content, _ := json.Marshal(map[string]any{"commit": entry.Commit, "author": entry.Author,
			"timestamp": entry.Timestamp, "subject": entry.Subject, "paths": entry.Paths})
		add(ContextSource{Kind: "history", Title: "Recent change " + entry.Commit,
			Content: string(content), Priority: 50, Verified: true,
			Citation: Citation{ProjectID: index.ProjectID, WorkspaceRevision: index.WorkspaceRevision,
				IndexRevision: index.IndexRevision, Path: "git/" + entry.Commit, Source: "history"}})
	}
}

func addDiagnosticContext(index ProjectIndex, query, pathScope string, add func(ContextSource)) {
	for _, diagnostic := range index.Diagnostics {
		if pathScope != "" && diagnostic.Path != pathScope && !strings.HasPrefix(diagnostic.Path, pathScope+"/") {
			continue
		}
		if strings.TrimSpace(query) != "" && semanticScore(query, diagnostic.Code+" "+diagnostic.Message+" "+diagnostic.Path) == 0 {
			continue
		}
		record, ok := fileByPath(index.Files, diagnostic.Path)
		if !ok || record.SHA256 == "" {
			continue
		}
		add(ContextSource{Kind: "diagnostic", Title: diagnostic.Source + " " + diagnostic.Code,
			Content: diagnostic.Severity + ": " + diagnostic.Message, Priority: 85, Verified: true,
			Citation: Citation{ProjectID: index.ProjectID, WorkspaceRevision: index.WorkspaceRevision,
				IndexRevision: index.IndexRevision, Path: diagnostic.Path, SHA256: record.SHA256,
				LineStart: diagnostic.Line, LineEnd: diagnostic.Line, Source: "diagnostic"}})
	}
}

func deduplicateContextItems(items []ContextItem) []ContextItem {
	seen := map[string]struct{}{}
	result := make([]ContextItem, 0, len(items))
	for _, item := range items {
		key := item.Kind + "\x00" + item.Citation.Path + "\x00" + fmt.Sprint(item.Citation.LineStart) + "\x00" + item.Content
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}

func safeContextName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			return character
		}
		return '-'
	}, value)
	value = strings.Trim(value, "-")
	if value == "" {
		return "source"
	}
	if len(value) > 96 {
		value = value[:96]
	}
	return path.Clean(value)
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
