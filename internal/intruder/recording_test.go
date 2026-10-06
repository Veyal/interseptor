package intruder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const rawGet = "GET /x HTTP/1.1\nHost: example.com\n\n"

func okServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestRecordingTimingAndWorkers(t *testing.T) {
	up := okServer(t, 15*time.Millisecond)
	e := newEngine(t)
	if err := e.Start(Spec{Target: up.URL, Template: rawGet, AttackType: "repeat", Repeat: 12, Threads: 3}); err != nil {
		t.Fatal(err)
	}
	st := waitDone(t, e)
	if len(st.Results) != 12 {
		t.Fatalf("results=%d", len(st.Results))
	}
	byWorker := map[int][]Result{}
	for _, r := range st.Results {
		if r.StartUs > r.EndUs || r.EndUs < 0 || r.EndUs == 0 {
			t.Fatalf("bad timing %+v", r)
		}
		if r.Worker < 1 || r.Worker > 3 {
			t.Fatalf("worker out of range %+v", r)
		}
		if r.Seq != r.ID || r.Seq < 1 {
			t.Fatalf("seq %+v", r)
		}
		if r.BodyHash == "" {
			t.Fatalf("no body hash %+v", r)
		}
		byWorker[r.Worker] = append(byWorker[r.Worker], r)
	}
	for w, rs := range byWorker {
		sort.Slice(rs, func(i, j int) bool { return rs[i].StartUs < rs[j].StartUs })
		for i := 1; i < len(rs); i++ {
			if rs[i].StartUs < rs[i-1].EndUs {
				t.Fatalf("worker %d overlaps: %+v %+v", w, rs[i-1], rs[i])
			}
		}
	}
}

func TestRecordingRunIDAndStateFields(t *testing.T) {
	up := okServer(t, 0)
	e := newEngine(t)
	cases := []Spec{
		{Target: up.URL + "/p?q=1", Template: "GET /§a§ HTTP/1.1\nHost: example.com\n\n", AttackType: "sniper", Payloads: [][]string{{"1", "2"}}, Threads: 2, DelayMs: 1},
		{Target: up.URL, Template: "GET /§a§/§b§ HTTP/1.1\nHost: example.com\n\n", AttackType: "pitchfork", Payloads: [][]string{{"1", "2"}, {"3", "4"}}},
		{Target: up.URL, Template: "GET /§a§/§b§ HTTP/1.1\nHost: example.com\n\n", AttackType: "clusterbomb", Payloads: [][]string{{"1", "2"}, {"3", "4"}}},
		{Target: up.URL, Template: rawGet, AttackType: "null", Repeat: 3, Threads: 2},
	}
	want := []string{"sniper", "pitchfork", "cluster", "repeat"}
	seen := map[string]bool{}
	for i, sp := range cases {
		if err := e.Start(sp); err != nil {
			t.Fatal(err)
		}
		mid := e.State().RunID
		st := waitDone(t, e)
		if st.RunID == "" || len(st.RunID) < 16 || st.RunID != mid && mid != "" {
			t.Fatalf("run id unstable: %q vs %q", mid, st.RunID)
		}
		if seen[st.RunID] {
			t.Fatalf("run id reused %s", st.RunID)
		}
		seen[st.RunID] = true
		if st.Attack != want[i] || st.StartedTs <= 0 || st.Threads < 1 {
			t.Fatalf("state %d: %+v", i, st)
		}
		if st.TargetHost == "" || strings.ContainsAny(st.TargetHost, "/?") || strings.Contains(st.TargetHost, "http") {
			t.Fatalf("target host %q", st.TargetHost)
		}
	}
	e.Start(cases[3])
	st := waitDone(t, e)
	if st.Repeat != 3 || st.Threads != 2 {
		t.Fatalf("%+v", st)
	}
}

func TestRecordingBarrierTightensLaunchSpread(t *testing.T) {
	up := okServer(t, 0)
	spread := func(barrier bool) (int64, State) {
		e := newEngine(t)
		err := e.Start(Spec{Target: up.URL, Template: rawGet, AttackType: "repeat", Repeat: 4, Threads: 4, DelayMs: 40, Barrier: barrier})
		if err != nil {
			t.Fatal(err)
		}
		st := waitDone(t, e)
		lo, hi := int64(1<<62), int64(0)
		for _, r := range st.Results {
			if r.StartUs < lo {
				lo = r.StartUs
			}
			if r.StartUs > hi {
				hi = r.StartUs
			}
		}
		return hi - lo, st
	}
	base, bst := spread(false)
	tight, st := spread(true)
	if bst.Barrier || !st.Barrier {
		t.Fatalf("barrier flags base=%v tight=%v", bst.Barrier, st.Barrier)
	}
	if base < 100_000 {
		t.Fatalf("baseline spread unexpectedly small: %d", base)
	}
	if tight >= base/2 {
		t.Fatalf("barrier spread %dus not below baseline %dus", tight, base)
	}
}

