// Package dreamweaver implements bounded, provenance-preserving memory
// reorganization during idle time.
package dreamweaver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
)

const (
	DefaultClusterSize    = 12
	MaxBeliefsPerCycle    = 5
	MinSupportingMemories = 3
	DefaultCooldown       = 24 * time.Hour
)

// MemoryStore is the journal-authoritative Cortex surface required by a cycle.
type MemoryStore interface {
	ListByType(memory.Type) []uuid.UUID
	Resolve(uuid.UUID) (*cortex.Memory, error)
	Write(context.Context, memory.Type, []byte, string) (*cortex.Memory, error)
	WriteWithTrust(
		context.Context,
		memory.Type,
		[]byte,
		string,
		cortex.TrustLevel,
	) (*cortex.Memory, error)
	Update(context.Context, uuid.UUID, []byte, string) (*cortex.Memory, error)
}

// MutationGuard enforces SADR-002 before graph reorganization.
type MutationGuard interface {
	AuthorizeDreamweaverMutation(string, memory.Type, string) error
}

// Clock supplies selection and provenance timestamps.
type Clock interface {
	Now() time.Time
}

// Candidate is the immutable model-facing view of one selected source.
type Candidate struct {
	ID          uuid.UUID   `json:"id"`
	Type        memory.Type `json:"type"`
	Text        string      `json:"text"`
	Domain      string      `json:"domain"`
	SourceTrust float64     `json:"source_trust"`
}

// ProposedInsight is one model-generated cross-domain connection.
type ProposedInsight struct {
	Statement       string      `json:"statement"`
	SourceMemoryIDs []uuid.UUID `json:"source_memory_ids"`
}

// PatternGenerator finds cross-domain patterns using no tool surface.
type PatternGenerator interface {
	Generate(context.Context, []Candidate, int) ([]ProposedInsight, error)
}

// Config supplies the real boundaries and bounded selection policy.
type Config struct {
	Store       MemoryStore
	Guard       MutationGuard
	Generator   PatternGenerator
	Clock       Clock
	ClusterSize int
	Cooldown    time.Duration
}

// Engine performs the five-step Dreamweaver cycle.
type Engine struct {
	store       MemoryStore
	guard       MutationGuard
	generator   PatternGenerator
	clock       Clock
	clusterSize int
	cooldown    time.Duration
}

// New constructs a fail-closed Dreamweaver.
func New(config Config) (*Engine, error) {
	if config.Store == nil || config.Guard == nil ||
		config.Generator == nil || config.Clock == nil {
		return nil, fmt.Errorf("dreamweaver: store, guard, generator, and clock are required")
	}
	clusterSize := config.ClusterSize
	if clusterSize <= 0 {
		clusterSize = DefaultClusterSize
	}
	if clusterSize < MinSupportingMemories {
		return nil, fmt.Errorf(
			"dreamweaver: cluster size must be at least %d",
			MinSupportingMemories,
		)
	}
	cooldown := config.Cooldown
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &Engine{
		store:       config.Store,
		guard:       config.Guard,
		generator:   config.Generator,
		clock:       config.Clock,
		clusterSize: clusterSize,
		cooldown:    cooldown,
	}, nil
}

// DerivedBelief is the durable payload stored in the Belief namespace and
// surfaced through the Dreams activation tier.
type DerivedBelief struct {
	Statement       string      `json:"statement"`
	DreamDerived    bool        `json:"dream_derived"`
	Consolidated    bool        `json:"consolidated"`
	SourceMemoryIDs []uuid.UUID `json:"source_memory_ids"`
	SourceDomains   []string    `json:"source_domains"`
	GeneratedAt     time.Time   `json:"generated_at"`
}

// ProvenanceLink makes the source-to-derived relationship directly inspectable.
type ProvenanceLink struct {
	SourceMemoryIDs []uuid.UUID `json:"source_memory_ids"`
	DerivedBeliefID uuid.UUID   `json:"derived_belief_id"`
}

