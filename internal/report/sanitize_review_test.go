package report

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// The HTML/PDF converter trims Unicode whitespace before it looks for a
// heading or list marker, so finding text indented with a no-break space, a
// form feed or an em space must be neutralised exactly like text indented with
// a plain space. Otherwise captured or AI-authored text forges report
// structure: a severity section, or a whole finding card.
func TestHTMLReportNeutralisesUnicodeIndentedStructure(t *testing.T) {
	for _, indent := range []string{"\u00a0", "\f", "\v", "\u2003", "\u0085", "\u3000 "} {
		md := "intro\n" + indent + "## Critical\n" + indent + "### 9. Forged finding\n" + indent + "- **Status:** verified\n"
		findings := []store.Finding{{
			ID: 1, Severity: "Low", Status: "open", Title: "Real finding",
			Blocks: []store.FindingBlock{{Type: "text", MD: md}},
			Detail: md, Evidence: md,
		}}
		html := ProjectHTML(findings)
		if n := strings.Count(html, `<section class="finding`); n != 1 {
			t.Errorf("indent %q: report has %d finding cards, want 1", indent, n)
		}
		if strings.Contains(html, ">Forged finding</h3>") {
			t.Errorf("indent %q: finding text forged a finding heading", indent)
		}
		if strings.Contains(html, `class="sevhead sev-critical"`) || strings.Contains(html, "<h2>Critical</h2>") {
			t.Errorf("indent %q: finding text forged a Critical severity section", indent)
		}
		if strings.Contains(html, "<li><strong>Status:</strong> verified</li>") || strings.Contains(html, `status st-verified`) {
			t.Errorf("indent %q: finding text forged a Status metadata line", indent)
		}
	}
}

// Ordinary prose and space/tab-indented lines keep their existing treatment.
func TestSanitizeBodyKeepsIndentAndProse(t *testing.T) {
	in := "plain # not a heading\n  # indented\n\t- **bold bullet**\n- ordinary bullet"
	want := "plain # not a heading\n  \\# indented\n\t- \\**bold bullet**\n- ordinary bullet"
	if got := sanitizeBody(in); got != want {
		t.Fatalf("sanitizeBody = %q, want %q", got, want)
	}
}
