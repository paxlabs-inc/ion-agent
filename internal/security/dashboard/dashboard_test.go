package dashboard

import (
	"encoding/json"
	"testing"
	"time"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

var baseTime = time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

func Test_Dashboard_PublishAndSubscribe(t *testing.T) {
	clock := &testClock{baseTime}
	d, err := New(clock, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sub := d.Subscribe(EventSafetyViolation, EventCanaryAccess)
	defer d.Unsubscribe(sub.ID)

	d.Publish(EventSafetyViolation, SeverityCritical, "test", "violation detected", nil)

	select {
	case event := <-sub.Events:
		if event.Type != EventSafetyViolation {
			t.Fatalf("expected safety_violation, got %s", event.Type)
		}
		if event.Severity != SeverityCritical {
			t.Fatalf("expected critical, got %s", event.Severity)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func Test_Dashboard_HistoryAndSubscribersCannotMutateStoredEvents(t *testing.T) {
	clock := &testClock{baseTime}
	dashboard, _ := New(clock, 100)
	subscription := dashboard.Subscribe(EventToolExecution)
	defer dashboard.Unsubscribe(subscription.ID)
	subscription.Types[EventSafetyViolation] = true

	published := dashboard.Publish(
		EventToolExecution,
		SeverityInfo,
		"test",
		"immutable",
		map[string]bool{"ok": true},
	)
	published.Data[2] = 'X'
	received := <-subscription.Events
	received.Data[2] = 'Y'
	history := dashboard.History(1, "")
	if !json.Valid(history[0].Data) || string(history[0].Data) != `{"ok":true}` {
		t.Fatalf("stored event data mutated: %s", history[0].Data)
	}
	history[0].Data[2] = 'Z'
	again := dashboard.History(1, "")
	if string(again[0].Data) != `{"ok":true}` {
		t.Fatalf("history exposed stored data: %s", again[0].Data)
	}

	dashboard.Publish(
		EventSafetyViolation,
		SeverityCritical,
		"test",
		"not subscribed",
		nil,
	)
	select {
	case event := <-subscription.Events:
		t.Fatalf("public filter mutation changed subscription: %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func Test_Dashboard_SubscriberFiltering(t *testing.T) {
	clock := &testClock{baseTime}
	d, _ := New(clock, 100)

	// Subscribe only to canary events.
	sub := d.Subscribe(EventCanaryAccess)
	defer d.Unsubscribe(sub.ID)

	// Publish a safety violation (should not be received).
	d.Publish(EventSafetyViolation, SeverityInfo, "test", "test", nil)

	select {
	case <-sub.Events:
		t.Fatal("subscriber should not receive non-matching event type")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

func Test_Dashboard_AllTypesSubscriber(t *testing.T) {
	clock := &testClock{baseTime}
	d, _ := New(clock, 100)

	// Subscribe to all types (no filter).
	sub := d.Subscribe()
	defer d.Unsubscribe(sub.ID)

	d.Publish(EventSafetyViolation, SeverityInfo, "test", "test", nil)
	d.Publish(EventCanaryAccess, SeverityWarning, "test", "test", nil)

	// Should receive both.
	<-sub.Events
	<-sub.Events
}

func Test_Dashboard_History(t *testing.T) {
	clock := &testClock{baseTime}
	d, _ := New(clock, 100)

	d.Publish(EventSafetyViolation, SeverityInfo, "s1", "msg1", nil)
	d.Publish(EventCanaryAccess, SeverityWarning, "s2", "msg2", nil)
	d.Publish(EventSafetyViolation, SeverityCritical, "s3", "msg3", nil)

	// All events.
	all := d.History(0, "")
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}

	// Filter by type.
	violations := d.History(0, EventSafetyViolation)
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d", len(violations))
	}

	// Limit.
	limited := d.History(1, "")
	if len(limited) != 1 {
		t.Fatalf("expected 1 event, got %d", len(limited))
	}
}

func Test_Dashboard_SubscriberCount(t *testing.T) {
	clock := &testClock{baseTime}
	d, _ := New(clock, 100)

	if d.SubscriberCount() != 0 {
		t.Fatal("expected 0 subscribers initially")
	}

	sub1 := d.Subscribe()
	sub2 := d.Subscribe()
	if d.SubscriberCount() != 2 {
		t.Fatal("expected 2 subscribers")
	}

	d.Unsubscribe(sub1.ID)
	if d.SubscriberCount() != 1 {
		t.Fatal("expected 1 subscriber after unsubscribe")
	}

	d.Unsubscribe(sub2.ID)
	if d.SubscriberCount() != 0 {
		t.Fatal("expected 0 subscribers after all unsubscribed")
	}
}

func Test_Dashboard_NilClockRejected(t *testing.T) {
	_, err := New(nil, 100)
	if err == nil {
		t.Fatal("expected error for nil clock")
	}
}

func Test_Dashboard_EventData(t *testing.T) {
	clock := &testClock{baseTime}
	d, _ := New(clock, 100)

	data := map[string]interface{}{"key": "value"}
	published := d.Publish(EventToolExecution, SeverityInfo, "test", "test data", data)
	if published.Data == nil {
		t.Fatal("expected non-nil data")
	}
}

func Test_Dashboard_DefaultMaxHistory(t *testing.T) {
	clock := &testClock{baseTime}
	d, _ := New(clock, 0) // should default to 1000

	for i := 0; i < 1100; i++ {
		d.Publish(EventToolExecution, SeverityInfo, "test", "test", nil)
	}

	history := d.History(0, "")
	if len(history) > 1000 {
		t.Fatalf("history should be capped at 1000, got %d", len(history))
	}
}
