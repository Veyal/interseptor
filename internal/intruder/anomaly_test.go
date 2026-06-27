package intruder

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnomalyFlagLengthOutlier verifies that a result with a wildly different
// response length is flagged anomalous while uniform-length results are not.
// Setup: most payloads get a short "ok" body; one gets a large response that
// exceeds the 20%/50-byte tolerance band.
func TestAnomalyFlagLengthOutlier(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("p") == "big" {
			// Return a body much larger than "ok" — clearly outside the tolerance band.
			for i := 0; i < 100; i++ {
				io.WriteString(w, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
			}
			return
		}
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "POST /x HTTP/1.1\nHost: h\nContent-Type: application/x-www-form-urlencoded\n\np=§p§",
		AttackType: "sniper",
		Payloads:   [][]string{{"a", "b", "c", "d", "e", "f", "big"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	st := waitDone(t, e)
	if len(st.Results) != 7 {
		t.Fatalf("expected 7 results, got %d", len(st.Results))
	}

	var bigResult *Result
	for i := range st.Results {
		if st.Results[i].Payload == "big" {
			bigResult = &st.Results[i]
		}
	}
	if bigResult == nil {
		t.Fatal("could not find 'big' result")
	}
	if !bigResult.Anomaly {
		t.Errorf("expected 'big' payload result to have Anomaly=true (length outlier), got Anomaly=false; length=%d", bigResult.Length)
	}
	if !bigResult.Flagged {
		t.Errorf("expected 'big' payload result to have Flagged=true, got Flagged=false")
	}

	// Normal results must not be anomalous.
	for i := range st.Results {
		r := st.Results[i]
		if r.Payload == "big" {
			continue
		}
		if r.Anomaly {
			t.Errorf("result %d (payload=%q, length=%d) unexpectedly marked Anomaly=true", r.ID, r.Payload, r.Length)
		}
	}
}

// TestAnomalyUniformResultsNoneFlagged verifies that when all results have the
// same status and similar length no result is flagged.
func TestAnomalyUniformResultsNoneFlagged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "uniform") // every response identical
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "POST /x HTTP/1.1\nHost: h\n\nv=§v§",
		AttackType: "sniper",
		Payloads:   [][]string{{"a", "b", "c", "d", "e"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	st := waitDone(t, e)
	for _, r := range st.Results {
		if r.Flagged {
			t.Errorf("uniform results: result %d (payload=%q) unexpectedly flagged", r.ID, r.Payload)
		}
		if r.Anomaly {
			t.Errorf("uniform results: result %d (payload=%q) unexpectedly has Anomaly=true", r.ID, r.Payload)
		}
	}
}

// TestAnomalySingleResultNoCrash ensures flagAnomalies does not panic or
// incorrectly flag the sole result in a single-result attack.
func TestAnomalySingleResultNoCrash(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "solo")
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "GET /x HTTP/1.1\nHost: h\n\n",
		AttackType: "repeat",
		Repeat:     1,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	st := waitDone(t, e)
	if len(st.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(st.Results))
	}
	// A single result has no peers to deviate from — must not be flagged.
	if st.Results[0].Flagged {
		t.Errorf("single result unexpectedly flagged: %+v", st.Results[0])
	}
	if st.Results[0].Anomaly {
		t.Errorf("single result unexpectedly has Anomaly=true: %+v", st.Results[0])
	}
}

// TestAnomalyZeroResultsNoCrash ensures flagAnomalies handles an empty result
// set without panicking (e.g. all jobs failed to parse before sending).
func TestAnomalyZeroResultsNoCrash(t *testing.T) {
	e := newEngine(t)
	// Call flagAnomalies directly on an engine with no results.
	e.mu.Lock()
	e.results = nil
	e.mu.Unlock()
	// Must not panic.
	e.flagAnomalies()
}

// TestAnomalyStatusAndLengthBothFlagRespectively verifies that a 500 (status
// anomaly) AND a differently-sized result (length anomaly) are both flagged,
// and that Anomaly is only set on the length outlier, not on the 500.
func TestAnomalyStatusAndLengthBothFlagRespectively(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.FormValue("p") {
		case "error":
			w.WriteHeader(500)
			io.WriteString(w, "internal")
		case "big":
			w.WriteHeader(200)
			for i := 0; i < 200; i++ {
				fmt.Fprintf(w, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
			}
		default:
			w.WriteHeader(200)
			io.WriteString(w, "normal")
		}
	}))
	defer upstream.Close()

	e := newEngine(t)
	err := e.Start(Spec{
		Target:     upstream.URL,
		Template:   "POST /x HTTP/1.1\nHost: h\nContent-Type: application/x-www-form-urlencoded\n\np=§p§",
		AttackType: "sniper",
		Payloads:   [][]string{{"a", "b", "c", "error", "big"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	st := waitDone(t, e)

	var errResult, bigResult *Result
	for i := range st.Results {
		switch st.Results[i].Payload {
		case "error":
			errResult = &st.Results[i]
		case "big":
			bigResult = &st.Results[i]
		}
	}

	if errResult == nil || bigResult == nil {
		t.Fatal("could not locate error and big results")
	}

	// 500 → Flagged, but not Anomaly (status signal, not length signal).
	if !errResult.Flagged {
		t.Errorf("500 result should be Flagged")
	}
	// 500 response body is "internal" (8 bytes) which is close to "normal" (6 bytes),
	// so it should NOT be an Anomaly (length is within tolerance).
	if errResult.Anomaly {
		t.Errorf("500 result with similar-length body should not have Anomaly=true")
	}

	// Large body → both Flagged and Anomaly.
	if !bigResult.Flagged {
		t.Errorf("length-outlier result should be Flagged")
	}
	if !bigResult.Anomaly {
		t.Errorf("length-outlier result should have Anomaly=true; length=%d", bigResult.Length)
	}

	// Normal results should not be flagged.
	for i := range st.Results {
		r := st.Results[i]
		if r.Payload == "error" || r.Payload == "big" {
			continue
		}
		if r.Flagged {
			t.Errorf("normal result %d (payload=%q) unexpectedly Flagged", r.ID, r.Payload)
		}
		if r.Anomaly {
			t.Errorf("normal result %d (payload=%q) unexpectedly has Anomaly=true", r.ID, r.Payload)
		}
	}
}

// TestMedianHelper exercises the median helper directly for correctness.
func TestMedianHelper(t *testing.T) {
	cases := []struct {
		in   []int64
		want int64
	}{
		{nil, 0},
		{[]int64{}, 0},
		{[]int64{7}, 7},
		{[]int64{3, 1, 2}, 2},        // odd: middle of sorted [1,2,3]
		{[]int64{4, 2, 3, 1}, 2},     // even: (2+3)/2=2
		{[]int64{100, 200, 300, 400}, 250}, // even average
	}
	for _, tc := range cases {
		got := median(tc.in)
		if got != tc.want {
			t.Errorf("median(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestAnomalyGrepMajoritySignal verifies that flagAnomalies flags the result
// whose grep-match outcome is in the minority. This is tested via direct engine
// manipulation because wiring a store+bodyReader is covered by TestGrepAndPayloadProcessing.
func TestAnomalyGrepMajoritySignal(t *testing.T) {
	e := &Engine{}
	// 4 matched, 1 not-matched → the non-matching result is the minority → flagged.
	e.results = []Result{
		{ID: 1, Status: 200, Length: 10, Matched: true},
		{ID: 2, Status: 200, Length: 10, Matched: true},
		{ID: 3, Status: 200, Length: 10, Matched: true},
		{ID: 4, Status: 200, Length: 10, Matched: true},
		{ID: 5, Status: 200, Length: 10, Matched: false}, // minority
	}
	e.flagAnomalies()

	for _, r := range e.results {
		if r.ID == 5 && !r.Flagged {
			t.Errorf("result 5 (minority grep) should be Flagged after flagAnomalies")
		}
		if r.ID != 5 && r.Flagged {
			t.Errorf("result %d (majority grep) unexpectedly Flagged", r.ID)
		}
	}
}

// TestAnomalyGrepAllMatchNoneExtraFlagged verifies that when ALL results match
// (no minority) the grep signal does not produce false flags.
func TestAnomalyGrepAllMatchNoneExtraFlagged(t *testing.T) {
	e := &Engine{}
	e.results = []Result{
		{ID: 1, Status: 200, Length: 10, Matched: true},
		{ID: 2, Status: 200, Length: 10, Matched: true},
		{ID: 3, Status: 200, Length: 10, Matched: true},
	}
	e.flagAnomalies()
	for _, r := range e.results {
		if r.Flagged {
			t.Errorf("all-match set: result %d unexpectedly Flagged", r.ID)
		}
	}
}
