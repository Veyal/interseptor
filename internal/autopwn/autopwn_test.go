package autopwn

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/oob"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/verify"
)

// --- fakes ---------------------------------------------------------------

// fakeCaller scripts a sequence of turns; each Complete returns the next.
type fakeCaller struct {
	mu    sync.Mutex
	turns []aiagent.Turn
	i     int
}

func (f *fakeCaller) Complete(ctx context.Context, system string, msgs []aiagent.Message, tools []aiagent.ToolSpec) (aiagent.Turn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.i >= len(f.turns) {
		// Default: a tool-less final answer so Run terminates.
		return aiagent.Turn{Text: "{}"}, nil
	}
	t := f.turns[f.i]
	f.i++
	return t, nil
}

// fakeExec returns a canned result for any tool call.
type fakeExec struct {
	mu      sync.Mutex
	results map[string]string
	calls   []string
}

func (f *fakeExec) Exec(ctx context.Context, call aiagent.ToolCall) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call.Name)
	if r, ok := f.results[call.Name]; ok {
		return r, nil
	}
	return "{}", nil
}

// fakeSender scripts Gate-1 differential outcomes: it always returns the
// exchange for the request's marker/URL so the oracle behaves deterministically.
type fakeSender struct {
	mu       sync.Mutex
	respond  func(req verify.Request) verify.Exchange
	nextID   int64
	seenURLs []string // every URL the verifier actually sent to
}

func (f *fakeSender) Send(ctx context.Context, req verify.Request) verify.Exchange {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.seenURLs = append(f.seenURLs, req.URL)
	ex := f.respond(req)
	if ex.FlowID == 0 {
		ex.FlowID = f.nextID
	}
	return ex
}

func (f *fakeSender) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.seenURLs))
	copy(out, f.seenURLs)
	return out
}

// fakeOOB scripts Gate-3: HitsForToken returns >0 once armed.
type fakeOOB struct {
	hit bool
}

func (f *fakeOOB) HitsForToken(token string) int {
	if f.hit {
		return 1
	}
	return 0
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time {
	if c.t.IsZero() {
		c.t = time.Unix(0, 0)
	}
	c.t = c.t.Add(time.Millisecond)
	return c.t
}

// completion barrier: the test-side Broadcast signals when a terminal status
// arrives so tests can wait without polling.
type doneWaiter struct {
	ch     chan struct{}
	once   sync.Once
	mu     sync.Mutex
	events []map[string]any
}

func newDoneWaiter() *doneWaiter { return &doneWaiter{ch: make(chan struct{})} }

func (w *doneWaiter) broadcast(v any) {
	m, _ := v.(map[string]any)
	if m != nil {
		w.mu.Lock()
		w.events = append(w.events, m)
		w.mu.Unlock()
		if st, _ := m["status"].(string); st == StatusDone || st == StatusStopped || st == StatusError {
			w.once.Do(func() { close(w.ch) })
		}
	}
}

func (w *doneWaiter) wait(t *testing.T) {
	t.Helper()
	select {
	case <-w.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish within 5s")
	}
}

// --- test scaffolding ----------------------------------------------------

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addScope(t *testing.T, s *store.Store) {
	t.Helper()
	if _, err := s.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "victim.test"}); err != nil {
		t.Fatalf("CreateScopeRule: %v", err)
	}
}

// reflectedCandidate is a non-blind reflected-XSS candidate whose Gate-1 oracle
// holds via the fakeSender (marker present in payload response, absent in
// baseline). Severity Medium so Gate 4 does not apply.
func reflectedCandidate() Candidate {
	marker := "XPWNX"
	return Candidate{
		VulnClass: "xss-reflected",
		Severity:  verify.SeverityMedium,
		Target:    "https://victim.test/search",
		Point:     "param q",
		Summary:   "reflected marker in q",
		Diff: verify.DiffSpec{
			Class:    verify.ClassReflected,
			Marker:   marker,
			N:        2,
			Baseline: verify.Request{Method: "GET", URL: "https://victim.test/search?q=baseline"},
			Payload:  verify.Request{Method: "GET", URL: "https://victim.test/search?q=" + marker},
		},
	}
}

