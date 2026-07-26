package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
)

func TestMachineMailSourceFeedsPrivateEncryptedVerificationStore(t *testing.T) {
	const apiKey = "test-machine-mail-key"
	var eventCursors []string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Authorization") != "Bearer "+apiKey {
			http.Error(writer, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/events":
			eventCursors = append(eventCursors, request.URL.Query().Get("after_seq"))
			if request.URL.Query().Get("after_seq") == "0" {
				_, _ = writer.Write([]byte(`{"events":[{
					"seq":41,"type":"message.received",
					"data":{"message_id":"msg_verification_1"}
				}]}`))
			} else {
				_, _ = writer.Write([]byte(`{"events":[]}`))
			}
		case "/v1/messages":
			if request.URL.Query().Get("direction") != "inbound" ||
				request.URL.Query().Get("limit") != "50" {
				t.Fatalf("message query = %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"messages":[{
				"id":"msg_verification_1","direction":"inbound",
				"from":{"name":"Accounts","address":"accounts@example.test"},
				"subject":"Verify the agent account","received_at":1784635200000
			}]}`))
		case "/v1/messages/msg_verification_1":
			_, _ = writer.Write([]byte(`{"message":{
				"id":"msg_verification_1","direction":"inbound",
				"from":{"name":"Accounts","address":"accounts@example.test"},
				"subject":"Verify the agent account",
				"text_body":"Verification code: 735194",
				"html_body":"<a href=\"https://supabase.example.test/auth/v1/verify?token=private-link&amp;type=magiclink\">Sign in</a>",
				"received_at":1784635200000
			}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	source, err := NewMachineMailSource(MachineMailConfig{
		BaseURL: server.URL, APIKey: apiKey, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{9}, vault.KeySize)
	cipher, err := vault.New(key)
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	path := t.TempDir() + "/mailbox/state.enc"
	store, err := Open("ion@machinemail.org", path, cipher, source)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.Sync(context.Background())
	if err != nil || added != 1 {
		t.Fatalf("sync = %d, %v", added, err)
	}
	listed := store.List(10)
	if len(listed) != 1 || listed[0].FromDomain != "example.test" ||
		listed[0].Kind != "confirmation_link" ||
		listed[0].TargetDomain != "supabase.example.test" {
		t.Fatalf("redacted metadata = %+v", listed)
	}
	encoded, _ := json.Marshal(listed)
	if bytes.Contains(encoded, []byte("735194")) ||
		bytes.Contains(encoded, []byte("private-link")) {
		t.Fatalf("model-visible metadata leaked verification: %s", encoded)
	}
	secret, err := store.Peek(listed[0].ID)
	if err != nil || !strings.Contains(secret.Value, "&type=magiclink") ||
		strings.Contains(secret.Value, "&amp;") {
		t.Fatalf("private confirmation link was not decoded safely: kind=%q err=%v",
			secret.Kind, err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(onDisk, []byte("735194")) ||
		bytes.Contains(onDisk, []byte("private-link")) ||
		bytes.Contains(onDisk, []byte("Verify the agent account")) ||
		bytes.Contains(onDisk, []byte("msg_verification_1")) {
		t.Fatal("encrypted machine-mail state leaked plaintext")
	}
	if added, err := store.Sync(context.Background()); err != nil || added != 0 {
		t.Fatalf("idempotent resync = %d, %v", added, err)
	}
	restarted, err := Open("ion@machinemail.org", path, cipher, source)
	if err != nil {
		t.Fatal(err)
	}
	if added, err := restarted.Sync(context.Background()); err != nil || added != 0 {
		t.Fatalf("restart cursor sync = %d, %v", added, err)
	}
	if len(eventCursors) != 3 || eventCursors[0] != "0" ||
		eventCursors[1] != "41" || eventCursors[2] != "41" {
		t.Fatalf("durable event cursors = %v", eventCursors)
	}
}

func TestMachineMailSourceValidationAndFailures(t *testing.T) {
	for _, config := range []MachineMailConfig{
		{},
		{BaseURL: "http://example.com", APIKey: "key"},
		{BaseURL: "https://user@example.com", APIKey: "key"},
	} {
		if _, err := NewMachineMailSource(config); err == nil {
			t.Fatalf("NewMachineMailSource(%+v) succeeded", config)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(writer, `{"error":"denied"}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	source, err := NewMachineMailSource(MachineMailConfig{
		BaseURL: server.URL, APIKey: "wrong", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.FetchRecent(context.Background(), 1); err == nil ||
		strings.Contains(err.Error(), "wrong") || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("safe authentication error = %v", err)
	}
}

func TestParseMachineMailTime(t *testing.T) {
	want := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	for _, raw := range []json.RawMessage{
		json.RawMessage(`1784635200000`),
		json.RawMessage(`"2026-07-21T12:00:00Z"`),
	} {
		if got := parseMachineMailTime(raw); !got.Equal(want) {
			t.Fatalf("parseMachineMailTime(%s) = %s, want %s", raw, got, want)
		}
	}
}

func TestConfiguredMachineMailService(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("MACHINE_MAIL_API_KEY"))
	address := strings.TrimSpace(os.Getenv("MACHINE_MAIL_ADDRESS"))
	if key == "" || address == "" {
		t.Skip("machine-mail live credentials are not configured in the process environment")
	}
	if _, err := mail.ParseAddress(address); err != nil {
		t.Fatal("configured machine-mail address is invalid")
	}
	source, err := NewMachineMailSource(MachineMailConfig{
		BaseURL: strings.TrimSpace(os.Getenv("MACHINE_MAIL_URL")),
		APIKey:  key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.FetchRecent(context.Background(), 10); err != nil {
		t.Fatalf("configured machine-mail read failed: %v", err)
	}
	if _, _, err := source.FetchAfter(context.Background(), 0, 10); err != nil {
		t.Fatalf("configured machine-mail event read failed: %v", err)
	}
}
