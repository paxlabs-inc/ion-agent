package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/localipc"
	"github.com/paxlabs-inc/ion-agent/internal/operatorapp"
	"github.com/paxlabs-inc/ion-agent/internal/provider"
	tuiassets "github.com/paxlabs-inc/ion-agent/ui/tui"
	webassets "github.com/paxlabs-inc/ion-agent/ui/web"
)

const defaultDashboardAddress = "127.0.0.1:4174"

func runDashboard(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	dataDirectory := flags.String("data-dir", defaultDataDirectory(), "Ion data directory")
	developmentFileKEK := flags.Bool(
		"dev-file-kek", false, "use the development-only file KEK",
	)
	listen := flags.String("listen", defaultDashboardAddress, "loopback listen address")
	origin := flags.String(
		"origin",
		strings.TrimSpace(os.Getenv("ION_WEB_ORIGIN")),
		"exact browser origin allowlist entry",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("ion dashboard: unexpected positional arguments")
	}
	host, port, err := net.SplitHostPort(*listen)
	if err != nil {
		return fmt.Errorf("ion dashboard: invalid listen address: %w", err)
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	if address == nil || !address.IsLoopback() {
		return fmt.Errorf(
			"ion dashboard: plain HTTP may only listen on loopback; use a TLS reverse proxy for remote access",
		)
	}
	allowedOrigin := strings.TrimSpace(*origin)
	if allowedOrigin == "" {
		allowedOrigin = "http://" + net.JoinHostPort(host, port)
	}
	runtime, err := openOperatorRuntime(ctx, *dataDirectory, *developmentFileKEK)
	if err != nil {
		return err
	}
	defer runtime.Close()
	assets, err := webassets.Distribution()
	if err != nil {
		return err
	}
	operatorDirectory := filepath.Join(*dataDirectory, "operator")
	if err := os.MkdirAll(operatorDirectory, 0o700); err != nil {
		return fmt.Errorf("ion dashboard: create operator directory: %w", err)
	}
	actorID, err := loadOrCreateActorID(operatorDirectory)
	if err != nil {
		return fmt.Errorf("ion dashboard: operator identity: %w", err)
	}
	handler, err := runtime.BrowserHandler(assets, allowedOrigin, actorID)
	if err != nil {
		return err
	}
	socketPath := filepath.Join(operatorDirectory, "controlplane.sock")
	brokerPath := filepath.Join(operatorDirectory, "capability.sock")
	localCtx, cancelLocal := context.WithCancel(ctx)
	defer cancelLocal()
	localErrors := make(chan error, 2)
	go func() { localErrors <- runtime.ServeLocal(localCtx, socketPath) }()
	go func() {
		localErrors <- serveCapabilityBroker(localCtx, brokerPath, actorID, runtime)
	}()
	if err := waitForPath(ctx, socketPath, 5*time.Second); err != nil {
		return err
	}
	server := &http.Server{
		Addr: *listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second,
		WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	fmt.Printf("Ion dashboard ready at %s\n", allowedOrigin)
	fmt.Printf("TUI attach endpoint ready in %s\n", operatorDirectory)
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-localErrors:
		if ctx.Err() != nil || err == nil {
			return nil
		}
		return err
	case <-ctx.Done():
		return nil
	}
}

func runTUI(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	dataDirectory := flags.String("data-dir", defaultDataDirectory(), "Ion data directory")
	developmentFileKEK := flags.Bool(
		"dev-file-kek", false, "use the development-only file KEK",
	)
	attach := flags.Bool("attach", false, "attach to a running dashboard daemon")
	check := flags.Bool("check", false, "verify the terminal client can connect, then exit")
	startupTimeout := flags.Duration("startup-timeout", 10*time.Second, "bounded startup window")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("ion tui: unexpected positional arguments")
	}
	operatorDirectory := filepath.Join(*dataDirectory, "operator")
	socketPath := filepath.Join(operatorDirectory, "controlplane.sock")
	if *attach {
		capability, err := requestCapability(
			ctx,
			filepath.Join(operatorDirectory, "capability.sock"),
			*startupTimeout,
		)
		if err != nil {
			return fmt.Errorf("ion tui attach: %w", err)
		}
		return launchTUI(ctx, *dataDirectory, socketPath, capability, *check)
	}
	runtime, err := openOperatorRuntime(ctx, *dataDirectory, *developmentFileKEK)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := os.MkdirAll(operatorDirectory, 0o700); err != nil {
		return err
	}
	localCtx, cancelLocal := context.WithCancel(ctx)
	defer cancelLocal()
	localErrors := make(chan error, 1)
	go func() { localErrors <- runtime.ServeLocal(localCtx, socketPath) }()
	if err := waitForPath(ctx, socketPath, *startupTimeout); err != nil {
		return err
	}
	actorID, err := loadOrCreateActorID(operatorDirectory)
	if err != nil {
		return fmt.Errorf("ion tui: operator identity: %w", err)
	}
	capability, err := runtime.IssueCapability(actorID)
	if err != nil {
		return err
	}
	runErr := launchTUI(ctx, *dataDirectory, socketPath, capability.Value, *check)
	cancelLocal()
	select {
	case localErr := <-localErrors:
		if localErr != nil && ctx.Err() == nil {
			return errors.Join(runErr, localErr)
		}
	case <-time.After(2 * time.Second):
		return errors.Join(runErr, errors.New("ion tui: local daemon shutdown timed out"))
	}
	return runErr
}

