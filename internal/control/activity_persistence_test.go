package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActivityWriteReportsPersistenceFailure(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/activity",
		strings.NewReader(`{"tool":"send_request","summary":"example"}`))
	rec := httptest.NewRecorder()
	(&metaAPI{h}).postActivity(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
