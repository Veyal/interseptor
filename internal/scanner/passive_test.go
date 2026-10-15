package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// passiveFlows builds n flows whose responses each trip exactly one built-in
// check (a missing-security-headers HTML response), so issue counts are
// predictable.
func passiveFlows(n int) []*store.Flow {
	out := make([]*store.Flow, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, &store.Flow{
			ID: int64(i), Method: "GET", Scheme: "https", Host: "example.com",
			Port: 443, Path: fmt.Sprintf("/p%d", i), Status: 200, Mime: "text/html",
			ReqHeaders: map[string][]string{},
			ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
		})
	}
	return out
}

func emptyBodies(*store.Flow) ([]byte, []byte, error) { return nil, nil, nil }

// The engine must return issues and scanned ids in input flow order, whatever
// order the workers finish in.
func TestRunPassiveOrdersResultsByFlowNotByCompletion(t *testing.T) {
	flows := passiveFlows(200)
	req := Request{
		Flows:   flows,
		Workers: 8,
		Bodies:  emptyBodies,
		// One synthetic issue per flow, carrying the flow id in the title so
		// the order is checkable.
		Custom: func(f *store.Flow, _, _ []byte) []store.Issue {
			return []store.Issue{{FlowID: f.ID, Severity: "Info", Title: fmt.Sprintf("custom-%d", f.ID)}}
		},
		Disabled: allBuiltinsDisabled(),
	}
	res, err := RunPassive(context.Background(), req)
	if err != nil {
		t.Fatalf("RunPassive: %v", err)
	}
	if len(res.Issues) != len(flows) {
		t.Fatalf("got %d issues, want %d", len(res.Issues), len(flows))
	}
	for i, is := range res.Issues {
		want := fmt.Sprintf("custom-%d", flows[i].ID)
		if is.Title != want {
			t.Fatalf("issue %d = %q, want %q — results are not in flow order", i, is.Title, want)
		}
	}
	if len(res.FlowIDs) != len(flows) {
		t.Fatalf("got %d scanned ids, want %d", len(res.FlowIDs), len(flows))
	}
	for i, id := range res.FlowIDs {
		if id != flows[i].ID {
			t.Fatalf("scanned id %d = %d, want %d", i, id, flows[i].ID)
		}
	}
	// Same input, same output, run after run.
	again, err := RunPassive(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for i := range again.Issues {
		if again.Issues[i] != res.Issues[i] {
			t.Fatalf("issue %d differs between runs: %#v vs %#v", i, again.Issues[i], res.Issues[i])
		}
	}
}

// The work must actually be spread across goroutines, and default to NumCPU.
func TestRunPassiveUsesSeveralWorkersAndDefaultsToNumCPU(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("single-CPU machine: concurrency is unobservable")
	}
	var mu sync.Mutex
	goroutines := map[string]bool{}
	var live, peak int64
	req := Request{
		Flows:    passiveFlows(400),
		Disabled: allBuiltinsDisabled(),
		Bodies:   emptyBodies,
		Custom: func(f *store.Flow, _, _ []byte) []store.Issue {
			n := atomic.AddInt64(&live, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
					break
				}
			}
			mu.Lock()
			goroutines[goroutineLabel()] = true
			mu.Unlock()
			// Hold briefly so overlap is observable.
			for i := 0; i < 2000; i++ {
				_ = i * i
			}
			atomic.AddInt64(&live, -1)
			return nil
		},
	}
	if _, err := RunPassive(context.Background(), req); err != nil {
		t.Fatalf("RunPassive: %v", err)
	}
	mu.Lock()
	distinct := len(goroutines)
	mu.Unlock()
	want := min(runtime.NumCPU(), 400)
	if distinct < 2 {
		t.Fatalf("the scan ran on %d goroutine(s); Workers:0 must default to runtime.NumCPU() = %d", distinct, runtime.NumCPU())
	}
	if distinct > want {
		t.Errorf("the scan ran on %d goroutines, more than NumCPU = %d", distinct, want)
	}
}

