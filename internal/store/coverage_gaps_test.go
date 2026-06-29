package store

// coverage_gaps_test.go — targeted tests for previously under-covered paths in
// internal/store.  Each test group is annotated with the function/path it covers.
//
// Areas covered here (not exhaustively exercised elsewhere):
//   - GCBodies: shared body hash kept alive when only one of two referencing flows is deleted
//   - GCBodies: body referenced as res_body_hash by a second flow survives after the first is deleted
//   - QueryFlowsFilter: RequireFlags, HasNote, Tag, FlowIDs combinators
//   - QueryFlowsFilter keyset pagination with non-id sort keys (host, status, size)
//   - bindSortCursorVal / FlowSortValue coverage via end-to-end sort+paginate
//   - retentionHostMatches: empty-pattern matches-all branch
//   - buildBlocks / marshalBody / updateFirstTextInBody internal helpers (via findings round-trip)
//   - DeleteFlowsByHost + GCBodies interaction: purge flows for a host, GC orphans the bodies
//   - Finding Missing flag is set when a PoC flow is deleted via DeleteFlowsByHost

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// GCBodies — shared-hash dedup
// ---------------------------------------------------------------------------

// TestGCBodies_SharedHashKeptWhileOneFlowSurvives verifies that when two flows
// reference the same content-addressed body (identical hash via dedup), deleting
// one flow must NOT cause GC to remove the shared body file — the second flow
// still references it.
func TestGCBodies_SharedHashKeptWhileOneFlowSurvives(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Write the same content twice — dedup means one on-disk file.
	h1 := writeBody(t, s, "shared body content")
	h2 := writeBody(t, s, "shared body content") // identical → same hash
	if h1 != h2 {
		t.Fatalf("expected dedup: h1=%s h2=%s", h1, h2)
	}

	// Two flows, both referencing the same hash.
	id1 := mustInsertFlow(t, s, "a.com", h1, "", int64(len("shared body content")), 0)
	_ = mustInsertFlow(t, s, "b.com", h1, "", int64(len("shared body content")), 0)

	// Delete only the first flow — second still holds the reference.
	if _, err := s.DeleteFlows([]int64{id1}); err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}

	removed, freed, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("GC removed %d files; shared body still referenced by second flow", removed)
	}
	if freed != 0 {
		t.Fatalf("GC freed %d bytes; want 0", freed)
	}
}

// TestGCBodies_SharedHashBothFlowsDeleted confirms that once ALL flows referencing
// a shared body are deleted, GC does remove the body file.
func TestGCBodies_SharedHashBothFlowsDeleted(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	h := writeBody(t, s, "content referenced by two flows")
	id1 := mustInsertFlow(t, s, "a.com", h, "", int64(len("content referenced by two flows")), 0)
	id2 := mustInsertFlow(t, s, "b.com", h, "", int64(len("content referenced by two flows")), 0)

	if _, err := s.DeleteFlows([]int64{id1, id2}); err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}

	removed, freed, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 1 {
		t.Fatalf("GC removed %d files; want 1 (both flows deleted)", removed)
	}
	expectedBytes := int64(len("content referenced by two flows"))
	if freed != expectedBytes {
		t.Fatalf("GC freed %d bytes; want %d", freed, expectedBytes)
	}
}

// TestGCBodies_BodyReferencedAsReqAndResHash covers the case where a body is
// referenced as both req_body_hash on one flow and res_body_hash on another.
// Deleting one flow must not orphan the body while the other survives.
func TestGCBodies_BodyReferencedAsReqAndResHash(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	h := writeBody(t, s, "dual-role body")
	id1 := mustInsertFlow(t, s, "req.com", h, "", int64(len("dual-role body")), 0)   // req body
	_ = mustInsertFlow(t, s, "res.com", "", h, 0, int64(len("dual-role body")))       // res body

	// Delete the flow that uses it as req body.
	if _, err := s.DeleteFlows([]int64{id1}); err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}

	removed, _, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 0 {
		t.Fatalf("GC removed body still referenced by res flow; want 0 removals")
	}
}

