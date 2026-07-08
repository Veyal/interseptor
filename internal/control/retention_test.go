package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// POST /api/flows/purge with mode=delete removes matching flows, runs GC,
// and returns the right counts.
func TestPurgeFlowsByHostDelete(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "noise.com", Path: "/", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "noise.com", Path: "/2", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "GET", Host: "keep.com", Path: "/", Status: 200})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"hosts":["noise.com"],"mode":"delete"}`
	resp, err := http.Post(ts.URL+"/api/flows/purge", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST purge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("purge status %d, want 200", resp.StatusCode)
	}
	var out struct {
		Deleted      int64 `json:"deleted"`
		RemovedFiles int64 `json:"removedFiles"`
		FreedBytes   int64 `json:"freedBytes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Deleted != 2 {
		t.Fatalf("deleted %d, want 2", out.Deleted)
	}

	// keep.com flow must still be there.
	if n := flowCount(t, ts.URL+"/api/flows"); n != 1 {
		t.Fatalf("remaining flows = %d, want 1", n)
	}
}

// POST /api/flows/purge with mode=keepOnly keeps the listed hosts, deletes everything else.
func TestPurgeFlowsKeepOnly(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "keep.com", Path: "/", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "noise.com", Path: "/", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "GET", Host: "also-noise.com", Path: "/", Status: 200})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"hosts":["keep.com"],"mode":"keepOnly"}`
	resp, err := http.Post(ts.URL+"/api/flows/purge", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST purge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("purge keepOnly status %d, want 200", resp.StatusCode)
	}
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Deleted != 2 {
		t.Fatalf("deleted %d, want 2", out.Deleted)
	}

	if n := flowCount(t, ts.URL+"/api/flows"); n != 1 {
		t.Fatalf("remaining flows = %d, want 1", n)
	}
}

// POST /api/flows/purge with mode=keepOnly and empty hosts → 400.
func TestPurgeFlowsKeepOnlyEmptyHosts(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "x.com", Path: "/", Status: 200})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"hosts":[],"mode":"keepOnly"}`
	resp, err := http.Post(ts.URL+"/api/flows/purge", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST purge: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty keepOnly: want 400, got %d", resp.StatusCode)
	}

	// Flow must not have been deleted.
	if n := flowCount(t, ts.URL+"/api/flows"); n != 1 {
		t.Fatalf("flows after bad purge = %d, want 1", n)
	}
}

// POST /api/flows/gc runs GC and returns removedFiles + freedBytes (both 0
// when there's nothing to collect, which is fine).
func TestGCEndpoint(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/flows/gc", "application/json", nil)
	if err != nil {
		t.Fatalf("POST gc: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gc status %d, want 200", resp.StatusCode)
	}
	var out struct {
		RemovedFiles int64 `json:"removedFiles"`
		FreedBytes   int64 `json:"freedBytes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	// Values are >= 0; we just check the shape is present.
	if out.RemovedFiles < 0 || out.FreedBytes < 0 {
		t.Fatalf("gc returned negative counts: %+v", out)
	}
}

// GET /api/hosts/stats returns per-host rows plus totalFlows/totalBytes.
func TestHostStatsEndpoint(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "big.com", Path: "/", Status: 200, ReqLen: 100, ResLen: 900})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "big.com", Path: "/b", Status: 200, ReqLen: 50, ResLen: 50})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "GET", Host: "small.com", Path: "/", Status: 200, ReqLen: 10, ResLen: 10})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/hosts/stats")
	if err != nil {
		t.Fatalf("GET hosts/stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hosts/stats status %d, want 200", resp.StatusCode)
	}
	var out struct {
		Hosts []struct {
			Host  string `json:"host"`
			Flows int64  `json:"flows"`
			Bytes int64  `json:"bytes"`
		} `json:"hosts"`
		TotalFlows int64 `json:"totalFlows"`
		TotalBytes int64 `json:"totalBytes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)

	if len(out.Hosts) != 2 {
		t.Fatalf("hosts = %d, want 2", len(out.Hosts))
	}
	// big.com is first (more bytes).
	if out.Hosts[0].Host != "big.com" {
		t.Fatalf("first host = %q, want big.com", out.Hosts[0].Host)
	}
	if out.Hosts[0].Flows != 2 {
		t.Fatalf("big.com flows = %d, want 2", out.Hosts[0].Flows)
	}
	if out.TotalFlows != 3 {
		t.Fatalf("totalFlows = %d, want 3", out.TotalFlows)
	}
	if out.TotalBytes != 1120 { // 100+900+50+50+10+10
		t.Fatalf("totalBytes = %d, want 1120", out.TotalBytes)
	}
}

// Purge with a wildcard pattern must delete matching subdomains.
func TestPurgeFlowsWildcardHost(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "api.example.com", Path: "/", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "www.example.com", Path: "/", Status: 200})
	s.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "GET", Host: "other.com", Path: "/", Status: 200})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"hosts":["*.example.com"],"mode":"delete"}`
	resp, err := http.Post(ts.URL+"/api/flows/purge", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST purge wildcard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("purge wildcard status %d, want 200", resp.StatusCode)
	}
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Deleted != 2 {
		t.Fatalf("deleted %d, want 2 (wildcard match)", out.Deleted)
	}
	if n := flowCount(t, ts.URL+"/api/flows"); n != 1 {
		t.Fatalf("remaining = %d, want 1", n)
	}
}
