package memoryguard

import (
	"errors"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/memory"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type eventSink struct{ events []Event }

func (sink *eventSink) ProtectedMemoryTargeted(event Event) {
	sink.events = append(sink.events, event)
}

func Test_Guard_DreamweaverCannotModifyBindingMemoryTypes(t *testing.T) {
	sink := &eventSink{}
	guard, err := New(fixedClock{time.Unix(10, 0)}, sink)
	if err != nil {
		t.Fatal(err)
	}
	protected := []memory.Type{
		memory.Identity,
		memory.Preference,
		memory.Constraint,
	}
	for _, memoryType := range protected {
		if err := guard.AuthorizeDreamweaverMutation(
			"memory-1",
			memoryType,
			"reorganize",
		); !errors.Is(err, ErrProtectedType) {
			t.Fatalf("%s mutation error = %v", memoryType, err)
		}
	}
	if len(sink.events) != len(protected) {
		t.Fatalf("events = %+v", sink.events)
	}
	for _, memoryType := range []memory.Type{
		memory.Fact,
		memory.Belief,
		memory.Event,
		memory.Goal,
		memory.Capability,
		memory.Pattern,
	} {
		if err := guard.AuthorizeDreamweaverMutation(
			"memory-2",
			memoryType,
			"reorganize",
		); err != nil {
			t.Fatalf("%s mutation blocked: %v", memoryType, err)
		}
	}
}

func Test_Guard_RejectsInvalidTypesAndConfiguration(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("nil clock accepted")
	}
	guard, _ := New(fixedClock{}, nil)
	if err := guard.AuthorizeDreamweaverMutation(
		"memory",
		memory.Type("invented"),
		"reorganize",
	); err == nil {
		t.Fatal("invalid memory type accepted")
	}
}
