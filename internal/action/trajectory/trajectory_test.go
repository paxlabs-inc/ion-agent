package trajectory

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/action"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
)

func TestEncryptedTrajectoryExportImportsAndReplaysInOrder(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x31}, vault.KeySize)
	cipher, err := vault.New(key)
	if err != nil {
		t.Fatal(err)
	}
	defer cipher.Close()
	entries := testEntries()
	var export bytes.Buffer
	if err := Export(&export, cipher, entries, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(export.Bytes(), []byte("sensitive-effect")) ||
		bytes.Contains(export.Bytes(), []byte("operation-one")) {
		t.Fatal("encrypted export contains plaintext")
	}
	imported, err := Import(bytes.NewReader(export.Bytes()), cipher)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported) != len(entries) || imported[0].ID != entries[0].ID {
		t.Fatalf("imported = %+v", imported)
	}
	var replayed []string
	if err := Replay(bytes.NewReader(export.Bytes()), cipher, func(entry action.RunEntry) error {
		replayed = append(replayed, entry.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 || replayed[0] != "run-1" || replayed[1] != "run-2" {
		t.Fatalf("replay order = %v", replayed)
	}
}

func TestTrajectoryRejectsWrongKeyTamperingTruncationAndTrailingBytes(t *testing.T) {
	t.Parallel()
	cipher, _ := vault.New(bytes.Repeat([]byte{1}, vault.KeySize))
	defer cipher.Close()
	wrong, _ := vault.New(bytes.Repeat([]byte{2}, vault.KeySize))
	defer wrong.Close()
	var export bytes.Buffer
	if err := Export(&export, cipher, testEntries(), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(bytes.NewReader(export.Bytes()), wrong); !errors.Is(err, vault.ErrDecryptionFailed) {
		t.Fatalf("wrong-key error = %v", err)
	}
	tampered := append([]byte(nil), export.Bytes()...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Import(bytes.NewReader(tampered), cipher); !errors.Is(err, vault.ErrDecryptionFailed) {
		t.Fatalf("tamper error = %v", err)
	}
	if _, err := Import(bytes.NewReader(export.Bytes()[:10]), cipher); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("truncation error = %v", err)
	}
	trailing := append(append([]byte(nil), export.Bytes()...), 1)
	if _, err := Import(bytes.NewReader(trailing), cipher); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("trailing-byte error = %v", err)
	}
}

func TestTrajectoryValidatesEntriesAndReplayFailure(t *testing.T) {
	t.Parallel()
	cipher, _ := vault.New(bytes.Repeat([]byte{3}, vault.KeySize))
	defer cipher.Close()
	var export bytes.Buffer
	if err := Export(&export, cipher, []action.RunEntry{{}}, time.Unix(1, 0)); !errors.Is(err, ErrInvalidFormat) {
		t.Fatalf("invalid entry error = %v", err)
	}
	if err := Export(&export, cipher, testEntries(), time.Time{}); err == nil {
		t.Fatal("zero export time succeeded")
	}
	export.Reset()
	if err := Export(&export, cipher, testEntries(), time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	expected := errors.New("apply failed")
	if err := Replay(bytes.NewReader(export.Bytes()), cipher, func(action.RunEntry) error {
		return expected
	}); !errors.Is(err, expected) {
		t.Fatalf("replay error = %v", err)
	}
}

func testEntries() []action.RunEntry {
	completed := time.Unix(11, 0)
	return []action.RunEntry{
		{
			ID:          "run-1",
			OperationID: "operation-one",
			Attempt:     1,
			Outcome:     action.OutcomeSuccess,
			StartedAt:   time.Unix(10, 0),
			CompletedAt: &completed,
			Effect:      []byte(`{"result":"sensitive-effect"}`),
		},
		{
			ID:          "run-2",
			OperationID: "operation-two",
			Attempt:     1,
			Outcome:     action.OutcomeFailure,
			StartedAt:   time.Unix(12, 0),
			Error:       "expected",
		},
	}
}
