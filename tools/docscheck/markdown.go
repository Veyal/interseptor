package main

import (
	"bytes"
	"fmt"
	"html"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Markdown is rendered with goldmark configured to match the kramdown GFM
// output of the former Jekyll site: GFM extensions, typographic characters
// written as characters (kramdown entity_output: as_char), raw HTML passed
// through (the sources are trusted repository documents), and kramdown's
// header ids.

var liquidRaw = regexp.MustCompile(`\{%-?\s*(?:end)?raw\s*-?%\}`)

// stripLiquidRaw removes Liquid raw tags left from the Jekyll era; there is no
// template language in Markdown, so {{name}} renders literally.
func stripLiquidRaw(s string) string { return liquidRaw.ReplaceAllString(s, "") }

var markdownEngine = sync.OnceValue(func() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.NewTypographer(extension.WithTypographicSubstitutions(map[extension.TypographicPunctuation]string{
				extension.LeftSingleQuote:  "‘",
				extension.RightSingleQuote: "’",
				extension.LeftDoubleQuote:  "“",
				extension.RightDoubleQuote: "”",
				extension.EnDash:           "–",
				extension.EmDash:           "—",
				extension.Ellipsis:         "…",
				extension.LeftAngleQuote:   "«",
				extension.RightAngleQuote:  "»",
				extension.Apostrophe:       "’",
			})),
		),
		goldmark.WithRendererOptions(
			ghtml.WithUnsafe(),
			renderer.WithNodeRenderers(util.Prioritized(siteRenderer{}, 100)),
		),
	)
})

// siteRenderer overrides goldmark's HTML for headings (id and self-link),
// code blocks (highlighted code cards), tables (a labelled scroll region) and
// GitHub alert blockquotes (callouts).
type siteRenderer struct{}

func (siteRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHeading, renderHeading)
	reg.Register(ast.KindFencedCodeBlock, renderCodeBlock)
	reg.Register(ast.KindCodeBlock, renderCodeBlock)
	reg.Register(extast.KindTable, renderTable)
	reg.Register(extast.KindTaskCheckBox, renderTaskCheckBox)
	reg.Register(ast.KindBlockquote, renderBlockquote)
}

// codeLanguage is the first word of a fence info string, lowercased and
// limited to characters that are safe in a class name; "text" when empty.
func codeLanguage(info string) string {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return "text"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(fields[0]) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("+#._-", r) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "text"
	}
	return b.String()
}

// renderCodeBlock writes a fenced or indented code block as a code card with
// highlighted, escaped contents. Raw HTML <pre> blocks are HTML blocks and
// never reach this renderer.
func renderCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lang := "text"
	if fenced, ok := node.(*ast.FencedCodeBlock); ok && fenced.Info != nil {
		lang = codeLanguage(string(fenced.Info.Segment.Value(source)))
	}
	var code strings.Builder
	lines := node.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		code.Write(line.Value(source))
	}
	name := html.EscapeString(lang)
	_, _ = w.WriteString(`<div class="code-card" data-lang="` + name + `"><div class="code-bar"><span class="code-lang">` + name +
		`</span><button class="code-copy" type="button" hidden>Copy</button></div><pre class="code"><code class="language-` + name + `">`)
	_, _ = w.WriteString(highlightCode(lang, code.String()))
	_, _ = w.WriteString("</code></pre></div>\n")
	return ast.WalkSkipChildren, nil
}

// tableLabelAttr holds the accessible name labelTables chose for a table; it is
// never rendered as an attribute.
const tableLabelAttr = "docscheck-label"

