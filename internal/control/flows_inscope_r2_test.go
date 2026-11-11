package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/harx"
	"github.com/Veyal/interseptor/internal/store"
)

// seedInScopeBehindNoise stores `inScope` old in-scope flows followed by `noise`
// newer out-of-scope ones, with an include rule for the in-scope host. 7,600 noise
// rows are past what one bounded scan reads for a small page (25 batches of 300).
func seedInScopeBehindNoise(t *testing.T, h *Hub, st *store.Store, inScope, noise int) {
	t.Helper()
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "target.example.com"}); err != nil {
		t.Fatal(err)
	}
	h.refreshScope()
	insert := func(host string, i int) {
		if _, err := st.InsertFlow(&store.Flow{
			TS: time.UnixMilli(int64(i + 1)), Method: "GET", Scheme: "https", Host: host, Port: 443,
			Path: fmt.Sprintf("/p%d", i), HTTPVersion: "HTTP/1.1", Status: 200,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < inScope; i++ {
		insert("target.example.com", i)
	}
	for i := 0; i < noise; i++ {
		insert("noise.example.net", inScope+i)
	}
}

type inScopePage struct {
	Flows []struct {
		ID   int64  `json:"id"`
		Host string `json:"host"`
	} `json:"flows"`
	Truncated       bool   `json:"truncated"`
	SearchMore      bool   `json:"searchMore"`
	SearchCursor    int64  `json:"searchCursor"`
	SearchCursorVal string `json:"searchCursorVal"`
	ScanStopped     bool   `json:"scanStopped"`
}

func getInScopePage(t *testing.T, base string, q url.Values) inScopePage {
	t.Helper()
	resp, err := http.Get(base + "/api/flows?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/flows?%s: status %d", q.Encode(), resp.StatusCode)
	}
	var page inScopePage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

// followInScope pages the way the History client does: from the last listed row,
// or from searchCursor when the server says the scan stopped there (curId only,
// which is all the current client sends).
func followInScope(t *testing.T, base string, q url.Values) (ids []int64, requests int, sawStop bool) {
	t.Helper()
	for requests = 1; requests <= 40; requests++ {
		page := getInScopePage(t, base, q)
		for _, f := range page.Flows {
			if f.Host != "target.example.com" {
				t.Fatalf("out-of-scope flow %d (%s) listed", f.ID, f.Host)
			}
			ids = append(ids, f.ID)
		}
		if !page.SearchMore {
			if page.Truncated {
				t.Fatalf("page says truncated but gives no cursor to continue from (%d flows listed)", len(page.Flows))
			}
			return ids, requests, sawStop
		}
		sawStop = sawStop || page.ScanStopped
		if page.SearchCursor <= 0 {
			t.Fatal("searchMore without a searchCursor")
		}
		q.Set("curId", strconv.FormatInt(page.SearchCursor, 10))
		q.Del("curVal")
	}
	t.Fatal("in-scope paging did not terminate")
	return nil, 0, false
}

// C3: with more out-of-scope rows than one bounded scan reads in front of them,
// in-scope flows were unreachable: the page came back empty with truncated=true and
// no position to continue from.
func TestInScopeHistoryScanIsResumable(t *testing.T) {
	h, st, _ := newHub(t)
	seedInScopeBehindNoise(t, h, st, 3, 7600)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	ids, requests, sawStop := followInScope(t, ts.URL, url.Values{"inScope": {"1"}, "limit": {"10"}})
	if len(ids) != 3 || ids[0] != 3 || ids[2] != 1 {
		t.Fatalf("in-scope flows reached by paging: %v, want [3 2 1]", ids)
	}
	if !sawStop || requests < 2 {
		t.Fatalf("expected a stopped scan to be reported and resumed (requests=%d, scanStopped seen=%v)", requests, sawStop)
	}
}

// The scan cursor also works for a non-id sort: the client sends only curId, and
// the server reads that row's sort value to finish the keyset.
func TestInScopeHistoryScanResumesNonIDSort(t *testing.T) {
	h, st, _ := newHub(t)
	// Ascending host order lists noise.example.net before target.example.com.
	seedInScopeBehindNoise(t, h, st, 3, 7600)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	q := url.Values{"inScope": {"1"}, "limit": {"10"}, "sort": {"host"}, "dir": {"asc"}}
	first := getInScopePage(t, ts.URL, q)
	if !first.ScanStopped || first.SearchCursorVal != "noise.example.net" {
		t.Fatalf("first page: scanStopped=%v searchCursorVal=%q, want a stopped scan at the noise host", first.ScanStopped, first.SearchCursorVal)
	}
	ids, _, _ := followInScope(t, ts.URL, q)
	if len(ids) != 3 {
		t.Fatalf("host-sorted in-scope paging reached %v, want the 3 in-scope flows", ids)
	}
}

// A full in-scope page that ends inside the scan budget pages on from its last
// row exactly as before: no scan cursor, truncated=true.
func TestInScopeHistoryFullPageKeepsRowCursor(t *testing.T) {
	h, st, _ := newHub(t)
	seedInScopeBehindNoise(t, h, st, 30, 5)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	page := getInScopePage(t, ts.URL, url.Values{"inScope": {"1"}, "limit": {"10"}})
	if len(page.Flows) != 10 || !page.Truncated || page.SearchMore || page.ScanStopped {
		t.Fatalf("full page: %d flows truncated=%v searchMore=%v scanStopped=%v", len(page.Flows), page.Truncated, page.SearchMore, page.ScanStopped)
	}
	last := getInScopePage(t, ts.URL, url.Values{"inScope": {"1"}, "limit": {"50"}})
	if len(last.Flows) != 30 || last.Truncated || last.SearchMore {
		t.Fatalf("complete listing: %d flows truncated=%v searchMore=%v, want 30/false/false", len(last.Flows), last.Truncated, last.SearchMore)
	}
}

// The HAR export has no caller-side paging, so it must read to the end of history.
func TestExportHARInScopeReadsPastScanBudget(t *testing.T) {
	h, st, _ := newHub(t)
	seedInScopeBehindNoise(t, h, st, 3, 7600)

	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportHAR(rec, httptest.NewRequest(http.MethodGet, "/api/export/har?inScope=1&limit=5", nil))
	entries, err := harx.Parse(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("parse exported HAR: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("in-scope export returned %d entries, want the 3 in-scope flows behind 7,600 out-of-scope rows", len(entries))
	}
}

// /api/flows/inscope (and readiness) answered "no in-scope traffic" when the
// in-scope flows sat behind more out-of-scope rows than the scan budget.
func TestHasInScopeTrafficReadsPastScanBudget(t *testing.T) {
	h, st, _ := newHub(t)
	seedInScopeBehindNoise(t, h, st, 1, 7600)
	if !h.hasInScopeTraffic() {
		t.Fatal("hasInScopeTraffic = false with an in-scope flow behind 7,600 out-of-scope rows")
	}
}

// The full-history scan is paid once per scope: a repeat call only reads flows
// captured since, and the memo follows scope changes, new traffic and deletes.
func TestHasInScopeTrafficMemoFollowsChanges(t *testing.T) {
	h, st, _ := newHub(t)
	seedInScopeBehindNoise(t, h, st, 0, 700)
	if h.hasInScopeTraffic() {
		t.Fatal("nothing is in scope yet")
	}
	if h.inScopeMemo.scannedTo != 700 {
		t.Fatalf("memo scannedTo = %d, want 700 (the newest scanned id)", h.inScopeMemo.scannedTo)
	}
	if h.hasInScopeTraffic() {
		t.Fatal("repeat call changed the answer")
	}
	// A new in-scope flow is found although everything older is memoised away.
	id, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(9000), Method: "GET", Scheme: "https", Host: "target.example.com", Port: 443, Path: "/new", HTTPVersion: "HTTP/1.1", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !h.hasInScopeTraffic() || !h.hasInScopeTraffic() {
		t.Fatal("new in-scope flow not seen")
	}
	// Deleting the remembered flow must not leave a stale "yes".
	if _, err := st.DeleteFlows([]int64{id}); err != nil {
		t.Fatal(err)
	}
	if h.hasInScopeTraffic() {
		t.Fatal("stale positive after the in-scope flow was deleted")
	}
	// A bulk change (import, merge, prune undo) can put in-scope flows below the
	// scanned mark, e.g. a restored prune keeps its ids: the memo is dropped.
	if h.inScopeMemo.scannedTo == 0 {
		t.Fatal("expected a remembered negative before the bulk change")
	}
	h.epsCache.invalidate()
	h.hasInScopeTraffic()
	if h.inScopeMemo.bulkGen != h.epsCache.bulkGeneration() {
		t.Fatal("memo not recomputed after a bulk flow change")
	}
	// A scope change invalidates a remembered "no".
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "noise.example.net"}); err != nil {
		t.Fatal(err)
	}
	h.refreshScope()
	if !h.hasInScopeTraffic() {
		t.Fatal("stale negative after the scope rules changed")
	}
}
