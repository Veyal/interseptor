package control

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The cancel registry's invariant, tested directly without goroutines or the
// clock: cancel and the commit claim race on one state, and exactly one wins.
func TestScanJobCancelAndCommitAreMutuallyExclusive(t *testing.T) {
	var j scanJob

	// A running scan is interruptible: a cancel counts it, and the commit claim
	// then loses — the handler for this scan must abort.
	_, running := j.begin(context.Background())
	if j.count() != 1 {
		t.Fatalf("count = %d, want 1", j.count())
	}
	if got := j.cancelAll(); got != 1 {
		t.Fatalf("cancelAll on a running scan = %d, want 1", got)
	}
	if running.claimCommit() {
		t.Error("claimCommit succeeded after the scan was cancelled")
	}
	// Nothing interruptible is left, so a second cancel reports zero — this is
	// the count the old len(running) got wrong, reporting 1 for a scan that had
	// already finished and would go on to persist.
	if got := j.cancelAll(); got != 0 {
		t.Errorf("a second cancelAll = %d, want 0", got)
	}
	running.release()
	if j.count() != 0 {
		t.Errorf("count after release = %d, want 0", j.count())
	}

	// A scan that claims the commit first cannot then be cancelled, so a cancel
	// arriving in its persist phase reports zero and the scan keeps its 200.
	_, committed := j.begin(context.Background())
	if !committed.claimCommit() {
		t.Fatal("claimCommit failed on a running scan")
	}
	if got := j.cancelAll(); got != 0 {
		t.Errorf("cancelAll after the commit was claimed = %d, want 0", got)
	}
	committed.release()
}

// The commit window, driven deterministically with no wall-clock timing: a
// cancel lands at the exact instant the scan is about to persist — every worker
// has finished, RunPassive has returned a complete result, but nothing is
// written yet. The run must answer 409 and persist nothing. This is the window
// the TOCTOU bug lived in, where the scan ran to completion, returned 200, and
// persisted issues while cancel claimed success.
func TestScanCancelInTheCommitWindowReturns409AndPersistsNothing(t *testing.T) {
	h, st, _ := newHub(t)
	seedScannableFlows(t, st, 6)

	// The hook fires inside the handler's commit window. Set before the server
	// starts and guarded by a one-shot counter, so it is race-free: the write
	// happens-before the listener and every handler goroutine it spawns.
	var scans atomic.Int32
	h.beforeScanCommit = func() {
		if scans.Add(1) == 1 {
			if got := h.scanJob.cancelAll(); got != 1 {
				t.Errorf("cancel in the commit window reported %d, want 1", got)
			}
		}
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// First scan: the ambush fires, the commit claim loses the race → 409.
	resp, err := http.Post(ts.URL+"/api/scanner/run", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/scanner/run: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("scan cancelled at the commit window: status = %d body = %q, want 409", resp.StatusCode, body)
	}
	if issues, err := st.ListIssues(); err != nil {
		t.Fatal(err)
	} else if len(issues) != 0 {
		t.Fatalf("a scan cancelled at the commit window persisted %d issues; it must persist none", len(issues))
	}

	// Second scan: the one-shot has fired, so no cancel lands → 200, and it
	// persists the issues the first scan withheld, proving the flows really do
	// produce issues and the first run deliberately dropped them.
	resp2, err := http.Post(ts.URL+"/api/scanner/run", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/scanner/run: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("uncancelled run status = %d, want 200", resp2.StatusCode)
	}
	if issues, err := st.ListIssues(); err != nil {
		t.Fatal(err)
	} else if len(issues) == 0 {
		t.Fatal("the uncancelled scan persisted nothing; the seeded flows produce no issues")
	}
}
