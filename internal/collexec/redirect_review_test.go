package collexec

import (
	"testing"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// A redirect from https to http on the same host must not carry credentials:
// they would cross the network in clear text. base.Host and target.Host are
// equal ("example.com") for that hop, so a host-only comparison keeps them.
func TestNextHopStripsCredentialsOnSchemeDowngrade(t *testing.T) {
	flow := &store.Flow{Status: 302, ResHeaders: map[string][]string{"Location": {"http://example.com/next"}}}
	for _, raw := range []bool{false, true} {
		req := sender.Request{Method: "GET", URL: "https://example.com/start", Options: &sender.SendOptions{}}
		if raw {
			req.Options.RawHeaders = []sender.RawHeader{
				{Name: "Authorization", Value: "Bearer canary-token"}, {Name: "Cookie", Value: "sid=canary"},
				{Name: "Proxy-Authorization", Value: "Basic canary"}, {Name: "X-Keep", Value: "1"},
			}
		} else {
			req.Headers = map[string][]string{
				"Authorization": {"Bearer canary-token"}, "Cookie": {"sid=canary"},
				"Proxy-Authorization": {"Basic canary"}, "X-Keep": {"1"},
			}
		}
		next, ok := nextHop(req, flow)
		if !ok || next.URL != "http://example.com/next" {
			t.Fatalf("raw=%v: next=%+v ok=%v", raw, next, ok)
		}
		kept := map[string]bool{}
		for k := range next.Headers {
			kept[k] = true
		}
		for _, h := range next.Options.RawHeaders {
			kept[h.Name] = true
		}
		for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
			if kept[name] {
				t.Errorf("raw=%v: %s forwarded over an https -> http downgrade", raw, name)
			}
		}
		if !kept["X-Keep"] {
			t.Errorf("raw=%v: unrelated header dropped", raw)
		}
	}

	// Same scheme and host: credentials stay (this is the ordinary redirect).
	same := &store.Flow{Status: 302, ResHeaders: map[string][]string{"Location": {"/next"}}}
	next, ok := nextHop(sender.Request{URL: "https://example.com/start", Headers: map[string][]string{"Authorization": {"Bearer canary-token"}}}, same)
	if !ok || len(next.Headers["Authorization"]) != 1 {
		t.Fatalf("same-origin redirect lost Authorization: %+v", next.Headers)
	}
	// http -> https upgrade on the same host keeps them too.
	up := &store.Flow{Status: 302, ResHeaders: map[string][]string{"Location": {"https://example.com/next"}}}
	next, ok = nextHop(sender.Request{URL: "http://example.com/start", Headers: map[string][]string{"Authorization": {"Bearer canary-token"}}}, up)
	if !ok || len(next.Headers["Authorization"]) != 1 {
		t.Fatalf("upgrade redirect lost Authorization: %+v", next.Headers)
	}
}
