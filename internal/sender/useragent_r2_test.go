package sender

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/store"
)

// wireCapture is an httptest server that remembers the request it received.
type wireCapture struct {
	mu    sync.Mutex
	hdr   http.Header
	proto string
	srv   *httptest.Server
}

func newWireCapture(t *testing.T, h2 bool) *wireCapture {
	t.Helper()
	wc := &wireCapture{}
	wc.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wc.mu.Lock()
		wc.hdr, wc.proto = r.Header.Clone(), r.Proto
		wc.mu.Unlock()
		w.Write([]byte("ok"))
	}))
	if h2 {
		wc.srv.EnableHTTP2 = true
		wc.srv.StartTLS()
	} else {
		wc.srv.Start()
	}
	t.Cleanup(wc.srv.Close)
	return wc
}

func (wc *wireCapture) got() (http.Header, string) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return wc.hdr, wc.proto
}

func headerValues(h map[string][]string, name string) []string {
	return http.Header(h).Values(name)
}

// The flow is evidence of what the target received. When the template has no
// User-Agent, Go's transport adds its own on the wire; the stored flow must
// show that same value, for HTTP/1.1 and HTTP/2.
func TestR2DefaultUserAgentIsRecordedAsSent(t *testing.T) {
	cases := []struct {
		name  string
		h2    bool
		opts  *SendOptions
		proto string
	}{
		{"http/1.1 default client", false, nil, "HTTP/1.1"},
		{"http/1.1 options client", false, &SendOptions{}, "HTTP/1.1"},
		{"http/2", true, &SendOptions{HTTP2: true}, "HTTP/2.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wc := newWireCapture(t, tc.h2)
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			snd := New(st, capture.New(st))

			f, err := snd.Send(Request{Method: "POST", URL: wc.srv.URL + "/p", Body: []byte("a=1"),
				Headers: map[string][]string{"X-Probe": {"1"}}, Options: tc.opts})
			if err != nil || f.Status != 200 {
				t.Fatalf("send: %v %+v", err, f)
			}
			wire, proto := wc.got()
			if proto != tc.proto {
				t.Fatalf("server saw %s, want %s", proto, tc.proto)
			}
			wireUA := wire.Values("User-Agent")
			if len(wireUA) != 1 || wireUA[0] == "" {
				t.Fatalf("precondition: the transport should have added a User-Agent, wire has %q", wireUA)
			}
			if got := headerValues(f.ReqHeaders, "User-Agent"); len(got) != 1 || got[0] != wireUA[0] {
				t.Errorf("returned flow User-Agent = %q, wire sent %q", got, wireUA)
			}
			stored, err := st.GetFlow(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := headerValues(stored.ReqHeaders, "User-Agent"); len(got) != 1 || got[0] != wireUA[0] {
				t.Errorf("stored flow User-Agent = %q, wire sent %q", got, wireUA)
			}
			// Every other header the server saw must be on the flow as sent.
			for name, vs := range wire {
				if got := headerValues(stored.ReqHeaders, name); len(got) != len(vs) || (len(vs) > 0 && got[0] != vs[0]) {
					t.Logf("wire-only or differing header %s: wire %q, flow %q", name, vs, got)
				}
			}
		})
	}
}

// A User-Agent the operator set is what goes out and what is recorded; an
// explicitly empty one suppresses the header on the wire and no value is
// invented for it.
func TestR2ExplicitUserAgentIsUntouched(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		wc := newWireCapture(t, h2)
		snd := newOptSender(t)
		opts := &SendOptions{HTTP2: h2}
		f, err := snd.Send(Request{URL: wc.srv.URL, Headers: map[string][]string{"User-Agent": {"probe/1"}}, Options: opts})
		if err != nil || f.Status != 200 {
			t.Fatalf("send: %v %+v", err, f)
		}
		wire, _ := wc.got()
		if got := headerValues(f.ReqHeaders, "User-Agent"); len(got) != 1 || got[0] != "probe/1" || wire.Get("User-Agent") != "probe/1" {
			t.Errorf("h2=%v explicit UA: flow %q wire %q", h2, got, wire.Values("User-Agent"))
		}
		f, err = snd.Send(Request{URL: wc.srv.URL, Headers: map[string][]string{"User-Agent": {""}}, Options: opts})
		if err != nil || f.Status != 200 {
			t.Fatalf("send: %v %+v", err, f)
		}
		wire, _ = wc.got()
		if len(wire.Values("User-Agent")) != 0 {
			t.Fatalf("h2=%v precondition: an empty User-Agent suppresses the header, wire has %q", h2, wire.Values("User-Agent"))
		}
		// Nothing may be invented for it. (The flow still lists the operator's
		// empty "User-Agent:" line, which is the template as written.)
		if got := headerValues(f.ReqHeaders, "User-Agent"); len(got) > 1 || (len(got) == 1 && got[0] != "") {
			t.Errorf("h2=%v suppressed UA: flow records %q", h2, got)
		}
	}
}

// The raw-header path writes exactly the caller's bytes: no User-Agent is
// sent, so none may be recorded.
func TestR2RawHeaderSendRecordsNoInventedUserAgent(t *testing.T) {
	wc := newWireCapture(t, false)
	snd := newOptSender(t)
	f, err := snd.Send(Request{URL: wc.srv.URL, Options: &SendOptions{RawHeaders: []RawHeader{{Name: "X-Probe", Value: "1"}}}})
	if err != nil || f.Status != 200 {
		t.Fatalf("send: %v %+v", err, f)
	}
	wire, _ := wc.got()
	if len(wire.Values("User-Agent")) != 0 {
		t.Fatalf("precondition: raw path sends no User-Agent, wire has %q", wire.Values("User-Agent"))
	}
	if got := headerValues(f.ReqHeaders, "User-Agent"); len(got) != 0 {
		t.Errorf("raw send recorded an unsent User-Agent %q", got)
	}
}

// Nothing was written when the connection is refused, so nothing is recorded.
func TestR2FailedDialRecordsNoUserAgent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	snd := newOptSender(t)
	f, err := snd.Send(Request{URL: "http://" + addr + "/", Options: &SendOptions{Timeout: 2 * time.Second}})
	if err != nil || f.Error == "" {
		t.Fatalf("want an errored flow: %v %+v", err, f)
	}
	if got := headerValues(f.ReqHeaders, "User-Agent"); len(got) != 0 {
		t.Errorf("a request that was never written recorded User-Agent %q", got)
	}
}
