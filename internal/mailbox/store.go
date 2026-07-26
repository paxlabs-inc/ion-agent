// Package mailbox provides an encrypted, receive-only mailbox boundary for
// agent verification workflows. Raw messages and verification secrets never
// enter model-visible tool results.
package mailbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
)

const (
	maxRawMessage = 2 << 20
	maxRecords    = 500
)

var (
	codePattern = regexp.MustCompile(
		`(?i)(?:verification|confirmation|security|sign[ -]?in|login|one[ -]?time|otp|code)[^0-9]{0,40}([0-9]{4,8})`,
	)
	urlPattern = regexp.MustCompile(`https://[^\s<>"']+`)
)

// Message is the bounded transport-neutral source representation.
type Message struct {
	// ID is the transport-stable message identifier. Sources that only expose a
	// numeric UID may leave it empty and populate UID for compatibility.
	ID         string
	UID        uint32
	From       string
	Subject    string
	ReceivedAt time.Time
	Raw        []byte
}

// Source retrieves a bounded recent mailbox window.
type Source interface {
	FetchRecent(context.Context, int) ([]Message, error)
}

// CursorSource supplies an ordered durable transport cursor. The Store keeps
// that cursor inside its Vault-encrypted state and advances it atomically with
// imported verifications.
type CursorSource interface {
	FetchAfter(context.Context, int64, int) ([]Message, int64, error)
}

// Verification is encrypted at rest. Value is never JSON serialized through
// model-visible list operations.
type Verification struct {
	ID           uuid.UUID  `json:"id"`
	MessageID    string     `json:"message_id,omitempty"`
	MessageUID   uint32     `json:"message_uid"`
	From         string     `json:"from"`
	Subject      string     `json:"subject"`
	ReceivedAt   time.Time  `json:"received_at"`
	Kind         string     `json:"kind"`
	TargetDomain string     `json:"target_domain,omitempty"`
	Value        string     `json:"value"`
	ConsumedAt   *time.Time `json:"consumed_at,omitempty"`
}

// Metadata is the redacted model-visible view of a verification message.
type Metadata struct {
	ID           uuid.UUID `json:"id"`
	FromDomain   string    `json:"from_domain"`
	Subject      string    `json:"subject"`
	ReceivedAt   time.Time `json:"received_at"`
	Kind         string    `json:"kind"`
	TargetDomain string    `json:"target_domain,omitempty"`
}

type state struct {
	Records                  []Verification `json:"records"`
	SourceCursor             int64          `json:"source_cursor,omitempty"`
	AlternativeBodiesScanned bool           `json:"alternative_bodies_scanned,omitempty"`
}

// Store owns encrypted verification state for one dedicated address.
type Store struct {
	address string
	path    string
	vault   *vault.Vault
	source  Source
	clock   func() time.Time

	mu    sync.Mutex
	state state
}

