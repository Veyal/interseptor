package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Canary strings: none of these may appear in any scrubbed export.
var secretCanaries = []string{
	"CANARY-CURRENT-VALUE", "CANARY-INITIAL-SECRET", "CANARY-COOKIE", "CANARY-OAUTH-TOKEN",
	"CANARY-COLL-BEARER", "CANARY-ITEM-BEARER", "CANARY-HEADER-AUTH", "CANARY-PARAM-KEY",
	"CANARY-REV-BEARER", "CANARY-APIKEY-VALUE", "CANARY-BASIC-PASSWORD", "CANARY-DIRECT-TOKEN",
}

// seedSecrets builds a project carrying every kind of secret the scrub must remove,
// and one kept shareable value ("example.com") plus a {{template}} reference.
func seedSecrets(t *testing.T, s *Store) (*Collection, *Item) {
	t.Helper()
	c := mustColl(t, s, Collection{Name: "c",
		Auth:   json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"CANARY-COLL-BEARER","type":"string"}]}`),
		Events: json.RawMessage(`[{"listen":"test","script":{"exec":["pm.test('x')"]}}]`)})
	it := mustItem(t, s, Item{CollectionUID: c.UID, Kind: "request", Name: "r", Method: "GET",
		URL:     json.RawMessage(`{"raw":"https://example.com/a"}`),
		Auth:    json.RawMessage(`{"type":"apikey","apikey":[{"key":"key","value":"X-Api"},{"key":"value","value":"CANARY-APIKEY-VALUE"}]}`),
		Headers: json.RawMessage(`[{"key":"Authorization","value":"Bearer CANARY-HEADER-AUTH"},{"key":"X-Trace","value":"keepme"},{"key":"X-Tpl","value":"{{token}}"},{"key":"Cookie","value":"{{ck}}"}]`),
		Params:  json.RawMessage(`[{"key":"api_key","value":"CANARY-PARAM-KEY"},{"key":"page","value":"2"}]`)})
	// an edit stores the previous (secret-bearing) state as a revision
	cur, _ := s.GetItem(it.UID)
	cur.Auth = json.RawMessage(`{"type":"basic","basic":[{"key":"username","value":"bob"},{"key":"password","value":"CANARY-BASIC-PASSWORD"}]}`)
	cur.Headers = json.RawMessage(`[{"key":"Authorization","value":"Bearer CANARY-REV-BEARER"}]`)
	if _, err := s.UpdateItem(*cur, CollChange{Actor: "t"}); err != nil {
		t.Fatal(err)
	}
	first := *it
	first.Rev = 0
	first.Auth = json.RawMessage(`{"type":"bearer","token":"CANARY-DIRECT-TOKEN"}`)
	cur, _ = s.GetItem(it.UID)
	cur.Auth = first.Auth
	if _, err := s.UpdateItem(*cur, CollChange{}); err != nil {
		t.Fatal(err)
	}
	e, _ := s.CreateEnvironment(Environment{Name: "env"})
	if err := s.SetVariables(VarOwnerEnvironment, e.UID, []Variable{
		{Key: "host", Type: "default", InitialValue: "example.com", Enabled: true},
		{Key: "tok", Type: "secret", InitialValue: "CANARY-INITIAL-SECRET", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	// bypass the write-time blanking to prove the scrub itself blanks secrets
	if _, err := s.db.Exec(`UPDATE ix_variables SET initial_value='CANARY-INITIAL-SECRET' WHERE key='tok'`); err != nil {
		t.Fatal(err)
	}
	_ = s.SetCurrentValue(VarOwnerEnvironment, e.UID, "tok", "CANARY-CURRENT-VALUE", "script")
	_ = s.PutCookie(CollCookie{Domain: "example.com", Name: "sid", Value: "CANARY-COOKIE"})
	_, _ = s.PutToken(CollToken{CollectionUID: c.UID, Name: "o", TokenJSON: `{"access_token":"CANARY-OAUTH-TOKEN"}`})
	_ = s.TrustScript(c.UID, "trusted-hash")
	return c, it
}

func assertNoCanary(t *testing.T, label string, data []byte, canaries []string) {
	t.Helper()
	for _, c := range canaries {
		if bytes.Contains(data, []byte(c)) {
			t.Errorf("%s: canary %q leaked", label, c)
		}
	}
}

func TestScrubbedBackupHasNoSecretCanary(t *testing.T) {
	s := newTestStore(t)
	seedSecrets(t, s)
	dest := filepath.Join(t.TempDir(), "snap.db")
	rep, err := s.BackupToScrubbed(dest, ScrubOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.IncludedSecrets || rep.SecretsBlanked == 0 || rep.RowsDeleted == 0 {
		t.Fatalf("report %+v", rep)
	}
	raw, _ := os.ReadFile(dest)
	assertNoCanary(t, "scrubbed snapshot bytes", raw, secretCanaries)
	for _, side := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dest + side); err == nil {
			t.Errorf("sidecar %s left behind", side)
		}
	}
	// shareable data survives
	db, _ := sql.Open("sqlite", "file:"+dest+"?mode=ro")
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM ix_variables WHERE key='host' AND initial_value='example.com'`).Scan(&n)
	if n != 1 {
		t.Error("shareable initial value lost")
	}
	var h string
	db.QueryRow(`SELECT headers_json FROM ix_items`).Scan(&h)
	if h == "" {
		t.Error("headers lost")
	}
	for _, tb := range []string{"ix_var_current", "ix_tokens", "ix_cookies", "ix_script_trust"} {
		db.QueryRow(`SELECT COUNT(*) FROM ` + tb).Scan(&n)
		if n != 0 {
			t.Errorf("%s not emptied", tb)
		}
	}
	// the live store is untouched
	cv, _ := s.ListCurrentValues(VarOwnerEnvironment, mustFirstEnv(t, s))
	if len(cv) != 1 {
		t.Error("live current value was removed by export")
	}
}

