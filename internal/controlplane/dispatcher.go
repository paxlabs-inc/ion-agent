package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
	"lukechampine.com/blake3"
)

// Handler is a narrow production application-service adapter. Implementations
// perform authorization and policy checks at their existing domain boundary.
type Handler interface {
	Handle(context.Context, Request, EventEmitter) (json.RawMessage, error)
}

// HandlerFunc adapts a function into a Handler.
type HandlerFunc func(context.Context, Request, EventEmitter) (json.RawMessage, error)

// Handle invokes the adapted handler.
func (handler HandlerFunc) Handle(
	ctx context.Context,
	request Request,
	emitter EventEmitter,
) (json.RawMessage, error) {
	return handler(ctx, request, emitter)
}

// SnapshotProvider captures reconstructible state for initial connection and
// retention-gap recovery.
type SnapshotProvider interface {
	Snapshot(context.Context, Scope) (json.RawMessage, error)
}

// SnapshotFunc adapts a function into a SnapshotProvider.
type SnapshotFunc func(context.Context, Scope) (json.RawMessage, error)

// Snapshot invokes the adapted provider.
func (provider SnapshotFunc) Snapshot(ctx context.Context, scope Scope) (json.RawMessage, error) {
	return provider(ctx, scope)
}

// EventEmitter validates, redacts, sequences, and durably publishes events.
type EventEmitter interface {
	Emit(context.Context, EventType, Correlation, json.RawMessage) (Event, error)
}

// Redactor removes sensitive fields before serialization.
type Redactor interface {
	Redact(json.RawMessage) (json.RawMessage, error)
}

type registeredHandler struct {
	handler     Handler
	description string
}

// Dispatcher enforces the common envelope before crossing application-service
// boundaries. Command serialization makes idempotency and revision checks
// deterministic, while queries remain concurrent.
type Dispatcher struct {
	journal  *Journal
	clock    types.Clock
	snapshot SnapshotProvider
	redactor Redactor

	mu        sync.RWMutex
	commandMu sync.Mutex
	handlers  map[Operation]registeredHandler
}

// NewDispatcher constructs a dispatcher with built-in recovery and catalog
// handlers.
func NewDispatcher(
	journal *Journal,
	clock types.Clock,
	snapshot SnapshotProvider,
	redactor Redactor,
) (*Dispatcher, error) {
	if journal == nil || clock == nil || snapshot == nil {
		return nil, fmt.Errorf("controlplane: journal, clock, and snapshot provider are required")
	}
	if redactor == nil {
		redactor = JSONRedactor{}
	}
	dispatcher := &Dispatcher{
		journal: journal, clock: clock, snapshot: snapshot, redactor: redactor,
		handlers: make(map[Operation]registeredHandler),
	}
	if err := dispatcher.registerBuiltins(); err != nil {
		return nil, err
	}
	return dispatcher, nil
}

