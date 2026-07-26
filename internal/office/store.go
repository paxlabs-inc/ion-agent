package office

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	_ "modernc.org/sqlite"
)

// Store manages durable office metadata in SQLite.
type Store struct {
	db    *sql.DB
	clock types.Clock
}

// OpenStore opens or migrates the office metadata database.
func OpenStore(ctx context.Context, dbPath string, clock types.Clock) (*Store, error) {
	if clock == nil {
		return nil, fmt.Errorf("office: store clock is required")
	}
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("office: resolve store: %w", err)
	}
	parameters := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
	} {
		parameters.Add("_pragma", pragma)
	}
	dsn := (&url.URL{
		Scheme: "file", Path: absolute, RawQuery: parameters.Encode(),
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("office: open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("office: ping store: %w", err)
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("office: secure store: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, clock: clock}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func migrate(ctx context.Context, db *sql.DB) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL,
			title TEXT NOT NULL,
			kind TEXT NOT NULL,
			extension TEXT NOT NULL,
			current_version_id TEXT NOT NULL DEFAULT '',
			starred INTEGER NOT NULL DEFAULT 0,
			archived_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_actor ON documents(actor_id, deleted_at, updated_at)`,
		`CREATE TABLE IF NOT EXISTS document_versions (
			id TEXT PRIMARY KEY,
			document_id TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			extension TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			origin TEXT NOT NULL,
			engine_doc_key TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_versions_doc ON document_versions(document_id, sequence)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_versions_doc_sequence ON document_versions(document_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS editor_sessions (
			id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL,
			document_id TEXT NOT NULL,
			version_id TEXT NOT NULL,
			engine_doc_key TEXT NOT NULL,
			state TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			last_callback_at TEXT,
			opened_at TEXT NOT NULL,
			closed_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_doc ON editor_sessions(document_id, state)`,
		`CREATE TABLE IF NOT EXISTS save_callbacks (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			engine_key TEXT NOT NULL,
			status INTEGER NOT NULL,
			url_digest TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 1,
			outcome TEXT NOT NULL DEFAULT '',
			version_id TEXT,
			received_at TEXT NOT NULL,
			completed_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_callbacks_session ON save_callbacks(session_id, status)`,
		`CREATE TABLE IF NOT EXISTS templates (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			extension TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size_bytes INTEGER NOT NULL
		)`,
	}
	for _, m := range migrations {
		if _, err := db.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("office: migrate: %w", err)
		}
	}
	return nil
}

// --- Document operations ---

func (s *Store) CreateDocument(ctx context.Context, doc Document) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO documents (id, actor_id, title, kind, extension, current_version_id, starred, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		doc.ID.String(), doc.ActorID.String(), doc.Title, string(doc.Kind),
		doc.Extension, doc.CurrentVersionID.String(), boolToInt(doc.Starred),
		doc.CreatedAt.Format(time.RFC3339Nano), doc.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) CreateDocumentWithVersion(
	ctx context.Context,
	doc Document,
	version DocumentVersion,
) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO documents (id, actor_id, title, kind, extension, current_version_id, starred, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		doc.ID.String(), doc.ActorID.String(), doc.Title, string(doc.Kind),
		doc.Extension, doc.CurrentVersionID.String(), boolToInt(doc.Starred),
		doc.CreatedAt.Format(time.RFC3339Nano), doc.UpdatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO document_versions (id, document_id, actor_id, sequence, extension, mime_type, sha256, size_bytes, origin, engine_doc_key, created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID.String(), version.DocumentID.String(), version.ActorID.String(),
		version.Sequence, version.Extension, version.MIMEType, version.SHA256,
		version.SizeBytes, string(version.Origin), version.EngineDocKey,
		version.CreatedAt.Format(time.RFC3339Nano), version.CreatedBy,
	); err != nil {
		return err
	}
	return transaction.Commit()
}

func (s *Store) GetDocument(ctx context.Context, actorID, docID uuid.UUID) (Document, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, actor_id, title, kind, extension, current_version_id, starred, archived_at, created_at, updated_at, deleted_at
		 FROM documents WHERE id = ? AND actor_id = ? AND deleted_at IS NULL`,
		docID.String(), actorID.String(),
	)
	return scanDocument(row)
}

func (s *Store) ListDocuments(ctx context.Context, actorID uuid.UUID, archived bool, limit int) ([]Document, error) {
	query := `SELECT id, actor_id, title, kind, extension, current_version_id, starred, archived_at, created_at, updated_at, deleted_at
		      FROM documents WHERE actor_id = ? AND deleted_at IS NULL`
	if archived {
		query += ` AND archived_at IS NOT NULL`
	} else {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, query, actorID.String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := make([]Document, 0)
	for rows.Next() {
		doc, err := scanDocumentRows(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func (s *Store) UpdateDocument(ctx context.Context, actorID, docID uuid.UUID, title *string, starred *bool) error {
	var sets []string
	var args []any
	if title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *title)
	}
	if starred != nil {
		sets = append(sets, "starred = ?")
		args = append(args, boolToInt(*starred))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, s.clock.Now().UTC().Format(time.RFC3339Nano))
	args = append(args, docID.String(), actorID.String())
	result, err := s.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE documents SET %s WHERE id = ? AND actor_id = ? AND deleted_at IS NULL", strings.Join(sets, ", ")),
		args...,
	)
	return requireOneDocument(result, err)
}

func (s *Store) ArchiveDocument(ctx context.Context, actorID, docID uuid.UUID) error {
	now := s.clock.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx,
		`UPDATE documents SET archived_at = ?, updated_at = ? WHERE id = ? AND actor_id = ? AND deleted_at IS NULL`,
		now, now, docID.String(), actorID.String(),
	)
	return requireOneDocument(result, err)
}

func (s *Store) RestoreDocument(ctx context.Context, actorID, docID uuid.UUID) error {
	now := s.clock.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx,
		`UPDATE documents SET archived_at = NULL, updated_at = ? WHERE id = ? AND actor_id = ? AND deleted_at IS NULL`,
		now, docID.String(), actorID.String(),
	)
	return requireOneDocument(result, err)
}

func (s *Store) DeleteDocument(ctx context.Context, actorID, docID uuid.UUID) error {
	now := s.clock.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx,
		`UPDATE documents SET deleted_at = ?, updated_at = ? WHERE id = ? AND actor_id = ? AND deleted_at IS NULL`,
		now, now, docID.String(), actorID.String(),
	)
	return requireOneDocument(result, err)
}

func (s *Store) SetCurrentVersion(ctx context.Context, actorID, docID, versionID uuid.UUID) error {
	now := s.clock.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx,
		`UPDATE documents SET current_version_id = ?, updated_at = ? WHERE id = ? AND actor_id = ? AND deleted_at IS NULL`,
		versionID.String(), now, docID.String(), actorID.String(),
	)
	return requireOneDocument(result, err)
}

// --- Version operations ---

func (s *Store) CreateVersion(ctx context.Context, ver DocumentVersion) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO document_versions (id, document_id, actor_id, sequence, extension, mime_type, sha256, size_bytes, origin, engine_doc_key, created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ver.ID.String(), ver.DocumentID.String(), ver.ActorID.String(),
		ver.Sequence, ver.Extension, ver.MIMEType, ver.SHA256, ver.SizeBytes,
		string(ver.Origin), ver.EngineDocKey,
		ver.CreatedAt.Format(time.RFC3339Nano), ver.CreatedBy,
	)
	return err
}

func (s *Store) CommitVersion(
	ctx context.Context,
	actorID uuid.UUID,
	version DocumentVersion,
) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO document_versions (id, document_id, actor_id, sequence, extension, mime_type, sha256, size_bytes, origin, engine_doc_key, created_at, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID.String(), version.DocumentID.String(), version.ActorID.String(),
		version.Sequence, version.Extension, version.MIMEType, version.SHA256,
		version.SizeBytes, string(version.Origin), version.EngineDocKey,
		version.CreatedAt.Format(time.RFC3339Nano), version.CreatedBy,
	); err != nil {
		return err
	}
	result, err := transaction.ExecContext(ctx,
		`UPDATE documents SET current_version_id = ?, updated_at = ?
		 WHERE id = ? AND actor_id = ? AND deleted_at IS NULL`,
		version.ID.String(), version.CreatedAt.Format(time.RFC3339Nano),
		version.DocumentID.String(), actorID.String(),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return ErrNotFound
	}
	return transaction.Commit()
}

func (s *Store) GetVersion(ctx context.Context, docID, versionID uuid.UUID) (DocumentVersion, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, document_id, actor_id, sequence, extension, mime_type, sha256, size_bytes, origin, engine_doc_key, created_at, created_by
		 FROM document_versions WHERE id = ? AND document_id = ?`,
		versionID.String(), docID.String(),
	)
	return scanVersion(row)
}

func (s *Store) ListVersions(ctx context.Context, docID uuid.UUID, limit int) ([]DocumentVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, document_id, actor_id, sequence, extension, mime_type, sha256, size_bytes, origin, engine_doc_key, created_at, created_by
		 FROM document_versions WHERE document_id = ? ORDER BY sequence DESC LIMIT ?`,
		docID.String(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]DocumentVersion, 0)
	for rows.Next() {
		ver, err := scanVersionRows(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, ver)
	}
	return versions, rows.Err()
}

