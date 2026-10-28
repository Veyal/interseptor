package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Proxy listeners never require authentication. Binding a proxy to a
// non-loopback interface is an explicit operator decision (still guarded by
// INTERSEPTOR_ALLOW_EXTERNAL_BIND=0), so demanding a full-scope API key as a
// proxy password only broke clients reaching the proxy through a LAN or
// tailnet address — including same-machine clients. Anyone who can reach the
// listener can use the proxy; remote control-plane access still requires an
// API key.

// proxyTestMarkerHeader identifies responses served by this test's handler.
// A 0.0.0.0:port bind does not stop another process binding 127.0.0.1:port on
// macOS, so a request to 127.0.0.1 can reach a foreign listener (observed as a
// 401 from a concurrent test binary); the marker detects that.
const proxyTestMarkerHeader = "X-Interseptor-Proxy-Test"

// errForeignListener means the response did not come from this test's handler.
var errForeignListener = errors.New("response from a foreign listener")

var proxyTestMarkerSeq atomic.Int64

func newProxyTestMarker() string {
	return fmt.Sprintf("proxy-test-%d", proxyTestMarkerSeq.Add(1))
}

func markedNoContentHandler(marker string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(proxyTestMarkerHeader, marker)
		w.WriteHeader(http.StatusNoContent)
	})
}

// getMarked GETs url and returns its status, or errForeignListener when the
// response lacks marker or the connection was refused by someone else.
func getMarked(url, marker string) (int, error) {
	response, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errForeignListener, err)
	}
	response.Body.Close()
	if response.Header.Get(proxyTestMarkerHeader) != marker {
		return response.StatusCode, fmt.Errorf("%w: status %d", errForeignListener, response.StatusCode)
	}
	return response.StatusCode, nil
}

// retryOnForeignListener runs attempt with fresh ports up to three times,
// retrying only when another process answered on the same port.
func retryOnForeignListener(t *testing.T, attempt func(marker string) error) {
	t.Helper()
	var err error
	for i := 0; i < 3; i++ {
		if err = attempt(newProxyTestMarker()); err == nil || !errors.Is(err, errForeignListener) {
			break
		}
		t.Logf("attempt %d: %v; retrying with fresh ports", i+1, err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func loopbackURLForPort(addr string) string {
	return "http://127.0.0.1" + addr[strings.LastIndexByte(addr, ':'):]
}

func TestProxyListenerServesUnauthenticatedOnNonLoopbackBind(t *testing.T) {
	retryOnForeignListener(t, func(marker string) error {
		// Given
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			return err
		}
		addr := listener.Addr().String()
		defer listener.Close()
		manager := &proxyManager{
			handler: markedNoContentHandler(marker),
			listenFn: func(want string) (net.Listener, error) {
				if want != addr {
					return nil, errors.New("unexpected proxy test address")
				}
				return listener, nil
			},
		}
		if err := manager.Start(addr); err != nil {
			return err
		}
		defer manager.Shutdown(context.Background())

		// When
		status, err := getMarked(loopbackURLForPort(addr), marker)
		if err != nil {
			return err
		}

		// Then
		if status != http.StatusNoContent {
			return fmt.Errorf("unauthenticated status = %d, want %d", status, http.StatusNoContent)
		}
		return nil
	})
}

func TestProxyManager_Rebind_servesWildcardAndLoopbackBinds(t *testing.T) {
	retryOnForeignListener(t, func(marker string) error {
		// Given
		wildcardListener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			return err
		}
		loopbackListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			wildcardListener.Close()
			return err
		}
		wildcardAddr := wildcardListener.Addr().String()
		loopbackAddr := loopbackListener.Addr().String()
		listeners := map[string]net.Listener{
			wildcardAddr: wildcardListener,
			loopbackAddr: loopbackListener,
		}
		defer func() {
			for _, listener := range listeners {
				_ = listener.Close()
			}
		}()
		manager := &proxyManager{
			handler: markedNoContentHandler(marker),
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
			return err
		}
		defer manager.Shutdown(context.Background())

		// When
		wildcardStatus, err := getMarked(loopbackURLForPort(wildcardAddr), marker)
		if err != nil {
			return err
		}
		if err := manager.Rebind(loopbackAddr); err != nil {
			return err
		}
		loopbackStatus, err := getMarked("http://"+loopbackAddr, marker)
		if err != nil {
			return err
		}

		// Then
		if wildcardStatus != http.StatusNoContent {
			return fmt.Errorf("wildcard status = %d, want %d", wildcardStatus, http.StatusNoContent)
		}
		if loopbackStatus != http.StatusNoContent {
			return fmt.Errorf("loopback status = %d, want %d", loopbackStatus, http.StatusNoContent)
		}
		return nil
	})
}
