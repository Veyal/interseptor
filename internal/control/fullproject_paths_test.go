package control

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func exportFileReq(t *testing.T, h *Hub, body map[string]any, ai bool) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/export/full/file", bytes.NewReader(raw))
	if ai {
		r.Header.Set("X-Interseptor-Source", "ai")
	}
	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportFullFile(rec, r)
	return rec
}

// Regression: export_full_project overwrote any file (a dotfile became a zip).
func TestExportFullFileRefusesUnsafeDestinations(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	h.GlobalDir = t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.rc")
	if err := os.WriteFile(victim, []byte("important config"), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(outside, "old.zip")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		body map[string]any
		ai   bool
		want int
	}{
		{"non-zip target", map[string]any{"path": victim, "overwrite": true}, false, http.StatusBadRequest},
		{"existing file without overwrite", map[string]any{"path": existing}, false, http.StatusConflict},
		{"ai outside exports", map[string]any{"path": filepath.Join(outside, "new.zip")}, true, http.StatusForbidden},
		{"ai traversal", map[string]any{"path": "../../escape.zip"}, true, http.StatusForbidden},
	}
	for _, c := range cases {
		if rec := exportFileReq(t, h, c.body, c.ai); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	if got, _ := os.ReadFile(victim); string(got) != "important config" {
		t.Fatalf("victim file was modified: %q", got)
	}
	if got, _ := os.ReadFile(existing); string(got) != "old" {
		t.Fatalf("existing archive was replaced without overwrite: %q", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.zip")); !os.IsNotExist(err) {
		t.Fatalf("AI export escaped the exports folder: %v", err)
	}
}

func TestExportFullFileAIWritesBareNameUnderExports(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	h.GlobalDir = t.TempDir()
	rec := exportFileReq(t, h, map[string]any{"path": "acme.zip"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	dest := filepath.Join(h.GlobalDir, exportsDirName, "acme.zip")
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("archive not written under exports: %v", err)
	}
	zr.Close()
	if fi, err := os.Stat(filepath.Dir(dest)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("exports dir mode = %v, %v; want 0700", fi.Mode().Perm(), err)
	}
	if again := exportFileReq(t, h, map[string]any{"path": "acme.zip"}, true); again.Code != http.StatusConflict {
		t.Fatalf("second export without overwrite = %d, want 409", again.Code)
	}
	if again := exportFileReq(t, h, map[string]any{"path": "acme.zip", "overwrite": true}, true); again.Code != http.StatusOK {
		t.Fatalf("export with overwrite = %d: %s", again.Code, again.Body.String())
	}
}

func TestExportFullFileAIRefusesSymlinkedExportsEscape(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	h.GlobalDir = t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(h.GlobalDir, exportsDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(h.GlobalDir, exportsDirName, "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if rec := exportFileReq(t, h, map[string]any{"path": "link/x.zip"}, true); rec.Code != http.StatusForbidden {
		t.Fatalf("symlinked escape: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(outside, "x.zip")); !os.IsNotExist(err) {
		t.Fatal("export followed a symlink out of the exports folder")
	}
}

// A key-authenticated caller (e.g. over a share tunnel) is confined too.
func TestExportFullFileAPIKeyCallerConfinedToExports(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	h.GlobalDir = t.TempDir()
	dest := filepath.Join(t.TempDir(), "remote.zip")
	raw, _ := json.Marshal(map[string]any{"path": dest})
	r := httptest.NewRequest(http.MethodPost, "/api/export/full/file", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer ick_example")
	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportFullFile(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("API-key export outside exports: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

// A relative --data-dir must still let an absolute path inside exports through.
func TestExportsDirIsAbsolute(t *testing.T) {
	h, _, _ := newHub(t)
	h.GlobalDir = "relative-data"
	if d := h.exportsDir(); !filepath.IsAbs(d) {
		t.Fatalf("exportsDir = %q, want absolute", d)
	}
}

func TestImportFullFileAIConfinedToExports(t *testing.T) {
	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	raw, _ := json.Marshal(map[string]any{"path": filepath.Join(t.TempDir(), "x.zip"), "name": "acme"})
	r := httptest.NewRequest(http.MethodPost, "/api/import/full/file", bytes.NewReader(raw))
	r.Header.Set("X-Interseptor-Source", "ai")
	rec := httptest.NewRecorder()
	(&projectAPI{h}).importFullFile(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("AI import outside exports: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}
