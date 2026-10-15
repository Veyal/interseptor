package report

import (
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// White-label rendering. Unset branding renders the report Interseptor produced
// before this existed; the tests pin that, because a consulting template must
// never change a default export.

// WithBranding applies project branding to a rendered report: it replaces the
// title when one is set and inserts the cover block directly beneath it. Apply
// it after WithContext so the cover sits above the engagement context.
//
// Logos are deliberately not written here. A Markdown export would carry
// hundreds of kilobytes of base64 in a line nobody can read; the logos belong to
// the HTML and PDF renders, where HTMLDocument puts them.
func WithBranding(md string, b store.ReportBranding) string {
	if b.IsEmpty() {
		return md
	}
	title, rest, found := strings.Cut(md, "\n\n")
	if !found || !strings.HasPrefix(title, "# ") {
		return brandingCover(b) + md
	}
	if t := strings.TrimSpace(sanitizeLine(b.ReportTitle)); t != "" {
		title = "# " + t
	}
	cover := brandingCover(b)
	if cover == "" {
		return title + "\n\n" + rest
	}
	return title + "\n\n" + cover + rest
}

// brandingCover is the Markdown cover block, or "" when there is nothing to say.
func brandingCover(b store.ReportBranding) string {
	if cover := strings.TrimSpace(b.CoverPage); cover != "" {
		return cover + "\n\n---\n\n"
	}
	var lines []string
	if parties := brandingParties(b); parties != "" {
		lines = append(lines, parties, "")
	}
	rows := [][2]string{
		{"Engagement", b.EngagementName},
		{"Assessment period", brandingPeriod(b)},
		{"Lead assessor", b.LeadAssessor},
		{"Contact", b.Contact},
		{"Classification", b.Classification},
	}
	for _, row := range rows {
		if v := strings.TrimSpace(sanitizeLine(row[1])); v != "" {
			lines = append(lines, "- **"+row[0]+":** "+v)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n\n---\n\n"
}

func brandingParties(b store.ReportBranding) string {
	company := strings.TrimSpace(sanitizeLine(b.CompanyName))
	client := strings.TrimSpace(sanitizeLine(b.ClientName))
	switch {
	case company != "" && client != "":
		return fmt.Sprintf("Prepared by **%s** for **%s**.", company, client)
	case company != "":
		return fmt.Sprintf("Prepared by **%s**.", company)
	case client != "":
		return fmt.Sprintf("Prepared for **%s**.", client)
	}
	return ""
}

// brandingPeriod states the window that is actually known. A half-open window is
// written as half-open rather than filled in with today's date.
func brandingPeriod(b store.ReportBranding) string {
	start, end := strings.TrimSpace(b.AssessmentStart), strings.TrimSpace(b.AssessmentEnd)
	switch {
	case start != "" && end != "":
		return start + " to " + end
	case start != "":
		return "from " + start
	case end != "":
		return "to " + end
	}
	return ""
}

// HTMLDocument renders report Markdown as the self-contained HTML document,
// applying branding to the document title and adding the logo header. It is the
// branded form of HTMLFromMarkdown and produces identical bytes when branding is
// unset.
func HTMLDocument(md string, findings []store.Finding, b store.ReportBranding) string {
	return projectHTMLDoc(md, secretLintNotice(findings), b)
}

// brandingTitle is the document title: the configured one, or the default.
func brandingTitle(b store.ReportBranding) string {
	if t := strings.TrimSpace(sanitizeLine(b.ReportTitle)); t != "" {
		return t
	}
	return "Interseptor — Engagement Report"
}

// brandingLogos renders the logo header. Every interpolated value is escaped:
// the data URI and the alt text both come from project settings and are written
// into attributes.
func brandingLogos(b store.ReportBranding) string {
	logos := [][2]string{{b.CompanyLogo, b.CompanyName}, {b.ClientLogo, b.ClientName}}
	var imgs []string
	for _, l := range logos {
		src := strings.TrimSpace(l[0])
		if src == "" {
			continue
		}
		name := strings.TrimSpace(sanitizeLine(l[1]))
		alt := "logo"
		if name != "" {
			alt = name + " logo"
		}
		imgs = append(imgs, `<img src="`+htmlEsc(src)+`" alt="`+htmlEsc(alt)+`">`)
	}
	if len(imgs) == 0 {
		return ""
	}
	return `<header class="report-brand">` + strings.Join(imgs, "") + "</header>\n"
}

const reportBrandCSS = `
header.report-brand{max-width:860px;margin:0 auto 18px;display:flex;align-items:center;justify-content:space-between;gap:24px;flex-wrap:wrap}
header.report-brand img{max-height:56px;max-width:45%;height:auto;object-fit:contain}
@media print{header.report-brand{break-after:avoid}}
`