// CycleResult is the complete auditable outcome of one cycle.
type CycleResult struct {
	Selected    []uuid.UUID      `json:"selected"`
	Patterns    int              `json:"patterns"`
	Beliefs     []uuid.UUID      `json:"beliefs"`
	Provenance  []ProvenanceLink `json:"provenance"`
	Reorganized []uuid.UUID      `json:"reorganized"`
	Quarantined bool             `json:"quarantined"`
	Reason      string           `json:"reason,omitempty"`
}

type sourceDocument struct {
	Text              string      `json:"text"`
	Domain            string      `json:"domain"`
	Connections       []uuid.UUID `json:"connections,omitempty"`
	LastReorganizedAt *time.Time  `json:"last_reorganized_at,omitempty"`
	Adversarial       bool        `json:"adversarial,omitempty"`
}

type selectedMemory struct {
	candidate Candidate
	resolved  *cortex.Memory
	document  sourceDocument
	raw       map[string]any
}

type quarantineEvent struct {
	Kind            string      `json:"kind"`
	Reason          string      `json:"reason"`
	SourceMemoryIDs []uuid.UUID `json:"source_memory_ids"`
	QuarantinedAt   time.Time   `json:"quarantined_at"`
}

// RunCycle selects a recent cross-domain cluster, generates at most five
// supported insights, writes derived Beliefs, connects each source to its
// derivative, and returns the surfaced belief IDs.
func (engine *Engine) RunCycle(ctx context.Context) (CycleResult, error) {
	if err := ctx.Err(); err != nil {
		return CycleResult{}, err
	}
	now := engine.clock.Now().UTC()
	selected, err := engine.selectCluster(ctx, now)
	if err != nil {
		return CycleResult{}, err
	}
	result := CycleResult{Selected: selectedIDs(selected)}
	if len(selected) < MinSupportingMemories || countDomains(selected) < 2 {
		return result, nil
	}
	if adversarial := adversarialSources(selected); len(adversarial) > 0 {
		result.Quarantined = true
		result.Reason = "selected cluster contains adversarial source"
		if err := engine.recordQuarantine(ctx, result.Reason, adversarial, now); err != nil {
			return result, err
		}
		return result, nil
	}

	candidates := make([]Candidate, len(selected))
	for index := range selected {
		candidates[index] = selected[index].candidate
	}
	proposals, err := engine.generator.Generate(ctx, candidates, MaxBeliefsPerCycle)
	if err != nil {
		return result, fmt.Errorf("dreamweaver: generate patterns: %w", err)
	}
	valid := validateProposals(proposals, selected)
	if len(valid) > MaxBeliefsPerCycle {
		valid = valid[:MaxBeliefsPerCycle]
	}
	result.Patterns = len(valid)
	for _, proposal := range valid {
		derived, link, err := engine.writeDerivedBelief(ctx, proposal, selected, now)
		if err != nil {
			return result, err
		}
		result.Beliefs = append(result.Beliefs, derived.Head.ID)
		result.Provenance = append(result.Provenance, link)
	}
	if len(result.Beliefs) == 0 {
		return result, nil
	}
	reorganized, err := engine.reorganize(ctx, selected, result.Provenance, now)
	if err != nil {
		return result, err
	}
	result.Reorganized = reorganized
	return result, nil
}

func (engine *Engine) selectCluster(
	ctx context.Context,
	now time.Time,
) ([]selectedMemory, error) {
	allowed := []memory.Type{
		memory.Fact,
		memory.Belief,
		memory.Event,
		memory.Capability,
		memory.Pattern,
	}
	var selected []selectedMemory
	for _, memoryType := range allowed {
		for _, id := range engine.store.ListByType(memoryType) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			resolved, err := engine.store.Resolve(id)
			if err != nil {
				return nil, fmt.Errorf("dreamweaver: resolve candidate %s: %w", id, err)
			}
			document, raw := decodeSourceDocument(resolved.Version.Data)
			if document.LastReorganizedAt != nil &&
				now.Sub(document.LastReorganizedAt.UTC()) < engine.cooldown {
				continue
			}
			selected = append(selected, selectedMemory{
				candidate: Candidate{
					ID:          id,
					Type:        memoryType,
					Text:        document.Text,
					Domain:      document.Domain,
					SourceTrust: resolved.Head.SourceTrust,
				},
				resolved: resolved,
				document: document,
				raw:      raw,
			})
		}
	}
	sort.Slice(selected, func(left, right int) bool {
		leftTime := selected[left].resolved.Head.LastUpdatedAt
		rightTime := selected[right].resolved.Head.LastUpdatedAt
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return selected[left].candidate.ID.String() <
			selected[right].candidate.ID.String()
	})
	if len(selected) > engine.clusterSize {
		selected = selected[:engine.clusterSize]
	}
	return selected, nil
}

