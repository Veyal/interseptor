package store

import (
	"strings"
	"testing"
	"time"
)

// A status-class filter on the default (newest-first) History list used to be
// answered from idx_flows_status: every flow of the class was read and sorted
// before the first 200 could be returned, 1.2 s for "2xx" on 500,000 flows.
// When the class is common the list must instead walk flows in id order and
// stop at a full page; when it is rare the index is still the right plan.
func TestR2StatusClassListPlanFollowsHowCommonTheClassIs(t *testing.T) {
	defer func(old int) { statusClassScanThreshold = old }(statusClassScanThreshold)
	statusClassScanThreshold = 20
	s := reviewOpen(t)
	for i := 0; i < 60; i++ {
		status := 200
		if i%20 == 0 {
			status = 503
		}
		reviewFlow(t, s, Flow{TS: time.UnixMilli(int64(1000 + i)), Scheme: "https", Host: "api.example.com", Status: status})
	}
	plan := func(f FlowFilter) string {
		t.Helper()
		q, args := s.flowListQuery(f, "id", 200)
		return explainPlan(t, s, q, args...)
	}
	common := plan(FlowFilter{StatusClass: 2, SortKey: "id", SortDir: -1})
	if strings.Contains(common, "TEMP B-TREE") || strings.Contains(common, "idx_flows_status") {
		t.Errorf("common class: the list sorts every match instead of walking ids:\n%s", common)
	}
	rare := plan(FlowFilter{StatusClass: 5, SortKey: "id", SortDir: -1})
	if !strings.Contains(rare, "idx_flows_status") {
		t.Errorf("rare class: the list should still use the status index:\n%s", rare)
	}
	byStatus := plan(FlowFilter{StatusClass: 2, SortKey: "status", SortDir: -1})
	if strings.Contains(byStatus, "TEMP B-TREE") {
		t.Errorf("sorted by status: the status index already gives the order:\n%s", byStatus)
	}

	// Either plan returns the same rows in the same order.
	for _, class := range []int{2, 5} {
		for _, threshold := range []int{1, 1000} {
			statusClassScanThreshold = threshold
			for _, list := range []func(FlowFilter) ([]*Flow, error){s.QueryFlowsListFilter, s.QueryFlowsFilter} {
				got, err := list(FlowFilter{StatusClass: class, SortKey: "id", SortDir: -1, Limit: 500})
				if err != nil {
					t.Fatalf("list: %v", err)
				}
				want := map[int]int{2: 57, 5: 3}[class]
				if len(got) != want {
					t.Fatalf("class %d threshold %d: %d rows, want %d", class, threshold, len(got), want)
				}
				for i, f := range got {
					if f.Status/100 != class || (i > 0 && got[i-1].ID <= f.ID) {
						t.Fatalf("class %d threshold %d: row %d = id %d status %d out of order or class", class, threshold, i, f.ID, f.Status)
					}
				}
			}
		}
	}
}

// A prune spanning more ids than one IN-list chunk must report each host once.
func TestR2PruneReportsEachHostOnce(t *testing.T) {
	s := reviewOpen(t)
	for i := 0; i < 900; i++ {
		host := "a.example.com"
		if i%3 == 0 {
			host = "b.example.com"
		}
		reviewFlow(t, s, Flow{TS: time.UnixMilli(int64(1000 + i)), Scheme: "https", Host: host, Status: 200, ReqLen: 1, ResLen: 2})
	}
	res, err := s.PruneFlows(PruneOptions{Hosts: []string{"keep.example.com"}, KeepOnly: true, DryRun: true})
	if err != nil {
		t.Fatalf("PruneFlows: %v", err)
	}
	if res.Matched != 900 || res.Bytes != 2700 {
		t.Fatalf("matched %d bytes %d, want 900 / 2700", res.Matched, res.Bytes)
	}
	if len(res.PerHost) != 2 {
		t.Fatalf("PerHost has %d rows, want one per host: %+v", len(res.PerHost), res.PerHost)
	}
	if a, b := res.PerHost[0], res.PerHost[1]; a.Host != "a.example.com" || a.Flows != 600 || a.Bytes != 1800 || b.Host != "b.example.com" || b.Flows != 300 || b.Bytes != 900 {
		t.Fatalf("PerHost = %+v, want a.example.com 600/1800 then b.example.com 300/900", res.PerHost)
	}
}
