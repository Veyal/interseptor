package collexec

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/collauth"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

const authCanary = "CANARY-auth-secret-5d2e8b"

func authPipe(e *env, now func() time.Time) *collauth.Manager {
	mgr := collauth.New(collauth.Options{Doer: StepDoer{}, Now: now})
	e.pipe.Auth = mgr
	e.pipe.Registry = mgr.Secrets()
	return mgr
}

func tokenServer(t *testing.T, calls *atomic.Int32, expiresIn int) *recorder {
	return newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		out := map[string]any{"access_token": "tok-" + string(rune('0'+n)), "token_type": "Bearer", "expires_in": expiresIn}
		if r.FormValue("grant_type") != "refresh_token" {
			out["refresh_token"] = "refresh-1"
		}
		json.NewEncoder(w).Encode(out)
	})
}

func oauthChain(api, tokenURL string) Chain {
	ch := chainOf(store.Item{Method: "GET", URL: js(api + "/data")})
	ch.Collection.Auth = js(m{"type": "oauth2", "oauth2": []m{
		{"key": "grant_type", "value": "client_credentials"}, {"key": "accessTokenUrl", "value": tokenURL},
		{"key": "clientId", "value": "cid"}, {"key": "clientSecret", "value": "{{secret}}"},
	}})
	return ch
}

// OAuth2 runs through the pipeline: the token request goes out through the
// scope-guarded sender and is captured, the token is cached across steps and
// refreshed when it expires, and no secret reaches the result.
func TestOAuth2TokenFetchCacheAndRefreshThroughPipeline(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	idp := tokenServer(t, &calls, 60)
	api := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("127.0.0.1")
	var mu atomic.Int64
	mu.Store(time.Unix(1700000000, 0).UnixNano())
	authPipe(e, func() time.Time { return time.Unix(0, mu.Load()) })
	in := StepInput{Chain: oauthChain(api.srv.URL, idp.srv.URL+"/token"), Source: SourceRunner,
		Layers: []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"secret": authCanary}, "secret")}}

	res := e.step(in)
	if res.Outcome != OutcomeSent {
		t.Fatalf("outcome %+v", res)
	}
	if req, _ := api.last(); req.Header.Get("Authorization") != "Bearer tok-1" {
		t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
	}
	if !contains(res.Applied, "auth:oauth2") {
		t.Fatalf("applied %v", res.Applied)
	}
	// The token request was a real, scope-guarded, captured collection flow.
	flows, _ := e.st.QueryFlowsListFilter(store.FlowFilter{RequireFlags: store.FlagCollection, Limit: 10})
	foundToken := false
	for _, f := range flows {
		foundToken = foundToken || f.Path == "/token"
	}
	if !foundToken {
		t.Fatal("token request was not captured as a collection flow")
	}

	e.step(in) // cached
	if calls.Load() != 1 {
		t.Fatalf("token fetched %d times, want 1 (cached)", calls.Load())
	}
	mu.Add(int64(2 * time.Minute)) // expire
	e.step(in)
	if calls.Load() != 2 {
		t.Fatalf("expected one refresh, token calls = %d", calls.Load())
	}
	if req, _ := api.last(); req.Header.Get("Authorization") != "Bearer tok-2" {
		t.Fatalf("after refresh authorization = %q", req.Header.Get("Authorization"))
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), authCanary) || strings.Contains(string(raw), "tok-1") {
		t.Fatalf("secret leaked into the step result: %s", raw)
	}
}

// The token endpoint is a destination like any other: out of scope under the
// block policy it is refused and the API call is never made.
func TestOAuth2TokenEndpointObeysScopePolicy(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	idp := tokenServer(t, &calls, 60)
	api := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("somewhere.example.com")
	authPipe(e, nil)
	res := e.step(StepInput{Chain: oauthChain(api.srv.URL, idp.srv.URL+"/token"), Source: SourceRunner, ScopePolicy: store.ScopePolicyOff,
		Layers: []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"secret": authCanary}, "secret")}})
	// policy off lets it through; now block it.
	if res.Outcome != OutcomeSent {
		t.Fatalf("policy off should send: %+v", res)
	}
	calls.Store(0)
	e.pipe.Auth = collauth.New(collauth.Options{Doer: StepDoer{}}) // fresh token cache
	res = e.step(StepInput{Chain: oauthChain(api.srv.URL, idp.srv.URL+"/token"), Source: SourceRunner, ScopePolicy: store.ScopePolicyBlock,
		Layers: []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"secret": authCanary}, "secret")}})
	if res.Outcome == OutcomeSent || calls.Load() != 0 {
		t.Fatalf("out-of-scope token endpoint must not be called: %+v calls=%d", res, calls.Load())
	}
	if strings.Contains(res.Error, authCanary) {
		t.Fatalf("secret in error: %s", res.Error)
	}
}

