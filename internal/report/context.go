package report

import (
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Context is engagement metadata cited in a report header: the authorisation
// brief the work was produced under.
type Context struct {
	Brief store.EngagementBrief
}

// WithContext inserts an "Engagement context" section directly after the
// report's title line. A context with nothing to cite returns md unchanged.
func WithContext(md string, c Context) string {
	section := contextSection(c)
	if section == "" {
		return md
	}
	title, rest, found := strings.Cut(md, "\n\n")
	if !found || !strings.HasPrefix(title, "# ") {
		return section + md
	}
	return title + "\n\n" + section + rest
}

func contextSection(c Context) string {
	var b strings.Builder
	if c.Brief.Version > 0 {
		writeBriefContext(&b, c.Brief)
	}
	if b.Len() == 0 {
		return ""
	}
	return "## Engagement context\n\n" + b.String()
}

func writeBriefContext(b *strings.Builder, br store.EngagementBrief) {
	fmt.Fprintf(b, "Produced under engagement brief v%d.\n\n", br.Version)
	rows := []struct{ label, v string }{
		{"Scope", br.Scope}, {"Authorisation", br.Authorisation}, {"Conduct rules", br.ConductRules},
		{"Rate limits", br.RateLimits}, {"Do not touch", br.DoNotTouch}, {"Credential policy", br.CredentialPolicy},
	}
	for _, r := range rows {
		if strings.TrimSpace(r.v) != "" {
			fmt.Fprintf(b, "- **%s:** %s\n", r.label, sanitizeLine(strings.TrimSpace(r.v)))
		}
	}
	b.WriteString("\n")
}

// MarkdownToHTML renders report markdown (as produced by Project and
// WithContext) as the self-contained HTML report document.
func MarkdownToHTML(md string) string { return projectHTMLFromMD(md) }
