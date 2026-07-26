package vault

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdCredentialSourceRoundTripAndPermissions(t *testing.T) {
	directory := t.TempDir()
	hostKeyPath := filepath.Join(t.TempDir(), "host-key")
	argumentsPath := filepath.Join(t.TempDir(), "arguments")
	script := fakeSystemdCreds(t, directory, hostKeyPath, argumentsPath)
	credentialPath := filepath.Join(directory, MachineKEKFilename)
	source := &SystemdCredentialSource{
		path: credentialPath, name: "ion-kek", executable: script,
	}
	if source.Name() != "machine-credential" {
		t.Fatalf("Name() = %q", source.Name())
	}
	if _, err := source.Load(context.Background()); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Load(missing) error = %v", err)
	}

	key := repeatedKey(0x5a)
	if err := source.Store(context.Background(), key); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o, want 600", info.Mode().Perm())
	}
	credential, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(credential, key) {
		t.Fatal("encrypted credential contains plaintext KEK")
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(arguments, key) {
		t.Fatal("KEK appeared in subprocess arguments")
	}

	loaded, err := source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	defer zero(loaded)
	if !bytes.Equal(loaded, key) {
		t.Fatal("loaded KEK differs")
	}
}

func TestSystemdCredentialSourceValidationAndFailures(t *testing.T) {
	for _, input := range [][2]string{{"", "name"}, {"path", ""}} {
		if _, err := NewSystemdCredentialSource(input[0], input[1]); err == nil {
			t.Fatalf("NewSystemdCredentialSource(%q, %q) succeeded", input[0], input[1])
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := NewSystemdCredentialSource("path", "name"); err == nil {
		t.Fatal("NewSystemdCredentialSource(without systemd-creds) succeeded")
	}

	directory := t.TempDir()
	credentialPath := filepath.Join(directory, MachineKEKFilename)
	failing := writeExecutable(t, directory, "failing", "#!/bin/sh\nexit 2\n")
	source := &SystemdCredentialSource{
		path: credentialPath, name: "name", executable: failing,
	}
	if err := source.Store(context.Background(), []byte("short")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Store(short) error = %v", err)
	}
	if err := source.Store(context.Background(), repeatedKey(1)); err == nil {
		t.Fatal("Store(process failure) succeeded")
	}
	if err := os.WriteFile(credentialPath, []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Load(context.Background()); err == nil {
		t.Fatal("Load(process failure) succeeded")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := source.Store(cancelled, repeatedKey(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Store(cancelled) error = %v", err)
	}
	if _, err := source.Load(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
}

func TestSystemdCredentialSourceRejectsUnsafeCredential(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	for name, setup := range map[string]func(string) error{
		"broad permissions": func(path string) error {
			return os.WriteFile(path, []byte("opaque"), 0o644)
		},
		"directory": func(path string) error {
			return os.Mkdir(path, 0o700)
		},
		"symlink": func(path string) error {
			target := filepath.Join(directory, "target")
			if err := os.WriteFile(target, []byte("opaque"), 0o600); err != nil {
				return err
			}
			return os.Symlink(target, path)
		},
	} {
		name, setup := name, setup
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(name, " ", "-"))
			if err := setup(path); err != nil {
				t.Fatal(err)
			}
			source := &SystemdCredentialSource{
				path: path, name: "name", executable: "/bin/false",
			}
			if _, err := source.Load(context.Background()); err == nil {
				t.Fatal("Load(unsafe credential) succeeded")
			}
		})
	}
}

func TestNewProductionKEKSourceSelection(t *testing.T) {
	tools := t.TempDir()
	fakeSystemdCreds(
		t,
		tools,
		filepath.Join(t.TempDir(), "host-key"),
		filepath.Join(t.TempDir(), "arguments"),
	)
	writeExecutable(t, tools, "secret-tool", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", tools)

	fresh := t.TempDir()
	source, err := NewProductionKEKSource(fresh, "ion", "default")
	if err != nil {
		t.Fatal(err)
	}
	if source.Name() != "machine-credential" {
		t.Fatalf("fresh source = %q", source.Name())
	}

	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, "user-key.enc"), []byte("wrapped"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err = NewProductionKEKSource(legacy, "ion", "default")
	if err != nil {
		t.Fatal(err)
	}
	if source.Name() != "libsecret" {
		t.Fatalf("legacy source = %q", source.Name())
	}

	upgraded := t.TempDir()
	for name, content := range map[string][]byte{
		"user-key.enc":     []byte("wrapped"),
		MachineKEKFilename: []byte("credential"),
	} {
		if err := os.WriteFile(filepath.Join(upgraded, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source, err = NewProductionKEKSource(upgraded, "ion", "default")
	if err != nil {
		t.Fatal(err)
	}
	if source.Name() != "machine-credential" {
		t.Fatalf("upgraded source = %q", source.Name())
	}
}

func fakeSystemdCreds(
	t *testing.T,
	directory string,
	hostKeyPath string,
	argumentsPath string,
) string {
	t.Helper()
	content := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > '" + argumentsPath + "'\n" +
		"case \"$2\" in\n" +
		"  encrypt)\n" +
		"    /bin/cat > '" + hostKeyPath + "'\n" +
		"    printf 'opaque-machine-credential'\n" +
		"    ;;\n" +
		"  decrypt)\n" +
		"    [ \"$(/bin/cat \"$4\")\" = 'opaque-machine-credential' ] || exit 3\n" +
		"    /bin/cat '" + hostKeyPath + "'\n" +
		"    ;;\n" +
		"  *) exit 2 ;;\n" +
		"esac\n"
	return writeExecutable(t, directory, "systemd-creds", content)
}
