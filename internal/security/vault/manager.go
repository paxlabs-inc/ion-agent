package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const wrappedUserKeySize = ContentIVSize + KeySize + AuthenticationTagSize

// WrappedKeyStore durably stores the User Key encrypted by the KEK.
type WrappedKeyStore interface {
	Load(context.Context) ([]byte, error)
	Store(context.Context, []byte) error
}

// FileWrappedKeyStore atomically stores an encrypted User Key.
type FileWrappedKeyStore struct {
	path string
}

// NewFileWrappedKeyStore creates a wrapped-key store.
func NewFileWrappedKeyStore(path string) (*FileWrappedKeyStore, error) {
	if path == "" {
		return nil, fmt.Errorf("vault: wrapped-key path is required")
	}
	return &FileWrappedKeyStore{path: filepath.Clean(path)}, nil
}

func (store *FileWrappedKeyStore) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("vault: read wrapped User Key: %w", err)
	}
	return content, nil
}

func (store *FileWrappedKeyStore) Store(ctx context.Context, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return atomicWriteFile(store.path, content, 0o600)
}

// Manager owns the complete KEK -> User Key lifecycle.
type Manager struct {
	mu       sync.Mutex
	kek      []byte
	source   KEKSource
	keyStore WrappedKeyStore
	vault    *Vault
	random   io.Reader
	closed   bool
}

// Initialize creates a new KEK and User Key. It refuses to overwrite existing
// key material.
func Initialize(ctx context.Context, source KEKSource, keyStore WrappedKeyStore) (*Manager, error) {
	return InitializeForUser(ctx, source, keyStore, "default")
}

// InitializeForUser derives the initial User Key with HKDF-SHA256 using a
// fresh per-user salt, then persists only its KEK-wrapped form.
func InitializeForUser(
	ctx context.Context,
	source KEKSource,
	keyStore WrappedKeyStore,
	userID string,
) (*Manager, error) {
	return initializeForUserWithReader(ctx, source, keyStore, userID, rand.Reader)
}

func initializeWithReader(
	ctx context.Context,
	source KEKSource,
	keyStore WrappedKeyStore,
	random io.Reader,
) (*Manager, error) {
	return initializeForUserWithReader(ctx, source, keyStore, "default", random)
}

func initializeForUserWithReader(
	ctx context.Context,
	source KEKSource,
	keyStore WrappedKeyStore,
	userID string,
	random io.Reader,
) (*Manager, error) {
	if source == nil || keyStore == nil {
		return nil, fmt.Errorf("vault: KEK source and wrapped-key store are required")
	}
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("vault: user identity is required")
	}
	if random == nil {
		return nil, fmt.Errorf("vault: random source is required")
	}
	if _, err := keyStore.Load(ctx); err == nil {
		return nil, fmt.Errorf("vault: wrapped User Key already exists")
	} else if !errors.Is(err, ErrKeyNotFound) {
		return nil, err
	}

	kek, err := loadOrCreateKEK(ctx, source, random)
	if err != nil {
		return nil, err
	}
	defer zero(kek)

	salt := make([]byte, UserSaltSize)
	if _, err := io.ReadFull(random, salt); err != nil {
		zero(salt)
		return nil, fmt.Errorf("vault: generate per-user salt: %w", err)
	}
	userKey, err := DeriveUserKey(kek, userID, salt)
	zero(salt)
	if err != nil {
		return nil, err
	}
	defer zero(userKey)

	wrapped, err := wrapUserKeyWithReader(random, kek, userKey)
	if err != nil {
		return nil, err
	}
	defer zero(wrapped)
	if err := keyStore.Store(ctx, wrapped); err != nil {
		return nil, fmt.Errorf("vault: persist wrapped User Key: %w", err)
	}
	return newManager(kek, source, keyStore, userKey, random)
}

// Open unlocks an existing User Key using the configured KEK source.
func Open(ctx context.Context, source KEKSource, keyStore WrappedKeyStore) (*Manager, error) {
	if source == nil || keyStore == nil {
		return nil, fmt.Errorf("vault: KEK source and wrapped-key store are required")
	}
	kek, err := source.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("vault: load KEK from %s: %w", source.Name(), err)
	}
	defer zero(kek)
	if len(kek) != KeySize {
		return nil, ErrInvalidKey
	}

	wrapped, err := keyStore.Load(ctx)
	if err != nil {
		return nil, err
	}
	defer zero(wrapped)
	userKey, err := unwrapUserKey(kek, wrapped)
	if err != nil {
		return nil, err
	}
	defer zero(userKey)
	return newManager(kek, source, keyStore, userKey, rand.Reader)
}

func newManager(
	kek []byte,
	source KEKSource,
	keyStore WrappedKeyStore,
	userKey []byte,
	random io.Reader,
) (*Manager, error) {
	managedVault, err := New(userKey)
	if err != nil {
		return nil, err
	}
	return &Manager{
		kek:      append([]byte(nil), kek...),
		source:   source,
		keyStore: keyStore,
		vault:    managedVault,
		random:   random,
	}, nil
}

