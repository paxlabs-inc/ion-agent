// Package identity loads the immutable-per-turn SOUL.md identity anchor.
package identity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxSoulBytes = 64 << 10

type File struct {
	path string
}

// Bootstrap creates a secure production identity root and default SOUL.md only
// when no identity exists. Existing files are never rewritten.
func Bootstrap(root string, defaultSoul string) (*File, error) {
	root = strings.TrimSpace(root)
	defaultSoul = strings.TrimSpace(defaultSoul)
	if root == "" || defaultSoul == "" || len(defaultSoul) > maxSoulBytes {
		return nil, fmt.Errorf("identity: bounded root and default SOUL are required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("identity: create root: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("identity: root must be a private regular directory")
	}
	path := filepath.Join(absolute, "SOUL.md")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := file.WriteString(defaultSoul + "\n"); writeErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("identity: bootstrap SOUL.md: %w", writeErr)
		}
		if syncErr := file.Sync(); syncErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("identity: sync SOUL.md: %w", syncErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(path)
			return nil, fmt.Errorf("identity: close SOUL.md: %w", closeErr)
		}
	} else if !os.IsExist(err) {
		return nil, fmt.Errorf("identity: create SOUL.md: %w", err)
	}
	result := &File{path: path}
	if _, err := result.Load(context.Background()); err != nil {
		return nil, err
	}
	return result, nil
}

func NewFile(root string) (*File, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("identity: root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("identity: root must be an existing directory")
	}
	return &File{path: filepath.Join(absolute, "SOUL.md")}, nil
}

// Load reads the file on every call so every session, channel, and interaction
// observes the current approved identity. Symlinks and broad permissions fail
// closed.
func (file *File) Load(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(file.path)
	if err != nil {
		return "", fmt.Errorf("identity: inspect SOUL.md: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("identity: SOUL.md must be a regular file")
	}
	if info.Size() <= 0 || info.Size() > maxSoulBytes {
		return "", fmt.Errorf("identity: SOUL.md size is outside bounds")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("identity: SOUL.md must not be group/world accessible")
	}
	payload, err := os.ReadFile(file.path)
	if err != nil {
		return "", fmt.Errorf("identity: read SOUL.md: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	soul := strings.TrimSpace(string(payload))
	if soul == "" {
		return "", fmt.Errorf("identity: SOUL.md is empty")
	}
	return soul, nil
}

// Replace atomically installs an already-approved identity revision.
func (file *File) Replace(ctx context.Context, soul string) error {
	if file == nil {
		return fmt.Errorf("identity: file is required")
	}
	if _, err := file.Load(ctx); err != nil {
		return err
	}
	soul = strings.TrimSpace(soul)
	if soul == "" || len(soul) > maxSoulBytes {
		return fmt.Errorf("identity: SOUL.md size is outside bounds")
	}
	directory := filepath.Dir(file.path)
	temporary, err := os.CreateTemp(directory, ".SOUL.md.*")
	if err != nil {
		return fmt.Errorf("identity: create atomic revision: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("identity: secure atomic revision: %w", err)
	}
	if _, err := temporary.WriteString(soul + "\n"); err != nil {
		return fmt.Errorf("identity: write atomic revision: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("identity: sync atomic revision: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("identity: close atomic revision: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, file.path); err != nil {
		return fmt.Errorf("identity: install atomic revision: %w", err)
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("identity: open root for sync: %w", err)
	}
	if err := directoryHandle.Sync(); err != nil {
		_ = directoryHandle.Close()
		return fmt.Errorf("identity: sync root: %w", err)
	}
	if err := directoryHandle.Close(); err != nil {
		return fmt.Errorf("identity: close root: %w", err)
	}
	committed = true
	return nil
}
