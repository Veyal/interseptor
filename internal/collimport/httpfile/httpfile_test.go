package httpfile

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	exp "github.com/Veyal/interseptor/internal/collexport/postman"
	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func counter() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("id%03d", n) }
}

func parse(t *testing.T, src string) *postman.Result {
	t.Helper()
	res, err := Parse([]byte(src), Options{NewID: counter()})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return res
}

func golden(t *testing.T, name string) *postman.Result {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".http")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(data, Options{NewID: counter()})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.MarshalIndent(res, "", "  ")
	got = append(got, '\n')
	g := "testdata/" + name + ".golden.json"
	if *update {
		if err := os.WriteFile(g, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(g)
	if err != nil {
		t.Fatalf("missing golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s golden mismatch; rerun with -update and review the diff", name)
	}
	return res
}

func TestGoldens(t *testing.T) {
	for _, n := range []string{"vscode", "jetbrains"} {
		t.Run(n, func(t *testing.T) {
			res := golden(t, n)
			if _, err := exp.ExportCollection(res.Bundle(), res.Collection.UID, exp.Options{}); err != nil {
				t.Fatalf("round-trip export: %v", err)
			}
		})
	}
}

func has(r *postman.Result, l impkit.Level, feature string) bool {
	return r.Report.Has(l, feature)
}

func item(t *testing.T, r *postman.Result, i int) store.Item {
	t.Helper()
	if i >= len(r.Items) {
		t.Fatalf("only %d items", len(r.Items))
	}
	return r.Items[i]
}

func bodyOf(it store.Item) struct {
	Mode, Raw string
	Formdata  []map[string]any
	Graphql   struct {
		Query     string
		Variables json.RawMessage
	}
	Options struct{ Raw struct{ Language string } }
} {
	var b struct {
		Mode, Raw string
		Formdata  []map[string]any
		Graphql   struct {
			Query     string
			Variables json.RawMessage
		}
		Options struct{ Raw struct{ Language string } }
	}
	_ = json.Unmarshal(it.Body, &b)
	return b
}

func hdrs(it store.Item) map[string]string {
	var rows []impkit.Row
	_ = json.Unmarshal(it.Headers, &rows)
	m := map[string]string{}
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	return m
}

func TestNaming(t *testing.T) {
	res := parse(t, "### From separator\nGET https://example.com/a\n\n###\nGET https://example.com/b?x=1\n\n### sep\n# @name wins\nGET https://example.com/c\n")
	got := []string{res.Items[0].Name, res.Items[1].Name, res.Items[2].Name}
	want := []string{"From separator", "GET /b", "wins"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
	if res.Report.Stats.Requests != 3 {
		t.Fatalf("stats %+v", res.Report.Stats)
	}
}

func TestNoSeparatorSingleRequest(t *testing.T) {
	res := parse(t, "POST https://example.com/x\nContent-Type: application/json\n\n{\"a\":1}\n")
	it := item(t, res, 0)
	b := bodyOf(it)
	if it.Method != "POST" || b.Mode != "raw" || b.Raw != `{"a":1}` || b.Options.Raw.Language != "json" {
		t.Fatalf("%s %+v", it.Method, b)
	}
	if it.Name != "POST /x" {
		t.Fatalf("name %q", it.Name)
	}
}

func TestBarePathWithHost(t *testing.T) {
	res := parse(t, "GET /health HTTP/1.1\nHost: example.com\nAccept: */*\n")
	it := item(t, res, 0)
	if !strings.Contains(string(it.URL), `"raw":"http://example.com/health"`) {
		t.Fatalf("url %s", it.URL)
	}
	if _, ok := hdrs(it)["Host"]; ok || hdrs(it)["Accept"] != "*/*" {
		t.Fatalf("headers %v", hdrs(it))
	}
	if !has(res, impkit.Degraded, "host-header-folded") {
		t.Fatal("expected host-header-folded")
	}
	res = parse(t, "GET /x\n")
	if !has(res, impkit.NeedsReview, "no-host") {
		t.Fatal("expected no-host")
	}
}

func TestUrlOnlyAndContinuation(t *testing.T) {
	res := parse(t, "https://example.com/a\n  ?x=1\n  &y=2\n")
	it := item(t, res, 0)
	if it.Method != "GET" || !strings.Contains(string(it.URL), `"raw":"https://example.com/a?x=1&y=2"`) && !strings.Contains(string(it.URL), `a?x=1&y=2`) {
		t.Fatalf("%s %s", it.Method, it.URL)
	}
	res = parse(t, "GET https://example.com/a\n  ?x=1\n  HTTP/2\n")
	if strings.Contains(string(item(t, res, 0).URL), "HTTP") {
		t.Fatalf("version kept: %s", item(t, res, 0).URL)
	}
}

func TestCommentsAndHeaders(t *testing.T) {
	res := parse(t, "# comment\n// another\nGET https://example.com/\n# header comment\nX-A: 1\n// x\nX-A: 2\n\nbody\n")
	it := item(t, res, 0)
	var rows []impkit.Row
	_ = json.Unmarshal(it.Headers, &rows)
	if len(rows) != 2 || rows[0].Value != "1" || rows[1].Value != "2" {
		t.Fatalf("headers %v", rows)
	}
}

func TestCRLF(t *testing.T) {
	lf := parse(t, "### A\nPOST https://example.com/a\nContent-Type: text/plain\n\nhi\n\n### B\nGET https://example.com/b\n")
	crlf := parse(t, "### A\r\nPOST https://example.com/a\r\nContent-Type: text/plain\r\n\r\nhi\r\n\r\n### B\r\nGET https://example.com/b\r\n")
	a, _ := json.Marshal(lf.Items)
	b, _ := json.Marshal(crlf.Items)
	if !bytes.Equal(a, b) || strings.ContainsRune(string(b), '\r') {
		t.Fatalf("CRLF differs from LF:\n%s\n%s", a, b)
	}
	bom := parse(t, "\ufeffGET https://example.com/\n")
	if len(bom.Items) != 1 {
		t.Fatal("BOM")
	}
}

func TestVariablesAndSecrets(t *testing.T) {
	res := parse(t, "@host = https://example.com\n@apiToken = canary-secret-xyz\n@host = https://example.org\n\nGET {{host}}/{{id}}\n")
	if len(res.Variables) != 2 {
		t.Fatalf("variables %+v", res.Variables)
	}
	if res.Variables[0].InitialValue != "https://example.org" {
		t.Fatalf("last wins: %+v", res.Variables[0])
	}
	sv := res.Variables[1]
	if sv.Type != store.VarTypeSecret || sv.InitialValue != "" || len(res.SecretValues) != 1 || res.SecretValues[0].Value != "canary-secret-xyz" {
		t.Fatalf("secret %+v %+v", sv, res.SecretValues)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "canary-secret-xyz") {
		t.Fatal("secret value leaked into the result JSON")
	}
	if !has(res, impkit.NeedsReview, "environment-variable") {
		t.Fatal("undefined {{id}} should be reported")
	}
	for _, e := range res.Report.Entries {
		if e.Feature == "environment-variable" && strings.Contains(e.Message, "{{host}}") {
			t.Fatal("defined variable reported as undefined")
		}
	}
}

func TestScriptsInert(t *testing.T) {
	res := parse(t, "< {%\n  request.variables.set('a', 1)\n%}\nPOST https://example.com/\n\n{}\n\n> {% client.global.set('t', response.body.t) %}\n")
	it := item(t, res, 0)
	var evs []struct {
		Listen  string
		Dialect string
		Script  struct{ Exec []string }
	}
	if err := json.Unmarshal(it.Events, &evs); err != nil || len(evs) != 2 {
		t.Fatalf("events %s (%v)", it.Events, err)
	}
	if evs[0].Listen != "prerequest" || evs[1].Listen != "test" || evs[0].Dialect != "jetbrains" || evs[1].Dialect != "jetbrains" {
		t.Fatalf("events %+v", evs)
	}
	if strings.Join(evs[1].Script.Exec, "\n") != "client.global.set('t', response.body.t)" {
		t.Fatalf("exec %v", evs[1].Script.Exec)
	}
	if res.Report.Scripts.Total != 2 || res.Report.Scripts.Unsupported != 2 || res.Report.Scripts.Supported != 0 {
		t.Fatalf("scripts %+v", res.Report.Scripts)
	}
	for _, s := range res.Report.ScriptList {
		if !s.Quarantine || s.Status != "unsupported" {
			t.Fatalf("script row %+v", s)
		}
	}
	if !has(res, impkit.Blocked, "script-quarantined") || !has(res, impkit.PreservedInert, "script-inert") {
		t.Fatal("quarantine entries missing")
	}
	if strings.Contains(string(it.Body), "client.global") {
		t.Fatal("handler leaked into body")
	}
}

func TestFileIncludeNotRead(t *testing.T) {
	dir := t.TempDir()
	secret := dir + "/secret.json"
	if err := os.WriteFile(secret, []byte(`CANARY-FILE-CONTENT`), 0o600); err != nil {
		t.Fatal(err)
	}
	res := parse(t, "POST https://example.com/\nContent-Type: application/json\n\n< "+secret+"\n\n### x\nPOST https://example.com/\n\n<@utf8 ./b.json\n\n###\nGET https://example.com/\n> ./h.js\n")
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "CANARY-FILE-CONTENT") {
		t.Fatal("file was read")
	}
	if bodyOf(item(t, res, 0)).Mode != "file" || bodyOf(item(t, res, 1)).Mode != "file" {
		t.Fatalf("body modes: %s / %s", item(t, res, 0).Body, item(t, res, 1).Body)
	}
	if res.Report.Stats.NeedsAsset != 3 {
		t.Fatalf("needsAsset = %d", res.Report.Stats.NeedsAsset)
	}
	var paths []string
	for _, e := range res.Report.Entries {
		if e.Feature == "needs-asset" {
			paths = append(paths, e.Message)
		}
	}
	joined := strings.Join(paths, "|")
	for _, p := range []string{secret, "./b.json", "./h.js"} {
		if !strings.Contains(joined, p) {
			t.Errorf("path %q not recorded in %q", p, joined)
		}
	}
}

func TestChainingLiteral(t *testing.T) {
	res := parse(t, "###\n# @name login\nPOST https://example.com/login\n\n###\nGET https://example.com/u/{{login.response.body.$.id}}\nX-T: {{login.response.headers.X-Token}}\n")
	if !strings.Contains(string(item(t, res, 1).URL), "{{login.response.body.$.id}}") {
		t.Fatalf("chain rewritten: %s", item(t, res, 1).URL)
	}
	n := 0
	for _, e := range res.Report.Entries {
		if e.Feature == "request-chaining" && e.Level == impkit.NeedsReview {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("chaining entries = %d", n)
	}
	for _, e := range res.Report.Entries {
		if e.Feature == "environment-variable" {
			t.Fatalf("chain reported as env var: %+v", e)
		}
	}
}

func TestDynamicVariables(t *testing.T) {
	res := parse(t, "GET https://example.com/{{$uuid}}/{{$guid}}/{{$random.uuid}}/{{$timestamp}}/{{$randomInt}}/{{$datetime iso8601}}/{{$random.email}}\n")
	u := string(item(t, res, 0).URL)
	if !strings.Contains(u, "{{$guid}}/{{$guid}}/{{$guid}}/{{$timestamp}}/{{$randomInt}}/{{$isoTimestamp}}/{{$randomEmail}}") {
		t.Fatalf("mapping: %s", u)
	}
	res = parse(t, "GET https://example.com/{{$randomInt 1 10}}/{{$processEnv TOKEN}}/{{$random.integer(1,5)}}/{{$timestamp -1 h}}/{{$bogus}}\n")
	for _, f := range []string{"$randomInt", "$processEnv", "$random.integer", "$timestamp", "$bogus"} {
		if !has(res, impkit.NeedsReview, "dynamic-variable:"+f) {
			t.Errorf("missing dynamic-variable:%s in %+v", f, res.Report.Entries)
		}
	}
	if !strings.Contains(string(item(t, res, 0).URL), "{{$processEnv TOKEN}}") {
		t.Fatalf("unsupported dynamic variable should stay literal: %s", item(t, res, 0).URL)
	}
}

func TestMultipart(t *testing.T) {
	res := golden(t, "vscode")
	var up store.Item
	for _, it := range res.Items {
		if it.Name == "Upload" {
			up = it
		}
	}
	b := bodyOf(up)
	if b.Mode != "formdata" || len(b.Formdata) != 2 {
		t.Fatalf("multipart body %s", up.Body)
	}
	if b.Formdata[0]["key"] != "title" || b.Formdata[0]["value"] != "hello" {
		t.Fatalf("part 0 %v", b.Formdata[0])
	}
	if b.Formdata[1]["type"] != "file" || b.Formdata[1]["src"] != "" || b.Formdata[1]["fileName"] != "a.png" {
		t.Fatalf("part 1 %v", b.Formdata[1])
	}
}

func TestGraphQL(t *testing.T) {
	res := golden(t, "jetbrains")
	var g store.Item
	for _, it := range res.Items {
		if it.Name == "GraphQL" {
			g = it
		}
	}
	b := bodyOf(g)
	if b.Mode != "graphql" || !strings.Contains(b.Graphql.Query, "user(id: $id)") || !strings.Contains(string(b.Graphql.Variables), `"id"`) {
		t.Fatalf("graphql body %s", g.Body)
	}
	if _, ok := hdrs(g)["X-REQUEST-TYPE"]; ok {
		t.Fatal("marker header should be consumed")
	}
}

func TestDirectives(t *testing.T) {
	res := parse(t, "# @name n\n# @no-redirect\n# @timeout 5 s\n# @no-log\n# @prompt who\n# @mystery\nGET https://example.com/\n")
	it := item(t, res, 0)
	if !strings.Contains(string(it.Settings), `"followRedirects":false`) || !strings.Contains(string(it.Settings), `"timeoutMs":5000`) {
		t.Fatalf("settings %s", it.Settings)
	}
	for _, f := range []string{"directive:no-log", "directive:prompt", "directive:mystery"} {
		if len(res.Report.Entries) == 0 || !anyFeature(res, f) {
			t.Errorf("missing %s", f)
		}
	}
	res = parse(t, "# @timeout 30\nGET https://example.com/\n")
	if !anyFeature(res, "timeout-unit-assumed") {
		t.Fatal("unit assumption should be reported")
	}
}

func anyFeature(r *postman.Result, f string) bool {
	for _, e := range r.Report.Entries {
		if e.Feature == f {
			return true
		}
	}
	return false
}

func TestUnsupportedAndGarbage(t *testing.T) {
	res := parse(t, "GRAPHQL https://example.com/g\n\n###\nGET https://example.com/ok\n")
	if len(res.Items) != 1 || !anyFeature(res, "request-type:graphql") {
		t.Fatalf("%d items, %+v", len(res.Items), res.Report.Entries)
	}
	for _, in := range []string{"", "just some prose\nwith no request\n", "# only comments\n@a = b\n"} {
		if _, err := Parse([]byte(in), Options{}); err != ErrNotHTTPFile {
			t.Errorf("Parse(%q) err = %v", in, err)
		}
	}
	if _, err := Parse([]byte("GET https://example.com/\x00"), Options{}); err != ErrNotHTTPFile {
		t.Fatalf("binary: %v", err)
	}
}

func TestCredentials(t *testing.T) {
	res := parse(t, "GET https://example.com/\nAuthorization: Bearer literal-canary-token\nX-Other: {{tok}}\n")
	if res.Report.Stats.EmbeddedCredentials != 1 {
		t.Fatalf("stats %+v", res.Report.Stats)
	}
	for _, e := range res.Report.Entries {
		if strings.Contains(e.Message+e.Suggestion, "literal-canary-token") {
			t.Fatal("credential value in report")
		}
	}
}

func TestBounds(t *testing.T) {
	if _, err := Parse(bytes.Repeat([]byte("a"), MaxInputBytes+1), Options{}); err != ErrTooLarge {
		t.Fatalf("oversize: %v", err)
	}
	var sb strings.Builder
	for i := 0; i < MaxRequests+25; i++ {
		sb.WriteString("###\nGET https://example.com/")
		sb.WriteString(fmt.Sprint(i))
		sb.WriteString("\n\n")
	}
	res, err := Parse([]byte(sb.String()), Options{NewID: counter()})
	if err != nil || len(res.Items) != MaxRequests || !res.Report.Has(impkit.Blocked, "too-many-requests") {
		t.Fatalf("request cap: %v %d", err, len(res.Items))
	}
	long := "GET https://example.com/" + strings.Repeat("a", impkit.MaxURLLen+10) + "\n"
	res = parse(t, long)
	if !anyFeature(res, "url-truncated") {
		t.Fatal("URL cap not enforced")
	}
	huge := "POST https://example.com/\n\n" + strings.Repeat("x", MaxBodyBytes+10)
	res = parse(t, huge)
	if !anyFeature(res, "body-truncated") || len(bodyOf(item(t, res, 0)).Raw) > MaxBodyBytes {
		t.Fatal("body cap not enforced")
	}
}

func TestDialect(t *testing.T) {
	cases := map[string]string{
		"@a = b\nGET https://example.com/{{$guid}}\n":                  DialectVSCode,
		"GET https://example.com/\n\n> {% client.log('x') %}\n":        DialectJetBrains,
		"GET https://example.com/\nX-REQUEST-TYPE: GraphQL\n\nquery{}": DialectJetBrains,
		"GET https://example.com/\n":                                   DialectCommon,
	}
	for in, want := range cases {
		if got := DetectDialect(in); got != want {
			t.Errorf("DetectDialect(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestLooks(t *testing.T) {
	yes := []string{
		"### a\nGET https://example.com/\n",
		"@host = x\nGET {{host}}/a\n",
		"GET https://example.com/{{id}}\n",
		"# @name n\nPOST https://example.com/\n",
		"POST https://example.com/\n\n{}\n\n> {% client.log(1) %}\n",
	}
	no := []string{
		"",
		"GET /path HTTP/1.1\nHost: example.com\n\n",            // raw HTTP request
		"HTTP/1.1 200 OK\nContent-Type: text/plain\n\n{{x}}\n", // raw response
		"### Usage\n\n```\nGET https://example.com/\n```\n",    // markdown
		"### Notes\nnothing to see here\n",                     // separator but no request
		"{\"openapi\":\"3.0.0\"}",                              // JSON
		"curl https://example.com\n",                           // curl
		"GET https://example.com/\x00{{x}}",                    // binary
	}
	for _, s := range yes {
		if !Looks([]byte(s)) {
			t.Errorf("Looks(%q) = false", s)
		}
	}
	for _, s := range no {
		if Looks([]byte(s)) {
			t.Errorf("Looks(%q) = true", s)
		}
	}
	for _, n := range []string{"vscode", "jetbrains"} {
		d, _ := os.ReadFile("testdata/" + n + ".http")
		if !Looks(d) {
			t.Errorf("fixture %s not detected", n)
		}
	}
}

func FuzzParse(f *testing.F) {
	d, _ := os.ReadFile("testdata/vscode.http")
	f.Add(string(d))
	d, _ = os.ReadFile("testdata/jetbrains.http")
	f.Add(string(d))
	f.Add("> {%\n")
	f.Add("< {%\nGET x")
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = Parse([]byte(s), Options{NewID: counter()})
		_ = Looks([]byte(s))
	})
}
