package sender

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestOptionsHTTP2NegotiatesAndRecordsProto(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "proto=%s", r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	snd := newOptSender(t)

	// Default (HTTP2 off): TLS origin, no h2 ALPN -> server falls back to HTTP/1.1.
	f, err := snd.Send(Request{URL: srv.URL, Options: &SendOptions{}})
	if err != nil || f.Status != 200 {
		t.Fatalf("h1 send: %v %+v", err, f)
	}
	if f.HTTPVersion != "HTTP/1.1" {
		t.Fatalf("default must stay HTTP/1.1, got %q", f.HTTPVersion)
	}

	// HTTP2 on: ALPN negotiates h2 end to end.
	f2, err := snd.Send(Request{URL: srv.URL, Options: &SendOptions{HTTP2: true}})
	if err != nil || f2.Status != 200 {
		t.Fatalf("h2 send: %v %+v", err, f2)
	}
	if f2.HTTPVersion != "HTTP/2.0" {
		t.Fatalf("HTTP2 option must record HTTP/2.0, got %q", f2.HTTPVersion)
	}
}

func TestOptionsHTTP2SplitsClientCache(t *testing.T) {
	snd := newOptSender(t)
	h1, _ := snd.clientFor(&SendOptions{})
	h2, _ := snd.clientFor(&SendOptions{HTTP2: true})
	if h1 == h2 {
		t.Fatal("HTTP2 flag must not share a client with HTTP/1.1")
	}
}

func TestOptionsMaxRedirectsHopLimit(t *testing.T) {
	// An endless redirect chain must stop after MaxRedirects hops and return the
	// last 3xx flow rather than looping forever.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer srv.Close()
	snd := newOptSender(t)
	var hops int
	f, err := snd.Send(Request{URL: srv.URL + "/start", Options: &SendOptions{
		FollowRedirects: true, MaxRedirects: 3,
		OnFlow: func(*store.Flow, Meta) { hops++ },
	}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != http.StatusFound {
		t.Fatalf("final flow status = %d, want 302 (chain never resolves)", f.Status)
	}
	// 1 initial send + 3 redirect hops = 4 flows.
	if hops != 4 {
		t.Fatalf("hops = %d, want 4 (initial + MaxRedirects)", hops)
	}
	if !strings.HasSuffix(f.Path, "/next") && !strings.HasSuffix(f.Path, "/start") {
		t.Fatalf("unexpected final path %q", f.Path)
	}
}
