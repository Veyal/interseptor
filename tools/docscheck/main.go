package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const repositoryURL = "https://github.com/Veyal/interseptor"

type Feature struct {
	Number string `json:"number"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Link   string `json:"link,omitempty"`
}

type SearchItem struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	URL   string `json:"url"`
	Page  string `json:"page"`
	Group string `json:"group"`
}

type pageMeta struct{ Slug, Title, Class string }
type command struct{ name, site, root, out, addr string }

const usage = "usage: docscheck generate [root] | check [root] | build <outdir> [root] | serve [-addr host:port] [root] | check-site <site> [root]"

var (
	publicDocs = map[string]pageMeta{
		"docs/workspace.md":       {"workspace", "Workspace guide", "current"},
		"docs/settings.md":        {"settings", "Settings", "current"},
		"docs/getting-started.md": {"getting-started", "Getting started", "current"}, "docs/api-and-mcp.md": {"api-and-mcp", "API and MCP", "current"}, "docs/history-search.md": {"history-search", "History search", "current"}, "docs/architecture.md": {"architecture", "Architecture", "current"}, "docs/custom-checks.md": {"custom-checks", "Custom checks", "current"}, "docs/rule-packs.md": {"rule-packs", "Rule packs", "current"}, "docs/vault.md": {"vault", "Project vault", "current"}, "docs/collections.md": {"collections", "Collections", "current"}, "docs/engagement-closeout.md": {"engagement-closeout", "Engagement close-out", "current"}, "docs/content-discovery.md": {"content-discovery", "Content discovery", "current"}, "docs/http2.md": {"http2", "HTTP/2", "current"}, "docs/message-codecs.md": {"message-codecs", "Message codecs", "current"}, "docs/extensions.md": {"extensions", "Extensions", "current"}, "docs/benchmarks.md": {"benchmarks", "Benchmarks", "reference"}, "docs/product/mcp-cookbook.md": {"mcp-cookbook", "MCP cookbook", "current"}, "docs/agents.md": {"ai-agents", "Interseptor for AI agents", "current"},
		"docs/proxy-and-tls.md": {"proxy-and-tls", "Proxy, TLS, and networking", "current"}, "docs/findings-and-reporting.md": {"findings-and-reporting", "Findings and reporting", "current"}, "docs/cli-reference.md": {"cli-reference", "CLI reference", "reference"}, "docs/projects-and-data.md": {"projects-and-data", "Projects and data", "current"}, "docs/mobile-testing.md": {"mobile-testing", "Mobile testing", "current"}, "docs/troubleshooting.md": {"troubleshooting", "Troubleshooting", "reference"},
	}
	markdownLink   = regexp.MustCompile(`\]\(([^)]+)\)`)
	featureTitle   = regexp.MustCompile(`(?m)^- \*\*([^*]+)\*\*`)
	refPattern     = regexp.MustCompile(`(?i)\s(?:href|src)=["']([^"']+)["']`)
	idPattern      = regexp.MustCompile(`(?i)\s+id=["']([^"']+)["']`)
	fencedBlock    = regexp.MustCompile("(?s)```.*?```")
	htmlTag        = regexp.MustCompile(`<[^>]+>`)
	markdownURL    = regexp.MustCompile(`\[([^]]+)\]\([^)]+\)`)
	releaseHeading = regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\] - ([0-9]{4}-[0-9]{2}-[0-9]{2})$`)
)

