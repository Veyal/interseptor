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
		"docs/getting-started.md": {"getting-started", "Getting started", "current"}, "docs/api-and-mcp.md": {"api-and-mcp", "API and MCP", "current"}, "docs/history-search.md": {"history-search", "History search", "current"}, "docs/architecture.md": {"architecture", "Architecture", "current"}, "docs/custom-checks.md": {"custom-checks", "Custom checks", "current"}, "docs/custom-active-checks.md": {"custom-active-checks", "Custom active checks", "current"}, "docs/rule-packs.md": {"rule-packs", "Rule packs", "current"}, "docs/vault.md": {"vault", "Project vault", "current"}, "docs/engagement-closeout.md": {"engagement-closeout", "Engagement close-out", "current"}, "docs/content-discovery.md": {"content-discovery", "Content discovery", "current"}, "docs/http2.md": {"http2", "HTTP/2", "current"}, "docs/message-codecs.md": {"message-codecs", "Message codecs", "current"}, "docs/extensions.md": {"extensions", "Extensions", "current"}, "docs/benchmarks.md": {"benchmarks", "Benchmarks", "reference"}, "docs/product/mcp-cookbook.md": {"mcp-cookbook", "MCP cookbook", "current"},
	}
	markdownLink = regexp.MustCompile(`\]\(([^)]+)\)`)
	featureTitle = regexp.MustCompile(`(?m)^- \*\*([^*]+)\*\*`)
	hrefPattern  = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)
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
	return fmt.Sprintf("---\nlayout: default\ntitle: %s\nclassification: %s\nsource: %s\n---\n<p class=\"eyebrow\">%s</p>\n%s\n", meta.Title, meta.Class, source, strings.ToUpper(meta.Class), body), nil
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
	return features, string(canonical), nil
}

func searchJSON(features []Feature) ([]byte, error) {
	copyFeatures := append([]Feature(nil), features...)
	sort.Slice(copyFeatures, func(i, j int) bool { return copyFeatures[i].ID < copyFeatures[j].ID })
	search := make([]SearchItem, 0, len(copyFeatures)+len(publicDocs))
	for _, f := range copyFeatures {
		search = append(search, SearchItem{f.Title, f.Text, "features/#" + f.ID})
	}
	for _, meta := range publicDocs {
		search = append(search, SearchItem{meta.Title, meta.Class, meta.Slug + "/"})
	}
	sort.Slice(search, func(i, j int) bool { return search[i].URL < search[j].URL })
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
	data, err := searchJSON(features)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "website/data/search.json"), data, 0o644)
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
	}
	want, err := searchJSON(features)
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
	return nil
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
			if err != nil || parsed.Scheme != "" || parsed.Host != "" || strings.HasPrefix(href, "#") {
				continue
			}
			local := parsed.Path
			if basePath != "" && (local == basePath || strings.HasPrefix(local, basePath+"/")) {
				local = strings.TrimPrefix(local, basePath)
			}
			local = strings.TrimPrefix(local, "/")
			if local == "" {
				local = "index.html"
			} else if strings.HasSuffix(local, "/") {
				local += "index.html"
			}
			if _, err := os.Stat(filepath.Join(site, filepath.FromSlash(local))); err != nil {
				return fmt.Errorf("%s has broken local href %s", file, href)
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
