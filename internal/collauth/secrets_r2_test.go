package collauth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A credential placed in the query string is percent-encoded there. The
// manager's scrubber must cover that spelling too, or the resolved URL shown
// in a step result carries the key.
func TestQueryCredentialIsScrubbedInItsEncodedForm(t *testing.T) {
	const key = "k3y+with/special=="
	m := New(Options{})
	req := &Request{Method: "GET", URL: "https://api.example.com/x", Header: http.Header{}}
	if _, err := m.Apply(context.Background(), "c|e|i", Config{Type: "apikey", Fields: map[string]string{"key": "auth", "value": key, "in": "query"}}, req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL, "k3y%2Bwith%2Fspecial%3D%3D") {
		t.Fatalf("precondition: the key should be on the wire URL encoded, got %s", req.URL)
	}
	if got := m.Scrub(req.URL); strings.Contains(got, "k3y") || strings.Contains(got, "special") {
		t.Fatalf("encoded api key survives the scrub: %s", got)
	}

	// Same for an OAuth2 access token sent as a query parameter.
	const tok = "tok+en/value=="
	st := NewMemoryStore()
	st.Put("c|e|i", Token{Access: tok, Expiry: time.Now().Add(time.Hour)})
	m = New(Options{Store: st})
	req = &Request{Method: "GET", URL: "https://api.example.com/x", Header: http.Header{}}
	if _, err := m.Apply(context.Background(), "c|e|i", Config{Type: "oauth2", Fields: map[string]string{"addTokenTo": "queryParams", "queryParamKey": "auth"}}, req); err != nil {
		t.Fatal(err)
	}
	if got := m.Scrub(req.URL); strings.Contains(got, "tok%2Ben") || strings.Contains(got, "value%3D") {
		t.Fatalf("encoded access token survives the scrub: %s", got)
	}
}
