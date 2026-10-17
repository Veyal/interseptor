package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tlsca"
)

func mitmTestResponse(body string, chunked bool) *http.Response {
	h := http.Header{}
	for i := 0; i < 12; i++ {
		h.Set(fmt.Sprintf("X-Header-%02d", i), "value")
	}
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       httptest.NewRequest(http.MethodGet, "https://example.com/", nil),
	}
	if chunked {
		resp.ContentLength = -1
		resp.TransferEncoding = []string{"chunked"}
	}
	return resp
}

// BenchmarkWriteResponseConnTLS measures relaying a small response over a real
// TLS tunnel (loopback TCP), the MITM HTTP/1.1 client leg.
func BenchmarkWriteResponseConnTLS(b *testing.B) {
	ca, err := tlsca.LoadOrCreate(b.TempDir())
	if err != nil {
		b.Fatalf("CA: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		srvTLS := tls.Server(c, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return ca.LeafForHost("example.com")
		}})
		_, _ = io.Copy(io.Discard, srvTLS) // the "client" side just drains
	}()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: "example.com"})
	if err := conn.Handshake(); err != nil {
		b.Fatalf("handshake: %v", err)
	}
	defer conn.Close()
	srv := New(nil, nil, nil, nil, nil)
	body := strings.Repeat("x", 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		flow := &proxyFlow{Flow: &store.Flow{Host: "example.com", Scheme: "https", Port: 443}, suppressCapture: true}
		if err := srv.writeResponseConn(conn, mitmTestResponse(body, i%2 == 1), flow); err != nil {
			b.Fatalf("writeResponseConn: %v", err)
		}
	}
}

// Each write on a MITM tunnel is a TLS record and a syscall. A small response
// used to go out as 53-56 writes (four per header line from Header.Write,
// three per chunk). Now the head, the body, and the chunked terminator each
// leave in at most one write; the head is never held back waiting for body
// bytes, so a stream's head still reaches the client at once.
func TestWriteResponseConnCoalescesWrites(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s), nil, nil, nil)
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			conn := &countConn{}
			flow := srv.newFlow(&store.Flow{Host: "example.com", Scheme: "https", Port: 443})
			if err := srv.writeResponseConn(conn, mitmTestResponse("hello example.com", chunked), flow); err != nil {
				t.Fatalf("writeResponseConn: %v", err)
			}
			if conn.writes > 3 {
				t.Fatalf("small response took %d writes on the tunnel, want at most 3", conn.writes)
			}
			got, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(conn.buf.Bytes())), nil)
			if err != nil {
				t.Fatalf("parse relayed response: %v", err)
			}
			body, _ := io.ReadAll(got.Body)
			if string(body) != "hello example.com" || got.Header.Get("X-Header-11") != "value" {
				t.Fatalf("relayed response body=%q header=%q", body, got.Header.Get("X-Header-11"))
			}
		})
	}
}
