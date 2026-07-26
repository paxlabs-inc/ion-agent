// Package cassandra implements the doubt controller that revises the agent's
// prior messages when new evidence arrives. It enforces rate limits, preserves
// originals in the append-only journal as dual-record deltas, and provides
// 72-hour undo. Cassandra cannot modify tool calls, tool arguments, roles,
// message authors, user messages, or system messages.
package cassandra

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"lukechampine.com/blake3"
)

const (
	// MaxEditsPerTurn is the maximum edits per assistant turn.
	MaxEditsPerTurn = 3
	// CooldownBetweenEdits is the minimum time between edits to the same message.
	CooldownBetweenEdits = 30 * time.Second
	// MaxContentChangeRatio is the maximum fraction of content that can change.
	MaxContentChangeRatio = 0.30
	// UndoWindow is the duration after an edit during which undo is allowed.
	UndoWindow = 72 * time.Hour
)

// Trigger identifies why Cassandra is editing.
type Trigger string

const (
	TriggerPredictionMismatch Trigger = "prediction_mismatch"
	TriggerUserCorrection     Trigger = "user_correction"
	TriggerRefutedPremise     Trigger = "refuted_premise"
	TriggerRepeatedLoop       Trigger = "repeated_loop"
	TriggerPrematureClose     Trigger = "premature_close"
	TriggerOverVerify         Trigger = "over_verification"
)

// Side identifies the damping direction.
type Side string

const (
	SideDoubt     Side = "doubt"
	SideAssurance Side = "assurance"
)

// EditState is the lifecycle of an edit.
type EditState string

const (
	EditActive  EditState = "active"
	EditUndone  EditState = "undone"
	EditExpired EditState = "expired"
)

// Edit is the dual-record delta for one Cassandra modification.
type Edit struct {
	ID              uuid.UUID  `json:"id"`
	OriginalMsgID   string     `json:"original_msg_id"`
	OriginalContent string     `json:"original_content"`
	OriginalHash    string     `json:"original_hash"`
	Delta           string     `json:"delta"`
	ResultContent   string     `json:"result_content"`
	ResultHash      string     `json:"result_hash"`
	Trigger         Trigger    `json:"trigger"`
	Side            Side       `json:"side"`
	Reason          string     `json:"reason"`
	Timestamp       time.Time  `json:"timestamp"`
	Actor           string     `json:"actor"`
	Approved        bool       `json:"approved"`
	State           EditState  `json:"state"`
	UndoneAt        *time.Time `json:"undone_at,omitempty"`
}

// AuditEvent is the complete record of one Cassandra action.
type AuditEvent struct {
	EditID        uuid.UUID `json:"edit_id"`
	OriginalMsgID string    `json:"original_msg_id"`
	OriginalHash  string    `json:"original_hash"`
	Delta         string    `json:"delta"`
	Trigger       Trigger   `json:"trigger"`
	Side          Side      `json:"side"`
	Reason        string    `json:"reason"`
	Timestamp     time.Time `json:"timestamp"`
	Actor         string    `json:"actor"`
	Approved      bool      `json:"approved"`
	ResultHash    string    `json:"result_hash"`
}

// Clock abstracts time for deterministic testing.
type Clock interface {
	Now() time.Time
}

// Auditor durably records Cassandra events.
type Auditor interface {
	RecordCassandraEvent(Edit) error
}

// ProtectedMemoryType identifies memory types that require RED approval for
// Cassandra edits.
type ProtectedMemoryType string

var protectedTypes = map[ProtectedMemoryType]bool{
	"0x01": true, // Identity
	"0x07": true, // Constraint
}

// Controller manages Cassandra's edit behavior.
type Controller struct {
	mu            sync.Mutex
	clock         Clock
	auditor       Auditor
	edits         []Edit
	editsThisTurn int
	lastEditTimes map[string]time.Time // msgID -> last edit time
}

// State is the durable controller state required for cooldown and undo
// continuity across daemon restart.
type State struct {
	Edits         []Edit               `json:"edits"`
	EditsThisTurn int                  `json:"edits_this_turn"`
	LastEditTimes map[string]time.Time `json:"last_edit_times"`
}

// New creates a Cassandra controller.
func New(clock Clock, auditor Auditor) (*Controller, error) {
	if clock == nil {
		return nil, fmt.Errorf("cassandra: clock is required")
	}
	if auditor == nil {
		return nil, fmt.Errorf("cassandra: auditor is required")
	}
	return &Controller{
		clock:         clock,
		auditor:       auditor,
		lastEditTimes: make(map[string]time.Time),
	}, nil
}

