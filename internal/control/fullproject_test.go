package control

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// A full-project archive must round-trip losslessly: export from one hub, import
// into a fresh machine's GlobalDir as a new named project, and re-open that
// project directory to find the exact same flow, body, rule, and scope.
func TestFullProjectExportImportRoundTrip(t *testing.T) {
	h, s, _ := newHub(t)
	// A flow with a real body so the content-addressed blob must travel too.
	bodyHash, _ := (&projectAPI{h}).storeBody([]byte(`{"secret":"keepme"}`))
	if _, err := s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "app.test",
		Path: "/login", Status: 200, ResBodyHash: bodyHash, ResLen: 19,
	}); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	s.CreateRule(&store.Rule{Enabled: true, Type: "req-header", Match: "A: .*", Replace: "A: b"})
	s.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "*.test"})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Export the full archive.
	er, err := http.Get(ts.URL + "/api/export/full")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if ct := er.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q, want application/zip", ct)
	}
	archive, _ := io.ReadAll(er.Body)
	er.Body.Close()
	if len(archive) == 0 {
		t.Fatal("empty archive")
	}

	// Fresh hub with its own GlobalDir (simulates the second laptop).
	h2, _, _ := newHub(t)
	h2.GlobalDir = t.TempDir()
	ts2 := httptest.NewServer(h2.Handler())
	defer ts2.Close()

	ir, err := http.Post(ts2.URL+"/api/import/full?name=acme", "application/zip", bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if ir.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(ir.Body)
		t.Fatalf("import status %d: %s", ir.StatusCode, b)
	}
	ir.Body.Close()

	// Re-open the imported project directory directly and confirm every artifact.
	projDir := filepath.Join(h2.GlobalDir, "projects", "acme")
	if _, err := os.Stat(filepath.Join(projDir, "interceptor.db")); err != nil {
		t.Fatalf("imported db missing: %v", err)
	}
	s3, err := store.Open(projDir)
	if err != nil {
		t.Fatalf("open imported project: %v", err)
	}
	defer s3.Close()

	flows, _ := s3.QueryFlowsFilter(store.FlowFilter{Limit: 10})
	if len(flows) != 1 || flows[0].Host != "app.test" {
		t.Fatalf("flow not restored: %+v", flows)
	}
	rc, err := s3.OpenBody(bodyHash)
	if err != nil {
		t.Fatalf("body blob not restored: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != `{"secret":"keepme"}` {
		t.Fatalf("body content wrong: %q", got)
	}
	if r, _ := s3.ListRules(); len(r) != 1 {
		t.Fatalf("rule not restored: %d", len(r))
	}
	if sc, _ := s3.ListScopeRules(); len(sc) != 1 {
		t.Fatalf("scope not restored: %d", len(sc))
	}
}

// Importing onto an existing project name must be refused unless overwrite=1.
func TestFullProjectImportRefusesOverwrite(t *testing.T) {
	h, s, _ := newHub(t)
	s.InsertFlow(&store.Flow{Method: "GET", Scheme: "https", Host: "app.test", Path: "/", Status: 200})
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	er, _ := http.Get(ts.URL + "/api/export/full")
	archive, _ := io.ReadAll(er.Body)
	er.Body.Close()

	h2, _, _ := newHub(t)
	h2.GlobalDir = t.TempDir()
	ts2 := httptest.NewServer(h2.Handler())
	defer ts2.Close()

	post := func(q string) int {
		r, err := http.Post(ts2.URL+"/api/import/full?"+q, "application/zip", bytes.NewReader(archive))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := post("name=acme"); code != http.StatusOK {
		t.Fatalf("first import: want 200, got %d", code)
	}
	if code := post("name=acme"); code != http.StatusConflict {
		t.Fatalf("second import without overwrite: want 409, got %d", code)
	}
	if code := post("name=acme&overwrite=1"); code != http.StatusOK {
		t.Fatalf("overwrite import: want 200, got %d", code)
	}
	// A path-like name must be rejected outright.
	if code := post("name=../evil"); code != http.StatusBadRequest {
		t.Fatalf("path-like name: want 400, got %d", code)
	}
}
