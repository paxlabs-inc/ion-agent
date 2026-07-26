package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	securitycron "github.com/paxlabs-inc/ion-agent/internal/security/cron"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	stateKind           = "agent_chronos"
	stateVersion        = 1
	defaultTick         = time.Second
	defaultClaimLease   = 2 * time.Minute
	defaultMaxFailures  = 5
	defaultBatch        = 32
	maxAlarmsPerActor   = 256
	maxWakeMessageBytes = 32 << 10
	maxPayloadBytes     = 64 << 10
	maxSafeErrorBytes   = 512
	maxAlarmLabelBytes  = 256
	maxIdempotencyBytes = 256
	maxFailureLimit     = 20
	retryBaseBackoff    = 30 * time.Second
	retryMaximumBackoff = 15 * time.Minute
)

type Kind string

const (
	KindOnce Kind = "once"
	KindCron Kind = "cron"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusClaimed   Status = "claimed"
	StatusFired     Status = "fired"
	StatusCancelled Status = "cancelled"
	StatusFailed    Status = "failed"
)

// Alarm is the encrypted durable timer. WakeMessage and Payload never enter
// operator projections; they are returned only through actor-scoped tools and
// delivered to the owning session.
type Alarm struct {
	ID               uuid.UUID       `json:"id"`
	ActorID          uuid.UUID       `json:"actor_id"`
	SessionID        uuid.UUID       `json:"session_id"`
	Label            string          `json:"label"`
	Kind             Kind            `json:"kind"`
	FireAt           *time.Time      `json:"fire_at,omitempty"`
	CronExpr         string          `json:"cron_expr,omitempty"`
	Timezone         string          `json:"timezone"`
	NextFireAt       time.Time       `json:"next_fire_at"`
	WakeMessage      string          `json:"wake_message"`
	Payload          json.RawMessage `json:"payload,omitempty"`
	Status           Status          `json:"status"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
	DefinitionHash   string          `json:"definition_hash"`
	MaxFailures      int             `json:"max_failures"`
	FailureCount     int             `json:"failure_count"`
	LastFailureCount int             `json:"last_failure_count,omitempty"`
	LastError        string          `json:"last_error,omitempty"`
	ClaimedAt        *time.Time      `json:"claimed_at,omitempty"`
	OccurrenceID     string          `json:"occurrence_id,omitempty"`
	OccurrenceAt     *time.Time      `json:"occurrence_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	LastFiredAt      *time.Time      `json:"last_fired_at,omitempty"`
}

type document struct {
	Version int     `json:"version"`
	Alarms  []Alarm `json:"alarms"`
}

type CreateRequest struct {
	ActorID        uuid.UUID
	SessionID      uuid.UUID
	Label          string
	Kind           Kind
	DelaySeconds   int64
	FireAt         string
	CronExpr       string
	Timezone       string
	WakeMessage    string
	Payload        json.RawMessage
	IdempotencyKey string
	MaxFailures    int
}

// Projection is safe for shared operator surfaces.
type Projection struct {
	ID               uuid.UUID  `json:"id"`
	Label            string     `json:"label"`
	Kind             Kind       `json:"kind"`
	Status           Status     `json:"status"`
	Timezone         string     `json:"timezone,omitempty"`
	CronExpr         string     `json:"cron_expr,omitempty"`
	NextFireAt       *time.Time `json:"next_fire_at,omitempty"`
	LastFiredAt      *time.Time `json:"last_fired_at,omitempty"`
	FailureCount     int        `json:"failure_count"`
	LastFailureCount int        `json:"last_failure_count,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	Source           string     `json:"source"`
}

type Health struct {
	Status      string     `json:"status"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Source      string     `json:"source"`
}

type Wake struct {
	AlarmID      uuid.UUID
	OccurrenceID string
	ActorID      uuid.UUID
	SessionID    uuid.UUID
	Message      string
	Payload      json.RawMessage
}

type Waker interface {
	Wake(context.Context, Wake) error
}

type StateStore interface {
	SaveLivingState(context.Context, string, string, json.RawMessage) error
	ListLivingStates(context.Context, string) ([]session.LivingState, error)
}

type Config struct {
	Store       StateStore
	Clock       types.Clock
	Waker       Waker
	Tick        time.Duration
	ClaimLease  time.Duration
	Batch       int
	MaxFailures int
	Scanner     securitycron.Scanner
}

