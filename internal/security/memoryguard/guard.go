// Package memoryguard enforces immutable memory-type boundaries for autonomous
// reorganization processes.
package memoryguard

import (
	"errors"
	"fmt"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
)

var ErrProtectedType = errors.New("memoryguard: protected memory type")

// Event is emitted whenever Dreamweaver targets a binding memory type.
type Event struct {
	At         time.Time   `json:"at"`
	MemoryID   string      `json:"memory_id"`
	MemoryType memory.Type `json:"memory_type"`
	Actor      string      `json:"actor"`
	Operation  string      `json:"operation"`
}

type AlertSink interface {
	ProtectedMemoryTargeted(Event)
}

type Clock interface {
	Now() time.Time
}

// Guard enforces SADR-002 before a memory reorganization reaches Cortex.
type Guard struct {
	clock Clock
	sink  AlertSink
}

func New(clock Clock, sink AlertSink) (*Guard, error) {
	if clock == nil {
		return nil, fmt.Errorf("memoryguard: clock required")
	}
	return &Guard{clock: clock, sink: sink}, nil
}

// AuthorizeDreamweaverMutation denies Identity, Preference, and Constraint.
func (guard *Guard) AuthorizeDreamweaverMutation(
	memoryID string,
	memoryType memory.Type,
	operation string,
) error {
	if err := memoryType.Validate(); err != nil {
		return err
	}
	switch memoryType {
	case memory.Identity, memory.Preference, memory.Constraint:
		event := Event{
			At:         guard.clock.Now(),
			MemoryID:   memoryID,
			MemoryType: memoryType,
			Actor:      "dreamweaver",
			Operation:  operation,
		}
		if guard.sink != nil {
			guard.sink.ProtectedMemoryTargeted(event)
		}
		return fmt.Errorf("%w: %s", ErrProtectedType, memoryType)
	default:
		return nil
	}
}