func parseCommand(args []string) (command, error) {
	if len(args) == 1 && args[0] == "--write" {
		return command{name: "generate", root: "."}, nil
	}
	if len(args) == 0 {
		return command{}, errors.New(usage)
	}
	cmd := command{name: args[0], root: "."}
	switch cmd.name {
	case "generate", "check":
		if len(args) > 2 {
			return command{}, fmt.Errorf("%s accepts at most one root", cmd.name)
		}
		if len(args) == 2 {
			cmd.root = args[1]
		}
	case "check-site", "build":
		if len(args) < 2 || len(args) > 3 {
			return command{}, fmt.Errorf("%s requires a directory and an optional [root]", cmd.name)
		}
		if cmd.name == "build" {
			cmd.out = args[1]
		} else {
			cmd.site = args[1]
		}
		if len(args) == 3 {
			cmd.root = args[2]
		}
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		addr := flags.String("addr", defaultServeAddr, "listen address")
		if err := flags.Parse(args[1:]); err != nil {
			return command{}, fmt.Errorf("serve: %w", err)
		}
		if flags.NArg() > 1 {
			return command{}, errors.New("serve accepts at most one root")
		}
		cmd.addr = *addr
		if flags.NArg() == 1 {
			cmd.root = flags.Arg(0)
		}
	default:
		return command{}, fmt.Errorf("unknown command %q", cmd.name)
	}
	return cmd, nil
}

func parseFeatures(data string) ([]Feature, error) {
	var out []Feature
	var f Feature
	have := false
	flush := func() {
		if have {
			out = append(out, f)
		}
		f, have = Feature{}, false
	}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- number: ") {
			flush()
			f.Number, have = strings.Trim(strings.TrimPrefix(line, "- number: "), "\""), true
			continue
		}
		for _, key := range []string{"id", "title", "text", "link"} {
			if prefix := key + ": "; strings.HasPrefix(line, prefix) {
				value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), "\"")
				switch key {
				case "id":
					f.ID = value
				case "title":
					f.Title = value
				case "text":
					f.Text = value
				case "link":
					f.Link = value
				}
			}
		}
	}
	flush()
	if len(out) == 0 {
		return nil, errors.New("no features")
	}
	return out, nil
}

func normalizedTitle(s string) string {
	s = strings.NewReplacer("&", " and ", "/", " and ", "-", " ").Replace(strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

func canonicalFeatureTitles(canonical string) []string {
	matches := featureTitle.FindAllStringSubmatch(canonical, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, normalizedTitle(match[1]))
	}
	return out
}

func validateFeatures(features []Feature, canonical string) error {
	titles := canonicalFeatureTitles(canonical)
	if len(features) != len(titles) {
		return fmt.Errorf("feature coverage mismatch: canonical=%d site=%d", len(titles), len(features))
	}
	ids := map[string]bool{}
	for i, f := range features {
		if f.ID == "" || f.Title == "" || f.Text == "" {
			return errors.New("feature missing id, title, or text")
		}
		if ids[f.ID] {
			return fmt.Errorf("duplicate feature id: %s", f.ID)
		}
		ids[f.ID] = true
		n, err := strconv.Atoi(f.Number)
		if err != nil || n != i+1 {
			return fmt.Errorf("feature numbers must be unique and sequential: %s", f.Number)
		}
		if normalizedTitle(f.Title) != titles[i] {
			return fmt.Errorf("feature title mismatch at %d: canonical=%q site=%q", i+1, titles[i], normalizedTitle(f.Title))
		}
	}
	return nil
}

func validateFeatureGuides(features []Feature) error {
	pages := map[string]bool{}
	for _, meta := range publicDocs {
		pages["/"+meta.Slug+"/"] = true
	}
	for _, feature := range features {
		guide, err := url.Parse(feature.Link)
		if err != nil || guide.Scheme != "" || guide.Host != "" || !pages[guide.Path] {
			return fmt.Errorf("feature %s has no published guide: %s", feature.ID, feature.Link)
		}
	}
	return nil
}

func loadFeatures(root string) ([]Feature, string, error) {
	canonical, err := os.ReadFile(filepath.Join(root, "docs", "FEATURES.md"))
	if err != nil {
		return nil, "", fmt.Errorf("read canonical features: %w", err)
	}
	yml, err := os.ReadFile(filepath.Join(root, featuresFile))
	if err != nil {
		return nil, "", fmt.Errorf("read published features: %w", err)
	}
	features, err := parseFeatures(string(yml))
	if err != nil {
		return nil, "", fmt.Errorf("parse published features: %w", err)
	}
	if err := validateFeatures(features, string(canonical)); err != nil {
		return nil, "", err
	}
	if err := validateFeatureGuides(features); err != nil {
		return nil, "", err
	}
	return features, string(canonical), nil
}

// tableDelimiterRow matches a table's |---|:---:| row (and a --- rule).
var tableDelimiterRow = regexp.MustCompile(`(?m)^[ \t]*\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)

// searchableText flattens Markdown into the plain text of a search excerpt:
// no code samples, link targets, tags, emphasis marks or table structure.
func searchableText(s string) string {
	s = fencedBlock.ReplaceAllString(s, " ")
	s = tableDelimiterRow.ReplaceAllString(s, " ")
	s = markdownURL.ReplaceAllString(s, "$1")
	s = htmlTag.ReplaceAllString(s, " ")
	s = strings.NewReplacer(`\|`, " ", "`", "", "#", " ", "*", "", "_", " ", ">", " ", "|", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 1200 {
		cut := 1200
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}

// generate writes the committed generated files: llms.txt, llms-full.txt and
// the reference block of docs/agents.md. The site itself is built by build.
func generate(root string) error {
	return writeAgentFiles(root)
}

// loadRelease reads the newest published changelog entry rather than the
// development fallback, which can lag a tag until post-release maintenance.
// Unreleased work never advances the badge.
func loadRelease(root string) (Release, error) {
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return Release{}, err
	}
	match := releaseHeading.FindSubmatch(data)
	if match == nil {
		return Release{}, errors.New("changelog has no published release")
	}
	return Release{Version: string(match[1]), Date: string(match[2])}, nil
}

