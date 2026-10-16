package pmsandbox

import (
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

func TestSmokeVarsAndTests(t *testing.T) {
	in := base(scriptctx.PhaseTest, `
pm.environment.set("token", "abc");
pm.test("status ok", function(){ pm.response.to.have.status(200); });
pm.test("json", () => { pm.expect(pm.response.json().user.id).to.equal(7); });
console.log("hi", {a:1});
`)
	in.Response = jsonResp(200, `{"user":{"id":7}}`)
	out := run(t, in)
	mustPass(t, out)
	if len(out.Tests) != 2 || out.Tests[0].Status != "pass" {
		t.Fatalf("%+v", out.Tests)
	}
	if out.Vars.Environment["token"] != "abc" {
		t.Fatalf("%+v", out.Vars)
	}
	if len(out.Console) != 1 || out.Console[0].Text != `hi {"a":1}` {
		t.Fatalf("%+v", out.Console)
	}
}
