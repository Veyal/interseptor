package activescan

import "testing"

func TestMergeChecksOverridesBuiltin(t *testing.T) {
	builtin := []Check{{ID: "a", Title: "go"}, {ID: "b", Title: "go2"}}
	custom := []Check{{ID: "a", Title: "starlark"}}
	got := mergeChecks(builtin, custom)
	if len(got) != 2 || got[0].Title != "starlark" || got[1].ID != "b" {
		t.Fatalf("merge: %+v", got)
	}
}
