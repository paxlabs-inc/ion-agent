// Package dashboard implements the safety dashboard WebSocket backend and
// event schema for real-time safety monitoring.
package dashboard

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// EventType classifies dashboard events.
type EventType string

const (
	EventSafetyViolation    EventType = "safety_violation"
	EventCircuitBreaker     EventType = "circuit_breaker"
	EventCanaryAccess       EventType = "canary_access"
	EventPolicyDecision     EventType = "policy_decision"
	EventEmotionalState     EventType = "emotional_state"
	EventCoordinationAuth   EventType = "coordination_auth"
	EventToolExecution      EventType = "tool_execution"
	EventPremiseRefuted     EventType = "premise_refuted"
	EventConvergenceWarning EventType = "convergence_warning"
	EventCassandraEdit      EventType = "cassandra_edit"
)

// Severity is the urgency level of a dashboard event.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Event is a safety dashboard event.
type Event struct {
	ID        string          `json:"id"`
	Type      EventType       `json:"type"`
	Severity  Severity        `json:"severity"`
	Source    string          `json:"source"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// Subscriber receives dashboard events.
type Subscriber struct {
	ID     string
	Events chan Event
	Types  map[EventType]bool // nil = all types
	types  map[EventType]bool
}

// Dashboard manages real-time safety event distribution.
type Dashboard struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscriber
	history     []Event
	maxHistory  int
	clock       Clock
}

// Clock abstracts time for testing.
type Clock interface {
	Now() time.Time
}

// New creates a safety dashboard.
func New(clock Clock, maxHistory int) (*Dashboard, error) {
	if clock == nil {
		return nil, fmt.Errorf("dashboard: clock required")
	}
	if maxHistory <= 0 {
		maxHistory = 1000
	}
	return &Dashboard{
		subscribers: make(map[string]*Subscriber),
		maxHistory:  maxHistory,
		clock:       clock,
	}, nil
}

// Publish emits an event to all matching subscribers.
func (d *Dashboard) Publish(eventType EventType, severity Severity, source, message string, data interface{}) Event {
	d.mu.Lock()
	defer d.mu.Unlock()

	var dataJSON json.RawMessage
	if data != nil {
		encoded, err := json.Marshal(data)
		if err == nil {
			dataJSON = encoded
		}
	}

	event := Event{
		ID:        uuid.New().String(),
		Type:      eventType,
		Severity:  severity,
		Source:    source,
		Message:   message,
		Data:      dataJSON,
		Timestamp: d.clock.Now(),
	}

	// Store in history.
	d.history = append(d.history, cloneEvent(event))
	if len(d.history) > d.maxHistory {
		d.history = d.history[len(d.history)-d.maxHistory:]
	}

	// Fan out to subscribers (non-blocking).
	for _, sub := range d.subscribers {
		if sub.types != nil && !sub.types[eventType] {
			continue
		}
		select {
		case sub.Events <- cloneEvent(event):
		default:
			// Subscriber is slow; drop event rather than block.
		}
	}

	return event
}

// Subscribe creates a new subscriber that receives events of the specified types.
// If types is nil, the subscriber receives all events.
func (d *Dashboard) Subscribe(types ...EventType) *Subscriber {
	d.mu.Lock()
	defer d.mu.Unlock()

	sub := &Subscriber{
		ID:     uuid.New().String(),
		Events: make(chan Event, 256),
	}
	if len(types) > 0 {
		sub.Types = make(map[EventType]bool, len(types))
		sub.types = make(map[EventType]bool, len(types))
		for _, t := range types {
			sub.Types[t] = true
			sub.types[t] = true
		}
	}
	d.subscribers[sub.ID] = sub
	return sub
}

// Unsubscribe removes a subscriber.
func (d *Dashboard) Unsubscribe(subID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sub, ok := d.subscribers[subID]; ok {
		close(sub.Events)
		delete(d.subscribers, subID)
	}
}

// History returns the most recent events, optionally filtered by type.
func (d *Dashboard) History(limit int, eventType EventType) []Event {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if limit <= 0 || limit > len(d.history) {
		limit = len(d.history)
	}

	var result []Event
	for i := len(d.history) - 1; i >= 0 && len(result) < limit; i-- {
		if eventType != "" && d.history[i].Type != eventType {
			continue
		}
		result = append(result, cloneEvent(d.history[i]))
	}
	return result
}

func cloneEvent(event Event) Event {
	event.Data = append(json.RawMessage(nil), event.Data...)
	return event
}

// SubscriberCount returns the number of active subscribers.
func (d *Dashboard) SubscriberCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.subscribers)
}
