// Package hnsw provides the Go client, durable fallback, and reconciliation
// path for the Rust USearch microservice.
package hnsw

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

const (
	protocolVersion = 1
	responseBit     = 0x80
	statusOK        = 0
	statusError     = 1
	opInsert        = 0x01
	opSearch        = 0x02
	opDelete        = 0x03
	opCount         = 0x04
	opReset         = 0x05
	maxFrameSize    = 64 << 20
	defaultTimeout  = 5 * time.Second
)

var (
	// ErrServiceUnavailable marks UDS connection and transport failures. The
	// resilient Index uses it as the precise signal to activate brute force.
	ErrServiceUnavailable = errors.New("hnsw: service unavailable")
	// ErrInvalidVector marks dimensions, magnitude, or float values that cannot
	// safely be compared using cosine distance.
	ErrInvalidVector = errors.New("hnsw: invalid vector")
)

// Match is one nearest-neighbor result. Distance is cosine distance, where
// smaller values are closer and zero means identical direction.
type Match struct {
	Key      uint64
	Distance float32
}

// Remote is the microservice operation boundary used by Index.
type Remote interface {
	Insert(context.Context, uint64, []float32) error
	Search(context.Context, []float32, int) ([]Match, error)
	Delete(context.Context, uint64) (bool, error)
	Count(context.Context) (uint64, error)
	Reset(context.Context) error
	Close() error
}

// Client is a concurrency-safe persistent UDS protocol client. A transport
// failure drops the connection; the next operation attempts a fresh dial.
type Client struct {
	socketPath string
	dimensions int
	timeout    time.Duration

	mu     sync.Mutex
	conn   net.Conn
	closed bool
}

// NewClient configures a lazy UDS client. The first operation establishes the
// connection, which permits startup in fallback mode while the service is down.
func NewClient(socketPath string, dimensions int, timeout time.Duration) (*Client, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("hnsw: socket path is required")
	}
	if dimensions <= 0 {
		return nil, fmt.Errorf("hnsw: dimensions must be positive")
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		socketPath: socketPath,
		dimensions: dimensions,
		timeout:    timeout,
	}, nil
}

// Insert adds or replaces a vector under key.
func (client *Client) Insert(ctx context.Context, key uint64, vector []float32) error {
	if err := validateVector(vector, client.dimensions); err != nil {
		return err
	}
	request := bytes.NewBuffer(make([]byte, 0, 14+len(vector)*4))
	request.WriteByte(protocolVersion)
	request.WriteByte(opInsert)
	writeUint64(request, key)
	writeVector(request, vector)
	_, err := client.call(ctx, opInsert, request.Bytes())
	return err
}

// Search returns up to k nearest neighbors ordered by increasing distance.
func (client *Client) Search(
	ctx context.Context,
	vector []float32,
	k int,
) ([]Match, error) {
	if err := validateVector(vector, client.dimensions); err != nil {
		return nil, err
	}
	if k < 0 {
		return nil, fmt.Errorf("hnsw: k must not be negative")
	}
	if uint64(k) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("hnsw: k is too large")
	}
	request := bytes.NewBuffer(make([]byte, 0, 10+len(vector)*4))
	request.WriteByte(protocolVersion)
	request.WriteByte(opSearch)
	writeUint32(request, uint32(k))
	writeVector(request, vector)
	payload, err := client.call(ctx, opSearch, request.Bytes())
	if err != nil {
		return nil, err
	}
	if len(payload) < 4 {
		return nil, client.protocolFailure("truncated search response")
	}
	count := int(binary.BigEndian.Uint32(payload[:4]))
	expected, overflow := checkedResponseSize(count)
	if overflow || len(payload) != expected {
		return nil, client.protocolFailure("invalid search response length")
	}
	matches := make([]Match, count)
	offset := 4
	for index := range matches {
		matches[index] = Match{
			Key:      binary.BigEndian.Uint64(payload[offset : offset+8]),
			Distance: math.Float32frombits(binary.BigEndian.Uint32(payload[offset+8 : offset+12])),
		}
		if !isFinite(matches[index].Distance) {
			return nil, client.protocolFailure("non-finite search distance")
		}
		offset += 12
	}
	return matches, nil
}

// Delete removes key and reports whether it existed.
func (client *Client) Delete(ctx context.Context, key uint64) (bool, error) {
	request := bytes.NewBuffer(make([]byte, 0, 10))
	request.WriteByte(protocolVersion)
	request.WriteByte(opDelete)
	writeUint64(request, key)
	payload, err := client.call(ctx, opDelete, request.Bytes())
	if err != nil {
		return false, err
	}
	if len(payload) != 1 || payload[0] > 1 {
		return false, client.protocolFailure("invalid delete response")
	}
	return payload[0] == 1, nil
}

