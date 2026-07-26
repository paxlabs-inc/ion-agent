package session

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const maxLivingStateBytes = 1 << 20

// LivingState is one decrypted, versioned liveness/identity document.
type LivingState struct {
	Kind  string
	Scope string
	State json.RawMessage
}

// SaveLivingState atomically replaces one independently scoped encrypted
// living-state document.
func (store *Store) SaveLivingState(
	ctx context.Context,
	kind string,
	scope string,
	state json.RawMessage,
) error {
	if err := store.checkOpen(); err != nil {
		return err
	}
	kind = strings.TrimSpace(kind)
	scope = strings.TrimSpace(scope)
	if kind == "" || scope == "" || len(state) == 0 ||
		len(state) > maxLivingStateBytes || !json.Valid(state) {
		return fmt.Errorf("session: valid bounded living state is required")
	}
	envelope, err := store.encryptJSON(state)
	if err != nil {
		return fmt.Errorf("session: encrypt living state: %w", err)
	}
	defer zeroBytes(envelope)
	keyID := livingStateKey(kind, scope)
	now := store.clock.Now()
	_, err = store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		_, executeErr := db.ExecContext(
			runCtx,
			`INSERT INTO living_state(key_id, kind, scope, state, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(kind, scope) DO UPDATE SET
			   key_id = excluded.key_id,
			   state = excluded.state,
			   updated_at = excluded.updated_at`,
			keyID, kind, scope, envelope, toMicros(now),
		)
		if executeErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: save living state: %w", executeErr,
			)}
		}
		return writeResult{}
	})
	return err
}

// LoadLivingState decrypts one exact kind/scope document.
func (store *Store) LoadLivingState(
	ctx context.Context,
	kind string,
	scope string,
) (json.RawMessage, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(kind)
	scope = strings.TrimSpace(scope)
	if kind == "" || scope == "" {
		return nil, fmt.Errorf("session: living state kind and scope are required")
	}
	var envelope []byte
	if err := store.readDB.QueryRowContext(
		ctx,
		`SELECT state FROM living_state WHERE kind = ? AND scope = ?`,
		kind, scope,
	).Scan(&envelope); err != nil {
		return nil, fmt.Errorf("session: load living state: %w", err)
	}
	return store.decryptJSON(envelope, "living state")
}

// ListLivingStates decrypts documents of one kind. Filtering is performed
// against the exact stored scope rather than decrypted content.
func (store *Store) ListLivingStates(
	ctx context.Context,
	kind string,
) ([]LivingState, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return nil, fmt.Errorf("session: living state kind is required")
	}
	rows, err := store.readDB.QueryContext(
		ctx,
		`SELECT scope, state FROM living_state
		 WHERE kind = ? ORDER BY scope`,
		kind,
	)
	if err != nil {
		return nil, fmt.Errorf("session: list living state: %w", err)
	}
	defer rows.Close()
	var result []LivingState
	for rows.Next() {
		var scope string
		var envelope []byte
		if err := rows.Scan(&scope, &envelope); err != nil {
			return nil, fmt.Errorf("session: scan living state: %w", err)
		}
		plaintext, err := store.decryptJSON(envelope, "living state")
		zeroBytes(envelope)
		if err != nil {
			return nil, err
		}
		result = append(result, LivingState{
			Kind: kind, Scope: scope, State: plaintext,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: iterate living state: %w", err)
	}
	return result, nil
}

// DeleteLivingState removes one exact encrypted document after its durable
// workflow has either committed or rolled back.
func (store *Store) DeleteLivingState(ctx context.Context, kind, scope string) error {
	if err := store.checkOpen(); err != nil {
		return err
	}
	kind, scope = strings.TrimSpace(kind), strings.TrimSpace(scope)
	if kind == "" || scope == "" {
		return fmt.Errorf("session: living state kind and scope are required")
	}
	_, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		_, executeErr := db.ExecContext(runCtx, `DELETE FROM living_state WHERE kind = ? AND scope = ?`, kind, scope)
		if executeErr != nil {
			return writeResult{err: fmt.Errorf("session: delete living state: %w", executeErr)}
		}
		return writeResult{}
	})
	return err
}

func livingStateKey(kind string, scope string) string {
	return kind + ":" + base64.RawURLEncoding.EncodeToString([]byte(scope))
}