func mustFirstEnv(t *testing.T, s *Store) string {
	t.Helper()
	es, _ := s.ListEnvironments()
	if len(es) == 0 {
		t.Fatal("no env")
	}
	return es[0].UID
}

func TestScrubKeepsTemplateRefsAndNonSecrets(t *testing.T) {
	s := newTestStore(t)
	_, it := seedSecrets(t, s)
	dest := filepath.Join(t.TempDir(), "snap.db")
	if _, err := s.BackupToScrubbed(dest, ScrubOptions{}); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", "file:"+dest+"?mode=ro")
	defer db.Close()
	var hdr, params string
	db.QueryRow(`SELECT headers_json, params_json FROM ix_items WHERE uid=?`, it.UID).Scan(&hdr, &params)
	// current headers are the second edit's; check the original-shape rules on a fresh item instead
	c2 := mustColl(t, s, Collection{Name: "c2"})
	it2 := mustItem(t, s, Item{CollectionUID: c2.UID, Kind: "request", Name: "k",
		Headers: json.RawMessage(`[{"key":"X-Trace","value":"keepme"},{"key":"X-Tpl","value":"{{token}}"},{"key":"Cookie","value":"{{ck}}"},{"key":"Authorization","value":"Bearer lit"}]`),
		Params:  json.RawMessage(`[{"key":"page","value":"2"}]`),
		Auth:    json.RawMessage(`{"type":"oauth2","oauth2":[{"key":"accessTokenUrl","value":"https://example.com/t"},{"key":"clientSecret","value":"{{cs}}"},{"key":"tokenType","value":"Bearer"}]}`)})
	dest2 := filepath.Join(t.TempDir(), "snap2.db")
	if _, err := s.BackupToScrubbed(dest2, ScrubOptions{}); err != nil {
		t.Fatal(err)
	}
	db2, _ := sql.Open("sqlite", "file:"+dest2+"?mode=ro")
	defer db2.Close()
	var h2, p2, a2 string
	db2.QueryRow(`SELECT headers_json, params_json, auth_json FROM ix_items WHERE uid=?`, it2.UID).Scan(&h2, &p2, &a2)
	var hs []map[string]string
	json.Unmarshal([]byte(h2), &hs)
	want := map[string]string{"X-Trace": "keepme", "X-Tpl": "{{token}}", "Cookie": "{{ck}}", "Authorization": ""}
	for _, h := range hs {
		if h["value"] != want[h["key"]] {
			t.Errorf("header %s = %q want %q", h["key"], h["value"], want[h["key"]])
		}
	}
	if p2 != `[{"key":"page","value":"2"}]` {
		t.Errorf("params changed: %s", p2)
	}
	if !bytes.Contains([]byte(a2), []byte("https://example.com/t")) || !bytes.Contains([]byte(a2), []byte("{{cs}}")) || !bytes.Contains([]byte(a2), []byte("Bearer")) {
		t.Errorf("non-secret auth fields lost: %s", a2)
	}
}

