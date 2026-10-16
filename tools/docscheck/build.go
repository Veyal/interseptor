package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// buildMarker marks a directory written by build. A non-empty output
// directory without it is never wiped.
const buildMarker = ".docscheck-site"

type renderedPage struct {
	Page     *Page
	Layout   string
	Format   string // markdown | html
	Body     []byte
	NavPath  string
	File     string // output file relative to the site root
	Sections []searchSection
	HTML     []byte
}

type siteBuilder struct {
	root    string
	docs    map[string]pageMeta
	site    *Site
	funcs   *siteFuncs
	layouts map[string]*template.Template
	paths   map[string]string // every published source and its site path
}

func newSiteBuilder(root string) (*siteBuilder, error) {
	release, err := loadRelease(root)
	if err != nil {
		return nil, err
	}
	nav, err := loadNavigation(root)
	if err != nil {
		return nil, err
	}
	features, _, err := loadFeatures(root)
	if err != nil {
		return nil, err
	}
	funcs := newSiteFuncs(root)
	layouts, err := loadLayouts(root, funcs.funcMap())
	if err != nil {
		return nil, err
	}
	site := &Site{
		Title: siteTitle, Origin: siteOrigin, Base: siteBase, Repo: repositoryURL, Parent: siteParent,
		Release: release, Nav: nav, Features: features, Year: time.Now().Year(),
	}
	return &siteBuilder{root: root, docs: publicDocs, site: site, funcs: funcs, layouts: layouts}, nil
}

