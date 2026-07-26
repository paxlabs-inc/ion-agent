// Package automatrix implements the priority queue and restricted idle-cycle
// execution boundary used by background liveness systems.
package automatrix

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/protocol"
)

// Source identifies the subsystem that proposed idle work.
type Source string

const (
	SourceConversation Source = "conversation"
	SourceCuriosity    Source = "curiosity"
	SourceDreamweaver  Source = "dreamweaver"
)

// Action is one bounded operation in an idle work item. Exactly one of
// ToolCall and FetchURL may be set. URL reads deliberately bypass the general
// tool surface and are handled only by the runner's SSRF dispatcher.
type Action struct {
	ToolCall *protocol.NormalizedToolCall `json:"tool_call,omitempty"`
	FetchURL string                       `json:"fetch_url,omitempty"`
}

// WorkItem is a durable-ready unit of idle work. Payload preserves the
// originating subsystem's target without coupling the queue to that package.
type WorkItem struct {
	ID          uuid.UUID       `json:"id"`
	Source      Source          `json:"source"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Priority    float64         `json:"priority"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Actions     []Action        `json:"actions,omitempty"`
	Risk        DamageRisk      `json:"risk"`
	CreatedAt   time.Time       `json:"created_at"`
	ApprovedAt  *time.Time      `json:"approved_at,omitempty"`
}

// DamageRisk encodes the three immutable Automatrix non-negotiables.
type DamageRisk struct {
	Monetary      bool `json:"monetary"`
	Reputational  bool `json:"reputational"`
	Psychological bool `json:"psychological"`
}

func (risk DamageRisk) Any() bool {
	return risk.Monetary || risk.Reputational || risk.Psychological
}

// Queue is a concurrency-safe, deterministic max-priority queue. IDs are
// unique so repeated scans update a target instead of multiplying idle work.
type Queue struct {
	mu    sync.Mutex
	items map[uuid.UUID]WorkItem
}

// NewQueue constructs an empty Automatrix queue.
func NewQueue() *Queue {
	return &Queue{items: make(map[uuid.UUID]WorkItem)}
}

// Enqueue inserts or replaces one work item.
func (queue *Queue) Enqueue(ctx context.Context, item WorkItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateItem(item); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.items[item.ID] = cloneItem(item)
	return nil
}

// Dequeue removes the highest-priority item, using creation time and UUID as
// stable tie breakers.
func (queue *Queue) Dequeue(ctx context.Context) (WorkItem, bool, error) {
	return queue.dequeueExcluding(ctx, nil)
}

func (queue *Queue) dequeueExcluding(
	ctx context.Context,
	excluded map[string]struct{},
) (WorkItem, bool, error) {
	if err := ctx.Err(); err != nil {
		return WorkItem{}, false, err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	ordered := queue.orderedLocked()
	if len(ordered) == 0 {
		return WorkItem{}, false, nil
	}
	for _, item := range ordered {
		if _, skip := excluded[item.ID.String()]; skip {
			continue
		}
		delete(queue.items, item.ID)
		return cloneItem(item), true, nil
	}
	return WorkItem{}, false, nil
}

// Snapshot returns an immutable priority-ordered view.
func (queue *Queue) Snapshot() []WorkItem {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	ordered := queue.orderedLocked()
	result := make([]WorkItem, len(ordered))
	for index, item := range ordered {
		result[index] = cloneItem(item)
	}
	return result
}

// Approve binds an exact reviewed action plan to a queued opportunity. Work
// without this marker is proposal-only and cannot be executed by Runner.
func (queue *Queue) Approve(
	ctx context.Context,
	id uuid.UUID,
	actions []Action,
	at time.Time,
) (WorkItem, error) {
	if err := ctx.Err(); err != nil {
		return WorkItem{}, err
	}
	if id == uuid.Nil || len(actions) == 0 || at.IsZero() {
		return WorkItem{}, fmt.Errorf("automatrix: exact approved work plan is required")
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	item, exists := queue.items[id]
	if !exists {
		return WorkItem{}, fmt.Errorf("automatrix: work item not found")
	}
	item.Actions = append([]Action(nil), actions...)
	approved := at.UTC()
	item.ApprovedAt = &approved
	if err := validateItem(item); err != nil {
		return WorkItem{}, err
	}
	queue.items[id] = cloneItem(item)
	return cloneItem(item), nil
}

// Reject removes one proposal before execution. No action is attempted.
func (queue *Queue) Reject(ctx context.Context, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == uuid.Nil {
		return fmt.Errorf("automatrix: work item ID is required")
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if _, exists := queue.items[id]; !exists {
		return fmt.Errorf("automatrix: work item not found")
	}
	delete(queue.items, id)
	return nil
}

func (queue *Queue) orderedLocked() []WorkItem {
	ordered := make([]WorkItem, 0, len(queue.items))
	for _, item := range queue.items {
		ordered = append(ordered, cloneItem(item))
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].Priority != ordered[right].Priority {
			return ordered[left].Priority > ordered[right].Priority
		}
		if !ordered[left].CreatedAt.Equal(ordered[right].CreatedAt) {
			return ordered[left].CreatedAt.Before(ordered[right].CreatedAt)
		}
		return ordered[left].ID.String() < ordered[right].ID.String()
	})
	return ordered
}

func validateItem(item WorkItem) error {
	if item.ID == uuid.Nil {
		return fmt.Errorf("automatrix: work item ID is required")
	}
	switch item.Source {
	case SourceConversation, SourceCuriosity, SourceDreamweaver:
	default:
		return fmt.Errorf("automatrix: invalid work source %q", item.Source)
	}
	if strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.Description) == "" {
		return fmt.Errorf("automatrix: work item kind and description are required")
	}
	if item.Priority < 0 {
		return fmt.Errorf("automatrix: work item priority cannot be negative")
	}
	if item.Risk.Any() {
		return fmt.Errorf("automatrix: work violates a non-negotiable damage boundary")
	}
	if len(item.Payload) != 0 && !json.Valid(item.Payload) {
		return fmt.Errorf("automatrix: work item payload must be valid JSON")
	}
	if item.CreatedAt.IsZero() {
		return fmt.Errorf("automatrix: work item creation time is required")
	}
	for index, action := range item.Actions {
		hasTool := action.ToolCall != nil
		hasFetch := strings.TrimSpace(action.FetchURL) != ""
		if hasTool == hasFetch {
			return fmt.Errorf("automatrix: action %d must contain exactly one operation", index)
		}
		if hasTool {
			if err := action.ToolCall.Validate(); err != nil {
				return fmt.Errorf("automatrix: action %d: %w", index, err)
			}
		}
	}
	if item.ApprovedAt != nil && len(item.Actions) == 0 {
		return fmt.Errorf("automatrix: approved work must contain an exact action plan")
	}
	return nil
}

func cloneItem(item WorkItem) WorkItem {
	item.Payload = append(json.RawMessage(nil), item.Payload...)
	item.Actions = append([]Action(nil), item.Actions...)
	if item.ApprovedAt != nil {
		approved := *item.ApprovedAt
		item.ApprovedAt = &approved
	}
	for index := range item.Actions {
		if item.Actions[index].ToolCall != nil {
			call := *item.Actions[index].ToolCall
			call.Arguments = append(json.RawMessage(nil), call.Arguments...)
			item.Actions[index].ToolCall = &call
		}
	}
	return item
}
