package intruder

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestResultLogChunksToDiskAndReadsBackContiguously(t *testing.T) {
	dir := t.TempDir()
	l := newResultLog(dir, 3, 5) // chunkSize 3, headCap 5
	for i := 1; i <= 10; i++ {
		l.append(Result{ID: i, Status: 200, Length: 10})
	}
	if l.count() != 10 {
		t.Fatalf("count = %d, want 10", l.count())
	}
	// Full chunks must be flushed to disk, not held in memory.
	ents, _ := os.ReadDir(dir)
	if len(ents) < 3 {
		t.Fatalf("expected >=3 on-disk chunks, found %d", len(ents))
	}

	// readRange across chunk boundaries returns the exact contiguous window.
	win := l.readRange(4, 3)
	if len(win) != 3 || win[0].ID != 5 || win[1].ID != 6 || win[2].ID != 7 {
		t.Fatalf("readRange(4,3) = %+v, want IDs 5,6,7", ids(win))
	}
	// Window spanning the in-memory tail chunk.
	tail := l.readRange(8, 10)
	if len(tail) != 2 || tail[0].ID != 9 || tail[1].ID != 10 {
		t.Fatalf("readRange(8,10) = %v, want IDs 9,10", ids(tail))
	}
	all := l.all()
	if len(all) != 10 || all[0].ID != 1 || all[9].ID != 10 {
		t.Fatalf("all() = %v", ids(all))
	}
}

func TestResultLogFlagsAppliedOnlyAfterFinalize(t *testing.T) {
	l := newResultLog(t.TempDir(), 100, 100)
	// One clear length outlier among uniform results.
	for i := 0; i < 5; i++ {
		l.append(Result{ID: i + 1, Status: 200, Length: 100})
	}
	l.append(Result{ID: 6, Status: 200, Length: 100000}) // outlier

	// Before finalize, no flags are derived.
	pre := l.all()
	for _, r := range pre {
		if r.Flagged || r.Anomaly {
			t.Fatalf("result %d flagged before finalize", r.ID)
		}
	}
	l.finalize()
	post := l.all()
	if !post[5].Anomaly || !post[5].Flagged {
		t.Fatalf("outlier not flagged after finalize: %+v", post[5])
	}
	for i := 0; i < 5; i++ {
		if post[i].Flagged {
			t.Fatalf("uniform result %d unexpectedly flagged", post[i].ID)
		}
	}
}

func TestResultLogMemoryModeFallback(t *testing.T) {
	l := newResultLog("", 3, 5) // empty dir -> memory only
	for i := 1; i <= 7; i++ {
		l.append(Result{ID: i, Status: 200})
	}
	if l.count() != 7 {
		t.Fatalf("count = %d", l.count())
	}
	if got := l.readRange(2, 3); len(got) != 3 || got[0].ID != 3 {
		t.Fatalf("readRange = %v", ids(got))
	}
}

// Engine-level: results beyond what State returns live are reachable via the
// paginated Results API, streamed from the disk log.
func TestEngineResultsPagination(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	e := newEngine(t)
	if err := e.Start(Spec{
		Target: upstream.URL, Template: "GET / HTTP/1.1\nHost: h\n\n",
		AttackType: "repeat", Repeat: 5, Threads: 2,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, e)
	page, total := e.Results(1, 2)
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(page) != 2 {
		t.Fatalf("page len = %d, want 2", len(page))
	}
	all, _ := e.Results(0, 0)
	if len(all) != 5 {
		t.Fatalf("Results(0,0) len = %d, want 5", len(all))
	}
}

func TestMaxRequestsRaisedForDiskBackedRunner(t *testing.T) {
	if maxRequests < 20000 {
		t.Fatalf("maxRequests = %d, want >= 20000 (disk-backed capacity)", maxRequests)
	}
}

func ids(rs []Result) []int {
	out := make([]int, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}
