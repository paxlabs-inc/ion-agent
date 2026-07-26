// Package premise implements the load-bearing premise ledger. Every plan's
// factual assumptions are extracted with provenance, visible in the activation,
// and enforced: refuted premises block dependent dispatches and force replanning.
package premise

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// Status is the lifecycle state of a premise.
type Status string

const (
	Assumption Status = "assumption"
	Cited      Status = "cited"
	Refuted    Status = "refuted"
)

// Source identifies where a premise's support comes from.
type Source string

const (
	SourceSelfModel    Source = "self-model"
	SourceCortex       Source = "cortex"
	SourceToolEvidence Source = "tool-evidence"
	SourceUser         Source = "user"
	SourceAssumption   Source = "assumption"
)

// Premise is one load-bearing factual claim in the current plan.
type Premise struct {
	ID        uuid.UUID          `json:"id"`
	Statement string             `json:"statement"`
	Status    Status             `json:"status"`
	Source    Source             `json:"source"`
	Citation  *protocol.Citation `json:"citation,omitempty"`
	Load      float64            `json:"load"`
	CreatedAt time.Time          `json:"created_at"`
	RefutedAt *time.Time         `json:"refuted_at,omitempty"`
	PlanID    uuid.UUID          `json:"plan_id"`
	StepIndex int                `json:"step_index"`
	MemoryID  *uuid.UUID         `json:"memory_id,omitempty"`
	Affected  []string           `json:"affected_subgoals,omitempty"`
}

// Clock abstracts time for deterministic testing.
type Clock interface {
	Now() time.Time
}

// Ledger manages the run-state premise collection.
type Ledger struct {
	mu     sync.RWMutex
	clock  Clock
	items  []*Premise
	nextID int
	planID uuid.UUID
}

// Snapshot is the durable, encryption-ready state of one premise ledger.
type Snapshot struct {
	PlanID uuid.UUID  `json:"plan_id"`
	Items  []*Premise `json:"items"`
}

// New creates an empty ledger.
func New(clock Clock) (*Ledger, error) {
	if clock == nil {
		return nil, fmt.Errorf("premise: clock is required")
	}
	return &Ledger{
		clock:  clock,
		nextID: 1,
		planID: uuid.New(),
	}, nil
}

// Restore validates and reconstructs a premise ledger from durable state.
func Restore(clock Clock, snapshot Snapshot) (*Ledger, error) {
	if clock == nil {
		return nil, fmt.Errorf("premise: clock is required")
	}
	if snapshot.PlanID == uuid.Nil {
		return nil, fmt.Errorf("premise: durable plan ID is required")
	}
	ledger := &Ledger{
		clock: clock, nextID: 1, planID: snapshot.PlanID,
		items: make([]*Premise, 0, len(snapshot.Items)),
	}
	seen := make(map[uuid.UUID]struct{}, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if item == nil || item.ID == uuid.Nil ||
			item.PlanID == uuid.Nil || strings.TrimSpace(item.Statement) == "" ||
			!item.Source.Valid() || !item.Status.valid() ||
			item.CreatedAt.IsZero() || item.Load < 0 || item.Load > 1 {
			return nil, fmt.Errorf("premise: invalid durable premise")
		}
		if _, exists := seen[item.ID]; exists {
			return nil, fmt.Errorf("premise: duplicate durable premise %s", item.ID)
		}
		if item.Citation != nil {
			if err := item.Citation.Validate(); err != nil {
				return nil, fmt.Errorf("premise: invalid durable citation: %w", err)
			}
		}
		seen[item.ID] = struct{}{}
		ledger.items = append(ledger.items, clonePremise(item))
	}
	return ledger, nil
}

func (status Status) valid() bool {
	switch status {
	case Assumption, Cited, Refuted:
		return true
	default:
		return false
	}
}

// Snapshot returns a defensive copy suitable for encrypted persistence.
func (ledger *Ledger) Snapshot() Snapshot {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	snapshot := Snapshot{
		PlanID: ledger.planID,
		Items:  make([]*Premise, 0, len(ledger.items)),
	}
	for _, item := range ledger.items {
		snapshot.Items = append(snapshot.Items, clonePremise(item))
	}
	return snapshot
}