func (s *Store) NextSequence(ctx context.Context, docID uuid.UUID) (int, error) {
	var max sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(sequence) FROM document_versions WHERE document_id = ?`,
		docID.String(),
	).Scan(&max)
	if err != nil {
		return 0, err
	}
	if !max.Valid {
		return 1, nil
	}
	return int(max.Int64) + 1, nil
}

func (s *Store) CountVersions(ctx context.Context, docID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM document_versions WHERE document_id = ?`,
		docID.String(),
	).Scan(&count)
	return count, err
}

// --- Session operations ---

func (s *Store) CreateSession(ctx context.Context, sess EditorSession) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO editor_sessions (id, actor_id, document_id, version_id, engine_doc_key, state, expires_at, opened_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID.String(), sess.ActorID.String(), sess.DocumentID.String(),
		sess.VersionID.String(), sess.EngineDocKey, string(sess.State),
		sess.ExpiresAt.Format(time.RFC3339Nano), sess.OpenedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) GetSession(ctx context.Context, sessionID uuid.UUID) (EditorSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, actor_id, document_id, version_id, engine_doc_key, state, expires_at, last_callback_at, opened_at, closed_at
		 FROM editor_sessions WHERE id = ?`,
		sessionID.String(),
	)
	return scanSession(row)
}

func (s *Store) GetActiveSession(ctx context.Context, actorID, docID uuid.UUID) (EditorSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, actor_id, document_id, version_id, engine_doc_key, state, expires_at, last_callback_at, opened_at, closed_at
		 FROM editor_sessions WHERE actor_id = ? AND document_id = ? AND state = 'active'
		 ORDER BY opened_at DESC LIMIT 1`,
		actorID.String(), docID.String(),
	)
	return scanSession(row)
}

