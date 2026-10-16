package main

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func mustRender(t *testing.T, src string, in markdownInput) markdownOutput {
	t.Helper()
	out, err := renderMarkdown([]byte(src), in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func docInput(source string) markdownInput {
	return markdownInput{Source: source, Paths: docSitePaths(), Base: siteBase}
}

// Deep links and the search index depend on ids matching the ones kramdown's
// GFM parser generated for the Jekyll site.
func TestHeadingIDsMatchKramdownGFM(t *testing.T) {
	for heading, want := range map[string]string{
		"Limits & safety": "limits--safety",
		"Recommended — `go install` (uses the release tags)": "recommended--go-install-uses-the-release-tags",
		"The `flow` object":              "the-flow-object",
		"1. Scope & capture":             "1-scope--capture",
		"Client → proxy leg":             "client--proxy-leg",
		"HTTP/1.1 client, HTTP/2 origin": "http11-client-http2-origin",
		"`list_flows` and `get_flow`":    "list_flows-and-get_flow",
		"Don't panic":                    "dont-panic",
		"Before -- after":                "before--after",
		"See [the guide](workspace.md)":  "see-the-guide",
		"Café au lait":                   "café-au-lait",
		"*Emphasis* and **strong**":      "emphasis-and-strong",
		"Ampersand &amp; entity":         "ampersand--entity",
	} {
		out := mustRender(t, "## "+heading+"\n", docInput("docs/example.md"))
		if !strings.Contains(out.HTML, `<h2 id="`+want+`">`) {
			t.Errorf("heading %q rendered\n%s\nwant id %q", heading, out.HTML, want)
		}
	}
}

func TestSearchSlugMatchesHeadingIDs(t *testing.T) {
	for input, want := range map[string]string{
		"Limits & safety":    "limits--safety",
		"list_flows tool":    "list_flows-tool",
		"1. Scope & capture": "1-scope--capture",
	} {
		if got := searchSlug(input); got != want {
			t.Errorf("searchSlug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHeadingIDsDeduplicateLikeKramdown(t *testing.T) {
	out := mustRender(t, "# Settings\n\n## Settings\n\n## Examples\n\n### Examples\n\n## Examples\n", docInput("docs/example.md"))
	if out.HeadingID != "settings" {
		t.Fatalf("H1 id = %q, want settings", out.HeadingID)
	}
	last := -1
	for _, want := range []string{`<h2 id="settings-1">`, `<h2 id="examples">`, `<h3 id="examples-1">`, `<h2 id="examples-2">`} {
		at := strings.Index(out.HTML, want)
		if at < 0 || at < last {
			t.Fatalf("missing or out-of-order %s in\n%s", want, out.HTML)
		}
		last = at
	}
}

func TestHeadingsCarrySelfLinks(t *testing.T) {
	out := mustRender(t, "## Two\n\n### Three\n\n#### Four\n\n##### Five\n", docInput("docs/example.md"))
	for _, want := range []string{
		`<h2 id="two">Two <a class="heading-anchor" href="#two" aria-label="Link to this section"><span aria-hidden="true">#</span></a></h2>`,
		`<h3 id="three">Three <a class="heading-anchor" href="#three" aria-label="Link to this section"><span aria-hidden="true">#</span></a></h3>`,
		`<h4 id="four">Four <a class="heading-anchor" href="#four" aria-label="Link to this section"><span aria-hidden="true">#</span></a></h4>`,
		`<h5 id="five">Five</h5>`,
	} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %s in\n%s", want, out.HTML)
		}
	}
}

// The Jekyll site evaluated Liquid and silently dropped {{variables}} from
// the rendered guides. The generator has no template language in Markdown.
func TestLiquidTagsAreStrippedAndBracesStayLiteral(t *testing.T) {
	out := mustRender(t, "Variables use {% raw %}`{{token}}`{% endraw %} and resolve {{variables}} or `{{name}}`.\n", docInput("docs/collections.md"))
	if strings.Contains(out.HTML, "{%") || strings.Contains(out.HTML, "raw") {
		t.Fatalf("Liquid tags leaked into %s", out.HTML)
	}
	for _, want := range []string{"<code>{{token}}</code>", "resolve {{variables}}", "<code>{{name}}</code>"} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing literal %q in %s", want, out.HTML)
		}
	}
}

func TestLeadingH1MovesToPageHeading(t *testing.T) {
	out := mustRender(t, "# API & MCP\n\nIntro paragraph.\n\n## Usage\n", docInput("docs/api-and-mcp.md"))
	if out.Heading != "API & MCP" || out.HeadingID != "api--mcp" {
		t.Fatalf("heading = %q id = %q", out.Heading, out.HeadingID)
	}
	if strings.Contains(out.HTML, "<h1") {
		t.Fatalf("H1 left in content:\n%s", out.HTML)
	}
	if !strings.HasPrefix(out.HTML, "<p>Intro paragraph.</p>") {
		t.Fatalf("content does not start with the intro:\n%s", out.HTML)
	}
	late := mustRender(t, "Intro first.\n\n# Late title\n", docInput("docs/example.md"))
	if late.Heading != "" || !strings.Contains(late.HTML, "<h1") {
		t.Fatalf("only a leading H1 is extracted: heading=%q html=%s", late.Heading, late.HTML)
	}
}

func TestHeadingsListH2AndH3InOrder(t *testing.T) {
	out := mustRender(t, "# Title\n\n## Alpha\n\n### Beta `code`\n\n#### Gamma\n\n## Alpha\n", docInput("docs/example.md"))
	want := []Heading{{Level: 2, ID: "alpha", Text: "Alpha"}, {Level: 3, ID: "beta-code", Text: "Beta code"}, {Level: 2, ID: "alpha-1", Text: "Alpha"}}
	if !reflect.DeepEqual(out.Headings, want) {
		t.Fatalf("headings = %+v, want %+v", out.Headings, want)
	}
}

func TestDocLinksBecomeSiteOrRepositoryURLs(t *testing.T) {
	body := strings.Join([]string{
		"[cookbook](product/mcp-cookbook.md#recipe)",
		"[security](../SECURITY.md)",
		"[checks](../internal/checks/README.md)",
		"[examples](../examples/checks/)",
		"[PRDs](product/)",
		"[features](../website/pages/features.html#mobile)",
		"[external](https://example.com/guide)",
		"[local](#usage)",
		"[mail](mailto:security@example.com)",
		"[root](/compare/)",
		"![mark](../website/assets/mark.svg)",
	}, "\n\n")
	paths := docSitePaths()
	paths["website/pages/features.html"] = "/features/"
	out := mustRender(t, body, markdownInput{Source: "docs/api-and-mcp.md", Paths: paths, Base: siteBase})
	for _, want := range []string{
		`href="/interseptor/mcp-cookbook/#recipe"`,
		`href="https://github.com/Veyal/interseptor/blob/main/SECURITY.md"`,
		`href="https://github.com/Veyal/interseptor/blob/main/internal/checks/README.md"`,
		`href="https://github.com/Veyal/interseptor/tree/main/examples/checks/"`,
		`href="https://github.com/Veyal/interseptor/tree/main/docs/product/"`,
		`href="/interseptor/features/#mobile"`,
		`href="https://example.com/guide"`,
		`href="#usage"`,
		`href="mailto:security@example.com"`,
		`href="/interseptor/compare/"`,
		`src="/interseptor/website/assets/mark.svg"`,
	} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %s in\n%s", want, out.HTML)
		}
	}
	if !contains(out.Links, "https://github.com/Veyal/interseptor/blob/main/SECURITY.md") {
		t.Errorf("rendered links are not reported: %v", out.Links)
	}
}

