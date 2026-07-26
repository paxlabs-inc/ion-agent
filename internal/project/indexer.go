package project

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	maxInventoryFiles      = 12_000
	maxIndexedFileBytes    = 2 << 20
	maxTotalHashedBytes    = 256 << 20
	maxSymbolsPerFile      = 256
	maxReferencesPerFile   = 512
	maxDependenciesPerFile = 256
	maxInstructionBytes    = 32 << 10
	maxHistoryEntries      = 24
)

type ignoreRule struct {
	base          string
	pattern       string
	negated       bool
	directoryOnly bool
}

var (
	secretAssignment = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?key|client[_-]?secret|secret|token|password|passwd|private[_-]?key)(["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s#,;}]+)`)
	awsAccessKey     = regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`)
	githubToken      = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
	pemMarker        = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	identifier       = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
	goDefinition     = regexp.MustCompile(`^\s*(?:func\s+(?:\([^)]*\)\s*)?|type\s+|var\s+|const\s+)([A-Za-z_][A-Za-z0-9_]*)`)
	tsDefinition     = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function|class|interface|type|enum|const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	pyDefinition     = regexp.MustCompile(`^\s*(?:async\s+)?(?:def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rustDefinition   = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:fn|struct|enum|trait|type|const|static|mod)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	jvmDefinition    = regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|static\s+|final\s+|abstract\s+)*(?:class|interface|enum|record|fun)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	solDefinition    = regexp.MustCompile(`^\s*(?:abstract\s+)?(?:contract|interface|library|function|event|error|struct|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func buildProjectIndex(ctx context.Context, actor uuid.UUID, project Project, prior ProjectIndex,
	input RefreshInput, now time.Time) (ProjectIndex, error) {
	started := time.Now()
	root, err := filepath.Abs(project.Root)
	if err != nil {
		return ProjectIndex{}, err
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ProjectIndex{}, fmt.Errorf("project: indexed root is unavailable")
	}
	rules, err := loadIgnoreRules(ctx, root)
	if err != nil {
		return ProjectIndex{}, err
	}
	deniedInput := input.DeniedPaths
	if deniedInput == nil && prior.Version == IntelligenceVersion {
		deniedInput = prior.DeniedPaths
	}
	denied := normalizeDeniedPaths(deniedInput)
	priorFiles := make(map[string]FileRecord, len(prior.Files))
	for _, record := range prior.Files {
		priorFiles[record.Path] = record
	}
	index := ProjectIndex{Version: IntelligenceVersion, ActorID: actor, ProjectID: project.ID,
		WorkspaceRevision: project.WorkspaceRevision, DeniedPaths: denied,
		Files: []FileRecord{}, Languages: []string{},
		Frameworks: []string{}, EntryPoints: []EntryPoint{}, Ownership: []OwnershipRule{},
		Configuration: []ConfigurationRecord{}, GeneratedOwnership: []GeneratedOwnership{},
		Instructions: []Instruction{}, History: []HistoryEntry{}, Diagnostics: []Diagnostic{},
		Omissions: []Omission{}, UpdatedAt: now}
	seen := make(map[string]struct{})
	languages, frameworks := map[string]struct{}{}, map[string]struct{}{}
	var bytesHashed int64
	walkErr := filepath.WalkDir(root, func(absolute string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			relative := safeRelative(root, absolute)
			index.Omissions = append(index.Omissions, Omission{Path: relative, Class: string(ContentUnreadable), Reason: "metadata unavailable"})
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative := safeRelative(root, absolute)
		if relative == "" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			index.Omissions = append(index.Omissions, Omission{Path: relative, Class: string(ContentProtected), Reason: "symbolic links are excluded"})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			class, reason := classifyDirectory(relative, entry.Name(), denied, rules)
			if class != "" {
				index.Omissions = append(index.Omissions, Omission{Path: relative, Class: string(class), Reason: reason})
				return filepath.SkipDir
			}
			return nil
		}
		index.Stats.FilesSeen++
		if index.Stats.FilesSeen > maxInventoryFiles {
			index.Truncated = true
			return nil
		}
		fileInfo, infoErr := entry.Info()
		if infoErr != nil {
			index.Files = append(index.Files, FileRecord{Path: relative, Class: ContentUnreadable, Reason: "metadata unavailable"})
			return nil
		}
		record := FileRecord{Path: relative, Size: fileInfo.Size(), ModifiedUnixNS: fileInfo.ModTime().UnixNano(),
			Class: ContentSource, Language: languageFor(relative)}
		seen[relative] = struct{}{}
		if reason := protectedPathReason(relative, denied); reason != "" {
			record.Class, record.Reason = ContentProtected, reason
			index.Files = append(index.Files, record)
			return nil
		}
		if ignoredPath(relative, false, rules) {
			record.Class, record.Reason = ContentIgnored, "matched repository ignore rules"
			index.Files = append(index.Files, record)
			return nil
		}
		if fileInfo.Size() > maxIndexedFileBytes || bytesHashed+fileInfo.Size() > maxTotalHashedBytes {
			record.Class, record.Reason = ContentOversized, "content exceeds bounded indexing limits"
			index.Files = append(index.Files, record)
			index.Truncated = true
			return nil
		}
		data, readErr := os.ReadFile(absolute)
		if readErr != nil {
			record.Class, record.Reason = ContentUnreadable, "content unavailable"
			index.Files = append(index.Files, record)
			return nil
		}
		bytesHashed += int64(len(data))
		index.Stats.BytesHashed += int64(len(data))
		if looksBinary(data) {
			record.Class, record.Reason = ContentBinary, "binary content is excluded from indexes and model context"
			index.Files = append(index.Files, record)
			return nil
		}
		digest := sha256.Sum256(data)
		record.SHA256 = hex.EncodeToString(digest[:])
		if generatedFile(relative, data) {
			record.Class = ContentGenerated
			record.Reason = "generated content is indexed as metadata and omitted from context by default"
		}
		if previous, ok := priorFiles[relative]; ok && previous.SHA256 == record.SHA256 &&
			previous.Class == record.Class {
			record.Language, record.Frameworks = previous.Language, append([]string(nil), previous.Frameworks...)
			record.Symbols, record.References = append([]Symbol(nil), previous.Symbols...), append([]Reference(nil), previous.References...)
			record.Dependencies, record.EntryPoints = append([]Dependency(nil), previous.Dependencies...), append([]EntryPoint(nil), previous.EntryPoints...)
			record.Secrets = append([]SecretFinding(nil), previous.Secrets...)
			index.Stats.Reused++
		} else {
			sanitized, findings := redactSecrets(relative, data)
			record.Secrets = findings
			parseFile(&record, sanitized)
			record.EntryPoints = append(record.EntryPoints, manifestEntryPoints(relative, sanitized)...)
			if _, existed := priorFiles[relative]; existed {
				index.Stats.Updated++
			} else {
				index.Stats.Added++
			}
		}
		if record.Language != "" {
			languages[record.Language] = struct{}{}
		}
		for _, framework := range record.Frameworks {
			frameworks[framework] = struct{}{}
		}
		index.EntryPoints = append(index.EntryPoints, record.EntryPoints...)
		if isInstructionPath(relative) && record.Class != ContentProtected {
			sanitized, _ := redactSecrets(relative, data)
			index.Instructions = append(index.Instructions, instructionRecord(relative, record.SHA256, sanitized))
		}
		index.Files = append(index.Files, record)
		return nil
	})
	if walkErr != nil {
		return ProjectIndex{}, fmt.Errorf("project: inventory: %w", walkErr)
	}
	for path, record := range priorFiles {
		if _, ok := seen[path]; !ok {
			index.Stats.Deleted++
			_ = record
		}
	}
	detectRenames(prior.Files, index.Files, &index.Stats)
	sort.Slice(index.Files, func(left, right int) bool { return index.Files[left].Path < index.Files[right].Path })
	index.Languages, index.Frameworks = sortedKeys(languages), sortedKeys(frameworks)
	index.Frameworks = mergeSorted(index.Frameworks, frameworkSignals(index.Files))
	sort.Slice(index.EntryPoints, func(left, right int) bool {
		if index.EntryPoints[left].Path == index.EntryPoints[right].Path {
			return index.EntryPoints[left].Line < index.EntryPoints[right].Line
		}
		return index.EntryPoints[left].Path < index.EntryPoints[right].Path
	})
	index.Ownership = readOwnership(root, index.Files)
	index.Configuration, index.GeneratedOwnership = deriveConfigurationOwnership(index.Files, index.Ownership)
	index.History = recentHistory(ctx, root)
	index.Diagnostics = normalizeDiagnostics(input.Diagnostics, project.WorkspaceRevision, now, index.Files)
	sort.Slice(index.Instructions, func(left, right int) bool {
		if index.Instructions[left].Precedence == index.Instructions[right].Precedence {
			return index.Instructions[left].Path < index.Instructions[right].Path
		}
		return index.Instructions[left].Precedence < index.Instructions[right].Precedence
	})
	index.RootDigest = indexDigest(index)
	index.IndexRevision = 1
	if prior.Version == IntelligenceVersion {
		index.IndexRevision = prior.IndexRevision
		if prior.RootDigest != index.RootDigest || diagnosticsDigest(prior.Diagnostics) != diagnosticsDigest(index.Diagnostics) {
			index.IndexRevision++
		}
	}
	index.Stats.DurationMS = time.Since(started).Milliseconds()
	return index, nil
}

func loadIgnoreRules(ctx context.Context, root string) ([]ignoreRule, error) {
	rules := []ignoreRule{}
	err := filepath.WalkDir(root, func(absolute string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || ctx.Err() != nil {
			return errors.Join(walkErr, ctx.Err())
		}
		relative := safeRelative(root, absolute)
		if entry.IsDir() && relative != "" && defaultExcludedDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != ".gitignore" || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(absolute)
		if err != nil || len(data) > 1<<20 {
			return nil
		}
		base := filepath.ToSlash(filepath.Dir(relative))
		if base == "." {
			base = ""
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			rule := ignoreRule{base: base}
			if strings.HasPrefix(line, "!") {
				rule.negated, line = true, strings.TrimPrefix(line, "!")
			}
			rule.directoryOnly = strings.HasSuffix(line, "/")
			line = strings.Trim(line, "/")
			if line != "" {
				rule.pattern = filepath.ToSlash(line)
				rules = append(rules, rule)
			}
		}
		return nil
	})
	return rules, err
}

func ignoredPath(relative string, directory bool, rules []ignoreRule) bool {
	ignored := false
	for _, rule := range rules {
		if rule.directoryOnly && !directory {
			continue
		}
		candidate := relative
		if rule.base != "" {
			if candidate != rule.base && !strings.HasPrefix(candidate, rule.base+"/") {
				continue
			}
			candidate = strings.TrimPrefix(strings.TrimPrefix(candidate, rule.base), "/")
		}
		matched := false
		if strings.Contains(rule.pattern, "/") {
			matched, _ = path.Match(rule.pattern, candidate)
			matched = matched || candidate == rule.pattern || strings.HasPrefix(candidate, rule.pattern+"/")
		} else {
			matched, _ = path.Match(rule.pattern, path.Base(candidate))
		}
		if matched {
			ignored = !rule.negated
		}
	}
	return ignored
}

func classifyDirectory(relative, name string, denied []string, rules []ignoreRule) (ContentClass, string) {
	if protectedPathReason(relative, denied) != "" {
		return ContentProtected, protectedPathReason(relative, denied)
	}
	if ignoredPath(relative, true, rules) {
		return ContentIgnored, "matched repository ignore rules"
	}
	if defaultExcludedDirectory(name) {
		if name == ".git" || name == ".hg" || name == ".svn" {
			return ContentProtected, "version-control internals are excluded"
		}
		if name == "node_modules" || name == "vendor" {
			return ContentVendor, "dependency cache is excluded"
		}
		return ContentGenerated, "generated or build cache directory is excluded"
	}
	return "", ""
}

func defaultExcludedDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "target", "dist", "build", ".next", ".cache", "coverage", "__pycache__", ".venv", "venv":
		return true
	default:
		return false
	}
}

func protectedPathReason(relative string, denied []string) string {
	lower := strings.ToLower(path.Base(relative))
	if strings.HasPrefix(lower, ".env") {
		return "environment files are excluded"
	}
	for _, credentialName := range []string{"credentials", "credentials.json", "secrets.json", "secrets.yaml",
		"secrets.yml", "id_rsa", "id_ed25519", ".netrc", ".npmrc", ".pypirc", "auth.json",
		"service-account.json"} {
		if lower == credentialName {
			return "credential or private-key file is excluded"
		}
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kdbx"} {
		if strings.HasSuffix(lower, suffix) {
			return "credential or private-key file is excluded"
		}
	}
	for _, deniedPath := range denied {
		if relative == deniedPath || strings.HasPrefix(relative, deniedPath+"/") {
			return "path was denied by the user"
		}
	}
	return ""
}

func normalizeDeniedPaths(input []string) []string {
	seen := map[string]struct{}{}
	for _, value := range input {
		if clean := cleanRelativePath(value); clean != "" {
			seen[clean] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func parseFile(record *FileRecord, data []byte) {
	record.Frameworks = frameworksFor(record.Path, data)
	lines := strings.Split(string(data), "\n")
	definitions := definitionPattern(record.Language)
	definitionLines := map[int]string{}
	for index, line := range lines {
		lineNumber := index + 1
		if definitions != nil {
			if match := definitions.FindStringSubmatch(line); len(match) > 1 && len(record.Symbols) < maxSymbolsPerFile {
				name := match[1]
				record.Symbols = append(record.Symbols, Symbol{Name: name, Kind: symbolKind(line), Path: record.Path,
					LineStart: lineNumber, LineEnd: lineNumber, Exported: exportedName(name, record.Language, line)})
				definitionLines[lineNumber] = name
			}
		}
		for _, dependency := range dependenciesForLine(record.Path, record.Language, line, lineNumber) {
			if len(record.Dependencies) < maxDependenciesPerFile {
				record.Dependencies = append(record.Dependencies, dependency)
			}
		}
		if entry, ok := entryPointForLine(record.Path, record.Language, line, lineNumber); ok {
			record.EntryPoints = append(record.EntryPoints, entry)
		}
	}
	seenReference := map[string]struct{}{}
	for index, line := range lines {
		lineNumber := index + 1
		for _, name := range identifier.FindAllString(line, -1) {
			if len(record.References) >= maxReferencesPerFile || languageKeyword(name) || definitionLines[lineNumber] == name {
				continue
			}
			key := name + "\x00" + strconv.Itoa(lineNumber)
			if _, duplicate := seenReference[key]; duplicate {
				continue
			}
			seenReference[key] = struct{}{}
			record.References = append(record.References, Reference{Name: name, Path: record.Path, Line: lineNumber})
		}
	}
}

func definitionPattern(language string) *regexp.Regexp {
	switch language {
	case "Go":
		return goDefinition
	case "TypeScript", "JavaScript":
		return tsDefinition
	case "Python":
		return pyDefinition
	case "Rust":
		return rustDefinition
	case "Java", "Kotlin":
		return jvmDefinition
	case "Solidity":
		return solDefinition
	default:
		return nil
	}
}

func symbolKind(line string) string {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, kind := range []string{"function", "func", "class", "interface", "struct", "enum", "trait", "type", "contract", "event", "error", "const", "var", "let", "mod", "def"} {
		if strings.Contains(lower, kind+" ") {
			return kind
		}
	}
	return "symbol"
}

func exportedName(name, language, line string) bool {
	if language == "Go" && name != "" {
		return unicode.IsUpper(rune(name[0]))
	}
	return strings.Contains(line, "export ") || strings.Contains(line, "public ") || strings.Contains(line, "pub ")
}

func dependenciesForLine(file, language, line string, lineNumber int) []Dependency {
	trimmed := strings.TrimSpace(line)
	values := []string{}
	switch language {
	case "Go":
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, `"`) {
			values = quotedValues(trimmed)
		}
	case "TypeScript", "JavaScript":
		if strings.HasPrefix(trimmed, "import ") || strings.Contains(trimmed, "require(") || strings.HasPrefix(trimmed, "export ") {
			values = quotedValues(trimmed)
		}
	case "Python":
		if strings.HasPrefix(trimmed, "import ") {
			values = strings.Split(strings.TrimSpace(strings.TrimPrefix(trimmed, "import ")), ",")
		} else if strings.HasPrefix(trimmed, "from ") {
			fields := strings.Fields(strings.TrimPrefix(trimmed, "from "))
			if len(fields) > 0 {
				values = []string{fields[0]}
			}
		}
	case "Rust":
		if strings.HasPrefix(trimmed, "use ") || strings.HasPrefix(trimmed, "mod ") {
			fields := strings.Fields(trimmed)
			if len(fields) > 1 {
				values = []string{strings.Trim(fields[1], ";")}
			}
		}
	case "Java", "Kotlin":
		if strings.HasPrefix(trimmed, "import ") {
			values = []string{strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "import ")), ";")}
		}
	case "Solidity":
		if strings.HasPrefix(trimmed, "import ") {
			values = quotedValues(trimmed)
		}
	}
	result := make([]Dependency, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		external := !strings.HasPrefix(value, ".") && !strings.HasPrefix(value, "github.com/paxlabs-inc/ion-agent") && !strings.HasPrefix(value, "crate") && !strings.HasPrefix(value, "self")
		result = append(result, Dependency{From: file, To: value, Kind: "import", Line: lineNumber, External: external})
	}
	return result
}

