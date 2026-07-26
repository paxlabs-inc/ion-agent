package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/localipc"
)

// PeerIdentityChecker validates an accepted UDS peer before credentials are
// consumed.
type PeerIdentityChecker interface {
	Check(*net.UnixConn) error
}

// LocalServerConfig bounds the UDS transport.
type LocalServerConfig struct {
	SocketPath      string
	MaxConnections  int
	MaxPayloadBytes int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ReplayLimit     int
}

// LocalServer serves the common protocol over a permission-restricted UDS.
type LocalServer struct {
	dispatcher   *Dispatcher
	journal      *Journal
	capabilities *CapabilityManager
	peerChecker  PeerIdentityChecker
	config       LocalServerConfig
	connections  chan struct{}
}

type localHandshake struct {
	Capability string `json:"capability"`
}

type localClientFrame struct {
	Type          string   `json:"type"`
	Request       *Request `json:"request,omitempty"`
	AfterSequence uint64   `json:"after_sequence,omitempty"`
	ClientID      string   `json:"client_id,omitempty"`
	Sequence      uint64   `json:"sequence,omitempty"`
}

// LocalServerFrame is the local transport's generated outer envelope.
type LocalServerFrame struct {
	Type            string         `json:"type"`
	ProtocolVersion string         `json:"protocol_version"`
	ActorID         *uuid.UUID     `json:"actor_id,omitempty"`
	Response        *Response      `json:"response,omitempty"`
	Recovery        *Recovery      `json:"recovery,omitempty"`
	Event           *Event         `json:"event,omitempty"`
	Acknowledged    *uint64        `json:"acknowledged,omitempty"`
	Error           *ProtocolError `json:"error,omitempty"`
}

// NewLocalServer constructs a bounded local capability transport.
func NewLocalServer(
	dispatcher *Dispatcher,
	journal *Journal,
	capabilities *CapabilityManager,
	peerChecker PeerIdentityChecker,
	config LocalServerConfig,
) (*LocalServer, error) {
	if dispatcher == nil || journal == nil || capabilities == nil ||
		strings.TrimSpace(config.SocketPath) == "" {
		return nil, fmt.Errorf("controlplane: local transport dependencies and socket are required")
	}
	if config.MaxConnections <= 0 {
		config.MaxConnections = 8
	}
	if config.MaxConnections > 64 {
		return nil, fmt.Errorf("controlplane: local connection limit exceeds 64")
	}
	if config.MaxPayloadBytes <= 0 {
		config.MaxPayloadBytes = MaximumPayloadBytes
	}
	if config.MaxPayloadBytes > MaximumPayloadBytes {
		return nil, fmt.Errorf("controlplane: local payload bound exceeds protocol maximum")
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = 5 * time.Minute
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 10 * time.Second
	}
	if config.ReplayLimit <= 0 || config.ReplayLimit > maxReplayEvents {
		config.ReplayLimit = maxReplayEvents
	}
	if peerChecker == nil {
		peerChecker = platformPeerChecker{}
	}
	return &LocalServer{
		dispatcher: dispatcher, journal: journal, capabilities: capabilities,
		peerChecker: peerChecker, config: config,
		connections: make(chan struct{}, config.MaxConnections),
	}, nil
}

// ListenAndServe owns the socket from creation through context cancellation.
func (server *LocalServer) ListenAndServe(ctx context.Context) error {
	socketPath, err := filepath.Abs(server.config.SocketPath)
	if err != nil {
		return fmt.Errorf("controlplane: resolve local socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("controlplane: create local socket directory: %w", err)
	}
	listener, err := localipc.Listen(socketPath)
	if err != nil {
		return fmt.Errorf("controlplane: listen on local socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()      // Best-effort cleanup after insecure socket creation.
		_ = os.Remove(socketPath) // Best-effort cleanup after insecure socket creation.
		return fmt.Errorf("controlplane: secure local socket: %w", err)
	}
	defer func() {
		_ = listener.Close()      // Best-effort listener cleanup during shutdown.
		_ = os.Remove(socketPath) // Best-effort owned socket cleanup during shutdown.
	}()
	var handlers sync.WaitGroup
	defer handlers.Wait()
	go func() {
		<-ctx.Done()
		_ = listener.Close() // Context cancellation owns listener shutdown.
	}()
	for {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("controlplane: accept local connection: %w", acceptErr)
		}
		select {
		case server.connections <- struct{}{}:
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { <-server.connections }()
				server.handleConnection(ctx, connection)
			}()
		default:
			_ = connection.Close() // Connection limit rejects without blocking accept.
		}
	}
}

