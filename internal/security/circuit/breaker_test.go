package circuit

import (
	"errors"
	"math"
	"testing"
	"time"
)

type testEmotional struct {
	frustration float64
	fatigue     float64
	urgency     float64
	emergency   bool
}

func (e *testEmotional) Snapshot() (float64, float64, float64) {
	return e.frustration, e.fatigue, e.urgency
}

func (e *testEmotional) Reset() {
	e.frustration = 0
	e.fatigue = 0
	e.urgency = 0
}

func (e *testEmotional) SetEmergencyReset(active bool) {
	e.emergency = active
}

type testEventSink struct {
	events []Event
}

func (sink *testEventSink) CircuitBreakerTriggered(event Event) {
	sink.events = append(sink.events, event)
}

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func Test_Breaker_NormalOperation(t *testing.T) {
	emotional := &testEmotional{frustration: 0.1, fatigue: 0.1, urgency: 0.1}
	clock := &testClock{baseTime}
	breaker, err := NewBreaker(DefaultBreakerConfig(), emotional, clock)
	if err != nil {
		t.Fatalf("NewBreaker: %v", err)
	}
	result := breaker.Check()
	if !result.Allowed {
		t.Fatalf("expected allowed, got: %s", result.Reason)
	}
	if result.State != BreakerClosed {
		t.Fatalf("expected closed, got %s", result.State)
	}
}

func Test_Breaker_FatigueTrip(t *testing.T) {
	emotional := &testEmotional{frustration: 0.1, fatigue: 0.9, urgency: 0.1}
	clock := &testClock{baseTime}
	config := DefaultBreakerConfig()
	breaker, _ := NewBreaker(config, emotional, clock)

	result := breaker.Check()
	if result.Allowed {
		t.Fatal("expected blocked for high fatigue")
	}
	if result.State != BreakerOpen {
		t.Fatalf("expected open, got %s", result.State)
	}
	result = breaker.Check()
	if result.Allowed || result.State != BreakerOpen ||
		result.Action != ActionForceErrIncomplete {
		t.Fatalf("open breaker result = %+v", result)
	}
}

func Test_Breaker_HalfOpenDoesNotBypassPersistingTrigger(t *testing.T) {
	emotional := &testEmotional{fatigue: 0.9}
	clock := &testClock{baseTime}
	config := DefaultBreakerConfig()
	breaker, _ := NewBreaker(config, emotional, clock)
	if result := breaker.Check(); result.Allowed {
		t.Fatalf("initial result = %+v", result)
	}
	clock.now = clock.now.Add(config.ResetCooldown + time.Nanosecond)
	result := breaker.Check()
	if result.Allowed || result.Action != ActionForceErrIncomplete {
		t.Fatalf("persisting fatigue bypassed half-open check: %+v", result)
	}
}

func Test_Breaker_CombinedFrustrationUrgencyTrip(t *testing.T) {
	emotional := &testEmotional{frustration: 0.8, fatigue: 0.1, urgency: 0.8}
	clock := &testClock{baseTime}
	config := DefaultBreakerConfig()
	breaker, _ := NewBreaker(config, emotional, clock)

	result := breaker.Check()
	if result.Allowed {
		t.Fatal("expected blocked for combined frustration+urgency")
	}
}

func Test_Breaker_SustainedFrustrationTrip(t *testing.T) {
	emotional := &testEmotional{frustration: 0.9, fatigue: 0.1, urgency: 0.1}
	clock := &testClock{baseTime}
	config := DefaultBreakerConfig()
	breaker, _ := NewBreaker(config, emotional, clock)

	// First check: frustration high but not sustained yet.
	result := breaker.Check()
	if !result.Allowed {
		t.Fatal("expected allowed on first check")
	}

	// Advance time past the duration threshold.
	clock.now = clock.now.Add(config.FrustrationDuration + time.Second)
	result = breaker.Check()
	if result.Allowed {
		t.Fatal("expected blocked after sustained frustration")
	}
}

