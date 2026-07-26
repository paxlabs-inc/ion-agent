package controlplane

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
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	_ "modernc.org/sqlite"
)

const (
	defaultJournalRetention = 10_000
	defaultSubscriberBuffer = 256
	maxReplayEvents         = 2_000
)

var (
	// ErrJournalClosed is returned after the journal has shut down.
	ErrJournalClosed = errors.New("controlplane: event journal closed")
	// ErrCursorAhead indicates a corrupt or invalid acknowledgement cursor.
	ErrCursorAhead = errors.New("controlplane: event cursor is ahead of journal")
)

// JournalConfig bounds durable retention and per-client live buffering.
type JournalConfig struct {
	Retention        int
	SubscriberBuffer int
}

// Journal owns the durable event sequence and non-blocking live subscribers.
type Journal struct {
	db               *sql.DB
	clock            types.Clock
	retention        int
	subscriberBuffer int

	mu          sync.Mutex
	closed      bool
	nextSubID   uint64
	subscribers map[uint64]journalSubscriber
}

type journalSubscriber struct {
	actorID *uuid.UUID
	events  chan Event
}

// Subscription atomically combines cursor replay with subsequent live events.
// Closing Live means the client fell behind or the journal shut down; clients
// recover by reconnecting from their last acknowledged sequence.
type Subscription struct {
	Replay Replay
	Live   <-chan Event

	journal *Journal
	id      uint64
	once    sync.Once
}

// OpenJournal initializes the durable sequence database.
func OpenJournal(
	ctx context.Context,
	path string,
	clock types.Clock,
	config JournalConfig,
) (*Journal, error) {
	if path == "" || clock == nil {
		return nil, fmt.Errorf("controlplane: journal path and clock are required")
	}
	if config.Retention <= 0 {
		config.Retention = defaultJournalRetention
	}
	if config.SubscriberBuffer <= 0 {
		config.SubscriberBuffer = defaultSubscriberBuffer
	}
	dsn, filePath, err := journalDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("controlplane: open journal: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close() // Best-effort cleanup after failed open.
		return nil, fmt.Errorf("controlplane: connect journal: %w", err)
	}
	if filePath != "" {
		if err := os.Chmod(filePath, 0o600); err != nil {
			_ = db.Close() // Best-effort cleanup after insecure permissions.
			return nil, fmt.Errorf("controlplane: secure journal: %w", err)
		}
	}
	if err := initializeJournal(ctx, db); err != nil {
		_ = db.Close() // Best-effort cleanup after failed initialization.
		return nil, err
	}
	return &Journal{
		db: db, clock: clock, retention: config.Retention,
		subscriberBuffer: config.SubscriberBuffer,
		subscribers:      make(map[uint64]journalSubscriber),
	}, nil
}

func journalDSN(path string) (string, string, error) {
	if path == ":memory:" {
		return "file:controlplane-journal?mode=memory&cache=shared", "", nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("controlplane: resolve journal path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return "", "", fmt.Errorf("controlplane: create journal directory: %w", err)
	}
	parameters := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"synchronous(FULL)",
		"foreign_keys(ON)",
	} {
		parameters.Add("_pragma", pragma)
	}
	location := &url.URL{Scheme: "file", Path: absolute, RawQuery: parameters.Encode()}
	return location.String(), absolute, nil
}

