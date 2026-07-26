package operatorapp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane/adapters"
	"github.com/paxlabs-inc/ion-agent/internal/presence/gateway"
	"github.com/paxlabs-inc/ion-agent/internal/session"
)

const (
	gatewaySecretKind          = "gateway_secret"
	gatewaySecretScope         = "production"
	channelMappingKind         = "channel_session"
	telegramCursorKind         = "channel_cursor"
	telegramUpdateKind         = "channel_update"
	telegramCursorBase         = "telegram"
	telegramMaxAttempts        = 3
	defaultTelegramTurnTimeout = 10 * time.Minute
	telegramRetryQueueSize     = 64
)

type channelSession struct {
	Version              int              `json:"version"`
	SessionKey           string           `json:"session_key"`
	Platform             gateway.Platform `json:"platform"`
	ActorID              uuid.UUID        `json:"actor_id"`
	SessionID            uuid.UUID        `json:"session_id"`
	LastCommandMessageID string           `json:"last_command_message_id,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

type channelCursor struct {
	Version      int   `json:"version"`
	NextUpdateID int64 `json:"next_update_id"`
}

type telegramUpdateState struct {
	Version     int               `json:"version"`
	UpdateID    int64             `json:"update_id"`
	Status      string            `json:"status"`
	Attempts    int               `json:"attempts"`
	FailureCode string            `json:"failure_code,omitempty"`
	TurnID      *uuid.UUID        `json:"turn_id,omitempty"`
	NextRetry   *time.Time        `json:"next_retry,omitempty"`
	Inbound     *gateway.Inbound  `json:"inbound,omitempty"`
	Outbound    *gateway.Outbound `json:"outbound,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type telegramDeadLetter struct {
	UpdateID    int64  `json:"update_id"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	FailureCode string `json:"failure_code"`
}

type telegramActiveUpdate struct {
	UpdateID int64  `json:"update_id"`
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
}

type channelProjection struct {
	Name            string                 `json:"name"`
	Priority        string                 `json:"priority"`
	Status          string                 `json:"status"`
	Configured      bool                   `json:"configured"`
	Transport       string                 `json:"transport"`
	Session         string                 `json:"session"`
	Setup           []string               `json:"setup,omitempty"`
	Health          any                    `json:"health,omitempty"`
	DeadLetters     []telegramDeadLetter   `json:"dead_letters,omitempty"`
	ActiveUpdates   []telegramActiveUpdate `json:"active_updates,omitempty"`
	RecoveryActions []string               `json:"recovery_actions,omitempty"`
}

// channelRuntime owns external transports while deliberately delegating every
// authorized message to the same durable dispatcher used by browser and TUI.
type channelRuntime struct {
	ctx         context.Context
	cancel      context.CancelFunc
	store       *session.Store
	dispatcher  *controlplane.Dispatcher
	clock       interface{ Now() time.Time }
	secret      []byte
	gateway     *gateway.Gateway
	telegram    *gateway.TelegramConnector
	cursorScope string
	typingEvery time.Duration
	retryQueue  chan int64
	turnTimeout time.Duration

	mu        sync.RWMutex
	status    string
	lastError string
	wg        sync.WaitGroup
}

func openChannelRuntime(
	parent context.Context,
	config RuntimeConfig,
	store *session.Store,
	dispatcher *controlplane.Dispatcher,
	living *livingContext,
) (*channelRuntime, error) {
	if parent == nil || store == nil || dispatcher == nil || living == nil {
		return nil, fmt.Errorf("operator channels: runtime dependencies are required")
	}
	turnTimeout := config.TelegramTurnTimeout
	if turnTimeout <= 0 {
		turnTimeout = defaultTelegramTurnTimeout
	}
	ctx, cancel := context.WithCancel(parent)
	runtime := &channelRuntime{
		ctx: ctx, cancel: cancel, store: store, dispatcher: dispatcher,
		clock: living.clock, status: "not_configured", typingEvery: 4 * time.Second,
		turnTimeout: turnTimeout,
	}
	secret, err := loadOrCreateGatewaySecret(ctx, store)
	if err != nil {
		cancel()
		return nil, err
	}
	runtime.secret = secret
	core := &channelCore{runtime: runtime}
	runtime.gateway, err = gateway.New(core, secret, living.soul)
	if err != nil {
		cancel()
		return nil, err
	}

	token := strings.TrimSpace(config.TelegramBotToken)
	if token == "" {
		return runtime, nil
	}
	allowed := parseAllowedUsers(config.TelegramAllowedUsers)
	runtime.telegram, err = gateway.NewTelegramConnector(gateway.TelegramConfig{
		BotToken: token, AllowedUsers: allowed,
		HTTPClient:  config.TelegramHTTPClient,
		APIBaseURL:  config.TelegramAPIBaseURL,
		PollTimeout: config.TelegramPollTimeout,
	})
	if err != nil {
		runtime.status = "setup_required"
		runtime.lastError = err.Error()
		return runtime, nil
	}
	if err := runtime.gateway.Register(runtime.telegram); err != nil {
		cancel()
		return nil, err
	}
	runtime.cursorScope = telegramCursorScope(secret, token)
	runtime.retryQueue = make(chan int64, telegramRetryQueueSize)
	retries, err := runtime.reconcileTelegramUpdates(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	runtime.status = "starting"
	runtime.wg.Add(2)
	go runtime.runTelegram()
	go runtime.runTelegramRetries(retries)
	return runtime, nil
}

func (runtime *channelRuntime) Close() {
	if runtime == nil {
		return
	}
	runtime.cancel()
	runtime.wg.Wait()
	for index := range runtime.secret {
		runtime.secret[index] = 0
	}
}

func (runtime *channelRuntime) List() []channelProjection {
	runtime.mu.RLock()
	status := runtime.status
	lastError := runtime.lastError
	runtime.mu.RUnlock()
	setup := []string{
		"Create a bot with Telegram @BotFather.",
		"Set TELEGRAM_BOT_TOKEN and TELEGRAM_ALLOWED_USERS in the protected environment file.",
		"Restart Ion, then message the bot. Normal messages use the same chat engine as the dashboard.",
	}
	projection := channelProjection{
		Name: "Telegram", Priority: "first_class", Status: status,
		Configured: runtime.telegram != nil, Transport: "HTTPS long polling",
		Session: "encrypted and isolated per Telegram user, chat, and topic",
	}
	if runtime.telegram == nil {
		projection.Setup = setup
	}
	if runtime.telegram != nil {
		health := runtime.telegram.Health()
		if lastError != "" {
			health.LastError = lastError
			if health.Status == "ready" {
				health.Status = "degraded"
			}
		}
		projection.Health = health
		projection.DeadLetters, projection.ActiveUpdates =
			runtime.telegramUpdateProjections()
		if len(projection.DeadLetters) > 0 {
			if projection.Status == "ready" {
				projection.Status = "degraded"
			}
			if health.Status == "ready" {
				health.Status = "degraded"
				projection.Health = health
			}
			projection.RecoveryActions = []string{
				"channel.retry with update_id retries one quarantined update",
				"channel.skip with update_id permanently acknowledges it",
			}
		} else if len(projection.ActiveUpdates) > 0 &&
			projection.Status == "ready" && health.Status == "ready" {
			projection.Status = "working"
			health.Status = "working"
			projection.Health = health
		}
	}
	return []channelProjection{
		{
			Name: "Main dashboard", Priority: "first_class", Status: "ready",
			Configured: true, Transport: "authenticated browser and local TUI",
			Session: "encrypted production conversation",
		},
		projection,
	}
}

func (runtime *channelRuntime) Health() map[string]any {
	channels := runtime.List()
	healthy := 0
	configured := 0
	for _, channel := range channels {
		if channel.Configured {
			configured++
		}
		if channel.Status == "ready" || channel.Status == "working" {
			healthy++
		}
	}
	return map[string]any{
		"configured": configured, "healthy": healthy,
		"primary_external_channel": "telegram", "channels": channels,
	}
}

func (runtime *channelRuntime) runTelegram() {
	defer runtime.wg.Done()
	cursor, initialized, err := runtime.loadTelegramCursor(runtime.ctx)
	if err != nil {
		runtime.setError(err)
		return
	}
	backoff := time.Second
	for !initialized && runtime.ctx.Err() == nil {
		// Establish a clean first-start boundary: old queued updates are
		// confirmed but never replayed into a newly enabled agent.
		updates, pollErr := runtime.telegram.Updates(runtime.ctx, -1)
		if pollErr != nil {
			if runtime.ctx.Err() == nil {
				runtime.setError(pollErr)
			}
			if errors.Is(pollErr, gateway.ErrTelegramConflict) {
				return
			}
			timer := time.NewTimer(backoff)
			select {
			case <-runtime.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		if len(updates) > 0 {
			cursor = updates[len(updates)-1].ID + 1
		}
		if err := runtime.saveTelegramCursor(runtime.ctx, cursor); err != nil {
			runtime.setError(err)
			return
		}
		initialized = true
	}
	runtime.setStatus("ready")
	backoff = time.Second
	for runtime.ctx.Err() == nil {
		updates, err := runtime.telegram.Updates(runtime.ctx, cursor)
		if err != nil {
			if runtime.ctx.Err() != nil {
				return
			}
			runtime.setError(err)
			if errors.Is(err, gateway.ErrTelegramConflict) {
				return
			}
			timer := time.NewTimer(backoff)
			select {
			case <-runtime.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		runtime.setStatus("ready")
		for _, update := range updates {
			if update.ID < cursor {
				continue
			}
			if update.Inbound != nil && update.Authorized {
				advance, processErr := runtime.processTelegramUpdate(
					runtime.ctx, update.ID, *update.Inbound,
				)
				if processErr != nil && runtime.ctx.Err() == nil {
					runtime.setError(processErr)
				}
				if !advance {
					timer := time.NewTimer(250 * time.Millisecond)
					select {
					case <-runtime.ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
					break
				}
			}
			cursor = update.ID + 1
			if err := runtime.saveTelegramCursor(runtime.ctx, cursor); err != nil {
				runtime.setError(err)
				return
			}
		}
	}
}

func (runtime *channelRuntime) reconcileTelegramUpdates(
	ctx context.Context,
) ([]int64, error) {
	states, err := runtime.store.ListLivingStates(ctx, telegramUpdateKind)
	if err != nil {
		return nil, err
	}
	prefix := runtime.cursorScope + ":"
	var retries []int64
	for _, found := range states {
		if !strings.HasPrefix(found.Scope, prefix) {
			continue
		}
		var state telegramUpdateState
		if err := json.Unmarshal(found.State, &state); err != nil ||
			state.Version != 1 || state.UpdateID < 0 {
			return nil, fmt.Errorf(
				"operator channels: invalid persisted Telegram update state",
			)
		}
		switch state.Status {
		case "pending", "retry_wait":
			state.Status = "retry_wait"
			state.NextRetry = nil
			state.UpdatedAt = runtime.clock.Now().UTC()
			if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
				return nil, err
			}
			retries = append(retries, state.UpdateID)
		case "processing":
			state.Status = "quarantined"
			state.FailureCode = "processing_interrupted"
			state.NextRetry = nil
			state.UpdatedAt = runtime.clock.Now().UTC()
			if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
				return nil, err
			}
		case "sending":
			state.Status = "quarantined"
			state.FailureCode = "delivery_outcome_unknown"
			state.NextRetry = nil
			state.UpdatedAt = runtime.clock.Now().UTC()
			if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(retries, func(left, right int) bool {
		return retries[left] < retries[right]
	})
	return retries, nil
}

func (runtime *channelRuntime) runTelegramRetries(initial []int64) {
	defer runtime.wg.Done()
	for _, updateID := range initial {
		runtime.processQueuedTelegramRetry(updateID)
	}
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case updateID := <-runtime.retryQueue:
			runtime.processQueuedTelegramRetry(updateID)
		}
	}
}

func (runtime *channelRuntime) processQueuedTelegramRetry(updateID int64) {
	for runtime.ctx.Err() == nil {
		state, found, err := runtime.loadTelegramUpdate(runtime.ctx, updateID)
		if err != nil || !found || state.Inbound == nil {
			if err != nil {
				runtime.setError(err)
			}
			return
		}
		if state.Status != "retry_wait" {
			return
		}
		if state.NextRetry != nil && state.NextRetry.After(runtime.clock.Now().UTC()) {
			timer := time.NewTimer(
				state.NextRetry.Sub(runtime.clock.Now().UTC()),
			)
			select {
			case <-runtime.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		_, processErr := runtime.processTelegramUpdate(
			runtime.ctx, updateID, *state.Inbound,
		)
		if processErr != nil && runtime.ctx.Err() == nil {
			runtime.setError(processErr)
		}
		updated, found, loadErr := runtime.loadTelegramUpdate(runtime.ctx, updateID)
		if loadErr != nil || !found {
			if loadErr != nil {
				runtime.setError(loadErr)
			}
			return
		}
		if updated.Status != "retry_wait" {
			if updated.Status == "delivered" {
				runtime.setStatus("ready")
			}
			return
		}
	}
}

func (runtime *channelRuntime) processTelegramUpdate(
	ctx context.Context,
	updateID int64,
	inbound gateway.Inbound,
) (bool, error) {
	state, found, err := runtime.loadTelegramUpdate(ctx, updateID)
	if err != nil {
		return false, err
	}
	now := runtime.clock.Now().UTC()
	if !found {
		state = telegramUpdateState{
			Version: 1, UpdateID: updateID, Status: "pending",
			Inbound: &inbound, UpdatedAt: now,
		}
	}
	switch state.Status {
	case "delivered", "skipped", "quarantined":
		return true, nil
	case "sending":
		state.Status = "quarantined"
		state.FailureCode = "delivery_outcome_unknown"
		state.UpdatedAt = now
		return true, runtime.saveTelegramUpdate(ctx, state)
	case "retry_wait":
		if state.NextRetry != nil && state.NextRetry.After(now) {
			return false, nil
		}
	}
	if state.Inbound == nil {
		state.Inbound = &inbound
	}
	state.Attempts++
	state.Status = "processing"
	state.NextRetry = nil
	state.FailureCode = ""
	state.UpdatedAt = now
	if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
		return false, err
	}

	var outbound gateway.Outbound
	if state.TurnID != nil && *state.TurnID != uuid.Nil {
		outbound, err = runtime.retryTelegramTurn(ctx, state, *state.Inbound)
	} else {
		outbound, err = runtime.gateway.Prepare(ctx, *state.Inbound)
	}
	if err != nil {
		var turnErr channelTurnError
		if errors.As(err, &turnErr) && turnErr.turnID != uuid.Nil {
			turnID := turnErr.turnID
			state.TurnID = &turnID
		}
		code := channelFailureCode(err)
		state.FailureCode = code
		state.UpdatedAt = runtime.clock.Now().UTC()
		if state.Attempts >= telegramMaxAttempts || !retryableChannelFailure(code) {
			state.Status = "quarantined"
			if saveErr := runtime.saveTelegramUpdate(
				context.WithoutCancel(ctx), state,
			); saveErr != nil {
				return false, saveErr
			}
			return true, fmt.Errorf("operator channels: quarantined update %d: %s",
				updateID, code)
		}
		retryAt := state.UpdatedAt.Add(time.Duration(state.Attempts) * time.Second)
		state.Status = "retry_wait"
		state.NextRetry = &retryAt
		if saveErr := runtime.saveTelegramUpdate(
			context.WithoutCancel(ctx), state,
		); saveErr != nil {
			return false, saveErr
		}
		return false, fmt.Errorf("operator channels: update %d retry scheduled: %s",
			updateID, code)
	}
	state.Outbound = &outbound
	state.Status = "sending"
	state.UpdatedAt = runtime.clock.Now().UTC()
	if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
		return false, err
	}
	if err := runtime.gateway.Deliver(ctx, outbound); err != nil {
		state.Status = "quarantined"
		state.FailureCode = "delivery_outcome_unknown"
		state.UpdatedAt = runtime.clock.Now().UTC()
		if saveErr := runtime.saveTelegramUpdate(
			context.WithoutCancel(ctx), state,
		); saveErr != nil {
			return false, saveErr
		}
		return true, fmt.Errorf(
			"operator channels: delivery outcome unknown for update %d", updateID,
		)
	}
	state.Status = "delivered"
	state.FailureCode = ""
	state.UpdatedAt = runtime.clock.Now().UTC()
	if err := runtime.saveTelegramUpdate(
		context.WithoutCancel(ctx), state,
	); err != nil {
		return false, err
	}
	return true, nil
}

func (runtime *channelRuntime) loadTelegramUpdate(
	ctx context.Context,
	updateID int64,
) (telegramUpdateState, bool, error) {
	raw, err := runtime.store.LoadLivingState(
		ctx, telegramUpdateKind, runtime.telegramUpdateScope(updateID),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return telegramUpdateState{}, false, nil
	}
	if err != nil {
		return telegramUpdateState{}, false, err
	}
	var state telegramUpdateState
	if json.Unmarshal(raw, &state) != nil || state.Version != 1 ||
		state.UpdateID != updateID || state.Attempts < 0 {
		return telegramUpdateState{}, false,
			fmt.Errorf("operator channels: invalid Telegram update state")
	}
	return state, true, nil
}

func (runtime *channelRuntime) saveTelegramUpdate(
	ctx context.Context,
	state telegramUpdateState,
) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return runtime.store.SaveLivingState(
		ctx, telegramUpdateKind, runtime.telegramUpdateScope(state.UpdateID), raw,
	)
}

func (runtime *channelRuntime) telegramUpdateScope(updateID int64) string {
	return fmt.Sprintf("%s:%d", runtime.cursorScope, updateID)
}

func (runtime *channelRuntime) telegramDeadLetters() []telegramDeadLetter {
	dead, _ := runtime.telegramUpdateProjections()
	return dead
}

func (runtime *channelRuntime) telegramUpdateProjections() (
	[]telegramDeadLetter,
	[]telegramActiveUpdate,
) {
	if runtime == nil || runtime.store == nil || runtime.cursorScope == "" {
		return nil, nil
	}
	states, err := runtime.store.ListLivingStates(
		context.Background(), telegramUpdateKind,
	)
	if err != nil {
		return nil, nil
	}
	prefix := runtime.cursorScope + ":"
	var dead []telegramDeadLetter
	var active []telegramActiveUpdate
	for _, found := range states {
		if !strings.HasPrefix(found.Scope, prefix) {
			continue
		}
		var state telegramUpdateState
		if json.Unmarshal(found.State, &state) != nil {
			continue
		}
		switch state.Status {
		case "quarantined":
			dead = append(dead, telegramDeadLetter{
				UpdateID: state.UpdateID, Status: state.Status,
				Attempts: state.Attempts, FailureCode: state.FailureCode,
			})
		case "pending", "processing", "retry_wait", "sending":
			active = append(active, telegramActiveUpdate{
				UpdateID: state.UpdateID, Status: state.Status,
				Attempts: state.Attempts,
			})
		}
	}
	sort.Slice(dead, func(left, right int) bool {
		return dead[left].UpdateID < dead[right].UpdateID
	})
	sort.Slice(active, func(left, right int) bool {
		return active[left].UpdateID < active[right].UpdateID
	})
	return dead, active
}

func (runtime *channelRuntime) RetryTelegramUpdate(
	ctx context.Context,
	updateID int64,
) (telegramUpdateState, error) {
	state, found, err := runtime.loadTelegramUpdate(ctx, updateID)
	if err != nil {
		return telegramUpdateState{}, err
	}
	if !found || state.Status != "quarantined" || state.Inbound == nil {
		return telegramUpdateState{}, fmt.Errorf(
			"operator channels: quarantined update %d was not found", updateID,
		)
	}
	if state.Outbound != nil {
		state.Status = "sending"
		state.FailureCode = ""
		state.UpdatedAt = runtime.clock.Now().UTC()
		if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
			return telegramUpdateState{}, err
		}
		if err := runtime.gateway.Deliver(ctx, *state.Outbound); err != nil {
			state.Status = "quarantined"
			state.FailureCode = "delivery_outcome_unknown"
			state.UpdatedAt = runtime.clock.Now().UTC()
			_ = runtime.saveTelegramUpdate(context.WithoutCancel(ctx), state)
			return state, err
		}
		state.Status = "delivered"
		state.UpdatedAt = runtime.clock.Now().UTC()
		if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
			return telegramUpdateState{}, err
		}
		return state, nil
	}
	state.Status = "retry_wait"
	state.NextRetry = nil
	state.FailureCode = ""
	state.UpdatedAt = runtime.clock.Now().UTC()
	if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
		return telegramUpdateState{}, err
	}
	select {
	case runtime.retryQueue <- updateID:
		return state, nil
	case <-ctx.Done():
		return state, ctx.Err()
	case <-runtime.ctx.Done():
		return state, runtime.ctx.Err()
	}
}

func (runtime *channelRuntime) SkipTelegramUpdate(
	ctx context.Context,
	updateID int64,
) (telegramUpdateState, error) {
	state, found, err := runtime.loadTelegramUpdate(ctx, updateID)
	if err != nil {
		return telegramUpdateState{}, err
	}
	if !found || state.Status != "quarantined" {
		return telegramUpdateState{}, fmt.Errorf(
			"operator channels: quarantined update %d was not found", updateID,
		)
	}
	state.Status = "skipped"
	state.UpdatedAt = runtime.clock.Now().UTC()
	if err := runtime.saveTelegramUpdate(ctx, state); err != nil {
		return telegramUpdateState{}, err
	}
	return state, nil
}

type channelTurnError struct {
	code   string
	turnID uuid.UUID
}

func (err channelTurnError) Error() string {
	return "operator channels: turn ended with " + err.code
}

func channelFailureCode(err error) string {
	var turnErr channelTurnError
	if errors.As(err, &turnErr) {
		return turnErr.code
	}
	lower := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		strings.Contains(lower, "timeout"),
		strings.Contains(lower, "deadline"):
		return "timeout"
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"):
		return "rate_limit"
	case strings.Contains(lower, "temporary"),
		strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "connection refused"):
		return "transient"
	default:
		return "permanent"
	}
}

func retryableChannelFailure(code string) bool {
	switch code {
	case "timeout", "rate_limit", "transient", "turn_retry_pending":
		return true
	default:
		return false
	}
}

func (runtime *channelRuntime) loadTelegramCursor(
	ctx context.Context,
) (int64, bool, error) {
	raw, err := runtime.store.LoadLivingState(
		ctx, telegramCursorKind, runtime.cursorScope,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var cursor channelCursor
	if err := json.Unmarshal(raw, &cursor); err != nil ||
		cursor.Version != 1 || cursor.NextUpdateID < 0 {
		return 0, false, fmt.Errorf("operator channels: invalid Telegram cursor")
	}
	return cursor.NextUpdateID, true, nil
}

func (runtime *channelRuntime) saveTelegramCursor(
	ctx context.Context,
	next int64,
) error {
	raw, err := json.Marshal(channelCursor{Version: 1, NextUpdateID: next})
	if err != nil {
		return err
	}
	return runtime.store.SaveLivingState(
		ctx, telegramCursorKind, runtime.cursorScope, raw,
	)
}

// telegramCursorScope isolates offsets by bot credential without persisting
// either the credential or a directly comparable digest of it. Rotating a bot
// token therefore establishes a fresh polling boundary for the new bot.
func telegramCursorScope(secret []byte, token string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("telegram-cursor"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(token))
	return telegramCursorBase + ":" +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

func (runtime *channelRuntime) setStatus(status string) {
	runtime.mu.Lock()
	runtime.status = status
	if status == "ready" {
		runtime.lastError = ""
	}
	runtime.mu.Unlock()
}

func (runtime *channelRuntime) setError(err error) {
	runtime.mu.Lock()
	runtime.status = "degraded"
	runtime.lastError = safeChannelError(err)
	runtime.mu.Unlock()
}

type channelCore struct{ runtime *channelRuntime }

func (core *channelCore) Respond(
	ctx context.Context,
	turn gateway.Turn,
) (string, error) {
	command := telegramCommand(turn.Inbound.Text)
	switch command {
	case "start":
		return "Ion is connected. Send a message here and it will use the same agent, memory, tools, safety rules, and durable conversation path as the main dashboard.", nil
	case "help":
		return "Send any message to chat. Use /new to start a fresh encrypted Telegram conversation.", nil
	}
	mapping, err := core.runtime.resolveSession(ctx, turn)
	if err != nil {
		return "", err
	}
	if command == "new" {
		if mapping.LastCommandMessageID == turn.Inbound.MessageID {
			return "A fresh conversation is ready.", nil
		}
		created, err := core.runtime.store.CreateSession(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("operator channels: create fresh session: %w", err)
		}
		mapping.SessionID = created.ID
		mapping.LastCommandMessageID = turn.Inbound.MessageID
		mapping.UpdatedAt = core.runtime.clock.Now().UTC()
		if err := core.runtime.saveMapping(ctx, mapping); err != nil {
			return "", err
		}
		return "A fresh conversation is ready.", nil
	}
	return core.runtime.submitAndWait(ctx, mapping, turn.Inbound)
}

func (runtime *channelRuntime) resolveSession(
	ctx context.Context,
	turn gateway.Turn,
) (channelSession, error) {
	raw, err := runtime.store.LoadLivingState(
		ctx, channelMappingKind, turn.SessionKey,
	)
	if err == nil {
		var mapping channelSession
		if decodeErr := json.Unmarshal(raw, &mapping); decodeErr != nil ||
			mapping.Version != 1 || mapping.SessionKey != turn.SessionKey ||
			mapping.Platform != turn.Inbound.Platform ||
			mapping.ActorID == uuid.Nil || mapping.SessionID == uuid.Nil {
			return channelSession{}, fmt.Errorf("operator channels: invalid encrypted session mapping")
		}
		if _, err := runtime.store.GetSession(ctx, mapping.SessionID); err != nil {
			return channelSession{}, fmt.Errorf("operator channels: mapped session is unavailable: %w", err)
		}
		return mapping, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return channelSession{}, err
	}
	created, err := runtime.store.CreateSession(ctx, nil)
	if err != nil {
		return channelSession{}, err
	}
	now := runtime.clock.Now().UTC()
	mapping := channelSession{
		Version: 1, SessionKey: turn.SessionKey, Platform: turn.Inbound.Platform,
		ActorID: runtime.actorID(turn.Inbound), SessionID: created.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := runtime.saveMapping(ctx, mapping); err != nil {
		return channelSession{}, err
	}
	return mapping, nil
}

func (runtime *channelRuntime) saveMapping(
	ctx context.Context,
	mapping channelSession,
) error {
	raw, err := json.Marshal(mapping)
	if err != nil {
		return err
	}
	return runtime.store.SaveLivingState(
		ctx, channelMappingKind, mapping.SessionKey, raw,
	)
}

func (runtime *channelRuntime) submitAndWait(
	ctx context.Context,
	mapping channelSession,
	inbound gateway.Inbound,
) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"content": inbound.Text, "surface": adapters.TurnSurfaceGeneral,
	})
	if err != nil {
		return "", err
	}
	response := runtime.dispatcher.Dispatch(ctx, mapping.ActorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationTurnSubmit,
		Scope: controlplane.Scope{
			ActorID: mapping.ActorID, SessionID: &mapping.SessionID,
			Profile: "operator", Channel: string(inbound.Platform),
		},
		IdempotencyKey: "channel:" + mapping.SessionKey + ":" + inbound.MessageID,
		Payload:        payload,
	})
	if response.Error != nil {
		return "", fmt.Errorf("operator channels: turn rejected: %s", response.Error.Message)
	}
	var submitted struct {
		TurnID uuid.UUID `json:"turn_id"`
	}
	if err := json.Unmarshal(response.Result, &submitted); err != nil ||
		submitted.TurnID == uuid.Nil {
		return "", fmt.Errorf("operator channels: invalid turn submission response")
	}
	return runtime.waitForTelegramTurn(ctx, mapping, inbound, submitted.TurnID)
}

func (runtime *channelRuntime) retryTelegramTurn(
	ctx context.Context,
	state telegramUpdateState,
	inbound gateway.Inbound,
) (gateway.Outbound, error) {
	if state.TurnID == nil || *state.TurnID == uuid.Nil {
		return gateway.Outbound{}, fmt.Errorf(
			"operator channels: retry is missing its durable turn reference",
		)
	}
	sessionKey := runtime.gateway.SessionKey(inbound)
	mapping, err := runtime.resolveSession(ctx, gateway.Turn{
		SessionKey: sessionKey, Inbound: inbound,
	})
	if err != nil {
		return gateway.Outbound{}, err
	}
	payload, err := json.Marshal(map[string]uuid.UUID{"turn_id": *state.TurnID})
	if err != nil {
		return gateway.Outbound{}, err
	}
	retryCtx, cancelRetry := context.WithTimeout(ctx, 2*time.Second)
	defer cancelRetry()
	var response controlplane.Response
	for retryIndex := 0; ; retryIndex++ {
		response = runtime.dispatcher.Dispatch(retryCtx, mapping.ActorID, controlplane.Request{
			ProtocolVersion: controlplane.ProtocolVersion,
			RequestID:       uuid.New(), Kind: controlplane.KindCommand,
			Operation: controlplane.OperationTurnRetry,
			Scope: controlplane.Scope{
				ActorID: mapping.ActorID, SessionID: &mapping.SessionID,
				Profile: "operator", Channel: string(inbound.Platform),
			},
			IdempotencyKey: fmt.Sprintf(
				"channel-retry:%s:%s:%d:%d",
				sessionKey, inbound.MessageID, state.Attempts, retryIndex,
			),
			Payload: payload,
		})
		if response.Error == nil || response.Error.Code != controlplane.ErrorConflict {
			break
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return gateway.Outbound{}, channelTurnError{
				code: "turn_retry_pending", turnID: *state.TurnID,
			}
		case <-timer.C:
		}
	}
	if response.Error != nil {
		return gateway.Outbound{}, fmt.Errorf(
			"operator channels: turn retry rejected: %s", response.Error.Message,
		)
	}
	var submitted struct {
		TurnID uuid.UUID `json:"turn_id"`
	}
	if err := json.Unmarshal(response.Result, &submitted); err != nil ||
		submitted.TurnID == uuid.Nil {
		return gateway.Outbound{}, fmt.Errorf(
			"operator channels: invalid turn retry response",
		)
	}
	text, err := runtime.waitForTelegramTurn(
		ctx, mapping, inbound, submitted.TurnID,
	)
	if err != nil {
		return gateway.Outbound{}, err
	}
	return gateway.Outbound{
		SessionKey: sessionKey, Platform: inbound.Platform,
		TargetID: inbound.ConversationID, ThreadID: inbound.ThreadID, Text: text,
	}, nil
}

func (runtime *channelRuntime) waitForTelegramTurn(
	ctx context.Context,
	mapping channelSession,
	inbound gateway.Inbound,
	turnID uuid.UUID,
) (string, error) {
	turnCtx, cancel := context.WithTimeout(ctx, runtime.turnTimeout)
	defer cancel()
	stopTyping := runtime.startTelegramTyping(turnCtx, inbound)
	defer stopTyping()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	contextFailure := func() error {
		deadline, hasDeadline := turnCtx.Deadline()
		deadlineExpired := hasDeadline && !time.Now().Before(deadline)
		if turnCtx.Err() == nil && !deadlineExpired {
			return nil
		}
		if errors.Is(turnCtx.Err(), context.DeadlineExceeded) || deadlineExpired {
			runtime.cancelTelegramTurn(ctx, mapping, turnID)
			return channelTurnError{
				code: "channel_turn_timeout", turnID: turnID,
			}
		}
		return turnCtx.Err()
	}
	for {
		if err := contextFailure(); err != nil {
			return "", err
		}
		state, err := runtime.store.LoadTurnState(turnCtx, turnID)
		if err != nil {
			return "", err
		}
		if err := contextFailure(); err != nil {
			return "", err
		}
		switch state.Status {
		case session.TurnCompleted:
			messages, err := runtime.store.ListMessages(turnCtx, mapping.SessionID)
			if err != nil {
				return "", err
			}
			for index := len(messages) - 1; index >= 0; index-- {
				if messages[index].Role == session.RoleAssistant &&
					strings.TrimSpace(string(messages[index].Content)) != "" {
					return string(messages[index].Content), nil
				}
			}
			return "", fmt.Errorf("operator channels: completed turn has no assistant response")
		case session.TurnFailed, session.TurnCancelled, session.TurnInterrupted,
			session.TurnIncomplete:
			code := strings.TrimSpace(state.FailureCode)
			if code == "" {
				code = string(state.Status)
			}
			return "", channelTurnError{code: code, turnID: turnID}
		}
		select {
		case <-turnCtx.Done():
			return "", contextFailure()
		case <-ticker.C:
		}
	}
}

func (runtime *channelRuntime) cancelTelegramTurn(
	ctx context.Context,
	mapping channelSession,
	turnID uuid.UUID,
) {
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	payload, err := json.Marshal(map[string]uuid.UUID{"turn_id": turnID})
	if err != nil {
		return
	}
	response := runtime.dispatcher.Dispatch(cancelCtx, mapping.ActorID, controlplane.Request{
		ProtocolVersion: controlplane.ProtocolVersion,
		RequestID:       uuid.New(), Kind: controlplane.KindCommand,
		Operation: controlplane.OperationTurnCancel,
		Scope: controlplane.Scope{
			ActorID: mapping.ActorID, SessionID: &mapping.SessionID,
			Profile: "operator", Channel: string(gateway.Telegram),
		},
		IdempotencyKey: "channel-timeout-cancel:" + turnID.String(),
		Payload:        payload,
	})
	if response.Error != nil {
		return
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := runtime.store.LoadTurnState(cancelCtx, turnID)
		if err != nil || state.Status != session.TurnRunning {
			return
		}
		select {
		case <-cancelCtx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (runtime *channelRuntime) startTelegramTyping(
	parent context.Context,
	inbound gateway.Inbound,
) func() {
	if inbound.Platform != gateway.Telegram || runtime.telegram == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	stopRequested := make(chan struct{})
	go func() {
		defer close(done)
		send := func() {
			// Typing is advisory. A transient indicator failure must never fail
			// or duplicate the durable agent turn.
			_ = runtime.telegram.SendTyping(
				ctx, inbound.ConversationID, inbound.ThreadID,
			)
		}
		send()
		interval := runtime.typingEvery
		if interval <= 0 {
			interval = 4 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopRequested:
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(stopRequested)
			<-done
			cancel()
		})
	}
}

func (runtime *channelRuntime) actorID(inbound gateway.Inbound) uuid.UUID {
	mac := hmac.New(sha256.New, runtime.secret)
	for _, value := range []string{
		"actor", string(inbound.Platform), inbound.ScopeID, inbound.SenderID,
	} {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(value))
	}
	raw := mac.Sum(nil)[:16]
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	id, _ := uuid.FromBytes(raw)
	return id
}

func loadOrCreateGatewaySecret(
	ctx context.Context,
	store *session.Store,
) ([]byte, error) {
	raw, err := store.LoadLivingState(ctx, gatewaySecretKind, gatewaySecretScope)
	if err == nil {
		var document struct {
			Version int    `json:"version"`
			Key     string `json:"key"`
		}
		if json.Unmarshal(raw, &document) != nil || document.Version != 1 {
			return nil, fmt.Errorf("operator channels: invalid gateway secret")
		}
		key, err := base64.RawStdEncoding.DecodeString(document.Key)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("operator channels: invalid gateway secret")
		}
		return key, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	document, err := json.Marshal(map[string]any{
		"version": 1, "key": base64.RawStdEncoding.EncodeToString(key),
	})
	if err != nil {
		return nil, err
	}
	if err := store.SaveLivingState(
		ctx, gatewaySecretKind, gatewaySecretScope, document,
	); err != nil {
		return nil, err
	}
	return key, nil
}

func parseAllowedUsers(raw string) []string {
	return strings.FieldsFunc(raw, func(value rune) bool {
		return value == ',' || value == ';' || value == ' ' ||
			value == '\t' || value == '\r' || value == '\n'
	})
}

func telegramCommand(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return ""
	}
	command := strings.TrimPrefix(fields[0], "/")
	if at := strings.IndexByte(command, '@'); at >= 0 {
		command = command[:at]
	}
	switch strings.ToLower(command) {
	case "start", "help", "new":
		return strings.ToLower(command)
	default:
		return ""
	}
}

func safeChannelError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.Join(strings.Fields(err.Error()), " ")
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}
