// Package adapters connects control-plane operations to existing production
// application services.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/session"
)

// SessionStore is the narrow encrypted-session contract consumed by adapters.
type SessionStore interface {
	CreateSession(context.Context, *uuid.UUID) (session.Session, error)
	GetSession(context.Context, uuid.UUID) (session.Session, error)
	ListSessions(context.Context, int) ([]session.Session, error)
	ListArchivedSessions(context.Context, int) ([]session.Session, error)
	ListMessages(context.Context, uuid.UUID) ([]session.Message, error)
	RenameSession(context.Context, uuid.UUID, string) (session.Session, error)
	ArchiveSession(context.Context, uuid.UUID, bool) (session.Session, error)
	DeleteSession(context.Context, uuid.UUID) error
}

type sessionPayload struct {
	ParentID       *uuid.UUID `json:"parent_id,omitempty"`
	ThroughMessage *uuid.UUID `json:"through_message_id,omitempty"`
	CopyMessages   *bool      `json:"copy_messages,omitempty"`
	Archived       *bool      `json:"archived,omitempty"`
	Title          string     `json:"title,omitempty"`
	ConfirmID      *uuid.UUID `json:"confirm_session_id,omitempty"`
}

type sessionListPayload struct {
	Archived bool `json:"archived,omitempty"`
}

type branchMessageStore interface {
	AppendMessage(
		context.Context,
		uuid.UUID,
		session.Role,
		session.MemoryType,
		[]byte,
		int,
	) (session.Message, error)
}

type branchSessionStore interface {
	CreateSession(context.Context, *uuid.UUID) (session.Session, error)
	ListMessages(context.Context, uuid.UUID) ([]session.Message, error)
}

type atomicBranchStore interface {
	BranchSession(
		context.Context,
		uuid.UUID,
		[]session.Message,
	) (session.Session, error)
}

