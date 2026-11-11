package store

import (
	"strings"
	"testing"
	"time"
)

func reviewOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func reviewFlow(t *testing.T, s *Store, f Flow) int64 {
	t.Helper()
	if f.TS.IsZero() {
		f.TS = time.UnixMilli(1_700_000_000_000)
	}
	if f.Method == "" {
		f.Method = "GET"
	}
	if f.Path == "" {
		f.Path = "/"
	}
	id, err := s.InsertFlow(&f)
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	return id
}

// The readiness and TLS-diagnosis endpoints count and list TLS failures on
// every call. Those reads must walk the partial index of failed rows, never
// the whole flows table.
func TestReviewTLSFailureReadsUseThePartialIndex(t *testing.T) {
	s := reviewOpen(t)
	queries := map[string]string{
		"count":  `SELECT COUNT(*) FROM flows WHERE ` + flagsAnyPredicate(FlagTLSFailed),
		"newest": `SELECT COALESCE(MAX(ts),0), COALESCE(SUM(CASE WHEN ts >= 1 THEN 1 ELSE 0 END),0) FROM flows WHERE ` + flagsAnyPredicate(FlagTLSFailed),
		"list":   `SELECT id FROM flows WHERE ` + flagsAnyPredicate(FlagTLSFailed) + ` ORDER BY id DESC, id DESC LIMIT 50`,
	}
	for name, q := range queries {
		if plan := explainPlan(t, s, q); !strings.Contains(plan, "idx_flows_tls_failed") {
			t.Errorf("%s: plan does not use the TLS-failure index:\n%s\n%s", name, q, plan)
		}
	}
	where, args := buildFlowFilterWhere(FlowFilter{RequireFlags: FlagTLSFailed})
	if len(where) != 1 || where[0] != flagsAnyPredicate(FlagTLSFailed) || len(args) != 0 {
		t.Fatalf("RequireFlags predicate = %q args %v, want the index-matching literal form", where, args)
	}
}

// The index-backed queries must report exactly what the full scans did.
func TestReviewTLSStatsMatchAcrossFlagsAndHosts(t *testing.T) {
	s := reviewOpen(t)
	base := int64(1_700_000_000_000)
	reviewFlow(t, s, Flow{TS: time.UnixMilli(base + 1), Scheme: "https", Host: "api.example.com", Status: 200})
	reviewFlow(t, s, Flow{TS: time.UnixMilli(base + 2), Scheme: "https", Host: "pinned.example.com", Flags: FlagTLSFailed})
	reviewFlow(t, s, Flow{TS: time.UnixMilli(base + 3), Scheme: "https", Host: "pinned.example.com", Flags: FlagTLSFailed | FlagAI})
	reviewFlow(t, s, Flow{TS: time.UnixMilli(base + 4), Scheme: "http", Host: "plain.example.com", Status: 200, Flags: FlagRepeater})
	reviewFlow(t, s, Flow{TS: time.UnixMilli(base + 5), Scheme: "https", Host: "API.example.com", Status: 204, Flags: FlagAI})
	reviewFlow(t, s, Flow{TS: time.UnixMilli(base + 6), Scheme: "https", Host: "pending.example.com"})

	for flag, want := range map[int64]int64{FlagTLSFailed: 2, FlagAI: 2, FlagRepeater: 1, FlagTLSFailed | FlagRepeater: 3, FlagIntruder: 0} {
		if got, err := s.CountFlowsWithFlag(flag); err != nil || got != want {
			t.Errorf("CountFlowsWithFlag(%d) = %d, %v; want %d", flag, got, err, want)
		}
	}
	for host, want := range map[string]int64{"": 2, "api.": 2, "pinned": 0, "nomatch": 0} {
		if got, err := s.CountSuccessfulHTTPS(host); err != nil || got != want {
			t.Errorf("CountSuccessfulHTTPS(%q) = %d, %v; want %d", host, got, err, want)
		}
	}
	type stats struct{ newestFail, recent, newestOK int64 }
	for _, c := range []struct {
		host  string
		since int64
		want  stats
	}{
		{"", 0, stats{base + 3, 2, base + 5}},
		{"", base + 3, stats{base + 3, 1, base + 5}},
		{"pinned", base + 4, stats{base + 3, 0, 0}},
		{"api", 0, stats{0, 0, base + 5}},
		{"nomatch", 0, stats{0, 0, 0}},
	} {
		nf, rc, ok, err := s.TLSFailureStats(c.host, c.since)
		if got := (stats{nf, rc, ok}); err != nil || got != c.want {
			t.Errorf("TLSFailureStats(%q, %d) = %+v, %v; want %+v", c.host, c.since, got, err, c.want)
		}
	}
	got, err := s.QueryFlowsFilter(FlowFilter{RequireFlags: FlagTLSFailed, Limit: 50, SortKey: "id", SortDir: -1})
	if err != nil || len(got) != 2 || got[0].ID != 3 || got[1].ID != 2 {
		t.Fatalf("TLS failure list = %v, %v; want ids [3 2]", got, err)
	}
}

