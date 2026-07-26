// Package skills implements procedural memory stored as auditable SKILL.md
// bundles.
package skills

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const fileName = "SKILL.md"

// Skill is the binding YAML-frontmatter schema plus optional explanatory body.
type Skill struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Trigger       string   `json:"trigger"`
	Aliases       []string `json:"aliases,omitempty"`
	Category      string   `json:"category,omitempty"`
	Platforms     []string `json:"platforms,omitempty"`
	RequiredTools []string `json:"required_tools,omitempty"`
	Steps         []string `json:"steps"`
	Pitfalls      []string `json:"pitfalls"`
	Verification  []string `json:"verification"`
	Origin        string   `json:"origin,omitempty"`
	SourcePath    string   `json:"source_path,omitempty"`
	SourceDigest  string   `json:"source_digest,omitempty"`
	Resources     []string `json:"resources,omitempty"`
	Revision      int      `json:"revision"`
	Uses          int      `json:"uses"`
	Body          string   `json:"body,omitempty"`
}

// MatchContext constrains procedural selection to the current execution
// environment. Empty capability fields retain backward-compatible matching.
type MatchContext struct {
	Platform string
	Tools    map[string]struct{}
}

// Store owns a root-bounded set of skill bundles.
type Store struct {
	root string
	mu   sync.Mutex
}

func NewStore(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("skills: root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("skills: create root: %w", err)
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: absolute}, nil
}

// List returns every valid installed skill in stable name order. Malformed
// bundles are reported instead of silently disappearing from the operator
// surface.
func (store *Store) List(ctx context.Context) ([]Skill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, err
	}
	result := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skill, err := store.Load(ctx, entry.Name())
		if err != nil {
			return nil, err
		}
		result = append(result, skill)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result, nil
}

// Summaries returns bounded active-skill metadata for operator projections.
func (store *Store) Summaries(ctx context.Context) ([]SkillSummary, error) {
	active, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]SkillSummary, 0, len(active))
	for _, skill := range active {
		result = append(result, summarizeSkill(skill))
	}
	return result, nil
}

// Save persists a newly learned approach as SKILL.md.
func (store *Store) Save(ctx context.Context, skill Skill) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validate(skill); err != nil {
		return "", err
	}
	if skill.Revision <= 0 {
		skill.Revision = 1
	}
	if strings.TrimSpace(skill.Origin) == "" {
		skill.Origin = "authored"
	}
	slug := slugify(skill.Name)
	directory := filepath.Join(store.root, slug)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, fileName)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("skills: %q already exists", skill.Name)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := writeAtomic(path, encode(skill)); err != nil {
		return "", err
	}
	return path, nil
}

// Load parses and validates one named SKILL.md.
func (store *Store) Load(ctx context.Context, name string) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	path := filepath.Join(store.root, slugify(name), fileName)
	payload, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("skills: load %q: %w", name, err)
	}
	skill, err := decode(string(payload))
	if err != nil {
		return Skill{}, fmt.Errorf("skills: parse %q: %w", name, err)
	}
	return skill, nil
}

// Match loads the most specific recurring procedure whose trigger terms occur
// in the problem text. Loading increments its use count durably.
func (store *Store) Match(ctx context.Context, problem string) (*Skill, error) {
	found, err := store.MatchAll(ctx, problem, MatchContext{}, 1)
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return &found[0], nil
}

