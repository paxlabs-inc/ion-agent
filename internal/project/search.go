package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultSearchLimit = 24
	maxSearchLimit     = 100
	defaultSearchBytes = 64 << 10
	maxSearchBytes     = 256 << 10
)

func searchProject(ctx context.Context, project Project, index ProjectIndex, request SearchRequest) (SearchResponse, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" || len(request.Query) > 4096 || !validSearchKind(request.Kind) {
		return SearchResponse{}, fmt.Errorf("project: valid bounded search kind and query are required")
	}
	if request.PathPrefix != "" {
		request.PathPrefix = cleanRelativePath(request.PathPrefix)
		if request.PathPrefix == "" {
			return SearchResponse{}, fmt.Errorf("project: path prefix must be project-relative")
		}
	}
	if request.Limit == 0 {
		request.Limit = defaultSearchLimit
	}
	if request.Limit < 1 || request.Limit > maxSearchLimit {
		return SearchResponse{}, fmt.Errorf("project: search limit is out of bounds")
	}
	if request.MaxResultBytes == 0 {
		request.MaxResultBytes = defaultSearchBytes
	}
	if request.MaxResultBytes < 1024 || request.MaxResultBytes > maxSearchBytes {
		return SearchResponse{}, fmt.Errorf("project: search byte limit is out of bounds")
	}
	var expression *regexp.Regexp
	var err error
	if request.Kind == SearchRegex {
		if len(request.Query) > 512 {
			return SearchResponse{}, fmt.Errorf("project: regular expression is too large")
		}
		expression, err = regexp.Compile(request.Query)
		if err != nil {
			return SearchResponse{}, fmt.Errorf("project: invalid regular expression: %w", err)
		}
	}
	response := SearchResponse{ProjectID: project.ID, WorkspaceRevision: index.WorkspaceRevision,
		IndexRevision: index.IndexRevision, Matches: []SearchMatch{}, Omissions: []Omission{}}
	candidates := []SearchMatch{}
	omitted := map[string]int{}
	for _, record := range index.Files {
		if err := ctx.Err(); err != nil {
			return SearchResponse{}, err
		}
		if request.PathPrefix != "" && record.Path != request.PathPrefix && !strings.HasPrefix(record.Path, request.PathPrefix+"/") {
			continue
		}
		if record.Class != ContentSource {
			if request.Kind == SearchFilename && filenameScore(record.Path, request.Query) > 0 &&
				record.Class == ContentGenerated && record.SHA256 != "" {
				if _, readErr := verifyIndexedFile(project, record); readErr != nil {
					return SearchResponse{}, readErr
				}
				candidates = append(candidates, indexedMatch(request.Kind, filenameScore(record.Path, request.Query), record,
					0, 0, "", string(record.Class)+" file: "+record.Path, index))
			}
			omitted[string(record.Class)]++
			continue
		}
		data, readErr := readIndexedFile(project, record)
		if readErr != nil {
			return SearchResponse{}, readErr
		}
		switch request.Kind {
		case SearchFilename:
			if score := filenameScore(record.Path, request.Query); score > 0 {
				candidates = append(candidates, indexedMatch(request.Kind, score, record, 0, 0, "", record.Path, index))
			}
		case SearchSymbol:
			for _, symbol := range record.Symbols {
				if score := nameScore(symbol.Name, request.Query); score > 0 {
					candidates = append(candidates, indexedMatch(request.Kind, score, record, symbol.LineStart,
						symbol.LineEnd, symbol.Name, symbol.Kind+" "+symbol.Name, index))
				}
			}
		case SearchReference:
			for _, reference := range record.References {
				if score := nameScore(reference.Name, request.Query); score > 0 {
					candidates = append(candidates, indexedMatch(request.Kind, score, record, reference.Line,
						reference.Line, reference.Name, "reference to "+reference.Name, index))
				}
			}
		case SearchDependency:
			for _, dependency := range record.Dependencies {
				if score := nameScore(dependency.To, request.Query); score > 0 {
					candidates = append(candidates, indexedMatch(request.Kind, score, record, dependency.Line,
						dependency.Line, "", dependency.Kind+" "+dependency.To, index))
				}
			}
		case SearchDiagnostic:
			for _, diagnostic := range index.Diagnostics {
				if diagnostic.Path != record.Path {
					continue
				}
				haystack := diagnostic.Code + " " + diagnostic.Message + " " + diagnostic.Source
				if score := textScore(haystack, request.Query); score > 0 {
					candidates = append(candidates, indexedMatch(request.Kind, score, record, diagnostic.Line,
						diagnostic.Line, "", diagnostic.Severity+": "+boundedSnippet(diagnostic.Message), index))
				}
			}
		case SearchLexical, SearchRegex, SearchSemantic:
			lines := strings.Split(string(data), "\n")
			for lineIndex, line := range lines {
				var score float64
				switch request.Kind {
				case SearchLexical:
					score = textScore(line, request.Query)
				case SearchRegex:
					if expression.MatchString(line) {
						score = 1
					}
				case SearchSemantic:
					score = semanticScore(request.Query, record.Path+" "+line)
				}
				if score > 0 {
					candidates = append(candidates, indexedMatch(request.Kind, score, record, lineIndex+1,
						lineIndex+1, "", boundedSnippet(line), index))
				}
			}
		}
	}
	for class, count := range omitted {
		response.Omissions = append(response.Omissions, Omission{Class: class, Reason: "content excluded from this retrieval class", Count: count})
	}
	sort.Slice(response.Omissions, func(left, right int) bool { return response.Omissions[left].Class < response.Omissions[right].Class })
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].Score != candidates[right].Score {
			return candidates[left].Score > candidates[right].Score
		}
		if candidates[left].Path != candidates[right].Path {
			return candidates[left].Path < candidates[right].Path
		}
		return candidates[left].LineStart < candidates[right].LineStart
	})
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		key := candidate.Path + "\x00" + candidate.Symbol + "\x00" + fmt.Sprint(candidate.LineStart) + "\x00" + candidate.Snippet
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		size := len(candidate.Path) + len(candidate.Symbol) + len(candidate.Snippet) + 128
		if len(response.Matches) >= request.Limit || response.ResultBytes+size > request.MaxResultBytes {
			response.Truncated = true
			break
		}
		response.Matches = append(response.Matches, candidate)
		response.ResultBytes += size
	}
	return response, nil
}

