package collmatrix

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// manyIdentities returns n distinguishable credential-carrying identities.
func manyIdentities(n int) []Identity {
	out := make([]Identity, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Identity{Name: fmt.Sprintf("u%02d", i), Headers: []Header{{"Authorization", fmt.Sprintf("Bearer t%d", i)}}})
	}
	return out
}

// The MaxIdentities column cap must be reported by name: a role that was not
// tested is indistinguishable from a role with no finding, so dropping it
// silently would turn an untested identity into a clean column in the report.
func TestRunMatrixIdentityCapIsReported(t *testing.T) {
	be := newFake("a")
	svc := testService(be, manyIdentities(MaxIdentities+2))
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Identities) != MaxIdentities {
		t.Fatalf("identities = %d, want %d", len(m.Identities), MaxIdentities)
	}
	if len(m.CappedIdentities) != 3 { // two saved identities plus the implicit anonymous one
		t.Fatalf("capped = %v, want the last 3 identities", m.CappedIdentities)
	}
	if len(m.Rows) != 1 || len(m.Rows[0].Cells) != MaxIdentities {
		t.Fatalf("rows/cells = %d/%d", len(m.Rows), len(m.Rows[0].Cells))
	}
	found := false
	for _, w := range m.Warnings {
		if strings.Contains(w, "identities tested") && strings.Contains(w, m.CappedIdentities[0]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warning names the capped identities: %q", m.Warnings)
	}
	// A capped identity is not "skipped": that label means no usable credentials.
	for _, s := range m.Skipped {
		for _, c := range m.CappedIdentities {
			if s == c {
				t.Fatalf("%q is both skipped and capped", s)
			}
		}
	}
}

// The captured-flow matrix runs through the same engine, so it must apply and
// report the same identity cap: it used to drop identities past the limit with
// no CappedIdentities entry and no warning.
func TestRunFlowMatrixIdentityCapIsReported(t *testing.T) {
	rp := stubReplayer{respond: func(FlowTarget, Identity) ReplayResult {
		return ReplayResult{Status: 200, Size: 10}
	}}
	svc := testService(newFake(), manyIdentities(MaxIdentities+2))
	m, err := svc.RunFlowMatrix(context.Background(), rp, FlowMatrixRequest{FlowIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Identities) != MaxIdentities {
		t.Fatalf("identities = %d, want %d", len(m.Identities), MaxIdentities)
	}
	if len(m.CappedIdentities) != 3 {
		t.Fatalf("capped = %v, want the last 3 identities", m.CappedIdentities)
	}
	found := false
	for _, w := range m.Warnings {
		if strings.Contains(w, "identities tested") && strings.Contains(w, m.CappedIdentities[0]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warning names the capped identities: %q", m.Warnings)
	}
}

// When a run is aborted between two identities, the cells of the row already
// started must read as "not run" — an empty class is neither a known outcome
// nor counted as differ-from-baseline, and the UI shows a blank cell.
func TestRunFlowMatrixAbortMidRowLeavesNotRunCells(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	rp := stubReplayer{respond: func(FlowTarget, Identity) ReplayResult {
		calls++
		if calls == 1 {
			cancel()
		}
		return ReplayResult{Status: 200, Size: 10}
	}}
	svc := testService(newFake(), manyIdentities(3))
	m, err := svc.RunFlowMatrix(ctx, rp, FlowMatrixRequest{FlowIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(m.Rows))
	}
	cells := m.Rows[0].Cells
	if len(cells) != 4 { // three saved identities plus anonymous
		t.Fatalf("cells = %d, want 4", len(cells))
	}
	if cells[0].Class != ClassSuccess {
		t.Fatalf("first cell = %+v, want success", cells[0])
	}
	for i := 1; i < len(cells); i++ {
		if cells[i].Class != ClassNotRun {
			t.Fatalf("cell %d = %q, want %q after the abort", i, cells[i].Class, ClassNotRun)
		}
	}
	if !m.Partial {
		t.Fatal("matrix not marked partial after an aborted run")
	}
}
