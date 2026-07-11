package control

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/mcp"
)

func TestToolSpecsFromMCPIncludesTriageTools(t *testing.T) {
	srv := mcp.New("http://127.0.0.1:1")
	specs := toolSpecsFromMCP(srv, findingsTriageToolNames)
	if len(specs) != len(findingsTriageToolNames) {
		t.Fatalf("got %d specs, want %d", len(specs), len(findingsTriageToolNames))
	}
	for i, name := range findingsTriageToolNames {
		if specs[i].Name != name {
			t.Fatalf("spec[%d]=%q, want %q", i, specs[i].Name, name)
		}
		if specs[i].Description == "" {
			t.Fatalf("%s: empty description", name)
		}
		if specs[i].Schema == nil {
			t.Fatalf("%s: nil schema", name)
		}
	}
}

func TestFindingsTriageSystemForbidsActiveAttacks(t *testing.T) {
	for _, want := range []string{
		"Do NOT run active attacks",
		"create_finding",
		"add_finding_poc",
		"needs_verification",
		"filed:",
		"skipped:",
	} {
		if !strings.Contains(findingsTriageSystem, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
}

func TestTruncateReportRaw(t *testing.T) {
	if got := truncateReportRaw("hi", 10); got != "hi" {
		t.Fatalf("short: %q", got)
	}
	long := strings.Repeat("a", 20)
	got := truncateReportRaw(long, 10)
	if !strings.HasPrefix(got, "aaaaaaaaaa") || !strings.Contains(got, "[truncated]") {
		t.Fatalf("truncate wrong: %q", got)
	}
}
