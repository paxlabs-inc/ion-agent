// Package localipc owns safe creation of local Unix-domain socket listeners.
package localipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Listen reclaims a socket left behind by a crashed process, but never removes
// a live listener or a non-socket filesystem entry.
func Listen(path string) (*net.UnixListener, error) {
	if err := requirePrivateParent(path); err != nil {
		return nil, err
	}
	if err := prepare(path); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("local IPC: listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("local IPC: secure socket %s: %w", path, err)
	}
	return listener, nil
}

// requirePrivateParent makes the bind-to-chmod interval inaccessible to other
// users. Unix creates socket nodes according to the process umask, so chmod
// alone cannot prevent a brief observation window in a shared directory.
func requirePrivateParent(path string) error {
	directory := filepath.Dir(path)
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("local IPC: inspect parent directory %s: %w", directory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local IPC: parent path is not a directory: %s", directory)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf(
			"local IPC: parent directory must not be accessible by group or others: %s",
			directory,
		)
	}
	return nil
}

func prepare(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("local IPC: inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("local IPC: refusing to replace non-socket path %s", path)
	}
	connection, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return fmt.Errorf("local IPC: socket already has a live listener at %s", path)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) &&
		!errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("local IPC: cannot prove socket is stale at %s: %w", path, dialErr)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("local IPC: remove stale socket %s: %w", path, err)
	}
	return nil
}
