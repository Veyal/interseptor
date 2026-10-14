package main

import (
	"encoding/xml"
	"reflect"
	"strings"
	"testing"
)

func TestSplitFrontMatterSeparatesKnownKeysAndParams(t *testing.T) {
	src := "---\nlayout: page\ntitle: Feature index\nheading: All features\ndescription: Every feature.\npermalink: /features/\n" +
		"image: /website/assets/img/social-card.png\nimage_alt: Alt text\nnoindex: true\nnav_path: /workspace/\nog_type: website\n" +
		"eyebrow: Workspace reference\nsections:\n  - title: One\n    id: one\n---\n<p>Body</p>\n"
	fm, body, err := splitFrontMatter([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := frontMatter{
		Layout: "page", Title: "Feature index", Heading: "All features", Description: "Every feature.", Permalink: "/features/",
		Image: "/website/assets/img/social-card.png", ImageAlt: "Alt text", NavPath: "/workspace/", OGType: "website", NoIndex: true,
		Params: map[string]any{"eyebrow": "Workspace reference", "sections": []any{map[string]any{"title": "One", "id": "one"}}},
	}
	if !reflect.DeepEqual(fm, want) {
		t.Fatalf("front matter = %#v\nwant %#v", fm, want)
	}
	if string(body) != "<p>Body</p>\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestSplitFrontMatterRejectsMalformedInput(t *testing.T) {
	for name, src := range map[string]string{
		"missing":      "<p>No front matter</p>\n",
		"unterminated": "---\ntitle: x\n",
		"bad yaml":     "---\ntitle: [unclosed\n---\n",
		"bool type":    "---\nnoindex: maybe\n---\n",
		"string type":  "---\ntitle: {nested: 1}\n---\n",
	} {
		if _, _, err := splitFrontMatter([]byte(src)); err == nil {
			t.Errorf("%s: splitFrontMatter accepted %q", name, src)
		}
	}
}

func TestOutputFileForPermalink(t *testing.T) {
	for permalink, want := range map[string]string{
		"/":          "index.html",
		"/features/": "features/index.html",
		"/a/b/":      "a/b/index.html",
		"/404.html":  "404.html",
	} {
		got, err := outputFile(permalink)
		if err != nil || got != want {
			t.Errorf("outputFile(%q) = %q, %v; want %q", permalink, got, err, want)
		}
	}
	for _, bad := range []string{"", "features/", "/../x/", "/a//b/", "/x"} {
		if _, err := outputFile(bad); err == nil {
			t.Errorf("outputFile(%q) accepted", bad)
		}
	}
}

const navFixture = `- title: Start here
  id: start
  color: yellow
  links:
    - title: Overview
      url: /
      icon: book-open
      summary: What it is
    - title: Getting started
      url: /getting-started/
- title: Reference
  links:
    - title: Workspace intercept
      url: /workspace/#intercept
    - title: CLI reference
      url: /cli-reference/
`

func TestParseNavigationReadsGroupsAndOptionalKeys(t *testing.T) {
	nav, err := parseNavigation([]byte(navFixture), siteBase)
	if err != nil {
		t.Fatal(err)
	}
	if len(nav) != 2 || nav[0].ID != "start" || nav[0].Color != "yellow" || nav[1].ID != "reference" {
		t.Fatalf("groups = %+v", nav)
	}
	want := NavLink{Title: "Overview", URL: "/interseptor/", Path: "/", Icon: "book-open", Summary: "What it is"}
	if nav[0].Links[0] != want {
		t.Fatalf("link = %+v, want %+v", nav[0].Links[0], want)
	}
	for _, bad := range []string{
		"- title: Group\n  links:\n    - title: No URL\n",
		"- title: Group\n  links:\n    - title: Relative\n      url: getting-started/\n",
		"- links:\n    - title: Untitled group\n      url: /\n",
		"[]",
	} {
		if _, err := parseNavigation([]byte(bad), siteBase); err == nil {
			t.Errorf("parseNavigation accepted %q", bad)
		}
	}
}

func TestNavigationMarksCurrentPageAndNeighbours(t *testing.T) {
	nav, err := parseNavigation([]byte(navFixture), siteBase)
	if err != nil {
		t.Fatal(err)
	}
	marked, group := markNavigation(nav, "/getting-started/")
	if group == nil || group.Title != "Start here" || !group.Current || !marked[0].Links[1].Current || marked[0].Links[0].Current || marked[1].Current {
		t.Fatalf("marked = %+v, group = %+v", marked, group)
	}
	if nav[0].Links[1].Current || nav[0].Current {
		t.Fatal("markNavigation changed the shared navigation")
	}
	if _, group := markNavigation(nav, "/workspace/"); group == nil || group.Title != "Reference" {
		t.Fatalf("a link with a fragment marks its page: %+v", group)
	}
	if _, group := markNavigation(nav, "/404.html"); group != nil {
		t.Fatalf("a page outside the navigation has no group: %+v", group)
	}
	cases := []struct{ path, prev, next string }{
		{"/", "", "/getting-started/"},
		{"/getting-started/", "/", "/workspace/#intercept"},
		{"/workspace/", "/getting-started/", "/cli-reference/"},
		{"/cli-reference/", "/workspace/#intercept", ""},
		{"/404.html", "", ""},
	}
	for _, c := range cases {
		prev, next := neighbours(nav, c.path)
		if got := linkPath(prev); got != c.prev {
			t.Errorf("prev(%s) = %q, want %q", c.path, got, c.prev)
		}
		if got := linkPath(next); got != c.next {
			t.Errorf("next(%s) = %q, want %q", c.path, got, c.next)
		}
	}
	if prev, _ := neighbours(nav, "/getting-started/"); prev.URL != "/interseptor/" || prev.Title != "Overview" {
		t.Fatalf("prev link = %+v", prev)
	}
}

func linkPath(link *NavLink) string {
	if link == nil {
		return ""
	}
	return link.Path
}

func TestSearchIndexCarriesPageAndGroup(t *testing.T) {
	nav, err := parseNavigation([]byte(navFixture+"    - title: All features\n      url: /features/\n"), siteBase)
	if err != nil {
		t.Fatal(err)
	}
	b := &siteBuilder{site: &Site{Nav: nav, Features: []Feature{{Number: "01", ID: "proxy", Title: "Intercepting proxy", Text: "Hold requests.", Link: "/getting-started/"}}}}
	guide, err := renderMarkdown([]byte("# Getting started\n\nInstall it.\n\n## Install\n\nRun the binary.\n"), docInput("docs/getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	pages := []*renderedPage{
		{Page: &Page{Kind: "docs", Path: "/getting-started/", Title: "Getting started"}, NavPath: "/getting-started/", Sections: guide.Sections},
		{Page: &Page{Kind: "page", Path: "/features/", Title: "Feature index"}, NavPath: "/features/"},
	}
	got := b.searchIndex(pages)
	want := []SearchItem{
		{Title: "Intercepting proxy", Text: "Hold requests.", URL: "features/#proxy", Page: "Feature index", Group: "Reference"},
		{Title: "Getting started", Text: "Getting started Install it.", URL: "getting-started/", Page: "Getting started", Group: "Start here"},
		{Title: "Install", Text: "Run the binary.", URL: "getting-started/#install", Page: "Getting started", Group: "Start here"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("search index =\n%+v\nwant\n%+v", got, want)
	}
}

func TestSitemapListsIndexablePages(t *testing.T) {
	pages := []*Page{
		{Kind: "docs", Path: "/getting-started/", Canonical: "https://veyal.github.io/interseptor/getting-started/"},
		{Kind: "landing", Path: "/", Canonical: "https://veyal.github.io/interseptor/"},
		{Kind: "notfound", Path: "/404.html", Canonical: "https://veyal.github.io/interseptor/404.html"},
		{Kind: "page", Path: "/draft/", Canonical: "https://veyal.github.io/interseptor/draft/", NoIndex: true},
	}
	data := sitemapXML(pages)
	if !strings.HasPrefix(string(data), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Fatalf("sitemap lacks the XML declaration:\n%s", data)
	}
	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(data, &sitemap); err != nil {
		t.Fatal(err)
	}
	var locs []string
	for _, u := range sitemap.URLs {
		locs = append(locs, u.Loc)
	}
	want := []string{"https://veyal.github.io/interseptor/", "https://veyal.github.io/interseptor/getting-started/"}
	if !reflect.DeepEqual(locs, want) {
		t.Fatalf("sitemap locs = %v, want %v", locs, want)
	}
}

func TestRobotsTxtAllowsAllAndNamesSitemap(t *testing.T) {
	want := "User-agent: *\nAllow: /\n\nSitemap: https://veyal.github.io/interseptor/sitemap.xml\n"
	if got := robotsTxt(siteOrigin + siteBase); got != want {
		t.Fatalf("robots.txt = %q, want %q", got, want)
	}
}