// labelTables names each table "Table: <first header cell>". When a name
// repeats on the page, each use adds the nearest preceding h2 or h3, and a
// counter only if that still repeats.
func labelTables(doc ast.Node, src []byte) {
	type table struct {
		node                ast.Node
		name, section, part string
	}
	var tables []*table
	section := ""
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Heading:
			if v.Level == 2 || v.Level == 3 {
				section = displayText(v, src)
			}
			return ast.WalkSkipChildren, nil
		case *extast.Table:
			name := "Table"
			if header := v.FirstChild(); header != nil && header.FirstChild() != nil {
				if text := displayText(header.FirstChild(), src); text != "" {
					name += ": " + text
				}
			}
			tables = append(tables, &table{node: v, name: name, section: section})
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	names := map[string]int{}
	for _, t := range tables {
		names[t.name]++
	}
	for _, t := range tables {
		if names[t.name] > 1 {
			t.part = t.section
		}
	}
	key := func(t *table) string { return t.name + "\x00" + t.part }
	repeats, seen := map[string]int{}, map[string]int{}
	for _, t := range tables {
		repeats[key(t)]++
	}
	for _, t := range tables {
		var parts []string
		if t.part != "" {
			parts = append(parts, t.part)
		}
		if k := key(t); repeats[k] > 1 {
			seen[k]++
			parts = append(parts, strconv.Itoa(seen[k]))
		}
		label := t.name
		if len(parts) > 0 {
			label += " (" + strings.Join(parts, ", ") + ")"
		}
		t.node.SetAttributeString(tableLabelAttr, []byte(label))
	}
}

// renderTable wraps a Markdown table in a focusable region named by
// labelTables, so a wide table scrolls without widening the page.
func renderTable(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</table></div>\n")
		return ast.WalkContinue, nil
	}
	label := attrString(node, tableLabelAttr)
	if label == "" {
		label = "Table"
	}
	_, _ = w.WriteString(`<div class="table-wrap" role="region" tabindex="0" aria-label="` + html.EscapeString(label) + `"><table`)
	if node.Attributes() != nil {
		ghtml.RenderAttributes(w, node, extension.TableAttributeFilter)
	}
	_, _ = w.WriteString(">\n")
	return ast.WalkContinue, nil
}

// renderTaskCheckBox writes a task list checkbox named after its item, so a
// screen reader announces what each box stands for.
func renderTaskCheckBox(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	label := "Task"
	if parent := node.Parent(); parent != nil {
		if text := truncateText(displayText(parent, source), 80); text != "" {
			label = text
		}
	}
	checked := ""
	if node.(*extast.TaskCheckBox).IsChecked {
		checked = `checked="" `
	}
	_, _ = w.WriteString(`<input ` + checked + `disabled="" type="checkbox" aria-label="` + html.EscapeString(label) + `"> `)
	return ast.WalkContinue, nil
}

// calloutKind names the GitHub alert type of a blockquote; it is set by
// markAlerts and never rendered as an attribute.
const calloutKind = "docscheck-callout"

var (
	alertMarker   = regexp.MustCompile(`(?i)^\[!(note|tip|important|warning|caution)\]$`)
	calloutTitles = map[string]string{"note": "Note", "tip": "Tip", "important": "Important", "warning": "Warning", "caution": "Caution"}
)

// markAlerts turns blockquotes whose first line is [!NOTE], [!TIP],
// [!IMPORTANT], [!WARNING] or [!CAUTION] into callouts and drops the marker.
func markAlerts(doc ast.Node, src []byte) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		quote, ok := n.(*ast.Blockquote)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		para, ok := quote.FirstChild().(*ast.Paragraph)
		if !ok || para.Lines().Len() == 0 {
			return ast.WalkContinue, nil
		}
		first := para.Lines().At(0)
		m := alertMarker.FindSubmatch(bytes.TrimSpace(first.Value(src)))
		if m == nil {
			return ast.WalkContinue, nil
		}
		for c := para.FirstChild(); c != nil; {
			text, ok := c.(*ast.Text)
			if !ok || text.Segment.Start >= first.Stop {
				break
			}
			next := c.NextSibling()
			para.RemoveChild(para, c)
			c = next
		}
		if para.ChildCount() == 0 {
			quote.RemoveChild(quote, para)
		}
		quote.SetAttributeString(calloutKind, strings.ToLower(string(m[1])))
		return ast.WalkContinue, nil
	})
}

