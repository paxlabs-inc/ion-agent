// Package swarm implements multi-agent multiplicity with bounded goroutine
// execution, isolated contexts, reduced self-models, and HMAC-authenticated
// coordination verbs.
package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/security/coordination"
)

var (
	ErrAgentNotFound        = errors.New("swarm: agent not found")
	ErrAgentSessionMismatch = errors.New("swarm: agent session mismatch")
	ErrAgentStateConflict   = errors.New("swarm: agent state conflict")
)

const (
	// MaxDepth is the maximum spawn depth (parent -> child -> grandchild).
	MaxDepth = 2

	// GlobalLaneMax is the maximum concurrent agents across all sessions.
	GlobalLaneMax = 32

	// SessionLaneMax is the maximum concurrent agents per session.
	SessionLaneMax = 20

	// ParentLaneMax is the maximum concurrent agents per parent.
	ParentLaneMax = 20

	// OrphanThreshold is the time without a valid verb before an agent is orphaned.
	OrphanThreshold = 5 * time.Minute

	// OrphanScanInterval is how often the orphan scanner runs.
	OrphanScanInterval = 60 * time.Second

	// OrphanAbortTimeout is the time to wait after ABORT before cancel/kill.
	OrphanAbortTimeout = 30 * time.Second
)

// ReducedSelfModel is a structurally safe type for sub-agent inheritance.
// It contains only permitted identity/structural/capability/failure summaries.
// Sensitive material (vault keys, cross-session handles, consequential authority,
// unrestricted spawn) is excluded by compilation.
type ReducedSelfModel struct {
	ID           string   `json:"id"`
	Capabilities []string `json:"capabilities"`
	Limitations  []string `json:"limitations"`
	Version      int      `json:"version"`
}

// AgentState tracks the lifecycle of a sub-agent.
type AgentState string

const (
	StateRunning   AgentState = "running"
	StateYielded   AgentState = "yielded"
	StateCompleted AgentState = "completed"
	StateAborted   AgentState = "aborted"
	StateOrphaned  AgentState = "orphaned"
)

// Agent represents a spawned sub-agent.
type Agent struct {
	mu            sync.Mutex
	ID            string             `json:"id"`
	ParentID      string             `json:"parent_id,omitempty"`
	Depth         int                `json:"depth"`
	State         AgentState         `json:"state"`
	Model         ReducedSelfModel   `json:"model"`
	CreatedAt     time.Time          `json:"created_at"`
	LastVerbAt    time.Time          `json:"last_verb_at"`
	LastVerb      string             `json:"last_verb,omitempty"`
	CancelFunc    context.CancelFunc `json:"-"`
	ArtifactCount int                `json:"artifact_count"`
	Assignment    string             `json:"assignment,omitempty"`
	sessionID     string
	tools         ToolSurface
	artifact      json.RawMessage
	workerError   string
	done          chan struct{}
}

