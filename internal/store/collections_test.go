package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func mustColl(t *testing.T, s *Store, c Collection) *Collection {
	t.Helper()
	out, err := s.CreateCollection(c)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	return out
}

func mustItem(t *testing.T, s *Store, it Item) *Item {
	t.Helper()
	out, err := s.CreateItem(it)
	if err != nil {
		t.Fatalf("CreateItem(%s): %v", it.Name, err)
	}
	return out
}

func TestNewUIDShapeAndOrder(t *testing.T) {
	a := NewUID()
	if len(a) != 26 {
		t.Fatalf("uid %q len %d", a, len(a))
	}
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		u := NewUID()
		if seen[u] {
			t.Fatal("duplicate uid")
		}
		seen[u] = true
		for _, c := range u {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z') || c == 'I' || c == 'L' || c == 'O' || c == 'U' {
				t.Fatalf("bad char in %q", u)
			}
		}
	}
}

func TestRankBetween(t *testing.T) {
	cases := [][2]string{{"", ""}, {"a", ""}, {"", "a"}, {"a", "b"}, {"a", "a1"}, {"az", "b"}, {"m", "n"}, {"0", "1"}}
	for _, c := range cases {
		r := RankBetween(c[0], c[1])
		if r == "" || (c[0] != "" && r <= c[0]) || (c[1] != "" && r >= c[1]) {
			t.Errorf("RankBetween(%q,%q)=%q not strictly between", c[0], c[1], r)
		}
	}
	// repeated insertion at the front and in the middle never collides.
	lo, hi := "a", "b"
	for i := 0; i < 40; i++ {
		m := RankBetween(lo, hi)
		if !(lo < m && m < hi) {
			t.Fatalf("step %d: %q not in (%q,%q)", i, m, lo, hi)
		}
		hi = m
	}
	last := ""
	for i := 0; i < 200; i++ {
		r := RankBetween(last, "")
		if r <= last {
			t.Fatalf("append not increasing: %q after %q", r, last)
		}
		last = r
	}
}

