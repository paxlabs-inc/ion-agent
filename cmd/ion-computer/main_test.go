package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthKeyFileCanBeConsumedBeforeDesktopStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-key")
	key := strings.Repeat("k", 32)
	if err := os.WriteFile(path, []byte(key), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ION_COMPUTER_AUTH_KEY", "")
	t.Setenv("ION_COMPUTER_AUTH_KEY_FILE", path)
	t.Setenv("ION_COMPUTER_CONSUME_AUTH_KEY_FILE", "true")
	got, isolated, err := authKeyEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if got != key || !isolated {
		t.Fatalf("consumed auth key = %q, isolated = %t", got, isolated)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consumed auth key file remains: %v", err)
	}
}

func TestAuthKeySourcesFailClosedOnConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-key")
	if err := os.WriteFile(path, []byte(strings.Repeat("k", 32)), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ION_COMPUTER_AUTH_KEY", strings.Repeat("e", 32))
	t.Setenv("ION_COMPUTER_AUTH_KEY_FILE", path)
	if _, _, err := authKeyEnvironment(); err == nil {
		t.Fatal("conflicting auth key sources were accepted")
	}
}
