package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func TestTransformedBodylessResponseKeepsOriginContentLength(t *testing.T) {
	eng := intercept.New()
	if err := eng.SetRules([]store.Rule{{
		ID: 4, Enabled: true, Type: "res-body", Match: "a", Replace: "b",
	}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	srv := &Server{eng: eng}
	for _, tc := range []struct {
		name   string
		method string
		status int
		cl     string
	}{
		{"HEAD", http.MethodHead, http.StatusOK, "1234"},
		{"304", http.MethodGet, http.StatusNotModified, "500"},
		{"204", http.MethodGet, http.StatusNoContent, ""},
	} {
		h := http.Header{"Content-Type": {"text/plain"}}
		if tc.cl != "" {
			h.Set("Content-Length", tc.cl)
		}
		resp := &http.Response{
			StatusCode: tc.status,
			Header:     h,
			Body:       http.NoBody,
			Request:    httptest.NewRequest(tc.method, "https://example.com/x", nil),
		}
		flow := srv.newFlow(&store.Flow{Host: "example.com"})
		_, got, _, transformed, _ := srv.maybeInterceptResponse(flow, resp)
		if !transformed {
			t.Fatalf("%s: response not transformed", tc.name)
		}
		if cl := got.Get("Content-Length"); cl != tc.cl {
			t.Errorf("%s: Content-Length = %q, want origin's %q", tc.name, cl, tc.cl)
		}
	}
}
