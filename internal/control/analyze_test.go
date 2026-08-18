package control

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestAnalyzeFlowRejectsMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	id, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/flows/1/analyze", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	(&flowAPI{h}).analyzeFlow(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}
