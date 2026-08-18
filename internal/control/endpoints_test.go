package control

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/harx"
	"github.com/Veyal/interseptor/internal/store"
)

const legacyActiveScanBit int64 = 1 << 9

// Tag endpoints: set tags on a flow, see them on the flow + list filter, list
// distinct tags with counts, and set a color.
func TestTagEndpoints(t *testing.T) {
	h, s, _ := newHub(t)
	f1, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/1", Status: 200})
	f2, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "b.com", Path: "/2", Status: 200})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	put := func(path, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+path, strings.NewReader(body))
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT %s: %v", path, err)
		}
		return r
	}

	// Set tags on f1.
	put("/api/flows/"+itoa(f1)+"/tags", `{"tags":["Auth","IDOR"]}`).Body.Close()
	// Bulk-add a shared tag to both.
	bp, _ := http.Post(ts.URL+"/api/flows/tags", "application/json",
		strings.NewReader(`{"flowIds":[`+itoa(f1)+`,`+itoa(f2)+`],"add":["recon"]}`))
	bp.Body.Close()
	// Bulk-remove recon from f2 only.
	br, _ := http.Post(ts.URL+"/api/flows/tags", "application/json",
		strings.NewReader(`{"flowIds":[`+itoa(f2)+`],"remove":["recon"]}`))
	br.Body.Close()

	// Filter History by tag=idor → only f1.
	r, _ := http.Get(ts.URL + "/api/flows?tag=idor")
	var fl struct {
		Flows []struct {
			ID   int64    `json:"id"`
			Tags []string `json:"tags"`
		} `json:"flows"`
	}
	json.NewDecoder(r.Body).Decode(&fl)
	r.Body.Close()
	if len(fl.Flows) != 1 || fl.Flows[0].ID != f1 {
		t.Fatalf("tag=idor should match only f1, got %+v", fl.Flows)
	}
	if len(fl.Flows[0].Tags) != 3 { // auth, idor, recon (sorted)
		t.Fatalf("f1 tags = %v", fl.Flows[0].Tags)
	}

	// List distinct tags: recon=1 (f2 lost it), auth=1, idor=1.
	r2, _ := http.Get(ts.URL + "/api/tags")
	var tl struct {
		Tags []store.TagCount `json:"tags"`
	}
	json.NewDecoder(r2.Body).Decode(&tl)
	r2.Body.Close()
	if len(tl.Tags) != 3 || tl.Tags[0].Tag != "auth" || tl.Tags[0].Count != 1 {
		t.Fatalf("DistinctTags after remove = %+v", tl.Tags)
	}
	recon := tl.Tags[2]
	if recon.Tag != "recon" || recon.Count != 1 {
		t.Fatalf("recon count after remove = %+v", recon)
	}

	// Set a color; reject a bad one.
	if rc := put("/api/tags/recon/color", `{"color":"#4aa8ff"}`); rc.StatusCode != http.StatusNoContent {
		t.Fatalf("set color: %d", rc.StatusCode)
	}
	if rc := put("/api/tags/recon/color", `{"color":"javascript:alert(1)"}`); rc.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad color should be rejected, got %d", rc.StatusCode)
	}
}

