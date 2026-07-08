package autopwn

// This is a REAL end-to-end integration test for the autonomous-pentest engine.
// Unlike autopwn_test.go — which fakes the Gate-1 sender and the OOB poller — this
// test exercises the ACTUAL verification path over a real (localhost) network:
//
//   - a real *sender.Sender re-issues every Gate-1 baseline/payload request to a
//     live, deliberately-vulnerable httptest.Server (VerifySender left nil so the
//     engine builds the real senderAdapter over the store+sender);
//   - the real differential oracle (verify.ReproduceDifferential + the reflected /
//     error signatures) confirms the differential from bodies loaded back out of
//     the content-addressed store;
//   - a real oob.Catcher + the real oobAdapter correlate a blind callback (Gate 3),
//     with the vulnerable endpoint performing a genuine server-side fetch to the
//     minted token URL (VerifyOOB left nil so the real adapter runs);
//   - findings + finding_verification proof-records land in a real store.
//
// ONLY the LLM's cognition is mocked: the planning turn and the adversarial-verifier
// (Gate 2) verdict come from a scripted aiagent.ToolCaller. That is legitimate — the
// machine ground truth in this test is Gate 1/Gate 3 reproducing over the wire, not
// the model's judgement — and it keeps the test free, deterministic, and CI-runnable.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/oob"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/verify"
)

// vulnServer is a deliberately-vulnerable HTTP server whose flaws are
// deterministically reproducible by the real Gate-1 oracle. It counts every hit
// per endpoint so a test can prove the verifier actually re-sent over the wire.
type vulnServer struct {
	srv *httptest.Server

	search atomic.Int64 // GET /search?q=…  (reflected marker)
	item   atomic.Int64 // GET /item?id=…    (SQL error signature)
	safe   atomic.Int64 // GET /safe?q=…     (non-vulnerable control)
	fetch  atomic.Int64 // GET /fetch?url=…  (blind SSRF: server-side callback)

	oobHits atomic.Int64 // callbacks the /fetch handler actually made
}

func newVulnServer(t *testing.T) *vulnServer {
	t.Helper()
	vs := &vulnServer{}
	mux := http.NewServeMux()

	// Reflected marker: q is echoed UNESCAPED into the body. A baseline value
	// (q=safe) never contains the payload marker, so ReflectedMarkerHeld holds
	// only for the payload request.
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		vs.search.Add(1)
		q := r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><body>Results for: %s</body></html>", q)
	})

	// Error signature: a clean id returns a benign 200; an id containing a single
	// quote returns a body carrying a DB error string the oracle's dbErrRe matches
	// ("SQL syntax"). The baseline (clean id) never contains that string.
	mux.HandleFunc("/item", func(w http.ResponseWriter, r *http.Request) {
		vs.item.Add(1)
		id := r.URL.Query().Get("id")
		w.Header().Set("Content-Type", "text/html")
		if strings.Contains(id, "'") {
			// Matches dbErrRe: "You have an error in your SQL syntax".
			io.WriteString(w, "<html>Database error: You have an error in your SQL syntax near \"'\"</html>")
			return
		}
		fmt.Fprintf(w, "<html>Item %s: in stock</html>", id)
	})

	// Non-vulnerable control: always the same static body regardless of input, so
	// no marker ever reflects and no error string ever appears.
	mux.HandleFunc("/safe", func(w http.ResponseWriter, r *http.Request) {
		vs.safe.Add(1)
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><body>Static catalog page.</body></html>")
	})

	// Blind SSRF: the server dereferences the url parameter server-side. When it
	// points at the injected OOB callback, the server actually fetches it — which
	// is what the real oob.Catcher records as ground truth.
	mux.HandleFunc("/fetch", func(w http.ResponseWriter, r *http.Request) {
		vs.fetch.Add(1)
		target := r.URL.Query().Get("url")
		if target != "" {
			// Genuine server-side request to the injected callback URL.
			if resp, err := http.Get(target); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				vs.oobHits.Add(1)
			}
		}
		io.WriteString(w, "<html>fetched</html>")
	})

	vs.srv = httptest.NewServer(mux)
	t.Cleanup(vs.srv.Close)
	return vs
}

func (vs *vulnServer) url(path string) string { return vs.srv.URL + path }

