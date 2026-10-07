package pmsandbox

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/scriptctx"
)

// A script holding only vars.read must not be able to reach a secret value
// through any path, including the scope's internal storage.
func TestSecretNeverEntersHeapWithoutSecretsRead(t *testing.T) {
	in := base(scriptctx.PhasePreRequest, `
		const d = pm.environment.__d;
		pm.variables.set('rev', d ? String(d.tok).split('').reverse().join('') : 'none');
	`)
	in.Caps = scriptctx.Caps{VarsRead: true, VarsWrite: true}
	in.Vars = scriptctx.Vars{
		Environment: map[string]any{"tok": "SECRETVALUE123", "plain": "ok"},
		Secret:      []string{"tok"},
	}
	out := run(t, in)
	rev := "321EULAVTERCES"
	for _, ch := range out.Changes {
		if s, _ := ch.Value.(string); strings.Contains(s, "SECRETVALUE123") || strings.Contains(s, rev) {
			t.Fatalf("secret leaked via change: %+v", ch)
		}
	}
	for _, l := range out.Console {
		if strings.Contains(l.Text, "SECRETVALUE123") {
			t.Fatalf("secret leaked to console: %q", l.Text)
		}
	}
}

func TestSecretReadableWithSecretsRead(t *testing.T) {
	in := base(scriptctx.PhasePreRequest, `pm.variables.set('copy', pm.environment.get('tok'))`)
	in.Vars = scriptctx.Vars{Environment: map[string]any{"tok": "SECRETVALUE123"}, Secret: []string{"tok"}}
	out := run(t, in)
	found := false
	for _, ch := range out.Changes {
		if ch.Name == "copy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected copy change with secrets.read: %+v", out.Changes)
	}
}
