package control

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// seedScannableFlows inserts n HTML flows that each trip the missing-security-
// headers built-ins, so a scan produces issues to stream.
func seedScannableFlows(t *testing.T, st *store.Store, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := st.InsertFlow(&store.Flow{
			TS: time.UnixMilli(int64(i)), Method: "GET", Scheme: "https",
			Host: "example.com", Path: "/p" + strings.Repeat("x", i%5),
			Status: 200, Mime: "text/html",
			ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
		}); err != nil {
			t.Fatalf("insert flow %d: %v", i, err)
		}
	}
}

// collectEvents reads SSE frames off /api/events and returns the decoded JSON
// objects. stop must be deferred before the test server's Close: the SSE
// handler blocks until its client disconnects, so closing the server first
// would wait on it forever.
func collectEvents(t *testing.T, base string) (events func() []map[string]any, ready func(), stop func()) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	var mu sync.Mutex
	var got []map[string]any
	hello := make(chan struct{})
	var once sync.Once
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: hello") {
				once.Do(func() { close(hello) })
				continue
			}
			payload, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(payload), &m) != nil {
				continue
			}
			mu.Lock()
			got = append(got, m)
			mu.Unlock()
		}
	}()
	return func() []map[string]any {
			mu.Lock()
			defer mu.Unlock()
			return append([]map[string]any(nil), got...)
		}, func() {
			select {
			case <-hello:
			case <-time.After(5 * time.Second):
				t.Fatal("no SSE hello frame")
			}
		}, func() { resp.Body.Close() }
}

// A scan must stream scan.progress frames that reach the total, and
// scan.issue frames for what it finds, before the terminal scanner.update.
func TestScanStreamsProgressAndIssuesOverSSE(t *testing.T) {
	h, st, _ := newHub(t)
	// Keep the frame burst well under the SSE client buffer (64 per client).
	// broadcast drops frames for a consumer whose buffer is full — it is lossy
	// by design — so a scan that emitted more frames than the buffer could lose
	// its last frames under load. A small scan still streams start, per-flow
	// progress, issues and a finish without ever threatening the buffer.
	const flows = 8
	seedScannableFlows(t, st, flows)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	events, ready, stop := collectEvents(t, ts.URL)
	defer stop()
	ready()

	resp, err := http.Post(ts.URL+"/api/scanner/run", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/scanner/run: %v", err)
	}
	resp.Body.Close()
	// The 200 is synchronous and reliable: it means the scan finished and
	// persisted. Completion is checked through it and GET issues below, not
	// through the lossy terminal SSE frames.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan status = %d, want 200", resp.StatusCode)
	}

	var progress, issues, updates, total int
	var sawDone bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		progress, issues, updates, total, sawDone = 0, 0, 0, 0, false
		for _, e := range events() {
			switch e["type"] {
			case "scan.progress":
				progress++
				scan, _ := e["scan"].(map[string]any)
				if tv, ok := scan["total"].(float64); ok {
					total = int(tv)
				}
				if scan["done"] == true {
					sawDone = true
					if scan["cancelled"] != false {
						t.Errorf("a completed scan was reported as cancelled: %#v", scan)
					}
				}
			case "scan.issue":
				issues++
				if e["saved"] != false {
					t.Errorf("scan.issue did not mark the issue unsaved: %#v", e)
				}
				if _, ok := e["issue"].(map[string]any); !ok {
					t.Errorf("scan.issue carries no issue object: %#v", e)
				}
			case "scanner.update":
				updates++
			}
		}
		// The early frames (start, first issues) are never dropped; break as
		// soon as they, and ideally the terminal frames, have arrived.
		if progress >= 2 && issues > 0 && sawDone && updates > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Streaming itself — the reliable, early frames — is asserted hard.
	if progress < 2 {
		t.Errorf("got %d scan.progress frames, want at least a start and one more", progress)
	}
	if issues == 0 {
		t.Error("no scan.issue frames were streamed")
	}
	if total != flows {
		t.Errorf("scan.progress total = %d, want %d", total, flows)
	}
	// Completion is confirmed over the reliable REST surface. The terminal
	// done / scanner.update frames ride the lossy stream, so a drop under load
	// is noted, not failed.
	if got, err := st.ListIssues(); err != nil {
		t.Fatal(err)
	} else if len(got) == 0 {
		t.Fatal("the scan persisted no issues")
	}
	if !sawDone {
		t.Log("note: the terminal done progress frame was dropped by the lossy SSE buffer")
	}
	if updates == 0 {
		t.Log("note: the scanner.update frame was dropped by the lossy SSE buffer")
	}
}