func (s *Store) ListActiveSessions(ctx context.Context) ([]EditorSession, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, actor_id, document_id, version_id, engine_doc_key, state, expires_at, last_callback_at, opened_at, closed_at
		 FROM editor_sessions WHERE state = 'active' ORDER BY opened_at LIMIT 200`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]EditorSession, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *Store) UpdateSessionState(ctx context.Context, sessionID uuid.UUID, state EditorSessionState) error {
	query := `UPDATE editor_sessions SET state = ?`
	args := []any{string(state)}
	if state == SessionStateClosed || state == SessionStateExpired || state == SessionStateError {
		now := s.clock.Now().UTC().Format(time.RFC3339Nano)
		query += `, closed_at = ?`
		args = append(args, now)
	}
	query += ` WHERE id = ?`
	args = append(args, sessionID.String())
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *Store) UpdateSessionCallback(ctx context.Context, sessionID uuid.UUID) error {
	now := s.clock.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE editor_sessions SET last_callback_at = ? WHERE id = ?`,
		now, sessionID.String(),
	)
	return err
}

func (s *Store) UpdateSessionVersion(
	ctx context.Context,
	sessionID, versionID uuid.UUID,
) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE editor_sessions SET version_id = ? WHERE id = ? AND state = 'active'`,
		versionID.String(), sessionID.String(),
	)
	return requireOneDocument(result, err)
}

// --- Callback operations ---

func (s *Store) CreateCallback(ctx context.Context, cb SaveCallback) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO save_callbacks (id, session_id, engine_key, status, url_digest, attempt, outcome, version_id, received_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cb.ID, cb.SessionID.String(), cb.EngineKey, int(cb.Status),
		cb.URLDigest, cb.Attempt, cb.Outcome, uuidPtrStr(cb.VersionID),
		cb.ReceivedAt.Format(time.RFC3339Nano), timePtrStr(cb.CompletedAt),
	)
	return err
}

func (s *Store) GetCallbackByDigest(ctx context.Context, sessionID uuid.UUID, urlDigest string) (SaveCallback, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, engine_key, status, url_digest, attempt, outcome, version_id, received_at, completed_at
		 FROM save_callbacks WHERE session_id = ? AND url_digest = ? AND outcome = 'committed'
		 ORDER BY received_at DESC LIMIT 1`,
		sessionID.String(), urlDigest,
	)
	return scanCallback(row)
}

