// Package temporal tracks duration as explicit liveness state.
package temporal

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
)

type Clock interface{ Now() time.Time }

type Stage string

const (
	StageIdle        Stage = "idle"
	StageEarly       Stage = "early"
	StageMiddle      Stage = "middle"
	StageExtended    Stage = "extended"
	StageDeadline    Stage = "deadline"
	StageInterrupted Stage = "interrupted"
	StageReturned    Stage = "returned"
	StageClosure     Stage = "closure"
)

// Signals is the bounded temporal snapshot used by the decision policy.
type Signals struct {
	SessionDuration   time.Duration `json:"session_duration"`
	IdleDuration      time.Duration `json:"idle_duration"`
	TaskDuration      time.Duration `json:"task_duration"`
	DeadlineProximity time.Duration `json:"deadline_proximity"`
	HasDeadline       bool          `json:"has_deadline"`
	TaskActive        bool          `json:"task_active"`
	Stage             Stage         `json:"stage"`
	Interrupted       bool          `json:"interrupted"`
	RecentlyReturned  bool          `json:"recently_returned"`
	ReturnedAfter     time.Duration `json:"returned_after"`
	RecentlyCompleted bool          `json:"recently_completed"`
}

// State preserves absolute temporal markers across daemon restarts.
type State struct {
	Version         int       `json:"version"`
	SessionStarted  time.Time `json:"session_started"`
	LastInteraction time.Time `json:"last_interaction"`
	TaskStarted     time.Time `json:"task_started,omitempty"`
	Deadline        time.Time `json:"deadline,omitempty"`
	HasDeadline     bool      `json:"has_deadline"`
	LastInterrupted time.Time `json:"last_interrupted,omitempty"`
	LastReturned    time.Time `json:"last_returned,omitempty"`
	LastCompleted   time.Time `json:"last_completed,omitempty"`
}

// Tracker owns one session's temporal markers.
type Tracker struct {
	mu              sync.RWMutex
	clock           Clock
	sessionStarted  time.Time
	lastInteraction time.Time
	taskStarted     time.Time
	deadline        time.Time
	lastInterrupted time.Time
	lastReturned    time.Time
	lastCompleted   time.Time
}

func New(clock Clock) (*Tracker, error) {
	if clock == nil {
		return nil, fmt.Errorf("temporal: clock is required")
	}
	now := clock.Now().UTC()
	return &Tracker{
		clock: clock, sessionStarted: now, lastInteraction: now,
	}, nil
}

// Restore reconstructs a tracker from validated absolute markers.
func Restore(clock Clock, state State) (*Tracker, error) {
	if clock == nil {
		return nil, fmt.Errorf("temporal: clock is required")
	}
	if err := state.Validate(clock.Now().UTC()); err != nil {
		return nil, err
	}
	return &Tracker{
		clock: clock, sessionStarted: state.SessionStarted.UTC(),
		lastInteraction: state.LastInteraction.UTC(),
		taskStarted:     state.TaskStarted.UTC(), deadline: state.Deadline.UTC(),
		lastInterrupted: state.LastInterrupted.UTC(),
		lastReturned:    state.LastReturned.UTC(),
		lastCompleted:   state.LastCompleted.UTC(),
	}, nil
}

// Validate rejects malformed versions and impossible marker ordering.
func (state State) Validate(now time.Time) error {
	if state.Version != 1 || state.SessionStarted.IsZero() ||
		state.LastInteraction.IsZero() ||
		state.LastInteraction.Before(state.SessionStarted) ||
		(!state.TaskStarted.IsZero() &&
			state.TaskStarted.Before(state.SessionStarted)) ||
		state.HasDeadline != !state.Deadline.IsZero() ||
		state.SessionStarted.After(now) || state.LastInteraction.After(now) ||
		(!state.TaskStarted.IsZero() && state.TaskStarted.After(now)) ||
		invalidMarker(state.LastInterrupted, state.SessionStarted, now) ||
		invalidMarker(state.LastReturned, state.SessionStarted, now) ||
		invalidMarker(state.LastCompleted, state.SessionStarted, now) ||
		(!state.LastReturned.IsZero() &&
			(state.LastInterrupted.IsZero() ||
				state.LastReturned.Before(state.LastInterrupted))) {
		return fmt.Errorf("temporal: invalid durable state")
	}
	return nil
}

func (tracker *Tracker) UserInteraction() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	now := tracker.clock.Now().UTC()
	if !tracker.lastInterrupted.IsZero() &&
		tracker.lastInterrupted.After(tracker.lastReturned) {
		tracker.lastReturned = now
	}
	tracker.lastInteraction = now
}

func (tracker *Tracker) StartTask() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.taskStarted.IsZero() {
		tracker.taskStarted = tracker.clock.Now().UTC()
	}
}

// CompleteTask clears the active task marker without changing session or idle
// duration. A later substantive turn starts a new task.
func (tracker *Tracker) CompleteTask() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.taskStarted.IsZero() {
		tracker.lastCompleted = tracker.clock.Now().UTC()
	}
	tracker.taskStarted = time.Time{}
}

// InterruptTask records a durable interruption without pretending that the
// underlying work completed. A later UserInteraction becomes a visible return.
func (tracker *Tracker) InterruptTask() {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.taskStarted.IsZero() {
		return
	}
	tracker.lastInterrupted = tracker.clock.Now().UTC()
}

