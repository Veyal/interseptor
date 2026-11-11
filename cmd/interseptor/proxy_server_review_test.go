package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// An idle keep-alive client must not hold a proxy connection (a goroutine and a
// descriptor) for the life of the process. The limit was added once and lost in
// a merge; ReadHeaderTimeout alone does not cover a connection that is waiting
// for its next request, because http.Server falls back to ReadTimeout (unset).
func TestProxyServerReapsIdleKeepAliveConnections(t *testing.T) {
	srv := newProxyServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	if srv.IdleTimeout <= 0 {
		t.Fatalf("proxy listener IdleTimeout = %v, want a bound", srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("proxy listener ReadHeaderTimeout = %v, want a bound", srv.ReadHeaderTimeout)
	}
	// The bound must not cut bodies, tunnels or long polls.
	if srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Fatalf("proxy listener must not bound whole requests: ReadTimeout=%v WriteTimeout=%v", srv.ReadTimeout, srv.WriteTimeout)
	}

	// Behaviour, with the limit shortened: the server closes the idle connection.
	srv.IdleTimeout = 150 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("idle connection was not closed by the server: read err = %v", err)
	}
}
