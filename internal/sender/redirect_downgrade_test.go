package sender

import (
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// An https → http redirect on the same host would send credentials in clear
// text; it is treated like a cross-host hop and they are stripped.
func TestNextRedirectStripsCredentialsOnSchemeDowngrade(t *testing.T) {
	r := Request{Method: "GET", URL: "https://a.example.com/x",
		Headers: map[string][]string{"Authorization": {"t"}, "Cookie": {"c"}, "X-Keep": {"1"}},
		Options: &SendOptions{RawHeaders: []RawHeader{{Name: "Cookie", Value: "c"}, {Name: "X-Keep", Value: "1"}}}}
	fl := &store.Flow{Status: 302, ResHeaders: map[string][]string{"Location": {"http://a.example.com/y"}}}
	n, ok := nextRedirect(r, fl)
	if !ok {
		t.Fatal("redirect not followed")
	}
	if n.Headers["Authorization"] != nil || n.Headers["Cookie"] != nil || n.Headers["X-Keep"] == nil {
		t.Fatalf("headers after downgrade = %v", n.Headers)
	}
	if len(n.Options.RawHeaders) != 1 || n.Options.RawHeaders[0].Name != "X-Keep" {
		t.Fatalf("raw headers after downgrade = %v", n.Options.RawHeaders)
	}

	// Same host, same scheme keeps them.
	fl.ResHeaders["Location"] = []string{"https://a.example.com/z"}
	if n, _ := nextRedirect(r, fl); n.Headers["Authorization"] == nil {
		t.Fatal("same-origin redirect dropped credentials")
	}
}
