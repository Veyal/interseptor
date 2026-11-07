package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReportRejectsRetiredScannerIssues(t *testing.T) {
	h, _, _ := newHub(t)
	req := httptest.NewRequest(http.MethodGet, "/api/export/report?issues=1", nil)
	rec := httptest.NewRecorder()
	(&findingsAPI{h}).findingsReport(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "scanner issues are no longer available") {
		t.Fatalf("got %d %s; want explicit scanner-issues rejection", rec.Code, rec.Body.String())
	}
}
