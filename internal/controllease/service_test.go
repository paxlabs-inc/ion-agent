package controllease

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func TestDurableLeaseCoordinatesExecutorOperatorExpiryAndRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &testClock{now: time.Date(2026, time.July, 24, 8, 0, 0, 0, time.UTC)}
	cipher, err := vault.New(bytes.Repeat([]byte{0x54}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(
		ctx, filepath.Join(t.TempDir(), "sessions.db"), cipher, clock, 128<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close(ctx)
		_ = cipher.Close()
	})
	service, err := New(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	actorID, sessionID, turnID, taskID, toolID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	target := Target{
		ActorID: actorID, SessionID: &sessionID,
		Kind: ResourceBrowser, ResourceID: sessionID.String(),
	}
	owner := Owner{
		TurnID: &turnID, TaskID: &taskID, AgentID: "researcher",
		ToolEventID: &toolID, Action: "browser_interact", Revision: 41,
	}
	lease, err := service.Acquire(ctx, target, owner, 0, MinimumLeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Authority != AuthorityOperator || lease.State != StateActive ||
		lease.Revision != 1 {
		t.Fatalf("acquired lease = %+v", lease)
	}
	if _, err := service.BeginAutomation(ctx, target); !errors.Is(err, ErrHeld) {
		t.Fatalf("automation during takeover error = %v", err)
	}
	releaseOperator, _, err := service.BeginOperator(
		ctx, target, lease.ID, lease.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	releaseOperator()
	if _, _, err := service.BeginOperator(
		ctx, target, lease.ID, lease.Revision+1,
	); !errors.Is(err, ErrStale) {
		t.Fatalf("stale operator revision error = %v", err)
	}
	restarted, err := New(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.Status(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ID != lease.ID || recovered.Authority != AuthorityOperator {
		t.Fatalf("recovered lease = %+v", recovered)
	}
	clock.Advance(MinimumLeaseTTL + time.Second)
	releaseAutomation, err := restarted.BeginAutomation(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	releaseAutomation()
	expired, err := restarted.Status(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if expired.State != StateExpired || expired.Authority != AuthorityExecutor ||
		expired.Revision != lease.Revision+1 ||
		expired.Reconciliation != "lease_expired_executor_resumed" {
		t.Fatalf("expired lease = %+v", expired)
	}
}

func TestAcquireWaitsForTheCurrentExecutorActionBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &testClock{now: time.Date(2026, time.July, 24, 9, 0, 0, 0, time.UTC)}
	cipher, err := vault.New(bytes.Repeat([]byte{0x55}, vault.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(
		ctx, filepath.Join(t.TempDir(), "sessions.db"), cipher, clock, 128<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close(ctx)
		_ = cipher.Close()
	})
	service, err := New(store, clock)
	if err != nil {
		t.Fatal(err)
	}
	actorID, turnID := uuid.New(), uuid.New()
	target := Target{
		ActorID: actorID, Kind: ResourceTerminal, ResourceID: uuid.NewString(),
	}
	owner := Owner{
		TurnID: &turnID, TaskID: &turnID, AgentID: "ion",
		Action: "project.process.start", Revision: 1,
	}
	releaseAction, err := service.BeginAutomation(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan Lease, 1)
	failed := make(chan error, 1)
	go func() {
		lease, acquireErr := service.Acquire(
			ctx, target, owner, 0, MinimumLeaseTTL,
		)
		if acquireErr != nil {
			failed <- acquireErr
			return
		}
		acquired <- lease
	}()
	select {
	case lease := <-acquired:
		t.Fatalf("lease acquired before executor action ended: %+v", lease)
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseAction()
	select {
	case lease := <-acquired:
		if lease.Reconciliation != "executor_paused_at_action_boundary" {
			t.Fatalf("lease = %+v", lease)
		}
		if err := service.ReconcileStopped(
			ctx, target, "daemon_restart_terminal_stopped",
		); err != nil {
			t.Fatal(err)
		}
		reconciled, err := service.Status(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		if reconciled.State != StateReleased ||
			reconciled.Authority != AuthorityExecutor ||
			reconciled.Reconciliation != "daemon_restart_terminal_stopped" {
			t.Fatalf("stopped executor reconciliation = %+v", reconciled)
		}
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("lease did not acquire after executor action boundary")
	}
}