func indexedMatch(kind SearchKind, score float64, record FileRecord, lineStart, lineEnd int,
	symbol, snippet string, index ProjectIndex) SearchMatch {
	return SearchMatch{Kind: kind, Score: score, Path: record.Path, LineStart: lineStart,
		LineEnd: lineEnd, Symbol: symbol, Snippet: boundedSnippet(snippet),
		Citation: Citation{ProjectID: index.ProjectID, WorkspaceRevision: index.WorkspaceRevision,
			IndexRevision: index.IndexRevision, Path: record.Path, SHA256: record.SHA256,
			LineStart: lineStart, LineEnd: lineEnd, Symbol: symbol, Source: "repository"}}
}

func readIndexedFile(project Project, record FileRecord) ([]byte, error) {
	if record.Class != ContentSource {
		return nil, ErrProtectedPath
	}
	data, err := verifyIndexedFile(project, record)
	if err != nil {
		return nil, err
	}
	sanitized, _ := redactSecrets(record.Path, data)
	return sanitized, nil
}

func verifyIndexedFile(project Project, record FileRecord) ([]byte, error) {
	if record.SHA256 == "" || record.Class != ContentSource && record.Class != ContentGenerated {
		return nil, ErrProtectedPath
	}
	absolute := filepath.Join(project.Root, filepath.FromSlash(record.Path))
	if !pathWithin(project.Root, absolute) {
		return nil, ErrProtectedPath
	}
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxIndexedFileBytes {
		return nil, ErrStaleIndex
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, ErrStaleIndex
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != record.SHA256 {
		return nil, ErrStaleIndex
	}
	return data, nil
}

func validSearchKind(kind SearchKind) bool {
	switch kind {
	case SearchLexical, SearchRegex, SearchFilename, SearchSymbol, SearchReference,
		SearchDependency, SearchDiagnostic, SearchSemantic:
		return true
	default:
		return false
	}
}

func filenameScore(filename, query string) float64 {
	return textScore(strings.ReplaceAll(filename, "/", " "), query)
}

func nameScore(name, query string) float64 {
	name, query = strings.ToLower(name), strings.ToLower(strings.TrimSpace(query))
	switch {
	case name == query:
		return 2
	case strings.HasPrefix(name, query):
		return 1.5
	case strings.Contains(name, query):
		return 1
	default:
		return semanticScore(query, name)
	}
}

func textScore(text, query string) float64 {
	text, query = strings.ToLower(text), strings.ToLower(strings.TrimSpace(query))
	if text == query {
		return 2
	}
	if index := strings.Index(text, query); index >= 0 {
		return 1.5 - float64(index)/float64(max(len(text), 1))*0.25
	}
	return 0
}

func semanticScore(query, text string) float64 {
	queryTokens, textTokens := semanticTokens(query), semanticTokens(text)
	if len(queryTokens) == 0 || len(textTokens) == 0 {
		return 0
	}
	intersection := 0
	for token := range queryTokens {
		if _, ok := textTokens[token]; ok {
			intersection++
		}
	}
	if intersection == 0 {
		return 0
	}
	return float64(intersection)/float64(len(queryTokens)) + float64(intersection)/float64(len(textTokens))*0.25
}

func semanticTokens(value string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, token := range identifier.FindAllString(strings.ToLower(value), -1) {
		if !languageKeyword(token) {
			result[token] = struct{}{}
		}
	}
	return result
}

func boundedSnippet(value string) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if len(value) > 512 {
		value = value[:509] + "..."
	}
	return value
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
