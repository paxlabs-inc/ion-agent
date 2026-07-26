// Package taskgraph implements the reified task DAG with evidence-delta
// convergence detection. The graph structure is goal -> subgoals -> premises
// -> evidence. Progress is measured by information gain, not model confidence.
//
// The DAG enforces:
// - Explicit node types (Goal, Subgoal, Premise, Evidence)
// - Explicit edge types (decomposes, requires, supports, refutes)
// - Cycle rejection via DFS back-edge detection
// - BLAKE3 canonical evidence identity
// - Action-to-subgoal links
package taskgraph

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"lukechampine.com/blake3"
)

const (
	// DefaultConvergenceWindow is the consecutive non-growth actions before
	// forced revision.
	DefaultConvergenceWindow = 4
)

// NodeType classifies DAG nodes.
type NodeType string

const (
	NodeGoal     NodeType = "goal"
	NodeSubgoal  NodeType = "subgoal"
	NodePremise  NodeType = "premise"
	NodeEvidence NodeType = "evidence"
)

// EdgeType classifies DAG edges.
type EdgeType string

const (
	EdgeDecomposes EdgeType = "decomposes" // goal -> subgoal
	EdgeRequires   EdgeType = "requires"   // subgoal -> premise
	EdgeSupports   EdgeType = "supports"   // premise -> evidence
	EdgeRefutes    EdgeType = "refutes"    // premise -> evidence
)

// NodeStatus is the lifecycle state of a graph node.
type NodeStatus string

const (
	StatusPending    NodeStatus = "pending"
	StatusInProgress NodeStatus = "in_progress"
	StatusCompleted  NodeStatus = "completed"
	StatusBlocked    NodeStatus = "blocked"
)

// Node is a typed vertex in the DAG.
type Node struct {
	ID        string     `json:"id"`
	Type      NodeType   `json:"type"`
	Text      string     `json:"text"`
	Status    NodeStatus `json:"status"`
	Actions   int        `json:"actions"`
	CreatedAt int64      `json:"created_at"`
}

// Edge is a typed directed edge in the DAG.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Type EdgeType `json:"type"`
}

// Evidence is a BLAKE3-identified evidence item.
type Evidence struct {
	Key      string   `json:"key"`       // BLAKE3 hex digest
	SourceID string   `json:"source_id"` // ToolEvent ID or other provenance
	Content  string   `json:"content"`
	Targets  []string `json:"targets"` // premise IDs this evidence relates to
}

// TaskGraph tracks the active goal, its nodes/edges, evidence set, and
// convergence state.
type TaskGraph struct {
	mu                 sync.RWMutex
	Goal               string      `json:"goal"`
	Nodes              []*Node     `json:"nodes"`
	Edges              []Edge      `json:"edges"`
	EvidenceItems      []*Evidence `json:"evidence"`
	evidenceSet        map[string]struct{}
	nodeIndex          map[string]*Node
	actionsSinceGrowth int
	convergenceWindow  int
	actionLog          []ActionRecord
}

// State is the complete durable task-DAG and convergence state.
type State struct {
	Goal               string         `json:"goal"`
	Nodes              []*Node        `json:"nodes"`
	Edges              []Edge         `json:"edges"`
	EvidenceItems      []*Evidence    `json:"evidence"`
	ActionsSinceGrowth int            `json:"actions_since_growth"`
	ConvergenceWindow  int            `json:"convergence_window"`
	ActionLog          []ActionRecord `json:"action_log"`
}

// ActionRecord tracks one action's contribution.
type ActionRecord struct {
	ActionID    string `json:"action_id"`
	TargetID    string `json:"target_id"`
	GrewSet     bool   `json:"grew_set"`
	EvidenceKey string `json:"evidence_key,omitempty"`
}

