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

	"github.com/Veyal/interseptor/internal/store"
)

type retestResponse struct {
	FindingID   int64  `json:"findingId"`
	Observation string `json:"observation"`
	Conclusion  string `json:"conclusion"`
	Replayed    int    `json:"replayed"`
	Status      string `json:"status"`
	Flows       []struct {
		EvidenceFlowID int64    `json:"evidenceFlowId"`
		RetestFlowID   int64    `json:"retestFlowId"`
		Request        string   `json:"request"`
		Observation    string   `json:"observation"`
		Changes        []string `json:"changes"`
		Skipped        string   `json:"skipped"`
		Error          string   `json:"error"`
		Attached       bool     `json:"attached"`
		Recorded       struct {
			Status    int   `json:"status"`
			BodyBytes int64 `json:"bodyBytes"`
		} `json:"recorded"`
		Observed *struct {
			Status    int   `json:"status"`
			BodyBytes int64 `json:"bodyBytes"`
		} `json:"observed"`
	} `json:"flows"`
}

func postRetest(t *testing.T, base string, id int64, body string) (*http.Response, retestResponse, string) {
	t.Helper()
	url := fmt.Sprintf("%s/api/findings/%d/retest", base, id)
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var out retestResponse
	_ = json.Unmarshal(raw, &out)
	return resp, out, string(raw)
}

// retestTarget is a throwaway origin whose response the test controls, so a
// retest can be observed as unchanged or changed on demand.
type retestTarget struct {
	server *httptest.Server
	status int
	body   string
}

func newRetestTarget(t *testing.T) *retestTarget {
	t.Helper()
	tg := &retestTarget{status: 200, body: `{"invoice":"8","tenant":"other"}`}
	tg.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tg.status)
		w.Write([]byte(tg.body))
	}))
	t.Cleanup(tg.server.Close)
	return tg
}

