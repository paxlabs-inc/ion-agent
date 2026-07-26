package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

const (
	defaultBrowserConnections = 32
	maximumBrowserConnections = 256
)

// BrowserServerConfig bounds the HTTPS and WebSocket transport.
type BrowserServerConfig struct {
	AllowedOrigins    []string
	MaxConnections    int
	MaxPayloadBytes   int64
	RequestsPerMinute int
	WriteTimeout      time.Duration
	PongTimeout       time.Duration
	PingInterval      time.Duration
	ReplayLimit       int
}

// BrowserServer exposes authenticated RPC, ticket, and event endpoints.
type BrowserServer struct {
	dispatcher *Dispatcher
	journal    *Journal
	auth       BrowserAuthenticator
	tickets    *TicketManager
	clock      types.Clock

	allowedOrigins  map[string]struct{}
	connections     chan struct{}
	maxPayloadBytes int64
	writeTimeout    time.Duration
	pongTimeout     time.Duration
	pingInterval    time.Duration
	replayLimit     int
	limiter         *actorRateLimiter
	upgrader        websocket.Upgrader
}

// NewBrowserServer constructs the fail-closed browser transport.
func NewBrowserServer(
	dispatcher *Dispatcher,
	journal *Journal,
	auth BrowserAuthenticator,
	tickets *TicketManager,
	clock types.Clock,
	config BrowserServerConfig,
) (*BrowserServer, error) {
	if dispatcher == nil || journal == nil || auth == nil || tickets == nil || clock == nil {
		return nil, fmt.Errorf("controlplane: browser dependencies are required")
	}
	if len(config.AllowedOrigins) == 0 {
		return nil, fmt.Errorf("controlplane: browser origin allowlist is required")
	}
	origins := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, raw := range config.AllowedOrigins {
		normalized, err := normalizeBrowserOrigin(raw)
		if err != nil {
			return nil, err
		}
		origins[normalized] = struct{}{}
	}
	if config.MaxConnections <= 0 {
		config.MaxConnections = defaultBrowserConnections
	}
	if config.MaxConnections > maximumBrowserConnections {
		return nil, fmt.Errorf("controlplane: browser connection limit exceeds %d",
			maximumBrowserConnections)
	}
	if config.MaxPayloadBytes <= 0 {
		config.MaxPayloadBytes = MaximumPayloadBytes
	}
	if config.MaxPayloadBytes > MaximumPayloadBytes {
		return nil, fmt.Errorf("controlplane: browser payload bound exceeds protocol maximum")
	}
	if config.RequestsPerMinute <= 0 {
		config.RequestsPerMinute = 120
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 10 * time.Second
	}
	if config.PongTimeout <= 0 {
		config.PongTimeout = 60 * time.Second
	}
	if config.PingInterval <= 0 {
		config.PingInterval = 25 * time.Second
	}
	if config.PingInterval >= config.PongTimeout {
		return nil, fmt.Errorf("controlplane: ping interval must be below pong timeout")
	}
	if config.ReplayLimit <= 0 || config.ReplayLimit > maxReplayEvents {
		config.ReplayLimit = maxReplayEvents
	}
	server := &BrowserServer{
		dispatcher: dispatcher, journal: journal, auth: auth, tickets: tickets,
		clock: clock, allowedOrigins: origins,
		connections:     make(chan struct{}, config.MaxConnections),
		maxPayloadBytes: config.MaxPayloadBytes,
		writeTimeout:    config.WriteTimeout, pongTimeout: config.PongTimeout,
		pingInterval: config.PingInterval, replayLimit: config.ReplayLimit,
		limiter: newActorRateLimiter(clock, config.RequestsPerMinute),
	}
	server.upgrader = websocket.Upgrader{
		HandshakeTimeout: config.WriteTimeout,
		CheckOrigin:      server.originAllowed,
	}
	return server, nil
}