func (s *Store) CompleteCallback(ctx context.Context, callbackID string, outcome string, versionID *uuid.UUID) error {
	now := s.clock.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE save_callbacks SET outcome = ?, version_id = ?, completed_at = ? WHERE id = ?`,
		outcome, uuidPtrStr(versionID), now, callbackID,
	)
	return err
}

// --- Template operations ---

func (s *Store) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, name, extension, sha256, size_bytes FROM templates ORDER BY kind, name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	templates := make([]Template, 0)
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &t.Extension, &t.SHA256, &t.SizeBytes); err != nil {
			return nil, err
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

func (s *Store) GetTemplate(ctx context.Context, templateID uuid.UUID) (Template, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, name, extension, sha256, size_bytes FROM templates WHERE id = ?`,
		templateID.String(),
	)
	var t Template
	err := row.Scan(&t.ID, &t.Kind, &t.Name, &t.Extension, &t.SHA256, &t.SizeBytes)
	return t, err
}

func (s *Store) UpsertTemplate(ctx context.Context, t Template) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO templates (id, kind, name, extension, sha256, size_bytes) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID.String(), string(t.Kind), t.Name, t.Extension, t.SHA256, t.SizeBytes,
	)
	return err
}

// --- Scan helpers ---

type scannable interface {
	Scan(dest ...any) error
}

func scanDocument(row scannable) (Document, error) {
	var d Document
	var id, actorID, currentVerID string
	var archivedAt, deletedAt sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&id, &actorID, &d.Title, &d.Kind, &d.Extension,
		&currentVerID, &d.Starred, &archivedAt, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return d, ErrNotFound
		}
		return d, err
	}
	d.ID = uuid.MustParse(id)
	d.ActorID = uuid.MustParse(actorID)
	if currentVerID != "" {
		d.CurrentVersionID = uuid.MustParse(currentVerID)
	}
	if archivedAt.Valid {
		t, err := parseDatabaseTime(archivedAt.String)
		if err != nil {
			return d, err
		}
		d.ArchivedAt = &t
	}
	d.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return d, err
	}
	d.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return d, err
	}
	if deletedAt.Valid {
		t, err := parseDatabaseTime(deletedAt.String)
		if err != nil {
			return d, err
		}
		d.DeletedAt = &t
	}
	return d, nil
}

func scanDocumentRows(rows *sql.Rows) (Document, error) {
	var d Document
	var id, actorID, currentVerID string
	var archivedAt, deletedAt sql.NullString
	var createdAt, updatedAt string
	err := rows.Scan(&id, &actorID, &d.Title, &d.Kind, &d.Extension,
		&currentVerID, &d.Starred, &archivedAt, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return d, err
	}
	d.ID = uuid.MustParse(id)
	d.ActorID = uuid.MustParse(actorID)
	if currentVerID != "" {
		d.CurrentVersionID = uuid.MustParse(currentVerID)
	}
	if archivedAt.Valid {
		t, err := parseDatabaseTime(archivedAt.String)
		if err != nil {
			return d, err
		}
		d.ArchivedAt = &t
	}
	d.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return d, err
	}
	d.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return d, err
	}
	if deletedAt.Valid {
		t, err := parseDatabaseTime(deletedAt.String)
		if err != nil {
			return d, err
		}
		d.DeletedAt = &t
	}
	return d, nil
}