func (engine *Engine) writeDerivedBelief(
	ctx context.Context,
	proposal ProposedInsight,
	selected []selectedMemory,
	now time.Time,
) (*cortex.Memory, ProvenanceLink, error) {
	byID := selectedByID(selected)
	sourceIDs := uniqueSortedIDs(proposal.SourceMemoryIDs)
	domains := make(map[string]struct{})
	trust := cortex.TrustVerified
	for _, id := range sourceIDs {
		source := byID[id]
		domains[source.candidate.Domain] = struct{}{}
		sourceTrust := cortex.TrustLevel(source.candidate.SourceTrust)
		if sourceTrust < trust {
			trust = sourceTrust
		}
	}
	sourceDomains := make([]string, 0, len(domains))
	for domain := range domains {
		sourceDomains = append(sourceDomains, domain)
	}
	sort.Strings(sourceDomains)
	payload, err := json.Marshal(DerivedBelief{
		Statement:       strings.TrimSpace(proposal.Statement),
		DreamDerived:    true,
		Consolidated:    true,
		SourceMemoryIDs: sourceIDs,
		SourceDomains:   sourceDomains,
		GeneratedAt:     now,
	})
	if err != nil {
		return nil, ProvenanceLink{}, fmt.Errorf("dreamweaver: encode belief: %w", err)
	}
	if err := engine.guard.AuthorizeDreamweaverMutation(
		"new",
		memory.Belief,
		"derive",
	); err != nil {
		return nil, ProvenanceLink{}, fmt.Errorf("dreamweaver: authorize belief: %w", err)
	}
	derived, err := engine.store.WriteWithTrust(
		ctx,
		memory.Belief,
		payload,
		"dreamweaver",
		trust,
	)
	if err != nil {
		return nil, ProvenanceLink{}, fmt.Errorf("dreamweaver: write belief: %w", err)
	}
	return derived, ProvenanceLink{
		SourceMemoryIDs: sourceIDs,
		DerivedBeliefID: derived.Head.ID,
	}, nil
}

func (engine *Engine) reorganize(
	ctx context.Context,
	selected []selectedMemory,
	links []ProvenanceLink,
	now time.Time,
) ([]uuid.UUID, error) {
	derivedBySource := make(map[uuid.UUID][]uuid.UUID)
	for _, link := range links {
		for _, sourceID := range link.SourceMemoryIDs {
			derivedBySource[sourceID] = append(
				derivedBySource[sourceID],
				link.DerivedBeliefID,
			)
		}
	}
	reorganized := make([]uuid.UUID, 0, len(selected))
	for _, source := range selected {
		derivedIDs := derivedBySource[source.candidate.ID]
		if len(derivedIDs) == 0 {
			continue
		}
		if err := engine.guard.AuthorizeDreamweaverMutation(
			source.candidate.ID.String(),
			source.candidate.Type,
			"connect-and-consolidate",
		); err != nil {
			return reorganized, fmt.Errorf("dreamweaver: authorize reorganization: %w", err)
		}
		connections := append([]uuid.UUID(nil), source.document.Connections...)
		connections = append(connections, derivedIDs...)
		connections = uniqueSortedIDs(connections)
		source.raw["connections"] = connections
		source.raw["last_reorganized_at"] = now
		encoded, err := json.Marshal(source.raw)
		if err != nil {
			return reorganized, fmt.Errorf("dreamweaver: encode reorganization: %w", err)
		}
		if _, err := engine.store.Update(
			ctx,
			source.candidate.ID,
			encoded,
			"dreamweaver",
		); err != nil {
			return reorganized, fmt.Errorf("dreamweaver: update source: %w", err)
		}
		reorganized = append(reorganized, source.candidate.ID)
	}
	return reorganized, nil
}

