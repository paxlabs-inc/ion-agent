package operatorapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/presence/gateway"
)

func TestTelegramUsesProductionTurnPathAndDurableCursorAcrossRestart(
	t *testing.T,
) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	workspace := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)

	var providerMu sync.Mutex
	var providerBodies [][]byte
	provider := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if bytes.Contains(body, []byte("Extract only load-bearing factual premises")) {
			_, _ = io.WriteString(writer, `{
				"id":"telegram-premises","model":"telegram-test",
				"choices":[{"message":{"content":"{\"premises\":[]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`)
			return
		}
		providerMu.Lock()
		providerBodies = append(providerBodies, append([]byte(nil), body...))
		providerMu.Unlock()
		_, _ = io.WriteString(writer, `{
			"id":"telegram-turn","model":"telegram-test",
			"choices":[{"message":{"content":"Telegram shares the production core."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	defer provider.Close()

	var bootstrap atomic.Bool
	var delivered atomic.Int32
	var updateOffsetsMu sync.Mutex
	var updateOffsets []int64
	telegram := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/getUpdates"):
			var payload struct {
				Offset int64 `json:"offset"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			updateOffsetsMu.Lock()
			updateOffsets = append(updateOffsets, payload.Offset)
			updateOffsetsMu.Unlock()
			if !bootstrap.Swap(true) {
				_, _ = io.WriteString(writer, `{"ok":true,"result":[]}`)
				return
			}
			if payload.Offset <= 7 && delivered.Load() == 0 {
				_, _ = io.WriteString(writer, `{"ok":true,"result":[{
					"update_id":7,
					"message":{"message_id":5,"text":"hello","chat":{"id":99},
					"from":{"id":42,"is_bot":false}}
				}]}`)
				return
			}
			if payload.Offset <= 8 && delivered.Load() == 1 {
				_, _ = io.WriteString(writer, `{"ok":true,"result":[{
					"update_id":8,
					"message":{"message_id":6,"text":"Andrew","chat":{"id":99},
					"from":{"id":42,"is_bot":false}}
				}]}`)
				return
			}
			if payload.Offset <= 9 && delivered.Load() == 2 {
				_, _ = io.WriteString(writer, `{"ok":true,"result":[{
					"update_id":9,
					"message":{"message_id":7,"text":"hello","chat":{"id":99},
					"from":{"id":42,"is_bot":false}}
				}]}`)
				return
			}
			select {
			case <-request.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
				_, _ = io.WriteString(writer, `{"ok":true,"result":[]}`)
			}
		case strings.HasSuffix(request.URL.Path, "/sendMessage"):
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			expected := []string{
				"Before we begin, what should I call you?",
				"Thanks, Andrew. What should we work on?",
				"Telegram shares the production core.",
			}
			index := int(delivered.Load())
			if index >= len(expected) || payload.Text != expected[index] {
				t.Errorf("Telegram response = %q", payload.Text)
			}
			delivered.Add(1)
			_, _ = io.WriteString(writer, `{"ok":true,"result":{"message_id":8}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer telegram.Close()

	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: workspace,
		ProviderName:       "openai-test", ProviderBaseURL: provider.URL,
		ProviderAPIKey: "test-only", ProviderModel: "telegram-test",
		ProviderHTTPClient:   provider.Client(),
		TelegramBotToken:     "123456:test-token",
		TelegramAllowedUsers: "42",
		TelegramHTTPClient:   telegram.Client(),
		TelegramAPIBaseURL:   telegram.URL,
		TelegramPollTimeout:  100 * time.Millisecond,
	}
	runtime, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	waitForTelegram(t, 10*time.Second, func() bool { return delivered.Load() == 3 })
	channels := runtime.capabilityRoot.ChannelList()
	telegramReady := false
	for _, channel := range channels {
		if channel.Name == "Telegram" && channel.Configured {
			telegramReady = true
		}
	}
	if len(channels) != 2 || !telegramReady {
		t.Fatalf("channel projection = %+v", channels)
	}
	mappings, err := runtime.sessions.ListLivingStates(ctx, channelMappingKind)
	if err != nil || len(mappings) != 1 {
		t.Fatalf("channel mappings = %+v, %v", mappings, err)
	}
	var mapping channelSession
	if err := json.Unmarshal(mappings[0].State, &mapping); err != nil {
		t.Fatal(err)
	}
	messages, err := runtime.sessions.ListMessages(ctx, mapping.SessionID)
	if err != nil || len(messages) != 6 ||
		string(messages[0].Content) != "hello" ||
		string(messages[1].Content) != "Before we begin, what should I call you?" ||
		string(messages[2].Content) != "Andrew" ||
		string(messages[3].Content) != "Thanks, Andrew. What should we work on?" ||
		string(messages[4].Content) != "hello" ||
		string(messages[5].Content) != "Telegram shares the production core." {
		t.Fatalf("Telegram transcript = %+v, %v", messages, err)
	}
	owner, err := runtime.capabilityRoot.living.Owner(ctx, mapping.SessionID)
	if err != nil || owner != mapping.ActorID {
		t.Fatalf("Telegram owner = %s, want %s (%v)", owner, mapping.ActorID, err)
	}
	providerMu.Lock()
	if len(providerBodies) != 1 ||
		!bytes.Contains(providerBodies[0], []byte("Immutable living-context snapshot")) ||
		!bytes.Contains(providerBodies[0], []byte("SOUL identity anchor")) {
		providerMu.Unlock()
		t.Fatalf("provider bodies do not prove shared living path: %d", len(providerBodies))
	}
	providerMu.Unlock()
	database, err := os.ReadFile(dataDirectory + "/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte("Telegram shares the production core.")) ||
		bytes.Contains(database, []byte("hello")) {
		t.Fatal("Telegram transcript appeared in plaintext SQLite")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 3 {
		t.Fatalf("durable cursor replayed Telegram update: deliveries=%d", delivered.Load())
	}
	updateOffsetsMu.Lock()
	defer updateOffsetsMu.Unlock()
	foundDurableOffset := false
	for _, offset := range updateOffsets {
		if offset == 10 {
			foundDurableOffset = true
			break
		}
	}
	if !foundDurableOffset {
		t.Fatalf("Telegram offsets never restored durable cursor: %v", updateOffsets)
	}
}

func waitForTelegram(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for Telegram delivery")
}

func TestTelegramTypingRefreshStopsImmediatelyWithTurn(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if !strings.HasSuffix(request.URL.Path, "/sendChatAction") {
			http.NotFound(writer, request)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload["chat_id"] != "99" || payload["action"] != "typing" ||
			payload["message_thread_id"] != float64(8) {
			t.Errorf("typing payload = %+v", payload)
		}
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ok":true,"result":true}`)
	}))
	defer telegramAPI.Close()
	connector, err := gateway.NewTelegramConnector(gateway.TelegramConfig{
		BotToken: "123456:test-token", AllowedUsers: []string{"42"},
		HTTPClient: telegramAPI.Client(), APIBaseURL: telegramAPI.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &channelRuntime{
		telegram: connector, typingEvery: 10 * time.Millisecond,
	}
	stop := runtime.startTelegramTyping(context.Background(), gateway.Inbound{
		Platform: gateway.Telegram, ConversationID: "99", ThreadID: "8",
	})
	waitForTelegram(t, time.Second, func() bool { return calls.Load() >= 3 })
	stop()
	stoppedAt := calls.Load()
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != stoppedAt {
		t.Fatalf("typing continued after turn completion: before=%d after=%d",
			stoppedAt, calls.Load())
	}
}

func TestTelegramTurnTimeoutIsVisibleAndRetryRunsAsynchronously(t *testing.T) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	var mainCalls atomic.Int32
	var allowRecovery atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		body, _ := io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		if bytes.Contains(body, []byte("Extract only load-bearing factual premises")) {
			_, _ = io.WriteString(writer, `{"id":"premises","model":"test",
				"choices":[{"message":{"content":"{\"premises\":[]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		mainCalls.Add(1)
		if !allowRecovery.Load() {
			<-request.Context().Done()
			return
		}
		_, _ = io.WriteString(writer, `{"id":"turn","model":"test",
			"choices":[{"message":{"content":"Recovered Telegram reply."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer provider.Close()

	var delivered atomic.Int32
	telegram := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(request.URL.Path, "/getUpdates"):
			_, _ = io.WriteString(writer, `{"ok":true,"result":[]}`)
		case strings.HasSuffix(request.URL.Path, "/sendChatAction"):
			_, _ = io.WriteString(writer, `{"ok":true,"result":true}`)
		case strings.HasSuffix(request.URL.Path, "/sendMessage"):
			delivered.Add(1)
			_, _ = io.WriteString(writer, `{"ok":true,"result":{"message_id":10}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer telegram.Close()

	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
		ProviderName:       "test", ProviderBaseURL: provider.URL,
		ProviderAPIKey: "test-only", ProviderModel: "test",
		ProviderHTTPClient: provider.Client(),
		TelegramBotToken:   "123456:test-token", TelegramAllowedUsers: "42",
		TelegramHTTPClient: telegram.Client(), TelegramAPIBaseURL: telegram.URL,
		TelegramPollTimeout: 50 * time.Millisecond,
		TelegramTurnTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	inbound := gateway.Inbound{
		Platform: gateway.Telegram, ConversationID: "99", SenderID: "42",
		MessageID: "77", Text: "perform bounded work",
	}
	processed := make(chan error, 1)
	go func() {
		_, processErr := runtime.channels.processTelegramUpdate(ctx, 77, inbound)
		processed <- processErr
	}()
	waitForTelegram(t, 5*time.Second, func() bool {
		for _, channel := range runtime.capabilityRoot.ChannelList() {
			if channel.Name == "Telegram" {
				if channel.Status == "working" &&
					len(channel.ActiveUpdates) == 1 &&
					channel.ActiveUpdates[0].Status == "processing" {
					return true
				}
			}
		}
		durable, found, loadErr := runtime.channels.loadTelegramUpdate(ctx, 77)
		return loadErr == nil && found &&
			(durable.Status == "processing" || durable.Status == "quarantined")
	})
	select {
	case processErr := <-processed:
		if processErr == nil ||
			!strings.Contains(processErr.Error(), "channel_turn_timeout") {
			t.Fatalf("timeout processing error = %v", processErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Telegram turn did not stop at its total deadline")
	}
	state, found, err := runtime.channels.loadTelegramUpdate(ctx, 77)
	if err != nil || !found || state.Status != "quarantined" ||
		state.FailureCode != "channel_turn_timeout" || state.TurnID == nil {
		t.Fatalf("timed out update = %+v, found=%v, err=%v", state, found, err)
	}
	allowRecovery.Store(true)
	runtime.channels.turnTimeout = 10 * time.Second
	started := time.Now()
	queued, err := runtime.channels.RetryTelegramUpdate(ctx, 77)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "retry_wait" || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("retry was not queued asynchronously: %+v after %s", queued, time.Since(started))
	}
	deadline := time.Now().Add(15 * time.Second)
	var terminal telegramUpdateState
	for time.Now().Before(deadline) {
		terminal, _, _ = runtime.channels.loadTelegramUpdate(ctx, 77)
		if delivered.Load() == 1 && terminal.Status == "delivered" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if delivered.Load() != 1 || terminal.Status != "delivered" {
		failed, _, loadErr := runtime.channels.loadTelegramUpdate(ctx, 77)
		t.Fatalf(
			"timed out waiting for Telegram delivery: state=%+v main_calls=%d load_err=%v",
			failed, mainCalls.Load(), loadErr,
		)
	}
	state, found, err = runtime.channels.loadTelegramUpdate(ctx, 77)
	if err != nil || !found || state.Status != "delivered" || state.Attempts != 2 {
		t.Fatalf("retried update = %+v, found=%v, err=%v", state, found, err)
	}
	dead, active := runtime.channels.telegramUpdateProjections()
	if len(dead) != 0 || len(active) != 0 {
		t.Fatalf("terminal projections: dead=%+v active=%+v", dead, active)
	}
}

func TestTelegramCursorScopeChangesOnTokenRotationWithoutLeakingToken(
	t *testing.T,
) {
	secret := []byte("01234567890123456789012345678901")
	firstToken := "123456:first-private-token"
	secondToken := "123456:second-private-token"
	first := telegramCursorScope(secret, firstToken)
	if first == telegramCursorScope(secret, secondToken) {
		t.Fatal("rotated Telegram token reused its durable cursor scope")
	}
	if first != telegramCursorScope(secret, firstToken) {
		t.Fatal("Telegram cursor scope is not stable for the same token")
	}
	if strings.Contains(first, firstToken) ||
		strings.Contains(first, "first-private-token") {
		t.Fatal("Telegram cursor scope leaked token material")
	}
}

func TestTelegramRepairsInitialTextualMarkupAndContinues(t *testing.T) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	var mainCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		body, _ := io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		if bytes.Contains(body, []byte("Extract only load-bearing factual premises")) {
			_, _ = io.WriteString(writer, `{"id":"premises","model":"test",
				"choices":[{"message":{"content":"{\"premises\":[]}"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		content := "healthy response"
		if mainCalls.Add(1) == 1 {
			content = "<tool_call><function=filesystem_list><parameter=path>."
		}
		encoded, _ := json.Marshal(content)
		_, _ = fmt.Fprintf(writer, `{"id":"turn","model":"test",
			"choices":[{"message":{"content":%s},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			encoded)
	}))
	defer provider.Close()

	var bootstrapped atomic.Bool
	var delivered atomic.Int32
	telegram := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/sendMessage") {
			delivered.Add(1)
			_, _ = io.WriteString(writer, `{"ok":true,"result":{"message_id":9}}`)
			return
		}
		var payload struct {
			Offset int64 `json:"offset"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if !bootstrapped.Swap(true) {
			_, _ = io.WriteString(writer, `{"ok":true,"result":[]}`)
			return
		}
		switch {
		case payload.Offset <= 7:
			_, _ = io.WriteString(writer, `{"ok":true,"result":[{
				"update_id":7,"message":{"message_id":7,"text":"poison",
				"chat":{"id":99},"from":{"id":42,"is_bot":false}}}]}`)
		case payload.Offset == 8:
			_, _ = io.WriteString(writer, `{"ok":true,"result":[{
				"update_id":8,"message":{"message_id":8,"text":"healthy",
				"chat":{"id":99},"from":{"id":42,"is_bot":false}}}]}`)
		default:
			_, _ = io.WriteString(writer, `{"ok":true,"result":[]}`)
		}
	}))
	defer telegram.Close()

	runtime, err := OpenRuntime(ctx, RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
		ProviderName:       "test", ProviderBaseURL: provider.URL,
		ProviderAPIKey: "test-only", ProviderModel: "test",
		ProviderHTTPClient: provider.Client(),
		TelegramBotToken:   "123456:test-token", TelegramAllowedUsers: "42",
		TelegramHTTPClient: telegram.Client(), TelegramAPIBaseURL: telegram.URL,
		TelegramPollTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	waitForTelegram(t, 10*time.Second, func() bool {
		if delivered.Load() != 2 {
			return false
		}
		cursor, initialized, err := runtime.channels.loadTelegramCursor(ctx)
		return err == nil && initialized && cursor == 9
	})
	if dead := runtime.channels.telegramDeadLetters(); len(dead) != 0 {
		t.Fatalf("repairable update was quarantined: %+v", dead)
	}
	cursor, initialized, err := runtime.channels.loadTelegramCursor(ctx)
	if err != nil || !initialized || cursor != 9 {
		t.Fatalf("cursor after poison = %d, %v, %v", cursor, initialized, err)
	}
	if delivered.Load() != 2 {
		t.Fatalf("repaired delivery count = %d", delivered.Load())
	}
}

func TestTelegramRestartDoesNotDuplicateAmbiguousDelivery(t *testing.T) {
	ctx := context.Background()
	dataDirectory := t.TempDir()
	initializeRuntimeVault(t, ctx, dataDirectory)
	var replay atomic.Bool
	var sends atomic.Int32
	telegram := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/sendMessage") {
			sends.Add(1)
			_, _ = io.WriteString(writer, `{"ok":true,"result":{"message_id":9}}`)
			return
		}
		var payload struct {
			Offset int64 `json:"offset"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if replay.Load() && payload.Offset <= 7 {
			_, _ = io.WriteString(writer, `{"ok":true,"result":[{
				"update_id":7,"message":{"message_id":7,"text":"already handled",
				"chat":{"id":99},"from":{"id":42,"is_bot":false}}}]}`)
			return
		}
		_, _ = io.WriteString(writer, `{"ok":true,"result":[]}`)
	}))
	defer telegram.Close()
	config := RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: true,
		WorkspaceDirectory: t.TempDir(),
		TelegramBotToken:   "123456:test-token", TelegramAllowedUsers: "42",
		TelegramHTTPClient: telegram.Client(), TelegramAPIBaseURL: telegram.URL,
		TelegramPollTimeout: 100 * time.Millisecond,
	}
	first, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	waitForTelegram(t, time.Second, func() bool {
		_, initialized, _ := first.channels.loadTelegramCursor(ctx)
		return initialized
	})
	inbound := gateway.Inbound{
		Platform: gateway.Telegram, ConversationID: "99", SenderID: "42",
		MessageID: "7", Text: "already handled",
	}
	outbound := gateway.Outbound{
		SessionKey: first.channels.gateway.SessionKey(inbound),
		Platform:   gateway.Telegram, TargetID: "99", Text: "ambiguous response",
	}
	if err := first.channels.saveTelegramCursor(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := first.channels.saveTelegramUpdate(ctx, telegramUpdateState{
		Version: 1, UpdateID: 7, Status: "sending", Attempts: 1,
		Inbound: &inbound, Outbound: &outbound, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	interrupted := inbound
	interrupted.MessageID = "8"
	if err := first.channels.saveTelegramUpdate(ctx, telegramUpdateState{
		Version: 1, UpdateID: 8, Status: "processing", Attempts: 1,
		Inbound: &interrupted, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	replay.Store(true)
	restarted, err := OpenRuntime(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	waitForTelegram(t, time.Second, func() bool {
		cursor, _, _ := restarted.channels.loadTelegramCursor(ctx)
		return cursor == 8
	})
	if sends.Load() != 0 {
		t.Fatalf("ambiguous delivery was duplicated %d time(s)", sends.Load())
	}
	dead := restarted.channels.telegramDeadLetters()
	if len(dead) != 2 || dead[0].FailureCode != "delivery_outcome_unknown" ||
		dead[1].UpdateID != 8 || dead[1].FailureCode != "processing_interrupted" {
		t.Fatalf("ambiguous delivery state = %+v", dead)
	}
}
