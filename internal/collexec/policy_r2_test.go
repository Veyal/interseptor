package collexec

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collauth"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// A request the policy refuses (base-target pin, scope) must not cause any
// auth traffic first: the digest challenge probe goes to the request URL
// itself, so it used to reach a host the step then reported as blocked.
func TestBlockedRequestSendsNoDigestProbe(t *testing.T) {
	e := newEnv(t)
	api := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Digest realm="r", nonce="abc", qop="auth"`)
		w.WriteHeader(401)
	})
	e.pipe.Scope = scopeOf1("127.0.0.1")
	e.pipe.Auth = collauth.New(collauth.Options{Doer: StepDoer{}})
	ch := chainOf(store.Item{Method: "GET", URL: js(api.srv.URL + "/data")})
	ch.Collection.Auth = js(m{"type": "digest", "digest": []m{{"key": "username", "value": "u"}, {"key": "password", "value": "pw-123456"}}})

	res := e.step(StepInput{Chain: ch, Source: SourceRunner, EnvPin: "api.example.com"})
	if res.Outcome != OutcomeBlocked || res.BlockReason != BlockPin {
		t.Fatalf("outcome %s/%s (%s)", res.Outcome, res.BlockReason, res.Error)
	}
	if n := api.count(); n != 0 {
		t.Fatalf("a pin-blocked request still sent %d digest probe(s) to the refused host", n)
	}

	// Control: with a matching pin the probe and the signed request both go out.
	res = e.step(StepInput{Chain: ch, Source: SourceRunner, EnvPin: "127.0.0.1"})
	if res.Outcome != OutcomeSent || api.count() != 2 {
		t.Fatalf("matching pin: outcome %s, %d requests (%s)", res.Outcome, api.count(), res.Error)
	}

	// Warn policy sends anyway and says so exactly once.
	before := api.count()
	res = e.step(StepInput{Chain: ch, Source: SourceRunner, EnvPin: "api.example.com", ScopePolicy: store.ScopePolicyWarn})
	warns := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "base target pin") {
			warns++
		}
	}
	if res.Outcome != OutcomeSent || api.count() != before+2 || warns != 1 {
		t.Fatalf("warn policy: outcome %s, %d new requests, %d pin warnings %v", res.Outcome, api.count()-before, warns, res.Warnings)
	}
}

// A secret with URL-special characters is percent-encoded on its way into the
// query or the path. The result must not show that form either.
func TestSecretIsMaskedInItsURLEncodedForms(t *testing.T) {
	const canary = "CANARY pass+word/Zq91"
	e := newEnv(t)
	api := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("127.0.0.1")
	it := store.Item{Method: "GET",
		URL:    js(m{"raw": api.srv.URL + "/u/:id/x", "variable": []m{{"key": "id", "value": "{{pw}}"}}}),
		Params: js([]m{{"key": "q", "value": "{{pw}}"}}),
	}
	res := e.step(StepInput{Chain: chainOf(it), Source: SourceRunner,
		Layers: []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"pw": canary}, "pw")}})
	if res.Outcome != OutcomeSent {
		t.Fatalf("outcome %s (%s)", res.Outcome, res.Error)
	}
	req, _ := api.last()
	// The params table escapes minimally (a typed '+' stays '+'), the path
	// variable is path-escaped: neither wire form equals the stored value.
	if !strings.Contains(req.URL.RawQuery, "Zq91") || !strings.Contains(req.URL.Path, canary) {
		t.Fatalf("secret did not reach the wire: %s", req.URL)
	}
	raw, _ := json.Marshal(res)
	for _, frag := range []string{"pass+word", "pass%2Bword", "Zq91"} {
		if strings.Contains(string(raw), frag) {
			t.Fatalf("encoded secret (%q) visible in the step result:\n%s", frag, raw)
		}
	}
}
