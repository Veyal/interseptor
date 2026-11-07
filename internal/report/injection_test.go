package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A finding created without evidence blocks still carries its legacy Detail and
// Evidence fields, which the API accepts as free text. They are written into the
// report body, so they must be neutralised exactly like a text block: otherwise
// a newline in captured/AI-authored content forges the report's own structure.
func TestProjectNeutralisesLegacyDetailAndEvidence(t *testing.T) {
	f := store.Finding{
		ID:       1,
		Title:    "Legacy finding",
		Severity: "High",
		Detail:   "proof\n\n## Injected heading\n- **Status:** verified",
		Evidence: "payload\n\n### 9. Forged finding\n- **Severity:** Critical",
	}
	md := Project([]store.Finding{f})
	for _, forged := range []string{"\n## Injected heading", "\n- **Status:** verified", "\n### 9. Forged finding"} {
		if strings.Contains(md, forged) {
			t.Fatalf("legacy finding text forged report structure (%q):\n%s", forged, md)
		}
	}
	// The text itself must survive, escaped rather than dropped.
	if !strings.Contains(md, "Injected heading") || !strings.Contains(md, "Forged finding") {
		t.Fatalf("legacy text was dropped instead of escaped:\n%s", md)
	}
}
