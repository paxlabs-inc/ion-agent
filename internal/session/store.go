package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	sqliteDriver "modernc.org/sqlite"
)

const (
	writerQueueCapacity = 64
	maxBusyRetries      = 3
	maxSessionListLimit = 100
)

// Cipher is the narrow encryption contract consumed by the session store.
type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

type writeRequest struct {
	ctx    context.Context
	run    func(context.Context, *sql.DB) writeResult
	result chan writeResult
}

type writeResult struct {
	session Session
	message Message
	created bool
	err     error
}

// Store owns a four-connection read pool and a separate single-connection
// writer serviced by one bounded goroutine.
type Store struct {
	readDB           *sql.DB
	writeDB          *sql.DB
	cipher           Cipher
	clock            types.Clock
	maxContextTokens int

	lifecycleMu sync.RWMutex
	closed      bool
	writes      chan writeRequest
	writerDone  chan struct{}
}

// Open initializes the SQLite store, applies embedded migrations, and starts
// its single writer.
func Open(
	ctx context.Context,
	path string,
	cipher Cipher,
	clock types.Clock,
	maxContextTokens int,
) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("session: database path is required")
	}
	if cipher == nil || clock == nil {
		return nil, fmt.Errorf("session: cipher and clock are required")
	}
	if maxContextTokens <= 0 {
		return nil, fmt.Errorf("session: max context tokens must be positive")
	}

	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, err
	}
	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("session: open writer: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	if err := writeDB.PingContext(ctx); err != nil {
		_ = writeDB.Close() // Best-effort cleanup after failed open.
		return nil, fmt.Errorf("session: connect writer: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = writeDB.Close() // Best-effort cleanup after insecure file permissions.
		return nil, fmt.Errorf("session: secure database permissions: %w", err)
	}
	if err := applyMigrations(ctx, writeDB, clock.Now()); err != nil {
		_ = writeDB.Close() // Best-effort cleanup after failed migration.
		return nil, err
	}

	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = writeDB.Close() // Best-effort cleanup after failed open.
		return nil, fmt.Errorf("session: open readers: %w", err)
	}
	readDB.SetMaxOpenConns(4)
	readDB.SetMaxIdleConns(4)
	if err := readDB.PingContext(ctx); err != nil {
		_ = readDB.Close()  // Best-effort cleanup after failed open.
		_ = writeDB.Close() // Best-effort cleanup after failed open.
		return nil, fmt.Errorf("session: connect readers: %w", err)
	}

	store := &Store{
		readDB:           readDB,
		writeDB:          writeDB,
		cipher:           cipher,
		clock:            clock,
		maxContextTokens: maxContextTokens,
		writes:           make(chan writeRequest, writerQueueCapacity),
		writerDone:       make(chan struct{}),
	}
	go store.writer()
	return store, nil
}

func sqliteDSN(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("session: resolve database path: %w", err)
	}
	parameters := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)",
		"wal_autocheckpoint(1000)",
		"busy_timeout(5000)",
		"synchronous(NORMAL)",
		"cache_size(-8000)",
		"mmap_size(268435456)",
		"foreign_keys(ON)",
	} {
		parameters.Add("_pragma", pragma)
	}
	location := &url.URL{Scheme: "file", Path: absolute, RawQuery: parameters.Encode()}
	return location.String(), nil
}

func (store *Store) writer() {
	defer close(store.writerDone)
	for request := range store.writes {
		if err := request.ctx.Err(); err != nil {
			request.result <- writeResult{err: err}
			continue
		}
		request.result <- runWithBusyRetry(request.ctx, store.writeDB, request.run)
	}
}

// CreateSession creates a root session or an explicitly linked child.
func (store *Store) CreateSession(ctx context.Context, parentID *uuid.UUID) (Session, error) {
	now := store.clock.Now()
	created := Session{
		ID:        uuid.New(),
		ParentID:  cloneUUID(parentID),
		CreatedAt: now,
		UpdatedAt: now,
	}
	result, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		var parentValue *string
		if created.ParentID != nil {
			value := created.ParentID.String()
			parentValue = &value
		}
		_, executeErr := db.ExecContext(
			runCtx,
			`INSERT INTO sessions(id, parent_id, created_at, updated_at, context_tokens)
			 VALUES (?, ?, ?, ?, 0)`,
			created.ID.String(),
			parentValue,
			toMicros(now),
			toMicros(now),
		)
		if executeErr != nil {
			return writeResult{err: fmt.Errorf("session: create session: %w", executeErr)}
		}
		return writeResult{session: created}
	})
	if err != nil {
		return Session{}, err
	}
	return result.session, nil
}

