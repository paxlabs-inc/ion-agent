package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type journalClock struct{ at time.Time }

func (clock journalClock) Now() time.Time { return clock.at }

func TestJournalDurableRetentionReplayAndAcknowledgement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controlplane.db")
	clock := journalClock{at: time.Unix(1_000, 0).UTC()}
	actorID := uuid.New()
	journal, err := OpenJournal(ctx, path, clock, JournalConfig{
		Retention: 3, SubscriberBuffer: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	appended := make([]Event, 0, 5)
	for index := 0; index < 5; index++ {
		event := newJournalEvent(t, actorID, index)
		stored, appendErr := journal.Append(ctx, event)
		if appendErr != nil {
			t.Fatal(appendErr)
		}
		appended = append(appended, stored)
	}
	if appended[0].Sequence != 1 || appended[4].Sequence != 5 {
		t.Fatalf("assigned sequences = %d..%d", appended[0].Sequence, appended[4].Sequence)
	}

	gap, err := journal.Replay(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !gap.Gap || gap.Earliest != 3 || gap.Latest != 5 || len(gap.Events) != 0 {
		t.Fatalf("retention gap = %+v", gap)
	}
	replay, err := journal.Replay(ctx, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Gap || len(replay.Events) != 3 {
		t.Fatalf("retained replay = %+v", replay)
	}
	for index, event := range replay.Events {
		if event.Sequence != uint64(index+3) {
			t.Fatalf("replay sequence[%d] = %d", index, event.Sequence)
		}
	}
	if _, err := journal.Replay(ctx, 6, 100); !errors.Is(err, ErrCursorAhead) {
		t.Fatalf("future cursor error = %v", err)
	}
	if err := journal.Acknowledge(ctx, actorID, "web-one", 4); err != nil {
		t.Fatal(err)
	}
	if err := journal.Acknowledge(ctx, actorID, "web-one", 3); err != nil {
		t.Fatal(err)
	}
	acknowledged, err := journal.Acknowledged(ctx, actorID, "web-one")
	if err != nil || acknowledged != 4 {
		t.Fatalf("acknowledgement = %d, %v", acknowledged, err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenJournal(ctx, path, clock, JournalConfig{
		Retention: 3, SubscriberBuffer: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.Append(ctx, newJournalEvent(t, actorID, 5))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Sequence != 6 {
		t.Fatalf("sequence after restart = %d, want 6", stored.Sequence)
	}
	acknowledged, err = reopened.Acknowledged(ctx, actorID, "web-one")
	if err != nil || acknowledged != 4 {
		t.Fatalf("durable acknowledgement = %d, %v", acknowledged, err)
	}
}

func TestJournalSubscriptionIsAtomicAndSlowClientsCannotBlock(t *testing.T) {
	ctx := context.Background()
	actorID := uuid.New()
	journal, err := OpenJournal(ctx, ":memory:", journalClock{
		at: time.Unix(2_000, 0).UTC(),
	}, JournalConfig{Retention: 10, SubscriberBuffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	first, err := journal.Append(ctx, newJournalEvent(t, actorID, 0))
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := journal.Subscribe(ctx, first.Sequence, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if len(subscription.Replay.Events) != 0 || subscription.Replay.Latest != 1 {
		t.Fatalf("atomic replay = %+v", subscription.Replay)
	}

	start := time.Now()
	second, err := journal.Append(ctx, newJournalEvent(t, actorID, 1))
	if err != nil {
		t.Fatal(err)
	}
	third, err := journal.Append(ctx, newJournalEvent(t, actorID, 2))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("slow subscriber blocked producer for %s", elapsed)
	}
	event, open := <-subscription.Live
	if !open || event.Sequence != second.Sequence {
		t.Fatalf("buffered live event = %+v, open=%t", event, open)
	}
	if _, open := <-subscription.Live; open {
		t.Fatal("overflowed subscription remained open")
	}
	recovered, err := journal.Replay(ctx, second.Sequence, 10)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Gap || len(recovered.Events) != 1 ||
		recovered.Events[0].Sequence != third.Sequence {
		t.Fatalf("recovery replay = %+v", recovered)
	}
}

func TestJournalReconnectAtEveryBoundaryHasNoLossOrDuplication(t *testing.T) {
	ctx := context.Background()
	actorID := uuid.New()
	journal, err := OpenJournal(ctx, ":memory:", journalClock{
		at: time.Unix(3_000, 0).UTC(),
	}, JournalConfig{Retention: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for index := 0; index < 12; index++ {
		if _, err := journal.Append(ctx, newJournalEvent(t, actorID, index)); err != nil {
			t.Fatal(err)
		}
	}
	for boundary := uint64(0); boundary <= 12; boundary++ {
		replay, replayErr := journal.Replay(ctx, boundary, 64)
		if replayErr != nil {
			t.Fatalf("boundary %d: %v", boundary, replayErr)
		}
		if replay.Gap || len(replay.Events) != 12-int(boundary) {
			t.Fatalf("boundary %d replay = %+v", boundary, replay)
		}
		for offset, event := range replay.Events {
			want := boundary + uint64(offset) + 1
			if event.Sequence != want {
				t.Fatalf("boundary %d event %d = %d, want %d",
					boundary, offset, event.Sequence, want)
			}
		}
	}
}

func TestJournalActorReplayPagesDoNotJumpCursorOrDuplicate(t *testing.T) {
	ctx := context.Background()
	actorID := uuid.New()
	journal, err := OpenJournal(ctx, ":memory:", journalClock{
		at: time.Unix(4_000, 0).UTC(),
	}, JournalConfig{Retention: 5_000, SubscriberBuffer: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()

	for index := 0; index < maxReplayEvents+501; index++ {
		if _, err := journal.Append(ctx, newJournalEvent(t, actorID, index)); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 10; index++ {
		if _, err := journal.Append(ctx, newJournalEvent(t, uuid.New(), 10_000+index)); err != nil {
			t.Fatal(err)
		}
	}

	subscription, err := journal.SubscribeActor(ctx, actorID, 0, maxReplayEvents)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	first := subscription.Replay
	if first.Gap || len(first.Events) != maxReplayEvents ||
		first.Latest != maxReplayEvents || first.Head != maxReplayEvents+511 {
		t.Fatalf("first replay page = events:%d latest:%d head:%d gap:%t",
			len(first.Events), first.Latest, first.Head, first.Gap)
	}
	second, err := journal.replayActorThrough(
		ctx, actorID, first.Latest, maxReplayEvents, first.Head,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Gap || len(second.Events) != 501 || second.Latest != first.Head ||
		second.Head != first.Head {
		t.Fatalf("second replay page = events:%d latest:%d head:%d gap:%t",
			len(second.Events), second.Latest, second.Head, second.Gap)
	}
	seen := make(map[uint64]struct{}, maxReplayEvents+501)
	for _, page := range []Replay{first, second} {
		for _, event := range page.Events {
			if _, duplicate := seen[event.Sequence]; duplicate {
				t.Fatalf("duplicate sequence %d", event.Sequence)
			}
			seen[event.Sequence] = struct{}{}
		}
	}
	if len(seen) != maxReplayEvents+501 {
		t.Fatalf("delivered %d actor events", len(seen))
	}
}

func newJournalEvent(t *testing.T, actorID uuid.UUID, index int) Event {
	t.Helper()
	payload, err := json.Marshal(map[string]int{"index": index})
	if err != nil {
		t.Fatal(err)
	}
	event, err := NewEvent(
		EventTurnDelta,
		Correlation{ActorID: actorID},
		payload,
		time.Unix(10_000+int64(index), int64(index)).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