// ServeHTTP routes the fixed v1 browser API.
func (server *BrowserServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setBrowserSecurityHeaders(writer.Header())
	if !secureBrowserTransport(request) {
		http.Error(writer, "TLS required", http.StatusUpgradeRequired)
		return
	}
	switch request.URL.Path {
	case "/v1/rpc":
		server.serveRPC(writer, request)
	case "/v1/ws-ticket":
		server.serveTicket(writer, request)
	case "/v1/events":
		server.serveEvents(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (server *BrowserServer) serveRPC(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, ok := server.authenticate(writer, request)
	if !ok {
		return
	}
	if !server.limiter.Allow(actor.ActorID) {
		writeProtocolHTTPError(writer, http.StatusTooManyRequests,
			protocolError(ErrorRateLimited, "request rate exceeded", true))
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, server.maxPayloadBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var rpcRequest Request
	if err := decoder.Decode(&rpcRequest); err != nil || !atJSONEnd(decoder) {
		writeProtocolHTTPError(writer, http.StatusBadRequest,
			protocolError(ErrorInvalid, "invalid request body", false))
		return
	}
	if rpcRequest.Kind == KindCommand {
		if !server.originAllowed(request) ||
			server.auth.ValidateCSRF(request, actor) != nil {
			writeProtocolHTTPError(writer, http.StatusForbidden,
				protocolError(ErrorForbidden, "mutation proof rejected", false))
			return
		}
	}
	response := server.dispatcher.Dispatch(request.Context(), actor.ActorID, rpcRequest)
	status := statusForResponse(response)
	writeJSON(writer, status, response)
}

func (server *BrowserServer) serveTicket(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	actor, ok := server.authenticate(writer, request)
	if !ok {
		return
	}
	if !server.originAllowed(request) ||
		server.auth.ValidateCSRF(request, actor) != nil {
		writeProtocolHTTPError(writer, http.StatusForbidden,
			protocolError(ErrorForbidden, "ticket proof rejected", false))
		return
	}
	if !server.limiter.Allow(actor.ActorID) {
		writeProtocolHTTPError(writer, http.StatusTooManyRequests,
			protocolError(ErrorRateLimited, "request rate exceeded", true))
		return
	}
	ticket, err := server.tickets.Issue(actor.ActorID)
	if err != nil {
		writeProtocolHTTPError(writer, http.StatusServiceUnavailable,
			protocolError(ErrorUnavailable, "ticket service unavailable", true))
		return
	}
	writeJSON(writer, http.StatusCreated, ticket)
}

func (server *BrowserServer) serveEvents(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !server.originAllowed(request) {
		http.Error(writer, "forbidden origin", http.StatusForbidden)
		return
	}
	actorID, err := server.tickets.Consume(request.URL.Query().Get("ticket"))
	if err != nil {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	after, err := parseCursor(request.URL.Query().Get("after"))
	if err != nil {
		http.Error(writer, "invalid cursor", http.StatusBadRequest)
		return
	}
	select {
	case server.connections <- struct{}{}:
		defer func() { <-server.connections }()
	default:
		http.Error(writer, "connection limit reached", http.StatusServiceUnavailable)
		return
	}
	subscription, err := server.journal.SubscribeActor(
		request.Context(), actorID, after, server.replayLimit,
	)
	if err != nil {
		http.Error(writer, "event journal unavailable", http.StatusServiceUnavailable)
		return
	}
	defer subscription.Close()
	connection, err := server.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	server.stream(request, connection, actorID, subscription)
}

func (server *BrowserServer) stream(
	request *http.Request,
	connection *websocket.Conn,
	actorID uuid.UUID,
	subscription *Subscription,
) {
	connection.SetReadLimit(4096)
	_ = connection.SetReadDeadline(time.Now().Add(server.pongTimeout))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(server.pongTimeout))
	})
	recovery, err := server.dispatcher.Recover(
		request.Context(),
		Scope{ActorID: actorID},
		subscription.Replay,
	)
	if err != nil {
		_ = server.writeStreamFrame(connection, StreamFrame{
			Type: "error", Error: protocolError(ErrorUnavailable, "snapshot unavailable", true),
		})
		return
	}
	if err := server.writeStreamFrame(connection, StreamFrame{
		Type: "recovery", Recovery: &recovery,
	}); err != nil {
		return
	}
	var maximumSent atomic.Uint64
	maximumSent.Store(recovery.Replay.Latest)
	for recovery.Replay.Latest < recovery.Replay.Head {
		page, pageErr := server.journal.replayActorThrough(
			request.Context(), actorID, recovery.Replay.Latest,
			server.replayLimit, recovery.Replay.Head,
		)
		if pageErr != nil || page.Gap || page.Latest <= recovery.Replay.Latest {
			_ = server.writeStreamFrame(connection, StreamFrame{
				Type: "error", Error: protocolError(
					ErrorUnavailable, "event catch-up unavailable", true,
				),
			})
			return
		}
		// Publish the acknowledgement ceiling before the page is readable by a
		// fast client. Later recovery pages deliberately omit the snapshot so the
		// client folds them into, rather than replaces, recovered state.
		maximumSent.Store(page.Latest)
		if err := server.writeStreamFrame(connection, StreamFrame{
			Type: "recovery", Recovery: &Recovery{Replay: page},
		}); err != nil {
			return
		}
		recovery.Replay = page
	}
	readerDone := make(chan error, 1)
	go server.readAcknowledgements(
		request, connection, actorID, &maximumSent, readerDone,
	)
	ticker := time.NewTicker(server.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-readerDone:
			return
		case event, open := <-subscription.Live:
			if !open {
				return
			}
			// Publish the acknowledgement ceiling before the frame becomes
			// readable by a fast client. Storing it after WriteJSON creates a
			// race where an immediate acknowledgement is rejected.
			maximumSent.Store(event.Sequence)
			if err := server.writeStreamFrame(connection, StreamFrame{
				Type: "event", Event: &event,
			}); err != nil {
				return
			}
		case <-ticker.C:
			_ = connection.SetWriteDeadline(time.Now().Add(server.writeTimeout))
			if err := connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (server *BrowserServer) readAcknowledgements(
	request *http.Request,
	connection *websocket.Conn,
	actorID uuid.UUID,
	maximumSent *atomic.Uint64,
	done chan<- error,
) {
	for {
		_, payload, err := connection.ReadMessage()
		if err != nil {
			done <- err
			return
		}
		if len(payload) > 4096 {
			done <- fmt.Errorf("controlplane: oversized acknowledgement")
			return
		}
		frame, err := decodeClientStreamFrame(payload)
		if err != nil || frame.Type != "ack" || frame.ClientID == "" ||
			frame.Sequence > maximumSent.Load() {
			done <- fmt.Errorf("controlplane: invalid acknowledgement")
			return
		}
		if err := server.journal.Acknowledge(
			request.Context(), actorID, frame.ClientID, frame.Sequence,
		); err != nil {
			done <- err
			return
		}
	}
}

func (server *BrowserServer) writeStreamFrame(
	connection *websocket.Conn,
	frame StreamFrame,
) error {
	if err := connection.SetWriteDeadline(time.Now().Add(server.writeTimeout)); err != nil {
		return err
	}
	return connection.WriteJSON(frame)
}

func (server *BrowserServer) authenticate(
	writer http.ResponseWriter,
	request *http.Request,
) (BrowserActor, bool) {
	actor, err := server.auth.Authenticate(request)
	if err != nil {
		writer.Header().Set("WWW-Authenticate", `Cookie realm="ion"`)
		writeProtocolHTTPError(writer, http.StatusUnauthorized,
			protocolError(ErrorUnauthorized, "authentication required", false))
		return BrowserActor{}, false
	}
	return actor, true
}

func (server *BrowserServer) originAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	normalized, err := normalizeBrowserOrigin(origin)
	if err != nil {
		return false
	}
	_, allowed := server.allowedOrigins[normalized]
	return allowed
}

func normalizeBrowserOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("controlplane: invalid browser origin")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("controlplane: invalid browser origin scheme")
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), nil
}

func secureBrowserTransport(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func parseCursor(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func atJSONEnd(decoder *json.Decoder) bool {
	var extra json.RawMessage
	return errors.Is(decoder.Decode(&extra), io.EOF)
}

func setBrowserSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}

func writeProtocolHTTPError(writer http.ResponseWriter, status int, protocolErr *ProtocolError) {
	writeJSON(writer, status, map[string]any{"error": protocolErr})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		// The response is already committed; the transport will observe truncation.
		return
	}
}

func statusForResponse(response Response) int {
	if response.Error == nil {
		return http.StatusOK
	}
	switch response.Error.Code {
	case ErrorInvalid:
		return http.StatusBadRequest
	case ErrorUnauthorized:
		return http.StatusUnauthorized
	case ErrorForbidden:
		return http.StatusForbidden
	case ErrorNotFound:
		return http.StatusNotFound
	case ErrorConflict:
		return http.StatusConflict
	case ErrorRateLimited:
		return http.StatusTooManyRequests
	case ErrorUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

type rateWindow struct {
	start time.Time
	count int
}

type actorRateLimiter struct {
	clock   types.Clock
	limit   int
	mu      sync.Mutex
	windows map[uuid.UUID]rateWindow
}

func newActorRateLimiter(clock types.Clock, limit int) *actorRateLimiter {
	return &actorRateLimiter{
		clock: clock, limit: limit, windows: make(map[uuid.UUID]rateWindow),
	}
}

func (limiter *actorRateLimiter) Allow(actorID uuid.UUID) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.clock.Now()
	window := limiter.windows[actorID]
	if window.start.IsZero() || now.Sub(window.start) >= time.Minute {
		window = rateWindow{start: now}
	}
	if window.count >= limiter.limit {
		limiter.windows[actorID] = window
		return false
	}
	window.count++
	limiter.windows[actorID] = window
	if len(limiter.windows) > 10_000 {
		for id, candidate := range limiter.windows {
			if now.Sub(candidate.start) >= time.Minute {
				delete(limiter.windows, id)
			}
		}
	}
	return true
}
