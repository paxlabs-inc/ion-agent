// Package curiosity detects structural anomalies in Cortex and promotes them
// into restricted Automatrix work.
package curiosity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/presence/automatrix"
)

const (
	FringePercentile       = 0.15
	ContradictionThreshold = 0.3
	GapOccurrenceThreshold = 3
	defaultRecencyHalfLife = 30 * 24 * time.Hour
)

// TargetType is the closed curiosity anomaly taxonomy.
type TargetType string

const (
	TargetFringe        TargetType = "fringe"
	TargetContradiction TargetType = "contradiction"
	TargetGap           TargetType = "gap"
)

// Status tracks curiosity target lifecycle.
type Status string

const (
	StatusPending   Status = "pending"
	StatusExploring Status = "exploring"
	StatusResolved  Status = "resolved"
	StatusArchived  Status = "archived"
)

// PriorityFactors makes the binding priority equation auditable.
type PriorityFactors struct {
	Recency         float64 `json:"recency"`
	Frequency       float64 `json:"frequency"`
	GraphCentrality float64 `json:"graph_centrality"`
}

// Target is one graph-derived question promoted for idle exploration.
type Target struct {
	ID              uuid.UUID       `json:"id"`
	Type            TargetType      `json:"type"`
	Description     string          `json:"description"`
	SourceMemoryIDs []uuid.UUID     `json:"source_memory_ids"`
	Priority        float64         `json:"priority"`
	Factors         PriorityFactors `json:"factors"`
	Status          Status          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
	ResolvedAt      *time.Time      `json:"resolved_at,omitempty"`
}

// GraphDocument is the structured Cortex payload understood by the knowledge
// graph adapter. Plaintext memories remain usable: their full content becomes
// Text and they simply have no explicit edges.
type GraphDocument struct {
	Text        string      `json:"text"`
	Connections []uuid.UUID `json:"connections,omitempty"`
	AccessCount int         `json:"access_count,omitempty"`
}

