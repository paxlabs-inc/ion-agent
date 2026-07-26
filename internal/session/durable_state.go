package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// TurnStatus is the durable lifecycle of a production turn.
type TurnStatus string

const (
	TurnRunning     TurnStatus = "running"
	TurnRecovering  TurnStatus = "recovering"
	TurnIncomplete  TurnStatus = "incomplete"
	TurnCompleted   TurnStatus = "completed"
	TurnFailed      TurnStatus = "failed"
	TurnCancelled   TurnStatus = "cancelled"
	TurnInterrupted TurnStatus = "interrupted"
)

// TurnState preserves restart-safe input, loop checkpoint, and typed recovery
// metadata. The complete value is encrypted as one envelope.
type TurnState struct {
	TurnID      uuid.UUID       `json:"turn_id"`
	ActorID     uuid.UUID       `json:"actor_id"`
	SessionID   uuid.UUID       `json:"session_id"`
	Content     string          `json:"content"`
	Surface     string          `json:"surface,omitempty"`
	Origin      string          `json:"origin,omitempty"`
	ProjectID   uuid.UUID       `json:"project_id,omitempty"`
	Status      TurnStatus      `json:"status"`
	Checkpoint  json.RawMessage `json:"checkpoint,omitempty"`
	Recovery    json.RawMessage `json:"recovery,omitempty"`
	FailureCode string          `json:"failure_code,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// SaveCognitionState atomically replaces one session's encrypted epistemic
// checkpoint.
func (store *Store) SaveCognitionState(
	ctx context.Context,
	sessionID uuid.UUID,
	state json.RawMessage,
) error {
	if sessionID == uuid.Nil || len(state) == 0 || !json.Valid(state) {
		return fmt.Errorf("session: valid cognition state and session are required")
	}
	envelope, err := store.encryptJSON(state)
	if err != nil {
		return fmt.Errorf("session: encrypt cognition state: %w", err)
	}
	defer zeroBytes(envelope)
	now := store.clock.Now()
	_, err = store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		_, executeErr := db.ExecContext(
			runCtx,
			`INSERT INTO cognition_state(session_id, state, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(session_id) DO UPDATE SET
			   state = excluded.state,
			   updated_at = excluded.updated_at`,
			sessionID.String(), envelope, toMicros(now),
		)
		if executeErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: save cognition state: %w", executeErr,
			)}
		}
		return writeResult{}
	})
	return err
}

// LoadCognitionState decrypts one session's epistemic checkpoint.
func (store *Store) LoadCognitionState(
	ctx context.Context,
	sessionID uuid.UUID,
) (json.RawMessage, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	if sessionID == uuid.Nil {
		return nil, fmt.Errorf("session: cognition session is required")
	}
	var envelope []byte
	if err := store.readDB.QueryRowContext(
		ctx,
		`SELECT state FROM cognition_state WHERE session_id = ?`,
		sessionID.String(),
	).Scan(&envelope); err != nil {
		return nil, fmt.Errorf("session: load cognition state: %w", err)
	}
	return store.decryptJSON(envelope, "cognition state")
}

// CreateTurnState writes the initial durable record before execution starts.
func (store *Store) CreateTurnState(ctx context.Context, state TurnState) error {
	if err := validateTurnState(state); err != nil {
		return err
	}
	if state.Status != TurnRunning {
		return fmt.Errorf("session: new turn must be running")
	}
	return store.saveTurnState(ctx, state, false)
}

// CreateScheduledTurn atomically persists the scheduled user message and its
// deterministic durable turn. Replaying one occurrence ID returns the existing
// turn without appending a second transcript message.
func (store *Store) CreateScheduledTurn(
	ctx context.Context,
	state TurnState,
	messageID uuid.UUID,
	contextTokens int,
) (TurnState, bool, error) {
	if err := validateTurnState(state); err != nil {
		return TurnState{}, false, err
	}
	if state.Status != TurnRunning || messageID == uuid.Nil || contextTokens < 0 {
		return TurnState{}, false, fmt.Errorf(
			"session: valid scheduled turn, message, and context usage are required",
		)
	}
	state.UpdatedAt = store.clock.Now()
	message, err := NewMessage(
		state.SessionID, RoleUser, MemoryTranscript,
		[]byte(state.Content), state.UpdatedAt,
	)
	if err != nil {
		return TurnState{}, false, err
	}
	message.ID = messageID
	statePlaintext, err := json.Marshal(state)
	if err != nil {
		return TurnState{}, false, fmt.Errorf("session: encode scheduled turn: %w", err)
	}
	defer zeroBytes(statePlaintext)
	stateEnvelope, err := store.encryptJSON(statePlaintext)
	if err != nil {
		return TurnState{}, false, fmt.Errorf("session: encrypt scheduled turn: %w", err)
	}
	defer zeroBytes(stateEnvelope)
	messageEnvelope, err := store.cipher.Encrypt(message.Content)
	if err != nil {
		return TurnState{}, false, fmt.Errorf("session: encrypt scheduled message: %w", err)
	}
	defer zeroBytes(messageEnvelope)

	result, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		tx, beginErr := db.BeginTx(runCtx, nil)
		if beginErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: begin scheduled turn: %w", beginErr,
			)}
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		var exists int
		if queryErr := tx.QueryRowContext(
			runCtx, `SELECT COUNT(*) FROM turn_state WHERE turn_id = ?`,
			state.TurnID.String(),
		).Scan(&exists); queryErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: inspect scheduled occurrence: %w", queryErr,
			)}
		}
		if exists == 1 {
			return writeResult{created: false}
		}
		if _, insertErr := tx.ExecContext(
			runCtx,
			`INSERT INTO messages(id, session_id, role, memory_type, content, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			message.ID.String(), state.SessionID.String(), string(message.Role),
			string(message.MemoryType), messageEnvelope, toMicros(message.CreatedAt),
		); insertErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: insert scheduled message: %w", insertErr,
			)}
		}
		if _, updateErr := tx.ExecContext(
			runCtx,
			`UPDATE sessions SET context_tokens = ?, updated_at = ? WHERE id = ?`,
			contextTokens, toMicros(message.CreatedAt), state.SessionID.String(),
		); updateErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: update scheduled context usage: %w", updateErr,
			)}
		}
		if _, insertErr := tx.ExecContext(
			runCtx,
			`INSERT INTO turn_state(
				turn_id, actor_id, session_id, status, state, updated_at
			 ) VALUES (?, ?, ?, ?, ?, ?)`,
			state.TurnID.String(), state.ActorID.String(), state.SessionID.String(),
			string(state.Status), stateEnvelope, toMicros(state.UpdatedAt),
		); insertErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: insert scheduled turn: %w", insertErr,
			)}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: commit scheduled turn: %w", commitErr,
			)}
		}
		committed = true
		return writeResult{created: true}
	})
	if err != nil {
		return TurnState{}, false, err
	}
	if !result.created {
		existing, loadErr := store.LoadTurnState(ctx, state.TurnID)
		if loadErr != nil {
			return TurnState{}, false, loadErr
		}
		return existing, false, nil
	}
	return cloneTurnState(state), true, nil
}

