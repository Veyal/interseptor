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

// analyze_flow reports the surface, not a body: a flow whose body was pruned (or
// never stored) must still be analysable rather than 404-ing the tool.
func TestAnalyzeFlowSurvivesMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	id, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit?price=1&id=2",
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	})
	if err != nil {
		t.Fatalf("insert flow: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/flows/1/analyze", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	(&flowAPI{h}).analyzeFlow(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"url":"https://example.com/submit?price=1\u0026id=2"`, `"queryParams":["`, `"inScope":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("analyze response missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "scannerFindings") {
		t.Error("analyze_flow must not claim passive-scanner findings: the scanner is gone")
	}
}
