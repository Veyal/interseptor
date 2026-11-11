package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fallbackDo(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:9966"
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://127.0.0.1:9966")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func jsonError(rec *httptest.ResponseRecorder) string {
	var out struct {
		Error string `json:"error"`
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		return ""
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Error
}

// The catch-all route has no method pattern, so an unknown /api/ path fell into
// the static-asset handler and answered a text/plain "404 page not found".
func TestUnknownAPIPathAnswersJSON404(t *testing.T) {
	h, _, _ := newHub(t)
	handler := h.Handler()
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/no-such-route"},
		{http.MethodPost, "/api/no-such-route"},
		{http.MethodDelete, "/api/nope/1/2/3"},
		{http.MethodGet, "/api"},
	} {
		rec := fallbackDo(t, handler, c.method, c.path)
		if rec.Code != http.StatusNotFound || jsonError(rec) == "" {
			t.Errorf("%s %s: status %d content-type %q body %q, want a JSON 404", c.method, c.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
}

// A known API path under another method stays a 404 (an existing contract test
// forbids 405 on API routes), but it is now the JSON envelope too.
func TestKnownAPIPathWrongMethodAnswersJSON404(t *testing.T) {
	h, _, _ := newHub(t)
	rec := fallbackDo(t, h.Handler(), http.MethodDelete, "/api/flows/1/raw")
	if rec.Code != http.StatusNotFound || jsonError(rec) == "" {
		t.Fatalf("DELETE /api/flows/1/raw: status %d body %q, want a JSON 404", rec.Code, rec.Body.String())
	}
}

// A non-GET/HEAD request to a static asset path was served like a GET.
func TestStaticAssetRejectsNonGETMethods(t *testing.T) {
	h, _, _ := newHub(t)
	handler := h.Handler()
	for _, path := range []string{"/", "/js/app.js", "/no-such-asset.txt"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			rec := fallbackDo(t, handler, method, path)
			if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: status %d Allow %q, want 405 with Allow: GET, HEAD", method, path, rec.Code, rec.Header().Get("Allow"))
			}
		}
	}
	// GET and HEAD are unchanged.
	if rec := fallbackDo(t, handler, http.MethodGet, "/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("GET /: status %d", rec.Code)
	}
	if rec := fallbackDo(t, handler, http.MethodHead, "/js/app.js"); rec.Code != http.StatusOK {
		t.Fatalf("HEAD /js/app.js: status %d", rec.Code)
	}
	if rec := fallbackDo(t, handler, http.MethodGet, "/no-such-asset.txt"); rec.Code != http.StatusNotFound {
		t.Fatalf("GET of a missing asset: status %d, want 404", rec.Code)
	}
}
