package intercept

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A request body rule must leave a body-less request body-less. Swapping the
// empty body for a non-nil reader makes net/http's transport treat the length
// as unknown (a body-less POST then goes out chunked), and stamping
// "Content-Length: 0" onto a GET records a header the client never sent.
func TestRequestBodyRuleLeavesBodylessRequestAlone(t *testing.T) {
	e := New()
	if err := e.SetRules([]store.Rule{{Enabled: true, Type: "req-body", Match: "zzz-nomatch", Replace: "x"}}); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "http://example.com/a", nil)
	if err := e.ApplyRules(get); err != nil {
		t.Fatal(err)
	}
	if get.Body != http.NoBody {
		t.Fatalf("GET body = %T, want http.NoBody", get.Body)
	}
	if v, ok := get.Header["Content-Length"]; ok {
		t.Fatalf("GET gained Content-Length %v", v)
	}

	post := httptest.NewRequest(http.MethodPost, "http://example.com/a", http.NoBody)
	post.Header.Set("Content-Length", "0")
	if err := e.ApplyRules(post); err != nil {
		t.Fatal(err)
	}
	if post.Body != http.NoBody || post.ContentLength != 0 || post.Header.Get("Content-Length") != "0" {
		t.Fatalf("empty POST: body=%T ContentLength=%d header=%q, want NoBody / 0 / \"0\"",
			post.Body, post.ContentLength, post.Header.Get("Content-Length"))
	}
}

// An edit that keeps an empty body must also stay body-less on the wire.
func TestEditedEmptyBodyRequestKeepsNoBody(t *testing.T) {
	orig := httptest.NewRequest(http.MethodPost, "http://example.com/a", http.NoBody)
	r, err := parseEditedRequest([]byte("POST /a HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n"), orig)
	if err != nil {
		t.Fatal(err)
	}
	if r.Body != http.NoBody || r.ContentLength != 0 {
		t.Fatalf("edited empty POST: body=%T ContentLength=%d, want NoBody / 0", r.Body, r.ContentLength)
	}
}
