package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// KEKSource stores the key-encryption key outside Ion configuration and
// data files.
type KEKSource interface {
	Load(context.Context) ([]byte, error)
	Store(context.Context, []byte) error
	Name() string
}

// DeploymentKEKSource loads a write-only deployment secret supplied by the
// hosting platform. The encoded secret is consumed after the first successful
// load so plaintext key material does not remain in configuration state.
type DeploymentKEKSource struct {
	mu  sync.Mutex
	key []byte
}

// NewDeploymentKEKSource validates a base64-encoded 256-bit deployment KEK.
func NewDeploymentKEKSource(encoded string) (*DeploymentKEKSource, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("vault: deployment KEK is required")
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(encoded)
	}
	if err != nil || len(key) != KeySize {
		zero(key)
		return nil, fmt.Errorf("vault: deployment KEK must be a base64-encoded 256-bit key")
	}
	return &DeploymentKEKSource{key: key}, nil
}

func (source *DeploymentKEKSource) Name() string {
	return "deployment-secret"
}

func (source *DeploymentKEKSource) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.key) != KeySize {
		return nil, ErrKeyNotFound
	}
	key := append([]byte(nil), source.key...)
	zero(source.key)
	source.key = nil
	return key, nil
}

func (*DeploymentKEKSource) Store(context.Context, []byte) error {
	return fmt.Errorf("vault: deployment KEK source is read-only")
}

// SecretToolSource accesses libsecret through the standard secret-tool CLI.
// The subprocess is a true external boundary; key bytes are sent through stdin
// and never command-line arguments.
type SecretToolSource struct {
	service string
	account string
	path    string
}

// NewSecretToolSource creates a Linux libsecret-backed KEK source.
func NewSecretToolSource(service, account string) (*SecretToolSource, error) {
	if service == "" || account == "" {
		return nil, fmt.Errorf("vault: libsecret service and account are required")
	}
	path, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil, fmt.Errorf("vault: find secret-tool: %w", err)
	}
	return &SecretToolSource{service: service, account: account, path: path}, nil
}

func (source *SecretToolSource) Name() string {
	return "libsecret"
}

func (source *SecretToolSource) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := exec.CommandContext(
		ctx,
		source.path,
		"lookup",
		"service",
		source.service,
		"account",
		source.account,
	)
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && len(exitError.Stderr) == 0 {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("vault: load KEK from libsecret: %w", err)
	}
	defer zero(output)

	encoded := bytes.TrimSpace(output)
	key := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	length, err := base64.StdEncoding.Decode(key, encoded)
	if err != nil || length != KeySize {
		zero(key)
		return nil, fmt.Errorf("vault: invalid KEK in libsecret")
	}
	return key[:length], nil
}

func (source *SecretToolSource) Store(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(key) != KeySize {
		return ErrInvalidKey
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(key)))
	base64.StdEncoding.Encode(encoded, key)
	defer zero(encoded)

	command := exec.CommandContext(
		ctx,
		source.path,
		"store",
		"--label",
		"Ion key-encryption key",
		"service",
		source.service,
		"account",
		source.account,
	)
	command.Stdin = bytes.NewReader(encoded)
	if _, err := command.Output(); err != nil {
		return fmt.Errorf("vault: store KEK in libsecret: %w", err)
	}
	return nil
}

// FileKEKSource is the explicit development-only fallback. It rejects files
// with permissions broader than 0600.
type FileKEKSource struct {
	path string
}

// NewFileKEKSource creates a development key source.
func NewFileKEKSource(path string) (*FileKEKSource, error) {
	if path == "" {
		return nil, fmt.Errorf("vault: KEK file path is required")
	}
	return &FileKEKSource{path: filepath.Clean(path)}, nil
}

func (source *FileKEKSource) Name() string {
	return "development-file"
}

func (source *FileKEKSource) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(source.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("vault: inspect development KEK: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("vault: development KEK permissions must be 0600")
	}
	key, err := os.ReadFile(source.path)
	if err != nil {
		return nil, fmt.Errorf("vault: read development KEK: %w", err)
	}
	if len(key) != KeySize {
		zero(key)
		return nil, ErrInvalidKey
	}
	return key, nil
}

func (source *FileKEKSource) Store(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(key) != KeySize {
		return ErrInvalidKey
	}
	return atomicWriteFile(source.path, key, 0o600)
}

// NewPlatformKEKSource selects the legacy desktop OS keychain implementation.
// New installations should use NewProductionKEKSource so headless Linux hosts
// do not depend on a desktop keyring daemon.
func NewPlatformKEKSource(service, account string) (KEKSource, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("vault: OS keychain is not implemented for %s", runtime.GOOS)
	}
	return NewSecretToolSource(service, account)
}

// NewProductionKEKSource selects a non-interactive production key source.
// Existing libsecret installations remain readable; new Linux installations
// use a machine-bound encrypted systemd credential in the private data
// directory.
func NewProductionKEKSource(dataDirectory, service, account string) (KEKSource, error) {
	dataDirectory = filepath.Clean(dataDirectory)
	if dataDirectory == "." || dataDirectory == "" {
		return nil, fmt.Errorf("vault: data directory is required")
	}
	if runtime.GOOS != "linux" {
		return NewPlatformKEKSource(service, account)
	}

	credentialPath := filepath.Join(dataDirectory, MachineKEKFilename)
	credentialExists, err := pathExists(credentialPath)
	if err != nil {
		return nil, fmt.Errorf("vault: inspect machine credential: %w", err)
	}
	if credentialExists {
		source, sourceErr := NewSystemdCredentialSource(credentialPath, "ion-kek")
		if sourceErr != nil {
			return nil, sourceErr
		}
		source.fallbackNames = []string{"prometheus-kek"}
		return source, nil
	}

	wrappedKeyExists, err := pathExists(filepath.Join(dataDirectory, "user-key.enc"))
	if err != nil {
		return nil, fmt.Errorf("vault: inspect wrapped User Key: %w", err)
	}
	if wrappedKeyExists {
		source, sourceErr := NewSecretToolSource(service, account)
		if sourceErr != nil {
			return nil, fmt.Errorf(
				"vault: this data directory uses the legacy desktop keyring: %w",
				sourceErr,
			)
		}
		return source, nil
	}

	source, systemdErr := NewSystemdCredentialSource(
		credentialPath,
		"ion-kek",
	)
	if systemdErr == nil {
		return source, nil
	}
	legacy, legacyErr := NewSecretToolSource(service, account)
	if legacyErr == nil {
		return legacy, nil
	}
	return nil, fmt.Errorf(
		"vault: no secure OS key store is available (machine credentials: %v; desktop keyring: %v)",
		systemdErr,
		legacyErr,
	)
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func atomicWriteFile(path string, content []byte, mode fs.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("vault: create key directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, ".ion-key-*")
	if err != nil {
		return fmt.Errorf("vault: create temporary key: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			// Best-effort removal of an uncommitted temporary file.
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close() // Best-effort cleanup after the primary error.
		return fmt.Errorf("vault: set key permissions: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close() // Best-effort cleanup after the primary error.
		return fmt.Errorf("vault: write temporary key: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close() // Best-effort cleanup after the primary error.
		return fmt.Errorf("vault: sync temporary key: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("vault: close temporary key: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("vault: commit key: %w", err)
	}
	committed = true

	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("vault: open key directory: %w", err)
	}
	defer func() {
		// The directory is already synced; close failure cannot be recovered.
		_ = dir.Close()
	}()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("vault: sync key directory: %w", err)
	}
	return nil
}
