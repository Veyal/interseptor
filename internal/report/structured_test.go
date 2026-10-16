package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func structuredFixture() []store.Finding {
	return []store.Finding{
		{ID: 30, Severity: "High", Status: "open", Title: "Staging token accepted", Target: "GET /a"},
		{ID: 40, Severity: "Critical", Status: "open", Title: "Privileged token accepted",
			Claims: []store.FindingClaim{
				{ID: "c1", Statement: "Fabricated timestamps accepted", Verdict: "confirmed", Evidence: []store.FindingEvidenceReference{{FlowID: 7}}},
				{ID: "c2", Statement: "KYC approval obtained", Verdict: "refuted", Note: "profile stayed empty"},
			},
			NotExecuted:     []store.FindingNotExecuted{{Method: "POST", Target: "https://example.com/register", Reason: "would provision an account", Risk: "creates a real account", RequiresAuthorisation: true}},
			RelatedFindings: []store.FindingRelation{{ID: 30, Relation: "escalates"}},
		},
	}
}

func TestProjectRendersClaimsWithWithdrawnMarker(t *testing.T) {
	out := Project(structuredFixture(), nil)
	for _, want := range []string{"**Claims:**", "Fabricated timestamps accepted", "CONFIRMED", "WITHDRAWN (refuted)", "profile stayed empty", "flow #7"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "**Withdrawn claims:** c2") {
		t.Fatalf("withdrawn claims not in metadata:\n%s", out)
	}
}

func TestProjectRendersNotExecutedSection(t *testing.T) {
	out := Project(structuredFixture(), nil)
	i := strings.Index(out, "## Requests Deliberately Not Executed")
	if i < 0 {
		t.Fatalf("no dedicated section:\n%s", out)
	}
	section := out[i:]
	for _, want := range []string{"`POST https://example.com/register`", "would provision an account", "creates a real account", "authorisation required", "#40"} {
		if !strings.Contains(section, want) {
			t.Fatalf("section missing %q:\n%s", want, section)
		}
	}
}

func TestProjectRendersRelationsSymmetricallyAndChain(t *testing.T) {
	out := Project(structuredFixture(), nil)
	// 40 escalates 30, so 30 shows the inverse and both appear in the chain section.
	if !strings.Contains(out, "**Related findings:**") || !strings.Contains(out, "escalates") || !strings.Contains(out, "#30") || !strings.Contains(out, "#40") {
		t.Fatalf("relations missing:\n%s", out)
	}
	if strings.Count(out, "**Related findings:**") != 2 {
		t.Fatalf("link should display on both findings:\n%s", out)
	}
	if !strings.Contains(out, "## Finding Chains") || !strings.Contains(out, "#40 Privileged token accepted **escalates** #30 Staging token accepted") {
		t.Fatalf("chain section missing:\n%s", out)
	}
}

func TestProjectEnabledByIsDisplayedAsEnablesFromOtherEnd(t *testing.T) {
	fs := []store.Finding{
		{ID: 1, Severity: "High", Title: "A"},
		{ID: 2, Severity: "High", Title: "B", RelatedFindings: []store.FindingRelation{{ID: 1, Relation: "enabled_by"}}},
	}
	out := Project(fs, nil)
	if !strings.Contains(out, "#1 A **enables** #2 B") {
		t.Fatalf("enabled_by not normalised into a directed chain:\n%s", out)
	}
}

func TestProjectHTMLCarriesStructuredSections(t *testing.T) {
	out := ProjectHTML(structuredFixture(), nil)
	for _, want := range []string{"Finding Chains", "Requests Deliberately Not Executed", "WITHDRAWN (refuted)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("html missing %q", want)
		}
	}
}

func TestAuditTrailAppendixOmitsSnapshotsAndEscapes(t *testing.T) {
	revs := map[int64][]store.FindingRevision{
		5: {{ID: 2, FindingID: 5, TS: 1700000000000, FindingChange: store.FindingChange{Actor: "reviewer", Source: "api", Reason: "Rescored\n# injected"}, Action: "update", Fields: []string{"cvss", "claims"}, Snapshot: &store.Finding{Title: "SECRET-SNAPSHOT"}}},
	}
	out := AuditTrail([]store.Finding{{ID: 5, Title: "T"}}, revs)
	if !strings.Contains(out, "## Appendix: Audit Trail") || !strings.Contains(out, "reviewer") || !strings.Contains(out, "cvss, claims") {
		t.Fatalf("audit trail incomplete:\n%s", out)
	}
	if strings.Contains(out, "SECRET-SNAPSHOT") || strings.Contains(out, "\n# injected") {
		t.Fatalf("audit trail leaked snapshot or unescaped reason:\n%s", out)
	}
}