// Service owns every local actor's schedules. The encrypted store is ground
// truth; the in-memory map is a disposable projection reconstructed at boot.
type Service struct {
	store       StateStore
	clock       types.Clock
	waker       Waker
	tick        time.Duration
	claimLease  time.Duration
	batch       int
	maxFailures int
	scanner     securitycron.Scanner

	mu        sync.RWMutex
	documents map[uuid.UUID]document
	cancel    context.CancelFunc
	done      chan struct{}
	running   bool
	lastSweep *time.Time
	lastOK    *time.Time
	lastError string
}

func New(ctx context.Context, config Config) (*Service, error) {
	if ctx == nil || config.Store == nil || config.Clock == nil || config.Waker == nil {
		return nil, errors.New("scheduler: store, clock, and waker are required")
	}
	if config.Tick <= 0 {
		config.Tick = defaultTick
	}
	if config.ClaimLease <= 0 {
		config.ClaimLease = defaultClaimLease
	}
	if config.Batch <= 0 {
		config.Batch = defaultBatch
	}
	if config.MaxFailures <= 0 {
		config.MaxFailures = defaultMaxFailures
	}
	if config.Scanner == nil {
		config.Scanner = securitycron.DefaultScanner{}
	}
	service := &Service{
		store: config.Store, clock: config.Clock, waker: config.Waker,
		tick: config.Tick, claimLease: config.ClaimLease,
		batch: config.Batch, maxFailures: config.MaxFailures,
		scanner:   config.Scanner,
		documents: make(map[uuid.UUID]document),
	}
	if err := service.restore(ctx); err != nil {
		return nil, err
	}
	return service, nil
}

func (service *Service) Start(parent context.Context) {
	service.mu.Lock()
	if service.cancel != nil {
		service.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	service.cancel = cancel
	service.done = make(chan struct{})
	service.running = true
	done := service.done
	service.mu.Unlock()
	go func() {
		defer func() {
			service.mu.Lock()
			service.running = false
			service.mu.Unlock()
			close(done)
		}()
		ticker := time.NewTicker(service.tick)
		defer ticker.Stop()
		_ = service.ProcessDue(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = service.ProcessDue(ctx)
			}
		}
	}()
}

