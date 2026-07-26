// Package browser provides Ion' native, policy-bound browser runtime.
// It talks to a locally installed Chromium browser over CDP and does not rely
// on an application API or an MCP server.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/controllease"
	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

const (
	defaultTimeout = 25 * time.Second
	maxPageText    = 24 << 10
	maxElements    = 200
)

var (
	refPattern = regexp.MustCompile(`^p[0-9]{1,12}$`)
)

// Config controls the local browser process. AllowPrivateNetwork and
// DisableSandbox exist only for hermetic acceptance tests; production leaves
// both false.
type Config struct {
	ExecutablePath      string
	ProfileRoot         string
	AllowPrivateNetwork bool
	DisableSandbox      bool
	Control             *controllease.Service
}

// Element is one bounded, model-addressable interactive or semantic element.
type Element struct {
	Ref         string `json:"ref"`
	Tag         string `json:"tag"`
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	Name        string `json:"name,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// Snapshot is a bounded semantic projection of the current page.
type Snapshot struct {
	URL              string    `json:"url"`
	Title            string    `json:"title"`
	Text             string    `json:"text"`
	Elements         []Element `json:"elements"`
	Truncated        bool      `json:"truncated"`
	UntrustedContent bool      `json:"untrusted_content"`
}

type PreviewDiagnostic struct {
	Source   string   `json:"source"`
	Severity string   `json:"severity"`
	Code     string   `json:"code,omitempty"`
	Message  string   `json:"message"`
	Path     string   `json:"path,omitempty"`
	Line     int      `json:"line,omitempty"`
	Column   int      `json:"column,omitempty"`
	Evidence []string `json:"causal_evidence,omitempty"`
}

type AccessibilityFinding struct {
	Ref     string `json:"ref"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type PreviewInspection struct {
	Snapshot      Snapshot               `json:"snapshot"`
	ScreenshotPNG string                 `json:"screenshot_png"`
	Width         int64                  `json:"width"`
	Height        int64                  `json:"height"`
	DarkMode      bool                   `json:"dark_mode"`
	Diagnostics   []PreviewDiagnostic    `json:"diagnostics,omitempty"`
	Accessibility []AccessibilityFinding `json:"accessibility,omitempty"`
}

type session struct {
	ctx           context.Context
	cancel        context.CancelFunc
	allocCancel   context.CancelFunc
	mu            sync.Mutex
	guardMu       sync.RWMutex
	allowedOrigin string
}

// Service owns one isolated Chromium context per authenticated conversation.
type Service struct {
	executable     string
	profileRoot    string
	allowPrivate   bool
	disableSandbox bool
	readinessErr   error
	control        *controllease.Service

	mu       sync.Mutex
	sessions map[string]*session
	nextRef  atomic.Uint64
	closed   bool
}

// New validates the browser installation without launching it.
func New(config Config) (*Service, error) {
	executable, readinessErr := findExecutable(config.ExecutablePath)
	root := strings.TrimSpace(config.ProfileRoot)
	if root == "" {
		return nil, fmt.Errorf("browser: profile root is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("browser: create profile root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("browser: secure profile root: %w", err)
	}
	return &Service{
		executable: executable, profileRoot: root,
		allowPrivate:   config.AllowPrivateNetwork,
		disableSandbox: config.DisableSandbox,
		readinessErr:   readinessErr, control: config.Control,
		sessions: make(map[string]*session),
	}, nil
}

// Executable returns the configured local browser path for readiness probes.
func (service *Service) Executable() string {
	if service == nil {
		return ""
	}
	return service.executable
}

// Ready reports the local browser dependency without launching a process.
func (service *Service) Ready() error {
	if service == nil {
		return errors.New("browser: native browser is not configured")
	}
	if service.readinessErr != nil {
		return service.readinessErr
	}
	info, err := os.Stat(service.executable)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return errors.New("browser: configured Chromium executable is unavailable")
	}
	return nil
}

// Navigate opens a public HTTP(S) page and returns a fresh semantic snapshot.
func (service *Service) Navigate(
	ctx context.Context,
	rawURL string,
) (Snapshot, error) {
	release, err := service.beginAutomation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.navigate(ctx, rawURL)
}

func (service *Service) navigate(
	ctx context.Context,
	rawURL string,
) (Snapshot, error) {
	if err := service.validateURL(ctx, rawURL); err != nil {
		return Snapshot{}, err
	}
	current, err := service.session(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	runCtx, cancel := scopedContext(ctx, current.ctx, defaultTimeout)
	defer cancel()
	if err := chromedp.Run(runCtx,
		chromedp.Navigate(strings.TrimSpace(rawURL)),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return Snapshot{}, translateContextError(runCtx, err)
	}
	return service.observeLocked(runCtx)
}

// Observe returns the current page without causing a new navigation.
func (service *Service) Observe(ctx context.Context) (Snapshot, error) {
	release, err := service.beginAutomation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.observe(ctx)
}

func (service *Service) observe(ctx context.Context) (Snapshot, error) {
	current, err := service.session(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	runCtx, cancel := scopedContext(ctx, current.ctx, defaultTimeout)
	defer cancel()
	return service.observeLocked(runCtx)
}

// InspectPreview captures a semantic snapshot and screenshot at an exact
// viewport. It is intended for an isolated browser service whose caller
// supplies only a runtime-manager-owned loopback URL.
func (service *Service) InspectPreview(ctx context.Context, rawURL string, width, height int64,
	dark bool) (PreviewInspection, error) {
	if width < 320 || width > 2560 || height < 480 || height > 1600 {
		return PreviewInspection{}, fmt.Errorf("browser: preview viewport is out of bounds")
	}
	if err := service.validateURL(ctx, rawURL); err != nil {
		return PreviewInspection{}, err
	}
	current, err := service.session(ctx)
	if err != nil {
		return PreviewInspection{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	parsedOrigin, _ := url.Parse(strings.TrimSpace(rawURL))
	current.guardMu.Lock()
	current.allowedOrigin = parsedOrigin.Scheme + "://" + parsedOrigin.Host
	current.guardMu.Unlock()
	defer func() {
		current.guardMu.Lock()
		current.allowedOrigin = ""
		current.guardMu.Unlock()
	}()
	runCtx, cancel := scopedContext(ctx, current.ctx, defaultTimeout)
	defer cancel()
	diagnostics := []PreviewDiagnostic{}
	var diagnosticMu sync.Mutex
	chromedp.ListenTarget(runCtx, func(event any) {
		diagnosticMu.Lock()
		defer diagnosticMu.Unlock()
		switch value := event.(type) {
		case *cdpruntime.EventConsoleAPICalled:
			parts := make([]string, 0, len(value.Args))
			for _, argument := range value.Args {
				if argument.Value != nil {
					raw, _ := json.Marshal(argument.Value)
					parts = append(parts, string(raw))
				} else if argument.Description != "" {
					parts = append(parts, argument.Description)
				}
			}
			diagnostic := PreviewDiagnostic{Source: "console", Severity: string(value.Type), Message: strings.Join(parts, " ")}
			if value.StackTrace != nil && len(value.StackTrace.CallFrames) > 0 {
				frame := value.StackTrace.CallFrames[0]
				diagnostic.Path, diagnostic.Line, diagnostic.Column = frame.URL, int(frame.LineNumber)+1, int(frame.ColumnNumber)+1
				diagnostic.Evidence = []string{"source-map-location:" + frame.URL}
			}
			diagnostics = append(diagnostics, diagnostic)
		case *network.EventLoadingFailed:
			diagnostics = append(diagnostics, PreviewDiagnostic{Source: "network", Severity: "error", Code: string(value.Type), Message: value.ErrorText,
				Evidence: []string{"request_id:" + string(value.RequestID)}})
		case *network.EventResponseReceived:
			if value.Response.Status >= 400 {
				diagnostics = append(diagnostics, PreviewDiagnostic{Source: "network", Severity: "error",
					Code: fmt.Sprintf("http_%d", int(value.Response.Status)), Message: value.Response.URL,
					Path: value.Response.URL, Evidence: []string{"request_id:" + string(value.RequestID)}})
			}
		}
	})
	features := []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: "light"}}
	if dark {
		features[0].Value = "dark"
	}
	var screenshot []byte
	if err := chromedp.Run(runCtx,
		network.Enable(), cdpruntime.Enable(),
		emulation.SetDeviceMetricsOverride(width, height, 1, false),
		emulation.SetEmulatedMedia().WithFeatures(features),
		chromedp.Navigate(strings.TrimSpace(rawURL)),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.FullScreenshot(&screenshot, 90),
	); err != nil {
		return PreviewInspection{}, translateContextError(runCtx, err)
	}
	snapshot, err := service.observeLocked(runCtx)
	if err != nil {
		return PreviewInspection{}, err
	}
	accessibility := []AccessibilityFinding{}
	for _, element := range snapshot.Elements {
		if (element.Tag == "button" || element.Tag == "input" || element.Tag == "select" || element.Tag == "textarea") &&
			strings.TrimSpace(element.Name+element.Text+element.Placeholder) == "" {
			accessibility = append(accessibility, AccessibilityFinding{Ref: element.Ref,
				Rule: "interactive-name", Message: "Interactive element has no accessible name."})
		}
	}
	diagnosticMu.Lock()
	resultDiagnostics := append([]PreviewDiagnostic(nil), diagnostics...)
	diagnosticMu.Unlock()
	return PreviewInspection{Snapshot: snapshot, ScreenshotPNG: "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot),
		Width: width, Height: height, DarkMode: dark, Diagnostics: resultDiagnostics, Accessibility: accessibility}, nil
}

// Interact performs one reversible interaction. Sensitive inputs and
// consequential controls are rejected and must use a supervised boundary.
func (service *Service) Interact(
	ctx context.Context,
	action string,
	ref string,
	value string,
) (Snapshot, error) {
	release, err := service.beginAutomation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.interact(ctx, action, ref, value)
}

func (service *Service) interact(
	ctx context.Context,
	action string,
	ref string,
	value string,
) (Snapshot, error) {
	selector, err := selectorForRef(ref)
	if err != nil {
		return Snapshot{}, err
	}
	current, err := service.session(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	runCtx, cancel := scopedContext(ctx, current.ctx, defaultTimeout)
	defer cancel()
	meta, err := inspectElement(runCtx, selector)
	if err != nil {
		return Snapshot{}, err
	}
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "fill":
		if meta.Sensitive {
			return Snapshot{}, errors.New(
				"browser: sensitive field requires a private human or verification handoff",
			)
		}
		if meta.Tag != "input" && meta.Tag != "textarea" {
			return Snapshot{}, fmt.Errorf("browser: %s is not a fillable field", ref)
		}
		if err := fillWithKeyboard(runCtx, selector, value); err != nil {
			return Snapshot{}, err
		}
	case "click":
		if meta.NewContext || meta.Download {
			return Snapshot{}, errors.New(
				"browser: popup and download controls require a human takeover",
			)
		}
		if meta.Consequential {
			return Snapshot{}, errors.New(
				"browser: consequential control requires browser_submit approval",
			)
		}
		if err := runClick(runCtx, selector); err != nil {
			return Snapshot{}, err
		}
	default:
		return Snapshot{}, fmt.Errorf("browser: unsupported reversible action %q", action)
	}
	return service.observeLocked(runCtx)
}

// Submit activates one consequential control. The caller must route this
// through the RED production tool registration before reaching this method.
func (service *Service) Submit(ctx context.Context, ref string) (Snapshot, error) {
	release, err := service.beginAutomation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.submit(ctx, ref)
}

func (service *Service) submit(ctx context.Context, ref string) (Snapshot, error) {
	selector, err := selectorForRef(ref)
	if err != nil {
		return Snapshot{}, err
	}
	current, err := service.session(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	runCtx, cancel := scopedContext(ctx, current.ctx, defaultTimeout)
	defer cancel()
	meta, err := inspectElement(runCtx, selector)
	if err != nil {
		return Snapshot{}, err
	}
	if !meta.Consequential {
		return Snapshot{}, errors.New(
			"browser: browser_submit only accepts a submit or consequential control",
		)
	}
	if meta.NewContext || meta.Download {
		return Snapshot{}, errors.New(
			"browser: popup and download controls require a human takeover",
		)
	}
	if err := runClick(runCtx, selector); err != nil {
		return Snapshot{}, err
	}
	return service.observeLocked(runCtx)
}

// FillVerification inserts a secret without returning it through a tool result.
// It is intentionally separate from Interact and is called only after RED
// approval by the mailbox bridge.
func (service *Service) FillVerification(
	ctx context.Context,
	ref string,
	secret string,
	expectedDomain string,
) (Snapshot, error) {
	return service.fillSensitive(ctx, ref, secret, expectedDomain, "verification")
}

// FillCredential inserts a protected credential without exposing it through
// a model-visible argument or result.
func (service *Service) FillCredential(
	ctx context.Context,
	ref string,
	secret string,
	expectedOrigin string,
) (Snapshot, error) {
	parsed, err := url.Parse(strings.TrimSpace(expectedOrigin))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.Path != "" && parsed.Path != "/" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return Snapshot{}, errors.New("browser: credential origin is invalid")
	}
	return service.fillSensitive(
		ctx,
		ref,
		secret,
		parsed.Scheme+"://"+parsed.Host,
		"credential",
	)
}

func (service *Service) fillSensitive(
	ctx context.Context,
	ref string,
	secret string,
	expectedTarget string,
	kind string,
) (Snapshot, error) {
	release, err := service.beginAutomation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	selector, err := selectorForRef(ref)
	if err != nil {
		return Snapshot{}, err
	}
	current, err := service.session(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	runCtx, cancel := scopedContext(ctx, current.ctx, defaultTimeout)
	defer cancel()
	var currentURL string
	if err := chromedp.Run(runCtx, chromedp.Location(&currentURL)); err != nil {
		return Snapshot{}, err
	}
	parsed, err := url.Parse(currentURL)
	if err != nil {
		return Snapshot{}, errors.New("browser: active page URL is invalid")
	}
	if kind == "credential" {
		if !strings.EqualFold(
			parsed.Scheme+"://"+parsed.Host,
			strings.TrimSpace(expectedTarget),
		) {
			return Snapshot{}, errors.New(
				"browser: active page origin does not match the protected credential origin",
			)
		}
	} else if !strings.EqualFold(
		parsed.Hostname(), strings.TrimSpace(expectedTarget),
	) {
		return Snapshot{}, errors.New(
			"browser: active page domain does not match the approved verification domain",
		)
	}
	meta, err := inspectElement(runCtx, selector)
	if err != nil {
		return Snapshot{}, err
	}
	if !meta.Sensitive {
		return Snapshot{}, errors.New(
			"browser: protected handoff requires a sensitive field",
		)
	}
	if err := fillWithKeyboard(runCtx, selector, secret); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := service.observeLocked(runCtx)
	if err != nil {
		return Snapshot{}, err
	}
	redactSnapshot(&snapshot, []string{secret})
	return snapshot, nil
}

// fillWithKeyboard emits the same focus, key, input, and change signals a
// human edit produces. Direct DOM value assignment does not update controlled
// React/Vue form state and can make a later approved submit send stale data.
func fillWithKeyboard(ctx context.Context, selector string, value string) error {
	return chromedp.Run(ctx,
		chromedp.Focus(selector, chromedp.ByQuery),
		chromedp.Clear(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, value, chromedp.ByQuery),
	)
}

// OpenVerification follows a server-side confirmation link without exposing it
// through a model-visible browser argument or result.
func (service *Service) OpenVerification(
	ctx context.Context,
	rawURL string,
) (Snapshot, error) {
	snapshot, err := service.Navigate(ctx, rawURL)
	if err != nil {
		return Snapshot{}, err
	}
	secrets := []string{rawURL}
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil {
		for _, values := range parsed.Query() {
			secrets = append(secrets, values...)
		}
	}
	redactSnapshot(&snapshot, secrets)
	return snapshot, nil
}

func (service *Service) ObserveWithLease(
	ctx context.Context,
	leaseID uuid.UUID,
	revision uint64,
) (Snapshot, error) {
	release, err := service.beginOperator(ctx, leaseID, revision)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.observe(ctx)
}

func (service *Service) NavigateWithLease(
	ctx context.Context,
	leaseID uuid.UUID,
	revision uint64,
	rawURL string,
) (Snapshot, error) {
	release, err := service.beginOperator(ctx, leaseID, revision)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.navigate(ctx, rawURL)
}

func (service *Service) InteractWithLease(
	ctx context.Context,
	leaseID uuid.UUID,
	revision uint64,
	action string,
	ref string,
	value string,
) (Snapshot, error) {
	release, err := service.beginOperator(ctx, leaseID, revision)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.interact(ctx, action, ref, value)
}

func (service *Service) SubmitWithLease(
	ctx context.Context,
	leaseID uuid.UUID,
	revision uint64,
	ref string,
) (Snapshot, error) {
	release, err := service.beginOperator(ctx, leaseID, revision)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return service.submit(ctx, ref)
}

func (service *Service) beginAutomation(ctx context.Context) (func(), error) {
	if service.control == nil {
		return func() {}, nil
	}
	target, err := ControlTargetFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.control.BeginAutomation(ctx, target)
}

func (service *Service) beginOperator(
	ctx context.Context,
	leaseID uuid.UUID,
	revision uint64,
) (func(), error) {
	if service.control == nil {
		return nil, errors.New("browser: operator control is unavailable")
	}
	target, err := ControlTargetFromContext(ctx)
	if err != nil {
		return nil, err
	}
	release, _, err := service.control.BeginOperator(
		ctx, target, leaseID, revision,
	)
	return release, err
}

func (service *Service) observeLocked(runCtx context.Context) (Snapshot, error) {
	var snapshot Snapshot
	start := service.nextRef.Add(maxElements) - maxElements
	script := strings.Replace(snapshotScript, "__ION_REF_START__", strconv.FormatUint(start, 10), 1)
	if err := chromedp.Run(runCtx, chromedp.Evaluate(script, &snapshot)); err != nil {
		return Snapshot{}, err
	}
	if len(snapshot.Text) > maxPageText {
		snapshot.Text = snapshot.Text[:maxPageText]
		snapshot.Truncated = true
	}
	if len(snapshot.Elements) > maxElements {
		snapshot.Elements = snapshot.Elements[:maxElements]
		snapshot.Truncated = true
	}
	snapshot.URL = sanitizeDisplayedURL(snapshot.URL)
	snapshot.UntrustedContent = true
	return snapshot, nil
}

func (service *Service) session(ctx context.Context) (*session, error) {
	if err := service.Ready(); err != nil {
		return nil, err
	}
	key, err := scopeKey(ctx)
	if err != nil {
		return nil, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return nil, errors.New("browser: service is closed")
	}
	if found := service.sessions[key]; found != nil {
		return found, nil
	}
	profile := filepath.Join(service.profileRoot, key)
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	options := append([]chromedp.ExecAllocatorOption{},
		chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options,
		chromedp.ExecPath(service.executable),
		chromedp.UserDataDir(profile),
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("password-store", "basic"),
	)
	if service.disableSandbox {
		options = append(options, chromedp.NoSandbox)
	}
	allocator, allocCancel := chromedp.NewExecAllocator(
		context.Background(), options...,
	)
	browserCtx, cancel := chromedp.NewContext(
		allocator,
		// CDP event parse failures must not dump raw browser events, URLs, or
		// form data into process logs. Command errors still propagate normally.
		chromedp.WithErrorf(func(string, ...any) {}),
	)
	found := &session{
		ctx: browserCtx, cancel: cancel, allocCancel: allocCancel,
	}
	chromedp.ListenBrowser(browserCtx, func(event any) {
		created, ok := event.(*target.EventTargetCreated)
		if !ok || created.TargetInfo == nil || created.TargetInfo.OpenerID == "" {
			return
		}
		go func() {
			_ = chromedp.Run(
				browserCtx,
				target.CloseTarget(created.TargetInfo.TargetID),
			)
		}()
	})
	chromedp.ListenTarget(browserCtx, service.requestGuard(browserCtx, found))
	if err := chromedp.Run(browserCtx,
		cdpbrowser.SetDownloadBehavior(
			cdpbrowser.SetDownloadBehaviorBehaviorDeny,
		),
		fetch.Enable().WithPatterns([]*fetch.RequestPattern{{
			URLPattern: "*",
		}}),
	); err != nil {
		cancel()
		allocCancel()
		return nil, fmt.Errorf("browser: start Chromium: %w", err)
	}
	service.sessions[key] = found
	return found, nil
}

func (service *Service) requestGuard(
	browserCtx context.Context,
	current *session,
) func(any) {
	return func(event any) {
		paused, ok := event.(*fetch.EventRequestPaused)
		if !ok || paused.Request == nil {
			return
		}
		go func() {
			target := paused.Request.URL
			parsed, err := url.Parse(target)
			current.guardMu.RLock()
			allowedOrigin := current.allowedOrigin
			current.guardMu.RUnlock()
			if err == nil && (parsed.Scheme == "data" || parsed.Scheme == "blob" ||
				parsed.Scheme == "about") {
				err = nil
			} else if err == nil {
				err = service.validateRequestURL(browserCtx, target, allowedOrigin)
			}
			if err != nil {
				_ = chromedp.Run(browserCtx,
					fetch.FailRequest(paused.RequestID, network.ErrorReasonBlockedByClient),
				)
				return
			}
			_ = chromedp.Run(browserCtx, fetch.ContinueRequest(paused.RequestID))
		}()
	}
}

func (service *Service) validateRequestURL(
	ctx context.Context,
	raw string,
	allowedOrigin string,
) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("browser: request URL is invalid")
	}
	if allowedOrigin != "" &&
		parsed.Scheme+"://"+parsed.Host != allowedOrigin {
		return errors.New("browser: preview request escaped its owned origin")
	}
	return service.validateURL(ctx, raw)
}

func (service *Service) validateURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil {
		return errors.New(
			"browser: destination must be an HTTP or HTTPS URL without embedded credentials",
		)
	}
	if service.allowPrivate {
		return nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return errors.New("browser: destination did not resolve")
	}
	for _, address := range addresses {
		ip := address.IP
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("browser: private or local destinations are blocked")
		}
	}
	return nil
}

