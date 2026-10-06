package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestFindingCreateRejectsUnknownEnvironmentWithoutMapping(t *testing.T) {
	h, _, _ := newHub(t)
	handler := h.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/findings", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "localhost"
		handler.ServeHTTP(r, req)
		return r
	}
	r := post(`{"title":"Example","environment":"qa-lab"}`)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "environment must be") {
		t.Fatalf("unknown environment: %d %s", r.Code, r.Body.String())
	}
	r = post(`{"title":"Example","environment":"development"}`)
	var f store.Finding
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &f) != nil || f.Environment != "development" {
		t.Fatalf("development environment: %d %s", r.Code, r.Body.String())
	}
}