func check(root string) error {
	if _, _, err := loadFeatures(root); err != nil {
		return err
	}
	if err := checkAgentFiles(root); err != nil {
		return err
	}
	for _, source := range sortedKeys(publicDocs) {
		body, err := readDoc(root, source)
		if err != nil {
			return err
		}
		if err := validateProjectLinks(root, rewriteLinks(body, source)); err != nil {
			return fmt.Errorf("%s: %w", publicDocs[source].Slug, err)
		}
		if err := validateTablePipes(body); err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
	}
	pages, err := filepath.Glob(filepath.Join(root, pagesDir, "*"))
	if err != nil {
		return err
	}
	for _, page := range pages {
		body, err := os.ReadFile(page)
		if err != nil {
			return err
		}
		if err := validateProjectLinks(root, string(body)); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(page), err)
		}
		if filepath.Ext(page) == ".md" {
			if err := validateTablePipes(string(body)); err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(page), err)
			}
		}
	}
	return nil
}

// validateProjectLinks fails when a Markdown link or an href targets
// https://github.com/Veyal/interseptor/(blob|tree)/main/<path> and that path is
// not in the local repo. Other absolute links are left alone. Fenced samples
// are not links on the published page.
func validateProjectLinks(root, body string) error {
	body = fencedBlock.ReplaceAllString(body, "")
	var targets []string
	for _, match := range markdownLink.FindAllStringSubmatch(body, -1) {
		targets = append(targets, markdownLinkTarget(match[1]))
	}
	for _, match := range refPattern.FindAllStringSubmatch(body, -1) {
		targets = append(targets, match[1])
	}
	var dead []string
	seen := map[string]bool{}
	for _, target := range targets {
		rel, kind, ok := projectLinkPath(target)
		if !ok || seen[target] {
			continue
		}
		seen[target] = true
		if !projectPathExists(root, rel, kind) {
			dead = append(dead, target)
		}
	}
	if len(dead) == 0 {
		return nil
	}
	return fmt.Errorf("dead project link(s):\n%s", strings.Join(dead, "\n"))
}

func markdownLinkTarget(raw string) string {
	target := strings.TrimSpace(raw)
	if i := strings.IndexAny(target, " \t"); i >= 0 {
		target = target[:i]
	}
	return strings.Trim(target, "<>")
}

func projectLinkPath(raw string) (rel, kind string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return "", "", false
	}
	rest := strings.TrimPrefix(u.Path, "/")
	const prefix = "Veyal/interseptor/"
	if !strings.HasPrefix(rest, prefix) {
		return "", "", false
	}
	rest = strings.TrimPrefix(rest, prefix)
	kind, rest, ok = strings.Cut(rest, "/")
	if !ok || (kind != "blob" && kind != "tree") {
		return "", "", false
	}
	branch, rel, found := strings.Cut(rest, "/")
	if !found || branch != "main" {
		return "", "", false
	}
	rel = path.Clean(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || strings.Contains(rel, "..") {
		return rel, kind, true
	}
	return rel, kind, true
}

