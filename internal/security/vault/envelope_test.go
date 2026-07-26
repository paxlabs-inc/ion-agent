package vault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestVaultEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 1, 15, 16, 31, 32, 1024, 64 * 1024} {
		size := size
		t.Run(fmt.Sprintf("bytes_%d", size), func(t *testing.T) {
			t.Parallel()
			key := repeatedKey(0x11)
			instance, err := New(key)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer closeVault(t, instance)

			plaintext := bytes.Repeat([]byte{0x7a}, size)
			envelope, err := instance.Encrypt(plaintext)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}
			if got, want := len(envelope), envelopeOverhead+size; got != want {
				t.Fatalf("envelope length = %d, want %d", got, want)
			}
			decrypted, err := instance.Decrypt(envelope)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}
			if !bytes.Equal(decrypted, plaintext) {
				t.Fatal("decrypted plaintext differs")
			}
		})
	}
}

func TestVaultUsesFreshDEKAndNonces(t *testing.T) {
	t.Parallel()
	instance, err := New(repeatedKey(0x22))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer closeVault(t, instance)

	first, err := instance.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("first Encrypt() error = %v", err)
	}
	second, err := instance.Encrypt([]byte("same"))
	if err != nil {
		t.Fatalf("second Encrypt() error = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two envelopes for the same plaintext are identical")
	}
}

func TestVaultWrongKeyReturnsOnlySentinel(t *testing.T) {
	t.Parallel()
	first, err := New(repeatedKey(0x33))
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	defer closeVault(t, first)
	second, err := New(repeatedKey(0x44))
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	defer closeVault(t, second)
	envelope, err := first.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	_, err = second.Decrypt(envelope)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("Decrypt() error = %v, want ErrDecryptionFailed", err)
	}
	if err.Error() != ErrDecryptionFailed.Error() {
		t.Fatalf("error leaks detail: %q", err)
	}
}

func TestVaultMalformedEnvelopeReturnsOnlySentinel(t *testing.T) {
	t.Parallel()
	instance, err := New(repeatedKey(0x55))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer closeVault(t, instance)
	valid, err := instance.Encrypt([]byte("authenticated content"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	cases := map[string][]byte{
		"nil":         nil,
		"empty":       {},
		"short":       bytes.Repeat([]byte{1}, envelopeOverhead-1),
		"bad_dek_iv":  mutateByte(valid, 0),
		"bad_dek":     mutateByte(valid, DEKNonceSize),
		"bad_body_iv": mutateByte(valid, DEKNonceSize+encryptedDEKSize),
		"bad_content": mutateByte(valid, len(valid)-1),
	}
	for name, envelope := range cases {
		envelope := envelope
		t.Run(name, func(t *testing.T) {
			_, decryptErr := instance.Decrypt(envelope)
			if decryptErr != ErrDecryptionFailed {
				t.Fatalf("Decrypt() error = %v, want exact sentinel", decryptErr)
			}
		})
	}
}

func TestVaultRejectsInvalidConstruction(t *testing.T) {
	t.Parallel()
	for name, key := range map[string][]byte{
		"nil":   nil,
		"short": make([]byte, KeySize-1),
		"long":  make([]byte, KeySize+1),
	} {
		key := key
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := New(key)
			if !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("New() error = %v, want ErrInvalidKey", err)
			}
		})
	}
	if _, err := newWithReader(repeatedKey(1), nil); err == nil {
		t.Fatal("newWithReader(nil random) succeeded")
	}
}

