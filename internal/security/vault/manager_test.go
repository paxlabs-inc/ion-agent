package vault

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerInitializeOpenAndRoundTrip(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source, err := NewFileKEKSource(filepath.Join(directory, "kek"))
	if err != nil {
		t.Fatalf("NewFileKEKSource() error = %v", err)
	}
	store, err := NewFileWrappedKeyStore(filepath.Join(directory, "user.enc"))
	if err != nil {
		t.Fatalf("NewFileWrappedKeyStore() error = %v", err)
	}
	manager, err := Initialize(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	envelope, err := manager.Vault().Encrypt([]byte("persistent"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer closeManager(t, reopened)
	plaintext, err := reopened.Vault().Decrypt(envelope)
	if err != nil || string(plaintext) != "persistent" {
		t.Fatalf("Decrypt() = %q, %v", plaintext, err)
	}
}

func TestManagerInitializeRefusesOverwrite(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source, _ := NewFileKEKSource(filepath.Join(directory, "kek"))
	store, _ := NewFileWrappedKeyStore(filepath.Join(directory, "user.enc"))
	manager, err := Initialize(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	defer closeManager(t, manager)
	if _, err := Initialize(context.Background(), source, store); err == nil {
		t.Fatal("second Initialize() succeeded")
	}
}

func TestManagerKEKRotation(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	oldSource, _ := NewFileKEKSource(filepath.Join(directory, "old.kek"))
	newSource, _ := NewFileKEKSource(filepath.Join(directory, "new.kek"))
	store, _ := NewFileWrappedKeyStore(filepath.Join(directory, "user.enc"))
	manager, err := Initialize(context.Background(), oldSource, store)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	envelope, err := manager.Vault().Encrypt([]byte("same user key"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if err := manager.RotateKEK(context.Background(), newSource); err != nil {
		t.Fatalf("RotateKEK() error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := Open(context.Background(), oldSource, store); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("Open(old KEK) error = %v, want ErrDecryptionFailed", err)
	}
	reopened, err := Open(context.Background(), newSource, store)
	if err != nil {
		t.Fatalf("Open(new KEK) error = %v", err)
	}
	defer closeManager(t, reopened)
	plaintext, err := reopened.Vault().Decrypt(envelope)
	if err != nil || string(plaintext) != "same user key" {
		t.Fatalf("Decrypt() = %q, %v", plaintext, err)
	}
}

func TestManagerKEKRotationFailurePreservesOldKey(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source, _ := NewFileKEKSource(filepath.Join(directory, "old.kek"))
	store, _ := NewFileWrappedKeyStore(filepath.Join(directory, "user.enc"))
	manager, err := Initialize(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	oldKEK := append([]byte(nil), manager.kek...)
	expected := errors.New("write failure")
	if err := manager.RotateKEK(context.Background(), failingKEKSource{err: expected}); !errors.Is(err, expected) {
		t.Fatalf("RotateKEK() error = %v", err)
	}
	if !bytes.Equal(manager.kek, oldKEK) {
		t.Fatal("manager KEK changed after failed rotation")
	}
	closeManager(t, manager)
	reopened, err := Open(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Open(old key) error = %v", err)
	}
	closeManager(t, reopened)
}

func TestManagerUserKeyRotationPersistsReplacement(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source, _ := NewFileKEKSource(filepath.Join(directory, "kek"))
	store, _ := NewFileWrappedKeyStore(filepath.Join(directory, "user.enc"))
	manager, err := Initialize(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	envelope, err := manager.Vault().Encrypt([]byte("durable rotation"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	rewrapper := &memoryRewrapper{envelopes: [][]byte{envelope}}
	if err := manager.RotateUserKey(context.Background(), rewrapper); err != nil {
		t.Fatalf("RotateUserKey() error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer closeManager(t, reopened)
	plaintext, err := reopened.Vault().Decrypt(rewrapper.envelopes[0])
	if err != nil || string(plaintext) != "durable rotation" {
		t.Fatalf("Decrypt(rotated) = %q, %v", plaintext, err)
	}
}

func TestManagerUserKeyRotationPersistenceFailureRollsBack(t *testing.T) {
	t.Parallel()
	source := &memoryKEKSource{key: repeatedKey(8)}
	wrapped, err := wrapUserKey(source.key, repeatedKey(9))
	if err != nil {
		t.Fatalf("wrapUserKey() error = %v", err)
	}
	expected := errors.New("persistence failed")
	store := &memoryWrappedStore{content: wrapped, storeErr: expected}
	manager, err := Open(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer closeManager(t, manager)
	envelope, err := manager.Vault().Encrypt([]byte("rolls back"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	rewrapper := &memoryRewrapper{envelopes: [][]byte{envelope}}
	if err := manager.RotateUserKey(context.Background(), rewrapper); !errors.Is(err, expected) {
		t.Fatalf("RotateUserKey() error = %v", err)
	}
	plaintext, err := manager.Vault().Decrypt(rewrapper.envelopes[0])
	if err != nil || string(plaintext) != "rolls back" {
		t.Fatalf("Decrypt(rolled back) = %q, %v", plaintext, err)
	}
}

func TestManagerUserKeyRotationValidationAndFailures(t *testing.T) {
	t.Parallel()
	source := &memoryKEKSource{key: repeatedKey(10)}
	wrapped, err := wrapUserKey(source.key, repeatedKey(11))
	if err != nil {
		t.Fatalf("wrapUserKey() error = %v", err)
	}
	manager, err := Open(
		context.Background(),
		source,
		&memoryWrappedStore{content: wrapped},
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := manager.RotateUserKey(context.Background(), nil); err == nil {
		t.Fatal("RotateUserKey(nil) succeeded")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.RotateUserKey(cancelled, &memoryRewrapper{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("RotateUserKey(cancelled) error = %v", err)
	}
	manager.random = &limitedErrorReader{}
	if err := manager.RotateUserKey(context.Background(), &memoryRewrapper{}); err == nil {
		t.Fatal("RotateUserKey(key random failure) succeeded")
	}
	manager.random = &limitedErrorReader{remaining: KeySize}
	if err := manager.RotateUserKey(context.Background(), &memoryRewrapper{}); err == nil {
		t.Fatal("RotateUserKey(IV random failure) succeeded")
	}
	manager.random = bytes.NewReader(bytes.Repeat([]byte{1}, 128))
	expected := errors.New("rewrap failed")
	if err := manager.RotateUserKey(
		context.Background(),
		&memoryRewrapper{err: expected},
	); !errors.Is(err, expected) {
		t.Fatalf("RotateUserKey(rewrap failure) error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := manager.RotateUserKey(context.Background(), &memoryRewrapper{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("RotateUserKey(closed) error = %v", err)
	}
}

func TestManagerValidationAndCloseZeroization(t *testing.T) {
	t.Parallel()
	if _, err := NewFileWrappedKeyStore(""); err == nil {
		t.Fatal("NewFileWrappedKeyStore(empty) succeeded")
	}
	if _, err := Initialize(context.Background(), nil, nil); err == nil {
		t.Fatal("Initialize(nil) succeeded")
	}
	if _, err := Open(context.Background(), nil, nil); err == nil {
		t.Fatal("Open(nil) succeeded")
	}

	directory := t.TempDir()
	source, _ := NewFileKEKSource(filepath.Join(directory, "kek"))
	store, _ := NewFileWrappedKeyStore(filepath.Join(directory, "user.enc"))
	manager, err := Initialize(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	ownedKEK := manager.kek
	if err := manager.RotateKEK(context.Background(), nil); err == nil {
		t.Fatal("RotateKEK(nil) succeeded")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !bytes.Equal(ownedKEK, make([]byte, KeySize)) {
		t.Fatal("Close() did not zero KEK")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := manager.RotateKEK(context.Background(), source); !errors.Is(err, ErrClosed) {
		t.Fatalf("RotateKEK(closed) error = %v", err)
	}
}

func TestWrapUserKeyValidationAndAuthentication(t *testing.T) {
	t.Parallel()
	if _, err := wrapUserKey([]byte("short"), repeatedKey(1)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("wrapUserKey(short) error = %v", err)
	}
	wrapped, err := wrapUserKey(repeatedKey(2), repeatedKey(3))
	if err != nil {
		t.Fatalf("wrapUserKey() error = %v", err)
	}
	for name, testCase := range map[string]struct {
		key     []byte
		content []byte
	}{
		"wrong_key":  {repeatedKey(4), wrapped},
		"short_key":  {[]byte("short"), wrapped},
		"short_blob": {repeatedKey(2), wrapped[:len(wrapped)-1]},
		"tampered":   {repeatedKey(2), mutateByte(wrapped, len(wrapped)-1)},
	} {
		testCase := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := unwrapUserKey(testCase.key, testCase.content); !errors.Is(err, ErrDecryptionFailed) {
				t.Fatalf("unwrapUserKey() error = %v", err)
			}
		})
	}
}

func TestLoadOrCreateKEKPaths(t *testing.T) {
	t.Parallel()
	expected := errors.New("source failure")
	cases := map[string]struct {
		source *memoryKEKSource
		target error
	}{
		"existing": {
			source: &memoryKEKSource{key: repeatedKey(1)},
		},
		"invalid_existing": {
			source: &memoryKEKSource{key: []byte("short")},
			target: ErrInvalidKey,
		},
		"load_failure": {
			source: &memoryKEKSource{loadErr: expected},
			target: expected,
		},
		"store_failure": {
			source: &memoryKEKSource{loadErr: ErrKeyNotFound, storeErr: expected},
			target: expected,
		},
		"create": {
			source: &memoryKEKSource{loadErr: ErrKeyNotFound},
		},
	}
	for name, testCase := range cases {
		testCase := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			key, err := loadOrCreateKEK(
				context.Background(),
				testCase.source,
				bytes.NewReader(make([]byte, KeySize)),
			)
			if testCase.target != nil {
				if !errors.Is(err, testCase.target) {
					t.Fatalf("loadOrCreateKEK() error = %v, want %v", err, testCase.target)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadOrCreateKEK() error = %v", err)
			}
			defer zero(key)
			if len(key) != KeySize {
				t.Fatalf("key length = %d", len(key))
			}
		})
	}
}

func TestManagerBoundaryFailures(t *testing.T) {
	t.Parallel()
	expected := errors.New("boundary failure")
	validSource := &memoryKEKSource{key: repeatedKey(2)}
	validWrapped, err := wrapUserKey(validSource.key, repeatedKey(3))
	if err != nil {
		t.Fatalf("wrapUserKey() error = %v", err)
	}
	cases := map[string]func() error{
		"initialize_load": func() error {
			_, initializeErr := Initialize(
				context.Background(),
				validSource,
				&memoryWrappedStore{loadErr: expected},
			)
			return initializeErr
		},
		"initialize_store": func() error {
			_, initializeErr := Initialize(
				context.Background(),
				validSource,
				&memoryWrappedStore{loadErr: ErrKeyNotFound, storeErr: expected},
			)
			return initializeErr
		},
		"initialize_kek_source": func() error {
			_, initializeErr := Initialize(
				context.Background(),
				&memoryKEKSource{loadErr: expected},
				&memoryWrappedStore{loadErr: ErrKeyNotFound},
			)
			return initializeErr
		},
		"open_source": func() error {
			_, openErr := Open(
				context.Background(),
				&memoryKEKSource{loadErr: expected},
				&memoryWrappedStore{content: validWrapped},
			)
			return openErr
		},
		"open_short_kek": func() error {
			_, openErr := Open(
				context.Background(),
				&memoryKEKSource{key: []byte("short")},
				&memoryWrappedStore{content: validWrapped},
			)
			return openErr
		},
		"open_store": func() error {
			_, openErr := Open(
				context.Background(),
				validSource,
				&memoryWrappedStore{loadErr: expected},
			)
			return openErr
		},
		"open_corrupt": func() error {
			_, openErr := Open(
				context.Background(),
				validSource,
				&memoryWrappedStore{content: make([]byte, wrappedUserKeySize)},
			)
			return openErr
		},
		"new_manager_short_user_key": func() error {
			_, managerErr := newManager(
				repeatedKey(1),
				validSource,
				&memoryWrappedStore{},
				[]byte("short"),
				bytes.NewReader(make([]byte, KeySize)),
			)
			return managerErr
		},
	}
	for name, operation := range cases {
		operation := operation
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); err == nil {
				t.Fatal("operation succeeded")
			}
		})
	}
}

func TestManagerRandomFailures(t *testing.T) {
	t.Parallel()
	source := &memoryKEKSource{key: repeatedKey(6)}
	missingStore := &memoryWrappedStore{loadErr: ErrKeyNotFound}

	if _, err := initializeWithReader(
		context.Background(),
		source,
		missingStore,
		&limitedErrorReader{},
	); err == nil {
		t.Fatal("Initialize(User Key random failure) succeeded")
	}
	if _, err := initializeWithReader(
		context.Background(),
		source,
		missingStore,
		&limitedErrorReader{remaining: KeySize},
	); err == nil {
		t.Fatal("Initialize(User Key IV random failure) succeeded")
	}
	if _, err := initializeWithReader(
		context.Background(),
		source,
		missingStore,
		nil,
	); err == nil {
		t.Fatal("Initialize(nil random source) succeeded")
	}

	missingSource := &memoryKEKSource{loadErr: ErrKeyNotFound}
	if _, err := loadOrCreateKEK(
		context.Background(),
		missingSource,
		&limitedErrorReader{},
	); err == nil {
		t.Fatal("loadOrCreateKEK(random failure) succeeded")
	}
	if _, err := wrapUserKeyWithReader(
		&limitedErrorReader{},
		repeatedKey(1),
		repeatedKey(2),
	); err == nil {
		t.Fatal("wrapUserKeyWithReader(random failure) succeeded")
	}

	wrapped, err := wrapUserKey(source.key, repeatedKey(7))
	if err != nil {
		t.Fatalf("wrapUserKey() error = %v", err)
	}
	manager, err := Open(
		context.Background(),
		source,
		&memoryWrappedStore{content: wrapped},
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer closeManager(t, manager)
	manager.random = &limitedErrorReader{}
	if err := manager.RotateKEK(context.Background(), &memoryKEKSource{}); err == nil {
		t.Fatal("RotateKEK(KEK random failure) succeeded")
	}
	manager.random = &limitedErrorReader{remaining: KeySize}
	if err := manager.RotateKEK(context.Background(), &memoryKEKSource{}); err == nil {
		t.Fatal("RotateKEK(wrapped key random failure) succeeded")
	}
}

func TestManagerRotationWrappedStoreFailureAndClosedVault(t *testing.T) {
	t.Parallel()
	expected := errors.New("wrapped store failure")
	source := &memoryKEKSource{key: repeatedKey(4)}
	wrapped, err := wrapUserKey(source.key, repeatedKey(5))
	if err != nil {
		t.Fatalf("wrapUserKey() error = %v", err)
	}
	store := &memoryWrappedStore{content: wrapped, storeErr: expected}
	manager, err := Open(context.Background(), source, store)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	oldKEK := append([]byte(nil), manager.kek...)
	if err := manager.RotateKEK(context.Background(), &memoryKEKSource{}); !errors.Is(err, expected) {
		t.Fatalf("RotateKEK(store failure) error = %v", err)
	}
	if !bytes.Equal(manager.kek, oldKEK) {
		t.Fatal("KEK changed after wrapped store failure")
	}
	store.storeErr = nil
	if err := manager.vault.Close(); err != nil {
		t.Fatalf("Vault.Close() error = %v", err)
	}
	if err := manager.RotateKEK(context.Background(), &memoryKEKSource{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("RotateKEK(closed vault) error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Manager.Close() error = %v", err)
	}
}

func TestFileWrappedKeyStoreCancellationAndMissing(t *testing.T) {
	t.Parallel()
	store, err := NewFileWrappedKeyStore(filepath.Join(t.TempDir(), "wrapped"))
	if err != nil {
		t.Fatalf("NewFileWrappedKeyStore() error = %v", err)
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load(missing) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Load(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
	if err := store.Store(cancelled, []byte("content")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Store(cancelled) error = %v", err)
	}
}

func TestFileWrappedKeyStoreReadError(t *testing.T) {
	t.Parallel()
	directoryPath := filepath.Join(t.TempDir(), "wrapped-directory")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	store, err := NewFileWrappedKeyStore(directoryPath)
	if err != nil {
		t.Fatalf("NewFileWrappedKeyStore() error = %v", err)
	}
	if _, err := store.Load(context.Background()); err == nil {
		t.Fatal("Load(directory) succeeded")
	}
}

type failingKEKSource struct {
	err error
}

type memoryKEKSource struct {
	key      []byte
	loadErr  error
	storeErr error
}

func (source *memoryKEKSource) Load(context.Context) ([]byte, error) {
	if source.loadErr != nil {
		return nil, source.loadErr
	}
	return append([]byte(nil), source.key...), nil
}

func (source *memoryKEKSource) Store(_ context.Context, key []byte) error {
	if source.storeErr != nil {
		return source.storeErr
	}
	source.key = append([]byte(nil), key...)
	source.loadErr = nil
	return nil
}

func (*memoryKEKSource) Name() string {
	return "memory-test-boundary"
}

type memoryWrappedStore struct {
	content  []byte
	loadErr  error
	storeErr error
}

func (store *memoryWrappedStore) Load(context.Context) ([]byte, error) {
	if store.loadErr != nil {
		return nil, store.loadErr
	}
	return append([]byte(nil), store.content...), nil
}

func (store *memoryWrappedStore) Store(_ context.Context, content []byte) error {
	if store.storeErr != nil {
		return store.storeErr
	}
	store.content = append([]byte(nil), content...)
	store.loadErr = nil
	return nil
}

func (source failingKEKSource) Load(context.Context) ([]byte, error) {
	return nil, source.err
}

func (source failingKEKSource) Store(context.Context, []byte) error {
	return source.err
}

func (failingKEKSource) Name() string {
	return "failing-test-boundary"
}

func closeManager(t *testing.T, manager *Manager) {
	t.Helper()
	if err := manager.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
