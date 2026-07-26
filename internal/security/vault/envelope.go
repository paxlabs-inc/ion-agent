package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
)

const (
	KeySize               = 32
	DEKNonceSize          = 16
	ContentIVSize         = 12
	AuthenticationTagSize = 16
	encryptedDEKSize      = KeySize + AuthenticationTagSize
	envelopeOverhead      = DEKNonceSize + encryptedDEKSize + ContentIVSize + AuthenticationTagSize
)

// EnvelopeRewrapper atomically replaces all envelopes encrypted by oldKey.
// Implementations must leave every envelope untouched when they return an
// error.
type EnvelopeRewrapper interface {
	RewrapEnvelopes(context.Context, []byte, []byte) error
}

// Vault owns a per-user key and creates a fresh DEK for every object.
type Vault struct {
	mu      sync.RWMutex
	userKey []byte
	rand    io.Reader
	closed  bool
}

// New constructs a vault by copying userKey into vault-owned memory.
func New(userKey []byte) (*Vault, error) {
	return newWithReader(userKey, rand.Reader)
}

func newWithReader(userKey []byte, random io.Reader) (*Vault, error) {
	if len(userKey) != KeySize {
		return nil, ErrInvalidKey
	}
	if random == nil {
		return nil, fmt.Errorf("vault: random source is required")
	}
	keyCopy := append([]byte(nil), userKey...)
	return &Vault{userKey: keyCopy, rand: random}, nil
}

// Encrypt creates the canonical self-contained envelope:
// [16-byte nonce][encrypted DEK][12-byte IV][ciphertext][16-byte tag].
func (vault *Vault) Encrypt(plaintext []byte) ([]byte, error) {
	vault.mu.RLock()
	defer vault.mu.RUnlock()
	if vault.closed {
		return nil, ErrClosed
	}
	return encryptWithKey(vault.rand, vault.userKey, plaintext)
}

// Decrypt authenticates and decrypts an envelope. All malformed or
// unauthentic envelopes return the same sentinel error.
func (vault *Vault) Decrypt(envelope []byte) ([]byte, error) {
	vault.mu.RLock()
	defer vault.mu.RUnlock()
	if vault.closed {
		return nil, ErrClosed
	}
	return decryptWithKey(vault.userKey, envelope)
}

