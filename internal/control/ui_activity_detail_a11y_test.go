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

// QA round 4: a failed row that names a flow was a pure jump row, so its failure
// reason lived only in a tooltip; and at ~900px the fixed desktop columns left the
// result ~6 characters wide.
func TestUIActivityFailedRowsExpandAndNarrowLayoutKeepsResultReadable(t *testing.T) {
	js := readUIAsset(t, "js/activity.js")
	requireUIContains(t, js, "const expandable=!fid||!it.ok;", "act-open-flow")
	css := readUIAsset(t, "css/desktop/panel-activity.css")
	requireUIContains(t, css, "@media (max-width:1180px)", ".act-res{grid-column:2 / -1;grid-row:2;white-space:normal")
}
