package collexec

import (
	"testing"

	"github.com/Veyal/interseptor/internal/varstore"
)

// Query parameters (including an apikey auth placed in the query) go before
// the fragment, never into it.
func TestAppendQueryKeepsFragmentLast(t *testing.T) {
	kv := []varstore.KV{{Key: "api_key", Value: "k 1"}}
	cases := map[string]string{
		"https://example.com/p#frag":      "https://example.com/p?api_key=k%201#frag",
		"https://example.com/p?a=1#frag":  "https://example.com/p?a=1&api_key=k%201#frag",
		"https://example.com/p":           "https://example.com/p?api_key=k%201",
		"https://example.com/p?a=1":       "https://example.com/p?a=1&api_key=k%201",
		"https://example.com/p#f?not=q":   "https://example.com/p?api_key=k%201#f?not=q",
		"https://example.com/p?#frag":     "https://example.com/p?api_key=k%201#frag",
		"https://example.com/p?a=1&#frag": "https://example.com/p?a=1&api_key=k%201#frag",
	}
	for in, want := range cases {
		if got := appendQuery(in, kv); got != want {
			t.Errorf("appendQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