func quotedValues(value string) []string {
	result := []string{}
	for _, quote := range []byte{'"', '\''} {
		parts := strings.Split(value, string(quote))
		for index := 1; index < len(parts); index += 2 {
			if strings.TrimSpace(parts[index]) != "" {
				result = append(result, parts[index])
			}
		}
	}
	return result
}

func entryPointForLine(file, language, line string, lineNumber int) (EntryPoint, bool) {
	base := path.Base(file)
	trimmed := strings.TrimSpace(line)
	switch language {
	case "Go":
		if (base == "main.go" || strings.HasPrefix(file, "cmd/")) && strings.HasPrefix(trimmed, "func main(") {
			return EntryPoint{Path: file, Line: lineNumber, Kind: "application"}, true
		}
	case "Python":
		if strings.Contains(trimmed, `if __name__ == "__main__"`) || strings.Contains(trimmed, "if __name__ == '__main__'") {
			return EntryPoint{Path: file, Line: lineNumber, Kind: "application"}, true
		}
	case "Rust":
		if base == "main.rs" && strings.HasPrefix(trimmed, "fn main(") {
			return EntryPoint{Path: file, Line: lineNumber, Kind: "application"}, true
		}
	case "TypeScript", "JavaScript":
		if base == "main.ts" || base == "main.tsx" || base == "index.ts" || base == "index.js" {
			return EntryPoint{Path: file, Line: lineNumber, Kind: "application"}, true
		}
	}
	return EntryPoint{}, false
}