// RotateUserKey generates a new user key and asks the durable store to
// re-encrypt every DEK in one atomic operation. The in-memory key changes only
// after that operation succeeds.
func (vault *Vault) RotateUserKey(ctx context.Context, rewrapper EnvelopeRewrapper) error {
	if rewrapper == nil {
		return fmt.Errorf("vault: envelope rewrapper is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	vault.mu.Lock()
	defer vault.mu.Unlock()
	if vault.closed {
		return ErrClosed
	}

	newKey := make([]byte, KeySize)
	if _, err := io.ReadFull(vault.rand, newKey); err != nil {
		zero(newKey)
		return fmt.Errorf("vault: generate user key: %w", err)
	}
	defer zero(newKey)

	if err := rewrapper.RewrapEnvelopes(ctx, vault.userKey, newKey); err != nil {
		return fmt.Errorf("vault: rotate user key: %w", err)
	}

	oldKey := vault.userKey
	vault.userKey = append([]byte(nil), newKey...)
	zero(oldKey)
	return nil
}

// Close irreversibly zeroes the user key held by this Vault.
func (vault *Vault) Close() error {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if vault.closed {
		return nil
	}
	zero(vault.userKey)
	vault.closed = true
	return nil
}

func encryptWithKey(random io.Reader, userKey, plaintext []byte) ([]byte, error) {
	if len(userKey) != KeySize {
		return nil, ErrInvalidKey
	}

	dek := make([]byte, KeySize)
	if _, err := io.ReadFull(random, dek); err != nil {
		zero(dek)
		return nil, fmt.Errorf("vault: generate DEK: %w", err)
	}
	defer zero(dek)

	dekNonce := make([]byte, DEKNonceSize)
	if _, err := io.ReadFull(random, dekNonce); err != nil {
		return nil, fmt.Errorf("vault: generate DEK nonce: %w", err)
	}
	wrappedDEK, err := seal(userKey, dekNonce, dek)
	if err != nil {
		return nil, err
	}

	contentIV := make([]byte, ContentIVSize)
	if _, err := io.ReadFull(random, contentIV); err != nil {
		return nil, fmt.Errorf("vault: generate content IV: %w", err)
	}
	ciphertext, err := seal(dek, contentIV, plaintext)
	if err != nil {
		return nil, err
	}

	envelope := make([]byte, 0, envelopeOverhead+len(plaintext))
	envelope = append(envelope, dekNonce...)
	envelope = append(envelope, wrappedDEK...)
	envelope = append(envelope, contentIV...)
	envelope = append(envelope, ciphertext...)
	return envelope, nil
}

func decryptWithKey(userKey, envelope []byte) ([]byte, error) {
	if len(userKey) != KeySize || len(envelope) < envelopeOverhead {
		return nil, ErrDecryptionFailed
	}

	dekNonceEnd := DEKNonceSize
	wrappedDEKEnd := dekNonceEnd + encryptedDEKSize
	contentIVEnd := wrappedDEKEnd + ContentIVSize

	dek, err := open(
		userKey,
		envelope[:dekNonceEnd],
		envelope[dekNonceEnd:wrappedDEKEnd],
	)
	if err != nil || len(dek) != KeySize {
		zero(dek)
		return nil, ErrDecryptionFailed
	}
	defer zero(dek)

	plaintext, err := open(
		dek,
		envelope[wrappedDEKEnd:contentIVEnd],
		envelope[contentIVEnd:],
	)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}

func rewrapWithKeys(oldKey, newKey, envelope []byte) ([]byte, error) {
	return rewrapWithReader(rand.Reader, oldKey, newKey, envelope)
}

func rewrapWithReader(random io.Reader, oldKey, newKey, envelope []byte) ([]byte, error) {
	if len(oldKey) != KeySize || len(newKey) != KeySize || len(envelope) < envelopeOverhead {
		return nil, ErrDecryptionFailed
	}

	dekNonceEnd := DEKNonceSize
	wrappedDEKEnd := dekNonceEnd + encryptedDEKSize
	dek, err := open(oldKey, envelope[:dekNonceEnd], envelope[dekNonceEnd:wrappedDEKEnd])
	if err != nil || len(dek) != KeySize {
		zero(dek)
		return nil, ErrDecryptionFailed
	}
	defer zero(dek)

	newNonce := make([]byte, DEKNonceSize)
	if _, err := io.ReadFull(random, newNonce); err != nil {
		return nil, fmt.Errorf("vault: generate DEK nonce: %w", err)
	}
	wrappedDEK, err := seal(newKey, newNonce, dek)
	if err != nil {
		return nil, err
	}

	result := make([]byte, 0, len(envelope))
	result = append(result, newNonce...)
	result = append(result, wrappedDEK...)
	result = append(result, envelope[wrappedDEKEnd:]...)
	return result, nil
}

// Rewrap replaces only an envelope's encrypted DEK; content ciphertext stays
// byte-identical.
func Rewrap(oldKey, newKey, envelope []byte) ([]byte, error) {
	return rewrapWithKeys(oldKey, newKey, envelope)
}

func seal(key, nonce, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: initialize cipher: %w", err)
	}
	gcm, err := newGCM(block, len(nonce))
	if err != nil {
		return nil, fmt.Errorf("vault: initialize GCM: %w", err)
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nil
}

func open(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(block, len(nonce))
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func newGCM(block cipher.Block, nonceSize int) (cipher.AEAD, error) {
	if nonceSize == ContentIVSize {
		return cipher.NewGCM(block)
	}
	return cipher.NewGCMWithNonceSize(block, nonceSize)
}
