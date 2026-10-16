package control

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/intruder"
)

func bigRun(n int, errLen int, withHeaders bool) intruder.RunRecord {
	rec := syntheticRun("bigrun0000000001")
	rec.State.Results = nil
	for i := 1; i <= n; i++ {
		r := intruder.Result{ID: i, Seq: i, Worker: 1, Payload: "p", Status: 200, Length: 5, TimeMs: 3, FlowID: int64(i), StartUs: int64(i), EndUs: int64(i + 10), BodyHash: "h"}
		r.Error = strings.Repeat("e", errLen)
		if withHeaders {
			r.RLHeaders = map[string]string{"X-RateLimit-Remaining": strings.Repeat("9", 100)}
		}
		rec.State.Results = append(rec.State.Results, r)
	}
	rec.State.Total, rec.State.Done = n, n
	return rec
}

func TestPersistSecondStageDropsBulkyFields(t *testing.T) {
	h, s, _ := newHub(t)
	rec := bigRun(1500, 3000, true) // about 4.7 MB of error text alone
	persistIntruderRun(s, rec)
	raw, ok, err := s.GetIntruderRun(rec.State.RunID)
	if err != nil || !ok {
		t.Fatalf("a run that is too large only because of bulky fields must still persist: ok=%v err=%v", ok, err)
	}
	var env intruderRunEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if len(env.State.Results) != 1500 || env.State.Results[0].Status != 200 || env.State.Results[0].StartUs == 0 {
		t.Fatalf("timing and status must survive: %d results", len(env.State.Results))
	}
	if env.State.Results[0].Error == "" {
		t.Fatal("the error flag must survive even when the text is dropped")
	}
	_ = h
}

func TestPersistThirdStageKeepsHeadAndTailWithMarker(t *testing.T) {
	_, s, _ := newHub(t)
	rec := bigRun(40000, 0, true) // too many results even without bulky fields
	persistIntruderRun(s, rec)
	raw, ok, err := s.GetIntruderRun(rec.State.RunID)
	if err != nil || !ok {
		t.Fatalf("some run record must always persist: ok=%v err=%v", ok, err)
	}
	var env intruderRunEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	n := len(env.State.Results)
	if n == 0 || n >= 40000 {
		t.Fatalf("expected a bounded head and tail, got %d", n)
	}
	if !strings.Contains(env.State.Error, "truncated") || !env.State.Capped {
		t.Fatalf("truncation must be visible in the run state: error=%q capped=%v", env.State.Error, env.State.Capped)
	}
	if env.State.Results[0].Seq != 1 || env.State.Results[n-1].Seq != 40000 {
		t.Fatalf("head and tail must be kept: first=%d last=%d", env.State.Results[0].Seq, env.State.Results[n-1].Seq)
	}
}

// A run persisted before timing was recorded (no startUs/endUs/seq/worker) must
// still render end to end and say so, without inventing a spread or latency.
func TestLegacyRunRendersWithoutFabricatedTiming(t *testing.T) {
	h, s, _ := newHub(t)
	legacy := `{"runId":"legacy0000000001","startedTs":1700000000000,"finishedTs":1700000009000,"attack":"repeat",
 "state":{"running":false,"total":3,"done":3,"error":"","capped":false,"runId":"legacy0000000001","attack":"repeat","threads":2,
  "results":[{"id":1,"payload":"a","status":200,"length":10,"timeMs":40,"error":"","flowId":0,"flagged":false,"anomaly":false,"matched":false,"extracted":"","binary":false},
             {"id":2,"payload":"b","status":429,"length":5,"timeMs":55,"error":"","flowId":0,"flagged":false,"anomaly":false,"matched":false,"extracted":"","binary":false},
             {"id":3,"payload":"c","status":429,"length":5,"timeMs":50,"error":"","flowId":0,"flagged":false,"anomaly":false,"matched":false,"extracted":"","binary":false}]},
 "spec":{"attack":"repeat","target":"example.com","threads":2}}`
	if err := s.PutIntruderRun("legacy0000000001", []byte(legacy)); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	for _, kind := range []string{"timeline", "distribution", "race", "strip"} {
		resp, body := getBytes(t, ts.URL+"/api/intruder/attacks/legacy0000000001/render.png?kind="+kind)
		requirePNG(t, resp, body)
		alt, _ := url.PathUnescape(resp.Header.Get("X-Render-Alt"))
		sum, _ := url.PathUnescape(resp.Header.Get("X-Render-Summary"))
		all := strings.ToLower(alt + " | " + sum)
		switch kind {
		case "timeline":
			if !strings.Contains(all, "not recorded") {
				t.Fatalf("timeline must say timing was not recorded: %s", all)
			}
			for _, fabricated := range []string{"req/s", "first block after", "time to first block", "launched within"} {
				if strings.Contains(all, fabricated) {
					t.Fatalf("legacy timeline fabricated %q: %s", fabricated, all)
				}
			}
		case "race":
			if !strings.Contains(all, "not recorded") || strings.Contains(all, "launched within") || strings.Contains(all, "started within") {
				t.Fatalf("race must not invent a launch spread: %s", all)
			}
		}
	}
}
