package operatorapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
	"github.com/paxlabs-inc/ion-agent/internal/privatecomputer"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	privateDesktopTicketTTL  = 30 * time.Second
	maxPrivateDesktopTickets = 256
)

type privateDesktopHost struct {
	baseURL string
	authKey string
	client  *http.Client
}

func newPrivateDesktopHost(
	rawURL string,
	authKey string,
	client *http.Client,
) (*privateDesktopHost, error) {
	rawURL = strings.TrimSpace(rawURL)
	authKey = strings.TrimSpace(authKey)
	if rawURL == "" && authKey == "" {
		return nil, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		strings.TrimSpace(parsed.Host) == "" ||
		len(authKey) < 32 || len(authKey) > 4096 {
		return nil, fmt.Errorf("operator private desktop: valid host URL and auth key are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	return &privateDesktopHost{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		authKey: authKey,
		client:  client,
	}, nil
}

func (host *privateDesktopHost) state(
	ctx context.Context,
) (json.RawMessage, int, error) {
	if host == nil {
		return nil, http.StatusServiceUnavailable, nil
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, host.baseURL+"/v1/state", nil,
	)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+host.authKey)
	response, err := host.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	return payload, response.StatusCode, nil
}

func (host *privateDesktopHost) frame(
	ctx context.Context,
) ([]byte, http.Header, int, error) {
	if host == nil {
		return nil, nil, http.StatusServiceUnavailable, nil
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, host.baseURL+"/v1/desktop/frame", nil,
	)
	if err != nil {
		return nil, nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+host.authKey)
	response, err := host.client.Do(request)
	if err != nil {
		return nil, nil, 0, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, nil, 0, err
	}
	return payload, response.Header.Clone(), response.StatusCode, nil
}

func (host *privateDesktopHost) input(
	ctx context.Context,
	leaseID uuid.UUID,
	input privatecomputer.DesktopInput,
) (json.RawMessage, int, error) {
	if host == nil {
		return nil, http.StatusServiceUnavailable, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		host.baseURL+"/v1/desktop/input",
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+host.authKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Ion-Control-Lease", leaseID.String())
	request.Header.Set("X-Ion-Input-ID", uuid.NewString())
	response, err := host.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	return result, response.StatusCode, nil
}

func (host *privateDesktopHost) observe(
	ctx context.Context,
	input privatecomputer.DesktopObservationRequest,
) (json.RawMessage, int, error) {
	if host == nil {
		return nil, http.StatusServiceUnavailable, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		host.baseURL+"/v1/desktop/observe",
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+host.authKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := host.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	return result, response.StatusCode, nil
}

func (host *privateDesktopHost) windowInput(
	ctx context.Context,
	leaseID uuid.UUID,
	input privatecomputer.DesktopWindowInput,
) (json.RawMessage, int, error) {
	if host == nil {
		return nil, http.StatusServiceUnavailable, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		host.baseURL+"/v1/desktop/window-input",
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+host.authKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Ion-Control-Lease", leaseID.String())
	request.Header.Set("X-Ion-Input-ID", uuid.NewString())
	response, err := host.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	return result, response.StatusCode, nil
}

type privateDesktopTicketKind string

const (
	privateDesktopViewTicket  privateDesktopTicketKind = "view"
	privateDesktopInputTicket privateDesktopTicketKind = "input"
)

type privateDesktopTicketRecord struct {
	ActorID       uuid.UUID
	SessionID     uuid.UUID
	Kind          privateDesktopTicketKind
	LeaseID       uuid.UUID
	LeaseRevision uint64
	ExpiresAt     time.Time
}

type privateDesktopTicketStore struct {
	clock types.Clock
	mu    sync.Mutex
	items map[[32]byte]privateDesktopTicketRecord
}

func newPrivateDesktopTicketStore(clock types.Clock) *privateDesktopTicketStore {
	return &privateDesktopTicketStore{
		clock: clock,
		items: make(map[[32]byte]privateDesktopTicketRecord),
	}
}

func (store *privateDesktopTicketStore) issue(
	record privateDesktopTicketRecord,
) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.expire()
	if len(store.items) >= maxPrivateDesktopTickets {
		return "", fmt.Errorf("private desktop ticket capacity reached")
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	record.ExpiresAt = store.clock.Now().UTC().Add(privateDesktopTicketTTL)
	store.items[sha256.Sum256([]byte(token))] = record
	return token, nil
}

func (store *privateDesktopTicketStore) validate(
	token string,
	kind privateDesktopTicketKind,
	actorID uuid.UUID,
) (privateDesktopTicketRecord, error) {
	if len(token) < 32 || len(token) > 256 {
		return privateDesktopTicketRecord{}, controlplane.ErrUnauthorized
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.expire()
	record, exists := store.items[sha256.Sum256([]byte(token))]
	if !exists || record.Kind != kind || record.ActorID != actorID ||
		!store.clock.Now().Before(record.ExpiresAt) {
		return privateDesktopTicketRecord{}, controlplane.ErrUnauthorized
	}
	return record, nil
}

func (store *privateDesktopTicketStore) expire() {
	now := store.clock.Now()
	for digest, record := range store.items {
		if !now.Before(record.ExpiresAt) {
			delete(store.items, digest)
		}
	}
}

type privateDesktopHandler struct {
	host          *privateDesktopHost
	authenticator controlplane.BrowserAuthenticator
	control       *controllease.Service
	tickets       *privateDesktopTicketStore
	origin        string
}

func newPrivateDesktopHandler(
	host *privateDesktopHost,
	authenticator controlplane.BrowserAuthenticator,
	control *controllease.Service,
	clock types.Clock,
	origin string,
) (*privateDesktopHandler, error) {
	if authenticator == nil || control == nil || clock == nil ||
		strings.TrimSpace(origin) == "" {
		return nil, fmt.Errorf("operator private desktop: transport dependencies are required")
	}
	return &privateDesktopHandler{
		host:          host,
		authenticator: authenticator,
		control:       control,
		tickets:       newPrivateDesktopTicketStore(clock),
		origin:        strings.TrimRight(origin, "/"),
	}, nil
}

func (handler *privateDesktopHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	actor, err := handler.authenticator.Authenticate(request)
	if err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch request.URL.Path {
	case "/v1/computer/status":
		handler.serveStatus(writer, request)
	case "/v1/computer/ticket":
		handler.serveTicket(writer, request, actor)
	case "/v1/computer/frame":
		handler.serveFrame(writer, request, actor)
	case "/v1/computer/input":
		handler.serveInput(writer, request, actor)
	default:
		http.NotFound(writer, request)
	}
}

func (handler *privateDesktopHandler) serveStatus(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	payload, status, err := handler.host.state(ctx)
	if err != nil || status == 0 {
		writePrivateDesktopJSON(writer, http.StatusServiceUnavailable, map[string]any{
			"state":  "unavailable",
			"reason": "private computer host could not be reached",
		})
		return
	}
	if status != http.StatusOK {
		writePrivateDesktopJSON(writer, status, map[string]any{
			"state":  "unavailable",
			"reason": "private computer host is not ready",
		})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(payload)
}

func (handler *privateDesktopHandler) serveTicket(
	writer http.ResponseWriter,
	request *http.Request,
	actor controlplane.BrowserActor,
) {
	if request.Method != http.MethodPost ||
		request.Header.Get("Origin") != handler.origin ||
		handler.authenticator.ValidateCSRF(request, actor) != nil {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Kind          privateDesktopTicketKind `json:"kind"`
		SessionID     uuid.UUID                `json:"session_id"`
		LeaseID       uuid.UUID                `json:"lease_id,omitempty"`
		LeaseRevision uint64                   `json:"lease_revision,omitempty"`
	}
	if decoder.Decode(&input) != nil ||
		input.SessionID == uuid.Nil ||
		(input.Kind != privateDesktopViewTicket &&
			input.Kind != privateDesktopInputTicket) {
		http.Error(writer, "invalid ticket request", http.StatusBadRequest)
		return
	}
	record := privateDesktopTicketRecord{
		ActorID:   actor.ActorID,
		SessionID: input.SessionID,
		Kind:      input.Kind,
	}
	if input.Kind == privateDesktopInputTicket {
		target := controllease.Target{
			ActorID:    actor.ActorID,
			SessionID:  &input.SessionID,
			Kind:       controllease.ResourceDesktop,
			ResourceID: input.SessionID.String(),
		}
		lease, err := handler.control.Status(request.Context(), target)
		if err != nil || lease.State != controllease.StateActive ||
			lease.Authority != controllease.AuthorityOperator ||
			lease.ID != input.LeaseID ||
			lease.Revision != input.LeaseRevision {
			http.Error(writer, "desktop control lease is not active", http.StatusConflict)
			return
		}
		record.LeaseID = lease.ID
		record.LeaseRevision = lease.Revision
	}
	token, err := handler.tickets.issue(record)
	if err != nil {
		http.Error(writer, "ticket service unavailable", http.StatusServiceUnavailable)
		return
	}
	writePrivateDesktopJSON(writer, http.StatusCreated, map[string]any{
		"ticket":     token,
		"kind":       input.Kind,
		"expires_at": handler.clockNow().Add(privateDesktopTicketTTL),
	})
}

func (handler *privateDesktopHandler) serveFrame(
	writer http.ResponseWriter,
	request *http.Request,
	actor controlplane.BrowserActor,
) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if origin := request.Header.Get("Origin"); origin != "" && origin != handler.origin {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	if _, err := handler.tickets.validate(
		request.URL.Query().Get("ticket"),
		privateDesktopViewTicket,
		actor.ActorID,
	); err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 12*time.Second)
	defer cancel()
	payload, headers, status, err := handler.host.frame(ctx)
	if err != nil || status == 0 {
		http.Error(writer, "desktop frame unavailable", http.StatusServiceUnavailable)
		return
	}
	if status != http.StatusOK {
		http.Error(writer, "desktop frame unavailable", status)
		return
	}
	for _, name := range []string{
		"Content-Type",
		"X-Ion-Frame-Sequence",
		"X-Ion-Frame-Digest",
		"X-Ion-Frame-Width",
		"X-Ion-Frame-Height",
		"X-Ion-Frame-Captured-At",
	} {
		if value := headers.Get(name); value != "" {
			writer.Header().Set(name, value)
		}
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(payload)
}

func (handler *privateDesktopHandler) serveInput(
	writer http.ResponseWriter,
	request *http.Request,
	actor controlplane.BrowserActor,
) {
	if request.Method != http.MethodPost ||
		request.Header.Get("Origin") != handler.origin ||
		handler.authenticator.ValidateCSRF(request, actor) != nil {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	record, err := handler.tickets.validate(
		request.URL.Query().Get("ticket"),
		privateDesktopInputTicket,
		actor.ActorID,
	)
	if err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	target := controllease.Target{
		ActorID:    actor.ActorID,
		SessionID:  &record.SessionID,
		Kind:       controllease.ResourceDesktop,
		ResourceID: record.SessionID.String(),
	}
	lease, err := handler.control.Status(request.Context(), target)
	if err != nil || lease.State != controllease.StateActive ||
		lease.Authority != controllease.AuthorityOperator ||
		lease.ID != record.LeaseID || lease.Revision != record.LeaseRevision {
		http.Error(writer, "desktop control lease changed", http.StatusConflict)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input privatecomputer.DesktopInput
	if decoder.Decode(&input) != nil {
		http.Error(writer, "invalid desktop input", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 6*time.Second)
	defer cancel()
	payload, status, err := handler.host.input(ctx, lease.ID, input)
	if err != nil || status == 0 {
		http.Error(writer, "desktop input unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}

func (handler *privateDesktopHandler) clockNow() time.Time {
	return handler.tickets.clock.Now().UTC()
}

func writePrivateDesktopJSON(
	writer http.ResponseWriter,
	status int,
	value any,
) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
