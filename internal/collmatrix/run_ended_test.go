package collmatrix

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collrun"
)

func TestRunEndedWarningForUnapprovedScriptsIsHuman(t *testing.T) {
	rep := &collrun.Report{Status: collrun.StatusNotApproved, StopReason: "scripts are not approved; trust them in the UI"}
	rep.Totals.Quarantined = 3
	got := runEndedWarning("admin", rep)
	if strings.Contains(got, "scripts_not_approved") || !strings.Contains(got, "3 scripts are not approved") || !strings.Contains(got, "without scripts") {
		t.Fatalf("warning = %q", got)
	}
	rep.Totals.Quarantined = 1
	if got := runEndedWarning("admin", rep); !strings.Contains(got, "1 script is not approved") {
		t.Fatalf("singular = %q", got)
	}
	other := &collrun.Report{Status: collrun.StatusAborted, StopReason: "boom"}
	if got := runEndedWarning("admin", other); got != "admin: run ended aborted boom" {
		t.Fatalf("other = %q", got)
	}
}
