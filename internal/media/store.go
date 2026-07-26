package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

type Store struct {
	db     *sql.DB
	cipher Cipher
	mu     sync.Mutex
}

func OpenStore(ctx context.Context, path string, cipher Cipher) (*Store, error) {
	if path == "" || cipher == nil {
		return nil, fmt.Errorf("media: database path and cipher are required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("media: resolve database path: %w", err)
	}
	parameters := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)",
		"foreign_keys(ON)",
	} {
		parameters.Add("_pragma", pragma)
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: parameters.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("media: open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("media: connect database: %w", err)
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("media: secure database: %w", err)
	}
	schema := `
CREATE TABLE IF NOT EXISTS media_jobs (
  id TEXT PRIMARY KEY,
  actor_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  provider TEXT NOT NULL,
  provider_task_id TEXT NOT NULL DEFAULT '',
  request_envelope BLOB NOT NULL,
  request_view_envelope BLOB NOT NULL,
  progress INTEGER NOT NULL DEFAULT 0,
  error_message TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  completed_at INTEGER
);
CREATE INDEX IF NOT EXISTS media_jobs_actor_updated
  ON media_jobs(actor_id, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS media_jobs_actor_idempotency
  ON media_jobs(actor_id, idempotency_key);
CREATE TABLE IF NOT EXISTS media_assets (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL REFERENCES media_jobs(id) ON DELETE CASCADE,
  media_type TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  name TEXT NOT NULL,
  path TEXT NOT NULL,
  size INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS media_assets_job ON media_assets(job_id);`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("media: initialize database: %w", err)
	}
	return &Store{db: db, cipher: cipher}, nil
}

