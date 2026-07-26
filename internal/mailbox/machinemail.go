package mailbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultMachineMailURL = "https://api.machinemail.org"

// MachineMailConfig configures the hosted machine-mail receive source.
type MachineMailConfig struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// MachineMailSource reads inbound messages from the machine-mail HTTPS API.
// It deliberately implements the same private verification Source boundary as
// IMAP: message bodies are consumed in-process and never returned by a
// model-visible tool.
type MachineMailSource struct {
	baseURL *url.URL
	apiKey  string
	client  *http.Client
}

// NewMachineMailSource validates configuration without making a network call.
func NewMachineMailSource(config MachineMailConfig) (*MachineMailSource, error) {
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		base = defaultMachineMailURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("mailbox: machine-mail URL is invalid")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("mailbox: machine-mail URL cannot contain a query or fragment")
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("mailbox: machine-mail URL must use HTTPS")
	}
	key := strings.TrimSpace(config.APIKey)
	if key == "" {
		return nil, errors.New("mailbox: machine-mail API key is required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return &MachineMailSource{baseURL: parsed, apiKey: key, client: client}, nil
}

// FetchRecent retrieves at most limit newest inbound messages without changing
// their read/archive state.
func (source *MachineMailSource) FetchRecent(
	ctx context.Context,
	limit int,
) ([]Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var listed struct {
		Messages []machineMailMessage `json:"messages"`
	}
	if err := source.get(ctx, "/v1/messages", url.Values{
		"direction": {"inbound"}, "limit": {strconv.Itoa(limit)},
	}, &listed); err != nil {
		return nil, err
	}
	result := make([]Message, 0, len(listed.Messages))
	for _, summary := range listed.Messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		full := summary
		if strings.TrimSpace(summary.ID) != "" && summary.body() == "" {
			decoded, err := source.fetchMessage(ctx, summary.ID)
			if err != nil {
				return nil, err
			}
			full = mergeMachineMailMessage(summary, decoded)
		}
		message, ok := full.toMessage()
		if ok {
			result = append(result, message)
		}
	}
	return result, nil
}

