package control

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func uiGet(t *testing.T, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	(&Hub{}).serveUI(rec, req)
	return rec
}

// Embedded UI assets carry a content validator so a reload revalidates with a
// conditional request instead of re-downloading every module, while
// Cache-Control: no-cache still forces that revalidation after an upgrade.
func TestServeUIAssetsRevalidateWithETag(t *testing.T) {
	want, err := uiFS.ReadFile("ui/js/core.js")
	if err != nil {
		t.Fatal(err)
	}
	first := uiGet(t, "/js/core.js", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d", first.Code)
	}
	if !bytes.Equal(first.Body.Bytes(), want) {
		t.Fatal("identity body differs from the embedded file")
	}
	etag := first.Header().Get("ETag")
	if !strings.HasPrefix(etag, `"`) || len(etag) < 10 {
		t.Fatalf("ETag = %q, want a strong quoted validator", etag)
	}
	if cc := first.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") || strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-cache (revalidate) without no-store", cc)
	}
	if ct := first.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !strings.Contains(first.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", first.Header().Get("Vary"))
	}

	for _, inm := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		again := uiGet(t, "/js/core.js", map[string]string{"If-None-Match": inm})
		if again.Code != http.StatusNotModified {
			t.Fatalf("If-None-Match %q: status = %d, want 304", inm, again.Code)
		}
		if again.Body.Len() != 0 {
			t.Fatalf("304 carried a body of %d bytes", again.Body.Len())
		}
		if again.Header().Get("ETag") != etag {
			t.Fatalf("304 ETag = %q, want %q", again.Header().Get("ETag"), etag)
		}
	}
	stale := uiGet(t, "/js/core.js", map[string]string{"If-None-Match": `"stale"`})
	if stale.Code != http.StatusOK || stale.Body.Len() != len(want) {
		t.Fatalf("stale validator: status = %d len = %d, want full 200", stale.Code, stale.Body.Len())
	}
	other := uiGet(t, "/app.css", nil)
	if other.Header().Get("ETag") == etag {
		t.Fatal("different assets must not share an ETag")
	}
}

func TestServeUIAssetsGzipWhenAccepted(t *testing.T) {
	want, err := uiFS.ReadFile("ui/app.css")
	if err != nil {
		t.Fatal(err)
	}
	plain := uiGet(t, "/app.css", map[string]string{"Accept-Encoding": "br"})
	if plain.Header().Get("Content-Encoding") != "" || !bytes.Equal(plain.Body.Bytes(), want) {
		t.Fatal("a client that does not accept gzip must get the identity body")
	}
	gz := uiGet(t, "/app.css", map[string]string{"Accept-Encoding": "gzip, deflate, br"})
	if gz.Code != http.StatusOK || gz.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status = %d Content-Encoding = %q, want 200 gzip", gz.Code, gz.Header().Get("Content-Encoding"))
	}
	if gz.Body.Len() >= len(want)/2 {
		t.Fatalf("gzip body %d bytes is not meaningfully smaller than %d", gz.Body.Len(), len(want))
	}
	if cl := gz.Header().Get("Content-Length"); cl == "" {
		t.Fatal("gzip response should carry Content-Length")
	}
	zr, err := gzip.NewReader(gz.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("gunzipped body differs (err %v)", err)
	}
	if gz.Header().Get("ETag") == plain.Header().Get("ETag") {
		t.Fatal("the gzip and identity representations need distinct ETags")
	}
	if code := uiGet(t, "/app.css", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": gz.Header().Get("ETag")}).Code; code != http.StatusNotModified {
		t.Fatalf("gzip revalidation status = %d, want 304", code)
	}
	if code := uiGet(t, "/app.css", map[string]string{"Accept-Encoding": "gzip;q=0"}).Header().Get("Content-Encoding"); code != "" {
		t.Fatal("gzip;q=0 refuses gzip")
	}
}

func TestServeUIIndexAndMissingAssets(t *testing.T) {
	root := uiGet(t, "/", map[string]string{"Accept-Encoding": "gzip"})
	if root.Code != http.StatusOK || root.Header().Get("Content-Type") != "text/html; charset=utf-8" || root.Header().Get("ETag") == "" {
		t.Fatalf("index: status %d type %q etag %q", root.Code, root.Header().Get("Content-Type"), root.Header().Get("ETag"))
	}
	if code := uiGet(t, "/js/nope.js", nil).Code; code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", code)
	}
	if code := uiGet(t, "/../control.go", nil).Code; code != http.StatusNotFound {
		t.Fatalf("traversal status = %d", code)
	}
	head := httptest.NewRequest(http.MethodHead, "/js/app.js", nil)
	rec := httptest.NewRecorder()
	(&Hub{}).serveUI(rec, head)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == "" {
		t.Fatalf("HEAD: status %d etag %q", rec.Code, rec.Header().Get("ETag"))
	}
}
