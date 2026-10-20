package report

import (
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// ProjectHTML renders the same engagement report as Project, as a self-contained
// HTML document suitable for download or print-to-PDF.
func ProjectHTML(findings []store.Finding, issues []store.Issue) string {
	return projectHTMLFromMD(Project(findings, issues), secretLintNotice(findings))
}

// ProjectHTMLGroupedByTag is the HTML counterpart of ProjectGroupedByTag.
func ProjectHTMLGroupedByTag(findings []store.Finding, issues []store.Issue, tagOrder, omitTags []string) string {
	return projectHTMLFromMD(ProjectGroupedByTag(findings, issues, tagOrder, omitTags), secretLintNotice(findings))
}

// HTMLFromMarkdown wraps an already-rendered engagement report in the HTML shell.
func HTMLFromMarkdown(md string, findings []store.Finding) string {
	return projectHTMLFromMD(md, secretLintNotice(findings))
}

func projectHTMLFromMD(md, lintNotice string) string {
	return projectHTMLDoc(md, lintNotice, store.ReportBranding{})
}

// projectHTMLDoc assembles the one HTML report document. Every HTML and PDF
// export goes through here, so the two cannot differ.
func projectHTMLDoc(md, lintNotice string, brand store.ReportBranding) string {
	// markdownBlocks keeps the byte-stable conversion the unit tests pin; the
	// enrichment pass then upgrades the flat blocks into severity-tinted finding
	// cards. Both HTML and PDF go through here, so the document stays one thing.
	body := strings.Join(enrichReport(markdownBlocks(md)), "\n")
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	// The document is self-contained: styles and images are inline. Pin that so
	// the browser that prints the PDF (or opens the download) never fetches.
	b.WriteString("<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; img-src data:; style-src 'unsafe-inline'\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>" + htmlEsc(brandingTitle(brand)) + "</title>\n")
	b.WriteString("<style>")
	b.WriteString(reportHTMLCSS)
	logos := brandingLogos(brand)
	if logos != "" {
		// Only a branded report carries the brand rules, so an unbranded export
		// is byte-identical to the one Interseptor produced before white-label.
		b.WriteString(reportBrandCSS)
	}
	b.WriteString("</style>\n</head>\n<body>\n")
	b.WriteString(lintNotice)
	b.WriteString(logos)
	b.WriteString("<article class=\"md\">\n")
	b.WriteString(body)
	b.WriteString("\n</article>\n</body>\n</html>")
	return b.String()
}

const reportHTMLCSS = `
:root{
  --page:#eef1f5;--sheet:#fff;--ink:#1a2230;--ink2:#49546b;--ink3:#737e8f;
  --line:#e6eaf0;--line2:#d4dae3;--soft:#f6f8fb;
  --accent:#047857;--accent2:#065f46;--accent-bg:#ecfdf3;--accent-bd:#a7e8cf;
  --sev-critical:#7e22ce;--sev-critical-bg:#faf5ff;--sev-critical-bd:#d8b4fe;
  --sev-high:#b42318;--sev-high-bg:#fef3f2;--sev-high-bd:#fda29b;
  --sev-medium:#b54708;--sev-medium-bg:#fffaeb;--sev-medium-bd:#fdb022;
  --sev-low:#175cd3;--sev-low-bg:#eff8ff;--sev-low-bd:#84caff;
  --sev-info:#475467;--sev-info-bg:#f2f4f7;--sev-info-bd:#d0d5dd;
  --mono:ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,monospace;
  --sans:system-ui,-apple-system,'Segoe UI',Roboto,'Helvetica Neue',sans-serif;
}
*{box-sizing:border-box}
body{font-family:var(--sans);line-height:1.62;color:var(--ink);background:var(--page);margin:0;padding:40px 18px;font-size:14px;-webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility}
article.md{max-width:900px;margin:0 auto;background:var(--sheet);border:1px solid var(--line);border-radius:14px;padding:44px 48px;box-shadow:0 1px 2px rgba(16,24,40,.04),0 16px 40px rgba(16,24,40,.07)}

/* severity palette tokens, shared by cards, group headers and pills */
.sev-critical{--sv:var(--sev-critical);--svbg:var(--sev-critical-bg);--svbd:var(--sev-critical-bd)}
.sev-high{--sv:var(--sev-high);--svbg:var(--sev-high-bg);--svbd:var(--sev-high-bd)}
.sev-medium{--sv:var(--sev-medium);--svbg:var(--sev-medium-bg);--svbd:var(--sev-medium-bd)}
.sev-low{--sv:var(--sev-low);--svbg:var(--sev-low-bg);--svbd:var(--sev-low-bd)}
.sev-info{--sv:var(--sev-info);--svbg:var(--sev-info-bg);--svbd:var(--sev-info-bd)}

/* cover title + subtitle */
article.md h1{font-size:30px;line-height:1.18;letter-spacing:-.021em;font-weight:800;color:var(--ink);margin:0 0 18px;padding:2px 0 20px 18px;border:0;border-left:4px solid var(--accent)}
article.md>p.meta:first-of-type,article.md h1+p.meta{font-size:15px;color:var(--ink2);font-style:normal;margin:-8px 0 20px}
.meta{color:var(--ink3);font-style:italic}

/* section headers */
article.md h2{font-size:17px;font-weight:700;letter-spacing:-.012em;color:var(--ink);margin:38px 0 16px;padding-bottom:9px;border-bottom:1px solid var(--line2)}
article.md h2.sevhead{display:flex;align-items:center;gap:9px;border-bottom:0;margin:34px 0 14px;font-size:12px;font-weight:700;text-transform:uppercase;letter-spacing:.1em;color:var(--sv)}
article.md h2.sevhead::before{content:"";display:inline-block;width:13px;height:13px;border-radius:4px;background:var(--sv)}
article.md h3{font-size:14px;font-weight:700;color:var(--ink);margin:18px 0 8px}

p{margin:9px 0}
ul,ol{margin:9px 0 9px 2px;padding-left:22px}
li{margin:4px 0}
strong{font-weight:700}
em{font-style:italic;color:var(--ink2)}
a{color:var(--accent2);text-decoration:none;border-bottom:1px solid var(--accent-bd)}
hr{border:0;border-top:1px solid var(--line2);margin:26px 0}

code{font-family:var(--mono);font-size:12px;background:var(--soft);border:1px solid var(--line);border-radius:5px;padding:1px 5px;word-break:break-word}
pre{font-family:var(--mono);font-size:12px;line-height:1.55;background:#0f1623;color:#d7e0ee;border:1px solid #223049;border-radius:9px;padding:14px 16px;overflow-x:auto;white-space:pre-wrap;word-break:break-word;margin:12px 0}
pre code{background:none;border:0;padding:0;color:inherit;font-size:inherit}

table{table-layout:fixed;overflow-wrap:anywhere;width:100%;border-collapse:separate;border-spacing:0;font-size:13px;margin:14px 0;border:1px solid var(--line2);border-radius:9px;overflow:hidden}
th,td{border-bottom:1px solid var(--line);padding:9px 12px;text-align:left;vertical-align:top}
th{background:var(--soft);font-weight:700;color:var(--ink2);font-size:11px;text-transform:uppercase;letter-spacing:.05em}
tr:last-child td{border-bottom:0}

blockquote{border-left:3px solid var(--accent);margin:12px 0;padding:10px 14px;background:var(--soft);color:var(--ink2);border-radius:0 8px 8px 0;font-family:var(--mono);font-size:12px}
blockquote p{margin:4px 0}

/* finding cards */
.finding{margin:20px 0;border:1px solid var(--line);border-left:4px solid var(--sv,var(--sev-info));border-radius:11px;background:var(--sheet);overflow:hidden;box-shadow:0 1px 2px rgba(16,24,40,.04)}
.finding-head{display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:15px 18px;background:linear-gradient(180deg,color-mix(in srgb,var(--svbg) 55%,#fff),#fff);border-bottom:1px solid var(--line)}
.finding-id{flex:none;align-self:center;font:800 12px/1 var(--mono);color:#fff;background:var(--sv);border-radius:7px;padding:7px 9px;min-width:26px;text-align:center}
.finding-title{flex:1 1 auto;min-width:200px;margin:0;font-size:17px;font-weight:700;letter-spacing:-.012em;color:var(--ink);line-height:1.3}
.finding-tags{display:flex;gap:7px;align-items:center;margin-left:auto}
.sev-tag{font:700 11px/1 var(--sans);text-transform:uppercase;letter-spacing:.05em;color:var(--sv);background:var(--svbg);border:1px solid var(--svbd);border-radius:999px;padding:5px 11px}
.score-tag{font:800 12px/1 var(--mono);color:#fff;background:var(--sv);border-radius:999px;padding:5px 10px}

.finding-meta{display:flex;flex-direction:column;gap:10px;padding:13px 18px;background:var(--soft);border-bottom:1px solid var(--line)}
.m-badges{display:flex;flex-wrap:wrap;gap:7px}
.m-badge{display:inline-flex;align-items:baseline;gap:6px;font-size:12px;background:#fff;border:1px solid var(--line2);border-radius:8px;padding:4px 10px}
.m-badge .m-k{font-size:10px;font-weight:700;text-transform:uppercase;letter-spacing:.06em;color:var(--ink3)}
.m-badge .m-v{color:var(--ink);font-weight:600}
.m-badge.status{color:var(--stc,var(--ink));background:var(--stbg,#fff);border-color:var(--stbd,var(--line2))}
.m-badge.status .m-k{color:var(--stc,var(--ink3));opacity:.8}
.m-badge.status .m-v{color:var(--stc,var(--ink))}
.st-verified{--stc:#067647;--stbg:#ecfdf3;--stbd:#abefc6}
.st-open,.st-needs-verification{--stc:#b54708;--stbg:#fffaeb;--stbd:#fdb022}
.st-fixed{--stc:#175cd3;--stbg:#eff8ff;--stbd:#84caff}
.st-wont-fix,.st-false-positive{--stc:#475467;--stbg:#f2f4f7;--stbd:#d0d5dd}
.m-row{display:flex;gap:12px;align-items:baseline;font-size:12.5px}
.m-row .m-k{flex:none;min-width:116px;font-size:10px;font-weight:700;text-transform:uppercase;letter-spacing:.06em;color:var(--ink3);padding-top:1px}
.m-row .m-v{color:var(--ink2);min-width:0;overflow-wrap:anywhere}
.m-vec{font-family:var(--mono);font-size:11.5px;color:var(--ink2);background:#fff;border:1px solid var(--line2);border-radius:6px;padding:3px 8px;overflow-wrap:anywhere}
.m-tagwrap{display:flex;flex-wrap:wrap;gap:5px}
.m-tag{font-size:11px;font-family:var(--mono);color:var(--accent2);background:var(--accent-bg);border:1px solid var(--accent-bd);border-radius:999px;padding:2px 9px}

.finding-body{padding:14px 18px 18px}
.finding-body>:first-child{margin-top:0}
.finding-body>:last-child{margin-bottom:0}
.finding-body>p>strong:first-child{color:var(--accent2)}
.finding-body img{max-width:100%;height:auto;border:1px solid var(--line2);border-radius:8px;margin-top:6px}
img{max-width:100%}
figure.shot{margin:12px 0}
figure.shot figcaption{font-size:12px;color:var(--ink2);margin-top:6px}

/* secret-lint callout */
.secret-lint{display:block;max-width:900px;margin:0 auto 18px;padding:12px 16px;border:1px solid var(--sev-medium-bd);border-left-width:4px;background:var(--sev-medium-bg);color:#7a3e08;border-radius:9px;font-size:13px}
.secret-lint ul{margin:8px 0 0}
.secret-lint code{background:#fff}

@media (max-width:560px){
  body{padding:16px 0}
  article.md{border-radius:0;border-left:0;border-right:0;padding:24px 18px}
  .finding-head{gap:9px}
  .finding-title{font-size:16px;flex-basis:100%}
  .finding-tags{margin-left:0}
  .m-row{flex-direction:column;gap:2px}
  .m-row .m-k{min-width:0}
}
@page{size:A4;margin:16mm 14mm 18mm;@bottom-right{content:"Page " counter(page) " of " counter(pages);font:10px system-ui,sans-serif;color:#737e8f}}
@media print{
  :root{--page:#fff}
  figure.shot{break-inside:avoid}
  p:has(+pre),h1,h2,h3{break-after:avoid}
  body{padding:0;font-size:12px}
  article.md{max-width:none;border:0;border-radius:0;box-shadow:none;padding:0}
  .secret-lint{display:none}
  .finding,table,pre,blockquote,tr,img,.finding-head{break-inside:avoid}
  h1,h2,h3,.finding-head{break-after:avoid}
  pre{background:#f4f4f4;color:#111;border:1px solid #ddd}
  a,code,p,li{overflow-wrap:anywhere}
}
`

// enrichReport upgrades the flat block list from markdownBlocks into a
// designed report: engagement findings become severity-tinted cards and their
// leading "- **Label:** value" bullet list becomes a metadata badge band. Every
// other block passes through untouched, so the conversion and its tests are
// unaffected and the HTML the PDF renderer prints is the HTML downloaded.
func enrichReport(blocks []string) []string {
	out := make([]string, 0, len(blocks)+len(blocks)/4)
	var cur []string // blocks of the finding currently being buffered
	curSev := ""     // severity from the most recent "## <Severity>" group header

	flush := func() {
		if len(cur) > 0 {
			out = append(out, renderFindingCard(cur, curSev)...)
			cur = nil
		}
	}

	for _, b := range blocks {
		switch {
		case isFindingHeading(b):
			flush()
			cur = []string{b}
		case strings.HasPrefix(b, "<h2>"):
			flush()
			sev := severityOfHeading(b)
			curSev = sev
			out = append(out, decorateHeading(b, sev))
		case strings.HasPrefix(b, "<h1>"), strings.HasPrefix(b, "<hr>"):
			flush()
			out = append(out, b)
		default:
			if len(cur) > 0 {
				cur = append(cur, b)
			} else {
				out = append(out, b)
			}
		}
	}
	flush()
	return out
}

// isFindingHeading reports whether a block is a numbered finding heading
// ("### 3. Title"); the engagement-context sub-head "### How evidence was
// obtained" deliberately does not match, so it is never boxed as a finding.
func isFindingHeading(block string) bool {
	inner, ok := innerOfTag(block, "h3")
	if !ok {
		return false
	}
	num, _ := splitFindingTitle(inner)
	return num != ""
}

// severityOfHeading returns the severity slug when an <h2> is exactly one of the
// severity group headers Project emits, else "".
func severityOfHeading(block string) string {
	inner, ok := innerOfTag(block, "h2")
	if !ok {
		return ""
	}
	return sevSlug(inner)
}

// decorateHeading tags a severity group header so CSS can colour it; other
// section headers pass through so their generic styling applies.
func decorateHeading(block, sev string) string {
	if sev == "" {
		return block
	}
	inner, _ := innerOfTag(block, "h2")
	return `<h2 class="sevhead sev-` + sev + `">` + inner + `</h2>`
}

// renderFindingCard turns a buffered finding (heading, metadata list, body
// blocks) into a severity card. groupSev is the enclosing "## <Severity>"
// group, used when the finding carries no calculated CVSS of its own.
func renderFindingCard(cur []string, groupSev string) []string {
	title, _ := innerOfTag(cur[0], "h3")
	num, name := splitFindingTitle(title)

	// The metadata list, when present, is exactly: cur[1]=="<ul>", then "<li>"
	// items, then "</ul>". Its items all carry a "<strong>Label:</strong>".
	metaStart, metaEnd := -1, -1
	if len(cur) >= 3 && cur[1] == "<ul>" && strings.Contains(cur[2], "<strong>") {
		for j := 2; j < len(cur); j++ {
			if cur[j] == "</ul>" {
				metaStart, metaEnd = 1, j
				break
			}
		}
	}
	var items []string
	if metaStart >= 0 {
		items = cur[metaStart+1 : metaEnd]
	}

	sev, score := groupSev, ""
	if s, sc := scanCalculatedCVSS(items); sc != "" || s != "" {
		score = sc
		if sev == "" {
			sev = s
		}
	}
	if sev == "" {
		// No group heading and no CVSS (the false-positive appendix): the card's
		// own "Severity:" item is the truth, not a default of info.
		sev = scanSeverityItem(items)
	}
	if sev == "" {
		sev = "info"
	}

	out := make([]string, 0, len(cur)+6)
	out = append(out, `<section class="finding sev-`+sev+`">`)
	out = append(out, findingHead(num, name, sev, score))
	if len(items) > 0 {
		out = append(out, buildMetaBand(items))
	}
	out = append(out, `<div class="finding-body">`)
	for i := 1; i < len(cur); i++ {
		if metaStart >= 0 && i >= metaStart && i <= metaEnd {
			continue
		}
		out = append(out, cur[i])
	}
	out = append(out, `</div>`, `</section>`)
	return out
}

func findingHead(num, name, sev, score string) string {
	var b strings.Builder
	b.WriteString(`<header class="finding-head">`)
	if num != "" {
		b.WriteString(`<span class="finding-id">` + num + `</span>`)
	}
	b.WriteString(`<h3 class="finding-title">` + name + `</h3>`)
	b.WriteString(`<span class="finding-tags">`)
	b.WriteString(`<span class="sev-tag">` + sevLabel(sev) + `</span>`)
	if score != "" {
		b.WriteString(`<span class="score-tag" title="CVSS v4.0 base score">` + score + `</span>`)
	}
	b.WriteString(`</span></header>`)
	return b.String()
}

// buildMetaBand lays the metadata bullets out as a badge band: short fields as
// chips, long ones (CVSS vector, verification notes, readiness gaps) as rows,
// tags as pills. Nothing is dropped — an unrecognised label becomes a row.
func buildMetaBand(items []string) string {
	var badges, rows []string
	for _, li := range items {
		label, value := parseMetaItem(li)
		if value == "" && label == "" {
			continue
		}
		switch label {
		case "", "Calculated CVSS v4.0":
			// Score is surfaced in the header; a bare value becomes a row.
			if label == "" && value != "" {
				rows = append(rows, metaRow("", value))
			}
		case "Tags":
			rows = append(rows, metaTagsRow(value))
		case "CVSS":
			rows = append(rows, `<div class="m-row"><span class="m-k">CVSS vector</span><code class="m-vec">`+value+`</code></div>`)
		case "Status":
			badges = append(badges, `<span class="m-badge status st-`+slugify(value)+`"><span class="m-k">Status</span><span class="m-v">`+value+`</span></span>`)
		case "Confidence", "CWE", "Environment", "Impact verification", "Autopilot confidence",
			"action evidence", "result evidence", "control evidence":
			badges = append(badges, metaBadge(label, value))
		default:
			rows = append(rows, metaRow(label, value))
		}
	}
	if len(badges) == 0 && len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="finding-meta">`)
	if len(badges) > 0 {
		b.WriteString(`<div class="m-badges">` + strings.Join(badges, "") + `</div>`)
	}
	b.WriteString(strings.Join(rows, ""))
	b.WriteString(`</div>`)
	return b.String()
}

func metaBadge(label, value string) string {
	return `<span class="m-badge"><span class="m-k">` + label + `</span><span class="m-v">` + value + `</span></span>`
}

func metaRow(label, value string) string {
	if label == "" {
		return `<div class="m-row"><span class="m-v">` + value + `</span></div>`
	}
	return `<div class="m-row"><span class="m-k">` + label + `</span><span class="m-v">` + value + `</span></div>`
}

func metaTagsRow(value string) string {
	var chips strings.Builder
	for _, t := range strings.Split(value, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		chips.WriteString(`<span class="m-tag">` + t + `</span>`)
	}
	return `<div class="m-row"><span class="m-k">Tags</span><span class="m-tagwrap">` + chips.String() + `</span></div>`
}

// scanCalculatedCVSS pulls the score (e.g. "8.7") and severity slug from the
// "Calculated CVSS v4.0: 8.7 (HIGH, CVSS-B)" metadata line, when present.
func scanCalculatedCVSS(items []string) (sev, score string) {
	for _, li := range items {
		label, value := parseMetaItem(li)
		if label != "Calculated CVSS v4.0" {
			continue
		}
		score = firstField(value)
		if lp := strings.Index(value, "("); lp >= 0 {
			rating := value[lp+1:]
			if c := strings.IndexAny(rating, ",)"); c >= 0 {
				rating = rating[:c]
			}
			sev = sevSlug(rating)
		}
		return sev, score
	}
	return "", ""
}

// scanSeverityItem returns the slug of a "Severity:" metadata item, or "".
func scanSeverityItem(items []string) string {
	for _, li := range items {
		if label, value := parseMetaItem(li); label == "Severity" {
			return sevSlug(value)
		}
	}
	return ""
}

// parseMetaItem splits a "<li><strong>Label:</strong> value</li>" item into the
// label (trailing colon removed) and the remaining value HTML.
func parseMetaItem(li string) (label, value string) {
	inner, ok := innerOfTag(li, "li")
	if !ok {
		return "", ""
	}
	if strings.HasPrefix(inner, "<strong>") {
		if end := strings.Index(inner, "</strong>"); end >= 0 {
			label = strings.TrimSuffix(strings.TrimSpace(inner[len("<strong>"):end]), ":")
			value = strings.TrimSpace(inner[end+len("</strong>"):])
			return label, value
		}
	}
	return "", strings.TrimSpace(inner)
}

// splitFindingTitle pulls the leading "N." number off a finding heading, so
// "3. SQL injection" yields ("3", "SQL injection"). A heading with no leading
// number yields ("", heading).
func splitFindingTitle(inner string) (num, name string) {
	i := 0
	for i < len(inner) && inner[i] >= '0' && inner[i] <= '9' {
		i++
	}
	if i > 0 && strings.HasPrefix(inner[i:], ". ") {
		return inner[:i], strings.TrimSpace(inner[i+2:])
	}
	return "", inner
}

// innerOfTag returns the content of a block wrapped in exactly <tag>...</tag>.
func innerOfTag(block, tag string) (string, bool) {
	open, close := "<"+tag+">", "</"+tag+">"
	if strings.HasPrefix(block, open) && strings.HasSuffix(block, close) && len(block) >= len(open)+len(close) {
		return block[len(open) : len(block)-len(close)], true
	}
	return "", false
}

func firstField(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

// sevSlug maps any severity or CVSS rating word to a palette slug, or "".
func sevSlug(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "critical"
	case "high":
		return "high"
	case "medium":
		return "medium"
	case "low":
		return "low"
	case "info", "informational", "none":
		return "info"
	}
	return ""
}

func sevLabel(slug string) string {
	switch slug {
	case "critical":
		return "Critical"
	case "high":
		return "High"
	case "medium":
		return "Medium"
	case "low":
		return "Low"
	default:
		return "Info"
	}
}

// slugify lowercases and dashes a value for a CSS class suffix (status badges).
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// markdownToHTML converts the subset of Markdown emitted by Project/Findings into HTML.
func markdownToHTML(md string) string {
	return strings.Join(markdownBlocks(md), "\n")
}

// markdownBlocks is markdownToHTML's worker: it returns the HTML block elements
// in order. markdownToHTML joins them, so its output is byte-for-byte what it
// always was; projectHTMLDoc enriches the slice before joining.
func markdownBlocks(md string) []string {
	if md == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	inUL := false
	inQuote := false
	inFence := false
	fenceLen := 0
	var fence []string
	var quote []string
	var tableRows [][]string

	flushFence := func() {
		if !inFence {
			return
		}
		out = append(out, "<pre><code>"+htmlEsc(strings.Join(fence, "\n"))+"</code></pre>")
		fence = nil
		inFence = false
		fenceLen = 0
	}
	flushQuote := func() {
		if !inQuote {
			return
		}
		out = append(out, "<blockquote>"+strings.Join(quote, "<br>")+"</blockquote>")
		quote = nil
		inQuote = false
	}
	closeList := func() {
		if inUL {
			out = append(out, "</ul>")
			inUL = false
		}
	}
	flushTable := func() {
		if len(tableRows) == 0 {
			return
		}
		var tb strings.Builder
		tb.WriteString("<table>")
		for i, row := range tableRows {
			tag := "td"
			if i == 0 {
				tag = "th"
			}
			tb.WriteString("<tr>")
			for _, c := range row {
				tb.WriteString("<" + tag + ">" + inlineMD(c) + "</" + tag + ">")
			}
			tb.WriteString("</tr>")
		}
		tb.WriteString("</table>")
		out = append(out, tb.String())
		tableRows = nil
	}

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if inFence {
			if trim == strings.Repeat("`", fenceLen) {
				flushFence()
				continue
			}
			fence = append(fence, line)
			continue
		}
		if openingLen := backtickFenceLen(trim); openingLen >= 3 {
			flushQuote()
			closeList()
			flushTable()
			inFence = true
			fenceLen = openingLen
			continue
		}
		if strings.HasPrefix(trim, "|") {
			flushQuote()
			closeList()
			if strings.Contains(trim, "---") {
				continue
			}
			tableRows = append(tableRows, splitTableRow(trim))
			continue
		}
		flushTable()

		if strings.HasPrefix(trim, "> ") {
			closeList()
			inQuote = true
			quote = append(quote, inlineMD(strings.TrimPrefix(trim, "> ")))
			continue
		}
		if inQuote && trim == ">" {
			// A bare ">" only continues the quote in Markdown; it has no text.
			continue
		}
		if inQuote && trim != "" {
			quote = append(quote, inlineMD(trim))
			continue
		}
		flushQuote()

		switch {
		case trim == "":
			closeList()
		case strings.HasPrefix(trim, "# "):
			closeList()
			out = append(out, "<h1>"+inlineMD(strings.TrimPrefix(trim, "# "))+"</h1>")
		case strings.HasPrefix(trim, "## "):
			closeList()
			out = append(out, "<h2>"+inlineMD(strings.TrimPrefix(trim, "## "))+"</h2>")
		case strings.HasPrefix(trim, "### "):
			closeList()
			out = append(out, "<h3>"+inlineMD(strings.TrimPrefix(trim, "### "))+"</h3>")
		case trim == "---":
			closeList()
			out = append(out, "<hr>")
		case strings.HasPrefix(trim, "- "):
			if !inUL {
				out = append(out, "<ul>")
				inUL = true
			}
			out = append(out, "<li>"+inlineMD(strings.TrimPrefix(trim, "- "))+"</li>")
		default:
			closeList()
			if alt, src, ok := parseMDImage(trim); ok {
				if !embeddedImageSrc(src) {
					// Only embedded images render. The PDF is printed by a local
					// browser, so a remote or file: URL in finding text (which can
					// come from captured, attacker-controlled content) would make
					// that browser fetch it.
					out = append(out, `<p class="meta">Image not embedded: `+htmlEsc(alt)+`</p>`)
				} else {
					out = append(out, `<figure class="shot"><img src="`+htmlEsc(src)+`" alt="`+htmlEsc(alt)+`" style="max-width:100%"></figure>`)
				}
			} else if cap, ok := figureCaption(trim); ok && len(out) > 0 && strings.HasPrefix(out[len(out)-1], `<figure class="shot">`) {
				last := out[len(out)-1]
				out[len(out)-1] = strings.TrimSuffix(last, "</figure>") + "<figcaption>" + htmlEsc(cap) + "</figcaption></figure>"
			} else if strings.HasPrefix(trim, "_") && strings.HasSuffix(trim, "_") {
				out = append(out, "<p class=\"meta\">"+inlineMD(trim)+"</p>")
			} else {
				out = append(out, "<p>"+inlineMD(trim)+"</p>")
			}
		}
	}
	flushFence()
	flushQuote()
	closeList()
	flushTable()
	return out
}