// TestGCBodies_AfterDeleteFlowsByHostOrphansBody verifies the full
// DeleteFlowsByHost → GCBodies pipeline: pruning a host's flows makes their
// exclusive bodies eligible for GC.
func TestGCBodies_AfterDeleteFlowsByHostOrphansBody(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Two flows on different hosts, each with a unique body.
	hKeep := writeBody(t, s, "keep me")
	hPurge := writeBody(t, s, "purge me when host is deleted")
	mustInsertFlow(t, s, "keep.com", hKeep, "", int64(len("keep me")), 0)
	mustInsertFlow(t, s, "purge.com", hPurge, "", int64(len("purge me when host is deleted")), 0)

	// Delete all flows for purge.com.
	n, err := s.DeleteFlowsByHost([]string{"purge.com"}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 flow deleted, got %d", n)
	}

	removed, freed, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if removed != 1 {
		t.Fatalf("GC removed %d files; want 1 (orphaned by host purge)", removed)
	}
	if freed != int64(len("purge me when host is deleted")) {
		t.Fatalf("GC freed %d bytes; want %d", freed, len("purge me when host is deleted"))
	}
}

// ---------------------------------------------------------------------------
// retentionHostMatches — uncovered branches
// ---------------------------------------------------------------------------

// TestRetentionHostMatches_EmptyPatternMatchesAll exercises the empty-pattern
// "match any" branch that existing tests don't reach (retentionHostMatches is
// unexported, so we cover it via DeleteFlowsByHost with a wildcard-empty pattern
// by calling the function directly as we're in package store).
func TestRetentionHostMatches_EmptyPatternMatchesAll(t *testing.T) {
	// Empty pattern → match any host.
	if !retentionHostMatches("", "anything.example.com") {
		t.Error("empty pattern should match any host")
	}
	if !retentionHostMatches("", "") {
		t.Error("empty pattern should match empty host")
	}
}

