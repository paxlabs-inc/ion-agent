package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigDerivesRailwayOrigin(t *testing.T) {
	clearApplianceEnvironment(t)
	t.Setenv("RAILWAY_SERVICE_ID", "service")
	t.Setenv("RAILWAY_PUBLIC_DOMAIN", "ion.example")
	t.Setenv("ION_AUTH_USERNAME", "operator")
	t.Setenv("ION_AUTH_PASSWORD", strings.Repeat("p", 32))
	t.Setenv("ION_VAULT_KEK", strings.Repeat("k", 44))
	t.Setenv("PORT", "9191")

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.PublicListen != "[::]:9191" {
		t.Fatalf("PublicListen = %q", config.PublicListen)
	}
	if config.WebOrigin != "https://ion.example" {
		t.Fatalf("WebOrigin = %q", config.WebOrigin)
	}
	if config.InternalOrigin != "http://127.0.0.1:9191" {
		t.Fatalf("InternalOrigin = %q", config.InternalOrigin)
	}
}

func TestLoadConfigRejectsRailwayWithoutVaultKEK(t *testing.T) {
	clearApplianceEnvironment(t)
	t.Setenv("RAILWAY_SERVICE_ID", "service")
	t.Setenv("RAILWAY_PUBLIC_DOMAIN", "ion.example")
	t.Setenv("ION_AUTH_USERNAME", "operator")
	t.Setenv("ION_AUTH_PASSWORD", strings.Repeat("p", 32))

	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "ION_VAULT_KEK") {
		t.Fatalf("loadConfig() error = %v", err)
	}
}

func TestLoadConfigRejectsNonOriginWebURL(t *testing.T) {
	clearApplianceEnvironment(t)
	t.Setenv("ION_WEB_ORIGIN", "https://ion.example/path?query=yes")

	if _, err := loadConfig(); err == nil ||
		!strings.Contains(err.Error(), "http or https origin") {
		t.Fatalf("loadConfig() error = %v", err)
	}
}

func TestInternalCredentialsPersistInApplianceState(t *testing.T) {
	clearApplianceEnvironment(t)
	root := t.TempDir()
	state := filepath.Join(root, "appliance")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	instance := &appliance{config: applianceConfig{DataRoot: root}}
	path := filepath.Join(state, "computer-auth-key")

	first, err := instance.loadOrCreateSecret("ION_COMPUTER_AUTH_KEY", path)
	if err != nil {
		t.Fatalf("loadOrCreateSecret(first) error = %v", err)
	}
	second, err := instance.loadOrCreateSecret("ION_COMPUTER_AUTH_KEY", path)
	if err != nil {
		t.Fatalf("loadOrCreateSecret(second) error = %v", err)
	}
	if first != second || len(first) < 32 {
		t.Fatal("generated internal credential was not durably reused")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o", info.Mode().Perm())
	}
}

func clearApplianceEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"PORT",
		"ION_APPLIANCE_DATA_ROOT",
		"ION_WEB_ORIGIN",
		"ION_AUTH_USERNAME",
		"ION_AUTH_PASSWORD",
		"ION_AUTH_PASSWORD_HASH",
		"ION_VAULT_KEK",
		"RAILWAY_ENVIRONMENT_ID",
		"RAILWAY_PROJECT_ID",
		"RAILWAY_SERVICE_ID",
		"RAILWAY_PUBLIC_DOMAIN",
	} {
		t.Setenv(name, "")
	}
}
