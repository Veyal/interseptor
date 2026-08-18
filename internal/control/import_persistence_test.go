package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/harx"
	"github.com/Veyal/interseptor/internal/store"
)

func oneFlowHAR() []byte {
	return harx.Build([]*store.Flow{{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Port: 443,
		Path: "/import", HTTPVersion: "HTTP/1.1", Status: http.StatusCreated,
	}}, func(string) []byte { return nil })
}

func TestImportHARReportsPersistenceFailure(t *testing.T) {
	h, st, _ := newHub(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import/har", bytes.NewReader(oneFlowHAR()))
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importHAR(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
}

func TestImportProjectRejectsMalformedEmbeddedHAR(t *testing.T) {
	h, _, _ := newHub(t)
	req := httptest.NewRequest(http.MethodPost, "/api/import/project",
		bytes.NewBufferString(`{"version":"1","har":{"not":"a HAR"}}`))
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importProject(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestImportProjectReportsRulePersistenceFailure(t *testing.T) {
	h, st, _ := newHub(t)
	bundle, err := json.Marshal(projectBundle{
		Version: "1", HAR: json.RawMessage(`{"log":{"version":"1.2","entries":[]}}`),
		Rules: []store.Rule{{Enabled: true, Type: "req-header", Match: "X-Test", Replace: "one"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(bundle))
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importProject(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
}
