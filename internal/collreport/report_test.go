package collreport

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/redact"
)

// canary is a secret value only a registry can know (no credential shape).
const canary = "CANARY-s3kret-value-Q7x9ZZ41"

func sampleReport() *collrun.Report {
	r := &collrun.Report{
		RunUID: "RUN1", CollectionUID: "c1", CollectionName: "Demo API", EnvName: "staging", Source: "cli",
		Status: collrun.StatusDone, StartedMs: 1_700_000_000_000, FinishedMs: 1_700_000_001_500, Iterations: 2,
		Data:    &collrun.DataInfo{Format: "csv", Rows: 2, Hash: strings.Repeat("ab", 32)},
		Persist: collrun.PersistInfo{Mode: "discard", Pending: []collrun.VarChangeView{{Scope: "environment", Key: "token", Display: "[secret]", Secret: true}}},
		Items: []collrun.ItemResult{
			{Seq: 1, Iteration: 0, ItemUID: "i1", Name: "login", Path: "Auth", Method: "POST", URL: "https://api.example.com/login?api_key=" + canary,
				Outcome: collexec.OutcomeSent, HTTPStatus: 200, StatusText: "OK", DurationMs: 88, FlowID: 812,
				Tests: []collexec.TestResult{
					{Name: "status 200", Status: collexec.TestPass},
					{Name: "body has token", Status: collexec.TestFail, Message: "expected " + canary + " got nothing\nsecond line", Expected: "x", Actual: canary, Source: "3:5"},
					{Name: "uses lodash", Status: collexec.TestUnsupported, Message: "unsupported API: require(\"lodash\")"},
					{Name: "boom", Status: collexec.TestError, Message: "TypeError: x is undefined"},
					{Name: "maybe", Status: collexec.TestSkip},
				},
				Console: []collexec.ConsoleLine{{Owner: "request", Level: "log", Text: "secret is " + canary + " ctl:\x01\x02"}}},
			{Seq: 2, Iteration: 0, ItemUID: "i2", Name: `<script>alert(1)</script>`, Method: "GET", URL: "https://api.example.com/x",
				Outcome: collexec.OutcomeBlocked, BlockReason: collexec.BlockScope, Error: "host cdn.example.net is out of scope"},
			{Seq: 3, Iteration: 0, ItemUID: "i3", Name: "dial", Method: "GET", Outcome: collexec.OutcomeError, Error: "dial tcp 192.0.2.1:443: i/o timeout"},
			{Seq: 4, Iteration: 0, ItemUID: "i4", Name: "skipped", Method: "GET", Outcome: collexec.OutcomeSkipped},
			{Seq: 5, Iteration: 1, ItemUID: "i5", Name: "notests", Method: "GET", URL: "https://api.example.com/n", Outcome: collexec.OutcomeSent, HTTPStatus: 204, DurationMs: 3},
		},
	}
	for _, it := range r.Items {
		r.Totals.Requests++
		_ = it
	}
	r.Totals = collrun.Totals{Requests: 5, Sent: 2, Skipped: 1, Blocked: 1, Errors: 1, Pass: 1, Fail: 1, TestError: 1, Unsupported: 1, TestSkip: 1, ScopeBlocks: 1}
	return r
}

func registryScrub() func(string) string {
	reg := redact.NewRegistry()
	reg.Add(canary)
	return func(s string) string { return redact.Text(reg.Mask(s)) }
}

func render(t *testing.T, format string) string {
	t.Helper()
	b, err := Render(format, sampleReport(), Options{Scrub: registryScrub()})
	if err != nil {
		t.Fatalf("%s: %v", format, err)
	}
	return string(b)
}

func TestNoFormatLeaksSecrets(t *testing.T) {
	for _, f := range Formats() {
		out := render(t, f)
		if strings.Contains(out, canary) {
			t.Errorf("%s report leaked the canary secret", f)
		}
		if strings.Contains(out, "api_key="+canary) {
			t.Errorf("%s report leaked the api_key query value", f)
		}
	}
}

func TestDefaultScrubMasksCredentialShapes(t *testing.T) {
	rep := sampleReport()
	rep.Items[0].URL = "https://api.example.com/login?access_token=abcdef1234567890abcdef"
	rep.Items[0].Error = "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig12345"
	b, err := Render(FormatJSON, rep, Options{}) // default scrubber, no registry
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "abcdef1234567890abcdef") || strings.Contains(string(b), "eyJhbGci") {
		t.Fatalf("credential-shaped text survived the default scrub:\n%s", b)
	}
}

