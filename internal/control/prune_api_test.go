package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func pruneTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	h, s, _ := newHub(t)
	now := time.Now()
	for _, f := range []*store.Flow{
		{TS: now, Method: "GET", Scheme: "https", Host: "app.example", Port: 443, Path: "/index.html", Status: 200, Mime: "text/html", ResLen: 900},
		{TS: now, Method: "GET", Scheme: "https", Host: "app.example", Port: 443, Path: "/app.js", Status: 200, Mime: "application/javascript", ResLen: 5000},
		{TS: now, Method: "GET", Scheme: "https", Host: "cdn.example", Port: 443, Path: "/lib.css", Status: 200, Mime: "text/css", ResLen: 300},
	} {
		if _, err := s.InsertFlow(f); err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
	}
	return httptest.NewServer(h.Handler()), s
}

func postJSONPrune(t *testing.T, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestPurgeDryRunDeletesNothing(t *testing.T) {
	ts, s := pruneTestServer(t)
	defer ts.Close()

	resp, out := postJSONPrune(t, ts.URL+"/api/flows/purge", map[string]any{
		"hosts": []string{"app.example"}, "mode": "delete", "dryRun": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	if out["dryRun"] != true || out["matched"].(float64) != 2 || out["deleted"].(float64) != 0 {
		t.Fatalf("dry run = %v", out)
	}
	if n, _ := s.FlowCount(); n != 3 {
		t.Fatalf("dry run deleted flows: %d", n)
	}
}

func TestPurgeFiltersAndUndo(t *testing.T) {
	ts, s := pruneTestServer(t)
	defer ts.Close()

	resp, out := postJSONPrune(t, ts.URL+"/api/flows/purge", map[string]any{
		"hosts": []string{"app.example"}, "mode": "delete",
		"mimePrefixes": []string{"application/javascript"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
	if out["deleted"].(float64) != 1 {
		t.Fatalf("deleted = %v", out)
	}
	pruneID := int64(out["pruneId"].(float64))
	if pruneID == 0 {
		t.Fatalf("no pruneId: %v", out)
	}
	if n, _ := s.FlowCount(); n != 2 {
		t.Fatalf("count after prune = %d", n)
	}

	// The pending list advertises the restorable batch.
	listResp, err := http.Get(ts.URL + "/api/flows/prune/pending")
	if err != nil {
		t.Fatalf("GET pending: %v", err)
	}
	var pending struct {
		Prunes []store.PendingPrune `json:"prunes"`
	}
	_ = json.NewDecoder(listResp.Body).Decode(&pending)
	listResp.Body.Close()
	if len(pending.Prunes) != 1 || pending.Prunes[0].ID != pruneID || pending.Prunes[0].Flows != 1 {
		t.Fatalf("pending = %+v", pending.Prunes)
	}

	resp, out = postJSONPrune(t, ts.URL+"/api/flows/prune/undo", map[string]any{"pruneId": pruneID})
	if resp.StatusCode != http.StatusOK || out["restored"].(float64) != 1 {
		t.Fatalf("undo: %d %v", resp.StatusCode, out)
	}
	if n, _ := s.FlowCount(); n != 3 {
		t.Fatalf("count after undo = %d", n)
	}
}

func TestPurgeUndoRejectsUnknownPrune(t *testing.T) {
	ts, _ := pruneTestServer(t)
	defer ts.Close()
	resp, out := postJSONPrune(t, ts.URL+"/api/flows/prune/undo", map[string]any{"pruneId": 999})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d: %v", resp.StatusCode, out)
	}
}