// New creates a task graph with the given goal and convergence window.
func New(goal string, convergenceWindow int) *TaskGraph {
	if convergenceWindow <= 0 {
		convergenceWindow = DefaultConvergenceWindow
	}
	graph := &TaskGraph{
		Goal:              goal,
		evidenceSet:       make(map[string]struct{}),
		nodeIndex:         make(map[string]*Node),
		convergenceWindow: convergenceWindow,
	}
	// Create the root goal node.
	goalNode := &Node{
		ID:     uuid.New().String(),
		Type:   NodeGoal,
		Text:   goal,
		Status: StatusInProgress,
	}
	graph.Nodes = append(graph.Nodes, goalNode)
	graph.nodeIndex[goalNode.ID] = goalNode
	return graph
}

// Restore validates and reconstructs a durable task graph.
func Restore(state State) (*TaskGraph, error) {
	if strings.TrimSpace(state.Goal) == "" || state.ConvergenceWindow <= 0 ||
		state.ActionsSinceGrowth < 0 {
		return nil, fmt.Errorf("taskgraph: invalid durable state")
	}
	graph := &TaskGraph{
		Goal:               state.Goal,
		evidenceSet:        make(map[string]struct{}),
		nodeIndex:          make(map[string]*Node),
		actionsSinceGrowth: state.ActionsSinceGrowth,
		convergenceWindow:  state.ConvergenceWindow,
		actionLog:          append([]ActionRecord(nil), state.ActionLog...),
	}
	goalCount := 0
	for _, item := range state.Nodes {
		if item == nil || strings.TrimSpace(item.ID) == "" ||
			strings.TrimSpace(item.Text) == "" || !item.Type.valid() ||
			!item.Status.valid() {
			return nil, fmt.Errorf("taskgraph: invalid durable node")
		}
		if _, exists := graph.nodeIndex[item.ID]; exists {
			return nil, fmt.Errorf("taskgraph: duplicate durable node %s", item.ID)
		}
		node := *item
		graph.Nodes = append(graph.Nodes, &node)
		graph.nodeIndex[node.ID] = &node
		if node.Type == NodeGoal {
			goalCount++
		}
	}
	if goalCount != 1 {
		return nil, fmt.Errorf("taskgraph: durable graph requires one goal")
	}
	for _, edge := range state.Edges {
		if _, ok := graph.nodeIndex[edge.From]; !ok {
			return nil, fmt.Errorf("taskgraph: durable edge source is missing")
		}
		if _, ok := graph.nodeIndex[edge.To]; !ok || !edge.Type.valid() {
			return nil, fmt.Errorf("taskgraph: invalid durable edge")
		}
		graph.Edges = append(graph.Edges, edge)
	}
	for _, item := range state.EvidenceItems {
		if item == nil || strings.TrimSpace(item.Key) == "" ||
			strings.TrimSpace(item.SourceID) == "" ||
			strings.TrimSpace(item.Content) == "" {
			return nil, fmt.Errorf("taskgraph: invalid durable evidence")
		}
		evidence := *item
		evidence.Targets = append([]string(nil), item.Targets...)
		graph.EvidenceItems = append(graph.EvidenceItems, &evidence)
		graph.evidenceSet[evidence.Key] = struct{}{}
	}
	for _, action := range graph.actionLog {
		if action.EvidenceKey != "" {
			graph.evidenceSet[action.EvidenceKey] = struct{}{}
		}
	}
	if graph.HasCycle() {
		return nil, fmt.Errorf("taskgraph: durable graph contains a cycle")
	}
	return graph, nil
}

func (nodeType NodeType) valid() bool {
	switch nodeType {
	case NodeGoal, NodeSubgoal, NodePremise, NodeEvidence:
		return true
	default:
		return false
	}
}

func (status NodeStatus) valid() bool {
	switch status {
	case StatusPending, StatusInProgress, StatusCompleted, StatusBlocked:
		return true
	default:
		return false
	}
}

func (edgeType EdgeType) valid() bool {
	switch edgeType {
	case EdgeDecomposes, EdgeRequires, EdgeSupports, EdgeRefutes:
		return true
	default:
		return false
	}
}

