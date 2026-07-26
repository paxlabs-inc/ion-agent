// Package activation implements the activation composer that assembles
// the memory context for each provider call with a fixed token budget.
//
// Tiers (in priority order):
// 1. Pinned: identity, immutable constraints, active goal, relevant premises
// 2. Timeline: epoch rollups over 90 days
// 3. Dreams: optional insertion point (empty until Dreamweaver exists)
// 4. Recent: hour-level rollups over 24 hours
// 5. Transcript: current session
//
// Features:
// - Fixed configurable 32K token budget (default)
// - Deterministic salience ordering
// - Trim lowest salience first
// - Pinned is never trimmed
// - Memory prefetch before every provider call (HNSW k=10, FTS5, salience scan)
// - Cross-session FTS5 with strict user/session isolation
package activation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	// DefaultTokenBudget is the default activation context size.
	DefaultTokenBudget = 32000
)

// Tier identifies an activation tier.
type Tier int

const (
	TierPinned     Tier = iota // never trimmed
	TierTimeline               // epoch rollups
	TierDreams                 // placeholder
	TierRecent                 // hour-level rollups
	TierTranscript             // current session
)

func (t Tier) String() string {
	switch t {
	case TierPinned:
		return "pinned"
	case TierTimeline:
		return "timeline"
	case TierDreams:
		return "dreams"
	case TierRecent:
		return "recent"
	case TierTranscript:
		return "transcript"
	default:
		return "unknown"
	}
}

// Entry is one item in the activation context.
type Entry struct {
	Tier     Tier    `json:"tier"`
	Content  string  `json:"content"`
	Salience float64 `json:"salience"`
	Tokens   int     `json:"tokens"`
}

// Composer assembles the activation context for each provider call.
type Composer struct {
	mu          sync.RWMutex
	tokenBudget int
	entries     []Entry
}

// NewComposer creates an activation composer with the given token budget.
func NewComposer(tokenBudget int) *Composer {
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}
	return &Composer{
		tokenBudget: tokenBudget,
	}
}

// Add adds an entry to the activation context.
func (c *Composer) Add(tier Tier, content string, salience float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, Entry{
		Tier:     tier,
		Content:  content,
		Salience: salience,
		Tokens:   estimateTokens(content),
	})
}

// Compose assembles the activation context within the token budget.
// It uses deterministic salience ordering and trims lowest-salience
// entries first. Pinned tier entries are never trimmed.
func (c *Composer) Compose() []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Sort by tier first (pinned first), then by salience descending.
	sorted := make([]Entry, len(c.entries))
	copy(sorted, c.entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Tier != sorted[j].Tier {
			return sorted[i].Tier < sorted[j].Tier
		}
		return sorted[i].Salience > sorted[j].Salience
	})

	// Calculate total tokens.
	totalTokens := 0
	for _, entry := range sorted {
		totalTokens += entry.Tokens
	}

	// If within budget, return all.
	if totalTokens <= c.tokenBudget {
		return sorted
	}

	// Trim lowest-salience non-pinned entries until within budget.
	// Build result in reverse priority: trim from the end first.
	result := make([]Entry, 0, len(sorted))
	remaining := c.tokenBudget

	// First pass: include all pinned entries.
	for _, entry := range sorted {
		if entry.Tier == TierPinned {
			result = append(result, entry)
			remaining -= entry.Tokens
		}
	}
	// "Pinned is never trimmed" takes precedence over the configured budget.
	// If pinned context alone exceeds it, return all pinned entries and no
	// lower-priority context.
	if remaining <= 0 {
		return result
	}

	// Second pass: include non-pinned entries by salience (highest first).
	nonPinned := make([]Entry, 0)
	for _, entry := range sorted {
		if entry.Tier != TierPinned {
			nonPinned = append(nonPinned, entry)
		}
	}
	sort.Slice(nonPinned, func(i, j int) bool {
		return nonPinned[i].Salience > nonPinned[j].Salience
	})

	for _, entry := range nonPinned {
		if remaining >= entry.Tokens {
			result = append(result, entry)
			remaining -= entry.Tokens
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Tier != result[j].Tier {
			return result[i].Tier < result[j].Tier
		}
		return result[i].Salience > result[j].Salience
	})
	return result
}

