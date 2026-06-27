package intruder

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

func waitDone(t *testing.T, e *Engine) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := e.State()
		if !st.Running && st.Done >= st.Total && st.Total > 0 {
			return st
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("attack did not finish: %+v", e.State())
	return State{}
}

func newEngine(t *testing.T) *Engine {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(sender.New(s, capture.New(s)))
}

func TestSniperVariesEachPositionAndFlagsAnomaly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "boom") {
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "POST /x HTTP/1.1\nHost: h\nContent-Type: text/plain\n\nval=§seed§",
		AttackType: "sniper",
		Payloads:   [][]string{{"a", "b", "boom"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	st := waitDone(t, e)
	if st.Total != 3 || len(st.Results) != 3 {
		t.Fatalf("expected 3 results, got total=%d len=%d", st.Total, len(st.Results))
	}
	var boom *Result
	for i := range st.Results {
		if st.Results[i].Payload == "boom" {
			boom = &st.Results[i]
		}
	}
	if boom == nil || boom.Status != 500 || !boom.Flagged {
		t.Fatalf("expected the boom payload to be 500 and flagged: %+v", boom)
	}
}

func TestPitchforkWalksListsInParallel(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, string(body))
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "POST /x HTTP/1.1\nHost: h\n\n§u§:§p§",
		AttackType: "pitchfork",
		Payloads:   [][]string{{"u1", "u2"}, {"p1", "p2"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if st.Total != 2 {
		t.Fatalf("pitchfork should pair lists: expected 2, got %d", st.Total)
	}
	joined := strings.Join(seen, ",")
	if !strings.Contains(joined, "u1:p1") || !strings.Contains(joined, "u2:p2") {
		t.Fatalf("pitchfork pairing wrong: %q", joined)
	}
}

func TestBatteringRamSamePayloadAllPositions(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, string(body))
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target: upstream.URL, Template: "POST /x HTTP/1.1\nHost: h\n\n§u§:§p§",
		AttackType: "battering", Payloads: [][]string{{"X", "Y"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if st.Total != 2 {
		t.Fatalf("battering: expected 2, got %d", st.Total)
	}
	for _, s := range seen {
		if s != "X:X" && s != "Y:Y" {
			t.Fatalf("battering should set all markers to the same payload: %q", s)
		}
	}
}

func TestClusterBombCartesianProduct(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, string(body))
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target: upstream.URL, Template: "POST /x HTTP/1.1\nHost: h\n\n§u§:§p§",
		AttackType: "cluster", Payloads: [][]string{{"a", "b"}, {"1", "2"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if st.Total != 4 {
		t.Fatalf("cluster: expected 4 combos, got %d", st.Total)
	}
	joined := strings.Join(seen, ",")
	for _, want := range []string{"a:1", "a:2", "b:1", "b:2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("cluster missing %q in %q", want, joined)
		}
	}
}
func TestSniperFuzzesPathNotJustBody(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	if err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET /api/§seg§ HTTP/1.1\nHost: h\n\n",
		AttackType: "sniper",
		Payloads:   [][]string{{"alpha", "beta", "gamma"}},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, e)
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(paths, ",")
	for _, want := range []string{"/api/alpha", "/api/beta", "/api/gamma"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected path %s to be fuzzed; saw %q", want, joined)
		}
	}
}

func TestParseFailuresDoNotSkewAnomalyFlagging(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok") // every reachable request → 200
	}))
	defer upstream.Close()

	e := newEngine(t)
	// "bad path" injects a space into the request line → http.ReadRequest fails
	// → those jobs record Status 0. The 200s must NOT be flagged as anomalies.
	if err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET /§seg§ HTTP/1.1\nHost: h\n\n",
		AttackType: "sniper",
		Payloads:   [][]string{{"bad path", "another bad one", "ok"}},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	for _, r := range st.Results {
		if r.Status == 200 && r.Flagged {
			t.Fatalf("a valid 200 response was wrongly flagged as an anomaly: %+v", r)
		}
	}
}

