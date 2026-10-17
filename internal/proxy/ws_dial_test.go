package proxy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

// A hardened client that resolves the host itself sends "CONNECT <IP>:443"
// and the real hostname only in SNI. Plain HTTPS requests already dial that IP
// with the SNI hostname; a WebSocket upgrade on the same tunnel must too,
// instead of re-resolving the SNI name (which may not resolve, or resolve
// elsewhere).
func TestMITMWebSocketUpgradeDialsConnectIPWithSNIHost(t *testing.T) {
	const logicalHost = "ws.example.test" // never resolvable
	upCA, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("upstream CA: %v", err)
	}
	originSNI := make(chan string, 1)
	upLn, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		originSNI <- hello.ServerName
		return upCA.LeafForHost(logicalHost)
	}})
	if err != nil {
		t.Fatalf("upstream listen: %v", err)
	}
	defer upLn.Close()
	go func() {
		c, err := upLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		io.Copy(c, br)
	}()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("proxy CA: %v", err)
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
		t.Fatalf("dial proxy: %v", err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(5 * time.Second))
	target := upLn.Addr().String()
	fmt.Fprintf(raw, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	if resp, err := http.ReadResponse(bufio.NewReader(raw), &http.Request{Method: http.MethodConnect}); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v / %v", err, resp)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())
	tc := tls.Client(raw, &tls.Config{RootCAs: pool, ServerName: logicalHost})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	fmt.Fprintf(tc, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", logicalHost)
	resp, err := http.ReadResponse(bufio.NewReader(tc), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upgrade status = %d (%s), want 101", resp.StatusCode, body)
	}
	select {
	case got := <-originSNI:
		if got != logicalHost {
			t.Fatalf("origin SNI = %q, want %q", got, logicalHost)
		}
	case <-time.After(time.Second):
		t.Fatal("origin saw no TLS handshake")
	}
}
