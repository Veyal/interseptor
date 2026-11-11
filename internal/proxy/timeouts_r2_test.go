package proxy

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// r2Silent accepts connections and never speaks: a blackholed TLS peer or
// SOCKS server.
func r2Silent(t *testing.T) string {
	t.Helper()
	return tcpOrigin(t, func(c net.Conn) { time.Sleep(10 * time.Second) })
}

func r2Within(t *testing.T, what string, fn func() error) error {
	t.Helper()
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return: connection setup is unbounded", what)
	}
	return nil
}

// From 457abea1 (test file deleted by merge 2e1a562e): the TLS handshake with
// an HTTPS chained upstream proxy is bounded like the handshake with an origin.
func TestUpstreamProxyTLSHandshakeIsBounded(t *testing.T) {
	old := originTLSHandshakeTimeout
	originTLSHandshakeTimeout = 150 * time.Millisecond
	t.Cleanup(func() { originTLSHandshakeTimeout = old })
	for name, target := range map[string]string{
		// An HTTPS request takes dialViaUpstream instead, whose whole CONNECT
		// setup (TCP, TLS, CONNECT reply) shares one 30 s connection deadline:
		// see TestDialViaUpstreamTimesOutSilentCONNECTAndClosesConnection.
		"plain request through a silent HTTPS upstream": "http://example.com/",
	} {
		t.Run(name, func(t *testing.T) {
			srv := New(nil, nil, nil, nil, nil)
			if err := srv.SetUpstreamProxy("https://" + r2Silent(t)); err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequest(http.MethodGet, target, nil)
			err := r2Within(t, name, func() error {
				resp, err := srv.tr.RoundTrip(req)
				if err == nil {
					resp.Body.Close()
				}
				return err
			})
			if err == nil {
				t.Fatal("round trip through a silent HTTPS upstream succeeded")
			}
		})
	}
}

// From 457abea1: every caller that dials through a SOCKS upstream is bounded
// (transport, WebSocket/upgrade dial, TLS-passthrough dial).
func TestSOCKSUpstreamDialsAreBoundedOnEveryPath(t *testing.T) {
	old := socksSetupTimeout
	socksSetupTimeout = 150 * time.Millisecond
	t.Cleanup(func() { socksSetupTimeout = old })
	srv := New(nil, nil, nil, nil, nil)
	if err := srv.SetUpstreamProxy("socks5h://" + r2Silent(t)); err != nil {
		t.Fatal(err)
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
		"upgrade dial": func() error {
			c, err := srv.dialUpstream("http", "example.com", 80)
			if err == nil {
				c.Close()
			}
			return err
		},
		"tls-passthrough dial": func() error {
			c, err := srv.dialRawUpstream("example.com", 443)
			if err == nil {
				c.Close()
			}
			return err
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if err := r2Within(t, name, fn); err == nil {
				t.Fatal("dial through a silent SOCKS upstream succeeded")
			}
		})
	}
}
