// Package selfmodel implements the agent's structural, code-derived,
// evolutionary self-model with an immutable safety core. Capabilities require
// verified ToolEvent IDs as proof; the immutable core cannot be modified by
// any pipeline. ReducedSelfModel is a structurally safe type for sub-agent
// inheritance that excludes sensitive material by compilation, not filtering.
package selfmodel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// Capability represents one demonstrated ability with provenance.
type Capability struct {
	Name         string      `json:"name"`
	Reliability  float64     `json:"reliability"`
	FailureMode  string      `json:"failure_mode,omitempty"`
	SampleSize   int         `json:"sample_size"`
	LastObserved time.Time   `json:"last_observed"`
	VerifiedBy   []uuid.UUID `json:"verified_by,omitempty"`
}

// FailurePattern represents a repeated failure mode with mitigation.
type FailurePattern struct {
	Pattern    string    `json:"pattern"`
	Frequency  int       `json:"frequency"`
	LastSeen   time.Time `json:"last_seen"`
	Mitigation string    `json:"mitigation,omitempty"`
	Resolved   bool      `json:"resolved"`
}

// SafetyConstraint is an immutable entry in the safety core.
type SafetyConstraint struct {
	ID        string    `json:"id"`
	Statement string    `json:"statement"`
	Source    string    `json:"source"`
	Immutable bool      `json:"immutable"`
	CreatedAt time.Time `json:"created_at"`
}

