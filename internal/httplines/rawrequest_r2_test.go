package httplines

import (
	"errors"
	"testing"
)

// The parser returned non-origin-form request targets as a "path": an opaque URI
// such as "x:@other.example/a" came back as "@other.example/a", which moves the
// authority when a caller appends it to scheme://host.
func TestParseRawRequestRejectsOpaqueRequestTarget(t *testing.T) {
	for _, line := range []string{
		"GET x:@other.example/a HTTP/1.1",
		"POST x:@127.0.0.1:9966/api/settings HTTP/1.1",
		"GET mailto:someone@other.example HTTP/1.1",
		"GET http:other.example/a HTTP/1.1",
	} {
		_, path, _, _, err := ParseRawRequest(line + "\r\nHost: target.example\r\n\r\n")
		if !errors.Is(err, ErrRequestTarget) {
			t.Errorf("%q: path=%q err=%v, want ErrRequestTarget", line, path, err)
		}
	}
}

// Forms people legitimately type keep working, and the path is always either
// origin-form or the asterisk.
func TestParseRawRequestKeepsLegitimateRequestTargets(t *testing.T) {
	cases := []struct{ line, method, path, host string }{
		{"GET /a?b=1 HTTP/1.1", "GET", "/a?b=1", "target.example"},
		{"GET http://host.example/p?q=1 HTTP/1.1", "GET", "/p?q=1", "host.example"},        // absolute-form
		{"GET https://host.example HTTP/1.1", "GET", "/", "host.example"},                  // absolute-form, no path
		{"OPTIONS * HTTP/1.1", "OPTIONS", "*", "target.example"},                           // asterisk-form
		{"GET //double.example/a HTTP/1.1", "GET", "//double.example/a", "target.example"}, // a path, not an authority
		{"GET /a/@b:c HTTP/1.1", "GET", "/a/@b:c", "target.example"},
	}
	for _, c := range cases {
		method, path, headers, _, err := ParseRawRequest(c.line + "\r\nHost: target.example\r\n\r\n")
		if err != nil || method != c.method || path != c.path {
			t.Errorf("%q: method=%q path=%q err=%v, want %q %q", c.line, method, path, err, c.method, c.path)
			continue
		}
		if got := headers["Host"]; len(got) != 1 || got[0] != c.host {
			t.Errorf("%q: Host=%v, want %q", c.line, got, c.host)
		}
	}
}
