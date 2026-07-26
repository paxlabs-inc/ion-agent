// Package controllease coordinates exclusive operator and executor authority.
package controllease

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

const (
	stateKind       = "computer_control_lease_v1"
	LeaseVersion    = "ion.computer-control-lease.v1"
	DefaultLeaseTTL = 90 * time.Second
	MinimumLeaseTTL = 15 * time.Second
	MaximumLeaseTTL = 10 * time.Minute
)

var (
	ErrHeld         = errors.New("computer control: resource is held by the operator")
	ErrConflict     = errors.New("computer control: lease conflict")
	ErrNotFound     = errors.New("computer control: lease not found")
	ErrUnauthorized = errors.New("computer control: lease is not authorized")
	ErrStale        = errors.New("computer control: stale lease revision")
)

type ResourceKind string

const (
	ResourceBrowser  ResourceKind = "browser"
	ResourceDesktop  ResourceKind = "desktop"
	ResourceTerminal ResourceKind = "terminal"
)

type State string

const (
	StateAvailable State = "available"
	StateActive    State = "active"
	StateReleased  State = "released"
	StateExpired   State = "expired"
)

type Authority string

const (
	AuthorityExecutor Authority = "executor"
	AuthorityOperator Authority = "operator"
)

type Store interface {
	SaveLivingState(context.Context, string, string, json.RawMessage) error
	LoadLivingState(context.Context, string, string) (json.RawMessage, error)
}

type Target struct {
	ActorID    uuid.UUID    `json:"actor_id"`
	SessionID  *uuid.UUID   `json:"session_id,omitempty"`
	Kind       ResourceKind `json:"resource_kind"`
	ResourceID string       `json:"resource_id"`
}

type Owner struct {
	TurnID      *uuid.UUID `json:"turn_id,omitempty"`
	TaskID      *uuid.UUID `json:"task_id,omitempty"`
	AgentID     string     `json:"agent_id"`
	ToolEventID *uuid.UUID `json:"tool_event_id,omitempty"`
	Action      string     `json:"action"`
	Revision    uint64     `json:"revision"`
}

