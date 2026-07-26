package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/paxlabs-inc/ion-agent/internal/presence/gateway"
)

type sharedCore struct {
	mu       sync.Mutex
	sessions map[string][]string
	calls    int
}

func (core *sharedCore) Respond(_ context.Context, turn gateway.Turn) (string, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.sessions == nil {
		core.sessions = make(map[string][]string)
	}
	previous := append([]string(nil), core.sessions[turn.SessionKey]...)
	core.sessions[turn.SessionKey] = append(core.sessions[turn.SessionKey], turn.Inbound.Text)
	core.calls++
	return fmt.Sprintf("Ion[%d] previous=%v current=%s",
		core.calls, previous, turn.Inbound.Text), nil
}

type connector struct {
	platform gateway.Platform
	mu       sync.Mutex
	sent     []gateway.Outbound
}

type staticSoul struct{}

func (staticSoul) Load(context.Context) (string, error) {
	return "# SOUL\nI am the same Ion.", nil
}

func (connector *connector) Platform() gateway.Platform { return connector.platform }
func (connector *connector) Send(_ context.Context, outbound gateway.Outbound) error {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.sent = append(connector.sent, outbound)
	return nil
}

func TestGatewayAcceptanceThreePlatformsOneCoreAndSessionIsolation(t *testing.T) {
	core := &sharedCore{}
	gatewayInstance, err := gateway.New(
		core, []byte("0123456789abcdef0123456789abcdef"), staticSoul{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []gateway.Platform{
		gateway.Telegram, gateway.Discord, gateway.Slack,
	} {
		if err := gatewayInstance.Register(&connector{platform: platform}); err != nil {
			t.Fatal(err)
		}
	}

	telegram, err := gateway.DecodeTelegram([]byte(
		`{"update_id":1,"message":{"message_id":11,"text":"telegram secret",` +
			`"chat":{"id":100},"from":{"id":7}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	discord, err := gateway.DecodeDiscord([]byte(
		`{"id":"d1","guild_id":"guild-a","channel_id":"channel-a",` +
			`"content":"discord secret","author":{"id":"user-a"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	slack, err := gateway.DecodeSlack([]byte(
		`{"team_id":"workspace-a","event":{"ts":"1.1","channel":"channel-a",` +
			`"user":"user-a","text":"slack secret"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	telegramFirst, err := gatewayInstance.Handle(ctx, telegram)
	if err != nil {
		t.Fatal(err)
	}
	discordFirst, err := gatewayInstance.Handle(ctx, discord)
	if err != nil {
		t.Fatal(err)
	}
	slackFirst, err := gatewayInstance.Handle(ctx, slack)
	if err != nil {
		t.Fatal(err)
	}
	if telegramFirst.SessionKey == discordFirst.SessionKey ||
		discordFirst.SessionKey == slackFirst.SessionKey ||
		telegramFirst.SessionKey == slackFirst.SessionKey {
		t.Fatal("different platform scopes collided")
	}
	for _, outbound := range []gateway.Outbound{discordFirst, slackFirst} {
		if contains(outbound.Text, "telegram secret") {
			t.Fatalf("cross-channel leakage in %q", outbound.Text)
		}
	}

	telegram.Text = "telegram follow-up"
	telegram.MessageID = "12"
	telegramSecond, err := gatewayInstance.Handle(ctx, telegram)
	if err != nil {
		t.Fatal(err)
	}
	if telegramSecond.SessionKey != telegramFirst.SessionKey ||
		!contains(telegramSecond.Text, "telegram secret") ||
		contains(telegramSecond.Text, "discord secret") ||
		contains(telegramSecond.Text, "slack secret") {
		t.Fatalf("isolated continuation = %q", telegramSecond.Text)
	}
	if core.calls != 4 {
		t.Fatalf("shared core calls = %d", core.calls)
	}
}

func TestGatewayRequiresDiscordAndSlackTenantScope(t *testing.T) {
	payloads := [][]byte{
		[]byte(`{"id":"d1","channel_id":"c","content":"x","author":{"id":"u"}}`),
		[]byte(`{"event":{"ts":"1","channel":"c","user":"u","text":"x"}}`),
	}
	decoders := []func([]byte) (gateway.Inbound, error){
		gateway.DecodeDiscord, gateway.DecodeSlack,
	}
	for index, decode := range decoders {
		if _, err := decode(payloads[index]); err == nil {
			t.Fatalf("decoder %d accepted missing tenant scope", index)
		}
	}
}

func contains(value, fragment string) bool {
	encoded, _ := json.Marshal(value)
	return string(encoded) != "" &&
		len(fragment) <= len(value) &&
		func() bool {
			for index := 0; index+len(fragment) <= len(value); index++ {
				if value[index:index+len(fragment)] == fragment {
					return true
				}
			}
			return false
		}()
}
