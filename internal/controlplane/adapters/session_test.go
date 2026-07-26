package adapters

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/session"
)

type branchTestStore struct {
	sessions map[uuid.UUID]session.Session
	messages map[uuid.UUID][]session.Message
}

func (store *branchTestStore) CreateSession(
	_ context.Context,
	parentID *uuid.UUID,
) (session.Session, error) {
	created := session.Session{
		ID: uuid.New(), ParentID: parentID, CreatedAt: time.Now().UTC(),
	}
	store.sessions[created.ID] = created
	return created, nil
}

func (store *branchTestStore) GetSession(
	_ context.Context,
	id uuid.UUID,
) (session.Session, error) {
	return store.sessions[id], nil
}

func (store *branchTestStore) ListSessions(
	context.Context,
	int,
) ([]session.Session, error) {
	return nil, nil
}

func (store *branchTestStore) ListMessages(
	_ context.Context,
	id uuid.UUID,
) ([]session.Message, error) {
	return append([]session.Message(nil), store.messages[id]...), nil
}

func (store *branchTestStore) AppendMessage(
	_ context.Context,
	id uuid.UUID,
	role session.Role,
	memoryType session.MemoryType,
	content []byte,
	_ int,
) (session.Message, error) {
	message := session.Message{
		ID: uuid.New(), SessionID: id, Role: role, MemoryType: memoryType,
		Content: append([]byte(nil), content...),
	}
	store.messages[id] = append(store.messages[id], message)
	return message, nil
}

func TestCleanPreviewMakesConversationTitlesSafeAndReadable(t *testing.T) {
	t.Parallel()
	if got := cleanPreview("  Plan\n\tthe   release  ", 56); got != "Plan the release" {
		t.Fatalf("cleanPreview() = %q", got)
	}
	value := strings.Repeat("a", 80)
	got := cleanPreview(value, 12)
	if got != strings.Repeat("a", 11)+"…" {
		t.Fatalf("truncated preview = %q", got)
	}
	if got := cleanPreview("Research 🌍 climate policy", 12); got != "Research 🌍…" {
		t.Fatalf("Unicode preview = %q", got)
	}
}

func TestBranchSessionCopiesTranscriptThroughSelectedMessage(t *testing.T) {
	t.Parallel()
	parentID := uuid.New()
	firstID := uuid.New()
	store := &branchTestStore{
		sessions: map[uuid.UUID]session.Session{
			parentID: {ID: parentID},
		},
		messages: map[uuid.UUID][]session.Message{
			parentID: {
				{
					ID: firstID, SessionID: parentID, Role: session.RoleUser,
					MemoryType: session.MemoryTranscript, Content: []byte("first"),
				},
				{
					ID: uuid.New(), SessionID: parentID, Role: session.RoleAssistant,
					MemoryType: session.MemoryTranscript, Content: []byte("second"),
				},
			},
		},
	}
	child, err := branchSession(
		context.Background(), store, parentID,
		sessionPayload{ThroughMessage: &firstID},
	)
	if err != nil {
		t.Fatal(err)
	}
	copied := store.messages[child.ID]
	if child.ParentID == nil || *child.ParentID != parentID ||
		len(copied) != 1 || string(copied[0].Content) != "first" {
		t.Fatalf("child=%+v copied=%+v", child, copied)
	}

	copyMessages := false
	empty, err := branchSession(
		context.Background(), store, parentID,
		sessionPayload{CopyMessages: &copyMessages},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.messages[empty.ID]) != 0 {
		t.Fatalf("edit branch copied messages: %+v", store.messages[empty.ID])
	}
}
