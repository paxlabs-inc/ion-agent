// Package gateway normalizes Telegram, Discord, and Slack into one shared
// agent core while deriving collision-resistant, platform-scoped session keys.
package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Platform string

const (
	Telegram Platform = "telegram"
	Discord  Platform = "discord"
	Slack    Platform = "slack"
)

func (platform Platform) Valid() bool {
	switch platform {
	case Telegram, Discord, Slack:
		return true
	default:
		return false
	}
}

// Inbound is the platform-neutral message envelope. ScopeID is mandatory for
// Discord guilds and Slack workspaces, preventing cross-tenant collisions.
type Inbound struct {
	Platform       Platform `json:"platform"`
	ScopeID        string   `json:"scope_id,omitempty"`
	ConversationID string   `json:"conversation_id"`
	ThreadID       string   `json:"thread_id,omitempty"`
	SenderID       string   `json:"sender_id"`
	MessageID      string   `json:"message_id"`
	Text           string   `json:"text"`
}

type Turn struct {
	SessionKey string
	Inbound    Inbound
	Soul       string
}

type Outbound struct {
	SessionKey string   `json:"session_key"`
	Platform   Platform `json:"platform"`
	TargetID   string   `json:"target_id"`
	ThreadID   string   `json:"thread_id,omitempty"`
	Text       string   `json:"text"`
}

// Core is the single identity-bearing agent instance shared by all connectors.
type Core interface {
	Respond(context.Context, Turn) (string, error)
}

// SoulProvider loads the identity anchor for every interaction.
type SoulProvider interface {
	Load(context.Context) (string, error)
}

// Connector owns platform delivery at the true external boundary.
type Connector interface {
	Platform() Platform
	Send(context.Context, Outbound) error
}

type Gateway struct {
	mu         sync.RWMutex
	core       Core
	soul       SoulProvider
	key        []byte
	connectors map[Platform]Connector
}

func New(core Core, sessionKeySecret []byte, soul SoulProvider) (*Gateway, error) {
	if core == nil || soul == nil || len(sessionKeySecret) < 32 {
		return nil, fmt.Errorf("gateway: core, SOUL provider, and at least 32 bytes of session key material are required")
	}
	return &Gateway{
		core: core, soul: soul, key: append([]byte(nil), sessionKeySecret...),
		connectors: make(map[Platform]Connector),
	}, nil
}

