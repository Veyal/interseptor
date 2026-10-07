package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
}

type pageMeta struct{ Slug, Title, Class string }
type command struct{ name, site, root string }

var (
	publicDocs = map[string]pageMeta{
		"docs/workspace.md":       {"workspace", "Workspace guide", "current"},
		"docs/settings.md":        {"settings", "Settings", "current"},
		"docs/getting-started.md": {"getting-started", "Getting started", "current"}, "docs/api-and-mcp.md": {"api-and-mcp", "API and MCP", "current"}, "docs/history-search.md": {"history-search", "History search", "current"}, "docs/architecture.md": {"architecture", "Architecture", "current"}, "docs/custom-checks.md": {"custom-checks", "Custom checks", "current"}, "docs/rule-packs.md": {"rule-packs", "Rule packs", "current"}, "docs/vault.md": {"vault", "Project vault", "current"}, "docs/collections.md": {"collections", "Collections", "current"}, "docs/engagement-closeout.md": {"engagement-closeout", "Engagement close-out", "current"}, "docs/content-discovery.md": {"content-discovery", "Content discovery", "current"}, "docs/http2.md": {"http2", "HTTP/2", "current"}, "docs/message-codecs.md": {"message-codecs", "Message codecs", "current"}, "docs/extensions.md": {"extensions", "Extensions", "current"}, "docs/benchmarks.md": {"benchmarks", "Benchmarks", "reference"}, "docs/product/mcp-cookbook.md": {"mcp-cookbook", "MCP cookbook", "current"},
		"docs/proxy-and-tls.md": {"proxy-and-tls", "Proxy, TLS, and networking", "current"}, "docs/findings-and-reporting.md": {"findings-and-reporting", "Findings and reporting", "current"}, "docs/cli-reference.md": {"cli-reference", "CLI reference", "reference"}, "docs/projects-and-data.md": {"projects-and-data", "Projects and data", "current"}, "docs/mobile-testing.md": {"mobile-testing", "Mobile testing", "current"}, "docs/troubleshooting.md": {"troubleshooting", "Troubleshooting", "reference"},
	}
	markdownLink  = regexp.MustCompile(`\]\(([^)]+)\)`)
	featureTitle  = regexp.MustCompile(`(?m)^- \*\*([^*]+)\*\*`)
	hrefPattern   = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)
	idPattern     = regexp.MustCompile(`(?i)\s+id=["']([^"']+)["']`)
	searchHeading = regexp.MustCompile(`(?m)^(#{2,3})[ \t]+(.+?)[ \t]*$`)
	fencedBlock   = regexp.MustCompile("(?s)```.*?```")
	htmlTag       = regexp.MustCompile(`<[^>]+>`)
	markdownURL   = regexp.MustCompile(`\[([^]]+)\]\([^)]+\)`)
	nonSlug       = regexp.MustCompile(`[^a-z0-9 -]+`)
)

func parseCommand(args []string) (command, error) {
	if len(args) == 1 && args[0] == "--write" {
		return command{name: "generate", root: "."}, nil
	}
	if len(args) == 0 {
		return command{}, errors.New("usage: docscheck generate [root] | check [root] | check-site <site> [root]")
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
	case "check-site":
		if len(args) < 2 || len(args) > 3 {
			return command{}, errors.New("check-site requires <site> and optional [root]")
		}
		cmd.site = args[1]
		if len(args) == 3 {
			cmd.root = args[2]
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

func rewriteLinks(body, source string) string {
	return markdownLink.ReplaceAllStringFunc(body, func(link string) string {
		target := link[2 : len(link)-1]
		if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			return link
		}
		parts := strings.SplitN(target, "#", 2)
		resolved := path.Clean(path.Join(path.Dir(source), parts[0]))
		fragment := ""
		if len(parts) == 2 {
			fragment = "#" + parts[1]
		}
		if meta, ok := publicDocs[resolved]; ok {
			return `]({{ "/` + meta.Slug + `/" | relative_url }}` + fragment + `)`
		}
		kind := "blob"
		if strings.HasSuffix(parts[0], "/") || path.Ext(resolved) == "" {
			kind = "tree"
		}
		absolute := repositoryURL + "/" + kind + "/main/" + resolved
		if kind == "tree" && strings.HasSuffix(parts[0], "/") {
			absolute += "/"
		}
		return "](" + absolute + fragment + ")"
	})
}

func pageContent(root, source string, meta pageMeta) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, source))
	if err != nil {
		return "", err
	}
	body := rewriteLinks(string(data), source)
	return fmt.Sprintf("---\nlayout: default\ntitle: %s\nclassification: %s\nsource: %s\n---\n%s\n", meta.Title, meta.Class, source, body), nil
}