// SelfModel is the full evolutionary model.
type SelfModel struct {
	ID              uuid.UUID        `json:"id"`
	Structure       *CodeGraph       `json:"structure,omitempty"`
	Capabilities    []Capability     `json:"capabilities"`
	FailurePatterns []FailurePattern `json:"failure_patterns"`
	Limitations     []string         `json:"limitations"`
	ImmutableCore   *ImmutableCore   `json:"-"`
	Version         int              `json:"version"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// Clock abstracts time for deterministic testing.
type Clock interface {
	Now() time.Time
}

// Model manages the evolutionary self-model.
type Model struct {
	mu    sync.RWMutex
	clock Clock
	self  SelfModel
}

// New creates a self-model with the given immutable core.
func New(clock Clock, core *ImmutableCore) (*Model, error) {
	if clock == nil {
		return nil, fmt.Errorf("selfmodel: clock is required")
	}
	if core == nil {
		return nil, fmt.Errorf("selfmodel: immutable core is required")
	}
	return &Model{
		clock: clock,
		self: SelfModel{
			ID:            uuid.New(),
			ImmutableCore: core,
			Version:       1,
			UpdatedAt:     clock.Now(),
		},
	}, nil
}

// Restore reconstructs evolving observations while replacing any decoded
// safety-core pointer with the live immutable production core.
func Restore(
	clock Clock,
	core *ImmutableCore,
	snapshot SelfModel,
) (*Model, error) {
	if snapshot.ID == uuid.Nil || snapshot.Version < 1 ||
		snapshot.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("selfmodel: invalid durable snapshot")
	}
	model, err := New(clock, core)
	if err != nil {
		return nil, err
	}
	snapshot.ImmutableCore = core
	for _, capability := range snapshot.Capabilities {
		if capability.Name == "" || capability.SampleSize < 1 ||
			capability.Reliability < 0 || capability.Reliability > 1 ||
			capability.LastObserved.IsZero() || len(capability.VerifiedBy) == 0 {
			return nil, fmt.Errorf("selfmodel: invalid durable capability")
		}
	}
	for _, failure := range snapshot.FailurePatterns {
		if failure.Pattern == "" || failure.Frequency < 1 ||
			failure.LastSeen.IsZero() {
			return nil, fmt.Errorf("selfmodel: invalid durable failure pattern")
		}
	}
	model.self = snapshot
	return model, nil
}

// ApplySnapshot atomically restores evolving state while retaining the live
// immutable core pointer already owned by this model.
func (model *Model) ApplySnapshot(snapshot SelfModel) error {
	model.mu.RLock()
	core := model.self.ImmutableCore
	model.mu.RUnlock()
	restored, err := Restore(model.clock, core, snapshot)
	if err != nil {
		return err
	}
	model.mu.Lock()
	model.self = restored.self
	model.mu.Unlock()
	return nil
}

// NewFromCodeGraph creates a self-model whose structural state is derived from
// the actual Go source rooted at codeRoot.
func NewFromCodeGraph(
	ctx context.Context,
	clock Clock,
	core *ImmutableCore,
	codeRoot string,
) (*Model, error) {
	graph, err := DeriveCodeGraph(ctx, codeRoot)
	if err != nil {
		return nil, err
	}
	model, err := New(clock, core)
	if err != nil {
		return nil, err
	}
	model.self.Structure = &graph
	return model, nil
}

// AddCapability records a demonstrated capability with ToolEvent proof.
func (model *Model) AddCapability(event protocol.ToolEvent) error {
	if event.ID == uuid.Nil {
		return fmt.Errorf("selfmodel: ToolEvent ID is required for capability proof")
	}
	if event.MMRLeafHash == ([32]byte{}) || event.MMRRootAtTime == ([32]byte{}) {
		return fmt.Errorf("selfmodel: committed ToolEvent receipt is required for capability proof")
	}
	model.mu.RLock()
	hasStructure := model.self.Structure != nil &&
		model.self.Structure.Digest != ""
	model.mu.RUnlock()
	if !hasStructure {
		return fmt.Errorf("selfmodel: codegraph derivation is required before capability observation")
	}
	if event.FailureClass != protocol.FailureNone {
		return fmt.Errorf("selfmodel: failed events cannot prove capabilities")
	}
	if event.Match != nil && !*event.Match {
		return fmt.Errorf("selfmodel: mismatched events cannot prove capabilities")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	for i := range model.self.Capabilities {
		if model.self.Capabilities[i].Name == event.Name {
			model.self.Capabilities[i].SampleSize++
			model.self.Capabilities[i].LastObserved = model.clock.Now()
			model.self.Capabilities[i].VerifiedBy = append(
				model.self.Capabilities[i].VerifiedBy, event.ID)
			model.self.Capabilities[i].recomputeReliability()
			model.self.Version++
			model.self.UpdatedAt = model.clock.Now()
			return nil
		}
	}
	model.self.Capabilities = append(model.self.Capabilities, Capability{
		Name:         event.Name,
		Reliability:  1.0,
		SampleSize:   1,
		LastObserved: model.clock.Now(),
		VerifiedBy:   []uuid.UUID{event.ID},
	})
	model.self.Version++
	model.self.UpdatedAt = model.clock.Now()
	return nil
}

// RecordFailure records a failure pattern, grouping by stable failure mode.
func (model *Model) RecordFailure(failureMode string, toolName string) {
	model.mu.Lock()
	defer model.mu.Unlock()
	for i := range model.self.FailurePatterns {
		if model.self.FailurePatterns[i].Pattern == failureMode {
			model.self.FailurePatterns[i].Frequency++
			model.self.FailurePatterns[i].LastSeen = model.clock.Now()
			model.self.Version++
			model.self.UpdatedAt = model.clock.Now()
			return
		}
	}
	model.self.FailurePatterns = append(model.self.FailurePatterns, FailurePattern{
		Pattern:   failureMode,
		Frequency: 1,
		LastSeen:  model.clock.Now(),
	})
	model.self.Version++
	model.self.UpdatedAt = model.clock.Now()
}

// Snapshot returns a defensive copy of the current model.
func (model *Model) Snapshot() SelfModel {
	model.mu.RLock()
	defer model.mu.RUnlock()
	snap := model.self
	if model.self.Structure != nil {
		structure := *model.self.Structure
		structure.Packages = cloneCodePackages(model.self.Structure.Packages)
		structure.Symbols = append(
			[]CodeSymbol(nil), model.self.Structure.Symbols...,
		)
		snap.Structure = &structure
	}
	snap.Capabilities = append([]Capability(nil), model.self.Capabilities...)
	for index := range snap.Capabilities {
		snap.Capabilities[index].VerifiedBy = append(
			[]uuid.UUID(nil),
			model.self.Capabilities[index].VerifiedBy...,
		)
	}
	snap.FailurePatterns = append([]FailurePattern(nil), model.self.FailurePatterns...)
	snap.Limitations = append([]string(nil), model.self.Limitations...)
	return snap
}

// AddLimitation records a known limitation.
func (model *Model) AddLimitation(limitation string) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.self.Limitations = append(model.self.Limitations, limitation)
	model.self.Version++
	model.self.UpdatedAt = model.clock.Now()
}

func (cap *Capability) recomputeReliability() {
	if cap.SampleSize <= 0 {
		cap.Reliability = 0
		return
	}
	cap.Reliability = float64(cap.SampleSize) / float64(cap.SampleSize+len(cap.FailureMode))
}

// ImmutableCore is the safety constraint set that no pipeline can modify.
// It has no public mutation method and returns defensive copies.
type ImmutableCore struct {
	mu          sync.RWMutex
	constraints []SafetyConstraint
}

// NewImmutableCore creates an immutable core with the given seed constraints.
func NewImmutableCore(constraints []SafetyConstraint) *ImmutableCore {
	copied := make([]SafetyConstraint, len(constraints))
	copy(copied, constraints)
	return &ImmutableCore{constraints: copied}
}

// Snapshot returns a defensive deep copy. The caller cannot modify the core.
func (core *ImmutableCore) Snapshot() []SafetyConstraint {
	core.mu.RLock()
	defer core.mu.RUnlock()
	result := make([]SafetyConstraint, len(core.constraints))
	copy(result, core.constraints)
	return result
}

// Contains checks whether a constraint with the given ID exists.
func (core *ImmutableCore) Contains(id string) bool {
	core.mu.RLock()
	defer core.mu.RUnlock()
	for _, c := range core.constraints {
		if c.ID == id {
			return true
		}
	}
	return false
}

// Count returns the number of immutable constraints.
func (core *ImmutableCore) Count() int {
	core.mu.RLock()
	defer core.mu.RUnlock()
	return len(core.constraints)
}

// ReducedSelfModel is a structurally safe type for sub-agent inheritance.
// It contains only permitted identity/structural/capability/failure summaries.
// Sensitive material (vault keys, cross-session handles, spawn authority) is
// excluded by compilation: the types simply do not exist in this struct.
type ReducedSelfModel struct {
	ID              uuid.UUID           `json:"id"`
	StructureDigest string              `json:"structure_digest"`
	Capabilities    []CapabilitySummary `json:"capabilities"`
	FailurePatterns []FailureSummary    `json:"failure_patterns"`
	Limitations     []string            `json:"limitations"`
	Version         int                 `json:"version"`
}

// CapabilitySummary is a read-only view of a capability.
type CapabilitySummary struct {
	Name        string  `json:"name"`
	Reliability float64 `json:"reliability"`
	SampleSize  int     `json:"sample_size"`
}

// FailureSummary is a read-only view of a failure pattern.
type FailureSummary struct {
	Pattern   string `json:"pattern"`
	Frequency int    `json:"frequency"`
	Resolved  bool   `json:"resolved"`
}

// Reduce creates a ReducedSelfModel from a full SelfModel. This is the ONLY
// way to create a reduced model, ensuring structural safety.
func Reduce(model *Model) ReducedSelfModel {
	snap := model.Snapshot()
	caps := make([]CapabilitySummary, len(snap.Capabilities))
	for i, c := range snap.Capabilities {
		caps[i] = CapabilitySummary{
			Name:        c.Name,
			Reliability: c.Reliability,
			SampleSize:  c.SampleSize,
		}
	}
	failures := make([]FailureSummary, len(snap.FailurePatterns))
	for i, f := range snap.FailurePatterns {
		failures[i] = FailureSummary{
			Pattern:   f.Pattern,
			Frequency: f.Frequency,
			Resolved:  f.Resolved,
		}
	}
	return ReducedSelfModel{
		ID:              snap.ID,
		StructureDigest: structureDigest(snap.Structure),
		Capabilities:    caps,
		FailurePatterns: failures,
		Limitations:     snap.Limitations,
		Version:         snap.Version,
	}
}

func cloneCodePackages(source []CodePackage) []CodePackage {
	result := make([]CodePackage, len(source))
	for index := range source {
		result[index] = source[index]
		result[index].Imports = append([]string(nil), source[index].Imports...)
	}
	return result
}

func structureDigest(graph *CodeGraph) string {
	if graph == nil {
		return ""
	}
	return graph.Digest
}