var commitDate = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// git runs a read-only git command in root; it never prompts and gives up
// after ten seconds.
func git(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// commitDates maps each source to the date (YYYY-MM-DD) of the last commit
// that touched it, as git log -1 --format=%cs -- <source> reports it. A
// source has no date when git or the repository is unavailable, when the
// file was never committed, or in a shallow clone, whose history would give
// every file the date of its one commit.
func commitDates(root string, sources []string) map[string]string {
	dates := make(map[string]string, len(sources))
	for _, source := range sources {
		dates[source] = ""
	}
	if shallow, err := git(root, "rev-parse", "--is-shallow-repository"); err != nil || shallow != "false" {
		return dates
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for _, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			date, err := git(root, "log", "-1", "--format=%cs", "--", source)
			<-limit
			if err == nil && commitDate.MatchString(date) {
				mu.Lock()
				dates[source] = date
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return dates
}

// buildSite renders the whole site in memory, then replaces out with it.
func buildSite(root, out string) error {
	b, err := newSiteBuilder(root)
	if err != nil {
		return err
	}
	pages, err := b.render()
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	plain := make([]*Page, 0, len(pages))
	for _, rp := range pages {
		files[rp.File] = rp.HTML
		plain = append(plain, rp.Page)
	}
	search, err := json.MarshalIndent(b.searchIndex(pages), "", "  ")
	if err != nil {
		return err
	}
	files[searchFile] = search
	files["sitemap.xml"] = sitemapXML(plain)
	files["robots.txt"] = []byte(robotsTxt(siteOrigin + siteBase))
	files[".nojekyll"] = nil
	for _, name := range []string{llmsFile, llmsFullFile} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		files[name] = data
	}
	if err := prepareOutDir(out, root); err != nil {
		return err
	}
	if err := copyAssets(root, out); err != nil {
		return err
	}
	for _, name := range sortedKeys(files) {
		file := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, files[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}

// render loads and renders every page: the public guides, then each file in
// website/pages.
func (b *siteBuilder) render() ([]*renderedPage, error) {
	pages, err := b.loadPages()
	if err != nil {
		return nil, err
	}
	b.paths = map[string]string{}
	byPath := map[string]*renderedPage{}
	for _, rp := range pages {
		if other, dup := byPath[rp.Page.Path]; dup {
			return nil, fmt.Errorf("%s and %s both publish %s", other.Page.Source, rp.Page.Source, rp.Page.Path)
		}
		byPath[rp.Page.Path] = rp
		b.paths[rp.Page.Source] = rp.Page.Path
	}
	b.funcs.paths = b.paths
	for _, group := range b.site.Nav {
		for _, link := range group.Links {
			if byPath[linkPage(link.Path)] == nil {
				return nil, fmt.Errorf("%s links %q to %s, which no page publishes", navFile, link.Title, link.Path)
			}
		}
	}
	for _, rp := range pages {
		if rp.Format == "markdown" {
			if err := b.renderMarkdownBody(rp); err != nil {
				return nil, fmt.Errorf("%s: %w", rp.Page.Source, err)
			}
		}
		if rp.Page.Description == "" {
			rp.Page.Description = defaultDescription
		}
	}
	b.site.Pages = sitePages(pages)
	for _, rp := range pages {
		if err := b.renderPage(rp); err != nil {
			return nil, fmt.Errorf("%s: %w", rp.Page.Source, err)
		}
	}
	return pages, nil
}

// sitePages lists the indexable pages by path, with their descriptions.
func sitePages(pages []*renderedPage) []NavLink {
	var links []NavLink
	for _, rp := range pages {
		if p := rp.Page; !p.NoIndex && p.Kind != "notfound" {
			links = append(links, NavLink{Title: p.Title, URL: p.URL, Path: p.Path, Summary: p.Description})
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Path < links[j].Path })
	return links
}

func (b *siteBuilder) loadPages() ([]*renderedPage, error) {
	var pages []*renderedPage
	sources := sortedKeys(b.docs)
	updated := commitDates(b.root, sources)
	for _, source := range sources {
		meta := b.docs[source]
		body, err := readDoc(b.root, source)
		if err != nil {
			return nil, err
		}
		pages = append(pages, &renderedPage{
			Page: &Page{
				Kind: "docs", Path: "/" + meta.Slug + "/", Title: meta.Title, Classification: meta.Class,
				Source: source, Description: llmsDescriptions[meta.Slug], Updated: updated[source],
			},
			Layout: "docs", Format: "markdown", Body: []byte(body),
		})
	}
	standalone, err := b.loadStandalonePages()
	if err != nil {
		return nil, err
	}
	pages = append(pages, standalone...)
	for _, rp := range pages {
		p := rp.Page
		if rp.NavPath == "" {
			rp.NavPath = p.Path
		}
		if p.Params == nil {
			p.Params = map[string]any{}
		}
		p.URL = siteBase + p.Path
		p.Canonical = siteOrigin + p.URL
		p.SourceURL = repositoryURL + "/blob/main/" + p.Source
		if p.Image == "" {
			p.Image = defaultImage
		}
		if strings.HasPrefix(p.Image, "/") {
			if _, err := os.Stat(filepath.Join(b.root, filepath.FromSlash(p.Image))); err != nil {
				return nil, fmt.Errorf("%s: image %s: %w", p.Source, p.Image, err)
			}
		}
		p.Image = b.funcs.abs(p.Image)
		if p.ImageAlt == "" {
			p.ImageAlt = defaultImageAlt
		}
		if p.OGType == "" {
			p.OGType = "article"
			if p.Kind == "landing" {
				p.OGType = "website"
			}
		}
		file, err := outputFile(p.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Source, err)
		}
		rp.File = file
	}
	return pages, nil
}

func (b *siteBuilder) loadStandalonePages() ([]*renderedPage, error) {
	entries, err := os.ReadDir(filepath.Join(b.root, pagesDir))
	if err != nil {
		return nil, err
	}
	var pages []*renderedPage
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		source := pagesDir + "/" + name
		format := map[string]string{".html": "html", ".md": "markdown"}[path.Ext(name)]
		if format == "" {
			return nil, fmt.Errorf("%s: pages must be .html or .md", source)
		}
		data, err := os.ReadFile(filepath.Join(b.root, pagesDir, name))
		if err != nil {
			return nil, err
		}
		fm, body, err := splitFrontMatter(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		if !pageKinds[fm.Layout] {
			return nil, fmt.Errorf("%s: layout %q must be one of docs, landing, page, notfound", source, fm.Layout)
		}
		if fm.Title == "" || fm.Permalink == "" {
			return nil, fmt.Errorf("%s: front matter needs title and permalink", source)
		}
		pages = append(pages, &renderedPage{
			Page: &Page{
				Kind: fm.Layout, Path: fm.Permalink, Title: fm.Title, Heading: fm.Heading, Description: fm.Description,
				Source: source, Image: fm.Image, ImageAlt: fm.ImageAlt, OGType: fm.OGType, NoIndex: fm.NoIndex, Params: fm.Params,
			},
			Layout: fm.Layout, Format: format, Body: body, NavPath: fm.NavPath,
		})
	}
	return pages, nil
}

// renderMarkdownBody renders a Markdown page's content; its description is
// final afterwards, before Site.Pages is built.
func (b *siteBuilder) renderMarkdownBody(rp *renderedPage) error {
	p := rp.Page
	out, err := renderMarkdown(rp.Body, markdownInput{Source: p.Source, Paths: b.paths, Base: siteBase})
	if err != nil {
		return err
	}
	p.Content = template.HTML(out.HTML)
	if p.Heading == "" {
		p.Heading = out.Heading
	}
	p.HeadingID = out.HeadingID
	p.Headings = out.Headings
	if p.Description == "" {
		p.Description = out.Description
	}
	rp.Sections = out.Sections
	return nil
}

// renderPage executes an HTML page's body, then the page's layout.
func (b *siteBuilder) renderPage(rp *renderedPage) error {
	p := rp.Page
	nav, group := markNavigation(b.site.Nav, rp.NavPath)
	p.Group = group
	p.Prev, p.Next = neighbours(b.site.Nav, rp.NavPath)
	site := *b.site
	site.Nav = nav
	view := View{Site: &site, Page: p}
	if rp.Format == "html" {
		body, err := template.New(p.Source).Funcs(b.funcs.funcMap()).Parse(string(rp.Body))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := body.Execute(&buf, view); err != nil {
			return err
		}
		content := prefixSiteRootLinks(buf.String(), siteBase)
		p.Content = template.HTML(content)
		p.Headings = htmlHeadings(content)
	}
	if p.Heading == "" {
		p.Heading = p.Title
	}
	p.Words = countWords(string(p.Content))
	p.ReadingMinutes = max(1, (p.Words+219)/220)
	layout := b.layouts[rp.Layout]
	if layout == nil {
		return fmt.Errorf("no layout template %s/layouts/%s.html", templatesDir, rp.Layout)
	}
	var buf bytes.Buffer
	if err := layout.ExecuteTemplate(&buf, "base", view); err != nil {
		return err
	}
	rp.HTML = buf.Bytes()
	return nil
}

var htmlHeading = regexp.MustCompile(`(?is)<h([23])\b[^>]*\sid="([^"]+)"[^>]*>(.*?)</h[23]>`)

// htmlHeadings lists the h2 and h3 elements with an id in rendered HTML.
func htmlHeadings(s string) []Heading {
	var headings []Heading
	for _, m := range htmlHeading.FindAllStringSubmatch(s, -1) {
		level, _ := strconv.Atoi(m[1])
		text := strings.Join(strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(m[3], " "))), " ")
		headings = append(headings, Heading{Level: level, ID: m[2], Text: text})
	}
	return headings
}

var codeBar = regexp.MustCompile(`<div class="code-bar">.*?</div>`)

// countWords counts the words a reader reads: tags and the code cards'
// language label and copy button are not counted.
func countWords(s string) int {
	s = codeBar.ReplaceAllString(s, " ")
	return len(strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(s, " "))))
}