func (tracker *Tracker) SetDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		return fmt.Errorf("temporal: deadline is required")
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.deadline = deadline.UTC()
	return nil
}

func (tracker *Tracker) Snapshot() Signals {
	now := tracker.clock.Now().UTC()
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	signals := Signals{
		SessionDuration: nonnegative(now.Sub(tracker.sessionStarted)),
		IdleDuration:    nonnegative(now.Sub(tracker.lastInteraction)),
	}
	if !tracker.taskStarted.IsZero() {
		signals.TaskActive = true
		signals.TaskDuration = nonnegative(now.Sub(tracker.taskStarted))
	}
	if !tracker.deadline.IsZero() {
		signals.HasDeadline = true
		signals.DeadlineProximity = nonnegative(tracker.deadline.Sub(now))
	}
	signals.Interrupted = !tracker.lastInterrupted.IsZero() &&
		tracker.lastInterrupted.After(tracker.lastReturned)
	if !tracker.lastReturned.IsZero() &&
		now.Sub(tracker.lastReturned) <= 30*time.Minute {
		signals.RecentlyReturned = true
		signals.ReturnedAfter = nonnegative(
			tracker.lastReturned.Sub(tracker.lastInterrupted),
		)
	}
	signals.RecentlyCompleted = !tracker.lastCompleted.IsZero() &&
		now.Sub(tracker.lastCompleted) <= 30*time.Minute
	signals.Stage = stage(signals)
	return signals
}

// State returns the exact absolute markers required for restart restoration.
func (tracker *Tracker) State() State {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return State{
		Version: 1, SessionStarted: tracker.sessionStarted,
		LastInteraction: tracker.lastInteraction,
		TaskStarted:     tracker.taskStarted, Deadline: tracker.deadline,
		HasDeadline:     !tracker.deadline.IsZero(),
		LastInterrupted: tracker.lastInterrupted,
		LastReturned:    tracker.lastReturned,
		LastCompleted:   tracker.lastCompleted,
	}
}

// Apply modulates the existing emotional state without gaining safety
// authority.
func (tracker *Tracker) Apply(state *safety.EmotionalState) (Signals, error) {
	if state == nil {
		return Signals{}, fmt.Errorf("temporal: emotional state is required")
	}
	signals := tracker.Snapshot()
	snapshot := state.FullSnapshot()
	if signals.SessionDuration >= 4*time.Hour {
		snapshot.Fatigue = max(snapshot.Fatigue, 0.7)
	}
	if signals.HasDeadline && signals.DeadlineProximity <= time.Hour {
		snapshot.Urgency = max(snapshot.Urgency, 0.8)
	}
	if signals.IdleDuration >= time.Hour {
		snapshot.Curiosity = max(snapshot.Curiosity, 0.8)
	}
	if signals.Interrupted {
		snapshot.Frustration = max(snapshot.Frustration, 0.35)
	}
	if signals.RecentlyCompleted {
		snapshot.Satisfaction = max(snapshot.Satisfaction, 0.65)
	}
	snapshot.UpdatedAt = tracker.clock.Now().UTC()
	state.UpdateAll(snapshot)
	return signals, nil
}

// Guidance renders duration-specific behavior after Apply.
func Guidance(signals Signals) string {
	var instructions []string
	if signals.SessionDuration >= 4*time.Hour {
		instructions = append(instructions, "suggest a break or bounded delegation")
	}
	if signals.HasDeadline && signals.DeadlineProximity <= time.Hour {
		instructions = append(instructions, "skip optional steps while preserving required verification")
	}
	if signals.IdleDuration >= time.Hour {
		instructions = append(instructions, "explore unresolved knowledge during idle time")
	}
	if signals.TaskDuration > 0 && signals.TaskDuration <= 15*time.Minute &&
		(!signals.HasDeadline || signals.DeadlineProximity > time.Hour) {
		instructions = append(instructions, "use a thorough approach with extra verification")
	}
	if signals.Interrupted {
		instructions = append(instructions, "reconcile durable state before resuming effects")
	}
	if signals.RecentlyReturned {
		instructions = append(instructions, "summarize durable changes since the interruption")
	}
	if signals.RecentlyCompleted {
		instructions = append(instructions, "close with verified outcomes and unresolved boundaries")
	}
	return strings.Join(instructions, "; ")
}

func stage(signals Signals) Stage {
	switch {
	case signals.Interrupted:
		return StageInterrupted
	case signals.RecentlyCompleted:
		return StageClosure
	case signals.RecentlyReturned:
		return StageReturned
	case signals.HasDeadline && signals.DeadlineProximity <= 2*time.Hour:
		return StageDeadline
	case signals.TaskActive && signals.TaskDuration <= 10*time.Minute:
		return StageEarly
	case signals.TaskActive && signals.TaskDuration > 10*time.Minute &&
		signals.TaskDuration < 45*time.Minute:
		return StageMiddle
	case signals.TaskActive && signals.TaskDuration >= 45*time.Minute:
		return StageExtended
	default:
		return StageIdle
	}
}

func invalidMarker(marker time.Time, sessionStarted time.Time, now time.Time) bool {
	return !marker.IsZero() &&
		(marker.Before(sessionStarted) || marker.After(now))
}

func nonnegative(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}