func TestRecordingBarrierIgnoredWhenNotApplicable(t *testing.T) {
	up := okServer(t, 0)
	e := newEngine(t)
	e.Start(Spec{Target: up.URL, Template: rawGet, AttackType: "repeat", Repeat: 2, Threads: 1, Barrier: true})
	if st := waitDone(t, e); st.Barrier {
		t.Fatal("barrier must be false with one thread")
	}
}

func TestRecordingRateLimitHeaders(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("RateLimit-Limit", strings.Repeat("9", 300))
		w.Header().Set("Set-Cookie", "sid=secret")
		w.Header().Set("X-Other", "nope")
		w.WriteHeader(429)
	}))
	defer up.Close()
	e := newEngine(t)
	e.Start(Spec{Target: up.URL, Template: rawGet, AttackType: "repeat", Repeat: 1})
	r := waitDone(t, e).Results[0]
	if r.RLHeaders["retry-after"] != "7" || r.RLHeaders["x-ratelimit-remaining"] != "0" {
		t.Fatalf("%v", r.RLHeaders)
	}
	if len(r.RLHeaders["ratelimit-limit"]) != 128 {
		t.Fatalf("not capped: %d", len(r.RLHeaders["ratelimit-limit"]))
	}
	if len(r.RLHeaders) != 3 {
		t.Fatalf("unexpected headers %v", r.RLHeaders)
	}
}

func TestRunSinkCalledOnceOnCompletion(t *testing.T) {
	up := okServer(t, 0)
	e := newEngine(t)
	var n int32
	got := make(chan RunRecord, 4)
	e.SetRunSink(func(r RunRecord) { atomic.AddInt32(&n, 1); got <- r })
	e.Start(Spec{Target: up.URL + "/secret?x=1", Template: rawGet, AttackType: "repeat", Repeat: 3})
	st := waitDone(t, e)
	rec := <-got
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&n) != 1 {
		t.Fatalf("sink calls=%d", n)
	}
	if rec.State.RunID != st.RunID || len(rec.State.Results) != 3 || rec.State.Running {
		t.Fatalf("%+v", rec.State)
	}
	if rec.Spec.Attack != "repeat" || strings.Contains(rec.Spec.Target, "secret") {
		t.Fatalf("%+v", rec.Spec)
	}
}

func TestRunSinkCalledOnceOnStop(t *testing.T) {
	var mu sync.Mutex
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer up.Close()
	defer close(release)
	e := newEngine(t)
	var n int32
	got := make(chan RunRecord, 4)
	e.SetRunSink(func(r RunRecord) { mu.Lock(); n++; mu.Unlock(); got <- r })
	e.Start(Spec{Target: up.URL, Template: rawGet, AttackType: "repeat", Repeat: 5, Threads: 2})
	time.Sleep(80 * time.Millisecond)
	e.Stop()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("sink not called on stop")
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Fatalf("sink calls=%d", n)
	}
}

func TestLegacyJSONDecodes(t *testing.T) {
	var st State
	old := `{"running":false,"total":1,"done":1,"results":[{"id":1,"payload":"a","status":200,"length":2,"timeMs":3,"error":"","flowId":9,"flagged":false,"anomaly":false,"matched":false,"extracted":"","binary":false}],"error":"","capped":false}`
	if err := json.Unmarshal([]byte(old), &st); err != nil || st.Results[0].StartUs != 0 || st.RunID != "" {
		t.Fatalf("%v %+v", err, st)
	}
	b, _ := json.Marshal(Result{ID: 1})
	for _, k := range []string{"startUs", "rlHeaders", "worker", "bodyHash"} {
		if strings.Contains(string(b), k) {
			t.Fatalf("%s should be omitted: %s", k, b)
		}
	}
}

func TestEndpointOfDropsQueryValues(t *testing.T) {
	cases := []struct{ in, method, path string }{
		{"POST /api/login?user=alice&pw=§x§ HTTP/1.1\nHost: example.com\n\n", "POST", "/api/login"},
		{"get /a/§1§ HTTP/1.1\n\n", "GET", "/a/§1§"},
		{"garbage", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		m, p := endpointOf(c.in)
		if m != c.method || p != c.path {
			t.Fatalf("endpointOf(%q)=%q %q want %q %q", c.in, m, p, c.method, c.path)
		}
	}
}

func TestSpecSummaryCarriesEndpoint(t *testing.T) {
	up := okServer(t, 0)
	e := newEngine(t)
	recs := make(chan RunRecord, 1)
	e.SetRunSink(func(r RunRecord) { recs <- r })
	if err := e.Start(Spec{Target: up.URL, Template: "GET /x?token=secret HTTP/1.1\nHost: example.com\n\n", AttackType: "repeat", Repeat: 1, Threads: 1}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, e)
	var rec RunRecord
	select {
	case rec = <-recs:
	case <-time.After(5 * time.Second):
		t.Fatal("no run record")
	}
	if rec.Spec.Method != "GET" || rec.Spec.Path != "/x" {
		t.Fatalf("spec=%+v", rec.Spec)
	}
}
