package control

import (
	"testing"
)

func TestNormalizeProxyAddrsDedupes(t *testing.T) {
	got := normalizeProxyAddrs([]string{"127.0.0.1:8080", "127.0.0.1:8080", " 0.0.0.0:8080 "})
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 unique addrs", got)
	}
}

func TestValidateProxyAddrsRejectsExternalWhenLocked(t *testing.T) {
	t.Setenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND", "0")
	if err := validateProxyAddrs([]string{"0.0.0.0:8080"}); err == nil {
		t.Fatal("expected external bind rejection")
	}
	if err := validateProxyAddrs([]string{"127.0.0.1:8080", "192.168.1.5:8080"}); err == nil {
		t.Fatal("expected LAN bind rejection")
	}
	if err := validateProxyAddrs([]string{"127.0.0.1:8080", "127.0.0.1:8081"}); err != nil {
		t.Fatalf("loopback multi-listen should be allowed: %v", err)
	}
}

func TestIsExternalProxyBind(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", "[::]:8080", "192.168.1.5:8080", ":8080"} {
		if !isExternalProxyBind(addr) {
			t.Errorf("%q should be external", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		if isExternalProxyBind(addr) {
			t.Errorf("%q should not be external", addr)
		}
	}
}

func TestDisplayProxyAddrs(t *testing.T) {
	if got := displayProxyAddrs([]string{"127.0.0.1:8080"}); got != "127.0.0.1:8080" {
		t.Fatalf("single: %q", got)
	}
	if got := displayProxyAddrs([]string{"127.0.0.1:8080", "0.0.0.0:8080"}); got != "127.0.0.1:8080, 0.0.0.0:8080" {
		t.Fatalf("multi: %q", got)
	}
}
