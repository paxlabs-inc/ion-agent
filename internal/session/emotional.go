package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
)

// SaveEmotionalState persists encrypted six-axis state across sessions.
func (store *Store) SaveEmotionalState(
	ctx context.Context,
	userID string,
	state *safety.EmotionalState,
) error {
	if err := store.checkOpen(); err != nil {
		return err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" || state == nil {
		return fmt.Errorf("session: user and emotional state are required")
	}
	now := store.clock.Now()
	if err := state.Decay(now); err != nil {
		return err
	}
	plaintext, err := json.Marshal(state.FullSnapshot())
	if err != nil {
		return fmt.Errorf("session: encode emotional state: %w", err)
	}
	defer zeroBytes(plaintext)
	envelope, err := store.cipher.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("session: encrypt emotional state: %w", err)
	}
	defer zeroBytes(envelope)
	_, err = store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		_, executeErr := db.ExecContext(
			runCtx,
			`INSERT INTO emotional_state(user_id, state, updated_at)
			 VALUES (?, ?, ?)
			 ON CONFLICT(user_id) DO UPDATE SET
			   state = excluded.state,
			   updated_at = excluded.updated_at`,
			userID,
			envelope,
			toMicros(now),
		)
		if executeErr != nil {
			return writeResult{
				err: fmt.Errorf("session: save emotional state: %w", executeErr),
			}
		}
		return writeResult{}
	})
	return err
}

// LoadEmotionalState decrypts state and applies elapsed decay on session start.
func (store *Store) LoadEmotionalState(
	ctx context.Context,
	userID string,
) (*safety.EmotionalState, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("session: user is required")
	}
	var envelope []byte
	if err := store.readDB.QueryRowContext(
		ctx,
		`SELECT state FROM emotional_state WHERE user_id = ?`,
		userID,
	).Scan(&envelope); err != nil {
		return nil, fmt.Errorf("session: load emotional state: %w", err)
	}
	defer zeroBytes(envelope)
	plaintext, err := store.cipher.Decrypt(envelope)
	if err != nil {
		return nil, fmt.Errorf("session: decrypt emotional state: %w", err)
	}
	defer zeroBytes(plaintext)
	var snapshot safety.EmotionalSnapshot
	if err := json.Unmarshal(plaintext, &snapshot); err != nil {
		return nil, fmt.Errorf("session: decode emotional state: %w", err)
	}
	state := safety.NewEmotionalState()
	state.UpdateAll(snapshot)
	state.SetEmergencyReset(snapshot.EmergencyReset)
	if err := state.Decay(store.clock.Now()); err != nil {
		return nil, err
	}
	return state, nil
}