// renderBlockquote writes a callout for a marked alert and a plain
// blockquote otherwise.
func renderBlockquote(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	kind := attrString(node, calloutKind)
	switch {
	case kind != "" && entering:
		_, _ = w.WriteString(`<aside class="callout callout-` + kind + `" role="note"><p class="callout-title">` + calloutTitles[kind] + "</p>\n")
	case kind != "":
		_, _ = w.WriteString("</aside>\n")
	case entering:
		_, _ = w.WriteString("<blockquote")
		if node.Attributes() != nil {
			ghtml.RenderAttributes(w, node, ghtml.BlockquoteAttributeFilter)
		}
		_, _ = w.WriteString(">\n")
	default:
		_, _ = w.WriteString("</blockquote>\n")
	}
	return ast.WalkContinue, nil
}

func renderHeading(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Heading)
	level := "0123456"[n.Level]
	if entering {
		_, _ = w.WriteString("<h")
		_ = w.WriteByte(level)
		if n.Attributes() != nil {
			ghtml.RenderAttributes(w, n, ghtml.HeadingAttributeFilter)
		}
		_ = w.WriteByte('>')
		return ast.WalkContinue, nil
	}
	if id := attrString(n, "id"); id != "" && n.Level >= 2 && n.Level <= 4 {
		escaped := html.EscapeString(id)
		_, _ = w.WriteString(` <a class="heading-anchor" href="#` + escaped + `" aria-label="Link to this section"><span aria-hidden="true">#</span></a>`)
	}
	_, _ = w.WriteString("</h")
	_ = w.WriteByte(level)
	_, _ = w.WriteString(">\n")
	return ast.WalkContinue, nil
}

type markdownInput struct {
	Source string            // repository path that relative links resolve against
	Paths  map[string]string // published source files and their site paths
	Base   string            // prefix for site paths: the base path, or an absolute site URL
	Inline bool              // keep a leading H1 and unwrap a lone paragraph
}

type markdownOutput struct {
	HTML        string
	Heading     string // leading H1 text, removed from HTML
	HeadingID   string
	Headings    []Heading // h2 and h3 in document order
	Description string    // first paragraph as plain text, at most 160 characters
	Links       []string  // final link and image destinations
	Sections    []searchSection
}

// searchSection is one indexed slice of a document: the introduction (no ID),
// then each top-level h2 or h3 with the raw Markdown below it.
type searchSection struct{ ID, Title, Text string }

func renderMarkdown(src []byte, in markdownInput) (markdownOutput, error) {
	src = []byte(stripLiquidRaw(string(src)))
	md := markdownEngine()
	doc := md.Parser().Parse(text.NewReader(src))
	assignHeadingIDs(doc, src)
	markAlerts(doc, src)
	labelTables(doc, src)
	out := markdownOutput{Sections: collectSections(doc, src)}
	if !in.Inline {
		out.Heading, out.HeadingID = extractLeadingH1(doc, src)
	}
	out.Headings = collectHeadings(doc, src)
	out.Description = firstParagraph(doc, src)
	out.Links = rewriteLinkNodes(doc, src, in)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return out, err
	}
	out.HTML = prefixSiteRootLinks(buf.String(), in.Base)
	if in.Inline {
		out.HTML = unwrapParagraph(out.HTML)
	}
	return out, nil
}

func unwrapParagraph(s string) string {
	s = strings.TrimSpace(s)
	inner, ok := strings.CutPrefix(s, "<p>")
	if inner, ok2 := strings.CutSuffix(inner, "</p>"); ok && ok2 && !strings.Contains(inner, "<p>") {
		return inner
	}
	return s
}

// searchSlug is kramdown's GFM header id: lowercase, drop everything except
// letters, marks, digits, connector punctuation, hyphens, spaces and tabs,
// then turn each space or tab into a hyphen. Spaces around removed
// punctuation therefore give a double hyphen ("Limits & safety" ->
// "limits--safety"), and nothing is trimmed.
func searchSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == ' ' || r == '\t':
			b.WriteByte('-')
		case r == '-' || unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || unicode.Is(unicode.Pc, r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// assignHeadingIDs numbers repeated ids as kramdown does: the second
// occurrence gets -1, the third -2.
func assignHeadingIDs(doc ast.Node, src []byte) {
	counter := map[string]int{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		id := searchSlug(inlineText(h, src))
		if id == "" {
			id = "section"
		}
		if count, seen := counter[id]; seen {
			counter[id] = count + 1
			id += "-" + strconv.Itoa(count+1)
		} else {
			counter[id] = 0
		}
		h.SetAttributeString("id", []byte(id))
		return ast.WalkSkipChildren, nil
	})
}

