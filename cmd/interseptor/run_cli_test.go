package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

const cliCanary = "CANARY-cli-secret-W3v8QQ52-nope"

type cliProject struct {
	t    *testing.T
	dir  string
	st   *store.Store
	srv  *httptest.Server
	coll *store.Collection
	env  *store.Environment
	host string
	seen []string
}

func cliJS(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func cliEvent(listen string, lines ...string) json.RawMessage {
	return cliJS([]any{map[string]any{"listen": listen, "script": map[string]any{"exec": lines}}})
}

// newCLIProject seeds a project directory with a collection and a local server.
func newCLIProject(t *testing.T) *cliProject {
	t.Helper()
	p := &cliProject{t: t, dir: t.TempDir()}
	st, err := store.Open(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	p.st = st
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.seen = append(p.seen, r.URL.RequestURI())
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true,"token":"`+cliCanary+`"}`)
		case "/bad":
			w.WriteHeader(500)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(p.srv.Close)
	p.host = strings.TrimPrefix(p.srv.URL, "http://")
	p.host = p.host[:strings.LastIndex(p.host, ":")]
	p.coll, err = st.CreateCollection(store.Collection{Name: "CLI API", Caps: cliJS([]string{"vars.read", "vars.write", "secrets.read"})})
	if err != nil {
		t.Fatal(err)
	}
	p.env, err = st.CreateEnvironment(store.Environment{Name: "local", Kind: "env"})
	if err != nil {
		t.Fatal(err)
	}
	p.vars(map[string]string{"baseUrl": p.srv.URL}, nil)
	return p
}

func (p *cliProject) vars(plain map[string]string, secret map[string]string) {
	var vs []store.Variable
	for k, v := range plain {
		vs = append(vs, store.Variable{OwnerKind: store.VarOwnerEnvironment, OwnerUID: p.env.UID, Key: k, Type: store.VarTypeDefault, InitialValue: v, Enabled: true})
	}
	for k := range secret {
		vs = append(vs, store.Variable{OwnerKind: store.VarOwnerEnvironment, OwnerUID: p.env.UID, Key: k, Type: store.VarTypeSecret, Enabled: true})
	}
	if err := p.st.SetVariables(store.VarOwnerEnvironment, p.env.UID, vs); err != nil {
		p.t.Fatal(err)
	}
	for k, v := range secret {
		if err := p.st.SetCurrentValue(store.VarOwnerEnvironment, p.env.UID, k, v, "test"); err != nil {
			p.t.Fatal(err)
		}
	}
}

func (p *cliProject) add(name, path string, mod func(*store.Item)) *store.Item {
	it := store.Item{CollectionUID: p.coll.UID, Kind: "request", Name: name, Method: "GET", URL: cliJS("{{baseUrl}}" + path)}
	if mod != nil {
		mod(&it)
	}
	out, err := p.st.CreateItem(it)
	if err != nil {
		p.t.Fatal(err)
	}
	return out
}

// cli runs the command with the project closed (SQLite is single-writer here).
func (p *cliProject) cli(cmd string, args ...string) (int, string, string) {
	p.t.Helper()
	if p.st != nil {
		p.st.Close()
		p.st = nil
	}
	var out, errb bytes.Buffer
	full := append([]string{"--data-dir", p.dir}, args...)
	code := runCollectionCLI(context.Background(), cmd, full, &out, &errb)
	st, err := store.Open(p.dir)
	if err != nil {
		p.t.Fatal(err)
	}
	p.st = st
	return code, out.String(), errb.String()
}

func (p *cliProject) scriptHashes() []string {
	var out []string
	items, _ := p.st.ListItems(p.coll.UID)
	co, _ := p.st.GetCollection(p.coll.UID)
	for _, s := range collrun.CollectionScripts(*co, items) {
		out = append(out, s.Hash)
	}
	return out
}

func TestCLIExitCodePass(t *testing.T) {
	p := newCLIProject(t)
	p.add("ok", "/ok", nil)
	code, out, errs := p.cli("run", "CLI API", "-e", "local", "--scope", p.host)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	for _, want := range []string{"Collection: CLI API", "[PASS]", "Exit code: 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestCLIExitCodeTestFailure(t *testing.T) {
	p := newCLIProject(t)
	p.add("bad", "/bad", func(it *store.Item) {
		it.Assertions = cliJS([]map[string]any{{"id": "a1", "type": "status", "op": "eq", "value": 200}})
	})
	code, out, _ := p.cli("run", p.coll.UID, "-e", "local", "--scope", p.host)
	if code != collrun.ExitTestFail {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
}

func TestCLIExitCodeRuntimeUnresolved(t *testing.T) {
	p := newCLIProject(t)
	p.add("unresolved", "/ok?x={{missing}}", nil)
	code, out, _ := p.cli("run", "CLI API", "-e", "local", "--scope", p.host)
	if code != collrun.ExitRuntime || len(p.seen) != 0 {
		t.Fatalf("exit %d seen %v\n%s", code, p.seen, out)
	}
}

func TestCLIExitCodeScriptsNotApproved(t *testing.T) {
	p := newCLIProject(t)
	p.add("scripted", "/ok", func(it *store.Item) {
		it.Events = cliEvent("test", "pm.test('x', () => pm.expect(1).to.eql(1));")
	})
	code, out, _ := p.cli("run", "CLI API", "-e", "local", "--scope", p.host)
	if code != collrun.ExitNotApproved || len(p.seen) != 0 {
		t.Fatalf("exit %d, sent %v\n%s", code, p.seen, out)
	}
	if !strings.Contains(out, p.scriptHashes()[0]) {
		t.Fatalf("the hash to pin must be printed:\n%s", out)
	}
	// --no-scripts runs the requests without them.
	code, _, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--no-scripts")
	if code != 0 {
		t.Fatalf("--no-scripts exit %d", code)
	}
	// Pinning the exact hash allows them for this process only.
	code, out, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--allow-scripts", "--trust-hash", p.scriptHashes()[0])
	if code != 0 || !strings.Contains(out, "ok    x") {
		t.Fatalf("pinned run exit %d\n%s", code, out)
	}
	if ok, _ := p.st.IsScriptTrusted(p.coll.UID, p.scriptHashes()[0]); ok {
		t.Fatal("a --trust-hash pin must never be written to the store")
	}
	// A wrong pin does not approve anything.
	code, _, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--allow-scripts", "--trust-hash", strings.Repeat("0", 64))
	if code != collrun.ExitNotApproved {
		t.Fatalf("wrong pin exit %d", code)
	}
}

func TestCLIExitCodeScopeRefusedAndBlocked(t *testing.T) {
	p := newCLIProject(t)
	p.add("ok", "/ok", nil)
	code, _, errs := p.cli("run", "CLI API", "-e", "local")
	if code != collrun.ExitScope || !strings.Contains(errs, "no scope is declared") || len(p.seen) != 0 {
		t.Fatalf("no declared scope must refuse to send: exit %d %q seen %v", code, errs, p.seen)
	}
	// An item pointing outside the declared scope is blocked, not sent.
	p.add("outside", "/x", func(it *store.Item) { it.URL = cliJS("http://other.example.net/x") })
	code, out, _ := p.cli("run", "CLI API", "-e", "local", "--scope", p.host)
	if code != collrun.ExitScope || !strings.Contains(out, "[BLOCK]") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestCLIUsageErrorsExitFive(t *testing.T) {
	p := newCLIProject(t)
	p.add("ok", "/ok", nil)
	for name, args := range map[string][]string{
		"no collection":    {},
		"unknown flag":     {"CLI API", "--nope"},
		"missing coll":     {"Nonexistent", "--scope", p.host},
		"bad env":          {"CLI API", "-e", "ghost", "--scope", p.host},
		"bad report":       {"CLI API", "--report", "pdf=x"},
		"allow w/o pin":    {"CLI API", "--allow-scripts"},
		"pin w/o allow":    {"CLI API", "--trust-hash", "abc"},
		"bad env-var":      {"CLI API", "--env-var", "novalue"},
		"bad data":         {"CLI API", "--data", filepath.Join(p.dir, "missing.csv")},
		"bad delay":        {"CLI API", "--delay-request", "soon"},
		"bad bail":         {"CLI API", "--bail=sometimes"},
		"missing project":  {"CLI API", "--project", "nope-project"},
		"no-scripts+allow": {"CLI API", "--no-scripts", "--allow-scripts", "--trust-hash", "a"},
		"two collections":  {"CLI API", "Other"},
	} {
		code, _, _ := p.cli("run", args...)
		if code != collrun.ExitImportLint {
			t.Errorf("%s: exit %d, want 5", name, code)
		}
	}
}

func TestCLIReportsJUnitJSONHTML(t *testing.T) {
	p := newCLIProject(t)
	p.add("ok", "/ok", nil)
	p.add("bad", "/bad", func(it *store.Item) {
		it.Assertions = cliJS([]map[string]any{{"id": "a1", "type": "status", "op": "eq", "value": 200}})
	})
	out := t.TempDir()
	code, stdout, errs := p.cli("run", "CLI API", "-e", "local", "--scope", p.host,
		"--report", "junit=ci.xml,json=ci.json,html", "--out", out)
	if code != collrun.ExitTestFail {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, errs)
	}
	if stdout != "" {
		t.Fatalf("with --report and no cli entry nothing goes to stdout: %q", stdout)
	}
	junit, err := os.ReadFile(filepath.Join(out, "ci.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		XMLName xml.Name `xml:"testsuites"`
		Tests   int      `xml:"tests,attr"`
		Fail    int      `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(junit, &doc); err != nil || doc.Tests < 2 || doc.Fail != 1 {
		t.Fatalf("junit: %v tests=%d failures=%d\n%s", err, doc.Tests, doc.Fail, junit)
	}
	var env struct {
		Schema   string `json:"schema"`
		ExitCode int    `json:"exitCode"`
	}
	raw, _ := os.ReadFile(filepath.Join(out, "ci.json"))
	if json.Unmarshal(raw, &env) != nil || env.ExitCode != 1 || env.Schema == "" {
		t.Fatalf("json report: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(out, "report.html")); err != nil {
		t.Fatalf("html default name: %v", err)
	}
}

func TestCLIDataFileAndIterations(t *testing.T) {
	p := newCLIProject(t)
	p.add("user", "/ok?u={{user}}", nil)
	csv := filepath.Join(t.TempDir(), "users.csv")
	if err := os.WriteFile(csv, []byte("user\nalice@example.com\nbob@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--data", csv)
	if code != 0 || len(p.seen) != 2 || !strings.Contains(p.seen[0], "alice") || !strings.Contains(p.seen[1], "bob") {
		t.Fatalf("exit %d seen %v\n%s", code, p.seen, out)
	}
	p.seen = nil
	code, _, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--data", csv, "-n", "3")
	if code != 0 || len(p.seen) != 3 {
		t.Fatalf("-n 3 sent %d", len(p.seen))
	}
	p.seen = nil
	code, _, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--env-var", "user=cli@example.com")
	if code != 0 || len(p.seen) != 1 || !strings.Contains(p.seen[0], "cli@example.com") {
		t.Fatalf("--env-var: %v", p.seen)
	}
}

func TestCLIFolderAndBail(t *testing.T) {
	p := newCLIProject(t)
	f, err := p.st.CreateItem(store.Item{CollectionUID: p.coll.UID, Kind: "folder", Name: "Group"})
	if err != nil {
		t.Fatal(err)
	}
	p.add("first", "/bad", func(it *store.Item) {
		it.ParentUID = f.UID
		it.Assertions = cliJS([]map[string]any{{"id": "a1", "type": "status", "op": "eq", "value": 200}})
	})
	p.add("second", "/ok", func(it *store.Item) { it.ParentUID = f.UID })
	p.add("outside", "/ok?outside=1", nil)
	code, _, _ := p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--folder", "group", "--bail")
	if code != collrun.ExitTestFail || len(p.seen) != 1 {
		t.Fatalf("bail: exit %d seen %v", code, p.seen)
	}
	p.seen = nil
	code, _, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--folder", "group")
	if len(p.seen) != 2 {
		t.Fatalf("folder run sent %v (code %d)", p.seen, code)
	}
}

func TestCLIPersistDiscardDefaultAndKeep(t *testing.T) {
	p := newCLIProject(t)
	p.add("login", "/ok", func(it *store.Item) {
		it.Events = cliEvent("test", "pm.environment.set('session', pm.response.json().ok ? 'sess-123456' : '');")
	})
	pin := p.scriptHashes()[0]
	code, _, errs := p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--allow-scripts", "--trust-hash", pin)
	if code != 0 {
		t.Fatalf("exit %d %s", code, errs)
	}
	cur, _ := p.st.ListCurrentValues(store.VarOwnerEnvironment, p.env.UID)
	if len(cur) != 0 {
		t.Fatalf("default persist must discard, stored %+v", cur)
	}
	code, _, _ = p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--allow-scripts", "--trust-hash", pin, "--persist")
	cur, _ = p.st.ListCurrentValues(store.VarOwnerEnvironment, p.env.UID)
	if code != 0 || len(cur) != 1 || cur[0].Key != "session" || cur[0].Value != "sess-123456" {
		t.Fatalf("--persist must store the write: exit %d %+v", code, cur)
	}
}

func TestCLISecretsNeverInReportFiles(t *testing.T) {
	p := newCLIProject(t)
	p.vars(map[string]string{"baseUrl": p.srv.URL}, map[string]string{"apiSecret": cliCanary})
	p.add("leaky", "/ok?k={{apiSecret}}", func(it *store.Item) {
		it.Headers = cliJS([]map[string]any{{"key": "X-Api-Key", "value": "{{apiSecret}}"}})
		it.Events = cliEvent("test",
			"console.log('token ' + pm.environment.get('apiSecret'));",
			"pm.test('t ' + pm.environment.get('apiSecret'), () => pm.expect(1).to.eql(2));")
	})
	pin := p.scriptHashes()[0]
	out := t.TempDir()
	code, stdout, errs := p.cli("run", "CLI API", "-e", "local", "--scope", p.host, "--allow-scripts", "--trust-hash", pin,
		"--report", "cli,junit=r.xml,json=r.json,html=r.html", "--out", out)
	if code != collrun.ExitTestFail {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, errs)
	}
	if strings.Contains(stdout, cliCanary) || strings.Contains(errs, cliCanary) {
		t.Fatal("canary leaked to the terminal")
	}
	for _, name := range []string{"r.xml", "r.json", "r.html"} {
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), cliCanary) {
			t.Errorf("canary leaked into %s", name)
		}
	}
	rows, _ := p.st.ListRuns(p.coll.UID, 1)
	res, _ := p.st.ListRunResults(rows[0].UID)
	for _, r := range res {
		if strings.Contains(r.ResultJSON, cliCanary) {
			t.Error("canary leaked into ix_run_results")
		}
	}
}

