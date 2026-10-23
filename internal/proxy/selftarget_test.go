package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// ownListener stands in for the control plane: any request reaching it is a
// failure, since a proxied loopback request is trusted there as the local user.
func ownListener(t *testing.T) (port int, hits *int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	hits = new(int32)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(hits, 1)
			fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\nsecret")
			c.Close()
		}
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ = strconv.Atoi(p)
	return port, hits
}

func startSelfTargetProxy(t *testing.T, ownPort int) (*Server, string) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New(s, capture.New(s), nil, nil, nil)
	srv.SetSelfPorts([]int{ownPort})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go srv.Serve(ln)
	return srv, ln.Addr().String()
}

func TestProxyRefusesForwardToOwnListener(t *testing.T) {
	ownPort, hits := ownListener(t)
	_, proxyAddr := startSelfTargetProxy(t, ownPort)
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}

	for _, host := range []string{"127.0.0.1", "localhost", "127.0.0.2"} {
		resp, err := client.Get("http://" + net.JoinHostPort(host, strconv.Itoa(ownPort)) + "/api/keys")
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || string(body) == "secret" {
			t.Fatalf("%s: proxy forwarded to its own listener: %d %q", host, resp.StatusCode, body)
		}
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("own listener was reached %d times", n)
	}
}

func TestProxyRefusesUpgradeToOwnListener(t *testing.T) {
	ownPort, hits := ownListener(t)
	_, proxyAddr := startSelfTargetProxy(t, ownPort)
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(ownPort))
	fmt.Fprintf(c, "GET http://%s/ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", target, target)
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err == nil {
		resp.Body.Close()
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("upgrade reached the own listener %d times", n)
	}
}

func TestSelfDialGuardSeesResolvedAddress(t *testing.T) {
	srv := New(nil, nil, nil, nil, nil)
	srv.SetSelfPorts([]int{9966})
	for _, addr := range []string{"127.0.0.1:9966", "[::1]:9966", "0.0.0.0:9966"} {
		if err := srv.refuseSelfDial("tcp", addr, nil); err == nil {
			t.Errorf("refuseSelfDial(%s) = nil, want refusal", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:9967", "192.0.2.10:9966"} {
		if err := srv.refuseSelfDial("tcp", addr, nil); err != nil {
			t.Errorf("refuseSelfDial(%s) = %v, want nil", addr, err)
		}
	}
}

func TestUpgradeStripsProxyCredentials(t *testing.T) {
	got := make(chan http.Header, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		req, err := http.ReadRequest(bufio.NewReader(c))
		if err != nil {
			return
		}
		got <- req.Header
		fmt.Fprint(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	}()
	_, proxyAddr := startSelfTargetProxy(t, 1)
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	target := ln.Addr().String()
	fmt.Fprintf(c, "GET http://%s/ws HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic dXNlcjpwYXNz\r\nProxy-Connection: keep-alive\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", target, target)
	select {
	case h := <-got:
		if h.Get("Proxy-Authorization") != "" || h.Get("Proxy-Connection") != "" {
			t.Fatalf("origin received proxy headers: %v", h)
		}
		if h.Get("Upgrade") != "websocket" {
			t.Fatalf("Upgrade header lost: %v", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("origin never received the upgrade")
	}
}
