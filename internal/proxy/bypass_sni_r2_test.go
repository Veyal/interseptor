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

// sniBypassRig is a proxy in front of one real TLS origin whose certificate is
// valid for "example.com" (httptest's built-in leaf). Clients reach it the way
// a self-resolving app does: "CONNECT <IP>:port" with the hostname only in SNI.
type sniBypassRig struct {
	srv    *Server
	st     *store.Store
	addr   string
	ip     string
	port   int
	origin *httptest.Server
}

func newSNIBypassRig(t *testing.T) *sniBypassRig {
	t.Helper()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ORIGIN-OK")
	}))
	t.Cleanup(origin.Close)
	ou, _ := url.Parse(origin.URL)
	ip, port := splitHostPort(ou.Host, 443)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	srv := New(st, capture.New(st), ca, nil, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go srv.Serve(ln)
	return &sniBypassRig{srv: srv, st: st, addr: ln.Addr().String(), ip: ip, port: port, origin: origin}
}

// connectByIP opens "CONNECT <origin IP>" and starts a TLS handshake with the
// given SNI, trusting only the real origin's certificate: the handshake
// succeeds only when the proxy passes the tunnel through untouched.
func (r *sniBypassRig) connectByIP(t *testing.T, sni string) (*tls.Conn, error) {
	t.Helper()
	raw, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	raw.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(raw, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", r.ip, r.port, r.ip, r.port)
	cr, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"})
	if err != nil || cr.StatusCode != 200 {
		t.Fatalf("CONNECT: %v / %v", err, cr)
	}
	pool := x509.NewCertPool()
	pool.AddCert(r.origin.Certificate())
	tc := tls.Client(raw, &tls.Config{ServerName: sni, RootCAs: pool})
	return tc, tc.Handshake()
}

func getOverTunnel(t *testing.T, tc *tls.Conn, host string) string {
	t.Helper()
	fmt.Fprintf(tc, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	resp, err := http.ReadResponse(bufio.NewReader(tc), &http.Request{Method: "GET"})
	if err != nil {
		t.Fatalf("read origin response: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// A passthrough rule names a hostname. A client that resolves DNS itself sends
// "CONNECT <IP>" and carries that hostname only in the ClientHello SNI, so the
// rule must be matched against the SNI too or it never applies to that client.
func TestTLSBypassMatchesSNIForIPConnect(t *testing.T) {
	rig := newSNIBypassRig(t)
	rig.srv.SetTLSBypassHosts([]string{"example.com"})

	tc, err := rig.connectByIP(t, "example.com")
	if err != nil {
		t.Fatalf("CONNECT <IP> with a bypassed SNI was still intercepted: %v", err)
	}
	if got := getOverTunnel(t, tc, "example.com"); got != "ORIGIN-OK" {
		t.Fatalf("body = %q, want ORIGIN-OK", got)
	}

	// The passthrough is recorded under the name the operator bypassed.
	deadline := time.Now().Add(2 * time.Second)
	for {
		flows, _ := rig.st.QueryFlows(10)
		if len(flows) == 1 && flows[0].Flags&store.FlagTLSBypassed != 0 && flows[0].Host == "example.com" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want one FlagTLSBypassed flow for example.com, got %+v", flows)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An SNI that is not on the list is still intercepted on an IP CONNECT, and the
// peeked ClientHello is replayed into the MITM handshake intact.
func TestTLSBypassSNIPeekKeepsInterceptingOtherHosts(t *testing.T) {
	rig := newSNIBypassRig(t)
	rig.srv.SetTLSBypassHosts([]string{"other.example.com"})

	if _, err := rig.connectByIP(t, "example.com"); err == nil {
		t.Fatal("origin certificate seen: a host that is not bypassed was passed through")
	}

	// With the MITM CA trusted the same tunnel works end to end (the bytes the
	// peek consumed were replayed to the TLS server).
	raw, err := net.Dial("tcp", rig.addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(raw, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", rig.ip, rig.port, rig.ip, rig.port)
	if cr, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: "CONNECT"}); err != nil || cr.StatusCode != 200 {
		t.Fatalf("CONNECT: %v / %v", err, cr)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(rig.srv.ca.CertPEM())
	tc := tls.Client(raw, &tls.Config{ServerName: "example.com", RootCAs: pool, NextProtos: []string{"http/1.1"}})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("MITM handshake after the SNI peek: %v", err)
	}
	if got := getOverTunnel(t, tc, "example.com"); got != "ORIGIN-OK" {
		t.Fatalf("intercepted body = %q, want ORIGIN-OK", got)
	}
}

// After a pinning failure on "CONNECT <IP>", auto-bypass adds the SNI hostname.
// The app's retry (again by IP) must then be passed through; before the fix the
// bypass check only saw the IP, so the retry was intercepted and failed forever.
func TestAutoBypassTakesEffectForIPConnect(t *testing.T) {
	rig := newSNIBypassRig(t)
	rig.srv.SetAutoBypassOnPinFailure(true)
	added := make(chan []string, 4)
	rig.srv.OnBypassAdded = func(list []string) { added <- list }

	if _, err := rig.connectByIP(t, "example.com"); err == nil {
		t.Fatal("first connection was not intercepted")
	}
	select {
	case list := <-added:
		if !contains(list, "example.com") {
			t.Fatalf("auto-bypass list %v, want example.com", list)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("pinning failure did not auto-bypass the SNI host")
	}

	tc, err := rig.connectByIP(t, "example.com")
	if err != nil {
		t.Fatalf("retry after auto-bypass is still intercepted: %v", err)
	}
	if got := getOverTunnel(t, tc, "example.com"); got != "ORIGIN-OK" {
		t.Fatalf("body = %q, want ORIGIN-OK", got)
	}
}

// A client that opens "CONNECT <IP>" and never sends a ClientHello must still
// be reaped quietly while the proxy is waiting to peek the SNI.
func TestSNIPeekReapsSilentIPConnect(t *testing.T) {
	rig := newSNIBypassRig(t)
	rig.srv.clientHandshake = 150 * time.Millisecond
	rig.srv.SetAutoBypassOnPinFailure(true)
	rig.srv.SetTLSBypassHosts([]string{"other.example.com"})

	c, err := net.Dial("tcp", rig.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", rig.ip, rig.port, rig.ip, rig.port)
	br := bufio.NewReader(c)
	if resp, err := http.ReadResponse(br, nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: resp=%v err=%v", resp, err)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.Copy(io.Discard, br); err != nil {
		t.Fatalf("proxy did not close the silent tunnel: %v", err)
	}
	if got := rig.srv.TLSBypassHosts(); len(got) != 1 {
		t.Fatalf("silent client changed the bypass list: %v", got)
	}
	if flows, _ := rig.st.QueryFlows(10); len(flows) != 0 {
		t.Fatalf("silent client recorded %d flows, want 0", len(flows))
	}
}
