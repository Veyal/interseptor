package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The guard is tested on its own so a broken check can never reach a wipe of
// the file system root or the home directory.
func TestCheckOutDirRefusesDangerousTargets(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	for _, out := range []string{
		"", "/", home, filepath.Dir(home), root, filepath.Dir(root),
		filepath.Join(root, "docs"), filepath.Join(root, "website"), filepath.Join(root, "website", "out"), filepath.Join(root, ".git"),
	} {
		if err := checkOutDir(out, root, home); err == nil {
			t.Errorf("checkOutDir(%q) accepted a dangerous target", out)
		}
	}
	for _, out := range []string{filepath.Join(root, "_site"), filepath.Join(t.TempDir(), "site"), filepath.Join(home, "site")} {
		if err := checkOutDir(out, root, home); err != nil {
			t.Errorf("checkOutDir(%q) = %v", out, err)
		}
	}
}

func TestPrepareOutDirWipesOnlyPreviousBuilds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	out := filepath.Join(t.TempDir(), "site")
	if err := prepareOutDir(out, root); err != nil {
		t.Fatalf("missing directory: %v", err)
	}
	writeFixture(t, filepath.Join(out, "stale", "index.html"), "old")
	if err := prepareOutDir(out, root); err != nil {
		t.Fatalf("previous build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "stale")); !os.IsNotExist(err) {
		t.Fatal("a previous build was not wiped")
	}
	if _, err := os.Stat(filepath.Join(out, buildMarker)); err != nil {
		t.Fatal("prepared directory lacks the build marker")
	}
	if err := prepareOutDir(t.TempDir(), root); err != nil {
		t.Fatalf("empty directory: %v", err)
	}
	unmarked := t.TempDir()
	writeFixture(t, filepath.Join(unmarked, "keep.txt"), "mine")
	if err := prepareOutDir(unmarked, root); err == nil {
		t.Fatal("a non-empty directory without a marker was accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(unmarked, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareOutDir(link, root); err == nil {
		t.Fatal("a symbolic link was accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	writeFixture(t, file, "x")
	if err := prepareOutDir(file, root); err == nil {
		t.Fatal("a file was accepted")
	}
	if data, err := os.ReadFile(filepath.Join(unmarked, "keep.txt")); err != nil || string(data) != "mine" {
		t.Fatalf("a refused directory was modified: %q, %v", data, err)
	}
}

func readSiteFile(t *testing.T, site, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(site, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// tagAttrValue returns attribute attr of the first tag matched by pattern whose
// key attribute equals value, whatever the attribute order.
func tagAttrValue(doc string, pattern *regexp.Regexp, key, value, attr string) string {
	for _, tag := range pattern.FindAllString(doc, -1) {
		attrs := map[string]string{}
		for _, a := range tagAttr.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(a[1])] = a[2]
		}
		if strings.EqualFold(attrs[key], value) {
			return attrs[attr]
		}
	}
	return ""
}

// The integration test checks structure and engine output only, so templates
// and page markup can change without breaking it.
func TestBuildSiteFromRepository(t *testing.T) {
	site := filepath.Join(t.TempDir(), "site")
	if err := buildSite(repoRoot, site); err != nil {
		t.Fatal(err)
	}
	if err := validateBuiltSite(site, siteBase); err != nil {
		t.Fatal(err)
	}
	if err := validateBuiltLLMS(site, repoRoot); err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(site, func(file string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(file) != ".html" {
			return err
		}
		data, err := os.ReadFile(file)
		if n := len(h1Open.FindAllIndex(data, -1)); err == nil && n != 1 {
			t.Errorf("%s has %d <h1> elements, want exactly one", file, n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	guide := readSiteFile(t, site, "getting-started/index.html")
	if got := tagAttrValue(guide, linkTag, "rel", "canonical", "href"); got != "https://veyal.github.io/interseptor/getting-started/" {
		t.Errorf("getting-started canonical = %q", got)
	}
	if want := `<h2 id="install">Install <a class="heading-anchor" href="#install" aria-label="Link to this section">`; !strings.Contains(guide, want) {
		t.Errorf("getting-started lacks the rendered heading %s", want)
	}
	if !strings.Contains(readSiteFile(t, site, "index.html"), `id="capabilities"`) {
		t.Error(`landing page lacks its id="capabilities" section`)
	}
	if !strings.Contains(readSiteFile(t, site, "features/index.html"), `id="intercepting-proxy"`) {
		t.Error("feature index lacks the anchor search results link to")
	}
	notFound := readSiteFile(t, site, "404.html")
	if robots := tagAttrValue(notFound, metaTag, "name", "robots", "content"); !strings.Contains(robots, "noindex") || strings.Contains(readSiteFile(t, site, "sitemap.xml"), "404.html") {
		t.Errorf("the 404 page must be noindex (robots %q) and outside the sitemap", robots)
	}
	var items []SearchItem
	if err := json.Unmarshal([]byte(readSiteFile(t, site, searchFile)), &items); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Page == "" {
			t.Fatalf("search item without a page: %+v", item)
		}
		if strings.Contains(item.Text, " --- ") || strings.Contains(item.Text, "|") {
			t.Errorf("search item %s keeps table structure: %q", item.URL, item.Text)
		}
		if item.URL == "getting-started/#install" && item.Page == "Getting started" && item.Group == "Start here" {
			found = true
		}
	}
	if !found {
		t.Error("search index lacks getting-started/#install with its page and group")
	}
	for _, name := range []string{".nojekyll", "robots.txt"} {
		if _, err := os.Stat(filepath.Join(site, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	assets := filepath.Join(repoRoot, filepath.FromSlash(assetsDir))
	if err := filepath.WalkDir(assets, func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, file)
		if _, err := os.Stat(filepath.Join(site, rel)); err != nil {
			t.Errorf("asset %s was not copied", rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := buildSite(repoRoot, site); err != nil {
		t.Fatalf("rebuilding into a previous build: %v", err)
	}
}

func TestValidateBuiltSiteRequiresDocumentMetadata(t *testing.T) {
	for name, mutate := range map[string]func(string) string{
		"lang":  func(s string) string { return strings.Replace(s, ` lang="en"`, "", 1) },
		"title": func(s string) string { return strings.Replace(s, "<title>Fixture</title>", "<title> </title>", 1) },
		"description": func(s string) string {
			return strings.Replace(s, `<meta name="description" content="Fixture page.">`, "", 1)
		},
		"canonical": func(s string) string {
			return strings.Replace(s, `<link rel="canonical" href="https://veyal.github.io/interseptor/">`, "", 1)
		},
		"no h1":  func(s string) string { return strings.Replace(s, "<h1>Fixture</h1>", "", 1) },
		"two h1": func(s string) string { return strings.Replace(s, "</body>", "<h1>Again</h1></body>", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			site := builtSiteFixture(t)
			writeFixture(t, filepath.Join(site, "index.html"), mutate(fixtureDocument("")))
			if err := validateBuiltSite(site, siteBase); err == nil {
				t.Fatalf("a page with %s missing was accepted", name)
			}
		})
	}
}

func TestValidateBuiltSiteChecksSearchAndSitemapTargets(t *testing.T) {
	site := builtSiteFixture(t)
	writeFixture(t, filepath.Join(site, filepath.FromSlash(searchFile)), `[{"title":"Gone","text":"","url":"features/#gone","page":"Feature index","group":""}]`)
	if err := validateBuiltSite(site, siteBase); err == nil {
		t.Fatal("a search result pointing at a missing heading was accepted")
	}
	site = builtSiteFixture(t)
	writeFixture(t, filepath.Join(site, "sitemap.xml"), string(sitemapXML([]*Page{{Path: "/missing/", Canonical: "https://veyal.github.io/interseptor/missing/"}})))
	if err := validateBuiltSite(site, siteBase); err == nil {
		t.Fatal("a sitemap URL without a page was accepted")
	}
}

// The Jekyll site ran Liquid over the guides and printed nothing where a guide
// showed a {{variable}}.
func TestRenderedGuidesKeepDoubleBraces(t *testing.T) {
	for source, wants := range map[string][]string{
		"docs/api-and-mcp.md": {"<code>{{variables}}</code>"},
		"docs/collections.md": {"<code>{{token}}</code>", "<code>{{name}}</code>", "<code>{{variable}}</code>", "<code>{{references}}</code>"},
	} {
		body, err := readDoc(repoRoot, source)
		if err != nil {
			t.Fatal(err)
		}
		out, err := renderMarkdown([]byte(body), docInput(source))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(out.HTML, want) {
				t.Errorf("%s renders without %s", source, want)
			}
		}
	}
}

func TestCanonicalDocsUseNoLiquid(t *testing.T) {
	for source := range publicDocs {
		data, err := os.ReadFile(filepath.Join(repoRoot, source))
		if err != nil {
			t.Fatal(err)
		}
		if liquidRaw.Match(data) {
			t.Errorf("%s still has Liquid raw tags; Markdown is not templated", source)
		}
	}
}

func TestHTMLPagesPrefixSiteRootLinks(t *testing.T) {
	got := prefixSiteRootLinks(`<a href="/compare/">a</a><a href="/interseptor/features/">b</a><img src="/website/assets/mark.svg"><a href="//cdn.example.com/">c</a><a href="https://example.com/">d</a>`, siteBase)
	want := `<a href="/interseptor/compare/">a</a><a href="/interseptor/features/">b</a><img src="/interseptor/website/assets/mark.svg"><a href="//cdn.example.com/">c</a><a href="https://example.com/">d</a>`
	if got != want {
		t.Fatalf("prefixSiteRootLinks =\n%s\nwant\n%s", got, want)
	}
}

func TestWordCountSkipsCodeCardChrome(t *testing.T) {
	card := codeCard("bash", "curl "+sp("flag", "-s")+"\n")
	if got := countWords("<p>Two words</p>" + card); got != 4 {
		t.Fatalf("countWords = %d, want 4 (the language label and Copy button are not reading text)", got)
	}
}

// gitRepo makes a repository with one commit of docs/guide.md dated 2026-01-02.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "docs", "guide.md"), "# Guide\n")
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "docs/guide.md"},
		{"-c", "user.name=Docs", "-c", "user.email=docs@example.com", "commit", "-q", "-m", "docs: add guide"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-01-02T10:00:00Z", "GIT_COMMITTER_DATE=2026-01-02T10:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestCommitDatesComeFromGitHistory(t *testing.T) {
	repo := gitRepo(t)
	if got := commitDates(repo, []string{"docs/guide.md", "docs/missing.md"}); got["docs/guide.md"] != "2026-01-02" || got["docs/missing.md"] != "" {
		t.Fatalf("commitDates = %v", got)
	}
	if got := commitDates(t.TempDir(), []string{"docs/guide.md"}); got["docs/guide.md"] != "" {
		t.Fatalf("outside a repository the date must be empty, got %v", got)
	}
	shallow := filepath.Join(t.TempDir(), "shallow")
	if out, err := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+repo, shallow).CombinedOutput(); err != nil {
		t.Fatalf("shallow clone: %v\n%s", err, out)
	}
	if got := commitDates(shallow, []string{"docs/guide.md"}); got["docs/guide.md"] != "" {
		t.Fatalf("a shallow clone has no reliable history, got %v", got)
	}
}

func TestRenderedPagesCarryUpdatedAndSitePages(t *testing.T) {
	b, err := newSiteBuilder(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := b.render()
	if err != nil {
		t.Fatal(err)
	}
	want := commitDates(repoRoot, []string{"docs/getting-started.md"})["docs/getting-started.md"]
	var listed []*Page
	for _, rp := range pages {
		p := rp.Page
		switch {
		case p.Path == "/getting-started/" && p.Updated != want:
			t.Errorf("getting-started Updated = %q, want %q", p.Updated, want)
		case p.Kind != "docs" && p.Updated != "":
			t.Errorf("%s is not a guide but has Updated %q", p.Path, p.Updated)
		}
		if !p.NoIndex && p.Kind != "notfound" {
			listed = append(listed, p)
		}
	}
	if len(b.site.Pages) != len(listed) {
		t.Fatalf("Site.Pages has %d pages, want the %d indexable ones", len(b.site.Pages), len(listed))
	}
	for i, link := range b.site.Pages {
		if i > 0 && b.site.Pages[i-1].Path >= link.Path {
			t.Fatalf("Site.Pages is not sorted by path at %s", link.Path)
		}
		if link.Path == "/404.html" || link.Title == "" || link.Summary == "" || link.URL != siteBase+link.Path {
			t.Fatalf("Site.Pages entry %+v", link)
		}
		if link.Path == "/getting-started/" && link.Summary != llmsDescriptions["getting-started"] {
			t.Fatalf("Summary = %q, want the page description", link.Summary)
		}
	}
}

func TestMarkdownImagesLoadLazily(t *testing.T) {
	out := mustRender(t, "![Captured requests](../website/assets/img/social-card.png)\n", docInput("docs/example.md"))
	want := `<img src="/interseptor/website/assets/img/social-card.png" alt="Captured requests" loading="lazy" decoding="async">`
	if !strings.Contains(out.HTML, want) {
		t.Fatalf("image rendered %s, want %s", out.HTML, want)
	}
}
