package proxy

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// A peer that completes the TCP handshake and then never speaks TLS — a
// SYN-accepting firewall, a hung load balancer, a half-open middlebox — must not
// park the request forever. The connect is bounded (upstreamConnectTimeout), so
// the TLS handshake after it must be too; without a bound the client waits with
// no response, no error, and no recorded flow.
func TestOriginTLSHandshakeIsBounded(t *testing.T) {
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer c.Close() // accept and say nothing
		}
	}()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	srv := New(st, capture.New(st), nil, nil, nil)
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	defer pl.Close()
	go srv.Serve(pl)

	old := originTLSHandshakeTimeout
	originTLSHandshakeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { originTLSHandshakeTimeout = old })

	c, err := net.Dial("tcp", pl.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "GET https://%s/ HTTP/1.1\r\nHost: %s\r\n\r\n", silent.Addr(), silent.Addr())
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("no response within the deadline (request parked on the TLS handshake): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}