// Snapshot returns a defensive copy for encrypted persistence.
func (graph *TaskGraph) Snapshot() State {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	state := State{
		Goal:               graph.Goal,
		Edges:              append([]Edge(nil), graph.Edges...),
		ActionsSinceGrowth: graph.actionsSinceGrowth,
		ConvergenceWindow:  graph.convergenceWindow,
		ActionLog:          append([]ActionRecord(nil), graph.actionLog...),
	}
	for _, item := range graph.Nodes {
		node := *item
		state.Nodes = append(state.Nodes, &node)
	}
	for _, item := range graph.EvidenceItems {
		evidence := *item
		evidence.Targets = append([]string(nil), item.Targets...)
		state.EvidenceItems = append(state.EvidenceItems, &evidence)
	}
	return state
}

// AddSubgoal appends a new subgoal connected to the goal.
func (graph *TaskGraph) AddSubgoal(text string) *Subgoal {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	id := uuid.New().String()
	node := &Node{
		ID:     id,
		Type:   NodeSubgoal,
		Text:   text,
		Status: StatusPending,
	}
	graph.Nodes = append(graph.Nodes, node)
	graph.nodeIndex[id] = node

	// Find the goal node and add a decomposes edge.
	goalNode := graph.findNodeByType(NodeGoal)
	if goalNode != nil {
		graph.Edges = append(graph.Edges, Edge{
			From: goalNode.ID,
			To:   id,
			Type: EdgeDecomposes,
		})
	}

	return &Subgoal{ID: id, Text: text, Status: StatusPending}
}

// AddPremise adds a premise node connected to a subgoal.
func (graph *TaskGraph) AddPremise(subgoalID string, statement string) (*Node, error) {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if _, ok := graph.nodeIndex[subgoalID]; !ok {
		return nil, fmt.Errorf("taskgraph: subgoal not found: %s", subgoalID)
	}
	parent, ok := graph.nodeIndex[subgoalID]
	if !ok || parent.Type != NodeSubgoal {
		return nil, fmt.Errorf("taskgraph: subgoal not found: %s", subgoalID)
	}
	id := uuid.New().String()
	node := &Node{
		ID:     id,
		Type:   NodePremise,
		Text:   statement,
		Status: StatusPending,
	}
	graph.Nodes = append(graph.Nodes, node)
	graph.nodeIndex[id] = node
	if err := graph.addEdgeLocked(Edge{
		From: subgoalID,
		To:   id,
		Type: EdgeRequires,
	}); err != nil {
		delete(graph.nodeIndex, id)
		graph.Nodes = graph.Nodes[:len(graph.Nodes)-1]
		return nil, err
	}
	return node, nil
}

// AddEvidence adds evidence with BLAKE3 canonical identity and links it to premises.
func (graph *TaskGraph) AddEvidence(sourceID string, content string, premiseIDs []string, edgeType EdgeType) (*Evidence, error) {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if strings.TrimSpace(sourceID) == "" || strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("taskgraph: evidence source and content are required")
	}
	if edgeType != EdgeSupports && edgeType != EdgeRefutes {
		return nil, fmt.Errorf("taskgraph: invalid evidence edge type %q", edgeType)
	}
	if len(premiseIDs) == 0 {
		return nil, fmt.Errorf("taskgraph: evidence must target at least one premise")
	}
	for _, premiseID := range premiseIDs {
		node, ok := graph.nodeIndex[premiseID]
		if !ok || node.Type != NodePremise {
			return nil, fmt.Errorf("taskgraph: premise not found: %s", premiseID)
		}
	}

	// Compute BLAKE3 canonical evidence identity.
	key := ComputeEvidenceKey(sourceID, content)

	// Check for duplicate.
	if _, exists := graph.evidenceSet[key]; exists {
		return graph.findEvidenceByKey(key), nil
	}

	evidence := &Evidence{
		Key:      key,
		SourceID: sourceID,
		Content:  content,
		Targets:  premiseIDs,
	}
	graph.EvidenceItems = append(graph.EvidenceItems, evidence)
	graph.evidenceSet[key] = struct{}{}

	// Create evidence node.
	node := &Node{
		ID:     key,
		Type:   NodeEvidence,
		Text:   content,
		Status: StatusCompleted,
	}
	graph.Nodes = append(graph.Nodes, node)
	graph.nodeIndex[node.ID] = node

	// Link evidence to premises.
	for _, pid := range premiseIDs {
		if err := graph.addEdgeLocked(Edge{
			From: pid,
			To:   node.ID,
			Type: edgeType,
		}); err != nil {
			return nil, err
		}
	}

	return evidence, nil
}

