package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var ErrTelegramConflict = errors.New("telegram: another update consumer is active")

const (
	defaultTelegramAPIBase = "https://api.telegram.org"
	telegramMessageRunes   = 4096
	maxTelegramResponse    = 2 << 20
)

// TelegramConfig is the complete, secret-bearing Telegram transport
// configuration. Projection methods intentionally never return this value.
type TelegramConfig struct {
	BotToken     string
	AllowedUsers []string
	HTTPClient   *http.Client
	APIBaseURL   string
	PollTimeout  time.Duration
}

// TelegramHealth is a redaction-safe live transport projection.
type TelegramHealth struct {
	Status           string     `json:"status"`
	Configured       bool       `json:"configured"`
	AllowedUserCount int        `json:"allowed_user_count"`
	LastReceivedAt   *time.Time `json:"last_received_at,omitempty"`
	LastDeliveredAt  *time.Time `json:"last_delivered_at,omitempty"`
	LastErrorAt      *time.Time `json:"last_error_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

// TelegramUpdate is one normalized Bot API update. Unsupported update shapes
// are represented with Inbound == nil so their cursor can still be confirmed.
type TelegramUpdate struct {
	ID         int64
	Inbound    *Inbound
	Authorized bool
}

// TelegramConnector owns HTTPS long polling and text delivery. It has no agent
// logic: every authorized Inbound still crosses Gateway.Handle and the shared
// Core.
type TelegramConnector struct {
	token       string
	allowed     map[string]struct{}
	client      *http.Client
	apiBase     string
	pollTimeout time.Duration

	mu     sync.RWMutex
	health TelegramHealth
}

func NewTelegramConnector(config TelegramConfig) (*TelegramConnector, error) {
	token := strings.TrimSpace(config.BotToken)
	if token == "" || strings.ContainsAny(token, " \t\r\n/") {
		return nil, fmt.Errorf("telegram: a valid bot token is required")
	}
	allowed := make(map[string]struct{}, len(config.AllowedUsers))
	for _, raw := range config.AllowedUsers {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("telegram: allowed users must be positive numeric IDs")
		}
		allowed[strconv.FormatInt(parsed, 10)] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("telegram: at least one allowed user is required")
	}
	apiBase := strings.TrimRight(strings.TrimSpace(config.APIBaseURL), "/")
	if apiBase == "" {
		apiBase = defaultTelegramAPIBase
	}
	parsedBase, err := url.Parse(apiBase)
	if err != nil || parsedBase.Scheme != "https" || parsedBase.Host == "" {
		// A loopback HTTP endpoint is permitted only as an injected test boundary.
		if err != nil || parsedBase.Scheme != "http" ||
			parsedBase.Hostname() != "127.0.0.1" {
			return nil, fmt.Errorf("telegram: HTTPS API base is required")
		}
	}
	pollTimeout := config.PollTimeout
	if pollTimeout <= 0 {
		pollTimeout = 30 * time.Second
	}
	if pollTimeout > 50*time.Second {
		return nil, fmt.Errorf("telegram: poll timeout must not exceed 50 seconds")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: pollTimeout + 10*time.Second}
	}
	return &TelegramConnector{
		token: token, allowed: allowed, client: client, apiBase: apiBase,
		pollTimeout: pollTimeout,
		health: TelegramHealth{
			Status: "starting", Configured: true, AllowedUserCount: len(allowed),
		},
	}, nil
}

func (*TelegramConnector) Platform() Platform { return Telegram }

// Updates performs one bounded long-poll request. Offset must be one greater
// than the last durably handled update; -1 is useful for first-start bootstrap.
func (connector *TelegramConnector) Updates(
	ctx context.Context,
	offset int64,
) ([]TelegramUpdate, error) {
	payload := map[string]any{
		"offset":          offset,
		"limit":           100,
		"timeout":         int(connector.pollTimeout / time.Second),
		"allowed_updates": []string{"message"},
	}
	var response struct {
		OK          bool             `json:"ok"`
		Description string           `json:"description"`
		Result      []telegramUpdate `json:"result"`
	}
	if err := connector.call(ctx, "getUpdates", payload, &response); err != nil {
		connector.recordError(err)
		return nil, err
	}
	if !response.OK {
		err := fmt.Errorf("telegram: getUpdates rejected: %s", cleanTelegramDescription(response.Description))
		connector.recordError(err)
		return nil, err
	}
	result := make([]TelegramUpdate, 0, len(response.Result))
	for _, update := range response.Result {
		normalized := TelegramUpdate{ID: update.UpdateID}
		if update.Message == nil || update.Message.From.IsBot ||
			strings.TrimSpace(update.Message.Text) == "" {
			result = append(result, normalized)
			continue
		}
		inbound := Inbound{
			Platform:       Telegram,
			ConversationID: strconv.FormatInt(update.Message.Chat.ID, 10),
			SenderID:       strconv.FormatInt(update.Message.From.ID, 10),
			MessageID:      strconv.FormatInt(update.Message.ID, 10),
			Text:           update.Message.Text,
		}
		if update.Message.ThreadID != 0 {
			inbound.ThreadID = strconv.FormatInt(update.Message.ThreadID, 10)
		}
		if err := validateInbound(inbound); err != nil {
			result = append(result, normalized)
			continue
		}
		normalized.Inbound = &inbound
		_, normalized.Authorized = connector.allowed[inbound.SenderID]
		result = append(result, normalized)
	}
	now := time.Now().UTC()
	connector.mu.Lock()
	connector.health.Status = "ready"
	connector.health.LastError = ""
	if len(result) > 0 {
		connector.health.LastReceivedAt = &now
	}
	connector.mu.Unlock()
	return result, nil
}

func (connector *TelegramConnector) Send(ctx context.Context, outbound Outbound) error {
	if outbound.Platform != Telegram || strings.TrimSpace(outbound.TargetID) == "" ||
		strings.TrimSpace(outbound.Text) == "" {
		return fmt.Errorf("telegram: valid text delivery is required")
	}
	chunks := splitTelegramText(outbound.Text)
	for _, chunk := range chunks {
		payload := map[string]any{
			"chat_id": outbound.TargetID,
			"text":    chunk,
		}
		if strings.TrimSpace(outbound.ThreadID) != "" {
			threadID, err := strconv.ParseInt(outbound.ThreadID, 10, 64)
			if err != nil {
				return fmt.Errorf("telegram: invalid message thread")
			}
			payload["message_thread_id"] = threadID
		}
		var response struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if err := connector.call(ctx, "sendMessage", payload, &response); err != nil {
			connector.recordError(err)
			return err
		}
		if !response.OK {
			err := fmt.Errorf("telegram: sendMessage rejected: %s", cleanTelegramDescription(response.Description))
			connector.recordError(err)
			return err
		}
	}
	now := time.Now().UTC()
	connector.mu.Lock()
	connector.health.Status = "ready"
	connector.health.LastDeliveredAt = &now
	connector.health.LastError = ""
	connector.mu.Unlock()
	return nil
}

// SendTyping refreshes Telegram's short-lived typing indicator while a text
// response is being prepared. It never includes message content.
func (connector *TelegramConnector) SendTyping(
	ctx context.Context,
	targetID string,
	threadID string,
) error {
	if strings.TrimSpace(targetID) == "" {
		return fmt.Errorf("telegram: typing target is required")
	}
	payload := map[string]any{
		"chat_id": targetID,
		"action":  "typing",
	}
	if strings.TrimSpace(threadID) != "" {
		parsed, err := strconv.ParseInt(threadID, 10, 64)
		if err != nil {
			return fmt.Errorf("telegram: invalid message thread")
		}
		payload["message_thread_id"] = parsed
	}
	var response struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := connector.call(ctx, "sendChatAction", payload, &response); err != nil {
		return err
	}
	if !response.OK {
		return fmt.Errorf(
			"telegram: sendChatAction rejected: %s",
			cleanTelegramDescription(response.Description),
		)
	}
	return nil
}

func (connector *TelegramConnector) Health() TelegramHealth {
	connector.mu.RLock()
	defer connector.mu.RUnlock()
	return connector.health
}

func (connector *TelegramConnector) call(
	ctx context.Context,
	method string,
	payload any,
	result any,
) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := connector.apiBase + "/bot" + connector.token + "/" + method
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, bytes.NewReader(encoded),
	)
	if err != nil {
		return fmt.Errorf("telegram: construct %s request", method)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := connector.client.Do(request)
	if err != nil {
		return fmt.Errorf("telegram: %s request failed: %s", method, connector.cleanError(err))
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTelegramResponse+1))
	if err != nil {
		return fmt.Errorf("telegram: read %s response", method)
	}
	if len(body) > maxTelegramResponse {
		return fmt.Errorf("telegram: %s response exceeds size limit", method)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var rejected struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(body, &rejected)
		description := cleanTelegramDescription(rejected.Description)
		rejectedErr := fmt.Errorf(
			"telegram: %s returned HTTP %d: %s",
			method, response.StatusCode, description,
		)
		if response.StatusCode == http.StatusConflict && method == "getUpdates" {
			return fmt.Errorf("%w: %v", ErrTelegramConflict, rejectedErr)
		}
		return rejectedErr
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("telegram: decode %s response", method)
	}
	return nil
}

func (connector *TelegramConnector) cleanError(err error) string {
	value := strings.ReplaceAll(err.Error(), connector.token, "[REDACTED]")
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}

func (connector *TelegramConnector) recordError(err error) {
	now := time.Now().UTC()
	connector.mu.Lock()
	connector.health.Status = "degraded"
	connector.health.LastErrorAt = &now
	connector.health.LastError = connector.cleanError(err)
	connector.mu.Unlock()
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message,omitempty"`
}

type telegramMessage struct {
	ID       int64  `json:"message_id"`
	ThreadID int64  `json:"message_thread_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Chat     struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	From struct {
		ID    int64 `json:"id"`
		IsBot bool  `json:"is_bot"`
	} `json:"from"`
}

func splitTelegramText(value string) []string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= telegramMessageRunes {
		return []string{value}
	}
	runes := []rune(value)
	result := make([]string, 0, (len(runes)/telegramMessageRunes)+1)
	for len(runes) > 0 {
		end := min(len(runes), telegramMessageRunes)
		if end < len(runes) {
			// Prefer a readable boundary while never exceeding Telegram's limit.
			for index := end; index > end-512 && index > 0; index-- {
				if runes[index-1] == '\n' || runes[index-1] == ' ' {
					end = index
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(runes[:end]))
		if chunk != "" {
			result = append(result, chunk)
		}
		runes = runes[end:]
	}
	return result
}

func cleanTelegramDescription(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "request was rejected"
	}
	if len(value) > 240 {
		return value[:240]
	}
	return value
}
