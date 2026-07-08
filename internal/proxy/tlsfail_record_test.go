package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// TestTLSHandshakeFailureRecorded verifies that a client rejecting the MITM leaf
// produces a FlagTLSFailed flow — the signal for SSL pinning / untrusted CA.
func TestTLSHandshakeFailureRecorded(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	srv := New(st, capture.New(st), ca, nil, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(4 * time.Second))
	target := "api.example.com:443"
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	connResp, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"})
	if err != nil || connResp.StatusCode != 200 {
		t.Fatalf("CONNECT: %v / %v", err, connResp)
	}

	// Client does NOT trust the proxy CA — mimics pinning / untrusted CA.
	tlsClient := tls.Client(raw, &tls.Config{InsecureSkipVerify: false, ServerName: "api.example.com"})
	_ = tlsClient.Handshake() // expected to fail or hang; server records either way

	flows := waitFlows(t, st, 1)
	f := flows[0]
	if f.Flags&store.FlagTLSFailed == 0 {
		t.Fatalf("expected FlagTLSFailed, got flags=%d", f.Flags)
	}
	if f.Method != "CONNECT" || f.Host != "api.example.com" {
		t.Fatalf("unexpected flow: %+v", f)
	}
	if f.Error == "" {
		t.Fatal("expected error message on TLS failure flow")
	}
}
