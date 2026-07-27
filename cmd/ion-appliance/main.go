package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	ionUID      = 10001
	ionGID      = 10001
	computerUID = 10002
	computerGID = 10002
)

type applianceConfig struct {
	DataRoot         string
	IonData          string
	ComputerRoot     string
	PublicListen     string
	IonListen        string
	ComputerListen   string
	OfficeURL        string
	OfficeEntrypoint string
	WebOrigin        string
	InternalOrigin   string
}

type managedProcess struct {
	name string
	cmd  *exec.Cmd
	done chan error
}

type processExit struct {
	name string
	err  error
}

type appliance struct {
	config    applianceConfig
	logger    *slog.Logger
	ready     atomic.Bool
	processes []*managedProcess
	exits     chan processExit
	proxy     *http.Server
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config, err := loadConfig()
	if err != nil {
		logger.Error("appliance configuration rejected", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	instance := &appliance{
		config: config,
		logger: logger,
		exits:  make(chan processExit, 3),
	}
	if err := instance.run(ctx); err != nil {
		logger.Error("appliance stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (applianceConfig, error) {
	dataRoot := filepath.Clean(environment("ION_APPLIANCE_DATA_ROOT", "/data"))
	if !filepath.IsAbs(dataRoot) {
		return applianceConfig{}, fmt.Errorf("ION_APPLIANCE_DATA_ROOT must be absolute")
	}
	port, err := strconv.Atoi(environment("PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		return applianceConfig{}, fmt.Errorf("PORT must be between 1 and 65535")
	}
	origin := strings.TrimSpace(os.Getenv("ION_WEB_ORIGIN"))
	if origin == "" {
		if domain := strings.TrimSpace(os.Getenv("RAILWAY_PUBLIC_DOMAIN")); domain != "" {
			origin = "https://" + domain
		}
	}
	if origin == "" {
		origin = "http://127.0.0.1:" + strconv.Itoa(port)
	}
	parsedOrigin, err := url.Parse(origin)
	if err != nil ||
		(parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https") ||
		parsedOrigin.Host == "" ||
		parsedOrigin.User != nil ||
		parsedOrigin.RawQuery != "" ||
		parsedOrigin.Fragment != "" {
		return applianceConfig{}, fmt.Errorf("ION_WEB_ORIGIN must be an http or https origin")
	}
	origin = parsedOrigin.Scheme + "://" + parsedOrigin.Host
	if err := os.Setenv("ION_WEB_ORIGIN", origin); err != nil {
		return applianceConfig{}, err
	}
	if railwayDeployment() {
		if origin == "" {
			return applianceConfig{}, fmt.Errorf("ION_WEB_ORIGIN or RAILWAY_PUBLIC_DOMAIN is required")
		}
		if strings.TrimSpace(os.Getenv("ION_AUTH_USERNAME")) == "" ||
			(strings.TrimSpace(os.Getenv("ION_AUTH_PASSWORD")) == "" &&
				strings.TrimSpace(os.Getenv("ION_AUTH_PASSWORD_HASH")) == "") {
			return applianceConfig{}, fmt.Errorf(
				"ION_AUTH_USERNAME and ION_AUTH_PASSWORD or ION_AUTH_PASSWORD_HASH are required",
			)
		}
		if strings.TrimSpace(os.Getenv("ION_VAULT_KEK")) == "" {
			return applianceConfig{}, fmt.Errorf("ION_VAULT_KEK is required on Railway")
		}
	}
	return applianceConfig{
		DataRoot:       dataRoot,
		IonData:        filepath.Join(dataRoot, "ion"),
		ComputerRoot:   filepath.Join(dataRoot, "computer"),
		PublicListen:   net.JoinHostPort("::", strconv.Itoa(port)),
		IonListen:      "127.0.0.1:4174",
		ComputerListen: "127.0.0.1:8081",
		OfficeURL:      "http://127.0.0.1:80",
		OfficeEntrypoint: environment(
			"ION_APPLIANCE_OFFICE_ENTRYPOINT",
			"/app/ds/run-document-server.sh",
		),
		WebOrigin:      origin,
		InternalOrigin: "http://127.0.0.1:" + strconv.Itoa(port),
	}, nil
}

func (instance *appliance) run(ctx context.Context) error {
	if err := instance.prepareData(); err != nil {
		return err
	}
	computerKey, err := instance.loadOrCreateSecret(
		"ION_COMPUTER_AUTH_KEY",
		filepath.Join(instance.config.DataRoot, "appliance", "computer-auth-key"),
	)
	if err != nil {
		return err
	}
	if err := instance.installComputerAuthKey(computerKey); err != nil {
		return err
	}
	officeKey, err := instance.loadOrCreateSecret(
		"ION_OFFICE_JWT_SECRET",
		filepath.Join(instance.config.DataRoot, "appliance", "office-jwt-secret"),
	)
	if err != nil {
		return err
	}
	hostID, err := instance.loadOrCreateHostID()
	if err != nil {
		return err
	}
	computerDigest, err := executableDigest("/usr/local/bin/ion-computer")
	if err != nil {
		return err
	}
	if err := instance.initializeIon(ctx); err != nil {
		return err
	}
	proxyErrors := make(chan error, 1)
	if err := instance.startProxy(proxyErrors); err != nil {
		return err
	}
	defer instance.stopProxy()

	office, err := instance.startProcess(
		"onlyoffice",
		0,
		0,
		instance.officeEnvironment(officeKey),
		instance.config.OfficeEntrypoint,
	)
	if err != nil {
		return err
	}
	instance.processes = append(instance.processes, office)
	if err := waitForHTTP(
		ctx,
		instance.config.OfficeURL+"/healthcheck",
		10*time.Minute,
		office.done,
	); err != nil {
		instance.stopProcesses()
		return fmt.Errorf("onlyoffice readiness: %w", err)
	}

	computer, err := instance.startProcess(
		"ion-computer",
		computerUID,
		computerGID,
		instance.computerEnvironment(hostID, computerDigest),
		"/usr/local/bin/ion-computer",
	)
	if err != nil {
		instance.stopProcesses()
		return err
	}
	instance.processes = append(instance.processes, computer)
	if err := waitForHTTP(
		ctx,
		"http://"+instance.config.ComputerListen+"/readyz",
		2*time.Minute,
		computer.done,
	); err != nil {
		instance.stopProcesses()
		return fmt.Errorf("ion-computer readiness: %w", err)
	}

	ion, err := instance.startProcess(
		"ion",
		ionUID,
		ionGID,
		instance.ionEnvironment(computerKey, officeKey),
		"/usr/local/bin/ion",
		"dashboard",
		"--data-dir",
		instance.config.IonData,
		"--listen",
		instance.config.IonListen,
		"--origin",
		instance.config.WebOrigin,
	)
	if err != nil {
		instance.stopProcesses()
		return err
	}
	instance.processes = append(instance.processes, ion)
	if err := waitForHTTP(
		ctx,
		"http://"+instance.config.IonListen+"/",
		2*time.Minute,
		ion.done,
	); err != nil {
		instance.stopProcesses()
		return fmt.Errorf("ion readiness: %w", err)
	}

	instance.ready.Store(true)
	instance.logger.Info(
		"ion appliance ready",
		"listen",
		instance.config.PublicListen,
		"components",
		[]string{"ion", "ion-computer", "onlyoffice"},
	)
	var runErr error
	select {
	case <-ctx.Done():
	case result := <-instance.exits:
		if result.err == nil {
			runErr = fmt.Errorf("%s exited unexpectedly", result.name)
		} else {
			runErr = fmt.Errorf("%s exited: %w", result.name, result.err)
		}
	case err := <-proxyErrors:
		runErr = fmt.Errorf("public ingress stopped: %w", err)
	}
	instance.ready.Store(false)
	instance.stopProcesses()
	return runErr
}

func (instance *appliance) prepareData() error {
	directories := []struct {
		path string
		mode os.FileMode
		uid  int
		gid  int
	}{
		{instance.config.DataRoot, 0o711, 0, 0},
		{filepath.Join(instance.config.DataRoot, "appliance"), 0o700, 0, 0},
		{instance.config.IonData, 0o700, ionUID, ionGID},
		{instance.config.ComputerRoot, 0o700, computerUID, computerGID},
		{filepath.Join(instance.config.ComputerRoot, "home"), 0o700, computerUID, computerGID},
		{filepath.Join(instance.config.ComputerRoot, "state"), 0o700, computerUID, computerGID},
		{filepath.Join(instance.config.ComputerRoot, "workspaces"), 0o700, computerUID, computerGID},
		{filepath.Join(instance.config.ComputerRoot, "home", ".cache"), 0o700, computerUID, computerGID},
		{filepath.Join(instance.config.ComputerRoot, "home", ".config"), 0o700, computerUID, computerGID},
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory.path, directory.mode); err != nil {
			return fmt.Errorf("create %s: %w", directory.path, err)
		}
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return fmt.Errorf("secure %s: %w", directory.path, err)
		}
		if err := os.Chown(directory.path, directory.uid, directory.gid); err != nil {
			return fmt.Errorf("own %s: %w", directory.path, err)
		}
	}
	if err := chownTree(instance.config.IonData, ionUID, ionGID); err != nil {
		return err
	}
	return chownTree(instance.config.ComputerRoot, computerUID, computerGID)
}

func (instance *appliance) initializeIon(ctx context.Context) error {
	wrappedKey := filepath.Join(instance.config.IonData, "user-key.enc")
	if _, err := os.Stat(wrappedKey); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.TrimSpace(os.Getenv("ION_AUTO_INIT")) == "0" {
		return fmt.Errorf("Ion data is uninitialized and ION_AUTO_INIT=0")
	}
	arguments := []string{"init", "--data-dir", instance.config.IonData}
	if strings.TrimSpace(os.Getenv("ION_DEV_FILE_KEK")) == "1" {
		arguments = append(arguments, "--dev-file-kek")
	} else if strings.TrimSpace(os.Getenv("ION_VAULT_KEK")) == "" {
		return fmt.Errorf("ION_VAULT_KEK is required to initialize the appliance")
	}
	command := exec.CommandContext(ctx, "/usr/local/bin/ion", arguments...)
	command.Env = os.Environ()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = processAttributes(ionUID, ionGID)
	if err := command.Run(); err != nil {
		return fmt.Errorf("initialize Ion: %w", err)
	}
	return nil
}

func (instance *appliance) startProxy(errorsChannel chan<- error) error {
	target, err := url.Parse("http://" + instance.config.IonListen)
	if err != nil {
		return err
	}
	publicOrigin, err := url.Parse(instance.config.WebOrigin)
	if err != nil {
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalHost := request.Host
		originalDirector(request)
		request.Host = originalHost
		request.Header.Set("X-Forwarded-Host", originalHost)
		if request.Header.Get("X-Forwarded-Proto") == "" {
			request.Header.Set("X-Forwarded-Proto", publicOrigin.Scheme)
		}
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
	}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/livez":
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(writer, "live\n")
		case "/readyz":
			if !instance.componentsReady(request.Context()) {
				http.Error(writer, "not ready", http.StatusServiceUnavailable)
				return
			}
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(writer, "ready\n")
		default:
			proxy.ServeHTTP(writer, request)
		}
	})
	listener, err := net.Listen("tcp", instance.config.PublicListen)
	if err != nil {
		return err
	}
	instance.proxy = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		err := instance.proxy.Serve(listener)
		if !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- err
		}
	}()
	return nil
}

