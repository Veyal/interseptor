package collrun

import (
	"encoding/json"
	"strings"
	"testing"
)

func lintFake() *fakeBackend {
	f := newFake("login", "me")
	f.baseVars = map[string]string{"baseUrl": "https://api.example.com"}
	f.items[0].URL = json.RawMessage(`"{{baseUrl}}/login"`)
	f.items[1].URL = json.RawMessage(`"{{baseUrl}}/me/{{userId}}?x={{$guid}}"`)
	return f
}

func findings(rep *LintReport, code string) []LintFinding {
	var out []LintFinding
	for _, f := range rep.Findings {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func TestLintUnresolvedAndDynamic(t *testing.T) {
	rep, err := Lint(lintFake(), "c1", LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	u := findings(rep, LintUnresolved)
	if len(u) != 1 || !strings.Contains(u[0].Message, "{{userId}}") || u[0].Level != LintError || u[0].Path != "me" {
		t.Fatalf("unresolved: %+v", u)
	}
	if rep.ExitCode(false) != ExitImportLint {
		t.Fatalf("exit %d", rep.ExitCode(false))
	}
}

func TestLintDataColumnsLocalAndScriptSetCount(t *testing.T) {
	f := lintFake()
	rep, _ := Lint(f, "c1", LintOptions{DataColumns: []string{"userId"}})
	if len(findings(rep, LintUnresolved)) != 0 || rep.ExitCode(false) != ExitPass {
		t.Fatalf("data column must define the var: %+v", rep.Findings)
	}
	rep, _ = Lint(f, "c1", LintOptions{Local: map[string]string{"userId": "1"}})
	if len(findings(rep, LintUnresolved)) != 0 {
		t.Fatal("local override must define the var")
	}
	f.items[0].Events = json.RawMessage(`[{"listen":"test","script":{"exec":["pm.environment.set('userId', pm.response.json().id);"]}}]`)
	f.trusted[CollectionScripts(f.coll, f.items)[0].Hash] = true
	rep, _ = Lint(f, "c1", LintOptions{})
	if len(findings(rep, LintUnresolved)) != 0 || len(findings(rep, LintScriptSetVar)) != 1 {
		t.Fatalf("a var set by a script is info, not an error: %+v", rep.Findings)
	}
	if rep.Errors != 0 {
		t.Fatalf("errors %d", rep.Errors)
	}
}

func TestLintUnapprovedScriptsAndStrict(t *testing.T) {
	f := lintFake()
	f.items[1].URL = json.RawMessage(`"{{baseUrl}}/me"`)
	f.items[0].Events = json.RawMessage(`[{"listen":"test","script":{"exec":["pm.test('ok', () => {});"]}}]`)
	rep, _ := Lint(f, "c1", LintOptions{})
	u := findings(rep, LintUnapproved)
	if len(u) != 1 || u[0].Level != LintWarn || len(u[0].Hash) != 64 {
		t.Fatalf("unapproved: %+v", u)
	}
	if rep.ExitCode(false) != ExitPass || rep.ExitCode(true) != ExitImportLint {
		t.Fatalf("warnings fail only in strict mode: %d %d", rep.ExitCode(false), rep.ExitCode(true))
	}
	f.trusted[u[0].Hash] = true
	rep, _ = Lint(f, "c1", LintOptions{})
	if len(findings(rep, LintUnapproved)) != 0 {
		t.Fatal("trusted script must not be flagged")
	}
}

func TestLintUnsupportedAPIsAndFlags(t *testing.T) {
	f := lintFake()
	f.items[1].URL = json.RawMessage(`"{{baseUrl}}/me"`)
	f.items[0].Events = json.RawMessage(`[{"listen":"prerequest","script":{"exec":["const _ = require('lodash');","eval('1')"]}}]`)
	rep, _ := Lint(f, "c1", LintOptions{})
	if len(findings(rep, LintUnsupported)) == 0 {
		t.Fatalf("unsupported require not flagged: %+v", rep.Findings)
	}
	if len(findings(rep, LintScriptFlag)) == 0 {
		t.Fatalf("eval not flagged: %+v", rep.Findings)
	}
	if rep.ExitCode(false) != ExitImportLint {
		t.Fatal("unsupported API must fail the gate")
	}
}

func TestLintEmbeddedCredentialsNeverEchoValue(t *testing.T) {
	const secretVal = "hunter2-literal-credential-9876"
	f := lintFake()
	f.items[1].URL = json.RawMessage(`"https://user:` + secretVal + `@api.example.com/me?api_key=` + secretVal + `"`)
	f.items[1].Headers = json.RawMessage(`[{"key":"Authorization","value":"Bearer ` + secretVal + `"},{"key":"X-Api-Key","value":"{{apiKey}}"}]`)
	f.items[0].Auth = json.RawMessage(`{"type":"basic","basic":[{"key":"username","value":"u"},{"key":"password","value":"` + secretVal + `"}]}`)
	f.baseVars["apiKey"] = "k"
	rep, _ := Lint(f, "c1", LintOptions{})
	c := findings(rep, LintCredential)
	if len(c) < 4 {
		t.Fatalf("want header, userinfo, query and auth findings, got %+v", c)
	}
	raw, _ := json.Marshal(rep)
	if strings.Contains(string(raw), secretVal) {
		t.Fatal("lint report echoed a credential value")
	}
	for _, x := range c {
		if x.Level != LintError {
			t.Fatalf("embedded credentials are errors: %+v", x)
		}
	}
}

func TestLintTemplatedCredentialsAreFine(t *testing.T) {
	f := lintFake()
	f.items[1].URL = json.RawMessage(`"{{baseUrl}}/me?token={{token}}"`)
	f.baseVars["token"] = "x"
	f.items[1].Headers = json.RawMessage(`[{"key":"Authorization","value":"Bearer {{token}}"}]`)
	f.items[0].Auth = json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"{{token}}"}]}`)
	f.items[1].URL = json.RawMessage(`"{{baseUrl}}/me?token={{token}}"`)
	f.items[0].URL = json.RawMessage(`"{{baseUrl}}/login"`)
	rep, _ := Lint(f, "c1", LintOptions{})
	if len(findings(rep, LintCredential)) != 0 {
		t.Fatalf("templated credentials flagged: %+v", rep.Findings)
	}
}

func TestLintCollectionAuth(t *testing.T) {
	f := lintFake()
	f.items[1].URL = json.RawMessage(`"{{baseUrl}}/me"`)
	f.coll.Auth = json.RawMessage(`{"type":"bearer","bearer":{"token":"literal-token-value"}}`)
	rep, _ := Lint(f, "c1", LintOptions{})
	if len(findings(rep, LintCredential)) != 1 {
		t.Fatalf("collection auth: %+v", rep.Findings)
	}
}
