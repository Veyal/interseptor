package pmsandbox

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

func apisOf(r Report) []string {
	var out []string
	for _, f := range r.Unsupported {
		out = append(out, f.API)
	}
	return out
}

func TestAnalyzeFlagsUnsupportedBeforeRun(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"lodash require", `const _ = require("lodash"); _.map([1],x=>x)`, []string{`require("lodash")`, "_"}},
		{"moment global", `moment().format()`, []string{"moment"}},
		{"forbidden fs", `require('fs').readFileSync("/etc/passwd")`, []string{`require("fs")`}},
		{"node: prefix", `require("node:child_process")`, []string{`require("child_process")`}},
		{"dynamic require", `const n = "fs"; require(n)`, []string{"require(<dynamic>)"}},
		{"pm.vault", `pm.vault.get("x")`, []string{"pm.vault"}},
		{"pm.require", `pm.require("npm:lodash")`, []string{"pm.require"}},
		{"jsonSchema", `pm.response.to.have.jsonSchema({})`, []string{"pm.response.to.have.jsonSchema"}},
		{"unknown pm member", `pm.environment.frobnicate("x")`, []string{"pm.environment.frobnicate"}},
		{"unknown pm root", `pm.nothing.here()`, []string{"pm.nothing"}},
		{"chai unknown", `pm.expect(1).to.be.frozenish`, []string{"chai .frozenish"}},
		{"chai unknown after call", `pm.expect(a).to.equal(1).and.wobble()`, []string{"chai .wobble"}},
		{"fetch", `fetch("https://example.com")`, []string{"fetch"}},
		{"process", `process.env.X`, []string{"process"}},
		{"response chain unknown", `pm.response.to.have.bananas`, []string{"pm.response.to.bananas"}},
		{"iterationData write", `pm.iterationData.set("a", 1)`, []string{"pm.iterationData.set"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Analyze(c.src)
			if !r.HasUnsupported() {
				t.Fatalf("not flagged: %+v", r)
			}
			got := apisOf(r)
			for _, w := range c.want {
				if !contains(got, w) {
					t.Fatalf("missing %q in %v", w, got)
				}
			}
		})
	}
}

func TestAnalyzeCleanScriptsAreNotFlagged(t *testing.T) {
	srcs := []string{
		`pm.environment.set("a", 1); pm.test("t", () => { pm.expect(pm.response.json().a).to.equal(1).and.be.a("number"); pm.response.to.have.status(200); });`,
		`const CryptoJS = require("crypto-js"); const crypto = require('crypto'); pm.request.headers.upsert({key:"a",value:"b"}); pm.request.url.addQueryParams("a=1");`,
		`// pm.vault.get("in a comment"); "fetch( in a string"
		 const re = /pm\.vault\.get\(/; pm.variables.replaceIn("{{a}}"); postman.setNextRequest(null); tests["x"] = true; pm.sendRequest("https://api.example.com", ()=>{});`,
		"const s = `fetch(${1}) _.map`; pm.cookies.jar().set('https://a.example.com','n','v',()=>{});",
		`pm.expect(1).to.be.within(0, 2).and.not.equal(5); pm.expect(a).to.have.all.keys("a"); pm.expect(x).to.be.an("array").that.is.not.empty;`,
		`isp.assertScope("https://example.com"); isp.codec.hexEncode("a"); pm.execution.setNextRequest("n"); pm.info.iteration; pm.iterationData.get("a");`,
	}
	for i, s := range srcs {
		if r := Analyze(s); r.HasUnsupported() {
			t.Fatalf("script %d falsely flagged: %+v", i, r.Unsupported)
		}
	}
}

func TestAnalyzeReportsAPIsModulesHostsFlags(t *testing.T) {
	r := Analyze(`
const crypto = require("crypto");
pm.sendRequest("https://auth.example.com/token", () => {});
pm.sendRequest({url: "http://internal.example.net:8080/x"}, () => {});
pm.environment.set("a", 1);
eval("1+1");
const f = new Function("return 1");
while (true) { break }
for (;;) { break }
pm.visualizer.set("<p/>");
`)
	if !contains(r.Modules, "crypto") {
		t.Fatalf("modules %v", r.Modules)
	}
	if !contains(r.APIs, "pm.environment.set") {
		t.Fatalf("apis %v", r.APIs)
	}
	hosts := ""
	for _, h := range r.Hosts {
		hosts += h.Host + " "
	}
	if !strings.Contains(hosts, "auth.example.com") || !strings.Contains(hosts, "internal.example.net:8080") {
		t.Fatalf("hosts %q", hosts)
	}
	names := map[string]bool{}
	for _, f := range r.Flags {
		names[f.Name] = true
	}
	for _, w := range []string{"eval", "function-constructor", "infinite-loop"} {
		if !names[w] {
			t.Fatalf("missing flag %s in %+v", w, r.Flags)
		}
	}
	if r.HasUnsupported() {
		t.Fatalf("a stub must not count as unsupported: %+v", r.Unsupported)
	}
	found := false
	for _, f := range r.Unsupported {
		found = found || (f.API == "pm.visualizer" && f.Kind == "stub")
	}
	if !found {
		t.Fatalf("visualizer stub not reported: %+v", r.Unsupported)
	}
}

func TestAnalyzeObfuscationFlag(t *testing.T) {
	blob := strings.Repeat("QUJD", 200)
	r := Analyze(`var p = "` + blob + `"; String.fromCharCode(1,2,3)`)
	got := false
	for _, f := range r.Flags {
		got = got || f.Name == "obfuscation"
	}
	if !got {
		t.Fatalf("%+v", r.Flags)
	}
}

// Every corpus case the runtime reports as unsupported must also be flagged
// statically, and every passing case must not be.
func TestAnalyzerAgreesWithRuntimeOnCorpus(t *testing.T) {
	for _, c := range loadCorpus(t) {
		r := Analyze(c.Script)
		if c.Expect.Status == StatusUnsupported && !r.HasUnsupported() {
			t.Errorf("%q: runtime says unsupported but analyser did not flag: %+v", c.Name, r)
		}
		if c.Expect.Status == StatusPass && r.HasUnsupported() {
			t.Errorf("%q: analyser flags a script that passes: %+v", c.Name, r.Unsupported)
		}
	}
}

func TestRuntimeUnsupportedMatchesStaticAnalysis(t *testing.T) {
	src := `pm.environment.frobnicate("x")`
	in := base(scriptctx.PhaseTest, src)
	in.Response = jsonResp(200, "{}")
	out := run(t, in)
	if out.Status != StatusUnsupported || !contains(out.Unsupported, "pm.environment.frobnicate") {
		t.Fatalf("%s %v %+v", out.Status, out.Unsupported, out.Errors)
	}
	if !contains(apisOf(Analyze(src)), "pm.environment.frobnicate") {
		t.Fatal("static analysis disagrees")
	}
}
