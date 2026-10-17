package pmsandbox

import (
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// A trusted script that never touches variables must run without vars.read,
// and a write-only grant must allow pm.environment.set. The legacy sandbox
// globals (environment, globals, data) used to be built with a read at
// startup, so every script without vars.read failed before its first line.
func TestScriptsRunWithoutVarsRead(t *testing.T) {
	in := base(scriptctx.PhaseTest, `pm.test("status", () => pm.expect(pm.response.code).to.equal(200));`)
	in.Response = jsonResp(200, "{}")
	in.Caps = scriptctx.Caps{}
	mustPass(t, run(t, in))

	in = base(scriptctx.PhaseTest, `pm.environment.set("last", "x1"); pm.test("ok", () => {});`)
	in.Response = jsonResp(200, "{}")
	in.Caps = scriptctx.Caps{VarsWrite: true}
	out := run(t, in)
	mustPass(t, out)
	if len(out.Changes) != 1 {
		t.Fatalf("write-only grant should record the set, got %+v", out.Changes)
	}

	// The legacy globals still refuse to read without the grant.
	in = base(scriptctx.PhaseTest, `pm.test("legacy", () => pm.expect(environment.tok).to.equal("v"));`)
	in.Response = jsonResp(200, "{}")
	in.Caps = scriptctx.Caps{}
	in.Vars.Environment = map[string]any{"tok": "v"}
	if out := run(t, in); out.Status == StatusPass {
		t.Fatal("reading the legacy environment global without vars.read must not pass")
	}
}
