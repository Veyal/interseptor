package control

import (
	"encoding/json"
	"fmt"
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

// TestFindingBodySizeCap verifies the 1 MiB body cap and 256 KiB per-text-block
// cap on both the create and update paths.
func TestFindingBodySizeCap(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// ---- helpers ----
	post := func(payload string) *http.Response {
		resp, err := http.Post(ts.URL+"/api/findings", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatalf("POST /api/findings: %v", err)
		}
		return resp
	}
	patch := func(findingID int64, payload string) *http.Response {
		req, _ := http.NewRequest(http.MethodPatch,
			fmt.Sprintf("%s/api/findings/%d", ts.URL, findingID),
			strings.NewReader(payload))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH /api/findings/%d: %v", findingID, err)
		}
		return resp
	}
	errMsg := func(resp *http.Response) string {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var obj map[string]string
		json.Unmarshal(b, &obj)
		return obj["error"]
	}

	// ---- (1) Normal-sized body succeeds ----
	smallDetail := strings.Repeat("x", 1024) // 1 KiB — well under the cap
	payload1, _ := json.Marshal(map[string]string{
		"title":  "ok finding",
		"detail": smallDetail,
	})
	resp1 := post(string(payload1))
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("normal-size create: want 200, got %d", resp1.StatusCode)
	}
	var created store.Finding
	json.NewDecoder(resp1.Body).Decode(&created)
	resp1.Body.Close()

	// ---- (2) Over-cap detail on CREATE returns 413 with error message ----
	bigDetail := strings.Repeat("a", maxFindingBodyBytes+1)
	payload2, _ := json.Marshal(map[string]string{
		"title":  "big finding",
		"detail": bigDetail,
	})
	resp2 := post(string(payload2))
	if resp2.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-cap create: want 413, got %d", resp2.StatusCode)
	}
	if msg := errMsg(resp2); !strings.Contains(msg, "finding body too large") {
		t.Fatalf("over-cap create: error message %q does not contain 'finding body too large'", msg)
	}

	// ---- (3) Over-cap body JSON on UPDATE returns 413 with error message ----
	bigBody := fmt.Sprintf(`[{"type":"text","md":%q}]`, strings.Repeat("b", maxFindingBodyBytes+1))
	payload3, _ := json.Marshal(map[string]string{"body": bigBody})
	resp3 := patch(created.ID, string(payload3))
	if resp3.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-cap update (body): want 413, got %d", resp3.StatusCode)
	}
	if msg := errMsg(resp3); !strings.Contains(msg, "finding body too large") {
		t.Fatalf("over-cap update: error message %q does not contain 'finding body too large'", msg)
	}

	// ---- (4) Single over-cap text block within a valid total body returns 413 ----
	// The full JSON is under 1 MiB but the single MD field exceeds 256 KiB.
	bigMD := strings.Repeat("c", maxFindingTextBlock+1)
	blocks, _ := json.Marshal([]map[string]string{{"type": "text", "md": bigMD}})
	payload4, _ := json.Marshal(map[string]string{"body": string(blocks)})
	resp4 := patch(created.ID, string(payload4))
	if resp4.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-cap text block: want 413, got %d", resp4.StatusCode)
	}
	if msg := errMsg(resp4); !strings.Contains(msg, "finding text block too large") {
		t.Fatalf("over-cap text block: error message %q does not contain 'finding text block too large'", msg)
	}

	// ---- (5) Normal update after rejection still works (no corruption) ----
	goodBlocks, _ := json.Marshal([]map[string]string{{"type": "text", "md": "all good"}})
	payload5, _ := json.Marshal(map[string]string{"body": string(goodBlocks)})
	resp5 := patch(created.ID, string(payload5))
	if resp5.StatusCode != http.StatusOK {
		t.Fatalf("good update after rejected writes: want 200, got %d", resp5.StatusCode)
	}
	resp5.Body.Close()
}
