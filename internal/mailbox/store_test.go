package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
)

type fixedSource struct {
	messages []Message
}

func (source fixedSource) FetchRecent(
	context.Context,
	int,
) ([]Message, error) {
	return append([]Message(nil), source.messages...), nil
}

type refreshSource struct {
	messages []Message
}

func (source refreshSource) FetchRecent(
	context.Context,
	int,
) ([]Message, error) {
	return append([]Message(nil), source.messages...), nil
}

func (refreshSource) FetchAfter(
	context.Context,
	int64,
	int,
) ([]Message, int64, error) {
	return nil, 41, nil
}

func TestMailboxEncryptsAndRedactsVerificationSecretsAcrossRestart(t *testing.T) {
	key := bytes.Repeat([]byte{7}, vault.KeySize)
	cipher, err := vault.New(key)
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	path := t.TempDir() + "/mailbox/state.enc"
	source := fixedSource{messages: []Message{
		{
			UID: 1, From: "Accounts <accounts@example.test>",
			Subject:    "Your verification code",
			ReceivedAt: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
			Raw: []byte("From: accounts@example.test\r\n" +
				"Subject: Your verification code\r\n" +
				"Content-Type: text/plain\r\n\r\nVerification code: 482913\r\n"),
		},
		{
			UID: 2, From: "Security <security@example.test>",
			Subject:    "Confirm your agent",
			ReceivedAt: time.Date(2026, 7, 20, 12, 1, 0, 0, time.UTC),
			Raw: []byte("From: security@example.test\r\n" +
				"Subject: Confirm your agent\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				"Confirm: https://accounts.example.test/verify?token=top-secret\r\n"),
		},
	}}
	store, err := Open("agent@example.test", path, cipher, source)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.Sync(context.Background())
	if err != nil || added != 2 {
		t.Fatalf("sync = %d, %v", added, err)
	}
	metadata := store.List(10)
	encoded, _ := json.Marshal(metadata)
	if bytes.Contains(encoded, []byte("482913")) ||
		bytes.Contains(encoded, []byte("top-secret")) ||
		len(metadata) != 2 {
		t.Fatalf("metadata leaked or missed verification: %s", encoded)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(onDisk, []byte("482913")) ||
		bytes.Contains(onDisk, []byte("top-secret")) ||
		bytes.Contains(onDisk, []byte("Confirm your agent")) {
		t.Fatalf("encrypted state leaked plaintext: %q", onDisk)
	}
	restarted, err := Open("agent@example.test", path, cipher, source)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := restarted.List(10)
	if len(reloaded) != 2 {
		t.Fatalf("restart metadata = %+v", reloaded)
	}
	var codeID string
	for _, item := range reloaded {
		if item.Kind == "verification_code" {
			codeID = item.ID.String()
		}
	}
	if codeID == "" {
		t.Fatalf("verification code metadata missing: %+v", reloaded)
	}
	if strings.Contains(string(encoded), "https://") {
		t.Fatalf("metadata exposed raw confirmation URL: %s", encoded)
	}
}

func TestMailboxRefreshesLegacyCodeFromRicherConfirmationBody(t *testing.T) {
	key := bytes.Repeat([]byte{8}, vault.KeySize)
	cipher, err := vault.New(key)
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	path := t.TempDir() + "/mailbox/state.enc"
	codeMessage := Message{
		ID: "message-1", From: "accounts@example.test",
		ReceivedAt: time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC),
		Raw: []byte("From: accounts@example.test\r\nContent-Type: text/plain\r\n\r\n" +
			"Login code: 482913\r\n"),
	}
	store, err := Open(
		"agent@example.test", path, cipher, fixedSource{messages: []Message{codeMessage}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if added, syncErr := store.Sync(context.Background()); syncErr != nil || added != 1 {
		t.Fatalf("initial sync = %d, %v", added, syncErr)
	}
	legacy := store.List(10)
	if len(legacy) != 1 || legacy[0].Kind != "verification_code" {
		t.Fatalf("legacy verification = %+v", legacy)
	}
	richMessage := codeMessage
	richMessage.Raw = []byte("From: accounts@example.test\r\nContent-Type: text/plain\r\n\r\n" +
		"Login code: 482913\r\n" +
		"Sign in: https://supabase.example.test/auth/v1/verify?token=private\r\n")
	restarted, err := Open(
		"agent@example.test", path, cipher,
		refreshSource{messages: []Message{richMessage}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if added, syncErr := restarted.Sync(context.Background()); syncErr != nil || added != 0 {
		t.Fatalf("refresh sync = %d, %v", added, syncErr)
	}
	refreshed := restarted.List(10)
	if len(refreshed) != 1 || refreshed[0].ID != legacy[0].ID ||
		refreshed[0].Kind != "confirmation_link" ||
		refreshed[0].TargetDomain != "supabase.example.test" {
		t.Fatalf("refreshed verification = %+v", refreshed)
	}
}
