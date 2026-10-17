package sender

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

func newSelfGuardSender(t *testing.T) (*Sender, *httptest.Server, *atomic.Int32) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var hits atomic.Int32
	own := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(own.Close)
	_, p, _ := net.SplitHostPort(own.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	s := New(st, capture.New(st))
	s.SetOwnPorts(func() []int { return []int{port} })
	return s, own, &hits
}

func TestSelfGuardRefusesOwnListenerAliases(t *testing.T) {
	s, own, hits := newSelfGuardSender(t)
	_, port, _ := net.SplitHostPort(own.Listener.Addr().String())
	for _, host := range []string{"127.0.0.1", "0.0.0.0", "[::]", "[::ffff:127.0.0.1]", "localhost."} {
		for _, opts := range []*SendOptions{nil, {Timeout: 5e9}, {RawHeaders: []RawHeader{{Name: "Host", Value: "127.0.0.1:" + port}}}} {
			f, err := s.Send(Request{Method: "POST", URL: "http://" + host + ":" + port + "/api/keys", Host: "127.0.0.1:" + port, Options: opts})
			if err != nil {
				t.Fatalf("%s: %v", host, err)
			}
			if f.Status == http.StatusCreated || !strings.Contains(f.Error, "own listener") {
				t.Errorf("%s (opts=%v): status=%d err=%q, want own-listener refusal", host, opts != nil, f.Status, f.Error)
			}
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("own listener received %d request(s)", n)
	}
}

func TestSelfGuardRefusesOwnListenerThroughUpstreamProxy(t *testing.T) {
	s, own, hits := newSelfGuardSender(t)
	_, port, _ := net.SplitHostPort(own.Listener.Addr().String())
	var proxied atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer up.Close()
	if err := s.SetUpstreamProxy(up.URL); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []*SendOptions{nil, {Timeout: 5e9}} {
		f, err := s.Send(Request{URL: "http://0.0.0.0:" + port + "/", Options: opts})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(f.Error, "own listener") {
			t.Errorf("via upstream proxy (opts=%v): status=%d err=%q, want own-listener refusal", opts != nil, f.Status, f.Error)
		}
	}
	if proxied.Load() != 0 || hits.Load() != 0 {
		t.Fatalf("self-targeted send reached the proxy (%d) or own listener (%d)", proxied.Load(), hits.Load())
	}
	// Other destinations still go through the upstream proxy.
	f, err := s.Send(Request{URL: "http://example.com/"})
	if err != nil || f.Status != http.StatusTeapot {
		t.Fatalf("ordinary send via upstream proxy: status=%v err=%v", f, err)
	}
}

func TestSelfGuardLeavesOtherPortsAlone(t *testing.T) {
	s, _, _ := newSelfGuardSender(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer other.Close()
	f, err := s.Send(Request{URL: other.URL + "/"})
	if err != nil || f.Status != http.StatusAccepted {
		t.Fatalf("loopback target on a non-own port must still work: %+v %v", f, err)
	}
}

func TestDialTargetRefusesOwnListener(t *testing.T) {
	s, own, _ := newSelfGuardSender(t)
	_, port, _ := net.SplitHostPort(own.Listener.Addr().String())
	if c, err := s.DialTarget(t.Context(), "tcp", "localhost.:"+port); err == nil || !strings.Contains(err.Error(), "own listener") {
		if c != nil {
			c.Close()
		}
		t.Fatalf("DialTarget to own listener: err=%v", err)
	}
}
