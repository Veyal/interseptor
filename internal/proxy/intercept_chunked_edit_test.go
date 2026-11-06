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

// An operator may paste a chunked request into the Intercept editor. Forwarding
// it must send the decoded payload with chunked framing, not the chunk markers
// as literal body bytes (double framing).
func TestForwardEditedChunkedRequestKeepsPayload(t *testing.T) {
	bodies := make(chan string, 1)
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
		io.WriteString(w, "ok")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go origin.Serve(ln)
	defer origin.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	eng := intercept.New()
	eng.SetEnabled(true)
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
	}
	pl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pl.Close()
	go srv.Serve(pl)
	pu, _ := url.Parse("http://" + pl.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}

	go func() {
		resp, err := client.Post("http://example.com/up", "text/plain", nil)
		if err == nil {
			resp.Body.Close()
		}
	}()

	var held intercept.Held
	deadline := time.Now().Add(5 * time.Second)
	for {
		if q := eng.Queue(); len(q) > 0 {
			held = q[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("request never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("held raw: %q", held.Raw)

	// The operator rewrites the raw as a chunked request carrying "hello".
	edited := "POST /up HTTP/1.1\r\nHost: example.com\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"
	if err := eng.Forward(held.ID, []byte(edited)); err != nil {
		t.Fatalf("forward: %v", err)
	}
	select {
	case b := <-bodies:
		t.Logf("origin body = %q", b)
		if b != "hello" {
			t.Fatalf("origin body = %q, want %q", b, "hello")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("origin never saw the forwarded request")
	}
}
