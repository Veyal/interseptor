package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func writeParamTestBody(t *testing.T, s *store.Store, content string) string {
	t.Helper()
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestListParamsRejectsMissingBodyEvidence(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com", Path: "/submit",
		ReqHeaders:  map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		ReqBodyHash: strings.Repeat("a", 64), ReqLen: 9,
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/params", nil)
	rec := httptest.NewRecorder()
	(&flowAPI{h}).listParams(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}

func TestListParamsAggregatesQueryAndForm(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.test", Port: 443,
		Path: "/api?id=1&debug=true", Status: 200,
	})
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(2), Method: "POST", Scheme: "https", Host: "app.test", Port: 443,
		Path: "/login", Status: 200,
		ReqHeaders:  map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		ReqBodyHash: writeParamTestBody(t, s, "user=admin&pass=secret"),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/params?host=app.test", nil)
	rec := httptest.NewRecorder()
	(&flowAPI{h}).listParams(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Hosts []struct {
			Host   string       `json:"host"`
			Params []minedParam `json:"params"`
		} `json:"hosts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Hosts) != 1 || out.Hosts[0].Host != "app.test" {
		t.Fatalf("hosts: %+v", out.Hosts)
	}
	names := map[string]string{}
	for _, p := range out.Hosts[0].Params {
		names[p.Name] = p.Source
	}
	for _, want := range []struct{ n, src string }{
		{"id", "query"}, {"debug", "query"}, {"user", "form"}, {"pass", "form"},
	} {
		if names[want.n] != want.src {
			t.Fatalf("param %q source=%q want %q; names=%v", want.n, names[want.n], want.src, names)
		}
	}
}

func TestListParamsReportsWhenSampleIsCapped(t *testing.T) {
	h, s, _ := newHub(t)
	for i := 1; i <= 3; i++ {
		s.InsertFlow(&store.Flow{
			TS: time.UnixMilli(int64(i)), Method: "GET", Scheme: "https", Host: "app.test", Port: 443,
			Path: "/api?id=1", Status: 200,
		})
	}
	get := func(q string) (scanned int, capped bool) {
		rec := httptest.NewRecorder()
		(&flowAPI{h}).listParams(rec, httptest.NewRequest(http.MethodGet, "/api/params?"+q, nil))
		var out struct {
			FlowsScanned int  `json:"flowsScanned"`
			Capped       bool `json:"capped"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.FlowsScanned, out.Capped
	}
	if n, capped := get("host=app.test&limit=2"); n != 2 || !capped {
		t.Fatalf("limit=2: scanned=%d capped=%v, want 2 true", n, capped)
	}
	if n, capped := get("host=app.test&limit=10"); n != 3 || capped {
		t.Fatalf("limit=10: scanned=%d capped=%v, want 3 false", n, capped)
	}
}

func TestJsonTopKeys(t *testing.T) {
	keys := jsonTopKeys([]byte(`{"a":1,"nested":{"b":2}}`))
	if len(keys) != 2 {
		t.Fatalf("got %d keys", len(keys))
	}
	if _, ok := keys["a"]; !ok {
		t.Fatalf("missing a in %v", keys)
	}
	if _, ok := keys["nested"]; !ok {
		t.Fatalf("missing nested in %v", keys)
	}
}
