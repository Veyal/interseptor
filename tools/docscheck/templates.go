package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// layoutNames are the page kinds a layout template may implement.
var layoutNames = []string{"docs", "landing", "page", "notfound"}

// siteFuncs implements the template functions. Asset hashes and icons are
// read once per build.
type siteFuncs struct {
	root, base, origin string
	paths              map[string]string // published sources for markdown links

	mu     sync.Mutex
	hashes map[string]string
	icons  map[string]svgIcon
}

type svgIcon struct{ attrs, body string }

func newSiteFuncs(root string) *siteFuncs {
	return &siteFuncs{root: root, base: siteBase, origin: siteOrigin, paths: docSitePaths(), hashes: map[string]string{}, icons: map[string]svgIcon{}}
}

func (f *siteFuncs) funcMap() template.FuncMap {
	return template.FuncMap{
		"url":      f.url,
		"abs":      f.abs,
		"asset":    f.asset,
		"icon":     f.icon,
		"markdown": f.markdown,
		"dict":     dict,
		"add":      func(a, b int) int { return a + b },
		"sub":      func(a, b int) int { return a - b },
		"json":     toJSON,
		"lower":    strings.ToLower,
		"contains": strings.Contains,
	}
}

func isExternal(p string) bool {
	return strings.Contains(p, "://") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "mailto:") || strings.HasPrefix(p, "#")
}

// url turns a site path ("/features/") into a base-prefixed URL.
func (f *siteFuncs) url(p string) string {
	if isExternal(p) {
		return p
	}
	return f.base + "/" + strings.TrimPrefix(p, "/")
}

// abs turns a site path into an absolute URL on the published origin.
func (f *siteFuncs) abs(p string) string {
	if isExternal(p) {
		return p
	}
	return f.origin + f.url(p)
}

func cleanRelative(name string) bool {
	return name != "" && !path.IsAbs(name) && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}

// asset returns the URL of a file under website/assets with a content hash
// for cache busting. A missing file fails the build.
func (f *siteFuncs) asset(name string) (string, error) {
	if !cleanRelative(name) {
		return "", fmt.Errorf("asset %q must be a clean path relative to %s", name, assetsDir)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.hashes[name]; ok {
		return u, nil
	}
	data, err := os.ReadFile(filepath.Join(f.root, assetsDir, filepath.FromSlash(name)))
	if err != nil {
		return "", fmt.Errorf("asset %q: %w", name, err)
	}
	sum := sha256.Sum256(data)
	u := f.base + "/" + assetsDir + "/" + name + "?v=" + hex.EncodeToString(sum[:])[:8]
	f.hashes[name] = u
	return u, nil
}

var (
	xmlProlog   = regexp.MustCompile(`(?s)<\?xml.*?\?>`)
	xmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	svgOpen     = regexp.MustCompile(`(?s)^<svg\b([^>]*)>`)
	svgOwnAttr  = regexp.MustCompile(`(?is)\s(?:class|aria-hidden|focusable)\s*=\s*"[^"]*"`)
	spaceRun    = regexp.MustCompile(`\s+`)
	betweenTags = regexp.MustCompile(`>\s+<`)
	iconName    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// icon inlines website/assets/icons/<name>.svg as decorative markup: the XML
// prolog and comments are dropped, and class="icon" (plus any extra class),
// aria-hidden="true" and focusable="false" replace the file's own values.
func (f *siteFuncs) icon(name string, classes ...string) (template.HTML, error) {
	if !iconName.MatchString(name) {
		return "", fmt.Errorf("icon %q: invalid name", name)
	}
	f.mu.Lock()
	svg, ok := f.icons[name]
	f.mu.Unlock()
	if !ok {
		data, err := os.ReadFile(filepath.Join(f.root, assetsDir, "icons", name+".svg"))
		if err != nil {
			return "", fmt.Errorf("icon %q: %w", name, err)
		}
		s := xmlComment.ReplaceAllString(xmlProlog.ReplaceAllString(string(data), ""), "")
		s = betweenTags.ReplaceAllString(strings.TrimSpace(spaceRun.ReplaceAllString(s, " ")), "><")
		open := svgOpen.FindStringSubmatch(s)
		if open == nil {
			return "", fmt.Errorf("icon %q: not an SVG document", name)
		}
		svg = svgIcon{attrs: strings.TrimSpace(svgOwnAttr.ReplaceAllString(" "+open[1], "")), body: s[len(open[0]):]}
		f.mu.Lock()
		f.icons[name] = svg
		f.mu.Unlock()
	}
	class := strings.TrimSpace("icon " + strings.Join(classes, " "))
	return template.HTML(`<svg class="` + html.EscapeString(class) + `" aria-hidden="true" focusable="false" ` + svg.attrs + ">" + svg.body), nil
}

// markdown renders a short Markdown string as inline HTML.
func (f *siteFuncs) markdown(s string) (template.HTML, error) {
	out, err := renderMarkdown([]byte(s), markdownInput{Paths: f.paths, Base: f.base, Inline: true})
	if err != nil {
		return "", err
	}
	return template.HTML(out.HTML), nil
}

// dict builds a map from key/value pairs, for passing several values to a
// named template.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("dict needs key/value pairs")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict key %v is not a string", pairs[i])
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// toJSON encodes v for a script element; <, > and & are escaped.
func toJSON(v any) (template.JS, error) {
	data, err := json.Marshal(v)
	return template.JS(data), err
}

// loadLayouts parses, for each layout, base.html, every partial and that one
// layout, so a layout can override blocks independently of the others.
func loadLayouts(root string, funcs template.FuncMap) (map[string]*template.Template, error) {
	dir := filepath.Join(root, templatesDir)
	base, err := template.New("base.html").Funcs(funcs).ParseFiles(filepath.Join(dir, "base.html"))
	if err != nil {
		return nil, err
	}
	partials, err := filepath.Glob(filepath.Join(dir, "partials", "*.html"))
	if err != nil {
		return nil, err
	}
	if len(partials) > 0 {
		if _, err := base.ParseFiles(partials...); err != nil {
			return nil, err
		}
	}
	if base.Lookup("base") == nil {
		return nil, fmt.Errorf("%s/base.html must define the \"base\" template", templatesDir)
	}
	layouts := map[string]*template.Template{}
	for _, name := range layoutNames {
		file := filepath.Join(dir, "layouts", name+".html")
		if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		layout, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := layout.ParseFiles(file); err != nil {
			return nil, err
		}
		layouts[name] = layout
	}
	return layouts, nil
}
