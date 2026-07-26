package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/privatecomputer"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config, err := loadConfig()
	if err != nil {
		logger.Error("private computer configuration rejected", "error", err)
		os.Exit(1)
	}
	daemon, err := privatecomputer.NewDesktopDaemon(config, logger)
	if err != nil {
		logger.Error("private computer startup rejected", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	if err := daemon.Run(ctx); err != nil {
		logger.Error("private computer stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (privatecomputer.DaemonConfig, error) {
	authKey, authKeyIsolated, err := authKeyEnvironment()
	if err != nil {
		return privatecomputer.DaemonConfig{}, err
	}
	width, err := integerEnvironment("ION_COMPUTER_WIDTH", 1280)
	if err != nil {
		return privatecomputer.DaemonConfig{}, err
	}
	height, err := integerEnvironment("ION_COMPUTER_HEIGHT", 720)
	if err != nil {
		return privatecomputer.DaemonConfig{}, err
	}
	hostID, err := uuid.Parse(strings.TrimSpace(os.Getenv("ION_COMPUTER_HOST_ID")))
	if err != nil {
		return privatecomputer.DaemonConfig{}, err
	}
	mode := privatecomputer.PersistenceMode(strings.TrimSpace(os.Getenv("ION_COMPUTER_MODE")))
	artifactPublicKey, err := artifactPublicKeyEnvironment()
	if err != nil {
		return privatecomputer.DaemonConfig{}, err
	}
	config := privatecomputer.DaemonConfig{
		AuthKey:         authKey,
		AuthKeyIsolated: authKeyIsolated,
		ListenAddress:   environment("ION_COMPUTER_LISTEN", "[::]:8081"),
		Display:         environment("ION_COMPUTER_DISPLAY", ":99"),
		Width:           width,
		Height:          height,
		Mode:            mode,
		Home:            environment("ION_COMPUTER_HOME", "/home/ion"),
		StateRoot: environment(
			"ION_COMPUTER_STATE_ROOT",
			"/home/ion/.ion-computer/state",
		),
		WorkspaceRoot: environment(
			"ION_COMPUTER_WORKSPACE_ROOT",
			"/home/ion/.ion-computer/workspaces",
		),
		StartURL: environment("ION_COMPUTER_START_URL", "about:blank"),
		BrowserContainment: privatecomputer.BrowserContainment(
			environment(
				"ION_COMPUTER_BROWSER_CONTAINMENT",
				string(privatecomputer.BrowserSandboxed),
			),
		),
		ArtifactPublicKey: artifactPublicKey,
		HostID:            hostID,
		HostVersion:       environment("ION_COMPUTER_HOST_VERSION", "ion-computer/0.1.0"),
		ImageDigest:       os.Getenv("ION_COMPUTER_IMAGE_DIGEST"),
		Budget: privatecomputer.ResourceBudget{
			CPUMillis:         2_000,
			MemoryBytes:       4 << 30,
			Processes:         512,
			StorageBytes:      20 << 30,
			EgressBytes:       2 << 30,
			IdleSeconds:       900,
			SessionSeconds:    8 * 60 * 60,
			ScreenshotBytes:   8 << 20,
			ClipboardBytes:    64 << 10,
			CostMicrosPerHour: 500_000,
		},
	}
	return config, config.Validate()
}

func artifactPublicKeyEnvironment() (ed25519.PublicKey, error) {
	value := strings.TrimSpace(os.Getenv("ION_COMPUTER_ARTIFACT_PUBLIC_KEY"))
	if value == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("private computer artifact public key is invalid")
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("private computer artifact public key is invalid")
	}
	return ed25519.PublicKey(decoded), nil
}

func authKeyEnvironment() (string, bool, error) {
	path := strings.TrimSpace(os.Getenv("ION_COMPUTER_AUTH_KEY_FILE"))
	if path == "" {
		return os.Getenv("ION_COMPUTER_AUTH_KEY"), false, nil
	}
	if strings.TrimSpace(os.Getenv("ION_COMPUTER_AUTH_KEY")) != "" {
		return "", false, fmt.Errorf("private computer auth key sources conflict")
	}
	if !filepath.IsAbs(path) {
		return "", false, fmt.Errorf("private computer auth key file must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", false, fmt.Errorf("private computer auth key file permissions are unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", false, err
	}
	if len(payload) > 4096 {
		return "", false, fmt.Errorf("private computer auth key file is oversized")
	}
	isolated := strings.EqualFold(
		strings.TrimSpace(os.Getenv("ION_COMPUTER_CONSUME_AUTH_KEY_FILE")),
		"true",
	)
	if isolated {
		if err := file.Close(); err != nil {
			return "", false, err
		}
		if err := os.Remove(path); err != nil {
			return "", false, fmt.Errorf(
				"private computer auth key file could not be consumed: %w",
				err,
			)
		}
	}
	return strings.TrimSpace(string(payload)), isolated, nil
}

func environment(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func integerEnvironment(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}
