package control

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The shell is the only UI response the browser must revalidate. Everything it
// links is addressed under /_v/<build id>/, so a reload is one conditional
// request for the shell instead of one per stylesheet and module (about 190).
func TestServeUIIndexLinksVersionedAssets(t *testing.T) {
	root := uiGet(t, "/", nil)
	if root.Code != http.StatusOK {
		t.Fatalf("index status = %d", root.Code)
	}
	body := root.Body.String()
	id := uiBuildID()
	if len(id) < 12 {
		t.Fatalf("build id %q is too short to be a content digest", id)
	}
	prefix := "/_v/" + id + "/"
	// The stylesheets are concatenated in document order and linked once (see
	// bundleUIShellCSS), so the sheet that stands alone and the module entries
	// are what the shell still links one by one.
	for _, want := range []string{
		`<script type="module" src="` + prefix + `js/app.js">`,
		`<link rel="stylesheet" href="` + prefix + uiBundleCSSPath + `">`,
		`<link rel="stylesheet" href="` + prefix + `css/collections-matrix.css">`,
		`<link rel="modulepreload" href="` + prefix + `js/core.js">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("served index is missing %q", want)
		}
	}
	// A module reached through two URLs is evaluated twice, with two copies of
	// its state. Every module and stylesheet reference must use the one prefix.
	bare := regexp.MustCompile(`(?:src|href)="/(?:js|css)/[^"]+"|href="/[a-z-]+\.css"`)
	if m := bare.FindAllString(body, -1); len(m) != 0 {
		t.Fatalf("served index still links unversioned assets: %v", m)
	}
	// Non-asset links are not part of the embedded build and must stay as written.
	for _, keep := range []string{`href="/api/ca.crt"`, `href="/manifest.webmanifest"`} {
		if !strings.Contains(body, keep) {
			t.Errorf("served index rewrote %s", keep)
		}
	}
	if cc := root.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index Cache-Control = %q, want no-cache so an upgrade is seen at once", cc)
	}
	etag := root.Header().Get("ETag")
	if again := uiGet(t, "/", map[string]string{"If-None-Match": etag}); again.Code != http.StatusNotModified {
		t.Fatalf("index revalidation status = %d, want 304", again.Code)
	}
}

func TestServeUIVersionedAssetsAreImmutable(t *testing.T) {
	want, err := uiFS.ReadFile("ui/js/core.js")
	if err != nil {
		t.Fatal(err)
	}
	path := "/_v/" + uiBuildID() + "/js/core.js"
	rec := uiGet(t, path, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != string(want) {
		t.Fatalf("versioned asset: status %d, %d bytes (want %d)", rec.Code, rec.Body.Len(), len(want))
	}
	cc := rec.Header().Get("Cache-Control")
	for _, directive := range []string{"private", "max-age=31536000", "immutable"} {
		if !strings.Contains(cc, directive) {
			t.Fatalf("Cache-Control = %q, want %s", cc, directive)
		}
	}
	if rec.Header().Get("ETag") == "" || rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("versioned asset lost its validator or type: %v", rec.Header())
	}
	gz := uiGet(t, path, map[string]string{"Accept-Encoding": "gzip"})
	if gz.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(gz.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("versioned asset must still negotiate gzip: %v", gz.Header())
	}
}

// A tab opened before an upgrade keeps asking for the previous build's URLs.
// It must get the current file as before, but that answer is not the content
// the URL names, so it must never be stored as immutable.
func TestServeUIStaleBuildPrefixIsNotImmutable(t *testing.T) {
	rec := uiGet(t, "/_v/0000000000000000/js/core.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stale build prefix status = %d, want the current asset", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("stale build prefix Cache-Control = %q, want no-cache", cc)
	}
}

func TestServeUIVersionedPathRejectsNonAssets(t *testing.T) {
	prefix := "/_v/" + uiBuildID() + "/"
	for _, path := range []string{
		prefix + "js/nope.js",
		prefix + "index.html", // the shell is only served at "/"
		prefix + "../control.go",
		prefix,
		"/_v/",
		"/_v/" + uiBuildID(),
	} {
		rec := uiGet(t, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("%s: a miss must not be cached as immutable", path)
		}
	}
}

func TestServeUIVersionedAssetsDoNotIssueSessionCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/_v/"+uiBuildID()+"/js/app.js", nil)
	w := httptest.NewRecorder()
	(&Hub{}).serveUI(w, r)
	if w.Code != http.StatusOK || len(w.Result().Cookies()) != 0 {
		t.Fatalf("status %d cookies %v, want 200 and no cookie", w.Code, w.Result().Cookies())
	}
}

// collections-matrix.js adds its stylesheet when the shell did not link it. The
// shell links it under the build prefix, so the check must match by suffix or
// the sheet is appended a second time, after the mobile and desktop overrides.
func TestUICollectionsMatrixStylesheetCheckSurvivesVersionedLinks(t *testing.T) {
	src := readUIAsset(t, "js/collections-matrix.js")
	if strings.Contains(src, `link[href="/css/collections-matrix.css"]`) {
		t.Fatal("stylesheet presence check matches only the unversioned href")
	}
	requireUIContains(t, src, `link[href$="/css/collections-matrix.css"]`)
}