func TestMarkdownPagesPrefixSiteRootLinks(t *testing.T) {
	src := "[guide](/getting-started/#install) and <a href=\"/compare/\">compare</a> <img src=\"/website/assets/mark.svg\" alt=\"\">\n\n" +
		"[cdn](//cdn.example.com/x.js) [ext](https://example.com/) <a href=\"/interseptor/features/\">done</a> [home](/)\n\n" +
		"```html\n<a href=\"/in-code/\">kept</a>\n```\n"
	out := mustRender(t, src, markdownInput{Source: "website/pages/compare.md", Paths: docSitePaths(), Base: siteBase})
	for _, want := range []string{
		`href="/interseptor/getting-started/#install"`,
		`href="/interseptor/compare/"`,
		`src="/interseptor/website/assets/mark.svg"`,
		`href="//cdn.example.com/x.js"`,
		`href="https://example.com/"`,
		`href="/interseptor/features/"`,
		`href="/interseptor/"`,
		`&lt;a href=&quot;/in-code/&quot;&gt;`,
	} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %s in\n%s", want, out.HTML)
		}
	}
	if strings.Contains(out.HTML, "/interseptor/interseptor/") {
		t.Fatalf("base path applied twice:\n%s", out.HTML)
	}
}

func TestDescriptionIsFirstParagraphAsPlainText(t *testing.T) {
	out := mustRender(t, "# Title\n\nUse **Interseptor** with [MCP](api-and-mcp.md) and `curl`.\n\nSecond.\n", docInput("docs/example.md"))
	if out.Description != "Use Interseptor with MCP and curl." {
		t.Fatalf("description = %q", out.Description)
	}
	long := mustRender(t, "# Title\n\n"+strings.Repeat("evidence ", 40)+"\n", docInput("docs/example.md"))
	if n := utf8.RuneCountInString(long.Description); n > 160 || !strings.HasSuffix(long.Description, "…") {
		t.Fatalf("long description (%d runes) = %q", n, long.Description)
	}
}