// Retention's "keep the newest N" pass runs on a timer. It must address the
// rows to delete by an id cutoff, not test every flow against a list of the N
// ids to keep, and it must keep exactly the N newest rows when ids have gaps.
func TestReviewKeepNewestDeletesBelowAnIDCutoff(t *testing.T) {
	s := reviewOpen(t)
	var ids []int64
	for i := 0; i < 10; i++ {
		ids = append(ids, reviewFlow(t, s, Flow{Host: "example.com"}))
	}
	if _, err := s.DeleteFlows([]int64{ids[7], ids[4]}); err != nil { // gaps: 1 2 3 4 6 7 9 10
		t.Fatalf("DeleteFlows: %v", err)
	}
	if plan := explainPlan(t, s, keepNewestDeleteSQL, int64(5)); !strings.Contains(plan, "SEARCH flows USING INTEGER PRIMARY KEY (rowid<?)") {
		t.Fatalf("keep-newest delete is not an id-range search:\n%s", plan)
	}
	if n, err := s.DeleteFlowsKeepNewest(8); err != nil || n != 0 {
		t.Fatalf("keep 8 of 8 deleted %d, %v; want 0", n, err)
	}
	if n, err := s.DeleteFlowsKeepNewest(100); err != nil || n != 0 {
		t.Fatalf("keep 100 of 8 deleted %d, %v; want 0", n, err)
	}
	n, err := s.DeleteFlowsKeepNewest(3)
	if err != nil || n != 5 {
		t.Fatalf("keep 3 of 8 deleted %d, %v; want 5", n, err)
	}
	left, err := s.QueryFlows(100)
	if err != nil || len(left) != 3 || left[0].ID != ids[9] || left[1].ID != ids[8] || left[2].ID != ids[6] {
		t.Fatalf("kept %v, %v; want ids [10 9 7]", left, err)
	}
	var fts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM flows_fts`).Scan(&fts); err != nil || fts != 3 {
		t.Fatalf("full-text rows = %d, %v; want 3", fts, err)
	}
}

// GET /api/flows/prune/pending is requested whenever Settings opens. A project
// that has never pruned has no snapshot table yet; that is "nothing pending",
// not a server error.
func TestReviewPendingPrunesBeforeAnyPrune(t *testing.T) {
	s := reviewOpen(t)
	got, err := s.PendingPrunes()
	if err != nil || len(got) != 0 {
		t.Fatalf("PendingPrunes on a fresh project = %v, %v; want none, nil", got, err)
	}
	if _, err := s.RestorePrune(1); err == nil || strings.Contains(err.Error(), "no such table") {
		t.Fatalf("RestorePrune on a fresh project = %v; want a not-found error", err)
	}
}

// A NUL byte in the History search box (?search=%00) must not surface as a
// SQL error: FTS5 reads the NUL as the end of its query string.
func TestReviewFTSSearchWithNULByte(t *testing.T) {
	s := reviewOpen(t)
	id := reviewFlow(t, s, Flow{Host: "example.com", Path: "/orders"})
	for _, term := range []string{"ord\x00ers", "\x00", "orders\x00"} {
		got, err := s.QueryFlowsListFilter(FlowFilter{Search: term})
		if err != nil {
			t.Fatalf("search %q: %v", term, err)
		}
		if term == "orders\x00" && (len(got) != 1 || got[0].ID != id) {
			t.Fatalf("search %q = %v, want flow %d", term, got, id)
		}
	}
}

// Body GC must fail closed: when it cannot read which bodies a live prune
// snapshot still needs, deleting "unreferenced" files would destroy the bodies
// an undo is about to bring back.
func TestReviewGCBodiesFailsClosedWhenPruneProtectionIsUnreadable(t *testing.T) {
	s := reviewOpen(t)
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("body a pruned flow still needs")); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	// A snapshot table this build cannot read (wrong shape).
	if _, err := s.db.Exec(`CREATE TABLE prune_snapshots (id INTEGER PRIMARY KEY); CREATE TABLE prune_body_hashes (snapshot_id INTEGER, hash TEXT)`); err != nil {
		t.Fatal(err)
	}
	removed, _, err := s.GCBodies()
	if err == nil {
		t.Fatalf("GCBodies succeeded (removed %d) although prune protection could not be read", removed)
	}
	if !s.BodyExists(hash) {
		t.Fatal("GCBodies removed a body while prune protection was unreadable")
	}
}

// A flow id must never be handed out twice, even after the newest flows were
// deleted and the project reopened: findings, tags and prune snapshots address
// flows by id, so a reused id attaches old evidence to an unrelated request.
func TestReviewFlowIDsAreNotReusedAfterReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	reviewFlow(t, s, Flow{Host: "keep.example.com"})
	reviewFlow(t, s, Flow{Host: "old.example.com", Path: "/evidence"})
	res, err := s.PruneFlows(PruneOptions{Hosts: []string{"old.example.com"}})
	if err != nil || res.Deleted != 1 {
		t.Fatalf("PruneFlows = %+v, %v", res, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fresh := reviewFlow(t, s, Flow{Host: "new.example.com", Path: "/fresh"})
	if fresh != 3 {
		t.Fatalf("new flow got id %d; id 2 belonged to a deleted flow and must not be reused", fresh)
	}
	// Undoing the prune must bring the old flow back without replacing the new one.
	if n, err := s.RestorePrune(res.PruneID); err != nil || n != 1 {
		t.Fatalf("RestorePrune = %d, %v", n, err)
	}
	got, err := s.GetFlow(fresh)
	if err != nil || got.Host != "new.example.com" {
		t.Fatalf("flow %d after undo = %+v, %v; the undo overwrote a newer capture", fresh, got, err)
	}
	if old, err := s.GetFlow(2); err != nil || old.Host != "old.example.com" {
		t.Fatalf("restored flow 2 = %+v, %v", old, err)
	}
}

// rows.Close does not report an error that ended the iteration early; only
// rows.Err does. A prune that reads its candidate rows through queryRowsByIDs
// must fail on such an error instead of acting on a truncated list.
func TestReviewQueryRowsByIDsReportsIterationErrors(t *testing.T) {
	s := reviewOpen(t)
	a := reviewFlow(t, s, Flow{Host: "example.com", ReqLen: 1})
	b := reviewFlow(t, s, Flow{Host: "example.com", ReqLen: -1 << 63})
	// abs() of the minimum int64 overflows, but only when the second row is stepped.
	rows, err := s.queryRowsByIDs([]int64{a, b}, `SELECT abs(req_len) FROM flows WHERE id IN (%s) ORDER BY id`)
	if err == nil {
		t.Fatalf("queryRowsByIDs returned %d row(s) and no error for a query that failed part-way", len(rows))
	}
}