// Cancelling must stop the scan promptly and report the context error rather
// than returning a half-finished result as if it were complete.
func TestRunPassiveStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var seen int64
	_, err := RunPassive(ctx, Request{
		Flows:    passiveFlows(5000),
		Workers:  4,
		Disabled: allBuiltinsDisabled(),
		Bodies:   emptyBodies,
		Custom: func(f *store.Flow, _, _ []byte) []store.Issue {
			if atomic.AddInt64(&seen, 1) == 10 {
				cancel()
			}
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := atomic.LoadInt64(&seen); n >= 5000 {
		t.Fatalf("cancel did not stop the scan: %d of 5000 flows were scanned", n)
	}
}

// Progress must be monotonic, end at the total, and issues must stream in flow
// order so a live view and the persisted list agree.
func TestRunPassiveStreamsOrderedProgressAndIssues(t *testing.T) {
	flows := passiveFlows(120)
	var progress []Progress
	var streamed []store.Issue
	var mu sync.Mutex
	res, err := RunPassive(context.Background(), Request{
		Flows:    flows,
		Workers:  6,
		Disabled: allBuiltinsDisabled(),
		Bodies:   emptyBodies,
		Custom: func(f *store.Flow, _, _ []byte) []store.Issue {
			return []store.Issue{{FlowID: f.ID, Severity: "Info", Title: fmt.Sprintf("custom-%d", f.ID)}}
		},
		OnProgress: func(p Progress) { mu.Lock(); progress = append(progress, p); mu.Unlock() },
		OnIssue:    func(is store.Issue) { mu.Lock(); streamed = append(streamed, is); mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("RunPassive: %v", err)
	}
	if len(progress) == 0 {
		t.Fatal("no progress was reported")
	}
	last := progress[len(progress)-1]
	if last.Scanned != len(flows) || last.Total != len(flows) || !last.Done {
		t.Fatalf("final progress = %#v, want scanned=total=%d and done", last, len(flows))
	}
	prev := Progress{}
	for i, p := range progress {
		if p.Total != len(flows) {
			t.Errorf("progress %d total = %d, want %d", i, p.Total, len(flows))
		}
		if p.Scanned < prev.Scanned || p.Issues < prev.Issues {
			t.Errorf("progress went backwards at %d: %#v after %#v", i, p, prev)
		}
		prev = p
	}
	// Callbacks are serialised by the engine, so the stream is a prefix-ordered
	// view of the final list.
	mu.Lock()
	defer mu.Unlock()
	if len(streamed) != len(res.Issues) {
		t.Fatalf("streamed %d issues, result has %d", len(streamed), len(res.Issues))
	}
	for i := range streamed {
		if streamed[i].Title != res.Issues[i].Title {
			t.Fatalf("streamed issue %d = %q, result has %q — the stream is not in flow order",
				i, streamed[i].Title, res.Issues[i].Title)
		}
	}
}

// A missing body blob skips one flow; any other body error fails the scan.
func TestRunPassiveSkipsMissingBlobsAndPropagatesRealErrors(t *testing.T) {
	flows := passiveFlows(10)
	res, err := RunPassive(context.Background(), Request{
		Flows:    flows,
		Workers:  3,
		Disabled: allBuiltinsDisabled(),
		Bodies: func(f *store.Flow) ([]byte, []byte, error) {
			if f.ID == 4 {
				return nil, nil, fmt.Errorf("blob: %w", os.ErrNotExist)
			}
			return nil, nil, nil
		},
		Custom: func(f *store.Flow, _, _ []byte) []store.Issue {
			return []store.Issue{{FlowID: f.ID, Title: fmt.Sprintf("custom-%d", f.ID)}}
		},
	})
	if err != nil {
		t.Fatalf("a missing blob must not fail the scan: %v", err)
	}
	if len(res.Issues) != 9 {
		t.Fatalf("got %d issues, want 9 (flow 4 skipped)", len(res.Issues))
	}
	// The skipped flow is still part of the reconciliation universe.
	if len(res.FlowIDs) != 10 {
		t.Fatalf("got %d scanned ids, want all 10", len(res.FlowIDs))
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}

	boom := errors.New("disk on fire")
	if _, err := RunPassive(context.Background(), Request{
		Flows:    flows,
		Disabled: allBuiltinsDisabled(),
		Bodies: func(f *store.Flow) ([]byte, []byte, error) {
			if f.ID == 2 {
				return nil, nil, boom
			}
			return nil, nil, nil
		},
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the body error", err)
	}
}

// Out-of-scope flows are not analysed but stay in the reconciliation universe.
func TestRunPassiveHonoursScope(t *testing.T) {
	flows := passiveFlows(6)
	res, err := RunPassive(context.Background(), Request{
		Flows:    flows,
		Disabled: allBuiltinsDisabled(),
		Bodies:   emptyBodies,
		InScope:  func(f *store.Flow) bool { return f.ID%2 == 0 },
		Custom: func(f *store.Flow, _, _ []byte) []store.Issue {
			return []store.Issue{{FlowID: f.ID, Title: fmt.Sprintf("custom-%d", f.ID)}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 3 {
		t.Fatalf("got %d issues, want 3 in-scope", len(res.Issues))
	}
	if len(res.FlowIDs) != 6 {
		t.Fatalf("got %d scanned ids, want all 6", len(res.FlowIDs))
	}
	for _, is := range res.Issues {
		if is.FlowID%2 != 0 {
			t.Errorf("out-of-scope flow %d was analysed", is.FlowID)
		}
	}
}

// The built-in checks still run through the parallel engine and produce the
// same issues AnalyzeWithDisabled does for the same flow.
func TestRunPassiveMatchesAnalyzeForBuiltins(t *testing.T) {
	flows := passiveFlows(3)
	res, err := RunPassive(context.Background(), Request{Flows: flows, Bodies: emptyBodies})
	if err != nil {
		t.Fatal(err)
	}
	var want []store.Issue
	for _, f := range flows {
		want = append(want, AnalyzeWithDisabled(f, nil, nil, nil)...)
	}
	if len(res.Issues) != len(want) || len(want) == 0 {
		t.Fatalf("got %d issues, AnalyzeWithDisabled gives %d", len(res.Issues), len(want))
	}
	for i := range want {
		if res.Issues[i] != want[i] {
			t.Fatalf("issue %d = %#v, want %#v", i, res.Issues[i], want[i])
		}
	}
}

// An empty flow list is not an error, and reports a finished scan.
func TestRunPassiveWithNoFlows(t *testing.T) {
	var got []Progress
	res, err := RunPassive(context.Background(), Request{
		Bodies:     emptyBodies,
		OnProgress: func(p Progress) { got = append(got, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 0 || len(res.FlowIDs) != 0 {
		t.Fatalf("result = %#v, want empty", res)
	}
	if len(got) != 1 || !got[0].Done || got[0].Total != 0 {
		t.Fatalf("progress = %#v, want one finished report", got)
	}
}

func allBuiltinsDisabled() map[string]bool {
	out := make(map[string]bool, len(BuiltinChecks))
	for _, c := range BuiltinChecks {
		out[c.ID] = true
	}
	return out
}

// goroutineLabel returns the running goroutine's id, so the concurrency test
// can count how many distinct goroutines the scan actually ran on.
func goroutineLabel() string {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	return s
}