// Restore reconstructs Cassandra state against the live durable auditor.
func Restore(clock Clock, auditor Auditor, state State) (*Controller, error) {
	controller, err := New(clock, auditor)
	if err != nil {
		return nil, err
	}
	if state.EditsThisTurn < 0 || state.EditsThisTurn > MaxEditsPerTurn {
		return nil, fmt.Errorf("cassandra: invalid durable per-turn edit count")
	}
	controller.editsThisTurn = state.EditsThisTurn
	controller.lastEditTimes = make(map[string]time.Time, len(state.LastEditTimes))
	for messageID, editedAt := range state.LastEditTimes {
		if strings.TrimSpace(messageID) == "" || editedAt.IsZero() {
			return nil, fmt.Errorf("cassandra: invalid durable cooldown state")
		}
		controller.lastEditTimes[messageID] = editedAt
	}
	controller.edits = append([]Edit(nil), state.Edits...)
	for index := range controller.edits {
		edit := &controller.edits[index]
		if edit.ID == uuid.Nil || strings.TrimSpace(edit.OriginalMsgID) == "" ||
			edit.Timestamp.IsZero() || edit.State == "" {
			return nil, fmt.Errorf("cassandra: invalid durable edit")
		}
		if edit.UndoneAt != nil {
			undoneAt := *edit.UndoneAt
			edit.UndoneAt = &undoneAt
		}
	}
	return controller, nil
}

// Snapshot returns a defensive copy for encrypted persistence.
func (controller *Controller) Snapshot() State {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	state := State{
		Edits:         append([]Edit(nil), controller.edits...),
		EditsThisTurn: controller.editsThisTurn,
		LastEditTimes: make(map[string]time.Time, len(controller.lastEditTimes)),
	}
	for index := range state.Edits {
		if state.Edits[index].UndoneAt != nil {
			undoneAt := *state.Edits[index].UndoneAt
			state.Edits[index].UndoneAt = &undoneAt
		}
	}
	for messageID, editedAt := range controller.lastEditTimes {
		state.LastEditTimes[messageID] = editedAt
	}
	return state
}

// CanEdit checks whether an edit to the given message is allowed by rate limits.
func (controller *Controller) CanEdit(msgID string) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.canEditLocked(msgID)
}

func (controller *Controller) canEditLocked(msgID string) error {
	if controller.editsThisTurn >= MaxEditsPerTurn {
		return fmt.Errorf("cassandra: edit limit reached (%d/turn)", MaxEditsPerTurn)
	}
	if lastEdit, ok := controller.lastEditTimes[msgID]; ok {
		elapsed := controller.clock.Now().Sub(lastEdit)
		if elapsed < CooldownBetweenEdits {
			return fmt.Errorf("cassandra: cooldown not met (%.0fs remaining)",
				(CooldownBetweenEdits - elapsed).Seconds())
		}
	}
	return nil
}