func projectPathExists(root, rel, kind string) bool {
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "..") {
		return false
	}
	local := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(local)
	if err != nil {
		return false
	}
	if kind == "tree" {
		return info.IsDir()
	}
	return !info.IsDir()
}

// expectedSitePages lists the pages every build must contain: directories
// served as <name>/index.html, and files ending in .html.
func expectedSitePages() []string {
	pages := []string{"index.html", "404.html", "features", "reference", "compare"}
	for _, meta := range publicDocs {
		pages = append(pages, meta.Slug)
	}
	sort.Strings(pages)
	return pages
}

var requiredSiteFiles = []string{"website/assets/site.css", "website/assets/site.js", "website/assets/search.js", "website/assets/mark.svg", searchFile, "sitemap.xml", "robots.txt"}

func validateBuiltSite(site, basePath string) error {
	for _, forbidden := range []string{"AGENTS.md", "CLAUDE.md", "SECURITY.md", "internal", "examples", ".github", ".claude", ".opencode", "docs", pagesDir, templatesDir, navFile, featuresFile} {
		if _, err := os.Stat(filepath.Join(site, filepath.FromSlash(forbidden))); err == nil {
			return fmt.Errorf("forbidden publication: %s", forbidden)
		}
	}
	for _, page := range expectedSitePages() {
		file := filepath.Join(site, page)
		if !strings.HasSuffix(page, ".html") {
			file = filepath.Join(file, "index.html")
		}
		if _, err := os.Stat(file); err != nil {
			return fmt.Errorf("missing built page %s: %w", page, err)
		}
	}
	for _, asset := range requiredSiteFiles {
		if _, err := os.Stat(filepath.Join(site, filepath.FromSlash(asset))); err != nil {
			return fmt.Errorf("missing built asset %s: %w", asset, err)
		}
	}
	if err := filepath.Walk(site, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(file) != ".html" {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := validateDocumentMetadata(string(data)); err != nil {
			return fmt.Errorf("%s %w", file, err)
		}
		for _, match := range refPattern.FindAllStringSubmatch(string(data), -1) {
			if err := checkLocalTarget(site, basePath, file, match[1]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := validateSearchIndex(site, basePath); err != nil {
		return err
	}
	return validateSitemap(site, basePath)
}

// checkLocalTarget verifies that a site-local href or src stays under the base
// path, names a built file, and, for a page, names an existing id.
func checkLocalTarget(site, basePath, file, href string) error {
	if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") && basePath != "" && href != basePath && !strings.HasPrefix(href, basePath+"/") {
		return fmt.Errorf("%s has link outside base path: %s", file, href)
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return nil
	}
	local := parsed.Path
	if basePath != "" && (local == basePath || strings.HasPrefix(local, basePath+"/")) {
		local = strings.TrimPrefix(local, basePath)
	}
	local = strings.TrimPrefix(local, "/")
	if local == "" && parsed.Path == "" {
		local, err = filepath.Rel(site, file)
		if err != nil {
			return err
		}
		local = filepath.ToSlash(local)
	} else if local == "" {
		local = "index.html"
	} else if strings.HasSuffix(local, "/") {
		local += "index.html"
	}
	if _, err := os.Stat(filepath.Join(site, filepath.FromSlash(local))); err != nil {
		return fmt.Errorf("%s has broken local href %s", file, href)
	}
	if parsed.Fragment != "" && strings.HasSuffix(local, ".html") {
		target, err := os.ReadFile(filepath.Join(site, filepath.FromSlash(local)))
		if err != nil {
			return err
		}
		for _, id := range idPattern.FindAllStringSubmatch(string(target), -1) {
			if id[1] == parsed.Fragment {
				return nil
			}
		}
		return fmt.Errorf("%s has broken local heading %s", file, href)
	}
	return nil
}

var (
	htmlLang     = regexp.MustCompile(`(?is)<html\b[^>]*\slang="en"`)
	titleElement = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	metaTag      = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	linkTag      = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	tagAttr      = regexp.MustCompile(`(?is)\s([a-z:-]+)\s*=\s*"([^"]*)"`)
	h1Open       = regexp.MustCompile(`(?i)<h1[\s>]`)
)

// validateDocumentMetadata requires what every published page needs: a
// language, a title, a description, a canonical URL and exactly one h1.
func validateDocumentMetadata(doc string) error {
	if !htmlLang.MatchString(doc) {
		return errors.New(`lacks <html lang="en">`)
	}
	if m := titleElement.FindStringSubmatch(doc); m == nil || strings.TrimSpace(htmlTag.ReplaceAllString(m[1], "")) == "" {
		return errors.New("lacks a non-empty <title>")
	}
	if !hasTag(metaTag, doc, "name", "description", "content") {
		return errors.New(`lacks <meta name="description">`)
	}
	if !hasTag(linkTag, doc, "rel", "canonical", "href") {
		return errors.New("lacks a canonical link")
	}
	if n := len(h1Open.FindAllStringIndex(doc, -1)); n != 1 {
		return fmt.Errorf("has %d <h1> elements, want exactly one", n)
	}
	return nil
}

// hasTag reports whether a tag has key=value and a non-empty required attribute.
func hasTag(pattern *regexp.Regexp, doc, key, value, required string) bool {
	for _, tag := range pattern.FindAllString(doc, -1) {
		attrs := map[string]string{}
		for _, a := range tagAttr.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(a[1])] = a[2]
		}
		if strings.EqualFold(attrs[key], value) && strings.TrimSpace(attrs[required]) != "" {
			return true
		}
	}
	return false
}

// validateSearchIndex resolves every search result to a built page and id.
func validateSearchIndex(site, basePath string) error {
	data, err := os.ReadFile(filepath.Join(site, filepath.FromSlash(searchFile)))
	if err != nil {
		return err
	}
	var items []SearchItem
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("%s: %w", searchFile, err)
	}
	for _, item := range items {
		if item.Title == "" || item.URL == "" || strings.HasPrefix(item.URL, "/") {
			return fmt.Errorf("%s has an invalid item: %+v", searchFile, item)
		}
		if err := checkLocalTarget(site, basePath, searchFile, basePath+"/"+item.URL); err != nil {
			return err
		}
	}
	return nil
}