// AgentSnapshot is the immutable operator projection of one scoped child.
type AgentSnapshot struct {
	ID            string     `json:"id"`
	ParentID      string     `json:"parent_id,omitempty"`
	Depth         int        `json:"depth"`
	State         AgentState `json:"state"`
	Assignment    string     `json:"assignment,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	LastVerbAt    time.Time  `json:"last_verb_at"`
	LastVerb      string     `json:"last_verb,omitempty"`
	ArtifactCount int        `json:"artifact_count"`
	HasError      bool       `json:"has_error"`
}

// WorkerContext is the immutable authority-reduced view handed to a child.
type WorkerContext struct {
	AgentID string
	Model   ReducedSelfModel
	Tools   []string
}

// Worker runs as one child goroutine and must honor cancellation.
type Worker func(context.Context, WorkerContext) (json.RawMessage, error)

// Completion is pushed when a child worker exits. Artifacts are preserved even
// when the child was aborted or orphaned.
type Completion struct {
	AgentID   string          `json:"agent_id"`
	SessionID string          `json:"session_id"`
	State     AgentState      `json:"state"`
	Artifact  json.RawMessage `json:"artifact,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Registry tracks all spawned agents globally and per-session.
type Registry struct {
	mu           sync.Mutex
	agents       map[string]*Agent
	globalCount  int
	sessionCount map[string]int
	parentCount  map[string]int
	channels     map[string]*coordination.SpawnSession
	clock        Clock
	completions  chan Completion
	scanInterval time.Duration
	abortTimeout time.Duration
}

// Clock abstracts time for testing.
type Clock interface {
	Now() time.Time
}

// NewRegistry creates a new agent registry.
func NewRegistry(clock Clock) *Registry {
	return &Registry{
		agents:       make(map[string]*Agent),
		sessionCount: make(map[string]int),
		parentCount:  make(map[string]int),
		channels:     make(map[string]*coordination.SpawnSession),
		clock:        clock,
		completions:  make(chan Completion, 1024),
		scanInterval: OrphanScanInterval,
		abortTimeout: OrphanAbortTimeout,
	}
}

// Spawn registers a new sub-agent if lane limits allow it.
func (r *Registry) Spawn(parentID string, sessionID string, depth int) (*Agent, error) {
	return r.SpawnReduced(
		parentID,
		sessionID,
		depth,
		ReducedSelfModel{},
		ToolSurface{},
	)
}

// SpawnReduced binds the child's reduced identity and immutable tool surface
// at spawn time.
func (r *Registry) SpawnReduced(
	parentID string,
	sessionID string,
	depth int,
	model ReducedSelfModel,
	surface ToolSurface,
) (*Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check depth limit.
	if depth > MaxDepth {
		return nil, fmt.Errorf("swarm: depth %d exceeds max %d", depth, MaxDepth)
	}

	// Check global lane.
	if r.globalCount >= GlobalLaneMax {
		return nil, fmt.Errorf("swarm: global lane full (%d/%d)", r.globalCount, GlobalLaneMax)
	}

	// Check session lane.
	if r.sessionCount[sessionID] >= SessionLaneMax {
		return nil, fmt.Errorf("swarm: session lane full (%d/%d)", r.sessionCount[sessionID], SessionLaneMax)
	}

	// Check parent lane.
	if parentID != "" && r.parentCount[parentID] >= ParentLaneMax {
		return nil, fmt.Errorf("swarm: parent lane full (%d/%d)", r.parentCount[parentID], ParentLaneMax)
	}
	channel, err := coordination.NewSpawnSession(r.clock, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("swarm: create authenticated spawn channel: %w", err)
	}

	now := r.clock.Now()
	agent := &Agent{
		ID:         uuid.New().String(),
		ParentID:   parentID,
		Depth:      depth,
		State:      StateRunning,
		CreatedAt:  now,
		LastVerbAt: now,
		Model: ReducedSelfModel{
			ID:           model.ID,
			Capabilities: append([]string(nil), model.Capabilities...),
			Limitations:  append([]string(nil), model.Limitations...),
			Version:      model.Version,
		},
		sessionID: sessionID,
		tools:     ToolSurface{names: surface.Tools()},
	}

	r.agents[agent.ID] = agent
	r.channels[agent.ID] = channel
	r.globalCount++
	r.sessionCount[sessionID]++
	if parentID != "" {
		r.parentCount[parentID]++
	}

	return agent, nil
}

// SpawnWorker registers a child and starts exactly one goroutine with the
// inherited reduced model and immutable tool surface.
func (r *Registry) SpawnWorker(
	parentCtx context.Context,
	parentID string,
	sessionID string,
	depth int,
	model ReducedSelfModel,
	surface ToolSurface,
	worker Worker,
) (*Agent, error) {
	if parentCtx == nil || worker == nil {
		return nil, fmt.Errorf("swarm: parent context and worker are required")
	}
	agent, err := r.SpawnReduced(parentID, sessionID, depth, model, surface)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(parentCtx)
	agent.mu.Lock()
	agent.CancelFunc = cancel
	agent.done = make(chan struct{})
	view := WorkerContext{
		AgentID: agent.ID,
		Model: ReducedSelfModel{
			ID:           agent.Model.ID,
			Capabilities: append([]string(nil), agent.Model.Capabilities...),
			Limitations:  append([]string(nil), agent.Model.Limitations...),
			Version:      agent.Model.Version,
		},
		Tools: agent.tools.Tools(),
	}
	done := agent.done
	agent.mu.Unlock()
	go func() {
		artifact, runErr := worker(runCtx, view)
		artifact = append(json.RawMessage(nil), artifact...)
		agent.mu.Lock()
		if len(artifact) != 0 {
			agent.artifact = artifact
			agent.ArtifactCount++
		}
		if runErr != nil {
			agent.workerError = runErr.Error()
		}
		close(done)
		agent.mu.Unlock()
		r.Complete(agent.ID, sessionID)
		agent.mu.Lock()
		completion := Completion{
			AgentID:   agent.ID,
			SessionID: agent.sessionID,
			State:     agent.State,
			Artifact:  append(json.RawMessage(nil), agent.artifact...),
			Error:     agent.workerError,
		}
		agent.mu.Unlock()
		r.completions <- completion
	}()
	return agent, nil
}

// Completions is the push-based child completion stream.
func (r *Registry) Completions() <-chan Completion {
	return r.completions
}

// Tools returns the immutable spawn-time tool surface.
func (agent *Agent) Tools() []string {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	return agent.tools.Tools()
}

// Complete marks an agent as completed.
func (r *Registry) Complete(agentID string, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return
	}

	agent.mu.Lock()
	oldState := agent.State
	if oldState == StateRunning || oldState == StateYielded {
		agent.State = StateCompleted
	}
	agent.mu.Unlock()

	if oldState == StateRunning || oldState == StateYielded {
		r.globalCount--
		r.sessionCount[agent.sessionID]--
		if agent.ParentID != "" {
			r.parentCount[agent.ParentID]--
		}
	}
	if channel := r.channels[agentID]; channel != nil {
		channel.Close()
		delete(r.channels, agentID)
	}
}

