// Package circuit implements emotional-state circuit breakers that pause
// autonomous work without weakening the safety classification pipeline.
package circuit

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// BreakerState is the current state of a circuit breaker.
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// Action is the mandatory response to a tripped circuit.
type Action string

const (
	ActionNone               Action = ""
	ActionPauseAutonomous    Action = "pause_autonomous"
	ActionForceErrIncomplete Action = "force_err_incomplete"
	ActionPauseAndAlertUser  Action = "pause_and_alert_user"
)

// Severity describes the urgency of a circuit event.
type Severity string

const (
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

var ErrCircuitOpen = errors.New("circuit: autonomous operations paused")

// EmotionalProvider gives the circuit breaker a read-only snapshot and the
// ability to pin axes to their baselines after a critical event.
type EmotionalProvider interface {
	Snapshot() (frustration, fatigue, urgency float64)
	Reset()
}

type emergencyStateProvider interface {
	SetEmergencyReset(bool)
}

// Event is the complete user-visible record of a circuit breaker trigger.
type Event struct {
	At       time.Time `json:"at"`
	Action   Action    `json:"action"`
	Severity Severity  `json:"severity"`
	Reason   string    `json:"reason"`
}

// EventSink receives circuit events for durable audit and dashboard display.
type EventSink interface {
	CircuitBreakerTriggered(Event)
}

// BreakerResult is the outcome of a circuit breaker check.
type BreakerResult struct {
	Allowed        bool         `json:"allowed"`
	State          BreakerState `json:"state"`
	Action         Action       `json:"action,omitempty"`
	Severity       Severity     `json:"severity,omitempty"`
	Reason         string       `json:"reason,omitempty"`
	AlertUser      bool         `json:"alert_user"`
	EmergencyReset bool         `json:"emergency_reset"`
}

// BreakerConfig configures the binding thresholds.
type BreakerConfig struct {
	FrustrationThreshold float64
	FrustrationDuration  time.Duration
	FatigueThreshold     float64
	CombinedThreshold    float64
	ResetCooldown        time.Duration
	EventSink            EventSink
}

// DefaultBreakerConfig returns the thresholds from STANDARD-SEC-040..043.
func DefaultBreakerConfig() BreakerConfig {
	return BreakerConfig{
		FrustrationThreshold: 0.85,
		FrustrationDuration:  10 * time.Minute,
		FatigueThreshold:     0.8,
		CombinedThreshold:    0.7,
		ResetCooldown:        5 * time.Minute,
	}
}

// Clock abstracts time for deterministic testing.
type Clock interface {
	Now() time.Time
}

// Breaker monitors emotional state and blocks unsafe autonomous continuation.
type Breaker struct {
	mu               sync.Mutex
	config           BreakerConfig
	emotional        EmotionalProvider
	clock            Clock
	state            BreakerState
	trippedAt        time.Time
	frustrationSince time.Time
	frustrationHigh  bool
	emergencyReset   bool
	emergencyReason  string
	lastAction       Action
	lastSeverity     Severity
	lastReason       string
}

// NewBreaker creates a circuit breaker with validated thresholds.
func NewBreaker(config BreakerConfig, emotional EmotionalProvider, clock Clock) (*Breaker, error) {
	if emotional == nil {
		return nil, fmt.Errorf("circuit: emotional provider required")
	}
	if clock == nil {
		return nil, fmt.Errorf("circuit: clock required")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &Breaker{
		config:    config,
		emotional: emotional,
		clock:     clock,
		state:     BreakerClosed,
	}, nil
}

// Check applies strict-greater-than thresholds exactly as specified.
func (b *Breaker) Check() BreakerResult {
	b.mu.Lock()
	frustration, fatigue, urgency := b.emotional.Snapshot()
	now := b.clock.Now()
	enteredHalfOpen := false

	if b.emergencyReset {
		result := BreakerResult{
			Allowed:        false,
			State:          BreakerOpen,
			Action:         ActionPauseAndAlertUser,
			Severity:       SeverityCritical,
			Reason:         b.emergencyReason,
			AlertUser:      true,
			EmergencyReset: true,
		}
		b.mu.Unlock()
		return result
	}
	if !finiteAxis(frustration) || !finiteAxis(fatigue) || !finiteAxis(urgency) {
		result, event := b.tripCritical(now, "emotional state contains a non-finite axis")
		b.mu.Unlock()
		b.notify(event)
		return result
	}

	if b.state == BreakerOpen {
		if now.Sub(b.trippedAt) <= b.config.ResetCooldown {
			result := BreakerResult{
				Allowed:  false,
				State:    BreakerOpen,
				Action:   b.lastAction,
				Severity: b.lastSeverity,
				Reason: fmt.Sprintf(
					"breaker open: %s (tripped at %s, cooldown %s)",
					b.lastReason,
					b.trippedAt.Format(time.RFC3339Nano),
					b.config.ResetCooldown,
				),
			}
			b.mu.Unlock()
			return result
		}
		b.state = BreakerHalfOpen
		enteredHalfOpen = true
	}

	if frustration > b.config.CombinedThreshold && urgency > b.config.CombinedThreshold {
		reason := fmt.Sprintf(
			"frustration %.2f and urgency %.2f exceed %.2f",
			frustration,
			urgency,
			b.config.CombinedThreshold,
		)
		result, event := b.tripCritical(now, reason)
		b.mu.Unlock()
		b.notify(event)
		return result
	}

	if fatigue > b.config.FatigueThreshold {
		reason := fmt.Sprintf(
			"fatigue %.2f exceeds threshold %.2f",
			fatigue,
			b.config.FatigueThreshold,
		)
		result, event := b.trip(now, ActionForceErrIncomplete, SeverityHigh, reason)
		b.mu.Unlock()
		b.notify(event)
		return result
	}

	if frustration > b.config.FrustrationThreshold {
		if !b.frustrationHigh {
			b.frustrationHigh = true
			b.frustrationSince = now
		} else if now.Sub(b.frustrationSince) > b.config.FrustrationDuration {
			reason := fmt.Sprintf(
				"frustration %.2f sustained for %s",
				frustration,
				now.Sub(b.frustrationSince),
			)
			result, event := b.trip(now, ActionPauseAutonomous, SeverityHigh, reason)
			b.mu.Unlock()
			b.notify(event)
			return result
		}
	} else {
		b.frustrationHigh = false
		b.frustrationSince = time.Time{}
	}

	if b.state == BreakerHalfOpen {
		if enteredHalfOpen {
			result := BreakerResult{
				Allowed: true,
				State:   BreakerHalfOpen,
				Reason:  "testing recovery",
			}
			b.mu.Unlock()
			return result
		}
		b.state = BreakerClosed
	}
	result := BreakerResult{Allowed: true, State: b.state}
	b.mu.Unlock()
	return result
}

// IncompleteContext supplies the honest-partial details required when fatigue
// forces ErrIncomplete.
type IncompleteContext struct {
	Phase      string
	LastTool   string
	LastResult string
	StuckSince time.Time
	Recovery   string
	Attempt    int
}

// Enforce converts the breaker decision into the required typed error.
func (b *Breaker) Enforce(context IncompleteContext) error {
	result := b.Check()
	if result.Allowed {
		return nil
	}
	if result.Action == ActionForceErrIncomplete {
		return &ErrIncomplete{
			Phase:      context.Phase,
			LastTool:   context.LastTool,
			LastResult: context.LastResult,
			StuckSince: context.StuckSince,
			Recovery:   context.Recovery,
			Attempt:    context.Attempt,
		}
	}
	return fmt.Errorf("%w: %s", ErrCircuitOpen, result.Reason)
}

// CriticalReset manually pins emotional axes to baseline and latches the
// emergency state. It does not clear the circuit; only ClearEmergencyReset can.
func (b *Breaker) CriticalReset() bool {
	b.mu.Lock()
	if !b.emergencyReset {
		b.emotional.Reset()
		if provider, ok := b.emotional.(emergencyStateProvider); ok {
			provider.SetEmergencyReset(true)
		}
		b.emergencyReset = true
		b.emergencyReason = "manual critical reset"
		b.state = BreakerOpen
		b.trippedAt = b.clock.Now()
	}
	b.mu.Unlock()
	return true
}

// ClearEmergencyReset is the explicit human-clearance operation required by
// STANDARD-SEC-043.
func (b *Breaker) ClearEmergencyReset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emergencyReset = false
	b.emergencyReason = ""
	b.state = BreakerClosed
	b.frustrationHigh = false
	b.frustrationSince = time.Time{}
	b.lastAction = ActionNone
	b.lastSeverity = ""
	b.lastReason = ""
	if provider, ok := b.emotional.(emergencyStateProvider); ok {
		provider.SetEmergencyReset(false)
	}
}

// State returns the current breaker state.
func (b *Breaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) trip(
	now time.Time,
	action Action,
	severity Severity,
	reason string,
) (BreakerResult, Event) {
	b.state = BreakerOpen
	b.trippedAt = now
	b.lastAction = action
	b.lastSeverity = severity
	b.lastReason = reason
	return BreakerResult{
		Allowed:  false,
		State:    BreakerOpen,
		Action:   action,
		Severity: severity,
		Reason:   reason,
	}, Event{At: now, Action: action, Severity: severity, Reason: reason}
}

func (b *Breaker) tripCritical(now time.Time, reason string) (BreakerResult, Event) {
	b.emotional.Reset()
	if provider, ok := b.emotional.(emergencyStateProvider); ok {
		provider.SetEmergencyReset(true)
	}
	b.emergencyReset = true
	b.emergencyReason = reason
	result, event := b.trip(
		now,
		ActionPauseAndAlertUser,
		SeverityCritical,
		reason,
	)
	result.AlertUser = true
	result.EmergencyReset = true
	return result, event
}

func (b *Breaker) notify(event Event) {
	if b.config.EventSink != nil {
		b.config.EventSink.CircuitBreakerTriggered(event)
	}
}

func validateConfig(config BreakerConfig) error {
	for name, value := range map[string]float64{
		"frustration threshold": config.FrustrationThreshold,
		"fatigue threshold":     config.FatigueThreshold,
		"combined threshold":    config.CombinedThreshold,
	} {
		if !finiteAxis(value) || value < 0 || value > 1 {
			return fmt.Errorf("circuit: %s must be within [0,1]", name)
		}
	}
	if config.FrustrationDuration <= 0 || config.ResetCooldown < 0 {
		return fmt.Errorf("circuit: invalid breaker durations")
	}
	return nil
}

func finiteAxis(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// ErrIncomplete is returned when fatigue prevents honest completion.
type ErrIncomplete struct {
	Phase      string    `json:"phase"`
	LastTool   string    `json:"last_tool"`
	LastResult string    `json:"last_result"`
	StuckSince time.Time `json:"stuck_since"`
	Recovery   string    `json:"recovery"`
	Attempt    int       `json:"attempt"`
}

func (e *ErrIncomplete) Error() string {
	return fmt.Sprintf(
		"incomplete: phase=%s tool=%s attempt=%d recovery=%s",
		e.Phase,
		e.LastTool,
		e.Attempt,
		e.Recovery,
	)
}
