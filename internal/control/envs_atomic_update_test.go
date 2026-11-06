package control

import (
	"fmt"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A rejected variable replacement must not leave the environment renamed: the
// request is answered 400, so nothing it asked for may have been applied.
func TestUpdateEnvironmentRejectedVariablesDoNotApplyName(t *testing.T) {
	f := newCollFixture(t)
	var env struct {
		UID  string `json:"uid"`
		Rev  int64  `json:"rev"`
		Name string `json:"name"`
	}
	f.must("POST", "/api/environments", map[string]any{"name": "dev", "kind": "env"}, asUI, 201, &env)
	if env.UID == "" {
		t.Fatal("no uid")
	}
	vars := make([]store.Variable, 0, maxVariablesBatch+1)
	for i := 0; i <= maxVariablesBatch; i++ {
		vars = append(vars, store.Variable{Key: fmt.Sprintf("k%d", i), Type: store.VarTypeDefault, Enabled: true})
	}
	code, out := f.do("PUT", "/api/environments/"+env.UID, map[string]any{
		"name": "renamed", "rev": env.Rev, "variables": vars,
	}, asUI)
	if code != 400 {
		t.Fatalf("over-limit variables: got %d, want 400: %s", code, out)
	}
	var after struct {
		Name string `json:"name"`
		Rev  int64  `json:"rev"`
	}
	f.must("GET", "/api/environments/"+env.UID, nil, asUI, 200, &after)
	if after.Name != "dev" {
		t.Fatalf("environment was renamed to %q by a rejected request", after.Name)
	}
	if after.Rev != env.Rev {
		t.Fatalf("rev moved from %d to %d by a rejected request", env.Rev, after.Rev)
	}
}
