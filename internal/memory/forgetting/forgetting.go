// Package forgetting implements type-specific, recoverable memory decay.
package forgetting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/paxlabs-inc/ion-agent/internal/belief/premise"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"github.com/paxlabs-inc/ion-agent/internal/memory/cortex"
	"github.com/paxlabs-inc/ion-agent/internal/security/canary"
)

// Profile describes when a memory type becomes archive-eligible.
type Profile struct {
	HalfLife    time.Duration
	Infinite    bool
	Conditional string
}

// ProfileFor returns the binding decay profile.
func ProfileFor(memoryType memory.Type) Profile {
	switch memoryType {
	case memory.Identity, memory.Preference, memory.Constraint, memory.Capability:
		return Profile{Infinite: true}
	case memory.Fact:
		return Profile{HalfLife: 90 * 24 * time.Hour}
	case memory.Belief, memory.Event:
		return Profile{HalfLife: 30 * 24 * time.Hour}
	case memory.Goal:
		return Profile{Conditional: "resolved"}
	case memory.Pattern:
		return Profile{Conditional: "superseded"}
	default:
		return Profile{}
	}
}

// Salience applies half-life decay without mutating the source memory.
func Salience(memoryType memory.Type, initial float64, age time.Duration) float64 {
	profile := ProfileFor(memoryType)
	if profile.Infinite || profile.HalfLife <= 0 || age <= 0 {
		return initial
	}
	return initial * math.Pow(0.5, float64(age)/float64(profile.HalfLife))
}

// Store is the Cortex archival boundary.
type Store interface {
	ListByType(memory.Type) []uuid.UUID
	Resolve(uuid.UUID) (*cortex.Memory, error)
	Tombstone(context.Context, uuid.UUID, string, string) error
}

// CanaryProtector prevents honeypot archival and emits the canary alert.
type CanaryProtector interface {
	ProtectArchive(string, string) error
}

// Clock supplies the decay scan time.
type Clock interface{ Now() time.Time }

// ProtectionSet tracks live load-bearing and explicitly user-pinned memories.
type ProtectionSet struct {
	mu          sync.RWMutex
	loadBearing map[uuid.UUID]struct{}
	pinned      map[uuid.UUID]struct{}
	premises    interface{ Active() []*premise.Premise }
}

// NewProtectionSet constructs an empty protection index.
func NewProtectionSet(
	premises ...interface{ Active() []*premise.Premise },
) *ProtectionSet {
	var source interface{ Active() []*premise.Premise }
	if len(premises) > 0 {
		source = premises[0]
	}
	return &ProtectionSet{
		loadBearing: make(map[uuid.UUID]struct{}),
		pinned:      make(map[uuid.UUID]struct{}),
		premises:    source,
	}
}

func (set *ProtectionSet) MarkLoadBearing(id uuid.UUID) {
	set.mu.Lock()
	defer set.mu.Unlock()
	set.loadBearing[id] = struct{}{}
}

// ReplaceLoadBearing rebuilds premise-derived protection atomically while
// retaining explicit user pins.
func (set *ProtectionSet) ReplaceLoadBearing(ids []uuid.UUID) {
	rebuilt := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id != uuid.Nil {
			rebuilt[id] = struct{}{}
		}
	}
	set.mu.Lock()
	set.loadBearing = rebuilt
	set.mu.Unlock()
}

func (set *ProtectionSet) Pin(id uuid.UUID) {
	set.mu.Lock()
	defer set.mu.Unlock()
	set.pinned[id] = struct{}{}
}

func (set *ProtectionSet) IsProtected(id uuid.UUID) bool {
	set.mu.RLock()
	_, loadBearing := set.loadBearing[id]
	_, pinned := set.pinned[id]
	premises := set.premises
	set.mu.RUnlock()
	if loadBearing || pinned {
		return true
	}
	if premises != nil {
		for _, item := range premises.Active() {
			if item != nil && item.MemoryID != nil && *item.MemoryID == id {
				return true
			}
		}
	}
	return false
}

// Scanner archives only eligible, unprotected memories.
type Scanner struct {
	store       Store
	protections *ProtectionSet
	canaries    CanaryProtector
	clock       Clock
}

func NewScanner(
	store Store,
	protections *ProtectionSet,
	canaries CanaryProtector,
	clock Clock,
) (*Scanner, error) {
	if store == nil || protections == nil || canaries == nil || clock == nil {
		return nil, fmt.Errorf("forgetting: store, protections, canaries, and clock are required")
	}
	return &Scanner{
		store: store, protections: protections, canaries: canaries, clock: clock,
	}, nil
}

type ScanResult struct {
	Archived  []uuid.UUID `json:"archived"`
	Protected []uuid.UUID `json:"protected"`
	Canaries  []uuid.UUID `json:"canaries"`
}

// Scan archives due memories with Cortex tombstones, preserving all versions.
func (scanner *Scanner) Scan(ctx context.Context) (ScanResult, error) {
	now := scanner.clock.Now().UTC()
	var result ScanResult
	for _, memoryType := range memory.Types() {
		profile := ProfileFor(memoryType)
		if profile.Infinite {
			continue
		}
		for _, id := range scanner.store.ListByType(memoryType) {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			resolved, err := scanner.store.Resolve(id)
			if err != nil {
				return result, err
			}
			if scanner.protections.IsProtected(id) || payloadPinned(resolved.Version.Data) {
				result.Protected = append(result.Protected, id)
				continue
			}
			due := false
			switch profile.Conditional {
			case "resolved":
				due = payloadFlag(resolved.Version.Data, "resolved")
			case "superseded":
				due = payloadFlag(resolved.Version.Data, "superseded")
			default:
				due = now.Sub(resolved.Head.LastUpdatedAt) >= profile.HalfLife
			}
			if !due {
				continue
			}
			if err := scanner.canaries.ProtectArchive(id.String(), "strategic-forgetting"); err != nil {
				if errors.Is(err, canary.ErrCanaryArchive) {
					result.Canaries = append(result.Canaries, id)
					continue
				}
				return result, err
			}
			if err := scanner.store.Tombstone(
				ctx,
				id,
				"strategic forgetting: decay profile elapsed",
				"strategic-forgetting",
			); err != nil {
				return result, err
			}
			result.Archived = append(result.Archived, id)
		}
	}
	return result, nil
}

func payloadPinned(data []byte) bool {
	return payloadFlag(data, "pinned")
}

func payloadFlag(data []byte, field string) bool {
	var payload map[string]json.RawMessage
	if json.Unmarshal(data, &payload) != nil {
		return false
	}
	var value bool
	return json.Unmarshal(payload[field], &value) == nil && value
}