// validateSitemap resolves every sitemap URL to a built page.
func validateSitemap(site, basePath string) error {
	data, err := os.ReadFile(filepath.Join(site, "sitemap.xml"))
	if err != nil {
		return err
	}
	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(data, &sitemap); err != nil {
		return fmt.Errorf("sitemap.xml: %w", err)
	}
	if len(sitemap.URLs) == 0 {
		return errors.New("sitemap.xml lists no pages")
	}
	for _, u := range sitemap.URLs {
		local, ok := strings.CutPrefix(u.Loc, siteOrigin)
		if !ok || !strings.HasPrefix(local, basePath+"/") {
			return fmt.Errorf("sitemap.xml lists a URL outside the site: %s", u.Loc)
		}
		if err := checkLocalTarget(site, basePath, "sitemap.xml", local); err != nil {
			return err
		}
	}
	return nil
}

func run(args []string) error {
	cmd, err := parseCommand(args)
	if err != nil {
		return err
	}
	switch cmd.name {
	case "generate":
		err = generate(cmd.root)
	case "check":
		err = check(cmd.root)
	case "build":
		err = buildSite(cmd.root, cmd.out)
	case "serve":
		err = serve(cmd.root, cmd.addr, os.Stdout)
	case "check-site":
		err = validateBuiltSite(cmd.site, siteBase)
		if err == nil {
			err = validateBuiltLLMS(cmd.site, cmd.root)
		}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", cmd.name, err)
	}
	fmt.Fprintf(os.Stdout, "docscheck: %s passed\n", cmd.name)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "docscheck:", err)
		os.Exit(1)
	}
}
