package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUpdateFindingBodySyncsFindingFlows(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f1, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/a", Status: 200})
	f2, _ := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "POST", Host: "example.com", Path: "/b", Status: 201})
	id, err := s.CreateFinding(&Finding{Title: "sync test", Detail: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachFlow(id, f1, "old", -1); err != nil {
		t.Fatal(err)
	}

	body := `[{"type":"text","md":"step 1"},{"type":"flow","flowId":` + fmtInt64(f2) + `,"note":"new poc"},{"type":"flow","flowId":` + fmtInt64(f1) + `,"note":"again"}]`
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateFinding: %v", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Flows) != 2 {
		t.Fatalf("flows=%+v", got.Flows)
	}
	if got.Flows[0].FlowID != f2 || got.Flows[0].Note != "new poc" || got.Flows[0].Ord != 0 {
		t.Fatalf("flow[0]=%+v", got.Flows[0])
	}
	if got.Flows[1].FlowID != f1 || got.Flows[1].Note != "again" || got.Flows[1].Ord != 1 {
		t.Fatalf("flow[1]=%+v", got.Flows[1])
	}
	if got.Blocks[1].Missing || got.Blocks[1].Method != "POST" {
		t.Fatalf("block enrichment: %+v", got.Blocks[1])
	}
}

func TestUpdateFindingLegacyPartialBodyCapIncludesRetainedBlocks(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Keep every individual block below the per-block HTTP limit while placing
	// the aggregate canonical body just below the store limit. A legacy detail
	// update replaces only the first text block; the retained blocks must still
	// count toward the aggregate cap.
	blocks := make([]FindingBlock, 0, 5)
	blocks = append(blocks, FindingBlock{Type: "text", MD: strings.Repeat("a", 150<<10)})
	for i := 0; i < 4; i++ {
		blocks = append(blocks, FindingBlock{Type: "text", MD: strings.Repeat("b", 200<<10)})
	}
	body, err := MarshalFindingBlocks(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) >= maxFindingBodyBytes {
		t.Fatalf("fixture body is not below cap: %d", len(body))
	}
	id, err := s.CreateFinding(&Finding{Title: "aggregate cap", Body: body})
	if err != nil {
		t.Fatal(err)
	}

	tooLarge := strings.Repeat("c", 256<<10)
	err = s.UpdateFinding(id, nil, nil, nil, nil, &tooLarge, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "body too large") {
		t.Fatalf("legacy partial update error = %v, want aggregate body cap", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Blocks[0].MD != strings.Repeat("a", 150<<10) {
		t.Fatal("rejected partial update changed the canonical body")
	}
}

func TestFindingNarrativeCapIncludesEvidenceFirstEnvelopeFields(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	half := strings.Repeat("s", maxFindingBodyBytes/2+1)
	if _, err := s.CreateFinding(&Finding{
		Title: "oversized narrative", Summary: half, VerificationInstructions: half,
	}); err == nil || !strings.Contains(err.Error(), "narrative too large") {
		t.Fatalf("CreateFinding error=%v, want aggregate narrative cap", err)
	}

	id, err := s.CreateFinding(&Finding{
		Title: "bounded narrative", Summary: strings.Repeat("s", maxFindingBodyBytes/2),
	})
	if err != nil {
		t.Fatal(err)
	}
	impact := strings.Repeat("i", maxFindingBodyBytes/2+1)
	if err := s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, nil, &impact, nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "narrative too large") {
		t.Fatalf("UpdateFindingCanonical error=%v, want retained aggregate narrative cap", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Impact != "" || len(got.Summary) != maxFindingBodyBytes/2 {
		t.Fatal("rejected narrative update mutated the finding")
	}

	canonical := `[{"type":"text","md":"bounded canonical step"}]`
	legacyEvidence := strings.Repeat("e", maxFindingNarrativeBytes+1)
	if _, err := s.CreateFinding(&Finding{
		Title: "canonical plus legacy evidence", Body: canonical, Evidence: legacyEvidence,
	}); err == nil || !strings.Contains(err.Error(), "narrative too large") {
		t.Fatalf("CreateFinding canonical+legacy error=%v, want aggregate narrative cap", err)
	}
	if err := s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, &legacyEvidence, nil, &canonical, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "narrative too large") {
		t.Fatalf("UpdateFindingCanonical canonical+legacy error=%v, want aggregate narrative cap", err)
	}
}

func TestUpdateFindingBodyRejectsUnknownFlowID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _ := s.CreateFinding(&Finding{Title: "bad flow"})
	body := `[{"type":"flow","flowId":99999,"note":"ghost"}]`
	err = s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "flow") {
		t.Fatalf("want flow-not-found error, got %v", err)
	}
}

