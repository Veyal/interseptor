package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exportProjectJSON(t *testing.T, h *Hub) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	(&projectAPI{Hub: h}).exportProject(rec, httptest.NewRequest(http.MethodGet, "/api/export/project", nil))
	if rec.Code != 200 {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

func importProjectJSON(t *testing.T, h *Hub, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	(&projectAPI{Hub: h}).importProject(rec, httptest.NewRequest(http.MethodPost, "/api/import/project", bytes.NewReader(body)))
	return rec
}

func TestProjectBundleCarriesScrubbedCollections(t *testing.T) {
	h, s, _ := newHub(t)
	seedArchiveSecrets(t, s)
	data := exportProjectJSON(t, h)
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	if probe["version"] != "2" {
		t.Fatalf("bundle version = %v, want 2", probe["version"])
	}
	if _, ok := probe["collections"]; !ok {
		t.Fatal("bundle lacks collections section")
	}
	if strings.Contains(string(data), archiveCanary) {
		t.Fatal("secret canary leaked into the project bundle")
	}

	h2, s2, _ := newHub(t)
	rec := importProjectJSON(t, h2, data)
	if rec.Code != 200 {
		t.Fatalf("import %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp["importedCollections"]; !ok {
		t.Fatalf("import response lacks importedCollections: %v", resp)
	}
	cs, _ := s2.ListCollections()
	if len(cs) != 1 {
		t.Fatalf("collections after import = %d, want 1", len(cs))
	}
	// Idempotent re-import.
	importProjectJSON(t, h2, data)
	if cs, _ = s2.ListCollections(); len(cs) != 1 {
		t.Fatalf("re-import duplicated collections: %d", len(cs))
	}
}

func TestProjectBundleOldVersionWithoutCollectionsStillImports(t *testing.T) {
	h, _, _ := newHub(t)
	rec := importProjectJSON(t, h, []byte(`{"version":"1","har":null,"rules":[],"scope":[],"settings":{}}`))
	if rec.Code != 200 {
		t.Fatalf("old bundle import %d %s", rec.Code, rec.Body.String())
	}
}

func TestProjectBundleNewerVersionRejected(t *testing.T) {
	h, _, _ := newHub(t)
	rec := importProjectJSON(t, h, []byte(`{"version":"99","settings":{}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("newer bundle status = %d, want 400", rec.Code)
	}
}

func TestProjectBundleWithoutCollectionsOmitsSection(t *testing.T) {
	h, _, _ := newHub(t)
	var probe map[string]any
	_ = json.Unmarshal(exportProjectJSON(t, h), &probe)
	if _, ok := probe["collections"]; ok {
		t.Fatal("empty project must not emit a collections section")
	}
}