// hostPort splits the httptest server's host:port so a scope rule can target it.
func (vs *vulnServer) hostPort(t *testing.T) (host string, port int) {
	t.Helper()
	u, err := url.Parse(vs.srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}
	return u.Hostname(), p
}

// e2eBrain scripts only the LLM: the planning turn yields an empty plan (no tool
// steps to drive, since candidates are injected directly) and the adversarial
// verifier (Gate 2) returns "real". A single scripted turn serves both agent runs.
func e2eToolCaller() (aiagent.ToolCaller, error) {
	return &fakeCaller{turns: []aiagent.Turn{{Text: `{"result":"real","reasoning":"reproduced over the wire"}`}}}, nil
}

// realDeps wires the engine to REAL infra: real store, real capture+sender, real
// oob catcher. VerifySender / VerifyOOB are deliberately LEFT NIL so the engine
// builds its real senderAdapter / oobAdapter and re-sends actually hit the wire.
func realDeps(t *testing.T, s *store.Store, catcher *oob.Catcher, w *doneWaiter, cands []Candidate) Deps {
	t.Helper()
	sn := sender.New(s, capture.New(s))
	return Deps{
		Store:         s,
		Sender:        sn,
		OOB:           catcher,
		NewToolCaller: e2eToolCaller,
		ToolExecutor:  &fakeExec{}, // planning drives no real tools (empty plan)
		// VerifySender + VerifyOOB intentionally nil → real adapters over the wire.
		CollectCandidates: func(ctx context.Context, e *Engine, plan Plan) []Candidate {
			return cands
		},
		Broadcast:      w.broadcast,
		RecordActivity: func(store.Activity) {},
		AskHuman: func(ctx context.Context, msg string, opts []string) (string, error) {
			return confirmOption, nil
		},
		Clock: &fakeClock{},
	}
}

// scopeForServer authorizes the httptest server's exact host:port for the run so
// the boundary guard allows the re-sends (a run refuses to probe out-of-scope).
func scopeForServer(t *testing.T, s *store.Store, vs *vulnServer) {
	t.Helper()
	host, port := vs.hostPort(t)
	if _, err := s.CreateScopeRule(&store.ScopeRule{
		Enabled: true, Action: "include", Host: host, Port: port,
	}); err != nil {
		t.Fatalf("CreateScopeRule: %v", err)
	}
}

// e2eReflectedCandidate targets the live /search endpoint with a unique marker
// payload vs a clean baseline. N=2 ⇒ the oracle must hold twice (real re-sends).
func e2eReflectedCandidate(vs *vulnServer) Candidate {
	const marker = "E2EPWNMARKER7788"
	return Candidate{
		VulnClass: "xss-reflected",
		Severity:  verify.SeverityMedium, // <High ⇒ Gate 4 (human) skipped
		Target:    vs.url("/search"),
		Point:     "param q",
		Summary:   "q reflected unescaped into the response body",
		Diff: verify.DiffSpec{
			Class:    verify.ClassReflected,
			Marker:   marker,
			N:        2,
			Baseline: verify.Request{Method: "GET", URL: vs.url("/search?q=safe")},
			Payload:  verify.Request{Method: "GET", URL: vs.url("/search?q=" + marker)},
		},
	}
}

// e2eErrorCandidate targets the live /item endpoint: a single-quote payload
// surfaces a SQL error signature the baseline (clean id) lacks. N=2.
func e2eErrorCandidate(vs *vulnServer) Candidate {
	return Candidate{
		VulnClass: "sqli-error",
		Severity:  verify.SeverityMedium,
		Target:    vs.url("/item"),
		Point:     "param id",
		Summary:   "single-quote id surfaces a DB error signature",
		Diff: verify.DiffSpec{
			Class:    verify.ClassError,
			N:        2,
			Baseline: verify.Request{Method: "GET", URL: vs.url("/item?id=42")},
			Payload:  verify.Request{Method: "GET", URL: vs.url("/item?id=42'")},
		},
	}
}

