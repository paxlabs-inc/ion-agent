package hnsw

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// Vector is one durable Cortex vector.
type Vector struct {
	Key    uint64
	Values []float32
}

// VectorStore is the SQLite Cortex-index boundary used for exact fallback.
type VectorStore interface {
	Upsert(context.Context, Vector) error
	Delete(context.Context, uint64) (bool, error)
	Search(context.Context, []float32, int) ([]Match, error)
	Count(context.Context) (uint64, error)
	Snapshot(context.Context) ([]Vector, error)
	Replace(context.Context, []Vector) error
	Close() error
}

// SQLiteStore persists vectors as deterministic big-endian f32 blobs. Writes
// are serialized while reads use SQLite's WAL-backed connection pool.
type SQLiteStore struct {
	db         *sql.DB
	dimensions int
	writeMu    sync.Mutex
}

// OpenSQLiteStore creates the exact-search Cortex index with the project WAL
// pragmas and 0600 file permissions.
func OpenSQLiteStore(
	ctx context.Context,
	path string,
	dimensions int,
) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("hnsw: SQLite vector path is required")
	}
	if dimensions <= 0 {
		return nil, fmt.Errorf("hnsw: dimensions must be positive")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("hnsw: resolve SQLite vector path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("hnsw: create SQLite vector directory: %w", err)
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
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     absolute,
		RawQuery: parameters.Encode(),
	}).String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("hnsw: open SQLite vector index: %w", err)
	}
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(4)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hnsw: connect SQLite vector index: %w", err)
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hnsw: secure SQLite vector index: %w", err)
	}
	if _, err := database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cortex_vectors (
			vector_key BLOB PRIMARY KEY NOT NULL CHECK(length(vector_key) = 8),
			dimensions INTEGER NOT NULL,
			vector BLOB NOT NULL
		) WITHOUT ROWID`); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hnsw: create SQLite vector schema: %w", err)
	}
	return &SQLiteStore{db: database, dimensions: dimensions}, nil
}

func (store *SQLiteStore) Upsert(ctx context.Context, vector Vector) error {
	if err := validateVector(vector.Values, store.dimensions); err != nil {
		return err
	}
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	_, err := store.db.ExecContext(
		ctx,
		`INSERT INTO cortex_vectors(vector_key, dimensions, vector)
		 VALUES (?, ?, ?)
		 ON CONFLICT(vector_key) DO UPDATE SET
		   dimensions = excluded.dimensions,
		   vector = excluded.vector`,
		encodeKey(vector.Key),
		store.dimensions,
		encodeVector(vector.Values),
	)
	if err != nil {
		return fmt.Errorf("hnsw: upsert SQLite vector: %w", err)
	}
	return nil
}

func (store *SQLiteStore) Delete(ctx context.Context, key uint64) (bool, error) {
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	result, err := store.db.ExecContext(
		ctx,
		`DELETE FROM cortex_vectors WHERE vector_key = ?`,
		encodeKey(key),
	)
	if err != nil {
		return false, fmt.Errorf("hnsw: delete SQLite vector: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("hnsw: inspect SQLite delete: %w", err)
	}
	return affected != 0, nil
}

func (store *SQLiteStore) Search(
	ctx context.Context,
	query []float32,
	k int,
) ([]Match, error) {
	if err := validateVector(query, store.dimensions); err != nil {
		return nil, err
	}
	if k < 0 {
		return nil, fmt.Errorf("hnsw: k must not be negative")
	}
	if k == 0 {
		return []Match{}, nil
	}
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT vector_key, vector FROM cortex_vectors WHERE dimensions = ?`,
		store.dimensions,
	)
	if err != nil {
		return nil, fmt.Errorf("hnsw: scan SQLite vectors: %w", err)
	}
	defer rows.Close()

	queryNorm := vectorNorm(query)
	best := make(maxMatchHeap, 0, k)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var encodedKey, encodedValues []byte
		if err := rows.Scan(&encodedKey, &encodedValues); err != nil {
			return nil, fmt.Errorf("hnsw: read SQLite vector: %w", err)
		}
		key, err := decodeKey(encodedKey)
		if err != nil {
			return nil, err
		}
		values, err := decodeVector(encodedValues, store.dimensions)
		if err != nil {
			return nil, err
		}
		distance := cosineDistance(query, queryNorm, values)
		match := Match{Key: key, Distance: distance}
		if len(best) < k {
			heap.Push(&best, match)
		} else if lessMatch(match, best[0]) {
			best[0] = match
			heap.Fix(&best, 0)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hnsw: iterate SQLite vectors: %w", err)
	}
	matches := make([]Match, len(best))
	for index := len(matches) - 1; index >= 0; index-- {
		matches[index] = heap.Pop(&best).(Match)
	}
	return matches, nil
}

