package collmatrix

import (
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

func sent(uid, name string, iter int, ms int64, size int64) collrun.ItemResult {
	return collrun.ItemResult{ItemUID: uid, Name: name, Method: "GET", Iteration: iter, Outcome: collexec.OutcomeSent, DurationMs: ms, Size: size, FlowID: int64(iter*10) + ms}
}

func TestTimingBreakdown(t *testing.T) {
	rep := &collrun.Report{RunUID: "r1", StartedMs: 1000, FinishedMs: 2000, Iterations: 4, Items: []collrun.ItemResult{
		sent("a", "list", 0, 100, 10), sent("a", "list", 1, 110, 10), sent("a", "list", 2, 105, 10), sent("a", "list", 3, 900, 10),
		sent("b", "get", 0, 20, 5),
		{ItemUID: "c", Name: "skipped", Outcome: collexec.OutcomeSkipped, DurationMs: 999},
		{ItemUID: "d", Name: "blocked", Outcome: collexec.OutcomeBlocked},
	}}
	rep.Items[0].Tests = []collexec.TestResult{{Name: "t", Status: collexec.TestPass, DurationMs: 4}}
	tr := Timing(rep)
	if tr.WallMs != 1000 || tr.Requests != 5 || tr.RequestMs != 100+110+105+900+20 || tr.TestMs != 4 {
		t.Fatalf("report = %+v", tr)
	}
	if tr.OtherMs != 1000-tr.RequestMs-4 && tr.OtherMs != 0 {
		t.Fatalf("other = %d", tr.OtherMs)
	}
	if len(tr.Items) != 2 || tr.Items[0].ItemUID != "a" {
		t.Fatalf("items = %+v", tr.Items)
	}
	a := tr.Items[0]
	if a.Count != 4 || a.MinMs != 100 || a.MaxMs != 900 || a.P50Ms != 105 || a.P95Ms != 900 || a.MeanMs != 303 || a.Bytes != 40 {
		t.Fatalf("a = %+v", a)
	}
	if len(tr.Outliers) != 1 || tr.Outliers[0].DurationMs != 900 || tr.Outliers[0].MedianMs != 105 {
		t.Fatalf("outliers = %+v", tr.Outliers)
	}
	if tr.Note == "" || len(tr.Phases) != 3 {
		t.Fatalf("note/phases = %q %+v", tr.Note, tr.Phases)
	}
	var sum float64
	for _, p := range tr.Phases {
		sum += p.Percent
	}
	if sum < 99 || sum > 101 {
		t.Fatalf("phase percentages sum to %v", sum)
	}
}

func TestTimingEmptyAndNoClock(t *testing.T) {
	tr := Timing(&collrun.Report{RunUID: "r"})
	if tr.Requests != 0 || tr.WallMs != 0 || tr.Items == nil || len(tr.Phases) != 3 {
		t.Fatalf("tr = %+v", tr)
	}
	// wall shorter than the summed request time (parallel or clock skew) never goes negative
	tr = Timing(&collrun.Report{StartedMs: 10, FinishedMs: 20, Items: []collrun.ItemResult{sent("a", "x", 0, 500, 1)}})
	if tr.OtherMs != 0 {
		t.Fatalf("other = %d", tr.OtherMs)
	}
}

func TestOutlierNeedsAbsoluteGap(t *testing.T) {
	rep := &collrun.Report{Items: []collrun.ItemResult{sent("a", "x", 0, 2, 1), sent("a", "x", 1, 2, 1), sent("a", "x", 2, 9, 1)}}
	if tr := Timing(rep); len(tr.Outliers) != 0 {
		t.Fatalf("tiny blip flagged: %+v", tr.Outliers)
	}
}

func TestTimingDifferentialAcrossIdentities(t *testing.T) {
	m := &Matrix{Identities: []string{"admin", "user", "anonymous"}, Rows: []Row{
		{ItemUID: "a", Name: "login", Cells: []Cell{{Class: ClassSuccess, DurationMs: 100}, {Class: ClassSuccess, DurationMs: 110}, {Class: ClassAuthFailure, DurationMs: 900, FlowID: 4}}},
		{ItemUID: "b", Name: "fast", Cells: []Cell{{Class: ClassSuccess, DurationMs: 10}, {Class: ClassSuccess, DurationMs: 12}, {Class: ClassSuccess, DurationMs: 11}}},
		{ItemUID: "c", Name: "two", Cells: []Cell{{Class: ClassSuccess, DurationMs: 10}, {Class: ClassSuccess, DurationMs: 900}}},
	}}
	d := TimingDifferential(m)
	if len(d) != 1 || d[0].Identity != "anonymous" || d[0].Row != "login" || d[0].MedianMs != 110 || d[0].FlowID != 4 {
		t.Fatalf("diff = %+v", d)
	}
}
