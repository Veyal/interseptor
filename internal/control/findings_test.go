package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
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
	for _, want := range []string{"# Interseptor — Engagement Report", "IDOR on /user/{id}", "**Status:** verified", "t.com/user/2"} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}
	// Full reconstructed HTTP for PoC flows (default includeBodies).
	for _, want := range []string{"**Request**", "```http", "GET /user/2 HTTP/1.1", "Host: t.com", "**Response**", "HTTP/1.1 200"} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing PoC body %q:\n%s", want, md)
		}
	}
	// Opt-out omits bodies.
	rrOff, err := http.Get(ts.URL + "/api/findings/report?includeBodies=0")
	if err != nil {
		t.Fatalf("report opt-out: %v", err)
	}
	offBody, _ := io.ReadAll(rrOff.Body)
	rrOff.Body.Close()
	if strings.Contains(string(offBody), "```http") {
		t.Fatalf("includeBodies=0 should omit http fences:\n%s", offBody)
	}

	// Delete the finding.
	req4, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/findings/"+id(created.ID, 10), nil)
	r6, _ := http.DefaultClient.Do(req4)
	r6.Body.Close()
	if r6.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", r6.StatusCode)
	}
}

func TestFindingReportStatusDefaultsAndExplicitAll(t *testing.T) {
	h, s, _ := newHub(t)
	for _, f := range []store.Finding{
		{Title: "Open issue", Severity: "High", Status: "open"},
		{Title: "Verified issue", Severity: "High", Status: "verified"},
		{Title: "Fixed issue", Severity: "Low", Status: "fixed"},
		{Title: "Needs review", Severity: "Medium", Status: "needs_verification"},
		{Title: "False positive issue", Severity: "Info", Status: "false_positive"},
		{Title: "Accepted risk issue", Severity: "Low", Status: "wont_fix"},
	} {
		f := f
		if _, err := s.CreateFinding(&f); err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	get := func(query string) string {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/findings/report?format=json" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	defaultBody := get("")
	for _, want := range []string{"Open issue", "Verified issue", "Fixed issue"} {
		if !strings.Contains(defaultBody, want) {
			t.Errorf("default report missing %q: %s", want, defaultBody)
		}
	}
	for _, excluded := range []string{"Needs review", "False positive issue", "Accepted risk issue"} {
		if strings.Contains(defaultBody, excluded) {
			t.Errorf("default report unexpectedly includes %q: %s", excluded, defaultBody)
		}
	}
	allBody := get("&statuses=all")
	for _, want := range []string{"Needs review", "False positive issue", "Accepted risk issue"} {
		if !strings.Contains(allBody, want) {
			t.Errorf("explicit all report missing %q: %s", want, allBody)
		}
	}
	verifiedBody := get("&statuses=verified")
	if !strings.Contains(verifiedBody, "Verified issue") || strings.Contains(verifiedBody, "Open issue") {
		t.Errorf("status-filtered report is wrong: %s", verifiedBody)
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

// TestFindingImpactCreateAndPatch verifies that:
//   - POST /api/findings with "impact" returns the field populated.
//   - PATCH /api/findings/:id with "impact" updates it and returns the new value.
func TestFindingImpactCreateAndPatch(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	idStr := func(id int64) string { return strconv.FormatInt(id, 10) }

	// CREATE with impact.
	resp, err := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"Impact test","severity":"High","impact":"attacker reads PII of all users"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: want 200, got %d", resp.StatusCode)
	}
	if created.ID == 0 {
		t.Fatalf("create: no id returned")
	}
	if created.Impact != "attacker reads PII of all users" {
		t.Fatalf("create: impact want %q got %q", "attacker reads PII of all users", created.Impact)
	}

	// PATCH impact.
	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/findings/"+idStr(created.ID),
		strings.NewReader(`{"impact":"full account takeover — admin privilege escalation"}`))
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	var updated store.Finding
	json.NewDecoder(r2.Body).Decode(&updated)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("patch: want 200, got %d", r2.StatusCode)
	}
	want := "full account takeover — admin privilege escalation"
	if updated.Impact != want {
		t.Fatalf("patch: impact want %q got %q", want, updated.Impact)
	}
}

// TestFindingCvssCreateAndPatch verifies the cvss field round-trips through
// POST /api/findings and PATCH /api/findings/:id.
func TestFindingCvssCreateAndPatch(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	idStr := func(id int64) string { return strconv.FormatInt(id, 10) }

	// CREATE with cvss + Critical severity.
	resp, err := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"CVSS test","severity":"critical","cvss":"9.8"}`))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: want 200, got %d", resp.StatusCode)
	}
	if created.Cvss != "9.8" {
		t.Fatalf("create: cvss want %q got %q", "9.8", created.Cvss)
	}
	if created.Severity != "Critical" {
		t.Fatalf("create: severity want Critical got %q", created.Severity)
	}

	// PATCH cvss with a vector.
	vector := `CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H`
	payload, _ := json.Marshal(map[string]string{"cvss": vector})
	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/findings/"+idStr(created.ID), strings.NewReader(string(payload)))
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	var updated store.Finding
	json.NewDecoder(r2.Body).Decode(&updated)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("patch: want 200, got %d", r2.StatusCode)
	}
	if updated.Cvss != vector {
		t.Fatalf("patch: cvss want %q got %q", vector, updated.Cvss)
	}
}

// TestFindingAttachFlowPosition verifies the "position" field on
// POST /api/findings/:id/flows inserts the block at the right index.
func TestFindingAttachFlowPosition(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	f1, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/a", Status: 200})
	f2, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "t.com", Path: "/b", Status: 200})
	f3, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "GET", Host: "t.com", Path: "/c", Status: 200})

	// Create finding with a text block + f1 already attached.
	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"pos test","detail":"intro"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	fid := strconv.FormatInt(created.ID, 10)

	// Attach f1 (append).
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/findings/"+fid+"/flows",
		strings.NewReader(fmt.Sprintf(`{"flowId":%d,"note":"first"}`, f1)))
	http.DefaultClient.Do(req)

	// Attach f2 at position 1 (between text and f1).
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/findings/"+fid+"/flows",
		strings.NewReader(fmt.Sprintf(`{"flowId":%d,"note":"inserted","position":1}`, f2)))
	r2, _ := http.DefaultClient.Do(req2)
	var withPoC store.Finding
	json.NewDecoder(r2.Body).Decode(&withPoC)
	r2.Body.Close()

	// Block order must be: text(intro), flow:f2, flow:f1
	if len(withPoC.Blocks) < 3 {
		t.Fatalf("expected >=3 blocks, got %d: %+v", len(withPoC.Blocks), withPoC.Blocks)
	}
	if withPoC.Blocks[0].Type != "text" {
		t.Fatalf("block[0] should be text, got %+v", withPoC.Blocks[0])
	}
	if withPoC.Blocks[1].Type != "flow" || withPoC.Blocks[1].FlowID != f2 {
		t.Fatalf("block[1] should be flow f2, got %+v", withPoC.Blocks[1])
	}
	if withPoC.Blocks[2].Type != "flow" || withPoC.Blocks[2].FlowID != f1 {
		t.Fatalf("block[2] should be flow f1, got %+v", withPoC.Blocks[2])
	}

	// Attach f3 without position → appends at end.
	req3, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/findings/"+fid+"/flows",
		strings.NewReader(fmt.Sprintf(`{"flowId":%d,"note":"appended"}`, f3)))
	r3, _ := http.DefaultClient.Do(req3)
	var final store.Finding
	json.NewDecoder(r3.Body).Decode(&final)
	r3.Body.Close()
	last := final.Blocks[len(final.Blocks)-1]
	if last.Type != "flow" || last.FlowID != f3 {
		t.Fatalf("last block should be f3, got %+v", last)
	}
}

// TestCreateFindingSurfacesBadFlowIDWarning verifies that create_finding's
// flowIds loop no longer silently swallows a failed PoC attachment: the
// finding is still created (and the valid attachment still lands), but the
// response signals which flowId failed via a "warnings" array instead of
// discarding the error.
func TestCreateFindingSurfacesBadFlowIDWarning(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	validFlow, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/ok", Status: 200})
	const invalidFlow int64 = 999999 // does not exist

	payload := fmt.Sprintf(`{"title":"mixed attach","severity":"Medium","flowIds":[%d,%d]}`, validFlow, invalidFlow)
	resp, err := http.Post(ts.URL+"/api/findings", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: want 200, got %d", resp.StatusCode)
	}
	var out struct {
		ID       int64               `json:"id"`
		Flows    []store.FindingFlow `json:"flows"`
		Warnings []string            `json:"warnings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	// The finding itself must still be created despite one bad attachment.
	if out.ID == 0 {
		t.Fatalf("finding was not created: %+v", out)
	}
	// The valid attachment must still have landed.
	found := false
	for _, fl := range out.Flows {
		if fl.FlowID == validFlow {
			found = true
		}
	}
	if !found {
		t.Fatalf("valid flow attachment missing: %+v", out.Flows)
	}
	// The failed attachment must be surfaced, not silently dropped.
	if len(out.Warnings) == 0 {
		t.Fatalf("expected a warning about the invalid flowId %d, got none: %+v", invalidFlow, out)
	}
	joined := strings.Join(out.Warnings, " | ")
	if !strings.Contains(joined, strconv.FormatInt(invalidFlow, 10)) {
		t.Fatalf("warnings do not mention the failing flowId %d: %v", invalidFlow, out.Warnings)
	}
}

