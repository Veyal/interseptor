package control

import (
	"strings"
	"testing"
)

// Regression: in edit mode below about 1000px of container width, opening the
// Properties rail collapsed its grid row to 2px and the rail painted over the
// document, because overflow:hidden makes the rail a scroll container whose
// automatic minimum height is 0. overflow:clip clips without that side effect.
func TestUIFindingsRailClipsWithoutCollapsingItsRow(t *testing.T) {
	css := readUIAsset(t, "findings.css")
	i := strings.Index(css, ".find-rail{")
	if i < 0 {
		t.Fatal("findings.css lost the .find-rail rule")
	}
	rule := css[i : i+strings.Index(css[i:], "}")]
	if !strings.Contains(rule, "overflow:clip") || strings.Contains(rule, "overflow:hidden") {
		t.Fatalf(".find-rail must use overflow:clip, got %q", rule)
	}
}