func TestRetentionHostMatches_WildcardBaseDomain(t *testing.T) {
	// "*.example.com" should match "example.com" itself, plus any subdomain.
	cases := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"api.example.com", true},
		{"deep.api.example.com", true},
		{"notexample.com", false},
		{"example.com.evil.com", false},
	}
	for _, tc := range cases {
		got := retentionHostMatches("*.example.com", tc.host)
		if got != tc.want {
			t.Errorf("retentionHostMatches(*.example.com, %q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestRetentionHostMatches_CaseInsensitive(t *testing.T) {
	if !retentionHostMatches("EXAMPLE.COM", "example.com") {
		t.Error("pattern comparison should be case-insensitive")
	}
	if !retentionHostMatches("*.EXAMPLE.COM", "sub.example.com") {
		t.Error("wildcard pattern comparison should be case-insensitive")
	}
}

// ---------------------------------------------------------------------------
// QueryFlowsFilter — RequireFlags, HasNote, Tag, FlowIDs
// ---------------------------------------------------------------------------

func TestQueryFlowsFilter_RequireFlags(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mustInsertFlow(t, s, "h", "", "", 0, 0) // flags = 0
	if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "h", Path: "/scan",
		Flags: FlagActiveScan}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(3), Method: "GET", Host: "h", Path: "/repeat",
		Flags: FlagRepeater}); err != nil {
		t.Fatal(err)
	}

	// RequireFlags: only rows that have at least one of these bits.
	got, err := s.QueryFlowsFilter(FlowFilter{Limit: 10, RequireFlags: FlagActiveScan})
	if err != nil {
		t.Fatalf("QueryFlowsFilter: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/scan" {
		t.Fatalf("RequireFlags=FlagActiveScan: want 1 scan row, got %d", len(got))
	}

	// RequireFlags with multiple bits: any match is enough.
	got2, err := s.QueryFlowsFilter(FlowFilter{Limit: 10, RequireFlags: FlagActiveScan | FlagRepeater})
	if err != nil {
		t.Fatalf("QueryFlowsFilter: %v", err)
	}
	if len(got2) != 2 {
		t.Fatalf("RequireFlags multi-bit: want 2 rows, got %d", len(got2))
	}
}

func TestQueryFlowsFilter_HasNote(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id1 := mustInsertFlow(t, s, "h", "", "", 0, 0)
	id2 := mustInsertFlow(t, s, "h", "", "", 0, 0)
	_ = id2

	// Set a note on only the first flow.
	if err := s.SetFlowNote(id1, "interesting request"); err != nil {
		t.Fatalf("SetFlowNote: %v", err)
	}

	got, err := s.QueryFlowsFilter(FlowFilter{Limit: 10, HasNote: true})
	if err != nil {
		t.Fatalf("QueryFlowsFilter HasNote: %v", err)
	}
	if len(got) != 1 || got[0].ID != id1 {
		t.Fatalf("HasNote filter: want flow %d with note, got %d flows", id1, len(got))
	}
}

func TestQueryFlowsFilter_Tag(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id1 := mustInsertFlow(t, s, "h", "", "", 0, 0)
	id2 := mustInsertFlow(t, s, "h", "", "", 0, 0)

	// Tag only the first flow.
	if _, err := s.AddFlowTags(id1, []string{"interesting"}); err != nil {
		t.Fatalf("AddFlowTags: %v", err)
	}

	got, err := s.QueryFlowsFilter(FlowFilter{Limit: 10, Tag: "interesting"})
	if err != nil {
		t.Fatalf("QueryFlowsFilter Tag: %v", err)
	}
	if len(got) != 1 || got[0].ID != id1 {
		t.Fatalf("Tag filter: want flow %d, got %d flows", id1, len(got))
	}

	// Untagged flow must not appear.
	_ = id2
	got2, _ := s.QueryFlowsFilter(FlowFilter{Limit: 10, Tag: "non-existent-tag"})
	if len(got2) != 0 {
		t.Fatalf("Tag filter for missing tag: want 0 flows, got %d", len(got2))
	}
}

func TestQueryFlowsFilter_FlowIDs(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	id1 := mustInsertFlow(t, s, "h", "", "", 0, 0)
	id2 := mustInsertFlow(t, s, "h", "", "", 0, 0)
	id3 := mustInsertFlow(t, s, "h", "", "", 0, 0)

	got, err := s.QueryFlowsFilter(FlowFilter{Limit: 10, FlowIDs: []int64{id1, id3}})
	if err != nil {
		t.Fatalf("QueryFlowsFilter FlowIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("FlowIDs filter: want 2 flows, got %d", len(got))
	}
	// Both returned IDs must be in our requested set.
	wantSet := map[int64]bool{id1: true, id3: true}
	for _, f := range got {
		if !wantSet[f.ID] {
			t.Fatalf("FlowIDs filter returned unexpected ID %d (id2=%d should not appear)", f.ID, id2)
		}
	}
}

func TestQueryFlowsFilter_StatusClassBoundaries(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	for _, st := range []int{100, 199, 200, 299, 300, 399, 400, 499, 500, 599} {
		if _, err := s.InsertFlow(&Flow{
			TS: time.UnixMilli(int64(st)), Method: "GET", Host: "h", Path: "/" + time.UnixMilli(int64(st)).String(), Status: st,
		}); err != nil {
			t.Fatalf("InsertFlow status %d: %v", st, err)
		}
	}

	table := []struct {
		class int
		want  int
	}{
		{1, 2}, // 100, 199
		{2, 2}, // 200, 299
		{3, 2}, // 300, 399
		{4, 2}, // 400, 499
		{5, 2}, // 500, 599
	}
	for _, tc := range table {
		got, err := s.QueryFlowsFilter(FlowFilter{Limit: 100, StatusClass: tc.class})
		if err != nil {
			t.Fatalf("QueryFlowsFilter StatusClass %d: %v", tc.class, err)
		}
		if len(got) != tc.want {
			t.Fatalf("StatusClass %d: want %d flows, got %d", tc.class, tc.want, len(got))
		}
		// Every returned flow should be in the right class.
		lo, hi := tc.class*100, tc.class*100+100
		for _, f := range got {
			if f.Status < lo || f.Status >= hi {
				t.Fatalf("StatusClass %d: flow %d has status %d, out of range [%d,%d)", tc.class, f.ID, f.Status, lo, hi)
			}
		}
	}

	// StatusClass 0 (any) → all 10 rows.
	all, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100, StatusClass: 0})
	if len(all) != 10 {
		t.Fatalf("StatusClass 0 (any): want 10 flows, got %d", len(all))
	}
}

// ---------------------------------------------------------------------------
// Keyset pagination with non-id sort keys
// ---------------------------------------------------------------------------

