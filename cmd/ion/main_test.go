package main

import (
	"context"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	tuiassets "github.com/paxlabs-inc/ion-agent/ui/tui"
	webassets "github.com/paxlabs-inc/ion-agent/ui/web"
)

func TestRunHelpAndValidation(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		nil,
		{"unknown"},
		{"init", "unexpected"},
	} {
		if err := run(context.Background(), arguments); err == nil {
			t.Fatalf("run(%q) succeeded", arguments)
		}
	}
	for _, arguments := range [][]string{{"help"}, {"--help"}, {"version"}} {
		if err := run(context.Background(), arguments); err != nil {
			t.Fatalf("run(%q) error = %v", arguments, err)
		}
	}
}

func TestLoadStartupDotEnvIsRestrictedAndDoesNotOverride(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, ".env")
	payload := "MACHINE_MAIL_ADDRESS=agent@machinemail.org\n" +
		"MACHINE_MAIL_API_KEY='protected-test-key'\n" +
		"PROVIDER_NAME=test-provider\n" +
		"PROVIDER_BASE_URL=https://provider.example/v1\n" +
		"PROVIDER_API_KEY=protected-provider-key\n" +
		"LLM_MODEL=test-model\n" +
		"NOVITA_API_KEY=protected-novita-key\n" +
		"NOVITA_BASE_URL=https://api.novita.example\n" +
		"TELEGRAM_BOT_TOKEN=protected-telegram-token\n" +
		"TELEGRAM_ALLOWED_USERS=123456789,987654321\n" +
		"ION_COMPUTER_URL=http://ion-computer.internal:8081\n" +
		"ION_COMPUTER_AUTH_KEY=protected-computer-key-value-000000000000\n" +
		"ION_WEB_ORIGIN=https://ion.example.com\n" +
		"ION_AUTH_USERNAME=operator\n" +
		"ION_AUTH_PASSWORD=protected-operator-password\n" +
		"ION_SEARCH_ENDPOINT=https://search.example\n" +
		"TAVILY_API_KEY=protected-tavily-key\n" +
		"UNRELATED_API_KEY=must-not-load\n"
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"MACHINE_MAIL_ADDRESS": "existing@machinemail.org"}
	lookup := func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
	set := func(key, value string) error {
		values[key] = value
		return nil
	}
	if err := loadStartupDotEnvWith(path, lookup, set); err != nil {
		t.Fatal(err)
	}
	if got := values["MACHINE_MAIL_ADDRESS"]; got != "existing@machinemail.org" {
		t.Fatalf("address override = %q", got)
	}
	if got := values["MACHINE_MAIL_API_KEY"]; got != "protected-test-key" {
		t.Fatalf("machine-mail key = %q", got)
	}
	for key, want := range map[string]string{
		"PROVIDER_NAME": "test-provider", "PROVIDER_BASE_URL": "https://provider.example/v1",
		"PROVIDER_API_KEY": "protected-provider-key", "LLM_MODEL": "test-model",
		"NOVITA_API_KEY": "protected-novita-key", "NOVITA_BASE_URL": "https://api.novita.example",
		"TELEGRAM_BOT_TOKEN":     "protected-telegram-token",
		"TELEGRAM_ALLOWED_USERS": "123456789,987654321",
		"ION_COMPUTER_URL":       "http://ion-computer.internal:8081",
		"ION_COMPUTER_AUTH_KEY":  "protected-computer-key-value-000000000000",
		"ION_WEB_ORIGIN":         "https://ion.example.com",
		"ION_AUTH_USERNAME":      "operator",
		"ION_AUTH_PASSWORD":      "protected-operator-password",
		"ION_SEARCH_ENDPOINT":    "https://search.example",
		"TAVILY_API_KEY":         "protected-tavily-key",
	} {
		if got := values[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if got := values["UNRELATED_API_KEY"]; got != "" {
		t.Fatalf("unrelated variable was loaded: %q", got)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := loadStartupDotEnvWith(path, lookup, set); err == nil {
		t.Fatal("broadly-readable .env was accepted")
	}
}

func TestInitializeDevelopmentVault(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "data")
	if err := run(
		context.Background(),
		[]string{"init", "--dev-file-kek", "--data-dir", directory},
	); err != nil {
		t.Fatalf("run(init) error = %v", err)
	}
	for _, name := range []string{"development.kek", "user-key.enc", "sessions.db"} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	if err := run(
		context.Background(),
		[]string{"init", "--dev-file-kek", "--data-dir", directory},
	); err == nil {
		t.Fatal("second init succeeded")
	}
}

func TestInitializeAndReopenProductionVaultWithoutDesktopKeyring(t *testing.T) {
	tools := t.TempDir()
	hostKey := filepath.Join(t.TempDir(), "host-key")
	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  encrypt)\n" +
		"    /bin/cat > '" + hostKey + "'\n" +
		"    printf 'opaque-machine-credential'\n" +
		"    ;;\n" +
		"  decrypt)\n" +
		"    [ \"$(/bin/cat \"$4\")\" = 'opaque-machine-credential' ] || exit 3\n" +
		"    /bin/cat '" + hostKey + "'\n" +
		"    ;;\n" +
		"  *) exit 2 ;;\n" +
		"esac\n"
	if err := os.WriteFile(
		filepath.Join(tools, "systemd-creds"),
		[]byte(script),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)

	directory := filepath.Join(t.TempDir(), "data")
	if err := run(
		context.Background(),
		[]string{"init", "--data-dir", directory},
	); err != nil {
		t.Fatalf("run(production init) error = %v", err)
	}
	for _, name := range []string{
		vault.MachineKEKFilename,
		"user-key.enc",
		"sessions.db",
	} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "development.kek")); !os.IsNotExist(err) {
		t.Fatalf("development KEK exists in production: %v", err)
	}

	runtime, err := openOperatorRuntime(context.Background(), directory, false)
	if err != nil {
		t.Fatalf("reopen production runtime error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close production runtime error = %v", err)
	}
}