// FetchAfter consumes machine-mail's ordered event log. The returned cursor is
// safe to persist only after every referenced inbound message was fetched.
func (source *MachineMailSource) FetchAfter(
	ctx context.Context,
	after int64,
	limit int,
) ([]Message, int64, error) {
	if after < 0 {
		after = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var response struct {
		Events []machineMailEvent `json:"events"`
	}
	if err := source.get(ctx, "/v1/events", url.Values{
		"after_seq": {strconv.FormatInt(after, 10)},
		"limit":     {strconv.Itoa(limit)},
	}, &response); err != nil {
		return nil, after, err
	}
	next := after
	result := make([]Message, 0, len(response.Events))
	for _, event := range response.Events {
		if err := ctx.Err(); err != nil {
			return nil, after, err
		}
		if event.Seq > next {
			next = event.Seq
		}
		if event.Type != "message.received" {
			continue
		}
		messageID := event.messageID()
		if messageID == "" {
			return nil, after, errors.New("mailbox: machine-mail received event has no message ID")
		}
		message, err := source.fetchMessage(ctx, messageID)
		if err != nil {
			return nil, after, err
		}
		if converted, ok := message.toMessage(); ok {
			result = append(result, converted)
		}
	}
	return result, next, nil
}

func (source *MachineMailSource) fetchMessage(
	ctx context.Context,
	id string,
) (machineMailMessage, error) {
	var envelope json.RawMessage
	path := "/v1/messages/" + url.PathEscape(strings.TrimSpace(id))
	if err := source.get(ctx, path, nil, &envelope); err != nil {
		return machineMailMessage{}, err
	}
	decoded, err := decodeMachineMailMessage(envelope)
	if err != nil {
		return machineMailMessage{}, fmt.Errorf("mailbox: decode machine-mail message %q: %w", id, err)
	}
	return decoded, nil
}

func (source *MachineMailSource) get(
	ctx context.Context,
	path string,
	query url.Values,
	target any,
) error {
	endpoint := *source.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+source.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := source.client.Do(request)
	if err != nil {
		return fmt.Errorf("mailbox: call machine-mail: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRawMessage+1))
	if err != nil {
		return fmt.Errorf("mailbox: read machine-mail response: %w", err)
	}
	if len(body) > maxRawMessage {
		return errors.New("mailbox: machine-mail response exceeded limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("mailbox: machine-mail returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("mailbox: decode machine-mail response: %w", err)
	}
	return nil
}

type machineMailMessage struct {
	ID          string          `json:"id"`
	Direction   string          `json:"direction"`
	From        json.RawMessage `json:"from"`
	FromAddress string          `json:"from_address"`
	Sender      string          `json:"sender"`
	To          json.RawMessage `json:"to"`
	Subject     string          `json:"subject"`
	TextBody    string          `json:"text_body"`
	HTMLBody    string          `json:"html_body"`
	Body        string          `json:"body"`
	ReceivedAt  json.RawMessage `json:"received_at"`
	CreatedAt   json.RawMessage `json:"created_at"`
}

type machineMailEvent struct {
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	MessageID string          `json:"message_id"`
	Data      json.RawMessage `json:"data"`
	Payload   json.RawMessage `json:"payload"`
}

func (event machineMailEvent) messageID() string {
	if id := strings.TrimSpace(event.MessageID); id != "" {
		return id
	}
	for _, raw := range []json.RawMessage{event.Data, event.Payload} {
		var nested struct {
			MessageID string `json:"message_id"`
			Message   struct {
				ID string `json:"id"`
			} `json:"message"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &nested) == nil {
			if id := strings.TrimSpace(nested.MessageID); id != "" {
				return id
			}
			if id := strings.TrimSpace(nested.Message.ID); id != "" {
				return id
			}
		}
	}
	return ""
}

func decodeMachineMailMessage(raw json.RawMessage) (machineMailMessage, error) {
	var direct machineMailMessage
	if err := json.Unmarshal(raw, &direct); err != nil {
		return machineMailMessage{}, err
	}
	if direct.ID != "" {
		return direct, nil
	}
	var wrapped struct {
		Message machineMailMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return machineMailMessage{}, err
	}
	return wrapped.Message, nil
}

func mergeMachineMailMessage(summary, full machineMailMessage) machineMailMessage {
	if full.ID == "" {
		full.ID = summary.ID
	}
	if full.Subject == "" {
		full.Subject = summary.Subject
	}
	if len(full.From) == 0 {
		full.From = summary.From
	}
	if full.FromAddress == "" {
		full.FromAddress = summary.FromAddress
	}
	if full.Sender == "" {
		full.Sender = summary.Sender
	}
	if len(full.ReceivedAt) == 0 {
		full.ReceivedAt = summary.ReceivedAt
	}
	if len(full.CreatedAt) == 0 {
		full.CreatedAt = summary.CreatedAt
	}
	return full
}

func (message machineMailMessage) body() string {
	if strings.TrimSpace(message.TextBody) != "" {
		return message.TextBody
	}
	if strings.TrimSpace(message.Body) != "" {
		return message.Body
	}
	return message.HTMLBody
}

func (message machineMailMessage) verificationBody() string {
	parts := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	for _, candidate := range []string{message.TextBody, message.Body, message.HTMLBody} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		parts = append(parts, candidate)
	}
	return strings.Join(parts, "\n")
}

func (message machineMailMessage) toMessage() (Message, bool) {
	if strings.EqualFold(strings.TrimSpace(message.Direction), "outbound") {
		return Message{}, false
	}
	from := firstAddress(message.From)
	if from == "" {
		from = strings.TrimSpace(message.FromAddress)
	}
	if from == "" {
		from = strings.TrimSpace(message.Sender)
	}
	body := message.verificationBody()
	if strings.TrimSpace(message.ID) == "" || strings.TrimSpace(body) == "" {
		return Message{}, false
	}
	received := parseMachineMailTime(message.ReceivedAt)
	if received.IsZero() {
		received = parseMachineMailTime(message.CreatedAt)
	}
	var raw bytes.Buffer
	if parsed, err := mail.ParseAddress(from); err == nil {
		from = parsed.String()
	}
	fmt.Fprintf(&raw, "From: %s\r\n", sanitizeHeader(from))
	fmt.Fprintf(&raw, "Subject: %s\r\n", sanitizeHeader(message.Subject))
	if !received.IsZero() {
		fmt.Fprintf(&raw, "Date: %s\r\n", received.Format(time.RFC1123Z))
	}
	contentType := "text/plain; charset=utf-8"
	if message.TextBody == "" && message.Body == "" && message.HTMLBody != "" {
		contentType = "text/html; charset=utf-8"
	}
	fmt.Fprintf(&raw, "Content-Type: %s\r\n\r\n%s", contentType, body)
	if raw.Len() > maxRawMessage {
		return Message{}, false
	}
	return Message{
		ID: message.ID, From: from, Subject: message.Subject,
		ReceivedAt: received, Raw: raw.Bytes(),
	}, true
}

func firstAddress(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var object struct {
		Address string `json:"address"`
		Email   string `json:"email"`
		Name    string `json:"name"`
	}
	if json.Unmarshal(raw, &object) == nil {
		address := object.Address
		if address == "" {
			address = object.Email
		}
		if address != "" && object.Name != "" {
			return (&mail.Address{Name: object.Name, Address: address}).String()
		}
		return strings.TrimSpace(address)
	}
	return ""
}

func parseMachineMailTime(raw json.RawMessage) time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var milliseconds int64
	if json.Unmarshal(raw, &milliseconds) == nil {
		return time.UnixMilli(milliseconds).UTC()
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.RFC1123Z} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return net.ParseIP(host).IsLoopback()
}
