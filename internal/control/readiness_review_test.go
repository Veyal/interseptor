package control

import (
	"strings"
	"testing"
)

// The nudge truncated the id list to three plus an ellipsis and then counted the
// truncated list, so seven matching playbooks were reported as "4 playbook(s)".
func TestPlaybookNudgeCountsEveryMatch(t *testing.T) {
	if got := playbookNudgeText(nil); got != "" {
		t.Fatalf("no matches must produce no nudge, got %q", got)
	}
	two := playbookNudgeText([]string{"a", "b"})
	if !strings.HasPrefix(two, "2 playbook(s) apply") || !strings.Contains(two, "(a, b)") {
		t.Fatalf("two matches: %q", two)
	}
	ids := []string{"a", "b", "c", "d", "e", "f", "g"}
	seven := playbookNudgeText(ids)
	if !strings.HasPrefix(seven, "7 playbook(s) apply") {
		t.Fatalf("seven matches must be counted as 7: %q", seven)
	}
	if !strings.Contains(seven, "(a, b, c, …)") || strings.Contains(seven, "d") && strings.Contains(seven, ", d") {
		t.Fatalf("the list names the first three and elides the rest: %q", seven)
	}
	if len(ids) != 7 || ids[3] != "d" {
		t.Fatalf("the caller's slice was modified: %v", ids)
	}
}
