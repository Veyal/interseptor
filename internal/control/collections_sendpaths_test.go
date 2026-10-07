package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

// Every way a collection can put bytes on the network goes through the one
// collexec Step pipeline, so the scope policy and the dial-time destination
// guard apply identically. This test enumerates the send paths reachable from
// the control server and asserts that each of them is stopped by the same
// rules, and (to prove it is not vacuous) reaches the host once it is in scope.

type sendPath struct {
	name string
	// run triggers the path against a collection whose requests/scripts/auth
	// point at target; it returns once the work has finished.
	run func(f *collFixture, target string, ownURL string)
}

func (f *collFixture) pathCollection(target string, withScriptSend, withOAuth bool) (collUID, itemUID, envUID string) {
	return f.pathCollectionAt(target, target, withScriptSend, withOAuth)
}

// pathCollectionAt separates the main request's base URL from the OAuth2 token
// endpoint's, so a path can be tested against the token destination alone.
func (f *collFixture) pathCollectionAt(target, tokenBase string, withScriptSend, withOAuth bool) (collUID, itemUID, envUID string) {
	f.t.Helper()
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Paths"}, asUI, 201, &co)
	item := map[string]any{"kind": "request", "name": "main", "method": "GET", "url": target + "/main"}
	if withScriptSend {
		// The main request is skipped; only the script's pm.sendRequest may send.
		script := "pm.execution.skipRequest();\npm.sendRequest('" + target + "/from-script', function () {});"
		ev, _ := json.Marshal([]map[string]any{{"listen": "prerequest", "script": map[string]any{"exec": strings.Split(script, "\n")}}})
		item["events"] = json.RawMessage(ev)
	}
	var req store.Item
	f.must("POST", "/api/collections/"+co.UID+"/items", item, asUI, 201, &req)
	if withScriptSend {
		f.must("POST", "/api/collections/"+co.UID+"/trust", map[string]any{"confirm": true, "all": true,
			"capabilities": []string{CapVarsRead, CapNetSend}}, asUI, 200, nil)
	}
	if withOAuth {
		auth := `{"type":"oauth2","oauth2":[{"key":"grant_type","value":"client_credentials"},{"key":"accessTokenUrl","value":"` + tokenBase + `/token"},` +
			`{"key":"clientId","value":"cid"},{"key":"clientSecret","value":"csecret-value-123456"}]}`
		var cur collectionTree
		f.must("GET", "/api/collections/"+co.UID, nil, asUI, 200, &cur)
		f.must("PUT", "/api/collections/"+co.UID, map[string]any{"name": "Paths", "rev": cur.Collection.Rev, "auth": json.RawMessage(auth)}, asUI, 200, nil)
	}
	return co.UID, req.UID, f.newEnv("tok-value-123456")
}

func TestEverySendPathAppliesTheSameScopeAndDialGuard(t *testing.T) {
	paths := []sendPath{
		{"interactive send, scope policy block", func(f *collFixture, target, own string) {
			_, item, env := f.pathCollection(target, false, false)
			f.do("POST", "/api/collections/send", map[string]any{"itemUid": item, "envUid": env, "scopePolicy": "block"}, asUI)
		}},
		{"MCP/AI send", func(f *collFixture, target, own string) {
			_, item, env := f.pathCollection(target, false, false)
			f.do("POST", "/api/collections/send", map[string]any{"itemUid": item, "envUid": env}, asAI)
		}},
		{"synchronous run", func(f *collFixture, target, own string) {
			coll, _, env := f.pathCollection(target, false, false)
			f.do("POST", "/api/collections/run", map[string]any{"collectionUid": coll, "envUid": env}, asUI)
		}},
		{"asynchronous runner", func(f *collFixture, target, own string) {
			coll, _, env := f.pathCollection(target, false, false)
			var start struct {
				RunUID string `json:"runUid"`
			}
			f.must("POST", "/api/runner/runs", map[string]any{"collectionUid": coll, "envUid": env, "persist": "discard"}, asUI, 202, &start)
			f.waitRun(start.RunUID, func(p collrun.Progress) bool { return p.Finished })
		}},
		{"pm.sendRequest from a script", func(f *collFixture, target, own string) {
			_, item, env := f.pathCollection(target, true, false)
			f.do("POST", "/api/collections/send", map[string]any{"itemUid": item, "envUid": env, "scopePolicy": "block"}, asUI)
		}},
		{"oauth2 token endpoint", func(f *collFixture, target, own string) {
			// The API host is irrelevant: the token is fetched first, from the
			// out-of-scope target, and that fetch must be refused.
			_, item, env := f.pathCollectionAt("https://somewhere.example.com", target, false, true)
			f.do("POST", "/api/collections/send", map[string]any{"itemUid": item, "envUid": env, "scopePolicy": "block"}, asUI)
		}},
	}

	for _, p := range paths {
		t.Run(p.name+"/out of scope is never dialled", func(t *testing.T) {
			f := newCollFixture(t)
			var hits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"access_token":"canary-access-AAAA1111","token_type":"Bearer","expires_in":3600}`))
			}))
			defer target.Close()
			// Scope is explicit and does not list the loopback target.
			f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: "somewhere.example.com"}})
			p.run(f, target.URL, f.ts.URL)
			if hits.Load() != 0 {
				t.Fatalf("%s reached an out-of-scope loopback host %d time(s)", p.name, hits.Load())
			}
		})
	}
	for _, p := range paths[:5] {
		t.Run(p.name+"/in scope reaches the host", func(t *testing.T) {
			f := newCollFixture(t)
			var hits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
			defer target.Close()
			u, _ := url.Parse(target.URL)
			f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})
			p.run(f, target.URL, f.ts.URL)
			if hits.Load() == 0 {
				t.Fatalf("%s did not reach an explicitly scoped host: the guard test would be vacuous", p.name)
			}
		})
	}
}

// Interseptor's own listeners are refused by every path whatever the policy.
func TestEverySendPathRefusesTheOwnListener(t *testing.T) {
	f := newCollFixture(t)
	cu, _ := url.Parse(f.ts.URL)
	f.h.SetSelfAddr(cu.Host)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: cu.Hostname()}})
	own := f.ts.URL
	coll, item, env := f.pathCollection(own, false, false)

	var res collexecResult
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": item, "envUid": env, "scopePolicy": "off"}, asUI, 200, &res)
	if res.Outcome != "blocked" || res.BlockReason != "own_listener" {
		t.Fatalf("interactive send to the control port with policy off = %+v", res)
	}
	var run struct {
		Results []runRow `json:"results"`
	}
	f.must("POST", "/api/collections/run", map[string]any{"collectionUid": coll, "envUid": env, "scopePolicy": "off"}, asUI, 200, &run)
	if len(run.Results) != 1 || run.Results[0].Outcome != "blocked" {
		t.Fatalf("run against the control port = %+v", run.Results)
	}
}
