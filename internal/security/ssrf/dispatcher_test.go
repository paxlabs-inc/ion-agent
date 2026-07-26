package ssrf

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type staticResolver map[string][]net.IP

func (resolver staticResolver) LookupIPAddr(
	_ context.Context,
	host string,
) ([]net.IPAddr, error) {
	addresses, exists := resolver[host]
	if !exists {
		return nil, errors.New("not found")
	}
	results := make([]net.IPAddr, 0, len(addresses))
	for _, address := range addresses {
		results = append(results, net.IPAddr{IP: address})
	}
	return results, nil
}

func TestValidateURLBlocksSSRFVariants(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{
		"public.example":  {net.ParseIP("93.184.216.34")},
		"private.example": {net.ParseIP("10.1.2.3")},
		"mixed.example": {
			net.ParseIP("93.184.216.34"),
			net.ParseIP("127.0.0.1"),
		},
		"ipv6.example": {net.ParseIP("fd00::1")},
	}
	dispatcher, err := New(Config{
		AllowedHosts: []string{
			"public.example",
			"private.example",
			"mixed.example",
			"ipv6.example",
		},
		Resolver: resolver,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		raw     string
		allowed bool
	}{
		{raw: "https://public.example/path", allowed: true},
		{raw: "http://public.example/path"},
		{raw: "https://unlisted.example/path"},
		{raw: "https://private.example/path"},
		{raw: "https://mixed.example/path"},
		{raw: "https://ipv6.example/path"},
		{raw: "https://user:pass@public.example/path"},
	}
	for _, test := range tests {
		target, parseErr := url.Parse(test.raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		err := dispatcher.ValidateURL(context.Background(), target)
		if test.allowed && err != nil {
			t.Errorf("ValidateURL(%q) error = %v", test.raw, err)
		}
		if !test.allowed && !errors.Is(err, ErrBlocked) {
			t.Errorf("ValidateURL(%q) error = %v, want ErrBlocked", test.raw, err)
		}
	}
}

func TestWildcardDoesNotApproveApexOrLookalike(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{
		"api.example.com": {net.ParseIP("93.184.216.34")},
		"example.com":     {net.ParseIP("93.184.216.34")},
		"badexample.com":  {net.ParseIP("93.184.216.34")},
	}
	dispatcher, err := New(Config{
		AllowedHosts: []string{"*.example.com"},
		Resolver:     resolver,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for raw, allowed := range map[string]bool{
		"https://api.example.com": true,
		"https://example.com":     false,
		"https://badexample.com":  false,
	} {
		target, _ := url.Parse(raw)
		err := dispatcher.ValidateURL(context.Background(), target)
		if (err == nil) != allowed {
			t.Errorf("ValidateURL(%q) error = %v, allowed %v", raw, err, allowed)
		}
	}
}

func TestLiteralPrivateAddressesAreBlocked(t *testing.T) {
	t.Parallel()
	dispatcher, err := New(Config{AllowedHosts: []string{
		"127.0.0.1",
		"169.254.169.254",
		"100.64.0.1",
		"::1",
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, raw := range []string{
		"https://127.0.0.1",
		"https://169.254.169.254/latest/meta-data",
		"https://100.64.0.1",
		"https://[::1]",
	} {
		target, _ := url.Parse(raw)
		if err := dispatcher.ValidateURL(context.Background(), target); !errors.Is(err, ErrBlocked) {
			t.Errorf("ValidateURL(%q) error = %v", raw, err)
		}
	}
}

func TestReservedAndPrivateAddressFamiliesAreBlocked(t *testing.T) {
	t.Parallel()
	addresses := []string{
		"0.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1",
		"240.0.0.1",
		"64:ff9b::1",
		"2001:db8::1",
		"fc00::1",
		"fe80::1",
		"ff00::1",
	}
	dispatcher, err := New(Config{AllowedHosts: addresses})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		raw := "https://" + address
		if strings.Contains(address, ":") {
			raw = "https://[" + address + "]"
		}
		target, parseErr := url.Parse(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if err := dispatcher.ValidateURL(
			context.Background(),
			target,
		); !errors.Is(err, ErrBlocked) {
			t.Errorf("ValidateURL(%q) error = %v", raw, err)
		}
	}
}

func TestDNSRebindingIsBlockedAtDialTime(t *testing.T) {
	t.Parallel()
	resolver := &sequenceResolver{responses: [][]net.IP{
		{net.ParseIP("8.8.8.8")},
		{net.ParseIP("127.0.0.1")},
	}}
	dialer := &recordingDialer{err: errors.New("must not dial")}
	dispatcher, err := New(Config{
		AllowedHosts: []string{"rebind.example"},
		Resolver:     resolver,
		Dialer:       dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("https://rebind.example/resource")
	if err := dispatcher.ValidateURL(context.Background(), target); err != nil {
		t.Fatalf("initial ValidateURL() error = %v", err)
	}
	if _, err := dispatcher.dialContext(
		context.Background(),
		"tcp",
		"rebind.example:443",
	); !errors.Is(err, ErrBlocked) {
		t.Fatalf("dialContext() error = %v", err)
	}
	if len(dialer.addresses) != 0 {
		t.Fatalf("dialed addresses after rebind = %v", dialer.addresses)
	}
}

func TestDialUsesOnlyResolvedApprovedPublicAddresses(t *testing.T) {
	t.Parallel()
	dialer := &recordingDialer{err: errors.New("connection refused")}
	dispatcher, err := New(Config{
		AllowedHosts: []string{"public.example"},
		Resolver: staticResolver{
			"public.example": {
				net.ParseIP("8.8.8.8"),
				net.ParseIP("1.1.1.1"),
			},
		},
		Dialer: dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.dialContext(
		context.Background(),
		"tcp",
		"public.example:443",
	); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatalf("dialContext() error = %v", err)
	}
	want := []string{"8.8.8.8:443", "1.1.1.1:443"}
	if !equalStrings(dialer.addresses, want) {
		t.Fatalf("dialed addresses = %v, want %v", dialer.addresses, want)
	}
}

func TestDispatcherAPIFailsClosedBeforeNetwork(t *testing.T) {
	t.Parallel()
	dispatcher, err := New(Config{
		AllowedHosts: []string{"public.example"},
		Resolver: staticResolver{
			"public.example": {net.ParseIP("8.8.8.8")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dispatcher.Client() == nil {
		t.Fatal("Client() returned nil")
	}
	if _, err := dispatcher.Do(nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("Do(nil) error = %v", err)
	}
	if _, err := dispatcher.RoundTrip(nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("RoundTrip(nil) error = %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://public.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.RoundTrip(request); !errors.Is(err, ErrBlocked) {
		t.Fatalf("RoundTrip(http) error = %v", err)
	}
	dispatcher.CloseIdleConnections()
}

func TestDispatcherValidationAndConfigurationErrors(t *testing.T) {
	t.Parallel()
	for _, config := range []Config{
		{},
		{AllowedHosts: []string{""}},
		{AllowedHosts: []string{"*.-bad.example"}},
		{AllowedHosts: []string{"bad host.example"}},
		{AllowedHosts: []string{"tést.example"}},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("New(%+v) succeeded", config)
		}
	}
	dispatcher, err := New(Config{
		AllowedHosts: []string{"public.example", "8.8.8.8"},
		Resolver: staticResolver{
			"public.example": {net.ParseIP("8.8.8.8")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"https://public.example:0",
		"https://public.example:70000",
		"https://8.8.8.8:443",
	} {
		target, parseErr := url.Parse(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		err := dispatcher.ValidateURL(context.Background(), target)
		if raw == "https://8.8.8.8:443" {
			if err != nil {
				t.Errorf("ValidateURL(%q) error = %v", raw, err)
			}
		} else if !errors.Is(err, ErrBlocked) {
			t.Errorf("ValidateURL(%q) error = %v", raw, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	target, _ := url.Parse("https://public.example")
	if err := dispatcher.ValidateURL(cancelled, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ValidateURL() error = %v", err)
	}
}

type sequenceResolver struct {
	mu        sync.Mutex
	responses [][]net.IP
	calls     int
}

func (resolver *sequenceResolver) LookupIPAddr(
	_ context.Context,
	_ string,
) ([]net.IPAddr, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if resolver.calls >= len(resolver.responses) {
		return nil, errors.New("no response")
	}
	addresses := resolver.responses[resolver.calls]
	resolver.calls++
	results := make([]net.IPAddr, 0, len(addresses))
	for _, address := range addresses {
		results = append(results, net.IPAddr{IP: address})
	}
	return results, nil
}

type recordingDialer struct {
	mu        sync.Mutex
	addresses []string
	err       error
}

func (dialer *recordingDialer) DialContext(
	_ context.Context,
	_ string,
	address string,
) (net.Conn, error) {
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	dialer.addresses = append(dialer.addresses, address)
	return nil, dialer.err
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