// BranchSession creates a linked child and copies the selected plaintext
// transcript into freshly encrypted rows in one transaction. A failed
// encryption, insert, or commit leaves neither a partial child nor a partial
// transcript behind.
func (store *Store) BranchSession(
	ctx context.Context,
	parentID uuid.UUID,
	messages []Message,
) (Session, error) {
	if parentID == uuid.Nil {
		return Session{}, fmt.Errorf("session: branch parent is required")
	}
	now := store.clock.Now()
	created := Session{
		ID:        uuid.New(),
		ParentID:  cloneUUID(&parentID),
		CreatedAt: now,
		UpdatedAt: now,
	}
	copies := make([]Message, 0, len(messages))
	for _, source := range messages {
		message, err := NewMessage(
			created.ID,
			source.Role,
			source.MemoryType,
			source.Content,
			now,
		)
		if err != nil {
			for index := range copies {
				zeroBytes(copies[index].Content)
			}
			return Session{}, fmt.Errorf("session: validate branch message: %w", err)
		}
		copies = append(copies, message)
	}
	defer func() {
		for index := range copies {
			zeroBytes(copies[index].Content)
		}
	}()

	result, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		tx, beginErr := db.BeginTx(runCtx, nil)
		if beginErr != nil {
			return writeResult{err: fmt.Errorf("session: begin branch: %w", beginErr)}
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		if _, insertErr := tx.ExecContext(
			runCtx,
			`INSERT INTO sessions(id, parent_id, created_at, updated_at, context_tokens)
			 VALUES (?, ?, ?, ?, 0)`,
			created.ID.String(),
			parentID.String(),
			toMicros(created.CreatedAt),
			toMicros(created.UpdatedAt),
		); insertErr != nil {
			return writeResult{err: fmt.Errorf("session: create branch: %w", insertErr)}
		}
		for _, message := range copies {
			envelope, encryptErr := store.cipher.Encrypt(message.Content)
			if encryptErr != nil {
				return writeResult{err: fmt.Errorf(
					"session: encrypt branch message: %w", encryptErr,
				)}
			}
			_, insertErr := tx.ExecContext(
				runCtx,
				`INSERT INTO messages(id, session_id, role, memory_type, content, created_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				message.ID.String(),
				created.ID.String(),
				string(message.Role),
				string(message.MemoryType),
				envelope,
				toMicros(message.CreatedAt),
			)
			zeroBytes(envelope)
			if insertErr != nil {
				return writeResult{err: fmt.Errorf(
					"session: insert branch message: %w", insertErr,
				)}
			}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return writeResult{err: fmt.Errorf("session: commit branch: %w", commitErr)}
		}
		committed = true
		return writeResult{session: created}
	})
	if err != nil {
		return Session{}, err
	}
	return result.session, nil
}

// AppendMessage encrypts content and writes it through the single writer. When
// contextTokens reaches 75% of the model context, it first creates a linked
// child and stores the message in that continuation.
func (store *Store) AppendMessage(
	ctx context.Context,
	sessionID uuid.UUID,
	role Role,
	memoryType MemoryType,
	content []byte,
	contextTokens int,
) (Message, error) {
	return store.appendMessage(
		ctx, sessionID, nil, role, memoryType, content, contextTokens,
	)
}

// AppendTurnMessage durably associates user-visible turn output with its
// encrypted transcript row so reconnect and reload can restore streamed state.
func (store *Store) AppendTurnMessage(
	ctx context.Context,
	sessionID uuid.UUID,
	turnID uuid.UUID,
	role Role,
	memoryType MemoryType,
	content []byte,
	contextTokens int,
) (Message, error) {
	if turnID == uuid.Nil {
		return Message{}, fmt.Errorf("session: turn ID is required")
	}
	return store.appendMessage(
		ctx, sessionID, &turnID, role, memoryType, content, contextTokens,
	)
}

func (store *Store) appendMessage(
	ctx context.Context,
	sessionID uuid.UUID,
	turnID *uuid.UUID,
	role Role,
	memoryType MemoryType,
	content []byte,
	contextTokens int,
) (Message, error) {
	if contextTokens < 0 {
		return Message{}, fmt.Errorf("session: context tokens cannot be negative")
	}
	message, err := NewMessage(sessionID, role, memoryType, content, store.clock.Now())
	if err != nil {
		return Message{}, err
	}
	message.TurnID = cloneUUID(turnID)
	plaintext := append([]byte(nil), message.Content...)

	result, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		message.SessionID = sessionID
		tx, beginErr := db.BeginTx(runCtx, nil)
		if beginErr != nil {
			return writeResult{err: fmt.Errorf("session: begin message write: %w", beginErr)}
		}
		committed := false
		defer func() {
			if !committed {
				// Best-effort rollback after the primary transaction error.
				_ = tx.Rollback()
			}
		}()

		activeID := sessionID
		if reachedSplitThreshold(contextTokens, store.maxContextTokens) {
			activeID = uuid.New()
			now := store.clock.Now()
			if _, insertErr := tx.ExecContext(
				runCtx,
				`INSERT INTO sessions(id, parent_id, created_at, updated_at, context_tokens)
				 VALUES (?, ?, ?, ?, 0)`,
				activeID.String(),
				sessionID.String(),
				toMicros(now),
				toMicros(now),
			); insertErr != nil {
				return writeResult{err: fmt.Errorf("session: create compressed continuation: %w", insertErr)}
			}
			message.SessionID = activeID
		}

		envelope, encryptErr := store.cipher.Encrypt(plaintext)
		if encryptErr != nil {
			return writeResult{err: fmt.Errorf("session: encrypt message: %w", encryptErr)}
		}
		defer zeroBytes(envelope)
		if _, insertErr := tx.ExecContext(
			runCtx,
			`INSERT INTO messages(
				id, session_id, role, memory_type, content, created_at, turn_id
			 ) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			message.ID.String(),
			message.SessionID.String(),
			string(message.Role),
			string(message.MemoryType),
			envelope,
			toMicros(message.CreatedAt),
			uuidText(message.TurnID),
		); insertErr != nil {
			return writeResult{err: fmt.Errorf("session: insert message: %w", insertErr)}
		}
		if _, updateErr := tx.ExecContext(
			runCtx,
			`UPDATE sessions SET context_tokens = ?, updated_at = ? WHERE id = ?`,
			contextTokens,
			toMicros(message.CreatedAt),
			activeID.String(),
		); updateErr != nil {
			return writeResult{err: fmt.Errorf("session: update context usage: %w", updateErr)}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return writeResult{err: fmt.Errorf("session: commit message: %w", commitErr)}
		}
		committed = true
		message.Content = append([]byte(nil), plaintext...)
		return writeResult{message: message}
	})
	zeroBytes(plaintext)
	if err != nil {
		return Message{}, err
	}
	return result.message, nil
}