// TestListFlowsTagFilter verifies GET /api/flows?tag= wires into FlowFilter.Tag.
func TestListFlowsTagFilter(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	f1, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/tagged", Status: 200})
	f2, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "b.com", Path: "/untagged", Status: 200})
	s.SetFlowTags(f1, []string{"sqli"})
	_ = f2 // untagged

	// Filter by existing tag.
	resp, err := http.Get(ts.URL + "/api/flows?tag=sqli")
	if err != nil {
		t.Fatalf("GET flows?tag=sqli: %v", err)
	}
	var out struct {
		Flows []store.Flow `json:"flows"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if len(out.Flows) != 1 || out.Flows[0].ID != f1 {
		t.Fatalf("tag=sqli should return only f1, got %+v", out.Flows)
	}

	// Filter by non-existent tag → empty list.
	resp2, _ := http.Get(ts.URL + "/api/flows?tag=nonexistent")
	var out2 struct {
		Flows []store.Flow `json:"flows"`
	}
	json.NewDecoder(resp2.Body).Decode(&out2)
	resp2.Body.Close()
	if len(out2.Flows) != 0 {
		t.Fatalf("tag=nonexistent should return 0 flows, got %d", len(out2.Flows))
	}
}

// TestAttachFindingFlowRejectsMissingFlowID verifies POST .../flows returns 404
// (not 200 with missing:true) when the flowId does not exist.
func TestAttachFindingFlowRejectsMissingFlowID(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"attach miss","detail":"intro"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/findings/"+strconv.FormatInt(created.ID, 10)+"/flows",
		strings.NewReader(`{"flowId":999999,"note":"nope"}`))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for missing flowId, got %d", r.StatusCode)
	}
}

// TestAttachFindingFlowExistingFlowNotMissing verifies a successful attach
// returns an enriched PoC that is not marked missing.
func TestAttachFindingFlowExistingFlowNotMissing(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	flowID, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Host: "t.com", Path: "/ok", Status: 200})
	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"attach ok","detail":"intro"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/findings/"+strconv.FormatInt(created.ID, 10)+"/flows",
		strings.NewReader(fmt.Sprintf(`{"flowId":%d,"note":"baseline"}`, flowID)))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", r.StatusCode)
	}
	var out store.Finding
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Flows) != 1 || out.Flows[0].Missing || out.Flows[0].Path != "/ok" {
		t.Fatalf("expected enriched present flow, got %+v", out.Flows)
	}
	for _, b := range out.Blocks {
		if b.Type == "flow" && b.FlowID == flowID && b.Missing {
			t.Fatalf("flow block should not be missing: %+v", b)
		}
	}
}

// TestAttachFindingImageRoundTrip uploads a tiny PNG and serves it back by hash.
func TestAttachFindingImageRoundTrip(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"img","detail":"intro"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// 1x1 PNG as data URL
	const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	payload := fmt.Sprintf(`{"data":"data:image/png;base64,%s","caption":"xss alert","position":1}`, pngB64)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/api/findings/"+strconv.FormatInt(created.ID, 10)+"/images",
		strings.NewReader(payload))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("attach image: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", r.StatusCode)
	}
	var out store.Finding
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	var hash, url string
	for _, b := range out.Blocks {
		if b.Type == "image" {
			hash, url = b.Hash, b.URL
			if b.Caption != "xss alert" || b.Missing {
				t.Fatalf("image block: %+v", b)
			}
		}
	}
	if hash == "" || url == "" {
		t.Fatalf("no image block: %+v", out.Blocks)
	}

	imgResp, err := http.Get(ts.URL + url)
	if err != nil {
		t.Fatalf("GET image: %v", err)
	}
	defer imgResp.Body.Close()
	if imgResp.StatusCode != http.StatusOK {
		t.Fatalf("GET image status %d", imgResp.StatusCode)
	}
	if ct := imgResp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if imgResp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
}

// TestUpdateFindingRejectsInlineImageData ensures body PATCH cannot embed base64.
func TestUpdateFindingRejectsInlineImageData(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, _ := http.Post(ts.URL+"/api/findings", "application/json",
		strings.NewReader(`{"title":"img reject"}`))
	var created store.Finding
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	bodyJSON, _ := json.Marshal(map[string]string{
		"body": `[{"type":"image","data":"AAAA","caption":"nope"}]`,
	})
	req, _ := http.NewRequest(http.MethodPatch,
		ts.URL+"/api/findings/"+strconv.FormatInt(created.ID, 10),
		bytes.NewReader(bodyJSON))
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusRequestEntityTooLarge && r.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 413/400 for inline image data, got %d", r.StatusCode)
	}
}
