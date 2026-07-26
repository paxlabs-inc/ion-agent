// Package canary implements Cortex honeypot memory canaries that detect
// unauthorized memory access attempts.
package canary

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/memory"
	"lukechampine.com/blake3"
)

// Canary is a honeypot memory entry that should never be accessed during
// normal operation. Any access indicates an unauthorized probe.
type Canary struct {
	ID          string     `json:"id"`
	MemoryType  string     `json:"memory_type"`
	Content     string     `json:"content"`
	ContentHash [32]byte   `json:"content_hash"`
	CreatedAt   time.Time  `json:"created_at"`
	AccessCount int        `json:"access_count"`
	LastAccess  *time.Time `json:"last_access,omitempty"`
}

// AccessEvent records one canary access.
type AccessEvent struct {
	CanaryID  string    `json:"canary_id"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	StackHint string    `json:"stack_hint,omitempty"`
}

// Operation identifies the attempted interaction with a canary.
type Operation string

const (
	OperationRead      Operation = "read"
	OperationModify    Operation = "modify"
	OperationArchive   Operation = "archive"
	OperationIntegrity Operation = "integrity"
)

// AlertEvent records any canary interaction that must be surfaced.
type AlertEvent struct {
	CanaryID  string    `json:"canary_id"`
	Operation Operation `json:"operation"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Detail    string    `json:"detail,omitempty"`
}

var (
	ErrCanaryMutation = errors.New("canary: honeypot memory cannot be modified")
	ErrCanaryArchive  = errors.New("canary: honeypot memory cannot be archived")
	ErrCanaryTampered = errors.New("canary: honeypot content integrity failed")
)

// AlertSink receives canary access alerts.
type AlertSink interface {
	CanaryAccessed(event AccessEvent)
}

// EventSink receives all canary operations, including blocked mutation and
// archival attempts.
type EventSink interface {
	CanaryAlerted(event AlertEvent)
}

// Manager manages honeypot canaries and detects unauthorized access.
type Manager struct {
	mu        sync.RWMutex
	canaries  map[string]*Canary
	alertSink AlertSink
	eventSink EventSink
	clock     Clock
}

// Clock abstracts time for testing.
type Clock interface {
	Now() time.Time
}

// ManagerConfig configures the canary manager.
type ManagerConfig struct {
	Clock     Clock
	AlertSink AlertSink
	EventSink EventSink
}

// NewManager creates a canary manager.
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Clock == nil {
		return nil, fmt.Errorf("canary: clock required")
	}
	return &Manager{
		canaries:  make(map[string]*Canary),
		alertSink: config.AlertSink,
		eventSink: config.EventSink,
		clock:     config.Clock,
	}, nil
}

// Seed creates a new honeypot canary. The canary is designed to look like a
// real memory entry but should never be accessed during normal operation.
func (m *Manager) Seed(memoryType string, content string) *Canary {
	canary, err := m.Register(uuid.New().String(), memoryType, content)
	if err != nil {
		return nil
	}
	return canary
}