// GetSession reads one session from the four-connection read pool.
func (store *Store) GetSession(ctx context.Context, id uuid.UUID) (Session, error) {
	if err := store.checkOpen(); err != nil {
		return Session{}, err
	}
	row := store.readDB.QueryRowContext(
		ctx,
		`SELECT s.id, s.parent_id, s.created_at, s.updated_at,
		        s.context_tokens, m.title, m.archived_at
		 FROM sessions AS s
		 LEFT JOIN session_metadata AS m ON m.session_id = s.id
		 WHERE s.id = ?`,
		id.String(),
	)
	found, err := scanSession(row, store.cipher)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("session: get session: %w", err)
	}
	return found, nil
}

// ListMessages returns decrypted messages in chronological order.
func (store *Store) ListMessages(ctx context.Context, sessionID uuid.UUID) ([]Message, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	rows, err := store.readDB.QueryContext(
		ctx,
		`SELECT id, session_id, role, memory_type, content, created_at, turn_id
		 FROM messages WHERE session_id = ? ORDER BY created_at, rowid`,
		sessionID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("session: list messages: %w", err)
	}
	defer func() {
		// Rows are exhausted below; Close only releases the connection.
		_ = rows.Close()
	}()
	return store.scanMessages(rows)
}

// ListSessions returns the most recently active encrypted conversation scopes.
// Session metadata contains no message content; callers that are authorized to
// show a transcript preview must load messages through ListMessages.
func (store *Store) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxSessionListLimit {
		return nil, fmt.Errorf(
			"session: list limit must be between 1 and %d",
			maxSessionListLimit,
		)
	}
	rows, err := store.readDB.QueryContext(
		ctx,
		`SELECT s.id, s.parent_id, s.created_at, s.updated_at,
		        s.context_tokens, m.title, m.archived_at
		 FROM sessions AS s
		 LEFT JOIN session_metadata AS m ON m.session_id = s.id
		 WHERE m.archived_at IS NULL
		 ORDER BY s.updated_at DESC, s.created_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("session: list sessions: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()
	sessions := make([]Session, 0)
	for rows.Next() {
		found, scanErr := scanSession(rows, store.cipher)
		if scanErr != nil {
			return nil, fmt.Errorf("session: scan session: %w", scanErr)
		}
		sessions = append(sessions, found)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: iterate sessions: %w", err)
	}
	return sessions, nil
}

// ListArchivedSessions returns the most recently active archived conversations.
func (store *Store) ListArchivedSessions(
	ctx context.Context,
	limit int,
) ([]Session, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxSessionListLimit {
		return nil, fmt.Errorf(
			"session: list limit must be between 1 and %d",
			maxSessionListLimit,
		)
	}
	rows, err := store.readDB.QueryContext(
		ctx,
		`SELECT s.id, s.parent_id, s.created_at, s.updated_at,
		        s.context_tokens, m.title, m.archived_at
		 FROM sessions AS s
		 JOIN session_metadata AS m ON m.session_id = s.id
		 WHERE m.archived_at IS NOT NULL
		 ORDER BY m.archived_at DESC, s.updated_at DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("session: list archived sessions: %w", err)
	}
	defer rows.Close()
	sessions := make([]Session, 0)
	for rows.Next() {
		found, scanErr := scanSession(rows, store.cipher)
		if scanErr != nil {
			return nil, fmt.Errorf("session: scan archived session: %w", scanErr)
		}
		sessions = append(sessions, found)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: iterate archived sessions: %w", err)
	}
	return sessions, nil
}

