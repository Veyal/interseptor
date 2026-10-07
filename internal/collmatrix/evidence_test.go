package collmatrix

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

type sinkCall struct {
	finding, flow int64
	note, role    string
	source        string
	src           int64
}

type fakeSink struct {
	calls []sinkCall
	fail  int64
}

func (f *fakeSink) AttachFlowWithMetadata(fid, flow int64, note string, _ int, role, _, source string, srcFlow int64, _ ...store.FindingChange) error {
	if f.fail == flow {
		return errors.New("boom")
	}
	f.calls = append(f.calls, sinkCall{fid, flow, note, role, source, srcFlow})
	return nil
}

func brokenAccessMatrix(t *testing.T) *Matrix {
	t.Helper()
	be := newFake("list", "admin")
	be.respond = func(in collexec.StepInput) *collexec.StepResult {
		base := int64(100)
		if in.Chain.Item.Name == "admin" {
			base = 200
		}
		switch in.Identity {
		case "admin":
			return stepResp(200, 500, 9, base+1)
		case "user":
			if in.Chain.Item.Name == "admin" {
				return stepResp(200, 500, 8, base+2)
			}
			return stepResp(403, 30, 8, base+2)
		}
		return stepResp(401, 20, 3, base+3)
	}
	svc := testService(be, []Identity{
		{Name: "admin", Headers: []Header{{"Authorization", "a"}}},
		{Name: "user", Headers: []Header{{"Authorization", "u"}}, Expect: ExpectDeny},
	})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMatrixEvidenceOnlyFlagged(t *testing.T) {
	m := brokenAccessMatrix(t)
	all := MatrixEvidence(m, false)
	if len(all) != 6 {
		t.Fatalf("all = %d", len(all))
	}
	fl := MatrixEvidence(m, true)
	if len(fl) != 2 {
		t.Fatalf("flagged = %+v", fl)
	}
	if fl[0].Role != "baseline" || fl[1].Role != "result" || !strings.Contains(fl[1].Note, "violation") {
		t.Fatalf("roles/notes = %+v", fl)
	}
}

func TestAttachWritesTypedCapturedFlowBlocks(t *testing.T) {
	m := brokenAccessMatrix(t)
	sink := &fakeSink{}
	n, err := Attach(sink, 7, MatrixEvidence(m, true), func(s string) string { return s })
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, c := range sink.calls {
		if c.finding != 7 || c.source != "captured_flow" || c.src != c.flow || c.flow <= 0 {
			t.Fatalf("call = %+v", c)
		}
	}
}

func TestAttachGuards(t *testing.T) {
	if _, err := Attach(nil, 1, []Evidence{{FlowID: 1}}, nil); err == nil {
		t.Fatal("nil sink must error")
	}
	if _, err := Attach(&fakeSink{}, 0, []Evidence{{FlowID: 1}}, nil); err == nil {
		t.Fatal("zero finding must error")
	}
	sink := &fakeSink{fail: 2}
	n, err := Attach(sink, 1, []Evidence{{FlowID: 1}, {FlowID: 2}, {FlowID: 3}}, nil)
	if err == nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var many []Evidence
	for i := 1; i <= MaxAttach+10; i++ {
		many = append(many, Evidence{FlowID: int64(i)})
	}
	sink = &fakeSink{}
	if n, _ := Attach(sink, 1, many, nil); n != MaxAttach {
		t.Fatalf("cap = %d", n)
	}
}

func TestAttachScrubsNotes(t *testing.T) {
	sink := &fakeSink{}
	_, _ = Attach(sink, 1, []Evidence{{FlowID: 4, Note: "token canary-777"}}, func(s string) string { return strings.ReplaceAll(s, "canary-777", "[redacted]") })
	if strings.Contains(sink.calls[0].note, "canary") {
		t.Fatalf("note not scrubbed: %q", sink.calls[0].note)
	}
}

func TestReportEvidenceAddsRunContext(t *testing.T) {
	rep := &collrun.Report{RunUID: "r1", CollectionName: "Demo API", EnvName: "staging", Items: []collrun.ItemResult{
		{ItemUID: "a", Name: "login", FlowID: 5, HTTPStatus: 200},
		{ItemUID: "b", Name: "orders", FlowID: 6, HTTPStatus: 500, Tests: []collexec.TestResult{{Name: "status is 200", Status: collexec.TestFail}}},
		{ItemUID: "c", Name: "skipped"},
		{ItemUID: "b", Name: "orders", FlowID: 6},
	}}
	ev := ReportEvidence(rep, nil)
	if len(ev) != 2 {
		t.Fatalf("ev = %+v", ev)
	}
	if !strings.Contains(ev[1].Note, "r1") || !strings.Contains(ev[1].Note, "failed: status is 200") || !strings.Contains(ev[1].Note, "staging") {
		t.Fatalf("note = %q", ev[1].Note)
	}
	if got := ReportEvidence(rep, []string{"a"}); len(got) != 1 || got[0].FlowID != 5 {
		t.Fatalf("selection = %+v", got)
	}
}

func TestMatrixRendersThroughAuthzMatrixRenderer(t *testing.T) {
	m := brokenAccessMatrix(t)
	in := m.AuthzInput()
	if len(in.Cols) != 3 || in.BaselineName != "admin" || len(in.Rows) != 2 {
		t.Fatalf("input = %+v", in)
	}
	if !in.Rows[1].Cells[1].Broken || in.Rows[0].Cells[1].Broken || !in.Rows[0].Cells[1].AccessDenied {
		t.Fatalf("cells = %+v", in.Rows)
	}
	rd, err := m.Render(900, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(rd.PNG)); err != nil {
		t.Fatal(err)
	}
	if rd.Alt == "" {
		t.Fatal("missing alt text")
	}
}
