package main

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Published site identity. version.DocsSite must equal siteOrigin+siteBase
// (TestDocsSiteMatchesSiteConfig).
const (
	siteTitle  = "Interseptor"
	siteOrigin = "https://veyal.github.io"
	siteBase   = "/interseptor"
	siteParent = "https://veyal.github.io/"

	defaultDescription = "Guides and reference for Interseptor: capture traffic, inspect responses, organize projects, and review evidence."
	defaultImage       = "/website/assets/img/social-card.png"
	defaultImageAlt    = "Interseptor: an HTTP and HTTPS intercepting proxy and security toolkit in one static binary."
)

// Site sources, relative to the repository root.
const (
	assetsDir    = "website/assets"
	pagesDir     = "website/pages"
	templatesDir = "website/templates"
	navFile      = "website/data/navigation.yml"
	featuresFile = "website/data/features.yml"
	searchFile   = "website/data/search.json"
)

// The template data contract. Templates receive View; fields may be added but
// never renamed or removed.

type Release struct{ Version, Date string }

type Site struct {
	Title, Origin, Base, Repo, Parent string
	Release                           Release
	Nav                               []NavGroup
	Features                          []Feature
	Year                              int

	// Pages lists every indexable page by path (Summary is its description),
	// for example for suggestions on the 404 page.
	Pages []NavLink
}

type NavGroup struct {
	ID, Title, Color string
	Current          bool
	Links            []NavLink
}

// NavLink.URL is base-prefixed; Path is site-relative.
type NavLink struct {
	Title, URL, Path, Icon, Summary string
	Current                         bool
}

type Heading struct {
	Level    int
	ID, Text string
}

type Page struct {
	Kind                              string // docs | landing | page | notfound
	Path, URL, Canonical              string
	Title, Heading, Description       string
	Group                             *NavGroup
	Classification, Source, SourceURL string
	Content                           template.HTML
	Headings                          []Heading
	Words, ReadingMinutes             int
	Prev, Next                        *NavLink
	Image, ImageAlt, OGType           string
	NoIndex                           bool
	Params                            map[string]any

	// HeadingID is the kramdown-compatible id of the extracted H1, so links to
	// it keep working when a layout renders the heading.
	HeadingID string
	// Updated is the date (YYYY-MM-DD) of the last commit that changed a
	// guide's source; empty for other pages and without git history.
	Updated string
}

type View struct {
	Site *Site
	Page *Page
}

var pageKinds = map[string]bool{"docs": true, "landing": true, "page": true, "notfound": true}

// frontMatter holds the known keys of a website/pages file; every other key is
// passed to templates as Page.Params.
type frontMatter struct {
	Layout, Title, Heading, Description, Permalink string
	Image, ImageAlt, NavPath, OGType               string
	NoIndex                                        bool
	Params                                         map[string]any
}

var frontMatterDelimiter = regexp.MustCompile(`(?m)^---[ \t]*\r?$`)

// splitFrontMatter separates a leading YAML block delimited by --- lines from
// the page body.
func splitFrontMatter(data []byte) (frontMatter, []byte, error) {
	var fm frontMatter
	open := frontMatterDelimiter.FindIndex(data)
	if open == nil || open[0] != 0 {
		return fm, nil, errors.New("missing front matter: the file must start with a --- line")
	}
	rest := data[open[1]:]
	closing := frontMatterDelimiter.FindIndex(rest)
	if closing == nil {
		return fm, nil, errors.New("unterminated front matter: no closing --- line")
	}
	values := map[string]any{}
	if err := yaml.Unmarshal(rest[:closing[0]], &values); err != nil {
		return fm, nil, fmt.Errorf("front matter: %w", err)
	}
	body := rest[closing[1]:]
	body = bytes.TrimPrefix(bytes.TrimPrefix(body, []byte("\r")), []byte("\n"))
	strs := map[string]*string{
		"layout": &fm.Layout, "title": &fm.Title, "heading": &fm.Heading, "description": &fm.Description,
		"permalink": &fm.Permalink, "image": &fm.Image, "image_alt": &fm.ImageAlt, "nav_path": &fm.NavPath, "og_type": &fm.OGType,
	}
	fm.Params = map[string]any{}
	for key, value := range values {
		if dst, ok := strs[key]; ok {
			s, ok := value.(string)
			if !ok {
				return fm, nil, fmt.Errorf("front matter %q must be a string", key)
			}
			*dst = s
			continue
		}
		if key == "noindex" {
			b, ok := value.(bool)
			if !ok {
				return fm, nil, errors.New(`front matter "noindex" must be true or false`)
			}
			fm.NoIndex = b
			continue
		}
		fm.Params[key] = value
	}
	return fm, body, nil
}

