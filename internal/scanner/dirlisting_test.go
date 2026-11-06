package scanner

import (
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// HTML tag and attribute names are case-insensitive, and whitespace around an
// attribute's "=" is legal. A directory listing written as <A HREF="…"> (some
// embedded servers emit upper-case HTML) was skipped because the anchor gate
// looked for the exact lower-case substring "<a href=".
func TestDirectoryListingFindsUppercaseAndSpacedAnchors(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/uploads/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html"},
			"Strict-Transport-Security": {"max-age=63072000"},
			"Referrer-Policy":           {"no-referrer"},
			"X-Frame-Options":           {"DENY"},
			"Content-Security-Policy":   {"default-src 'self'"},
			"X-Content-Type-Options":    {"nosniff"},
		}),
	}
	for _, tc := range []struct {
		name, body string
	}{
		{"uppercase tags", `<HTML><HEAD><TITLE>Index of /uploads</TITLE></HEAD><BODY><A HREF="a.txt">a</A></BODY></HTML>`},
		{"spaces around =", `<html><head><title>Index of /uploads</title></head><body><a href = "a.txt">a</a></body></html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(flow, nil, []byte(tc.body))
			if !has(got, "Directory listing enabled") {
				t.Fatalf("expected directory-listing finding; got: %s", titles(got))
			}
		})
	}
}