// ComputeEvidenceKey computes a BLAKE3-based canonical evidence identity.
func ComputeEvidenceKey(sourceID string, content string) string {
	h := blake3.New(32, nil)
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(sourceID)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(sourceID))
	binary.BigEndian.PutUint64(length[:], uint64(len(content)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(content))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Subgoal is one work unit in the DAG.
type Subgoal struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Status   NodeStatus `json:"status"`
	Actions  int        `json:"actions"`
	Evidence []string   `json:"evidence,omitempty"`
}

// Current returns the in-progress subgoal, or the first pending one.
func (graph *TaskGraph) Current() *Subgoal {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	var inProgress, firstPending *Node
	for _, node := range graph.Nodes {
		if node.Type != NodeSubgoal {
			continue
		}
		if node.Status == StatusInProgress {
			inProgress = node
			break
		}
		if node.Status == StatusPending && firstPending == nil {
			firstPending = node
		}
	}
	target := inProgress
	if target == nil {
		target = firstPending
	}
	if target == nil {
		return nil
	}
	return &Subgoal{ID: target.ID, Text: target.Text, Status: target.Status}
}

// ObserveAction records a tool action's evidence contribution. Returns true
// if the evidence set grew (new information).
func (graph *TaskGraph) ObserveAction(evidenceKey string, currentSubgoalID string) bool {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	grew := false
	if evidenceKey != "" {
		if _, exists := graph.evidenceSet[evidenceKey]; !exists {
			graph.evidenceSet[evidenceKey] = struct{}{}
			grew = true
		}
	}
	if node, ok := graph.nodeIndex[currentSubgoalID]; ok {
		node.Actions++
	}
	graph.actionLog = append(graph.actionLog, ActionRecord{
		TargetID:    currentSubgoalID,
		GrewSet:     grew,
		EvidenceKey: evidenceKey,
	})
	if grew {
		graph.actionsSinceGrowth = 0
	} else {
		graph.actionsSinceGrowth++
	}
	return grew
}

// ShouldForceRevision returns true if the convergence window has been exceeded.
func (graph *TaskGraph) ShouldForceRevision() bool {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	return graph.actionsSinceGrowth >= graph.convergenceWindow
}

// ActionsSinceGrowth returns the consecutive non-growth action count.
func (graph *TaskGraph) ActionsSinceGrowth() int {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	return graph.actionsSinceGrowth
}

// EvidenceCount returns the total unique evidence count.
func (graph *TaskGraph) EvidenceCount() int {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	return len(graph.evidenceSet)
}

// CompleteSubgoal marks a subgoal as completed.
func (graph *TaskGraph) CompleteSubgoal(id string) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if node, ok := graph.nodeIndex[id]; ok {
		if node.Type != NodeSubgoal {
			return fmt.Errorf("taskgraph: node %s is not a subgoal", id)
		}
		node.Status = StatusCompleted
		return nil
	}
	return fmt.Errorf("taskgraph: subgoal not found: %s", id)
}