func (gateway *Gateway) Register(connector Connector) error {
	if connector == nil || !connector.Platform().Valid() {
		return fmt.Errorf("gateway: valid connector is required")
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	platform := connector.Platform()
	if _, exists := gateway.connectors[platform]; exists {
		return fmt.Errorf("gateway: duplicate %s connector", platform)
	}
	gateway.connectors[platform] = connector
	return nil
}

func (gateway *Gateway) Platforms() []Platform {
	gateway.mu.RLock()
	defer gateway.mu.RUnlock()
	result := make([]Platform, 0, len(gateway.connectors))
	for platform := range gateway.connectors {
		result = append(result, platform)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// Handle sends every platform through the exact same Core object.
func (gateway *Gateway) Handle(ctx context.Context, inbound Inbound) (Outbound, error) {
	outbound, err := gateway.Prepare(ctx, inbound)
	if err != nil {
		return Outbound{}, err
	}
	if err := gateway.Deliver(ctx, outbound); err != nil {
		return Outbound{}, err
	}
	return outbound, nil
}

// Prepare runs the shared core without crossing the external delivery
// boundary, allowing callers to durably record intent before sending.
func (gateway *Gateway) Prepare(ctx context.Context, inbound Inbound) (Outbound, error) {
	if err := validateInbound(inbound); err != nil {
		return Outbound{}, err
	}
	gateway.mu.RLock()
	connector := gateway.connectors[inbound.Platform]
	gateway.mu.RUnlock()
	if connector == nil {
		return Outbound{}, fmt.Errorf("gateway: no %s connector", inbound.Platform)
	}
	sessionKey := gateway.SessionKey(inbound)
	soul, err := gateway.soul.Load(ctx)
	if err != nil {
		return Outbound{}, fmt.Errorf("gateway: load SOUL.md: %w", err)
	}
	text, err := gateway.core.Respond(ctx, Turn{
		SessionKey: sessionKey,
		Inbound:    cloneInbound(inbound),
		Soul:       soul,
	})
	if err != nil {
		return Outbound{}, fmt.Errorf("gateway: core response: %w", err)
	}
	outbound := Outbound{
		SessionKey: sessionKey,
		Platform:   inbound.Platform, TargetID: inbound.ConversationID,
		ThreadID: inbound.ThreadID, Text: text,
	}
	return outbound, nil
}

// Deliver crosses only the registered connector boundary.
func (gateway *Gateway) Deliver(ctx context.Context, outbound Outbound) error {
	gateway.mu.RLock()
	connector := gateway.connectors[outbound.Platform]
	gateway.mu.RUnlock()
	if connector == nil {
		return fmt.Errorf("gateway: no %s connector", outbound.Platform)
	}
	if err := connector.Send(ctx, outbound); err != nil {
		return fmt.Errorf("gateway: %s delivery: %w", outbound.Platform, err)
	}
	return nil
}

// SessionKey is an opaque HMAC over every isolation discriminator.
func (gateway *Gateway) SessionKey(inbound Inbound) string {
	mac := hmac.New(sha256.New, gateway.key)
	for _, part := range []string{
		string(inbound.Platform), inbound.ScopeID, inbound.ConversationID,
		inbound.ThreadID, inbound.SenderID,
	} {
		_, _ = mac.Write([]byte{byte(len(part) >> 24), byte(len(part) >> 16), byte(len(part) >> 8), byte(len(part))})
		_, _ = mac.Write([]byte(part))
	}
	return "gw1_" + hex.EncodeToString(mac.Sum(nil))
}

func validateInbound(inbound Inbound) error {
	if !inbound.Platform.Valid() || strings.TrimSpace(inbound.ConversationID) == "" ||
		strings.TrimSpace(inbound.SenderID) == "" ||
		strings.TrimSpace(inbound.MessageID) == "" ||
		strings.TrimSpace(inbound.Text) == "" {
		return fmt.Errorf("gateway: platform, conversation, sender, message, and text are required")
	}
	if (inbound.Platform == Discord || inbound.Platform == Slack) &&
		strings.TrimSpace(inbound.ScopeID) == "" {
		return fmt.Errorf("gateway: %s scope ID is required", inbound.Platform)
	}
	return nil
}

func cloneInbound(inbound Inbound) Inbound { return inbound }

// DecodeTelegram accepts the relevant Telegram Bot API update shape.
func DecodeTelegram(payload []byte) (Inbound, error) {
	var update struct {
		UpdateID int64 `json:"update_id"`
		Message  struct {
			ID       int64  `json:"message_id"`
			ThreadID int64  `json:"message_thread_id"`
			Text     string `json:"text"`
			Chat     struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			From struct {
				ID int64 `json:"id"`
			} `json:"from"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &update); err != nil {
		return Inbound{}, fmt.Errorf("gateway: decode telegram: %w", err)
	}
	inbound := Inbound{
		Platform: Telegram, ConversationID: fmt.Sprint(update.Message.Chat.ID),
		SenderID:  fmt.Sprint(update.Message.From.ID),
		MessageID: fmt.Sprint(update.Message.ID), Text: update.Message.Text,
	}
	if update.Message.ThreadID != 0 {
		inbound.ThreadID = fmt.Sprint(update.Message.ThreadID)
	}
	if err := validateInbound(inbound); err != nil {
		return Inbound{}, err
	}
	return inbound, nil
}

// DecodeDiscord accepts a Discord message-create event body.
func DecodeDiscord(payload []byte) (Inbound, error) {
	var message struct {
		ID        string `json:"id"`
		GuildID   string `json:"guild_id"`
		ChannelID string `json:"channel_id"`
		Content   string `json:"content"`
		Author    struct {
			ID string `json:"id"`
		} `json:"author"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return Inbound{}, fmt.Errorf("gateway: decode discord: %w", err)
	}
	inbound := Inbound{
		Platform: Discord, ScopeID: message.GuildID,
		ConversationID: message.ChannelID, SenderID: message.Author.ID,
		MessageID: message.ID, Text: message.Content,
	}
	if err := validateInbound(inbound); err != nil {
		return Inbound{}, err
	}
	return inbound, nil
}

// DecodeSlack accepts a Slack Events API callback body.
func DecodeSlack(payload []byte) (Inbound, error) {
	var callback struct {
		TeamID string `json:"team_id"`
		Event  struct {
			TS       string `json:"ts"`
			ThreadTS string `json:"thread_ts"`
			Channel  string `json:"channel"`
			User     string `json:"user"`
			Text     string `json:"text"`
		} `json:"event"`
	}
	if err := json.Unmarshal(payload, &callback); err != nil {
		return Inbound{}, fmt.Errorf("gateway: decode slack: %w", err)
	}
	inbound := Inbound{
		Platform: Slack, ScopeID: callback.TeamID,
		ConversationID: callback.Event.Channel, ThreadID: callback.Event.ThreadTS,
		SenderID: callback.Event.User, MessageID: callback.Event.TS, Text: callback.Event.Text,
	}
	if err := validateInbound(inbound); err != nil {
		return Inbound{}, err
	}
	return inbound, nil
}
