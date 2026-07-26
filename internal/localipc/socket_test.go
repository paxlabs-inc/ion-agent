//go:build !windows

package localipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenReclaimsOnlyStaleSockets(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(directory, "stale.sock")
	stale, err := net.ListenUnix(
		"unix",
		&net.UnixAddr{Name: stalePath, Net: "unix"},
	)
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := Listen(stalePath)
	if err != nil {
		t.Fatalf("Listen(stale) error = %v", err)
	}
	defer recovered.Close()

	if _, err := Listen(stalePath); err == nil {
		t.Fatal("Listen(active) succeeded")
	}
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("active socket path was removed: %v", err)
	}
	connection, err := net.Dial("unix", stalePath)
	if err != nil {
		t.Fatalf("active listener was disrupted: %v", err)
	}
	_ = connection.Close()

	filePath := filepath.Join(directory, "keep.txt")
	if err := os.WriteFile(filePath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(filePath); err == nil {
		t.Fatal("Listen(regular file) succeeded")
	}
	content, err := os.ReadFile(filePath)
	if err != nil || string(content) != "keep" {
		t.Fatalf("regular file changed: content=%q error=%v", content, err)
	}
}

func TestListenCreatesPrivateSocketInPrivateDirectory(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "private.sock")
	listener, err := Listen(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}
}

func TestListenRejectsSharedParentDirectory(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(directory, "unsafe.sock")
	if _, err := Listen(socketPath); err == nil {
		t.Fatal("Listen(shared parent) succeeded")
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("unsafe socket path exists: %v", err)
	}
}
