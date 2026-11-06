package scanner

import (
	"net/http"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// Whitespace around an attribute's "=" is legal HTML, so an external
// target=_blank link written as `href = "http://…"` is the same reverse-
// tabnabbing risk as the compact spelling and must be found too.
func TestTabnabbingFindsSpacedHref(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html"},
			"Strict-Transport-Security": {"max-age=63072000"},
			"Content-Security-Policy":   {"default-src 'self'"},
			"Referrer-Policy":           {"no-referrer"},
			"X-Frame-Options":           {"DENY"},
		}),
	}
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"compact", `<a href="http://evil.example/" target="_blank">x</a>`, true},
		{"spaces around =", `<a href = "http://evil.example/" target="_blank">x</a>`, true},
		{"spaces around target =", `<a href="http://evil.example/" target = "_blank">x</a>`, true},
		{"unquoted href", `<a href=http://evil.example/ target=_blank>x</a>`, true},
		{"target first", `<a target="_blank" href="http://evil.example/">x</a>`, true},
		{"noopener", `<a href="http://evil.example/" target="_blank" rel="noopener">x</a>`, false},
		{"internal link", `<a href="/settings" target="_blank">x</a>`, false},
		// data-href / data-target are not the attributes that open a new tab,
		// so neither may be read as one.
		{"data-href is not an href", `<a data-href="http://evil.example/" target="_blank">x</a>`, false},
		{"data-target is not a target", `<a data-target="_blank" href="http://evil.example/">x</a>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(flow, nil, []byte(tc.body))
			if has(got, "Reverse tabnabbing (target=_blank without noopener)") != tc.want {
				t.Fatalf("tabnabbing = %v, want %v: %s", !tc.want, tc.want, titles(got))
			}
		})
	}
}