// SaveTurnCheckpoint replaces the resumable loop checkpoint without changing
// the current lifecycle status.
func (store *Store) SaveTurnCheckpoint(
	ctx context.Context,
	turnID uuid.UUID,
	checkpoint json.RawMessage,
) error {
	if turnID == uuid.Nil || len(checkpoint) == 0 || !json.Valid(checkpoint) {
		return fmt.Errorf("session: valid turn checkpoint is required")
	}
	state, err := store.LoadTurnState(ctx, turnID)
	if err != nil {
		return err
	}
	state.Checkpoint = append(json.RawMessage(nil), checkpoint...)
	state.UpdatedAt = store.clock.Now()
	return store.saveTurnState(ctx, state, true)
}

// SetTurnRecovery persists typed incomplete/recovery context and status.
func (store *Store) SetTurnRecovery(
	ctx context.Context,
	turnID uuid.UUID,
	status TurnStatus,
	recovery json.RawMessage,
) error {
	if !status.valid() || len(recovery) == 0 || !json.Valid(recovery) {
		return fmt.Errorf("session: valid recovery state is required")
	}
	state, err := store.LoadTurnState(ctx, turnID)
	if err != nil {
		return err
	}
	state.Status = status
	state.Recovery = append(json.RawMessage(nil), recovery...)
	state.UpdatedAt = store.clock.Now()
	return store.saveTurnState(ctx, state, true)
}