// retestFixture records a real exchange against the throwaway origin and
// attaches it to a finding, so the "recorded" side is a genuine capture.
func retestFixture(t *testing.T, h *Hub, s *store.Store, tg *retestTarget, method string) (findingID, flowID int64) {
	t.Helper()
	hostPort := strings.TrimPrefix(tg.server.URL, "http://")
	host, portStr, _ := strings.Cut(hostPort, ":")
	port, _ := strconv.Atoi(portStr)
	resp, err := http.Get(tg.server.URL + "/invoices/8")
	if err != nil {
		t.Fatal(err)
	}
	recorded, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	hash := writeBody(t, s, string(recorded))
	flowID, err = s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: method, Scheme: "http", Host: host, Port: port, Path: "/invoices/8",
		Status: tg.status, ResBodyHash: hash, ResLen: int64(len(recorded)),
		ReqHeaders: map[string][]string{"Accept": {"application/json"}},
		ResHeaders: map[string][]string{"Content-Type": {"application/json"}},
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	f := &store.Finding{
		Severity: "High", Status: "verified", Title: "IDOR on /invoices/{id}",
		Target: tg.server.URL + "/invoices/8",
	}
	findingID, err = s.CreateFinding(f, store.FindingChange{Actor: "test", Source: "test"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}
	if err := s.AttachFlowWithMetadata(findingID, flowID, "evidence", -1, "action",
		"Reads another tenant's invoice.", "captured_flow", 0,
		store.FindingChange{Actor: "test", Source: "test"}); err != nil {
		t.Fatalf("AttachFlowWithMetadata: %v", err)
	}
	return findingID, flowID
}

func TestRetestReportsUnchangedBehaviour(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, flowID := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, out, raw := postRetest(t, ts.URL, id, `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	if out.Observation != "unchanged" {
		t.Fatalf("observation = %q\n%s", out.Observation, raw)
	}
	if out.Replayed != 1 || len(out.Flows) != 1 {
		t.Fatalf("replayed=%d flows=%d", out.Replayed, len(out.Flows))
	}
	fl := out.Flows[0]
	if fl.EvidenceFlowID != flowID || fl.RetestFlowID == 0 || fl.Observed == nil {
		t.Fatalf("flow result: %+v", fl)
	}
	if len(fl.Changes) != 0 {
		t.Errorf("unchanged flow reported changes: %v", fl.Changes)
	}
	// The wording must not claim a verification of impact.
	for _, forbidden := range []string{"fixed", "remediated", "resolved", "no longer vulnerable"} {
		if strings.Contains(strings.ToLower(out.Conclusion), forbidden) {
			t.Errorf("conclusion claims too much (%q): %s", forbidden, out.Conclusion)
		}
	}
	if !strings.Contains(out.Conclusion, "not a fresh verification") {
		t.Errorf("conclusion does not state its limit: %s", out.Conclusion)
	}
}

func TestRetestReportsChangeWithoutClaimingAFix(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, _ := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	tg.status, tg.body = 403, `{"error":"forbidden"}`
	_, out, raw := postRetest(t, ts.URL, id, `{}`)
	if out.Observation != "changed" {
		t.Fatalf("observation = %q\n%s", out.Observation, raw)
	}
	fl := out.Flows[0]
	if fl.Observed == nil || fl.Observed.Status != 403 {
		t.Fatalf("observed: %+v", fl.Observed)
	}
	if fl.Recorded.Status != 200 {
		t.Fatalf("recorded: %+v", fl.Recorded)
	}
	joined := strings.Join(fl.Changes, " ")
	if !strings.Contains(joined, "status 200 → 403") {
		t.Errorf("changes = %v", fl.Changes)
	}
	// The whole point: a single differing response is not a fix.
	low := strings.ToLower(out.Conclusion)
	for _, forbidden := range []string{"fixed", "remediated", "resolved", "patched"} {
		if strings.Contains(low, forbidden) {
			t.Errorf("conclusion claims a fix (%q): %s", forbidden, out.Conclusion)
		}
	}
	if !strings.Contains(out.Conclusion, "not proof of remediation") {
		t.Errorf("conclusion must say what it cannot conclude: %s", out.Conclusion)
	}
	for _, caveat := range []string{"different session", "precondition", "rate limit"} {
		if !strings.Contains(out.Conclusion, caveat) {
			t.Errorf("conclusion omits the %q alternative: %s", caveat, out.Conclusion)
		}
	}
}

func TestRetestNeverMovesTheFindingStatus(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, _ := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	tg.status = 403
	_, out, _ := postRetest(t, ts.URL, id, `{}`)
	if out.Status != "verified" {
		t.Fatalf("response claims status %q", out.Status)
	}
	after, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "verified" {
		t.Fatalf("a retest changed the stored status to %q", after.Status)
	}
}

func TestRetestRecordsANewImmutableRevision(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, evidenceFlow := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	before, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	beforeRevs, _ := s.ListFindingRevisions(id, 100)

	tg.status = 403
	_, out, raw := postRetest(t, ts.URL, id, `{}`)
	if !out.Flows[0].Attached {
		t.Fatalf("the replay was not recorded: %s", raw)
	}

	after, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Blocks) != len(before.Blocks)+1 {
		t.Fatalf("blocks %d -> %d, want one more", len(before.Blocks), len(after.Blocks))
	}
	// The original evidence is untouched, and the new block is a retest.
	orig := after.Blocks[0]
	if orig.FlowID != evidenceFlow || orig.Role != "action" || orig.Proof != "Reads another tenant's invoice." {
		t.Fatalf("original evidence was rewritten: %+v", orig)
	}
	added := after.Blocks[len(after.Blocks)-1]
	if added.Role != "retest" || added.Source != "captured_flow" || added.SourceFlowID != evidenceFlow {
		t.Fatalf("retest block: %+v", added)
	}
	if !strings.Contains(added.Proof, "not proof of remediation") {
		t.Errorf("retest proof overclaims: %q", added.Proof)
	}
	afterRevs, _ := s.ListFindingRevisions(id, 100)
	if len(afterRevs) <= len(beforeRevs) {
		t.Fatalf("no revision appended: %d -> %d", len(beforeRevs), len(afterRevs))
	}
}

func TestRetestCanSkipRecording(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, _ := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	before, _ := s.GetFinding(id)
	_, out, raw := postRetest(t, ts.URL, id, `{"attach":false}`)
	if out.Observation == "" {
		t.Fatalf("no result: %s", raw)
	}
	if out.Flows[0].Attached {
		t.Error("attach=false still recorded a block")
	}
	after, _ := s.GetFinding(id)
	if len(after.Blocks) != len(before.Blocks) {
		t.Fatalf("blocks changed: %d -> %d", len(before.Blocks), len(after.Blocks))
	}
}

func TestRetestSkipsStateChangingMethodsUnlessAsked(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, _ := retestFixture(t, h, s, tg, http.MethodDelete)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	_, out, raw := postRetest(t, ts.URL, id, `{}`)
	if out.Observation != "inconclusive" {
		t.Fatalf("observation = %q\n%s", out.Observation, raw)
	}
	if out.Replayed != 0 {
		t.Fatalf("a DELETE was replayed without being asked for")
	}
	if !strings.Contains(out.Flows[0].Skipped, "allowUnsafeMethods") {
		t.Errorf("the skip does not say how to override it: %q", out.Flows[0].Skipped)
	}

	_, out, raw = postRetest(t, ts.URL, id, `{"allowUnsafeMethods":true}`)
	if out.Replayed != 1 {
		t.Fatalf("explicit opt-in was ignored: %s", raw)
	}
}

func TestRetestWithoutReplayableEvidence(t *testing.T) {
	h, s, _ := newHub(t)
	id, err := s.CreateFinding(&store.Finding{Title: "Bare claim"}, store.FindingChange{Actor: "t", Source: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, _, raw := postRetest(t, ts.URL, id, `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d, want 409; body=%s", resp.StatusCode, raw)
	}
	if !strings.Contains(raw, "no replayable evidence") {
		t.Errorf("unhelpful error: %s", raw)
	}
}

func TestRetestRejectsBadInput(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, flowID := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	if resp, _, raw := postRetest(t, ts.URL, id, `{"session":"whatever"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("session: status=%d body=%s", resp.StatusCode, raw)
	}
	// A flow that is not this finding's evidence must not be replayable through it.
	other, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Host: "example.com", Path: "/", Status: 200})
	resp, _, raw := postRetest(t, ts.URL, id, fmt.Sprintf(`{"flowIds":[%d]}`, other))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign flow: status=%d body=%s", resp.StatusCode, raw)
	}
	if !strings.Contains(raw, "not attached") {
		t.Errorf("unhelpful error: %s", raw)
	}
	// The finding's own flow is accepted.
	if resp, _, raw := postRetest(t, ts.URL, id, fmt.Sprintf(`{"flowIds":[%d]}`, flowID)); resp.StatusCode != http.StatusOK {
		t.Fatalf("own flow: status=%d body=%s", resp.StatusCode, raw)
	}
	if resp, _, _ := postRetest(t, ts.URL, 9999, `{}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing finding: status=%d", resp.StatusCode)
	}
}

func TestRetestReportsAnUnreachableTarget(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, _ := retestFixture(t, h, s, tg, http.MethodGet)
	tg.server.Close() // the origin is gone
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	_, out, raw := postRetest(t, ts.URL, id, `{}`)
	if out.Observation != "inconclusive" {
		t.Fatalf("an unreachable target must be inconclusive, not a fix: %q\n%s", out.Observation, raw)
	}
	low := strings.ToLower(out.Conclusion)
	for _, forbidden := range []string{"fixed", "remediated", "unchanged"} {
		if strings.Contains(low, forbidden) {
			t.Errorf("conclusion claims %q for an unreachable target: %s", forbidden, out.Conclusion)
		}
	}
	if !strings.Contains(out.Conclusion, "nothing is established") && !strings.Contains(out.Conclusion, "not established") {
		t.Errorf("conclusion does not admit it learned nothing: %s", out.Conclusion)
	}
}

// A retest attaches its own flow as evidence. Replaying that flow on the next
// retest would double the traffic each time, so it is excluded from the default
// set while remaining attached evidence.
func TestRetestDoesNotReplayItsOwnEarlierResults(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, evidenceFlow := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for round := 1; round <= 3; round++ {
		_, out, raw := postRetest(t, ts.URL, id, `{}`)
		if out.Replayed != 1 {
			t.Fatalf("round %d replayed %d flows, want 1\n%s", round, out.Replayed, raw)
		}
		if out.Flows[0].EvidenceFlowID != evidenceFlow {
			t.Fatalf("round %d replayed flow #%d, want the original #%d",
				round, out.Flows[0].EvidenceFlowID, evidenceFlow)
		}
	}
	// Each round still recorded its result.
	after, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	retests := 0
	for _, bl := range after.Blocks {
		if bl.Role == "retest" {
			retests++
		}
	}
	if retests != 3 {
		t.Fatalf("want 3 recorded retests, got %d", retests)
	}
	// Naming a retest flow explicitly still replays it.
	var retestFlow int64
	for _, bl := range after.Blocks {
		if bl.Role == "retest" {
			retestFlow = bl.FlowID
			break
		}
	}
	_, out, raw := postRetest(t, ts.URL, id, fmt.Sprintf(`{"flowIds":[%d],"attach":false}`, retestFlow))
	if out.Replayed != 1 || out.Flows[0].EvidenceFlowID != retestFlow {
		t.Fatalf("explicit retest-flow replay: %s", raw)
	}
}

// A retest adds evidence, so it must not be a way to make an incomplete finding
// look report-ready. A retest block carries the `retest` role, which satisfies
// none of the action/result/control capability checks.
func TestRetestDoesNotMoveReportReadiness(t *testing.T) {
	h, s, _ := newHub(t)
	tg := newRetestTarget(t)
	id, _ := retestFixture(t, h, s, tg, http.MethodGet)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	before, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if before.Ready {
		t.Fatal("the fixture is meant to be incomplete")
	}

	if _, out, raw := postRetest(t, ts.URL, id, `{}`); !out.Flows[0].Attached {
		t.Fatalf("nothing was recorded: %s", raw)
	}

	after, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Ready {
		t.Fatal("a retest made an incomplete finding report-ready")
	}
	if strings.Join(after.Missing, ",") != strings.Join(before.Missing, ",") {
		t.Fatalf("readiness gaps moved: %v -> %v", before.Missing, after.Missing)
	}
	if after.Readiness.Stage != before.Readiness.Stage {
		t.Fatalf("readiness stage moved: %q -> %q", before.Readiness.Stage, after.Readiness.Stage)
	}
	if after.Readiness.Capabilities != before.Readiness.Capabilities {
		t.Fatalf("capabilities moved: %+v -> %+v", before.Readiness.Capabilities, after.Readiness.Capabilities)
	}
	// A final report still refuses it.
	resp, body := getReport(t, ts.URL, "?statuses=all&mode=final&format=json")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("final report status=%d, want 409; body=%s", resp.StatusCode, body)
	}
}