func manifestEntryPoints(file string, data []byte) []EntryPoint {
	base := path.Base(file)
	result := []EntryPoint{}
	switch base {
	case "package.json":
		var manifest struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			for _, name := range []string{"build", "test", "start", "dev", "lint", "typecheck"} {
				if command := strings.TrimSpace(manifest.Scripts[name]); command != "" {
					kind := name
					if name == "start" || name == "dev" {
						kind = "application"
					}
					result = append(result, EntryPoint{Path: file, Kind: kind, Command: "npm run " + name})
				}
			}
		}
	case "go.mod":
		result = append(result, EntryPoint{Path: file, Kind: "build", Command: "go build ./..."},
			EntryPoint{Path: file, Kind: "test", Command: "go test ./..."})
	case "Cargo.toml":
		result = append(result, EntryPoint{Path: file, Kind: "build", Command: "cargo build"},
			EntryPoint{Path: file, Kind: "test", Command: "cargo test"})
	case "pyproject.toml", "requirements.txt":
		result = append(result, EntryPoint{Path: file, Kind: "test", Command: "python -m pytest"})
	case "Makefile", "makefile":
		for lineIndex, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, target := range []string{"build", "test", "check", "run"} {
				if strings.HasPrefix(trimmed, target+":") {
					kind := target
					if target == "run" {
						kind = "application"
					}
					result = append(result, EntryPoint{Path: file, Line: lineIndex + 1,
						Kind: kind, Command: "make " + target})
				}
			}
		}
	}
	return result
}