func TestFlowMetadataMutationsRejectTrailingJSONBeforeChangingState(t *testing.T) {
	put := func(t *testing.T, url, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT: %v", err)
		}
		return resp
	}

	t.Run("note", func(t *testing.T) {
		h, st, _ := newHub(t)
		id, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/"})
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		if err := st.SetFlowNote(id, "old note"); err != nil {
			t.Fatalf("SetFlowNote: %v", err)
		}
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp := put(t, ts.URL+"/api/flows/"+itoa(id)+"/note", `{"note":"new note"}{}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		flow, err := st.GetFlow(id)
		if err != nil {
			t.Fatalf("GetFlow: %v", err)
		}
		if flow.Note != "old note" {
			t.Fatalf("note = %q, want old note", flow.Note)
		}
	})

	t.Run("flow tags", func(t *testing.T) {
		h, st, _ := newHub(t)
		id, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/"})
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		if _, err := st.SetFlowTags(id, []string{"old"}); err != nil {
			t.Fatalf("SetFlowTags: %v", err)
		}
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp := put(t, ts.URL+"/api/flows/"+itoa(id)+"/tags", `{"tags":["new"]}{}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		tags, err := st.FlowTags(id)
		if err != nil {
			t.Fatalf("FlowTags: %v", err)
		}
		if len(tags) != 1 || tags[0] != "old" {
			t.Fatalf("tags = %v, want [old]", tags)
		}
	})

	t.Run("tag color", func(t *testing.T) {
		h, st, _ := newHub(t)
		id, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/"})
		if err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
		if _, err := st.SetFlowTags(id, []string{"recon"}); err != nil {
			t.Fatalf("SetFlowTags: %v", err)
		}
		if err := st.SetTagColor("recon", "#112233"); err != nil {
			t.Fatalf("SetTagColor: %v", err)
		}
		ts := httptest.NewServer(h.Handler())
		defer ts.Close()

		resp := put(t, ts.URL+"/api/tags/recon/color", `{"color":"#445566"}{}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		tags, err := st.DistinctTags()
		if err != nil {
			t.Fatalf("DistinctTags: %v", err)
		}
		if len(tags) != 1 || tags[0].Tag != "recon" || tags[0].Color != "#112233" {
			t.Fatalf("tag state changed after rejected color update: %+v", tags)
		}
	})
}

func TestBulkFlowTagsRollBackWholeSelectionOnFailure(t *testing.T) {
	h, st, _ := newHub(t)
	first, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "one.example.com", Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "two.example.com", Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(filepath.Dir(st.BodiesDir()), "interseptor.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_second_bulk_tag BEFORE INSERT ON flow_tags
		WHEN NEW.flow_id = ` + itoa(second) + ` AND NEW.tag = 'batch'
		BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/flows/tags", "application/json",
		strings.NewReader(`{"flowIds":[`+itoa(first)+`,`+itoa(second)+`],"add":["batch"]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	for _, id := range []int64{first, second} {
		tags, err := st.FlowTags(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(tags) != 0 {
			t.Errorf("flow %d retained partial tags %v", id, tags)
		}
	}
}

// Importing a HAR must invalidate the endpoints cache, otherwise the Map tab
// keeps showing the pre-import aggregate until the next live capture.
func TestImportHARInvalidatesEndpointsCache(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	endpointCount := func() int {
		resp, err := http.Get(ts.URL + "/api/endpoints")
		if err != nil {
			t.Fatalf("GET endpoints: %v", err)
		}
		defer resp.Body.Close()
		var out struct {
			Endpoints []json.RawMessage `json:"endpoints"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		return len(out.Endpoints)
	}

	if c := endpointCount(); c != 0 { // prime the cache while empty
		t.Fatalf("expected 0 endpoints initially, got %d", c)
	}

	har := harx.Build([]*store.Flow{{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https",
		Host: "imported.example", Port: 443, Path: "/x", HTTPVersion: "HTTP/1.1", Status: 200,
	}}, func(string) []byte { return nil })
	resp, err := http.Post(ts.URL+"/api/import/har", "application/json", bytes.NewReader(har))
	if err != nil {
		t.Fatalf("import HAR: %v", err)
	}
	resp.Body.Close()

	if c := endpointCount(); c == 0 {
		t.Fatal("endpoints cache stale after HAR import — epsCache.invalidate() missing")
	}
}

func TestRemovedActiveRESTRoutesAreUnavailable(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/activescan"},
		{http.MethodPost, "/api/activescan/start"},
		{http.MethodGet, "/api/active-checks"},
		{http.MethodPost, "/api/active-checks/test"},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// GET /api/endpoints returns unique endpoints aggregated from history.
func TestEndpointsEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "a.com", Path: "/x", Status: 404})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "POST", Host: "a.com", Path: "/y", Status: 201})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(4), Method: "GET", Host: "a.com", Path: "/z", Status: 200, Flags: store.FlagIntruder})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/endpoints")
	if err != nil {
		t.Fatalf("GET endpoints: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Endpoints []struct {
			Host string `json:"host"`
			Path string `json:"path"`
			Hits int    `json:"hits"`
		} `json:"endpoints"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Endpoints) != 2 {
		t.Fatalf("got %d endpoints, want 2 (tool traffic excluded, hits collapsed)", len(out.Endpoints))
	}
}

// A bulk delete with an absurd ids array is rejected before it amplifies into a
// ~10× allocation (make([]any,len)+placeholders). A normal delete still works.
func TestDeleteFlowsRejectsHugeIDArray(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/x"})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	post := func(ids []int64) int {
		b, _ := json.Marshal(map[string]any{"ids": ids})
		resp, err := http.Post(ts.URL+"/api/flows/delete", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	huge := make([]int64, maxBulkItems+1)
	if c := post(huge); c != http.StatusBadRequest {
		t.Fatalf("oversized id array: got %d, want 400", c)
	}
	if c := post([]int64{id}); c != http.StatusOK {
		t.Fatalf("normal delete: got %d, want 200", c)
	}
}

// GET /api/flows?onlyAi=1 (or manual=0&ai=1) returns only AI-originated flows (FlagAI).
func TestListFlowsOnlyAi(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/human", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "a.com", Path: "/ai", Status: 200, Flags: store.FlagRepeater | store.FlagAI})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, q := range []string{"onlyAi=1", "manual=0&ai=1"} {
		resp, err := http.Get(ts.URL + "/api/flows?" + q)
		if err != nil {
			t.Fatalf("GET %s: %v", q, err)
		}
		var out struct {
			Flows []struct {
				Path string `json:"path"`
			} `json:"flows"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if len(out.Flows) != 1 || out.Flows[0].Path != "/ai" {
			t.Fatalf("%s should return only the AI flow, got %+v", q, out.Flows)
		}
	}
}

func TestListFlowsManualOnly(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/human", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "a.com", Path: "/ai", Status: 200, Flags: store.FlagRepeater | store.FlagAI})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows?manual=1&ai=0")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []struct {
			Path string `json:"path"`
		} `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Flows) != 1 || out.Flows[0].Path != "/human" {
		t.Fatalf("manual=1&ai=0 should return only the human flow, got %+v", out.Flows)
	}
}

func TestListFlowsHideTlsFailed(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "CONNECT", Host: "ok.test", Path: "/", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "CONNECT", Host: "pin.test", Path: "/", Flags: store.FlagTLSFailed, Error: "tls fail"})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows?hideTlsFailed=1")
	if err != nil {
		t.Fatalf("GET hideTlsFailed: %v", err)
	}
	defer resp.Body.Close()
	var hidden struct {
		Flows []struct {
			Host string `json:"host"`
		} `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&hidden)
	if len(hidden.Flows) != 1 || hidden.Flows[0].Host != "ok.test" {
		t.Fatalf("hideTlsFailed=1 should drop PIN rows, got %+v", hidden.Flows)
	}

	resp2, err := http.Get(ts.URL + "/api/flows?tlsFailed=1")
	if err != nil {
		t.Fatalf("GET tlsFailed: %v", err)
	}
	defer resp2.Body.Close()
	var only struct {
		Flows []struct {
			Host string `json:"host"`
		} `json:"flows"`
	}
	json.NewDecoder(resp2.Body).Decode(&only)
	if len(only.Flows) != 1 || only.Flows[0].Host != "pin.test" {
		t.Fatalf("tlsFailed=1 should return only PIN rows, got %+v", only.Flows)
	}
}

// GET /api/flows?limit=<bad> must not panic on the truncation reslice. A
// negative limit previously produced flows[:limit] -> "slice bounds out of
// range" and a recovered 500. Bad limits now fall back to the default.
func TestListFlowsBadLimit(t *testing.T) {
	h, s, _ := newHub(t)
	for i := 0; i < 3; i++ {
		s.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, lim := range []string{"-1", "0", "-999999"} {
		resp, err := http.Get(ts.URL + "/api/flows?limit=" + lim)
		if err != nil {
			t.Fatalf("GET flows limit=%s: %v", lim, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("limit=%s: got status %d, want 200", lim, resp.StatusCode)
		}
		var out struct {
			Flows []json.RawMessage `json:"flows"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			t.Fatalf("limit=%s: decode: %v", lim, err)
		}
		resp.Body.Close()
		if len(out.Flows) != 3 {
			t.Fatalf("limit=%s: got %d flows, want 3", lim, len(out.Flows))
		}
	}
}

// TestListFlowsPreservesLegacyActiveRows verifies old active-scan rows remain
// readable after the active-scan flag loses its special interpretation.
func TestListFlowsPreservesLegacyActiveRows(t *testing.T) {
	h, s, _ := newHub(t)
	proxyID, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/proxy", Status: 200})
	legacyID, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "a.com", Path: "/scan", Status: 200, Flags: legacyActiveScanBit})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows")
	if err != nil {
		t.Fatalf("GET /api/flows: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
		} `json:"flows"`
		Truncated bool `json:"truncated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Flows) != 2 || out.Flows[0].ID != legacyID || out.Flows[0].Path != "/scan" || out.Flows[1].ID != proxyID {
		t.Fatalf("default list should preserve both flows, got %+v", out.Flows)
	}

	statsResp, err := http.Get(ts.URL + "/api/hosts/stats")
	if err != nil {
		t.Fatalf("GET /api/hosts/stats: %v", err)
	}
	defer statsResp.Body.Close()
	var stats struct {
		TotalFlows int64 `json:"totalFlows"`
	}
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.TotalFlows != 2 {
		t.Fatalf("host_stats should count both flows, got totalFlows=%d", stats.TotalFlows)
	}
}

// TestListFlowsIncludeToolsQuery returns Repeater/Intruder traffic when
// includeTools=1 is set — the escape hatch MCP agents need after a tool run.
func TestListFlowsIncludeToolsQuery(t *testing.T) {
	h, s, _ := newHub(t)
	_, _ = s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/proxy", Status: 200})
	_, _ = s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "a.com", Path: "/scan", Status: 200, Flags: legacyActiveScanBit})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows?includeTools=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows []struct {
			Path string `json:"path"`
		} `json:"flows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Flows) != 2 {
		t.Fatalf("includeTools=1 should return both flows, got %+v", out.Flows)
	}
}

// TestListFlowsShowsLegacyActiveRowByDefault verifies a legacy active-scan row
// is treated as ordinary history after active-scan filtering is removed.
func TestListFlowsShowsLegacyActiveRowByDefault(t *testing.T) {
	h, s, _ := newHub(t)
	_, _ = s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/scan", Status: 200, Flags: legacyActiveScanBit})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/flows")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Flows     []any `json:"flows"`
		Truncated bool  `json:"truncated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Flows) != 1 || out.Truncated {
		t.Fatalf("default list should preserve the legacy row with truncated=false, got %+v", out)
	}

	resp2, err := http.Get(ts.URL + "/api/flows?includeTools=1")
	if err != nil {
		t.Fatalf("GET includeTools: %v", err)
	}
	defer resp2.Body.Close()
	var out2 struct {
		Flows []any `json:"flows"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out2); err != nil {
		t.Fatal(err)
	}
	if len(out2.Flows) != 1 {
		t.Fatalf("includeTools=1 should return the scan flow, got %+v", out2.Flows)
	}
}