// ReplanSubtrees clears premise/evidence descendants and action state for only
// the listed subgoals. Unaffected subgoals and their evidence remain intact.
func (graph *TaskGraph) ReplanSubtrees(subgoalIDs []string) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()

	targets := make(map[string]struct{})
	for _, id := range subgoalIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		node, ok := graph.nodeIndex[id]
		if !ok || node.Type != NodeSubgoal {
			return fmt.Errorf("taskgraph: subgoal not found: %s", id)
		}
		targets[id] = struct{}{}
	}
	if len(targets) == 0 {
		return fmt.Errorf("taskgraph: at least one subgoal is required for replanning")
	}

	removed := make(map[string]struct{})
	for _, edge := range graph.Edges {
		if edge.Type != EdgeRequires {
			continue
		}
		if _, targeted := targets[edge.From]; targeted {
			removed[edge.To] = struct{}{}
		}
	}

	// Evidence nodes that no longer support any retained premise belong only to
	// the affected subtree and can be discarded.
	for _, edge := range graph.Edges {
		if edge.Type != EdgeSupports && edge.Type != EdgeRefutes {
			continue
		}
		if _, premiseRemoved := removed[edge.From]; !premiseRemoved {
			continue
		}
		retainedParent := false
		for _, candidate := range graph.Edges {
			if candidate.To != edge.To ||
				(candidate.Type != EdgeSupports && candidate.Type != EdgeRefutes) {
				continue
			}
			if _, candidateRemoved := removed[candidate.From]; !candidateRemoved {
				retainedParent = true
				break
			}
		}
		if !retainedParent {
			removed[edge.To] = struct{}{}
		}
	}

	nodes := graph.Nodes[:0]
	for _, node := range graph.Nodes {
		if _, drop := removed[node.ID]; drop {
			delete(graph.nodeIndex, node.ID)
			continue
		}
		if _, targeted := targets[node.ID]; targeted {
			node.Status = StatusPending
			node.Actions = 0
		}
		nodes = append(nodes, node)
	}
	graph.Nodes = nodes

	edges := graph.Edges[:0]
	for _, edge := range graph.Edges {
		_, fromRemoved := removed[edge.From]
		_, toRemoved := removed[edge.To]
		if !fromRemoved && !toRemoved {
			edges = append(edges, edge)
		}
	}
	graph.Edges = edges

	evidence := graph.EvidenceItems[:0]
	graph.evidenceSet = make(map[string]struct{})
	for _, item := range graph.EvidenceItems {
		if _, drop := removed[item.Key]; drop {
			continue
		}
		targetIDs := item.Targets[:0]
		for _, target := range item.Targets {
			if _, drop := removed[target]; !drop {
				targetIDs = append(targetIDs, target)
			}
		}
		item.Targets = targetIDs
		evidence = append(evidence, item)
		graph.evidenceSet[item.Key] = struct{}{}
	}
	graph.EvidenceItems = evidence

	actionLog := graph.actionLog[:0]
	for _, action := range graph.actionLog {
		if _, targeted := targets[action.TargetID]; !targeted {
			actionLog = append(actionLog, action)
			if action.EvidenceKey != "" {
				graph.evidenceSet[action.EvidenceKey] = struct{}{}
			}
		}
	}
	graph.actionLog = actionLog
	graph.actionsSinceGrowth = 0
	return nil
}

// HasCycle detects cycles via DFS back-edge detection.
func (graph *TaskGraph) HasCycle() bool {
	graph.mu.RLock()
	defer graph.mu.RUnlock()

	adjacency := make(map[string][]string)
	for _, edge := range graph.Edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)
	colors := make(map[string]int)

	var dfs func(string) bool
	dfs = func(u string) bool {
		colors[u] = gray
		for _, v := range adjacency[u] {
			if colors[v] == gray {
				return true // back edge = cycle
			}
			if colors[v] == white && dfs(v) {
				return true
			}
		}
		colors[u] = black
		return false
	}

	for _, node := range graph.Nodes {
		if colors[node.ID] == white {
			if dfs(node.ID) {
				return true
			}
		}
	}
	return false
}

// AddEdge validates a typed relationship and rejects cycles atomically.
func (graph *TaskGraph) AddEdge(edge Edge) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	return graph.addEdgeLocked(edge)
}

func (graph *TaskGraph) addEdgeLocked(edge Edge) error {
	from, fromOK := graph.nodeIndex[edge.From]
	to, toOK := graph.nodeIndex[edge.To]
	if !fromOK || !toOK {
		return fmt.Errorf("taskgraph: edge endpoint not found")
	}
	valid := (edge.Type == EdgeDecomposes && from.Type == NodeGoal && to.Type == NodeSubgoal) ||
		(edge.Type == EdgeRequires && from.Type == NodeSubgoal && to.Type == NodePremise) ||
		((edge.Type == EdgeSupports || edge.Type == EdgeRefutes) &&
			from.Type == NodePremise && to.Type == NodeEvidence)
	if !valid {
		return fmt.Errorf(
			"taskgraph: invalid %s edge from %s to %s",
			edge.Type, from.Type, to.Type,
		)
	}
	if graph.reachableLocked(edge.To, edge.From) {
		return fmt.Errorf("taskgraph: edge would create a cycle")
	}
	for _, existing := range graph.Edges {
		if existing == edge {
			return nil
		}
	}
	graph.Edges = append(graph.Edges, edge)
	return nil
}

