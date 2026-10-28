package main

import (
	"net/http"
	"testing"
)

// The control plane serves the public /oob/ catcher, so it must bound header
// reads (slowloris) and idle keep-alives, but never the write side: SSE
// streams stay open indefinitely.
func TestControlServerHasSlowlorisBounds(t *testing.T) {
	srv := newControlServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatal("control ReadHeaderTimeout unset")
	}
	if srv.IdleTimeout <= 0 {
		t.Fatal("control IdleTimeout unset")
	}
	if srv.MaxHeaderBytes <= 0 || srv.MaxHeaderBytes > 1<<20 {
		t.Fatalf("control MaxHeaderBytes = %d, want (0, 1 MiB]", srv.MaxHeaderBytes)
	}
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Fatalf("control WriteTimeout/ReadTimeout = %v/%v, want 0 (SSE and uploads)", srv.WriteTimeout, srv.ReadTimeout)
	}
}

// The proxy bounds only header reads: bodies, tunnels and long polls are
// forwarded without a deadline.
func TestProxyServerBoundsOnlyHeaderReads(t *testing.T) {
	srv := newProxyServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatal("proxy ReadHeaderTimeout unset")
	}
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Fatalf("proxy WriteTimeout/ReadTimeout = %v/%v, want 0", srv.WriteTimeout, srv.ReadTimeout)
	}
}