func TestTypographerWritesCharactersLikeKramdown(t *testing.T) {
	out := mustRender(t, "It's \"quoted\" -- and --- done...\n", docInput("docs/example.md"))
	if want := "<p>It’s “quoted” – and — done…</p>"; !strings.Contains(out.HTML, want) {
		t.Fatalf("typography = %s, want %s", out.HTML, want)
	}
}

func TestGFMExtensionsAreEnabled(t *testing.T) {
	out := mustRender(t, "| a | b |\n|---|---|\n| 1 | 2 |\n\n~~gone~~\n\n- [x] done\n\nSee https://example.com/docs for more.\n", docInput("docs/example.md"))
	for _, want := range []string{"<table>", "<del>gone</del>", `type="checkbox"`, `<a href="https://example.com/docs">`} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %s in\n%s", want, out.HTML)
		}
	}
}

func TestSearchSectionsUseRenderedHeadingIDs(t *testing.T) {
	items := searchSections(strings.Join([]string{
		"# Guide",
		"Intro text.",
		"## Examples",
		"First.",
		"```bash",
		"## not a heading",
		"```",
		"### Examples",
		"Nested.",
		"## Limits & safety",
		"Careful.",
	}, "\n"), pageMeta{"guide", "Guide", "current"})
	var urls []string
	for _, item := range items {
		urls = append(urls, item.URL)
		if strings.Contains(item.Title, "not a heading") {
			t.Fatalf("fenced comment indexed as a heading: %+v", item)
		}
		if item.Page != "Guide" {
			t.Errorf("item %q page = %q, want Guide", item.Title, item.Page)
		}
	}
	want := []string{"guide/", "guide/#examples", "guide/#examples-1", "guide/#limits--safety"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("urls = %v, want %v", urls, want)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// searchSections indexes one guide the way the site builder does.
func searchSections(body string, meta pageMeta) []SearchItem {
	out, err := renderMarkdown([]byte(body), docInput("docs/"+meta.Slug+".md"))
	if err != nil {
		panic(err)
	}
	return sectionItems(out.Sections, meta.Slug, meta.Title, "")
}

// GFM splits a table row at every unescaped pipe, even inside code, so a
// guide must write \| there (kramdown, the old renderer, did not split).
func TestTableCodePipesMustBeEscaped(t *testing.T) {
	for _, bad := range []string{
		"| Flag | Meaning |\n|---|---|\n| `--project <name|path>` | Open a project |\n",
		"| ``a|b`` | x |\n",
	} {
		if err := validateTablePipes(bad); err == nil || !strings.Contains(err.Error(), "line") {
			t.Errorf("validateTablePipes(%q) = %v, want a line-numbered error", bad, err)
		}
	}
	for _, ok := range []string{
		"| `--project <name\\|path>` | Open a project |\n",
		"Prose may show `a|b` outside a table.\n",
		"```text\n| `a|b` | fenced sample |\n```\n",
		"| plain | a|b cells are separate |\n",
	} {
		if err := validateTablePipes(ok); err != nil {
			t.Errorf("validateTablePipes(%q) = %v", ok, err)
		}
	}
}

func codeCard(lang, body string) string {
	return `<div class="code-card" data-lang="` + lang + `"><div class="code-bar"><span class="code-lang">` + lang +
		`</span><button class="code-copy" type="button" hidden>Copy</button></div><pre class="code"><code class="language-` + lang + `">` + body + "</code></pre></div>"
}

func TestCodeBlocksRenderAsHighlightedCards(t *testing.T) {
	src := "```Bash title=\"install\"\ncurl -s https://example.com/\n```\n\n```\n<plain> & \"text\"\n```\n\n    indented <b>\n\n<pre>raw <em>html</em></pre>\n"
	out := mustRender(t, src, docInput("docs/example.md"))
	for _, want := range []string{
		codeCard("bash", "curl "+sp("flag", "-s")+" "+sp("url", "https://example.com/")+"\n"),
		codeCard("text", "&lt;plain&gt; &amp; &quot;text&quot;\n"),
		codeCard("text", "indented &lt;b&gt;\n"),
		"<pre>raw <em>html</em></pre>",
	} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %s in\n%s", want, out.HTML)
		}
	}
	if strings.Count(out.HTML, `class="code-card"`) != 3 {
		t.Errorf("raw HTML <pre> must not become a code card:\n%s", out.HTML)
	}
}

