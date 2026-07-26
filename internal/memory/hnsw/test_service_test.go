package hnsw

import (
	"encoding/binary"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// testUDSService is a real Unix-stream protocol peer at the external service
// boundary. Its exact in-memory search keeps Go tests independent of a local
// Rust toolchain; the Rust crate separately tests the USearch implementation.
type testUDSService struct {
	listener net.Listener
	path     string

	mu         sync.Mutex
	vectors    map[uint64][]float32
	conns      map[net.Conn]struct{}
	resetCount int
	stopped    bool
}

func startTestUDSService(t *testing.T) *testUDSService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hnsw.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	service := &testUDSService{
		listener: listener,
		path:     path,
		vectors:  make(map[uint64][]float32),
		conns:    make(map[net.Conn]struct{}),
	}
	go service.accept()
	t.Cleanup(service.crash)
	return service
}

func (service *testUDSService) accept() {
	for {
		connection, err := service.listener.Accept()
		if err != nil {
			return
		}
		service.mu.Lock()
		if service.stopped {
			service.mu.Unlock()
			_ = connection.Close()
			return
		}
		service.conns[connection] = struct{}{}
		service.mu.Unlock()
		go service.serve(connection)
	}
}

func (service *testUDSService) serve(connection net.Conn) {
	defer func() {
		_ = connection.Close()
		service.mu.Lock()
		delete(service.conns, connection)
		service.mu.Unlock()
	}()
	for {
		request, err := readTestFrame(connection)
		if err != nil {
			return
		}
		response := service.handle(request)
		if err := writeTestFrame(connection, response); err != nil {
			return
		}
	}
}

func (service *testUDSService) handle(request []byte) []byte {
	if len(request) < 2 || request[0] != protocolVersion {
		return testErrorResponse(0, "bad request")
	}
	operation := request[1]
	payload := request[2:]
	service.mu.Lock()
	defer service.mu.Unlock()
	switch operation {
	case opInsert:
		if len(payload) < 12 {
			return testErrorResponse(operation, "truncated insert")
		}
		key := binary.BigEndian.Uint64(payload[:8])
		vector, ok := decodeTestVector(payload[8:])
		if !ok {
			return testErrorResponse(operation, "bad vector")
		}
		service.vectors[key] = vector
		return testSuccessResponse(operation, nil)
	case opSearch:
		if len(payload) < 8 {
			return testErrorResponse(operation, "truncated search")
		}
		k := int(binary.BigEndian.Uint32(payload[:4]))
		query, ok := decodeTestVector(payload[4:])
		if !ok {
			return testErrorResponse(operation, "bad vector")
		}
		matches := make([]Match, 0, len(service.vectors))
		for key, values := range service.vectors {
			if len(values) != len(query) {
				continue
			}
			matches = append(matches, Match{
				Key:      key,
				Distance: cosineDistance(query, vectorNorm(query), values),
			})
		}
		sort.Slice(matches, func(left, right int) bool {
			return lessMatch(matches[left], matches[right])
		})
		if len(matches) > k {
			matches = matches[:k]
		}
		response := make([]byte, 4, 4+len(matches)*12)
		binary.BigEndian.PutUint32(response, uint32(len(matches)))
		for _, match := range matches {
			var encoded [12]byte
			binary.BigEndian.PutUint64(encoded[:8], match.Key)
			binary.BigEndian.PutUint32(encoded[8:], math.Float32bits(match.Distance))
			response = append(response, encoded[:]...)
		}
		return testSuccessResponse(operation, response)
	case opDelete:
		if len(payload) != 8 {
			return testErrorResponse(operation, "bad delete")
		}
		key := binary.BigEndian.Uint64(payload)
		_, exists := service.vectors[key]
		delete(service.vectors, key)
		return testSuccessResponse(operation, []byte{boolByte(exists)})
	case opCount:
		var count [8]byte
		binary.BigEndian.PutUint64(count[:], uint64(len(service.vectors)))
		return testSuccessResponse(operation, count[:])
	case opReset:
		service.vectors = make(map[uint64][]float32)
		service.resetCount++
		return testSuccessResponse(operation, nil)
	default:
		return testErrorResponse(operation, "unknown operation")
	}
}

func (service *testUDSService) crash() {
	service.mu.Lock()
	if service.stopped {
		service.mu.Unlock()
		return
	}
	service.stopped = true
	_ = service.listener.Close()
	for connection := range service.conns {
		_ = connection.Close()
	}
	service.conns = make(map[net.Conn]struct{})
	service.mu.Unlock()
	_ = os.Remove(service.path)
}

func (service *testUDSService) vectorCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return len(service.vectors)
}

func (service *testUDSService) resets() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.resetCount
}

func readTestFrame(reader io.Reader) ([]byte, error) {
	var length [4]byte
	if _, err := io.ReadFull(reader, length[:]); err != nil {
		return nil, err
	}
	body := make([]byte, binary.BigEndian.Uint32(length[:]))
	_, err := io.ReadFull(reader, body)
	return body, err
}

func writeTestFrame(writer io.Writer, body []byte) error {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(body)))
	if err := writeAll(writer, length[:]); err != nil {
		return err
	}
	return writeAll(writer, body)
}

func decodeTestVector(payload []byte) ([]float32, bool) {
	if len(payload) < 4 {
		return nil, false
	}
	dimensions := int(binary.BigEndian.Uint32(payload[:4]))
	if len(payload) != 4+dimensions*4 {
		return nil, false
	}
	vector := make([]float32, dimensions)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.BigEndian.Uint32(payload[4+index*4:]))
	}
	return vector, true
}

func testSuccessResponse(operation byte, payload []byte) []byte {
	return append([]byte{protocolVersion, operation | responseBit, statusOK}, payload...)
}

func testErrorResponse(operation byte, message string) []byte {
	response := []byte{protocolVersion, operation | responseBit, statusError, 0, byte(len(message))}
	return append(response, message...)
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}
