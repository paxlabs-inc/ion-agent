// Command ion is the Ion process entry point.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
	"github.com/paxlabs-inc/ion-agent/internal/session"
	"github.com/paxlabs-inc/ion-agent/pkg/types"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

const defaultMaxContextTokens = 128 * 1024

func main() {
	if err := loadStartupDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// loadStartupDotEnv loads only the provider, channel, and machine-mail
// connection values from a permission-restricted local .env file. Existing
// process environment always wins, and the file is parsed as data rather than
// executed as shell code.
func loadStartupDotEnv(path string) error {
	return loadStartupDotEnvWith(path, os.LookupEnv, os.Setenv)
}

func loadStartupDotEnvWith(
	path string,
	lookup func(string) (string, bool),
	set func(string, string) error,
) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ion: inspect %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("ion: %s must not be group- or world-accessible", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("ion: open %s: %w", path, err)
	}
	defer file.Close()
	allowed := map[string]struct{}{
		"MACHINE_MAIL_ADDRESS":                       {},
		"MACHINE_MAIL_API_KEY":                       {},
		"MACHINE_MAIL_URL":                           {},
		"PROVIDER_NAME":                              {},
		"PROVIDER_BASE_URL":                          {},
		"PROVIDER_API_KEY":                           {},
		"PROVIDER_INPUT_MICROCENTS_PER_TOKEN":        {},
		"PROVIDER_CACHED_INPUT_MICROCENTS_PER_TOKEN": {},
		"PROVIDER_OUTPUT_MICROCENTS_PER_TOKEN":       {},
		"PROVIDER_SURCHARGE_BPS":                     {},
		"LLM_MODEL":                                  {},
		"NOVITA_API_KEY":                             {},
		"NOVITA_BASE_URL":                            {},
		"TELEGRAM_BOT_TOKEN":                         {},
		"TELEGRAM_ALLOWED_USERS":                     {},
		"ION_COMPUTER_URL":                           {},
		"ION_COMPUTER_AUTH_KEY":                      {},
		"ION_AUTH_USERNAME":                          {},
		"ION_AUTH_PASSWORD":                          {},
		"ION_AUTH_PASSWORD_HASH":                     {},
		"ION_WEB_ORIGIN":                             {},
		"TAVILY_API_KEY":                             {},
		"ION_TAVILY_API_KEY":                         {},
		"ION_TAVILY_SEARCH_ENDPOINT":                 {},
		"ION_SEARCH_ENDPOINT":                        {},
		"SEARXNG_URL":                                {},
		"ION_OFFICE_ENABLED":                         {},
		"ION_OFFICE_INTERNAL_URL":                    {},
		"ION_OFFICE_PUBLIC_PATH":                     {},
		"ION_OFFICE_CALLBACK_ORIGIN":                 {},
		"ION_OFFICE_JWT_SECRET":                      {},
		"ION_OFFICE_JWT_SECRET_FILE":                 {},
		"ION_OFFICE_MAX_FILE_BYTES":                  {},
		"ION_OFFICE_MAX_VERSIONS":                    {},
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		if !found {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		if _, exists := lookup(key); exists {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if err := set(key, value); err != nil {
			return fmt.Errorf("ion: load %s from %s: %w", key, path, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ion: read %s: %w", path, err)
	}
	return nil
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return usageError()
	}
	switch arguments[0] {
	case "init":
		return initialize(ctx, arguments[1:])
	case "version":
		fmt.Printf("ion %s (%s, %s)\n", version, commit, buildTime)
		return nil
	case "dashboard":
		return runDashboard(ctx, arguments[1:])
	case "tui":
		return runTUI(ctx, arguments[1:])
	case "help", "-h", "--help":
		fmt.Println(usageText())
		return nil
	default:
		return usageError()
	}
}

func initialize(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	dataDirectory := flags.String("data-dir", defaultDataDirectory(), "Ion data directory")
	developmentFileKEK := flags.Bool(
		"dev-file-kek",
		false,
		"use the explicit development-only 0600 file KEK fallback",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("ion init: unexpected positional arguments")
	}
	if err := os.MkdirAll(*dataDirectory, 0o700); err != nil {
		return fmt.Errorf("ion init: create data directory: %w", err)
	}

	var source vault.KEKSource
	var err error
	if *developmentFileKEK {
		source, err = vault.NewFileKEKSource(filepath.Join(*dataDirectory, "development.kek"))
	} else {
		source, err = vault.NewProductionKEKSource(
			*dataDirectory,
			"ion",
			"default",
		)
	}
	if err != nil {
		return fmt.Errorf(
			"ion init: configure secure host key: %w",
			err,
		)
	}
	keyStore, err := vault.NewFileWrappedKeyStore(filepath.Join(*dataDirectory, "user-key.enc"))
	if err != nil {
		return fmt.Errorf("ion init: configure User Key store: %w", err)
	}
	manager, err := vault.Initialize(ctx, source, keyStore)
	if err != nil {
		if errors.Is(err, vault.ErrKeyNotFound) {
			return fmt.Errorf("ion init: initialize vault: %w", err)
		}
		return fmt.Errorf("ion init: %w", err)
	}
	sessionStore, err := session.Open(
		ctx,
		filepath.Join(*dataDirectory, "sessions.db"),
		manager.Vault(),
		types.SystemClock{},
		defaultMaxContextTokens,
	)
	if err != nil {
		_ = manager.Close() // Best-effort key zeroization after failed initialization.
		return fmt.Errorf("ion init: initialize session store: %w", err)
	}
	if err := sessionStore.Close(ctx); err != nil {
		_ = manager.Close() // Best-effort key zeroization after failed initialization.
		return fmt.Errorf("ion init: close session store: %w", err)
	}
	if err := manager.Close(); err != nil {
		return fmt.Errorf("ion init: close vault: %w", err)
	}
	fmt.Printf("Ion is ready. Protected data is stored in %s\n", *dataDirectory)
	return nil
}

func defaultDataDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ion"
	}
	return filepath.Join(home, ".ion")
}

func usageError() error {
	return fmt.Errorf("%s", usageText())
}

func usageText() string {
	return `Usage:
  ion init [--data-dir PATH] [--dev-file-kek]
  ion dashboard [--data-dir PATH] [--dev-file-kek] [--listen ADDRESS]
  ion tui [--data-dir PATH] [--dev-file-kek] [--attach] [--check]
  ion version
  ion help`
}