// Open loads the encrypted receive-only store.
func Open(
	address string,
	path string,
	cipher *vault.Vault,
	source Source,
) (*Store, error) {
	address = strings.TrimSpace(address)
	if address == "" || !strings.Contains(address, "@") {
		return nil, errors.New("mailbox: a dedicated agent email address is required")
	}
	if cipher == nil || source == nil {
		return nil, errors.New("mailbox: vault and receive source are required")
	}
	path, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || path == "." {
		return nil, errors.New("mailbox: encrypted state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	store := &Store{
		address: address, path: path, vault: cipher, source: source,
		clock: func() time.Time { return time.Now().UTC() },
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// Address returns the non-secret dedicated address.
func (store *Store) Address() string {
	if store == nil {
		return ""
	}
	return store.address
}

// Sync imports new verification messages and persists them encrypted.
func (store *Store) Sync(ctx context.Context) (int, error) {
	store.mu.Lock()
	cursor := store.state.SourceCursor
	refreshAlternatives := !store.state.AlternativeBodiesScanned
	if refreshAlternatives {
		refreshAlternatives = false
		for _, record := range store.state.Records {
			if record.Kind == "verification_code" && record.ConsumedAt == nil {
				refreshAlternatives = true
				break
			}
		}
	}
	store.mu.Unlock()
	messages, nextCursor, err := store.fetch(ctx, cursor)
	if err != nil {
		return 0, err
	}
	alternativesScanned := false
	if refreshAlternatives {
		if recent, refreshErr := store.source.FetchRecent(ctx, 50); refreshErr == nil {
			messages = append(messages, recent...)
			alternativesScanned = true
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	seen := make(map[string]int, len(store.state.Records))
	for index, record := range store.state.Records {
		seen[verificationSourceID(record.MessageID, record.MessageUID)] = index
	}
	added := 0
	updated := false
	for _, message := range messages {
		sourceID := verificationSourceID(message.ID, message.UID)
		if len(message.Raw) > maxRawMessage {
			continue
		}
		record, found := extractVerification(message)
		if !found {
			continue
		}
		if index, exists := seen[sourceID]; exists {
			existing := store.state.Records[index]
			if existing.ConsumedAt == nil && existing.Kind == "verification_code" &&
				record.Kind == "confirmation_link" {
				record.ID = existing.ID
				store.state.Records[index] = record
				updated = true
			}
			continue
		}
		store.state.Records = append(store.state.Records, record)
		seen[sourceID] = len(store.state.Records) - 1
		added++
	}
	if alternativesScanned {
		store.state.AlternativeBodiesScanned = true
		updated = true
	}
	sort.Slice(store.state.Records, func(left, right int) bool {
		return store.state.Records[left].ReceivedAt.Before(
			store.state.Records[right].ReceivedAt,
		)
	})
	if len(store.state.Records) > maxRecords {
		store.state.Records = append(
			[]Verification(nil),
			store.state.Records[len(store.state.Records)-maxRecords:]...,
		)
	}
	advanced := nextCursor > store.state.SourceCursor
	if advanced {
		store.state.SourceCursor = nextCursor
	}
	if added > 0 || updated || advanced {
		if err := store.persistLocked(); err != nil {
			return 0, err
		}
	}
	return added, nil
}

func (store *Store) fetch(
	ctx context.Context,
	cursor int64,
) ([]Message, int64, error) {
	if source, ok := store.source.(CursorSource); ok {
		return source.FetchAfter(ctx, cursor, 100)
	}
	messages, err := store.source.FetchRecent(ctx, 50)
	return messages, cursor, err
}

// List returns redacted pending verification metadata newest first.
func (store *Store) List(limit int) []Metadata {
	store.mu.Lock()
	defer store.mu.Unlock()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	result := make([]Metadata, 0, limit)
	for index := len(store.state.Records) - 1; index >= 0 && len(result) < limit; index-- {
		record := store.state.Records[index]
		if record.ConsumedAt != nil {
			continue
		}
		result = append(result, Metadata{
			ID: record.ID, FromDomain: addressDomain(record.From),
			Subject: truncate(record.Subject, 240), ReceivedAt: record.ReceivedAt,
			Kind: record.Kind, TargetDomain: record.TargetDomain,
		})
	}
	return result
}

// Peek returns one secret for an in-process browser handoff.
func (store *Store) Peek(id uuid.UUID) (Verification, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, record := range store.state.Records {
		if record.ID == id {
			if record.ConsumedAt != nil {
				return Verification{}, errors.New("mailbox: verification was already consumed")
			}
			return record, nil
		}
	}
	return Verification{}, errors.New("mailbox: verification was not found")
}

// MarkConsumed durably retires a verification after successful browser use.
func (store *Store) MarkConsumed(id uuid.UUID) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.state.Records {
		if store.state.Records[index].ID != id {
			continue
		}
		if store.state.Records[index].ConsumedAt != nil {
			return nil
		}
		now := store.clock()
		store.state.Records[index].ConsumedAt = &now
		return store.persistLocked()
	}
	return errors.New("mailbox: verification was not found")
}

func (store *Store) load() error {
	envelope, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	plaintext, err := store.vault.Decrypt(envelope)
	if err != nil {
		return fmt.Errorf("mailbox: decrypt state: %w", err)
	}
	defer zero(plaintext)
	if err := json.Unmarshal(plaintext, &store.state); err != nil {
		return fmt.Errorf("mailbox: decode state: %w", err)
	}
	return nil
}

func (store *Store) persistLocked() error {
	plaintext, err := json.Marshal(store.state)
	if err != nil {
		return err
	}
	defer zero(plaintext)
	envelope, err := store.vault.Encrypt(plaintext)
	if err != nil {
		return err
	}
	defer zero(envelope)
	temporary := store.path + ".tmp"
	if err := os.WriteFile(temporary, envelope, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, store.path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func extractVerification(message Message) (Verification, bool) {
	body := html.UnescapeString(readableBody(message.Raw))
	if body == "" {
		return Verification{}, false
	}
	record := Verification{
		ID: uuid.NewSHA1(
			uuid.NameSpaceOID,
			[]byte(fmt.Sprintf("%s:%x", verificationSourceID(message.ID, message.UID), sha256.Sum256(message.Raw))),
		),
		MessageID: message.ID, MessageUID: message.UID, From: message.From,
		Subject: message.Subject, ReceivedAt: message.ReceivedAt,
	}
	for _, candidate := range urlPattern.FindAllString(body, 20) {
		candidate = strings.TrimRight(candidate, ".,);]")
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" ||
			parsed.User != nil {
			continue
		}
		lower := strings.ToLower(parsed.Path + "?" + parsed.RawQuery)
		if !strings.Contains(lower, "verify") &&
			!strings.Contains(lower, "confirm") &&
			!strings.Contains(lower, "activate") &&
			!strings.Contains(lower, "magic") &&
			!strings.Contains(lower, "token") &&
			!strings.Contains(lower, "reset") &&
			!strings.Contains(lower, "recover") &&
			!strings.Contains(lower, "invite") &&
			!strings.Contains(lower, "sign-in") &&
			!strings.Contains(lower, "signin") {
			continue
		}
		record.Kind = "confirmation_link"
		record.Value = candidate
		record.TargetDomain = strings.ToLower(parsed.Hostname())
		return record, true
	}
	match := codePattern.FindStringSubmatch(body)
	if len(match) == 2 {
		record.Kind = "verification_code"
		record.Value = match[1]
		return record, true
	}
	return Verification{}, false
}

func verificationSourceID(id string, uid uint32) string {
	if id = strings.TrimSpace(id); id != "" {
		return "id:" + id
	}
	return fmt.Sprintf("uid:%d", uid)
}

func readableBody(raw []byte) string {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return string(raw)
	}
	contentType := message.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(message.Body, params["boundary"])
		var parts []string
		for len(parts) < 20 {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				break
			}
			payload, _ := io.ReadAll(io.LimitReader(
				decodeTransfer(part.Header, part), maxRawMessage,
			))
			partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if partType == "text/plain" || partType == "text/html" || partType == "" {
				parts = append(parts, string(payload))
			}
		}
		return strings.Join(parts, "\n")
	}
	payload, _ := io.ReadAll(io.LimitReader(
		decodeTransfer(textproto.MIMEHeader(message.Header), message.Body),
		maxRawMessage,
	))
	return string(payload)
}

func decodeTransfer(header textproto.MIMEHeader, body io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "quoted-printable":
		return quotedprintable.NewReader(body)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, body)
	default:
		return body
	}
}

func addressDomain(raw string) string {
	if parsed, err := mail.ParseAddress(raw); err == nil {
		raw = parsed.Address
	}
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(raw[at+1:], " >"))
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

// StateDigest is used by acceptance tests to prove plaintext secrets are not
// stored on disk.
func (store *Store) StateDigest() string {
	payload, _ := os.ReadFile(store.path)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