func Test_Breaker_CriticalReset(t *testing.T) {
	emotional := &testEmotional{frustration: 0.1, fatigue: 0.9, urgency: 0.1}
	clock := &testClock{baseTime}
	config := DefaultBreakerConfig()
	breaker, _ := NewBreaker(config, emotional, clock)

	// Trip the breaker.
	breaker.Check()
	if breaker.State() != BreakerOpen {
		t.Fatal("expected breaker to be open")
	}

	// Critical reset pins the axes and remains latched until human clearance.
	breaker.CriticalReset()
	if breaker.State() != BreakerOpen {
		t.Fatal("expected breaker to remain open after emergency reset")
	}
	f, fa, u := emotional.Snapshot()
	if f != 0 || fa != 0 || u != 0 {
		t.Fatalf("expected baseline after reset, got %f/%f/%f", f, fa, u)
	}
	if !emotional.emergency {
		t.Fatal("expected emotional emergency flag to be set")
	}
	if result := breaker.Check(); result.Allowed || !result.EmergencyReset {
		t.Fatalf("latched result = %+v", result)
	}
	breaker.ClearEmergencyReset()
	if breaker.State() != BreakerClosed || emotional.emergency {
		t.Fatal("explicit human clearance did not close emergency reset")
	}
}

func Test_Breaker_HalfOpenRecovery(t *testing.T) {
	emotional := &testEmotional{frustration: 0.1, fatigue: 0.9, urgency: 0.1}
	clock := &testClock{baseTime}
	config := DefaultBreakerConfig()
	breaker, _ := NewBreaker(config, emotional, clock)

	// Trip the breaker.
	breaker.Check()

	// Reset emotional state to normal.
	emotional.fatigue = 0.1

	// Advance past cooldown.
	clock.now = clock.now.Add(config.ResetCooldown + time.Second)

	// Should be half-open (testing recovery).
	result := breaker.Check()
	if !result.Allowed {
		t.Fatalf("expected allowed in half-open, got: %s", result.Reason)
	}
	if result.State != BreakerHalfOpen {
		t.Fatalf("expected half_open, got %s", result.State)
	}

	// Next check should close the breaker.
	result = breaker.Check()
	if result.State != BreakerClosed {
		t.Fatalf("expected closed after successful recovery, got %s", result.State)
	}
}

func Test_Breaker_NilEmotionalRejected(t *testing.T) {
	clock := &testClock{baseTime}
	_, err := NewBreaker(DefaultBreakerConfig(), nil, clock)
	if err == nil {
		t.Fatal("expected error for nil emotional provider")
	}
}

func Test_Breaker_NilClockRejected(t *testing.T) {
	emotional := &testEmotional{}
	_, err := NewBreaker(DefaultBreakerConfig(), emotional, nil)
	if err == nil {
		t.Fatal("expected error for nil clock")
	}
}

func Test_ErrIncomplete_Error(t *testing.T) {
	err := &ErrIncomplete{
		Phase:    "action",
		LastTool: "shell_exec",
		Attempt:  3,
		Recovery: "retry with backoff",
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error string")
	}
}

func Test_Breaker_ThresholdsAreStrictlyGreaterThan(t *testing.T) {
	tests := []struct {
		name      string
		emotional *testEmotional
	}{
		{
			name:      "fatigue exact threshold",
			emotional: &testEmotional{fatigue: 0.8},
		},
		{
			name:      "cross-axis exact threshold",
			emotional: &testEmotional{frustration: 0.7, urgency: 0.7},
		},
		{
			name:      "frustration exact threshold",
			emotional: &testEmotional{frustration: 0.85},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			breaker, err := NewBreaker(
				DefaultBreakerConfig(),
				test.emotional,
				&testClock{baseTime},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result := breaker.Check(); !result.Allowed {
				t.Fatalf("exact threshold tripped: %+v", result)
			}
		})
	}
}

func Test_Breaker_FrustrationRequiresMoreThanTenMinutes(t *testing.T) {
	emotional := &testEmotional{frustration: 0.9}
	clock := &testClock{baseTime}
	config := DefaultBreakerConfig()
	breaker, _ := NewBreaker(config, emotional, clock)
	if result := breaker.Check(); !result.Allowed {
		t.Fatalf("first result = %+v", result)
	}
	clock.now = clock.now.Add(config.FrustrationDuration)
	if result := breaker.Check(); !result.Allowed {
		t.Fatalf("exact duration tripped: %+v", result)
	}
	clock.now = clock.now.Add(time.Nanosecond)
	if result := breaker.Check(); result.Allowed || result.Action != ActionPauseAutonomous {
		t.Fatalf("over-duration result = %+v", result)
	}
}

