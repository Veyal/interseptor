package scanner

import (
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// Media types are case-insensitive (RFC 9110), but the HTML/JavaScript gates
// compared the Content-Type value and the recorded MIME case-sensitively, so a
// response served as "TEXT/HTML" or "Application/JavaScript" skipped every
// check that only applies to that kind of document.
func TestMIMETypeMatchingIsCaseInsensitive(t *testing.T) {
	html := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200,
		Mime: "TEXT/HTML",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"TEXT/HTML; charset=utf-8"},
			"Strict-Transport-Security": {"max-age=63072000"},
		}),
	}
	got := Analyze(html, nil, []byte("<html><body>hi</body></html>"))
	if !has(got, "Missing security response headers") {
		t.Fatalf("an upper-case HTML content type must still get HTML checks; got: %s", titles(got))
	}

	js := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/app.js", Status: 200,
		Mime: "Application/JavaScript",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"Application/JavaScript"},
			"Strict-Transport-Security": {"max-age=63072000"},
		}),
	}
	got = Analyze(js, nil, []byte("var x=1;\n//# sourceMappingURL=app.js.map"))
	if !has(got, "Source map reference exposed") {
		t.Fatalf("an upper-case JavaScript content type must still get JS checks; got: %s", titles(got))
	}
}
