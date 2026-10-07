package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/store"
)

const canary = "CANARY-wp7-s3cr3t-9f2a41"

type collFixture struct {
	t      *testing.T
	h      *Hub
	st     *store.Store
	ts     *httptest.Server // control plane
	target *httptest.Server // example upstream (loopback)
}

func newCollFixture(t *testing.T) *collFixture {
	t.Helper()
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"path":%q,"token":%q}`, r.URL.Path, r.Header.Get("X-Token"))
	}))
	t.Cleanup(target.Close)
	return &collFixture{t: t, h: h, st: st, ts: ts, target: target}
}

type hdrs map[string]string

var (
	asUI = hdrs{"X-Interseptor-CSRF": "1"}
	asAI = hdrs{"X-Interseptor-Source": "ai", "X-Interseptor-CSRF": "1"}
)

func (f *collFixture) do(method, path string, body any, h hdrs) (int, string) {
	f.t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	case []byte:
		rdr = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			f.t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rdr)
	if err != nil {
		f.t.Fatal(err)
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func (f *collFixture) must(method, path string, body any, h hdrs, want int, into any) string {
	f.t.Helper()
	code, out := f.do(method, path, body, h)
	if code != want {
		f.t.Fatalf("%s %s = %d (want %d): %s", method, path, code, want, out)
	}
	if into != nil {
		if err := json.Unmarshal([]byte(out), into); err != nil {
			f.t.Fatalf("decode %s: %v: %s", path, err, out)
		}
	}
	return out
}

func (f *collFixture) importDemo() (collUID string) {
	f.t.Helper()
	doc := fmt.Sprintf(`{"info":{"name":"WP7 Demo","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
 "event":[{"listen":"test","script":{"exec":["pm.test('status ok', function(){ pm.response.to.have.status(200); });","pm.environment.set('seen','yes');"]}}],
 "variable":[{"key":"baseUrl","value":%q}],
 "item":[{"name":"Ping","request":{"method":"GET","header":[{"key":"X-Token","value":"{{tok}}"}],"url":{"raw":"{{baseUrl}}/ping"}}}]}`, f.target.URL)
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", doc, asUI, 201, &out)
	if out.CollectionUID == "" {
		f.t.Fatal("import returned no collection uid")
	}
	return out.CollectionUID
}

func (f *collFixture) firstRequest(collUID string) string {
	f.t.Helper()
	var tree collectionTree
	f.must("GET", "/api/collections/"+collUID, nil, asUI, 200, &tree)
	for _, it := range tree.Items {
		if it.Kind == "request" {
			return it.UID
		}
	}
	f.t.Fatal("no request item")
	return ""
}

func (f *collFixture) newEnv(secretValue string) string {
	f.t.Helper()
	var env envView
	f.must("POST", "/api/environments", map[string]any{"name": "dev", "kind": "env",
		"variables": []map[string]any{{"key": "tok", "type": "secret", "enabled": true}}}, asUI, 201, &env)
	f.must("PUT", "/api/variables/environment/"+env.UID+"/current", map[string]any{"key": "tok", "value": secretValue}, asUI, 200, nil)
	return env.UID
}

// ---- catalog / guard parity ---------------------------------------------------

func TestCollectionRoutesCatalogMatchesMux(t *testing.T) {
	f := newCollFixture(t)
	indexed := map[string]bool{}
	for _, r := range apiRoutes {
		indexed[r.Method+" "+r.Path] = true
	}
	for _, r := range collRoutes {
		if !indexed[r.method+" "+r.path] {
			t.Errorf("apiRoutes is missing %s %s", r.method, r.path)
		}
		if strings.TrimSpace(r.desc) == "" {
			t.Errorf("%s %s has no description", r.method, r.path)
		}
		req := httptest.NewRequest(r.method, concretePath(r.path), nil)
		if _, pattern := f.h.mux.Handler(req); pattern != r.method+" "+r.path {
			t.Errorf("mux pattern for %s %s = %q", r.method, r.path, pattern)
		}
	}
}

func concretePath(p string) string {
	for _, seg := range []string{"{uid}", "{id}", "{kind}"} {
		p = strings.ReplaceAll(p, seg, "x1")
	}
	return p
}

// A request with a foreign Host and no key must be refused exactly like the
// sibling endpoints (DNS rebinding / exposed port).
func TestCollectionRoutesRefuseForeignHost(t *testing.T) {
	f := newCollFixture(t)
	for _, r := range collRoutes {
		req, _ := http.NewRequest(r.method, f.ts.URL+concretePath(r.path), strings.NewReader("{}"))
		req.Host = "attacker.example.com"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with foreign Host = %d, want 401", r.method, r.path, resp.StatusCode)
		}
	}
}

func TestCollectionRoutesRejectCrossOrigin(t *testing.T) {
	f := newCollFixture(t)
	code, _ := f.do("POST", "/api/collections", `{"name":"x"}`, hdrs{"Origin": "https://evil.example.com"})
	if code != http.StatusForbidden {
		t.Fatalf("cross-origin create = %d, want 403", code)
	}
}

// ---- CRUD ---------------------------------------------------------------------

func TestCollectionItemExampleCRUD(t *testing.T) {
	f := newCollFixture(t)
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Demo"}, asUI, 201, &co)
	if co.ScopePolicy != store.ScopePolicyBlock {
		t.Fatalf("new collection policy = %q, want block", co.ScopePolicy)
	}
	var folder, req store.Item
	f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "folder", "name": "Users"}, asUI, 201, &folder)
	f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": "Get", "method": "GET",
		"parentUid": folder.UID, "url": "https://example.com/u"}, asUI, 201, &req)
	var moved store.Item
	req.ParentUID = ""
	f.must("PUT", "/api/items/"+req.UID, req, asUI, 200, &moved)
	if moved.ParentUID != "" || moved.Rev != req.Rev+1 {
		t.Fatalf("move: %+v", moved)
	}
	f.must("PUT", "/api/items/"+req.UID, store.Item{Rev: req.Rev, Name: "stale"}, asUI, 409, nil)

	var dup store.Item
	f.must("POST", "/api/items/"+req.UID+"/duplicate", nil, asUI, 201, &dup)
	if dup.UID == req.UID || dup.Name != "Get copy" {
		t.Fatalf("duplicate: %+v", dup)
	}
	f.must("POST", "/api/items/"+folder.UID+"/duplicate", nil, asUI, 400, nil)

	var ex map[string]json.RawMessage
	f.must("POST", "/api/items/"+req.UID+"/examples", map[string]any{"name": "ok", "code": 200}, asUI, 201, &ex)
	var exID string
	_ = json.Unmarshal(ex["id"], &exID)
	if exID == "" {
		t.Fatal("example has no id")
	}
	f.must("PUT", "/api/items/"+req.UID+"/examples/"+exID, map[string]any{"name": "renamed"}, asUI, 200, nil)
	var list struct {
		Examples []map[string]json.RawMessage `json:"examples"`
	}
	f.must("GET", "/api/items/"+req.UID+"/examples", nil, asUI, 200, &list)
	if len(list.Examples) != 1 || string(list.Examples[0]["name"]) != `"renamed"` {
		t.Fatalf("examples = %v", list.Examples)
	}
	f.must("DELETE", "/api/items/"+req.UID+"/examples/"+exID, nil, asUI, 204, nil)
	f.must("DELETE", "/api/items/"+req.UID+"/examples/"+exID, nil, asUI, 404, nil)

	f.must("DELETE", "/api/items/"+folder.UID, nil, asUI, 204, nil)
	f.must("DELETE", "/api/collections/"+co.UID, nil, asUI, 204, nil)
	f.must("GET", "/api/collections/"+co.UID, nil, asUI, 404, nil)
}

func TestEnvironmentVariablesNeverEchoSecrets(t *testing.T) {
	f := newCollFixture(t)
	envUID := f.newEnv(canary)
	body := f.must("GET", "/api/environments", nil, asUI, 200, nil)
	if strings.Contains(body, canary) {
		t.Fatalf("environment list leaked the secret: %s", body)
	}
	if !strings.Contains(body, `"hasCurrent":true`) {
		t.Fatalf("masked list should say a value exists: %s", body)
	}
	body = f.must("GET", "/api/environments/"+envUID+"?reveal=1", nil, asUI, 200, nil)
	if !strings.Contains(body, canary) {
		t.Fatalf("UI session reveal should show the value: %s", body)
	}
	for _, h := range []hdrs{asAI, {}} {
		if b := f.must("GET", "/api/environments/"+envUID+"?reveal=1", nil, h, 200, nil); strings.Contains(b, canary) {
			t.Fatalf("reveal must need an interactive UI session (hdrs %v): %s", h, b)
		}
	}
	out := f.must("PUT", "/api/variables/environment/"+envUID+"/current", map[string]any{"key": "tok", "value": canary}, asAI, 200, nil)
	if strings.Contains(out, canary) {
		t.Fatalf("set current echoed a secret: %s", out)
	}
	f.must("PUT", "/api/variables/environment/missing/current", map[string]any{"key": "k", "value": "v"}, asUI, 404, nil)
	f.must("PUT", "/api/variables/bogus/x/current", map[string]any{"key": "k", "value": "v"}, asUI, 400, nil)
}

// ---- import -------------------------------------------------------------------

func TestImportPreviewStoresNothingAndCommitQuarantines(t *testing.T) {
	f := newCollFixture(t)
	doc := `{"info":{"name":"Imp","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
 "event":[{"listen":"prerequest","script":{"exec":["pm.environment.set('a','b');"]}}],"item":[]}`
	var pv importPreview
	f.must("POST", "/api/import/collection/preview", doc, asUI, 200, &pv)
	if pv.Name != "Imp" || !pv.Quarantined || pv.Report.Scripts.Total != 1 {
		t.Fatalf("preview = %+v", pv)
	}
	var list struct{ Collections []store.Collection }
	f.must("GET", "/api/collections", nil, asUI, 200, &list)
	if len(list.Collections) != 0 {
		t.Fatalf("preview stored %d collections", len(list.Collections))
	}
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", doc, asUI, 201, &out)
	var sv scriptsView
	f.must("GET", "/api/collections/"+out.CollectionUID+"/scripts", nil, asUI, 200, &sv)
	if len(sv.Scripts) != 1 || sv.Scripts[0].Trusted || sv.Untrusted != 1 || len(sv.Capabilities) != 0 {
		t.Fatalf("imported scripts must be quarantined with no capabilities: %+v", sv)
	}
	f.must("POST", "/api/import/collection/commit?format=openapi", doc, asUI, 415, nil)
	f.must("POST", "/api/import/collection/commit?format=bogus", doc, asUI, 400, nil)
	f.must("POST", "/api/import/collection/preview", `{"nope":1}`, asUI, 415, nil)
	f.must("POST", "/api/import/collection/preview", `{{{`, asUI, 400, nil)
}

func TestImportCurlAndOpenAPIAreWired(t *testing.T) {
	f := newCollFixture(t)
	curlDoc := "curl -X POST https://api.example.com/v1/items -H 'Content-Type: application/json' -d '{\"a\":1}'"
	var pv importPreview
	f.must("POST", "/api/import/collection/preview", curlDoc, asUI, 200, &pv)
	if pv.Report.Format != "curl" || pv.Report.Stats.Requests != 1 {
		t.Fatalf("curl auto preview = %+v", pv)
	}
	var out struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit?format=curl", curlDoc, asUI, 201, &out)
	if out.CollectionUID == "" || f.firstRequest(out.CollectionUID) == "" {
		t.Fatalf("curl import stored no request: %+v", out)
	}
	oas := `{"openapi":"3.0.0","info":{"title":"Demo","version":"1"},"servers":[{"url":"https://api.example.com"}],
 "paths":{"/pets":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	for _, q := range []string{"", "?format=openapi"} {
		var pv2 importPreview
		f.must("POST", "/api/import/collection/preview"+q, oas, asUI, 200, &pv2)
		if pv2.Report.Format != "openapi" || pv2.Name != "Demo" {
			t.Fatalf("openapi preview%s = %+v", q, pv2)
		}
	}
	yml := "openapi: 3.0.0\ninfo:\n  title: Yml\n  version: '1'\npaths:\n  /a:\n    get:\n      responses:\n        '200':\n          description: ok\n"
	var out2 struct {
		CollectionUID string `json:"collectionUid"`
	}
	f.must("POST", "/api/import/collection/commit", yml, asUI, 201, &out2)
	if out2.CollectionUID == "" {
		t.Fatal("yaml openapi import stored nothing")
	}
}

// ---- send / run / scripts / trust ---------------------------------------------

func TestSendRunsTrustedScriptsEndToEnd(t *testing.T) {
	f := newCollFixture(t)
	collUID := f.importDemo()
	itemUID := f.firstRequest(collUID)
	envUID := f.newEnv(canary)

	// Quarantined: the request still goes out, the script does not run.
	var res collexecResult
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": itemUID, "envUid": envUID}, asUI, 200, &res)
	if res.Outcome != "sent" || res.FlowID == 0 {
		t.Fatalf("send = %+v", res)
	}
	if len(res.Tests) != 0 || len(res.Scripts) == 0 || !strings.Contains(res.Scripts[0].Reason, "quarantined") {
		t.Fatalf("scripts must be quarantined before approval: %+v", res)
	}

	// Approve through the UI session with explicit capabilities.
	var sv scriptsView
	f.must("GET", "/api/collections/"+collUID+"/scripts", nil, asUI, 200, &sv)
	f.must("POST", "/api/collections/"+collUID+"/trust", map[string]any{"confirm": true, "all": true,
		"capabilities": []string{CapVarsRead, CapVarsWrite}}, asUI, 200, nil)

	res = collexecResult{}
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": itemUID, "envUid": envUID}, asUI, 200, &res)
	if res.Outcome != "sent" || len(res.Tests) != 1 || res.Tests[0].Status != "pass" {
		t.Fatalf("trusted script should run and pass: %+v", res)
	}
	// persist defaults to keep for a UI send: pm.environment.set landed as a current value.
	var vars struct{ Variables []varView }
	f.must("GET", "/api/variables/environment/"+envUID, nil, asUI, 200, &vars)
	seen := false
	for _, v := range vars.Variables {
		seen = seen || (v.Key == "seen" && v.Current == "yes")
	}
	if !seen {
		t.Fatalf("script variable write was not persisted: %+v", vars.Variables)
	}
	// The secret reached the wire (flows stay truthful) but never the result.
	raw := f.must("POST", "/api/collections/send", map[string]any{"itemUid": itemUID, "envUid": envUID}, asUI, 200, nil)
	if strings.Contains(raw, canary) {
		t.Fatalf("step result leaked the secret: %s", raw)
	}
	fl, err := f.st.GetFlow(res.FlowID)
	if err != nil || fl.Flags&store.FlagCollection == 0 {
		t.Fatalf("flow %d should be flagged collection: %+v %v", res.FlowID, fl, err)
	}
	if got := fl.ReqHeaders["X-Token"]; len(got) != 1 || got[0] != canary {
		t.Fatalf("wire request header = %v", got)
	}
}

type collexecResult struct {
	Outcome string `json:"outcome"`
	FlowID  int64  `json:"flowId"`
	Tests   []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"tests"`
	Scripts []struct {
		Reason string `json:"reason"`
	} `json:"scripts"`
	Unresolved  []string `json:"unresolved"`
	BlockReason string   `json:"blockReason"`
}

func TestUnresolvedVariableBlocksSend(t *testing.T) {
	f := newCollFixture(t)
	itemUID := f.firstRequest(f.importDemo())
	var res collexecResult
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": itemUID}, asUI, 200, &res)
	if res.Outcome != "blocked" || res.BlockReason != "unresolved_variables" || len(res.Unresolved) == 0 {
		t.Fatalf("unresolved {{tok}} must block: %+v", res)
	}
}

func TestMCPSourceUsesBlockPolicyAndCannotOverride(t *testing.T) {
	f := newCollFixture(t)
	collUID := f.importDemo()
	itemUID := f.firstRequest(collUID)
	envUID := f.newEnv("tok-value-123456")

	// The loopback target is not explicitly in scope: block refuses it.
	var res collexecResult
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": itemUID, "envUid": envUID}, asAI, 200, &res)
	if res.Outcome == "sent" {
		t.Fatalf("AI-source send to an unscoped loopback host must not go out: %+v", res)
	}
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": itemUID, "envUid": envUID, "scopePolicy": "off"}, asAI, 400, nil)

	// With the host explicitly in scope the same call is allowed.
	u, _ := url.Parse(f.target.URL)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})
	res = collexecResult{}
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": itemUID, "envUid": envUID}, asAI, 200, &res)
	if res.Outcome != "sent" {
		t.Fatalf("in-scope AI send should go out: %+v", res)
	}
	fl, _ := f.st.GetFlow(res.FlowID)
	if fl == nil || fl.Flags&store.FlagAI == 0 || fl.Flags&store.FlagCollection == 0 {
		t.Fatalf("AI collection flow flags = %+v", fl)
	}
}

func TestRunCollectionSummaryAndBounds(t *testing.T) {
	f := newCollFixture(t)
	collUID := f.importDemo()
	envUID := f.newEnv("tok-value-123456")
	u, _ := url.Parse(f.target.URL)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})

	var out struct {
		RunUID  string `json:"runUid"`
		Status  string `json:"status"`
		Summary struct {
			Total      int `json:"total"`
			Quarantine int `json:"quarantinedScripts"`
		} `json:"summary"`
		Results []runRow `json:"results"`
	}
	f.must("POST", "/api/collections/run", map[string]any{"collectionUid": collUID, "envUid": envUID}, asAI, 200, &out)
	if out.Summary.Total != 1 || out.Results[0].Outcome != "sent" || out.Summary.Quarantine != 1 || out.Status != "done" {
		t.Fatalf("run = %+v", out)
	}
	var rows struct{ Results []store.CollRunResult }
	f.must("GET", "/api/runs/"+out.RunUID, nil, asUI, 200, &rows)
	if len(rows.Results) != 1 {
		t.Fatalf("persisted run rows = %d", len(rows.Results))
	}

	f.must("POST", "/api/collections/run", map[string]any{"collectionUid": collUID, "bail": "sometimes"}, asUI, 400, nil)
	f.must("POST", "/api/collections/run", map[string]any{"collectionUid": collUID, "delayMs": 999999}, asUI, 400, nil)
	f.must("POST", "/api/collections/run", map[string]any{"collectionUid": collUID, "persist": "forever"}, asUI, 400, nil)
	f.must("POST", "/api/collections/run", map[string]any{"collectionUid": collUID, "itemUids": []string{"nope"}}, asUI, 400, nil)
	f.must("POST", "/api/collections/run", map[string]any{"collectionUid": "missing"}, asUI, 404, nil)
}

// ---- trust: the AI can never trust ---------------------------------------------

func TestAISourceCannotTrustOrGrantCapabilities(t *testing.T) {
	f := newCollFixture(t)
	collUID := f.importDemo()
	trust := "/api/collections/" + collUID + "/trust"
	body := map[string]any{"confirm": true, "all": true, "capabilities": []string{CapNetSend}}

	for name, h := range map[string]hdrs{
		"ai source":          asAI,
		"ai source, no csrf": {"X-Interseptor-Source": "ai"},
		"other source":       {"X-Interseptor-Source": "cli", "X-Interseptor-CSRF": "1"},
		"no csrf marker":     {},
	} {
		code, out := f.do("POST", trust, body, h)
		if code != http.StatusForbidden {
			t.Errorf("%s: trust = %d (%s), want 403", name, code, out)
		}
		code, _ = f.do("POST", trust+"/revoke", map[string]any{"all": true}, h)
		if code != http.StatusForbidden {
			t.Errorf("%s: revoke = %d, want 403", name, code)
		}
	}
	var sv scriptsView
	f.must("GET", "/api/collections/"+collUID+"/scripts", nil, asUI, 200, &sv)
	if sv.Untrusted != len(sv.Scripts) || len(sv.Capabilities) != 0 {
		t.Fatalf("a refused trust call changed state: %+v", sv)
	}

	// An API key (bearer) is an agent credential: it can never approve either.
	r := httptest.NewRequest("POST", trust, nil)
	r.Header.Set("Authorization", "Bearer ick_example")
	r.Header.Set("X-Interseptor-CSRF", "1")
	if requireUISession(r) == nil {
		t.Fatal("bearer-key callers must not pass requireUISession")
	}

	// The UI session still can, and only for grantable capabilities.
	f.must("POST", trust, map[string]any{"confirm": true, "all": true, "capabilities": []string{"net.outOfScope"}}, asUI, 400, nil)
	f.must("POST", trust, map[string]any{"all": true}, asUI, 400, nil) // confirm required
	f.must("POST", trust, map[string]any{"confirm": true, "hashes": []string{"deadbeef"}}, asUI, 400, nil)
	f.must("POST", trust, body, asUI, 200, nil)
	f.must("GET", "/api/collections/"+collUID+"/scripts", nil, asUI, 200, &sv)
	if sv.Untrusted != 0 || len(sv.Capabilities) != 1 {
		t.Fatalf("UI trust should approve: %+v", sv)
	}
}

func TestAICannotWidenScopePolicyOrWriteCaps(t *testing.T) {
	f := newCollFixture(t)
	f.must("POST", "/api/collections", map[string]any{"name": "x", "scopePolicy": "off"}, asAI, 403, nil)
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "x"}, asAI, 201, &co)
	f.must("PUT", "/api/collections/"+co.UID, map[string]any{"name": "x", "scopePolicy": "warn"}, asAI, 403, nil)
	// Caps in a body are ignored: they have no input field at all.
	var upd store.Collection
	f.must("PUT", "/api/collections/"+co.UID, `{"name":"x","caps":["net.send"]}`, asAI, 200, &upd)
	if len(upd.Caps) != 0 {
		t.Fatalf("caps leaked through update: %s", upd.Caps)
	}
}

func TestAutoTrustOnlyForNewScriptsFromUISession(t *testing.T) {
	f := newCollFixture(t)
	collUID := f.importDemo()
	itemUID := f.firstRequest(collUID)
	var it store.Item
	f.must("GET", "/api/items/"+itemUID, nil, asUI, 200, &it)

	// Renaming an item must not approve the imported (collection-level) script.
	it.Name = "Renamed"
	f.must("PUT", "/api/items/"+itemUID, it, asUI, 200, nil)
	var sv scriptsView
	f.must("GET", "/api/collections/"+collUID+"/scripts", nil, asUI, 200, &sv)
	if sv.Untrusted != len(sv.Scripts) || len(sv.Scripts) == 0 {
		t.Fatalf("rename approved imported scripts: %+v", sv)
	}

	ev := json.RawMessage(`[{"listen":"test","script":{"exec":["pm.test('mine', function(){});"]}}]`)
	it.Events, it.Rev = ev, 0
	f.must("PUT", "/api/items/"+itemUID, it, asAI, 200, nil)
	f.must("GET", "/api/collections/"+collUID+"/scripts", nil, asUI, 200, &sv)
	for _, s := range sv.Scripts {
		if s.Trusted {
			t.Fatalf("an AI-written script was auto-trusted: %+v", s)
		}
	}
	it.Events = json.RawMessage(`[{"listen":"test","script":{"exec":["pm.test('human', function(){});"]}}]`)
	f.must("PUT", "/api/items/"+itemUID, it, asUI, 200, nil)
	f.must("GET", "/api/collections/"+collUID+"/scripts", nil, asUI, 200, &sv)
	humanTrusted := false
	for _, s := range sv.Scripts {
		for _, o := range s.Owners {
			if o == itemUID && s.Trusted {
				humanTrusted = true
			}
		}
	}
	if !humanTrusted {
		t.Fatalf("a script a human typed in the UI should be auto-trusted: %+v", sv)
	}
}

// ---- MCP ----------------------------------------------------------------------

func TestMCPToolsExposeNoTrustAndScrubSecrets(t *testing.T) {
	f := newCollFixture(t)
	// A collection with a literal bearer token and a secret variable.
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Secrets",
		"auth": map[string]any{"type": "bearer", "bearer": []map[string]any{{"key": "token", "value": canary}}}}, asUI, 201, &co)
	envUID := f.newEnv(canary)

	srv := mcp.New(f.ts.URL)
	for _, name := range srv.ToolNames() {
		if strings.Contains(strings.ToLower(name), "trust") {
			t.Errorf("MCP must not expose a trust tool: %s", name)
		}
	}
	human := f.must("GET", "/api/collections/"+co.UID, nil, asUI, 200, nil)
	if !strings.Contains(human, canary) {
		t.Fatalf("the human UI must still see its own auth: %s", human)
	}
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"list_collections", map[string]any{}},
		{"get_collection", map[string]any{"collectionUid": co.UID}},
		{"script_approval_status", map[string]any{"collectionUid": co.UID}},
		{"set_variable", map[string]any{"ownerUid": envUID, "key": "tok", "value": canary}},
	} {
		out, err := srv.Call(call.tool, call.args)
		if err != nil {
			t.Fatalf("%s: %v", call.tool, err)
		}
		if strings.Contains(out, canary) {
			t.Errorf("%s leaked the canary: %s", call.tool, out)
		}
	}
}

func TestMCPRunRequestEndToEnd(t *testing.T) {
	f := newCollFixture(t)
	collUID := f.importDemo()
	itemUID := f.firstRequest(collUID)
	envUID := f.newEnv(canary)
	u, _ := url.Parse(f.target.URL)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})

	srv := mcp.New(f.ts.URL)
	out, err := srv.Call("run_request", map[string]any{"itemUid": itemUID, "envUid": envUID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"outcome":"sent"`) || strings.Contains(out, canary) {
		t.Fatalf("run_request output: %s", out)
	}
	out, err = srv.Call("run_collection", map[string]any{"collectionUid": collUID, "envUid": envUID})
	if err != nil || !strings.Contains(out, `"quarantinedScripts":1`) || strings.Contains(out, canary) {
		t.Fatalf("run_collection: %v %s", err, out)
	}
	if _, err := srv.Call("run_request", map[string]any{}); err == nil {
		t.Fatal("missing itemUid should fail")
	}
}

// ---- bounds -------------------------------------------------------------------

func TestCollectionHandlersAreBounded(t *testing.T) {
	f := newCollFixture(t)
	old := maxCollectionSmallBytes
	maxCollectionSmallBytes = 256
	t.Cleanup(func() { maxCollectionSmallBytes = old })
	oldJSON := maxCollectionJSONBytes
	maxCollectionJSONBytes = 256
	t.Cleanup(func() { maxCollectionJSONBytes = oldJSON })
	oldImp := maxCollectionImportBytes
	maxCollectionImportBytes = 512
	t.Cleanup(func() { maxCollectionImportBytes = oldImp })

	pad := strings.Repeat(" ", 600)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/collections"},
		{"POST", "/api/collections/send"},
		{"POST", "/api/collections/run"},
		{"POST", "/api/variables/resolve"},
		{"POST", "/api/environments"},
		{"POST", "/api/collections/x1/trust"},
	} {
		// A valid value followed by padding past the cap must still be a 413.
		if code, out := f.do(c.method, c.path, `{"name":"x"}`+pad, asUI); code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s with padding = %d (%s), want 413", c.method, c.path, code, out)
		}
		if code, _ := f.do(c.method, c.path, `{"a":1}{"b":2}`, asUI); code != http.StatusBadRequest {
			t.Errorf("%s %s with two JSON values = %d, want 400", c.method, c.path, code)
		}
	}
	for _, p := range []string{"/api/import/collection/preview", "/api/import/collection/commit"} {
		if code, _ := f.do("POST", p, strings.Repeat("a", 600), asUI); code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s oversized = %d, want 413", p, code)
		}
	}
	// Exactly at the limit is accepted by the reader (then rejected as not Postman).
	if code, _ := f.do("POST", "/api/import/collection/preview", strings.Repeat("a", 512), asUI); code != http.StatusBadRequest && code != http.StatusUnsupportedMediaType {
		t.Errorf("limit-sized body = %d, want a parse error, not 413", code)
	}
}

func TestRunItemCountIsBounded(t *testing.T) {
	f := newCollFixture(t)
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Many"}, asUI, 201, &co)
	for i := 0; i < 3; i++ {
		f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": fmt.Sprint("r", i),
			"method": "GET", "url": "https://example.com/" + fmt.Sprint(i)}, asUI, 201, nil)
	}
	code, out := f.do("POST", "/api/collections/run", map[string]any{"collectionUid": co.UID, "maxItems": 2}, asUI)
	if code != http.StatusBadRequest || !strings.Contains(out, "item limit") {
		t.Fatalf("over-limit run = %d %s", code, out)
	}
}
