package pmsandbox

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// A denied capability names the grant the user sees in the Scripts sheet
// (vars.write), not the internal field name (varsWrite), and says where to
// grant it.
func TestCapabilityDeniedNamesTheGrantTheUserSees(t *testing.T) {
	cases := []struct{ script, want string }{
		{`pm.variables.set("a", 1)`, "writing variables requires vars.write"},
		{`pm.environment.get("a")`, "reading variables requires vars.read"},
		{`pm.cookies.get("x")`, "requires cookies.read"},
	}
	for _, c := range cases {
		out, _ := adv(t, c.script, func(in *Input) { in.Caps = scriptctx.Caps{} })
		var msgs []string
		for _, e := range out.Errors {
			msgs = append(msgs, e.Message)
		}
		all := strings.Join(msgs, "\n")
		if !strings.Contains(all, c.want) || !strings.Contains(all, "Scripts") {
			t.Errorf("%s: want %q and a pointer to Scripts in %q", c.script, c.want, all)
		}
		if strings.Contains(all, "varsWrite") || strings.Contains(all, "varsRead") || strings.Contains(all, "cookiesRead") {
			t.Errorf("%s: internal capability name leaked: %q", c.script, all)
		}
	}
}
