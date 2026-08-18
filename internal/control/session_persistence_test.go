package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetSessionReportsPersistenceFailure(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/session", strings.NewReader(`{
		"enabled":true,
		"headers":"Authorization: Bearer example-token",
		"hostHeaders":{"example.com":"X-Test: one"}
	}`))
	rec := httptest.NewRecorder()
	(&sessionAPI{h}).setSession(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when session settings cannot be persisted; body = %s", rec.Code, rec.Body.String())
	}
}