// Abort marks an agent as aborted.
func (r *Registry) Abort(agentID string, sessionID string) {
	_, _ = r.abort(agentID, sessionID, "", false)
}

func (r *Registry) AbortScoped(
	agentID string,
	sessionID string,
	expectedState AgentState,
) (AgentSnapshot, error) {
	return r.abort(agentID, sessionID, expectedState, true)
}

func (r *Registry) abort(
	agentID string,
	sessionID string,
	expectedState AgentState,
	requireExpected bool,
) (AgentSnapshot, error) {
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	if agentID == "" {
		return AgentSnapshot{}, fmt.Errorf("%w: empty agent ID", ErrAgentNotFound)
	}
	if requireExpected && sessionID == "" {
		return AgentSnapshot{}, fmt.Errorf(
			"%w: authenticated session is required",
			ErrAgentSessionMismatch,
		)
	}
	if requireExpected &&
		expectedState != StateRunning && expectedState != StateYielded {
		return AgentSnapshot{}, fmt.Errorf(
			"%w: expected state %q is not active",
			ErrAgentStateConflict,
			expectedState,
		)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	agent, ok := r.agents[agentID]
	if !ok {
		return AgentSnapshot{}, fmt.Errorf("%w: %s", ErrAgentNotFound, agentID)
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if sessionID != "" && agent.sessionID != sessionID {
		return AgentSnapshot{}, fmt.Errorf(
			"%w: %s",
			ErrAgentSessionMismatch,
			agentID,
		)
	}
	oldState := agent.State
	if oldState != StateRunning && oldState != StateYielded {
		return AgentSnapshot{}, fmt.Errorf(
			"%w: agent %s is %s",
			ErrAgentStateConflict,
			agentID,
			oldState,
		)
	}
	if requireExpected && oldState != expectedState {
		return AgentSnapshot{}, fmt.Errorf(
			"%w: agent %s changed from %s to %s",
			ErrAgentStateConflict,
			agentID,
			expectedState,
			oldState,
		)
	}
	agent.State = StateAborted
	agent.LastVerbAt = r.clock.Now()
	agent.LastVerb = string(coordination.VerbAbort)
	if agent.CancelFunc != nil {
		agent.CancelFunc()
	}

	r.globalCount--
	r.sessionCount[agent.sessionID]--
	if agent.ParentID != "" {
		r.parentCount[agent.ParentID]--
	}
	if channel := r.channels[agentID]; channel != nil {
		channel.Close()
		delete(r.channels, agentID)
	}
	return snapshotAgentLocked(agent), nil
}

// ScanOrphans detects and handles orphaned agents.
func (r *Registry) ScanOrphans() []string {
	now := r.clock.Now()
	var orphans []string
	r.mu.Lock()
	for _, agent := range r.agents {
		agent.mu.Lock()
		if (agent.State == StateRunning || agent.State == StateYielded) &&
			now.Sub(agent.LastVerbAt) > OrphanThreshold {
			orphans = append(orphans, agent.ID)
		}
		agent.mu.Unlock()
	}
	r.mu.Unlock()
	for _, agentID := range orphans {
		parent, exists := r.ParentEndpoint(agentID)
		if !exists {
			r.Abort(agentID, "")
		} else {
			message, err := parent.Sign(
				coordination.VerbAbort,
				json.RawMessage(`{"reason":"orphan-recovery"}`),
			)
			if err != nil || r.DeliverToSubAgent(agentID, message) != nil {
				r.Abort(agentID, "")
			}
		}
		if agent, exists := r.Get(agentID); exists {
			agent.mu.Lock()
			agent.State = StateOrphaned
			agent.mu.Unlock()
		}
	}
	return orphans
}

// StartOrphanRecovery starts the required background scan loop. The returned
// channel closes after cancellation and all in-flight recovery waits finish.
func (r *Registry) StartOrphanRecovery(ctx context.Context) <-chan struct{} {
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(r.scanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, agentID := range r.ScanOrphans() {
					agent, exists := r.Get(agentID)
					if !exists {
						continue
					}
					agent.mu.Lock()
					done := agent.done
					agent.mu.Unlock()
					if done == nil {
						continue
					}
					timer := time.NewTimer(r.abortTimeout)
					select {
					case <-done:
						timer.Stop()
					case <-timer.C:
						// Go cannot forcibly terminate a goroutine. The worker
						// context was canceled by authenticated ABORT; a worker
						// that ignores it remains isolated and owns no lane.
					case <-ctx.Done():
						timer.Stop()
						return
					}
				}
			}
		}
	}()
	return stopped
}