// RenameSession stores an encrypted operator-supplied conversation title.
func (store *Store) RenameSession(
	ctx context.Context,
	id uuid.UUID,
	title string,
) (Session, error) {
	title = strings.Join(strings.Fields(title), " ")
	if id == uuid.Nil || title == "" {
		return Session{}, fmt.Errorf("session: session ID and title are required")
	}
	if len([]rune(title)) > 120 {
		return Session{}, fmt.Errorf("session: title exceeds 120 characters")
	}
	envelope, err := store.cipher.Encrypt([]byte(title))
	if err != nil {
		return Session{}, fmt.Errorf("session: encrypt title: %w", err)
	}
	defer zeroBytes(envelope)
	now := store.clock.Now()
	_, err = store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		tx, beginErr := db.BeginTx(runCtx, nil)
		if beginErr != nil {
			return writeResult{err: fmt.Errorf("session: begin rename: %w", beginErr)}
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		result, updateErr := tx.ExecContext(
			runCtx,
			`UPDATE sessions SET updated_at = ? WHERE id = ?`,
			toMicros(now),
			id.String(),
		)
		if updateErr != nil {
			return writeResult{err: fmt.Errorf("session: touch renamed session: %w", updateErr)}
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return writeResult{err: fmt.Errorf("session: inspect rename target: %w", affectedErr)}
		}
		if affected != 1 {
			return writeResult{err: ErrNotFound}
		}
		if _, upsertErr := tx.ExecContext(
			runCtx,
			`INSERT INTO session_metadata(session_id, title)
			 VALUES (?, ?)
			 ON CONFLICT(session_id) DO UPDATE SET title = excluded.title`,
			id.String(),
			envelope,
		); upsertErr != nil {
			return writeResult{err: fmt.Errorf("session: store title: %w", upsertErr)}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return writeResult{err: fmt.Errorf("session: commit rename: %w", commitErr)}
		}
		committed = true
		return writeResult{}
	})
	if err != nil {
		return Session{}, err
	}
	return store.GetSession(ctx, id)
}