type gapDocument struct {
	Kind        string      `json:"kind"`
	Question    string      `json:"question"`
	Context     string      `json:"context,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	Answerable  bool        `json:"answerable"`
	Connections []uuid.UUID `json:"connections,omitempty"`
}

// MemoryStore is the production Cortex surface required by the engine.
type MemoryStore interface {
	ListByType(memory.Type) []uuid.UUID
	Resolve(uuid.UUID) (*cortex.Memory, error)
	Write(context.Context, memory.Type, []byte, string) (*cortex.Memory, error)
}

// TargetQueue is the Automatrix handoff boundary.
type TargetQueue interface {
	Enqueue(context.Context, automatrix.WorkItem) error
}

// Clock permits deterministic recency scoring.
type Clock interface {
	Now() time.Time
}

// EntailmentScorer computes whether two Fact/Belief statements can both be
// true. Scores below 0.3 are contradictions.
type EntailmentScorer interface {
	Score(context.Context, string, string) (float64, error)
}

// Config supplies all real boundaries used by a scan.
type Config struct {
	Store           MemoryStore
	Queue           TargetQueue
	Scorer          EntailmentScorer
	Clock           Clock
	RecencyHalfLife time.Duration
}

// Engine scans the journal-derived Cortex view and writes no protected memory.
type Engine struct {
	store           MemoryStore
	queue           TargetQueue
	scorer          EntailmentScorer
	clock           Clock
	recencyHalfLife time.Duration
}

// New constructs a fail-closed curiosity engine.
func New(config Config) (*Engine, error) {
	if config.Store == nil || config.Queue == nil || config.Scorer == nil || config.Clock == nil {
		return nil, fmt.Errorf("curiosity: store, queue, scorer, and clock are required")
	}
	halfLife := config.RecencyHalfLife
	if halfLife <= 0 {
		halfLife = defaultRecencyHalfLife
	}
	return &Engine{
		store:           config.Store,
		queue:           config.Queue,
		scorer:          config.Scorer,
		clock:           config.Clock,
		recencyHalfLife: halfLife,
	}, nil
}

// RecordGap durably records an observed unanswerable question as an Event.
// Gap promotion occurs only after three separately committed occurrences.
func (engine *Engine) RecordGap(
	ctx context.Context,
	question string,
	contextText string,
	sessionID string,
	connections []uuid.UUID,
) (*cortex.Memory, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("curiosity: gap question is required")
	}
	payload, err := json.Marshal(gapDocument{
		Kind:        "curiosity_gap",
		Question:    question,
		Context:     strings.TrimSpace(contextText),
		SessionID:   strings.TrimSpace(sessionID),
		Answerable:  false,
		Connections: append([]uuid.UUID(nil), connections...),
	})
	if err != nil {
		return nil, fmt.Errorf("curiosity: encode gap: %w", err)
	}
	return engine.store.Write(ctx, memory.Event, payload, "curiosity-gap-recorder")
}

// Scan detects all three anomaly classes and upserts them into Automatrix.
func (engine *Engine) Scan(ctx context.Context) ([]Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := engine.clock.Now().UTC()
	nodes, adjacency, err := engine.loadGraph(ctx)
	if err != nil {
		return nil, err
	}
	targets := engine.detectFringe(nodes, adjacency, now)
	contradictions, err := engine.detectContradictions(ctx, nodes, adjacency, now)
	if err != nil {
		return nil, err
	}
	targets = append(targets, contradictions...)
	gaps, err := engine.detectGaps(ctx, nodes, adjacency, now)
	if err != nil {
		return nil, err
	}
	targets = append(targets, gaps...)
	sortTargets(targets)
	for _, target := range targets {
		encoded, err := json.Marshal(target)
		if err != nil {
			return nil, fmt.Errorf("curiosity: encode target: %w", err)
		}
		if err := engine.queue.Enqueue(ctx, automatrix.WorkItem{
			ID:          target.ID,
			Source:      automatrix.SourceCuriosity,
			Kind:        string(target.Type),
			Description: target.Description,
			Priority:    target.Priority,
			Payload:     encoded,
			CreatedAt:   target.CreatedAt,
		}); err != nil {
			return nil, fmt.Errorf("curiosity: enqueue target: %w", err)
		}
	}
	return cloneTargets(targets), nil
}

type graphNode struct {
	id          uuid.UUID
	memoryType  memory.Type
	text        string
	connections []uuid.UUID
	accessCount int
	updatedAt   time.Time
}

func (engine *Engine) loadGraph(
	ctx context.Context,
) (map[uuid.UUID]graphNode, map[uuid.UUID]map[uuid.UUID]struct{}, error) {
	nodes := make(map[uuid.UUID]graphNode)
	for _, memoryType := range []memory.Type{memory.Fact, memory.Belief} {
		for _, id := range engine.store.ListByType(memoryType) {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			resolved, err := engine.store.Resolve(id)
			if err != nil {
				return nil, nil, fmt.Errorf("curiosity: resolve graph memory %s: %w", id, err)
			}
			document := decodeGraphDocument(resolved.Version.Data)
			nodes[id] = graphNode{
				id:          id,
				memoryType:  memoryType,
				text:        document.Text,
				connections: append([]uuid.UUID(nil), document.Connections...),
				accessCount: max(document.AccessCount, 1),
				updatedAt:   resolved.Head.LastUpdatedAt.UTC(),
			}
		}
	}
	adjacency := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(nodes))
	for id := range nodes {
		adjacency[id] = make(map[uuid.UUID]struct{})
	}
	for id, node := range nodes {
		for _, connected := range node.connections {
			if connected == id {
				continue
			}
			if _, exists := nodes[connected]; !exists {
				continue
			}
			adjacency[id][connected] = struct{}{}
			adjacency[connected][id] = struct{}{}
		}
	}
	return nodes, adjacency, nil
}

func (engine *Engine) detectFringe(
	nodes map[uuid.UUID]graphNode,
	adjacency map[uuid.UUID]map[uuid.UUID]struct{},
	now time.Time,
) []Target {
	if len(nodes) < 2 {
		return nil
	}
	densities := make([]float64, 0, len(nodes))
	byID := make(map[uuid.UUID]float64, len(nodes))
	denominator := float64(len(nodes) - 1)
	for id := range nodes {
		density := float64(len(adjacency[id])) / denominator
		densities = append(densities, density)
		byID[id] = density
	}
	threshold := percentile(densities, FringePercentile)
	var targets []Target
	for id, density := range byID {
		if density >= threshold {
			continue
		}
		node := nodes[id]
		factors := engine.factors(
			now,
			node.updatedAt,
			float64(node.accessCount),
			smoothedCentrality(len(adjacency[id]), len(nodes)),
		)
		targets = append(targets, newTarget(
			TargetFringe,
			"Explore sparsely connected memory: "+node.text,
			[]uuid.UUID{id},
			factors,
			now,
		))
	}
	return targets
}

func (engine *Engine) detectContradictions(
	ctx context.Context,
	nodes map[uuid.UUID]graphNode,
	adjacency map[uuid.UUID]map[uuid.UUID]struct{},
	now time.Time,
) ([]Target, error) {
	ordered := make([]graphNode, 0, len(nodes))
	for _, node := range nodes {
		ordered = append(ordered, node)
	}
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].id.String() < ordered[right].id.String()
	})
	var targets []Target
	for left := 0; left < len(ordered); left++ {
		for right := left + 1; right < len(ordered); right++ {
			score, err := engine.scorer.Score(ctx, ordered[left].text, ordered[right].text)
			if err != nil {
				return nil, fmt.Errorf(
					"curiosity: entailment %s/%s: %w",
					ordered[left].id,
					ordered[right].id,
					err,
				)
			}
			if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
				return nil, fmt.Errorf("curiosity: entailment scorer returned invalid score %v", score)
			}
			if score >= ContradictionThreshold {
				continue
			}
			latest := ordered[left].updatedAt
			if ordered[right].updatedAt.After(latest) {
				latest = ordered[right].updatedAt
			}
			centrality := (smoothedCentrality(
				len(adjacency[ordered[left].id]),
				len(nodes),
			) + smoothedCentrality(
				len(adjacency[ordered[right].id]),
				len(nodes),
			)) / 2
			factors := engine.factors(
				now,
				latest,
				float64(ordered[left].accessCount+ordered[right].accessCount),
				centrality,
			)
			targets = append(targets, newTarget(
				TargetContradiction,
				fmt.Sprintf(
					"Resolve incompatible memories (entailment %.3f): %s | %s",
					score,
					ordered[left].text,
					ordered[right].text,
				),
				[]uuid.UUID{ordered[left].id, ordered[right].id},
				factors,
				now,
			))
		}
	}
	return targets, nil
}

type gapGroup struct {
	question    string
	ids         []uuid.UUID
	connections map[uuid.UUID]struct{}
	latest      time.Time
}

func (engine *Engine) detectGaps(
	ctx context.Context,
	nodes map[uuid.UUID]graphNode,
	adjacency map[uuid.UUID]map[uuid.UUID]struct{},
	now time.Time,
) ([]Target, error) {
	groups := make(map[string]*gapGroup)
	for _, id := range engine.store.ListByType(memory.Event) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolved, err := engine.store.Resolve(id)
		if err != nil {
			return nil, fmt.Errorf("curiosity: resolve gap memory %s: %w", id, err)
		}
		var document gapDocument
		if json.Unmarshal(resolved.Version.Data, &document) != nil ||
			document.Kind != "curiosity_gap" ||
			document.Answerable ||
			strings.TrimSpace(document.Question) == "" {
			continue
		}
		key := normalizeQuestion(document.Question)
		group := groups[key]
		if group == nil {
			group = &gapGroup{
				question:    strings.TrimSpace(document.Question),
				connections: make(map[uuid.UUID]struct{}),
			}
			groups[key] = group
		}
		group.ids = append(group.ids, id)
		if resolved.Head.LastUpdatedAt.After(group.latest) {
			group.latest = resolved.Head.LastUpdatedAt
		}
		for _, connected := range document.Connections {
			if _, exists := nodes[connected]; exists {
				group.connections[connected] = struct{}{}
			}
		}
	}
	var targets []Target
	for _, group := range groups {
		if len(group.ids) < GapOccurrenceThreshold {
			continue
		}
		centrality := smoothedCentrality(0, max(len(nodes), 1))
		if len(group.connections) > 0 {
			centrality = 0
			for id := range group.connections {
				centrality += smoothedCentrality(len(adjacency[id]), len(nodes))
			}
			centrality /= float64(len(group.connections))
		}
		factors := engine.factors(
			now,
			group.latest,
			float64(len(group.ids)),
			centrality,
		)
		targets = append(targets, newTarget(
			TargetGap,
			fmt.Sprintf(
				"Investigate recurring unanswerable question (%d occurrences): %s",
				len(group.ids),
				group.question,
			),
			group.ids,
			factors,
			now,
		))
	}
	return targets, nil
}

func (engine *Engine) factors(
	now time.Time,
	updatedAt time.Time,
	frequency float64,
	centrality float64,
) PriorityFactors {
	age := now.Sub(updatedAt)
	if age < 0 {
		age = 0
	}
	recency := math.Pow(0.5, float64(age)/float64(engine.recencyHalfLife))
	return PriorityFactors{
		Recency:         recency,
		Frequency:       frequency,
		GraphCentrality: centrality,
	}
}

func newTarget(
	targetType TargetType,
	description string,
	sourceIDs []uuid.UUID,
	factors PriorityFactors,
	now time.Time,
) Target {
	sourceIDs = append([]uuid.UUID(nil), sourceIDs...)
	sort.Slice(sourceIDs, func(left, right int) bool {
		return sourceIDs[left].String() < sourceIDs[right].String()
	})
	identity := string(targetType)
	for _, id := range sourceIDs {
		identity += "\x00" + id.String()
	}
	return Target{
		ID:              uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)),
		Type:            targetType,
		Description:     description,
		SourceMemoryIDs: sourceIDs,
		Priority:        factors.Recency * factors.Frequency * factors.GraphCentrality,
		Factors:         factors,
		Status:          StatusPending,
		CreatedAt:       now,
	}
}

func decodeGraphDocument(data []byte) GraphDocument {
	var document GraphDocument
	if json.Unmarshal(data, &document) != nil {
		document.Text = strings.TrimSpace(string(data))
	}
	if strings.TrimSpace(document.Text) == "" {
		document.Text = strings.TrimSpace(string(data))
	}
	document.Text = strings.TrimSpace(document.Text)
	if document.AccessCount < 1 {
		document.AccessCount = 1
	}
	return document
}

func normalizeQuestion(question string) string {
	var builder strings.Builder
	space := false
	for _, value := range strings.ToLower(strings.TrimSpace(question)) {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			builder.WriteRune(value)
			space = false
			continue
		}
		if !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	if len(ordered) == 1 {
		return ordered[0]
	}
	position := quantile * float64(len(ordered)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return ordered[lower]
	}
	weight := position - float64(lower)
	return ordered[lower]*(1-weight) + ordered[upper]*weight
}

func smoothedCentrality(degree int, nodes int) float64 {
	if nodes <= 0 {
		return 0
	}
	return float64(degree+1) / float64(nodes)
}

func sortTargets(targets []Target) {
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].Priority != targets[right].Priority {
			return targets[left].Priority > targets[right].Priority
		}
		if targets[left].Type != targets[right].Type {
			return targets[left].Type < targets[right].Type
		}
		return targets[left].ID.String() < targets[right].ID.String()
	})
}

func cloneTargets(targets []Target) []Target {
	result := make([]Target, len(targets))
	copy(result, targets)
	for index := range result {
		result[index].SourceMemoryIDs = append(
			[]uuid.UUID(nil),
			result[index].SourceMemoryIDs...,
		)
	}
	return result
}
