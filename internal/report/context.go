package report

import (
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Context is engagement metadata cited in a report header: the authorisation
// brief the work was produced under and how the evidence was obtained.
type Context struct {
	Brief        store.EngagementBrief
	Interception store.InterceptionSetup
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
	if c.Interception.Version > 0 {
		writeInterceptionContext(&b, c.Interception)
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

func writeInterceptionContext(b *strings.Builder, in store.InterceptionSetup) {
	fmt.Fprintf(b, "### How evidence was obtained\n\nInterception setup v%d.\n\n", in.Version)
	if in.ProxyAddress != "" {
		fmt.Fprintf(b, "- **System proxy:** `%s`\n", code(in.ProxyAddress))
	}
	if in.CAFingerprint != "" {
		fmt.Fprintf(b, "- **CA fingerprint (SHA-256):** `%s`\n", code(in.CAFingerprint))
	}
	if len(in.Hosts) > 0 {
		fmt.Fprintf(b, "- **Applies to:** %s\n", sanitizeLine(strings.Join(in.Hosts, ", ")))
	}
	if len(in.Enablers) == 0 {
		b.WriteString("- **Pinning bypass:** none recorded; traffic was captured through the proxy and CA alone.\n\n")
		return
	}
	for _, e := range in.Enablers {
		parts := []string{sanitizeLine(e.Tool)}
		if e.TargetLibrary != "" {
			parts = append(parts, "library `"+code(e.TargetLibrary)+"`")
		}
		if e.Method != "" {
			parts = append(parts, "method `"+code(e.Method)+"`")
		}
		if e.ScriptHash != "" {
			parts = append(parts, "script `"+code(e.ScriptHash)+"`")
		}
		if len(e.Hosts) > 0 {
			parts = append(parts, "hosts "+sanitizeLine(strings.Join(e.Hosts, ", ")))
		}
		fmt.Fprintf(b, "- **Pinning bypass:** %s\n", strings.Join(parts, "; "))
	}
	b.WriteString("\n")
}

// MarkdownToHTML renders report markdown (as produced by Project and
// WithContext) as the self-contained HTML report document.
func MarkdownToHTML(md string, findings []store.Finding) string {
	return HTMLFromMarkdown(md, findings)
}