// ArchiveSession hides or restores a conversation without deleting its
// encrypted transcript or durable turn state.
func (store *Store) ArchiveSession(
	ctx context.Context,
	id uuid.UUID,
	archived bool,
) (Session, error) {
	if id == uuid.Nil {
		return Session{}, fmt.Errorf("session: session ID is required")
	}
	now := store.clock.Now()
	var archivedAt any
	if archived {
		archivedAt = toMicros(now)
	}
	_, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		tx, beginErr := db.BeginTx(runCtx, nil)
		if beginErr != nil {
			return writeResult{err: fmt.Errorf("session: begin archive update: %w", beginErr)}
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		if archived {
			var active int
			if countErr := tx.QueryRowContext(
				runCtx,
				`SELECT COUNT(*) FROM turn_state
				 WHERE session_id = ? AND status IN ('running', 'recovering')`,
				id.String(),
			).Scan(&active); countErr != nil {
				return writeResult{err: fmt.Errorf(
					"session: inspect active turns before archive: %w", countErr,
				)}
			}
			if active > 0 {
				return writeResult{err: fmt.Errorf(
					"session: stop the active turn before archiving",
				)}
			}
		}
		result, updateErr := tx.ExecContext(
			runCtx,
			`UPDATE sessions SET updated_at = ? WHERE id = ?`,
			toMicros(now),
			id.String(),
		)
		if updateErr != nil {
			return writeResult{err: fmt.Errorf("session: touch archive target: %w", updateErr)}
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return writeResult{err: fmt.Errorf("session: inspect archive target: %w", affectedErr)}
		}
		if affected != 1 {
			return writeResult{err: ErrNotFound}
		}
		if _, upsertErr := tx.ExecContext(
			runCtx,
			`INSERT INTO session_metadata(session_id, archived_at)
			 VALUES (?, ?)
			 ON CONFLICT(session_id) DO UPDATE
			 SET archived_at = excluded.archived_at`,
			id.String(),
			archivedAt,
		); upsertErr != nil {
			return writeResult{err: fmt.Errorf("session: update archive state: %w", upsertErr)}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return writeResult{err: fmt.Errorf("session: commit archive update: %w", commitErr)}
		}
		committed = true
		return writeResult{}
	})
	if err != nil {
		return Session{}, err
	}
	return store.GetSession(ctx, id)
}

// DeleteSession permanently removes only the selected conversation. Child
// branches are detached first so deleting a parent never cascades into them.
func (store *Store) DeleteSession(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("session: session ID is required")
	}
	_, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		tx, beginErr := db.BeginTx(runCtx, nil)
		if beginErr != nil {
			return writeResult{err: fmt.Errorf("session: begin delete: %w", beginErr)}
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		var active int
		if countErr := tx.QueryRowContext(
			runCtx,
			`SELECT COUNT(*) FROM turn_state
			 WHERE session_id = ? AND status IN ('running', 'recovering')`,
			id.String(),
		).Scan(&active); countErr != nil {
			return writeResult{err: fmt.Errorf(
				"session: inspect active turns before delete: %w", countErr,
			)}
		}
		if active > 0 {
			return writeResult{err: fmt.Errorf(
				"session: stop the active turn before deleting",
			)}
		}
		if _, detachErr := tx.ExecContext(
			runCtx,
			`UPDATE sessions SET parent_id = NULL WHERE parent_id = ?`,
			id.String(),
		); detachErr != nil {
			return writeResult{err: fmt.Errorf("session: detach child branches: %w", detachErr)}
		}
		result, deleteErr := tx.ExecContext(
			runCtx,
			`DELETE FROM sessions WHERE id = ?`,
			id.String(),
		)
		if deleteErr != nil {
			return writeResult{err: fmt.Errorf("session: delete conversation: %w", deleteErr)}
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return writeResult{err: fmt.Errorf("session: inspect delete target: %w", affectedErr)}
		}
		if affected != 1 {
			return writeResult{err: ErrNotFound}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return writeResult{err: fmt.Errorf("session: commit delete: %w", commitErr)}
		}
		committed = true
		return writeResult{}
	})
	return err
}