func (graph *TaskGraph) reachableLocked(start, target string) bool {
	seen := make(map[string]bool)
	var visit func(string) bool
	visit = func(node string) bool {
		if node == target {
			return true
		}
		if seen[node] {
			return false
		}
		seen[node] = true
		for _, edge := range graph.Edges {
			if edge.From == node && visit(edge.To) {
				return true
			}
		}
		return false
	}
	return visit(start)
}

// TodoProjection returns a checklist view of the graph.
func (graph *TaskGraph) TodoProjection() []TodoItem {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	var items []TodoItem
	for _, node := range graph.Nodes {
		if node.Type == NodeSubgoal {
			evidence := 0
			for _, edge := range graph.Edges {
				if edge.From != node.ID || edge.Type != EdgeRequires {
					continue
				}
				for _, support := range graph.Edges {
					if support.From == edge.To &&
						(support.Type == EdgeSupports || support.Type == EdgeRefutes) {
						evidence++
					}
				}
			}
			items = append(items, TodoItem{
				ID:       node.ID,
				Text:     node.Text,
				Status:   node.Status,
				Evidence: evidence,
				Actions:  node.Actions,
			})
		}
	}
	return items
}

// TodoItem is a projected checklist item from the graph.
type TodoItem struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Status   NodeStatus `json:"status"`
	Evidence int        `json:"evidence"`
	Actions  int        `json:"actions"`
}

// Render produces a deterministic text rendering of the graph state.
func (graph *TaskGraph) Render() string {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	if len(graph.Nodes) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Goal: %s\n", graph.Goal))
	for _, node := range graph.Nodes {
		if node.Type != NodeSubgoal {
			continue
		}
		marker := "[ ]"
		switch node.Status {
		case StatusInProgress:
			marker = "[>]"
		case StatusCompleted:
			marker = "[x]"
		case StatusBlocked:
			marker = "[!]"
		}
		builder.WriteString(fmt.Sprintf("  %s %s (%d actions)\n",
			marker, node.Text, node.Actions))
	}
	builder.WriteString(fmt.Sprintf("Evidence: %d unique | Since growth: %d/%d\n",
		len(graph.evidenceSet), graph.actionsSinceGrowth, graph.convergenceWindow))
	return builder.String()
}

// Reset clears all nodes, edges, and evidence for a new plan.
func (graph *TaskGraph) Reset() {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	graph.Nodes = nil
	graph.Edges = nil
	graph.EvidenceItems = nil
	graph.evidenceSet = make(map[string]struct{})
	graph.nodeIndex = make(map[string]*Node)
	graph.actionsSinceGrowth = 0
	graph.actionLog = nil
	goalNode := &Node{
		ID: uuid.New().String(), Type: NodeGoal,
		Text: graph.Goal, Status: StatusInProgress,
	}
	graph.Nodes = append(graph.Nodes, goalNode)
	graph.nodeIndex[goalNode.ID] = goalNode
}

// IsEmpty returns true if the graph has no subgoals.
func (graph *TaskGraph) IsEmpty() bool {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	for _, node := range graph.Nodes {
		if node.Type == NodeSubgoal {
			return false
		}
	}
	return true
}

// findNodeByType returns the first node of the given type (caller holds lock).
func (graph *TaskGraph) findNodeByType(t NodeType) *Node {
	for _, node := range graph.Nodes {
		if node.Type == t {
			return node
		}
	}
	return nil
}

// findEvidenceByKey returns evidence by BLAKE3 key (caller holds lock).
func (graph *TaskGraph) findEvidenceByKey(key string) *Evidence {
	for _, e := range graph.EvidenceItems {
		if e.Key == key {
			return e
		}
	}
	return nil
}
