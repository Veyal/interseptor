package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func brandingFixture() store.ReportBranding {
	return store.ReportBranding{
		Version:         3,
		ReportTitle:     "Example Corp — External Assessment",
		CompanyName:     "Example Security",
		ClientName:      "Example Corp",
		CompanyLogo:     "data:image/png;base64,iVBORw0KGgoCOMPANY",
		ClientLogo:      "data:image/png;base64,iVBORw0KGgoCLIENT",
		EngagementName:  "Q1 external assessment",
		AssessmentStart: "2026-03-03",
		AssessmentEnd:   "2026-03-14",
		LeadAssessor:    "A. Assessor",
		Contact:         "security@example.com",
		Classification:  "Confidential",
	}
}

// Unset branding must leave today's output alone, byte for byte.
func TestBrandingUnsetChangesNothing(t *testing.T) {
	findings := []store.Finding{{ID: 1, Severity: "High", Title: "IDOR on /invoices/{id}"}}
	md := Project(findings, nil)
	if got := WithBranding(md, store.ReportBranding{}); got != md {
		t.Fatal("empty branding altered the Markdown report")
	}
	want := HTMLFromMarkdown(md, findings)
	if got := HTMLDocument(md, findings, store.ReportBranding{}); got != want {
		t.Fatal("empty branding altered the HTML report")
	}
	if !strings.Contains(want, "<title>Interseptor — Engagement Report</title>") {
		t.Fatalf("default title changed:\n%s", want[:200])
	}
	if strings.Contains(want, "report-brand") {
		t.Error("an unbranded report must not carry the brand stylesheet")
	}
}

func TestBrandingRewritesTitleAndCover(t *testing.T) {
	md := WithBranding(Project([]store.Finding{{ID: 1, Title: "IDOR"}}, nil), brandingFixture())
	if !strings.HasPrefix(md, "# Example Corp — External Assessment\n") {
		t.Fatalf("title not replaced:\n%s", firstLines(md, 3))
	}
	if strings.Contains(md, "# Interseptor — Engagement Report") {
		t.Error("the default title survived")
	}
	for _, want := range []string{
		"Prepared by **Example Security** for **Example Corp**.",
		"- **Engagement:** Q1 external assessment",
		"- **Assessment period:** 2026-03-03 to 2026-03-14",
		"- **Lead assessor:** A. Assessor",
		"- **Contact:** security@example.com",
		"- **Classification:** Confidential",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("cover missing %q:\n%s", want, firstLines(md, 14))
		}
	}
	// The cover sits above the findings, not inside them.
	if strings.Index(md, "Lead assessor") > strings.Index(md, "IDOR") {
		t.Error("the cover was placed after the findings")
	}
}

func TestBrandingKeepsLogosOutOfMarkdownAndInHTML(t *testing.T) {
	b := brandingFixture()
	md := WithBranding(Project(nil, nil), b)
	if strings.Contains(md, "base64") {
		t.Error("a Markdown export must not carry embedded logo bytes")
	}
	html := HTMLDocument(md, nil, b)
	for _, want := range []string{
		`<title>Example Corp — External Assessment</title>`,
		`src="data:image/png;base64,iVBORw0KGgoCOMPANY"`,
		`src="data:image/png;base64,iVBORw0KGgoCLIENT"`,
		`alt="Example Security logo"`,
		`alt="Example Corp logo"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func TestBrandingEscapesEverythingItEmbeds(t *testing.T) {
	b := store.ReportBranding{
		ReportTitle: `Acme "</title><script>alert(1)</script>`,
		CompanyName: `Acme <img onerror=alert(1)>`,
		CompanyLogo: `data:image/png;base64,AAA" onerror="alert(1)`,
	}
	html := HTMLDocument(WithBranding(Project(nil, nil), b), nil, b)
	if strings.Contains(html, "<script>") {
		t.Fatalf("script survived into the report:\n%s", html[:400])
	}
	if strings.Contains(html, `onerror="alert(1)"`) || strings.Contains(html, "onerror=alert(1)>") {
		t.Fatalf("event handler survived:\n%s", html[:600])
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("markup was not escaped:\n%s", html[:400])
	}
}

func TestBrandingCustomCoverReplacesTheGeneratedOne(t *testing.T) {
	b := brandingFixture()
	b.CoverPage = "## Scope of work\n\nTwo web applications and one API, in scope between the dates above."
	md := WithBranding(Project(nil, nil), b)
	if !strings.Contains(md, "## Scope of work") {
		t.Fatalf("custom cover missing:\n%s", firstLines(md, 10))
	}
	if strings.Contains(md, "- **Lead assessor:**") {
		t.Error("the generated cover should give way to the operator's own")
	}
	// The title still comes from branding.
	if !strings.HasPrefix(md, "# Example Corp — External Assessment\n") {
		t.Errorf("title lost:\n%s", firstLines(md, 3))
	}
}

func TestBrandingPartialFieldsOnlyRenderWhatIsSet(t *testing.T) {
	md := WithBranding(Project(nil, nil), store.ReportBranding{Version: 1, LeadAssessor: "A. Assessor"})
	if !strings.Contains(md, "- **Lead assessor:** A. Assessor") {
		t.Fatalf("set field missing:\n%s", firstLines(md, 8))
	}
	for _, absent := range []string{"Prepared by", "Assessment period", "Classification", "Contact"} {
		if strings.Contains(md, absent) {
			t.Errorf("rendered %q with nothing to say", absent)
		}
	}
	if !strings.HasPrefix(md, "# Interseptor — Engagement Report\n") {
		t.Error("the default title should stand when no report title is set")
	}
}

func TestBrandingHandlesOneSidedPeriodAndParties(t *testing.T) {
	md := WithBranding(Project(nil, nil), store.ReportBranding{Version: 1, AssessmentStart: "2026-03-03", CompanyName: "Example Security"})
	if !strings.Contains(md, "- **Assessment period:** from 2026-03-03") {
		t.Errorf("open-ended period:\n%s", firstLines(md, 8))
	}
	if !strings.Contains(md, "Prepared by **Example Security**.") {
		t.Errorf("company without a client:\n%s", firstLines(md, 8))
	}
	md = WithBranding(Project(nil, nil), store.ReportBranding{Version: 1, AssessmentEnd: "2026-03-14", ClientName: "Example Corp"})
	if !strings.Contains(md, "- **Assessment period:** to 2026-03-14") {
		t.Errorf("open-start period:\n%s", firstLines(md, 8))
	}
	if !strings.Contains(md, "Prepared for **Example Corp**.") {
		t.Errorf("client without a company:\n%s", firstLines(md, 8))
	}
}

func TestBrandingSurvivesEngagementContext(t *testing.T) {
	md := Project(nil, nil)
	md = WithContext(md, Context{Brief: store.EngagementBrief{Version: 2, Scope: "example.com only"}})
	md = WithBranding(md, brandingFixture())
	coverAt := strings.Index(md, "Prepared by")
	contextAt := strings.Index(md, "## Engagement context")
	if coverAt < 0 || contextAt < 0 {
		t.Fatalf("one of the two sections is missing:\n%s", firstLines(md, 20))
	}
	if coverAt > contextAt {
		t.Error("the cover belongs above the engagement context")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