// Edit applies a content modification with dual-record preservation.
// It validates that the content change ratio is within bounds, preserves the
// original in the audit trail, and records the delta.
func (controller *Controller) Edit(
	msgID string,
	originalContent string,
	newContent string,
	trigger Trigger,
	side Side,
	reason string,
	actor string,
	approved bool,
) (*Edit, error) {
	changeRatio := contentChangeRatio(originalContent, newContent)
	if changeRatio > MaxContentChangeRatio {
		return nil, fmt.Errorf("cassandra: content change ratio %.1f%% exceeds maximum %.1f%%",
			changeRatio*100, MaxContentChangeRatio*100)
	}
	if originalContent == newContent {
		return nil, fmt.Errorf("cassandra: no change detected")
	}
	if NeedsApproval(originalContent+"\n"+newContent) && !approved {
		return nil, fmt.Errorf("cassandra: RED approval required for protected content")
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if err := controller.canEditLocked(msgID); err != nil {
		return nil, err
	}

	now := controller.clock.Now()
	edit := Edit{
		ID:              uuid.New(),
		OriginalMsgID:   msgID,
		OriginalContent: originalContent,
		OriginalHash:    hashContent(originalContent),
		Delta:           computeDelta(originalContent, newContent),
		ResultContent:   newContent,
		ResultHash:      hashContent(newContent),
		Trigger:         trigger,
		Side:            side,
		Reason:          reason,
		Timestamp:       now,
		Actor:           actor,
		Approved:        approved,
		State:           EditActive,
	}

	if err := controller.auditor.RecordCassandraEvent(edit); err != nil {
		return nil, fmt.Errorf("cassandra: audit failed: %w", err)
	}

	controller.edits = append(controller.edits, edit)
	controller.editsThisTurn++
	controller.lastEditTimes[msgID] = now
	return &edit, nil
}

// Undo reverts the most recent eligible edit, or a specific edit by ID.
// Returns a typed error if the undo window has expired.
func (controller *Controller) Undo(editID *uuid.UUID) (*Edit, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.clock.Now()
	for i := len(controller.edits) - 1; i >= 0; i-- {
		edit := &controller.edits[i]
		if edit.State != EditActive {
			continue
		}
		if editID != nil && edit.ID != *editID {
			continue
		}
		if now.Sub(edit.Timestamp) > UndoWindow {
			return nil, fmt.Errorf("cassandra: undo window expired (edit at %s, window %s)",
				edit.Timestamp.Format(time.RFC3339), UndoWindow)
		}
		undone := *edit
		undone.State = EditUndone
		undone.UndoneAt = &now
		if err := controller.auditor.RecordCassandraEvent(undone); err != nil {
			return nil, fmt.Errorf("cassandra: audit undo failed: %w", err)
		}
		*edit = undone
		result := *edit
		return &result, nil
	}
	if editID != nil {
		return nil, fmt.Errorf("cassandra: edit not found: %s", *editID)
	}
	return nil, fmt.Errorf("cassandra: no eligible edits to undo")
}

// NewTurn resets the per-turn edit counter.
func (controller *Controller) NewTurn() {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.editsThisTurn = 0
}

// IsProtected returns true if the memory type requires RED approval for edits.
func IsProtected(memoryType string) bool {
	return protectedTypes[ProtectedMemoryType(memoryType)]
}

// IsProtected reports whether a memory namespace requires RED approval.
func (controller *Controller) IsProtected(memoryType string) bool {
	return IsProtected(memoryType)
}

// Edits returns a defensive copy of all edits.
func (controller *Controller) Edits() []Edit {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	result := make([]Edit, len(controller.edits))
	copy(result, controller.edits)
	return result
}

// ActiveEdits returns only active (non-undone) edits.
func (controller *Controller) ActiveEdits() []Edit {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	var result []Edit
	for _, edit := range controller.edits {
		if edit.State == EditActive {
			result = append(result, edit)
		}
	}
	return result
}

// hashContent computes a BLAKE3-256 hash of the content.
func hashContent(content string) string {
	h := blake3.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// computeDelta produces a real content-difference string showing the edits
// between original and modified. It records line-level additions, removals,
// and changes with runic (Unicode-aware) length accounting.
func computeDelta(original, modified string) string {
	if original == modified {
		return ""
	}
	origLines := strings.Split(original, "\n")
	modLines := strings.Split(modified, "\n")

	var builder strings.Builder
	origIdx, modIdx := 0, 0
	for origIdx < len(origLines) || modIdx < len(modLines) {
		switch {
		case origIdx >= len(origLines):
			builder.WriteString(fmt.Sprintf("+%s\n", modLines[modIdx]))
			modIdx++
		case modIdx >= len(modLines):
			builder.WriteString(fmt.Sprintf("-%s\n", origLines[origIdx]))
			origIdx++
		case origLines[origIdx] == modLines[modIdx]:
			origIdx++
			modIdx++
		default:
			// Find next matching line to distinguish change from add/remove.
			foundMod := -1
			for j := modIdx; j < len(modLines) && j < modIdx+3; j++ {
				if modLines[j] == origLines[origIdx] {
					foundMod = j
					break
				}
			}
			foundOrig := -1
			for j := origIdx; j < len(origLines) && j < origIdx+3; j++ {
				if origLines[j] == modLines[modIdx] {
					foundOrig = j
					break
				}
			}
			switch {
			case foundMod >= 0:
				// Lines were added.
				for k := modIdx; k < foundMod; k++ {
					builder.WriteString(fmt.Sprintf("+%s\n", modLines[k]))
				}
				modIdx = foundMod
			case foundOrig >= 0:
				// Lines were removed.
				for k := origIdx; k < foundOrig; k++ {
					builder.WriteString(fmt.Sprintf("-%s\n", origLines[k]))
				}
				origIdx = foundOrig
			default:
				// Line changed.
				builder.WriteString(fmt.Sprintf("~%s\n", origLines[origIdx]))
				builder.WriteString(fmt.Sprintf("+%s\n", modLines[modIdx]))
				origIdx++
				modIdx++
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

// contentChangeRatio computes the real content difference using the
// Levenshtein-like edit distance at the rune level, normalized by
// the maximum content length. This is Unicode-aware.
func contentChangeRatio(original, modified string) float64 {
	origRunes := []rune(original)
	modRunes := []rune(modified)
	origLen := len(origRunes)
	modLen := len(modRunes)

	if origLen == 0 && modLen == 0 {
		return 0
	}
	if origLen == 0 || modLen == 0 {
		return 1.0
	}

	// Wagner-Fischer edit distance with O(min(m,n)) space.
	// Use the shorter string for the DP row to save memory.
	if origLen > modLen {
		origRunes, modRunes = modRunes, origRunes
		origLen, modLen = modLen, origLen
	}

	prev := make([]int, origLen+1)
	curr := make([]int, origLen+1)
	for i := 0; i <= origLen; i++ {
		prev[i] = i
	}

	for j := 1; j <= modLen; j++ {
		curr[0] = j
		for i := 1; i <= origLen; i++ {
			cost := 1
			if origRunes[i-1] == modRunes[j-1] {
				cost = 0
			}
			curr[i] = min3(
				prev[i]+1,      // deletion
				curr[i-1]+1,    // insertion
				prev[i-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	maxLen := origLen
	if modLen > maxLen {
		maxLen = modLen
	}
	return float64(prev[origLen]) / float64(maxLen)
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// NeedsApproval checks if an edit to the given content requires RED approval.
// Currently checks for references to protected memory types or SOUL.md.
func NeedsApproval(content string) bool {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "soul.md") {
		return true
	}
	for memType := range protectedTypes {
		if strings.Contains(lower, string(memType)) {
			return true
		}
	}
	return false
}