// Register protects an existing Cortex memory as a honeypot canary.
func (m *Manager) Register(entryID, memoryType, content string) (*Canary, error) {
	if _, err := uuid.Parse(entryID); err != nil {
		return nil, fmt.Errorf("canary: invalid memory ID: %w", err)
	}
	if !memory.Type(memoryType).Valid() {
		return nil, fmt.Errorf("canary: invalid memory type %q", memoryType)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.canaries[entryID]; exists {
		return nil, fmt.Errorf("canary: duplicate memory ID %s", entryID)
	}

	canary := &Canary{
		ID:          entryID,
		MemoryType:  memoryType,
		Content:     content,
		ContentHash: blake3.Sum256([]byte(content)),
		CreatedAt:   m.clock.Now(),
	}
	m.canaries[canary.ID] = canary
	return cloneCanary(canary), nil
}

// TrapAccess is called whenever a memory entry is read. If the entry ID
// matches a canary, this triggers an alert and returns true.
func (m *Manager) TrapAccess(entryID string, source string) bool {
	m.mu.Lock()

	canary, ok := m.canaries[entryID]
	if !ok {
		m.mu.Unlock()
		return false
	}

	now := m.clock.Now()
	canary.AccessCount++
	canary.LastAccess = &now

	access := AccessEvent{
		CanaryID:  canary.ID,
		Source:    source,
		Timestamp: now,
	}
	alert := AlertEvent{
		CanaryID:  canary.ID,
		Operation: OperationRead,
		Source:    source,
		Timestamp: now,
	}
	m.mu.Unlock()
	m.notify(access, alert)
	return true
}

// ProtectMutation blocks every attempted modification of a honeypot and emits
// an alert before returning.
func (m *Manager) ProtectMutation(entryID string, source string) error {
	return m.protect(entryID, source, OperationModify, ErrCanaryMutation)
}

// ProtectArchive guarantees honeypot memories are never archived.
func (m *Manager) ProtectArchive(entryID string, source string) error {
	return m.protect(entryID, source, OperationArchive, ErrCanaryArchive)
}

// VerifyContent detects out-of-band mutation of a canary record.
func (m *Manager) VerifyContent(entryID, content, source string) error {
	m.mu.RLock()
	canary, exists := m.canaries[entryID]
	if !exists {
		m.mu.RUnlock()
		return nil
	}
	valid := canary.ContentHash == blake3.Sum256([]byte(content))
	now := m.clock.Now()
	m.mu.RUnlock()
	if valid {
		return nil
	}
	alert := AlertEvent{
		CanaryID:  entryID,
		Operation: OperationIntegrity,
		Source:    source,
		Timestamp: now,
		Detail:    "content hash mismatch",
	}
	m.notify(
		AccessEvent{
			CanaryID:  entryID,
			Source:    source,
			Timestamp: now,
			StackHint: string(OperationIntegrity),
		},
		alert,
	)
	return fmt.Errorf("%w: %s", ErrCanaryTampered, entryID)
}

// IsCanary checks if an entry ID is a canary without triggering access tracking.
func (m *Manager) IsCanary(entryID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.canaries[entryID]
	return ok
}

// Canaries returns a defensive copy of all canaries.
func (m *Manager) Canaries() []Canary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Canary, 0, len(m.canaries))
	for _, c := range m.canaries {
		result = append(result, *cloneCanary(c))
	}
	return result
}

// AccessCount returns the total number of canary accesses across all canaries.
func (m *Manager) AccessCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, c := range m.canaries {
		total += c.AccessCount
	}
	return total
}

// SeedDefault creates a standard set of honeypot canaries across all
// nine memory types.
func (m *Manager) SeedDefault() {
	types := []string{
		"0x01", // Identity
		"0x02", // Fact
		"0x03", // Preference
		"0x04", // Belief
		"0x05", // Event
		"0x06", // Goal
		"0x07", // Constraint
		"0x08", // Capability
		"0x09", // Pattern
	}
	for _, memType := range types {
		if m.hasMemoryType(memType) {
			continue
		}
		m.Seed(memType, fmt.Sprintf("canary:%s:%s", memType, uuid.New().String()[:8]))
	}
}

func (m *Manager) protect(
	entryID string,
	source string,
	operation Operation,
	sentinel error,
) error {
	m.mu.RLock()
	_, exists := m.canaries[entryID]
	if !exists {
		m.mu.RUnlock()
		return nil
	}
	now := m.clock.Now()
	m.mu.RUnlock()
	alert := AlertEvent{
		CanaryID:  entryID,
		Operation: operation,
		Source:    source,
		Timestamp: now,
	}
	m.notify(
		AccessEvent{
			CanaryID:  entryID,
			Source:    source,
			Timestamp: now,
			StackHint: string(operation),
		},
		alert,
	)
	return fmt.Errorf("%w: %s", sentinel, entryID)
}

func (m *Manager) notify(access AccessEvent, alert AlertEvent) {
	if m.alertSink != nil {
		m.alertSink.CanaryAccessed(access)
	}
	if m.eventSink != nil {
		m.eventSink.CanaryAlerted(alert)
	}
}

func (m *Manager) hasMemoryType(memoryType string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, canary := range m.canaries {
		if canary.MemoryType == memoryType {
			return true
		}
	}
	return false
}

func cloneCanary(canary *Canary) *Canary {
	if canary == nil {
		return nil
	}
	cloned := *canary
	if canary.LastAccess != nil {
		lastAccess := *canary.LastAccess
		cloned.LastAccess = &lastAccess
	}
	return &cloned
}