func (instance *appliance) componentsReady(parent context.Context) bool {
	if !instance.ready.Load() {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	urls := []string{
		"http://" + instance.config.IonListen + "/",
		"http://" + instance.config.ComputerListen + "/readyz",
		instance.config.OfficeURL + "/healthcheck",
	}
	for _, endpoint := range urls {
		if err := probeHTTP(ctx, endpoint); err != nil {
			return false
		}
	}
	return true
}

func (instance *appliance) stopProxy() {
	if instance.proxy == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = instance.proxy.Shutdown(ctx)
}

func (instance *appliance) startProcess(
	name string,
	uid int,
	gid int,
	environment []string,
	executable string,
	arguments ...string,
) (*managedProcess, error) {
	command := exec.Command(executable, arguments...)
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.SysProcAttr = processAttributes(uid, gid)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	process := &managedProcess{name: name, cmd: command, done: make(chan error, 1)}
	go func() {
		err := command.Wait()
		process.done <- err
		close(process.done)
		instance.exits <- processExit{name: name, err: err}
	}()
	return process, nil
}

func (instance *appliance) stopProcesses() {
	for index := len(instance.processes) - 1; index >= 0; index-- {
		process := instance.processes[index]
		if process.cmd.Process != nil {
			_ = syscall.Kill(-process.cmd.Process.Pid, syscall.SIGTERM)
		}
	}
	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	for _, process := range instance.processes {
		select {
		case <-process.done:
		case <-deadline.C:
			for _, remaining := range instance.processes {
				if remaining.cmd.Process != nil {
					_ = syscall.Kill(-remaining.cmd.Process.Pid, syscall.SIGKILL)
				}
			}
			return
		}
	}
}

func (instance *appliance) ionEnvironment(computerKey, officeKey string) []string {
	environment := append([]string(nil), os.Environ()...)
	environment = setEnvironment(environment, "ION_DATA_DIR", instance.config.IonData)
	environment = setEnvironment(environment, "ION_WEB_LISTEN", instance.config.IonListen)
	environment = setEnvironment(environment, "ION_WEB_ORIGIN", instance.config.WebOrigin)
	environment = setEnvironment(
		environment,
		"ION_COMPUTER_URL",
		"http://"+instance.config.ComputerListen,
	)
	environment = setEnvironment(environment, "ION_COMPUTER_AUTH_KEY", computerKey)
	environment = setEnvironment(environment, "ION_OFFICE_ENABLED", "true")
	environment = setEnvironment(environment, "ION_OFFICE_INTERNAL_URL", instance.config.OfficeURL)
	environment = setEnvironment(environment, "ION_OFFICE_PUBLIC_PATH", "/office-engine/")
	environment = setEnvironment(environment, "ION_OFFICE_PUBLIC_ORIGIN", instance.config.WebOrigin)
	environment = setEnvironment(
		environment,
		"ION_OFFICE_CALLBACK_ORIGIN",
		instance.config.InternalOrigin,
	)
	environment = setEnvironment(environment, "ION_OFFICE_JWT_SECRET", officeKey)
	return environment
}

func (instance *appliance) computerEnvironment(
	hostID string,
	imageDigest string,
) []string {
	home := filepath.Join(instance.config.ComputerRoot, "home")
	authKeyPath := filepath.Join(instance.config.ComputerRoot, "state", "auth-key")
	return []string{
		"PATH=" + environment("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
		"LANG=" + environment("LANG", "C.UTF-8"),
		"HOME=" + home,
		"ION_COMPUTER_LISTEN=" + instance.config.ComputerListen,
		"ION_COMPUTER_AUTH_KEY_FILE=" + authKeyPath,
		"ION_COMPUTER_CONSUME_AUTH_KEY_FILE=true",
		"ION_COMPUTER_HOST_ID=" + hostID,
		"ION_COMPUTER_MODE=" + environment("ION_COMPUTER_MODE", "personal"),
		"ION_COMPUTER_HOME=" + home,
		"ION_COMPUTER_STATE_ROOT=" + filepath.Join(instance.config.ComputerRoot, "state"),
		"ION_COMPUTER_WORKSPACE_ROOT=" + filepath.Join(instance.config.ComputerRoot, "workspaces"),
		"ION_COMPUTER_BROWSER_CONTAINMENT=" + environment(
			"ION_COMPUTER_BROWSER_CONTAINMENT",
			"appliance_boundary",
		),
		"ION_COMPUTER_IMAGE_DIGEST=" + imageDigest,
		"ION_COMPUTER_HOST_VERSION=" + environment(
			"ION_COMPUTER_HOST_VERSION",
			"ion-appliance/0.1.0",
		),
	}
}

func (instance *appliance) installComputerAuthKey(authKey string) error {
	path := filepath.Join(instance.config.ComputerRoot, "state", "auth-key")
	if err := atomicWrite(path, []byte(authKey+"\n"), 0o400); err != nil {
		return fmt.Errorf("install Computer auth key: %w", err)
	}
	if err := os.Chown(path, computerUID, computerGID); err != nil {
		return fmt.Errorf("own Computer auth key: %w", err)
	}
	return nil
}

func (instance *appliance) officeEnvironment(jwtSecret string) []string {
	return []string{
		"PATH=" + environment("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
		"LANG=" + environment("LANG", "en_US.UTF-8"),
		"LANGUAGE=" + environment("LANGUAGE", "en_US:en"),
		"LC_ALL=" + environment("LC_ALL", "en_US.UTF-8"),
		"HOME=/root",
		"PG_VERSION=16",
		"BASE_VERSION=24.04",
		"COMPANY_NAME=onlyoffice",
		"PRODUCT_NAME=documentserver",
		"PRODUCT_EDITION=",
		"DS_PLUGIN_INSTALLATION=false",
		"DS_DOCKER_INSTALLATION=true",
		"ACCEPT_EULA=Y",
		"JWT_ENABLED=true",
		"JWT_SECRET=" + jwtSecret,
		"JWT_HEADER=Authorization",
		"JWT_IN_BODY=true",
		"GENERATE_FONTS=" + environment("GENERATE_FONTS", "true"),
		"NGINX_ACCESS_LOG=false",
	}
}

func (instance *appliance) loadOrCreateSecret(name, path string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		if len(value) < 32 || len(value) > 4096 {
			return "", fmt.Errorf("%s must contain between 32 and 4096 bytes", name)
		}
		return value, nil
	}
	payload, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(string(payload))
		if len(value) < 32 || len(value) > 4096 {
			return "", fmt.Errorf("%s is invalid", path)
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	random := make([]byte, 48)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(random)
	if err := atomicWrite(path, []byte(value+"\n"), 0o600); err != nil {
		return "", err
	}
	return value, nil
}

func (instance *appliance) loadOrCreateHostID() (string, error) {
	if value := strings.TrimSpace(os.Getenv("ION_COMPUTER_HOST_ID")); value != "" {
		if _, err := uuid.Parse(value); err != nil {
			return "", fmt.Errorf("ION_COMPUTER_HOST_ID is invalid")
		}
		return value, nil
	}
	path := filepath.Join(instance.config.DataRoot, "appliance", "computer-host-id")
	payload, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(string(payload))
		if _, err := uuid.Parse(value); err != nil {
			return "", fmt.Errorf("%s is invalid", path)
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	value := uuid.NewString()
	if err := atomicWrite(path, []byte(value+"\n"), 0o600); err != nil {
		return "", err
	}
	return value, nil
}

func processAttributes(uid, gid int) *syscall.SysProcAttr {
	attributes := &syscall.SysProcAttr{Setpgid: true}
	if uid != 0 || gid != 0 {
		attributes.Credential = &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: []uint32{uint32(gid)},
		}
	}
	return attributes
}

func waitForHTTP(
	ctx context.Context,
	endpoint string,
	timeout time.Duration,
	processDone <-chan error,
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := probeHTTP(probeCtx, endpoint)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case processErr := <-processDone:
			if processErr == nil {
				return fmt.Errorf("process exited before becoming ready")
			}
			return processErr
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s", endpoint)
		case <-ticker.C:
		}
	}
}

func probeHTTP(ctx context.Context, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 500 {
		return fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	return nil
}

func executableDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func chownTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("own %s: %w", path, err)
		}
		return nil
	})
}

func atomicWrite(path string, payload []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ion-appliance-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func setEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	for index, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			environment[index] = prefix + value
			return environment
		}
	}
	return append(environment, prefix+value)
}

func railwayDeployment() bool {
	for _, name := range []string{
		"RAILWAY_ENVIRONMENT_ID",
		"RAILWAY_PROJECT_ID",
		"RAILWAY_SERVICE_ID",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func environment(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