func (server *LocalServer) handleConnection(parent context.Context, connection *net.UnixConn) {
	defer connection.Close()
	if err := server.peerChecker.Check(connection); err != nil {
		return
	}
	_ = connection.SetReadDeadline(time.Now().Add(server.config.ReadTimeout))
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), server.config.MaxPayloadBytes)
	if !scanner.Scan() {
		return
	}
	var handshake localHandshake
	if err := decodeStrictJSON(scanner.Bytes(), &handshake); err != nil {
		return
	}
	actorID, err := server.capabilities.Consume(handshake.Capability)
	if err != nil {
		_ = writeLocalFrame(connection, server.config.WriteTimeout, LocalServerFrame{
			Type: "error", ProtocolVersion: ProtocolVersion,
			Error: protocolError(ErrorUnauthorized, "invalid local capability", false),
		})
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = connection.Close() // Connection context owns blocking read shutdown.
	}()
	var writeMu sync.Mutex
	writeFrame := func(frame LocalServerFrame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeLocalFrame(connection, server.config.WriteTimeout, frame)
	}
	if err := writeFrame(LocalServerFrame{
		Type: "ready", ProtocolVersion: ProtocolVersion, ActorID: &actorID,
	}); err != nil {
		return
	}
	var subscription *Subscription
	defer func() {
		if subscription != nil {
			subscription.Close()
		}
	}()
	var maximumSent atomic.Uint64
	for scanner.Scan() {
		_ = connection.SetReadDeadline(time.Now().Add(server.config.ReadTimeout))
		var frame localClientFrame
		if err := decodeStrictJSON(scanner.Bytes(), &frame); err != nil {
			_ = writeFrame(localErrorFrame(ErrorInvalid, "invalid local frame", false))
			return
		}
		switch frame.Type {
		case "rpc":
			if frame.Request == nil {
				_ = writeFrame(localErrorFrame(ErrorInvalid, "RPC request is required", false))
				return
			}
			response := server.dispatcher.Dispatch(ctx, actorID, *frame.Request)
			if err := writeFrame(LocalServerFrame{
				Type: "rpc", ProtocolVersion: ProtocolVersion, Response: &response,
			}); err != nil {
				return
			}
		case "subscribe":
			if subscription != nil {
				_ = writeFrame(localErrorFrame(ErrorConflict, "already subscribed", false))
				return
			}
			created, err := server.journal.SubscribeActor(
				ctx, actorID, frame.AfterSequence, server.config.ReplayLimit,
			)
			if err != nil {
				_ = writeFrame(localErrorFrame(ErrorUnavailable, "event journal unavailable", true))
				return
			}
			subscription = created
			recovery, err := server.dispatcher.Recover(
				ctx, Scope{ActorID: actorID}, created.Replay,
			)
			if err != nil {
				_ = writeFrame(localErrorFrame(ErrorUnavailable, "snapshot unavailable", true))
				return
			}
			maximumSent.Store(recovery.Replay.Latest)
			if err := writeFrame(LocalServerFrame{
				Type: "recovery", ProtocolVersion: ProtocolVersion, Recovery: &recovery,
			}); err != nil {
				return
			}
			for recovery.Replay.Latest < recovery.Replay.Head {
				page, pageErr := server.journal.replayActorThrough(
					ctx, actorID, recovery.Replay.Latest,
					server.config.ReplayLimit, recovery.Replay.Head,
				)
				if pageErr != nil || page.Gap || page.Latest <= recovery.Replay.Latest {
					_ = writeFrame(localErrorFrame(
						ErrorUnavailable, "event catch-up unavailable", true,
					))
					return
				}
				maximumSent.Store(page.Latest)
				if err := writeFrame(LocalServerFrame{
					Type: "recovery", ProtocolVersion: ProtocolVersion,
					Recovery: &Recovery{Replay: page},
				}); err != nil {
					return
				}
				recovery.Replay = page
			}
			go server.forwardLocalEvents(
				ctx, created, &maximumSent, writeFrame, cancel,
			)
		case "ack":
			if frame.ClientID == "" || frame.Sequence > maximumSent.Load() {
				_ = writeFrame(localErrorFrame(ErrorInvalid, "invalid acknowledgement", false))
				return
			}
			if err := server.journal.Acknowledge(
				ctx, actorID, frame.ClientID, frame.Sequence,
			); err != nil {
				_ = writeFrame(localErrorFrame(ErrorInvalid, "acknowledgement rejected", false))
				return
			}
			acknowledged := frame.Sequence
			if err := writeFrame(LocalServerFrame{
				Type: "ack", ProtocolVersion: ProtocolVersion,
				Acknowledged: &acknowledged,
			}); err != nil {
				return
			}
		default:
			_ = writeFrame(localErrorFrame(ErrorInvalid, "unknown local frame type", false))
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (server *LocalServer) forwardLocalEvents(
	ctx context.Context,
	subscription *Subscription,
	maximumSent *atomic.Uint64,
	writeFrame func(LocalServerFrame) error,
	cancel context.CancelFunc,
) {
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-subscription.Live:
			if !open {
				return
			}
			maximumSent.Store(event.Sequence)
			if err := writeFrame(LocalServerFrame{
				Type: "event", ProtocolVersion: ProtocolVersion, Event: &event,
			}); err != nil {
				return
			}
		}
	}
}

func writeLocalFrame(
	connection *net.UnixConn,
	timeout time.Duration,
	frame LocalServerFrame,
) error {
	if err := connection.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("controlplane: encode local frame: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := connection.Write(encoded); err != nil {
		return fmt.Errorf("controlplane: write local frame: %w", err)
	}
	return nil
}

func decodeStrictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || !atJSONEnd(decoder) {
		return fmt.Errorf("controlplane: invalid JSON frame")
	}
	return nil
}

func localErrorFrame(code ErrorCode, message string, retryable bool) LocalServerFrame {
	return LocalServerFrame{
		Type: "error", ProtocolVersion: ProtocolVersion,
		Error: protocolError(code, message, retryable),
	}
}
