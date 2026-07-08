package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// A host on the bypass list must be tunneled raw: the client completes TLS with
// the REAL origin certificate (not a MITM leaf), so pinning would succeed. We
// prove this by trusting only the origin's cert — if the proxy had MITM'd, the
// handshake would fail.
func TestTLSBypassPassthrough(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ORIGIN-OK")
	}))
	defer origin.Close()
	ou, _ := url.Parse(origin.URL)
	host, port := splitHostPort(ou.Host, 443)

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
	srv.SetTLSBypassHosts([]string{host}) // 127.0.0.1

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
	raw.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(raw, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port, host, port)
	cr, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"})
	if err != nil || cr.StatusCode != 200 {
		t.Fatalf("CONNECT: %v / %v", err, cr)
	}

	// Trust ONLY the origin's real certificate. A MITM leaf would fail here.
	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	tc := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: pool})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("passthrough handshake (should see real origin cert): %v", err)
	}
	fmt.Fprintf(tc, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	resp, err := http.ReadResponse(bufio.NewReader(tc), &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read origin response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ORIGIN-OK" {
		t.Fatalf("body = %q, want ORIGIN-OK", body)
	}

	// The passthrough should be recorded once as an informational bypass flow.
	// It is intentionally status-less (an opaque tunnel), so poll for the flag
	// directly rather than waitFlows (which expects a terminal status/error).
	deadline := time.Now().Add(2 * time.Second)
	for {
		flows, _ := st.QueryFlows(10)
		if len(flows) == 1 && flows[0].Flags&store.FlagTLSBypassed != 0 &&
			flows[0].Host == host && flows[0].Method == "CONNECT" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected one FlagTLSBypassed CONNECT flow, got %+v", flows)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// With auto-bypass on, a failed MITM handshake (pinning) must add the host to
// the bypass list and fire OnBypassAdded with the updated list.
func TestAutoBypassOnPinFailure(t *testing.T) {
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
	srv.SetAutoBypassOnPinFailure(true)
	added := make(chan []string, 1)
	srv.OnBypassAdded = func(list []string) { added <- list }

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
	target := "pinned.example.com:443"
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	cr, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"})
	if err != nil || cr.StatusCode != 200 {
		t.Fatalf("CONNECT: %v / %v", err, cr)
	}
	// Client rejects the MITM leaf (default RootCAs don't trust our CA).
	tc := tls.Client(raw, &tls.Config{ServerName: "pinned.example.com"})
	_ = tc.Handshake() // expected to fail

	select {
	case list := <-added:
		if !contains(list, "pinned.example.com") {
			t.Fatalf("OnBypassAdded list %v missing host", list)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("OnBypassAdded was not fired after pinning failure")
	}
	if !srv.shouldBypassTLS("pinned.example.com") {
		t.Fatal("host was not added to the runtime bypass list")
	}
}

func TestShouldBypassTLSPatterns(t *testing.T) {
	srv := New(nil, nil, nil, nil, nil)
	srv.SetTLSBypassHosts([]string{" *.Pinned.COM ", "exact.test", ""})
	cases := map[string]bool{
		"pinned.com":        true,
		"api.pinned.com":    true,
		"exact.test":        true,
		"notexact.test":     false,
		"other.com":         false,
	}
	for host, want := range cases {
		if got := srv.shouldBypassTLS(host); got != want {
			t.Fatalf("shouldBypassTLS(%q) = %v, want %v", host, got, want)
		}
	}
	// Normalization: dedupe + trim + lowercase.
	if got := srv.TLSBypassHosts(); len(got) != 2 {
		t.Fatalf("expected 2 normalized patterns, got %v", got)
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
