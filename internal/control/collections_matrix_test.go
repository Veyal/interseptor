package control

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The collmatrix routes are mounted on the control mux, listed in the API
// catalogue, and reachable by an AI-source caller, whose runs are always
// scope-policy block (the demo collection's target is never dialled).
func TestCollmatrixRoutesMountedAndCatalogued(t *testing.T) {
	f := newCollFixture(t)
	indexed := map[string]bool{}
	for _, r := range apiRoutes {
		indexed[r.Method+" "+r.Path] = true
	}
	for _, r := range collMatrixRouteDocs() {
		if !indexed[r.Method+" "+r.Path] {
			t.Errorf("apiRoutes is missing %s %s", r.Method, r.Path)
		}
		req := httptest.NewRequest(r.Method, strings.ReplaceAll(r.Path, "{id}", "x1"), nil)
		if _, pattern := f.h.mux.Handler(req); !strings.HasPrefix(pattern, r.Method+" /api/collmatrix/") {
			t.Errorf("mux pattern for %s %s = %q", r.Method, r.Path, pattern)
		}
	}
}

func TestCollmatrixRunRejectsMissingCollection(t *testing.T) {
	f := newCollFixture(t)
	code, _ := f.do("POST", "/api/collmatrix/run", map[string]any{"collectionUid": "nope"}, hdrs{"X-Interseptor-Source": "ai"})
	if code < 400 || code >= 500 {
		t.Fatalf("status = %d, want 4xx", code)
	}
}
