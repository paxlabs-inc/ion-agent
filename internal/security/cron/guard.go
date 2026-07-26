// Package cron stores scheduled prompts only after prompt-injection scanning
// and scans them again immediately before execution.
package cron

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

var ErrPromptInjection = errors.New("cron: prompt injection detected")

var (
	base64Token = regexp.MustCompile(`[A-Za-z0-9+/_-]{24,}={0,2}`)
	signatures  = []signature{
		{"instruction_override", []string{
			"ignore previous instruction",
			"ignore all instruction",
			"disregard previous",
			"override system prompt",
			"forget your instruction",
		}},
		{"prompt_exfiltration", []string{
			"reveal system prompt",
			"show developer message",
			"print hidden instruction",
			"exfiltrate prompt",
		}},
		{"safety_bypass", []string{
			"disable safety",
			"bypass policy",
			"remove guardrail",
			"ignore approval",
			"pretend this is approved",
		}},
		{"secret_exfiltration", []string{
			"reveal secret",
			"dump environment",
			"read private key",
			"send vault",
			"upload credential",
		}},
		{"authority_escalation", []string{
			"call core execute",
			"bypass approval",
			"execute shell",
			"spawn agent",
			"send email without approval",
		}},
	}
)

type signature struct {
	category string
	phrases  []string
}

// Finding is one explainable scanner match.
type Finding struct {
	Category string
	Pattern  string
	Encoded  bool
}

// Scanner is the mandatory boundary used on both registration and execution.
type Scanner interface {
	Scan(string) []Finding
}

// DefaultScanner detects instruction override, authority escalation, and data
// exfiltration language, including URL-encoded and base64-encoded payloads.
type DefaultScanner struct{}

func (DefaultScanner) Scan(prompt string) []Finding {
	seen := make(map[string]Finding)
	scanText(normalize(prompt), false, seen)
	if decoded, err := url.QueryUnescape(prompt); err == nil && decoded != prompt {
		scanText(normalize(decoded), true, seen)
	}
	for _, token := range base64Token.FindAllString(prompt, -1) {
		for _, encoding := range []*base64.Encoding{
			base64.StdEncoding,
			base64.RawStdEncoding,
			base64.URLEncoding,
			base64.RawURLEncoding,
		} {
			decoded, err := encoding.DecodeString(token)
			if err == nil && printable(decoded) {
				scanText(normalize(string(decoded)), true, seen)
			}
		}
	}
	result := make([]Finding, 0, len(seen))
	for _, finding := range seen {
		result = append(result, finding)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Category == result[right].Category {
			return result[left].Pattern < result[right].Pattern
		}
		return result[left].Category < result[right].Category
	})
	return result
}

func scanText(text string, encoded bool, seen map[string]Finding) {
	for _, signature := range signatures {
		for _, phrase := range signature.phrases {
			if strings.Contains(text, phrase) {
				key := signature.category + "\x00" + phrase
				seen[key] = Finding{
					Category: signature.category,
					Pattern:  phrase,
					Encoded:  encoded,
				}
			}
		}
	}
}

func normalize(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	space := false
	for _, character := range strings.ToLower(value) {
		switch {
		case character == '\u200b' || character == '\u200c' ||
			character == '\u200d' || character == '\ufeff':
			continue
		case unicode.IsLetter(character) || unicode.IsDigit(character):
			builder.WriteRune(character)
			space = false
		default:
			if !space {
				builder.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

func printable(value []byte) bool {
	if len(value) == 0 {
		return false
	}
	printableCount := 0
	for _, character := range string(value) {
		if unicode.IsPrint(character) || unicode.IsSpace(character) {
			printableCount++
		}
	}
	return printableCount*10 >= len([]rune(string(value)))*8
}

// Runner is the true execution boundary for an approved cron prompt.
type Runner interface {
	RunCron(context.Context, string, string) error
}

// Job is an immutable snapshot of a guarded scheduled prompt.
type Job struct {
	ID       string
	Schedule string
	Prompt   string
}

// Registry prevents unscanned prompts from reaching the cron runner.
type Registry struct {
	mu      sync.RWMutex
	scanner Scanner
	runner  Runner
	jobs    map[string]Job
}

func NewRegistry(scanner Scanner, runner Runner) (*Registry, error) {
	if scanner == nil || runner == nil {
		return nil, fmt.Errorf("cron: scanner and runner are required")
	}
	return &Registry{scanner: scanner, runner: runner, jobs: make(map[string]Job)}, nil
}

// Put creates or replaces a job only when its prompt scans cleanly.
func (registry *Registry) Put(job Job) error {
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.Schedule) == "" ||
		strings.TrimSpace(job.Prompt) == "" {
		return fmt.Errorf("cron: complete job is required")
	}
	if findings := registry.scanner.Scan(job.Prompt); len(findings) != 0 {
		return fmt.Errorf("%w: %s", ErrPromptInjection, summarize(findings))
	}
	registry.mu.Lock()
	registry.jobs[job.ID] = job
	registry.mu.Unlock()
	return nil
}

// Execute copies the stored job, rescans it, then invokes the runner. This
// catches newly added signatures and corrupted persisted prompts.
func (registry *Registry) Execute(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	registry.mu.RLock()
	job, exists := registry.jobs[id]
	registry.mu.RUnlock()
	if !exists {
		return fmt.Errorf("cron: unknown job %q", id)
	}
	if findings := registry.scanner.Scan(job.Prompt); len(findings) != 0 {
		return fmt.Errorf("%w: %s", ErrPromptInjection, summarize(findings))
	}
	return registry.runner.RunCron(ctx, job.ID, job.Prompt)
}

func (registry *Registry) Get(id string) (Job, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	job, exists := registry.jobs[id]
	return job, exists
}

func summarize(findings []Finding) string {
	parts := make([]string, len(findings))
	for index, finding := range findings {
		parts[index] = finding.Category + ":" + finding.Pattern
	}
	return strings.Join(parts, ",")
}