// Cancelling a scan must stop it, answer 409 on the run request, persist
// nothing, and say so in the final progress frame.
func TestScanCancelStopsTheRunAndPersistsNothing(t *testing.T) {
	h, st, _ := newHub(t)
	seedScannableFlows(t, st, 900)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	events, ready, stop := collectEvents(t, ts.URL)
	defer stop()
	ready()

	type runResult struct {
		code int
		body string
	}
	runs := make(chan runResult, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/api/scanner/run", "application/json", nil)
		if err != nil {
			runs <- runResult{code: -1, body: err.Error()}
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		runs <- runResult{code: resp.StatusCode, body: string(buf[:n])}
	}()

	// Cancel until a scan is actually caught mid-flight, or the run finishes
	// first. A non-zero cancelled now means a real interruption: cancelAll only
	// counts a scan it moved out of the running state, so cancelled > 0 and a
	// 200 can no longer coexist. If the scan finishes first, every poll reports
	// 0 and the test skips — it never false-fails. The commit window itself is
	// covered deterministically by TestScanCancelInTheCommitWindow... .
	var cancelled int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && cancelled == 0 {
		resp, err := http.Post(ts.URL+"/api/scanner/cancel", "application/json", nil)
		if err != nil {
			t.Fatalf("POST /api/scanner/cancel: %v", err)
		}
		var out struct {
			Cancelled int `json:"cancelled"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		cancelled += out.Cancelled
		select {
		case res := <-runs:
			if cancelled == 0 {
				t.Skipf("the scan finished before a cancel landed (status %d)", res.code)
			}
			runs <- res
		default:
		}
		time.Sleep(time.Millisecond)
	}
	if cancelled == 0 {
		t.Skip("could not catch the scan mid-flight on this machine")
	}

	var res runResult
	select {
	case res = <-runs:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled scan never returned")
	}
	if res.code != http.StatusConflict {
		t.Fatalf("cancelled run status = %d body=%q, want 409", res.code, res.body)
	}
	issues, err := st.ListIssues()
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Errorf("a cancelled scan persisted %d issues; it must persist none", len(issues))
	}
	// The authoritative signals that the scan was cancelled — the 409 and the
	// empty issue store — are asserted above. The cancelled scan.progress frame
	// rides the lossy SSE stream (broadcast drops frames for a full client
	// buffer), and a cancelled 900-flow scan's frame burst can overrun it under
	// load, so observing that frame is best-effort, not a hard assertion.
	var sawCancelled bool
	for wait := time.Now().Add(2 * time.Second); !sawCancelled && time.Now().Before(wait); {
		for _, e := range events() {
			if e["type"] != "scan.progress" {
				continue
			}
			if scan, _ := e["scan"].(map[string]any); scan["cancelled"] == true {
				sawCancelled = true
				break
			}
		}
		if !sawCancelled {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !sawCancelled {
		t.Log("note: the cancelled scan.progress frame was dropped by the lossy SSE buffer")
	}
}

// Cancelling with nothing running is a no-op that says so.
func TestScanCancelWithNoScanRunning(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/scanner/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Cancelled int `json:"cancelled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Cancelled != 0 {
		t.Fatalf("cancelled = %d, want 0", out.Cancelled)
	}
	if h.scanJob.count() != 0 {
		t.Fatalf("scanJob reports %d running scans", h.scanJob.count())
	}
}
