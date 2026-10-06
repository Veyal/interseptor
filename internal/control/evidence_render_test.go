package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/store"
)

const testRunID = "abcd1234abcd1234"

func syntheticRun(runID string) intruder.RunRecord {
	st := intruder.State{
		RunID: runID, StartedTs: 1700000000000, Attack: "repeat", Threads: 4, DelayMs: 0,
		TargetHost: "example.com", Barrier: true, Total: 24, Done: 24,
	}
	for i := 1; i <= 24; i++ {
		r := intruder.Result{
			ID: i, Seq: i, Worker: (i-1)%4 + 1, Payload: fmt.Sprintf("user%02d", i), Status: 200, Length: 512,
			TimeMs: 40, FlowID: int64(i), StartUs: int64(i * 500), EndUs: int64(i*500 + 40000),
			BodyHash: "h1", Matched: i%3 == 0, Extracted: "coupon-7",
		}
		if i > 16 {
			r.Status, r.Length, r.BodyHash = 429, 80, "h2"
			r.RLHeaders = map[string]string{"Retry-After": "30"}
		}
		st.Results = append(st.Results, r)
	}
	return intruder.RunRecord{State: st, Spec: intruder.SpecSummary{Attack: "repeat", Target: "example.com", Threads: 4, GrepMatch: "ok"}}
}

func seedRun(t *testing.T, s *store.Store, rec intruder.RunRecord) {
	t.Helper()
	persistIntruderRun(s, rec)
	if _, ok, err := s.GetIntruderRun(rec.State.RunID); err != nil || !ok {
		t.Fatalf("run not persisted: ok=%v err=%v", ok, err)
	}
}

