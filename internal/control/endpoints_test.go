package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/harx"
	"github.com/Veyal/interceptor/internal/store"
)

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

// A malformed JSON body is rejected with 400 rather than silently decoding to a
// zero value and flipping state (e.g. disarming the scanner). An empty body is
// still accepted (io.EOF tolerated) — that must not regress to 400.
func TestMalformedJSONBodyRejected(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	post := func(path, body string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if c := post("/api/activescan/arm", "{not json"); c != http.StatusBadRequest {
		t.Fatalf("malformed arm body: got %d, want 400", c)
	}
	if c := post("/api/activescan/arm", ""); c != http.StatusOK {
		t.Fatalf("empty arm body should still work (io.EOF tolerated): got %d, want 200", c)
	}
}

// GET /api/endpoints returns unique endpoints aggregated from history.
func TestEndpointsEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "a.com", Path: "/x", Status: 404})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "POST", Host: "a.com", Path: "/y", Status: 201})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(4), Method: "GET", Host: "a.com", Path: "/z", Status: 200, Flags: store.FlagActiveScan})

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
		t.Fatalf("got %d endpoints, want 2 (scan traffic excluded, hits collapsed)", len(out.Endpoints))
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
