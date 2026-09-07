package discovery

// White-box tests for the discovery package.
// Uses package discovery (not discovery_test) to access unexported helpers.

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func newTestResolver() *Resolver {
	return NewResolver(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func TestGetDomainListSingleSegment(t *testing.T) {
	// "foo.com" → one entry ("foo.com"), then loop ends at len-1=1
	list := getDomainList("foo.com")
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1; list = %v", len(list), list)
	}
	if list[0] != "foo.com" {
		t.Errorf("list[0] = %q, want %q", list[0], "foo.com")
	}
}

func TestGetDomainListMultipleSegments(t *testing.T) {
	// "a.b.c.com" → ["a.b.c.com", "b.c.com", "c.com"] (3 entries, within limit)
	list := getDomainList("a.b.c.com")
	want := []string{"a.b.c.com", "b.c.com", "c.com"}
	if len(list) != len(want) {
		t.Fatalf("len = %d, want %d; list = %v", len(list), len(want), list)
	}
	for i, w := range want {
		if list[i] != w {
			t.Errorf("list[%d] = %q, want %q", i, list[i], w)
		}
	}
}

func TestGetDomainListDepthCapped(t *testing.T) {
	// Deep domain: more than 3 sub-entries should be capped to 3
	list := getDomainList("a.b.c.d.e.com")
	if len(list) != 3 {
		t.Errorf("len = %d, want 3 (capped); list = %v", len(list), list)
	}
}

func TestGetDomainListTwoSegments(t *testing.T) {
	// "sub.example.com" → ["sub.example.com", "example.com"]
	list := getDomainList("sub.example.com")
	want := []string{"sub.example.com", "example.com"}
	if len(list) != len(want) {
		t.Fatalf("len = %d, want %d; list = %v", len(list), len(want), list)
	}
	for i, w := range want {
		if list[i] != w {
			t.Errorf("list[%d] = %q, want %q", i, list[i], w)
		}
	}
}

// Resolver.Resolve — paths that do not require network I/O

func TestResolveEmptyAddress(t *testing.T) {
	r := newTestResolver()
	_, err := r.Resolve(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty address")
	}
}

func TestResolveIPv4Direct(t *testing.T) {
	r := newTestResolver()
	addrs, err := r.Resolve(context.Background(), "192.168.1.1:9987")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected 1 result, got %d", len(addrs))
	}
	if addrs[0].Addr != "192.168.1.1:9987" {
		t.Errorf("Addr = %q, want %q", addrs[0].Addr, "192.168.1.1:9987")
	}
	if addrs[0].Source != "Direct" {
		t.Errorf("Source = %q, want Direct", addrs[0].Source)
	}
}

func TestResolveIPv4NoPort(t *testing.T) {
	// No port → default port 9987 is applied
	r := newTestResolver()
	addrs, err := r.Resolve(context.Background(), "10.0.0.1")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected 1 result, got %d", len(addrs))
	}
	if addrs[0].Addr != "10.0.0.1:9987" {
		t.Errorf("Addr = %q, want 10.0.0.1:9987", addrs[0].Addr)
	}
}

func TestResolveIPv6Direct(t *testing.T) {
	r := newTestResolver()
	addrs, err := r.Resolve(context.Background(), "[::1]:9987")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected 1 result, got %d", len(addrs))
	}
	if addrs[0].Source != "Direct" {
		t.Errorf("Source = %q, want Direct", addrs[0].Source)
	}
}

func TestResolveIPNotCached(t *testing.T) {
	// IP addresses bypass cache entirely: Expiry is zero-value
	r := newTestResolver()
	addrs, err := r.Resolve(context.Background(), "127.0.0.1:9987")
	if err != nil {
		t.Fatal(err)
	}
	if !addrs[0].Expiry.IsZero() {
		t.Error("IP resolution should not set a cache expiry")
	}
}
