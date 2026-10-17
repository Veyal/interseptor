package control

import (
	"strings"
	"testing"
)

func TestValidateProxyAddrsRejectsOutOfRangePorts(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:99999", "127.0.0.1:0", "127.0.0.1:http", "127.0.0.1:-1"} {
		err := validateProxyAddrs([]string{addr})
		if err == nil || !strings.Contains(err.Error(), "port must be 1-65535") {
			t.Errorf("%q: err = %v, want port range rejection", addr, err)
		}
	}
	if err := validateProxyAddrs([]string{"127.0.0.1:65535", "127.0.0.1:1"}); err != nil {
		t.Fatalf("valid ports rejected: %v", err)
	}
}

func TestPutSettingsRejectsOutOfRangeControlPort(t *testing.T) {
	h, st, _ := newHub(t)
	rebound := false
	h.ctrlRebind = rebindFunc(func(string) error { rebound = true; return nil })
	rec := putSettingsBody(t, h, `{"controlAddr":"127.0.0.1:70000"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "port must be 1-65535") {
		t.Fatalf("status = %d body = %s, want 400 port range", rec.Code, rec.Body.String())
	}
	if rebound {
		t.Fatal("control listener rebound for an invalid port")
	}
	if _, ok, _ := st.GetSetting("control.addr"); ok {
		t.Fatal("invalid control address persisted")
	}
}

type rebindFunc func(string) error

func (f rebindFunc) Rebind(addr string) error { return f(addr) }
func (f rebindFunc) Addr() string             { return "127.0.0.1:9966" }

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