type sessionView struct {
	ID            uuid.UUID  `json:"id"`
	ParentID      *uuid.UUID `json:"parent_id,omitempty"`
	Title         string     `json:"title,omitempty"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ContextTokens int        `json:"context_tokens"`
}

type messageView struct {
	ID         uuid.UUID          `json:"id"`
	SessionID  uuid.UUID          `json:"session_id"`
	TurnID     *uuid.UUID         `json:"turn_id,omitempty"`
	Role       session.Role       `json:"role"`
	MemoryType session.MemoryType `json:"memory_type"`
	Content    string             `json:"content"`
	CreatedAt  time.Time          `json:"created_at"`
}

type resumedSession struct {
	Session  sessionView   `json:"session"`
	Messages []messageView `json:"messages"`
}

type sessionListItem struct {
	sessionView
	Title   string `json:"title"`
	Preview string `json:"preview,omitempty"`
}

// RegisterSessionHandlers exposes create, resume, and branch through the
// encrypted production session store.
func RegisterSessionHandlers(
	dispatcher *controlplane.Dispatcher,
	store SessionStore,
) error {
	if dispatcher == nil || store == nil {
		return fmt.Errorf("controlplane adapters: dispatcher and session store are required")
	}
	if err := dispatcher.Register(
		controlplane.OperationSessionList,
		"List encrypted conversations with a short decrypted preview for the authenticated operator.",
		controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			var payload sessionListPayload
			if err := decode(request.Payload, &payload); err != nil {
				return nil, err
			}
			sessions, err := store.ListSessions(ctx, 50)
			if payload.Archived {
				sessions, err = store.ListArchivedSessions(ctx, 50)
			}
			if err != nil {
				return nil, fmt.Errorf("controlplane adapters: list sessions: %w", err)
			}
			items := make([]sessionListItem, 0, len(sessions))
			for _, found := range sessions {
				item := sessionListItem{
					sessionView: toSessionView(found),
					Title:       "New conversation",
				}
				messages, err := store.ListMessages(ctx, found.ID)
				if err != nil {
					return nil, fmt.Errorf(
						"controlplane adapters: preview session %s: %w",
						found.ID,
						err,
					)
				}
				for _, message := range messages {
					if message.Role == session.RoleUser && item.Preview == "" {
						item.Preview = cleanPreview(string(message.Content), 160)
						if found.Title == "" {
							item.Title = cleanPreview(string(message.Content), 56)
						}
					}
				}
				if found.Title != "" {
					item.Title = found.Title
				}
				items = append(items, item)
			}
			return json.Marshal(items)
		}),
	); err != nil {
		return err
	}
	if err := dispatcher.Register(
		controlplane.OperationSessionCreate,
		"Create an encrypted root session.",
		controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			var payload sessionPayload
			if err := decode(request.Payload, &payload); err != nil {
				return nil, err
			}
			if payload.ParentID != nil {
				return nil, controlplane.PublicError{
					Code:    controlplane.ErrorInvalid,
					Message: "session.create cannot specify a parent; use session.branch",
				}
			}
			created, err := store.CreateSession(ctx, nil)
			if err != nil {
				return nil, fmt.Errorf("controlplane adapters: create session: %w", err)
			}
			return json.Marshal(toSessionView(created))
		}),
	); err != nil {
		return err
	}
	if err := dispatcher.Register(
		controlplane.OperationSessionResume,
		"Resume one encrypted session with its decrypted transcript.",
		controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			sessionID, err := scopedSessionID(request)
			if err != nil {
				return nil, err
			}
			return resume(ctx, store, sessionID)
		}),
	); err != nil {
		return err
	}
	if err := dispatcher.Register(
		controlplane.OperationSessionRename,
		"Rename one encrypted conversation.",
		controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			var payload sessionPayload
			if err := decode(request.Payload, &payload); err != nil {
				return nil, err
			}
			sessionID, err := scopedSessionID(request)
			if err != nil {
				return nil, err
			}
			renamed, err := store.RenameSession(ctx, sessionID, payload.Title)
			if err != nil {
				return nil, fmt.Errorf("controlplane adapters: rename session: %w", err)
			}
			return json.Marshal(toSessionView(renamed))
		}),
	); err != nil {
		return err
	}
	if err := dispatcher.Register(
		controlplane.OperationSessionArchive,
		"Archive or restore one encrypted conversation without deleting it.",
		controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			var payload sessionPayload
			if err := decode(request.Payload, &payload); err != nil {
				return nil, err
			}
			if payload.Archived == nil {
				return nil, controlplane.PublicError{
					Code:    controlplane.ErrorInvalid,
					Message: "archived state is required",
				}
			}
			sessionID, err := scopedSessionID(request)
			if err != nil {
				return nil, err
			}
			updated, err := store.ArchiveSession(
				ctx, sessionID, *payload.Archived,
			)
			if err != nil {
				return nil, fmt.Errorf("controlplane adapters: archive session: %w", err)
			}
			return json.Marshal(toSessionView(updated))
		}),
	); err != nil {
		return err
	}
	if err := dispatcher.Register(
		controlplane.OperationSessionDelete,
		"Permanently delete exactly one encrypted conversation after exact confirmation.",
		controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			var payload sessionPayload
			if err := decode(request.Payload, &payload); err != nil {
				return nil, err
			}
			sessionID, err := scopedSessionID(request)
			if err != nil {
				return nil, err
			}
			if payload.ConfirmID == nil || *payload.ConfirmID != sessionID {
				return nil, controlplane.PublicError{
					Code:    controlplane.ErrorInvalid,
					Message: "exact conversation confirmation is required",
				}
			}
			if err := store.DeleteSession(ctx, sessionID); err != nil {
				return nil, fmt.Errorf("controlplane adapters: delete session: %w", err)
			}
			return json.Marshal(map[string]any{
				"deleted": true, "session_id": sessionID,
			})
		}),
	); err != nil {
		return err
	}
	return dispatcher.Register(
		controlplane.OperationSessionBranch,
		"Create an encrypted child session from the scoped parent.",
		controlplane.HandlerFunc(func(
			ctx context.Context,
			request controlplane.Request,
			_ controlplane.EventEmitter,
		) (json.RawMessage, error) {
			var payload sessionPayload
			if err := decode(request.Payload, &payload); err != nil {
				return nil, err
			}
			parentID, err := scopedSessionID(request)
			if err != nil {
				return nil, err
			}
			if _, err := store.GetSession(ctx, parentID); err != nil {
				return nil, fmt.Errorf("controlplane adapters: find branch parent: %w", err)
			}
			created, err := branchSession(ctx, store, parentID, payload)
			if err != nil {
				return nil, err
			}
			return json.Marshal(toSessionView(created))
		}),
	)
}

func branchSession(
	ctx context.Context,
	store branchSessionStore,
	parentID uuid.UUID,
	payload sessionPayload,
) (session.Session, error) {
	copyMessages := payload.CopyMessages == nil || *payload.CopyMessages
	appender, canCopy := store.(branchMessageStore)
	if !canCopy && payload.ThroughMessage != nil {
		return session.Session{}, fmt.Errorf(
			"controlplane adapters: branch transcript copy is unavailable",
		)
	}
	var messages []session.Message
	var err error
	if canCopy && copyMessages {
		messages, err = store.ListMessages(ctx, parentID)
		if err != nil {
			return session.Session{}, fmt.Errorf(
				"controlplane adapters: read branch transcript: %w", err,
			)
		}
	}
	copyCount := len(messages)
	if payload.ThroughMessage != nil {
		copyCount = -1
		for index, message := range messages {
			if message.ID == *payload.ThroughMessage {
				copyCount = index + 1
				break
			}
		}
		if copyCount < 0 {
			return session.Session{}, controlplane.PublicError{
				Code:    controlplane.ErrorInvalid,
				Message: "the selected message is not in this conversation",
			}
		}
	}
	if brancher, ok := store.(atomicBranchStore); ok {
		created, err := brancher.BranchSession(
			ctx, parentID, messages[:copyCount],
		)
		if err != nil {
			return session.Session{}, fmt.Errorf(
				"controlplane adapters: branch session atomically: %w", err,
			)
		}
		return created, nil
	}
	created, err := store.CreateSession(ctx, &parentID)
	if err != nil {
		return session.Session{}, fmt.Errorf(
			"controlplane adapters: branch session: %w", err,
		)
	}
	for _, message := range messages[:copyCount] {
		if _, err := appender.AppendMessage(
			ctx, created.ID, message.Role, message.MemoryType,
			message.Content, 0,
		); err != nil {
			return session.Session{}, fmt.Errorf(
				"controlplane adapters: copy branch transcript: %w", err,
			)
		}
	}
	return created, nil
}

func resume(
	ctx context.Context,
	store SessionStore,
	sessionID uuid.UUID,
) (json.RawMessage, error) {
	found, err := store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("controlplane adapters: get session: %w", err)
	}
	messages, err := store.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("controlplane adapters: list session messages: %w", err)
	}
	views := make([]messageView, 0, len(messages))
	for _, message := range messages {
		views = append(views, messageView{
			ID: message.ID, SessionID: message.SessionID, TurnID: message.TurnID,
			Role:       message.Role,
			MemoryType: message.MemoryType, Content: string(message.Content),
			CreatedAt: message.CreatedAt,
		})
	}
	return json.Marshal(resumedSession{
		Session: toSessionView(found), Messages: views,
	})
}

func scopedSessionID(request controlplane.Request) (uuid.UUID, error) {
	if request.Scope.SessionID == nil || *request.Scope.SessionID == uuid.Nil {
		return uuid.Nil, controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "session scope is required",
		}
	}
	return *request.Scope.SessionID, nil
}

func toSessionView(found session.Session) sessionView {
	return sessionView{
		ID: found.ID, ParentID: found.ParentID, Title: found.Title,
		ArchivedAt: found.ArchivedAt, CreatedAt: found.CreatedAt,
		UpdatedAt: found.UpdatedAt, ContextTokens: found.ContextTokens,
	}
}

func cleanPreview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
