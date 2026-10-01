package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

// Proxy listeners never require authentication. Binding a proxy to a
// non-loopback interface is an explicit operator decision (still guarded by
// INTERSEPTOR_ALLOW_EXTERNAL_BIND=0), so demanding a full-scope API key as a
// proxy password only broke clients reaching the proxy through a LAN or
// tailnet address — including same-machine clients. Anyone who can reach the
// listener can use the proxy; remote control-plane access still requires an
// API key.

func TestProxyListenerServesUnauthenticatedOnNonLoopbackBind(t *testing.T) {
	// Given
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	t.Cleanup(func() { _ = listener.Close() })
	manager := &proxyManager{
		handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		listenFn: func(want string) (net.Listener, error) {
			if want != addr {
				return nil, errors.New("unexpected proxy test address")
			}
			return listener, nil
		},
	}
	if err := manager.Start(addr); err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())

	// When
	response, err := http.Get("http://127.0.0.1" + addr[strings.LastIndexByte(addr, ':'):])
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	// Then
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unauthenticated status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

func TestProxyManager_Rebind_servesWildcardAndLoopbackBinds(t *testing.T) {
	// Given
	wildcardListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	loopbackListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		wildcardListener.Close()
		t.Fatal(err)
	}
	wildcardAddr := wildcardListener.Addr().String()
	loopbackAddr := loopbackListener.Addr().String()
	listeners := map[string]net.Listener{
		wildcardAddr: wildcardListener,
		loopbackAddr: loopbackListener,
	}
	t.Cleanup(func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	})
	manager := &proxyManager{
		handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		listenFn: func(addr string) (net.Listener, error) {
			listener, ok := listeners[addr]
			if !ok {
				return nil, errors.New("unexpected proxy test address")
			}
			delete(listeners, addr)
			return listener, nil
		},
	}
	if err := manager.Start(wildcardAddr); err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())

	// When
	wildcardResponse, err := http.Get("http://127.0.0.1" + wildcardAddr[strings.LastIndexByte(wildcardAddr, ':'):])
	if err != nil {
		t.Fatal(err)
	}
	wildcardResponse.Body.Close()
	if err := manager.Rebind(loopbackAddr); err != nil {
		t.Fatal(err)
	}
	loopbackResponse, err := http.Get("http://" + loopbackAddr)
	if err != nil {
		t.Fatal(err)
	}
	loopbackResponse.Body.Close()

	// Then
	if wildcardResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("wildcard status = %d, want %d", wildcardResponse.StatusCode, http.StatusNoContent)
	}
	if loopbackResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("loopback status = %d, want %d", loopbackResponse.StatusCode, http.StatusNoContent)
	}
}