func initializeJournal(ctx context.Context, db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS controlplane_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    occurred_at INTEGER NOT NULL,
    actor_id TEXT NOT NULL,
    session_id TEXT,
    turn_id TEXT,
    task_id TEXT,
    tool_id TEXT,
    payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_controlplane_events_type_sequence
    ON controlplane_events(event_type, sequence);
CREATE TABLE IF NOT EXISTS controlplane_acknowledgements (
    actor_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK(sequence >= 0),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(actor_id, client_id)
);
CREATE TABLE IF NOT EXISTS controlplane_revisions (
    scope_key TEXT PRIMARY KEY NOT NULL,
    revision INTEGER NOT NULL CHECK(revision >= 0)
);
CREATE TABLE IF NOT EXISTS controlplane_idempotency (
    actor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash BLOB NOT NULL,
    response BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY(actor_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS controlplane_approvals (
    approval_id TEXT PRIMARY KEY NOT NULL,
    actor_id TEXT NOT NULL,
    session_id TEXT,
    turn_id TEXT,
    operation TEXT NOT NULL,
    arguments BLOB NOT NULL,
    consequence TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    state TEXT NOT NULL,
    decision TEXT,
    resolved_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_controlplane_approvals_actor_state
    ON controlplane_approvals(actor_id, state, expires_at)
;`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("controlplane: initialize journal: %w", err)
	}
	return nil
}

// Append durably assigns the next sequence before publishing to live clients.
func (journal *Journal) Append(ctx context.Context, event Event) (Event, error) {
	if err := validateAppendEvent(event); err != nil {
		return Event{}, err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return Event{}, ErrJournalClosed
	}
	tx, err := journal.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, fmt.Errorf("controlplane: begin event append: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // Best-effort rollback after primary append error.
		}
	}()
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO controlplane_events(
			event_id, event_type, occurred_at, actor_id, session_id,
			turn_id, task_id, tool_id, payload
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID.String(), string(event.Type), event.OccurredAt.UnixMicro(),
		event.Correlation.ActorID.String(), nullableUUID(event.Correlation.SessionID),
		nullableUUID(event.Correlation.TurnID), nullableUUID(event.Correlation.TaskID),
		nullableUUID(event.Correlation.ToolID), []byte(event.Payload),
	)
	if err != nil {
		return Event{}, fmt.Errorf("controlplane: append event: %w", err)
	}
	sequence, err := result.LastInsertId()
	if err != nil || sequence <= 0 {
		return Event{}, fmt.Errorf("controlplane: read event sequence: %w", err)
	}
	event.Sequence = uint64(sequence)
	if err := journal.trimLocked(ctx, tx, event.Sequence); err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("controlplane: commit event append: %w", err)
	}
	committed = true
	journal.publishLocked(event)
	return cloneEvent(event), nil
}

func validateAppendEvent(event Event) error {
	if event.Sequence != 0 || event.EventID == uuid.Nil || !event.Type.Valid() ||
		event.OccurredAt.IsZero() || event.Correlation.ActorID == uuid.Nil {
		return fmt.Errorf("%w: incomplete event", ErrInvalidProtocol)
	}
	if len(event.Payload) == 0 || len(event.Payload) > MaximumPayloadBytes ||
		!jsonValid(event.Payload) {
		return fmt.Errorf("%w: invalid event payload", ErrInvalidProtocol)
	}
	return nil
}

type journalExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (journal *Journal) trimLocked(
	ctx context.Context,
	execer journalExecer,
	latest uint64,
) error {
	if latest <= uint64(journal.retention) {
		return nil
	}
	firstRetained := latest - uint64(journal.retention) + 1
	if _, err := execer.ExecContext(
		ctx,
		`DELETE FROM controlplane_events WHERE sequence < ?`,
		firstRetained,
	); err != nil {
		return fmt.Errorf("controlplane: trim journal: %w", err)
	}
	return nil
}

func (journal *Journal) publishLocked(event Event) {
	for id, subscriber := range journal.subscribers {
		if subscriber.actorID != nil &&
			*subscriber.actorID != event.Correlation.ActorID {
			continue
		}
		select {
		case subscriber.events <- cloneEvent(event):
		default:
			close(subscriber.events)
			delete(journal.subscribers, id)
		}
	}
}

// Replay returns retained events strictly after the acknowledged cursor.
func (journal *Journal) Replay(ctx context.Context, after uint64, limit int) (Replay, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return Replay{}, ErrJournalClosed
	}
	return journal.replayLocked(ctx, after, limit, nil)
}

// ReplayActor returns only events authorized for one actor while retaining the
// global monotonic cursor and gap semantics.
func (journal *Journal) ReplayActor(
	ctx context.Context,
	actorID uuid.UUID,
	after uint64,
	limit int,
) (Replay, error) {
	if actorID == uuid.Nil {
		return Replay{}, fmt.Errorf("%w: actor ID is required", ErrInvalidProtocol)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return Replay{}, ErrJournalClosed
	}
	return journal.replayLocked(ctx, after, limit, &actorID)
}

// replayActorThrough returns one actor-scoped page bounded by a previously
// captured journal head. Transports use it after SubscribeActor so events
// appended during catch-up remain exclusively on the live subscription and
// cannot be delivered twice.
func (journal *Journal) replayActorThrough(
	ctx context.Context,
	actorID uuid.UUID,
	after uint64,
	limit int,
	through uint64,
) (Replay, error) {
	if actorID == uuid.Nil {
		return Replay{}, fmt.Errorf("%w: actor ID is required", ErrInvalidProtocol)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return Replay{}, ErrJournalClosed
	}
	return journal.replayThroughLocked(ctx, after, limit, &actorID, through)
}

func (journal *Journal) replayLocked(
	ctx context.Context,
	after uint64,
	limit int,
	actorID *uuid.UUID,
) (Replay, error) {
	_, head, err := journal.boundsLocked(ctx)
	if err != nil {
		return Replay{}, err
	}
	return journal.replayThroughLocked(ctx, after, limit, actorID, head)
}

func (journal *Journal) replayThroughLocked(
	ctx context.Context,
	after uint64,
	limit int,
	actorID *uuid.UUID,
	head uint64,
) (Replay, error) {
	if limit <= 0 || limit > maxReplayEvents {
		limit = maxReplayEvents
	}
	earliest, currentHead, err := journal.boundsLocked(ctx)
	if err != nil {
		return Replay{}, err
	}
	if head > currentHead {
		return Replay{}, ErrCursorAhead
	}
	replay := Replay{
		AfterSequence: after, Earliest: earliest, Latest: after, Head: head,
		Events: make([]Event, 0),
	}
	if after > head {
		return Replay{}, ErrCursorAhead
	}
	if earliest > 0 && after < earliest-1 {
		replay.Gap = true
		// A fresh snapshot is authoritative through the captured head.
		replay.Latest = head
		return replay, nil
	}
	statement := `SELECT sequence, event_id, event_type, occurred_at, actor_id,
		        session_id, turn_id, task_id, tool_id, payload
		 FROM controlplane_events
		 WHERE sequence > ? AND sequence <= ?`
	arguments := []any{after, head}
	if actorID != nil {
		statement += ` AND actor_id = ?`
		arguments = append(arguments, actorID.String())
	}
	// Read one extra row so Latest can remain the actual delivered cursor when
	// another page exists, without guessing from a full page.
	statement += ` ORDER BY sequence LIMIT ?`
	arguments = append(arguments, limit+1)
	rows, err := journal.db.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return Replay{}, fmt.Errorf("controlplane: query replay: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		event, scanErr := scanEvent(rows)
		if scanErr != nil {
			return Replay{}, scanErr
		}
		if len(replay.Events) < limit {
			replay.Events = append(replay.Events, event)
		}
	}
	if err := rows.Err(); err != nil {
		return Replay{}, fmt.Errorf("controlplane: iterate replay: %w", err)
	}
	if len(replay.Events) == limit {
		// A full page may still be terminal. Probe for a remaining authorized row
		// through the captured head before deciding whether it is safe to advance.
		last := replay.Events[len(replay.Events)-1].Sequence
		var exists int
		probe := `SELECT 1 FROM controlplane_events WHERE sequence > ? AND sequence <= ?`
		probeArgs := []any{last, head}
		if actorID != nil {
			probe += ` AND actor_id = ?`
			probeArgs = append(probeArgs, actorID.String())
		}
		probe += ` LIMIT 1`
		probeErr := journal.db.QueryRowContext(ctx, probe, probeArgs...).Scan(&exists)
		if probeErr == nil {
			replay.Latest = last
			return replay, nil
		}
		if !errors.Is(probeErr, sql.ErrNoRows) {
			return Replay{}, fmt.Errorf("controlplane: probe replay continuation: %w", probeErr)
		}
	}
	// Every authorized event through Head was scanned, including the legitimate
	// actor-scoped case where only foreign events occupy the remaining sequence.
	replay.Latest = head
	return replay, nil
}

func (journal *Journal) boundsLocked(ctx context.Context) (uint64, uint64, error) {
	var earliest sql.NullInt64
	var latest sql.NullInt64
	if err := journal.db.QueryRowContext(
		ctx,
		`SELECT MIN(sequence), MAX(sequence) FROM controlplane_events`,
	).Scan(&earliest, &latest); err != nil {
		return 0, 0, fmt.Errorf("controlplane: read journal bounds: %w", err)
	}
	if !earliest.Valid {
		return 0, 0, nil
	}
	return uint64(earliest.Int64), uint64(latest.Int64), nil
}

// Subscribe atomically captures replay and registers a non-blocking live feed.
func (journal *Journal) Subscribe(
	ctx context.Context,
	after uint64,
	limit int,
) (*Subscription, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil, ErrJournalClosed
	}
	replay, err := journal.replayLocked(ctx, after, limit, nil)
	if err != nil {
		return nil, err
	}
	journal.nextSubID++
	id := journal.nextSubID
	live := make(chan Event, journal.subscriberBuffer)
	journal.subscribers[id] = journalSubscriber{events: live}
	return &Subscription{
		Replay: replay, Live: live, journal: journal, id: id,
	}, nil
}

// SubscribeActor atomically captures actor-scoped replay and registers a live
// feed that cannot expose other actors' events.
func (journal *Journal) SubscribeActor(
	ctx context.Context,
	actorID uuid.UUID,
	after uint64,
	limit int,
) (*Subscription, error) {
	if actorID == uuid.Nil {
		return nil, fmt.Errorf("%w: actor ID is required", ErrInvalidProtocol)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return nil, ErrJournalClosed
	}
	replay, err := journal.replayLocked(ctx, after, limit, &actorID)
	if err != nil {
		return nil, err
	}
	journal.nextSubID++
	id := journal.nextSubID
	live := make(chan Event, journal.subscriberBuffer)
	actorCopy := actorID
	journal.subscribers[id] = journalSubscriber{actorID: &actorCopy, events: live}
	return &Subscription{
		Replay: replay, Live: live, journal: journal, id: id,
	}, nil
}

// Close unregisters a subscription. It is safe to call more than once.
func (subscription *Subscription) Close() {
	if subscription == nil || subscription.journal == nil {
		return
	}
	subscription.once.Do(func() {
		subscription.journal.removeSubscriber(subscription.id)
	})
}

func (journal *Journal) removeSubscriber(id uint64) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	subscriber, exists := journal.subscribers[id]
	if !exists {
		return
	}
	close(subscriber.events)
	delete(journal.subscribers, id)
}

// Acknowledge durably stores only a client's last processed sequence.
func (journal *Journal) Acknowledge(
	ctx context.Context,
	actorID uuid.UUID,
	clientID string,
	sequence uint64,
) error {
	if actorID == uuid.Nil || clientID == "" {
		return fmt.Errorf("%w: actor and client IDs are required", ErrInvalidProtocol)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return ErrJournalClosed
	}
	_, latest, err := journal.boundsLocked(ctx)
	if err != nil {
		return err
	}
	if sequence > latest {
		return ErrCursorAhead
	}
	if _, err := journal.db.ExecContext(
		ctx,
		`INSERT INTO controlplane_acknowledgements(actor_id, client_id, sequence, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(actor_id, client_id) DO UPDATE SET
		   sequence = CASE
		     WHEN excluded.sequence > sequence THEN excluded.sequence
		     ELSE sequence
		   END,
		   updated_at = excluded.updated_at`,
		actorID.String(), clientID, sequence, journal.clock.Now().UnixMicro(),
	); err != nil {
		return fmt.Errorf("controlplane: acknowledge event: %w", err)
	}
	return nil
}

// Acknowledged returns a client's last durable cursor.
func (journal *Journal) Acknowledged(
	ctx context.Context,
	actorID uuid.UUID,
	clientID string,
) (uint64, error) {
	if actorID == uuid.Nil || clientID == "" {
		return 0, fmt.Errorf("%w: actor and client IDs are required", ErrInvalidProtocol)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed {
		return 0, ErrJournalClosed
	}
	var sequence uint64
	err := journal.db.QueryRowContext(
		ctx,
		`SELECT sequence FROM controlplane_acknowledgements
		 WHERE actor_id = ? AND client_id = ?`,
		actorID.String(), clientID,
	).Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("controlplane: read acknowledgement: %w", err)
	}
	return sequence, nil
}

// Close terminates subscribers and closes the durable database.
func (journal *Journal) Close() error {
	journal.mu.Lock()
	if journal.closed {
		journal.mu.Unlock()
		return nil
	}
	journal.closed = true
	for id, subscriber := range journal.subscribers {
		close(subscriber.events)
		delete(journal.subscribers, id)
	}
	journal.mu.Unlock()
	if err := journal.db.Close(); err != nil {
		return fmt.Errorf("controlplane: close journal: %w", err)
	}
	return nil
}

type eventScanner interface {
	Scan(...any) error
}

func scanEvent(scanner eventScanner) (Event, error) {
	var event Event
	var sequence int64
	var eventID string
	var eventType string
	var occurredAt int64
	var actorID string
	var sessionID sql.NullString
	var turnID sql.NullString
	var taskID sql.NullString
	var toolID sql.NullString
	var payload []byte
	if err := scanner.Scan(
		&sequence, &eventID, &eventType, &occurredAt, &actorID,
		&sessionID, &turnID, &taskID, &toolID, &payload,
	); err != nil {
		return Event{}, fmt.Errorf("controlplane: scan event: %w", err)
	}
	parsedEventID, err := uuid.Parse(eventID)
	if err != nil {
		return Event{}, fmt.Errorf("controlplane: parse event ID: %w", err)
	}
	parsedActorID, err := uuid.Parse(actorID)
	if err != nil {
		return Event{}, fmt.Errorf("controlplane: parse actor ID: %w", err)
	}
	event = Event{
		Sequence: uint64(sequence), EventID: parsedEventID, Type: EventType(eventType),
		OccurredAt:  time.UnixMicro(occurredAt).UTC(),
		Correlation: Correlation{ActorID: parsedActorID},
		Payload:     append([]byte(nil), payload...),
	}
	if !event.Type.Valid() || !jsonValid(event.Payload) {
		return Event{}, fmt.Errorf("controlplane: durable event failed validation")
	}
	event.Correlation.SessionID, err = parseNullableUUID(sessionID)
	if err != nil {
		return Event{}, err
	}
	event.Correlation.TurnID, err = parseNullableUUID(turnID)
	if err != nil {
		return Event{}, err
	}
	event.Correlation.TaskID, err = parseNullableUUID(taskID)
	if err != nil {
		return Event{}, err
	}
	event.Correlation.ToolID, err = parseNullableUUID(toolID)
	if err != nil {
		return Event{}, err
	}
	return event, nil
}

func parseNullableUUID(source sql.NullString) (*uuid.UUID, error) {
	if !source.Valid {
		return nil, nil
	}
	parsed, err := uuid.Parse(source.String)
	if err != nil {
		return nil, fmt.Errorf("controlplane: parse correlation ID: %w", err)
	}
	return &parsed, nil
}

func nullableUUID(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func cloneEvent(event Event) Event {
	cloned := event
	cloned.Correlation = cloneCorrelation(event.Correlation)
	cloned.Payload = append([]byte(nil), event.Payload...)
	return cloned
}

func jsonValid(payload []byte) bool {
	return len(payload) > 0 && json.Valid(payload)
}
