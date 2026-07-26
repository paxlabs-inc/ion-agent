package vault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MachineKEKFilename is the encrypted, machine-bound production credential.
// The file contains no plaintext key material and is safe to back up alongside
// the encrypted Ion data.
const MachineKEKFilename = "machine-kek.cred"

// SystemdCredentialSource stores the KEK as an authenticated systemd
// credential. systemd-creds binds it to TPM2 where available and to the
// installation's root-only host secret, requiring neither a GUI keyring nor an
// interactive unlock on reboot.
type SystemdCredentialSource struct {
	path          string
	name          string
	fallbackNames []string
	executable    string
}

// NewSystemdCredentialSource creates a machine-bound production KEK source.
func NewSystemdCredentialSource(path, name string) (*SystemdCredentialSource, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	name = strings.TrimSpace(name)
	if path == "." || path == "" {
		return nil, fmt.Errorf("vault: machine credential path is required")
	}
	if name == "" {
		return nil, fmt.Errorf("vault: machine credential name is required")
	}
	executable, err := exec.LookPath("systemd-creds")
	if err != nil {
		return nil, fmt.Errorf("vault: find systemd-creds: %w", err)
	}
	return &SystemdCredentialSource{
		path: path, name: name, executable: executable,
	}, nil
}

func (source *SystemdCredentialSource) Name() string {
	return "machine-credential"
}

func (source *SystemdCredentialSource) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(source.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("vault: inspect machine credential: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("vault: machine credential must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("vault: machine credential permissions must be 0600")
	}

	names := append([]string{source.name}, source.fallbackNames...)
	var lastErr error
	for _, name := range names {
		command := exec.CommandContext(
			ctx,
			source.executable,
			"--newline=no",
			"decrypt",
			"--name="+name,
			source.path,
			"-",
		)
		key, commandErr := command.Output()
		if commandErr == nil {
			if len(key) != KeySize {
				zero(key)
				return nil, ErrInvalidKey
			}
			return key, nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		lastErr = commandErr
	}
	return nil, fmt.Errorf("vault: unlock machine credential: %w", lastErr)
}

func (source *SystemdCredentialSource) Store(
	ctx context.Context,
	key []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(key) != KeySize {
		return ErrInvalidKey
	}

	command := exec.CommandContext(
		ctx,
		source.executable,
		"--newline=no",
		"encrypt",
		"--name="+source.name,
		"--with-key=auto",
		"-",
		"-",
	)
	command.Stdin = bytes.NewReader(key)
	credential, err := command.Output()
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("vault: protect machine credential: %w", err)
	}
	if len(credential) == 0 {
		return fmt.Errorf("vault: systemd-creds returned an empty credential")
	}
	return atomicWriteFile(source.path, credential, 0o600)
}