// searchIndex lists the feature cards and every section of the guides.
func (b *siteBuilder) searchIndex(pages []*renderedPage) []SearchItem {
	var featuresTitle string
	for _, rp := range pages {
		if rp.Page.Path == "/features/" {
			featuresTitle = rp.Page.Title
		}
	}
	features := append([]Feature(nil), b.site.Features...)
	sort.Slice(features, func(i, j int) bool { return features[i].ID < features[j].ID })
	items := make([]SearchItem, 0, len(features)+len(pages)*8)
	for _, f := range features {
		items = append(items, SearchItem{Title: f.Title, Text: f.Text, URL: "features/#" + f.ID, Page: featuresTitle, Group: groupTitle(b.site.Nav, "/features/")})
	}
	for _, rp := range pages {
		if rp.Page.Kind != "docs" || len(rp.Sections) == 0 {
			continue
		}
		items = append(items, sectionItems(rp.Sections, strings.Trim(rp.Page.Path, "/"), rp.Page.Title, groupTitle(b.site.Nav, rp.NavPath))...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].URL == items[j].URL {
			return items[i].Title < items[j].Title
		}
		return items[i].URL < items[j].URL
	})
	return items
}

// sectionItems turns a page's sections into search results; the first is the
// introduction under the page title.
func sectionItems(sections []searchSection, slug, page, group string) []SearchItem {
	items := make([]SearchItem, 0, len(sections))
	for i, s := range sections {
		item := SearchItem{Title: page, Text: searchableText(s.Text), URL: slug + "/", Page: page, Group: group}
		if i > 0 {
			item.Title, item.URL = searchableText(s.Title), slug+"/#"+s.ID
		}
		items = append(items, item)
	}
	return items
}