// e2eSafeCandidate targets the NON-vulnerable /safe endpoint: the marker never
// reflects, so Gate 1 rejects it over the real wire → no finding.
func e2eSafeCandidate(vs *vulnServer) Candidate {
	const marker = "E2ESAFEMARKER9911"
	return Candidate{
		VulnClass: "xss-reflected",
		Severity:  verify.SeverityMedium,
		Target:    vs.url("/safe"),
		Point:     "param q",
		Summary:   "control endpoint that never reflects",
		Diff: verify.DiffSpec{
			Class:    verify.ClassReflected,
			Marker:   marker,
			N:        2,
			Baseline: verify.Request{Method: "GET", URL: vs.url("/safe?q=safe")},
			Payload:  verify.Request{Method: "GET", URL: vs.url("/safe?q=" + marker)},
		},
	}
}

// waitRunDone polls State() until the run is no longer active, with a timeout —
// belt-and-suspenders alongside the broadcast barrier.
func waitRunDone(t *testing.T, e *Engine) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !e.State().Active {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("run did not become inactive within 10s")
}

// findingByTarget returns the single filed finding whose Target has the given path
// suffix, or fails.
func findingForPath(t *testing.T, s *store.Store, pathSuffix string) store.Finding {
	t.Helper()
	findings, err := s.ListFindings("", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	for _, f := range findings {
		if strings.HasSuffix(f.Target, pathSuffix) {
			return f
		}
	}
	t.Fatalf("no finding targeting %q; got %d findings: %+v", pathSuffix, len(findings), findings)
	return store.Finding{}
}

// TestE2E_RealWire_ReflectedAndErrorFileNegativeRejected is the core E2E proof:
// the reflected-XSS and error-SQLi candidates each reproduce over the REAL wire and
// file a verified finding with a proof-record + PoC flows, while the non-vulnerable
// control candidate is rejected at Gate 1 (real differential over the wire) and
// files nothing. Hit counters prove the sender genuinely re-sent to the server.
func TestE2E_RealWire_ReflectedAndErrorFileNegativeRejected(t *testing.T) {
	vs := newVulnServer(t)
	s := openStore(t)
	scopeForServer(t, s, vs)
	catcher := oob.New()
	w := newDoneWaiter()

	cands := []Candidate{
		e2eReflectedCandidate(vs),
		e2eErrorCandidate(vs),
		e2eSafeCandidate(vs), // negative: must be rejected at Gate 1
	}
	e := New(realDeps(t, s, catcher, w, cands))

	runID, err := e.Start(context.Background(), StartOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	waitRunDone(t, e)

	// --- Positive: exactly two verified findings (reflected + error). ---
	findings, err := s.ListFindings("", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected exactly 2 filed findings (reflected+error), got %d: %+v", len(findings), findings)
	}

	// Reflected finding: verified, ai-sourced, real proof-record over the wire.
	refl := findingForPath(t, s, "/search")
	if refl.Status != "verified" || refl.Source != "ai" {
		t.Fatalf("reflected finding not verified/ai: %+v", refl)
	}
	if len(refl.Flows) == 0 {
		t.Fatal("reflected finding has no PoC flows attached")
	}
	rver, err := s.GetFindingVerification(refl.ID)
	if err != nil {
		t.Fatalf("GetFindingVerification(reflected): %v", err)
	}
	if rver.RunID != runID {
		t.Fatalf("reflected verification runId=%d want %d", rver.RunID, runID)
	}
	if rver.VulnClass != "xss-reflected" {
		t.Fatalf("reflected verification class=%q", rver.VulnClass)
	}
	if rver.ReproCount <= 0 {
		t.Fatalf("reflected reproCount=%d, want > 0 (real re-sends)", rver.ReproCount)
	}
	if rver.Confidence <= 0 {
		t.Fatalf("reflected confidence=%d, want > 0", rver.Confidence)
	}

	// Error finding: same proof properties.
	errf := findingForPath(t, s, "/item")
	if errf.Status != "verified" || errf.Source != "ai" {
		t.Fatalf("error finding not verified/ai: %+v", errf)
	}
	if len(errf.Flows) == 0 {
		t.Fatal("error finding has no PoC flows attached")
	}
	ever, err := s.GetFindingVerification(errf.ID)
	if err != nil {
		t.Fatalf("GetFindingVerification(error): %v", err)
	}
	if ever.ReproCount <= 0 || ever.Confidence <= 0 {
		t.Fatalf("error proof weak: reproCount=%d confidence=%d", ever.ReproCount, ever.Confidence)
	}

	// --- Negative: the /safe candidate filed NOTHING. ---
	for _, f := range findings {
		if strings.HasSuffix(f.Target, "/safe") {
			t.Fatalf("non-vulnerable /safe candidate should not file a finding: %+v", f)
		}
	}

	// --- Real over-the-wire proof: the server actually got hit. ---
	// Reflected: N=2 attempts × (baseline + payload) = 4 hits on /search.
	if got := vs.search.Load(); got != 4 {
		t.Fatalf("/search hits=%d, want 4 (N=2 × baseline+payload real re-sends)", got)
	}
	// Error: N=2 attempts × (baseline + payload) = 4 hits on /item.
	if got := vs.item.Load(); got != 4 {
		t.Fatalf("/item hits=%d, want 4 (N=2 × baseline+payload real re-sends)", got)
	}
	// The negative candidate WAS probed (Gate 1 ran over the wire) but did not
	// reproduce. Gate 1 short-circuits on the first failing attempt: attempt 1
	// sends baseline + payload = 2 hits, then rejects.
	if got := vs.safe.Load(); got != 2 {
		t.Fatalf("/safe hits=%d, want 2 (one Gate-1 attempt: baseline+payload, then reject)", got)
	}

	// --- Engine state. ---
	st := e.State()
	if st.Active || st.Status != StatusDone {
		t.Fatalf("engine should be idle/done: %+v", st)
	}
	if st.Filed != 2 || st.Verified != 2 {
		t.Fatalf("counts wrong: filed=%d verified=%d (want 2/2): %+v", st.Filed, st.Verified, st)
	}
	if st.Rejected != 1 {
		t.Fatalf("expected 1 rejected (the /safe negative), got %d: %+v", st.Rejected, st)
	}

	// The recorded PoC flow bodies are genuinely in the store (loaded back by the
	// real senderAdapter to feed the oracle), i.e. the differential was over real
	// captured responses, not synthetic.
	assertPayloadFlowReflectsMarker(t, s, rver.PayloadFlow, "E2EPWNMARKER7788")
}

// assertPayloadFlowReflectsMarker loads the recorded payload flow's response body
// from the content-addressed store and confirms it carries the marker — proving the
// oracle judged a real captured response, not a fake exchange.
func assertPayloadFlowReflectsMarker(t *testing.T, s *store.Store, flowID int64, marker string) {
	t.Helper()
	if flowID == 0 {
		t.Fatal("no payload flow recorded on the proof-record")
	}
	f, err := s.GetFlow(flowID)
	if err != nil {
		t.Fatalf("GetFlow(%d): %v", flowID, err)
	}
	rc, err := s.OpenBody(f.ResBodyHash)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), marker) {
		t.Fatalf("recorded payload response body does not contain marker %q: %q", marker, body)
	}
}

