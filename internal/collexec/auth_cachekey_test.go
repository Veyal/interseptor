package collexec

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/varstore"
)

// Data-file iterations that resolve the same auth template ({{secret}}) to
// different credentials must not share one cached OAuth2 token.
func TestOAuth2TokenCacheKeyedOnResolvedCredentials(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	idp := tokenServer(t, &calls, 3600)
	api := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("127.0.0.1")
	authPipe(e, func() time.Time { return time.Unix(1700000000, 0) })
	ch := oauthChain(api.srv.URL, idp.srv.URL+"/token")
	stepAs := func(secret string) string {
		t.Helper()
		res := e.step(StepInput{Chain: ch, Source: SourceRunner,
			Layers: []varstore.Layer{layer(varstore.ScopeData, map[string]string{"secret": secret}, "secret")}})
		if res.Outcome != OutcomeSent {
			t.Fatalf("outcome %+v", res)
		}
		req, _ := api.last()
		return req.Header.Get("Authorization")
	}
	alice := stepAs("alice-secret")
	bob := stepAs("bob-secret")
	if alice == bob || calls.Load() != 2 {
		t.Fatalf("bob reused alice's token: alice=%q bob=%q token calls=%d", alice, bob, calls.Load())
	}
	if again := stepAs("alice-secret"); again != alice || calls.Load() != 2 {
		t.Fatalf("same credentials should hit the cache: %q vs %q, calls=%d", again, alice, calls.Load())
	}
}

// ResolveAuth (the authorization-code flow) stores the token under the key
// it returns; the step must look it up under the same key, which is derived
// from the resolved credentials rather than the template.
func TestResolveAuthKeyMatchesResolvedCredentials(t *testing.T) {
	ch := oauthChain("http://example.com", "http://example.com/token")
	in := func(secret string) StepInput {
		return StepInput{Chain: ch, EnvUID: "e", Identity: "i",
			Layers: []varstore.Layer{layer(varstore.ScopeData, map[string]string{"secret": secret}, "secret")}}
	}
	cfgA, keyA, err := ResolveAuth(in("alice-secret"), nil)
	if err != nil {
		t.Fatalf("ResolveAuth: %v", err)
	}
	_, keyB, err := ResolveAuth(in("bob-secret"), nil)
	if err != nil {
		t.Fatalf("ResolveAuth: %v", err)
	}
	if keyA == keyB {
		t.Fatal("different resolved credentials share one token cache key")
	}
	want := AuthCacheKey(ch.Collection.UID, "e", "i", AuthModel{Type: cfgA.Type, Fields: cfgA.Fields})
	if keyA != want {
		t.Fatalf("ResolveAuth key %q != key of resolved fields %q", keyA, want)
	}
}