type Lease struct {
	ProtocolVersion string     `json:"protocol_version"`
	ID              uuid.UUID  `json:"lease_id,omitempty"`
	Target          Target     `json:"target"`
	Owner           Owner      `json:"owner"`
	State           State      `json:"state"`
	Authority       Authority  `json:"authority"`
	Revision        uint64     `json:"revision"`
	AcquiredAt      *time.Time `json:"acquired_at,omitempty"`
	RenewedAt       *time.Time `json:"renewed_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	ReleasedAt      *time.Time `json:"released_at,omitempty"`
	Reconciliation  string     `json:"reconciliation"`
}

type Service struct {
	store Store
	clock types.Clock

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(store Store, clock types.Clock) (*Service, error) {
	if store == nil || clock == nil {
		return nil, fmt.Errorf("computer control: store and clock are required")
	}
	return &Service{store: store, clock: clock, locks: make(map[string]*sync.Mutex)}, nil
}

func (service *Service) Status(ctx context.Context, target Target) (Lease, error) {
	key, err := targetKey(target)
	if err != nil {
		return Lease{}, err
	}
	lock := service.resourceLock(key)
	lock.Lock()
	defer lock.Unlock()
	return service.loadEffective(ctx, key, target)
}

func (service *Service) Acquire(
	ctx context.Context,
	target Target,
	owner Owner,
	expectedRevision uint64,
	ttl time.Duration,
) (Lease, error) {
	key, err := targetKey(target)
	if err != nil {
		return Lease{}, err
	}
	if err := validateOwner(owner); err != nil {
		return Lease{}, err
	}
	ttl, err = boundedTTL(ttl)
	if err != nil {
		return Lease{}, err
	}
	lock := service.resourceLock(key)
	lock.Lock()
	defer lock.Unlock()
	current, err := service.loadEffective(ctx, key, target)
	if err != nil {
		return Lease{}, err
	}
	if current.Revision != expectedRevision {
		return Lease{}, ErrStale
	}
	if current.State == StateActive {
		return Lease{}, ErrConflict
	}
	now := service.clock.Now().UTC()
	expires := now.Add(ttl)
	next := Lease{
		ProtocolVersion: LeaseVersion,
		ID:              uuid.New(),
		Target:          cloneTarget(target),
		Owner:           cloneOwner(owner),
		State:           StateActive,
		Authority:       AuthorityOperator,
		Revision:        current.Revision + 1,
		AcquiredAt:      &now,
		RenewedAt:       &now,
		ExpiresAt:       &expires,
		Reconciliation:  "executor_paused_at_action_boundary",
	}
	if err := service.save(ctx, key, next); err != nil {
		return Lease{}, err
	}
	return next, nil
}

func (service *Service) Renew(
	ctx context.Context,
	target Target,
	leaseID uuid.UUID,
	expectedRevision uint64,
	ttl time.Duration,
) (Lease, error) {
	key, err := targetKey(target)
	if err != nil {
		return Lease{}, err
	}
	if leaseID == uuid.Nil {
		return Lease{}, ErrNotFound
	}
	ttl, err = boundedTTL(ttl)
	if err != nil {
		return Lease{}, err
	}
	lock := service.resourceLock(key)
	lock.Lock()
	defer lock.Unlock()
	current, err := service.loadEffective(ctx, key, target)
	if err != nil {
		return Lease{}, err
	}
	if current.ID != leaseID {
		return Lease{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Lease{}, ErrStale
	}
	if current.State != StateActive || current.Authority != AuthorityOperator {
		return Lease{}, ErrConflict
	}
	now := service.clock.Now().UTC()
	expires := now.Add(ttl)
	current.Revision++
	current.RenewedAt = &now
	current.ExpiresAt = &expires
	current.Reconciliation = "executor_paused_at_action_boundary"
	if err := service.save(ctx, key, current); err != nil {
		return Lease{}, err
	}
	return current, nil
}

func (service *Service) Release(
	ctx context.Context,
	target Target,
	leaseID uuid.UUID,
	expectedRevision uint64,
) (Lease, error) {
	key, err := targetKey(target)
	if err != nil {
		return Lease{}, err
	}
	if leaseID == uuid.Nil {
		return Lease{}, ErrNotFound
	}
	lock := service.resourceLock(key)
	lock.Lock()
	defer lock.Unlock()
	current, err := service.loadEffective(ctx, key, target)
	if err != nil {
		return Lease{}, err
	}
	if current.ID != leaseID {
		return Lease{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Lease{}, ErrStale
	}
	if current.State != StateActive || current.Authority != AuthorityOperator {
		return Lease{}, ErrConflict
	}
	now := service.clock.Now().UTC()
	current.State = StateReleased
	current.Authority = AuthorityExecutor
	current.Revision++
	current.ReleasedAt = &now
	current.Reconciliation = "executor_resumed_after_operator_release"
	if err := service.save(ctx, key, current); err != nil {
		return Lease{}, err
	}
	return current, nil
}

func (service *Service) ReconcileStopped(
	ctx context.Context,
	target Target,
	reason string,
) error {
	key, err := targetKey(target)
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 256 {
		return fmt.Errorf("computer control: bounded reconciliation reason is required")
	}
	lock := service.resourceLock(key)
	lock.Lock()
	defer lock.Unlock()
	current, err := service.loadEffective(ctx, key, target)
	if err != nil {
		return err
	}
	if current.State != StateActive {
		return nil
	}
	now := service.clock.Now().UTC()
	current.State = StateReleased
	current.Authority = AuthorityExecutor
	current.Revision++
	current.ReleasedAt = &now
	current.Reconciliation = reason
	return service.save(ctx, key, current)
}

func (service *Service) BeginAutomation(
	ctx context.Context,
	target Target,
) (func(), error) {
	key, err := targetKey(target)
	if err != nil {
		return nil, err
	}
	lock := service.resourceLock(key)
	lock.Lock()
	current, err := service.loadEffective(ctx, key, target)
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	if current.State == StateActive && current.Authority == AuthorityOperator {
		lock.Unlock()
		return nil, ErrHeld
	}
	return lock.Unlock, nil
}

func (service *Service) BeginOperator(
	ctx context.Context,
	target Target,
	leaseID uuid.UUID,
	expectedRevision uint64,
) (func(), Lease, error) {
	key, err := targetKey(target)
	if err != nil {
		return nil, Lease{}, err
	}
	lock := service.resourceLock(key)
	lock.Lock()
	current, err := service.loadEffective(ctx, key, target)
	if err != nil {
		lock.Unlock()
		return nil, Lease{}, err
	}
	if current.ID != leaseID || current.State != StateActive ||
		current.Authority != AuthorityOperator {
		lock.Unlock()
		return nil, Lease{}, ErrUnauthorized
	}
	if current.Revision != expectedRevision {
		lock.Unlock()
		return nil, Lease{}, ErrStale
	}
	return lock.Unlock, current, nil
}

func (service *Service) resourceLock(key string) *sync.Mutex {
	service.mu.Lock()
	defer service.mu.Unlock()
	if found := service.locks[key]; found != nil {
		return found
	}
	created := &sync.Mutex{}
	service.locks[key] = created
	return created
}

func (service *Service) loadEffective(
	ctx context.Context,
	key string,
	target Target,
) (Lease, error) {
	raw, err := service.store.LoadLivingState(ctx, stateKind, key)
	if errors.Is(err, sql.ErrNoRows) {
		return availableLease(target), nil
	}
	if err != nil {
		return Lease{}, err
	}
	var current Lease
	if json.Unmarshal(raw, &current) != nil ||
		current.ProtocolVersion != LeaseVersion ||
		current.Target.ActorID != target.ActorID ||
		!sameUUID(current.Target.SessionID, target.SessionID) ||
		current.Target.Kind != target.Kind ||
		current.Target.ResourceID != target.ResourceID ||
		current.Revision == 0 {
		return Lease{}, fmt.Errorf("computer control: invalid durable lease")
	}
	if current.State == StateActive &&
		current.ExpiresAt != nil &&
		!service.clock.Now().UTC().Before(*current.ExpiresAt) {
		now := service.clock.Now().UTC()
		current.State = StateExpired
		current.Authority = AuthorityExecutor
		current.Revision++
		current.ReleasedAt = &now
		current.Reconciliation = "lease_expired_executor_resumed"
		if err := service.save(ctx, key, current); err != nil {
			return Lease{}, err
		}
	}
	return current, nil
}

func (service *Service) save(ctx context.Context, key string, lease Lease) error {
	raw, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	return service.store.SaveLivingState(ctx, stateKind, key, raw)
}

func availableLease(target Target) Lease {
	return Lease{
		ProtocolVersion: LeaseVersion,
		Target:          cloneTarget(target),
		State:           StateAvailable,
		Authority:       AuthorityExecutor,
		Reconciliation:  "executor_active",
	}
}

func targetKey(target Target) (string, error) {
	target.ResourceID = strings.TrimSpace(target.ResourceID)
	if target.ActorID == uuid.Nil || target.ResourceID == "" ||
		len(target.ResourceID) > 512 {
		return "", fmt.Errorf("computer control: bounded resource target is required")
	}
	switch target.Kind {
	case ResourceBrowser, ResourceDesktop, ResourceTerminal:
	default:
		return "", fmt.Errorf("computer control: unsupported resource kind")
	}
	session := "none"
	if target.SessionID != nil {
		if *target.SessionID == uuid.Nil {
			return "", fmt.Errorf("computer control: session target is invalid")
		}
		session = target.SessionID.String()
	}
	return strings.Join([]string{
		target.ActorID.String(), session, string(target.Kind), target.ResourceID,
	}, ":"), nil
}

func validateOwner(owner Owner) error {
	owner.AgentID = strings.TrimSpace(owner.AgentID)
	owner.Action = strings.TrimSpace(owner.Action)
	if owner.AgentID == "" || owner.Action == "" || owner.Revision == 0 ||
		(owner.TaskID == nil && owner.TurnID == nil) {
		return fmt.Errorf("computer control: authoritative executor owner is required")
	}
	return nil
}

func boundedTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		ttl = DefaultLeaseTTL
	}
	if ttl < MinimumLeaseTTL || ttl > MaximumLeaseTTL {
		return 0, fmt.Errorf("computer control: lease duration is out of bounds")
	}
	return ttl, nil
}

func cloneTarget(target Target) Target {
	target.SessionID = cloneUUID(target.SessionID)
	target.ResourceID = strings.TrimSpace(target.ResourceID)
	return target
}

func cloneOwner(owner Owner) Owner {
	owner.TurnID = cloneUUID(owner.TurnID)
	owner.TaskID = cloneUUID(owner.TaskID)
	owner.ToolEventID = cloneUUID(owner.ToolEventID)
	owner.AgentID = strings.TrimSpace(owner.AgentID)
	owner.Action = strings.TrimSpace(owner.Action)
	return owner
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sameUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