// Register binds an operation to one production application service.
func (dispatcher *Dispatcher) Register(
	operation Operation,
	description string,
	handler Handler,
) error {
	if _, exists := operation.Kind(); !exists || handler == nil ||
		strings.TrimSpace(description) == "" {
		return fmt.Errorf("controlplane: valid operation, description, and handler are required")
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if _, exists := dispatcher.handlers[operation]; exists {
		return fmt.Errorf("controlplane: handler for %s already registered", operation)
	}
	dispatcher.handlers[operation] = registeredHandler{
		handler: handler, description: strings.TrimSpace(description),
	}
	return nil
}

// Dispatch authenticates actor scope, applies idempotency and optimistic
// revision checks, and invokes the registered production boundary.
func (dispatcher *Dispatcher) Dispatch(
	ctx context.Context,
	authenticatedActor uuid.UUID,
	request Request,
) Response {
	response := Response{
		ProtocolVersion: ProtocolVersion,
		RequestID:       request.RequestID,
	}
	if authenticatedActor == uuid.Nil || request.Scope.ActorID != authenticatedActor {
		response.Error = protocolError(ErrorUnauthorized, "actor scope is not authorized", false)
		return response
	}
	if err := request.Validate(); err != nil {
		response.Error = protocolError(ErrorInvalid, "invalid control-plane request", false)
		return response
	}
	dispatcher.mu.RLock()
	registered, exists := dispatcher.handlers[request.Operation]
	dispatcher.mu.RUnlock()
	if !exists {
		response.Error = protocolError(ErrorUnavailable, "operation is not available", true)
		return response
	}
	if request.Kind == KindCommand {
		dispatcher.commandMu.Lock()
		defer dispatcher.commandMu.Unlock()
		return dispatcher.dispatchCommand(ctx, registered.handler, request)
	}
	revision, err := dispatcher.currentRevision(ctx, request.Scope)
	if err != nil {
		response.Error = protocolError(ErrorInternal, "control-plane state is unavailable", true)
		return response
	}
	result, err := registered.handler.Handle(ctx, request, dispatcher)
	if err != nil {
		response.Error = publicProtocolError(err)
		response.Revision = revision
		return response
	}
	redacted, err := dispatcher.redactor.Redact(result)
	if err != nil {
		response.Error = protocolError(ErrorInternal, "response redaction failed", false)
		response.Revision = revision
		return response
	}
	response.Result = redacted
	response.Revision = revision
	return response
}

func (dispatcher *Dispatcher) dispatchCommand(
	ctx context.Context,
	handler Handler,
	request Request,
) Response {
	requestHash := hashRequest(request)
	cached, found, err := dispatcher.cachedResponse(
		ctx, request.Scope.ActorID, request.IdempotencyKey, requestHash,
	)
	if err != nil {
		return failureResponse(request.RequestID, err)
	}
	if found {
		cached.RequestID = request.RequestID
		return cached
	}
	revision, err := dispatcher.currentRevision(ctx, request.Scope)
	if err != nil {
		return failureResponse(request.RequestID, err)
	}
	if request.ExpectedRevision != nil && *request.ExpectedRevision != revision {
		response := Response{
			ProtocolVersion: ProtocolVersion, RequestID: request.RequestID,
			Revision: revision,
			Error:    protocolError(ErrorConflict, "expected revision does not match", false),
		}
		return response
	}
	result, err := handler.Handle(ctx, request, dispatcher)
	if err != nil {
		response := Response{
			ProtocolVersion: ProtocolVersion, RequestID: request.RequestID,
			Revision: revision, Error: publicProtocolError(err),
		}
		return response
	}
	redacted, err := dispatcher.redactor.Redact(result)
	if err != nil {
		return failureResponse(request.RequestID, err)
	}
	response := Response{
		ProtocolVersion: ProtocolVersion, RequestID: request.RequestID,
		Revision: revision + 1, Result: redacted,
	}
	if err := dispatcher.persistCommand(
		ctx, request, requestHash, response,
	); err != nil {
		return failureResponse(request.RequestID, err)
	}
	auditPayload, err := json.Marshal(map[string]any{
		"operation": request.Operation,
		"decision":  "accepted",
		"revision":  response.Revision,
	})
	if err != nil {
		return failureResponse(request.RequestID, err)
	}
	if _, err := dispatcher.Emit(
		ctx,
		EventPolicyDecision,
		Correlation{
			ActorID: request.Scope.ActorID, SessionID: cloneUUID(request.Scope.SessionID),
		},
		auditPayload,
	); err != nil {
		return failureResponse(request.RequestID, err)
	}
	return response
}

// Emit applies server-side redaction before durable sequencing.
func (dispatcher *Dispatcher) Emit(
	ctx context.Context,
	eventType EventType,
	correlation Correlation,
	payload json.RawMessage,
) (Event, error) {
	redacted, err := dispatcher.redactor.Redact(payload)
	if err != nil {
		return Event{}, err
	}
	event, err := NewEvent(eventType, correlation, redacted, dispatcher.clock.Now())
	if err != nil {
		return Event{}, err
	}
	return dispatcher.journal.Append(ctx, event)
}

// Recover attaches a redacted snapshot to an atomic replay. Browser
// connections always begin with this reconstructible frame.
func (dispatcher *Dispatcher) Recover(
	ctx context.Context,
	scope Scope,
	replay Replay,
) (Recovery, error) {
	snapshot, err := dispatcher.snapshot.Snapshot(ctx, scope)
	if err != nil {
		return Recovery{}, fmt.Errorf("controlplane: recovery snapshot: %w", err)
	}
	redacted, err := dispatcher.redactor.Redact(snapshot)
	if err != nil {
		return Recovery{}, err
	}
	recovery := Recovery{Replay: replay, Snapshot: redacted}
	if replay.Gap {
		recovery.GapMarker = &GapMarker{
			RequestedAfter: replay.AfterSequence,
			AvailableFrom:  replay.Earliest,
			Latest:         replay.Latest,
		}
	}
	return recovery, nil
}

func (dispatcher *Dispatcher) registerBuiltins() error {
	builtins := []struct {
		operation   Operation
		description string
		handler     Handler
	}{
		{
			OperationSystemSnapshot,
			"Return a redacted reconstructible system snapshot.",
			HandlerFunc(func(ctx context.Context, request Request, _ EventEmitter) (json.RawMessage, error) {
				return dispatcher.snapshot.Snapshot(ctx, request.Scope)
			}),
		},
		{
			OperationEventsReplay,
			"Replay sequenced events or return a snapshot with an explicit retention gap.",
			HandlerFunc(dispatcher.handleReplay),
		},
		{
			OperationEventsAcknowledge,
			"Persist the last event sequence processed by this client.",
			HandlerFunc(dispatcher.handleAcknowledge),
		},
		{
			OperationCommandsCatalog,
			"Return the server-owned command and query catalog.",
			HandlerFunc(dispatcher.handleCatalog),
		},
	}
	for _, builtin := range builtins {
		if err := dispatcher.Register(
			builtin.operation, builtin.description, builtin.handler,
		); err != nil {
			return err
		}
	}
	return nil
}

type replayRequest struct {
	AfterSequence uint64 `json:"after_sequence"`
	Limit         int    `json:"limit"`
}

func (dispatcher *Dispatcher) handleReplay(
	ctx context.Context,
	request Request,
	_ EventEmitter,
) (json.RawMessage, error) {
	var input replayRequest
	if err := decodePayload(request.Payload, &input); err != nil {
		return nil, err
	}
	replay, err := dispatcher.journal.ReplayActor(
		ctx, request.Scope.ActorID, input.AfterSequence, input.Limit,
	)
	if err != nil {
		return nil, err
	}
	recovery := Recovery{Replay: replay}
	if replay.Gap {
		fullRecovery, recoveryErr := dispatcher.Recover(ctx, request.Scope, replay)
		if recoveryErr != nil {
			return nil, recoveryErr
		}
		recovery = fullRecovery
	}
	return json.Marshal(recovery)
}

type acknowledgeRequest struct {
	ClientID string `json:"client_id"`
	Sequence uint64 `json:"sequence"`
}

func (dispatcher *Dispatcher) handleAcknowledge(
	ctx context.Context,
	request Request,
	_ EventEmitter,
) (json.RawMessage, error) {
	var input acknowledgeRequest
	if err := decodePayload(request.Payload, &input); err != nil {
		return nil, err
	}
	if err := dispatcher.journal.Acknowledge(
		ctx, request.Scope.ActorID, input.ClientID, input.Sequence,
	); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"acknowledged": input.Sequence})
}

