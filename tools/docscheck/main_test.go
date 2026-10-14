package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReleaseMetadataUsesPublishedChangelogEntry(t *testing.T) {
	root := t.TempDir()
	changelog := "# Changelog\n\n## [Unreleased]\n\nPending work.\n\n## [2.1.0] - 2026-09-08\n\nCurrent release.\n\n## [2.0.10] - 2026-09-06\n"
	if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "2.1.0" || got.Date != "2026-09-08" {
		t.Fatalf("published release metadata = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte("## [Unreleased]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRelease(root); err == nil {
		t.Fatal("missing published release must not produce a misleading version badge")
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		args    []string
		command command
		wantErr bool
	}{
		{[]string{"generate"}, command{name: "generate", root: "."}, false},
		{[]string{"generate", "/repo"}, command{name: "generate", root: "/repo"}, false},
		{[]string{"check"}, command{name: "check", root: "."}, false},
		{[]string{"check-site", "_site"}, command{name: "check-site", root: ".", site: "_site"}, false},
		{[]string{"check-site", "_site", "/repo"}, command{name: "check-site", root: "/repo", site: "_site"}, false},
		{[]string{"--write"}, command{name: "generate", root: "."}, false},
		{[]string{"build", "_site"}, command{name: "build", root: ".", out: "_site"}, false},
		{[]string{"build", "_site", "/repo"}, command{name: "build", root: "/repo", out: "_site"}, false},
		{[]string{"serve"}, command{name: "serve", root: ".", addr: "127.0.0.1:4000"}, false},
		{[]string{"serve", "-addr", "127.0.0.1:4100", "/repo"}, command{name: "serve", root: "/repo", addr: "127.0.0.1:4100"}, false},
		{nil, command{}, true},
		{[]string{"generate", "one", "two"}, command{}, true},
		{[]string{"check-site"}, command{}, true},
		{[]string{"build"}, command{}, true},
		{[]string{"serve", "-port", "4000"}, command{}, true},
		{[]string{"serve", "a", "b"}, command{}, true},
		{[]string{"wat"}, command{}, true},
	}
	for _, tt := range tests {
		got, err := parseCommand(tt.args)
		if (err != nil) != tt.wantErr {
			t.Fatalf("parseCommand(%q) error = %v", tt.args, err)
		}
		if err == nil && got != tt.command {
			t.Fatalf("parseCommand(%q) = %#v, want %#v", tt.args, got, tt.command)
		}
	}
}

func TestParseFeaturesAndValidate(t *testing.T) {
	canonical := "- **One** details\n- **Two** details\n"
	features, err := parseFeatures("- number: \"01\"\n  id: one\n  title: One\n  text: first\n- number: \"02\"\n  id: two\n  title: Two\n  text: second\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFeatures(features, canonical); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFeaturesRejectsSemanticDrift(t *testing.T) {
	features := []Feature{
		{Number: "01", ID: "one", Title: "One", Text: "text"},
		{Number: "02", ID: "two", Title: "Two", Text: "text"},
	}
	for _, canonical := range []string{
		"- **Renamed** details\n- **Two** details\n",
		"- **Two** details\n- **One** details\n",
	} {
		if err := validateFeatures(features, canonical); err == nil {
			t.Fatalf("expected semantic drift error for %q", canonical)
		}
	}
}

func TestValidateFeaturesRejectsDuplicateIDsAndNumbers(t *testing.T) {
	features := []Feature{{Number: "01", ID: "same", Title: "One", Text: "text"}, {Number: "01", ID: "same", Title: "Two", Text: "text"}}
	if err := validateFeatures(features, "- **One**\n- **Two**\n"); err == nil {
		t.Fatal("expected duplicate validation error")
	}
}

func TestFeatureGuidesResolveToPublishedPages(t *testing.T) {
	for _, link := range []string{"", "/missing/", "https://example.com/guide"} {
		if err := validateFeatureGuides([]Feature{{ID: "example", Link: link}}); err == nil {
			t.Errorf("feature guide %q must resolve to a public documentation page", link)
		}
	}
	if err := validateFeatureGuides([]Feature{{ID: "example", Link: "/findings-and-reporting/#revision-history-and-recovery"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRewriteLinksClassifiesTargetsAfterResolvingSourcePath(t *testing.T) {
	body := strings.Join([]string{
		"[cookbook](product/mcp-cookbook.md#recipe)",
		"[security](../SECURITY.md)",
		"[checks](../internal/checks/README.md)",
		"[examples](../examples/checks/)",
		"[PRDs](product/)",
	}, "\n")
	got := rewriteLinks(body, "docs/api-and-mcp.md")
	for _, want := range []string{
		`[cookbook](/interseptor/mcp-cookbook/#recipe)`,
		`[security](https://github.com/Veyal/interseptor/blob/main/SECURITY.md)`,
		`[checks](https://github.com/Veyal/interseptor/blob/main/internal/checks/README.md)`,
		`[examples](https://github.com/Veyal/interseptor/tree/main/examples/checks/)`,
		`[PRDs](https://github.com/Veyal/interseptor/tree/main/docs/product/)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rewriteLinks() missing %q\n%s", want, got)
		}
	}
}

func TestSearchSectionsIndexesHeadingsAndReadableBody(t *testing.T) {
	items := searchSections(strings.Join([]string{
		"# Proxy and TLS",
		"Intro text.",
		"## Proxy authentication",
		"Use a full API key as the password.",
		"```text",
		"Proxy-Authorization: secret-value",
		"```",
		"### Browser prompt",
		"The realm is Interseptor.",
	}, "\n"), pageMeta{"proxy-and-tls", "Proxy and TLS", "current"})

	assertItem := func(title, url, text string) {
		t.Helper()
		for _, item := range items {
			if item.Title == title && item.URL == url {
				if !strings.Contains(item.Text, text) {
					t.Fatalf("search item %q text = %q, want %q", title, item.Text, text)
				}
				if strings.Contains(item.Text, "secret-value") {
					t.Fatalf("search item %q indexed fenced example content", title)
				}
				return
			}
		}
		t.Fatalf("missing search item %q at %q: %+v", title, url, items)
	}
	assertItem("Proxy authentication", "proxy-and-tls/#proxy-authentication", "full API key")
	assertItem("Browser prompt", "proxy-and-tls/#browser-prompt", "realm is Interseptor")
}

func TestSearchableTextDropsTableEscapes(t *testing.T) {
	if got := searchableText("| `type must be text\\|flow\\|image` | Fix it |"); got != "type must be text flow image Fix it" {
		t.Fatalf("searchableText = %q", got)
	}
}

// Search excerpts must not show table structure: delimiter rows and pipes.
func TestSearchableTextDropsTableStructure(t *testing.T) {
	in := "Intro.\n\n| Field | Meaning |\n|---|:---:|\n| `a\\|b` | x |\n|-|-|\n---\n\nAfter   the table.\n"
	if got, want := searchableText(in), "Intro. Field Meaning a b x After the table."; got != want {
		t.Fatalf("searchableText = %q, want %q", got, want)
	}
}

func TestSearchableTextCutsAtACharacterBoundary(t *testing.T) {
	got := searchableText(strings.Repeat("a", 1199) + "—b")
	if !utf8.ValidString(got) || len(got) > 1200 {
		t.Fatalf("truncated excerpt is %d bytes, valid UTF-8 %v", len(got), utf8.ValidString(got))
	}
}

func TestSearchSlugMatchesKramdownPunctuationIDs(t *testing.T) {
	for input, want := range map[string]string{
		"Limits & safety":                       "limits--safety",
		"Client → proxy leg":                    "client--proxy-leg",
		"The hook API — internal/plugin":        "the-hook-api--internalplugin",
		"HTTP/1.1 client, HTTP/2 origin":        "http11-client-http2-origin",
		"Recipe 4 — Close out (engagement end)": "recipe-4--close-out-engagement-end",
	} {
		if got := searchSlug(input); got != want {
			t.Errorf("searchSlug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPublicDocumentationIncludesOperatorAndReportingGuides(t *testing.T) {
	for _, source := range []string{
		"docs/proxy-and-tls.md",
		"docs/findings-and-reporting.md",
		"docs/cli-reference.md",
		"docs/projects-and-data.md",
		"docs/mobile-testing.md",
		"docs/troubleshooting.md",
		"docs/workspace.md",
		"docs/settings.md",
	} {
		if _, ok := publicDocs[source]; !ok {
			t.Errorf("publicDocs missing %s", source)
		}
	}
}

func TestDocumentationSearchLoadsPublishedIndexPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "website", "assets", "search.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "website/data/search.json") {
		t.Fatal("documentation search does not fetch the published search index path")
	}
}

// fixtureDocument wraps body in the metadata check-site requires.
func fixtureDocument(body string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Fixture</title>` +
		`<meta name="description" content="Fixture page."><link rel="canonical" href="https://veyal.github.io/interseptor/">` +
		`</head><body><h1>Fixture</h1>` + body + `</body></html>`
}

func writeFixture(t *testing.T, file, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func builtSiteFixture(t *testing.T) string {
	t.Helper()
	site := t.TempDir()
	for _, name := range expectedSitePages() {
		file := filepath.Join(site, name)
		if !strings.HasSuffix(name, ".html") {
			file = filepath.Join(file, "index.html")
		}
		writeFixture(t, file, fixtureDocument(`<a href="/interseptor/features/">Features</a><h2 id="scanner">Scanner</h2>`))
	}
	writeFixture(t, filepath.Join(site, "assets", "site.css"), "")
	for _, asset := range []string{"website/assets/site.css", "website/assets/site.js", "website/assets/search.js", "website/assets/mark.svg"} {
		writeFixture(t, filepath.Join(site, filepath.FromSlash(asset)), "[]")
	}
	writeFixture(t, filepath.Join(site, filepath.FromSlash(searchFile)), `[{"title":"Scanner","text":"t","url":"features/#scanner","page":"Feature index","group":"Start here"}]`)
	writeFixture(t, filepath.Join(site, "sitemap.xml"), string(sitemapXML([]*Page{{Path: "/features/", Canonical: "https://veyal.github.io/interseptor/features/"}})))
	writeFixture(t, filepath.Join(site, "robots.txt"), robotsTxt(siteOrigin+siteBase))
	return site
}

func TestValidateBuiltSiteAcceptsBasePrefixedTargets(t *testing.T) {
	site := builtSiteFixture(t)
	links := `<link href="/interseptor/assets/site.css"><a href="/interseptor/features/">Features</a><a href="/interseptor/features/#scanner">Scanner</a>`
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte(fixtureDocument(links)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateBuiltSite(site, "/interseptor"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBuiltSiteRejectsInvalidTargets(t *testing.T) {
	for _, href := range []string{"/features/", "/interseptor/assets/missing.css"} {
		t.Run(href, func(t *testing.T) {
			site := builtSiteFixture(t)
			if err := os.WriteFile(filepath.Join(site, "index.html"), []byte(fixtureDocument(`<a href="`+href+`">bad</a>`)), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validateBuiltSite(site, "/interseptor"); err == nil {
				t.Fatalf("expected invalid target error for %s", href)
			}
		})
	}
}

func TestValidateBuiltSiteRequiresSearchIndex(t *testing.T) {
	site := builtSiteFixture(t)
	if err := os.Remove(filepath.Join(site, "website", "data", "search.json")); err != nil {
		t.Fatal(err)
	}
	if err := validateBuiltSite(site, "/interseptor"); err == nil || !strings.Contains(err.Error(), "website/data/search.json") {
		t.Fatalf("validateBuiltSite without search index = %v, want missing search asset", err)
	}
}

func TestValidateProjectLinksRejectsDeadBlobAndAcceptsLive(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := strings.Join([]string{
		"[readme](https://github.com/Veyal/interseptor/blob/main/README.md)",
		"[docs](https://github.com/Veyal/interseptor/tree/main/docs/)",
		"[external](https://example.com/does-not-exist)",
		"[other repo](https://github.com/golang/go/blob/master/README.md)",
		"```",
		"[fenced](https://github.com/Veyal/interseptor/blob/main/not-checked-inside-fence.md)",
		"```",
	}, "\n")
	if err := validateProjectLinks(root, live); err != nil {
		t.Fatalf("live and external links: %v", err)
	}
	dead := "[gone](https://github.com/Veyal/interseptor/blob/main/no/such-file.md)"
	if err := validateProjectLinks(root, dead); err == nil {
		t.Fatal("dead blob/main link must fail")
	}
	deadTree := "[gone](https://github.com/Veyal/interseptor/tree/main/missing-dir/)"
	if err := validateProjectLinks(root, deadTree); err == nil {
		t.Fatal("dead tree/main link must fail")
	}
}

func TestValidateBuiltSiteRejectsMissingFragments(t *testing.T) {
	site := builtSiteFixture(t)
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte(fixtureDocument(`<a href="/interseptor/features/#renamed-heading">Guide</a>`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateBuiltSite(site, "/interseptor"); err == nil {
		t.Fatal("a guide pointing at a missing heading must fail site validation")
	}
}
