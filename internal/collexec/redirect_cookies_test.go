package collexec

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// scriptedSender answers each URL with a canned flow and records the Cookie
// header every hop carried.
type scriptedSender struct {
	mu      sync.Mutex
	flows   map[string]*store.Flow
	cookies []string
	next    int64
}

func (s *scriptedSender) Send(r sender.Request) (*store.Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c []string
	for k, vs := range r.Headers {
		if strings.EqualFold(k, "Cookie") {
			c = append(c, vs...)
		}
	}
	if r.Options != nil {
		for _, h := range r.Options.RawHeaders {
			if strings.EqualFold(h.Name, "Cookie") {
				c = append(c, h.Value)
			}
		}
	}
	s.cookies = append(s.cookies, strings.Join(c, "; "))
	f, ok := s.flows[r.URL]
	if !ok {
		f = &store.Flow{Status: 200}
	}
	cp := *f
	s.next++
	cp.ID = s.next
	return &cp, nil
}

func cookieSet(h string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(h, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out[p] = true
		}
	}
	return out
}

// Each redirect hop takes its Cookie header from the jar for that hop's URL:
// a Set-Cookie on the 302 reaches the redirect target, and an https->http
// same-host hop no longer carries Secure cookies.
func TestRedirectHopsRecomputeCookiesFromJar(t *testing.T) {
	for _, raw := range []bool{false, true} {
		e := newEnv(t)
		snd := &scriptedSender{flows: map[string]*store.Flow{
			"https://example.com/login": {Status: 302, ResHeaders: map[string][]string{
				"Location":   {"http://example.com/next"},
				"Set-Cookie": {"fresh=1; Path=/"},
			}},
		}}
		e.pipe.Sender = snd
		e.pipe.Scope = scopeOf1("example.com")
		jar := e.pipe.Jars.For("col1", "", "")
		u, _ := url.Parse("https://example.com/")
		jar.Store(u, []*http.Cookie{{Name: "sec", Value: "1", Path: "/", Secure: true}, {Name: "plain", Value: "1", Path: "/"}}, e.pipe.Clock())

		settings := m{"followRedirects": true}
		if raw {
			settings["rawHeaders"] = true
		}
		res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js("https://example.com/login"), Settings: js(settings)}), Source: SourceRunner})
		if res.Outcome != OutcomeSent || len(snd.cookies) != 2 {
			t.Fatalf("raw=%v outcome %+v hops=%v", raw, res, snd.cookies)
		}
		first, second := cookieSet(snd.cookies[0]), cookieSet(snd.cookies[1])
		if !first["sec=1"] || !first["plain=1"] {
			t.Fatalf("raw=%v first hop cookies %q", raw, snd.cookies[0])
		}
		if second["sec=1"] {
			t.Fatalf("raw=%v Secure cookie sent over http hop: %q", raw, snd.cookies[1])
		}
		if !second["plain=1"] || !second["fresh=1"] {
			t.Fatalf("raw=%v redirect hop missing jar cookies: %q", raw, snd.cookies[1])
		}
	}
}

// An explicit Cookie header the user wrote is still kept on same-host hops.
func TestRedirectKeepsExplicitCookieSameHost(t *testing.T) {
	e := newEnv(t)
	snd := &scriptedSender{flows: map[string]*store.Flow{
		"https://example.com/a": {Status: 302, ResHeaders: map[string][]string{"Location": {"/b"}}},
	}}
	e.pipe.Sender = snd
	e.pipe.Scope = scopeOf1("example.com")
	res := e.step(StepInput{Chain: chainOf(store.Item{Method: "GET", URL: js("https://example.com/a"),
		Headers: js([]m{{"key": "Cookie", "value": "mine=1"}}), Settings: js(m{"followRedirects": true})}), Source: SourceRunner})
	if res.Outcome != OutcomeSent || len(snd.cookies) != 2 || snd.cookies[1] != "mine=1" {
		t.Fatalf("explicit cookie not kept: %+v hops=%v", res, snd.cookies)
	}
}
