package control

import (
	"strings"
	"testing"
)

// The resolve preview masks the resolved value. Its other strings, the
// unresolved names and the error text, must not carry a registered secret
// either: a name is operator text and can embed one.
func TestResolvePreviewMasksEveryStringItReturns(t *testing.T) {
	f := newCollFixture(t)
	const canary = "CANARYr2resolvePreview4471"
	collUID := f.importDemo()
	envUID := f.newEnv(canary)

	for _, template := range []string{
		"{{tok}}",                    // the value itself
		"{{pre-{{tok}}}}",            // a nested name built from the secret
		"{{missing-" + canary + "}}", // a name that spells the secret out
	} {
		body := f.must("POST", "/api/variables/resolve",
			map[string]any{"template": template, "collectionUid": collUID, "envUid": envUID}, asUI, 200, nil)
		if strings.Contains(body, canary) {
			t.Errorf("template %q: the preview returned the secret: %s", template, body)
		}
	}
}
