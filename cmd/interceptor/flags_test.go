package main

import (
	"strings"
	"testing"

	"github.com/Veyal/interceptor/internal/store"
)

func TestNormalizeCLIArgsControlPortUnderscore(t *testing.T) {
	got := normalizeCLIArgs([]string{"--control_port=1234"})
	if len(got) != 1 || got[0] != "--control-port=1234" {
		t.Fatalf("got %v", got)
	}
	got = normalizeCLIArgs([]string{"--control_port", "1234"})
	if len(got) != 2 || got[0] != "--control-port" || got[1] != "1234" {
		t.Fatalf("got %v", got)
	}
}

func TestControlAddrFromPort(t *testing.T) {
	addr, err := controlAddrFromPort(1234)
	if err != nil || addr != "127.0.0.1:1234" {
		t.Fatalf("addr=%q err=%v", addr, err)
	}
	if _, err := controlAddrFromPort(0); err == nil {
		t.Fatal("port 0 should fail")
	}
	if _, err := controlAddrFromPort(70000); err == nil {
		t.Fatal("port 70000 should fail")
	}
}

func TestResolveControlAddrPriority(t *testing.T) {
	t.Setenv("INTERCEPTOR_CONTROL_ADDR", "127.0.0.1:9999")
	t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", "")

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.SetSetting("control.addr", "127.0.0.1:8888")

	if got := resolveControlAddr(st, "127.0.0.1:1234"); got != "127.0.0.1:1234" {
		t.Fatalf("CLI override: got %q", got)
	}
	if got := resolveControlAddr(st, ""); got != "127.0.0.1:9999" {
		t.Fatalf("env: got %q", got)
	}
	t.Setenv("INTERCEPTOR_CONTROL_ADDR", "")
	if got := resolveControlAddr(st, ""); got != "127.0.0.1:8888" {
		t.Fatalf("persisted: got %q", got)
	}
}

func TestResolveControlAddrAllowsExternalByDefault(t *testing.T) {
	t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", "")
	if got := resolveControlAddr(nil, "0.0.0.0:9966"); got != "0.0.0.0:9966" {
		t.Fatalf("got %q, want 0.0.0.0:9966", got)
	}
}

func TestResolveControlAddrRejectsExternalWhenLocked(t *testing.T) {
	t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", "0")
	if got := resolveControlAddr(nil, "0.0.0.0:9966"); got != defaultControlAddr {
		t.Fatalf("got %q, want default", got)
	}
}

func TestDefaultControlHost(t *testing.T) {
	if h := defaultControlHost(); !strings.Contains(defaultControlAddr, h) {
		t.Fatalf("host %q not in %q", h, defaultControlAddr)
	}
}