// inlineText is the text kramdown uses for a header id: text, code, entities
// and typographic characters, link text but not image alt text or HTML tags.
func inlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(parent ast.Node) {
		for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
			switch v := c.(type) {
			case *ast.Text:
				value := v.Value(src)
				if !v.IsRaw() {
					value = util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(value)))
				}
				b.Write(value)
				if v.SoftLineBreak() {
					b.WriteByte('\n')
				}
			case *ast.String:
				b.Write(v.Value)
			case *ast.AutoLink:
				b.Write(v.Label(src))
			case *ast.RawHTML, *ast.Image:
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

func displayText(n ast.Node, src []byte) string {
	return strings.Join(strings.Fields(inlineText(n, src)), " ")
}

func attrString(n ast.Node, name string) string {
	v, ok := n.AttributeString(name)
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case []byte:
		return string(t)
	case string:
		return t
	}
	return ""
}

func extractLeadingH1(doc ast.Node, src []byte) (string, string) {
	h, ok := doc.FirstChild().(*ast.Heading)
	if !ok || h.Level != 1 {
		return "", ""
	}
	heading, id := displayText(h, src), attrString(h, "id")
	doc.RemoveChild(doc, h)
	return heading, id
}

func collectHeadings(doc ast.Node, src []byte) []Heading {
	var headings []Heading
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		if h.Level == 2 || h.Level == 3 {
			headings = append(headings, Heading{Level: h.Level, ID: attrString(h, "id"), Text: displayText(h, src)})
		}
		return ast.WalkSkipChildren, nil
	})
	return headings
}

func firstParagraph(doc ast.Node, src []byte) string {
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if p, ok := n.(*ast.Paragraph); ok {
			if s := displayText(p, src); s != "" {
				return truncateText(s, 160)
			}
		}
	}
	return ""
}

// truncateText shortens s to at most max characters at a word boundary.
func truncateText(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	cut := string([]rune(s)[:max-1])
	if i := strings.LastIndex(cut, " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.") + "…"
}

func lineStart(src []byte, pos int) int { return bytes.LastIndexByte(src[:pos], '\n') + 1 }

func lineEnd(src []byte, pos int) int {
	if i := bytes.IndexByte(src[pos:], '\n'); i >= 0 {
		return pos + i
	}
	return len(src)
}

// collectSections splits the source at top-level h2 and h3 headings. Headings
// inside fenced code are not headings in the AST, so they never split.
func collectSections(doc ast.Node, src []byte) []searchSection {
	type mark struct {
		start, end int
		id, title  string
	}
	var marks []mark
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || (h.Level != 2 && h.Level != 3) || h.Lines().Len() == 0 {
			continue
		}
		first, last := h.Lines().At(0), h.Lines().At(h.Lines().Len()-1)
		marks = append(marks, mark{lineStart(src, first.Start), lineEnd(src, last.Stop), attrString(h, "id"), strings.TrimSpace(string(first.Value(src)))})
	}
	introEnd := len(src)
	if len(marks) > 0 {
		introEnd = marks[0].start
	}
	sections := []searchSection{{Text: string(src[:introEnd])}}
	for i, m := range marks {
		stop := len(src)
		if i+1 < len(marks) {
			stop = marks[i+1].start
		}
		sections = append(sections, searchSection{ID: m.id, Title: m.title, Text: string(src[m.end:stop])})
	}
	return sections
}

// docSitePaths maps each public guide source to its site path.
func docSitePaths() map[string]string {
	paths := make(map[string]string, len(publicDocs))
	for source, meta := range publicDocs {
		paths[source] = "/" + meta.Slug + "/"
	}
	return paths
}