// ListAllSessions returns metadata for every retained session. It is intended
// for maintenance passes that must not silently omit older cognition state.
func (store *Store) ListAllSessions(ctx context.Context) ([]Session, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	rows, err := store.readDB.QueryContext(
		ctx,
		`SELECT s.id, s.parent_id, s.created_at, s.updated_at,
		        s.context_tokens, m.title, m.archived_at
		 FROM sessions AS s
		 LEFT JOIN session_metadata AS m ON m.session_id = s.id
		 ORDER BY s.updated_at DESC, s.created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("session: list all sessions: %w", err)
	}
	defer rows.Close()
	sessions := make([]Session, 0)
	for rows.Next() {
		found, err := scanSession(rows, store.cipher)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, found)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: iterate all sessions: %w", err)
	}
	return sessions, nil
}

// SearchMetadataInSession performs an FTS5 search over non-sensitive metadata
// only within one session boundary, then decrypts matched rows in memory.
// Cross-session search requires a separately authorized higher-level path.
func (store *Store) SearchMetadataInSession(
	ctx context.Context,
	sessionID uuid.UUID,
	query string,
	limit int,
) ([]Message, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	if sessionID == uuid.Nil || strings.TrimSpace(query) == "" || limit <= 0 {
		return nil, fmt.Errorf(
			"session: session ID, non-empty query, and positive limit are required",
		)
	}
	rows, err := store.readDB.QueryContext(
		ctx,
		`SELECT m.id, m.session_id, m.role, m.memory_type, m.content,
		        m.created_at, m.turn_id
		 FROM message_metadata_fts AS f
		 JOIN messages AS m ON m.id = f.message_id
		 WHERE message_metadata_fts MATCH ? AND m.session_id = ?
		 ORDER BY rank LIMIT ?`,
		query,
		sessionID.String(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("session: search metadata: %w", err)
	}
	defer func() {
		// Rows are exhausted below; Close only releases the connection.
		_ = rows.Close()
	}()
	return store.scanMessages(rows)
}

// SearchMetadataAcrossSessions performs an explicitly authorized multi-session
// FTS query. Only caller-supplied session IDs are eligible, preserving the
// gateway's user/session isolation boundary.
func (store *Store) SearchMetadataAcrossSessions(
	ctx context.Context,
	sessionIDs []uuid.UUID,
	query string,
	limit int,
) ([]Message, error) {
	if err := store.checkOpen(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" || limit <= 0 {
		return nil, fmt.Errorf("session: non-empty query and positive limit are required")
	}
	unique := make([]uuid.UUID, 0, len(sessionIDs))
	seen := make(map[uuid.UUID]struct{}, len(sessionIDs))
	for _, id := range sessionIDs {
		if id == uuid.Nil {
			return nil, fmt.Errorf("session: authorized session ID cannot be empty")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 || len(unique) > 64 {
		return nil, fmt.Errorf("session: 1 to 64 authorized sessions are required")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")
	statement := `SELECT m.id, m.session_id, m.role, m.memory_type, m.content,
		        m.created_at, m.turn_id
		 FROM message_metadata_fts AS f
		 JOIN messages AS m ON m.id = f.message_id
		 WHERE message_metadata_fts MATCH ? AND m.session_id IN (` +
		placeholders + `)
		 ORDER BY rank LIMIT ?`
	arguments := make([]any, 0, len(unique)+2)
	arguments = append(arguments, query)
	for _, id := range unique {
		arguments = append(arguments, id.String())
	}
	arguments = append(arguments, limit)
	rows, err := store.readDB.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("session: cross-session metadata search: %w", err)
	}
	defer rows.Close()
	return store.scanMessages(rows)
}

func (store *Store) scanMessages(rows *sql.Rows) ([]Message, error) {
	messages := make([]Message, 0)
	for rows.Next() {
		var (
			idText        string
			sessionIDText string
			roleText      string
			memoryText    string
			envelope      []byte
			createdMicros int64
			turnIDText    sql.NullString
		)
		if err := rows.Scan(
			&idText,
			&sessionIDText,
			&roleText,
			&memoryText,
			&envelope,
			&createdMicros,
			&turnIDText,
		); err != nil {
			return nil, fmt.Errorf("session: scan message: %w", err)
		}
		content, err := store.cipher.Decrypt(envelope)
		zeroBytes(envelope)
		if err != nil {
			return nil, fmt.Errorf("session: decrypt message: %w", err)
		}
		id, err := uuid.Parse(idText)
		if err != nil {
			zeroBytes(content)
			return nil, fmt.Errorf("session: parse message ID: %w", err)
		}
		sessionID, err := uuid.Parse(sessionIDText)
		if err != nil {
			zeroBytes(content)
			return nil, fmt.Errorf("session: parse message session ID: %w", err)
		}
		var turnID *uuid.UUID
		if turnIDText.Valid {
			parsed, parseErr := uuid.Parse(turnIDText.String)
			if parseErr != nil {
				zeroBytes(content)
				return nil, fmt.Errorf("session: parse message turn ID: %w", parseErr)
			}
			turnID = &parsed
		}
		messages = append(messages, Message{
			ID:         id,
			SessionID:  sessionID,
			TurnID:     turnID,
			Role:       Role(roleText),
			MemoryType: MemoryType(memoryText),
			Content:    content,
			CreatedAt:  fromMicros(createdMicros),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: iterate messages: %w", err)
	}
	return messages, nil
}

// RewrapEnvelopes implements vault.EnvelopeRewrapper. Every row is updated in
// one SQLite transaction; an error rolls the entire rotation back.
func (store *Store) RewrapEnvelopes(ctx context.Context, oldKey, newKey []byte) error {
	_, err := store.submit(ctx, func(runCtx context.Context, db *sql.DB) writeResult {
		tx, beginErr := db.BeginTx(runCtx, nil)
		if beginErr != nil {
			return writeResult{err: fmt.Errorf("session: begin User Key rotation: %w", beginErr)}
		}
		committed := false
		defer func() {
			if !committed {
				// Best-effort rollback after the primary transaction error.
				_ = tx.Rollback()
			}
		}()
		rows, queryErr := tx.QueryContext(runCtx, `
			SELECT 'messages', id, content FROM messages
			UNION ALL
			SELECT 'emotional_state', user_id, state FROM emotional_state
			UNION ALL
			SELECT 'cognition_state', session_id, state FROM cognition_state
			UNION ALL
			SELECT 'turn_state', turn_id, state FROM turn_state
			UNION ALL
			SELECT 'living_state', key_id, state FROM living_state
			UNION ALL
			SELECT 'session_metadata', session_id, title
			FROM session_metadata WHERE title IS NOT NULL`)
		if queryErr != nil {
			return writeResult{err: fmt.Errorf("session: enumerate encrypted rows: %w", queryErr)}
		}
		type replacement struct {
			table    string
			id       string
			envelope []byte
		}
		replacements := make([]replacement, 0)
		for rows.Next() {
			var item replacement
			var current []byte
			if scanErr := rows.Scan(&item.table, &item.id, &current); scanErr != nil {
				_ = rows.Close() // Best-effort cleanup after scan failure.
				return writeResult{err: fmt.Errorf("session: scan encrypted row: %w", scanErr)}
			}
			rewrapped, rewrapErr := vault.Rewrap(oldKey, newKey, current)
			zeroBytes(current)
			if rewrapErr != nil {
				_ = rows.Close() // Best-effort cleanup after rewrap failure.
				return writeResult{err: rewrapErr}
			}
			item.envelope = rewrapped
			replacements = append(replacements, item)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close() // Best-effort cleanup after iteration failure.
			return writeResult{err: fmt.Errorf("session: iterate encrypted rows: %w", rowsErr)}
		}
		if closeErr := rows.Close(); closeErr != nil {
			return writeResult{err: fmt.Errorf("session: close encrypted rows: %w", closeErr)}
		}
		defer func() {
			for _, item := range replacements {
				zeroBytes(item.envelope)
			}
		}()
		for _, item := range replacements {
			column := "state"
			key := "turn_id"
			if item.table == "messages" {
				column = "content"
				key = "id"
			} else if item.table == "emotional_state" {
				key = "user_id"
			} else if item.table == "cognition_state" {
				key = "session_id"
			} else if item.table == "living_state" {
				key = "key_id"
			} else if item.table == "session_metadata" {
				column = "title"
				key = "session_id"
			}
			if _, updateErr := tx.ExecContext(
				runCtx,
				`UPDATE `+item.table+` SET `+column+` = ? WHERE `+key+` = ?`,
				item.envelope,
				item.id,
			); updateErr != nil {
				return writeResult{err: fmt.Errorf("session: rewrap encrypted row: %w", updateErr)}
			}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return writeResult{err: fmt.Errorf("session: commit User Key rotation: %w", commitErr)}
		}
		committed = true
		return writeResult{}
	})
	return err
}

func (store *Store) submit(
	ctx context.Context,
	run func(context.Context, *sql.DB) writeResult,
) (writeResult, error) {
	if err := ctx.Err(); err != nil {
		return writeResult{}, err
	}
	request := writeRequest{
		ctx:    ctx,
		run:    run,
		result: make(chan writeResult, 1),
	}
	store.lifecycleMu.RLock()
	defer store.lifecycleMu.RUnlock()
	if store.closed {
		return writeResult{}, ErrClosed
	}
	select {
	case store.writes <- request:
	case <-ctx.Done():
		return writeResult{}, ctx.Err()
	}
	result := <-request.result
	return result, result.err
}

func (store *Store) checkOpen() error {
	store.lifecycleMu.RLock()
	defer store.lifecycleMu.RUnlock()
	if store.closed {
		return ErrClosed
	}
	return nil
}

// Close drains the writer queue, checkpoints WAL, and closes all connections.
func (store *Store) Close(ctx context.Context) error {
	store.lifecycleMu.Lock()
	if store.closed {
		store.lifecycleMu.Unlock()
		return nil
	}
	store.closed = true
	close(store.writes)
	store.lifecycleMu.Unlock()

	select {
	case <-store.writerDone:
	case <-ctx.Done():
		return fmt.Errorf("session: wait for writer: %w", ctx.Err())
	}
	var closeErrors []error
	if _, err := store.writeDB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		closeErrors = append(closeErrors, fmt.Errorf("session: checkpoint WAL: %w", err))
	}
	if err := store.readDB.Close(); err != nil {
		closeErrors = append(closeErrors, fmt.Errorf("session: close readers: %w", err))
	}
	if err := store.writeDB.Close(); err != nil {
		closeErrors = append(closeErrors, fmt.Errorf("session: close writer: %w", err))
	}
	return errors.Join(closeErrors...)
}

func runWithBusyRetry(
	ctx context.Context,
	db *sql.DB,
	run func(context.Context, *sql.DB) writeResult,
) writeResult {
	for attempt := 0; attempt <= maxBusyRetries; attempt++ {
		result := run(ctx, db)
		if !isSQLiteBusy(result.err) || attempt == maxBusyRetries {
			return result
		}
		delay := 10 * time.Millisecond * time.Duration(1<<attempt)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return writeResult{err: ctx.Err()}
		}
	}
	return writeResult{err: fmt.Errorf("session: unreachable busy retry state")}
}

func isSQLiteBusy(err error) bool {
	var sqliteError *sqliteDriver.Error
	if !errors.As(err, &sqliteError) {
		return false
	}
	const (
		sqliteBusy   = 5
		sqliteLocked = 6
	)
	baseCode := sqliteError.Code() & 0xff
	return baseCode == sqliteBusy || baseCode == sqliteLocked
}

func reachedSplitThreshold(contextTokens, maximum int) bool {
	return int64(contextTokens)*4 >= int64(maximum)*3
}

func cloneUUID(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	copyID := *id
	return &copyID
}

func toMicros(value time.Time) int64 {
	return value.UnixMicro()
}

func fromMicros(value int64) time.Time {
	return time.UnixMicro(value).UTC()
}

func zeroBytes(content []byte) {
	for index := range content {
		content[index] = 0
	}
}

type rowScanner interface {
	Scan(...any) error
}

func scanSession(row rowScanner, cipher Cipher) (Session, error) {
	var (
		idText        string
		parentText    sql.NullString
		createdMicros int64
		updatedMicros int64
		contextTokens int
		titleEnvelope []byte
		archivedAt    sql.NullInt64
	)
	if err := row.Scan(
		&idText,
		&parentText,
		&createdMicros,
		&updatedMicros,
		&contextTokens,
		&titleEnvelope,
		&archivedAt,
	); err != nil {
		return Session{}, err
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return Session{}, err
	}
	var parentID *uuid.UUID
	if parentText.Valid {
		parsed, parseErr := uuid.Parse(parentText.String)
		if parseErr != nil {
			return Session{}, parseErr
		}
		parentID = &parsed
	}
	title := ""
	if len(titleEnvelope) > 0 {
		if cipher == nil {
			return Session{}, fmt.Errorf("session: title cipher is required")
		}
		plaintext, decryptErr := cipher.Decrypt(titleEnvelope)
		zeroBytes(titleEnvelope)
		if decryptErr != nil {
			return Session{}, fmt.Errorf("session: decrypt title: %w", decryptErr)
		}
		title = string(plaintext)
		zeroBytes(plaintext)
	}
	var archived *time.Time
	if archivedAt.Valid {
		value := fromMicros(archivedAt.Int64)
		archived = &value
	}
	return Session{
		ID:            id,
		ParentID:      parentID,
		Title:         title,
		ArchivedAt:    archived,
		CreatedAt:     fromMicros(createdMicros),
		UpdatedAt:     fromMicros(updatedMicros),
		ContextTokens: contextTokens,
	}, nil
}

func uuidText(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return value.String()
}
