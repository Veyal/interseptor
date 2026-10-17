package report

import (
	"strings"
)

// markdownToJiraWiki converts the Markdown subset the report writes (headings,
// "- " and "N. " lists, "> " quotes, tables, fenced code, **bold**, `code`,
// whole-line _emphasis_ and images) into Jira wiki markup, the format Jira's v2
// issue API renders. Prose characters that open Jira macros or links ({ } [ ])
// are escaped so captured content cannot inject markup into the ticket.
func markdownToJiraWiki(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	fenceLen := 0
	headerRow := true
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if fenceLen > 0 {
			if trim == strings.Repeat("`", fenceLen) {
				out = append(out, "{noformat}")
				fenceLen = 0
				continue
			}
			// Nothing inside {noformat} is interpreted except its own terminator.
			out = append(out, strings.ReplaceAll(line, "{noformat}", "{ noformat}"))
			continue
		}
		if n := backtickFenceLen(trim); n >= 3 {
			fenceLen = n
			out = append(out, "{noformat}")
			continue
		}
		if !strings.HasPrefix(trim, "|") {
			headerRow = true
		}
		switch {
		case strings.HasPrefix(trim, "|"):
			if strings.Contains(trim, "---") {
				continue
			}
			sep := "|"
			if headerRow {
				sep = "||"
				headerRow = false
			}
			cells := splitTableRow(trim)
			for i, c := range cells {
				cells[i] = jiraInline(c)
			}
			out = append(out, sep+strings.Join(cells, sep)+sep)
		case strings.HasPrefix(trim, "### "):
			out = append(out, "h3. "+jiraInline(strings.TrimPrefix(trim, "### ")))
		case strings.HasPrefix(trim, "## "):
			out = append(out, "h2. "+jiraInline(strings.TrimPrefix(trim, "## ")))
		case strings.HasPrefix(trim, "# "):
			out = append(out, "h1. "+jiraInline(strings.TrimPrefix(trim, "# ")))
		case trim == "---":
			out = append(out, "----")
		case strings.HasPrefix(trim, "- "):
			out = append(out, "* "+jiraInline(strings.TrimPrefix(trim, "- ")))
		case orderedItem(trim) != "":
			out = append(out, "# "+jiraInline(orderedItem(trim)))
		case strings.HasPrefix(trim, "> "):
			out = append(out, "bq. "+jiraInline(strings.TrimPrefix(trim, "> ")))
		case trim == ">":
			continue
		default:
			if alt, src, ok := parseMDImage(trim); ok {
				out = append(out, jiraImage(alt, src))
				continue
			}
			out = append(out, jiraInline(line))
		}
	}
	if fenceLen > 0 {
		out = append(out, "{noformat}")
	}
	return strings.Join(out, "\n")
}

// orderedItem returns the text of a "N. text" list line, or "".
func orderedItem(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && strings.HasPrefix(line[i:], ". ") {
		return line[i+2:]
	}
	return ""
}

// jiraImage embeds only an absolute web image; a data: URI or an app-relative
// path cannot be fetched by Jira, so it is named instead.
func jiraImage(alt, src string) string {
	if absoluteURI(src) && (strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://")) {
		return "!" + strings.NewReplacer("!", "%21", "|", "%7C").Replace(src) + "!"
	}
	return "_Screenshot: " + jiraEscape(alt) + " (attach the image to the ticket)_"
}

// jiraInline converts inline code and bold, escaping everything else. Code
// spans are set aside first so bold around them ("**Proof of `x`:**") still
// pairs up and nothing inside them is read as bold.
func jiraInline(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	var spans []string
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			break
		}
		b.WriteString(s[:i])
		code := s[i+1 : i+1+j]
		if code == "" {
			b.WriteString("``")
		} else {
			b.WriteString("\x00" + string(rune('A'+len(spans)%26)) + "\x00")
			spans = append(spans, "{{"+jiraEscape(code)+"}}")
		}
		s = s[i+2+j:]
	}
	b.WriteString(s)
	out := jiraBold(b.String())
	for i, span := range spans {
		out = strings.Replace(out, "\x00"+string(rune('A'+i%26))+"\x00", span, 1)
	}
	return out
}

// jiraBold turns **x** into *x* after escaping the plain text.
func jiraBold(s string) string {
	s = jiraEscape(s)
	for {
		i := strings.Index(s, "**")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "**")
		if j < 0 {
			return s
		}
		j += i + 2
		s = s[:i] + "*" + s[i+2:j] + "*" + s[j+2:]
	}
}

var jiraEscaper = strings.NewReplacer("{", `\{`, "}", `\}`, "[", `\[`, "]", `\]`)

func jiraEscape(s string) string { return jiraEscaper.Replace(s) }