// resolveLink turns a Markdown link target into its published URL. Fragments,
// external URLs and mail links are kept; site-root paths get the prefix;
// relative paths resolve against the source file and become the published
// page, a site asset, or the GitHub blob or tree URL.
func resolveLink(target, source string, paths map[string]string, prefix string) string {
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") ||
		strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
		return target
	}
	if strings.HasPrefix(target, "/") {
		return prefix + target
	}
	file, fragment, hasFragment := strings.Cut(target, "#")
	if hasFragment {
		fragment = "#" + fragment
	}
	resolved := path.Clean(path.Join(path.Dir(source), file))
	if sitePath, ok := paths[resolved]; ok {
		return prefix + sitePath + fragment
	}
	if strings.HasPrefix(resolved, assetsDir+"/") {
		return prefix + "/" + resolved + fragment
	}
	kind := "blob"
	if strings.HasSuffix(file, "/") || path.Ext(resolved) == "" {
		kind = "tree"
	}
	absolute := repositoryURL + "/" + kind + "/main/" + resolved
	if kind == "tree" && strings.HasSuffix(file, "/") {
		absolute += "/"
	}
	return absolute + fragment
}

func rewriteLinkNodes(doc ast.Node, src []byte, in markdownInput) []string {
	var links []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Link:
			v.Destination = []byte(resolveLink(string(v.Destination), in.Source, in.Paths, in.Base))
			links = append(links, string(v.Destination))
		case *ast.Image:
			v.Destination = []byte(resolveLink(string(v.Destination), in.Source, in.Paths, in.Base))
			v.SetAttributeString("loading", []byte("lazy"))
			v.SetAttributeString("decoding", []byte("async"))
			links = append(links, string(v.Destination))
		case *ast.AutoLink:
			links = append(links, string(v.URL(src)))
		}
		return ast.WalkContinue, nil
	})
	return links
}

var siteRootAttr = regexp.MustCompile(`(\s(?:href|src)=")(/[^"]*)"`)

// prefixSiteRootLinks adds the base path to site-root href and src values in
// raw HTML. Protocol-relative and already prefixed values are left alone;
// escaped code samples never match.
func prefixSiteRootLinks(s, base string) string {
	if !strings.HasPrefix(base, "/") {
		return s
	}
	return siteRootAttr.ReplaceAllStringFunc(s, func(match string) string {
		parts := siteRootAttr.FindStringSubmatch(match)
		target := parts[2]
		if strings.HasPrefix(target, "//") || target == base || strings.HasPrefix(target, base+"/") {
			return match
		}
		return parts[1] + base + target + `"`
	})
}

// rewriteLinksWith applies resolveLink to the inline links of Markdown text.
func rewriteLinksWith(body, source string, paths map[string]string, prefix string) string {
	return markdownLink.ReplaceAllStringFunc(body, func(link string) string {
		return "](" + resolveLink(link[2:len(link)-1], source, paths, prefix) + ")"
	})
}

// rewriteLinks gives Markdown text the links the site publishes.
func rewriteLinks(body, source string) string {
	return rewriteLinksWith(body, source, docSitePaths(), siteBase)
}

// validateTablePipes fails when a table row has an unescaped | inside a code
// span: GFM splits the cell there, so the guide must write \|.
func validateTablePipes(body string) error {
	var bad []string
	fenced := false
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		for _, span := range codeSpans(trimmed) {
			if strings.Contains(strings.ReplaceAll(span, `\|`, ""), "|") {
				bad = append(bad, fmt.Sprintf("line %d: `%s`", i+1, span))
			}
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("a | inside code in a table row splits the cell; write \\| instead:\n%s", strings.Join(bad, "\n"))
}

// codeSpans returns the contents of the backtick code spans on one line.
func codeSpans(line string) []string {
	var spans []string
	run := func(at int) int {
		end := at
		for end < len(line) && line[end] == '`' {
			end++
		}
		return end
	}
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		open := run(i)
		width, next := open-i, open
		for j := open; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			close := run(j)
			if close-j == width {
				spans = append(spans, line[open:j])
				next = close
				break
			}
			j = close
		}
		i = next
	}
	return spans
}
