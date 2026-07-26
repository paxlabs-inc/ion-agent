package vault

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileKEKSourceRoundTripAndPermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "kek")
	source, err := NewFileKEKSource(path)
	if err != nil {
		t.Fatalf("NewFileKEKSource() error = %v", err)
	}
	if source.Name() != "development-file" {
		t.Fatalf("Name() = %q", source.Name())
	}
	if _, err := source.Load(context.Background()); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load(missing) error = %v", err)
	}
	key := repeatedKey(0x41)
	if err := source.Store(context.Background(), key); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	loaded, err := source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bytes.Equal(loaded, key) {
		t.Fatal("loaded KEK differs")
	}
}

func TestFileKEKSourceRejectsUnsafeOrInvalidFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	for name, testCase := range map[string]struct {
		data   []byte
		mode   os.FileMode
		target error
	}{
		"unsafe_permissions": {repeatedKey(1), 0o644, nil},
		"short_key":          {[]byte("short"), 0o600, ErrInvalidKey},
	} {
		testCase := testCase
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, testCase.data, testCase.mode); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			if err := os.Chmod(path, testCase.mode); err != nil {
				t.Fatalf("Chmod() error = %v", err)
			}
			source, err := NewFileKEKSource(path)
			if err != nil {
				t.Fatalf("NewFileKEKSource() error = %v", err)
			}
			_, err = source.Load(context.Background())
			if err == nil {
				t.Fatal("Load() succeeded")
			}
			if testCase.target != nil && !errors.Is(err, testCase.target) {
				t.Fatalf("Load() error = %v, want %v", err, testCase.target)
			}
		})
	}
}

func TestFileKEKSourceReadAndAtomicWriteErrors(t *testing.T) {
	t.Parallel()
	directoryPath := filepath.Join(t.TempDir(), "key-as-directory")
	if err := os.Mkdir(directoryPath, 0o600); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	source, err := NewFileKEKSource(directoryPath)
	if err != nil {
		t.Fatalf("NewFileKEKSource() error = %v", err)
	}
	if _, err := source.Load(context.Background()); err == nil {
		t.Fatal("Load(directory) succeeded")
	}
	tooLongPath := filepath.Join(t.TempDir(), string(bytes.Repeat([]byte{'x'}, 5000)))
	tooLong, err := NewFileKEKSource(tooLongPath)
	if err != nil {
		t.Fatalf("NewFileKEKSource(long path) error = %v", err)
	}
	if _, err := tooLong.Load(context.Background()); err == nil || errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load(long path) error = %v", err)
	}

	if err := atomicWriteFile("/dev/null/ion-key", repeatedKey(1), 0o600); err == nil {
		t.Fatal("atomicWriteFile(path below file) succeeded")
	}
	if err := atomicWriteFile("/proc/ion-key", repeatedKey(1), 0o600); err == nil {
		t.Fatal("atomicWriteFile(read-only directory) succeeded")
	}
	targetDirectory := filepath.Join(t.TempDir(), "existing-directory")
	if err := os.Mkdir(targetDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error = %v", err)
	}
	if err := atomicWriteFile(targetDirectory, repeatedKey(1), 0o600); err == nil {
		t.Fatal("atomicWriteFile(over directory) succeeded")
	}
}

func TestFileKEKSourceValidationAndCancellation(t *testing.T) {
	t.Parallel()
	if _, err := NewFileKEKSource(""); err == nil {
		t.Fatal("NewFileKEKSource(empty) succeeded")
	}
	source, err := NewFileKEKSource(filepath.Join(t.TempDir(), "kek"))
	if err != nil {
		t.Fatalf("NewFileKEKSource() error = %v", err)
	}
	if err := source.Store(context.Background(), []byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Store(short) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := source.Store(ctx, repeatedKey(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Store(cancelled) error = %v", err)
	}
	if _, err := source.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
}

func TestNewSecretToolSourceValidation(t *testing.T) {
	for _, input := range [][2]string{{"", "account"}, {"service", ""}} {
		if _, err := NewSecretToolSource(input[0], input[1]); err == nil {
			t.Fatalf("NewSecretToolSource(%q, %q) succeeded", input[0], input[1])
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := NewSecretToolSource("service", "account"); err == nil {
		t.Fatal("NewSecretToolSource(without secret-tool) succeeded")
	}
}
