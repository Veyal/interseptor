package control

import (
	"strings"
	"testing"
)

// Children of role="button" are presentational, so the expandable Activity
// detail must be rendered after its row (found through aria-controls), not
// inside it, or screen readers never reach the expanded text.
func TestUIActivityDetailRendersOutsideButtonRow(t *testing.T) {
	activity := requireUIContracts(t, "js/activity.js",
		"</div>${detail}`",
		"document.getElementById(row.getAttribute('aria-controls')||'')",
	)
	if strings.Contains(activity, "row.querySelector('.act-detail')") {
		t.Error("Activity detail must not be looked up inside the button row")
	}
}