// TestQueryFlowsFilter_SortByStatusPagination exercises the non-id keyset cursor
// path, covering bindSortCursorVal (integer binding for status) and
// appendFlowPageCursor's compound OR clause.
func TestQueryFlowsFilter_SortByStatusPagination(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Insert 6 flows with distinct statuses and deterministic timestamps.
	statuses := []int{100, 200, 301, 404, 500, 503}
	var inserted []*Flow
	for i, st := range statuses {
		f := &Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "h", Path: "/x", Status: st}
		id, err := s.InsertFlow(f)
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		f.ID = id
		inserted = append(inserted, f)
	}

	// Page 1: sort by status ASC, limit 2.
	page1, err := s.QueryFlowsFilter(FlowFilter{
		Limit: 2, SortKey: "status", SortDir: 1,
	})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: want 2 flows, got %d", len(page1))
	}
	if page1[0].Status != 100 || page1[1].Status != 200 {
		t.Fatalf("page1 order wrong: %v %v", page1[0].Status, page1[1].Status)
	}

	last1 := page1[len(page1)-1]
	// Page 2 via keyset cursor.
	page2, err := s.QueryFlowsFilter(FlowFilter{
		Limit:     2,
		SortKey:   "status",
		SortDir:   1,
		CursorID:  last1.ID,
		CursorVal: FlowSortValue(last1, "status"),
	})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: want 2 flows, got %d", len(page2))
	}
	if page2[0].Status != 301 || page2[1].Status != 404 {
		t.Fatalf("page2 order wrong: %v %v", page2[0].Status, page2[1].Status)
	}

	// Page 3 is the last page.
	last2 := page2[len(page2)-1]
	page3, err := s.QueryFlowsFilter(FlowFilter{
		Limit:     2,
		SortKey:   "status",
		SortDir:   1,
		CursorID:  last2.ID,
		CursorVal: FlowSortValue(last2, "status"),
	})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 2 {
		t.Fatalf("page3: want 2 flows, got %d", len(page3))
	}
	if page3[0].Status != 500 || page3[1].Status != 503 {
		t.Fatalf("page3 order wrong: %v %v", page3[0].Status, page3[1].Status)
	}
}

// TestQueryFlowsFilter_SortByHostPagination covers the string sort expression
// (lower(host)) and the string branch of bindSortCursorVal.
func TestQueryFlowsFilter_SortByHostPagination(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	hosts := []string{"alpha.com", "beta.com", "delta.com", "gamma.com"}
	var inserted []*Flow
	for i, h := range hosts {
		f := &Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: h, Path: "/x", Status: 200}
		id, err := s.InsertFlow(f)
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		f.ID = id
		inserted = append(inserted, f)
	}
	_ = inserted

	page1, err := s.QueryFlowsFilter(FlowFilter{Limit: 2, SortKey: "host", SortDir: 1})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].Host != "alpha.com" || page1[1].Host != "beta.com" {
		t.Fatalf("page1 host sort wrong: %v %v", page1[0].Host, page1[1].Host)
	}

	last := page1[len(page1)-1]
	page2, err := s.QueryFlowsFilter(FlowFilter{
		Limit:     2,
		SortKey:   "host",
		SortDir:   1,
		CursorID:  last.ID,
		CursorVal: FlowSortValue(last, "host"),
	})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].Host != "delta.com" || page2[1].Host != "gamma.com" {
		t.Fatalf("page2 host sort wrong: %v %v", page2[0].Host, page2[1].Host)
	}
}

// TestQueryFlowsFilter_SortBySizePagination covers the size (res_len) sort key
// and the integer binding path for "size" in bindSortCursorVal.
func TestQueryFlowsFilter_SortBySizePagination(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	sizes := []int64{100, 200, 300, 400}
	for i, sz := range sizes {
		if _, err := s.InsertFlow(&Flow{
			TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "h", Path: "/x",
			ResLen: sz, Status: 200,
		}); err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
	}

	page1, err := s.QueryFlowsFilter(FlowFilter{Limit: 2, SortKey: "size", SortDir: 1})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].ResLen != 100 || page1[1].ResLen != 200 {
		t.Fatalf("page1 size sort wrong: %v %v", page1[0].ResLen, page1[1].ResLen)
	}

	last := page1[len(page1)-1]
	page2, err := s.QueryFlowsFilter(FlowFilter{
		Limit:     2,
		SortKey:   "size",
		SortDir:   1,
		CursorID:  last.ID,
		CursorVal: FlowSortValue(last, "size"),
	})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ResLen != 300 || page2[1].ResLen != 400 {
		t.Fatalf("page2 size sort wrong: %v %v", page2[0].ResLen, page2[1].ResLen)
	}
}

