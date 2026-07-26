package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

// ApprovalState is the durable approval lifecycle.
type ApprovalState string

const (
	ApprovalPending  ApprovalState = "pending"
	ApprovalResolved ApprovalState = "resolved"
	ApprovalExpired  ApprovalState = "expired"
)

// ApprovalDecision is the closed human response.
type ApprovalDecision string

const (
	DecisionApprove ApprovalDecision = "approve"
	DecisionDeny    ApprovalDecision = "deny"
)

// ApprovalScope binds a consequential operation to its running turn.
type ApprovalScope struct {
	ActorID   uuid.UUID
	SessionID *uuid.UUID
	TurnID    *uuid.UUID
	TaskID    *uuid.UUID
	OutcomeID *uuid.UUID
	AgentID   string
}

type approvalScopeContextKey struct{}

// WithApprovalScope carries authenticated turn scope into consequential execution.
func WithApprovalScope(ctx context.Context, scope ApprovalScope) context.Context {
	return context.WithValue(ctx, approvalScopeContextKey{}, scope)
}

// ApprovalScopeFromContext reads the authenticated consequential-operation scope.
func ApprovalScopeFromContext(ctx context.Context) (ApprovalScope, bool) {
	scope, ok := ctx.Value(approvalScopeContextKey{}).(ApprovalScope)
	return scope, ok && scope.ActorID != uuid.Nil
}

// ApprovalRequest is the exact redacted operation shown before consequence.
type ApprovalRequest struct {
	ID          uuid.UUID         `json:"approval_id"`
	ActorID     uuid.UUID         `json:"actor_id"`
	SessionID   *uuid.UUID        `json:"session_id,omitempty"`
	TurnID      *uuid.UUID        `json:"turn_id,omitempty"`
	Operation   string            `json:"operation"`
	Arguments   json.RawMessage   `json:"arguments"`
	Consequence string            `json:"consequence"`
	ExpiresAt   time.Time         `json:"expires_at"`
	State       ApprovalState     `json:"state"`
	Decision    *ApprovalDecision `json:"decision,omitempty"`
	ResolvedAt  *time.Time        `json:"resolved_at,omitempty"`
}

// ApprovalInput omits server-owned identity, timestamps, and lifecycle state.
type ApprovalInput struct {
	Scope       ApprovalScope
	Operation   string
	Arguments   json.RawMessage
	Consequence string
	TTL         time.Duration
}

// ErrApprovalDenied is returned to the operation boundary after human denial.
var ErrApprovalDenied = errors.New("controlplane: approval denied")

// ApprovalBroker durably records pending approvals and wakes in-process turns.
type ApprovalBroker struct {
	journal  *Journal
	clock    types.Clock
	redactor Redactor

	mu      sync.Mutex
	waiters map[uuid.UUID]chan ApprovalDecision
}

// NewApprovalBroker constructs the durable approval boundary.
func NewApprovalBroker(
	journal *Journal,
	clock types.Clock,
	redactor Redactor,
) (*ApprovalBroker, error) {
	if journal == nil || clock == nil {
		return nil, fmt.Errorf("controlplane: approval journal and clock are required")
	}
	if redactor == nil {
		redactor = JSONRedactor{}
	}
	return &ApprovalBroker{
		journal: journal, clock: clock, redactor: redactor,
		waiters: make(map[uuid.UUID]chan ApprovalDecision),
	}, nil
}

// Request durably publishes an approval and waits for response, expiry, or
// daemon cancellation. Cancellation leaves pending state reconstructible.
func (broker *ApprovalBroker) Request(
	ctx context.Context,
	input ApprovalInput,
) (ApprovalDecision, error) {
	request, err := broker.create(ctx, input)
	if err != nil {
		return "", err
	}
	waiter := make(chan ApprovalDecision, 1)
	broker.mu.Lock()
	broker.waiters[request.ID] = waiter
	broker.mu.Unlock()
	defer func() {
		broker.mu.Lock()
		delete(broker.waiters, request.ID)
		broker.mu.Unlock()
	}()
	current, err := broker.Get(ctx, request.ID)
	if err != nil {
		return "", err
	}
	if current.State == ApprovalResolved && current.Decision != nil {
		if *current.Decision == DecisionDeny {
			return *current.Decision, ErrApprovalDenied
		}
		return *current.Decision, nil
	}
	if current.State == ApprovalExpired {
		return "", ErrApprovalDenied
	}
	duration := request.ExpiresAt.Sub(broker.clock.Now())
	if duration <= 0 {
		if err := broker.expire(context.WithoutCancel(ctx), request); err != nil {
			return "", err
		}
		return "", ErrApprovalDenied
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case decision := <-waiter:
		if decision == DecisionDeny {
			return decision, ErrApprovalDenied
		}
		return decision, nil
	case <-timer.C:
		if err := broker.expire(context.WithoutCancel(ctx), request); err != nil {
			return "", err
		}
		return "", ErrApprovalDenied
	}
}

