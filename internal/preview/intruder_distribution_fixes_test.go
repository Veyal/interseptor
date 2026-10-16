package preview

import (
	"strings"
	"testing"
)

func TestLatencyLabelsAreRanges(t *testing.T) {
	want := []string{"0-50", "50-100", "100-250", "250-500", "500-1k", "1k-2.5k", "2.5k-5k", "5k+"}
	for i, w := range want {
		if got := latencyLabel(i); got != w {
			t.Fatalf("latencyLabel(%d)=%q want %q", i, got, w)
		}
	}
	if !strings.Contains(latencyBinNote, "non-linear bins") || !strings.Contains(latencyBinNote, "count, not a density") {
		t.Fatalf("note: %q", latencyBinNote)
	}
}

func TestDistributionPluralsAndEmptyState(t *testing.T) {
	one := DistributionInput{RunID: "r", Rows: []DistRow{{Seq: 1, Status: 200, Length: 10, TimeMs: 5}}}
	r, err := RenderIntruderDistribution(one, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"1 clusters", "1 responses", "1 outliers"} {
		if strings.Contains(r.Alt, bad) || strings.Contains(r.Summary, bad) {
			t.Fatalf("bad plural %q in %q / %q", bad, r.Alt, r.Summary)
		}
	}
	if !strings.Contains(r.Summary, "1 response in 1 cluster") {
		t.Fatalf("summary: %s", r.Summary)
	}
	e, err := RenderIntruderDistribution(DistributionInput{RunID: "x"}, Opts{})
	if err != nil || len(e.PNG) == 0 || !strings.Contains(e.Alt, "no responses") {
		t.Fatalf("empty state: %v %+v", err, e.Alt)
	}
}

func TestStatusChipTextSpellsClass(t *testing.T) {
	cases := map[int]string{200: "200 2xx", 302: "302 3xx", 404: "404 4xx", 502: "502 5xx", 429: "429 throttle", 403: "403 throttle", 0: "err"}
	for st, want := range cases {
		if got := statusChipText(st); got != want {
			t.Fatalf("statusChipText(%d)=%q want %q", st, got, want)
		}
	}
	if !strings.Contains(classNames[3], "throttle-like") {
		t.Fatalf("class: %q", classNames[3])
	}
}

func TestOutlierDetailExplainsFlag(t *testing.T) {
	cases := []struct {
		o    distOutlier
		want string
	}{
		{distOutlier{Row: DistRow{Matched: true}, Reason: "flagged"}, "grep match"},
		{distOutlier{Row: DistRow{Anomaly: true}, Reason: "flagged"}, "length anomaly"},
		{distOutlier{Row: DistRow{}, Reason: "flagged"}, "status deviates"},
		{distOutlier{Row: DistRow{}, Reason: "rare"}, "rare"},
	}
	for _, c := range cases {
		if got := outlierDetail(c.o); !strings.Contains(got, c.want) {
			t.Fatalf("detail %q lacks %q", got, c.want)
		}
	}
}

func TestScatterPlanBoundedAndKeepsFlagged(t *testing.T) {
	var rows []DistRow
	for i := 1; i <= 10000; i++ {
		rows = append(rows, DistRow{Seq: i, Status: 200, Length: i, TimeMs: i % 700, Flagged: i == 7777})
	}
	pts := scatterPlan(rows, 1500)
	if len(pts) > 1700 {
		t.Fatalf("points=%d", len(pts))
	}
	found := false
	for _, p := range pts {
		found = found || p.Seq == 7777
	}
	if !found {
		t.Fatal("flagged row must always be plotted")
	}
}
