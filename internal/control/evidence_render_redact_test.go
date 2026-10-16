package control

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/preview"
	"github.com/Veyal/interseptor/internal/store"
)

func TestRedactEvidenceTextTable(t *testing.T) {
	cases := []struct{ name, in, secret string }{
		{"camelCase accessToken", `{"accessToken":"abc123def456"}`, "abc123def456"},
		{"camelCase csrfToken", `{"csrfToken":"Zx9Qw81"}`, "Zx9Qw81"},
		{"otp", "otp=482913", "482913"},
		{"reset_code", "reset_code=48291", "48291"},
		{"query code", "/reset?code=482913", "482913"},
		{"short password", "password=Summer2024!", "Summer2024!"},
		{"bearer", "Authorization: Bearer abcdefghijklmnop", "abcdefghijklmnop"},
		{"set-cookie", "Set-Cookie: sid=abcd1234; Path=/", "abcd1234"},
	}
	for _, c := range cases {
		if got := redactEvidenceText(c.in); strings.Contains(got, c.secret) {
			t.Errorf("%s leaked in %q", c.name, got)
		}
	}
	if got := redactEvidenceText("GET /api/orders?page=2 HTTP/1.1"); got != "GET /api/orders?page=2 HTTP/1.1" {
		t.Fatalf("ordinary evidence altered: %q", got)
	}
}

func under(t *testing.T, name string, max time.Duration, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	if d := time.Since(start); d > max {
		t.Fatalf("%s took %v, want <= %v", name, d, max)
	}
}

// renderBudget guards against super-linear blowups on hostile input (the
// unbounded versions took many seconds); it is deliberately loose so a loaded
// CI runner under -race does not flake (a 523ms run failed a 500ms bound).
const renderBudget = 2 * time.Second

func TestEvidenceAdaptersBoundHostileInput(t *testing.T) {
	big := strings.Repeat("a", 64<<10)
	fa := &store.Flow{ID: 1, Method: "GET", Scheme: "https", Host: "example.com", Path: "/" + big, Status: 200}
	fb := &store.Flow{ID: 2, Method: "GET", Scheme: "https", Host: "example.com", Path: "/b", Status: 200}
	d := flowDiff{
		HeaderDeltas: []headerDelta{{Name: "X-Big", A: big, B: big + "b", Kind: "changed"}},
		BodyDeltas:   []bodyLineDelta{{Line: 1, A: big, B: big + "x"}},
		Summary:      big,
	}
	under(t, "flow diff with 64KB inputs", renderBudget, func() {
		in := flowDiffInput(fa, fb, d, true)
		if len(in.A.URL) > 2048 || len(in.HeaderDeltas[0].A) > 1024 || len(in.Summary) > 2048 {
			t.Fatalf("inputs not bounded: url=%d hdr=%d sum=%d", len(in.A.URL), len(in.HeaderDeltas[0].A), len(in.Summary))
		}
		if _, err := preview.RenderFlowDiff(in, preview.Opts{}); err != nil {
			t.Fatal(err)
		}
	})

	huge := strings.Repeat("t", 1<<20)
	rec := syntheticRun(testRunID)
	rec.State.TargetHost = huge
	rec.Spec.Target = huge
	rec.Spec.GrepMatch = big
	rec.State.Attack = huge
	rec.State.Results[0].Payload = big
	rec.State.Results[1].Extracted = big
	env, _ := newIntruderEnvelope(rec)
	env.RunID = huge
	under(t, "intruder timeline with 1MiB target", renderBudget, func() {
		if _, err := preview.RenderIntruderTimeline(intruderTimelineInput(env), preview.Opts{}); err != nil {
			t.Fatal(err)
		}
	})
	under(t, "intruder race/strip with 64KB values", renderBudget, func() {
		if _, err := preview.RenderIntruderRace(intruderRaceInput(env, true, 0), preview.Opts{}); err != nil {
			t.Fatal(err)
		}
		if _, err := preview.RenderIntruderStrip(intruderStripInput(env, true), preview.Opts{}); err != nil {
			t.Fatal(err)
		}
	})

	fake := fakeFinder{1: {ID: 1, Title: big, Severity: "High"}}
	under(t, "chain with 64KB unbroken title", renderBudget, func() {
		in, err := chainInputFrom(fake, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(in.Nodes[0].Title) > 1024 {
			t.Fatalf("title not bounded: %d", len(in.Nodes[0].Title))
		}
		if _, err := preview.RenderFindingChain(in, preview.Opts{}); err != nil {
			t.Fatal(err)
		}
	})

	runs := []authzRunOut{{Method: "GET", Path: "/" + big, Results: []authzResult{{Name: big, Status: 200, Length: 10}}}}
	under(t, "authz with 64KB label and identity", renderBudget, func() {
		if _, err := preview.RenderAuthzMatrix(authzMatrixInput("r", runs), preview.Opts{}); err != nil {
			t.Fatal(err)
		}
	})
	_ = fmt.Sprint
	_ = intruder.Result{}
}
