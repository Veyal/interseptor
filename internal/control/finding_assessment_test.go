package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestFindingAssessmentRESTAndExports(t *testing.T) {
	h, _, _ := newHub(t)
	handler := h.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "localhost"
		handler.ServeHTTP(r, req)
		return r
	}
	r := request("POST", "/api/findings", `{"title":"Example assessment","environment":"development","status":"verified","targets":[{"url":"https://example.com/primary","method":"GET","role":"reader","relation":"affected"},{"url":"https://example.com/secondary","methods":["GET","POST"],"variant":"record","role":"reviewer","relation":"setup","evidenceException":"Preparation only"}],"proofReview":{"execution":"not_executed","reason":"Result evidence unavailable"}}`)
	if r.Code != http.StatusOK {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var f store.Finding
	if err := json.Unmarshal(r.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "needs_verification" || f.Environment != "development" || len(f.Targets) != 2 {
		t.Fatalf("create: %+v", f)
	}
	path := "/api/findings/" + strconv.FormatInt(f.ID, 10)
	r = request("PATCH", path, `{"environment":"invalid","title":"Must not save"}`)
	if r.Code != 400 {
		t.Fatalf("invalid environment: %d %s", r.Code, r.Body.String())
	}
	r = request("GET", path, "")
	if strings.Contains(r.Body.String(), "Must not save") {
		t.Fatal("invalid mutation partially persisted")
	}
	for _, format := range []string{"md", "html", "json"} {
		r = request("GET", "/api/findings/report?statuses=all&format="+format, "")
		if r.Code != 200 {
			t.Fatalf("export %s: %d %s", format, r.Code, r.Body.String())
		}
		for _, text := range []string{"https://example.com/primary", "https://example.com/secondary", "reviewer", "Preparation only", "Result evidence unavailable", "development"} {
			if !strings.Contains(r.Body.String(), text) {
				t.Errorf("%s export missing %q", format, text)
			}
		}
	}
	r = request("PATCH", path, `{"targets":[]}`)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	json.Unmarshal(r.Body.Bytes(), &f)
	if f.Target != "" || len(f.Targets) != 0 {
		t.Fatal("removing final target restored legacy scalar")
	}
}