func (store *SQLiteStore) Count(ctx context.Context) (uint64, error) {
	var count uint64
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT count(*) FROM cortex_vectors WHERE dimensions = ?`,
		store.dimensions,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("hnsw: count SQLite vectors: %w", err)
	}
	return count, nil
}

func (store *SQLiteStore) Snapshot(ctx context.Context) ([]Vector, error) {
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT vector_key, vector
		   FROM cortex_vectors
		  WHERE dimensions = ?
		  ORDER BY vector_key`,
		store.dimensions,
	)
	if err != nil {
		return nil, fmt.Errorf("hnsw: snapshot SQLite vectors: %w", err)
	}
	defer rows.Close()
	var vectors []Vector
	for rows.Next() {
		var encodedKey, encodedValues []byte
		if err := rows.Scan(&encodedKey, &encodedValues); err != nil {
			return nil, fmt.Errorf("hnsw: read SQLite vector snapshot: %w", err)
		}
		key, err := decodeKey(encodedKey)
		if err != nil {
			return nil, err
		}
		values, err := decodeVector(encodedValues, store.dimensions)
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, Vector{Key: key, Values: values})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hnsw: iterate SQLite vector snapshot: %w", err)
	}
	return vectors, nil
}

// Replace atomically refreshes the derived SQLite index from journal state.
func (store *SQLiteStore) Replace(ctx context.Context, vectors []Vector) error {
	for _, vector := range vectors {
		if err := validateVector(vector.Values, store.dimensions); err != nil {
			return fmt.Errorf("hnsw: replacement key %d: %w", vector.Key, err)
		}
	}
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("hnsw: begin SQLite vector replacement: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, `DELETE FROM cortex_vectors`); err != nil {
		return fmt.Errorf("hnsw: clear SQLite vectors: %w", err)
	}
	statement, err := transaction.PrepareContext(
		ctx,
		`INSERT INTO cortex_vectors(vector_key, dimensions, vector) VALUES (?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("hnsw: prepare SQLite vector replacement: %w", err)
	}
	defer statement.Close()
	for _, vector := range vectors {
		if _, err := statement.ExecContext(
			ctx,
			encodeKey(vector.Key),
			store.dimensions,
			encodeVector(vector.Values),
		); err != nil {
			return fmt.Errorf("hnsw: insert SQLite replacement vector: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("hnsw: commit SQLite vector replacement: %w", err)
	}
	return nil
}

func (store *SQLiteStore) Close() error {
	return store.db.Close()
}

func encodeKey(key uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, key)
	return encoded
}

func decodeKey(encoded []byte) (uint64, error) {
	if len(encoded) != 8 {
		return 0, fmt.Errorf("hnsw: corrupt SQLite vector key")
	}
	return binary.BigEndian.Uint64(encoded), nil
}

func encodeVector(values []float32) []byte {
	encoded := make([]byte, len(values)*4)
	for index, value := range values {
		binary.BigEndian.PutUint32(encoded[index*4:], math.Float32bits(value))
	}
	return encoded
}

func decodeVector(encoded []byte, dimensions int) ([]float32, error) {
	if len(encoded) != dimensions*4 {
		return nil, fmt.Errorf("hnsw: corrupt SQLite vector dimensions")
	}
	values := make([]float32, dimensions)
	for index := range values {
		values[index] = math.Float32frombits(binary.BigEndian.Uint32(encoded[index*4:]))
		if !isFinite(values[index]) {
			return nil, fmt.Errorf("hnsw: corrupt SQLite vector value")
		}
	}
	if vectorNorm(values) == 0 {
		return nil, fmt.Errorf("hnsw: corrupt zero SQLite vector")
	}
	return values, nil
}

func vectorNorm(vector []float32) float64 {
	var squared float64
	for _, value := range vector {
		squared += float64(value) * float64(value)
	}
	return math.Sqrt(squared)
}

func cosineDistance(query []float32, queryNorm float64, candidate []float32) float32 {
	var dot float64
	for index, value := range query {
		dot += float64(value) * float64(candidate[index])
	}
	similarity := dot / (queryNorm * vectorNorm(candidate))
	if similarity > 1 {
		similarity = 1
	} else if similarity < -1 {
		similarity = -1
	}
	return float32(1 - similarity)
}

func lessMatch(left, right Match) bool {
	if left.Distance == right.Distance {
		return left.Key < right.Key
	}
	return left.Distance < right.Distance
}

// maxMatchHeap keeps the worst retained match at index zero.
type maxMatchHeap []Match

func (matches maxMatchHeap) Len() int { return len(matches) }
func (matches maxMatchHeap) Less(left, right int) bool {
	return lessMatch(matches[right], matches[left])
}
func (matches maxMatchHeap) Swap(left, right int) {
	matches[left], matches[right] = matches[right], matches[left]
}
func (matches *maxMatchHeap) Push(value any) {
	*matches = append(*matches, value.(Match))
}
func (matches *maxMatchHeap) Pop() any {
	old := *matches
	index := len(old) - 1
	value := old[index]
	*matches = old[:index]
	return value
}
