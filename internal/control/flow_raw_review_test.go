package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// GET /api/flows/{id}/raw treated every side other than "res" as the request,
// so side=response (or a typo) answered 200 with the wrong message and nothing
// to tell the caller. An unknown side is now a 400; the two documented values
// and the omitted default are unchanged.
func TestFlowRawRejectsUnknownSide(t *testing.T) {
	h, s, _ := newHub(t)
	id, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/a",
		HTTPVersion: "HTTP/1.1", Status: 204,
		ReqHeaders: map[string][]string{"Host": {"example.com"}},
		ResHeaders: map[string][]string{"X-Marker": {"response-side"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	get := func(query string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/flows/" + itoa(id) + "/raw" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, readAll(resp.Body)
	}
	for _, q := range []string{"", "?side=req"} {
		if code, body := get(q); code != http.StatusOK || !strings.HasPrefix(body, "GET /a HTTP/1.1") {
			t.Errorf("raw%s: status %d body %q, want the request", q, code, body)
		}
	}
	if code, body := get("?side=res"); code != http.StatusOK || !strings.Contains(body, "X-Marker: response-side") {
		t.Errorf("raw?side=res: status %d body %q, want the response", code, body)
	}
	for _, side := range []string{"response", "request", "RES", "both", "resp"} {
		code, body := get("?side=" + side)
		if code != http.StatusBadRequest {
			t.Errorf("raw?side=%s: status %d body %q, want 400", side, code, body)
		}
		if strings.HasPrefix(body, "GET /a HTTP/1.1") {
			t.Errorf("raw?side=%s answered with the request", side)
		}
	}
}