// SetTurnStatus changes terminal or restart lifecycle state.
func (store *Store) SetTurnStatus(
	ctx context.Context,
	turnID uuid.UUID,
	status TurnStatus,
) error {
	if !status.valid() {
		return fmt.Errorf("session: invalid turn status %q", status)
	}
	state, err := store.LoadTurnState(ctx, turnID)
	if err != nil {
		return err
	}
	state.Status = status
	state.UpdatedAt = store.clock.Now()
	return store.saveTurnState(ctx, state, true)
}

// SetTurnFailure persists a redaction-safe machine-readable cause with the
// terminal state, without storing provider text or transcript content.
func (store *Store) SetTurnFailure(
	ctx context.Context,
	turnID uuid.UUID,
	status TurnStatus,
	code string,
) error {
	code = strings.TrimSpace(code)
	if status != TurnFailed && status != TurnCancelled ||
		code == "" || len(code) > 96 {
		return fmt.Errorf("session: valid terminal failure code is required")
	}
	for _, current := range code {
		if (current < 'a' || current > 'z') &&
			(current < '0' || current > '9') && current != '_' {
			return fmt.Errorf("session: invalid terminal failure code")
		}
	}
	state, err := store.LoadTurnState(ctx, turnID)
	if err != nil {
		return err
	}
	state.Status = status
	state.FailureCode = code
	state.UpdatedAt = store.clock.Now()
	return store.saveTurnState(ctx, state, true)
}

// LoadTurnState decrypts a turn record by ID.
func (store *Store) LoadTurnState(
	ctx context.Context,
	turnID uuid.UUID,
) (TurnState, error) {
	if err := store.checkOpen(); err != nil {
		return TurnState{}, err
	}
	if turnID == uuid.Nil {
		return TurnState{}, fmt.Errorf("session: turn ID is required")
	}
	var envelope []byte
	if err := store.readDB.QueryRowContext(
		ctx, `SELECT state FROM turn_state WHERE turn_id = ?`, turnID.String(),
	).Scan(&envelope); err != nil {
		return TurnState{}, fmt.Errorf("session: load turn state: %w", err)
	}
	plaintext, err := store.decryptJSON(envelope, "turn state")
	if err != nil {
		return TurnState{}, err
	}
	defer zeroBytes(plaintext)
	var state TurnState
	if err := json.Unmarshal(plaintext, &state); err != nil {
		return TurnState{}, fmt.Errorf("session: decode turn state: %w", err)
	}
	if err := validateTurnState(state); err != nil {
		return TurnState{}, err
	}
	return cloneTurnState(state), nil
}

// RecoverableTurnStates returns nonterminal turns in deterministic age order.
func (store *Store) RecoverableTurnStates(
	ctx context.Context,
) ([]TurnState, error) {
	return store.queryTurnStates(
		ctx,
		`SELECT state FROM turn_state
		 WHERE status IN (?, ?, ?, ?)
		 ORDER BY updated_at, turn_id`,
		string(TurnRunning), string(TurnRecovering),
		string(TurnIncomplete), string(TurnInterrupted),
	)
}

// RecentTurnStates returns truthful recovery projections for one session.
func (store *Store) RecentTurnStates(
	ctx context.Context,
	sessionID uuid.UUID,
	limit int,
) ([]TurnState, error) {
	if sessionID == uuid.Nil || limit < 1 || limit > 256 {
		return nil, fmt.Errorf("session: valid session and turn limit are required")
	}
	return store.queryTurnStates(
		ctx,
		`SELECT state FROM turn_state
		 WHERE session_id = ?
		 ORDER BY updated_at DESC, turn_id DESC LIMIT ?`,
		sessionID.String(), limit,
	)
}

func (store *Store) queryTurnStates(
	ctx context.Context,
	query string,
	arguments ...any,
) ([]TurnState, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	rows, err := store.readDB.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("session: query turn states: %w", err)
	}
	defer rows.Close()
	var states []TurnState
	for rows.Next() {
		var envelope []byte
		if err := rows.Scan(&envelope); err != nil {
			return nil, fmt.Errorf("session: scan turn state: %w", err)
		}
		plaintext, err := store.decryptJSON(envelope, "turn state")
		if err != nil {
			return nil, err
		}
		var state TurnState
		decodeErr := json.Unmarshal(plaintext, &state)
		zeroBytes(plaintext)
		if decodeErr != nil {
			return nil, fmt.Errorf("session: decode turn state: %w", decodeErr)
		}
		if err := validateTurnState(state); err != nil {
			return nil, err
		}
		states = append(states, cloneTurnState(state))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: iterate turn states: %w", err)
	}
	return states, nil
}

