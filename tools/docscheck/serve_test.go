package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSite builds a tiny site and counts builds; failWith makes builds fail.
type fakeSite struct {
	mu       sync.Mutex
	builds   int
	failWith error
}

func (f *fakeSite) build(root, out string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builds++
	if f.failWith != nil {
		return f.failWith
	}
	for name, body := range map[string]string{
		"index.html":                 "home",
		"getting-started/index.html": "guide",
		"404.html":                   "missing page",
		"website/assets/site.css":    "body{}",
	} {
		file := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSite) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.builds
}

func newTestServer(t *testing.T) (*devServer, *fakeSite, string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "website", "pages", "index.html"), "v1")
	site := &fakeSite{}
	return newDevServer(root, siteBase, t.TempDir(), site.build), site, root
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestServeRedirectsToTheBasePath(t *testing.T) {
	server, _, _ := newTestServer(t)
	for _, target := range []string{"/", "/interseptor"} {
		rec := get(t, server, http.MethodGet, target)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/interseptor/" {
			t.Errorf("GET %s = %d %q", target, rec.Code, rec.Header().Get("Location"))
		}
	}
	rec := get(t, server, http.MethodGet, "/interseptor/getting-started")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/interseptor/getting-started/" {
		t.Errorf("directory without slash = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestServeServesBuiltFilesAndNotFoundPage(t *testing.T) {
	server, _, _ := newTestServer(t)
	for target, want := range map[string]string{
		"/interseptor/":                           "home",
		"/interseptor/getting-started/":           "guide",
		"/interseptor/website/assets/site.css":    "body{}",
		"/interseptor/getting-started/index.html": "guide",
	} {
		rec := get(t, server, http.MethodGet, target)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("GET %s = %d %q", target, rec.Code, rec.Body.String())
		}
	}
	if ct := get(t, server, http.MethodGet, "/interseptor/website/assets/site.css").Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("stylesheet content type = %q", ct)
	}
	for _, target := range []string{"/interseptor/404-nope", "/elsewhere/", "/interseptor/../../etc/passwd", "/interseptor/website/"} {
		rec := get(t, server, http.MethodGet, target)
		if rec.Code != http.StatusNotFound || rec.Body.String() != "missing page" {
			t.Errorf("GET %s = %d %q, want the 404 page", target, rec.Code, rec.Body.String())
		}
	}
	if rec := get(t, server, http.MethodHead, "/interseptor/404-nope"); rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
		t.Errorf("HEAD miss = %d with %d body bytes", rec.Code, rec.Body.Len())
	}
	if rec := get(t, server, http.MethodPost, "/interseptor/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
}

func TestServeShowsBuildErrorsAsPlainText(t *testing.T) {
	server, site, root := newTestServer(t)
	site.failWith = errors.New(`template: base.html:3: function "nope" not defined`)
	rec := get(t, server, http.MethodGet, "/interseptor/")
	if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || !strings.Contains(rec.Body.String(), `function "nope" not defined`) {
		t.Fatalf("build error page = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	site.mu.Lock()
	site.failWith = nil
	site.mu.Unlock()
	server.debounce = 0
	touch(t, filepath.Join(root, "website", "pages", "index.html"))
	if rec := get(t, server, http.MethodGet, "/interseptor/"); rec.Code != http.StatusOK || rec.Body.String() != "home" {
		t.Fatalf("after the fix = %d %q", rec.Code, rec.Body.String())
	}
}

func touch(t *testing.T, file string) {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	later := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestServeRebuildsOnlyWhenSourcesChange(t *testing.T) {
	server, site, root := newTestServer(t)
	now := time.Unix(1000, 0)
	server.now = func() time.Time { return now }
	server.debounce = time.Second
	get(t, server, http.MethodGet, "/interseptor/")
	get(t, server, http.MethodGet, "/interseptor/getting-started/")
	if site.count() != 1 {
		t.Fatalf("builds = %d, want 1", site.count())
	}
	page := filepath.Join(root, "website", "pages", "index.html")
	touch(t, page)
	get(t, server, http.MethodGet, "/interseptor/")
	if site.count() != 1 {
		t.Fatalf("a change inside the debounce interval rebuilt: %d builds", site.count())
	}
	now = now.Add(2 * time.Second)
	get(t, server, http.MethodGet, "/interseptor/")
	if site.count() != 2 {
		t.Fatalf("a changed source did not rebuild: %d builds", site.count())
	}
	now = now.Add(2 * time.Second)
	get(t, server, http.MethodGet, "/interseptor/")
	if site.count() != 2 {
		t.Fatalf("unchanged sources rebuilt: %d builds", site.count())
	}
	if err := os.Remove(page); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	get(t, server, http.MethodGet, "/interseptor/")
	if site.count() != 3 {
		t.Fatalf("a deleted source did not rebuild: %d builds", site.count())
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:4000": true, "[::1]:4000": true, "localhost:4000": true, "0.0.0.0:4000": false, ":4000": false, "192.0.2.10:4000": false} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v", addr, got)
		}
	}
}
