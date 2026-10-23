package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func aiExportFull(t *testing.T, h *Hub, path string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path})
	req := httptest.NewRequest(http.MethodPost, "/api/export/full/file", bytes.NewReader(body))
	req.Header.Set("X-Interseptor-Source", "ai")
	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportFullFile(rec, req)
	return rec
}

func aiExportHub(t *testing.T) (*Hub, string) {
	t.Helper()
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	h.GlobalDir = t.TempDir()
	return h, filepath.Join(h.GlobalDir, "exports")
}

func TestExportFullFileAIRefusesPathOutsideExportsDir(t *testing.T) {
	h, _ := aiExportHub(t)
	outside := filepath.Join(t.TempDir(), "evil.zip")
	if rec := aiExportFull(t, h, outside); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("AI export wrote outside the exports dir: %v", err)
	}
	for _, p := range []string{"../escape.zip", "..", "sub/x.zip"} {
		if rec := aiExportFull(t, h, p); rec.Code != http.StatusForbidden {
			t.Errorf("path %q: status = %d, want 403", p, rec.Code)
		}
	}
}

func TestExportFullFileAIWritesBareNameIntoExportsDir(t *testing.T) {
	h, dir := aiExportHub(t)
	rec := aiExportFull(t, h, "acme.zip")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "acme.zip")); err != nil {
		t.Fatalf("archive not in exports dir: %v", err)
	}
	// The exports dir itself, given as an absolute path, is accepted too.
	if rec := aiExportFull(t, h, filepath.Join(dir, "second.zip")); rec.Code != http.StatusOK {
		t.Fatalf("absolute path inside exports dir: status = %d; body %s", rec.Code, rec.Body.String())
	}
}

func TestExportFullFileAINeverOverwrites(t *testing.T) {
	h, dir := aiExportHub(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "keep.zip")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := aiExportFull(t, h, "keep.zip"); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(existing); string(got) != "keep" {
		t.Fatalf("AI export replaced an existing file: %q", got)
	}
}