func (broker *ApprovalBroker) create(
	ctx context.Context,
	input ApprovalInput,
) (ApprovalRequest, error) {
	if input.Scope.ActorID == uuid.Nil || strings.TrimSpace(input.Operation) == "" ||
		strings.TrimSpace(input.Consequence) == "" || input.TTL <= 0 ||
		input.TTL > 15*time.Minute || len(input.Arguments) == 0 ||
		!json.Valid(input.Arguments) {
		return ApprovalRequest{}, fmt.Errorf("%w: invalid approval request", ErrInvalidProtocol)
	}
	arguments, err := broker.redactor.Redact(input.Arguments)
	if err != nil {
		return ApprovalRequest{}, err
	}
	request := ApprovalRequest{
		ID: uuid.New(), ActorID: input.Scope.ActorID,
		SessionID: cloneUUID(input.Scope.SessionID), TurnID: cloneUUID(input.Scope.TurnID),
		Operation: strings.TrimSpace(input.Operation), Arguments: arguments,
		Consequence: strings.TrimSpace(input.Consequence),
		ExpiresAt:   broker.clock.Now().Add(input.TTL), State: ApprovalPending,
	}
	if _, err := broker.journal.db.ExecContext(
		ctx,
		`INSERT INTO controlplane_approvals(
			approval_id, actor_id, session_id, turn_id, operation, arguments,
			consequence, expires_at, state
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ID.String(), request.ActorID.String(), nullableUUID(request.SessionID),
		nullableUUID(request.TurnID), request.Operation, []byte(request.Arguments),
		request.Consequence, request.ExpiresAt.UnixMicro(), string(request.State),
	); err != nil {
		return ApprovalRequest{}, fmt.Errorf("controlplane: persist approval request: %w", err)
	}
	if _, err := broker.emit(ctx, EventApprovalRequested, request); err != nil {
		return ApprovalRequest{}, err
	}
	return request, nil
}

// Respond resolves a pending approval for its authenticated actor.
func (broker *ApprovalBroker) Respond(
	ctx context.Context,
	actorID uuid.UUID,
	approvalID uuid.UUID,
	decision ApprovalDecision,
) (ApprovalRequest, error) {
	if actorID == uuid.Nil || approvalID == uuid.Nil ||
		(decision != DecisionApprove && decision != DecisionDeny) {
		return ApprovalRequest{}, fmt.Errorf("%w: invalid approval response", ErrInvalidProtocol)
	}
	request, err := broker.Get(ctx, approvalID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if request.ActorID != actorID {
		return ApprovalRequest{}, ErrUnauthorized
	}
	if request.State != ApprovalPending {
		return ApprovalRequest{}, ErrConflict
	}
	if !broker.clock.Now().Before(request.ExpiresAt) {
		if err := broker.expire(ctx, request); err != nil {
			return ApprovalRequest{}, err
		}
		return ApprovalRequest{}, ErrConflict
	}
	resolvedAt := broker.clock.Now()
	result, err := broker.journal.db.ExecContext(
		ctx,
		`UPDATE controlplane_approvals
		 SET state = ?, decision = ?, resolved_at = ?
		 WHERE approval_id = ? AND state = ?`,
		string(ApprovalResolved), string(decision), resolvedAt.UnixMicro(),
		approvalID.String(), string(ApprovalPending),
	)
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("controlplane: resolve approval: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return ApprovalRequest{}, ErrConflict
	}
	request.State = ApprovalResolved
	request.Decision = &decision
	request.ResolvedAt = &resolvedAt
	if _, err := broker.emit(ctx, EventApprovalResolved, request); err != nil {
		return ApprovalRequest{}, err
	}
	broker.mu.Lock()
	waiter := broker.waiters[approvalID]
	broker.mu.Unlock()
	if waiter != nil {
		select {
		case waiter <- decision:
		default:
		}
	}
	return request, nil
}

func (broker *ApprovalBroker) expire(ctx context.Context, request ApprovalRequest) error {
	expiredAt := broker.clock.Now()
	result, err := broker.journal.db.ExecContext(
		ctx,
		`UPDATE controlplane_approvals
		 SET state = ?, resolved_at = ?
		 WHERE approval_id = ? AND state = ?`,
		string(ApprovalExpired), expiredAt.UnixMicro(),
		request.ID.String(), string(ApprovalPending),
	)
	if err != nil {
		return fmt.Errorf("controlplane: expire approval: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("controlplane: inspect expired approval: %w", err)
	}
	if affected == 0 {
		return nil
	}
	request.State = ApprovalExpired
	request.ResolvedAt = &expiredAt
	_, err = broker.emit(ctx, EventApprovalExpired, request)
	return err
}

// Get reads one durable approval.
func (broker *ApprovalBroker) Get(
	ctx context.Context,
	approvalID uuid.UUID,
) (ApprovalRequest, error) {
	row := broker.journal.db.QueryRowContext(
		ctx,
		`SELECT approval_id, actor_id, session_id, turn_id, operation, arguments,
		        consequence, expires_at, state, decision, resolved_at
		 FROM controlplane_approvals WHERE approval_id = ?`,
		approvalID.String(),
	)
	request, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalRequest{}, PublicError{Code: ErrorNotFound, Message: "approval not found"}
	}
	return request, err
}

// Pending returns reconstructible pending approvals for one actor.
func (broker *ApprovalBroker) Pending(
	ctx context.Context,
	actorID uuid.UUID,
) ([]ApprovalRequest, error) {
	rows, err := broker.journal.db.QueryContext(
		ctx,
		`SELECT approval_id, actor_id, session_id, turn_id, operation, arguments,
		        consequence, expires_at, state, decision, resolved_at
		 FROM controlplane_approvals
		 WHERE actor_id = ? AND state = ?
		 ORDER BY expires_at, approval_id`,
		actorID.String(), string(ApprovalPending),
	)
	if err != nil {
		return nil, fmt.Errorf("controlplane: query pending approvals: %w", err)
	}
	defer rows.Close()
	pending := make([]ApprovalRequest, 0)
	for rows.Next() {
		request, scanErr := scanApproval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		pending = append(pending, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlplane: iterate pending approvals: %w", err)
	}
	return pending, nil
}

func (broker *ApprovalBroker) emit(
	ctx context.Context,
	eventType EventType,
	request ApprovalRequest,
) (Event, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Event{}, fmt.Errorf("controlplane: encode approval event: %w", err)
	}
	event, err := NewEvent(
		eventType,
		Correlation{
			ActorID: request.ActorID, SessionID: cloneUUID(request.SessionID),
			TurnID: cloneUUID(request.TurnID),
		},
		payload,
		broker.clock.Now(),
	)
	if err != nil {
		return Event{}, err
	}
	return broker.journal.Append(ctx, event)
}

type approvalScanner interface {
	Scan(...any) error
}

func scanApproval(scanner approvalScanner) (ApprovalRequest, error) {
	var request ApprovalRequest
	var approvalID string
	var actorID string
	var sessionID sql.NullString
	var turnID sql.NullString
	var arguments []byte
	var expiresAt int64
	var state string
	var decision sql.NullString
	var resolvedAt sql.NullInt64
	if err := scanner.Scan(
		&approvalID, &actorID, &sessionID, &turnID, &request.Operation,
		&arguments, &request.Consequence, &expiresAt, &state, &decision, &resolvedAt,
	); err != nil {
		return ApprovalRequest{}, err
	}
	var err error
	request.ID, err = uuid.Parse(approvalID)
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("controlplane: parse approval ID: %w", err)
	}
	request.ActorID, err = uuid.Parse(actorID)
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("controlplane: parse approval actor: %w", err)
	}
	request.SessionID, err = parseNullableUUID(sessionID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	request.TurnID, err = parseNullableUUID(turnID)
	if err != nil {
		return ApprovalRequest{}, err
	}
	request.Arguments = append([]byte(nil), arguments...)
	request.ExpiresAt = time.UnixMicro(expiresAt).UTC()
	request.State = ApprovalState(state)
	if decision.Valid {
		parsed := ApprovalDecision(decision.String)
		request.Decision = &parsed
	}
	if resolvedAt.Valid {
		parsed := time.UnixMicro(resolvedAt.Int64).UTC()
		request.ResolvedAt = &parsed
	}
	return request, nil
}

type approvalResponsePayload struct {
	ApprovalID uuid.UUID        `json:"approval_id"`
	Decision   ApprovalDecision `json:"decision"`
}

// RegisterApprovalHandler exposes approval.respond through the dispatcher.
func RegisterApprovalHandler(dispatcher *Dispatcher, broker *ApprovalBroker) error {
	if dispatcher == nil || broker == nil {
		return fmt.Errorf("controlplane: dispatcher and approval broker are required")
	}
	return dispatcher.Register(
		OperationApprovalRespond,
		"Resolve one exact pending consequential operation.",
		HandlerFunc(func(
			ctx context.Context,
			request Request,
			_ EventEmitter,
		) (json.RawMessage, error) {
			var payload approvalResponsePayload
			if err := decodePayload(request.Payload, &payload); err != nil {
				return nil, err
			}
			resolved, err := broker.Respond(
				ctx, request.Scope.ActorID, payload.ApprovalID, payload.Decision,
			)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resolved)
		}),
	)
}
