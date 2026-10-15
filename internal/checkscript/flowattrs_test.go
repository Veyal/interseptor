package checkscript

import (
	"slices"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// sampleFlow is a flow with a repeated query parameter and two cookies.
func sampleFlow() Flow {
	return Flow{
		ID: 7, Method: "GET", Scheme: "https", Host: "example.com", Port: 443,
		Path:   "/search?q=hello+world&q=second&empty=&tag=a%20b",
		Status: 200, Mime: "text/html",
		ReqHeaders: map[string][]string{"Cookie": {"session=abc123; theme=dark; broken"}},
		ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
	}
}

func runCheck(t *testing.T, src string, f Flow) ([]string, error) {
	t.Helper()
	c, _, err := CompileWithLogs("t", src)
	if err != nil {
		return nil, err
	}
	_, logs, err := c.RunWithLogs(f)
	return logs, err
}

// flow.query_params must expose every query parameter, url-decoded, with
// repeated names preserved, and must not blow up on a parameter with no value.
func TestFlowQueryParamsExposesEveryDecodedValue(t *testing.T) {
	logs, err := runCheck(t, `
def check(flow):
    p = flow.query_params
    print("keys=" + ",".join(sorted(p.keys())))
    print("q=" + "|".join(p["q"]))
    print("tag=" + p["tag"][0])
    print("empty=" + str(len(p["empty"])) + ":" + p["empty"][0])
    print("missing=" + str("nope" in p))
    return []
`, sampleFlow())
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	want := []string{
		"keys=empty,q,tag",
		"q=hello world|second",
		"tag=a b",
		"empty=1:",
		"missing=False",
	}
	if !slices.Equal(logs, want) {
		t.Fatalf("print output =\n  %q\nwant\n  %q", logs, want)
	}
}

// flow.cookies must parse the request Cookie header into a name → value dict,
// skipping malformed segments rather than failing the check.
func TestFlowCookiesParsesTheRequestCookieHeader(t *testing.T) {
	logs, err := runCheck(t, `
def check(flow):
    c = flow.cookies
    print("keys=" + ",".join(sorted(c.keys())))
    print("session=" + c["session"])
    print("theme=" + c["theme"])
    print("absent=" + str(c.get("nope", "")))
    return []
`, sampleFlow())
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	want := []string{"keys=session,theme", "session=abc123", "theme=dark", "absent="}
	if !slices.Equal(logs, want) {
		t.Fatalf("print output =\n  %q\nwant\n  %q", logs, want)
	}
}

// Both new attributes must be discoverable with dir() and frozen, so a check
// cannot mutate what it was handed.
func TestFlowExposesNewAttributesAndKeepsThemReadOnly(t *testing.T) {
	var v starlark.HasAttrs = &flowValue{sampleFlow()}
	for _, name := range []string{"query_params", "cookies"} {
		if !slices.Contains(v.AttrNames(), name) {
			t.Errorf("AttrNames() does not list %q: %v", name, v.AttrNames())
		}
		got, err := v.Attr(name)
		if err != nil || got == nil {
			t.Errorf("Attr(%q) = %v, %v", name, got, err)
		}
	}
	if _, err := runCheck(t, `
def check(flow):
    flow.query_params["q"] = ["mutated"]
    return []
`, sampleFlow()); err == nil {
		t.Error("a check mutated flow.query_params; the dict must be frozen")
	}
	if _, err := runCheck(t, `
def check(flow):
    flow.cookies["session"] = "mutated"
    return []
`, sampleFlow()); err == nil {
		t.Error("a check mutated flow.cookies; the dict must be frozen")
	}
}

// A flow with no query string and no Cookie header gets empty dicts, not an
// error — a check should not have to guard every access.
func TestFlowQueryParamsAndCookiesAreEmptyNotMissing(t *testing.T) {
	logs, err := runCheck(t, `
def check(flow):
    print("q=" + str(len(flow.query_params)) + " c=" + str(len(flow.cookies)))
    return []
`, Flow{Method: "GET", Host: "example.com", Path: "/"})
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !slices.Equal(logs, []string{"q=0 c=0"}) {
		t.Fatalf("print output = %q", logs)
	}
}

// print() output must come back from the run, not vanish to stderr.
func TestRunWithLogsCapturesPrintOutput(t *testing.T) {
	logs, err := runCheck(t, `
def check(flow):
    print("one")
    print("two", 3, True)
    return []
`, sampleFlow())
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !slices.Equal(logs, []string{"one", "two 3 True"}) {
		t.Fatalf("print output = %q", logs)
	}
}

// A check that prints at module level — the common "did this file even load"
// debug — must have that output attributed to the compile, not lost.
func TestCompileWithLogsCapturesModuleLevelPrint(t *testing.T) {
	c, logs, err := CompileWithLogs("t", "print(\"loading\")\ndef check(flow):\n    return []\n")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if c == nil {
		t.Fatal("compile returned no check")
	}
	if !slices.Equal(logs, []string{"loading"}) {
		t.Fatalf("compile output = %q", logs)
	}
}

// A runaway print loop must not grow without bound, and the caller must be
// told the output was cut rather than silently shown a partial log.
func TestRunWithLogsBoundsRunawayOutput(t *testing.T) {
	logs, err := runCheck(t, `
def check(flow):
    for i in range(5000):
        print("line " + str(i))
    return []
`, sampleFlow())
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if len(logs) > maxLogLines+1 {
		t.Fatalf("captured %d lines, want at most %d plus a truncation notice", len(logs), maxLogLines)
	}
	last := logs[len(logs)-1]
	if !strings.Contains(last, "truncated") {
		t.Fatalf("last line = %q, want a truncation notice", last)
	}
}

func TestRunWithLogsBoundsOneHugeLine(t *testing.T) {
	logs, err := runCheck(t, `
def check(flow):
    print("x" * 200000)
    return []
`, sampleFlow())
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	total := 0
	for _, l := range logs {
		total += len(l)
	}
	if total > maxLogBytes+512 {
		t.Fatalf("captured %d bytes of print output, cap is %d", total, maxLogBytes)
	}
	if !strings.Contains(logs[len(logs)-1], "truncated") {
		t.Fatalf("no truncation notice: %q", logs[len(logs)-1])
	}
}

// Run keeps working for callers that do not want the output, and a failing
// check still returns whatever it managed to print before the error.
func TestRunStillWorksAndLogsSurviveAFailure(t *testing.T) {
	c, err := Compile("t", "def check(flow):\n    print(\"before\")\n    fail(\"boom\")\n")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := c.Run(sampleFlow()); err == nil {
		t.Fatal("expected the check to fail")
	}
	_, logs, err := c.RunWithLogs(sampleFlow())
	if err == nil {
		t.Fatal("expected the check to fail")
	}
	if !slices.Equal(logs, []string{"before"}) {
		t.Fatalf("logs = %q, want the output from before the failure", logs)
	}
}
