// Package sandbox confines file access beneath a pre-opened directory.
//
// It walks every path component with openat(O_NOFOLLOW), so traversal and
// symlink swaps cannot redirect a read or write outside the sandbox root.
package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const DefaultMaxReadBytes = 16 << 20

var (
	ErrInvalidPath = errors.New("sandbox: invalid relative path")
	ErrEscape      = errors.New("sandbox: path escapes root")
	ErrTooLarge    = errors.New("sandbox: file exceeds read limit")
	ErrClosed      = errors.New("sandbox: closed")
)

// Files owns a stable file descriptor for the root directory.
type Files struct {
	mu       sync.RWMutex
	rootFD   int
	maxRead  int64
	closed   bool
	rootPath string
}

// Open creates a filesystem sandbox rooted at an existing directory.
func Open(root string, maxReadBytes int64) (*Files, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("sandbox: root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("sandbox: resolve root: %w", err)
	}
	fd, err := unix.Open(absolute, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("sandbox: open root: %w", err)
	}
	if maxReadBytes <= 0 {
		maxReadBytes = DefaultMaxReadBytes
	}
	return &Files{rootFD: fd, maxRead: maxReadBytes, rootPath: absolute}, nil
}

// Root returns the canonical root for audit display only. Authorization never
// relies on this string after Open succeeds.
func (files *Files) Root() string {
	files.mu.RLock()
	defer files.mu.RUnlock()
	return files.rootPath
}

// ReadFile reads a regular file without following any symlink.
func (files *Files) ReadFile(relative string) ([]byte, error) {
	parts, err := split(relative)
	if err != nil {
		return nil, err
	}
	files.mu.RLock()
	defer files.mu.RUnlock()
	if files.closed {
		return nil, ErrClosed
	}
	parent, closeParent, err := walkParent(files.rootFD, parts)
	if err != nil {
		return nil, err
	}
	defer closeParent()
	fd, err := unix.Openat(
		parent,
		parts[len(parts)-1],
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, classifyPathError(err)
	}
	file := os.NewFile(uintptr(fd), relative)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("sandbox: wrap file descriptor")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("sandbox: stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: target is not a regular file", ErrInvalidPath)
	}
	content, err := io.ReadAll(io.LimitReader(file, files.maxRead+1))
	if err != nil {
		return nil, fmt.Errorf("sandbox: read file: %w", err)
	}
	if int64(len(content)) > files.maxRead {
		return nil, ErrTooLarge
	}
	return content, nil
}

// WriteFile atomically replaces a file beneath the root. Parent directories
// must already exist and may not contain symlinks.
func (files *Files) WriteFile(relative string, content []byte) error {
	parts, err := split(relative)
	if err != nil {
		return err
	}
	files.mu.RLock()
	defer files.mu.RUnlock()
	if files.closed {
		return ErrClosed
	}
	parent, closeParent, err := walkParent(files.rootFD, parts)
	if err != nil {
		return err
	}
	defer closeParent()
	var existing unix.Stat_t
	target := parts[len(parts)-1]
	switch err := unix.Fstatat(parent, target, &existing, unix.AT_SYMLINK_NOFOLLOW); {
	case err == nil && existing.Mode&unix.S_IFMT != unix.S_IFREG:
		return fmt.Errorf("%w: write target is not a regular file", ErrEscape)
	case err != nil && !errors.Is(err, unix.ENOENT):
		return classifyPathError(err)
	}

	tempName, err := randomTempName()
	if err != nil {
		return err
	}
	fd, err := unix.Openat(
		parent,
		tempName,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("sandbox: create temporary file: %w", err)
	}
	tempExists := true
	defer func() {
		if tempExists {
			_ = unix.Unlinkat(parent, tempName, 0)
		}
	}()
	file := os.NewFile(uintptr(fd), tempName)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("sandbox: wrap temporary file descriptor")
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("sandbox: write temporary file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sandbox: sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("sandbox: close temporary file: %w", err)
	}
	if err := unix.Renameat(parent, tempName, parent, target); err != nil {
		return classifyPathError(err)
	}
	tempExists = false
	if err := unix.Fsync(parent); err != nil {
		return fmt.Errorf("sandbox: sync parent directory: %w", err)
	}
	return nil
}

// Close revokes future access and closes the root descriptor.
func (files *Files) Close() error {
	files.mu.Lock()
	defer files.mu.Unlock()
	if files.closed {
		return nil
	}
	files.closed = true
	if err := unix.Close(files.rootFD); err != nil {
		return fmt.Errorf("sandbox: close root: %w", err)
	}
	files.rootFD = -1
	return nil
}

func split(relative string) ([]string, error) {
	if relative == "" || strings.IndexByte(relative, 0) >= 0 ||
		filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return nil, ErrInvalidPath
	}
	normalized := filepath.ToSlash(relative)
	parts := strings.Split(normalized, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			if part == ".." {
				return nil, ErrEscape
			}
			return nil, ErrInvalidPath
		}
	}
	return parts, nil
}

func walkParent(rootFD int, parts []string) (int, func(), error) {
	if len(parts) == 1 {
		return rootFD, func() {}, nil
	}
	current := rootFD
	owned := false
	closeCurrent := func() {
		if owned {
			_ = unix.Close(current)
		}
	}
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(
			current,
			part,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0,
		)
		if err != nil {
			closeCurrent()
			return -1, func() {}, classifyPathError(err)
		}
		if owned {
			_ = unix.Close(current)
		}
		current = next
		owned = true
	}
	return current, closeCurrent, nil
}

func randomTempName() (string, error) {
	var nonce [16]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", fmt.Errorf("sandbox: generate temporary name: %w", err)
	}
	return ".ion-" + hex.EncodeToString(nonce[:]), nil
}

func classifyPathError(err error) error {
	switch {
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
		return fmt.Errorf("%w: symlink traversal", ErrEscape)
	case errors.Is(err, unix.ENOTDIR):
		return fmt.Errorf("%w: non-directory component", ErrEscape)
	default:
		return fmt.Errorf("sandbox: file operation: %w", err)
	}
}
