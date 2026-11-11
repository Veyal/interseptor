package control

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// uiShellStylesheets lists the stylesheet links of a served shell.
func uiShellStylesheets(t *testing.T, shell []byte) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`<link rel="stylesheet" href="([^"]+)">`).FindAllStringSubmatch(string(shell), -1) {
		out = append(out, m[1])
	}
	return out
}

// Thirty-eight stylesheets mean thirty-eight round trips on a cold load, over
// six connections and behind however much latency the operator has. The shell is
// served with them concatenated in document order, so the cascade is unchanged.
func TestServedShellConcatenatesItsStylesheets(t *testing.T) {
	index, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	shell := uiShell()
	if shell == nil {
		t.Fatal("no shell")
	}
	links := uiShellStylesheets(t, shell.data)
	if len(links) != 2 {
		t.Fatalf("served shell links %d stylesheets (%v), want the bundle plus the one a script appends itself", len(links), links)
	}
	var bundleURL string
	for _, l := range links {
		if strings.Contains(l, uiVersionPrefix) && strings.HasSuffix(l, "/"+uiBundleCSSPath) {
			bundleURL = l
		}
	}
	if bundleURL == "" {
		t.Fatalf("no bundle link in %v", links)
	}

	// The bundle is the concatenation of the sheets index.html links, in the
	// order it links them, minus the one the UI appends on its own.
	var want bytes.Buffer
	seen := 0
	for _, m := range regexp.MustCompile(`<link rel="stylesheet" href="/([A-Za-z0-9_./-]+\.css)">`).FindAllSubmatch(index, -1) {
		name := string(m[1])
		if uiStandaloneSheets[name] {
			continue
		}
		data, err := uiFS.ReadFile("ui/" + name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		seen++
		want.Write(data)
		want.WriteString("\n")
	}
	if seen < 2 {
		t.Fatalf("only %d sheets bundled", seen)
	}

	name := strings.TrimPrefix(strings.SplitN(bundleURL, uiVersionPrefix, 2)[1], uiBuildID()+"/")
	a, ok := loadUIAsset(name)
	if !ok {
		t.Fatalf("the bundle %q is not served", bundleURL)
	}
	if !bytes.Equal(a.data, want.Bytes()) {
		t.Fatalf("bundle is %d bytes, want %d (concatenation in document order)", len(a.data), want.Len())
	}

	// Document order matters: a later sheet must still come after an earlier one.
	for _, pair := range [][2]string{{"app.css", "css/desktop/type.css"}, {"app.css", "mobile.css"}} {
		first, _ := uiFS.ReadFile("ui/" + pair[0])
		second, _ := uiFS.ReadFile("ui/" + pair[1])
		i, j := bytes.Index(a.data, first[:40]), bytes.Index(a.data, second[:40])
		if i < 0 || j < 0 || i > j {
			t.Errorf("%s must precede %s in the bundle (found at %d and %d)", pair[0], pair[1], i, j)
		}
	}

	// The sheet a script looks for by URL keeps its own link, or the script
	// appends a second copy at the end of head, which changes the cascade.
	if uiStandaloneSheets["css/collections-matrix.css"] != true {
		t.Fatal("collections-matrix.css is appended by collections-matrix.js; it must keep its own link")
	}
	if !strings.Contains(string(shell.data), "/css/collections-matrix.css") {
		t.Fatal("collections-matrix.css lost its link")
	}
}

func TestCSSBundleIsServedAsAnImmutableVersionedAsset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/"+uiVersionPrefix+uiBuildID()+"/"+uiBundleCSSPath, nil)
	rec := httptest.NewRecorder()
	hub := &Hub{}
	hub.serveUI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("content type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != uiCacheImmutable {
		t.Fatalf("cache-control %q, want %q", cc, uiCacheImmutable)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("no validator")
	}
	// Gzip is offered when the client asks for it, and is the same bytes.
	gz := httptest.NewRequest(http.MethodGet, "/"+uiVersionPrefix+uiBuildID()+"/"+uiBundleCSSPath, nil)
	gz.Header.Set("Accept-Encoding", "gzip")
	gzRec := httptest.NewRecorder()
	hub.serveUI(gzRec, gz)
	if gzRec.Header().Get("Content-Encoding") != "gzip" || gzRec.Body.Len() >= rec.Body.Len() {
		t.Fatalf("gzip: encoding %q, %d bytes against %d", gzRec.Header().Get("Content-Encoding"), gzRec.Body.Len(), rec.Body.Len())
	}

	// An old build id gets the current bytes but must revalidate.
	stale := httptest.NewRequest(http.MethodGet, "/"+uiVersionPrefix+"0000000000000000/"+uiBundleCSSPath, nil)
	staleRec := httptest.NewRecorder()
	hub.serveUI(staleRec, stale)
	if staleRec.Code != http.StatusOK || staleRec.Header().Get("Cache-Control") != uiCacheRevalidate {
		t.Fatalf("stale id: %d %q", staleRec.Code, staleRec.Header().Get("Cache-Control"))
	}
	// Everything else about the UI still resolves.
	for _, name := range []string{"js/app.js", "app.css", "css/desktop/type.css"} {
		r := httptest.NewRequest(http.MethodGet, "/"+uiVersionPrefix+uiBuildID()+"/"+name, nil)
		w := httptest.NewRecorder()
		hub.serveUI(w, r)
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	n, _ := fs.ReadDir(uiFS, "ui")
	if len(n) == 0 {
		t.Fatal("embedded UI empty")
	}
}