// CloseScope cancels and removes only the authenticated actor/session browser.
func (service *Service) CloseScope(ctx context.Context) error {
	key, err := scopeKey(ctx)
	if err != nil {
		return err
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return errors.New("browser: service is closed")
	}
	current := service.sessions[key]
	delete(service.sessions, key)
	root := filepath.Join(service.profileRoot, key)
	service.mu.Unlock()
	if current != nil {
		current.cancel()
		current.allocCancel()
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("browser: remove scoped volatile profile: %w", err)
	}
	return nil
}

// Close stops all browser contexts and removes volatile profiles.
func (service *Service) Close() error {
	if service == nil {
		return nil
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return nil
	}
	service.closed = true
	for _, current := range service.sessions {
		current.cancel()
		current.allocCancel()
	}
	service.sessions = nil
	root := service.profileRoot
	service.mu.Unlock()
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("browser: remove volatile profiles: %w", err)
	}
	return nil
}

type elementMeta struct {
	Tag           string `json:"tag"`
	Type          string `json:"type"`
	Text          string `json:"text"`
	Sensitive     bool   `json:"sensitive"`
	Consequential bool   `json:"consequential"`
	NewContext    bool   `json:"new_context"`
	Download      bool   `json:"download"`
}

func inspectElement(ctx context.Context, selector string) (elementMeta, error) {
	var meta elementMeta
	script := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) return null;
		const tag = (el.tagName || "").toLowerCase();
		const type = (el.getAttribute("type") || "").toLowerCase();
		const autocomplete = (el.getAttribute("autocomplete") || "").toLowerCase();
		const text = ((el.innerText || el.value || el.getAttribute("aria-label") || "") + "").trim();
		const sensitive = type === "password" || type === "file" ||
			autocomplete.includes("password") || autocomplete === "one-time-code" ||
			autocomplete.startsWith("cc-") || /otp|verification|security.?code|passcode/i.test(
				(el.name || "") + " " + (el.id || "") + " " + (el.placeholder || "")
			);
		const consequential = type === "submit" || tag === "form" ||
			/\b(buy|purchase|pay|place order|confirm order|delete|remove account|publish|post|send|sign up|signup|create account|register|accept terms|agree and continue|authorize|transfer|withdraw|book now)\b/i.test(text);
		const newContext = (el.getAttribute("target") || "").toLowerCase() === "_blank";
		const download = el.hasAttribute("download");
		return {tag, type, text, sensitive, consequential, new_context: newContext, download};
	})()`, selector)
	var found *elementMeta
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &found)); err != nil {
		return meta, err
	}
	if found == nil {
		return meta, errors.New("browser: element reference is stale; observe the page again")
	}
	return *found, nil
}

func selectorForRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !refPattern.MatchString(ref) {
		return "", errors.New("browser: use an element ref from the latest observation")
	}
	return `[data-ion-ref="` + ref + `"]`, nil
}

func scopeKey(ctx context.Context) (string, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok {
		return "", errors.New("browser: authenticated actor scope is required")
	}
	parts := []string{scope.ActorID.String()}
	if scope.SessionID != nil {
		parts = append(parts, scope.SessionID.String())
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join(parts, ":"))).String(), nil
}

func ControlTarget(
	actorID uuid.UUID,
	sessionID *uuid.UUID,
) controllease.Target {
	resourceID := actorID.String()
	if sessionID != nil {
		resourceID = sessionID.String()
	}
	return controllease.Target{
		ActorID: actorID, SessionID: cloneUUID(sessionID),
		Kind: controllease.ResourceBrowser, ResourceID: resourceID,
	}
}

func ControlTargetFromContext(ctx context.Context) (controllease.Target, error) {
	scope, ok := controlplane.ApprovalScopeFromContext(ctx)
	if !ok {
		return controllease.Target{}, errors.New(
			"browser: authenticated actor scope is required",
		)
	}
	return ControlTarget(scope.ActorID, scope.SessionID), nil
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func findExecutable(configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		info, err := os.Stat(configured)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("browser: configured Chromium executable is unavailable")
		}
		return configured, nil
	}
	for _, name := range []string{
		"chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
	} {
		if found, err := exec.LookPath(name); err == nil {
			return found, nil
		}
	}
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(
		home, ".cache", "ms-playwright", "chromium-*", "chrome-linux64", "chrome",
	))
	if len(matches) > 0 {
		return matches[len(matches)-1], nil
	}
	return "", errors.New(
		"browser: install Chromium or set ION_BROWSER_EXECUTABLE",
	)
}

func translateContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func sanitizeDisplayedURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func redactSnapshot(snapshot *Snapshot, secrets []string) {
	if snapshot == nil {
		return
	}
	for _, secret := range secrets {
		if len(secret) < 4 {
			continue
		}
		snapshot.Text = strings.ReplaceAll(snapshot.Text, secret, "[REDACTED]")
		snapshot.Title = strings.ReplaceAll(snapshot.Title, secret, "[REDACTED]")
		for index := range snapshot.Elements {
			snapshot.Elements[index].Text = strings.ReplaceAll(
				snapshot.Elements[index].Text, secret, "[REDACTED]",
			)
			snapshot.Elements[index].Placeholder = strings.ReplaceAll(
				snapshot.Elements[index].Placeholder, secret, "[REDACTED]",
			)
		}
	}
}

func scopedContext(
	caller context.Context,
	browserCtx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(browserCtx, timeout)
	stop := context.AfterFunc(caller, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func runClick(ctx context.Context, selector string) error {
	loaded := make(chan struct{}, 1)
	chromedp.ListenTarget(ctx, func(event any) {
		if _, ok := event.(*page.EventLoadEventFired); ok {
			select {
			case loaded <- struct{}{}:
			default:
			}
		}
	})
	if err := chromedp.Run(ctx, chromedp.Click(selector, chromedp.ByQuery)); err != nil {
		return err
	}
	select {
	case <-loaded:
	case <-time.After(750 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	return chromedp.Run(ctx, chromedp.WaitReady("body", chromedp.ByQuery))
}

const snapshotScript = `(() => {
	const refStart = __ION_REF_START__;
	const nodes = Array.from(document.querySelectorAll(
		'a,button,input,textarea,select,[role="button"],[contenteditable="true"],' +
		'main,section,article,nav,header,footer,h1,h2,h3,h4,p,img'
	)).filter((el) => {
		const style = window.getComputedStyle(el);
		const rect = el.getBoundingClientRect();
		return style.visibility !== "hidden" && style.display !== "none" &&
			rect.width > 0 && rect.height > 0;
	}).slice(0, 200);
	const elements = nodes.map((el, index) => {
		const existingRef = el.getAttribute("data-ion-ref") || "";
		const ref = /^p[0-9]{1,12}$/.test(existingRef)
			? existingRef
			: "p" + (refStart + index + 1);
		if (ref !== existingRef) el.setAttribute("data-ion-ref", ref);
		const tag = (el.tagName || "").toLowerCase();
		const type = (el.getAttribute("type") || "").toLowerCase();
		const autocomplete = (el.getAttribute("autocomplete") || "").toLowerCase();
		const sensitive = type === "password" || type === "file" ||
			autocomplete.includes("password") || autocomplete === "one-time-code" ||
			autocomplete.startsWith("cc-") || /otp|verification|security.?code|passcode/i.test(
				(el.name || "") + " " + (el.id || "") + " " + (el.placeholder || "")
			);
		let text = (el.innerText || el.getAttribute("aria-label") ||
			el.getAttribute("title") || "").trim();
		if (!text && !sensitive) text = (el.value || "").trim();
		if (text.length > 240) text = text.slice(0, 240);
		return {
			ref, tag, type, text,
			name: (el.getAttribute("name") || "").slice(0, 120),
			placeholder: (el.getAttribute("placeholder") || "").slice(0, 240),
			disabled: !!el.disabled
		};
	});
	const bodyText = ((document.body && document.body.innerText) || "")
		.replace(/\s+/g, " ").trim();
	return {
		url: location.href,
		title: document.title || "",
		text: bodyText,
		elements,
		truncated: false
	};
})()`
