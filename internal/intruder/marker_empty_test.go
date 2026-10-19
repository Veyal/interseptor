package intruder

import "testing"

// Regression (round 3): an empty §§ pair is not a fuzz point, so it must not
// count as a position or swallow a payload.
func TestEmptyMarkerPairIsNotAPosition(t *testing.T) {
	tpl := "GET /a?x=§1§&y=§§ HTTP/1.1"
	if got := marker.FindAllString(tpl, -1); len(got) != 1 {
		t.Fatalf("positions = %v, want only §1§", got)
	}
	if got := substitute(tpl, []string{"P"}); got != "GET /a?x=P&y=§§ HTTP/1.1" {
		t.Fatalf("substitute = %q", got)
	}
}
