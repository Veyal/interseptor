package store

import (
	"bytes"
	"compress/gzip"
	"testing"
	"time"
)

// anywhereSearchIDs runs an anywhere search the way the History list does:
// rewrite the filter, then page over it in SQL.
func anywhereSearchIDs(s *Store, f FlowFilter, maxScan int) ([]int64, string, error) {
	rewritten, note, err := s.AnywhereSearchFilter(f, maxScan)
	if err != nil {
		return nil, "", err
	}
	if rewritten.Limit == 0 {
		rewritten.Limit = 1000
	}
	flows, err := s.QueryFlowsListFilter(rewritten)
	if err != nil {
		return nil, "", err
	}
	ids := make([]int64, 0, len(flows))
	for _, fl := range flows {
		ids = append(ids, fl.ID)
	}
	return ids, note, nil
}

func gzipTestBody(t *testing.T, s *Store, body string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeAnywhereTestBody(t, s, buf.String())
}

func Test_FlowSearch_decodes_content_encoded_bodies(t *testing.T) {
	// Given: browsers send Accept-Encoding, so captured bodies stay gzip-encoded on disk.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	gz := map[string][]string{"Content-Encoding": {"gzip"}}
	resID, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/res", Status: 200, ResHeaders: gz, ResBodyHash: gzipTestBody(t, s, `{"secret":"Gz-Res-Needle"}`)})
	if err != nil {
		t.Fatal(err)
	}
	reqID, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "POST", Scheme: "https", Host: "example.com", Path: "/req", Status: 200, ReqHeaders: map[string][]string{"content-encoding": {"gzip"}}, ReqBodyHash: gzipTestBody(t, s, "gz-req-needle")})
	if err != nil {
		t.Fatal(err)
	}

	// When / Then
	for term, want := range map[string]int64{"gz-res-needle": resID, "GZ-REQ-NEEDLE": reqID} {
		bodyIDs, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: term}, 8000)
		if err != nil {
			t.Fatal(err)
		}
		anyIDs, _, err := anywhereSearchIDs(s, FlowFilter{Search: term}, 8000)
		if err != nil {
			t.Fatal(err)
		}
		if len(bodyIDs) != 1 || bodyIDs[0] != want || len(anyIDs) != 1 || anyIDs[0] != want {
			t.Fatalf("search %q: body=%v anywhere=%v, want [%d]", term, bodyIDs, anyIDs, want)
		}
	}
}

func Test_SearchFlowsAnywhere_matches_stored_fields_beyond_the_body_scan_bound(t *testing.T) {
	// Given: the matching flows are older than the body-scan window.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	oldHost, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "needle-host.example.com", Path: "/a", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	oldHeader, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "example.com", Path: "/b", Status: 200, ResHeaders: map[string][]string{"X-Trace": {"Needle-Host-Trace"}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(10 + i)), Method: "GET", Scheme: "https", Host: "example.com", Path: "/newer", Status: 200}); err != nil {
			t.Fatal(err)
		}
	}

	// When: sorted by a column, the body window stays on the newest flows and the
	// stored-field match covers all history in one page.
	ids, note, err := anywhereSearchIDs(s, FlowFilter{Search: "needle-host", SortKey: "path", SortDir: 1}, 2)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != oldHost || ids[1] != oldHeader {
		t.Fatalf("ids = %v, want [%d %d]", ids, oldHost, oldHeader)
	}
	if note == "" {
		t.Fatal("want a note that the body scan was bounded")
	}
}

func Test_SearchFlowsAnywhere_id_pages_stop_at_the_scan_bound_and_continue_from_it(t *testing.T) {
	// Given (ids ascending): an old metadata match, a body match, then newer flows
	// that fill the body-scan window.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	meta, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "pager-needle.example.com", Path: "/", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	body, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "POST", Scheme: "https", Host: "example.com", Path: "/b", Status: 200, ResBodyHash: writeAnywhereTestBody(t, s, "pager-needle")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(int64(10 + i)), Method: "GET", Scheme: "https", Host: "example.com", Path: "/newer", Status: 200}); err != nil {
			t.Fatal(err)
		}
	}

	// When: page newest-first the way History does, continuing from the bound
	// whenever a page ends there.
	var got []int64
	cursor := int64(0)
	for page := 0; page < 10; page++ {
		f, _, err := s.AnywhereSearchFilter(FlowFilter{Search: "pager-needle", CursorID: cursor}, 2)
		if err != nil {
			t.Fatal(err)
		}
		f.Limit = 100
		rows, err := s.QueryFlowsListFilter(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			got = append(got, r.ID)
		}
		if f.ScanBoundID == 0 {
			break
		}
		cursor = f.ScanBoundID
	}

	// Then: the body match is not skipped in favour of the older metadata match.
	if len(got) != 2 || got[0] != body || got[1] != meta {
		t.Fatalf("paged ids = %v, want [%d %d]", got, body, meta)
	}
}

func Test_FlowSearch_body_window_follows_the_id_page_cursor(t *testing.T) {
	// Given: one old body match and newer non-matching flows past the scan bound.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	match, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/old", Status: 200, ResBodyHash: writeAnywhereTestBody(t, s, "deep-needle")})
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	for i := 0; i < 4; i++ {
		if last, err = s.InsertFlow(&Flow{TS: time.UnixMilli(int64(10 + i)), Method: "GET", Scheme: "https", Host: "example.com", Path: "/newer", Status: 200}); err != nil {
			t.Fatal(err)
		}
	}

	// When: the newest window misses it; a page cursor below that window and the
	// oldest-first order both reach it.
	first, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "deep-needle"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	paged, _, _, err := s.FlowIDsBodySearch(FlowFilter{Search: "deep-needle", CursorID: last - 2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	ascending, _, err := anywhereSearchIDs(s, FlowFilter{Search: "deep-needle", SortDir: 1}, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if len(first) != 0 {
		t.Fatalf("first page ids = %v, want none inside the newest window", first)
	}
	if len(paged) != 1 || paged[0] != match {
		t.Fatalf("paged ids = %v, want [%d]", paged, match)
	}
	if len(ascending) != 1 || ascending[0] != match {
		t.Fatalf("ascending ids = %v, want [%d] from the oldest window", ascending, match)
	}
}
