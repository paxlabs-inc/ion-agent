package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegramConnectorLongPollingAllowlistDeliveryAndRedaction(t *testing.T) {
	var mu sync.Mutex
	var deliveries []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/getUpdates"):
			_, _ = io.WriteString(writer, `{"ok":true,"result":[
				{"update_id":10,"message":{"message_id":1,"text":"allowed","chat":{"id":99},"from":{"id":42,"is_bot":false}}},
				{"update_id":11,"message":{"message_id":2,"text":"denied","chat":{"id":99},"from":{"id":43,"is_bot":false}}},
				{"update_id":12,"message":{"message_id":3,"text":"bot","chat":{"id":99},"from":{"id":42,"is_bot":true}}}
			]}`)
		case strings.HasSuffix(request.URL.Path, "/sendMessage"):
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			deliveries = append(deliveries, payload)
			mu.Unlock()
			_, _ = io.WriteString(writer, `{"ok":true,"result":{"message_id":1}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	connector, err := NewTelegramConnector(TelegramConfig{
		BotToken: "123456:test-token", AllowedUsers: []string{"42"},
		HTTPClient: server.Client(), APIBaseURL: server.URL,
		PollTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	updates, err := connector.Updates(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 3 || updates[0].Inbound == nil ||
		!updates[0].Authorized || updates[1].Authorized ||
		updates[2].Inbound != nil {
		t.Fatalf("updates = %+v", updates)
	}
	long := strings.Repeat("x", telegramMessageRunes+10)
	if err := connector.Send(context.Background(), Outbound{
		Platform: Telegram, TargetID: "99", ThreadID: "8", Text: long,
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(deliveries))
	}
	for _, delivery := range deliveries {
		if delivery["chat_id"] != "99" || delivery["message_thread_id"] != float64(8) ||
			len([]rune(delivery["text"].(string))) > telegramMessageRunes {
			t.Fatalf("delivery = %+v", delivery)
		}
	}
	health := connector.Health()
	encoded, _ := json.Marshal(health)
	if health.Status != "ready" || health.AllowedUserCount != 1 ||
		strings.Contains(string(encoded), "test-token") ||
		strings.Contains(string(encoded), `"42"`) {
		t.Fatalf("health = %s", encoded)
	}
}

func TestTelegramConnectorRequiresExplicitAllowlist(t *testing.T) {
	if _, err := NewTelegramConnector(TelegramConfig{
		BotToken: "123456:test-token",
	}); err == nil || !strings.Contains(err.Error(), "allowed user") {
		t.Fatalf("error = %v", err)
	}
}

func TestTelegramConnectorSendsThreadScopedTyping(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if !strings.HasSuffix(request.URL.Path, "/sendChatAction") {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(writer, `{"ok":true,"result":true}`)
	}))
	defer server.Close()
	connector, err := NewTelegramConnector(TelegramConfig{
		BotToken: "123456:test-token", AllowedUsers: []string{"42"},
		HTTPClient: server.Client(), APIBaseURL: server.URL,
		PollTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := connector.SendTyping(context.Background(), "99", "8"); err != nil {
		t.Fatal(err)
	}
	if payload["chat_id"] != "99" || payload["action"] != "typing" ||
		payload["message_thread_id"] != float64(8) {
		t.Fatalf("typing payload = %+v", payload)
	}
}

func TestTelegramConnectorClassifiesCompetingConsumerWithoutLeakingToken(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(writer, `{
			"ok":false,
			"description":"Conflict: terminated by other getUpdates request"
		}`)
	}))
	defer server.Close()
	connector, err := NewTelegramConnector(TelegramConfig{
		BotToken: "123456:test-token", AllowedUsers: []string{"42"},
		HTTPClient: server.Client(), APIBaseURL: server.URL,
		PollTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = connector.Updates(context.Background(), 0)
	if !errors.Is(err, ErrTelegramConflict) ||
		strings.Contains(err.Error(), "test-token") {
		t.Fatalf("conflict error = %v", err)
	}
}
