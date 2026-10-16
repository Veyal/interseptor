package pmsandbox

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// shimKeys introspects the real prelude: for every object path in
// pm_coverage.json it lists the member names the running shim exposes.
func shimKeys(t *testing.T, paths []string) map[string][]string {
	t.Helper()
	pj, _ := json.Marshal(paths)
	script := `
var paths = ` + string(pj) + `;
var out = {};
paths.forEach(function (p) {
  var o = (function () { try { return p.split('.').reduce(function (a, k) { return a == null ? a : a[k]; }, globalThis); } catch (e) { return null; } })();
  if (o == null || (typeof o !== 'object' && typeof o !== 'function')) { out[p] = null; return; }
  var names = {};
  var cur = o;
  while (cur && cur !== Object.prototype && cur !== Function.prototype) {
    Object.getOwnPropertyNames(cur).forEach(function (n) { names[n] = true; });
    cur = Object.getPrototypeOf(cur);
  }
  out[p] = Object.keys(names).sort();
});
console.log("SHIMKEYS" + JSON.stringify(out));
`
	in := base(scriptctx.PhaseTest, script)
	in.Response = jsonResp(200, `{"a":1}`)
	out := run(t, in)
	for _, c := range out.Console {
		if strings.HasPrefix(c.Text, "SHIMKEYS") {
			var m map[string][]string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(c.Text, "SHIMKEYS")), &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
	}
	t.Fatalf("introspection script did not report: status=%s errors=%+v console=%+v", out.Status, out.Errors, out.Console)
	return nil
}

// shimExtras are members the prelude defines that are deliberately not listed
// as parity surface (internal plumbing or inherited helpers). Anything else the
// shim exposes must be in pm_coverage.json, or the analyser would call a
// working API unsupported.
var shimExtras = map[string]bool{
	"constructor": true, "length": true, "name": true, "prototype": true, "caller": true, "arguments": true,
	"toString": true, "valueOf": true, "hasOwnProperty": true, "isPrototypeOf": true,
	"propertyIsEnumerable": true, "toLocaleString": true, "__proto__": true,
}

// pm_coverage.json is tested against the actual shim: every member it claims
// exists at run time (a stale claim would be a false "supported"), and every
// public member the shim exposes is claimed (a missing one would be a false
// "unsupported" in the import report and the analyser).
func TestCoverageMembersMatchTheRuntimeShim(t *testing.T) {
	cov := LoadCoverage()
	var paths []string
	for p, o := range cov.Objects {
		if len(o.Members) > 0 {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	got := shimKeys(t, paths)
	for _, p := range paths {
		keys := got[p]
		if keys == nil {
			t.Errorf("%s: object is not reachable in the runtime (coverage lists %d members)", p, len(cov.Objects[p].Members))
			continue
		}
		have := map[string]bool{}
		for _, k := range keys {
			have[k] = true
		}
		listed := map[string]bool{}
		for _, m := range cov.Objects[p].Members {
			listed[m] = true
			if !have[m] {
				t.Errorf("%s.%s is listed in pm_coverage.json but the shim does not define it", p, m)
			}
		}
		for _, k := range keys {
			// Members the coverage file declares unsupported (for example
			// pm.response.stream) may exist in the shim as honest stubs.
			if _, declared := cov.Unsupported[p+"."+k]; declared {
				continue
			}
			if !listed[k] && !shimExtras[k] && !strings.HasPrefix(k, "_") {
				t.Errorf("%s.%s exists in the shim but is missing from pm_coverage.json", p, k)
			}
		}
	}
}

// Every supported global in pm_coverage.json is really defined.
func TestCoverageGlobalsExistInTheRuntime(t *testing.T) {
	cov := LoadCoverage()
	gj, _ := json.Marshal(cov.Globals.Supported)
	script := `var missing = []; ` + string(gj) + `.forEach(function (g) { if (typeof globalThis[g] === 'undefined') missing.push(g); });
console.log("MISSING" + JSON.stringify(missing));`
	in := base(scriptctx.PhaseTest, script)
	in.Response = jsonResp(200, "{}")
	out := run(t, in)
	for _, c := range out.Console {
		if strings.HasPrefix(c.Text, "MISSING") {
			if rest := strings.TrimPrefix(c.Text, "MISSING"); rest != "[]" {
				t.Fatalf("globals listed as supported but undefined in the runtime: %s", rest)
			}
			return
		}
	}
	t.Fatalf("no report: %s %+v", out.Status, out.Errors)
}

// The assertion vocabulary (chain words and chai methods) matches the shim's
// pm.expect() object in both directions.
func TestCoverageChaiVocabularyMatchesTheRuntimeShim(t *testing.T) {
	script := `
var a = pm.expect(1);
var names = {};
var cur = a;
while (cur && cur !== Object.prototype) { Object.getOwnPropertyNames(cur).forEach(function (n) { names[n] = true; }); cur = Object.getPrototypeOf(cur); }
console.log("CHAI" + JSON.stringify(Object.keys(names).sort()));`
	in := base(scriptctx.PhaseTest, script)
	in.Response = jsonResp(200, "{}")
	out := run(t, in)
	var keys []string
	for _, c := range out.Console {
		if strings.HasPrefix(c.Text, "CHAI") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(c.Text, "CHAI")), &keys); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(keys) == 0 {
		t.Fatalf("no chai introspection: %s %+v", out.Status, out.Errors)
	}
	cov := LoadCoverage()
	listed := map[string]bool{}
	for _, w := range append(append([]string{}, cov.ChainWords...), cov.Chai...) {
		listed[w] = true
	}
	have := map[string]bool{}
	for _, k := range keys {
		have[k] = true
		if !listed[k] && !shimExtras[k] && !strings.HasPrefix(k, "_") {
			t.Errorf("pm.expect().%s exists in the shim but is missing from pm_coverage.json", k)
		}
	}
	for w := range listed {
		if !have[w] {
			t.Errorf("pm.expect().%s is listed in pm_coverage.json but the shim does not define it", w)
		}
	}
}