// TestE2E_RealWire_BlindSSRF_OOBCorrelationFiles exercises the full blind path over
// the real wire: a real oob.Catcher mints a correlated token, the engine injects the
// callback URL into the probe, the LIVE vulnerable /fetch endpoint performs a real
// server-side GET to that exact token URL (recorded by the catcher), and the real
// oobAdapter confirms Gate 3. A High severity + confirming human gate then files. A
// second candidate whose endpoint does NOT call back is rejected at Gate 3.
func TestE2E_RealWire_BlindSSRF_OOBCorrelationFiles(t *testing.T) {
	vs := newVulnServer(t)
	s := openStore(t)
	scopeForServer(t, s, vs)
	catcher := oob.New()
	w := newDoneWaiter()

	// The OOB callback base points at the SAME httptest server: its /oob/<token>
	// path is what the catcher records tokens from (TokenFromPath). The vulnerable
	// /fetch endpoint will GET this URL server-side, and a handler on the same mux
	// funnels /oob/ hits into the catcher.
	// We add the /oob/ route here (after server start it's already registered on the
	// mux via a dedicated catcher-recording server below).
	oobRecorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<12))
		catcher.Record(r, string(body))
		w.WriteHeader(200)
	}))
	t.Cleanup(oobRecorder.Close)
	oobBase := oobRecorder.URL + "/oob/"

	// Blind candidate whose /fetch endpoint really calls back to the token URL.
	blind := e2eReflectedCandidate(vs) // reuse Diff shape; overridden below for blind
	blind.VulnClass = "ssrf-blind"
	blind.Severity = verify.SeverityHigh // exercise Gate 4 (human) too
	blind.Target = vs.url("/fetch")
	blind.Blind = true
	blind.OOBProbe = verify.Request{Method: "GET", URL: vs.url("/fetch?url=OOBHERE")}
	blind.OOBPlaceholder = "OOBHERE"
	// A non-blind Diff still runs as Gate 1 first; make it reproduce against a
	// live reflected endpoint so Gate 1 passes and we reach Gate 3. Point it at
	// /search (in scope) with a real marker.
	const marker = "E2EBLINDMARK5150"
	blind.Diff = verify.DiffSpec{
		Class:    verify.ClassReflected,
		Marker:   marker,
		N:        1,
		Baseline: verify.Request{Method: "GET", URL: vs.url("/search?q=safe")},
		Payload:  verify.Request{Method: "GET", URL: vs.url("/search?q=" + marker)},
	}

	d := realDeps(t, s, catcher, w, []Candidate{blind})
	d.OOBBaseURL = oobBase
	d.OOBWindow = 3 * time.Second        // ample: the callback happens during the probe send
	d.OOBInterval = 5 * time.Millisecond // poll fast
	e := New(d)

	runID, err := e.Start(context.Background(), StartOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	waitRunDone(t, e)

	findings, err := s.ListFindings("", "")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 blind finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Status != "verified" || !strings.HasSuffix(f.Target, "/fetch") {
		t.Fatalf("unexpected blind finding: %+v", f)
	}
	ver, err := s.GetFindingVerification(f.ID)
	if err != nil {
		t.Fatalf("GetFindingVerification: %v", err)
	}
	if ver.OOBToken == "" {
		t.Fatal("expected a recorded OOB token on the blind proof-record")
	}
	if ver.RunID != runID || ver.Confidence != 100 {
		t.Fatalf("blind verification wrong: %+v", ver)
	}

	// The live /fetch endpoint actually performed the server-side callback.
	if got := vs.oobHits.Load(); got < 1 {
		t.Fatalf("expected the /fetch endpoint to have made >=1 real OOB callback, got %d", got)
	}
	// And the catcher recorded an interaction for the exact minted token.
	if got := len(catcher.InteractionsForToken(ver.OOBToken)); got < 1 {
		t.Fatalf("catcher recorded no interaction for minted token %q", ver.OOBToken)
	}

	// A parallel candidate that does NOT call back must be rejected at Gate 3.
	assertBlindNoCallbackRejected(t, vs, oobBase)
}

