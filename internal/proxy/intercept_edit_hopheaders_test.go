package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

// An edited raw request is re-parsed verbatim, so it can reintroduce the
// hop-by-hop and proxy-only headers the cloning path strips — most importantly
// Proxy-Authorization, which authenticates this proxy and must never be handed
// to an origin (the upgrade path already guarantees that). Editing a held
// request must not become a credential leak.
func TestEditedRequestStripsProxyCredentialsAndHopHeaders(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		io.WriteString(w, "ok")
	})}
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen origin: %v", err)
	}
	defer up.Close()
	go upstream.Serve(up)
	defer upstream.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, up.Addr().String())
	}
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	defer pl.Close()
	go srv.Serve(pl)

	// Client credentials configured on the proxy URL; this proxy listener has no
	// password of its own, so it must still refuse to relay the header upstream.
	proxyURL, _ := url.Parse("http://proxyuser:proxysecret@" + pl.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}
	go func() {
		resp, err := client.Get("http://example.com/thing")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()

	waitFor(t, "held request", func() bool { return len(eng.Queue()) == 1 })
	held := eng.Queue()[0]
	if err := eng.Forward(held.ID, []byte("GET /thing HTTP/1.1\r\nHost: example.com\r\nConnection: keep-alive\r\nProxy-Authorization: Basic cHJveHl1c2VyOnByb3h5c2VjcmV0\r\nX-Edited: 1\r\n\r\n")); err != nil {
		t.Fatalf("forward edited request: %v", err)
	}

	select {
	case h := <-seen:
		if got := h.Get("Proxy-Authorization"); got != "" {
			t.Fatalf("edited request leaked Proxy-Authorization to the origin: %q", got)
		}
		if got := h.Get("X-Edited"); got != "1" {
			t.Fatalf("edited header missing at origin (X-Edited=%q)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("origin saw no request")
	}
}