func frameworksFor(file string, data []byte) []string {
	lowerPath, lower := strings.ToLower(file), strings.ToLower(string(data))
	set := map[string]struct{}{}
	checks := map[string][]string{
		"React": {`"react"`, "from 'react'", `from "react"`}, "Next.js": {`"next"`, "next.config"},
		"Vite": {`"vite"`, "vite.config"}, "Vue": {`"vue"`}, "Svelte": {`"svelte"`},
		"Django": {"django"}, "Flask": {"flask"}, "FastAPI": {"fastapi"},
		"Axum": {"axum"}, "Actix": {"actix-web"}, "Gin": {"github.com/gin-gonic/gin"},
		"Remix": {`"@remix-run/`}, "Hardhat": {`"hardhat"`}, "Foundry": {"forge-std"},
	}
	for framework, needles := range checks {
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(needle)) || strings.Contains(lowerPath, strings.ToLower(needle)) {
				set[framework] = struct{}{}
				break
			}
		}
	}
	return sortedKeys(set)
}

func frameworkSignals(files []FileRecord) []string {
	set := map[string]struct{}{}
	for _, record := range files {
		switch path.Base(record.Path) {
		case "go.mod":
			set["Go modules"] = struct{}{}
		case "package.json":
			set["Node.js"] = struct{}{}
		case "Cargo.toml":
			set["Cargo"] = struct{}{}
		case "pyproject.toml", "requirements.txt":
			set["Python packaging"] = struct{}{}
		case "Makefile":
			set["Make"] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func languageFor(file string) string {
	switch strings.ToLower(path.Ext(file)) {
	case ".go":
		return "Go"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".kt", ".kts":
		return "Kotlin"
	case ".sol":
		return "Solidity"
	case ".c", ".h":
		return "C"
	case ".cc", ".cpp", ".cxx", ".hpp":
		return "C++"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".swift":
		return "Swift"
	case ".sh", ".bash":
		return "Shell"
	case ".yaml", ".yml":
		return "YAML"
	case ".json":
		return "JSON"
	case ".toml":
		return "TOML"
	case ".md":
		return "Markdown"
	default:
		return ""
	}
}

func generatedFile(file string, data []byte) bool {
	lower := strings.ToLower(file)
	if strings.Contains(lower, ".generated.") || strings.Contains(lower, ".gen.") || strings.HasSuffix(lower, "_generated.go") {
		return true
	}
	prefix := data
	if len(prefix) > 4096 {
		prefix = prefix[:4096]
	}
	text := strings.ToLower(string(prefix))
	return strings.Contains(text, "code generated") || strings.Contains(text, "generated file") && strings.Contains(text, "do not edit")
}

func looksBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

func redactSecrets(file string, data []byte) ([]byte, []SecretFinding) {
	lines := strings.Split(string(data), "\n")
	findings := []SecretFinding{}
	inPrivateKey := false
	for index, line := range lines {
		if inPrivateKey {
			lines[index] = "[REDACTED PRIVATE KEY MATERIAL]"
			if strings.Contains(line, "-----END ") && strings.Contains(line, "PRIVATE KEY-----") {
				inPrivateKey = false
			}
			continue
		}
		if pemMarker.MatchString(line) {
			findings = append(findings, SecretFinding{Path: file, Line: index + 1, Kind: "private_key"})
			lines[index] = "[REDACTED PRIVATE KEY MATERIAL]"
			inPrivateKey = true
			continue
		}
		for _, candidate := range []struct {
			expression *regexp.Regexp
			kind       string
		}{
			{secretAssignment, "credential_assignment"}, {awsAccessKey, "cloud_access_key"},
			{githubToken, "source_control_token"},
		} {
			if candidate.expression.MatchString(line) {
				findings = append(findings, SecretFinding{Path: file, Line: index + 1, Kind: candidate.kind})
				if candidate.expression == secretAssignment {
					line = candidate.expression.ReplaceAllString(line, `${1}${2}[REDACTED]`)
				} else {
					line = candidate.expression.ReplaceAllString(line, `[REDACTED]`)
				}
			}
		}
		lines[index] = line
	}
	return []byte(strings.Join(lines, "\n")), findings
}

func isInstructionPath(file string) bool {
	base := strings.ToLower(path.Base(file))
	return base == "agents.md" || base == "claude.md" || base == "copilot-instructions.md" ||
		file == ".ion/instructions.md"
}

func instructionRecord(file, digest string, data []byte) Instruction {
	truncated := len(data) > maxInstructionBytes
	if truncated {
		data = data[:maxInstructionBytes]
	}
	scope := path.Dir(file)
	if scope == "." || strings.HasSuffix(file, ".github/copilot-instructions.md") {
		scope = ""
	}
	depth := 0
	if scope != "" {
		depth = strings.Count(scope, "/") + 1
	}
	return Instruction{Path: file, Scope: scope, SHA256: digest, Precedence: depth,
		Content: string(data), Truncated: truncated, RepositoryData: true, CannotOverrideSafety: true}
}

func readOwnership(root string, files []FileRecord) []OwnershipRule {
	for _, candidate := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		record, ok := fileByPath(files, candidate)
		if !ok || record.Class != ContentSource || record.SHA256 == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate)))
		if err != nil || len(data) > 1<<20 {
			continue
		}
		result := []OwnershipRule{}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		line := 0
		for scanner.Scan() {
			line++
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
				continue
			}
			result = append(result, OwnershipRule{Pattern: fields[0], Owners: append([]string(nil), fields[1:]...), Source: candidate, Line: line})
			if len(result) >= 512 {
				break
			}
		}
		return result
	}
	return []OwnershipRule{}
}

