package checkscript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const hstsCheck = `
def check(flow):
    if flow.scheme == "https" and not flow.res_header("Strict-Transport-Security"):
        return [finding("medium", "Missing HSTS header", fix="Send Strict-Transport-Security.")]
    return []
`

func httpsFlow() Flow {
	return Flow{
		ID: 7, Method: "GET", Scheme: "https", Host: "app.test", Path: "/login?u=admin",
		Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
		ResBody:    "<html>hi</html>",
	}
}

func TestCompileAndRunFinding(t *testing.T) {
	c, err := Compile("missing-hsts", hstsCheck)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// No HSTS header → one finding, with flow id + target filled in.
	issues, err := c.Run(httpsFlow())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(issues))
	}
	is := issues[0]
	if is.Severity != "Medium" || is.Title != "Missing HSTS header" || is.FlowID != 7 || is.Target != "GET app.test/login?u=admin" {
		t.Fatalf("finding wrong: %+v", is)
	}

	// With HSTS present → no findings.
	f := httpsFlow()
	f.ResHeaders["Strict-Transport-Security"] = []string{"max-age=63072000"}
	if issues, _ := c.Run(f); len(issues) != 0 {
		t.Fatalf("expected 0 findings when HSTS present, got %d", len(issues))
	}
}

func TestHeaderAccessorsAndQueryAndRegex(t *testing.T) {
	src := `
def check(flow):
    out = []
    if flow.req_header("authorization"):  # case-insensitive
        out.append(finding("info", "has auth"))
    if flow.query_param("u") == "admin":
        out.append(finding("low", "admin param"))
    if re_search("[0-9]{3}-[0-9]{2}", flow.res_body):
        out.append(finding("high", "looks like an SSN"))
    return out
`
	c, err := Compile("multi", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	f := httpsFlow()
	f.ReqHeaders = map[string][]string{"Authorization": {"Bearer x"}}
	f.ResBody = "ssn 123-45-6789"
	issues, err := c.Run(f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, is := range issues {
		got[is.Title] = true
	}
	for _, want := range []string{"has auth", "admin param", "looks like an SSN"} {
		if !got[want] {
			t.Fatalf("missing %q; got %v", want, got)
		}
	}
}

func TestCompileRequiresCheckFunction(t *testing.T) {
	if _, err := Compile("nofn", `x = 1`); err == nil || !strings.Contains(err.Error(), "check(flow)") {
		t.Fatalf("expected missing-check error, got %v", err)
	}
}

func TestReturnMustBeFindings(t *testing.T) {
	c, err := Compile("bad", `def check(flow): return 42`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := c.Run(httpsFlow()); err == nil {
		t.Fatal("expected error when check returns a non-list")
	}
}

// Sandbox: the script environment has no file/network/clock builtins, and
// load() is disabled — referencing them must fail to compile.
func TestSandboxNoFileAccess(t *testing.T) {
	if _, err := Compile("evil", `def check(flow): return open("/etc/passwd")`); err == nil {
		t.Fatal("expected `open` to be undefined (no file access)")
	}
	if _, err := Compile("loader", `load("other.star", "x")`+"\ndef check(flow): return []"); err == nil {
		t.Fatal("expected load() to be disabled")
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "good.star"), []byte(hstsCheck), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.star"), []byte(`def check(flow): this is not valid`), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(`ignored`), 0o644) // non-.star ignored

	checks, errs := LoadDir(dir)
	if len(checks) != 1 || checks[0].ID != "good" {
		t.Fatalf("expected 1 compiled check 'good', got %v", checks)
	}
	if _, ok := errs["broken.star"]; !ok {
		t.Fatalf("expected a compile error for broken.star, got %v", errs)
	}

	// A missing directory is not an error (no checks configured).
	if c, e := LoadDir(filepath.Join(dir, "nope")); c != nil || e != nil {
		t.Fatalf("missing dir should yield (nil,nil), got %v %v", c, e)
	}
}

// The example checks we ship (and document as the standard) must always compile.
func TestShippedExamplesCompile(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "checks")
	checks, errs := LoadDir(dir)
	if len(errs) != 0 {
		t.Fatalf("shipped example checks failed to compile: %v", errs)
	}
	if len(checks) < 3 {
		t.Fatalf("expected the shipped examples to load (>=3), got %d", len(checks))
	}
}

// A runaway script is stopped by the step limit rather than hanging.
func TestExecutionIsBounded(t *testing.T) {
	c, err := Compile("loop", `
def check(flow):
    x = 0
    for i in range(1000000000):
        x += i
    return []
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := c.Run(httpsFlow()); err == nil {
		t.Fatal("expected the step limit to abort a runaway loop")
	}
}

// A runaway comprehension at MODULE TOP LEVEL (outside any function) must also
// be bounded. Starlark disallows for/while loops at module scope, but list/dict
// comprehensions are legal there — so Compile itself (which executes the module
// top level via starlark.ExecFile) must set a step limit, not just Run. Without
// the fix this either hangs or allocates unbounded memory; with the fix it must
// fail fast with a clear error.
func TestCompileIsBounded(t *testing.T) {
	const src = `
x = [i * i for i in range(1000000000)]
def check(flow):
    return []
`
	done := make(chan error, 1)
	go func() {
		_, err := Compile("runaway", src)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected Compile to fail on a runaway module-level comprehension")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Compile did not return within 5s — module-level execution is not step-bounded")
	}
}