func loadOrCreateKEK(ctx context.Context, source KEKSource, random io.Reader) ([]byte, error) {
	kek, err := source.Load(ctx)
	if err == nil {
		if len(kek) != KeySize {
			zero(kek)
			return nil, ErrInvalidKey
		}
		return kek, nil
	}
	if !errors.Is(err, ErrKeyNotFound) {
		return nil, fmt.Errorf("vault: load KEK from %s: %w", source.Name(), err)
	}

	kek = make([]byte, KeySize)
	if _, err := io.ReadFull(random, kek); err != nil {
		zero(kek)
		return nil, fmt.Errorf("vault: generate KEK: %w", err)
	}
	if err := source.Store(ctx, kek); err != nil {
		zero(kek)
		return nil, fmt.Errorf("vault: store KEK in %s: %w", source.Name(), err)
	}
	return kek, nil
}

// Vault returns the object-encryption service owned by the Manager.
func (manager *Manager) Vault() *Vault {
	return manager.vault
}

// RotateKEK re-encrypts only the User Key. The old KEK remains active if
// durable storage of the new wrapped key fails.
func (manager *Manager) RotateKEK(ctx context.Context, newSource KEKSource) error {
	if newSource == nil {
		return fmt.Errorf("vault: new KEK source is required")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ErrClosed
	}

	newKEK := make([]byte, KeySize)
	if _, err := io.ReadFull(manager.random, newKEK); err != nil {
		zero(newKEK)
		return fmt.Errorf("vault: generate replacement KEK: %w", err)
	}
	defer zero(newKEK)
	if err := newSource.Store(ctx, newKEK); err != nil {
		return fmt.Errorf("vault: store replacement KEK: %w", err)
	}

	manager.vault.mu.RLock()
	if manager.vault.closed {
		manager.vault.mu.RUnlock()
		return ErrClosed
	}
	userKey := append([]byte(nil), manager.vault.userKey...)
	manager.vault.mu.RUnlock()
	defer zero(userKey)

	wrapped, err := wrapUserKeyWithReader(manager.random, newKEK, userKey)
	if err != nil {
		return err
	}
	defer zero(wrapped)
	if err := manager.keyStore.Store(ctx, wrapped); err != nil {
		return fmt.Errorf("vault: atomically store rotated User Key: %w", err)
	}

	oldKEK := manager.kek
	manager.kek = append([]byte(nil), newKEK...)
	manager.source = newSource
	zero(oldKEK)
	return nil
}

// RotateUserKey atomically rewraps every durable object DEK and persists the
// replacement User Key under the current KEK. If key persistence fails, the
// object transaction is reversed before this method returns.
func (manager *Manager) RotateUserKey(ctx context.Context, rewrapper EnvelopeRewrapper) error {
	if rewrapper == nil {
		return fmt.Errorf("vault: envelope rewrapper is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ErrClosed
	}
	manager.vault.mu.Lock()
	defer manager.vault.mu.Unlock()
	if manager.vault.closed {
		return ErrClosed
	}

	newKey := make([]byte, KeySize)
	if _, err := io.ReadFull(manager.random, newKey); err != nil {
		zero(newKey)
		return fmt.Errorf("vault: generate replacement User Key: %w", err)
	}
	defer zero(newKey)
	wrapped, err := wrapUserKeyWithReader(manager.random, manager.kek, newKey)
	if err != nil {
		return err
	}
	defer zero(wrapped)

	if err := rewrapper.RewrapEnvelopes(ctx, manager.vault.userKey, newKey); err != nil {
		return fmt.Errorf("vault: rewrap object keys: %w", err)
	}
	if err := manager.keyStore.Store(ctx, wrapped); err != nil {
		rollbackErr := rewrapper.RewrapEnvelopes(ctx, newKey, manager.vault.userKey)
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("vault: persist replacement User Key: %w", err),
				fmt.Errorf("vault: rollback object keys: %w", rollbackErr),
			)
		}
		return fmt.Errorf("vault: persist replacement User Key: %w", err)
	}

	oldKey := manager.vault.userKey
	manager.vault.userKey = append([]byte(nil), newKey...)
	zero(oldKey)
	return nil
}

// Close zeroes KEK and User Key material. It is idempotent.
func (manager *Manager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil
	}
	if err := manager.vault.Close(); err != nil {
		return err
	}
	zero(manager.kek)
	manager.closed = true
	return nil
}

func wrapUserKey(kek, userKey []byte) ([]byte, error) {
	return wrapUserKeyWithReader(rand.Reader, kek, userKey)
}

func wrapUserKeyWithReader(random io.Reader, kek, userKey []byte) ([]byte, error) {
	if len(kek) != KeySize || len(userKey) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("vault: initialize KEK cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("vault: initialize KEK GCM: %w", err)
	}
	iv := make([]byte, ContentIVSize)
	if _, err := io.ReadFull(random, iv); err != nil {
		return nil, fmt.Errorf("vault: generate User Key IV: %w", err)
	}
	wrapped := make([]byte, 0, wrappedUserKeySize)
	wrapped = append(wrapped, iv...)
	wrapped = gcm.Seal(wrapped, iv, userKey, nil)
	return wrapped, nil
}

func unwrapUserKey(kek, wrapped []byte) ([]byte, error) {
	if len(kek) != KeySize || len(wrapped) != wrappedUserKeySize {
		return nil, ErrDecryptionFailed
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	userKey, err := gcm.Open(nil, wrapped[:ContentIVSize], wrapped[ContentIVSize:], nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return userKey, nil
}
