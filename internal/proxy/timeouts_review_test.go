package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// A client that opens a CONNECT and never sends a ClientHello must be reaped,
// not held forever, and must not be mistaken for a pinning failure.
func TestSilentConnectClientIsReapedWithoutPinningSignal(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, capture.New(st), ca, nil, nil)
	srv.clientHandshake = 150 * time.Millisecond
	srv.SetAutoBypassOnPinFailure(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, "CONNECT silent.example.com:443 HTTP/1.1\r\nHost: silent.example.com:443\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: resp=%v err=%v", resp, err)
	}
	// Send nothing. The proxy must close the tunnel on its own.
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.Copy(io.Discard, br); err != nil {
		t.Fatalf("proxy did not close the silent tunnel: %v", err)
	}
	if srv.shouldBypassTLS("silent.example.com") {
		t.Fatal("a silent client was auto-bypassed as if it were SSL pinning")
	}
	if flows, err := st.QueryFlows(10); err != nil || len(flows) != 0 {
		t.Fatalf("a silent client recorded %d TLS-failure flows (err=%v), want 0", len(flows), err)
	}
}

// A SOCKS upstream that accepts the connection and then never answers must not
// hang the dial: the upgrade and TLS-passthrough paths call it with a context
// that has no deadline.
func TestSilentSOCKSUpstreamTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold it open, never speak SOCKS
		}
	}()
	up, _ := url.Parse("socks5h://" + ln.Addr().String())
	errc := make(chan error, 1)
	go func() {
		c, err := dialViaSOCKSWithin(context.Background(), up, "tcp", "example.com:80", 150*time.Millisecond)
		if c != nil {
			c.Close()
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("dial through a silent SOCKS server succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dial through a silent SOCKS server never returned")
	}
}
