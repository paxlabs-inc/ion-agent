package dashboard

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const maximumWebSocketConnections = 16

// ServerConfig defines the authenticated, origin-restricted WebSocket API.
type ServerConfig struct {
	BearerToken    string
	AllowedOrigins []string
	MaxConnections int
	HistoryLimit   int
	WriteTimeout   time.Duration
	PongTimeout    time.Duration
	PingInterval   time.Duration
}

// Server streams safety events over a bounded WebSocket connection pool.
type Server struct {
	dashboard      *Dashboard
	bearerToken    string
	allowedOrigins map[string]struct{}
	connections    chan struct{}
	historyLimit   int
	writeTimeout   time.Duration
	pongTimeout    time.Duration
	pingInterval   time.Duration
	upgrader       websocket.Upgrader
}

// StreamMessage is the external JSON protocol. A client first receives one
// snapshot message, followed by event messages as they are published.
type StreamMessage struct {
	Type   string  `json:"type"`
	Events []Event `json:"events,omitempty"`
	Event  *Event  `json:"event,omitempty"`
}

// NewServer constructs a fail-closed WebSocket backend.
func NewServer(dashboard *Dashboard, config ServerConfig) (*Server, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard: event source required")
	}
	if strings.TrimSpace(config.BearerToken) == "" {
		return nil, fmt.Errorf("dashboard: bearer token required")
	}
	if config.MaxConnections <= 0 {
		config.MaxConnections = maximumWebSocketConnections
	}
	if config.MaxConnections > maximumWebSocketConnections {
		return nil, fmt.Errorf(
			"dashboard: max connections cannot exceed %d",
			maximumWebSocketConnections,
		)
	}
	if config.HistoryLimit <= 0 {
		config.HistoryLimit = 100
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 10 * time.Second
	}
	if config.PongTimeout <= 0 {
		config.PongTimeout = 60 * time.Second
	}
	if config.PingInterval <= 0 {
		config.PingInterval = 30 * time.Second
	}
	if config.PingInterval >= config.PongTimeout {
		return nil, fmt.Errorf("dashboard: ping interval must be less than pong timeout")
	}
	origins := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, rawOrigin := range config.AllowedOrigins {
		origin, err := normalizeOrigin(rawOrigin)
		if err != nil {
			return nil, err
		}
		origins[origin] = struct{}{}
	}
	server := &Server{
		dashboard:      dashboard,
		bearerToken:    config.BearerToken,
		allowedOrigins: origins,
		connections:    make(chan struct{}, config.MaxConnections),
		historyLimit:   config.HistoryLimit,
		writeTimeout:   config.WriteTimeout,
		pongTimeout:    config.PongTimeout,
		pingInterval:   config.PingInterval,
	}
	server.upgrader = websocket.Upgrader{
		HandshakeTimeout: config.WriteTimeout,
		CheckOrigin:      server.checkOrigin,
	}
	return server, nil
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !server.authorized(request) {
		writer.Header().Set("WWW-Authenticate", `Bearer realm="ion-safety"`)
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	types, err := requestedTypes(request.URL.Query()["type"])
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	historyLimit, err := requestedHistoryLimit(
		request.URL.Query().Get("history"),
		server.historyLimit,
	)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	select {
	case server.connections <- struct{}{}:
		defer func() { <-server.connections }()
	default:
		http.Error(writer, "dashboard connection limit reached", http.StatusServiceUnavailable)
		return
	}

	responseHeader := http.Header{}
	if protocol := bearerSubprotocol(request); protocol != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", protocol)
	}
	connection, err := server.upgrader.Upgrade(writer, request, responseHeader)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(4096)
	_ = connection.SetReadDeadline(time.Now().Add(server.pongTimeout))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(server.pongTimeout))
	})

	subscriber := server.dashboard.Subscribe(types...)
	defer server.dashboard.Unsubscribe(subscriber.ID)
	var history []Event
	if historyLimit > 0 {
		filter := EventType("")
		if len(types) == 1 {
			filter = types[0]
		}
		history = server.dashboard.History(historyLimit, filter)
		if len(types) > 1 {
			history = filterEvents(server.dashboard.History(0, ""), types)
			if len(history) > historyLimit {
				history = history[:historyLimit]
			}
		}
	}
	if err := server.write(connection, StreamMessage{
		Type:   "snapshot",
		Events: history,
	}); err != nil {
		return
	}

	// The handler owns this reader goroutine. Closing the connection on return
	// unblocks ReadMessage, and readerDone terminates the writer loop.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(server.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-readerDone:
			return
		case event, open := <-subscriber.Events:
			if !open {
				return
			}
			if err := server.write(connection, StreamMessage{
				Type:  "event",
				Event: &event,
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

func (server *Server) write(connection *websocket.Conn, message StreamMessage) error {
	if err := connection.SetWriteDeadline(time.Now().Add(server.writeTimeout)); err != nil {
		return err
	}
	return connection.WriteJSON(message)
}

func (server *Server) authorized(request *http.Request) bool {
	const prefix = "Bearer "
	header := request.Header.Get("Authorization")
	provided := ""
	if strings.HasPrefix(header, prefix) {
		provided = strings.TrimSpace(strings.TrimPrefix(header, prefix))
	} else if protocol := bearerSubprotocol(request); protocol != "" {
		const protocolPrefix = "ion-bearer."
		decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(protocol, protocolPrefix))
		if err != nil {
			return false
		}
		provided = string(decoded)
	}
	providedBytes := []byte(provided)
	expected := []byte(server.bearerToken)
	return len(providedBytes) == len(expected) &&
		subtle.ConstantTimeCompare(providedBytes, expected) == 1
}

func bearerSubprotocol(request *http.Request) string {
	const prefix = "ion-bearer."
	for _, protocol := range websocket.Subprotocols(request) {
		if strings.HasPrefix(protocol, prefix) {
			return protocol
		}
	}
	return ""
}

func (server *Server) checkOrigin(request *http.Request) bool {
	rawOrigin := request.Header.Get("Origin")
	if rawOrigin == "" {
		return true
	}
	origin, err := normalizeOrigin(rawOrigin)
	if err != nil {
		return false
	}
	_, allowed := server.allowedOrigins[origin]
	return allowed
}

func normalizeOrigin(rawOrigin string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawOrigin))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Path != "" {
		return "", fmt.Errorf("dashboard: invalid allowed origin %q", rawOrigin)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("dashboard: invalid allowed origin scheme")
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host), nil
}