// TestQueryFlowsFilter_SortByTimePagination covers the "time" sort key (ts column).
func TestQueryFlowsFilter_SortByTimePagination(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	base := int64(1_700_000_000_000) // fixed epoch ms
	var inserted []*Flow
	for i := 0; i < 4; i++ {
		f := &Flow{TS: time.UnixMilli(base + int64(i*1000)), Method: "GET", Host: "h", Path: "/x", Status: 200}
		id, err := s.InsertFlow(f)
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		f.ID = id
		inserted = append(inserted, f)
	}

	page1, err := s.QueryFlowsFilter(FlowFilter{Limit: 2, SortKey: "time", SortDir: 1})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: want 2 flows, got %d", len(page1))
	}
	if page1[0].TS.UnixMilli() != base || page1[1].TS.UnixMilli() != base+1000 {
		t.Fatalf("page1 time sort wrong: %v %v", page1[0].TS, page1[1].TS)
	}

	last := page1[len(page1)-1]
	page2, err := s.QueryFlowsFilter(FlowFilter{
		Limit:     2,
		SortKey:   "time",
		SortDir:   1,
		CursorID:  last.ID,
		CursorVal: FlowSortValue(last, "time"),
	})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: want 2 flows, got %d", len(page2))
	}
	if page2[0].TS.UnixMilli() != base+2000 || page2[1].TS.UnixMilli() != base+3000 {
		t.Fatalf("page2 time sort wrong: %v %v", page2[0].TS, page2[1].TS)
	}
}

// TestQueryFlowsFilter_SortByMimePagination covers the "mime" sort key.
func TestQueryFlowsFilter_SortByMimePagination(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	mimes := []string{"application/json", "image/png", "text/html", "text/plain"}
	for i, m := range mimes {
		if _, err := s.InsertFlow(&Flow{
			TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "h", Path: "/x", Mime: m, Status: 200,
		}); err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
	}

	page1, err := s.QueryFlowsFilter(FlowFilter{Limit: 2, SortKey: "mime", SortDir: 1})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 || page1[0].Mime != "application/json" || page1[1].Mime != "image/png" {
		t.Fatalf("page1 mime sort wrong: %q %q", page1[0].Mime, page1[1].Mime)
	}

	last := page1[len(page1)-1]
	page2, err := s.QueryFlowsFilter(FlowFilter{
		Limit:     2,
		SortKey:   "mime",
		SortDir:   1,
		CursorID:  last.ID,
		CursorVal: FlowSortValue(last, "mime"),
	})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 2 || page2[0].Mime != "text/html" || page2[1].Mime != "text/plain" {
		t.Fatalf("page2 mime sort wrong: %q %q", page2[0].Mime, page2[1].Mime)
	}
}

// ---------------------------------------------------------------------------
// Finding body helpers — marshalBody, buildBlocks, updateFirstTextInBody
// ---------------------------------------------------------------------------

// TestFindingBodyHelpers_MarshalAndBuildRoundTrip exercises marshalBody (0% coverage)
// and buildBlocks via the full findings API round-trip: create → read → verify blocks.
func TestFindingBodyHelpers_MarshalAndBuildRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	flowID, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/poc", Status: 200})

	// Create finding and attach a flow.
	fid, err := s.CreateFinding(&Finding{
		Severity: "High", Title: "test finding", Target: "t.com",
		Detail: "First text block",
	})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := s.AttachFlow(fid, flowID, "poc note", -1); err != nil {
		t.Fatalf("AttachFlow: %v", err)
	}

	got, err := s.GetFinding(fid)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}

	// Expect a text block + a flow block.
	var sawText, sawFlow bool
	for _, b := range got.Blocks {
		switch b.Type {
		case "text":
			if b.MD == "First text block" {
				sawText = true
			}
		case "flow":
			if b.FlowID == flowID && b.Note == "poc note" && !b.Missing {
				sawFlow = true
			}
		}
	}
	if !sawText {
		t.Fatalf("expected a text block with 'First text block', got: %+v", got.Blocks)
	}
	if !sawFlow {
		t.Fatalf("expected a flow block with flowID=%d, got: %+v", flowID, got.Blocks)
	}
}

