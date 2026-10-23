package store

import (
	"encoding/json"
	"strconv"
	"testing"
)

func putRun(t *testing.T, s *Store, kind, actor string) int64 {
	t.Helper()
	id, err := s.PutAuthzRun(AuthzRun{
		Kind:    kind,
		Actor:   actor,
		Summary: json.RawMessage(`{"kind":"` + kind + `","endpoints":1}`),
		Matrix:  json.RawMessage(`{"identities":["admin"],"rows":[]}`),
		Raw:     json.RawMessage(`{"runs":[]}`),
	})
	if err != nil {
		t.Fatalf("PutAuthzRun: %v", err)
	}
	if id <= 0 {
		t.Fatalf("PutAuthzRun id = %d, want > 0", id)
	}
	return id
}

func TestAuthzRunsInsertListGetDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first := putRun(t, s, "single", "you")
	second := putRun(t, s, "differential", "agent")

	runs, err := s.ListAuthzRuns(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != second || runs[1].ID != first {
		t.Fatalf("ListAuthzRuns = %+v, want newest first", runs)
	}
	if runs[0].TS == 0 {
		t.Error("list row missing ts")
	}
	if len(runs[0].Matrix) != 0 || len(runs[0].Raw) != 0 {
		t.Error("listing must not carry matrix/raw blobs")
	}

	got, ok, err := s.GetAuthzRun(second)
	if err != nil || !ok {
		t.Fatalf("GetAuthzRun = %v %v", ok, err)
	}
	if got.Kind != "differential" || got.Actor != "agent" || string(got.Matrix) != `{"identities":["admin"],"rows":[]}` {
		t.Fatalf("GetAuthzRun = %+v", got)
	}

	if _, ok, err := s.GetAuthzRun(99999); err != nil || ok {
		t.Fatalf("missing run = %v %v, want false/nil", ok, err)
	}

	ok, err = s.DeleteAuthzRun(second)
	if err != nil || !ok {
		t.Fatalf("DeleteAuthzRun = %v %v", ok, err)
	}
	if ok, err := s.DeleteAuthzRun(second); err != nil || ok {
		t.Fatalf("second delete = %v %v, want false/nil", ok, err)
	}
	runs, _ = s.ListAuthzRuns(50)
	if len(runs) != 1 || runs[0].ID != first {
		t.Fatalf("after delete = %+v", runs)
	}
}

func TestAuthzRunsRetentionCap(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < maxAuthzRuns+5; i++ {
		putRun(t, s, "single", "you"+strconv.Itoa(i))
	}
	runs, err := s.ListAuthzRuns(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != maxAuthzRuns {
		t.Fatalf("kept %d runs, want %d", len(runs), maxAuthzRuns)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM authz_runs`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != maxAuthzRuns {
		t.Fatalf("table holds %d rows, want %d", total, maxAuthzRuns)
	}
}

// TestAuthzRunsMigrationOnExistingDB drops the table from an already-populated
// project (simulating a database created before the feature) and reopens it:
// the schema pass must recreate it and the rest of the project must survive.
func TestAuthzRunsMigrationOnExistingDB(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFlow(&Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: "/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE authz_runs`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if runs, err := s2.ListAuthzRuns(10); err != nil || len(runs) != 0 {
		t.Fatalf("ListAuthzRuns after migration = %+v %v", runs, err)
	}
	putRun(t, s2, "bulk", "you")
	if runs, _ := s2.ListAuthzRuns(10); len(runs) != 1 {
		t.Fatal("run not stored after migration")
	}
	flows, err := s2.QueryFlows(10)
	if err != nil || len(flows) != 1 {
		t.Fatalf("flows after migration = %d %v", len(flows), err)
	}
}

func TestAuthzRunTooLarge(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	big := make([]byte, maxAuthzRunBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if _, err := s.PutAuthzRun(AuthzRun{Kind: "single", Raw: json.RawMessage(big)}); err == nil {
		t.Fatal("oversized run accepted")
	}
}

func TestFlowsWithCredentialBoundedScan(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < 5; i++ {
		if _, err := s.InsertFlow(&Flow{
			Method: "GET", Scheme: "https", Host: "example.com", Path: "/api/me", Status: 200,
			ReqHeaders: map[string][]string{"Authorization": {"Bearer tok-admin"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertFlow(&Flow{
		Method: "GET", Scheme: "https", Host: "example.com", Path: "/other", Status: 200,
		ReqHeaders: map[string][]string{"Authorization": {"Bearer tok-user"}},
	}); err != nil {
		t.Fatal(err)
	}

	count, recent, err := s.FlowsWithCredential([]string{"tok-admin"}, 5000, 3)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}
	if len(recent) != 3 {
		t.Fatalf("recent = %d, want 3 (the bound)", len(recent))
	}
	if recent[0].FlowID <= recent[1].FlowID {
		t.Fatalf("recent not newest-first: %+v", recent)
	}
	if recent[0].Host != "example.com" || recent[0].Path != "/api/me" || recent[0].Status != 200 {
		t.Fatalf("recent row = %+v", recent[0])
	}

	if count, _, err := s.FlowsWithCredential(nil, 5000, 3); err != nil || count != 0 {
		t.Fatalf("no credentials = %d %v, want 0/nil", count, err)
	}
	if count, _, err := s.FlowsWithCredential([]string{"nope"}, 5000, 3); err != nil || count != 0 {
		t.Fatalf("unmatched credential = %d %v", count, err)
	}
}
