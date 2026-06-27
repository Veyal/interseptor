package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

func TestFindingsEndpoints(t *testing.T) {
	h, s, _ := newHub(t)
	f1, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/user/1", Status: 200})
	f2, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "t.com", Path: "/user/2", Status: 200})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	id := strconv.FormatInt

	// Create a finding with one PoC flow attached up front.
	resp, err := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"IDOR on /user/{id}","severity":"high","source":"ai","flowIds":[`+id(f1, 10)+`]}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.ID == 0 || created.Severity != "High" || created.Source != "ai" || len(created.Flows) != 1 {
		t.Fatalf("create finding wrong: %+v", created)
	}
	if created.Flows[0].Path != "/user/1" {
		t.Fatalf("PoC flow not enriched: %+v", created.Flows[0])
	}

	// List.
	r2, _ := http.Get(ts.URL + "/api/findings")
	var lst struct {
		Findings []store.Finding `json:"findings"`
	}
	json.NewDecoder(r2.Body).Decode(&lst)
	r2.Body.Close()
	if len(lst.Findings) != 1 {
		t.Fatalf("list: got %d, want 1", len(lst.Findings))
	}

	// Attach a second PoC flow.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/findings/"+id(created.ID, 10)+"/flows",
		strings.NewReader(`{"flowId":`+id(f2, 10)+`,"note":"reads user 2"}`))
	r3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	var withPoC store.Finding
	json.NewDecoder(r3.Body).Decode(&withPoC)
	r3.Body.Close()
	if len(withPoC.Flows) != 2 {
		t.Fatalf("after attach: got %d PoC flows, want 2", len(withPoC.Flows))
	}

	// Update status via PATCH.
	req2, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/findings/"+id(created.ID, 10),
		strings.NewReader(`{"status":"verified"}`))
	r4, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	var upd store.Finding
	json.NewDecoder(r4.Body).Decode(&upd)
	r4.Body.Close()
	if upd.Status != "verified" {
		t.Fatalf("after patch: status %q, want verified", upd.Status)
	}

	// Detach a PoC flow.
	req3, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/findings/"+id(created.ID, 10)+"/flows/"+id(f1, 10), nil)
	r5, _ := http.DefaultClient.Do(req3)
	var afterDetach store.Finding
	json.NewDecoder(r5.Body).Decode(&afterDetach)
	r5.Body.Close()
	if len(afterDetach.Flows) != 1 || afterDetach.Flows[0].FlowID != f2 {
		t.Fatalf("after detach: %+v", afterDetach.Flows)
	}

	// Markdown engagement report renders the finding + its PoC flow.
	rr, err := http.Get(ts.URL + "/api/findings/report")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	body, _ := io.ReadAll(rr.Body)
	rr.Body.Close()
	if ct := rr.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("report content-type %q", ct)
	}
	md := string(body)
	for _, want := range []string{"# Interceptor — Engagement Report", "IDOR on /user/{id}", "**Status:** verified", "t.com/user/2"} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}

	// Delete the finding.
	req4, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/findings/"+id(created.ID, 10), nil)
	r6, _ := http.DefaultClient.Do(req4)
	r6.Body.Close()
	if r6.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", r6.StatusCode)
	}
}