func (store *Store) saveTurnState(
	ctx context.Context,
	state TurnState,
	replace bool,
) error {
	if err := validateTurnState(state); err != nil {
		return err
	}
	state.UpdatedAt = store.clock.Now()
	plaintext, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("session: encode turn state: %w", err)
	}
	defer zeroBytes(plaintext)
	envelope, err := store.encryptJSON(plaintext)
	if err != nil {
		return fmt.Errorf("session: encrypt turn state: %w", err)
	}
	defer zeroBytes(envelope)
	statement := `INSERT INTO turn_state(
		turn_id, actor_id, session_id, status, state, updated_at
	) VALUES (?, ?, ?, ?, ?, ?)`
	if replace {
		statement += ` ON CONFLICT(turn_id) DO UPDATE SET
			status = excluded.status,
			state = excluded.state,
			updated_at = excluded.updated_at`
	}
	_, err = store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		_, executeErr := db.ExecContext(
			runCtx, statement,
			state.TurnID.String(), state.ActorID.String(),
			state.SessionID.String(), string(state.Status),
			envelope, toMicros(state.UpdatedAt),
		)
		if executeErr != nil {
			return writeResult{err: fmt.Errorf("session: save turn state: %w", executeErr)}
		}
		return writeResult{}
	})
	return err
}

func (store *Store) encryptJSON(value []byte) ([]byte, error) {
	if len(value) == 0 || !json.Valid(value) {
		return nil, fmt.Errorf("session: encrypted state must be valid JSON")
	}
	return store.cipher.Encrypt(value)
}

func (store *Store) decryptJSON(envelope []byte, label string) (json.RawMessage, error) {
	defer zeroBytes(envelope)
	plaintext, err := store.cipher.Decrypt(envelope)
	if err != nil {
		return nil, fmt.Errorf("session: decrypt %s: %w", label, err)
	}
	if !json.Valid(plaintext) {
		zeroBytes(plaintext)
		return nil, fmt.Errorf("session: decrypted %s is invalid JSON", label)
	}
	return plaintext, nil
}

func validateTurnState(state TurnState) error {
	if state.TurnID == uuid.Nil || state.ActorID == uuid.Nil ||
		state.SessionID == uuid.Nil || strings.TrimSpace(state.Content) == "" ||
		!state.Status.valid() || state.UpdatedAt.IsZero() {
		return fmt.Errorf("session: invalid durable turn state")
	}
	switch state.Surface {
	case "":
		if state.ProjectID != uuid.Nil {
			return fmt.Errorf("session: legacy turn cannot declare a project")
		}
	case "general":
		if state.ProjectID != uuid.Nil {
			return fmt.Errorf("session: general turn cannot declare a project")
		}
	case "studio":
		if state.ProjectID == uuid.Nil {
			return fmt.Errorf("session: Studio turn requires a project")
		}
	default:
		return fmt.Errorf("session: invalid durable turn surface")
	}
	switch state.Origin {
	case "", "agent_schedule":
	default:
		return fmt.Errorf("session: invalid durable turn origin")
	}
	if len(state.Checkpoint) > 0 && !json.Valid(state.Checkpoint) {
		return fmt.Errorf("session: invalid durable turn checkpoint")
	}
	if len(state.Recovery) > 0 && !json.Valid(state.Recovery) {
		return fmt.Errorf("session: invalid durable turn recovery")
	}
	return nil
}

func (status TurnStatus) valid() bool {
	switch status {
	case TurnRunning, TurnRecovering, TurnIncomplete, TurnCompleted,
		TurnFailed, TurnCancelled, TurnInterrupted:
		return true
	default:
		return false
	}
}

func cloneTurnState(state TurnState) TurnState {
	state.Checkpoint = append(json.RawMessage(nil), state.Checkpoint...)
	state.Recovery = append(json.RawMessage(nil), state.Recovery...)
	return state
}