func TestScrubDoesNotMutateInput(t *testing.T) {
	rep := sampleReport()
	if _, err := Render(FormatJSON, rep, Options{Scrub: registryScrub()}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.Items[0].URL, canary) {
		t.Fatal("Render must scrub a copy, not the caller's report")
	}
}

func TestJSONEnvelope(t *testing.T) {
	var env struct {
		Schema   string         `json:"schema"`
		ExitCode int            `json:"exitCode"`
		Report   collrun.Report `json:"report"`
	}
	if err := json.Unmarshal([]byte(render(t, FormatJSON)), &env); err != nil {
		t.Fatal(err)
	}
	if env.Schema != SchemaVersion || env.ExitCode != collrun.ExitScope || len(env.Report.Items) != 5 || env.Report.Totals.Fail != 1 {
		t.Fatalf("%+v", env)
	}
}

// ---- JUnit ----

type xCase struct {
	Name      string `xml:"name,attr"`
	Classname string `xml:"classname,attr"`
	Time      string `xml:"time,attr"`
	Skipped   []struct {
		Message string `xml:"message,attr"`
	} `xml:"skipped"`
	Errors []struct {
		Type string `xml:"type,attr"`
	} `xml:"error"`
	Failures []struct {
		Type string `xml:"type,attr"`
	} `xml:"failure"`
}

type xSuite struct {
	Name     string  `xml:"name,attr"`
	Tests    int     `xml:"tests,attr"`
	Failures int     `xml:"failures,attr"`
	Errors   int     `xml:"errors,attr"`
	Skipped  int     `xml:"skipped,attr"`
	Cases    []xCase `xml:"testcase"`
	Out      string  `xml:"system-out"`
}