func (service *Service) Close() {
	if service == nil {
		return
	}
	service.mu.Lock()
	cancel, done := service.cancel, service.done
	service.cancel = nil
	service.done = nil
	service.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (service *Service) restore(ctx context.Context) error {
	states, err := service.store.ListLivingStates(ctx, stateKind)
	if err != nil {
		return fmt.Errorf("scheduler: restore encrypted alarms: %w", err)
	}
	now := service.clock.Now().UTC()
	for _, state := range states {
		actorID, err := uuid.Parse(state.Scope)
		if err != nil {
			return fmt.Errorf("scheduler: invalid actor scope in encrypted state")
		}
		var decoded document
		if err := json.Unmarshal(state.State, &decoded); err != nil ||
			decoded.Version != stateVersion {
			return fmt.Errorf("scheduler: invalid encrypted actor document")
		}
		changed := false
		for index := range decoded.Alarms {
			if err := validateAlarm(decoded.Alarms[index]); err != nil {
				return err
			}
			if decoded.Alarms[index].ActorID != actorID {
				return errors.New("scheduler: encrypted actor scope mismatch")
			}
			if decoded.Alarms[index].Status == StatusClaimed {
				decoded.Alarms[index].Status = StatusActive
				decoded.Alarms[index].ClaimedAt = nil
				decoded.Alarms[index].NextFireAt = now
				decoded.Alarms[index].LastError = "delivery interrupted by daemon restart; retry is due"
				decoded.Alarms[index].UpdatedAt = now
				changed = true
			}
		}
		sortAlarms(decoded.Alarms)
		service.documents[actorID] = decoded
		if changed {
			if err := service.saveDocument(ctx, actorID, decoded); err != nil {
				return err
			}
		}
	}
	return nil
}

func (service *Service) Create(
	ctx context.Context,
	request CreateRequest,
) (Alarm, bool, error) {
	now := service.clock.Now().UTC()
	request.Label = strings.TrimSpace(request.Label)
	request.WakeMessage = strings.TrimSpace(request.WakeMessage)
	request.Timezone = strings.TrimSpace(request.Timezone)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.ActorID == uuid.Nil || request.SessionID == uuid.Nil {
		return Alarm{}, false, errors.New("scheduler: authenticated actor and session are required")
	}
	if request.Label == "" || len(request.Label) > maxAlarmLabelBytes {
		return Alarm{}, false, errors.New("scheduler: bounded label is required")
	}
	if request.WakeMessage == "" || len(request.WakeMessage) > maxWakeMessageBytes {
		return Alarm{}, false, errors.New("scheduler: bounded wake_message is required")
	}
	if findings := service.scanner.Scan(request.WakeMessage); len(findings) != 0 {
		return Alarm{}, false, fmt.Errorf(
			"scheduler: wake_message failed prompt-injection scan: %s",
			summarizeFindings(findings),
		)
	}
	if len(request.IdempotencyKey) > maxIdempotencyBytes {
		return Alarm{}, false, errors.New("scheduler: idempotency_key is too long")
	}
	if len(request.Payload) == 0 {
		request.Payload = json.RawMessage(`{}`)
	}
	if len(request.Payload) > maxPayloadBytes || !json.Valid(request.Payload) {
		return Alarm{}, false, errors.New("scheduler: payload must be bounded valid JSON")
	}
	if request.MaxFailures <= 0 {
		request.MaxFailures = service.maxFailures
	}
	if request.MaxFailures < 1 || request.MaxFailures > maxFailureLimit {
		return Alarm{}, false, fmt.Errorf("scheduler: max_failures must be between 1 and %d", maxFailureLimit)
	}
	timezone := request.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	var next time.Time
	var fireAt *time.Time
	var err error
	switch request.Kind {
	case KindOnce:
		next, err = nextOnce(request.DelaySeconds, request.FireAt, now)
		if err == nil {
			resolved := next
			fireAt = &resolved
			timezone = "UTC"
		}
	case KindCron:
		next, err = nextCron(request.CronExpr, timezone, now)
	default:
		err = errors.New("scheduler: kind must be once or cron")
	}
	if err != nil {
		return Alarm{}, false, err
	}
	definitionHash, err := hashDefinition(request, timezone)
	if err != nil {
		return Alarm{}, false, err
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	decoded := cloneDocument(service.documents[request.ActorID])
	if decoded.Version == 0 {
		decoded.Version = stateVersion
	}
	if request.IdempotencyKey != "" {
		for _, existing := range decoded.Alarms {
			if existing.IdempotencyKey == request.IdempotencyKey {
				if existing.DefinitionHash != definitionHash {
					return Alarm{}, false, errors.New(
						"scheduler: idempotency_key conflicts with a different alarm",
					)
				}
				return cloneAlarm(existing), true, nil
			}
		}
	}
	decoded.Alarms = makeRoom(decoded.Alarms)
	if len(decoded.Alarms) >= maxAlarmsPerActor {
		return Alarm{}, false, errors.New("scheduler: actor alarm limit reached")
	}
	alarm := Alarm{
		ID: uuid.New(), ActorID: request.ActorID, SessionID: request.SessionID,
		Label: request.Label, Kind: request.Kind, FireAt: fireAt,
		CronExpr: strings.TrimSpace(request.CronExpr), Timezone: timezone,
		NextFireAt: next, WakeMessage: request.WakeMessage,
		Payload: append(json.RawMessage(nil), request.Payload...),
		Status:  StatusActive, IdempotencyKey: request.IdempotencyKey,
		DefinitionHash: definitionHash,
		MaxFailures:    request.MaxFailures, CreatedAt: now, UpdatedAt: now,
	}
	decoded.Alarms = append(decoded.Alarms, alarm)
	sortAlarms(decoded.Alarms)
	if err := service.saveDocument(ctx, request.ActorID, decoded); err != nil {
		return Alarm{}, false, err
	}
	service.documents[request.ActorID] = decoded
	return cloneAlarm(alarm), false, nil
}

func (service *Service) List(actorID uuid.UUID, limit int) []Alarm {
	if actorID == uuid.Nil {
		return nil
	}
	if limit <= 0 || limit > maxAlarmsPerActor {
		limit = 100
	}
	service.mu.RLock()
	alarms := service.documents[actorID].Alarms
	if len(alarms) > limit {
		alarms = alarms[:limit]
	}
	result := cloneAlarms(alarms)
	service.mu.RUnlock()
	return result
}

func (service *Service) Get(actorID, alarmID uuid.UUID) (Alarm, error) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	for _, alarm := range service.documents[actorID].Alarms {
		if alarm.ID == alarmID {
			return cloneAlarm(alarm), nil
		}
	}
	return Alarm{}, errors.New("scheduler: alarm not found")
}

func (service *Service) Cancel(
	ctx context.Context,
	actorID uuid.UUID,
	alarmID uuid.UUID,
) (Alarm, error) {
	if actorID == uuid.Nil || alarmID == uuid.Nil {
		return Alarm{}, errors.New("scheduler: actor and alarm are required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	decoded := cloneDocument(service.documents[actorID])
	for index := range decoded.Alarms {
		alarm := &decoded.Alarms[index]
		if alarm.ID != alarmID {
			continue
		}
		if alarm.Status == StatusActive || alarm.Status == StatusClaimed {
			alarm.Status = StatusCancelled
			alarm.ClaimedAt = nil
			alarm.UpdatedAt = service.clock.Now().UTC()
			result := cloneAlarm(*alarm)
			sortAlarms(decoded.Alarms)
			if err := service.saveDocument(ctx, actorID, decoded); err != nil {
				return Alarm{}, err
			}
			service.documents[actorID] = decoded
			return result, nil
		}
		return cloneAlarm(*alarm), nil
	}
	return Alarm{}, errors.New("scheduler: alarm not found")
}

func (service *Service) Projections(actorID uuid.UUID) []Projection {
	alarms := service.List(actorID, maxAlarmsPerActor)
	result := make([]Projection, 0, len(alarms))
	for _, alarm := range alarms {
		projection := Projection{
			ID: alarm.ID, Label: alarm.Label, Kind: alarm.Kind,
			Status: alarm.Status, Timezone: alarm.Timezone,
			CronExpr: alarm.CronExpr, LastFiredAt: cloneTime(alarm.LastFiredAt),
			FailureCount:     alarm.FailureCount,
			LastFailureCount: alarm.LastFailureCount,
			LastError:        alarm.LastError, Source: "agent_schedule",
		}
		if alarm.Status == StatusActive || alarm.Status == StatusClaimed {
			next := alarm.NextFireAt
			projection.NextFireAt = &next
		}
		result = append(result, projection)
	}
	return result
}

func (service *Service) Health() Health {
	service.mu.RLock()
	defer service.mu.RUnlock()
	status := "stopped"
	if service.running {
		status = "ready"
		if service.lastError != "" {
			status = "degraded"
		}
	}
	return Health{
		Status:      status,
		LastAttempt: cloneTime(service.lastSweep),
		LastSuccess: cloneTime(service.lastOK),
		LastError:   service.lastError,
		Source:      "agent_scheduler",
	}
}

// ProcessDue claims and delivers a bounded due batch. It is public so restart
// and deterministic acceptance can exercise the exact production algorithm.
func (service *Service) ProcessDue(ctx context.Context) (result error) {
	defer func() {
		now := service.clock.Now().UTC()
		service.mu.Lock()
		service.lastSweep = &now
		if result == nil {
			service.lastOK = &now
			service.lastError = ""
		} else {
			service.lastError = safeError(result)
		}
		service.mu.Unlock()
	}()
	for count := 0; count < service.batch; count++ {
		alarm, found, err := service.claimNext(ctx)
		if err != nil {
			return errors.Join(result, err)
		}
		if !found {
			return result
		}
		wake := Wake{
			AlarmID: alarm.ID, OccurrenceID: alarm.OccurrenceID,
			ActorID: alarm.ActorID, SessionID: alarm.SessionID,
			Message: alarm.WakeMessage,
			Payload: append(json.RawMessage(nil), alarm.Payload...),
		}
		var wakeErr error
		if findings := service.scanner.Scan(alarm.WakeMessage); len(findings) != 0 {
			wakeErr = fmt.Errorf(
				"scheduler: wake_message failed execution-time prompt-injection scan: %s",
				summarizeFindings(findings),
			)
		} else {
			wakeErr = service.waker.Wake(ctx, wake)
		}
		if err := service.finish(ctx, alarm, wakeErr); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (service *Service) claimNext(
	ctx context.Context,
) (Alarm, bool, error) {
	now := service.clock.Now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	var selectedActor uuid.UUID
	selectedIndex := -1
	var selectedTime time.Time
	for actorID, decoded := range service.documents {
		for index, alarm := range decoded.Alarms {
			due := alarm.Status == StatusActive && !alarm.NextFireAt.After(now)
			expiredClaim := alarm.Status == StatusClaimed && alarm.ClaimedAt != nil &&
				!alarm.ClaimedAt.Add(service.claimLease).After(now)
			if !due && !expiredClaim {
				continue
			}
			if selectedIndex < 0 || alarm.NextFireAt.Before(selectedTime) ||
				(alarm.NextFireAt.Equal(selectedTime) && alarm.ID.String() <
					service.documents[selectedActor].Alarms[selectedIndex].ID.String()) {
				selectedActor, selectedIndex, selectedTime = actorID, index, alarm.NextFireAt
			}
		}
	}
	if selectedIndex < 0 {
		return Alarm{}, false, nil
	}
	decoded := cloneDocument(service.documents[selectedActor])
	alarm := &decoded.Alarms[selectedIndex]
	alarm.Status = StatusClaimed
	alarm.ClaimedAt = &now
	alarm.UpdatedAt = now
	if alarm.OccurrenceID == "" {
		occurrenceAt := alarm.NextFireAt.UTC()
		alarm.OccurrenceAt = &occurrenceAt
		alarm.OccurrenceID = alarm.ID.String() + ":" +
			fmt.Sprintf("%d", occurrenceAt.UnixMicro())
	}
	if err := service.saveDocument(ctx, selectedActor, decoded); err != nil {
		return Alarm{}, false, err
	}
	service.documents[selectedActor] = decoded
	return cloneAlarm(*alarm), true, nil
}

func (service *Service) finish(
	ctx context.Context,
	claimed Alarm,
	wakeErr error,
) error {
	now := service.clock.Now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	decoded := cloneDocument(service.documents[claimed.ActorID])
	for index := range decoded.Alarms {
		alarm := &decoded.Alarms[index]
		if alarm.ID != claimed.ID || alarm.OccurrenceID != claimed.OccurrenceID {
			continue
		}
		if alarm.Status == StatusCancelled {
			return nil
		}
		if alarm.Status != StatusClaimed {
			return errors.New("scheduler: claimed occurrence changed before completion")
		}
		alarm.ClaimedAt = nil
		alarm.UpdatedAt = now
		if wakeErr == nil {
			firedAt := now
			alarm.LastFiredAt = &firedAt
			alarm.FailureCount = 0
			alarm.LastFailureCount = 0
			alarm.LastError = ""
			if alarm.Kind == KindOnce {
				alarm.Status = StatusFired
			} else {
				next, err := nextCron(alarm.CronExpr, alarm.Timezone, now)
				if err != nil {
					alarm.Status = StatusFailed
					alarm.LastError = safeError(err)
				} else {
					alarm.Status = StatusActive
					alarm.NextFireAt = next
				}
			}
			alarm.OccurrenceID = ""
			alarm.OccurrenceAt = nil
		} else {
			alarm.FailureCount++
			alarm.LastError = safeError(wakeErr)
			if alarm.FailureCount < alarm.MaxFailures {
				alarm.Status = StatusActive
				alarm.NextFireAt = now.Add(retryBackoff(alarm.FailureCount - 1))
			} else if alarm.Kind == KindOnce {
				alarm.Status = StatusFailed
				alarm.LastFailureCount = alarm.FailureCount
			} else {
				alarm.LastFailureCount = alarm.FailureCount
				alarm.FailureCount = 0
				next, err := nextCron(alarm.CronExpr, alarm.Timezone, now)
				if err != nil {
					alarm.Status = StatusFailed
					alarm.LastError = safeError(errors.Join(wakeErr, err))
				} else {
					alarm.Status = StatusActive
					alarm.NextFireAt = next
				}
				alarm.OccurrenceID = ""
				alarm.OccurrenceAt = nil
			}
		}
		sortAlarms(decoded.Alarms)
		if err := service.saveDocument(ctx, claimed.ActorID, decoded); err != nil {
			return err
		}
		service.documents[claimed.ActorID] = decoded
		return wakeErr
	}
	return errors.New("scheduler: claimed alarm disappeared")
}

func (service *Service) saveDocument(
	ctx context.Context,
	actorID uuid.UUID,
	decoded document,
) error {
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return fmt.Errorf("scheduler: encode encrypted actor document: %w", err)
	}
	if err := service.store.SaveLivingState(
		context.WithoutCancel(ctx), stateKind, actorID.String(), encoded,
	); err != nil {
		return fmt.Errorf("scheduler: persist encrypted actor document: %w", err)
	}
	return nil
}

func validateAlarm(alarm Alarm) error {
	if alarm.ID == uuid.Nil || alarm.ActorID == uuid.Nil ||
		alarm.SessionID == uuid.Nil || strings.TrimSpace(alarm.Label) == "" ||
		strings.TrimSpace(alarm.WakeMessage) == "" ||
		alarm.NextFireAt.IsZero() || alarm.CreatedAt.IsZero() ||
		alarm.UpdatedAt.IsZero() || alarm.MaxFailures < 1 ||
		alarm.MaxFailures > maxFailureLimit ||
		len(alarm.DefinitionHash) != sha256.Size*2 ||
		len(alarm.Payload) > maxPayloadBytes || !json.Valid(alarm.Payload) {
		return errors.New("scheduler: invalid encrypted alarm")
	}
	switch alarm.Kind {
	case KindOnce:
		if alarm.FireAt == nil {
			return errors.New("scheduler: once alarm has no fire time")
		}
	case KindCron:
		if _, err := nextCron(alarm.CronExpr, alarm.Timezone, alarm.CreatedAt); err != nil {
			return err
		}
	default:
		return errors.New("scheduler: invalid alarm kind")
	}
	switch alarm.Status {
	case StatusActive, StatusClaimed, StatusFired, StatusCancelled, StatusFailed:
	default:
		return errors.New("scheduler: invalid alarm status")
	}
	return nil
}

func makeRoom(alarms []Alarm) []Alarm {
	if len(alarms) < maxAlarmsPerActor {
		return alarms
	}
	for index := len(alarms) - 1; index >= 0; index-- {
		switch alarms[index].Status {
		case StatusFired, StatusCancelled, StatusFailed:
			return append(alarms[:index], alarms[index+1:]...)
		}
	}
	return alarms
}

func sortAlarms(alarms []Alarm) {
	sort.SliceStable(alarms, func(left, right int) bool {
		leftActive := alarms[left].Status == StatusActive || alarms[left].Status == StatusClaimed
		rightActive := alarms[right].Status == StatusActive || alarms[right].Status == StatusClaimed
		if leftActive != rightActive {
			return leftActive
		}
		if leftActive && !alarms[left].NextFireAt.Equal(alarms[right].NextFireAt) {
			return alarms[left].NextFireAt.Before(alarms[right].NextFireAt)
		}
		if !alarms[left].UpdatedAt.Equal(alarms[right].UpdatedAt) {
			return alarms[left].UpdatedAt.After(alarms[right].UpdatedAt)
		}
		return alarms[left].ID.String() < alarms[right].ID.String()
	})
}

func cloneAlarm(alarm Alarm) Alarm {
	alarm.Payload = append(json.RawMessage(nil), alarm.Payload...)
	alarm.FireAt = cloneTime(alarm.FireAt)
	alarm.ClaimedAt = cloneTime(alarm.ClaimedAt)
	alarm.OccurrenceAt = cloneTime(alarm.OccurrenceAt)
	alarm.LastFiredAt = cloneTime(alarm.LastFiredAt)
	return alarm
}

func cloneDocument(decoded document) document {
	return document{
		Version: decoded.Version,
		Alarms:  cloneAlarms(decoded.Alarms),
	}
}

func cloneAlarms(alarms []Alarm) []Alarm {
	result := make([]Alarm, len(alarms))
	for index := range alarms {
		result[index] = cloneAlarm(alarms[index])
	}
	return result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func retryBackoff(failureCount int) time.Duration {
	delay := retryBaseBackoff
	for index := 0; index < failureCount; index++ {
		delay *= 2
		if delay >= retryMaximumBackoff {
			return retryMaximumBackoff
		}
	}
	return delay
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > maxSafeErrorBytes {
		message = message[:maxSafeErrorBytes]
	}
	return message
}

func summarizeFindings(findings []securitycron.Finding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.Category+":"+finding.Pattern)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func hashDefinition(
	request CreateRequest,
	timezone string,
) (string, error) {
	definition := struct {
		SessionID    uuid.UUID       `json:"session_id"`
		Label        string          `json:"label"`
		Kind         Kind            `json:"kind"`
		DelaySeconds int64           `json:"delay_seconds,omitempty"`
		FireAt       string          `json:"fire_at,omitempty"`
		CronExpr     string          `json:"cron_expr,omitempty"`
		Timezone     string          `json:"timezone"`
		WakeMessage  string          `json:"wake_message"`
		Payload      json.RawMessage `json:"payload"`
		MaxFailures  int             `json:"max_failures"`
	}{
		SessionID: request.SessionID, Label: request.Label, Kind: request.Kind,
		DelaySeconds: request.DelaySeconds, FireAt: strings.TrimSpace(request.FireAt),
		CronExpr: strings.TrimSpace(request.CronExpr),
		Timezone: timezone, WakeMessage: request.WakeMessage,
		Payload: request.Payload, MaxFailures: request.MaxFailures,
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("scheduler: encode alarm identity: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:]), nil
}