// Count reports the number of vectors in the remote graph.
func (client *Client) Count(ctx context.Context) (uint64, error) {
	payload, err := client.call(ctx, opCount, []byte{protocolVersion, opCount})
	if err != nil {
		return 0, err
	}
	if len(payload) != 8 {
		return 0, client.protocolFailure("invalid count response")
	}
	return binary.BigEndian.Uint64(payload), nil
}

// Reset clears every vector. It is used only before a journal-derived rebuild.
func (client *Client) Reset(ctx context.Context) error {
	payload, err := client.call(ctx, opReset, []byte{protocolVersion, opReset})
	if err != nil {
		return err
	}
	if len(payload) != 0 {
		return client.protocolFailure("invalid reset response")
	}
	return nil
}

// Close terminates the persistent socket connection.
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.closed = true
	if client.conn == nil {
		return nil
	}
	err := client.conn.Close()
	client.conn = nil
	return err
}

func (client *Client) call(
	ctx context.Context,
	operation byte,
	request []byte,
) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return nil, fmt.Errorf("%w: client is closed", ErrServiceUnavailable)
	}
	if len(request) == 0 || len(request) > maxFrameSize {
		return nil, fmt.Errorf("hnsw: invalid request frame size")
	}
	if client.conn == nil {
		dialer := net.Dialer{Timeout: client.timeout}
		connection, err := dialer.DialContext(ctx, "unix", client.socketPath)
		if err != nil {
			return nil, unavailable(err)
		}
		client.conn = connection
	}
	deadline := time.Now().Add(client.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := client.conn.SetDeadline(deadline); err != nil {
		client.dropConnection()
		return nil, unavailable(err)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(request)))
	if err := writeAll(client.conn, append(length[:], request...)); err != nil {
		client.dropConnection()
		return nil, unavailable(err)
	}
	if _, err := io.ReadFull(client.conn, length[:]); err != nil {
		client.dropConnection()
		return nil, unavailable(err)
	}
	responseLength := int(binary.BigEndian.Uint32(length[:]))
	if responseLength < 3 || responseLength > maxFrameSize {
		client.dropConnection()
		return nil, unavailable(fmt.Errorf("invalid response frame size %d", responseLength))
	}
	response := make([]byte, responseLength)
	if _, err := io.ReadFull(client.conn, response); err != nil {
		client.dropConnection()
		return nil, unavailable(err)
	}
	if response[0] != protocolVersion || response[1] != operation|responseBit {
		client.dropConnection()
		return nil, unavailable(fmt.Errorf("invalid response header"))
	}
	switch response[2] {
	case statusOK:
		return response[3:], nil
	case statusError:
		if len(response) < 5 {
			client.dropConnection()
			return nil, unavailable(fmt.Errorf("truncated service error"))
		}
		messageLength := int(binary.BigEndian.Uint16(response[3:5]))
		if len(response) != 5+messageLength {
			client.dropConnection()
			return nil, unavailable(fmt.Errorf("invalid service error"))
		}
		return nil, &ServiceError{Operation: operation, Message: string(response[5:])}
	default:
		client.dropConnection()
		return nil, unavailable(fmt.Errorf("unknown response status %d", response[2]))
	}
}

// ServiceError is a valid error response from the Rust process, as distinct
// from a crash/transport failure that should activate fallback.
type ServiceError struct {
	Operation byte
	Message   string
}

func (err *ServiceError) Error() string {
	return fmt.Sprintf("hnsw: service operation %#02x: %s", err.Operation, err.Message)
}

func (client *Client) protocolFailure(message string) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.dropConnection()
	return unavailable(errors.New(message))
}

func (client *Client) dropConnection() {
	if client.conn != nil {
		_ = client.conn.Close()
		client.conn = nil
	}
}

func unavailable(err error) error {
	return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
}

func validateVector(vector []float32, dimensions int) error {
	if len(vector) != dimensions {
		return fmt.Errorf(
			"%w: dimension mismatch: got %d, want %d",
			ErrInvalidVector,
			len(vector),
			dimensions,
		)
	}
	norm := 0.0
	for _, value := range vector {
		if !isFinite(value) {
			return fmt.Errorf("%w: non-finite value", ErrInvalidVector)
		}
		norm += float64(value) * float64(value)
	}
	if norm == 0 {
		return fmt.Errorf("%w: zero magnitude", ErrInvalidVector)
	}
	return nil
}

func isFinite(value float32) bool {
	return !float32IsNaN(value) && !float32IsInf(value)
}

func float32IsNaN(value float32) bool {
	return value != value
}

func float32IsInf(value float32) bool {
	return value > math.MaxFloat32 || value < -math.MaxFloat32
}

func writeUint32(buffer *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeVector(buffer *bytes.Buffer, vector []float32) {
	writeUint32(buffer, uint32(len(vector)))
	for _, value := range vector {
		writeUint32(buffer, math.Float32bits(value))
	}
}

func checkedResponseSize(count int) (int, bool) {
	if count < 0 || count > (maxFrameSize-4)/12 {
		return 0, true
	}
	return 4 + count*12, false
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) != 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[written:]
	}
	return nil
}
