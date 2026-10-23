package proxy

import (
	"bufio"
	"bytes"
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

// pipelinedConnectConn sends the CONNECT request in the same Write as the
// first TLS record (the ClientHello), and swallows the proxy's CONNECT
// response head before handing reads to the TLS client.
type pipelinedConnectConn struct {
	net.Conn
	connect  []byte
	sawReply bool
}

func (c *pipelinedConnectConn) Write(p []byte) (int, error) {
	if c.connect != nil {
		buf := append(c.connect, p...)
		c.connect = nil
		if _, err := c.Conn.Write(buf); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *pipelinedConnectConn) Read(p []byte) (int, error) {
	if !c.sawReply {
		var head []byte
		one := make([]byte, 1)
		for !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
			if _, err := c.Conn.Read(one); err != nil {
				return 0, err
			}
			head = append(head, one[0])
		}
		if !bytes.HasPrefix(head, []byte("HTTP/1.1 200")) {
			return 0, fmt.Errorf("CONNECT reply: %q", head)
		}
		c.sawReply = true
	}
	return c.Conn.Read(p)
}

func TestConnectWithPipelinedClientHello(t *testing.T) {
	for _, bypass := range []bool{false, true} {
		t.Run(fmt.Sprintf("bypass=%v", bypass), func(t *testing.T) {
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, "ORIGIN-OK")
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
			srv.tr.TLSClientConfig = &tls.Config{RootCAs: originPool(origin)}
			pool := x509.NewCertPool()
			if bypass {
				srv.SetTLSBypassHosts([]string{host})
				pool.AddCert(origin.Certificate())
			} else {
				pool.AppendCertsFromPEM(ca.CertPEM())
			}
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
			raw.SetDeadline(time.Now().Add(3 * time.Second))
			pc := &pipelinedConnectConn{Conn: raw, connect: []byte(fmt.Sprintf("CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port, host, port))}
			tc := tls.Client(pc, &tls.Config{ServerName: host, RootCAs: pool, NextProtos: []string{"http/1.1"}})
			if err := tc.Handshake(); err != nil {
				t.Fatalf("handshake after pipelined CONNECT: %v", err)
			}
			fmt.Fprintf(tc, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
			resp, err := http.ReadResponse(bufio.NewReader(tc), &http.Request{Method: "GET"})
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "ORIGIN-OK" {
				t.Fatalf("body = %q", body)
			}
			if !bypass {
				waitFlows(t, st, 1)
			}
		})
	}
}
