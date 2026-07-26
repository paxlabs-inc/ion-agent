package relationship

import (
	"testing"
	"time"
)

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time { return clock.now }

func TestDurableStatePreservesDailyTrustLimitAndDeclarations(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)}
	model, err := New(clock)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		if _, err := model.ObserveAuthoritative(
			"user", "software", 1, Expert, "concise", 0,
			index == 0, index == 0,
		); err != nil {
			t.Fatal(err)
		}
	}
	state, ok := model.State("user", "software")
	if !ok || state.DailyChange != MaxTrustPerDay ||
		!state.ExpertiseDeclared || !state.PreferenceDeclared {
		t.Fatalf("durable state = %+v", state)
	}
	restored, err := Restore(clock, []State{state})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restored.ObserveAuthoritative(
		"user", "software", 1, Beginner, "detailed", 0,
		false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Trust != .7 || snapshot.Expertise != Expert ||
		snapshot.CommunicationPreference != "concise" {
		t.Fatalf("restart weakened limit or declaration authority: %+v", snapshot)
	}
}

func TestReviewableProfileCorrectPinRemoveAndRestore(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)}
	model, err := New(clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Prepare(
		"user", "software", Intermediate, "", false, false,
	); err != nil {
		t.Fatal(err)
	}
	responseLength, directness := "brief", "direct"
	conclusionFirst, proactive := true, false
	expertise := Expert
	tools := []string{"rg", "go test", "rg"}
	risk, cadence := "low", "milestones"
	dislikes := []string{"hidden work"}
	constraints := []string{"preserve dirty trees"}
	principles := []string{"prove production behavior"}
	snapshot, err := model.CorrectProfile("user", "software", ProfilePatch{
		ResponseLength: &responseLength, Directness: &directness,
		ConclusionFirst: &conclusionFirst, DomainExpertise: &expertise,
		PreferredTools: &tools, RiskTolerance: &risk,
		ProactiveSuggestions: &proactive, NotificationCadence: &cadence,
		Dislikes: &dislikes, Constraints: &constraints,
		ProjectPrinciples: &principles,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Expertise != Expert ||
		snapshot.CommunicationPreference != "brief" ||
		len(snapshot.Profile.PreferredTools) != 2 ||
		snapshot.Profile.ConclusionFirst == nil ||
		!*snapshot.Profile.ConclusionFirst {
		t.Fatalf("corrected profile = %+v", snapshot)
	}
	snapshot, err = model.PinProfileFields(
		"user", "software",
		[]string{"project_principles", "response_length"}, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profile.PinnedFields) != 2 {
		t.Fatalf("pinned profile = %+v", snapshot.Profile)
	}
	if _, err := model.ObserveAuthoritative(
		"user", "software", 0, Beginner, "detailed", 0,
		false, false,
	); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = model.Snapshot("user", "software")
	if snapshot.Expertise != Expert ||
		snapshot.CommunicationPreference != "brief" {
		t.Fatalf("inference replaced explicit profile = %+v", snapshot)
	}
	state, _ := model.State("user", "software")
	restored, err := Restore(clock, []State{state})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = restored.RemoveProfileFields(
		"user", "software", []string{"response_length"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profile.ResponseLength != "" ||
		snapshot.CommunicationPreference != "" ||
		len(snapshot.Profile.PinnedFields) != 1 {
		t.Fatalf("removed profile field = %+v", snapshot)
	}
}