func (dispatcher *Dispatcher) handleCatalog(
	context.Context,
	Request,
	EventEmitter,
) (json.RawMessage, error) {
	dispatcher.mu.RLock()
	defer dispatcher.mu.RUnlock()
	catalog := make([]CommandDescriptor, 0, len(operationKinds))
	for _, operation := range Operations() {
		kind, _ := operation.Kind()
		registered, available := dispatcher.handlers[operation]
		catalog = append(catalog, CommandDescriptor{
			Operation: operation, Kind: kind, Available: available,
			Description: registered.description,
		})
	}
	return json.Marshal(catalog)
}

func (dispatcher *Dispatcher) currentRevision(ctx context.Context, scope Scope) (uint64, error) {
	var revision uint64
	err := dispatcher.journal.db.QueryRowContext(
		ctx,
		`SELECT revision FROM controlplane_revisions WHERE scope_key = ?`,
		scopeKey(scope),
	).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("controlplane: read revision: %w", err)
	}
	return revision, nil
}

func (dispatcher *Dispatcher) cachedResponse(
	ctx context.Context,
	actorID uuid.UUID,
	idempotencyKey string,
	requestHash [32]byte,
) (Response, bool, error) {
	var storedHash []byte
	var encoded []byte
	err := dispatcher.journal.db.QueryRowContext(
		ctx,
		`SELECT request_hash, response FROM controlplane_idempotency
		 WHERE actor_id = ? AND idempotency_key = ?`,
		actorID.String(), idempotencyKey,
	).Scan(&storedHash, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return Response{}, false, nil
	}
	if err != nil {
		return Response{}, false, fmt.Errorf("controlplane: read idempotency result: %w", err)
	}
	if !equalHash(storedHash, requestHash) {
		return Response{}, false, ErrConflict
	}
	var response Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		return Response{}, false, fmt.Errorf("controlplane: decode idempotency result: %w", err)
	}
	return response, true, nil
}