// reflectingResponder makes the fakeSender reflect the marker only for the
// payload request (URL contains the marker).
func reflectingResponder(marker string) func(verify.Request) verify.Exchange {
	return func(req verify.Request) verify.Exchange {
		body := "<html>ok</html>"
		if contains(req.URL, marker) {
			body = "<html>" + marker + "</html>"
		}
		return verify.Exchange{Status: 200, Body: []byte(body)}
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// baseDeps builds a Deps wired with fakes and a scripted candidate set.
func baseDeps(t *testing.T, s *store.Store, w *doneWaiter, cands []Candidate) Deps {
	return Deps{
		Store: s,
		// Default caller: the planning turn returns an empty plan, and the
		// adversarial verifier (Gate 2) returns "real". A single scripted turn is
		// reused for both agent runs (each Run gets a fresh caller via this fn).
		NewToolCaller: func() (aiagent.ToolCaller, error) {
			return &fakeCaller{turns: []aiagent.Turn{{Text: `{"result":"real","reasoning":"reproduced"}`}}}, nil
		},
		ToolExecutor: &fakeExec{},
		VerifySender: &fakeSender{respond: reflectingResponder("XPWNX")},
		VerifyOOB:    &fakeOOB{},
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

// --- tests ---------------------------------------------------------------

// (a) Start refuses with no scope rules.
func TestStartRefusesWithoutScope(t *testing.T) {
	s := openStore(t)
	e := New(Deps{Store: s, NewToolCaller: func() (aiagent.ToolCaller, error) { return &fakeCaller{}, nil }})
	if _, err := e.Start(context.Background(), StartOpts{}); !errors.Is(err, ErrNoScope) {
		t.Fatalf("expected ErrNoScope, got %v", err)
	}
	if runs, _ := s.ListPentestRuns(); len(runs) != 0 {
		t.Fatalf("no run row should be created; got %d", len(runs))
	}
}

// (b) A candidate that passes every gate → exactly one verified Finding + a
// finding_verification row + PoC flows attached.
func TestVerifiedCandidateFilesFinding(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	e := New(baseDeps(t, s, w, []Candidate{reflectedCandidate()}))

	runID, err := e.Start(context.Background(), StartOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)

	findings, _ := s.ListFindings("", "")
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 filed finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Status != "verified" || f.Source != "ai" {
		t.Fatalf("finding not verified/ai: %+v", f)
	}
	ver, err := s.GetFindingVerification(f.ID)
	if err != nil {
		t.Fatalf("GetFindingVerification: %v", err)
	}
	if ver.RunID != runID {
		t.Fatalf("verification runId=%d want %d", ver.RunID, runID)
	}
	if ver.VulnClass != "xss-reflected" {
		t.Fatalf("verification class=%q", ver.VulnClass)
	}
	// Non-blind, <High: gates 1+2 → confidence 100.
	if ver.Confidence != 100 {
		t.Fatalf("confidence=%d want 100", ver.Confidence)
	}
	if ver.ReproCount != 2 {
		t.Fatalf("reproCount=%d want 2", ver.ReproCount)
	}
	if len(f.Flows) == 0 {
		t.Fatal("expected PoC flows attached to the finding")
	}

	st := e.State()
	if st.Active || st.Status != StatusDone {
		t.Fatalf("engine should be idle/done: %+v", st)
	}
	if st.Filed != 1 || st.Verified != 1 {
		t.Fatalf("counts wrong: %+v", st)
	}
}

// (c1) Gate 1 failure (differential not reproduced) → not filed.
func TestGate1FailureNotFiled(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	d := baseDeps(t, s, w, []Candidate{reflectedCandidate()})
	// Sender never reflects the marker → Gate 1 fails.
	d.VerifySender = &fakeSender{respond: func(req verify.Request) verify.Exchange {
		return verify.Exchange{Status: 200, Body: []byte("<html>ok</html>")}
	}}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	assertNoFindings(t, s)
	if st := e.State(); st.Rejected != 1 {
		t.Fatalf("expected 1 rejected, got %+v", st)
	}
}

// (c2) Gate 2 failure (agent refutes) → not filed. The scripted verifier caller
// returns a "refuted" verdict.
func TestGate2FailureNotFiled(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	d := baseDeps(t, s, w, []Candidate{reflectedCandidate()})
	d.NewToolCaller = func() (aiagent.ToolCaller, error) {
		return &fakeCaller{turns: []aiagent.Turn{{Text: `{"result":"refuted","reasoning":"WAF echo"}`}}}, nil
	}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	assertNoFindings(t, s)
}

// (c3) Gate 3 failure (blind class, no OOB callback) → not filed.
func TestGate3BlindFailureNotFiled(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	c := reflectedCandidate()
	c.VulnClass = "ssrf-blind"
	c.Blind = true
	c.OOBProbe = verify.Request{Method: "GET", URL: "https://victim.test/fetch?url=OOBPLACEHOLDER"}
	c.OOBPlaceholder = "OOBPLACEHOLDER"
	d := baseDeps(t, s, w, []Candidate{c})
	d.OOB = oob.New()                  // mints the correlated token
	d.VerifyOOB = &fakeOOB{hit: false} // but no callback ever arrives
	d.OOBWindow = time.Millisecond     // return fast on no-callback
	d.OOBInterval = time.Millisecond
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	assertNoFindings(t, s)
}

// (c4) Gate 4 failure (human declines) for a High candidate → not filed.
func TestGate4HumanDeclineNotFiled(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	c := reflectedCandidate()
	c.Severity = verify.SeverityHigh
	d := baseDeps(t, s, w, []Candidate{c})
	d.AskHuman = func(ctx context.Context, msg string, opts []string) (string, error) {
		return rejectOption, nil
	}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	assertNoFindings(t, s)
}

// (b-blind) A blind candidate with an OOB callback + human confirm files with
// confidence 100 and records the OOB token. This test exercises the FULL
// mint→inject→poll correlation: a real oob.Catcher mints a correlated token, the
// callback URL is injected into the probe, and the capturing sender asserts the
// exact token string reaches the wire BEFORE the (simulated) callback is honored.
func TestVerifiedBlindCandidateFiles(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	catcher := oob.New()

	c := reflectedCandidate()
	c.VulnClass = "ssrf-blind"
	c.Severity = verify.SeverityHigh
	c.Blind = true
	c.OOBProbe = verify.Request{Method: "GET", URL: "https://victim.test/fetch?url=OOBPLACEHOLDER"}
	c.OOBPlaceholder = "OOBPLACEHOLDER"

	fs := &fakeSender{respond: reflectingResponder("XPWNX")}
	d := baseDeps(t, s, w, []Candidate{c})
	d.VerifySender = fs
	d.OOB = catcher
	d.OOBBaseURL = "http://oob.example.com"
	// The poller returns a hit only for a token that has actually been recorded in
	// the catcher — so the correlation (mint→inject→callback for THIS token) is
	// genuinely exercised, not blanket-true.
	d.VerifyOOB = &tokenAwareOOB{c: catcher}
	// Simulate the server dereferencing the injected URL: record an interaction
	// for whatever token the catcher just minted, right when the probe is sent.
	fs.respond = func(req verify.Request) verify.Exchange {
		if idx := indexOf(req.URL, "http://oob.example.com/"); idx >= 0 {
			tok := req.URL[idx+len("http://oob.example.com/"):]
			// Simulate the target dereferencing the injected callback URL: the
			// catcher records an interaction for this exact token (path /oob/<tok>).
			hr, _ := http.NewRequest("GET", "http://oob.example.com/oob/"+tok, nil)
			catcher.Record(hr, "")
		}
		body := "<html>ok</html>"
		if contains(req.URL, "XPWNX") {
			body = "<html>XPWNX</html>"
		}
		return verify.Exchange{Status: 200, Body: []byte(body)}
	}

	e := New(d)
	runID, err := e.Start(context.Background(), StartOpts{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)

	findings, _ := s.ListFindings("", "")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	ver, err := s.GetFindingVerification(findings[0].ID)
	if err != nil {
		t.Fatalf("GetFindingVerification: %v", err)
	}
	if ver.OOBToken == "" {
		t.Fatal("expected a recorded OOB token for the blind finding")
	}
	if ver.RunID != runID || ver.Confidence != 100 {
		t.Fatalf("blind verification wrong: %+v", ver)
	}
	// The minted token must have actually appeared in a probe URL the sender saw.
	var injected bool
	for _, u := range fs.urls() {
		if contains(u, ver.OOBToken) {
			injected = true
		}
	}
	if !injected {
		t.Fatalf("minted OOB token %q was never injected into a probe URL: %v", ver.OOBToken, fs.urls())
	}
}

// tokenAwareOOB reports a hit only for tokens the catcher actually recorded, so
// the mint→inject→poll correlation is genuinely tested.
type tokenAwareOOB struct {
	c *oob.Catcher
}

func (o *tokenAwareOOB) HitsForToken(token string) int {
	return len(o.c.InteractionsForToken(token))
}

// (b-blind-inert) A blind candidate with OOBBaseURL unset is SKIPPED (never
// probed) with a loud diagnostic, not silently rejected at Gate 3.
func TestBlindCandidateSkippedWhenOOBBaseMissing(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	c := reflectedCandidate()
	c.VulnClass = "ssrf-blind"
	c.Blind = true
	c.OOBProbe = verify.Request{Method: "GET", URL: "https://victim.test/fetch?url=OOBPLACEHOLDER"}
	c.OOBPlaceholder = "OOBPLACEHOLDER"
	fs := &fakeSender{respond: reflectingResponder("XPWNX")}
	d := baseDeps(t, s, w, []Candidate{c})
	d.VerifySender = fs
	d.OOB = oob.New()
	d.OOBBaseURL = "" // the config omission under test
	var skipMsg string
	d.RecordActivity = func(a store.Activity) {
		if contains(a.Summary, "OOBBaseURL not configured") {
			skipMsg = a.Summary
		}
	}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	assertNoFindings(t, s)
	if skipMsg == "" {
		t.Fatal("expected a loud 'OOBBaseURL not configured' skip activity")
	}
	if len(fs.urls()) != 0 {
		t.Fatalf("inert blind candidate must never be probed; sender saw %v", fs.urls())
	}
	if st := e.State(); st.Rejected != 1 {
		t.Fatalf("expected 1 rejected (skipped), got %+v", st)
	}
}

// (d) Stop() before verification → run transitions to stopped and files nothing.
func TestStopBeforeVerifyFilesNothing(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	// Cancel the run from inside candidate collection (mid-pipeline), before any
	// gate runs.
	var e *Engine
	d := baseDeps(t, s, w, []Candidate{reflectedCandidate()})
	d.CollectCandidates = func(ctx context.Context, eng *Engine, plan Plan) []Candidate {
		e.Stop()
		return []Candidate{reflectedCandidate()}
	}
	e = New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	assertNoFindings(t, s)
	if st := e.State(); st.Status != StatusStopped {
		t.Fatalf("expected stopped, got %q", st.Status)
	}
}

// (d-budget) Request-budget exhaustion during verify stops the run and stops
// filing further candidates.
func TestBudgetExhaustionStops(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	// Two candidates; a tiny request budget so the first candidate's re-sends
	// exhaust it and the second is never verified/filed.
	c1 := reflectedCandidate()
	c2 := reflectedCandidate()
	c2.Target = "https://victim.test/other"
	d := baseDeps(t, s, w, []Candidate{c1, c2})
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{Budget: Budget{MaxRequests: 1}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)
	// At most one finding filed; run stopped by budget.
	findings, _ := s.ListFindings("", "")
	if len(findings) > 1 {
		t.Fatalf("budget cap should limit filings; got %d", len(findings))
	}
	if st := e.State(); st.Status != StatusStopped {
		t.Fatalf("expected stopped by budget, got %q", st.Status)
	}
}

// (e) Activity + Broadcast are emitted for phase transitions.
func TestGlassBoxEmitsPhases(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	var acts []store.Activity
	var amu sync.Mutex
	d := baseDeps(t, s, w, []Candidate{reflectedCandidate()})
	d.RecordActivity = func(a store.Activity) {
		amu.Lock()
		acts = append(acts, a)
		amu.Unlock()
	}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)

	amu.Lock()
	defer amu.Unlock()
	phases := map[string]bool{}
	for _, a := range acts {
		phases[a.Summary] = true
	}
	if !phases["phase: "+StatusExecuting] || !phases["phase: "+StatusVerifying] {
		t.Fatalf("missing phase-transition activity; got %v", phases)
	}
	// Broadcast events include planning + a terminal status.
	w.mu.Lock()
	defer w.mu.Unlock()
	var sawPlanning, sawDone bool
	for _, ev := range w.events {
		if p, _ := ev["phase"].(string); p == StatusPlanning {
			sawPlanning = true
		}
		if st, _ := ev["status"].(string); st == StatusDone {
			sawDone = true
		}
	}
	if !sawPlanning || !sawDone {
		t.Fatalf("broadcast missing planning/done: planning=%v done=%v", sawPlanning, sawDone)
	}
}

// Start refuses a second concurrent run.
func TestStartRefusesConcurrentRun(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	block := make(chan struct{})
	d := baseDeps(t, s, w, []Candidate{reflectedCandidate()})
	d.CollectCandidates = func(ctx context.Context, e *Engine, plan Plan) []Candidate {
		<-block // hold the first run in the execute phase
		return nil
	}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Give the goroutine a moment to mark active + reach the blocking collector.
	waitFor(t, func() bool { return e.State().Active })
	if _, err := e.Start(context.Background(), StartOpts{}); !errors.Is(err, ErrRunActive) {
		t.Fatalf("expected ErrRunActive, got %v", err)
	}
	close(block)
	w.wait(t)
}

// (defect 1) A candidate whose Target/probe URL is out of the run's scope
// snapshot, and one that is an Interseptor own listener, are both SKIPPED (never
// probed) and file nothing, while an in-scope candidate in the same run still
// files. This proves the verifier's re-send path enforces the safety boundary.
func TestOutOfScopeAndOwnListenerCandidatesSkipped(t *testing.T) {
	s := openStore(t)
	addScope(t, s) // scope = victim.test only
	w := newDoneWaiter()

	inScope := reflectedCandidate() // https://victim.test/search — allowed

	offScope := reflectedCandidate() // evil.test — NOT in the victim.test scope
	offScope.Target = "https://evil.test/search"
	offScope.Diff.Baseline.URL = "https://evil.test/search?q=baseline"
	offScope.Diff.Payload.URL = "https://evil.test/search?q=XPWNX"

	ownListener := reflectedCandidate() // loopback own listener
	ownListener.Target = "http://127.0.0.1:9966/search"
	ownListener.Diff.Baseline.URL = "http://127.0.0.1:9966/search?q=baseline"
	ownListener.Diff.Payload.URL = "http://127.0.0.1:9966/search?q=XPWNX"

	fs := &fakeSender{respond: reflectingResponder("XPWNX")}
	d := baseDeps(t, s, w, []Candidate{offScope, ownListener, inScope})
	d.VerifySender = fs
	// Own-listener predicate: treat the control port on loopback as own.
	d.IsOwnListener = func(rawURL string) bool { return contains(rawURL, "127.0.0.1:9966") }

	var skips []string
	d.RecordActivity = func(a store.Activity) {
		if contains(a.Summary, "candidate skipped") {
			skips = append(skips, a.Summary)
		}
	}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)

	// Exactly the in-scope candidate files.
	findings, _ := s.ListFindings("", "")
	if len(findings) != 1 {
		t.Fatalf("expected exactly the in-scope finding, got %d", len(findings))
	}
	if findings[0].Target != "https://victim.test/search" {
		t.Fatalf("wrong finding filed: %s", findings[0].Target)
	}
	// The out-of-scope + own-listener URLs must NEVER have been probed.
	for _, u := range fs.urls() {
		if contains(u, "evil.test") || contains(u, "127.0.0.1:9966") {
			t.Fatalf("boundary breached: verifier probed %q", u)
		}
	}
	// Two loud skip diagnostics, and both rejected in the counts.
	var sawScope, sawOwn bool
	for _, m := range skips {
		if contains(m, "out of scope") {
			sawScope = true
		}
		if contains(m, "own listener") {
			sawOwn = true
		}
	}
	if !sawScope || !sawOwn {
		t.Fatalf("missing skip diagnostics: scope=%v own=%v (%v)", sawScope, sawOwn, skips)
	}
	if st := e.State(); st.Rejected != 2 || st.Filed != 1 {
		t.Fatalf("counts wrong: %+v", st)
	}
}

// (defect 2) A panic in any phase must not brick the engine: the run ends status
// "error", active is cleared, a terminal SSE fires, and a subsequent Start works.
func TestPanicInPhaseDoesNotBrickEngine(t *testing.T) {
	s := openStore(t)
	addScope(t, s)
	w := newDoneWaiter()
	d := baseDeps(t, s, w, nil)
	d.CollectCandidates = func(ctx context.Context, e *Engine, plan Plan) []Candidate {
		panic("boom in collector")
	}
	e := New(d)
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.wait(t)

	st := e.State()
	if st.Active {
		t.Fatal("engine still active after a panic — bricked")
	}
	if st.Status != StatusError {
		t.Fatalf("expected error status after panic, got %q", st.Status)
	}
	if !contains(st.Error, "panic") {
		t.Fatalf("expected panic recorded in error, got %q", st.Error)
	}

	// A subsequent Start must succeed (the active slot was released).
	w2 := newDoneWaiter()
	d2 := baseDeps(t, s, w2, []Candidate{reflectedCandidate()})
	// Re-point broadcast to the fresh waiter.
	e2 := New(d2)
	if _, err := e2.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	w2.wait(t)

	// The SAME engine, too: after its run finished (paniced), Start works again.
	w3 := newDoneWaiter()
	e.d.Broadcast = w3.broadcast
	e.d.CollectCandidates = func(ctx context.Context, eng *Engine, plan Plan) []Candidate {
		return []Candidate{reflectedCandidate()}
	}
	if _, err := e.Start(context.Background(), StartOpts{}); err != nil {
		t.Fatalf("re-Start on the recovered engine failed: %v", err)
	}
	w3.wait(t)
	if f, _ := s.ListFindings("", ""); len(f) == 0 {
		t.Fatal("recovered engine should be able to file again")
	}
}

func assertNoFindings(t *testing.T, s *store.Store) {
	t.Helper()
	if f, _ := s.ListFindings("", ""); len(f) != 0 {
		t.Fatalf("expected no findings filed, got %d", len(f))
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 3s")
}
