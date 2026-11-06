package plugin

import "testing"

// Hooks are called from the capture hot path, where the contract is
// best-effort ("never block forwarding"). A panicking extension hook used to
// escape EmitFlowCaptured/EmitScanIssue and take the whole proxy process (and
// every in-flight capture) down with it, and it also skipped the hooks
// registered after it. One bad hook must not do either.
func TestFlowHookPanicIsContained(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	OnFlowCaptured(func(int64) { panic("buggy extension hook") })
	var ran int
	OnFlowCaptured(func(int64) { ran++ })

	EmitFlowCaptured(7) // must not panic

	if ran != 1 {
		t.Fatalf("hooks after the panicking one ran %d time(s), want 1", ran)
	}
}

func TestScanIssueHookPanicIsContained(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	OnScanIssue(func(int64, string, string) { panic("buggy extension hook") })
	var ran int
	OnScanIssue(func(int64, string, string) { ran++ })

	EmitScanIssue(1, "High", "Example") // must not panic

	if ran != 1 {
		t.Fatalf("hooks after the panicking one ran %d time(s), want 1", ran)
	}
}