func TestCollectionRoundTrip(t *testing.T) {
	s := newTestStore(t)
	in := Collection{Name: "API", Description: "d", Auth: json.RawMessage(`{"type":"bearer"}`),
		Events:   json.RawMessage(`[{"listen":"prerequest","script":{"exec":["a","b"]}}]`),
		Settings: json.RawMessage(`{"x":1}`), Caps: json.RawMessage(`{"net.send":true}`), ScopePolicy: "warn",
		Sidecar: json.RawMessage(`{"postman":{"id":"1"}}`), KeyOrder: json.RawMessage(`["info","item"]`),
		ImportReport: json.RawMessage(`{"warnings":[]}`)}
	c := mustColl(t, s, in)
	if c.UID == "" || c.Rev != 1 {
		t.Fatalf("created %+v", c)
	}
	got, err := s.GetCollection(c.UID)
	if err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]string{
		"auth": {string(in.Auth), string(got.Auth)}, "events": {string(in.Events), string(got.Events)},
		"settings": {string(in.Settings), string(got.Settings)}, "caps": {string(in.Caps), string(got.Caps)},
		"sidecar": {string(in.Sidecar), string(got.Sidecar)}, "keyOrder": {string(in.KeyOrder), string(got.KeyOrder)},
		"report": {string(in.ImportReport), string(got.ImportReport)},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: %q != %q", name, pair[0], pair[1])
		}
	}
	if got.Name != "API" || got.Description != "d" || got.ScopePolicy != "warn" {
		t.Fatalf("scalar mismatch %+v", got)
	}
	got.Name = "API2"
	up, err := s.UpdateCollection(*got)
	if err != nil || up.Rev != 2 {
		t.Fatalf("update: %v %+v", err, up)
	}
	if _, err := s.UpdateCollection(*got); !errors.Is(err, ErrCollConflict) { // stale rev 1
		t.Fatalf("stale update err=%v", err)
	}
	list, _ := s.ListCollections()
	if len(list) != 1 || list[0].Name != "API2" {
		t.Fatalf("list %+v", list)
	}
	if err := s.DeleteCollection(c.UID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCollection(c.UID); !errors.Is(err, ErrCollNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestCollectionDefaultsAndValidation(t *testing.T) {
	s := newTestStore(t)
	c := mustColl(t, s, Collection{Name: "x"})
	if c.ScopePolicy != "block" {
		t.Fatalf("default policy %q", c.ScopePolicy)
	}
	if _, err := s.CreateCollection(Collection{Name: ""}); !errors.Is(err, ErrCollInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := s.CreateCollection(Collection{Name: "y", ScopePolicy: "bogus"}); !errors.Is(err, ErrCollInvalid) {
		t.Fatalf("bad policy: %v", err)
	}
	if _, err := s.CreateCollection(Collection{Name: "z", Auth: json.RawMessage(`{bad`)}); !errors.Is(err, ErrCollInvalid) {
		t.Fatalf("bad json: %v", err)
	}
}

func TestItemTreeRoundTripOrderMoveAndCycle(t *testing.T) {
	s := newTestStore(t)
	c := mustColl(t, s, Collection{Name: "c"})
	f := mustItem(t, s, Item{CollectionUID: c.UID, Kind: "folder", Name: "F"})
	sub := mustItem(t, s, Item{CollectionUID: c.UID, ParentUID: f.UID, Kind: "folder", Name: "Sub"})
	r1 := mustItem(t, s, Item{CollectionUID: c.UID, ParentUID: f.UID, Kind: "request", Name: "R1", Method: "POST",
		URL:     json.RawMessage(`{"raw":"https://example.com/{{p}}"}`),
		Headers: json.RawMessage(`[{"key":"X-A","value":"1"},{"key":"X-A","value":"2"}]`),
		Params:  json.RawMessage(`[]`), Body: json.RawMessage(`{"mode":"raw","raw":"{}"}`),
		Auth: json.RawMessage(`{"type":"noauth"}`), Events: json.RawMessage(`[{"listen":"test","script":{"exec":["x"]}}]`),
		Vars: json.RawMessage(`[]`), Assertions: json.RawMessage(`[]`), Settings: json.RawMessage(`{}`),
		Examples: json.RawMessage(`[{"name":"ok"}]`), Tags: json.RawMessage(`["a"]`),
		Sidecar: json.RawMessage(`{}`), KeyOrder: json.RawMessage(`["name"]`), DescriptionMD: "# hi"})
	r2 := mustItem(t, s, Item{CollectionUID: c.UID, ParentUID: f.UID, Kind: "request", Name: "R2"})
	if !(r1.Rank < r2.Rank) {
		t.Fatalf("append order: %q %q", r1.Rank, r2.Rank)
	}
	got, err := s.GetItem(r1.UID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Headers) != string(r1.Headers) || string(got.URL) != string(r1.URL) || got.Method != "POST" ||
		string(got.Examples) != `[{"name":"ok"}]` || got.DescriptionMD != "# hi" || string(got.KeyOrder) != `["name"]` {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// reorder R2 before R1
	r2.Rank = RankBetween("", r1.Rank)
	if _, err := s.UpdateItem(*r2, CollChange{Actor: "t"}); err != nil {
		t.Fatal(err)
	}
	items, _ := s.ListItems(c.UID)
	var order []string
	for _, it := range items {
		if it.ParentUID == f.UID && it.Kind == "request" {
			order = append(order, it.Name)
		}
	}
	if len(order) != 2 || order[0] != "R2" {
		t.Fatalf("order after reorder %v", order)
	}
	// cycle: move F under Sub
	f2, _ := s.GetItem(f.UID)
	f2.ParentUID = sub.UID
	if _, err := s.UpdateItem(*f2, CollChange{}); !errors.Is(err, ErrCollInvalid) {
		t.Fatalf("cycle move err=%v", err)
	}
	// parent must be a folder
	r3 := Item{CollectionUID: c.UID, ParentUID: r1.UID, Kind: "request", Name: "bad"}
	if _, err := s.CreateItem(r3); !errors.Is(err, ErrCollInvalid) {
		t.Fatalf("request parent err=%v", err)
	}
	// deleting the folder removes descendants and revisions
	if err := s.DeleteItem(f.UID); err != nil {
		t.Fatal(err)
	}
	items, _ = s.ListItems(c.UID)
	if len(items) != 0 {
		t.Fatalf("descendants left: %d", len(items))
	}
	revs, _ := s.ListItemRevisions(r2.UID)
	if len(revs) != 0 {
		t.Fatalf("revisions left: %d", len(revs))
	}
}

func TestItemRevisions(t *testing.T) {
	s := newTestStore(t)
	c := mustColl(t, s, Collection{Name: "c"})
	it := mustItem(t, s, Item{CollectionUID: c.UID, Kind: "request", Name: "v1", Method: "GET"})
	for i, name := range []string{"v2", "v3"} {
		cur, _ := s.GetItem(it.UID)
		cur.Name = name
		up, err := s.UpdateItem(*cur, CollChange{Actor: "me", Source: "ui"})
		if err != nil || up.Rev != int64(i+2) {
			t.Fatalf("update %s: %v rev=%d", name, err, up.Rev)
		}
	}
	revs, err := s.ListItemRevisions(it.UID)
	if err != nil || len(revs) != 2 {
		t.Fatalf("revs=%d err=%v", len(revs), err)
	}
	if revs[0].Snapshot.Name != "v2" || revs[1].Snapshot.Name != "v1" || revs[0].Actor != "me" || revs[0].Source != "ui" {
		t.Fatalf("revisions %+v / %+v", revs[0], revs[1])
	}
	stale := Item{UID: it.UID, Name: "x", Rev: 1}
	if _, err := s.UpdateItem(stale, CollChange{}); !errors.Is(err, ErrCollConflict) {
		t.Fatalf("stale err=%v", err)
	}
}

func TestEnvironmentVariablesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	e, err := s.CreateEnvironment(Environment{Name: "prod", BoundIdentity: "alice", BaseTargetPin: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.SetVariables(VarOwnerEnvironment, e.UID, []Variable{
		{Key: "host", Type: "default", InitialValue: "example.com", Enabled: true},
		{Key: "tok", Type: "secret", InitialValue: "must-be-blanked", Enabled: true},
		{Key: "off", Type: "any", InitialValue: "x", Enabled: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	vs, _ := s.ListVariables(VarOwnerEnvironment, e.UID)
	if len(vs) != 3 {
		t.Fatalf("vars %+v", vs)
	}
	for _, v := range vs {
		switch v.Key {
		case "tok":
			if v.InitialValue != "" || v.Type != "secret" {
				t.Errorf("secret kept initial: %+v", v)
			}
		case "host":
			if v.InitialValue != "example.com" || !v.Enabled {
				t.Errorf("host: %+v", v)
			}
		case "off":
			if v.Enabled {
				t.Errorf("off enabled")
			}
		}
	}
	if err := s.SetCurrentValue(VarOwnerEnvironment, e.UID, "tok", "cur-1", "script"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCurrentValue(VarOwnerEnvironment, e.UID, "tok", "cur-2", "ui"); err != nil {
		t.Fatal(err)
	}
	cv, _ := s.ListCurrentValues(VarOwnerEnvironment, e.UID)
	if len(cv) != 1 || cv[0].Value != "cur-2" || cv[0].UpdatedBy != "ui" {
		t.Fatalf("current %+v", cv)
	}
	// dropping a key drops its current value
	if err := s.SetVariables(VarOwnerEnvironment, e.UID, []Variable{{Key: "host", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if cv, _ = s.ListCurrentValues(VarOwnerEnvironment, e.UID); len(cv) != 0 {
		t.Fatalf("stale current %+v", cv)
	}
	if err := s.SetVariables(VarOwnerEnvironment, e.UID, []Variable{{Key: "a"}, {Key: "a"}}); !errors.Is(err, ErrCollInvalid) {
		t.Fatalf("dup keys: %v", err)
	}
	e.Name = "prod2"
	if up, err := s.UpdateEnvironment(*e); err != nil || up.Rev != 2 {
		t.Fatalf("update env: %v", err)
	}
	if err := s.DeleteEnvironment(e.UID); err != nil {
		t.Fatal(err)
	}
	if vs, _ = s.ListVariables(VarOwnerEnvironment, e.UID); len(vs) != 0 {
		t.Fatal("vars survived env delete")
	}
}

func TestCookiesTokensRunsFlowCtxTrustRoundTrip(t *testing.T) {
	s := newTestStore(t)
	c := mustColl(t, s, Collection{Name: "c"})
	if err := s.PutCookie(CollCookie{Domain: "example.com", Name: "sid", Value: "v1", Flags: "Secure"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutCookie(CollCookie{Domain: "example.com", Name: "sid", Value: "v2"}); err != nil {
		t.Fatal(err)
	}
	ck, _ := s.ListCookies("")
	if len(ck) != 1 || ck[0].Value != "v2" || ck[0].Path != "/" {
		t.Fatalf("cookies %+v", ck)
	}
	tk, err := s.PutToken(CollToken{CollectionUID: c.UID, Name: "oauth", TokenJSON: `{"access_token":"a"}`})
	if err != nil {
		t.Fatal(err)
	}
	if ts, _ := s.ListTokens(c.UID); len(ts) != 1 || ts[0].UID != tk.UID {
		t.Fatalf("tokens %+v", ts)
	}
	run, err := s.PutRun(CollRun{CollectionUID: c.UID, Source: "cli", Status: "done", SummaryJSON: `{"n":1}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRunResult(CollRunResult{RunUID: run.UID, ItemUID: "i", Iteration: 1, FlowID: 7, Status: "pass", ResultJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	if rs, _ := s.ListRuns(c.UID, 0); len(rs) != 1 || rs[0].SummaryJSON != `{"n":1}` {
		t.Fatalf("runs %+v", rs)
	}
	if rr, _ := s.ListRunResults(run.UID); len(rr) != 1 || rr[0].FlowID != 7 {
		t.Fatalf("results %+v", rr)
	}
	if err := s.PutFlowCtx(FlowCtx{FlowID: 7, RunID: run.UID, ItemUID: "i", Phase: "main", TemplateHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if fc, ok, _ := s.GetFlowCtx(7); !ok || fc.TemplateHash != "abc" {
		t.Fatalf("flowctx %+v ok=%v", fc, ok)
	}
	if _, ok, _ := s.GetFlowCtx(8); ok {
		t.Fatal("phantom flow ctx")
	}
	if err := s.TrustScript(c.UID, "h1"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsScriptTrusted(c.UID, "h1"); !ok {
		t.Fatal("trust missing")
	}
	if ok, _ := s.IsScriptTrusted(c.UID, "h2"); ok {
		t.Fatal("unexpected trust")
	}
	_ = s.RevokeScriptTrust(c.UID, "")
	if ok, _ := s.IsScriptTrusted(c.UID, "h1"); ok {
		t.Fatal("trust survived revoke")
	}
	if err := s.AddImportLog(c.UID, "postman", `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureCollectionsIdempotentAndOldDB(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := s.ensureCollections(); err != nil {
			t.Fatal(err)
		}
	}
	collEnsured.Delete(s.db) // simulate a fresh process on an existing DB
	if _, err := s.db.Exec(collSchema); err != nil {
		t.Fatalf("schema is not re-runnable: %v", err)
	}
	for _, tb := range collTables {
		if ok, _ := peerHasTable(s.db, tb); !ok {
			t.Errorf("table %s missing", tb)
		}
	}
	_ = filepath.Join
}

func TestFlagCollectionBitFree(t *testing.T) {
	flags := []int64{FlagIntercepted, FlagEdited, FlagDropped, FlagCaptureError, FlagTLSFailed, FlagWebSocket, FlagRepeater,
		FlagIntruder, FlagImported, FlagAI, FlagAuthz, FlagDiscovery, FlagTLSBypassed, FlagResponseEdited}
	if FlagCollection != 1<<9 {
		t.Fatalf("FlagCollection=%d", FlagCollection)
	}
	for _, f := range flags {
		if f&FlagCollection != 0 {
			t.Fatalf("FlagCollection collides with %d", f)
		}
	}
}