func loadFeatures(root string) ([]Feature, string, error) {
	canonical, err := os.ReadFile(filepath.Join(root, "docs", "FEATURES.md"))
	if err != nil {
		return nil, "", fmt.Errorf("read canonical features: %w", err)
	}
	yml, err := os.ReadFile(filepath.Join(root, "_data", "features.yml"))
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

func searchSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "")
	// Match kramdown's auto IDs: punctuation is removed, then every remaining
	// whitespace character becomes a hyphen. Spaces on both sides of punctuation
	// therefore intentionally produce a double hyphen ("Limits & safety" →
	// "limits--safety").
	s = strings.NewReplacer(" ", "-", "\t", "-", "\n", "-", "\r", "-").Replace(s)
	return strings.Trim(s, "-")
}

func searchableText(s string) string {
	s = fencedBlock.ReplaceAllString(s, " ")
	s = markdownURL.ReplaceAllString(s, "$1")
	s = htmlTag.ReplaceAllString(s, " ")
	s = strings.NewReplacer("`", "", "#", " ", "*", "", "_", " ", ">", " ", "|", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 1200 {
		s = s[:1200]
	}
	return s
}

func searchSections(body string, meta pageMeta) []SearchItem {
	matches := searchHeading.FindAllStringSubmatchIndex(body, -1)
	introEnd := len(body)
	if len(matches) > 0 {
		introEnd = matches[0][0]
	}
	items := []SearchItem{{Title: meta.Title, Text: searchableText(body[:introEnd]), URL: meta.Slug + "/"}}
	for i, match := range matches {
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		title := strings.TrimSpace(body[match[4]:match[5]])
		items = append(items, SearchItem{
			Title: searchableText(title),
			Text:  searchableText(body[match[1]:end]),
			URL:   meta.Slug + "/#" + searchSlug(title),
		})
	}
	return items
}

func searchJSON(root string, features []Feature) ([]byte, error) {
	copyFeatures := append([]Feature(nil), features...)
	sort.Slice(copyFeatures, func(i, j int) bool { return copyFeatures[i].ID < copyFeatures[j].ID })
	search := make([]SearchItem, 0, len(copyFeatures)+len(publicDocs))
	for _, f := range copyFeatures {
		search = append(search, SearchItem{f.Title, f.Text, "features/#" + f.ID})
	}
	for source, meta := range publicDocs {
		body, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", source, err)
		}
		search = append(search, searchSections(string(body), meta)...)
	}
	sort.Slice(search, func(i, j int) bool {
		if search[i].URL == search[j].URL {
			return search[i].Title < search[j].Title
		}
		return search[i].URL < search[j].URL
	})
	return json.MarshalIndent(search, "", "  ")
}

func generate(root string) error {
	features, _, err := loadFeatures(root)
	if err != nil {
		return err
	}
	for source, meta := range publicDocs {
		content, err := pageContent(root, source, meta)
		if err != nil {
			return fmt.Errorf("generate %s: %w", meta.Slug, err)
		}
		if err := os.WriteFile(filepath.Join(root, meta.Slug+".md"), []byte(content), 0o644); err != nil {
			return err
		}
	}
	data, err := searchJSON(root, features)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "website/data/search.json"), data, 0o644); err != nil {
		return err
	}
	release, err := releaseJSON(root)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "_data/release.json"), release, 0o644)
}