func scanVersion(row scannable) (DocumentVersion, error) {
	var v DocumentVersion
	var id, docID, actorID string
	var createdAt string
	err := row.Scan(&id, &docID, &actorID, &v.Sequence, &v.Extension,
		&v.MIMEType, &v.SHA256, &v.SizeBytes, &v.Origin, &v.EngineDocKey,
		&createdAt, &v.CreatedBy)
	if err != nil {
		if err == sql.ErrNoRows {
			return v, ErrNotFound
		}
		return v, err
	}
	v.ID = uuid.MustParse(id)
	v.DocumentID = uuid.MustParse(docID)
	v.ActorID = uuid.MustParse(actorID)
	v.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return v, err
	}
	return v, nil
}

func scanVersionRows(rows *sql.Rows) (DocumentVersion, error) {
	var v DocumentVersion
	var id, docID, actorID string
	var createdAt string
	err := rows.Scan(&id, &docID, &actorID, &v.Sequence, &v.Extension,
		&v.MIMEType, &v.SHA256, &v.SizeBytes, &v.Origin, &v.EngineDocKey,
		&createdAt, &v.CreatedBy)
	if err != nil {
		return v, err
	}
	v.ID = uuid.MustParse(id)
	v.DocumentID = uuid.MustParse(docID)
	v.ActorID = uuid.MustParse(actorID)
	v.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return v, err
	}
	return v, nil
}

func scanSession(row scannable) (EditorSession, error) {
	var s EditorSession
	var id, actorID, docID, verID string
	var lastCB, closedAt sql.NullString
	var expiresAt, openedAt string
	err := row.Scan(&id, &actorID, &docID, &verID, &s.EngineDocKey,
		&s.State, &expiresAt, &lastCB, &openedAt, &closedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return s, ErrNotFound
		}
		return s, err
	}
	s.ID = uuid.MustParse(id)
	s.ActorID = uuid.MustParse(actorID)
	s.DocumentID = uuid.MustParse(docID)
	s.VersionID = uuid.MustParse(verID)
	s.ExpiresAt, err = parseDatabaseTime(expiresAt)
	if err != nil {
		return s, err
	}
	s.OpenedAt, err = parseDatabaseTime(openedAt)
	if err != nil {
		return s, err
	}
	if lastCB.Valid {
		t, err := parseDatabaseTime(lastCB.String)
		if err != nil {
			return s, err
		}
		s.LastCallbackAt = &t
	}
	if closedAt.Valid {
		t, err := parseDatabaseTime(closedAt.String)
		if err != nil {
			return s, err
		}
		s.ClosedAt = &t
	}
	return s, nil
}

func scanCallback(row scannable) (SaveCallback, error) {
	var cb SaveCallback
	var sessionID, versionID sql.NullString
	var receivedAt string
	var completedAt sql.NullString
	err := row.Scan(&cb.ID, &sessionID, &cb.EngineKey, &cb.Status,
		&cb.URLDigest, &cb.Attempt, &cb.Outcome, &versionID,
		&receivedAt, &completedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return cb, ErrNotFound
		}
		return cb, err
	}
	if sessionID.Valid {
		cb.SessionID = uuid.MustParse(sessionID.String)
	}
	if versionID.Valid {
		vid := uuid.MustParse(versionID.String)
		cb.VersionID = &vid
	}
	cb.ReceivedAt, err = parseDatabaseTime(receivedAt)
	if err != nil {
		return cb, err
	}
	if completedAt.Valid {
		t, err := parseDatabaseTime(completedAt.String)
		if err != nil {
			return cb, err
		}
		cb.CompletedAt = &t
	}
	return cb, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func uuidPtrStr(u *uuid.UUID) any {
	if u == nil {
		return nil
	}
	return u.String()
}

func timePtrStr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func parseDatabaseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("office: invalid stored timestamp")
	}
	return parsed, nil
}

func requireOneDocument(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}