func getBytes(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func requirePNG(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "private, max-age=60" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if _, err := png.Decode(bytes.NewReader(body)); err != nil {
		t.Fatalf("png decode: %v", err)
	}
}

func TestIntruderAttackRenderAllKinds(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, kind := range []string{"timeline", "distribution", "race", "strip"} {
		resp, body := getBytes(t, ts.URL+"/api/intruder/attacks/"+testRunID+"/render.png?kind="+kind)
		requirePNG(t, resp, body)
	}
	// default kind and the latest alias
	resp, body := getBytes(t, ts.URL+"/api/intruder/attacks/latest/render.png?width=800")
	requirePNG(t, resp, body)
	cfg, _ := png.DecodeConfig(bytes.NewReader(body))
	if cfg.Width != 800 {
		t.Fatalf("width = %d, want 800", cfg.Width)
	}
}

func TestIntruderAttackRenderJSONVariant(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, body := getBytes(t, ts.URL+"/api/intruder/attacks/latest/render.png?kind=race&format=json")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
	var out struct {
		Alt, Summary, Kind, SourceRef string
		Width, Height                 int
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Alt == "" || out.Summary == "" || out.Kind == "" || out.Width == 0 || out.Height == 0 {
		t.Fatalf("incomplete json: %+v", out)
	}
	if out.SourceRef != "intruder:"+testRunID {
		t.Fatalf("sourceRef = %q (alias must resolve to the real run id)", out.SourceRef)
	}
}

func TestIntruderAttackErrors(t *testing.T) {
	h, s, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	if resp, _ := getBytes(t, ts.URL+"/api/intruder/attacks/latest/render.png"); resp.StatusCode != 404 {
		t.Fatalf("latest with no runs = %d, want 404", resp.StatusCode)
	}
	seedRun(t, s, syntheticRun(testRunID))
	if resp, _ := getBytes(t, ts.URL+"/api/intruder/attacks/nope/render.png"); resp.StatusCode != 404 {
		t.Fatalf("unknown run = %d, want 404", resp.StatusCode)
	}
	if resp, _ := getBytes(t, ts.URL+"/api/intruder/attacks/nope"); resp.StatusCode != 404 {
		t.Fatalf("unknown record = %d, want 404", resp.StatusCode)
	}
	if resp, _ := getBytes(t, ts.URL+"/api/intruder/attacks/"+testRunID+"/render.png?kind=bogus"); resp.StatusCode != 400 {
		t.Fatalf("bad kind = %d, want 400", resp.StatusCode)
	}
	if resp, _ := getBytes(t, ts.URL+"/api/intruder/attacks/"+testRunID+"/render.png?width=abc"); resp.StatusCode != 400 {
		t.Fatalf("bad width = %d, want 400", resp.StatusCode)
	}
}

func TestIntruderAttackListAndGet(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, body := getBytes(t, ts.URL+"/api/intruder/attacks")
	if resp.StatusCode != 200 {
		t.Fatalf("list %d", resp.StatusCode)
	}
	var list struct {
		Attacks []store.RunMeta `json:"attacks"`
		Latest  string          `json:"latest"`
	}
	if err := json.Unmarshal(body, &list); err != nil || len(list.Attacks) != 1 || list.Latest != testRunID || list.Attacks[0].Attack != "repeat" {
		t.Fatalf("list = %s err=%v", body, err)
	}
	_, body = getBytes(t, ts.URL+"/api/intruder/attacks/"+testRunID)
	var rec struct {
		RunID string `json:"runId"`
		State struct {
			Results []map[string]any `json:"results"`
		} `json:"state"`
	}
	if err := json.Unmarshal(body, &rec); err != nil || rec.RunID != testRunID || len(rec.State.Results) != 24 {
		t.Fatalf("record = %.200s err=%v", body, err)
	}
}

func TestFlowRendersDiffWaterfallChain(t *testing.T) {
	h, s, _ := newHub(t)
	base := time.UnixMilli(1700000000000)
	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := s.InsertFlow(&store.Flow{
			TS: base.Add(time.Duration(i) * 250 * time.Millisecond), Method: "GET", Scheme: "https", Host: "example.com",
			Path: "/api/item/" + strconv.Itoa(i), Status: 200 + i*100, DurationMs: int64(40 + i*30),
			ReqHeaders: map[string][]string{"Authorization": {"Bearer SECRETTOKEN0123456789"}},
			ResHeaders: map[string][]string{"Content-Type": {"application/json"}, "X-Variant": {strconv.Itoa(i)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	f1, _ := s.CreateFinding(&store.Finding{Title: "IDOR on item", Severity: "High"})
	f2, _ := s.CreateFinding(&store.Finding{Title: "Token leak", Severity: "Medium"})
	rel := []store.FindingRelation{{ID: f2, Relation: "enables"}}
	if err := s.UpdateFindingMetadata(f1, store.FindingMetadataPatch{RelatedFindings: &rel}); err != nil {
		t.Fatalf("seed relations: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, u := range []string{
		fmt.Sprintf("/api/render/flow-diff.png?a=%d&b=%d", ids[0], ids[1]),
		fmt.Sprintf("/api/render/flow-waterfall.png?ids=%d,%d,%d", ids[0], ids[1], ids[2]),
		fmt.Sprintf("/api/render/finding-chain.png?findingId=%d", f1),
	} {
		resp, body := getBytes(t, ts.URL+u)
		requirePNG(t, resp, body)
	}
	for u, want := range map[string]int{
		"/api/render/flow-diff.png?a=1":                                                     400,
		"/api/render/flow-diff.png?a=9999&b=9998":                                           404,
		"/api/render/flow-waterfall.png":                                                    400,
		"/api/render/flow-waterfall.png?ids=9999":                                           404,
		"/api/render/flow-waterfall.png?ids=" + manyIDs(51):                                 400,
		"/api/render/finding-chain.png?findingId=9999":                                      404,
		"/api/render/finding-chain.png":                                                     400,
		"/api/render/authz/doesnotexist.png":                                                404,
		"/api/render/flow-waterfall.png?ids=1,x":                                            400,
		fmt.Sprintf("/api/render/flow-diff.png?a=%d&b=%d", ids[0], ids[1]) + "&format=json": 200,
	} {
		if resp, body := getBytes(t, ts.URL+u); resp.StatusCode != want {
			t.Errorf("%s = %d (%s), want %d", u, resp.StatusCode, body, want)
		}
	}
}

func manyIDs(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = strconv.Itoa(i + 1)
	}
	return strings.Join(p, ",")
}

func TestAuthzRunCaptureAndMatrixRender(t *testing.T) {
	h, _, _ := newHub(t)
	ev := newEvidenceAPI(h)
	runs := []authzRunOut{{
		FlowID: 1, Method: "GET", Host: "example.com", Path: "/api/orders/1", BaselineStatus: 200,
		Results: []authzResult{
			{Name: "admin", Status: 200, Length: 900, FlowID: 1},
			{Name: "user", Status: 200, Length: 900, Same: true, FlowID: 2},
			{Name: "anon", Status: 401, Length: 20, AccessDenied: true, FlowID: 3},
		},
	}}
	body, _ := json.Marshal(map[string]any{"runs": runs, "summary": map[string]any{"endpoints": 1}})
	wrapped := ev.captureAuthzRun(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest("POST", "/api/authz/run", nil))
	var out struct {
		RunID   string          `json:"runId"`
		Runs    []authzRunOut   `json:"runs"`
		Summary json.RawMessage `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.RunID == "" || len(out.Runs) != 1 {
		t.Fatalf("captured response = %s err=%v", rec.Body.String(), err)
	}
	// matrix semantics
	in := authzMatrixInput(out.RunID, runs)
	if in.BaselineName != "admin" || len(in.Cols) != 3 || !in.Rows[0].Cells[1].Broken || in.Rows[0].Cells[0].Broken || in.Rows[0].Cells[2].Broken {
		t.Fatalf("matrix = %+v", in)
	}
	e2 := httptest.NewServer(h.Handler())
	defer e2.Close()
	// the real route captures through the hub's own cache; use evidence API directly
	res, err := ev.render(evidenceRequest{Kind: "authz_matrix", RunID: out.RunID})
	if err != nil || len(res.R.PNG) == 0 || res.SourceRef != "authz:"+out.RunID {
		t.Fatalf("render = %v %+v", err, res.SourceRef)
	}
	if _, err := ev.render(evidenceRequest{Kind: "authz_matrix", RunID: "latest"}); err != nil {
		t.Fatalf("latest alias: %v", err)
	}
}

func newFindingID(t *testing.T, s *store.Store) int64 {
	t.Helper()
	id, err := s.CreateFinding(&store.Finding{Title: "rate limit missing"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func evPost(t *testing.T, url, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestAttachEvidenceRenderIntruder(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	fid := newFindingID(t, s)
	ch := make(chan string, 16)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	url := ts.URL + "/api/findings/" + strconv.FormatInt(fid, 10) + "/evidence-render"

	resp, body := evPost(t, url, `{"kind":"timeline","attackId":"latest","role":"observation","source":"browser_screenshot","sourceRef":"spoof"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("attach %d: %s", resp.StatusCode, body)
	}
	got, _ := s.GetFinding(fid)
	var img *store.FindingBlock
	for i := range got.Blocks {
		if got.Blocks[i].Type == "image" {
			img = &got.Blocks[i]
		}
	}
	if img == nil {
		t.Fatalf("no image block: %+v", got.Blocks)
	}
	if img.Source != "evidence_render" || img.SourceRef != "intruder:"+testRunID || img.Caption == "" {
		t.Fatalf("block = %+v", img)
	}
	if img.Provenance == nil || img.Provenance.Ingestion == "" {
		t.Fatalf("missing generated provenance: %+v", img.Provenance)
	}
	sawUpdate := false
drain:
	for {
		select {
		case m := <-ch:
			sawUpdate = sawUpdate || strings.Contains(m, "findings.update")
		default:
			break drain
		}
	}
	if !sawUpdate {
		t.Fatal("findings.update not broadcast")
	}

	// A second attach of a different run's render must not overwrite the first image's provenance.
	resp, body = evPost(t, url, `{"kind":"timeline","runId":"`+testRunID+`","caption":"again"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("second attach %d: %s", resp.StatusCode, body)
	}
	got, _ = s.GetFinding(fid)
	for _, b := range got.Blocks {
		if b.Type == "image" && (b.Source != "evidence_render" || b.SourceRef != "intruder:"+testRunID) {
			t.Fatalf("provenance changed: %+v", b)
		}
	}
}

func TestAttachEvidenceRenderErrors(t *testing.T) {
	h, s, _ := newHub(t)
	seedRun(t, s, syntheticRun(testRunID))
	fid := newFindingID(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	base := ts.URL + "/api/findings/"
	url := base + strconv.FormatInt(fid, 10) + "/evidence-render"
	if resp, _ := evPost(t, base+"9999/evidence-render", `{"kind":"timeline","attackId":"latest"}`); resp.StatusCode != 404 {
		t.Fatalf("missing finding = %d", resp.StatusCode)
	}
	if resp, _ := evPost(t, url, `{"kind":"nonsense"}`); resp.StatusCode != 400 {
		t.Fatalf("bad kind = %d", resp.StatusCode)
	}
	if resp, _ := evPost(t, url, `{"kind":"timeline","attackId":"missing"}`); resp.StatusCode != 404 {
		t.Fatalf("missing run = %d", resp.StatusCode)
	}
	big := `{"kind":"timeline","attackId":"latest","caption":"` + strings.Repeat("x", 70<<10) + `"}`
	if resp, _ := evPost(t, url, big); resp.StatusCode != 413 {
		t.Fatalf("oversize = %d, want 413", resp.StatusCode)
	}
}

func TestAttachEvidenceRenderWaterfallSingleFlowSetsSourceFlow(t *testing.T) {
	h, s, _ := newHub(t)
	id, _ := s.InsertFlow(&store.Flow{TS: time.UnixMilli(1700000000000), Method: "GET", Host: "example.com", Path: "/a", Status: 200, DurationMs: 12})
	fid := newFindingID(t, s)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, body := evPost(t, ts.URL+"/api/findings/"+strconv.FormatInt(fid, 10)+"/evidence-render", fmt.Sprintf(`{"kind":"flow-waterfall","flowIds":[%d]}`, id))
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	got, _ := s.GetFinding(fid)
	for _, b := range got.Blocks {
		if b.Type == "image" && (b.SourceFlowID != id || b.SourceRef != fmt.Sprintf("flows:%d", id)) {
			t.Fatalf("block = %+v", b)
		}
	}
}

func TestEvidenceAdaptersRedactCredentials(t *testing.T) {
	const secret = "SUPERSECRETTOKENVALUE0123456789"
	rec := syntheticRun(testRunID)
	rec.State.Results[0].Payload = "Authorization: Bearer " + secret
	rec.State.Results[1].Payload = "password=hunter2hunter2"
	rec.State.Results[2].Extracted = "token=" + secret
	rec.State.Results[3].RLHeaders = map[string]string{"Set-Cookie": "sid=" + secret}
	rec.Spec.GrepMatch = "Cookie: " + secret
	env, ok := newIntruderEnvelope(rec)
	if !ok {
		t.Fatal("envelope")
	}
	out := fmt.Sprintf("%+v|%+v|%+v|%+v", intruderStripInput(env, false), intruderRaceInput(env), intruderTimelineInput(env), intruderDistributionInput(env))
	for _, leak := range []string{secret, "hunter2hunter2", "Bearer " + secret} {
		if strings.Contains(out, leak) {
			t.Fatalf("adapter output leaks %q: %s", leak, out)
		}
	}
	if !strings.Contains(fmt.Sprintf("%+v", intruderStripInput(env, false).Rows[0].Payload), "redacted") {
		t.Fatalf("payload not redacted: %+v", intruderStripInput(env, false).Rows[0])
	}
}

func TestFlowDiffInputRedactsHeadersAndBody(t *testing.T) {
	const secret = "TOPSECRETCOOKIEVALUE12345"
	fa := &store.Flow{ID: 1, Method: "GET", Scheme: "https", Host: "example.com", Path: "/x?token=" + secret, Status: 200}
	fb := &store.Flow{ID: 2, Method: "GET", Scheme: "https", Host: "example.com", Path: "/x", Status: 403}
	d := flowDiff{
		HeaderDeltas: []headerDelta{{Name: "Set-Cookie", A: "sid=" + secret, Kind: "removed"}, {Name: "X-Note", B: "Bearer " + secret, Kind: "added"}},
		BodyDeltas:   []bodyLineDelta{{Line: 1, A: `{"access_token":"` + secret + `"}`, B: `{"error":"denied"}`}},
		Summary:      "status changed",
	}
	in := flowDiffInput(fa, fb, d)
	if s := fmt.Sprintf("%+v", in); strings.Contains(s, secret) {
		t.Fatalf("diff input leaks secret: %s", s)
	}
	if len(in.BodyDeltas) != 2 || in.BodyDeltas[0].Kind != "-" || in.BodyDeltas[1].Kind != "+" {
		t.Fatalf("body deltas = %+v", in.BodyDeltas)
	}
}

func TestChainInputNormalizesRelations(t *testing.T) {
	fake := fakeFinder{
		1: {ID: 1, Title: "A", Severity: "High", RelatedFindings: []store.FindingRelation{{ID: 2, Relation: "enables"}}},
		2: {ID: 2, Title: "B", Severity: "Low", RelatedFindings: []store.FindingRelation{{ID: 1, Relation: "enabled_by"}, {ID: 3, Relation: "chain"}}},
		3: {ID: 3, Title: "C", Severity: "Info", RelatedFindings: []store.FindingRelation{{ID: 2, Relation: "chain"}}},
	}
	in, err := chainInputFrom(fake, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Nodes) != 3 || len(in.Edges) != 2 {
		t.Fatalf("graph = %+v", in)
	}
	if in.Edges[0] != (previewChainEdge{From: "1", To: "2", Kind: "enables"}) {
		t.Fatalf("edge0 = %+v", in.Edges[0])
	}
}

type previewChainEdge = struct{ From, To, Kind string }

type fakeFinder map[int64]*store.Finding

func (f fakeFinder) GetFinding(id int64) (*store.Finding, error) {
	if v, ok := f[id]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("missing")
}

func TestWaterfallSourceRefStaysWithinLimit(t *testing.T) {
	ids := make([]int64, 50)
	for i := range ids {
		ids[i] = int64(100000 + i)
	}
	if ref := waterfallSourceRef(ids); len(ref) > 80 {
		t.Fatalf("ref too long: %d", len(ref))
	}
}