func (store *Store) Create(
	ctx context.Context,
	actorID uuid.UUID,
	idempotencyKey string,
	request Request,
	now time.Time,
) (Job, error) {
	if actorID == uuid.Nil || idempotencyKey == "" || now.IsZero() {
		return Job{}, fmt.Errorf("media: actor, idempotency key, and timestamp are required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var existingID string
	err := store.db.QueryRowContext(
		ctx,
		`SELECT id FROM media_jobs WHERE actor_id = ? AND idempotency_key = ?`,
		actorID.String(), idempotencyKey,
	).Scan(&existingID)
	if err == nil {
		id, parseErr := uuid.Parse(existingID)
		if parseErr != nil {
			return Job{}, fmt.Errorf("media: invalid stored job ID")
		}
		return store.Get(ctx, actorID, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("media: inspect idempotency key: %w", err)
	}
	var active int
	err = store.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM media_jobs
		  WHERE actor_id = ? AND status IN (?, ?, ?)`,
		actorID.String(), StatusQueued, StatusSubmitting, StatusRunning,
	).Scan(&active)
	if err != nil {
		return Job{}, fmt.Errorf("media: inspect active jobs: %w", err)
	}
	if active >= 4 {
		return Job{}, fmt.Errorf(
			"media: wait for an active generation to finish before starting another",
		)
	}
	plaintext, err := json.Marshal(request)
	if err != nil {
		return Job{}, fmt.Errorf("media: encode request: %w", err)
	}
	envelope, err := store.cipher.Encrypt(plaintext)
	clear(plaintext)
	if err != nil {
		return Job{}, fmt.Errorf("media: encrypt request: %w", err)
	}
	viewPlaintext, err := json.Marshal(request.view())
	if err != nil {
		return Job{}, fmt.Errorf("media: encode request view: %w", err)
	}
	viewEnvelope, err := store.cipher.Encrypt(viewPlaintext)
	clear(viewPlaintext)
	if err != nil {
		return Job{}, fmt.Errorf("media: encrypt request view: %w", err)
	}
	job := Job{
		ID: uuid.New(), ActorID: actorID, Kind: request.Kind, Status: StatusQueued,
		Provider: "novita", Request: request.view(), Assets: []Asset{},
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	_, err = store.db.ExecContext(
		ctx,
		`INSERT INTO media_jobs(
			id, actor_id, idempotency_key, kind, status, provider,
			request_envelope, request_view_envelope, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID.String(), actorID.String(), idempotencyKey, string(job.Kind),
		string(job.Status), job.Provider, envelope, viewEnvelope, micros(job.CreatedAt),
		micros(job.UpdatedAt),
	)
	if err != nil {
		return Job{}, fmt.Errorf("media: persist job: %w", err)
	}
	return job, nil
}

func (store *Store) List(ctx context.Context, actorID uuid.UUID, limit int) ([]Job, error) {
	if actorID == uuid.Nil {
		return nil, fmt.Errorf("media: actor is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT id, actor_id, kind, status, provider, provider_task_id,
		        request_view_envelope, progress, error_message, created_at, updated_at, completed_at
		   FROM media_jobs WHERE actor_id = ? ORDER BY updated_at DESC LIMIT ?`,
		actorID.String(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("media: list jobs: %w", err)
	}
	jobs := make([]Job, 0)
	for rows.Next() {
		job, err := store.scanJob(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("media: list jobs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("media: close job rows: %w", err)
	}
	for index := range jobs {
		jobs[index].Assets, err = store.assets(ctx, jobs[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

func (store *Store) Get(ctx context.Context, actorID, jobID uuid.UUID) (Job, error) {
	if actorID == uuid.Nil || jobID == uuid.Nil {
		return Job{}, ErrNotFound
	}
	row := store.db.QueryRowContext(
		ctx,
		`SELECT id, actor_id, kind, status, provider, provider_task_id,
		        request_view_envelope, progress, error_message, created_at, updated_at, completed_at
		   FROM media_jobs WHERE id = ? AND actor_id = ?`,
		jobID.String(), actorID.String(),
	)
	job, err := store.scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	job.Assets, err = store.assets(ctx, job.ID)
	return job, err
}

func (store *Store) Pending(ctx context.Context) ([]Job, error) {
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT id, actor_id, kind, status, provider, provider_task_id,
		        request_view_envelope, progress, error_message, created_at, updated_at, completed_at
		   FROM media_jobs WHERE status IN (?, ?, ?) ORDER BY created_at`,
		StatusQueued, StatusSubmitting, StatusRunning,
	)
	if err != nil {
		return nil, fmt.Errorf("media: list pending jobs: %w", err)
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		job, err := store.scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

type rowScanner interface {
	Scan(...any) error
}

func (store *Store) scanJob(row rowScanner) (Job, error) {
	var job Job
	var id, actorID, kind, status string
	var envelope []byte
	var createdAt, updatedAt int64
	var completedAt sql.NullInt64
	err := row.Scan(
		&id, &actorID, &kind, &status, &job.Provider, &job.ProviderTaskID,
		&envelope, &job.Progress, &job.Error, &createdAt, &updatedAt, &completedAt,
	)
	if err != nil {
		return Job{}, err
	}
	job.ID, err = uuid.Parse(id)
	if err != nil {
		return Job{}, fmt.Errorf("media: invalid stored job ID")
	}
	job.ActorID, err = uuid.Parse(actorID)
	if err != nil {
		return Job{}, fmt.Errorf("media: invalid stored actor ID")
	}
	job.Kind, job.Status = Kind(kind), Status(status)
	job.CreatedAt, job.UpdatedAt = fromMicros(createdAt), fromMicros(updatedAt)
	if completedAt.Valid {
		completed := fromMicros(completedAt.Int64)
		job.CompletedAt = &completed
	}
	plaintext, err := store.cipher.Decrypt(envelope)
	if err != nil {
		return Job{}, fmt.Errorf("media: decrypt request: %w", err)
	}
	var request RequestView
	err = json.Unmarshal(plaintext, &request)
	clear(plaintext)
	if err != nil {
		return Job{}, fmt.Errorf("media: decode request: %w", err)
	}
	job.Request = request
	job.Assets = []Asset{}
	return job, nil
}

func (store *Store) Request(ctx context.Context, jobID uuid.UUID) (Request, error) {
	var envelope []byte
	err := store.db.QueryRowContext(
		ctx, `SELECT request_envelope FROM media_jobs WHERE id = ?`, jobID.String(),
	).Scan(&envelope)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, err
	}
	plaintext, err := store.cipher.Decrypt(envelope)
	if err != nil {
		return Request{}, err
	}
	var request Request
	err = json.Unmarshal(plaintext, &request)
	clear(plaintext)
	return request, err
}

func (store *Store) Update(
	ctx context.Context,
	jobID uuid.UUID,
	status Status,
	taskID string,
	progress int,
	message string,
	now time.Time,
) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	var completed any
	if status == StatusSucceeded || status == StatusFailed {
		completed = micros(now)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result, err := store.db.ExecContext(
		ctx,
		`UPDATE media_jobs
		    SET status = ?, provider_task_id = ?, progress = ?, error_message = ?,
		        updated_at = ?, completed_at = COALESCE(?, completed_at)
		  WHERE id = ?`,
		string(status), taskID, progress, message, micros(now), completed, jobID.String(),
	)
	if err != nil {
		return fmt.Errorf("media: update job: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) AddAsset(ctx context.Context, asset Asset) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, err := store.db.ExecContext(
		ctx,
		`INSERT INTO media_assets(id, job_id, media_type, mime_type, name, path, size)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		asset.ID.String(), asset.JobID.String(), asset.MediaType, asset.MIMEType,
		asset.Name, asset.Path, asset.Size,
	)
	if err != nil {
		return fmt.Errorf("media: persist asset: %w", err)
	}
	return nil
}

func (store *Store) Asset(ctx context.Context, actorID, assetID uuid.UUID) (Asset, error) {
	var asset Asset
	var id, jobID string
	err := store.db.QueryRowContext(
		ctx,
		`SELECT a.id, a.job_id, a.media_type, a.mime_type, a.name, a.path, a.size
		   FROM media_assets a JOIN media_jobs j ON j.id = a.job_id
		  WHERE a.id = ? AND j.actor_id = ?`,
		assetID.String(), actorID.String(),
	).Scan(&id, &jobID, &asset.MediaType, &asset.MIMEType, &asset.Name, &asset.Path, &asset.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, err
	}
	asset.ID, _ = uuid.Parse(id)
	asset.JobID, _ = uuid.Parse(jobID)
	return asset, nil
}

func (store *Store) assets(ctx context.Context, jobID uuid.UUID) ([]Asset, error) {
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT id, job_id, media_type, mime_type, name, path, size
		   FROM media_assets WHERE job_id = ? ORDER BY rowid`,
		jobID.String(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]Asset, 0)
	for rows.Next() {
		var asset Asset
		var id, storedJobID string
		if err := rows.Scan(
			&id, &storedJobID, &asset.MediaType, &asset.MIMEType,
			&asset.Name, &asset.Path, &asset.Size,
		); err != nil {
			return nil, err
		}
		asset.ID, _ = uuid.Parse(id)
		asset.JobID, _ = uuid.Parse(storedJobID)
		asset.URL = "/v1/media/assets/" + asset.ID.String()
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (store *Store) Delete(ctx context.Context, actorID, jobID uuid.UUID) ([]string, error) {
	job, err := store.Get(ctx, actorID, jobID)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(job.Assets))
	for _, asset := range job.Assets {
		paths = append(paths, asset.Path)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	result, err := store.db.ExecContext(
		ctx, `DELETE FROM media_jobs WHERE id = ? AND actor_id = ?`,
		jobID.String(), actorID.String(),
	)
	if err != nil {
		return nil, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return nil, ErrNotFound
	}
	return paths, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func micros(value time.Time) int64 {
	return value.UTC().UnixMicro()
}

func fromMicros(value int64) time.Time {
	return time.UnixMicro(value).UTC()
}