func (dispatcher *Dispatcher) persistCommand(
	ctx context.Context,
	request Request,
	requestHash [32]byte,
	response Response,
) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("controlplane: encode idempotency result: %w", err)
	}
	tx, err := dispatcher.journal.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("controlplane: begin command state: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // Best-effort rollback after primary command-state error.
		}
	}()
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO controlplane_revisions(scope_key, revision) VALUES (?, ?)
		 ON CONFLICT(scope_key) DO UPDATE SET revision = excluded.revision`,
		scopeKey(request.Scope), response.Revision,
	); err != nil {
		return fmt.Errorf("controlplane: persist revision: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO controlplane_idempotency(
			actor_id, idempotency_key, request_hash, response, created_at
		 ) VALUES (?, ?, ?, ?, ?)`,
		request.Scope.ActorID.String(), request.IdempotencyKey, requestHash[:],
		encoded, dispatcher.clock.Now().UnixMicro(),
	); err != nil {
		return fmt.Errorf("controlplane: persist idempotency result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("controlplane: commit command state: %w", err)
	}
	committed = true
	return nil
}

func hashRequest(request Request) [32]byte {
	hashable := struct {
		Operation        Operation       `json:"operation"`
		Scope            Scope           `json:"scope"`
		ExpectedRevision *uint64         `json:"expected_revision,omitempty"`
		Payload          json.RawMessage `json:"payload"`
	}{
		Operation: request.Operation, Scope: request.Scope,
		ExpectedRevision: request.ExpectedRevision, Payload: request.Payload,
	}
	encoded, _ := json.Marshal(hashable)
	return blake3.Sum256(encoded)
}

func scopeKey(scope Scope) string {
	sessionID := ""
	if scope.SessionID != nil {
		sessionID = scope.SessionID.String()
	}
	return scope.ActorID.String() + "\x00" + sessionID + "\x00" +
		scope.Profile + "\x00" + scope.Channel
}

func equalHash(stored []byte, expected [32]byte) bool {
	if len(stored) != len(expected) {
		return false
	}
	var difference byte
	for index := range stored {
		difference |= stored[index] ^ expected[index]
	}
	return difference == 0
}

func decodePayload(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: invalid operation payload", ErrInvalidProtocol)
	}
	return nil
}

// PublicError lets a production boundary return a safe client-facing error.
type PublicError struct {
	Code      ErrorCode
	Message   string
	Retryable bool
}

// Error implements error.
func (public PublicError) Error() string { return public.Message }

func publicProtocolError(err error) *ProtocolError {
	var public PublicError
	if errors.As(err, &public) {
		return protocolError(public.Code, public.Message, public.Retryable)
	}
	if errors.Is(err, ErrConflict) {
		return protocolError(ErrorConflict, "request conflicts with durable state", false)
	}
	if errors.Is(err, ErrCursorAhead) {
		return protocolError(ErrorInvalid, "event cursor is ahead of journal", false)
	}
	return protocolError(ErrorInternal, "operation failed", false)
}

func failureResponse(requestID uuid.UUID, err error) Response {
	return Response{
		ProtocolVersion: ProtocolVersion, RequestID: requestID,
		Error: publicProtocolError(err),
	}
}

func protocolError(code ErrorCode, message string, retryable bool) *ProtocolError {
	return &ProtocolError{Code: code, Message: message, Retryable: retryable}
}
