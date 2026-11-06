package intruder

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A finished run longer than the live head must still be fully readable: State
// carries only the bounded head (so a poll stays small at any run length) and
// says so, while Results pages the rest from the disk log with flags applied.
func TestStateHeadIsBoundedAndResultsPageTheRest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	const n = liveHeadCap + 300
	list := make([]string, n)
	for i := range list {
		list[i] = fmt.Sprintf("p%d", i)
	}
	e := newEngine(t)
	if err := e.Start(Spec{
		Target: upstream.URL, Template: "GET /§x§ HTTP/1.1\nHost: h\n\n",
		AttackType: "sniper", Payloads: [][]string{list}, Threads: 8,
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st := waitDone(t, e)
	if st.Total != n || st.Done != n {
		t.Fatalf("total/done = %d/%d, want %d", st.Total, st.Done, n)
	}
	if len(st.Results) != liveHeadCap {
		t.Fatalf("state head = %d results, want the %d cap", len(st.Results), liveHeadCap)
	}
	if !st.PartialResults {
		t.Fatal("a truncated state head must set PartialResults")
	}
	// The head is exactly the first liveHeadCap results in completion order, so
	// paging from the cap continues the same sequence (no gap, no repeat).
	head, _ := e.Results(0, liveHeadCap)
	for i := range head {
		if head[i].ID != st.Results[i].ID {
			t.Fatalf("state head row %d id=%d, paged head id=%d", i, st.Results[i].ID, head[i].ID)
		}
	}

	all, total := e.Results(0, 0)
	if total != n || len(all) != n {
		t.Fatalf("Results(0,0) = %d rows / total %d, want %d", len(all), total, n)
	}
	if all[0].Status != 200 {
		t.Fatalf("first paged result status = %d, want 200", all[0].Status)
	}
	page, _ := e.Results(liveHeadCap, 10)
	if len(page) != 10 || page[0].ID != all[liveHeadCap].ID {
		t.Fatalf("page from the head cap = %d rows starting at %d", len(page), page[0].ID)
	}
	// Every dispatched attempt appears exactly once across head + pages.
	seen := map[int]bool{}
	for _, r := range all {
		if seen[r.ID] {
			t.Fatalf("result id %d appears twice", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != n {
		t.Fatalf("distinct result ids = %d, want %d", len(seen), n)
	}
}

// The persisted run record is bounded too: a huge run must not materialise (and
// marshal) every result before the store's 4 MiB limit refuses it.
func TestRecordStateKeepsHeadAndTailOfHugeRun(t *testing.T) {
	log := newResultLog("", 1000, liveHeadCap)
	for i := 1; i <= recordResultCap*2; i++ {
		log.append(Result{ID: i, Payload: fmt.Sprintf("payload-%d", i)})
	}
	log.finalize()
	e := &Engine{running: false, total: recordResultCap * 2, done: recordResultCap * 2, log: log}
	st := e.recordStateLocked()
	if len(st.Results) != recordResultCap {
		t.Fatalf("record results = %d, want the %d cap", len(st.Results), recordResultCap)
	}
	if !st.Capped || st.Note == "" {
		t.Fatalf("a truncated record must say so: capped=%v note=%q", st.Capped, st.Note)
	}
	// A bounded record is not a failed run: clients render State.Error as a
	// failure and hide the results, so truncation must never set it.
	if st.Error != "" {
		t.Fatalf("a truncated record must not be reported as an error: %q", st.Error)
	}
	if !st.PartialResults {
		t.Fatal("a truncated record must set PartialResults")
	}
	half := recordResultCap / 2
	if st.Results[0].ID != 1 || st.Results[half-1].ID != half {
		t.Fatalf("record head = %d..%d", st.Results[0].ID, st.Results[half-1].ID)
	}
	if got := st.Results[len(st.Results)-1].ID; got != recordResultCap*2 {
		t.Fatalf("record tail ends at %d, want %d", got, recordResultCap*2)
	}
}
