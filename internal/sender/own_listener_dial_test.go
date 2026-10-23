package sender

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// The own-listener check runs on the resolved address of the shared client's
// dial, so a name that resolves to loopback cannot reach our listeners, while
// ordinary sends keep the default transport (no per-options client).
func TestSharedClientRefusesOwnListenerAfterResolution(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hits, 1) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	_, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, capture.New(st))
	s.SetOwnListenerCheck(func(ip net.IP, p int) bool { return p == port && ip.IsLoopback() })

	if f, err := s.Send(Request{Method: "GET", URL: "http://localhost:" + portStr + "/api/keys"}); err == nil && (f == nil || f.Error == "") {
		t.Fatal("send to an own listener by name succeeded")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("own listener reached %d times", n)
	}

	s.SetOwnListenerCheck(func(net.IP, int) bool { return false })
	if _, err := s.Send(Request{Method: "GET", URL: srv.URL + "/ok"}); err != nil {
		t.Fatalf("ordinary send failed: %v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("hits = %d, want 1", n)
	}
}