func TestScrubIncludeSecretsKeepsValuesButResetsTrust(t *testing.T) {
	s := newTestStore(t)
	seedSecrets(t, s)
	dest := filepath.Join(t.TempDir(), "snap.db")
	rep, err := s.BackupToScrubbed(dest, ScrubOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.IncludedSecrets {
		t.Fatal("manifest must record includeSecrets")
	}
	raw, _ := os.ReadFile(dest)
	for _, c := range []string{"CANARY-CURRENT-VALUE", "CANARY-COOKIE", "CANARY-OAUTH-TOKEN", "CANARY-COLL-BEARER"} {
		if !bytes.Contains(raw, []byte(c)) {
			t.Errorf("includeSecrets dropped %s", c)
		}
	}
	db, _ := sql.Open("sqlite", "file:"+dest+"?mode=ro")
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM ix_script_trust`).Scan(&n)
	if n != 0 {
		t.Fatal("script trust traveled with an includeSecrets export")
	}
}

func TestScrubOnProjectWithoutCollectionTables(t *testing.T) {
	// A snapshot of a project that never used collections must scrub cleanly.
	dir := t.TempDir()
	p := filepath.Join(dir, "old.db")
	db, _ := sql.Open("sqlite", "file:"+p)
	db.Exec(`CREATE TABLE flows(id INTEGER PRIMARY KEY)`)
	db.Close()
	rep, err := ScrubSnapshotFile(p, ScrubOptions{})
	if err != nil || rep.SecretsBlanked != 0 {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
}

func TestBundleExportHasNoSecretCanaryAndOldBundleTolerated(t *testing.T) {
	s := newTestStore(t)
	seedSecrets(t, s)
	b, err := s.ExportCollectionsBundle(ScrubOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	assertNoCanary(t, "bundle json", raw, secretCanaries)
	if b.Version != CollectionsBundleVersion || len(b.Collections) != 1 || len(b.Environments) != 1 {
		t.Fatalf("bundle %+v", b)
	}
	// old project bundle: no collections section
	for _, old := range []json.RawMessage{nil, json.RawMessage(`null`)} {
		ob, err := DecodeCollectionsBundle(old)
		if err != nil {
			t.Fatal(err)
		}
		st, err := newTestStore(t).ImportCollectionsBundle(ob)
		if err != nil || st != (CollectionMergeStats{}) {
			t.Fatalf("old bundle: %+v %v", st, err)
		}
	}
	if _, err := newTestStore(t).ImportCollectionsBundle(CollectionsBundle{Version: 99}); err == nil {
		t.Fatal("newer bundle version must be rejected")
	}
	// round trip into a second project
	s2 := newTestStore(t)
	st, err := s2.ImportCollectionsBundle(b)
	if err != nil || st.CollectionsAdded != 1 || st.ItemsAdded != 1 || st.EnvironmentsAdded != 1 {
		t.Fatalf("import: %+v %v", st, err)
	}
	raw2, _ := json.Marshal(mustBundle(t, s2))
	assertNoCanary(t, "re-exported bundle", raw2, secretCanaries)
}

func mustBundle(t *testing.T, s *Store) CollectionsBundle {
	t.Helper()
	b, err := s.ExportCollectionsBundle(ScrubOptions{IncludeSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMergeCollectionsCanaryAndQuarantine(t *testing.T) {
	peer := newTestStore(t)
	c, _ := seedSecrets(t, peer)
	// peer raises its policy/caps: must not be inherited
	pc, _ := peer.GetCollection(c.UID)
	pc.ScopePolicy = "off"
	pc.Caps = json.RawMessage(`{"net.outOfScope":true}`)
	peer.UpdateCollection(*pc)
	// merge from an UNSCRUBBED peer file: merge itself must keep secrets out
	peerDB := filepath.Join(t.TempDir(), "peer.db")
	if err := peer.BackupTo(peerDB); err != nil {
		t.Fatal(err)
	}
	local := newTestStore(t)
	st, err := local.MergeCollectionsFrom(peerDB)
	if err != nil {
		t.Fatal(err)
	}
	if st.CollectionsAdded != 1 || st.ItemsAdded != 1 || st.EnvironmentsAdded != 1 {
		t.Fatalf("stats %+v", st)
	}
	got, _ := local.GetCollection(c.UID)
	if got.ScopePolicy != "block" || len(got.Caps) != 0 {
		t.Fatalf("peer policy/caps inherited: %q %s", got.ScopePolicy, got.Caps)
	}
	if ok, _ := local.IsScriptTrusted(c.UID, "trusted-hash"); ok {
		t.Fatal("trust merged from peer")
	}
	for _, tb := range []string{"ix_var_current", "ix_cookies", "ix_tokens", "ix_script_trust"} {
		var n int
		local.db.QueryRow(`SELECT COUNT(*) FROM ` + tb).Scan(&n)
		if n != 0 {
			t.Errorf("%s merged (%d rows)", tb, n)
		}
	}
	dest := filepath.Join(t.TempDir(), "local.db")
	if _, err := local.BackupToScrubbed(dest, ScrubOptions{}); err != nil {
		t.Fatal(err)
	}
	// even an unscrubbed dump of the merged project holds no secret that was in the peer
	raw, _ := os.ReadFile(dest)
	assertNoCanary(t, "merged+scrubbed", raw, secretCanaries)
	var dump bytes.Buffer
	for _, tb := range []string{"ix_collections", "ix_items", "ix_variables", "ix_item_revisions"} {
		rows, _ := local.db.Query(`SELECT * FROM ` + tb)
		cols, _ := rows.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			rows.Scan(ptrs...)
			for _, v := range vals {
				dump.WriteString(toStr(v))
				dump.WriteByte('|')
			}
		}
		rows.Close()
	}
	assertNoCanary(t, "merged live tables", dump.Bytes(), secretCanaries)
}

func toStr(v any) string {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case string:
		return x
	}
	return ""
}

func TestMergeCollectionsOldPeerIsNoop(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.db")
	db, _ := sql.Open("sqlite", "file:"+p)
	db.Exec(`CREATE TABLE flows(id INTEGER PRIMARY KEY)`)
	db.Close()
	local := newTestStore(t)
	st, err := local.MergeCollectionsFrom(p)
	if err != nil || st != (CollectionMergeStats{}) {
		t.Fatalf("old peer: %+v %v", st, err)
	}
	if cs, _ := local.ListCollections(); len(cs) != 0 {
		t.Fatal("old peer created collections")
	}
}

func TestMergeCollectionsIdempotentAndHigherRevWins(t *testing.T) {
	peer := newTestStore(t)
	c := mustColl(t, peer, Collection{Name: "shared"})
	f := mustItem(t, peer, Item{CollectionUID: c.UID, Kind: "folder", Name: "F"})
	r := mustItem(t, peer, Item{CollectionUID: c.UID, ParentUID: f.UID, Kind: "request", Name: "R", Method: "GET", URL: json.RawMessage(`{"raw":"https://example.com/1"}`)})
	e, _ := peer.CreateEnvironment(Environment{Name: "dev"})
	peer.SetVariables(VarOwnerEnvironment, e.UID, []Variable{{Key: "a", InitialValue: "1", Enabled: true}})

	snap := func() string {
		p := filepath.Join(t.TempDir(), "peer.db")
		if err := peer.BackupTo(p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	local := newTestStore(t)
	st, err := local.MergeCollectionsFrom(snap())
	if err != nil || st.ItemsAdded != 2 || st.VariablesAdded != 1 {
		t.Fatalf("first: %+v %v", st, err)
	}
	st, err = local.MergeCollectionsFrom(snap())
	if err != nil {
		t.Fatal(err)
	}
	if st.CollectionsAdded+st.ItemsAdded+st.EnvironmentsAdded+st.VariablesAdded != 0 {
		t.Fatalf("not idempotent: %+v", st)
	}
	if its, _ := local.ListItems(c.UID); len(its) != 2 {
		t.Fatalf("duplicates after re-merge: %d", len(its))
	}
	// peer edits the request twice (rev 3) -> local picks it up
	for _, name := range []string{"R-v2", "R-v3"} {
		cur, _ := peer.GetItem(r.UID)
		cur.Name = name
		peer.UpdateItem(*cur, CollChange{})
	}
	st, _ = local.MergeCollectionsFrom(snap())
	if st.ItemsUpdated != 1 {
		t.Fatalf("update: %+v", st)
	}
	got, _ := local.GetItem(r.UID)
	if got.Name != "R-v3" || got.Rev != 3 || got.ParentUID != f.UID {
		t.Fatalf("after update %+v", got)
	}
	// local edit bumps rev beyond the peer's: peer snapshot no longer wins
	got.Name = "local"
	local.UpdateItem(*got, CollChange{})
	cur, _ := local.GetItem(r.UID)
	cur.Name = "local2"
	local.UpdateItem(*cur, CollChange{})
	st, _ = local.MergeCollectionsFrom(snap())
	if st.ItemsUpdated != 0 {
		t.Fatalf("older peer overwrote local: %+v", st)
	}
	if g, _ := local.GetItem(r.UID); g.Name != "local2" {
		t.Fatalf("name %q", g.Name)
	}
}

func TestMergeCollectionsNameAndPathSignatureFallback(t *testing.T) {
	mk := func() (*Store, *Collection) {
		s := newTestStore(t)
		c := mustColl(t, s, Collection{Name: "API"})
		f := mustItem(t, s, Item{CollectionUID: c.UID, Kind: "folder", Name: "Users"})
		mustItem(t, s, Item{CollectionUID: c.UID, ParentUID: f.UID, Kind: "request", Name: "List", Method: "GET", URL: json.RawMessage(`{"raw":"https://example.com/u"}`)})
		return s, c
	}
	a, _ := mk()
	b, bc := mk() // same logical collection created independently: different uids
	p := filepath.Join(t.TempDir(), "b.db")
	if err := b.BackupTo(p); err != nil {
		t.Fatal(err)
	}
	st, err := a.MergeCollectionsFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.CollectionsAdded != 0 || st.ItemsAdded != 0 || st.ItemsSkipped != 2 {
		t.Fatalf("signature fallback: %+v", st)
	}
	_ = bc
	if cs, _ := a.ListCollections(); len(cs) != 1 {
		t.Fatalf("collections=%d", len(cs))
	}
}

func TestIsSecretNameIsTheScrubHeuristic(t *testing.T) {
	for _, n := range []string{"token", "accessToken", "client_secret", "Authorization", "session_id", "x-api-key", "signature", "db_password"} {
		if !IsSecretName(n) {
			t.Errorf("%q must be a secret name", n)
		}
	}
	for _, n := range []string{"baseUrl", "tokenUrl", "tokenType", "username", "page", "authName", ""} {
		if IsSecretName(n) {
			t.Errorf("%q must not be a secret name", n)
		}
	}
}
