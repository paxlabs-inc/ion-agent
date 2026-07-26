package tools

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

func TestDiscoverRegistryCalls(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"tools/a.go": {Data: []byte(`package tools
func register(registry Registry) {
	registry.Register(ctx, first)
	registry.Register(ctx, second)
}`)},
		"tools/b.go": {Data: []byte(`package tools
const text = "registry.Register(ctx, ignored)"
func helper(other Registry) { other.Register(ctx, ignored) }
`)},
		"tools/c_test.go": {Data: []byte(`package tools
func test(registry Registry) { registry.Register(ctx, ignored) }
`)},
	}
	sites, err := Discover(context.Background(), files, "tools")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sites) != 2 || sites[0].Path != "tools/a.go" ||
		sites[0].Line != 3 || sites[1].Line != 4 {
		t.Fatalf("sites = %+v", sites)
	}
}

func TestDiscoverValidationAndErrors(t *testing.T) {
	t.Parallel()
	if _, err := Discover(context.Background(), nil, "."); err == nil {
		t.Fatal("Discover(nil) succeeded")
	}
	broken := fstest.MapFS{
		"broken.go": {Data: []byte(`package broken
func f(registry R) { registry.Register(
`)},
	}
	if _, err := Discover(context.Background(), broken, ""); err == nil {
		t.Fatal("Discover(broken) succeeded")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Discover(cancelled, fstest.MapFS{"a.go": {Data: []byte("package a")}}, ".")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Discover(cancelled) error = %v", err)
	}
}
