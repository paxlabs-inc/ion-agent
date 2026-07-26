package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memoryStateStore struct {
	mu    sync.Mutex
	state map[string]json.RawMessage
}

func (store *memoryStateStore) SaveLivingState(
	_ context.Context,
	kind string,
	scope string,
	state json.RawMessage,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.state == nil {
		store.state = make(map[string]json.RawMessage)
	}
	store.state[kind+"\x00"+scope] = append(json.RawMessage(nil), state...)
	return nil
}

func (store *memoryStateStore) LoadLivingState(
	_ context.Context,
	kind string,
	scope string,
) (json.RawMessage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	raw, ok := store.state[kind+"\x00"+scope]
	if !ok {
		return nil, fmt.Errorf("load: %w", sql.ErrNoRows)
	}
	return append(json.RawMessage(nil), raw...), nil
}

type identityClock struct{ now time.Time }

func (clock *identityClock) Now() time.Time { return clock.now }

func TestServiceProposalApprovalRestartRollbackAndAttackRejection(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "identity")
	file, err := Bootstrap(root, "# SOUL\nprecise")
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStateStore{}
	clock := &identityClock{now: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)}
	service, err := NewService(ctx, file, store, clock)
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()
	proposal, err := service.Propose(ctx, actor, "# SOUL\nprecise and candid")
	if err != nil || proposal.Diff == "" {
		t.Fatalf("proposal = %+v, %v", proposal, err)
	}
	current, err := service.Current(ctx)
	if err != nil || current.Number != 1 {
		t.Fatalf("proposal changed current identity: %+v, %v", current, err)
	}
	denied, err := service.Propose(ctx, actor, "# SOUL\nunapproved candidate")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(ctx, actor, denied.ID, false); err != nil {
		t.Fatal(err)
	}
	current, err = service.Current(ctx)
	if err != nil || current.Number != 1 ||
		current.Content != "# SOUL\nprecise" {
		t.Fatalf("denied proposal changed identity: %+v, %v", current, err)
	}
	clock.now = clock.now.Add(time.Minute)
	approved, err := service.Resolve(ctx, actor, proposal.ID, true)
	if err != nil || approved.Number != 2 {
		t.Fatalf("approved version = %+v, %v", approved, err)
	}
	restartedFile, err := NewFile(root)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(ctx, restartedFile, store, clock)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := restarted.Projection(ctx, actor)
	if err != nil || projection.Current.Number != 2 ||
		len(projection.History) != 2 {
		t.Fatalf("restart projection = %+v, %v", projection, err)
	}
	clock.now = clock.now.Add(time.Minute)
	rolledBack, err := restarted.Rollback(ctx, actor, 1)
	if err != nil || rolledBack.Number != 3 ||
		rolledBack.Content != "# SOUL\nprecise" {
		t.Fatalf("rollback = %+v, %v", rolledBack, err)
	}
	if err := os.Chmod(filepath.Join(root, "SOUL.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Current(ctx); err == nil {
		t.Fatal("broad SOUL permissions were accepted")
	}
}