// assertBlindNoCallbackRejected runs a second run with a blind candidate whose
// probe hits an endpoint that never calls back, proving Gate 3 rejects it over the
// real wire (no finding filed).
func assertBlindNoCallbackRejected(t *testing.T, vs *vulnServer, oobBase string) {
	t.Helper()
	s := openStore(t)
	scopeForServer(t, s, vs)
	catcher := oob.New()
	w := newDoneWaiter()

	const marker = "E2ENOCB4242"
	blind := Candidate{
		VulnClass: "ssrf-blind",
		Severity:  verify.SeverityMedium,
		Target:    vs.url("/safe"), // /safe never fetches the url param → no callback
		Blind:     true,
		OOBProbe:  verify.Request{Method: "GET", URL: vs.url("/safe?url=OOBHERE")},
		Diff: verify.DiffSpec{
			Class:    verify.ClassReflected,
			Marker:   marker,
			N:        1,
			Baseline: verify.Request{Method: "GET", URL: vs.url("/search?q=safe")},
			Payload:  verify.Request{Method: "GET", URL: vs.url("/search?q=" + marker)},
		},
	}
	blind.OOBPlaceholder = "OOBHERE"

	d := realDeps(t, s, catcher, w, []Candidate{blind})
	d.OOBBaseURL = oobBase
	d.OOBWindow = 40 * time.Millisecond // short: no callback ever arrives → reject fast
	d.OOBInterval = 5 * time.Millisecond
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start(no-callback): %v", err)
	}
	w.wait(t)
	waitRunDone(t, e)

	if f, _ := s.ListFindings("", ""); len(f) != 0 {
		t.Fatalf("blind candidate without a callback must not file; got %d findings", len(f))
	}
	if st := e.State(); st.Rejected != 1 {
		t.Fatalf("expected 1 rejected (Gate 3 no-callback), got %+v", st)
	}
}
