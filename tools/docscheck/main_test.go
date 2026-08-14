package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		{nil, command{}, true},
		{[]string{"generate", "one", "two"}, command{}, true},
		{[]string{"check-site"}, command{}, true},
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
		`[cookbook]({{ "/mcp-cookbook/" | relative_url }}#recipe)`,
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

func TestJekyllConfigPublishesSearchIndex(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "_config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if !strings.Contains(config, "  - website/data/search.json") {
		t.Fatal("Jekyll config does not explicitly include the generated search index")
	}
	if strings.Contains(config, "  - '*.json'") || strings.Contains(config, "  - \"*.json\"") {
		t.Fatal("Jekyll config excludes every JSON file, including the generated search index")
	}
}

func builtSiteFixture(t *testing.T) string {
	t.Helper()
	site := t.TempDir()
	for _, name := range expectedSitePages() {
		file := filepath.Join(site, name)
		if name != "index.html" {
			file = filepath.Join(file, "index.html")
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(`<a href="/interseptor/features/">Features</a>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(site, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "assets", "site.css"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, asset := range []string{"website/assets/site.css", "website/assets/search.js", "website/data/search.json"} {
		file := filepath.Join(site, filepath.FromSlash(asset))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("[]"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return site
}

func TestValidateBuiltSiteAcceptsBasePrefixedTargets(t *testing.T) {
	site := builtSiteFixture(t)
	links := `<link href="/interseptor/assets/site.css"><a href="/interseptor/features/">Features</a><a href="/interseptor/features/#scanner">Scanner</a>`
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte(links), 0o644); err != nil {
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
			if err := os.WriteFile(filepath.Join(site, "index.html"), []byte(`<a href="`+href+`">bad</a>`), 0o644); err != nil {
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