// Every auth type reaches the real suite, not the "not supported yet" warning.
func TestAuthTypesBeyondBasicUseTheSuite(t *testing.T) {
	cases := []struct {
		name string
		auth any
		want func(h http.Header) bool
	}{
		{"awsv4", m{"type": "awsv4", "awsv4": []m{{"key": "accessKey", "value": "AKIDEXAMPLE"}, {"key": "secretKey", "value": "{{secret}}"},
			{"key": "region", "value": "us-east-1"}, {"key": "service", "value": "execute-api"}}},
			func(h http.Header) bool {
				return strings.HasPrefix(h.Get("Authorization"), "AWS4-HMAC-SHA256") && h.Get("X-Amz-Date") != ""
			}},
		{"jwt", m{"type": "jwt", "jwt": []m{{"key": "algorithm", "value": "HS256"}, {"key": "secret", "value": "{{secret}}"},
			{"key": "payload", "value": `{"sub":"ann"}`}}},
			func(h http.Header) bool {
				return strings.Count(strings.TrimPrefix(h.Get("Authorization"), "Bearer "), ".") == 2
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			api := newRecorder(t, nil)
			e.pipe.Scope = scopeOf1("127.0.0.1")
			authPipe(e, nil)
			ch := chainOf(store.Item{Method: "GET", URL: js(api.srv.URL + "/x")})
			ch.Collection.Auth = js(c.auth)
			res := e.step(StepInput{Chain: ch, Source: SourceRunner,
				Layers: []varstore.Layer{layer(varstore.ScopeEnvironment, map[string]string{"secret": authCanary}, "secret")}})
			if res.Outcome != OutcomeSent {
				t.Fatalf("outcome %+v", res)
			}
			req, _ := api.last()
			if !c.want(req.Header) {
				t.Fatalf("%s not applied; headers %v warnings %v", c.name, req.Header, res.Warnings)
			}
			for _, w := range res.Warnings {
				if strings.Contains(w, "not supported") {
					t.Fatalf("unsupported warning: %s", w)
				}
			}
		})
	}
}

// An explicit Authorization header still wins; builtin types behave as before.
func TestAuthWithManagerKeepsExplicitHeaderPrecedence(t *testing.T) {
	e := newEnv(t)
	api := newRecorder(t, nil)
	e.pipe.Scope = scopeOf1("127.0.0.1")
	authPipe(e, nil)
	ch := chainOf(store.Item{Method: "GET", URL: js(api.srv.URL + "/x"),
		Headers: js([]m{{"key": "Authorization", "value": "Custom explicit"}})})
	ch.Collection.Auth = js(m{"type": "bearer", "bearer": m{"token": "from-auth"}})
	res := e.step(StepInput{Chain: ch, Source: SourceRunner})
	if req, _ := api.last(); res.Outcome != OutcomeSent || req.Header.Get("Authorization") != "Custom explicit" {
		t.Fatalf("explicit header lost: %v %+v", req.Header, res)
	}
}

func TestJarExportReplaceAndLoader(t *testing.T) {
	now := time.Unix(1700000000, 0)
	j := NewJar()
	u := mustURL(t, "https://api.example.com/a/b")
	j.Store(u, []*http.Cookie{{Name: "sid", Value: "1", Path: "/", Secure: true, HttpOnly: true}, {Name: "gone", Value: "x", MaxAge: -1}}, now)
	snap := j.Export()
	if len(snap) != 1 || snap[0].Name != "sid" || !snap[0].Secure || !snap[0].HTTPOnly {
		t.Fatalf("export = %+v", snap)
	}
	j.Store(u, []*http.Cookie{{Name: "later", Value: "2", Path: "/"}}, now)
	j.Replace(snap)
	if got := j.List(now); len(got) != 1 || got[0].Name != "sid" {
		t.Fatalf("replace must restore the snapshot: %+v", got)
	}
	if h := j.Header(u, now); !strings.Contains(h, "sid=1") {
		t.Fatalf("header after replace = %q", h)
	}

	var loads int
	js := &Jars{Loader: func(coll, env, identity string) []JarCookie {
		loads++
		return snap
	}}
	a := js.For("c", "e", "i")
	js.For("c", "e", "i")
	if loads != 1 || len(a.List(now)) != 1 {
		t.Fatalf("loader calls = %d cookies = %d", loads, len(a.List(now)))
	}
}
