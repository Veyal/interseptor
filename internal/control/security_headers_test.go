package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// The control UI was frameable and served captured bodies with the target's
// Content-Type and no sniffing or script protection.
func TestControlResponsesCarryHardeningHeaders(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, path := range []string{"/", "/js/app.js", "/api/version", "/login"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, got)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := resp.Header.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
			t.Errorf("%s: CSP = %q", path, got)
		}
	}

	hash, n := (&projectAPI{h}).storeBody([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`))
	id, _ := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/x.svg",
		HTTPVersion: "HTTP/1.1", Status: 200, Mime: "image/svg+xml",
		ResHeaders: map[string][]string{"Content-Type": {"image/svg+xml"}}, ResBodyHash: hash, ResLen: n,
	})
	resp, err := http.Get(ts.URL + "/api/flows/" + itoa(id) + "/body?side=res")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("captured body download CSP = %q, want sandbox", csp)
	}
}