// TestFindingBodyHelpers_UpdateFirstTextInBody verifies the updateFirstTextInBody
// code path (0% coverage): updating a finding's detail when it already has a body
// should sync the first text block's MD.
func TestFindingBodyHelpers_UpdateFirstTextInBody(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Create with initial detail.
	fid, err := s.CreateFinding(&Finding{Severity: "Low", Title: "update test", Detail: "original detail"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	// Update the detail (body != nil constraint: detail only, body=nil path).
	newDetail := "updated detail"
	if err := s.UpdateFinding(fid, nil, nil, nil, nil, &newDetail, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateFinding: %v", err)
	}

	got, err := s.GetFinding(fid)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}

	// The detail column must be updated.
	if got.Detail != "updated detail" {
		t.Fatalf("detail not updated: %q", got.Detail)
	}
	// The first text block in Blocks must reflect the new detail.
	foundUpdated := false
	for _, b := range got.Blocks {
		if b.Type == "text" && b.MD == "updated detail" {
			foundUpdated = true
		}
	}
	if !foundUpdated {
		t.Fatalf("first text block not updated in body: %+v", got.Blocks)
	}
}

// TestFindingBodyHelpers_NoBodyFallsBackToLegacy covers the legacy synthesis
// branch of buildBlocks: a finding that has Detail/Evidence but no Body uses
// the legacy path. We exercise this by directly calling buildBlocks.
func TestFindingBodyHelpers_NoBodyFallsBackToLegacy(t *testing.T) {
	flows := []FindingFlow{
		{FlowID: 1, Note: "poc", Method: "GET", Host: "h", Path: "/poc", Status: 200},
	}
	blocks := buildBlocks("", "detail text", "evidence text", flows)

	if len(blocks) != 3 {
		t.Fatalf("legacy synthesis: expected 3 blocks (detail+evidence+flow), got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != "text" || blocks[0].MD != "detail text" {
		t.Fatalf("block[0] wrong: %+v", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].MD != "evidence text" {
		t.Fatalf("block[1] wrong: %+v", blocks[1])
	}
	if blocks[2].Type != "flow" || blocks[2].FlowID != 1 || blocks[2].Note != "poc" {
		t.Fatalf("block[2] wrong: %+v", blocks[2])
	}
}

// TestFindingBodyHelpers_MarshalBodyDirectly calls marshalBody directly to cover
// the 0% branch — verifies it strips enriched metadata and roundtrips correctly.
func TestFindingBodyHelpers_MarshalBodyDirectly(t *testing.T) {
	input := []FindingBlock{
		{Type: "text", MD: "hello"},
		// Enriched fields that must NOT be stored.
		{Type: "flow", FlowID: 42, Note: "n", Method: "GET", Host: "h", Path: "/p", Status: 200, Missing: false},
	}
	out := marshalBody(input)
	if out == "" {
		t.Fatal("marshalBody: got empty string, want JSON")
	}

	// Rebuild via buildBlocks from the marshaled body (no flows → both flow blocks missing).
	blocks := buildBlocks(out, "", "", nil)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks after roundtrip, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != "text" || blocks[0].MD != "hello" {
		t.Fatalf("text block wrong after roundtrip: %+v", blocks[0])
	}
	if blocks[1].Type != "flow" || blocks[1].FlowID != 42 {
		t.Fatalf("flow block ID wrong after roundtrip: %+v", blocks[1])
	}
	// Enriched fields stripped at storage, not in output JSON → Missing because no flow metadata.
	if !blocks[1].Missing {
		t.Fatalf("flow block should be Missing when no flows metadata provided: %+v", blocks[1])
	}
}

// ---------------------------------------------------------------------------
// Missing flag via DeleteFlowsByHost (not just direct DELETE)
// ---------------------------------------------------------------------------

// TestMissingFlagAfterDeleteFlowsByHost verifies the Missing flag is set correctly
// when a PoC flow is removed via DeleteFlowsByHost (not just a raw DELETE).
// This tests the integration of the retention path with the finding_flows LEFT JOIN.
func TestMissingFlagAfterDeleteFlowsByHost(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	keepID, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "keep.com", Path: "/safe", Status: 200})
	goneID, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "purge.com", Path: "/poc", Status: 200})

	fid, err := s.CreateFinding(&Finding{Severity: "High", Title: "host purge test"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := s.AttachFlow(fid, keepID, "kept", -1); err != nil {
		t.Fatalf("AttachFlow keep: %v", err)
	}
	if err := s.AttachFlow(fid, goneID, "will be purged", -1); err != nil {
		t.Fatalf("AttachFlow gone: %v", err)
	}

	// Purge purge.com — goneID's flow row is deleted but finding_flows survives.
	n, err := s.DeleteFlowsByHost([]string{"purge.com"}, false)
	if err != nil {
		t.Fatalf("DeleteFlowsByHost: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 flow deleted, got %d", n)
	}

	got, err := s.GetFinding(fid)
	if err != nil {
		t.Fatalf("GetFinding: %v", err)
	}

	if len(got.Flows) != 2 {
		t.Fatalf("both attachment rows must survive host purge, got %d", len(got.Flows))
	}

	for _, fl := range got.Flows {
		switch fl.FlowID {
		case keepID:
			if fl.Missing {
				t.Fatalf("kept flow marked Missing: %+v", fl)
			}
		case goneID:
			if !fl.Missing {
				t.Fatalf("purged flow not marked Missing: %+v", fl)
			}
			if fl.Note != "will be purged" {
				t.Fatalf("purged flow annotation lost: %+v", fl)
			}
		default:
			t.Fatalf("unexpected flow ID %d", fl.FlowID)
		}
	}

	// Body blocks also surface Missing.
	var missingBlock *FindingBlock
	for i := range got.Blocks {
		if got.Blocks[i].Type == "flow" && got.Blocks[i].FlowID == goneID {
			missingBlock = &got.Blocks[i]
		}
	}
	if missingBlock == nil || !missingBlock.Missing {
		t.Fatalf("body flow block for purged flow should be Missing: %+v", got.Blocks)
	}
}

// ---------------------------------------------------------------------------
// FlowSortValue — all keys
// ---------------------------------------------------------------------------

// TestFlowSortValue_AllKeys verifies FlowSortValue returns the correct string for
// every sort key. This function was at 22% coverage.
func TestFlowSortValue_AllKeys(t *testing.T) {
	ts := time.UnixMilli(1_700_000_000_123)
	f := &Flow{
		ID:     42,
		TS:     ts,
		Method: "GET",
		Host:   "Example.COM",
		Path:   "/Foo/Bar",
		Status: 200,
		ResLen: 512,
		Mime:   "TEXT/HTML",
	}

	cases := []struct {
		key  string
		want string
	}{
		{"id", "42"},
		{"method", "GET"},
		{"host", "example.com"},
		{"path", "/foo/bar"},
		{"status", "200"},
		{"size", "512"},
		{"time", "1700000000123"},
		{"mime", "text/html"},
		{"unknown", "42"}, // falls back to id
	}

	for _, tc := range cases {
		got := FlowSortValue(f, tc.key)
		if got != tc.want {
			t.Errorf("FlowSortValue(%q): got %q, want %q", tc.key, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// NormalizeFlowSortKey — all valid + invalid keys
// ---------------------------------------------------------------------------

// TestNormalizeFlowSortKey covers all enumerated keys and the default fallback.
func TestNormalizeFlowSortKey_AllKeys(t *testing.T) {
	valid := []string{"method", "host", "path", "status", "size", "time", "mime"}
	for _, k := range valid {
		if got := NormalizeFlowSortKey(k); got != k {
			t.Errorf("NormalizeFlowSortKey(%q) = %q, want %q", k, got, k)
		}
		// upper-case should normalize to lower.
		upper := k
		for i := range upper {
			upper = upper[:i] + string(rune('A'+(upper[i]-'a'))) + upper[i+1:]
			break
		}
		if got := NormalizeFlowSortKey(upper); got != k {
			t.Errorf("NormalizeFlowSortKey(%q upper) = %q, want %q", upper, got, k)
		}
	}
	// Unknown key → "id".
	if got := NormalizeFlowSortKey("random"); got != "id" {
		t.Errorf("NormalizeFlowSortKey(random) = %q, want id", got)
	}
	if got := NormalizeFlowSortKey(""); got != "id" {
		t.Errorf("NormalizeFlowSortKey('') = %q, want id", got)
	}
}

// ---------------------------------------------------------------------------
// GCBodies — empty bodiesDir (no files at all)
// ---------------------------------------------------------------------------

func TestGCBodies_EmptyBodiesDir(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// No bodies written, no flows inserted.
	removed, freed, err := s.GCBodies()
	if err != nil {
		t.Fatalf("GCBodies on empty dir: %v", err)
	}
	if removed != 0 || freed != 0 {
		t.Fatalf("empty bodiesDir: removed=%d freed=%d, want 0,0", removed, freed)
	}
}