func TestTablesAreWrappedInLabelledRegions(t *testing.T) {
	src := "| Flag `name` & role | Meaning |\n|---|---|\n| a | b |\n\n<table><tr><td>raw</td></tr></table>\n\n| | Empty |\n|---|---|\n| x | y |\n"
	out := mustRender(t, src, docInput("docs/example.md"))
	for _, want := range []string{
		`<div class="table-wrap" role="region" tabindex="0" aria-label="Table: Flag name &amp; role"><table>`,
		`<div class="table-wrap" role="region" tabindex="0" aria-label="Table"><table>`,
		"</table></div>",
		"<table><tr><td>raw</td></tr></table>",
	} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %s in\n%s", want, out.HTML)
		}
	}
	if strings.Count(out.HTML, `class="table-wrap"`) != 2 {
		t.Errorf("raw HTML tables must stay unwrapped:\n%s", out.HTML)
	}
}

func TestGitHubAlertsBecomeCallouts(t *testing.T) {
	for _, kind := range []string{"NOTE", "TIP", "IMPORTANT", "WARNING", "CAUTION"} {
		lower := strings.ToLower(kind)
		title := strings.ToUpper(lower[:1]) + lower[1:]
		out := mustRender(t, "> [!"+kind+"]\n> Keep **evidence** attached.\n", docInput("docs/example.md"))
		want := `<aside class="callout callout-` + lower + `" role="note"><p class="callout-title">` + title + "</p>\n<p>Keep <strong>evidence</strong> attached.</p>\n</aside>"
		if !strings.Contains(out.HTML, want) || strings.Contains(out.HTML, "[!") {
			t.Errorf("%s alert rendered\n%s\nwant\n%s", kind, out.HTML, want)
		}
	}
	separate := mustRender(t, "> [!warning]\n>\n> Second paragraph.\n", docInput("docs/example.md"))
	if want := `<aside class="callout callout-warning" role="note"><p class="callout-title">Warning</p>` + "\n<p>Second paragraph.</p>\n</aside>"; !strings.Contains(separate.HTML, want) {
		t.Errorf("marker paragraph alert rendered\n%s", separate.HTML)
	}
	plain := mustRender(t, "> Just a quote.\n\n> [!DANGER]\n> Not an alert type.\n\n> [!NOTE] text on the marker line\n", docInput("docs/example.md"))
	if strings.Contains(plain.HTML, "<aside") || strings.Count(plain.HTML, "<blockquote>") != 3 {
		t.Errorf("plain blockquotes must stay blockquotes:\n%s", plain.HTML)
	}
}

var tableLabel = regexp.MustCompile(`aria-label="(Table[^"]*)"`)

// A label used twice on a page names its section, then a counter if the
// section repeats too.
func TestRepeatedTableLabelsNameTheirSection(t *testing.T) {
	table := func(head string) string { return "| " + head + " | Meaning |\n|---|---|\n| a | b |\n\n" }
	src := "## Finding envelope\n\n" + table("Field") + "## Finding fields\n\n" + table("Field") +
		"### Notes `x`\n\n" + table("Field") + table("Field") + table("Other")
	out := mustRender(t, src, docInput("docs/example.md"))
	var got []string
	for _, m := range tableLabel.FindAllStringSubmatch(out.HTML, -1) {
		got = append(got, m[1])
	}
	want := []string{"Table: Field (Finding envelope)", "Table: Field (Finding fields)", "Table: Field (Notes x, 1)", "Table: Field (Notes x, 2)", "Table: Other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("table labels = %q\nwant %q", got, want)
	}
	early := mustRender(t, table("Field")+table("Field"), docInput("docs/example.md"))
	if got := tableLabel.FindAllStringSubmatch(early.HTML, -1); len(got) != 2 || got[0][1] != "Table: Field (1)" || got[1][1] != "Table: Field (2)" {
		t.Fatalf("tables before any heading = %q", got)
	}
}

func TestTaskCheckboxesAreLabelled(t *testing.T) {
	long := strings.Repeat("evidence words ", 12)
	out := mustRender(t, "- [ ] `list_findings` checked: no **duplicate**.\n- [x] Evidence attached\n- [ ] "+long+"\n", docInput("docs/example.md"))
	for _, want := range []string{
		`<input disabled="" type="checkbox" aria-label="list_findings checked: no duplicate."> `,
		`<input checked="" disabled="" type="checkbox" aria-label="Evidence attached"> `,
	} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %s in\n%s", want, out.HTML)
		}
	}
	m := regexp.MustCompile(`aria-label="(evidence words[^"]*)"`).FindStringSubmatch(out.HTML)
	if m == nil || utf8.RuneCountInString(m[1]) > 80 || !strings.HasSuffix(m[1], "…") {
		t.Fatalf("a long item label must be trimmed to about 80 characters: %q", m)
	}
}