func deriveConfigurationOwnership(files []FileRecord, rules []OwnershipRule) ([]ConfigurationRecord, []GeneratedOwnership) {
	configuration := []ConfigurationRecord{}
	generated := []GeneratedOwnership{}
	for _, record := range files {
		owners := ownersForPath(record.Path, rules)
		if kind := configurationKind(record.Path); kind != "" && record.Class == ContentSource {
			scope := path.Dir(record.Path)
			if scope == "." {
				scope = ""
			}
			configuration = append(configuration, ConfigurationRecord{Path: record.Path,
				Kind: kind, Scope: scope, SHA256: record.SHA256, Owners: owners})
		}
		if record.Class == ContentGenerated {
			generated = append(generated, GeneratedOwnership{Path: record.Path,
				Generator: "generated header or filename convention", Owners: owners})
		}
	}
	return configuration, generated
}

func configurationKind(file string) string {
	lower := strings.ToLower(path.Base(file))
	switch lower {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		"cargo.toml", "cargo.lock", "pyproject.toml", "requirements.txt", "makefile", "dockerfile",
		"compose.yaml", "compose.yml", ".gitignore", ".gitattributes", "codeowners":
		return "project_configuration"
	}
	if strings.HasPrefix(lower, "tsconfig") && strings.HasSuffix(lower, ".json") ||
		strings.Contains(lower, ".config.") || strings.HasSuffix(lower, ".config.js") ||
		strings.HasSuffix(lower, ".config.ts") || strings.HasPrefix(file, ".github/workflows/") ||
		strings.HasPrefix(file, ".ion/") {
		return "tool_configuration"
	}
	return ""
}