// Add inserts a new premise with auto-assigned ID and timestamp.
func (ledger *Ledger) Add(statement string, source Source, stepIndex int) (*Premise, error) {
	if strings.TrimSpace(statement) == "" {
		return nil, fmt.Errorf("premise: statement is required")
	}
	if !source.Valid() {
		return nil, fmt.Errorf("premise: invalid source %q", source)
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	premise := &Premise{
		ID:        uuid.New(),
		Statement: statement,
		Status:    Assumption,
		Source:    source,
		Load:      0.5,
		CreatedAt: ledger.clock.Now(),
		PlanID:    ledger.planID,
		StepIndex: stepIndex,
	}
	ledger.items = append(ledger.items, premise)
	return clonePremise(premise), nil
}

// Valid reports whether source is part of the closed provenance vocabulary.
func (source Source) Valid() bool {
	switch source {
	case SourceSelfModel, SourceCortex, SourceToolEvidence, SourceUser,
		SourceAssumption:
		return true
	default:
		return false
	}
}

// Attach records the task-DAG subgoals whose dispatch depends on a premise.
func (ledger *Ledger) Attach(id uuid.UUID, subgoalIDs []string) error {
	normalized := uniqueNonEmpty(subgoalIDs)
	if len(normalized) == 0 {
		return fmt.Errorf("premise: at least one affected subgoal is required")
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for _, item := range ledger.items {
		if item.ID == id {
			item.Affected = normalized
			return nil
		}
	}
	return fmt.Errorf("premise: not found: %s", id)
}

// LinkMemory binds a load-bearing premise to the Cortex memory whose archival
// would invalidate it.
func (ledger *Ledger) LinkMemory(id uuid.UUID, memoryID uuid.UUID) error {
	if memoryID == uuid.Nil {
		return fmt.Errorf("premise: memory ID is required")
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for _, item := range ledger.items {
		if item.ID == id {
			linked := memoryID
			item.MemoryID = &linked
			return nil
		}
	}
	return fmt.Errorf("premise: not found: %s", id)
}

// Cite marks a premise as cited with cryptographic provenance.
func (ledger *Ledger) Cite(id uuid.UUID, citation protocol.Citation) error {
	if err := citation.Validate(); err != nil {
		return fmt.Errorf("premise: invalid citation: %w", err)
	}
	if !citation.Verified {
		return fmt.Errorf("premise: citation has not been verified")
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for _, item := range ledger.items {
		if item.ID == id {
			item.Status = Cited
			citationCopy := citation
			item.Citation = &citationCopy
			return nil
		}
	}
	return fmt.Errorf("premise: not found: %s", id)
}

// Refute marks a premise as refuted, recording the refutation time.
func (ledger *Ledger) Refute(id uuid.UUID, citation *protocol.Citation) error {
	if citation != nil {
		if err := citation.Validate(); err != nil {
			return fmt.Errorf("premise: invalid refutation citation: %w", err)
		}
		if !citation.Verified {
			return fmt.Errorf("premise: refutation citation has not been verified")
		}
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	now := ledger.clock.Now()
	for _, item := range ledger.items {
		if item.ID == id {
			if item.Status == Refuted {
				return nil
			}
			item.Status = Refuted
			item.RefutedAt = &now
			if citation != nil {
				citationCopy := *citation
				item.Citation = &citationCopy
			}
			return nil
		}
	}
	return fmt.Errorf("premise: not found: %s", id)
}

// Assumptions returns all standing (non-refuted) assumptions.
func (ledger *Ledger) Assumptions() []*Premise {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	var result []*Premise
	for _, item := range ledger.items {
		if item.Status == Assumption {
			result = append(result, clonePremise(item))
		}
	}
	return result
}

// UnrevisedRefuted returns refuted premises whose forced revision has not
// yet been acknowledged by a plan change.
func (ledger *Ledger) UnrevisedRefuted() []*Premise {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	var result []*Premise
	for _, item := range ledger.items {
		if item.Status == Refuted {
			result = append(result, clonePremise(item))
		}
	}
	return result
}

// Active returns all non-refuted premises.
func (ledger *Ledger) Active() []*Premise {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	var result []*Premise
	for _, item := range ledger.items {
		if item.Status != Refuted {
			result = append(result, clonePremise(item))
		}
	}
	return result
}

// Get returns a premise by ID.
func (ledger *Ledger) Get(id uuid.UUID) (*Premise, bool) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	for _, item := range ledger.items {
		if item.ID == id {
			return clonePremise(item), true
		}
	}
	return nil, false
}

func clonePremise(source *Premise) *Premise {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Affected = append([]string(nil), source.Affected...)
	if source.Citation != nil {
		citation := *source.Citation
		cloned.Citation = &citation
	}
	if source.RefutedAt != nil {
		refutedAt := *source.RefutedAt
		cloned.RefutedAt = &refutedAt
	}
	if source.MemoryID != nil {
		memoryID := *source.MemoryID
		cloned.MemoryID = &memoryID
	}
	return &cloned
}

// PlanChanged resets the ledger for a new plan revision. Refuted premises
// are marked as revised; active premises are cleared.
func (ledger *Ledger) PlanChanged() {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.planID = uuid.New()
	ledger.items = nil
}

// ReviseAffected starts a plan revision for only the listed task-DAG
// subtrees. Premises supporting unrelated subgoals remain active.
func (ledger *Ledger) ReviseAffected(subgoalIDs []string) {
	affected := make(map[string]struct{})
	for _, id := range uniqueNonEmpty(subgoalIDs) {
		affected[id] = struct{}{}
	}
	if len(affected) == 0 {
		ledger.PlanChanged()
		return
	}

	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	ledger.planID = uuid.New()
	kept := ledger.items[:0]
	for _, item := range ledger.items {
		remove := false
		for _, id := range item.Affected {
			if _, ok := affected[id]; ok {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, item)
		}
	}
	ledger.items = kept
}

// HasRefuted returns true if any premise is refuted and blocks dispatch.
func (ledger *Ledger) HasRefuted() bool {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	for _, item := range ledger.items {
		if item.Status == Refuted {
			return true
		}
	}
	return false
}

// Render produces a deterministic text rendering of the ledger for injection
// into the activation context.
func (ledger *Ledger) Render() string {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	if len(ledger.items) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("## Premises\n")
	for _, item := range ledger.items {
		switch item.Status {
		case Assumption:
			builder.WriteString(fmt.Sprintf("- [ASSUMPTION] %s\n", item.Statement))
		case Cited:
			builder.WriteString(fmt.Sprintf("- [CITED] %s (source: %s)\n", item.Statement, item.Source))
		case Refuted:
			builder.WriteString(fmt.Sprintf("- [REFUTED] %s — REPLANNING REQUIRED\n", item.Statement))
		}
	}
	return builder.String()
}

// Plan is the committing assistant step from which premises are extracted.
// Tool calls are included because their expectations and arguments often carry
// the load-bearing facts that prose omits.
type Plan struct {
	Text      string                        `json:"text"`
	ToolCalls []protocol.NormalizedToolCall `json:"tool_calls"`
}

// Extractor derives premises from a committing plan step.
type Extractor interface {
	Extract(ctx context.Context, plan Plan) ([]Premise, error)
}

// DeterministicExtractor uses pattern matching to find self-referential
// capability claims and the explicit expectations attached to tool calls.
type DeterministicExtractor struct{}

// Extract finds premises via deterministic patterns.
func (DeterministicExtractor) Extract(_ context.Context, plan Plan) ([]Premise, error) {
	var premises []Premise
	seen := make(map[string]struct{})
	add := func(statement string) {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			return
		}
		key := strings.ToLower(statement)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		premises = append(premises, Premise{
			Statement: statement,
			Source:    SourceAssumption,
		})
	}

	planText := plan.Text
	lower := strings.ToLower(planText)
	selfPatterns := []string{
		"i can ", "i have ", "i am able", "i know ",
		"i'm able", "i've done", "i was able",
	}
	for _, pattern := range selfPatterns {
		if idx := strings.Index(lower, pattern); idx >= 0 {
			end := idx + len(pattern)
			for end < len(planText) && planText[end] != '.' && planText[end] != '\n' {
				end++
			}
			statement := strings.TrimSpace(planText[idx:end])
			if len(statement) > 10 {
				// A self-model statement without a verified ToolEvent citation
				// remains an explicit assumption.
				add(statement)
			}
		}
	}
	for _, call := range plan.ToolCalls {
		var arguments map[string]json.RawMessage
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			return nil, fmt.Errorf(
				"premise: parse %s tool arguments: %w", call.Name, err,
			)
		}
		raw, ok := arguments["expect"]
		if !ok {
			continue
		}
		var expectation string
		if err := json.Unmarshal(raw, &expectation); err != nil {
			return nil, fmt.Errorf(
				"premise: parse %s expectation: %w", call.Name, err,
			)
		}
		expectation = strings.TrimSpace(expectation)
		if expectation != "" {
			add(fmt.Sprintf("%s: %s", call.Name, expectation))
		}
	}
	return premises, nil
}

func uniqueNonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