func TestDefaultDataDirectoryAndUsage(t *testing.T) {
	t.Parallel()
	if defaultDataDirectory() == "" {
		t.Fatal("defaultDataDirectory() is empty")
	}
	if usageText() == "" || usageError() == nil {
		t.Fatal("usage helpers returned empty values")
	}
}

func TestDefaultDashboardAddressUsesItsExactBrowserOrigin(t *testing.T) {
	t.Parallel()
	host, port, err := net.SplitHostPort(defaultDashboardAddress)
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + net.JoinHostPort(host, port)
	if origin != "http://127.0.0.1:4174" {
		t.Fatalf("default dashboard origin = %q", origin)
	}
}

func TestEmbeddedOperatorClientsAndDeterministicArtifact(t *testing.T) {
	t.Parallel()
	assets, err := webassets.Distribution()
	if err != nil {
		t.Fatal(err)
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "<title>Ion</title>") {
		t.Fatal("embedded browser index is not the production operator bundle")
	}
	if len(tuiassets.Bundle) < 100_000 ||
		!strings.Contains(string(tuiassets.Bundle), "ION") {
		t.Fatal("embedded TUI artifact is missing or incomplete")
	}
	path := filepath.Join(t.TempDir(), "ion-tui.mjs")
	if err := writeArtifact(path, tuiassets.Bundle); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArtifact(path, tuiassets.Bundle); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("TUI artifact extraction is not deterministic")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("TUI artifact mode = %o", info.Mode().Perm())
	}
}

func TestOperatorActorIdentityPersistsAndFailsClosed(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "operator")
	first, err := loadOrCreateActorID(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateActorID(directory)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("operator actor changed across restart: %s != %s", first, second)
	}
	path := filepath.Join(directory, "actor-id")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("operator actor mode = %o, want 600", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateActorID(directory); err == nil {
		t.Fatal("broad actor identity permissions were accepted")
	}
}

func TestConfiguredProviderPricingRequiresAnAuthoritativeTariff(t *testing.T) {
	t.Setenv("PROVIDER_INPUT_MICROCENTS_PER_TOKEN", "125")
	t.Setenv("PROVIDER_CACHED_INPUT_MICROCENTS_PER_TOKEN", "")
	t.Setenv("PROVIDER_OUTPUT_MICROCENTS_PER_TOKEN", "500")
	t.Setenv("PROVIDER_SURCHARGE_BPS", "250")
	pricing, err := configuredProviderPricing()
	if err != nil {
		t.Fatal(err)
	}
	if pricing == nil ||
		pricing.InputMicrocentsPerToken != 125 ||
		pricing.CachedInputMicrocentsPerToken != 125 ||
		pricing.OutputMicrocentsPerToken != 500 ||
		pricing.ProviderSurchargeBPS != 250 {
		t.Fatalf("pricing = %+v", pricing)
	}
	t.Setenv("PROVIDER_OUTPUT_MICROCENTS_PER_TOKEN", "")
	if _, err := configuredProviderPricing(); err == nil {
		t.Fatal("partial provider tariff was accepted")
	}
}