func backtickFenceLen(line string) int {
	for i, r := range line {
		if r != '`' {
			return i
		}
	}
	return len(line)
}

func splitTableRow(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func inlineMD(s string) string {
	s = htmlEsc(s)
	for {
		i := strings.Index(s, "**")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+2:], "**")
		if j < 0 {
			break
		}
		j += i + 2
		s = s[:i] + "<strong>" + s[i+2:j] + "</strong>" + s[j+2:]
	}
	for {
		i := strings.Index(s, "`")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+1:], "`")
		if j < 0 {
			break
		}
		j += i + 1
		s = s[:i] + "<code>" + s[i+1:j] + "</code>" + s[j+1:]
	}
	// Whole-line emphasis. The inner text may contain underscores of its own:
	// every evidence provenance line the report emits looks like
	// "_Evidence: role=action, source=captured_flow._", and requiring exactly two
	// underscores left those delimiters visible in the delivered report. Follow
	// Markdown instead and only require that neither delimiter is padded.
	if len(s) > 2 && strings.HasPrefix(s, "_") && strings.HasSuffix(s, "_") &&
		!strings.ContainsAny(s[1:2], " \t") && !strings.ContainsAny(s[len(s)-2:len(s)-1], " \t") {
		s = "<em>" + s[1:len(s)-1] + "</em>"
	}
	return s
}

func htmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// embeddedImageSrc reports whether an image source is an inline data: image,
// the only kind a report document loads.
func embeddedImageSrc(src string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(src)), "data:image/")
}

// figureCaption recognizes the "_Figure N.M: caption_" line the report writes
// under each screenshot and returns its text without the emphasis markers.
func figureCaption(line string) (string, bool) {
	if len(line) < len("_Figure _") || !strings.HasPrefix(line, "_Figure ") || !strings.HasSuffix(line, "_") {
		return "", false
	}
	return line[1 : len(line)-1], true
}

// parseMDImage recognizes a whole-line markdown image ![alt](src).
func parseMDImage(line string) (alt, src string, ok bool) {
	if !strings.HasPrefix(line, "![") {
		return "", "", false
	}
	endAlt := strings.Index(line, "](")
	if endAlt < 0 {
		return "", "", false
	}
	if !strings.HasSuffix(line, ")") {
		return "", "", false
	}
	alt = line[2:endAlt]
	src = line[endAlt+2 : len(line)-1]
	if src == "" {
		return "", "", false
	}
	return alt, src, true
}