// outputFile maps a permalink to the file that serves it.
func outputFile(permalink string) (string, error) {
	clean := strings.HasPrefix(permalink, "/") &&
		(permalink == "/" || path.Clean(permalink) == strings.TrimSuffix(permalink, "/"))
	if !clean {
		return "", fmt.Errorf("permalink %q must be a clean site path starting with /", permalink)
	}
	switch {
	case permalink == "/":
		return "index.html", nil
	case strings.HasSuffix(permalink, "/"):
		return strings.TrimPrefix(permalink, "/") + "index.html", nil
	case strings.HasSuffix(permalink, ".html"):
		return strings.TrimPrefix(permalink, "/"), nil
	}
	return "", fmt.Errorf("permalink %q must end with / or .html", permalink)
}

type navEntry struct {
	Title, ID, Color string
	Links            []struct{ Title, URL, Icon, Summary string }
}

// parseNavigation reads website/data/navigation.yml. Group ids default to the
// slug of the group title.
func parseNavigation(data []byte, base string) ([]NavGroup, error) {
	var entries []navEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("navigation: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("navigation: no groups")
	}
	nav := make([]NavGroup, 0, len(entries))
	for _, entry := range entries {
		if entry.Title == "" || len(entry.Links) == 0 {
			return nil, fmt.Errorf("navigation group %q needs a title and links", entry.Title)
		}
		group := NavGroup{ID: entry.ID, Title: entry.Title, Color: entry.Color}
		if group.ID == "" {
			group.ID = searchSlug(entry.Title)
		}
		for _, link := range entry.Links {
			if link.Title == "" || !strings.HasPrefix(link.URL, "/") || strings.HasPrefix(link.URL, "//") {
				return nil, fmt.Errorf("navigation link %q in %q needs a title and a site path starting with /", link.Title, entry.Title)
			}
			group.Links = append(group.Links, NavLink{Title: link.Title, URL: base + link.URL, Path: link.URL, Icon: link.Icon, Summary: link.Summary})
		}
		nav = append(nav, group)
	}
	return nav, nil
}

func loadNavigation(root string) ([]NavGroup, error) {
	data, err := os.ReadFile(filepath.Join(root, navFile))
	if err != nil {
		return nil, err
	}
	return parseNavigation(data, siteBase)
}

func linkPage(p string) string {
	page, _, _ := strings.Cut(p, "#")
	return page
}

// markNavigation copies nav with the entry for current, and its group, marked.
func markNavigation(nav []NavGroup, current string) ([]NavGroup, *NavGroup) {
	marked := make([]NavGroup, len(nav))
	var group *NavGroup
	for i, g := range nav {
		g.Links = append([]NavLink(nil), g.Links...)
		g.Current = false
		for j := range g.Links {
			g.Links[j].Current = linkPage(g.Links[j].Path) == current
			if g.Links[j].Current {
				g.Current = true
			}
		}
		marked[i] = g
		if g.Current && group == nil {
			group = &marked[i]
		}
	}
	return marked, group
}

// neighbours returns the entries around current in flattened navigation order.
func neighbours(nav []NavGroup, current string) (prev, next *NavLink) {
	var flat []NavLink
	seen := map[string]bool{}
	for _, g := range nav {
		for _, link := range g.Links {
			if page := linkPage(link.Path); !seen[page] {
				seen[page] = true
				link.Current = false
				flat = append(flat, link)
			}
		}
	}
	for i, link := range flat {
		if linkPage(link.Path) != current {
			continue
		}
		if i > 0 {
			p := flat[i-1]
			prev = &p
		}
		if i+1 < len(flat) {
			n := flat[i+1]
			next = &n
		}
		break
	}
	return prev, next
}

// groupTitle names the navigation group that lists p.
func groupTitle(nav []NavGroup, p string) string {
	if _, group := markNavigation(nav, p); group != nil {
		return group.Title
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