func openOperatorRuntime(
	ctx context.Context,
	dataDirectory string,
	developmentFileKEK bool,
) (*operatorapp.Runtime, error) {
	pricing, err := configuredProviderPricing()
	if err != nil {
		return nil, err
	}
	authPassword := consumeEnvironmentSecret("ION_AUTH_PASSWORD")
	authPasswordHash := consumeEnvironmentSecret("ION_AUTH_PASSWORD_HASH")
	officeJWTSecret, err := consumeEnvironmentOrFileSecret(
		"ION_OFFICE_JWT_SECRET",
	)
	if err != nil {
		return nil, err
	}
	officeEnabled, err := parseOptionalBool("ION_OFFICE_ENABLED")
	if err != nil {
		return nil, err
	}
	officeMaxFileBytes, err := parseOptionalPositiveInt64("ION_OFFICE_MAX_FILE_BYTES")
	if err != nil {
		return nil, err
	}
	officeMaxVersionsRaw, err := parseOptionalPositiveInt64("ION_OFFICE_MAX_VERSIONS")
	if err != nil {
		return nil, err
	}
	if officeMaxFileBytes > 1<<30 {
		return nil, fmt.Errorf("ion: ION_OFFICE_MAX_FILE_BYTES cannot exceed 1073741824")
	}
	if officeMaxVersionsRaw > 10_000 {
		return nil, fmt.Errorf("ion: ION_OFFICE_MAX_VERSIONS cannot exceed 10000")
	}
	return operatorapp.OpenRuntime(ctx, operatorapp.RuntimeConfig{
		DataDirectory: dataDirectory, DevelopmentFileKEK: developmentFileKEK,
		AuthUsername:     os.Getenv("ION_AUTH_USERNAME"),
		AuthPassword:     authPassword,
		AuthPasswordHash: authPasswordHash,
		RailwayDeployment: firstNonEmpty(
			os.Getenv("RAILWAY_ENVIRONMENT_ID"),
			os.Getenv("RAILWAY_PROJECT_ID"),
			os.Getenv("RAILWAY_SERVICE_ID"),
		) != "",
		ProviderName:      os.Getenv("PROVIDER_NAME"),
		ProviderBaseURL:   os.Getenv("PROVIDER_BASE_URL"),
		ProviderAPIKey:    os.Getenv("PROVIDER_API_KEY"),
		ProviderModel:     os.Getenv("LLM_MODEL"),
		ProviderPricing:   pricing,
		NovitaAPIKey:      os.Getenv("NOVITA_API_KEY"),
		NovitaBaseURL:     os.Getenv("NOVITA_BASE_URL"),
		OfficeEnabled:     officeEnabled,
		OfficeInternalURL: os.Getenv("ION_OFFICE_INTERNAL_URL"),
		OfficePublicPath:  os.Getenv("ION_OFFICE_PUBLIC_PATH"),
		OfficeCallbackOrigin: firstNonEmpty(
			os.Getenv("ION_OFFICE_CALLBACK_ORIGIN"),
			os.Getenv("ION_WEB_ORIGIN"),
		),
		OfficeJWTSecret:    officeJWTSecret,
		OfficeMaxFileBytes: officeMaxFileBytes,
		OfficeMaxVersions:  int(officeMaxVersionsRaw),
		BrowserExecutable:  os.Getenv("ION_BROWSER_EXECUTABLE"),
		AgentEmailAddress: firstNonEmpty(
			os.Getenv("MACHINE_MAIL_ADDRESS"), os.Getenv("ION_AGENT_EMAIL"),
		),
		AgentIMAPAddress:       os.Getenv("ION_AGENT_IMAP_ADDRESS"),
		AgentIMAPUsername:      os.Getenv("ION_AGENT_IMAP_USERNAME"),
		AgentIMAPPassword:      os.Getenv("ION_AGENT_IMAP_PASSWORD"),
		MachineMailURL:         os.Getenv("MACHINE_MAIL_URL"),
		MachineMailAPIKey:      os.Getenv("MACHINE_MAIL_API_KEY"),
		TelegramBotToken:       os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramAllowedUsers:   os.Getenv("TELEGRAM_ALLOWED_USERS"),
		WorkspaceDirectory:     os.Getenv("ION_WORKSPACE"),
		SkillLibraryDirectory:  os.Getenv("ION_SKILL_LIBRARY"),
		ProjectWorkspaceRoot:   os.Getenv("ION_PROJECT_WORKSPACES"),
		ContainerRuntime:       os.Getenv("ION_CONTAINER_RUNTIME"),
		ContainerImage:         os.Getenv("ION_CONTAINER_IMAGE"),
		PrivateComputerURL:     os.Getenv("ION_COMPUTER_URL"),
		PrivateComputerAuthKey: os.Getenv("ION_COMPUTER_AUTH_KEY"),
		TavilyAPIKey: firstNonEmpty(
			os.Getenv("ION_TAVILY_API_KEY"),
			os.Getenv("TAVILY_API_KEY"),
		),
		TavilySearchEndpoint: os.Getenv("ION_TAVILY_SEARCH_ENDPOINT"),
		SearchEndpoint: firstNonEmpty(
			os.Getenv("ION_SEARCH_ENDPOINT"),
			os.Getenv("SEARXNG_URL"),
		),
	})
}

