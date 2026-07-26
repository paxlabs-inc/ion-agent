// Package ssrf provides the only supported outbound HTTP dispatch path.
package ssrf

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrBlocked is deliberately uniform so callers do not learn network details.
	ErrBlocked = errors.New("ssrf: destination blocked")
)

var prohibitedPrefixes = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// Resolver is the narrow DNS boundary used by the dispatcher.
type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Config controls explicit outbound access.
type Config struct {
	AllowedHosts   []string
	RequestTimeout time.Duration
	Resolver       Resolver
	Dialer         dialer
}

// Dispatcher validates URLs, resolves and checks every address, and dials the
// checked IP directly to close the DNS-rebinding time-of-check/time-of-use gap.
type Dispatcher struct {
	allowed          map[string]struct{}
	wildcardSuffixes []string
	resolver         Resolver
	dialer           dialer
	transport        *http.Transport
	client           *http.Client
}

// New creates a TLS-only dispatcher with no environment proxy inheritance.
func New(config Config) (*Dispatcher, error) {
	if len(config.AllowedHosts) == 0 {
		return nil, fmt.Errorf("ssrf: at least one approved host is required")
	}
	allowed := make(map[string]struct{}, len(config.AllowedHosts))
	var wildcards []string
	for _, raw := range config.AllowedHosts {
		host, wildcard, err := normalizeRule(raw)
		if err != nil {
			return nil, err
		}
		if wildcard {
			wildcards = append(wildcards, host)
		} else {
			allowed[host] = struct{}{}
		}
	}
	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	baseDialer := config.Dialer
	if baseDialer == nil {
		baseDialer = &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}
	}
	timeout := config.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dispatcher := &Dispatcher{
		allowed:          allowed,
		wildcardSuffixes: wildcards,
		resolver:         resolver,
		dialer:           baseDialer,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dispatcher.dialContext
	transport.MaxIdleConnsPerHost = 4
	transport.ResponseHeaderTimeout = timeout
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			transport.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	dispatcher.transport = transport
	dispatcher.client = &http.Client{
		Transport: dispatcher,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return dispatcher.ValidateURL(request.Context(), request.URL)
		},
	}
	return dispatcher, nil
}

// Client exposes a redirect-safe HTTP client backed by this dispatcher.
func (dispatcher *Dispatcher) Client() *http.Client {
	return dispatcher.client
}

// CloseIdleConnections releases pooled outbound connections.
func (dispatcher *Dispatcher) CloseIdleConnections() {
	dispatcher.transport.CloseIdleConnections()
}

// Do validates and sends one request.
func (dispatcher *Dispatcher) Do(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: nil request", ErrBlocked)
	}
	return dispatcher.client.Do(request)
}

// RoundTrip makes Dispatcher usable wherever an http.RoundTripper is required.
func (dispatcher *Dispatcher) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("%w: nil request", ErrBlocked)
	}
	if err := dispatcher.ValidateURL(request.Context(), request.URL); err != nil {
		return nil, err
	}
	return dispatcher.transport.RoundTrip(request)
}

// ValidateURL enforces TLS, the positive allowlist, and address safety.
func (dispatcher *Dispatcher) ValidateURL(ctx context.Context, target *url.URL) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if target == nil || !strings.EqualFold(target.Scheme, "https") {
		return fmt.Errorf("%w: TLS is required", ErrBlocked)
	}
	if target.User != nil {
		return fmt.Errorf("%w: URL credentials are forbidden", ErrBlocked)
	}
	host, err := normalizeHost(target.Hostname())
	if err != nil || !dispatcher.hostAllowed(host) {
		return fmt.Errorf("%w: host is not approved", ErrBlocked)
	}
	if target.Port() != "" {
		port, err := strconv.ParseUint(target.Port(), 10, 16)
		if err != nil || port == 0 {
			return fmt.Errorf("%w: invalid port", ErrBlocked)
		}
	}
	_, err = dispatcher.resolveSafe(ctx, host)
	return err
}

func (dispatcher *Dispatcher) dialContext(
	ctx context.Context,
	network string,
	address string,
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed dial address", ErrBlocked)
	}
	host, err = normalizeHost(host)
	if err != nil || !dispatcher.hostAllowed(host) {
		return nil, fmt.Errorf("%w: dial host is not approved", ErrBlocked)
	}
	addresses, err := dispatcher.resolveSafe(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, address := range addresses {
		connection, dialErr := dispatcher.dialer.DialContext(
			ctx,
			network,
			net.JoinHostPort(address.String(), port),
		)
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = errors.New("no resolved addresses")
	}
	return nil, fmt.Errorf("ssrf: connect approved destination: %w", lastErr)
}

func (dispatcher *Dispatcher) resolveSafe(
	ctx context.Context,
	host string,
) ([]net.IP, error) {
	if literal := net.ParseIP(host); literal != nil {
		literal = canonicalIP(literal)
		if blockedIP(literal) {
			return nil, fmt.Errorf("%w: prohibited address", ErrBlocked)
		}
		return []net.IP{literal}, nil
	}
	resolved, err := dispatcher.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: DNS resolution failed", ErrBlocked)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("%w: DNS resolution failed", ErrBlocked)
	}
	addresses := make([]net.IP, 0, len(resolved))
	seen := make(map[string]struct{}, len(resolved))
	for _, result := range resolved {
		address := canonicalIP(result.IP)
		if blockedIP(address) {
			return nil, fmt.Errorf("%w: DNS returned prohibited address", ErrBlocked)
		}
		key := address.String()
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}

func canonicalIP(address net.IP) net.IP {
	if ipv4 := address.To4(); ipv4 != nil {
		return ipv4
	}
	return address
}

func blockedIP(address net.IP) bool {
	if address == nil {
		return true
	}
	parsed, valid := netip.AddrFromSlice(address)
	if !valid {
		return true
	}
	parsed = parsed.Unmap()
	if !parsed.IsGlobalUnicast() {
		return true
	}
	for _, prefix := range prohibitedPrefixes {
		if prefix.Contains(parsed) {
			return true
		}
	}
	return false
}

func (dispatcher *Dispatcher) hostAllowed(host string) bool {
	if _, exists := dispatcher.allowed[host]; exists {
		return true
	}
	for _, suffix := range dispatcher.wildcardSuffixes {
		if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
			return true
		}
	}
	return false
}

func normalizeRule(raw string) (string, bool, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	wildcard := strings.HasPrefix(raw, "*.")
	if wildcard {
		raw = strings.TrimPrefix(raw, "*")
	}
	host, err := normalizeHost(strings.TrimPrefix(raw, "."))
	if err != nil {
		return "", false, fmt.Errorf("ssrf: invalid allowlist rule %q", raw)
	}
	if wildcard {
		return "." + host, true, nil
	}
	return host, false, nil
}

func normalizeHost(raw string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || strings.ContainsAny(host, "/\\@%") {
		return "", fmt.Errorf("invalid host")
	}
	if address := net.ParseIP(host); address != nil {
		return canonicalIP(address).String(), nil
	}
	if len(host) > 253 {
		return "", fmt.Errorf("host is too long")
	}
	for _, character := range host {
		if character > 127 {
			return "", fmt.Errorf("non-ASCII host")
		}
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 ||
			label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid DNS label")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') &&
				character != '-' {
				return "", fmt.Errorf("invalid DNS label")
			}
		}
	}
	return host, nil
}
