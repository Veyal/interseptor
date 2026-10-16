package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sidecar keeps a verbatim copy of the imported document so Postman export
// can be byte-exact. Once a user import stopped blanking credentials, that copy
// began carrying live secrets into every scrubbed path: archive, vault, bundle,
// peer merge and the AI read. scrubItemSecrets walked Auth/Headers/Params/URL/
// Body but never Sidecar or Examples, and scrubCollectionSecrets only walked
// Auth.
func TestCanaryScrubWalksSidecarAndExamples(t *testing.T) {
	const canary = "hunter2-SIDECAR-CANARY"

	it := &Item{
		Sidecar: json.RawMessage(`{"raw":{"auth":{"type":"bearer","bearer":[{"key":"token","value":"` + canary + `"}]},` +
			`"header":[{"key":"Authorization","value":"Bearer ` + canary + `"}]}}`),
		Examples: json.RawMessage(`[{"name":"ok","request":{"header":[{"key":"X-Api-Key","value":"` + canary + `"}]}}]`),
	}
	scrubItemSecrets(it)
	if strings.Contains(string(it.Sidecar), canary) {
		t.Errorf("item sidecar still carries the credential: %s", it.Sidecar)
	}
	if strings.Contains(string(it.Examples), canary) {
		t.Errorf("item examples still carry the credential: %s", it.Examples)
	}

	c := &Collection{
		Sidecar: json.RawMessage(`{"raw":{"auth":{"type":"apikey","apikey":[{"key":"value","value":"` + canary + `"}]}}}`),
	}
	scrubCollectionSecrets(c)
	if strings.Contains(string(c.Sidecar), canary) {
		t.Errorf("collection sidecar still carries the credential: %s", c.Sidecar)
	}
}

// The walk above is not enough on its own: scrubSecrets loads each row, scrubs
// the struct, then writes it back with an explicit column list. The first fix
// added Sidecar and Examples to the walk but not to those UPDATE statements, so
// the SQL snapshot scrub -- the path behind the full-project archive, file
// export, merge push and vault backup -- read the credential, blanked it in
// memory, counted it in SecretsBlanked, and then persisted the original. The
// in-memory test above passed throughout. This one goes through the real
// BackupToScrubbed path, which is what actually ships bytes off the machine.
func TestSnapshotScrubPersistsSidecarAndExamples(t *testing.T) {
	const canary = "hunter2-SNAPSHOT-CANARY"

	s := newTestStore(t)
	c := mustColl(t, s, Collection{Name: "Canary"})
	it := mustItem(t, s, Item{CollectionUID: c.UID, Kind: "request", Name: "authed"})

	// An import writes these columns verbatim; set them the same way.
	if _, err := s.db.Exec(`UPDATE ix_collections SET sidecar_json=? WHERE uid=?`,
		`{"raw":{"auth":{"type":"apikey","apikey":[{"key":"value","value":"`+canary+`"}]}}}`, c.UID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE ix_items SET sidecar_json=?,examples_json=? WHERE uid=?`,
		`{"raw":{"header":[{"key":"Authorization","value":"Bearer `+canary+`"}]}}`,
		`[{"name":"ok","request":{"header":[{"key":"X-Api-Key","value":"`+canary+`"}]}}]`, it.UID); err != nil {
		t.Fatal(err)
	}

	// A revision snapshot is a whole marshalled Item, so it cannot forget a
	// field the way a column list can -- but that is what was believed about the
	// walk, so assert it instead of trusting the shape.
	if _, err := s.db.Exec(`INSERT INTO ix_item_revisions(item_uid,rev,ts,snapshot) VALUES(?,?,?,?)`,
		it.UID, 1, 0,
		`{"uid":"`+it.UID+`","sidecar":{"raw":{"header":[{"key":"Authorization","value":"Bearer `+canary+`"}]}}}`); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "snap.db")
	rep, err := s.BackupToScrubbed(dest, ScrubOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.SecretsBlanked == 0 {
		t.Fatal("the scrub reported no secrets at all, so this test no longer exercises the walk")
	}

	raw, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(canary)) {
		t.Error("the scrubbed snapshot file still contains the credential; a sidecar or examples " +
			"column was blanked in memory and never written back")
	}
	db, err := sql.Open("sqlite", "file:"+dest+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []struct{ what, query, uid string }{
		{"item sidecar", `SELECT sidecar_json FROM ix_items WHERE uid=?`, it.UID},
		{"item examples", `SELECT examples_json FROM ix_items WHERE uid=?`, it.UID},
		{"collection sidecar", `SELECT sidecar_json FROM ix_collections WHERE uid=?`, c.UID},
		{"item revision snapshot", `SELECT COALESCE((SELECT snapshot FROM ix_item_revisions WHERE item_uid=? LIMIT 1),'')`, it.UID},
	} {
		var got string
		if err := db.QueryRow(q.query, q.uid).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q.what, err)
		}
		if strings.Contains(got, canary) {
			t.Errorf("%s still holds the credential after the snapshot scrub: %s", q.what, got)
		}
	}
}