func requestedHistoryLimit(raw string, maximum int) (int, error) {
	if raw == "" {
		return maximum, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 || limit > maximum {
		return 0, fmt.Errorf("dashboard: history must be between 0 and %d", maximum)
	}
	return limit, nil
}

func requestedTypes(values []string) ([]EventType, error) {
	types := make([]EventType, 0, len(values))
	seen := make(map[EventType]struct{}, len(values))
	for _, value := range values {
		eventType := EventType(value)
		if !validEventType(eventType) {
			return nil, fmt.Errorf("dashboard: unknown event type %q", value)
		}
		if _, exists := seen[eventType]; exists {
			continue
		}
		seen[eventType] = struct{}{}
		types = append(types, eventType)
	}
	return types, nil
}

func validEventType(eventType EventType) bool {
	switch eventType {
	case EventSafetyViolation,
		EventCircuitBreaker,
		EventCanaryAccess,
		EventPolicyDecision,
		EventEmotionalState,
		EventCoordinationAuth,
		EventToolExecution,
		EventPremiseRefuted,
		EventConvergenceWarning,
		EventCassandraEdit:
		return true
	default:
		return false
	}
}

func filterEvents(events []Event, types []EventType) []Event {
	allowed := make(map[EventType]struct{}, len(types))
	for _, eventType := range types {
		allowed[eventType] = struct{}{}
	}
	filtered := make([]Event, 0, len(events))
	for _, event := range events {
		if _, exists := allowed[event.Type]; exists {
			filtered = append(filtered, event)
		}
	}
	return filtered
}
