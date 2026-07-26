// Package trajectory exports encrypted, integrity-checked operation histories
// that can be replayed in their original order.
package trajectory

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/action"
	"lukechampine.com/blake3"
)

const (
	formatVersion = uint16(1)
	headerSize    = 8 + 2 + 8
	maxEnvelope   = 1 << 30
)

var (
	magic               = [8]byte{'P', 'R', 'O', 'M', 'T', 'R', 'J', 0}
	ErrInvalidFormat    = errors.New("trajectory: invalid export format")
	ErrIntegrityFailure = errors.New("trajectory: integrity verification failed")
)

// Cipher is deliberately compatible with vault.Vault.
type Cipher interface {
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
}

type payload struct {
	SchemaVersion uint16            `json:"schema_version"`
	ExportedAt    time.Time         `json:"exported_at"`
	Entries       []action.RunEntry `json:"entries"`
	ChainRoot     [32]byte          `json:"chain_root"`
}

// Export encrypts a complete trajectory into a versioned binary envelope.
func Export(
	writer io.Writer,
	cipher Cipher,
	entries []action.RunEntry,
	exportedAt time.Time,
) error {
	if writer == nil || cipher == nil {
		return fmt.Errorf("trajectory: writer and cipher are required")
	}
	if exportedAt.IsZero() {
		return fmt.Errorf("trajectory: export timestamp is required")
	}
	copied := cloneEntries(entries)
	root, err := chainRoot(copied)
	if err != nil {
		return err
	}
	plaintext, err := json.Marshal(payload{
		SchemaVersion: formatVersion,
		ExportedAt:    exportedAt.UTC(),
		Entries:       copied,
		ChainRoot:     root,
	})
	if err != nil {
		return fmt.Errorf("trajectory: encode payload: %w", err)
	}
	defer zero(plaintext)
	envelope, err := cipher.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("trajectory: encrypt export: %w", err)
	}
	defer zero(envelope)
	if len(envelope) > maxEnvelope {
		return fmt.Errorf("%w: envelope too large", ErrInvalidFormat)
	}
	header := make([]byte, headerSize)
	copy(header[:8], magic[:])
	binary.BigEndian.PutUint16(header[8:10], formatVersion)
	binary.BigEndian.PutUint64(header[10:18], uint64(len(envelope)))
	if err := writeFull(writer, header); err != nil {
		return err
	}
	return writeFull(writer, envelope)
}

// Import decrypts and verifies a complete trajectory before returning entries.
func Import(reader io.Reader, cipher Cipher) ([]action.RunEntry, error) {
	if reader == nil || cipher == nil {
		return nil, fmt.Errorf("trajectory: reader and cipher are required")
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, fmt.Errorf("%w: truncated header", ErrInvalidFormat)
	}
	if !bytes.Equal(header[:8], magic[:]) ||
		binary.BigEndian.Uint16(header[8:10]) != formatVersion {
		return nil, ErrInvalidFormat
	}
	length := binary.BigEndian.Uint64(header[10:18])
	if length == 0 || length > maxEnvelope {
		return nil, ErrInvalidFormat
	}
	envelope := make([]byte, int(length))
	if _, err := io.ReadFull(reader, envelope); err != nil {
		return nil, fmt.Errorf("%w: truncated envelope", ErrInvalidFormat)
	}
	defer zero(envelope)
	var extra [1]byte
	if count, err := reader.Read(extra[:]); count != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return nil, fmt.Errorf("%w: trailing bytes", ErrInvalidFormat)
	}
	plaintext, err := cipher.Decrypt(envelope)
	if err != nil {
		return nil, fmt.Errorf("trajectory: decrypt export: %w", err)
	}
	defer zero(plaintext)
	var decoded payload
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}
	if decoded.SchemaVersion != formatVersion || decoded.ExportedAt.IsZero() {
		return nil, ErrInvalidFormat
	}
	root, err := chainRoot(decoded.Entries)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(root[:], decoded.ChainRoot[:]) {
		return nil, ErrIntegrityFailure
	}
	return cloneEntries(decoded.Entries), nil
}

// Replay verifies the export before invoking apply once per entry in order.
func Replay(reader io.Reader, cipher Cipher, apply func(action.RunEntry) error) error {
	if apply == nil {
		return fmt.Errorf("trajectory: replay callback is required")
	}
	entries, err := Import(reader, cipher)
	if err != nil {
		return err
	}
	for index, entry := range entries {
		if err := apply(entry); err != nil {
			return fmt.Errorf("trajectory: replay entry %d: %w", index, err)
		}
	}
	return nil
}

func chainRoot(entries []action.RunEntry) ([32]byte, error) {
	var root [32]byte
	for index, entry := range entries {
		if entry.ID == "" || entry.OperationID == "" || entry.StartedAt.IsZero() {
			return [32]byte{}, fmt.Errorf(
				"%w: incomplete entry %d", ErrInvalidFormat, index,
			)
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return [32]byte{}, fmt.Errorf("trajectory: encode entry %d: %w", index, err)
		}
		hasher := blake3.New(32, nil)
		_, _ = hasher.Write(root[:])
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(encoded)))
		_, _ = hasher.Write(length[:])
		_, _ = hasher.Write(encoded)
		copy(root[:], hasher.Sum(nil))
	}
	return root, nil
}

func cloneEntries(entries []action.RunEntry) []action.RunEntry {
	result := make([]action.RunEntry, len(entries))
	for index := range entries {
		result[index] = entries[index]
		result[index].Effect = append(json.RawMessage(nil), entries[index].Effect...)
		result[index].Evidence = append(json.RawMessage(nil), entries[index].Evidence...)
		if entries[index].FailureLayer != nil {
			layer := *entries[index].FailureLayer
			result[index].FailureLayer = &layer
		}
	}
	return result
}

func writeFull(writer io.Writer, content []byte) error {
	for len(content) > 0 {
		written, err := writer.Write(content)
		if err != nil {
			return fmt.Errorf("trajectory: write export: %w", err)
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		content = content[written:]
	}
	return nil
}

func zero(content []byte) {
	for index := range content {
		content[index] = 0
	}
}
