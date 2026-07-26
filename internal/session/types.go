// Package session implements the encrypted SQLite conversation store.
package session

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrClosed is returned after the session store has shut down.
	ErrClosed = errors.New("session: store closed")
	// ErrNotFound indicates that the requested entity does not exist.
	ErrNotFound = errors.New("session: not found")
)

// Role identifies the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// MemoryType is non-sensitive FTS metadata describing a message.
type MemoryType string

const (
	MemoryTranscript MemoryType = "transcript"
	MemorySummary    MemoryType = "summary"
	MemoryToolEvent  MemoryType = "tool-event"
)

// Session is a conversation segment. A compressed continuation points to its
// predecessor through ParentID.
type Session struct {
	ID            uuid.UUID
	ParentID      *uuid.UUID
	Title         string
	ArchivedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ContextTokens int
}

// Message contains plaintext only in application memory. The SQLite row stores
// Content as an authenticated vault envelope.
type Message struct {
	ID         uuid.UUID
	SessionID  uuid.UUID
	TurnID     *uuid.UUID
	Role       Role
	MemoryType MemoryType
	Content    []byte
	CreatedAt  time.Time
}

// NewMessage validates a message before it reaches durable storage.
func NewMessage(sessionID uuid.UUID, role Role, memoryType MemoryType, content []byte, now time.Time) (Message, error) {
	if sessionID == uuid.Nil {
		return Message{}, fmt.Errorf("session: message session ID is required")
	}
	if !role.Valid() {
		return Message{}, fmt.Errorf("session: invalid message role")
	}
	if !memoryType.Valid() {
		return Message{}, fmt.Errorf("session: invalid message memory type")
	}
	if len(content) == 0 {
		return Message{}, fmt.Errorf("session: message content is required")
	}
	if now.IsZero() {
		return Message{}, fmt.Errorf("session: message timestamp is required")
	}
	return Message{
		ID:         uuid.New(),
		SessionID:  sessionID,
		Role:       role,
		MemoryType: memoryType,
		Content:    append([]byte(nil), content...),
		CreatedAt:  now,
	}, nil
}

// Valid reports whether role is part of the closed role set.
func (role Role) Valid() bool {
	switch role {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

// Valid reports whether memoryType is safe metadata.
func (memoryType MemoryType) Valid() bool {
	switch memoryType {
	case MemoryTranscript, MemorySummary, MemoryToolEvent:
		return true
	default:
		return false
	}
}
