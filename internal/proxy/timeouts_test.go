package proxy

import (
	"bufio"
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

// silentListener accepts connections and never writes to them: a blackholed
// origin, SOCKS server, or TLS peer.
func silentListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	conns := make(chan net.Conn, 16)
	t.Cleanup(func() {
		ln.Close()
		close(conns)
		for c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case conns <- c:
			default:
				c.Close()
			}
		}
	}()
	return ln.Addr().String()
}

// within runs fn and fails the test if it has not returned within d.
func within(t *testing.T, d time.Duration, what string, fn func() error) error {
	t.Helper()
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		t.Fatalf("%s did not return within %v: connection setup is unbounded", what, d)
	}
	return nil
}

func TestUpstreamTLSHandshakeIsBounded(t *testing.T) {
	addr := silentListener(t)
	srv := New(nil, nil, nil, nil, nil)
	srv.timeouts.upstreamTLS = 200 * time.Millisecond
	req, _ := http.NewRequest(http.MethodGet, "https://"+addr+"/", nil)
	err := within(t, 3*time.Second, "HTTPS round trip to a silent origin", func() error {
		resp, err := srv.tr.RoundTrip(req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	})
	if err == nil {
		t.Fatal("round trip to a silent TLS origin succeeded, want a handshake timeout")
	}
}

func TestSOCKSUpstreamDialsAreBounded(t *testing.T) {
	socksAddr := silentListener(t)
	srv := New(nil, nil, nil, nil, nil)
	srv.timeouts.dial = 200 * time.Millisecond
	if err := srv.SetUpstreamProxy("socks5h://" + socksAddr); err != nil {
		t.Fatalf("SetUpstreamProxy: %v", err)
	}
	cases := map[string]func() error{
		"transport": func() error {
			req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
			resp, err := srv.tr.RoundTrip(req)
			if err == nil {
				resp.Body.Close()
			}
			return err
		},
		"websocket dial": func() error {
			c, err := srv.dialUpstream("http", "example.com", 80)
			if err == nil {
				c.Close()
			}
			return err
		},
		"tls-bypass dial": func() error {
			c, err := srv.dialRawUpstream("example.com", 443)
			if err == nil {
				c.Close()
			}
			return err
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if err := within(t, 3*time.Second, name+" through a silent SOCKS upstream", fn); err == nil {
				t.Fatal("dial through a silent SOCKS upstream succeeded, want a timeout")
			}
		})
	}
}

// A client that opens a CONNECT tunnel and never sends a ClientHello is reaped
// after the handshake bound, without being recorded as a TLS/pinning failure
// or auto-bypassed.
func TestMITMClientHandshakeIsBounded(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ca, err := tlsca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	srv := New(s, capture.New(s), ca, nil, nil)
	srv.timeouts.clientHandshake = 200 * time.Millisecond
	srv.SetAutoBypassOnPinFailure(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v / %v", err, resp)
	}
	err = within(t, 3*time.Second, "idle MITM handshake", func() error {
		_, err := br.ReadByte()
		return err
	})
	if err != io.EOF {
		t.Fatalf("idle tunnel read = %v, want EOF after the proxy reaps it", err)
	}
	if hosts := srv.TLSBypassHosts(); len(hosts) != 0 {
		t.Fatalf("idle client auto-bypassed hosts %v", hosts)
	}
	time.Sleep(100 * time.Millisecond)
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if flows, _ := s.QueryFlows(10); len(flows) != 0 {
		t.Fatalf("idle client recorded %d flows, want none", len(flows))
	}
}

func TestProxyListenerBoundsRequestHeadAndIdle(t *testing.T) {
	hs := NewHTTPServer(http.NotFoundHandler())
	if hs.ReadHeaderTimeout <= 0 || hs.IdleTimeout <= 0 {
		t.Fatalf("proxy listener ReadHeaderTimeout=%v IdleTimeout=%v, want both bounded", hs.ReadHeaderTimeout, hs.IdleTimeout)
	}
	if hs.ReadTimeout != 0 || hs.WriteTimeout != 0 {
		t.Fatalf("ReadTimeout=%v WriteTimeout=%v must stay unset: they would cut slow bodies and long responses", hs.ReadTimeout, hs.WriteTimeout)
	}
}
