package verify

import (
	"context"
	"errors"
	"testing"
)

var errFake = errors.New("fake transport failure")

// scriptedSender returns exchanges from a script keyed by a request tag. The tag
// is derived from the request's Body (tests set Body to a label). Each call for a
// tag advances through that tag's slice so a fluke (differing per attempt) can be
// modeled; the last entry repeats once exhausted.
type scriptedSender struct {
	script map[string][]Exchange
	calls  map[string]int
	sent   int
}

func newScriptedSender(script map[string][]Exchange) *scriptedSender {
	return &scriptedSender{script: script, calls: map[string]int{}}
}

func (s *scriptedSender) Send(_ context.Context, req Request) Exchange {
	s.sent++
	tag := string(req.Body)
	seq := s.script[tag]
	i := s.calls[tag]
	s.calls[tag]++
	if len(seq) == 0 {
		return Exchange{Status: 200}
	}
	if i >= len(seq) {
		i = len(seq) - 1
	}
	return seq[i]
}

func req(tag string) Request { return Request{Body: []byte(tag)} }

func exFlow(body string, flow int64) Exchange {
	return Exchange{Status: 200, Body: []byte(body), FlowID: flow}
}

func TestReproduceDifferentialReflectedTruePositive(t *testing.T) {
	s := newScriptedSender(map[string][]Exchange{
		"base":    {exFlow("clean", 10)},
		"payload": {exFlow("echo MARK7 here", 11)},
	})
	spec := DiffSpec{
		Class:    ClassReflected,
		Baseline: req("base"),
		Payload:  req("payload"),
		Marker:   "MARK7",
		N:        3,
	}
	res := ReproduceDifferential(context.Background(), s, spec)
	if !res.Reproduced || res.Times != 3 {
		t.Fatalf("expected reproduced x3, got %+v", res)
	}
	if len(res.PayloadFlows) != 3 || len(res.Baseline) != 3 {
		t.Fatalf("expected 3 payload + 3 baseline flow ids, got %+v", res)
	}
}

func TestReproduceDifferentialReflectedFalsePositiveRejected(t *testing.T) {
	// marker present in BOTH baseline and payload → oracle false.
	s := newScriptedSender(map[string][]Exchange{
		"base":    {exFlow("has MARK7 already", 10)},
		"payload": {exFlow("still MARK7", 11)},
	})
	spec := DiffSpec{Class: ClassReflected, Baseline: req("base"), Payload: req("payload"), Marker: "MARK7"}
	res := ReproduceDifferential(context.Background(), s, spec)
	if res.Reproduced {
		t.Fatalf("expected rejected, got %+v", res)
	}
}

func TestReproduceDifferentialFlukeRejectedByNRule(t *testing.T) {
	// First attempt reflects, second does NOT → the N-consecutive rule rejects.
	s := newScriptedSender(map[string][]Exchange{
		"base":    {exFlow("clean", 10), exFlow("clean", 12)},
		"payload": {exFlow("echo MARK7", 11), exFlow("no reflection", 13)},
	})
	spec := DiffSpec{Class: ClassReflected, Baseline: req("base"), Payload: req("payload"), Marker: "MARK7", N: 3}
	res := ReproduceDifferential(context.Background(), s, spec)
	if res.Reproduced {
		t.Fatalf("fluke must be rejected, got %+v", res)
	}
	if res.Times != 1 {
		t.Fatalf("expected 1 pass before the fluke, got %d", res.Times)
	}
}

func TestReproduceDifferentialErrorClass(t *testing.T) {
	s := newScriptedSender(map[string][]Exchange{
		"base":    {exFlow("ok", 10)},
		"payload": {exFlow("You have an error in your SQL syntax", 11)},
	})
	spec := DiffSpec{Class: ClassError, Baseline: req("base"), Payload: req("payload"), N: 2}
	res := ReproduceDifferential(context.Background(), s, spec)
	if !res.Reproduced || res.Times != 2 {
		t.Fatalf("expected error class reproduced x2, got %+v", res)
	}
}

func TestReproduceDifferentialBooleanClass(t *testing.T) {
	s := newScriptedSender(map[string][]Exchange{
		"base":  {exFlow(body(1000), 10)},
		"true":  {exFlow(body(1000), 11)},
		"false": {exFlow(body(600), 12)},
	})
	spec := DiffSpec{
		Class:        ClassBoolean,
		Baseline:     req("base"),
		PayloadTrue:  req("true"),
		PayloadFalse: req("false"),
		N:            2,
	}
	res := ReproduceDifferential(context.Background(), s, spec)
	if !res.Reproduced {
		t.Fatalf("expected boolean class reproduced, got %+v", res)
	}
}

func TestReproduceDifferentialTimingClass(t *testing.T) {
	s := newScriptedSender(map[string][]Exchange{
		"base":    {Exchange{Status: 200, DurMs: 100, FlowID: 10}},
		"payload": {Exchange{Status: 200, DurMs: 6000, FlowID: 11}},
		"control": {Exchange{Status: 200, DurMs: 120, FlowID: 12}},
	})
	spec := DiffSpec{
		Class:    ClassTiming,
		Baseline: req("base"),
		Payload:  req("payload"),
		Control:  req("control"),
		N:        2,
	}
	res := ReproduceDifferential(context.Background(), s, spec)
	if !res.Reproduced {
		t.Fatalf("expected timing class reproduced, got %+v", res)
	}
}

func TestReproduceDifferentialDefaultN(t *testing.T) {
	s := newScriptedSender(map[string][]Exchange{
		"base":    {exFlow("clean", 10)},
		"payload": {exFlow("MARK7", 11)},
	})
	spec := DiffSpec{Class: ClassReflected, Baseline: req("base"), Payload: req("payload"), Marker: "MARK7"}
	res := ReproduceDifferential(context.Background(), s, spec)
	if res.Times != 3 {
		t.Fatalf("expected default N=3, got %d", res.Times)
	}
}

func TestReproduceDifferentialUnknownClassRejected(t *testing.T) {
	s := newScriptedSender(nil)
	spec := DiffSpec{Class: VulnClass("nonsense"), Baseline: req("base"), Payload: req("payload")}
	res := ReproduceDifferential(context.Background(), s, spec)
	if res.Reproduced {
		t.Fatalf("unknown class must reject, got %+v", res)
	}
}

func TestReproduceDifferentialContextCancelled(t *testing.T) {
	s := newScriptedSender(map[string][]Exchange{
		"base":    {exFlow("clean", 10)},
		"payload": {exFlow("MARK7", 11)},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the loop starts
	spec := DiffSpec{Class: ClassReflected, Baseline: req("base"), Payload: req("payload"), Marker: "MARK7"}
	res := ReproduceDifferential(ctx, s, spec)
	if res.Reproduced {
		t.Fatalf("cancelled ctx must not reproduce, got %+v", res)
	}
	if s.sent != 0 {
		t.Fatalf("cancelled before first attempt should send nothing, sent %d", s.sent)
	}
}