// ParentEndpoint returns the parent side of one spawn-scoped channel.
func (r *Registry) ParentEndpoint(agentID string) (*coordination.Endpoint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	channel, exists := r.channels[agentID]
	if !exists {
		return nil, false
	}
	return channel.Parent(), true
}

// SubAgentEndpoint returns the child side of one spawn-scoped channel.
func (r *Registry) SubAgentEndpoint(agentID string) (*coordination.Endpoint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	channel, exists := r.channels[agentID]
	if !exists {
		return nil, false
	}
	return channel.SubAgent(), true
}

// DeliverToSubAgent is the only registry path for a parent coordination verb.
// Authentication and replay checks occur before any lifecycle state changes.
func (r *Registry) DeliverToSubAgent(
	agentID string,
	message *coordination.Message,
) error {
	r.mu.Lock()
	channel, exists := r.channels[agentID]
	r.mu.Unlock()
	if !exists {
		return fmt.Errorf("swarm: unknown authenticated channel for %s", agentID)
	}
	if err := channel.SubAgent().Verify(message); err != nil {
		return fmt.Errorf("swarm: reject parent verb: %w", err)
	}
	return r.applyVerb(agentID, message.Verb)
}

// DeliverToParent is the only registry path for a sub-agent coordination verb.
func (r *Registry) DeliverToParent(
	agentID string,
	message *coordination.Message,
) error {
	r.mu.Lock()
	channel, exists := r.channels[agentID]
	r.mu.Unlock()
	if !exists {
		return fmt.Errorf("swarm: unknown authenticated channel for %s", agentID)
	}
	if err := channel.Parent().Verify(message); err != nil {
		return fmt.Errorf("swarm: reject sub-agent verb: %w", err)
	}
	return r.applyVerb(agentID, message.Verb)
}