// Read the published entry rather than the development fallback, which can lag
// a tag until post-release maintenance. Unreleased work never advances the badge.
func releaseJSON(root string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return nil, err
	}
	match := regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\] - ([0-9]{4}-[0-9]{2}-[0-9]{2})$`).FindSubmatch(data)
	if match == nil {
		return nil, errors.New("changelog has no published release")
	}
	return json.MarshalIndent(map[string]string{"version": string(match[1]), "date": string(match[2])}, "", "  ")
}

func check(root string) error {
	features, _, err := loadFeatures(root)
	if err != nil {
		return err
	}
	for source, meta := range publicDocs {
		want, err := pageContent(root, source, meta)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(root, meta.Slug+".md"))
		if err != nil {
			return fmt.Errorf("missing generated page %s: %w", meta.Slug, err)
		}
		if string(got) != want {
			return fmt.Errorf("generated page stale: %s.md", meta.Slug)
		}
		if err := validateProjectLinks(root, want); err != nil {
			return fmt.Errorf("%s: %w", meta.Slug, err)
		}
	}
	want, err := searchJSON(root, features)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(filepath.Join(root, "website/data/search.json"))
	if err != nil {
		return fmt.Errorf("missing search index: %w", err)
	}
	if string(got) != string(want) {
		return errors.New("generated search index stale")
	}
	want, err = releaseJSON(root)
	if err != nil {
		return err
	}
	got, err = os.ReadFile(filepath.Join(root, "_data/release.json"))
	if err != nil {
		return fmt.Errorf("missing release metadata: %w", err)
	}
	if string(got) != string(want) {
		return errors.New("generated release metadata stale")
	}
	return nil
}

// validateProjectLinks fails when a markdown link targets
// https://github.com/Veyal/interseptor/(blob|tree)/main/<path> and that path is
// not in the local repo. Other absolute links are left alone. Fenced samples
// are not links on the published page.
func validateProjectLinks(root, body string) error {
	body = fencedBlock.ReplaceAllString(body, "")
	var dead []string
	seen := map[string]bool{}
	for _, match := range markdownLink.FindAllStringSubmatch(body, -1) {
		target := markdownLinkTarget(match[1])
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

func expectedSitePages() []string {
	pages := []string{"index.html", "features", "reference"}
	for _, meta := range publicDocs {
		pages = append(pages, meta.Slug)
	}
	sort.Strings(pages)
	return pages
}

func validateBuiltSite(site, basePath string) error {
	for _, forbidden := range []string{"AGENTS.md", "CLAUDE.md", "SECURITY.md", "internal", "examples", ".github", ".claude", ".opencode", "docs"} {
		if _, err := os.Stat(filepath.Join(site, forbidden)); err == nil {
			return fmt.Errorf("forbidden publication: %s", forbidden)
		}
	}
	for _, page := range expectedSitePages() {
		file := filepath.Join(site, page)
		if page != "index.html" {
			file = filepath.Join(file, "index.html")
		}
		if _, err := os.Stat(file); err != nil {
			return fmt.Errorf("missing built page %s: %w", page, err)
		}
	}
	for _, asset := range []string{"website/assets/site.css", "website/assets/site.js", "website/assets/search.js", "website/assets/mark.svg", "website/data/search.json"} {
		if _, err := os.Stat(filepath.Join(site, filepath.FromSlash(asset))); err != nil {
			return fmt.Errorf("missing built asset %s: %w", asset, err)
		}
	}
	return filepath.Walk(site, func(file string, info os.FileInfo, err error) error {
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
		for _, match := range hrefPattern.FindAllStringSubmatch(string(data), -1) {
			href := match[1]
			if strings.HasPrefix(href, "/") && basePath != "" && href != basePath && !strings.HasPrefix(href, basePath+"/") {
				return fmt.Errorf("%s has link outside base path: %s", file, href)
			}
			parsed, err := url.Parse(href)
			if err != nil || parsed.Scheme != "" || parsed.Host != "" {
				continue
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
				found := false
				for _, id := range idPattern.FindAllStringSubmatch(string(target), -1) {
					if id[1] == parsed.Fragment {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("%s has broken local heading %s", file, href)
				}
			}
		}
		return nil
	})
}

func configuredBasePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "_config.yml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "baseurl:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "baseurl:")), `"'`)
		}
	}
	return ""
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
	case "check-site":
		err = validateBuiltSite(cmd.site, configuredBasePath(cmd.root))
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