// sitemapXML lists the canonical URL of every indexable page.
func sitemapXML(pages []*Page) []byte {
	listed := make([]*Page, 0, len(pages))
	for _, p := range pages {
		if !p.NoIndex && p.Kind != "notfound" {
			listed = append(listed, p)
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].Path < listed[j].Path })
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range listed {
		b.WriteString("  <url><loc>")
		_ = xml.EscapeText(&b, []byte(p.Canonical))
		b.WriteString("</loc></url>\n")
	}
	b.WriteString("</urlset>\n")
	return b.Bytes()
}

func robotsTxt(site string) string {
	return "User-agent: *\nAllow: /\n\nSitemap: " + site + "/sitemap.xml\n"
}

// resolvePath returns p with symbolic links resolved, including when only a
// parent of p exists.
func resolvePath(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolvePath(parent), filepath.Base(p))
}

// within reports whether p is dir or inside it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkOutDir refuses output directories whose wipe would destroy something
// else: the file system root, the home directory, the repository or any of
// their ancestors, and the site sources.
func checkOutDir(out, root, home string) error {
	if strings.TrimSpace(out) == "" {
		return errors.New("build needs an output directory")
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	abs, rootAbs, home = resolvePath(abs), resolvePath(rootAbs), resolvePath(home)
	refuse := func(why string) error { return fmt.Errorf("refusing to use %s as the output directory: %s", out, why) }
	if abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return refuse("it is the file system root")
	}
	if home != "" && within(abs, home) {
		return refuse("it is or contains the home directory")
	}
	if within(abs, rootAbs) {
		return refuse("it is or contains the repository")
	}
	for _, source := range []string{"docs", "website", ".git"} {
		dir := filepath.Join(rootAbs, source)
		if within(dir, abs) || within(abs, dir) {
			return refuse("it overlaps the " + source + " sources")
		}
	}
	return nil
}

// prepareOutDir empties out for a new build. It creates a missing directory,
// accepts an empty one, and wipes only a directory a previous build marked.
func prepareOutDir(out, root string) error {
	home, _ := os.UserHomeDir()
	if err := checkOutDir(out, root, home); err != nil {
		return err
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("refusing to build into %s: it is a symbolic link", out)
	case !info.IsDir():
		return fmt.Errorf("refusing to build into %s: it is not a directory", out)
	default:
		entries, err := os.ReadDir(abs)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			if _, err := os.Lstat(filepath.Join(abs, buildMarker)); err != nil {
				return fmt.Errorf("refusing to wipe %s: it is not empty and has no %s marker from a previous build; remove it or choose an empty directory", out, buildMarker)
			}
		}
		for _, entry := range entries {
			if err := os.RemoveAll(filepath.Join(abs, entry.Name())); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(abs, buildMarker), []byte("Written by go run ./tools/docscheck build. Every build wipes this directory.\n"), 0o644)
}

// copyAssets copies website/assets verbatim, skipping hidden files.
func copyAssets(root, out string) error {
	src := filepath.Join(root, assetsDir)
	return filepath.WalkDir(src, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if file != src && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(src, file)
		if err != nil {
			return err
		}
		dst := filepath.Join(out, filepath.FromSlash(assetsDir), rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: assets must be regular files", file)
		}
		return copyFile(file, dst)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
