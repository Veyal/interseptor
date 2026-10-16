package preview

import (
	"strings"
	"testing"
)

func TestTimelineSummaryIsNeutral(t *testing.T) {
	r, err := RenderIntruderTimeline(tlFixture12(), Opts{Width: 640})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"10 of 12 returned 2xx", "2 throttle statuses (429/403/423)"} {
		if !strings.Contains(r.Summary, want) {
			t.Fatalf("summary %q missing %q", r.Summary, want)
		}
	}
	if strings.Contains(r.Summary, "succeeded") || strings.Contains(r.Alt, "succeeded") {
		t.Fatalf("2xx must not read as success: %q / %q", r.Summary, r.Alt)
	}
}

func TestTimelineNoThrottleIsHedged(t *testing.T) {
	in := tlFixture12()
	for i := range in.Rows {
		in.Rows[i].Status = 200
		in.Rows[i].RLHeaders = nil
	}
	r, _ := RenderIntruderTimeline(in, Opts{Width: 640})
	if !strings.Contains(r.Alt, "no throttling status") || !strings.Contains(r.Alt, "body-based lockouts are not detected unless a grep pattern was set") {
		t.Fatalf("alt must hedge: %s", r.Alt)
	}
	if strings.Contains(r.Alt, "No 429, 403 or 423 response was recorded") {
		t.Fatal("old absolute wording")
	}
}

func TestTimelineCountsMatchedAndOther(t *testing.T) {
	in := tlFixture12()
	in.Rows[0].Matched = true
	in.Rows[1].Status = 503
	in.Rows[2].Status = 401
	_, st := tlPrepare(in)
	if st.matched != 1 || st.other != 2 {
		t.Fatalf("matched=%d other=%d", st.matched, st.other)
	}
	r, _ := RenderIntruderTimeline(in, Opts{Width: 640})
	for _, want := range []string{"1 matched the grep pattern", "2 5xx/other"} {
		if !strings.Contains(r.Summary, want) {
			t.Fatalf("summary %q missing %q", r.Summary, want)
		}
	}
	if !strings.Contains(r.Alt, "matched the grep pattern") {
		t.Fatalf("alt: %s", r.Alt)
	}
}

func TestTimelineRankBucketsAreDense(t *testing.T) {
	in := tlFixture12()
	for i := range in.Rows {
		in.Rows[i].StartUs, in.Rows[i].EndUs = 0, 0
	}
	bySeq, st := tlPrepare(in)
	b := tlBucketize(bySeq, st)
	if len(b) != 12 {
		t.Fatalf("12 rows must give 12 buckets, got %d", len(b))
	}
	for i, x := range b {
		if x.n != 1 {
			t.Fatalf("bucket %d has %d rows, want 1", i, x.n)
		}
	}
	big := TimelineInput{}
	for i := 1; i <= 100; i++ {
		big.Rows = append(big.Rows, TimelineRow{Seq: i, Status: 200})
	}
	bs, sts := tlPrepare(big)
	if got := len(tlBucketize(bs, sts)); got != timelineBuckets {
		t.Fatalf("got %d", got)
	}
}

func TestTimelineTimedEmptyBucketsAreVisible(t *testing.T) {
	in := TimelineInput{Threads: 1, Rows: []TimelineRow{
		{Seq: 1, StartUs: 0, EndUs: 1000, Status: 200},
		{Seq: 2, StartUs: 0, EndUs: 100000, Status: 200},
	}}
	bySeq, st := tlPrepare(in)
	b := tlBucketize(bySeq, st)
	empty := 0
	for _, x := range b {
		if x.n == 0 {
			empty++
		}
	}
	if len(b) != timelineBuckets || empty != timelineBuckets-2 {
		t.Fatalf("buckets=%d empty=%d", len(b), empty)
	}
	if !strings.Contains(tlSparkTitle(st), "completion time") || !strings.Contains(tlSparkTitle(st), "no completions") {
		t.Fatalf("title: %s", tlSparkTitle(st))
	}
}

func TestTimelineZeroSpanKeepsBarsInsidePlot(t *testing.T) {
	in := TimelineInput{Threads: 1, Rows: []TimelineRow{
		{Seq: 1, StartUs: 1000, EndUs: 1000, Status: 200},
		{Seq: 2, StartUs: 1000, EndUs: 1000, Status: 200},
	}}
	bySeq, st := tlPrepare(in)
	g := timelineGeometry(in, bySeq, st, Opts{Width: 640}, defaultMaxRows)
	if x := g.XOf(1000); x+4 > g.PlotX1 {
		t.Fatalf("bar at %d clipped by plot edge %d", x, g.PlotX1)
	}
	for _, l := range tlTickLabels(g) {
		if strings.Contains(l, "00000") || strings.Contains(l, "99999") {
			t.Fatalf("float noise in tick %q", l)
		}
	}
	if _, err := RenderIntruderTimeline(in, Opts{Width: 640}); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineFactsCarryReportContext(t *testing.T) {
	in := tlFixture12()
	in.Method, in.Path = "POST", "/api/login"
	in.StartedUnixMs = 1790000000000 // 2026-09-21T14:13:20Z
	bySeq, st := tlPrepare(in)
	facts := strings.Join(tlFacts(in, st, bySeq), "\n")
	for _, want := range []string{"POST /api/login", "2026-09-21 14:13:20 UTC", "req/s", "first block after 10 accepted", "Retry-After: 30", "duration"} {
		if !strings.Contains(facts, want) {
			t.Fatalf("facts missing %q:\n%s", want, facts)
		}
	}
	r, _ := RenderIntruderTimeline(in, Opts{Width: 640})
	if !strings.Contains(r.Summary, "Retry-After 30") {
		t.Fatalf("summary should carry Retry-After: %s", r.Summary)
	}
}

func TestTimelineDegenerateWording(t *testing.T) {
	in := TimelineInput{Rows: []TimelineRow{{Seq: 1, Status: 200}}}
	bySeq, st := tlPrepare(in)
	if f := tlFacts(in, st, bySeq)[0]; !strings.Contains(f, "threads -") {
		t.Fatalf("threads: %s", f)
	}
	r, _ := RenderIntruderTimeline(in, Opts{})
	if strings.Contains(r.Alt, "0 threads") {
		t.Fatalf("alt: %s", r.Alt)
	}
}