func parseOptionalBool(name string) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("ion: %s must be true or false", name)
	}
	return parsed, nil
}

func parseOptionalPositiveInt64(name string) (int64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("ion: %s must be a positive integer", name)
	}
	return parsed, nil
}

func consumeEnvironmentSecret(name string) string {
	value := os.Getenv(name)
	_ = os.Unsetenv(name)
	return value
}

func consumeEnvironmentOrFileSecret(name string) (string, error) {
	value := consumeEnvironmentSecret(name)
	fileName := name + "_FILE"
	path := strings.TrimSpace(consumeEnvironmentSecret(fileName))
	if value != "" && path != "" {
		return "", fmt.Errorf("ion: %s and %s cannot both be set", name, fileName)
	}
	if path == "" {
		return value, nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("ion: %s must be an absolute path", fileName)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("ion: open %s: %w", fileName, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("ion: inspect %s: %w", fileName, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("ion: %s permissions are unsafe", fileName)
	}
	payload, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", fmt.Errorf("ion: read %s: %w", fileName, err)
	}
	if len(payload) > 4096 {
		return "", fmt.Errorf("ion: %s is oversized", fileName)
	}
	return strings.TrimSpace(string(payload)), nil
}

func configuredProviderPricing() (*provider.TokenPricing, error) {
	inputRaw := strings.TrimSpace(
		os.Getenv("PROVIDER_INPUT_MICROCENTS_PER_TOKEN"),
	)
	cachedRaw := strings.TrimSpace(
		os.Getenv("PROVIDER_CACHED_INPUT_MICROCENTS_PER_TOKEN"),
	)
	outputRaw := strings.TrimSpace(
		os.Getenv("PROVIDER_OUTPUT_MICROCENTS_PER_TOKEN"),
	)
	surchargeRaw := strings.TrimSpace(os.Getenv("PROVIDER_SURCHARGE_BPS"))
	if inputRaw == "" && cachedRaw == "" && outputRaw == "" &&
		surchargeRaw == "" {
		return nil, nil
	}
	if inputRaw == "" || outputRaw == "" {
		return nil, fmt.Errorf(
			"ion: provider pricing requires input and output microcents per token",
		)
	}
	parse := func(name, value string) (int64, error) {
		if value == "" {
			return 0, nil
		}
		parsed, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || parsed < 0 {
			return 0, fmt.Errorf("ion: invalid %s", name)
		}
		return parsed, nil
	}
	input, err := parse("provider input price", inputRaw)
	if err != nil {
		return nil, err
	}
	cached := input
	if cachedRaw != "" {
		cached, err = parse("provider cached-input price", cachedRaw)
		if err != nil {
			return nil, err
		}
	}
	output, err := parse("provider output price", outputRaw)
	if err != nil {
		return nil, err
	}
	surcharge, err := parse("provider surcharge", surchargeRaw)
	if err != nil {
		return nil, err
	}
	return &provider.TokenPricing{
		InputMicrocentsPerToken:       input,
		CachedInputMicrocentsPerToken: cached,
		OutputMicrocentsPerToken:      output,
		ProviderSurchargeBPS:          surcharge,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func serveCapabilityBroker(
	ctx context.Context,
	socketPath string,
	actorID uuid.UUID,
	runtime *operatorapp.Runtime,
) error {
	listener, err := localipc.Listen(socketPath)
	if err != nil {
		return fmt.Errorf("ion dashboard: capability broker: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()
	server := &http.Server{
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost || request.URL.Path != "/capability" {
				http.NotFound(writer, request)
				return
			}
			capability, issueErr := runtime.IssueCapability(actorID)
			if issueErr != nil {
				http.Error(writer, "capability unavailable", http.StatusServiceUnavailable)
				return
			}
			writer.Header().Set("Cache-Control", "no-store")
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"capability": capability.Value,
			})
		}),
	}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func requestCapability(
	ctx context.Context,
	socketPath string,
	timeout time.Duration,
) (string, error) {
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "http://unix/capability", nil,
	)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("capability broker returned %s", response.Status)
	}
	var payload struct {
		Capability string `json:"capability"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Capability) == "" {
		return "", errors.New("capability broker returned an empty credential")
	}
	return payload.Capability, nil
}

func launchTUI(
	ctx context.Context,
	dataDirectory string,
	socketPath string,
	capability string,
	check bool,
) error {
	node, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("ion tui: Node.js 22+ is required: %w", err)
	}
	hash := sha256.Sum256(tuiassets.Bundle)
	artifactDirectory := filepath.Join(dataDirectory, "operator", "artifacts")
	if err := os.MkdirAll(artifactDirectory, 0o700); err != nil {
		return err
	}
	artifact := filepath.Join(
		artifactDirectory,
		fmt.Sprintf("ion-tui-%x.mjs", hash[:8]),
	)
	if err := writeArtifact(artifact, tuiassets.Bundle); err != nil {
		return err
	}
	arguments := []string{
		artifact,
		"--socket", socketPath,
		"--capability", capability,
		"--client-id", "ion-tui-" + uuid.NewString(),
	}
	if check {
		arguments = append(arguments, "--check", "true")
	}
	command := exec.CommandContext(ctx, node, arguments...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	if err := command.Run(); err != nil {
		return fmt.Errorf("ion tui: terminal client: %w", err)
	}
	return nil
}

func writeArtifact(path string, content []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && sha256.Sum256(existing) == sha256.Sum256(content) {
		return os.Chmod(path, 0o700)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".ion-tui-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o700); err != nil {
		_ = temp.Close()
		return err
	}
	writer := bufio.NewWriter(temp)
	if _, err := writer.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := writer.Flush(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func waitForPath(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil {
			if info.Mode().Perm() != 0o600 {
				return fmt.Errorf("operator socket permissions are %o, want 600", info.Mode().Perm())
			}
			connection, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
			if dialErr == nil {
				_ = connection.Close()
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("operator startup timed out waiting for %s", path)
		case <-ticker.C:
		}
	}
}

func loadOrCreateActorID(operatorDirectory string) (uuid.UUID, error) {
	if err := os.MkdirAll(operatorDirectory, 0o700); err != nil {
		return uuid.Nil, err
	}
	directoryInfo, err := os.Stat(operatorDirectory)
	if err != nil {
		return uuid.Nil, err
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o077 != 0 {
		return uuid.Nil, fmt.Errorf(
			"operator directory permissions are %o, want 700",
			directoryInfo.Mode().Perm(),
		)
	}
	path := filepath.Join(operatorDirectory, "actor-id")
	content, err := os.ReadFile(path)
	if err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return uuid.Nil, statErr
		}
		if info.Mode().Perm() != 0o600 {
			return uuid.Nil, fmt.Errorf(
				"operator actor identity permissions are %o, want 600",
				info.Mode().Perm(),
			)
		}
		actorID, parseErr := uuid.Parse(strings.TrimSpace(string(content)))
		if parseErr != nil || actorID == uuid.Nil {
			return uuid.Nil, fmt.Errorf("invalid persisted operator actor identity")
		}
		return actorID, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return uuid.Nil, err
	}
	actorID := uuid.New()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return loadOrCreateActorID(operatorDirectory)
	}
	if err != nil {
		return uuid.Nil, err
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	if _, err := fmt.Fprintln(file, actorID.String()); err != nil {
		return uuid.Nil, err
	}
	if err := file.Sync(); err != nil {
		return uuid.Nil, err
	}
	if err := file.Close(); err != nil {
		return uuid.Nil, err
	}
	success = true
	return actorID, nil
}