// TokenBudget returns the configured token budget.
func (c *Composer) TokenBudget() int {
	return c.tokenBudget
}

// Clear removes all entries.
func (c *Composer) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

// EntryCount returns the number of entries.
func (c *Composer) EntryCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// estimateTokens provides a rough token count for a string.
// Approximation: 1 token per 4 characters for English text.
func estimateTokens(text string) int {
	chars := len([]rune(text))
	tokens := (chars + 3) / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// PrefetchResult holds the results of a memory prefetch.
type PrefetchResult struct {
	HNSWResults  []string `json:"hnsw_results"`
	FTS5Results  []string `json:"fts5_results"`
	SalienceHits []string `json:"salience_hits"`
	PremiseRefs  []string `json:"premise_refs"`
}

// Request is the authorization and retrieval scope for one provider call.
type Request struct {
	Query     string
	SessionID string
	UserID    string
}

// Prefetcher retrieves relevant memories before a provider call.
type Prefetcher interface {
	Prefetch(context.Context, Request) (*PrefetchResult, error)
}

// TierSource loads the durable 4+1 base working set for one turn.
type TierSource interface {
	Entries(context.Context, Request) ([]Entry, error)
}

// Service refreshes and renders memory activation before every provider call.
type Service struct {
	tokenBudget int
	prefetcher  Prefetcher
	source      TierSource
}

func NewService(tokenBudget int, prefetcher Prefetcher, source TierSource) (*Service, error) {
	if prefetcher == nil {
		return nil, fmt.Errorf("activation: prefetcher is required")
	}
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}
	return &Service{
		tokenBudget: tokenBudget,
		prefetcher:  prefetcher,
		source:      source,
	}, nil
}

// Activate implements the agent's narrow memory-activation boundary.
func (service *Service) Activate(
	ctx context.Context,
	query string,
	userID string,
	sessionID string,
) (string, error) {
	request := Request{
		Query: strings.TrimSpace(query), UserID: strings.TrimSpace(userID),
		SessionID: strings.TrimSpace(sessionID),
	}
	if request.Query == "" || request.UserID == "" || request.SessionID == "" {
		return "", fmt.Errorf("activation: query, user, and session are required")
	}
	composer := NewComposer(service.tokenBudget)
	if service.source != nil {
		entries, err := service.source.Entries(ctx, request)
		if err != nil {
			return "", fmt.Errorf("activation: load tiers: %w", err)
		}
		for _, entry := range entries {
			if entry.Tier < TierPinned || entry.Tier > TierTranscript ||
				strings.TrimSpace(entry.Content) == "" {
				return "", fmt.Errorf("activation: invalid tier source entry")
			}
			composer.Add(entry.Tier, entry.Content, entry.Salience)
		}
	}
	prefetched, err := service.prefetcher.Prefetch(ctx, request)
	if err != nil {
		return "", fmt.Errorf("activation: prefetch: %w", err)
	}
	if prefetched == nil {
		return "", fmt.Errorf("activation: prefetch returned nil")
	}
	addUnique := func(tier Tier, salience float64, values []string) {
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			composer.Add(tier, value, salience)
		}
	}
	addUnique(TierPinned, 1, prefetched.PremiseRefs)
	addUnique(TierTimeline, 0.75, prefetched.FTS5Results)
	addUnique(TierRecent, 0.85, prefetched.HNSWResults)
	addUnique(TierRecent, 0.7, prefetched.SalienceHits)
	return Render(composer.Compose()), nil
}

// Render produces the final activation string for injection into the provider context.
func Render(entries []Entry) string {
	var builder strings.Builder
	// Group by tier.
	tierGroups := make(map[Tier][]Entry)
	for _, entry := range entries {
		tierGroups[entry.Tier] = append(tierGroups[entry.Tier], entry)
	}

	tierOrder := []Tier{TierPinned, TierTimeline, TierDreams, TierRecent, TierTranscript}
	for _, tier := range tierOrder {
		items := tierGroups[tier]
		if len(items) == 0 {
			continue
		}
		builder.WriteString(fmt.Sprintf("## %s\n", tier.String()))
		for _, item := range items {
			builder.WriteString(item.Content)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
	return builder.String()
}