func TestVaultCloseZeroesUserKey(t *testing.T) {
	t.Parallel()
	instance, err := New(repeatedKey(0x66))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ownedKey := instance.userKey
	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !bytes.Equal(ownedKey, make([]byte, KeySize)) {
		t.Fatal("Close() did not zero the owned User Key")
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := instance.Encrypt(nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Encrypt() after close error = %v, want ErrClosed", err)
	}
	if _, err := instance.Decrypt(nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Decrypt() after close error = %v, want ErrClosed", err)
	}
}

func TestVaultRotateUserKeySuccess(t *testing.T) {
	t.Parallel()
	instance, err := New(repeatedKey(0x77))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer closeVault(t, instance)
	envelope, err := instance.Encrypt([]byte("survives rotation"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	rewrapper := &memoryRewrapper{envelopes: [][]byte{envelope}}
	ownedOldKey := instance.userKey
	oldKey := append([]byte(nil), ownedOldKey...)
	if err := instance.RotateUserKey(context.Background(), rewrapper); err != nil {
		t.Fatalf("RotateUserKey() error = %v", err)
	}
	if bytes.Equal(instance.userKey, oldKey) {
		t.Fatal("User Key did not change")
	}
	if !bytes.Equal(ownedOldKey, make([]byte, KeySize)) {
		t.Fatal("old User Key was not zeroed")
	}
	plaintext, err := instance.Decrypt(rewrapper.envelopes[0])
	if err != nil {
		t.Fatalf("Decrypt(rotated) error = %v", err)
	}
	if string(plaintext) != "survives rotation" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestVaultRotateUserKeyFailureIsAtomic(t *testing.T) {
	t.Parallel()
	instance, err := New(repeatedKey(0x88))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer closeVault(t, instance)
	envelope, err := instance.Encrypt([]byte("unchanged"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	oldKey := append([]byte(nil), instance.userKey...)
	expected := errors.New("durable transaction failed")
	rewrapper := &memoryRewrapper{envelopes: [][]byte{envelope}, err: expected}
	err = instance.RotateUserKey(context.Background(), rewrapper)
	if !errors.Is(err, expected) {
		t.Fatalf("RotateUserKey() error = %v, want injected failure", err)
	}
	if !bytes.Equal(instance.userKey, oldKey) {
		t.Fatal("User Key changed after failed durable rotation")
	}
	plaintext, err := instance.Decrypt(envelope)
	if err != nil || string(plaintext) != "unchanged" {
		t.Fatalf("old envelope no longer decrypts: plaintext=%q error=%v", plaintext, err)
	}
}

func TestVaultRotateUserKeyValidation(t *testing.T) {
	t.Parallel()
	instance, err := New(repeatedKey(0x99))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := instance.RotateUserKey(context.Background(), nil); err == nil {
		t.Fatal("RotateUserKey(nil) succeeded")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := instance.RotateUserKey(cancelled, &memoryRewrapper{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("RotateUserKey(cancelled) error = %v", err)
	}
	if err := instance.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := instance.RotateUserKey(context.Background(), &memoryRewrapper{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("RotateUserKey(closed) error = %v", err)
	}
}

func TestRewrapChangesOnlyWrappedDEK(t *testing.T) {
	t.Parallel()
	oldKey := repeatedKey(0xaa)
	newKey := repeatedKey(0xbb)
	envelope, err := encryptWithKey(bytes.NewReader(bytes.Repeat([]byte{7}, 128)), oldKey, []byte("content"))
	if err != nil {
		t.Fatalf("encryptWithKey() error = %v", err)
	}
	rewrapped, err := Rewrap(oldKey, newKey, envelope)
	if err != nil {
		t.Fatalf("Rewrap() error = %v", err)
	}
	if bytes.Equal(rewrapped[:DEKNonceSize+encryptedDEKSize], envelope[:DEKNonceSize+encryptedDEKSize]) {
		t.Fatal("wrapped DEK did not change")
	}
	if !bytes.Equal(rewrapped[DEKNonceSize+encryptedDEKSize:], envelope[DEKNonceSize+encryptedDEKSize:]) {
		t.Fatal("content IV or ciphertext changed")
	}
	plaintext, err := decryptWithKey(newKey, rewrapped)
	if err != nil || string(plaintext) != "content" {
		t.Fatalf("new key decrypt = %q, %v", plaintext, err)
	}
	if _, err := decryptWithKey(oldKey, rewrapped); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("old key decrypt error = %v", err)
	}
}

func TestVaultRandomFailuresDoNotReturnPartialEnvelope(t *testing.T) {
	t.Parallel()
	for name, allowed := range map[string]int{
		"dek":           0,
		"dek_nonce":     KeySize,
		"content_nonce": KeySize + DEKNonceSize,
	} {
		allowed := allowed
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			instance, err := newWithReader(repeatedKey(1), &limitedErrorReader{remaining: allowed})
			if err != nil {
				t.Fatalf("newWithReader() error = %v", err)
			}
			defer closeVault(t, instance)
			envelope, err := instance.Encrypt([]byte("content"))
			if err == nil {
				t.Fatal("Encrypt() succeeded")
			}
			if envelope != nil {
				t.Fatalf("Encrypt() returned partial envelope of %d bytes", len(envelope))
			}
		})
	}
}

func TestVaultRotateUserKeyRandomFailurePreservesKey(t *testing.T) {
	t.Parallel()
	instance, err := newWithReader(repeatedKey(2), &limitedErrorReader{})
	if err != nil {
		t.Fatalf("newWithReader() error = %v", err)
	}
	defer closeVault(t, instance)
	before := append([]byte(nil), instance.userKey...)
	err = instance.RotateUserKey(context.Background(), &memoryRewrapper{})
	if err == nil {
		t.Fatal("RotateUserKey() succeeded")
	}
	if !bytes.Equal(instance.userKey, before) {
		t.Fatal("User Key changed after random source failure")
	}
}

func TestEnvelopeHelpersRejectInvalidKeys(t *testing.T) {
	t.Parallel()
	if _, err := encryptWithKey(bytes.NewReader(make([]byte, 128)), []byte("short"), nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("encryptWithKey(short) error = %v", err)
	}
	if _, err := Rewrap([]byte("short"), repeatedKey(1), make([]byte, envelopeOverhead)); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("Rewrap(short old key) error = %v", err)
	}
	if _, err := Rewrap(repeatedKey(1), []byte("short"), make([]byte, envelopeOverhead)); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("Rewrap(short new key) error = %v", err)
	}
	if _, err := seal([]byte("short"), make([]byte, ContentIVSize), nil); err == nil {
		t.Fatal("seal(short key) succeeded")
	}
	if _, err := open([]byte("short"), make([]byte, ContentIVSize), nil); err == nil {
		t.Fatal("open(short key) succeeded")
	}
	if _, err := seal(repeatedKey(1), nil, nil); err == nil {
		t.Fatal("seal(invalid nonce size) succeeded")
	}
	if _, err := open(repeatedKey(1), nil, nil); err == nil {
		t.Fatal("open(invalid nonce size) succeeded")
	}
	if _, err := Rewrap(
		repeatedKey(1),
		repeatedKey(2),
		make([]byte, envelopeOverhead),
	); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("Rewrap(corrupt envelope) error = %v", err)
	}
	valid, err := encryptWithKey(
		bytes.NewReader(bytes.Repeat([]byte{1}, 128)),
		repeatedKey(1),
		[]byte("content"),
	)
	if err != nil {
		t.Fatalf("encryptWithKey() error = %v", err)
	}
	if _, err := rewrapWithReader(
		&limitedErrorReader{},
		repeatedKey(1),
		repeatedKey(2),
		valid,
	); err == nil {
		t.Fatal("rewrapWithReader(random failure) succeeded")
	}
}

type memoryRewrapper struct {
	envelopes [][]byte
	err       error
}

type limitedErrorReader struct {
	remaining int
}

func (reader *limitedErrorReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	count := len(destination)
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := 0; index < count; index++ {
		destination[index] = byte(index)
	}
	reader.remaining -= count
	if count < len(destination) {
		return count, io.ErrUnexpectedEOF
	}
	return count, nil
}

func (rewrapper *memoryRewrapper) RewrapEnvelopes(
	ctx context.Context,
	oldKey []byte,
	newKey []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rewrapper.err != nil {
		return rewrapper.err
	}
	replacements := make([][]byte, len(rewrapper.envelopes))
	for index, envelope := range rewrapper.envelopes {
		rewrapped, err := Rewrap(oldKey, newKey, envelope)
		if err != nil {
			return err
		}
		replacements[index] = rewrapped
	}
	rewrapper.envelopes = replacements
	return nil
}

func repeatedKey(value byte) []byte {
	return bytes.Repeat([]byte{value}, KeySize)
}

func mutateByte(input []byte, index int) []byte {
	result := append([]byte(nil), input...)
	result[index] ^= 0xff
	return result
}

func closeVault(t *testing.T, instance *Vault) {
	t.Helper()
	if err := instance.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
