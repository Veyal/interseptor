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

func TestFindingStructuredFieldsAPI(t *testing.T) {
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
	r := request("POST", "/api/findings", `{"title":"Base"}`)
	var base store.Finding
	if err := json.Unmarshal(r.Body.Bytes(), &base); err != nil || r.Code != http.StatusOK {
		t.Fatalf("create base: %d %s", r.Code, r.Body.String())
	}
	body := `{"title":"Linked","claims":[{"id":"c1","statement":"Timestamps accepted","verdict":"confirmed"},{"id":"c2","statement":"KYC approved","verdict":"refuted"}],"notExecuted":[{"method":"post","target":"https://example.com/register","reason":"provisions an account","requiresAuthorisation":true}],"relatedFindings":[{"id":` + strconv.FormatInt(base.ID, 10) + `,"relation":"escalates"}]}`
	r = request("POST", "/api/findings", body)
	if r.Code != http.StatusOK {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var f store.Finding
	if err := json.Unmarshal(r.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Claims) != 2 || f.NotExecuted[0].Method != "POST" || f.RelatedFindings[0].Relation != "escalates" {
		t.Fatalf("create dropped fields: %+v", f)
	}
	path := "/api/findings/" + strconv.FormatInt(f.ID, 10)
	r = request("PATCH", path, `{"claims":[{"id":"c1","statement":"s","verdict":"partially_confirmed"}]}`)
	if r.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", r.Code, r.Body.String())
	}
	r = request("GET", path, "")
	var got store.Finding
	_ = json.Unmarshal(r.Body.Bytes(), &got)
	if len(got.Claims) != 1 || got.Claims[0].Verdict != "partially_confirmed" || len(got.NotExecuted) != 1 || len(got.RelatedFindings) != 1 {
		t.Fatalf("patch should replace claims only: %+v", got)
	}
	if r = request("PATCH", path, `{"relatedFindings":[{"id":424242,"relation":"chain"}]}`); r.Code != 400 {
		t.Fatalf("unknown related id: %d %s", r.Code, r.Body.String())
	}
}