func TestNormalizeFindingBodyCoerceAndReject(t *testing.T) {
	out, err := NormalizeFindingBody(`[{"type":"md","md":"hello"},{"type":"markdown","md":"world"}]`)
	if err != nil {
		t.Fatal(err)
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(out), &recs); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Type != "text" || recs[0].MD != "hello" || recs[1].Type != "text" {
		t.Fatalf("coerce=%+v", recs)
	}
	_, err = NormalizeFindingBody(`[{"type":"essay","md":"nope"}]`)
	if err == nil || !strings.Contains(err.Error(), "type must be") {
		t.Fatalf("want type error, got %v", err)
	}
	for _, field := range []string{"role", "source"} {
		body := `[{"type":"text","` + field + `":"typo"}]`
		if _, err := NormalizeFindingBody(body); err == nil {
			t.Fatalf("invalid %s should fail", field)
		}
	}
}

func TestFindingConfidenceValidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateFinding(&Finding{Title: "x", Confidence: "guess"}); err == nil {
		t.Fatal("invalid confidence should fail")
	}
	id, err := s.CreateFinding(&Finding{Title: "x", Confidence: "confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	v := "unknown"
	if err := s.UpdateFindingEnvelope(id, nil, &v, nil); err == nil {
		t.Fatal("invalid update confidence should fail")
	}
}

func TestFindingBlockMetadataRoundTripAndDuplicateFlowNormalization(t *testing.T) {
	body := `[{"type":"flow","flowId":7,"role":"before","proof":"baseline request is authenticated","source":"captured_flow","sourceFlowId":7,"note":"baseline"},{"type":"flow","flowId":7,"role":"result","proof":"baseline request is authenticated","source":"captured_flow","sourceFlowId":7,"note":"duplicate"}]`
	out, err := NormalizeFindingBody(body)
	if err != nil {
		t.Fatal(err)
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(out), &recs); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Role != "baseline" || recs[0].Proof != "baseline request is authenticated" || recs[0].Source != "captured_flow" || recs[0].SourceFlowID != 7 {
		t.Fatalf("normalized records=%+v", recs)
	}
}

func TestUpdateFindingAllowsPreviouslyAttachedPurgedFlowButRejectsNewUnknown(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fid, err := s.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateFinding(&Finding{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachFlow(id, fid, "proof", -1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM flows WHERE id=?`, fid); err != nil {
		t.Fatal(err)
	}
	body := marshalBody([]FindingBlock{{Type: "text", MD: "edited"}, {Type: "flow", FlowID: fid, Role: "result"}})
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("purged reference should survive edit: %v", err)
	}
	unknown := marshalBody([]FindingBlock{{Type: "flow", FlowID: 99999}})
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &unknown, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("new unknown flow should fail")
	}
}

func TestUpdateFindingPreservesPersistedMissingFlowMarker(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	flowID, err := s.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateFinding(&Finding{Title: "merged orphan"})
	if err != nil {
		t.Fatal(err)
	}
	persisted := marshalBody([]FindingBlock{{Type: "flow", FlowID: flowID, Note: "purged peer flow", Missing: true}})
	if _, err := s.db.Exec(`UPDATE findings SET body=? WHERE id=?`, persisted, id); err != nil {
		t.Fatal(err)
	}

	replacement := marshalBody([]FindingBlock{{Type: "flow", FlowID: flowID, Note: "edited note"}})
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &replacement, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 1 || !got.Blocks[0].Missing || got.Blocks[0].Note != "edited note" {
		t.Fatalf("missing marker was not retained: %+v", got.Blocks)
	}
	if len(got.Flows) != 0 {
		t.Fatalf("missing peer evidence attached to unrelated local flow: %+v", got.Flows)
	}
}

func TestFindingReadinessSummary(t *testing.T) {
	f := &Finding{Title: "x", Summary: "summary", Target: "example.com", Impact: "impact", Why: "why", Fix: "fix", Retest: "retest", Confidence: "firm", Severity: "Medium", Blocks: []FindingBlock{{Type: "image", Hash: "abc", Role: "result", Missing: false, Proof: "screen proves impact"}, {Type: "flow", FlowID: 1, Missing: false, Proof: "response proves impact"}}}
	r := f.ReadinessSummary()
	if r.Stage != "report_ready" || r.ImageCount != 1 || r.ScreenshotCount != 1 || r.AnnotatedEvidenceCount != 2 || r.VisualProofRecommended {
		t.Fatalf("summary=%+v", r)
	}
}

func TestFindingReadinessScreenshotOnlyCanBeReportReady(t *testing.T) {
	f := &Finding{Title: "x", Summary: "summary", Target: "example.com", Impact: "impact", Why: "why", Fix: "fix", Retest: "verify control is enforced", Confidence: "certain", Blocks: []FindingBlock{{Type: "image", Role: "result", Proof: "browser shows unauthorized data"}}}
	if got := f.ReadinessSummary().Stage; got != "report_ready" {
		t.Fatalf("stage=%q", got)
	}
}

func TestFindingReadinessDoesNotRequireProofOnTextSteps(t *testing.T) {
	f := &Finding{
		Title: "x", Summary: "summary", Target: "example.com", Impact: "impact", Why: "why",
		Fix: "fix", Retest: "verify control is enforced", Confidence: "certain",
		Blocks: []FindingBlock{
			{Type: "text", Role: "baseline", MD: "Sign in as the lower-privileged user."},
			{Type: "text", Role: "action", MD: "Request another user's resource."},
			{Type: "text", Role: "result", MD: "Observe the unauthorized response."},
			{Type: "image", Role: "result", Proof: "browser shows the unauthorized resource"},
		},
	}
	r := f.ReadinessSummary()
	if r.Stage != "report_ready" || len(r.Gaps) != 0 || r.AnnotatedEvidenceCount != 1 {
		t.Fatalf("summary=%+v", r)
	}
}

func TestFindingReadinessDoesNotTreatLegacyFlowCaptionAsProof(t *testing.T) {
	f := &Finding{
		Title: "x", Summary: "summary", Target: "example.com", Impact: "impact", Why: "why",
		Fix: "fix", Retest: "verify control is enforced", Confidence: "certain",
		Blocks: []FindingBlock{{Type: "flow", FlowID: 1, Role: "result", Note: "Cross-account response"}},
	}
	r := f.ReadinessSummary()
	hasProofGap := false
	for _, gap := range r.Gaps {
		hasProofGap = hasProofGap || gap == "proof"
	}
	if r.Stage == "report_ready" || r.AnnotatedEvidenceCount != 0 || !hasProofGap {
		t.Fatalf("legacy caption must remain distinct from proof: %+v", r)
	}
}

func TestFindingCompatibilityReadinessDerivesFromCanonicalEnvelope(t *testing.T) {
	f := &Finding{
		Severity: "Critical", Title: "Authorization bypass", Summary: "A user can read another account.",
		Target: "https://example.com/orders/2", Impact: "Sensitive order data is disclosed.",
		Why: "The object authorization check is missing.", Fix: "Enforce ownership before loading the order.",
		Retest: "Repeat the cross-account request and confirm a denial.", Confidence: "certain",
		Blocks: []FindingBlock{{Type: "text", Role: "action", MD: "Request the other account's order."},
			{Type: "image", Role: "result", Proof: "The response contains the other account's order."}},
	}
	f.EnrichCompleteness()
	if !f.Ready || len(f.Missing) != 0 {
		t.Fatalf("compatibility readiness=%t missing=%v", f.Ready, f.Missing)
	}
	if f.Readiness == nil || f.Readiness.Stage != "report_ready" || len(f.Readiness.Gaps) != 0 {
		t.Fatalf("canonical readiness=%+v", f.Readiness)
	}
	for _, gap := range f.Missing {
		if gap == "poc_before_after" {
			t.Fatal("legacy compatibility surface must not impose a universal differential requirement")
		}
	}
}

func TestMarshalFindingBlocksRejectsUnknownMetadataBeforeNormalization(t *testing.T) {
	for _, block := range []FindingBlock{
		{Type: "text", MD: "x", Role: "not-a-role"},
		{Type: "image", Hash: "a", Source: "not-a-source"},
	} {
		if _, err := MarshalFindingBlocks([]FindingBlock{block}); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("block metadata should be rejected, block=%+v err=%v", block, err)
		}
	}
}

func TestUpdateFindingLegacyEvidenceKeepsCanonicalBodyInSync(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "legacy", Detail: "reproduction", Evidence: "old observation"})
	if err != nil {
		t.Fatal(err)
	}
	next := "new observation"
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, &next, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Evidence != next || len(f.Blocks) != 2 || f.Blocks[1].MD != next || strings.Contains(f.Body, "old observation") {
		t.Fatalf("legacy evidence/body diverged: evidence=%q body=%q blocks=%+v", f.Evidence, f.Body, f.Blocks)
	}
}

func TestUpdateFindingCanonicalBodyClearsStaleLegacyText(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "legacy", Detail: "old step", Evidence: "old proof"})
	if err != nil {
		t.Fatal(err)
	}

	body := `[{"type":"text","md":"new step"}]`
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Detail != "new step" || got.Evidence != "" {
		t.Fatalf("legacy fields diverged after canonical replacement: detail=%q evidence=%q", got.Detail, got.Evidence)
	}

	body = `[]`
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, &body, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Detail != "" || got.Evidence != "" {
		t.Fatalf("empty canonical body retained legacy text: detail=%q evidence=%q", got.Detail, got.Evidence)
	}
}

func fmtInt64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
