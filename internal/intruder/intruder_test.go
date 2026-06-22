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

func TestStartRejectsNoPositions(t *testing.T) {
	e := newEngine(t)
	err := e.Start(Spec{Target: "http://x", Template: "GET / HTTP/1.1\nHost: x\n\n", AttackType: "sniper", Payloads: [][]string{{"a"}}})
	if err == nil {
		t.Fatal("expected error when template has no § markers")
	}
}
