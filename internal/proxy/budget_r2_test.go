package proxy

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func r2BudgetServer(t *testing.T, budget int64, rules []store.Rule) (*Server, *intercept.Engine) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := intercept.New()
	if err := eng.SetRules(rules); err != nil {
		t.Fatal(err)
	}
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.budget = newByteBudget(budget)
	return srv, eng
}

// The budget must charge what the exchange holds once the rewrite is done. A
// rule that grows the body used to be charged the origin's size only, so the
// 256 MB budget could be holding several times that.
func TestRewrittenResponseIsChargedWhatItHolds(t *testing.T) {
	srv, _ := r2BudgetServer(t, 64<<20, []store.Rule{{Enabled: true, Type: "res-body", BigBody: true, Match: "a", Replace: "aaaa"}})
	body := bytes.Repeat([]byte("a"), 1<<20)
	flow := srv.newFlow(&store.Flow{Host: "example.com"})
	_, _, got, transformed, _ := srv.maybeInterceptResponse(flow, budgetTestResponse(body, true))
	if !transformed || len(got) != 4<<20 {
		t.Fatalf("transformed=%v len=%d, want a 4 MiB rewrite", transformed, len(got))
	}
	if used := srv.budget.used.Load(); used != int64(len(got)) {
		t.Fatalf("budget charges %d bytes while the exchange holds a %d-byte body", used, len(got))
	}
	if flow.OriginalResBodyHash == "" {
		t.Fatal("original body was not kept for the original-vs-modified view")
	}
	rc, err := srv.st.OpenBody(flow.OriginalResBodyHash)
	if err != nil {
		t.Fatalf("open original: %v", err)
	}
	orig, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(orig, body) {
		t.Fatalf("stored original is %d bytes, want the origin's %d", len(orig), len(body))
	}
	srv.releaseBuffered(flow)
	if used := srv.budget.used.Load(); used != 0 {
		t.Fatalf("budget used after release = %d, want 0", used)
	}
}

func heapInUse() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

// While a response is held for editing, one copy of it should be alive (the
// editable snapshot). It used to be three: the read buffer, a defensive copy
// kept as "the original", and the snapshot.
func TestHeldResponseKeepsOneCopy(t *testing.T) {
	const size = 16 << 20
	srv, eng := r2BudgetServer(t, 256<<20, nil)
	eng.SetResponseEnabled(true)
	payload := bytes.Repeat([]byte("h"), size)
	flow := srv.newFlow(&store.Flow{Host: "example.com", Method: "GET", Path: "/big"})
	base := heapInUse()

	type result struct {
		body        []byte
		transformed bool
	}
	done := make(chan result, 1)
	go func() {
		_, _, b, tr, _ := srv.maybeInterceptResponse(flow, budgetTestResponse(payload, true))
		done <- result{b, tr}
	}()
	waitFor(t, "held response", func() bool { return len(eng.ResponseQueue()) == 1 })

	held := heapInUse() - base
	if held > size+size/2 {
		t.Errorf("a held %d MiB response keeps %d MiB alive, want about one copy", size>>20, held>>20)
	}
	if used := srv.budget.used.Load(); used < size || used > size+4096 {
		t.Errorf("budget charges %d bytes for a held %d-byte response", used, size)
	}

	if err := eng.ForwardResponse(eng.ResponseQueue()[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if !r.transformed || !bytes.Equal(r.body, payload) {
		t.Fatalf("forwarded held response: transformed=%v len=%d", r.transformed, len(r.body))
	}
	srv.releaseBuffered(flow)
	if used := srv.budget.used.Load(); used != 0 {
		t.Fatalf("budget used after release = %d, want 0", used)
	}
}

// An edited held response keeps the origin's body as evidence even though the
// original now aliases the held snapshot rather than a private copy.
func TestEditedHeldResponseKeepsOriginalBody(t *testing.T) {
	srv, eng := r2BudgetServer(t, 64<<20, nil)
	eng.SetResponseEnabled(true)
	flow := srv.newFlow(&store.Flow{Host: "example.com", Method: "GET", Path: "/x"})
	done := make(chan []byte, 1)
	go func() {
		_, _, b, _, _ := srv.maybeInterceptResponse(flow, budgetTestResponse([]byte("origin body"), true))
		done <- b
	}()
	waitFor(t, "held response", func() bool { return len(eng.ResponseQueue()) == 1 })
	edited := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nedited body!")
	if err := eng.ForwardResponse(eng.ResponseQueue()[0].ID, edited); err != nil {
		t.Fatal(err)
	}
	if got := <-done; string(got) != "edited body!" {
		t.Fatalf("forwarded body = %q", got)
	}
	rc, err := srv.st.OpenBody(flow.OriginalResBodyHash)
	if err != nil {
		t.Fatalf("original body not stored: %v", err)
	}
	orig, _ := io.ReadAll(rc)
	rc.Close()
	if string(orig) != "origin body" {
		t.Fatalf("stored original = %q, want the origin's body", orig)
	}
}

// A header rule that fires on a response whose body rule does not match still
// changed the response: the origin's head must be kept for History. (Dropped
// from bbd500ff by merge 2e1a562e, which only recorded it on a body change.)
func TestHeaderRuleChangeIsRecordedOnBufferedResponse(t *testing.T) {
	srv, _ := r2BudgetServer(t, 64<<20, []store.Rule{
		{Enabled: true, Type: "res-header", Match: "X-Origin: a", Replace: "X-Origin: b"},
		{Enabled: true, Type: "res-body", Match: "zzz-nomatch", Replace: "x"},
	})
	resp := budgetTestResponse([]byte("body"), true)
	resp.Header.Set("X-Origin", "a")
	flow := srv.newFlow(&store.Flow{Host: "example.com"})
	_, h, _, transformed, _ := srv.maybeInterceptResponse(flow, resp)
	if !transformed || h.Get("X-Origin") != "b" {
		t.Fatalf("transformed=%v X-Origin=%q, want the header rule applied", transformed, h.Get("X-Origin"))
	}
	if got := http.Header(flow.OriginalResHeaders).Get("X-Origin"); got != "a" {
		t.Fatalf("original X-Origin = %q, want a: the header change left no original head", got)
	}
	if flow.OriginalResBodyHash != "" {
		t.Fatal("an unchanged body was stored as a separate original")
	}
}

// When the shared budget is exhausted the log must say so; blaming the rule's
// scan limit ("set bigBody") points the operator at the wrong setting and uses
// up that rule's one-time log line. (Dropped from 3ad63280 by the same merge.)
func TestOverBudgetResponseIsNotLoggedAsOverScanLimit(t *testing.T) {
	srv, _ := r2BudgetServer(t, 1<<10, []store.Rule{{ID: 987654321, Enabled: true, Type: "res-body", Match: "zzz-nomatch", Replace: "x"}})
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	flow := srv.newFlow(&store.Flow{Host: "example.com"})
	if _, _, _, transformed, _ := srv.maybeInterceptResponse(flow, budgetTestResponse(bytes.Repeat([]byte("b"), 64<<10), true)); transformed {
		t.Fatal("over-budget response was transformed")
	}
	if strings.Contains(buf.String(), "rule 987654321") {
		t.Fatalf("budget exhaustion logged as a scan-limit skip: %s", buf.String())
	}
}
