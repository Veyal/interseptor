package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A flow carries a free-text note: empty by default, settable, persisted, and
// matched by the metadata search (so an operator can find annotated flows).
func TestFlowNoteSetGetSearch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "h", Path: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := s.GetFlow(id); f.Note != "" {
		t.Fatalf("new flow note = %q, want empty", f.Note)
	}

	if err := s.SetFlowNote(id, "possible IDOR"); err != nil {
		t.Fatalf("SetFlowNote: %v", err)
	}
	f, err := s.GetFlow(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Note != "possible IDOR" {
		t.Fatalf("note = %q, want %q", f.Note, "possible IDOR")
	}

	got, err := s.QueryFlowsFilter(FlowFilter{Search: "idor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("search by note: got %d results, want the annotated flow", len(got))
	}
}

func TestInsertFlowPersistsInitialNote(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.InsertFlow(&Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/annotated",
		Note: "imported evidence note",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFlow(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "imported evidence note" {
		t.Fatalf("note = %q, want initial note to survive insertion", got.Note)
	}
	matched, err := s.QueryFlowsFilter(FlowFilter{Search: "evidence"})
	if err != nil || len(matched) != 1 || matched[0].ID != id {
		t.Fatalf("search initial note = %+v, err = %v", matched, err)
	}
}

// Open runs its column migration on every call; reopening the same database must
// not error (the ALTER TABLE ADD COLUMN is ignored once the column exists).
func TestOpenMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}

// A database created by an older release (flows table without the note column)
// must migrate cleanly on Open: the column is added, existing rows are preserved,
// and notes become settable. This is the real upgrade path for live projects.
func TestOpenMigratesPreNoteDatabase(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", filepath.Join(dir, "interceptor.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE flows (
		id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL,
		method TEXT, scheme TEXT, host TEXT, port INTEGER, path TEXT,
		http_version TEXT, status INTEGER, req_headers TEXT, res_headers TEXT,
		req_body_hash TEXT, res_body_hash TEXT, req_len INTEGER, res_len INTEGER,
		mime TEXT, duration_ms INTEGER, client_addr TEXT, error TEXT,
		flags INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	// Populate every column the way InsertFlow does (non-null strings) so the row
	// scans back; real flows never store NULLs.
	if _, err := raw.Exec(`INSERT INTO flows
		(ts, method, scheme, host, port, path, http_version, status,
		 req_headers, res_headers, req_body_hash, res_body_hash,
		 req_len, res_len, mime, duration_ms, client_addr, error, flags)
		VALUES (1,'GET','https','h',443,'/old','HTTP/1.1',200,'{}','{}','','',0,0,'',0,'','',0)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open (migrating pre-note db): %v", err)
	}
	defer s.Close()
	flows, err := s.QueryFlowsFilter(FlowFilter{})
	if err != nil {
		t.Fatalf("query after migrate: %v", err)
	}
	if len(flows) != 1 || flows[0].Path != "/old" || flows[0].Note != "" {
		t.Fatalf("after migrate: got %d flows, want one /old with empty note", len(flows))
	}
	if err := s.SetFlowNote(flows[0].ID, "annotated"); err != nil {
		t.Fatalf("SetFlowNote after migrate: %v", err)
	}
	if f, _ := s.GetFlow(flows[0].ID); f.Note != "annotated" {
		t.Fatal("note not persisted after migrate")
	}
}
