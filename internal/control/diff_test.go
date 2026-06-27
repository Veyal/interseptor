package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

// The pure diff helper must summarize a status change, a header added, and a
// changed body line on two known request/response pairs.
func TestDiffResponses(t *testing.T) {
	headersA := map[string][]string{"Content-Type": {"text/html"}, "Server": {"nginx"}}
	headersB := map[string][]string{"Content-Type": {"text/html"}, "Server": {"nginx"}, "X-Debug": {"1"}}
	bodyA := "line one\nshared\nline three"
	bodyB := "line ONE\nshared\nline three"

	d := diffResponses(1, 2, 200, 500, 30, 42, headersA, headersB, []byte(bodyA), []byte(bodyB), false)

	if d.StatusSame || d.StatusA != 200 || d.StatusB != 500 {
		t.Fatalf("status diff wrong: %+v", d)
	}
	if d.ResLenDelta != 12 {
		t.Fatalf("resLenDelta: got %d want 12", d.ResLenDelta)
	}
	// Exactly one header delta: X-Debug added.
	if len(d.HeaderDeltas) != 1 || d.HeaderDeltas[0].Name != "X-Debug" || d.HeaderDeltas[0].Kind != "added" {
		t.Fatalf("header deltas wrong: %+v", d.HeaderDeltas)
	}
	// One changed body line (line 1); lines 2 and 3 are identical.
	if d.BodySame || len(d.BodyDeltas) != 1 || d.BodyDeltas[0].Line != 1 {
		t.Fatalf("body deltas wrong: %+v", d.BodyDeltas)
	}
	if d.BodyDeltas[0].A != "line one" || d.BodyDeltas[0].B != "line ONE" {
		t.Fatalf("body delta content wrong: %+v", d.BodyDeltas[0])
	}
	for _, want := range []string{"status 200→500", "+12 bytes", "1 header change", "1 body line"} {
		if !strings.Contains(d.Summary, want) {
			t.Fatalf("summary %q missing %q", d.Summary, want)
		}
	}

	// Identical responses → everything reports "same".
	same := diffResponses(3, 4, 200, 200, 10, 10, headersA, headersA, []byte(bodyA), []byte(bodyA), false)
	if !same.StatusSame || !same.BodySame || len(same.HeaderDeltas) != 0 || same.ResLenDelta != 0 {
		t.Fatalf("identical flows should diff clean: %+v", same)
	}
}

// The body diff must stay bounded: many changed lines are capped and the
// overflow counted, never dumping an unbounded body.
func TestDiffBodyLinesBounded(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < diffMaxBodyLines+25; i++ {
		a.WriteString("a\n")
		b.WriteString("b\n")
	}
	deltas, more := diffBodyLines(a.String(), b.String())
	if len(deltas) != diffMaxBodyLines {
		t.Fatalf("deltas not capped: got %d want %d", len(deltas), diffMaxBodyLines)
	}
	if more <= 0 {
		t.Fatalf("expected overflow lines counted, got %d", more)
	}
}

// The REST endpoint diffs two real stored flows and 404s on a missing id.
func TestDiffFlowsEndpoint(t *testing.T) {
	h, s, _ := newHub(t)

	resHashA := writeBody(t, s, "hello\nworld")
	resHashB := writeBody(t, s, "hello\nWORLD!")
	idA, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "x.com", Path: "/a",
		Status: 200, ResHeaders: map[string][]string{"Content-Type": {"text/plain"}},
		ResBodyHash: resHashA, ResLen: 11,
	})
	if err != nil {
		t.Fatalf("InsertFlow A: %v", err)
	}
	idB, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "x.com", Path: "/a",
		Status: 500, ResHeaders: map[string][]string{"Content-Type": {"text/plain"}, "X-Debug": {"1"}},
		ResBodyHash: resHashB, ResLen: 12,
	})
	if err != nil {
		t.Fatalf("InsertFlow B: %v", err)
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// JSON diff of two real flows.
	resp, err := http.Get(ts.URL + "/api/flows/diff?a=" + itoa(idA) + "&b=" + itoa(idB))
	if err != nil {
		t.Fatalf("GET diff: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff: expected 200, got %d", resp.StatusCode)
	}
	var d flowDiff
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if d.StatusSame || d.StatusA != 200 || d.StatusB != 500 {
		t.Fatalf("status not diffed: %+v", d)
	}
	if d.BodySame {
		t.Fatalf("body should differ: %+v", d)
	}
	if len(d.HeaderDeltas) != 1 || d.HeaderDeltas[0].Name != "X-Debug" {
		t.Fatalf("header delta wrong: %+v", d.HeaderDeltas)
	}

	// format=text returns a readable block.
	tresp, err := http.Get(ts.URL + "/api/flows/diff?a=" + itoa(idA) + "&b=" + itoa(idB) + "&format=text")
	if err != nil {
		t.Fatalf("GET diff text: %v", err)
	}
	defer tresp.Body.Close()
	txt := readAll(tresp.Body)
	if !strings.Contains(txt, "status 200→500") || !strings.Contains(txt, "X-Debug") {
		t.Fatalf("text diff missing content: %q", txt)
	}

	// Missing flow id → 404.
	bad, err := http.Get(ts.URL + "/api/flows/diff?a=" + itoa(idA) + "&b=999999")
	if err != nil {
		t.Fatalf("GET diff missing: %v", err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusNotFound {
		t.Fatalf("missing id: expected 404, got %d", bad.StatusCode)
	}

	// Missing query params → 400.
	noargs, err := http.Get(ts.URL + "/api/flows/diff")
	if err != nil {
		t.Fatalf("GET diff noargs: %v", err)
	}
	noargs.Body.Close()
	if noargs.StatusCode != http.StatusBadRequest {
		t.Fatalf("no params: expected 400, got %d", noargs.StatusCode)
	}
}

// writeBody stores a body via the store's content-addressed writer and returns
// its hash, so diff tests can attach real response bodies to flows.
func writeBody(t *testing.T, s *store.Store, content string) string {
	t.Helper()
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}
