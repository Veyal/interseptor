package collmatrix

import (
	"context"
	"errors"
	"testing"
)

type stubReplayer struct {
	respond func(tg FlowTarget, id Identity) ReplayResult
}

func (s stubReplayer) Targets(ids []int64) ([]FlowTarget, error) {
	var out []FlowTarget
	for _, id := range ids {
		out = append(out, FlowTarget{FlowID: id, Method: "GET", Path: "/admin/users"})
	}
	return out, nil
}

func (s stubReplayer) Replay(_ context.Context, tg FlowTarget, id Identity) ReplayResult {
	return s.respond(tg, id)
}

// The captured-flow matrix runs through the same engine: a lower-privileged
// identity that still succeeds on a captured admin flow is flagged as a
// candidate, with the same classes and evidence contract as the collection matrix.
func TestRunFlowMatrixDifferential(t *testing.T) {
	rp := stubReplayer{respond: func(tg FlowTarget, id Identity) ReplayResult {
		switch id.Name {
		case "admin":
			return ReplayResult{Status: 200, Size: 500, FlowID: 11}
		case "user":
			return ReplayResult{Status: 200, Size: 500, FlowID: 12} // broken access control
		default: // anonymous
			return ReplayResult{Status: 401, Size: 10, FlowID: 13}
		}
	}}
	svc := testService(newFake(), []Identity{
		{Name: "admin", Headers: []Header{{"Authorization", "Bearer a"}}},
		{Name: "user", Headers: []Header{{"Authorization", "Bearer u"}}, Expect: ExpectDeny},
	})
	m, err := svc.RunFlowMatrix(context.Background(), rp, FlowMatrixRequest{FlowIDs: []int64{42}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rows) != 1 || m.Baseline != "admin" {
		t.Fatalf("rows=%d baseline=%s", len(m.Rows), m.Baseline)
	}
	row := m.Rows[0]
	if row.ItemUID != "flow-42" {
		t.Fatalf("row item uid = %q, want flow-42", row.ItemUID)
	}
	if c := row.Cells[1]; c.Class != ClassSuccess || c.Flag != FlagViolation || !c.SameAsBaseline {
		t.Fatalf("user cell = %+v, want success+violation+same", c)
	}
	if c := row.Cells[2]; c.Class != ClassAuthFailure || c.Flag != "" {
		t.Fatalf("anonymous cell = %+v, want auth_failure no flag", c)
	}
	if m.Summary.Violations < 1 || len(m.Hypotheses) == 0 {
		t.Fatalf("summary=%+v hypotheses=%v", m.Summary, m.Hypotheses)
	}
	// Matrix is remembered for render/attach, like the collection matrix.
	if _, ok := svc.Matrix(m.ID); !ok {
		t.Fatal("flow matrix not remembered")
	}
}

func TestRunFlowMatrixGuards(t *testing.T) {
	svc := testService(newFake(), []Identity{{Name: "u", Headers: []Header{{"Authorization", "t"}}}})
	rp := stubReplayer{respond: func(FlowTarget, Identity) ReplayResult { return ReplayResult{Status: 200} }}
	if _, err := svc.RunFlowMatrix(context.Background(), nil, FlowMatrixRequest{FlowIDs: []int64{1}}); !errors.Is(err, errNoReplayer) {
		t.Fatalf("nil replayer err = %v, want errNoReplayer", err)
	}
	if _, err := svc.RunFlowMatrix(context.Background(), rp, FlowMatrixRequest{}); !errors.Is(err, errNoFlows) {
		t.Fatalf("no flows err = %v, want errNoFlows", err)
	}
}
