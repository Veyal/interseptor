//go:build !windows

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func reservedListenerFD(t *testing.T) (int, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback listener: %v", err)
	}
	tcp, ok := ln.(*net.TCPListener)
	if !ok {
		ln.Close()
		t.Fatalf("reserved listener type = %T, want *net.TCPListener", ln)
	}
	file, err := tcp.File()
	addr := ln.Addr().String()
	_ = ln.Close()
	if err != nil {
		t.Fatalf("duplicate reserved listener: %v", err)
	}
	fd, err := syscall.Dup(int(file.Fd()))
	_ = file.Close()
	if err != nil {
		t.Fatalf("detach reserved listener descriptor: %v", err)
	}
	return fd, addr
}

func TestStartRuntimeListenersUsesManagedInheritedSockets(t *testing.T) {
	controlFD, controlAddr := reservedListenerFD(t)
	proxyFD, proxyAddr := reservedListenerFD(t)
	t.Setenv(managedUIAuditEnv, "1")
	t.Setenv(managedUIAuditControlFDEnv, strconv.Itoa(controlFD))
	t.Setenv(managedUIAuditProxyFDEnv, strconv.Itoa(proxyFD))

	cm := &controlManager{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	pm := &proxyManager{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	if err := startRuntimeListeners(pm, cm, controlAddr, []string{proxyAddr}); err != nil {
		t.Fatalf("start inherited listeners: %v", err)
	}
	defer cm.Shutdown(context.Background())
	defer pm.Shutdown(context.Background())

	if got := cm.Addr(); got != controlAddr {
		t.Fatalf("control address = %q, want %q", got, controlAddr)
	}
	if got := pm.Addrs(); len(got) != 1 || got[0] != proxyAddr {
		t.Fatalf("proxy addresses = %v, want [%s]", got, proxyAddr)
	}
	for _, addr := range []string{controlAddr, proxyAddr} {
		competitor, err := net.Listen("tcp", addr)
		if err == nil {
			_ = competitor.Close()
			t.Fatalf("managed listener %s could be rebound by a competing process", addr)
		}
	}
}

func TestManagedInheritedListenerRejectsAddressMismatch(t *testing.T) {
	controlFD, controlAddr := reservedListenerFD(t)
	proxyFD, proxyAddr := reservedListenerFD(t)
	t.Setenv(managedUIAuditEnv, "1")
	t.Setenv(managedUIAuditControlFDEnv, strconv.Itoa(controlFD))
	t.Setenv(managedUIAuditProxyFDEnv, strconv.Itoa(proxyFD))

	listeners, err := loadManagedUIAuditListeners("127.0.0.1:1", []string{proxyAddr})
	_ = syscall.Close(proxyFD)
	if listeners != nil {
		listeners.close()
	}
	if err == nil {
		t.Fatalf("expected inherited control listener %s to reject a mismatched address", controlAddr)
	}
}

func TestManagedInheritedListenersAreIgnoredOutsideManagedAudit(t *testing.T) {
	t.Setenv(managedUIAuditEnv, "")
	t.Setenv(managedUIAuditControlFDEnv, "not-a-descriptor")
	t.Setenv(managedUIAuditProxyFDEnv, "not-a-descriptor")
	listeners, err := loadManagedUIAuditListeners("127.0.0.1:1", []string{"127.0.0.1:2"})
	if err != nil {
		t.Fatalf("normal startup must ignore managed listener descriptors: %v", err)
	}
	if listeners != nil {
		listeners.close()
		t.Fatal("normal startup unexpectedly consumed managed listener descriptors")
	}
}

func TestRuntimeListenerStartupUsesBoundedProxyCleanup(t *testing.T) {
	source, err := os.ReadFile("managed_audit_listeners.go")
	if err != nil {
		t.Fatalf("read managed listener startup: %v", err)
	}
	text := string(source)
	if strings.Contains(text, "pm.Shutdown(context.Background())") {
		t.Fatal("startup failure cleanup must not wait indefinitely for active proxy handlers")
	}
	for _, want := range []string{"context.WithTimeout", "startupListenerShutdownTimeout"} {
		if !strings.Contains(text, want) {
			t.Fatalf("startup failure cleanup must be bounded: missing %s", want)
		}
	}
}
