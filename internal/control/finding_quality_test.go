package control

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestFinalReportGateAndDraftRecovery(t *testing.T) {
	h, _, _ := newHub(t)
	handler := h.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "localhost"
		req.RemoteAddr = "127.0.0.1:12345"
		handler.ServeHTTP(r, req)
		return r
	}
	r := request("POST", "/api/findings", `{"title":"Example draft"}`)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = request("GET", "/api/findings/report?mode=final&statuses=all&format=json", "")
	if r.Code != 409 {
		t.Fatalf("incomplete final report allowed: %d %s", r.Code, r.Body.String())
	}
	var result struct {
		Quality struct {
			Ready    bool `json:"ready"`
			Findings []struct {
				Checks []struct{ Field, Message string }
			}
		}
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Quality.Ready || len(result.Quality.Findings) == 0 || len(result.Quality.Findings[0].Checks) == 0 || result.Quality.Findings[0].Checks[0].Message == "" {
		t.Fatal("gate missing field-specific checks")
	}
	r = request("GET", "/api/findings/report?mode=draft&statuses=all&format=json", "")
	if r.Code != 200 {
		t.Fatal("draft recovery unavailable")
	}
	r = request("GET", "/api/findings/readiness?statuses=all", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "checks") {
		t.Fatal(r.Body.String())
	}
	r = request("GET", "/api/findings/report?mode=typo", "")
	if r.Code != 400 {
		t.Fatal("unknown mode silently bypassed gate")
	}
}

func TestFindingRevisionHTTPRecoveryAndAttribution(t *testing.T) {
	h, st, _ := newHub(t)
	handler := h.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "localhost"
		req.RemoteAddr = "127.0.0.1:12345"
		handler.ServeHTTP(r, req)
		return r
	}
	r := request("POST", "/api/findings", `{"title":"Original review","tags":["example"]}`)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	path := "/api/findings/" + strconv.FormatInt(created.ID, 10)
	revisionPath := "/api/finding-revisions/" + strconv.FormatInt(created.ID, 10)
	if r = request("PATCH", path, `{"title":"Revised review"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	revisions, err := st.ListFindingRevisions(created.ID, 100)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("revisions %+v %v", revisions, err)
	}
	for _, revision := range revisions {
		if revision.Actor != "API client" || revision.Source != "http" {
			t.Fatalf("incorrect API attribution %+v", revision)
		}
	}
	if r = request("GET", revisionPath+"/"+strconv.FormatInt(revisions[0].ID, 10), ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"field":"title"`) {
		t.Fatalf("field diff: %d %s", r.Code, r.Body.String())
	}
	if r = request("DELETE", path, ""); r.Code != 200 && r.Code != 204 {
		t.Fatal(r.Body.String())
	}
	if r = request("POST", revisionPath+"/"+strconv.FormatInt(revisions[1].ID, 10)+"/restore", `{"reason":"Restore reviewed text"}`); r.Code != 200 {
		t.Fatalf("restore: %d %s", r.Code, r.Body.String())
	}
	f, err := st.GetFinding(created.ID)
	if err != nil || f.Title != "Original review" || len(f.Tags) != 1 {
		t.Fatalf("restored %+v %v", f, err)
	}
	revisions, _ = st.ListFindingRevisions(created.ID, 100)
	if revisions[0].Action != "restore" || revisions[0].Reason != "Restore reviewed text" {
		t.Fatal("restore event lost")
	}
}