func TestCLIRunIsRecordedInIxRuns(t *testing.T) {
	p := newCLIProject(t)
	p.add("ok", "/ok", nil)
	if code, _, _ := p.cli("run", "CLI API", "-e", "local", "--scope", p.host); code != 0 {
		t.Fatal("run failed")
	}
	rows, err := p.st.ListRuns(p.coll.UID, 5)
	if err != nil || len(rows) != 1 || rows[0].Source != "cli" || rows[0].Status != collrun.StatusDone || rows[0].EnvUID != p.env.UID {
		t.Fatalf("runs %+v err %v", rows, err)
	}
	flows, _ := p.st.ListRunResults(rows[0].UID)
	if len(flows) != 1 || flows[0].FlowID == 0 {
		t.Fatalf("result rows %+v", flows)
	}
	ctx, ok, _ := p.st.GetFlowCtx(flows[0].FlowID)
	if !ok || ctx.RunID != rows[0].UID {
		t.Fatalf("flow ctx %+v ok=%v", ctx, ok)
	}
}

func TestCLILint(t *testing.T) {
	p := newCLIProject(t)
	p.add("ok", "/ok", nil)
	code, out, _ := p.cli("lint", "CLI API", "-e", "local")
	if code != 0 || !strings.Contains(out, "0 errors") {
		t.Fatalf("clean lint exit %d\n%s", code, out)
	}
	p.add("bad", "/ok?x={{ghost}}", func(it *store.Item) {
		it.Headers = cliJS([]map[string]any{{"key": "Authorization", "value": "Bearer " + cliCanary}})
		it.Events = cliEvent("test", "pm.test('x', () => {});")
	})
	code, out, _ = p.cli("lint", "CLI API", "-e", "local")
	if code != collrun.ExitImportLint {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"unresolved_variable", "{{ghost}}", "embedded_credential", "script_unapproved", "hash "} {
		if !strings.Contains(out, want) {
			t.Errorf("lint output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, cliCanary) {
		t.Fatal("lint echoed a literal credential")
	}
	code, out, _ = p.cli("lint", "CLI API", "-e", "local", "--json")
	var rep collrun.LintReport
	if code != collrun.ExitImportLint || json.Unmarshal([]byte(out), &rep) != nil || rep.Errors < 2 {
		t.Fatalf("json lint: %d %s", code, out)
	}
}

func TestCLIHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCollectionCLI(context.Background(), "run", []string{"--help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "Exit codes") {
		t.Fatalf("help: %d %q", code, out.String())
	}
}

func TestParseReportSpecs(t *testing.T) {
	got, err := parseReportSpecs("cli,junit=out.xml,json,html=/abs/r.html", "outdir")
	if err != nil || len(got) != 4 {
		t.Fatalf("%v %+v", err, got)
	}
	if got[0].path != "" || got[1].path != filepath.Join("outdir", "out.xml") || got[2].path != filepath.Join("outdir", "report.json") || got[3].path != "/abs/r.html" {
		t.Fatalf("%+v", got)
	}
	if d, _ := parseReportSpecs("", "."); len(d) != 1 || d[0].format != "cli" {
		t.Fatal("default is the cli summary")
	}
	if _, err := parseReportSpecs("tap=x", "."); err == nil {
		t.Fatal("unknown format must fail")
	}
}
