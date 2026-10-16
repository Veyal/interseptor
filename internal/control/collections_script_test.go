package control

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A trusted script's pm.sendRequest goes through the same pipeline as every
// other send: in-scope hosts work and are captured, Interseptor's own
// listener is refused unconditionally.
func TestScriptSendRequestIsScopedAndRefusesOwnListener(t *testing.T) {
	f := newCollFixture(t)
	tu, _ := url.Parse(f.target.URL)
	cu, _ := url.Parse(f.ts.URL)
	f.h.SetSelfAddr(cu.Host)
	f.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: tu.Hostname()}})

	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Scripted"}, asUI, 201, &co)
	script := "pm.sendRequest('" + f.target.URL + "/nested', function (err, res) { pm.environment.set('nested', err ? 'err' : String(res.code)); });\n" +
		"pm.sendRequest('" + f.ts.URL + "/api/collections', function (err, res) { pm.environment.set('own', err ? 'blocked' : 'reached'); });"
	events, _ := json.Marshal([]map[string]any{{"listen": "test", "script": map[string]any{"exec": strings.Split(script, "\n")}}})
	var req store.Item
	f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": "Main", "method": "GET",
		"url": f.target.URL + "/main", "events": json.RawMessage(events)}, asUI, 201, &req)

	envUID := f.newEnv("tok-value-123456")
	// Without capabilities the script cannot send at all.
	var res collexecResult
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": req.UID, "envUid": envUID}, asUI, 200, &res)
	if res.Outcome != "sent" {
		t.Fatalf("main request: %+v", res)
	}
	var vars struct{ Variables []varView }
	f.must("GET", "/api/variables/environment/"+envUID, nil, asUI, 200, &vars)
	for _, v := range vars.Variables {
		if v.Key == "nested" || v.Key == "own" {
			t.Fatalf("a script with no granted capabilities wrote %s", v.Key)
		}
	}

	f.must("POST", "/api/collections/"+co.UID+"/trust", map[string]any{"confirm": true, "all": true,
		"capabilities": []string{CapVarsRead, CapVarsWrite, CapNetSend}}, asUI, 200, nil)
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": req.UID, "envUid": envUID}, asUI, 200, &res)
	f.must("GET", "/api/variables/environment/"+envUID, nil, asUI, 200, &vars)
	got := map[string]string{}
	for _, v := range vars.Variables {
		got[v.Key] = v.Current
	}
	if got["nested"] != "200" || got["own"] != "blocked" {
		t.Fatalf("script sends: nested=%q own=%q (all %v)", got["nested"], got["own"], got)
	}
	// Both nested sends were captured as collection flows (the refused one too, or not at all),
	// and the nested target request is a real flow.
	flows, err := f.st.QueryFlowsListFilter(store.FlowFilter{RequireFlags: store.FlagCollection, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	foundNested := false
	for _, fl := range flows {
		foundNested = foundNested || fl.Path == "/nested"
		if fl.Port == atoiOr(cu.Port(), 0) {
			t.Fatalf("a script reached the control listener: flow %d", fl.ID)
		}
	}
	if !foundNested {
		t.Fatal("the script's in-scope send was not captured as a collection flow")
	}
}

// Script failures are reported, never silently passed.
func TestScriptErrorsAndUnsupportedAreNotPasses(t *testing.T) {
	f := newCollFixture(t)
	var co store.Collection
	f.must("POST", "/api/collections", map[string]any{"name": "Bad"}, asUI, 201, &co)
	events, _ := json.Marshal([]map[string]any{{"listen": "test", "script": map[string]any{"exec": []string{
		"pm.test('boom', function(){ throw new Error('nope'); });",
		"const x = pm.visualizer.set('<p/>');",
	}}}})
	var req store.Item
	f.must("POST", "/api/collections/"+co.UID+"/items", map[string]any{"kind": "request", "name": "R", "method": "GET",
		"url": f.target.URL + "/x", "events": json.RawMessage(events)}, asUI, 201, &req)
	f.must("POST", "/api/collections/"+co.UID+"/trust", map[string]any{"confirm": true, "all": true,
		"capabilities": []string{CapVarsRead}}, asUI, 200, nil)
	var res collexecResult
	f.must("POST", "/api/collections/send", map[string]any{"itemUid": req.UID}, asUI, 200, &res)
	for _, tr := range res.Tests {
		if tr.Status == "pass" {
			t.Fatalf("a throwing/unsupported script must not read as pass: %+v", res.Tests)
		}
	}
	if len(res.Tests) == 0 {
		t.Fatalf("expected a failing or unsupported result, got none: %+v", res)
	}
}