func (engine *Engine) recordQuarantine(
	ctx context.Context,
	reason string,
	sourceIDs []uuid.UUID,
	now time.Time,
) error {
	payload, err := json.Marshal(quarantineEvent{
		Kind:            "dreamweaver_quarantine",
		Reason:          reason,
		SourceMemoryIDs: uniqueSortedIDs(sourceIDs),
		QuarantinedAt:   now,
	})
	if err != nil {
		return err
	}
	if _, err := engine.store.Write(
		ctx,
		memory.Event,
		payload,
		"dreamweaver-quarantine",
	); err != nil {
		return fmt.Errorf("dreamweaver: record quarantine: %w", err)
	}
	return nil
}

func validateProposals(
	proposals []ProposedInsight,
	selected []selectedMemory,
) []ProposedInsight {
	byID := selectedByID(selected)
	seenStatements := make(map[string]struct{})
	valid := make([]ProposedInsight, 0, min(len(proposals), MaxBeliefsPerCycle))
	for _, proposal := range proposals {
		statement := strings.TrimSpace(proposal.Statement)
		key := strings.ToLower(statement)
		if statement == "" {
			continue
		}
		if _, exists := seenStatements[key]; exists {
			continue
		}
		sourceIDs := uniqueSortedIDs(proposal.SourceMemoryIDs)
		if len(sourceIDs) < MinSupportingMemories {
			continue
		}
		domains := make(map[string]struct{})
		known := true
		for _, id := range sourceIDs {
			source, exists := byID[id]
			if !exists {
				known = false
				break
			}
			domains[source.candidate.Domain] = struct{}{}
		}
		if !known || len(domains) < 2 {
			continue
		}
		seenStatements[key] = struct{}{}
		valid = append(valid, ProposedInsight{
			Statement:       statement,
			SourceMemoryIDs: sourceIDs,
		})
	}
	return valid
}

func decodeSourceDocument(data []byte) (sourceDocument, map[string]any) {
	raw := make(map[string]any)
	if json.Unmarshal(data, &raw) != nil {
		raw = map[string]any{"text": strings.TrimSpace(string(data))}
	}
	normalized, _ := json.Marshal(raw)
	var document sourceDocument
	_ = json.Unmarshal(normalized, &document)
	if strings.TrimSpace(document.Text) == "" {
		document.Text = strings.TrimSpace(string(data))
		raw["text"] = document.Text
	}
	document.Text = strings.TrimSpace(document.Text)
	document.Domain = strings.TrimSpace(document.Domain)
	if document.Domain == "" {
		document.Domain = "unknown"
		raw["domain"] = document.Domain
	}
	return document, raw
}

func selectedByID(selected []selectedMemory) map[uuid.UUID]selectedMemory {
	result := make(map[uuid.UUID]selectedMemory, len(selected))
	for _, source := range selected {
		result[source.candidate.ID] = source
	}
	return result
}

func selectedIDs(selected []selectedMemory) []uuid.UUID {
	result := make([]uuid.UUID, len(selected))
	for index := range selected {
		result[index] = selected[index].candidate.ID
	}
	return result
}

func adversarialSources(selected []selectedMemory) []uuid.UUID {
	var result []uuid.UUID
	for _, source := range selected {
		if source.document.Adversarial ||
			source.candidate.SourceTrust <= float64(cortex.TrustUntrusted) {
			result = append(result, source.candidate.ID)
		}
	}
	return uniqueSortedIDs(result)
}

func countDomains(selected []selectedMemory) int {
	domains := make(map[string]struct{})
	for _, source := range selected {
		domains[source.candidate.Domain] = struct{}{}
	}
	return len(domains)
}

func uniqueSortedIDs(values []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(values))
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].String() < result[right].String()
	})
	return result
}