func ownersForPath(file string, rules []OwnershipRule) []string {
	owners := []string{}
	for _, rule := range rules {
		pattern := strings.TrimPrefix(strings.TrimSpace(rule.Pattern), "/")
		matched := false
		if strings.HasSuffix(pattern, "/") {
			matched = strings.HasPrefix(file, pattern)
		} else if strings.Contains(pattern, "/") {
			matched, _ = path.Match(pattern, file)
			matched = matched || file == pattern
		} else {
			matched, _ = path.Match(pattern, path.Base(file))
		}
		if matched {
			owners = append([]string(nil), rule.Owners...)
		}
	}
	return owners
}

func recentHistory(ctx context.Context, root string) []HistoryEntry {
	command := exec.CommandContext(ctx, "git", "-C", root, "log", "--date=iso-strict", "--name-only",
		"--pretty=format:%x1e%H%x1f%an%x1f%aI%x1f%s", "-n", strconv.Itoa(maxHistoryEntries))
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.Output()
	if err != nil || len(output) > 2<<20 {
		return []HistoryEntry{}
	}
	result := []HistoryEntry{}
	for _, raw := range bytes.Split(output, []byte{0x1e}) {
		parts := strings.SplitN(strings.TrimSpace(string(raw)), "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		subjectAndPaths := strings.Split(parts[3], "\n")
		timestamp, _ := time.Parse(time.RFC3339, strings.TrimSpace(parts[2]))
		subject, _ := redactSecrets("git-history", []byte(subjectAndPaths[0]))
		entry := HistoryEntry{Commit: strings.TrimSpace(parts[0]), Author: strings.TrimSpace(parts[1]),
			Timestamp: timestamp, Subject: strings.TrimSpace(string(subject)), Paths: []string{}}
		for _, changed := range subjectAndPaths[1:] {
			if clean := cleanRelativePath(changed); clean != "" && protectedPathReason(clean, nil) == "" {
				entry.Paths = append(entry.Paths, clean)
			}
		}
		if entry.Commit != "" {
			result = append(result, entry)
		}
	}
	return result
}

func normalizeDiagnostics(input []Diagnostic, revision uint64, now time.Time, files []FileRecord) []Diagnostic {
	result := []Diagnostic{}
	for _, diagnostic := range input {
		path := cleanRelativePath(diagnostic.Path)
		record, ok := fileByPath(files, path)
		if path == "" || !ok || record.Class == ContentProtected || diagnostic.Line < 1 ||
			strings.TrimSpace(diagnostic.Message) == "" || len(diagnostic.Message) > 4096 {
			continue
		}
		diagnostic.Path, diagnostic.Revision = path, revision
		message, _ := redactSecrets(path, []byte(diagnostic.Message))
		diagnostic.Message = string(message)
		if diagnostic.RecordedAt.IsZero() {
			diagnostic.RecordedAt = now
		}
		result = append(result, diagnostic)
		if len(result) >= 2048 {
			break
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Path == result[right].Path {
			return result[left].Line < result[right].Line
		}
		return result[left].Path < result[right].Path
	})
	return result
}

func detectRenames(before, after []FileRecord, stats *RefreshStats) {
	oldByHash, newByHash := map[string][]string{}, map[string][]string{}
	afterPaths, beforePaths := map[string]struct{}{}, map[string]struct{}{}
	for _, record := range before {
		beforePaths[record.Path] = struct{}{}
		if record.SHA256 != "" {
			oldByHash[record.SHA256] = append(oldByHash[record.SHA256], record.Path)
		}
	}
	for _, record := range after {
		afterPaths[record.Path] = struct{}{}
		if record.SHA256 != "" {
			newByHash[record.SHA256] = append(newByHash[record.SHA256], record.Path)
		}
	}
	for digest, oldPaths := range oldByHash {
		newPaths := newByHash[digest]
		for _, oldPath := range oldPaths {
			if _, remains := afterPaths[oldPath]; remains {
				continue
			}
			for _, newPath := range newPaths {
				if _, existed := beforePaths[newPath]; existed {
					continue
				}
				stats.Renamed = append(stats.Renamed, Rename{From: oldPath, To: newPath, SHA256: digest})
				break
			}
		}
	}
	sort.Slice(stats.Renamed, func(left, right int) bool { return stats.Renamed[left].From < stats.Renamed[right].From })
}

func indexDigest(index ProjectIndex) string {
	digest := sha256.New()
	for _, denied := range index.DeniedPaths {
		_, _ = io.WriteString(digest, "denied\x00"+denied+"\n")
	}
	for _, record := range index.Files {
		_, _ = io.WriteString(digest, record.Path+"\x00"+record.SHA256+"\x00"+string(record.Class)+"\n")
	}
	for _, instruction := range index.Instructions {
		_, _ = io.WriteString(digest, instruction.Path+"\x00"+instruction.SHA256+"\n")
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func diagnosticsDigest(diagnostics []Diagnostic) string {
	raw, _ := json.Marshal(diagnostics)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func safeRelative(root, absolute string) string {
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." {
		return ""
	}
	return filepath.ToSlash(relative)
}

func sortedKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func mergeSorted(left, right []string) []string {
	set := map[string]struct{}{}
	for _, value := range append(append([]string{}, left...), right...) {
		if strings.TrimSpace(value) != "" {
			set[value] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func languageKeyword(value string) bool {
	switch strings.ToLower(value) {
	case "package", "import", "return", "func", "function", "const", "var", "let", "type", "struct", "class", "interface", "public", "private", "export", "default", "string", "error", "context", "true", "false", "none", "self", "this", "async", "await", "from", "where", "match":
		return true
	default:
		return false
	}
}
