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

// A flow whose body blob is missing (externally deleted body file, partial
// project copy, disk issue) must be skipped, not fatal: one broken blob
// discarding a whole scan's results (up to 5000 flows of scanning work) is
// worse than scanning around it. The scan continues over the healthy flows.
func TestScannerRunSkipsFlowsWithMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	}); err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	// A healthy flow after the broken one must still be scanned.
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "app.example.com", Path: "/", Status: 200, Mime: "text/html",
	}); err != nil {
		t.Fatalf("insert healthy flow: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/scanner/run", nil)
	rec := httptest.NewRecorder()
	(&scannerAPI{h}).scannerRunWithLimit(rec, req, 10)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200 (one missing blob must not abort the scan)", rec.Code, rec.Body.String())
	}
	var out struct {
		Issues []map[string]any `json:"issues"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Issues) == 0 {
		t.Fatalf("expected issues from the healthy flow, got none (scan aborted?)")
	}
	for _, i := range out.Issues {
		if host, _ := i["host"].(string); host == "example.com" {
			t.Fatalf("issue produced for the skipped broken flow: %v", i)
		}
	}
}
