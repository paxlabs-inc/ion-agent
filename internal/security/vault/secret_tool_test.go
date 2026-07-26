package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// These tests use a real subprocess as the documented external boundary for
// libsecret's secret-tool CLI.
func TestSecretToolSourceLoadAndStore(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	capturePath := filepath.Join(directory, "stdin")
	key := repeatedKey(0x5a)
	encoded := base64.StdEncoding.EncodeToString(key)
	script := writeExecutable(t, directory, "secret-tool", "#!/bin/sh\n"+
		"if [ \"$1\" = \"lookup\" ]; then\n"+
		"  printf '"+encoded+"\\n'\n"+
		"  exit 0\n"+
		"fi\n"+
		"/bin/cat > '"+capturePath+"'\n")
	source := &SecretToolSource{service: "ion", account: "test", path: script}
	if source.Name() != "libsecret" {
		t.Fatalf("Name() = %q", source.Name())
	}
	loaded, err := source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bytes.Equal(loaded, key) {
		t.Fatal("loaded key differs")
	}
	if err := source.Store(context.Background(), key); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("ReadFile(capture) error = %v", err)
	}
	if !bytes.Equal(captured, []byte(encoded)) {
		t.Fatalf("captured stdin = %q", captured)
	}
}

func TestSecretToolSourceFailures(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	notFound := writeExecutable(t, directory, "not-found", "#!/bin/sh\nexit 1\n")
	invalid := writeExecutable(t, directory, "invalid", "#!/bin/sh\nprintf 'not-base64\\n'\n")
	failing := writeExecutable(t, directory, "failing", "#!/bin/sh\nprintf 'failure\\n' >&2\nexit 2\n")

	source := &SecretToolSource{service: "x", account: "y", path: notFound}
	if _, err := source.Load(context.Background()); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load(not found) error = %v", err)
	}
	source.path = invalid
	if _, err := source.Load(context.Background()); err == nil {
		t.Fatal("Load(invalid base64) succeeded")
	}
	source.path = failing
	if _, err := source.Load(context.Background()); err == nil || errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load(process failure) error = %v", err)
	}
	if err := source.Store(context.Background(), []byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Store(short) error = %v", err)
	}
	if err := source.Store(context.Background(), repeatedKey(1)); err == nil {
		t.Fatal("Store(process failure) succeeded")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Load(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
	if err := source.Store(cancelled, repeatedKey(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Store(cancelled) error = %v", err)
	}
}

func TestNewPlatformKEKSourceUsesSecretToolOnLinux(t *testing.T) {
	directory := t.TempDir()
	writeExecutable(t, directory, "secret-tool", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", directory)
	source, err := NewPlatformKEKSource("ion", "account")
	if err != nil {
		t.Fatalf("NewPlatformKEKSource() error = %v", err)
	}
	if source.Name() != "libsecret" {
		t.Fatalf("source.Name() = %q", source.Name())
	}
}

func writeExecutable(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("WriteFile(script) error = %v", err)
	}
	return path
}