func TestStartRejectsNoPositions(t *testing.T) {
	e := newEngine(t)
	err := e.Start(Spec{Target: "http://x", Template: "GET / HTTP/1.1\nHost: x\n\n", AttackType: "sniper", Payloads: [][]string{{"a"}}})
	if err == nil {
		t.Fatal("expected error when template has no § markers")
	}
}

func TestRepeatModeSendsTemplateNTimesConcurrently(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	// No markers, no payloads — just fire the same request 8 times across 8 threads.
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "POST /buy HTTP/1.1\nHost: h\n\ncoupon=SAVE10",
		AttackType: "repeat",
		Repeat:     8,
		Threads:    8,
	})
	if err != nil {
		t.Fatalf("Start (repeat): %v", err)
	}
	st := waitDone(t, e)
	if st.Total != 8 || len(st.Results) != 8 {
		t.Fatalf("expected 8 results, got total=%d len=%d", st.Total, len(st.Results))
	}
	mu.Lock()
	got := hits
	mu.Unlock()
	if got != 8 {
		t.Fatalf("expected 8 requests sent, got %d", got)
	}
}

func TestRepeatModeRequiresCount(t *testing.T) {
	e := newEngine(t)
	err := e.Start(Spec{Target: "http://x", Template: "GET / HTTP/1.1\nHost: x\n\n", AttackType: "repeat", Repeat: 0})
	if err == nil {
		t.Fatal("expected error when repeat count is 0")
	}
}

func TestNullAttackTypeAlias(t *testing.T) {
	e := newEngine(t)
	err := e.Start(Spec{Target: "http://x", Template: "GET / HTTP/1.1\nHost: x\n\n", AttackType: "null", Repeat: 2, Threads: 1})
	if err != nil {
		t.Fatalf("Start (null alias): %v", err)
	}
	st := waitDone(t, e)
	if st.Total != 2 || len(st.Results) != 2 {
		t.Fatalf("expected 2 results, got total=%d len=%d", st.Total, len(st.Results))
	}
}

func TestDelayThrottlesDispatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	start := time.Now()
	// 3 sends, single thread, 60ms between dispatches → at least ~120ms total.
	err := e.Start(Spec{Target: upstream.URL, Template: "GET / HTTP/1.1\nHost: h\n\n", AttackType: "repeat", Repeat: 3, Threads: 1, DelayMs: 60})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, e)
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("expected delay to throttle dispatch (>=100ms), took %v", elapsed)
	}
}

func TestGrepAndPayloadProcessing(t *testing.T) {
	var gotBodies []string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBodies = append(gotBodies, string(b))
		mu.Unlock()
		if string(b) == "v=YWRtaW4=" { // base64("admin")
			io.WriteString(w, "secret-token=ABC123 WELCOME")
		} else {
			io.WriteString(w, "denied")
		}
	}))
	defer upstream.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	e := New(sender.New(st, capture.New(st)))
	e.SetBodyReader(func(hash string) []byte {
		rc, err := st.OpenBody(hash)
		if err != nil {
			return nil
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return b
	})

	err = e.Start(Spec{
		Target:       upstream.URL,
		Template:     "POST /x HTTP/1.1\nHost: h\n\nv=§p§",
		AttackType:   "sniper",
		Payloads:     [][]string{{"admin", "guest"}},
		ProcessRules: []string{"base64"},
		GrepMatch:    "WELCOME",
		GrepExtract:  `secret-token=(\w+)`,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	st2 := waitDone(t, e)

	// payload processing: "admin" must have been base64-encoded on the wire.
	mu.Lock()
	joined := strings.Join(gotBodies, "|")
	mu.Unlock()
	if !strings.Contains(joined, "v=YWRtaW4=") {
		t.Fatalf("expected base64-processed payload on the wire, got %q", joined)
	}
	var adminRes *Result
	for i := range st2.Results {
		if st2.Results[i].Payload == "admin" { // label stays the original
			adminRes = &st2.Results[i]
		}
	}
	if adminRes == nil || !adminRes.Matched {
		t.Fatalf("expected grep-match on the admin response: %+v", adminRes)
	}
	if adminRes.Extracted != "ABC123" {
		t.Fatalf("expected extracted token ABC123, got %q", adminRes.Extracted)
	}
}
