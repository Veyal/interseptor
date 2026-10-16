package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const lucideFixture = `<?xml version="1.0" encoding="UTF-8"?>
<!-- @license lucide-static v0.453.0 - ISC -->
<svg
  class="lucide lucide-search"
  xmlns="http://www.w3.org/2000/svg"
  width="24"
  height="24"
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
>
  <circle cx="11" cy="11" r="8" />
  <path d="m21 21-4.3-4.3" />
</svg>
`

func TestAssetURLCarriesContentHash(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "website", "assets", "site.css"), "body{}")
	f := newSiteFuncs(root)
	got, err := f.asset("site.css")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("body{}"))
	if want := "/interseptor/website/assets/site.css?v=" + hex.EncodeToString(sum[:])[:8]; got != want {
		t.Fatalf("asset = %q, want %q", got, want)
	}
	if _, err := f.asset("missing.css"); err == nil || !strings.Contains(err.Error(), "missing.css") {
		t.Fatalf("missing asset error = %v", err)
	}
	for _, bad := range []string{"../go.mod", "/site.css", "fonts/../site.css", ""} {
		if _, err := f.asset(bad); err == nil {
			t.Errorf("asset(%q) accepted", bad)
		}
	}
}

func TestIconInlinesDecorativeSVG(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "website", "assets", "icons", "search.svg"), lucideFixture)
	f := newSiteFuncs(root)
	got, err := f.icon("search")
	if err != nil {
		t.Fatal(err)
	}
	want := `<svg class="icon" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor"><circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" /></svg>`
	if string(got) != want {
		t.Fatalf("icon =\n%s\nwant\n%s", got, want)
	}
	extra, err := f.icon("search", "search-icon")
	if err != nil || !strings.HasPrefix(string(extra), `<svg class="icon search-icon" aria-hidden="true"`) {
		t.Fatalf("icon with class = %s, %v", extra, err)
	}
	for _, bad := range []string{"missing", "../search", "Search"} {
		if _, err := f.icon(bad); err == nil {
			t.Errorf("icon(%q) accepted", bad)
		}
	}
}

func TestTemplateHelpers(t *testing.T) {
	f := newSiteFuncs(t.TempDir())
	for in, want := range map[string]string{"/features/": "/interseptor/features/", "/": "/interseptor/", "https://example.com/": "https://example.com/", "#top": "#top"} {
		if got := f.url(in); got != want {
			t.Errorf("url(%q) = %q, want %q", in, got, want)
		}
	}
	if got := f.abs("/features/"); got != "https://veyal.github.io/interseptor/features/" {
		t.Errorf("abs = %q", got)
	}
	if got := f.abs("https://example.com/x"); got != "https://example.com/x" {
		t.Errorf("abs kept external URL as %q", got)
	}
	m, err := dict("title", "Install", "level", 2)
	if err != nil || !reflect.DeepEqual(m, map[string]any{"title": "Install", "level": 2}) {
		t.Errorf("dict = %v, %v", m, err)
	}
	if _, err := dict("odd"); err == nil {
		t.Error("dict accepted an odd argument count")
	}
	if _, err := dict(1, 2); err == nil {
		t.Error("dict accepted a non-string key")
	}
	if got, err := toJSON(map[string]string{"a": "<b>"}); err != nil || got != template.JS(`{"a":"\u003cb\u003e"}`) {
		t.Errorf("json = %s, %v", got, err)
	}
	got, err := f.markdown("Use **bold** and [the guide](/getting-started/).")
	if err != nil || string(got) != `Use <strong>bold</strong> and <a href="/interseptor/getting-started/">the guide</a>.` {
		t.Errorf("markdown = %s, %v", got, err)
	}
	funcs := f.funcMap()
	for _, name := range []string{"url", "abs", "asset", "icon", "markdown", "dict", "add", "sub", "json", "lower", "contains"} {
		if funcs[name] == nil {
			t.Errorf("template function %q is not registered", name)
		}
	}
	tmpl := template.Must(template.New("t").Funcs(funcs).Parse(`{{add 2 3}} {{sub 5 3}} {{lower "ABC"}} {{contains "abc" "b"}}`))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil || buf.String() != "5 2 abc true" {
		t.Errorf("helpers rendered %q, %v", buf.String(), err)
	}
}

func TestLayoutsComposeBasePartialsAndBlocks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "website", "templates")
	writeFixture(t, filepath.Join(dir, "base.html"), `{{define "base"}}<html>{{template "head" .}}{{block "extra-head" .}}{{end}}|{{block "main" .}}{{end}}|{{template "footer" .}}{{block "extra-scripts" .}}{{end}}</html>{{end}}`)
	writeFixture(t, filepath.Join(dir, "partials", "head.html"), `{{define "head"}}<title>{{.Page.Title}}</title>{{end}}`)
	writeFixture(t, filepath.Join(dir, "partials", "footer.html"), `{{define "footer"}}<footer>{{.Site.Title}}</footer>{{end}}`)
	writeFixture(t, filepath.Join(dir, "layouts", "docs.html"), `{{define "main"}}<main>{{.Page.Content}}</main>{{end}}{{define "extra-head"}}<meta name="x">{{end}}`)
	writeFixture(t, filepath.Join(dir, "layouts", "page.html"), `{{define "main"}}<article>{{.Page.Content}}</article>{{end}}{{define "extra-scripts"}}<script></script>{{end}}`)
	layouts, err := loadLayouts(root, newSiteFuncs(root).funcMap())
	if err != nil {
		t.Fatal(err)
	}
	view := View{Site: &Site{Title: "Interseptor"}, Page: &Page{Title: "T", Content: "<p>c</p>"}}
	for name, want := range map[string]string{
		"docs": `<html><title>T</title><meta name="x">|<main><p>c</p></main>|<footer>Interseptor</footer></html>`,
		"page": `<html><title>T</title>|<article><p>c</p></article>|<footer>Interseptor</footer><script></script></html>`,
	} {
		var buf bytes.Buffer
		if err := layouts[name].ExecuteTemplate(&buf, "base", view); err != nil {
			t.Fatal(err)
		}
		if buf.String() != want {
			t.Errorf("%s layout =\n%s\nwant\n%s", name, buf.String(), want)
		}
	}
	if layouts["landing"] != nil {
		t.Error("a layout without a file was loaded")
	}
}