type xSuites struct {
	XMLName  xml.Name `xml:"testsuites"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Errors   int      `xml:"errors,attr"`
	Skipped  int      `xml:"skipped,attr"`
	Suites   []xSuite `xml:"testsuite"`
}

func TestJUnitCountsAndMapping(t *testing.T) {
	out := render(t, FormatJUnit)
	var doc xSuites
	if err := xml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not well-formed XML: %v\n%s", err, out)
	}
	if len(doc.Suites) != 5 {
		t.Fatalf("one suite per executed request, got %d", len(doc.Suites))
	}
	var tests, fails, errs, skips int
	for _, s := range doc.Suites {
		if s.Tests != len(s.Cases) {
			t.Errorf("suite %q: tests=%d but %d cases", s.Name, s.Tests, len(s.Cases))
		}
		var f, e, k int
		for _, c := range s.Cases {
			f += len(c.Failures)
			e += len(c.Errors)
			k += len(c.Skipped)
		}
		if f != s.Failures || e != s.Errors || k != s.Skipped {
			t.Errorf("suite %q counters %d/%d/%d != elements %d/%d/%d", s.Name, s.Failures, s.Errors, s.Skipped, f, e, k)
		}
		tests, fails, errs, skips = tests+s.Tests, fails+s.Failures, errs+s.Errors, skips+s.Skipped
	}
	if doc.Tests != tests || doc.Failures != fails || doc.Errors != errs || doc.Skipped != skips {
		t.Fatalf("root counters %d/%d/%d/%d != sums %d/%d/%d/%d", doc.Tests, doc.Failures, doc.Errors, doc.Skipped, tests, fails, errs, skips)
	}
	login := doc.Suites[0]
	types := map[string]string{}
	for _, c := range login.Cases {
		for _, e := range c.Errors {
			types[c.Name] = "error:" + e.Type
		}
		for _, f := range c.Failures {
			types[c.Name] = "failure:" + f.Type
		}
		if len(c.Skipped) > 0 {
			types[c.Name] = "skipped"
		}
	}
	if types["body has token"] != "failure:AssertionError" || types["uses lodash"] != "error:UnsupportedAPI" ||
		types["boom"] != "error:ScriptError" || types["maybe"] != "skipped" || types["status 200"] != "" {
		t.Fatalf("status mapping wrong: %v", types)
	}
	if login.Classname() != "Auth.login" {
		t.Fatalf("classname %q", login.Classname())
	}
	if blocked := doc.Suites[1].Cases[0].Errors[0].Type; blocked != "Blocked:out_of_scope" {
		t.Fatalf("blocked type %q", blocked)
	}
	if doc.Suites[2].Cases[0].Errors[0].Type != "RequestError" || len(doc.Suites[3].Cases[0].Skipped) != 1 {
		t.Fatal("error/skip request cases wrong")
	}
	if len(doc.Suites[4].Cases) != 1 || !strings.Contains(doc.Suites[4].Cases[0].Name, "204") {
		t.Fatalf("a sent request without tests gets one passing case: %+v", doc.Suites[4].Cases)
	}
	if strings.Contains(out, "\x01") || strings.Contains(login.Out, "\x02") {
		t.Fatal("control characters must be stripped from XML")
	}
	if !strings.Contains(doc.Suites[4].Name, "iteration 2") {
		t.Fatalf("multi-iteration suites name the iteration: %q", doc.Suites[4].Name)
	}
}

func (s xSuite) Classname() string {
	if len(s.Cases) == 0 {
		return ""
	}
	return s.Cases[0].Classname
}

func TestJUnitRunFailureSuites(t *testing.T) {
	rep := sampleReport()
	rep.Status, rep.StopReason = collrun.StatusNotApproved, "scripts are not approved"
	rep.Items = nil
	b, err := Render(FormatJUnit, rep, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var doc xSuites
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Errors != 1 || len(doc.Suites) != 1 || doc.Suites[0].Cases[0].Errors[0].Type != "RunError" {
		t.Fatalf("a run that never started must still be visible to CI as an error: %s", b)
	}
}

func TestJUnitValidatesAgainstSchema(t *testing.T) {
	path, err := exec.LookPath("xmllint")
	if err != nil {
		t.Skip("xmllint not installed; structural JUnit checks still run in TestJUnitCountsAndMapping")
	}
	xsd, _ := filepath.Abs(filepath.Join("testdata", "junit-10.xsd"))
	dir := t.TempDir()
	cases := map[string]*collrun.Report{"mixed": sampleReport(), "empty": {CollectionName: "Empty", Status: collrun.StatusDone, Items: []collrun.ItemResult{}}}
	notApproved := sampleReport()
	notApproved.Status, notApproved.StopReason, notApproved.Items = collrun.StatusNotApproved, "no", nil
	cases["notapproved"] = notApproved
	for name, rep := range cases {
		b, err := Render(FormatJUnit, rep, Options{Scrub: registryScrub()})
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, name+".xml")
		if err := os.WriteFile(f, b, 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(path, "--noout", "--schema", xsd, f).CombinedOutput()
		if err != nil {
			t.Errorf("%s: xmllint rejected the JUnit output: %v\n%s\n%s", name, err, out, b)
		}
	}
	// The schema must actually reject a broken document (negative control).
	bad := filepath.Join(dir, "bad.xml")
	_ = os.WriteFile(bad, []byte(`<testsuites><testsuite name="x" tests="many"/></testsuites>`), 0o600)
	if err := exec.Command(path, "--noout", "--schema", xsd, bad).Run(); err == nil {
		t.Fatal("xsd accepted a non-numeric tests attribute: schema check is not strict")
	}
}

// ---- HTML and text ----

func TestHTMLIsSelfContainedAndEscaped(t *testing.T) {
	out := render(t, FormatHTML)
	if strings.Contains(out, "<script>alert(1)</script>") || !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("item names must be HTML-escaped")
	}
	for _, bad := range []string{"<script", "src=", "href=", "@import", "url(", "<link", "<iframe"} {
		if strings.Contains(out, bad) {
			t.Errorf("self-contained report must not contain %q", bad)
		}
	}
	for _, want := range []string{"Demo API", "staging", "exit code 4", "prefers-color-scheme", "FAIL", "BLOCKED"} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q", want)
		}
	}
}

func TestTextSummary(t *testing.T) {
	out := render(t, FormatCLI)
	for _, want := range []string{"Collection: Demo API", "[FAIL] POST Auth / login -> 200", "[BLOCK]", "[ERROR]", "[SKIP]", "Iteration 2/2", "Exit code: 4", "Tests: 1 passed, 1 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q\n%s", want, out)
		}
	}
}

func TestTextListsQuarantinedHashes(t *testing.T) {
	rep := &collrun.Report{CollectionName: "X", Status: collrun.StatusNotApproved, Items: []collrun.ItemResult{},
		Quarantined: []collrun.QuarantinedScript{{Owner: "request", Name: "login", Listen: "test", Hash: strings.Repeat("c", 64)}}}
	b, _ := Render(FormatCLI, rep, Options{Scrub: func(s string) string { return s }})
	if !strings.Contains(string(b), strings.Repeat("c", 64)) || !strings.Contains(string(b), "Exit code: 3") {
		t.Fatalf("hash must be shown so it can be pinned:\n%s", b)
	}
}

func TestUnknownFormat(t *testing.T) {
	if _, err := Render("pdf", sampleReport(), Options{}); err == nil {
		t.Fatal("unknown format must fail")
	}
	var buf bytes.Buffer
	if err := Write(&buf, "XML", sampleReport(), Options{}); err != nil {
		t.Fatalf("xml alias: %v", err)
	}
}
