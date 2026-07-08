package report

import (
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// ProjectHTML renders the same engagement report as Project, as a self-contained
// HTML document suitable for download or print-to-PDF.
func ProjectHTML(findings []store.Finding, issues []store.Issue) string {
	md := Project(findings, issues)
	body := markdownToHTML(md)
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>Interseptor — Engagement Report</title>\n")
	b.WriteString("<style>")
	b.WriteString(reportHTMLCSS)
	b.WriteString("</style>\n</head>\n<body>\n<article class=\"md\">\n")
	b.WriteString(body)
	b.WriteString("\n</article>\n</body>\n</html>")
	return b.String()
}

const reportHTMLCSS = `
body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;line-height:1.65;color:#1a1a1a;background:#fff;margin:0;padding:32px 24px}
article.md{max-width:860px;margin:0 auto}
h1{font-size:22px;margin:0 0 14px;padding-bottom:8px;border-bottom:1px solid #ddd}
h2{font-size:17px;margin:22px 0 10px;color:#111}
h3{font-size:14px;margin:18px 0 8px;color:#222}
p{margin:8px 0}
ul,ol{margin:8px 0 8px 4px;padding-left:20px}
li{margin:4px 0}
code{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:12px;background:#f4f4f4;border:1px solid #e0e0e0;border-radius:4px;padding:1px 5px}
table{width:100%;border-collapse:collapse;font-size:12px;margin:12px 0}
th,td{border:1px solid #ddd;padding:7px 10px;text-align:left;vertical-align:top}
th{background:#f5f5f5;font-weight:700}
blockquote{border-left:3px solid #0a8f62;margin:12px 0;padding:8px 14px;background:#f7f7f7;color:#333;border-radius:0 6px 6px 0;font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:12px}
hr{border:none;border-top:1px solid #ddd;margin:20px 0}
em{font-style:italic;color:#444}
strong{font-weight:700}
.meta{color:#555;font-style:italic}
@media print{body{padding:12px}}
`

// markdownToHTML converts the subset of Markdown emitted by Project/Findings into HTML.
func markdownToHTML(md string) string {
	if md == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	inUL := false
	inQuote := false
	var quote []string
	var tableRows [][]string

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
			if strings.HasPrefix(trim, "_") && strings.HasSuffix(trim, "_") {
				out = append(out, "<p class=\"meta\">"+inlineMD(trim)+"</p>")
			} else {
				out = append(out, "<p>"+inlineMD(trim)+"</p>")
			}
		}
	}
	flushQuote()
	closeList()
	flushTable()
	return strings.Join(out, "\n")
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
	if strings.HasPrefix(s, "_") && strings.HasSuffix(s, "_") && strings.Count(s, "_") == 2 {
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
