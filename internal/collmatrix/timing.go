package collmatrix

import (
	"sort"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

// TimingNote is shown with every timing view: the sender records the total
// round-trip time of a request, not its DNS, connect, TLS or first-byte phases.
const TimingNote = "Request time is the total round trip recorded by the sender. DNS, connect, TLS and first-byte phases are not recorded, so they are not split out."

// Phase is one slice of a run's wall time.
type Phase struct {
	Name    string  `json:"name"`
	Ms      int64   `json:"ms"`
	Percent float64 `json:"percent"`
}

// TimingStat summarises one request across iterations (or identities).
type TimingStat struct {
	ItemUID string `json:"itemUid"`
	Name    string `json:"name"`
	Method  string `json:"method,omitempty"`
	Count   int    `json:"count"`
	MinMs   int64  `json:"minMs"`
	P50Ms   int64  `json:"p50Ms"`
	P95Ms   int64  `json:"p95Ms"`
	MaxMs   int64  `json:"maxMs"`
	MeanMs  int64  `json:"meanMs"`
	TotalMs int64  `json:"totalMs"`
	TestMs  int64  `json:"testMs"`
	Bytes   int64  `json:"bytes"`
}

// TimingOutlier is one execution much slower than its request's median.
type TimingOutlier struct {
	ItemUID    string `json:"itemUid"`
	Name       string `json:"name"`
	Iteration  int    `json:"iteration"`
	FlowID     int64  `json:"flowId,omitempty"`
	DurationMs int64  `json:"durationMs"`
	MedianMs   int64  `json:"medianMs"`
}

// TimingReport is the timing breakdown of a run.
type TimingReport struct {
	RunUID     string          `json:"runUid"`
	WallMs     int64           `json:"wallMs"`
	RequestMs  int64           `json:"requestMs"`
	TestMs     int64           `json:"testMs"`
	OtherMs    int64           `json:"otherMs"`
	Requests   int             `json:"requests"`
	Phases     []Phase         `json:"phases"`
	Items      []TimingStat    `json:"items"` // slowest total first
	Outliers   []TimingOutlier `json:"outliers,omitempty"`
	Note       string          `json:"note"`
	Iterations int             `json:"iterations,omitempty"`
}

// Outlier rule: at least this many times the request's median and this many
// milliseconds above it, so a 2 ms vs 9 ms blip is never reported.
const (
	outlierFactor = 3
	outlierMinMs  = 50
	maxOutliers   = 20
)

func quantile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(q*float64(len(sorted))+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(int(float64(part)*1000/float64(whole)+0.5)) / 10
}

// Timing breaks a run report down by request. Only sent requests carry a
// request time. Wall time minus request and test time is "other": pre-request
// scripts, delays, variable resolution and scope checks.
func Timing(rep *collrun.Report) *TimingReport {
	tr := &TimingReport{RunUID: rep.RunUID, Note: TimingNote, Iterations: rep.Iterations, Items: []TimingStat{}}
	if rep.FinishedMs > rep.StartedMs {
		tr.WallMs = rep.FinishedMs - rep.StartedMs
	}
	type acc struct {
		stat TimingStat
		ms   []int64
	}
	byItem := map[string]*acc{}
	var order []string
	for _, it := range rep.Items {
		if it.Outcome != collexec.OutcomeSent {
			continue
		}
		a := byItem[it.ItemUID]
		if a == nil {
			a = &acc{stat: TimingStat{ItemUID: it.ItemUID, Name: it.Name, Method: it.Method}}
			byItem[it.ItemUID] = a
			order = append(order, it.ItemUID)
		}
		a.ms = append(a.ms, it.DurationMs)
		a.stat.Bytes += it.Size
		for _, t := range it.Tests {
			a.stat.TestMs += t.DurationMs
		}
		tr.Requests++
	}
	for _, uid := range order {
		a := byItem[uid]
		sorted := append([]int64(nil), a.ms...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		s := a.stat
		s.Count = len(sorted)
		s.MinMs, s.MaxMs = sorted[0], sorted[len(sorted)-1]
		s.P50Ms, s.P95Ms = quantile(sorted, 0.5), quantile(sorted, 0.95)
		for _, v := range sorted {
			s.TotalMs += v
		}
		s.MeanMs = s.TotalMs / int64(s.Count)
		tr.RequestMs += s.TotalMs
		tr.TestMs += s.TestMs
		tr.Items = append(tr.Items, s)
	}
	sort.SliceStable(tr.Items, func(i, j int) bool { return tr.Items[i].TotalMs > tr.Items[j].TotalMs })
	for _, it := range rep.Items {
		a := byItem[it.ItemUID]
		if a == nil || it.Outcome != collexec.OutcomeSent || len(a.ms) < 3 {
			continue
		}
		med := quantile(sortedCopy(a.ms), 0.5)
		if it.DurationMs >= med*outlierFactor && it.DurationMs-med >= outlierMinMs && len(tr.Outliers) < maxOutliers {
			tr.Outliers = append(tr.Outliers, TimingOutlier{ItemUID: it.ItemUID, Name: it.Name, Iteration: it.Iteration, FlowID: it.FlowID, DurationMs: it.DurationMs, MedianMs: med})
		}
	}
	tr.OtherMs = tr.WallMs - tr.RequestMs - tr.TestMs
	if tr.OtherMs < 0 {
		tr.OtherMs = 0
	}
	whole := tr.RequestMs + tr.TestMs + tr.OtherMs
	tr.Phases = []Phase{
		{Name: "requests", Ms: tr.RequestMs, Percent: pct(tr.RequestMs, whole)},
		{Name: "tests", Ms: tr.TestMs, Percent: pct(tr.TestMs, whole)},
		{Name: "other", Ms: tr.OtherMs, Percent: pct(tr.OtherMs, whole)},
	}
	return tr
}

func sortedCopy(in []int64) []int64 {
	out := append([]int64(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// TimingDiff is a response time that stands out across identities for one
// request. A different code path (for example a user lookup that skips the
// password hash) can show up as time before it shows up as a status.
type TimingDiff struct {
	Row      string `json:"row"`
	ItemUID  string `json:"itemUid"`
	Identity string `json:"identity"`
	FlowID   int64  `json:"flowId,omitempty"`
	Ms       int64  `json:"ms"`
	MedianMs int64  `json:"medianMs"`
}

// TimingDifferential lists cells of a matrix at least outlierFactor times (and
// outlierMinMs above) their row's median. Needs three or more identities.
func TimingDifferential(m *Matrix) []TimingDiff {
	var out []TimingDiff
	for _, r := range m.Rows {
		var ms []int64
		for _, c := range r.Cells {
			if c.Class != ClassNotRun && c.Class != ClassBlocked && c.Class != ClassError {
				ms = append(ms, c.DurationMs)
			}
		}
		if len(ms) < 3 {
			continue
		}
		med := quantile(sortedCopy(ms), 0.5)
		for i, c := range r.Cells {
			if c.Class == ClassNotRun || c.Class == ClassBlocked || c.Class == ClassError || i >= len(m.Identities) {
				continue
			}
			if c.DurationMs >= med*outlierFactor && c.DurationMs-med >= outlierMinMs {
				out = append(out, TimingDiff{Row: r.Name, ItemUID: r.ItemUID, Identity: m.Identities[i], FlowID: c.FlowID, Ms: c.DurationMs, MedianMs: med})
			}
		}
	}
	return out
}
