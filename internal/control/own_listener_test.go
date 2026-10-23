package control

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsLocalLiteralHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1":     true,
		"localhost":     true,
		"localhost.":    true,
		"LocalHost.":    true,
		"app.localhost": true,
		"0.0.0.0":       true,
		"::":            true,
		"[::1]":         true,
		"::1":           true,
		"example.com":   false,
		"10.0.0.1":      false,
		"":              false,
	} {
		if got := isLocalLiteralHost(host); got != want {
			t.Errorf("isLocalLiteralHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// ownListenerHub serves the control API on a real loopback port and records it
// as the hub's own address, so sends to that port must be refused.
func ownListenerHub(t *testing.T, hit ...*atomic.Bool) (*Hub, *httptest.Server, string) {
	t.Helper()
	h, _, _ := newHub(t)
	var handler http.Handler = h.Handler()
	if len(hit) > 0 {
		handler = wrapHit(handler, hit[0])
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	h.SetSelfAddr("127.0.0.1:" + port)
	return h, ts, port
}

func postRepeater(t *testing.T, ts *httptest.Server, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRepeaterRefusesOwnListenerAliases(t *testing.T) {
	_, ts, port := ownListenerHub(t)
	for _, host := range []string{"0.0.0.0", "localhost.", "[::]"} {
		body := `{"method":"GET","url":"http://` + host + `:` + port + `/api/keys","headers":"Host: 127.0.0.1:` + port + `"}`
		if c, _ := postRepeater(t, ts, body); c != http.StatusForbidden {
			t.Errorf("repeater → %s own port: got %d, want 403", host, c)
		}
	}
}

func TestRepeaterRedirectToOwnListenerBlockedAtDial(t *testing.T) {
	var hit atomic.Bool
	_, ts, port := ownListenerHub(t, &hit)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:"+port+"/api/settings", http.StatusFound)
	}))
	defer target.Close()
	c, out := postRepeater(t, ts, `{"method":"GET","url":"`+target.URL+`/","followRedirects":true}`)
	if c != http.StatusOK {
		t.Fatalf("status %d: %v", c, out)
	}
	if hit.Load() {
		t.Fatal("redirect hop reached the control API")
	}
	if e, _ := out["error"].(string); !strings.Contains(e, "blocked by IP guard") {
		t.Fatalf("final hop error = %q, want IP guard refusal (%v)", e, out)
	}
}

func TestRepeaterDefaultSendStillReachesOtherLoopbackPorts(t *testing.T) {
	_, ts, _ := ownListenerHub(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	c, out := postRepeater(t, ts, `{"method":"GET","url":"`+target.URL+`/"}`)
	if c != http.StatusOK || out["status"] != float64(204) {
		t.Fatalf("status %d, flow %v", c, out)
	}
}

// wrapHit records whether any request other than the repeater call itself
// reached the control handler.
func wrapHit(next http.Handler, hit *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/repeater/send" {
			hit.Store(true)
		}
		next.ServeHTTP(w, r)
	})
}
