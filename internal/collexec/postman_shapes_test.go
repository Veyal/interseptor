package collexec

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func hostPort(t *testing.T, raw string) (host, port string) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), u.Port()
}

// A Postman URL object may omit raw; the URL is rebuilt from its parts.
func TestURLObjectWithoutRawIsRebuilt(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	h, p := hostPort(t, rec.srv.URL)
	it := store.Item{Method: "GET", URL: js(m{"protocol": "http", "host": strings.Split(h, "."), "port": p,
		"path":  []any{"api", "v1", m{"type": "string", "value": "users"}},
		"query": []m{{"key": "a", "value": "1"}, {"key": "flag", "value": nil}}})}
	res := send1(e, it)
	if res.Outcome != OutcomeSent {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Error)
	}
	req, _ := rec.last()
	if req.URL.Path != "/api/v1/users" || req.URL.Query().Get("a") != "1" {
		t.Fatalf("rebuilt URL wrong: %s", req.URL)
	}
	if _, ok := req.URL.Query()["flag"]; !ok || strings.Contains(req.URL.RawQuery, "flag=") {
		t.Fatalf("null query value must be a bare key: %q", req.URL.RawQuery)
	}
}

// In Postman the url.query array is authoritative over raw: a disabled row
// must not go out even though raw still lists it.
func TestPostmanURLQueryArrayIsAuthoritative(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	it := store.Item{Method: "GET", URL: js(m{"raw": rec.srv.URL + "/p?a=1&b=2&c=3#frag",
		"query": []m{{"key": "a", "value": "1"}, {"key": "b", "value": "2", "disabled": true}, {"key": "c", "value": "3"}}})}
	res := send1(e, it)
	if res.Outcome != OutcomeSent {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Error)
	}
	req, _ := rec.last()
	if req.URL.RawQuery != "a=1&c=3" {
		t.Fatalf("query on the wire: %q", req.URL.RawQuery)
	}
	// params_json still wins over everything when non-empty (existing contract)
	it.Params = js([]m{{"key": "only", "value": "x"}})
	send1(e, it)
	if req, _ := rec.last(); req.URL.RawQuery != "only=x" {
		t.Fatalf("params_json must stay authoritative: %q", req.URL.RawQuery)
	}
}

func TestDisabledBodyIsNotSent(t *testing.T) {
	e := newEnv(t)
	rec := newRecorder(t, nil)
	it := store.Item{Method: "POST", URL: js(rec.srv.URL),
		Body: js(m{"mode": "raw", "raw": "should-not-go-out", "disabled": true})}
	res := send1(e, it)
	if res.Outcome != OutcomeSent {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Error)
	}
	if _, body := rec.last(); body != "" {
		t.Fatalf("a disabled body was sent: %q", body)
	}
}

// An auth type with no authenticator (ntlm, hawk, ...) is reported at import as
// preserved-inert, so the request must still go out, with a warning.
func TestUnsupportedAuthSendsWithoutItAndWarns(t *testing.T) {
	e := newEnv(t)
	authPipe(e, nil) // the real pipeline always has the collauth suite wired
	rec := newRecorder(t, nil)
	for _, typ := range []string{"ntlm", "hawk", "oauth1", "edgegrid", "asap"} {
		it := store.Item{Method: "GET", URL: js(rec.srv.URL),
			Auth: js(m{"type": typ, typ: []m{{"key": "username", "value": "u"}}})}
		res := send1(e, it)
		if res.Outcome != OutcomeSent {
			t.Fatalf("%s: outcome %s: %s", typ, res.Outcome, res.Error)
		}
		found := false
		for _, w := range res.Warnings {
			if strings.Contains(w, typ) && strings.Contains(w, "sent without") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no 'sent without' warning in %v", typ, res.Warnings)
		}
	}
}