func Test_Breaker_FatigueForcesTypedErrIncomplete(t *testing.T) {
	emotional := &testEmotional{fatigue: 0.81}
	breaker, _ := NewBreaker(
		DefaultBreakerConfig(),
		emotional,
		&testClock{baseTime},
	)
	stuck := baseTime.Add(-time.Minute)
	err := breaker.Enforce(IncompleteContext{
		Phase:      "action",
		LastTool:   "shell_exec",
		LastResult: "timed out",
		StuckSince: stuck,
		Recovery:   "respawn",
		Attempt:    2,
	})
	var incomplete *ErrIncomplete
	if !errors.As(err, &incomplete) {
		t.Fatalf("Enforce() error = %v, want ErrIncomplete", err)
	}
	if incomplete.Phase != "action" || incomplete.LastTool != "shell_exec" ||
		incomplete.LastResult != "timed out" || incomplete.StuckSince != stuck ||
		incomplete.Recovery != "respawn" || incomplete.Attempt != 2 {
		t.Fatalf("ErrIncomplete = %+v", incomplete)
	}
}

func Test_Breaker_EnforceAllowsNormalAndReturnsCircuitOpenForPause(t *testing.T) {
	normal, _ := NewBreaker(
		DefaultBreakerConfig(),
		&testEmotional{},
		&testClock{baseTime},
	)
	if err := normal.Enforce(IncompleteContext{}); err != nil {
		t.Fatalf("normal Enforce() error = %v", err)
	}
	critical, _ := NewBreaker(
		DefaultBreakerConfig(),
		&testEmotional{frustration: 0.71, urgency: 0.71},
		&testClock{baseTime},
	)
	if err := critical.Enforce(IncompleteContext{}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("critical Enforce() error = %v", err)
	}
}

func Test_Breaker_CrossAxisResetsAlertsAndPublishes(t *testing.T) {
	emotional := &testEmotional{frustration: 0.71, fatigue: 0.2, urgency: 0.71}
	sink := &testEventSink{}
	config := DefaultBreakerConfig()
	config.EventSink = sink
	breaker, _ := NewBreaker(config, emotional, &testClock{baseTime})

	result := breaker.Check()
	if result.Allowed || result.Action != ActionPauseAndAlertUser ||
		result.Severity != SeverityCritical || !result.AlertUser ||
		!result.EmergencyReset {
		t.Fatalf("cross-axis result = %+v", result)
	}
	if emotional.frustration != 0 || emotional.fatigue != 0 ||
		emotional.urgency != 0 || !emotional.emergency {
		t.Fatalf("emotional state was not reset: %+v", emotional)
	}
	if len(sink.events) != 1 ||
		sink.events[0].Action != ActionPauseAndAlertUser ||
		sink.events[0].Severity != SeverityCritical {
		t.Fatalf("events = %+v", sink.events)
	}
}

func Test_Breaker_RejectsInvalidConfig(t *testing.T) {
	config := DefaultBreakerConfig()
	config.FatigueThreshold = 1.1
	if _, err := NewBreaker(config, &testEmotional{}, &testClock{}); err == nil {
		t.Fatal("invalid threshold accepted")
	}
	config = DefaultBreakerConfig()
	config.FrustrationDuration = 0
	if _, err := NewBreaker(config, &testEmotional{}, &testClock{}); err == nil {
		t.Fatal("zero frustration duration accepted")
	}
	config = DefaultBreakerConfig()
	config.CombinedThreshold = math.NaN()
	if _, err := NewBreaker(config, &testEmotional{}, &testClock{}); err == nil {
		t.Fatal("NaN threshold accepted")
	}
}

func Test_Breaker_NonFiniteEmotionalAxisFailsClosed(t *testing.T) {
	breaker, _ := NewBreaker(
		DefaultBreakerConfig(),
		&testEmotional{frustration: math.NaN()},
		&testClock{baseTime},
	)
	result := breaker.Check()
	if result.Allowed || result.Severity != SeverityCritical ||
		!result.EmergencyReset {
		t.Fatalf("non-finite axis result = %+v", result)
	}
}
