package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestScannerRunRejectsMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	}); err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/scanner/run", nil)
	rec := httptest.NewRecorder()
	(&scannerAPI{h}).scannerRunWithLimit(rec, req, 10)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}