func (r *Registry) applyVerb(agentID string, verb coordination.Verb) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, exists := r.agents[agentID]
	if !exists {
		return fmt.Errorf("swarm: unknown agent %s", agentID)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.State == StateCompleted || agent.State == StateAborted ||
		agent.State == StateOrphaned {
		return fmt.Errorf("swarm: agent %s is not active", agentID)
	}
	agent.LastVerbAt = r.clock.Now()
	agent.LastVerb = string(verb)
	switch verb {
	case coordination.VerbYield:
		agent.State = StateYielded
	case coordination.VerbResume:
		agent.State = StateRunning
	case coordination.VerbAbort:
		agent.State = StateAborted
		if agent.CancelFunc != nil {
			agent.CancelFunc()
		}
		r.globalCount--
		r.sessionCount[agent.sessionID]--
		if agent.ParentID != "" {
			r.parentCount[agent.ParentID]--
		}
		if channel := r.channels[agentID]; channel != nil {
			channel.Close()
			delete(r.channels, agentID)
		}
	}
	return nil
}

// ActiveCount returns the number of active (running/yielded) agents.
func (r *Registry) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.globalCount
}

// SessionCount returns the number of active agents in a session.
func (r *Registry) SessionCount(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionCount[sessionID]
}

// Get returns an agent by ID. The returned pointer is the live agent;
// callers should not hold it across concurrent mutations.
func (r *Registry) Get(agentID string) (*Agent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, ok := r.agents[agentID]
	return agent, ok
}

// Artifact returns a defensive copy of a child's preserved terminal artifact.
func (r *Registry) Artifact(agentID string) (json.RawMessage, bool) {
	r.mu.Lock()
	agent, exists := r.agents[agentID]
	r.mu.Unlock()
	if !exists {
		return nil, false
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.artifact) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), agent.artifact...), true
}

// SetAssignment records the bounded user-facing assignment after a successful
// spawn without changing the child's immutable authority.
func (r *Registry) SetAssignment(agentID string, assignment string) error {
	r.mu.Lock()
	agent, exists := r.agents[agentID]
	r.mu.Unlock()
	if !exists {
		return fmt.Errorf("swarm: unknown agent %s", agentID)
	}
	assignment = strings.TrimSpace(assignment)
	if len(assignment) > 4096 {
		assignment = assignment[:4096]
	}
	agent.mu.Lock()
	agent.Assignment = assignment
	agent.mu.Unlock()
	return nil
}

// Snapshot returns a stable, session-scoped operator projection without
// artifacts, private worker output, channels, keys, or cancellation handles.
func (r *Registry) Snapshot(sessionID string) []AgentSnapshot {
	r.mu.Lock()
	found := make([]*Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		if sessionID == "" || agent.sessionID == sessionID {
			found = append(found, agent)
		}
	}
	r.mu.Unlock()
	result := make([]AgentSnapshot, 0, len(found))
	for _, agent := range found {
		agent.mu.Lock()
		result = append(result, snapshotAgentLocked(agent))
		agent.mu.Unlock()
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result
}

func snapshotAgentLocked(agent *Agent) AgentSnapshot {
	return AgentSnapshot{
		ID: agent.ID, ParentID: agent.ParentID, Depth: agent.Depth,
		State: agent.State, Assignment: agent.Assignment,
		CreatedAt: agent.CreatedAt, LastVerbAt: agent.LastVerbAt,
		LastVerb: agent.LastVerb, ArtifactCount: agent.ArtifactCount,
		HasError: strings.TrimSpace(agent.workerError) != "",
	}
}