// MatchAll returns every materially applicable skill up to a strict bounded
// limit, ordered by task relevance and compatibility.
func (store *Store) MatchAll(
	ctx context.Context,
	problem string,
	matchContext MatchContext,
	limit int,
) ([]Skill, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	problem = strings.ToLower(strings.TrimSpace(problem))
	if problem == "" {
		return []Skill{}, nil
	}
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return nil, err
	}
	type scoredSkill struct {
		skill Skill
		score int
	}
	var matches []scoredSkill
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skill, err := store.Load(ctx, entry.Name())
		if err != nil {
			continue
		}
		if !skillApplicable(skill, matchContext) {
			continue
		}
		if score := skillMatchScore(skill, problem); score >= minimumMatchScore {
			matches = append(matches, scoredSkill{skill: skill, score: score})
		}
	}
	if len(matches) == 0 {
		return []Skill{}, nil
	}
	sort.Slice(matches, func(left, right int) bool {
		if matches[left].score != matches[right].score {
			return matches[left].score > matches[right].score
		}
		return matches[left].skill.Name < matches[right].skill.Name
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	result := make([]Skill, 0, len(matches))
	for _, match := range matches {
		match.skill.Uses++
		if err := store.replace(ctx, match.skill); err != nil {
			return nil, err
		}
		result = append(result, match.skill)
	}
	return result, nil
}

// Refinement is evidence learned while following an existing procedure.
type Refinement struct {
	Steps        []string
	Pitfalls     []string
	Verification []string
	BodyNote     string
}

// Refine evolves a skill without discarding its previous procedural content.
func (store *Store) Refine(
	ctx context.Context,
	name string,
	refinement Refinement,
) (Skill, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	skill, err := store.Load(ctx, name)
	if err != nil {
		return Skill{}, err
	}
	if err := store.archiveActive(skill); err != nil {
		return Skill{}, err
	}
	skill = applyRefinement(skill, refinement)
	skill.Revision++
	if err := store.replace(ctx, skill); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func (store *Store) replace(ctx context.Context, skill Skill) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validate(skill); err != nil {
		return err
	}
	path := filepath.Join(store.root, slugify(skill.Name), fileName)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return writeAtomic(path, encode(skill))
}

func validate(skill Skill) error {
	if strings.TrimSpace(skill.Name) == "" ||
		strings.TrimSpace(skill.Trigger) == "" ||
		len(unique(skill.Steps)) == 0 ||
		len(unique(skill.Pitfalls)) == 0 ||
		len(unique(skill.Verification)) == 0 {
		return fmt.Errorf("skills: name, trigger, steps, pitfalls, and verification are required")
	}
	return nil
}

func encode(skill Skill) []byte {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("name: " + quote(skill.Name) + "\n")
	if strings.TrimSpace(skill.Description) != "" {
		builder.WriteString("description: " + quote(skill.Description) + "\n")
	}
	builder.WriteString("trigger: " + quote(skill.Trigger) + "\n")
	writeOptionalList(&builder, "aliases", skill.Aliases)
	if strings.TrimSpace(skill.Category) != "" {
		builder.WriteString("category: " + quote(skill.Category) + "\n")
	}
	writeOptionalList(&builder, "platforms", skill.Platforms)
	writeOptionalList(&builder, "required_tools", skill.RequiredTools)
	writeList(&builder, "steps", skill.Steps)
	writeList(&builder, "pitfalls", skill.Pitfalls)
	writeList(&builder, "verification", skill.Verification)
	if strings.TrimSpace(skill.Origin) != "" {
		builder.WriteString("origin: " + quote(skill.Origin) + "\n")
	}
	if strings.TrimSpace(skill.SourcePath) != "" {
		builder.WriteString("source_path: " + quote(skill.SourcePath) + "\n")
	}
	if strings.TrimSpace(skill.SourceDigest) != "" {
		builder.WriteString("source_digest: " + quote(skill.SourceDigest) + "\n")
	}
	writeOptionalList(&builder, "resources", skill.Resources)
	builder.WriteString("revision: " + strconv.Itoa(skill.Revision) + "\n")
	builder.WriteString("uses: " + strconv.Itoa(skill.Uses) + "\n")
	builder.WriteString("---\n")
	if body := strings.TrimSpace(skill.Body); body != "" {
		builder.WriteString("\n")
		builder.WriteString(body)
		builder.WriteString("\n")
	}
	return []byte(builder.String())
}

func writeOptionalList(builder *strings.Builder, name string, values []string) {
	if len(unique(values)) == 0 {
		return
	}
	writeList(builder, name, values)
}

func writeList(builder *strings.Builder, name string, values []string) {
	builder.WriteString(name + ":\n")
	for _, value := range unique(values) {
		builder.WriteString("  - " + quote(value) + "\n")
	}
}

func quote(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func decode(payload string) (Skill, error) {
	scanner := bufio.NewScanner(strings.NewReader(payload))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Skill{}, fmt.Errorf("missing YAML frontmatter")
	}
	var skill Skill
	var list *[]string
	inFrontmatter := true
	var body []string
	for scanner.Scan() {
		line := scanner.Text()
		if inFrontmatter && strings.TrimSpace(line) == "---" {
			inFrontmatter = false
			list = nil
			continue
		}
		if !inFrontmatter {
			body = append(body, line)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") && list != nil {
			value, err := strconv.Unquote(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			if err != nil {
				return Skill{}, err
			}
			*list = append(*list, value)
			continue
		}
		key, raw, found := strings.Cut(trimmed, ":")
		if !found {
			return Skill{}, fmt.Errorf("invalid frontmatter line %q", line)
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		switch key {
		case "name":
			value, err := strconv.Unquote(raw)
			if err != nil {
				return Skill{}, err
			}
			skill.Name = value
		case "description":
			value, err := strconv.Unquote(raw)
			if err != nil {
				return Skill{}, err
			}
			skill.Description = value
		case "trigger":
			value, err := strconv.Unquote(raw)
			if err != nil {
				return Skill{}, err
			}
			skill.Trigger = value
		case "aliases":
			list = &skill.Aliases
		case "category":
			value, err := strconv.Unquote(raw)
			if err != nil {
				return Skill{}, err
			}
			skill.Category = value
		case "platforms":
			list = &skill.Platforms
		case "required_tools":
			list = &skill.RequiredTools
		case "steps":
			list = &skill.Steps
		case "pitfalls":
			list = &skill.Pitfalls
		case "verification":
			list = &skill.Verification
		case "origin":
			value, err := strconv.Unquote(raw)
			if err != nil {
				return Skill{}, err
			}
			skill.Origin = value
		case "source_path":
			value, err := strconv.Unquote(raw)
			if err != nil {
				return Skill{}, err
			}
			skill.SourcePath = value
		case "source_digest":
			value, err := strconv.Unquote(raw)
			if err != nil {
				return Skill{}, err
			}
			skill.SourceDigest = value
		case "resources":
			list = &skill.Resources
		case "revision":
			skill.Revision, _ = strconv.Atoi(raw)
		case "uses":
			skill.Uses, _ = strconv.Atoi(raw)
		default:
			return Skill{}, fmt.Errorf("unknown frontmatter field %q", key)
		}
	}
	if err := scanner.Err(); err != nil {
		return Skill{}, err
	}
	if inFrontmatter {
		return Skill{}, fmt.Errorf("unterminated YAML frontmatter")
	}
	skill.Body = strings.TrimSpace(strings.Join(body, "\n"))
	if err := validate(skill); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func writeAtomic(path string, payload []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".skill-*.tmp")
	if err != nil {
		return err
	}
	tempName := temporary.Name()
	defer os.Remove(tempName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func slugify(value string) string {
	var builder strings.Builder
	dash := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
			dash = false
		} else if !dash && builder.Len() > 0 {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func unique(values []string) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
