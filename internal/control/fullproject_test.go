package control

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestAddArchiveFilesReportsWalkErrors(t *testing.T) {
	badArchive := zip.NewWriter(io.Discard)
	if err := addArchiveFiles(badArchive, string([]byte{0}), archiveBodyRoot, true); err == nil {
		t.Fatal("addArchiveFiles swallowed an invalid source-path walk error")
	}
	_ = badArchive.Close()

	missingArchive := zip.NewWriter(io.Discard)
	if err := addArchiveFiles(missingArchive, filepath.Join(t.TempDir(), "optional-missing"), archiveCodecRoot, false); err != nil {
		t.Fatalf("optional missing archive directory should be allowed: %v", err)
	}
	if err := missingArchive.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAddArchiveFilesRejectsSymlinks(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("must not enter project archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	codecs := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(codecs, "linked.star")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	zw := zip.NewWriter(io.Discard)
	err := addArchiveFiles(zw, codecs, archiveCodecRoot, false)
	_ = zw.Close()
	if err == nil {
		t.Fatal("codec symlink was followed into the project archive")
	}
}

func TestProjectImportDirRejectsReservedDefault(t *testing.T) {
	h := &Hub{GlobalDir: t.TempDir()}
	for _, name := range []string{"default", "Default", "DEFAULT"} {
		if dir, err := h.projectImportDir(name); err == nil {
			t.Errorf("projectImportDir(%q) = %q, want reserved-name error", name, dir)
		}
	}
}

func TestExportFullReportsArchiveFailureBeforeSendingZip(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = string([]byte{0})
	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportFull(rec, httptest.NewRequest(http.MethodGet, "/api/export/full", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "application/zip") {
		t.Fatalf("failed export was presented as ZIP: headers = %#v", rec.Header())
	}
}

func TestExportFullFilePreservesDestinationOnArchiveFailure(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = string([]byte{0})
	dest := filepath.Join(t.TempDir(), "known-good.zip")
	if err := os.WriteFile(dest, []byte("known-good-backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"path": dest})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportFullFile(rec, httptest.NewRequest(http.MethodPost, "/api/export/full/file", bytes.NewReader(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "known-good-backup" {
		t.Fatalf("failed export replaced existing destination with %q", got)
	}
}

func TestExportFullFileReplacesDestinationAfterSuccess(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	dest := filepath.Join(t.TempDir(), "project.zip")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"path": dest})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportFullFile(rec, httptest.NewRequest(http.MethodPost, "/api/export/full/file", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("replacement is not a ZIP: %v", err)
	}
	defer zr.Close()
	if len(zr.File) == 0 || zr.File[0].Name != archiveDBName {
		t.Fatalf("replacement archive entries = %+v", zr.File)
	}
}

func TestExportFullFileRejectsTrailingJSONBeforeCreatingDestination(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	dest := filepath.Join(t.TempDir(), "project.zip")
	body, err := json.Marshal(map[string]string{"path": dest})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportFullFile(rec, httptest.NewRequest(http.MethodPost, "/api/export/full/file", io.MultiReader(bytes.NewReader(body), strings.NewReader(`{}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected export created destination: %v", err)
	}
}

func TestImportFullFileRejectsTrailingJSONBeforeInstallingProject(t *testing.T) {
	source, _, _ := newHub(t)
	source.ProjectDir = t.TempDir()
	exported := httptest.NewRecorder()
	(&projectAPI{source}).exportFull(exported, httptest.NewRequest(http.MethodGet, "/api/export/full", nil))
	if exported.Code != http.StatusOK {
		t.Fatalf("source export status = %d, body = %s", exported.Code, exported.Body.String())
	}
	archive := filepath.Join(t.TempDir(), "source.zip")
	if err := os.WriteFile(archive, exported.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	target, _, _ := newHub(t)
	target.GlobalDir = t.TempDir()
	body, err := json.Marshal(map[string]any{"path": archive, "name": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&projectAPI{target}).importFullFile(rec, httptest.NewRequest(http.MethodPost, "/api/import/full/file", io.MultiReader(bytes.NewReader(body), strings.NewReader(`{}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if dirHasProject(filepath.Join(target.GlobalDir, "projects", "acme")) {
		t.Fatal("rejected import installed the project")
	}
}

func TestDestinationImportLockNormalizesCaseAliases(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "projects", "..", "projects", "Acme")
	lower := filepath.Join(root, "projects", "acme")
	upperKey, err := normalizeImportLockKey(upper)
	if err != nil {
		t.Fatalf("normalize upper: %v", err)
	}
	lowerKey, err := normalizeImportLockKey(lower)
	if err != nil {
		t.Fatalf("normalize lower: %v", err)
	}
	if upperKey != lowerKey {
		t.Fatalf("case aliases use different lock keys: %q != %q", upperKey, lowerKey)
	}
	if !filepath.IsAbs(upperKey) {
		t.Fatalf("lock key is not absolute: %q", upperKey)
	}
}

func TestSwapProjectDirectoriesRollsBackRenameFailure(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "project")
	stage := filepath.Join(parent, "stage")
	backup := filepath.Join(parent, "backup")
	os.MkdirAll(dest, 0o755)
	os.MkdirAll(stage, 0o755)
	os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(stage, "new"), []byte("new"), 0o644)
	ops := projectDirOps{
		rename: func(oldPath, newPath string) error {
			if oldPath == stage {
				return errors.New("injected install rename failure")
			}
			return os.Rename(oldPath, newPath)
		},
		removeAll: os.RemoveAll,
	}
	if err := swapProjectDirectories(stage, dest, backup, ops); err == nil {
		t.Fatal("swap succeeded despite injected rename failure")
	}
	if _, err := os.Stat(filepath.Join(dest, "old")); err != nil {
		t.Fatalf("original project not restored: %v", err)
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback directory remains after successful restore: %v", err)
	}
}

func TestSwapProjectDirectoriesRetainsBackupOnCleanupFailure(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "project")
	stage := filepath.Join(parent, "stage")
	backup := filepath.Join(parent, "backup")
	os.MkdirAll(dest, 0o755)
	os.MkdirAll(stage, 0o755)
	os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(stage, "new"), []byte("new"), 0o644)
	ops := projectDirOps{
		rename: os.Rename,
		removeAll: func(string) error {
			return errors.New("injected cleanup failure")
		},
	}
	if err := swapProjectDirectories(stage, dest, backup, ops); err == nil {
		t.Fatal("cleanup failure was not reported")
	}
	if _, err := os.Stat(filepath.Join(dest, "new")); err != nil {
		t.Fatalf("new project not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "old")); err != nil {
		t.Fatalf("rollback backup not retained: %v", err)
	}
}

func TestSwapProjectDirectoriesRetainsBackupWhenRollbackFails(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "project")
	stage := filepath.Join(parent, "stage")
	backup := filepath.Join(parent, "backup")
	os.MkdirAll(dest, 0o755)
	os.MkdirAll(stage, 0o755)
	os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(stage, "new"), []byte("new"), 0o644)
	ops := projectDirOps{
		rename: func(oldPath, newPath string) error {
			if oldPath == stage || oldPath == backup {
				return errors.New("injected rename failure")
			}
			return os.Rename(oldPath, newPath)
		},
		removeAll: os.RemoveAll,
	}
	err := swapProjectDirectories(stage, dest, backup, ops)
	if err == nil || !strings.Contains(err.Error(), "original retained") {
		t.Fatalf("swap error=%v, want retained-backup diagnostic", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "old")); err != nil {
		t.Fatalf("rollback backup not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "new")); err != nil {
		t.Fatalf("staged replacement unexpectedly removed: %v", err)
	}
}

func corruptArchivedBody(t *testing.T, archive []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		h := f.FileHeader
		w, err := zw.CreateHeader(&h)
		if err != nil {
			t.Fatalf("create archive entry: %v", err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open archive entry: %v", err)
		}
		if len(f.Name) > len("bodies/") && f.Name[:len("bodies/")] == "bodies/" && !f.FileInfo().IsDir() {
			_, err = io.WriteString(w, "corrupt")
		} else {
			_, err = io.Copy(w, rc)
		}
		rc.Close()
		if err != nil {
			t.Fatalf("copy archive entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return out.Bytes()
}

func archiveWithoutBodies(t *testing.T, archive []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "bodies/") && !f.FileInfo().IsDir() {
			continue
		}
		w, err := zw.CreateHeader(&f.FileHeader)
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry: %v", err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			t.Fatalf("copy entry: %v", err)
		}
		rc.Close()
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return out.Bytes()
}

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
func TestFullProjectArchivePreservesCodecs(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	codecPath := filepath.Join(h.ProjectDir, "codecs", "example.star")
	if err := os.MkdirAll(filepath.Dir(codecPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codecPath, []byte("def decode(flow):\n    return None\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// When
	resp, err := http.Get(ts.URL + "/api/export/full")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Then
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range zr.File {
		if file.Name == "codecs/example.star" {
			return
		}
	}
	t.Fatal("project codec missing from full archive")
}

func TestFullProjectCodecRoundTripPreservesExactBytes(t *testing.T) {
	// Given
	source, _, _ := newHub(t)
	source.ProjectDir = t.TempDir()
	codecBytes := []byte("# codec\ndef decode(flow):\n    return None\n")
	codecPath := filepath.Join(source.ProjectDir, "codecs", "exact.star")
	if err := os.MkdirAll(filepath.Dir(codecPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codecPath, codecBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sourceServer := httptest.NewServer(source.Handler())
	defer sourceServer.Close()
	resp, err := http.Get(sourceServer.URL + "/api/export/full")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	target, _, _ := newHub(t)
	target.GlobalDir = t.TempDir()
	targetServer := httptest.NewServer(target.Handler())
	defer targetServer.Close()

	// When
	resp, err = http.Post(targetServer.URL+"/api/import/full?name=codec-copy", "application/zip", bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("codec archive import status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	restored, err := os.ReadFile(filepath.Join(target.GlobalDir, "projects", "codec-copy", "codecs", "exact.star"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, codecBytes) {
		t.Fatalf("restored codec bytes = %q, want %q", restored, codecBytes)
	}
}

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

func TestFullProjectOverwriteRejectsCorruptBodyAndPreservesOriginal(t *testing.T) {
	source, sourceStore, _ := newHub(t)
	bodyHash, _ := (&projectAPI{source}).storeBody([]byte("source body"))
	if _, err := sourceStore.InsertFlow(&store.Flow{
		TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "source.example.com",
		Path: "/", Status: 200, ResBodyHash: bodyHash, ResLen: 11,
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	sourceServer := httptest.NewServer(source.Handler())
	defer sourceServer.Close()
	resp, err := http.Get(sourceServer.URL + "/api/export/full")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	archive = corruptArchivedBody(t, archive)

	target, _, _ := newHub(t)
	target.GlobalDir = t.TempDir()
	dest := filepath.Join(target.GlobalDir, "projects", "acme")
	original, err := store.Open(dest)
	if err != nil {
		t.Fatalf("open original: %v", err)
	}
	if _, err := original.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https",
		Host: "original.example.com", Path: "/", Status: 200,
	}); err != nil {
		t.Fatalf("seed original: %v", err)
	}
	original.Close()
	targetServer := httptest.NewServer(target.Handler())
	defer targetServer.Close()

	resp, err = http.Post(targetServer.URL+"/api/import/full?name=acme&overwrite=1", "application/zip", bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("corrupt overwrite status = %d, want 400", resp.StatusCode)
	}
	reopened, err := store.Open(dest)
	if err != nil {
		t.Fatalf("original project no longer opens: %v", err)
	}
	defer reopened.Close()
	flows, err := reopened.QueryFlows(10)
	if err != nil || len(flows) != 1 || flows[0].Host != "original.example.com" {
		t.Fatalf("original project changed: flows=%+v err=%v", flows, err)
	}
}

func TestFullProjectImportRejectsDatabaseBodyReferenceMissingFromArchive(t *testing.T) {
	source, sourceStore, _ := newHub(t)
	bodyHash, _ := (&projectAPI{source}).storeBody([]byte("required body"))
	if _, err := sourceStore.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com",
		Path: "/", Status: 200, ResBodyHash: bodyHash, ResLen: 13,
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	server := httptest.NewServer(source.Handler())
	defer server.Close()
	resp, err := http.Get(server.URL + "/api/export/full")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	archive = archiveWithoutBodies(t, archive)

	target, _, _ := newHub(t)
	target.GlobalDir = t.TempDir()
	targetServer := httptest.NewServer(target.Handler())
	defer targetServer.Close()
	resp, err = http.Post(targetServer.URL+"/api/import/full?name=missing", "application/zip", bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.StatusCode)
	}
	if dirHasProject(filepath.Join(target.GlobalDir, "projects", "missing")) {
		t.Fatal("invalid project was installed")
	}
}

func TestValidateImportedProjectRejectsMissingOriginalBody(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "POST", Scheme: "https", Host: "example.com",
		Path: "/edited", Status: 200, OriginalReqBodyHash: strings.Repeat("a", 64),
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "interseptor.db"), filepath.Join(dir, archiveDBName)); err != nil {
		t.Fatal(err)
	}

	if err := validateImportedProject(dir); err == nil {
		t.Fatal("project with a missing original request body passed validation")
	}
}

func TestUnpackFullArchiveRejectsExpandedFileOverLimitAndRemovesPartialFile(t *testing.T) {
	// Given
	archivePath := filepath.Join(t.TempDir(), "project.zip")
	writeProjectZip(t, archivePath, []zipMember{{name: archiveDBName, data: bytes.Repeat([]byte("x"), 33)}})
	dest := t.TempDir()

	// When
	err := unpackFullArchiveWithLimits(archivePath, dest, archiveReadLimits{entries: 8, fileBytes: 32, totalBytes: 128})

	// Then
	if err == nil || !strings.Contains(err.Error(), "expanded size limit") {
		t.Fatalf("unpack error=%v, want expanded size limit", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, archiveDBName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial file remains: %v", statErr)
	}
}

func TestUnpackFullArchiveAcceptsExpandedFileAtLimit(t *testing.T) {
	// Given
	archivePath := filepath.Join(t.TempDir(), "project.zip")
	writeProjectZip(t, archivePath, []zipMember{{name: archiveDBName, data: bytes.Repeat([]byte("x"), 32)}})
	dest := t.TempDir()

	// When
	err := unpackFullArchiveWithLimits(archivePath, dest, archiveReadLimits{entries: 8, fileBytes: 32, totalBytes: 128})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, archiveDBName))
	if err != nil || info.Size() != 32 {
		t.Fatalf("extracted DB size=%v err=%v", info, err)
	}
}

func TestUnpackFullArchiveRejectsEntryAndCumulativeLimits(t *testing.T) {
	tests := []struct {
		name    string
		members []zipMember
		limits  archiveReadLimits
		want    string
	}{
		{name: "entry count", members: []zipMember{{archiveDBName, []byte("db")}, {"extra", []byte("x")}}, limits: archiveReadLimits{entries: 1, fileBytes: 32, totalBytes: 64}, want: "entry count limit"},
		{name: "cumulative bytes", members: []zipMember{{archiveDBName, bytes.Repeat([]byte("x"), 24)}, {"bodies/aa/bb/aabb000000000000000000000000000000000000000000000000000000000000", bytes.Repeat([]byte("x"), 24)}}, limits: archiveReadLimits{entries: 8, fileBytes: 32, totalBytes: 40}, want: "cumulative expanded size limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "project.zip")
			writeProjectZip(t, archivePath, tt.members)

			err := unpackFullArchiveWithLimits(archivePath, t.TempDir(), tt.limits)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unpack error=%v, want %s", err, tt.want)
			}
		})
	}
}

type zipMember struct {
	name string
	data []byte
}

func writeProjectZip(t *testing.T, filename string, members []zipMember) {
	t.Helper()
	out, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for _, member := range members {
		w, err := zw.Create(member.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFullProjectConcurrentOverwritesRemainUsable(t *testing.T) {
	source, sourceStore, _ := newHub(t)
	if _, err := sourceStore.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https",
		Host: "example.com", Path: "/", Status: 200,
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	sourceServer := httptest.NewServer(source.Handler())
	defer sourceServer.Close()
	resp, err := http.Get(sourceServer.URL + "/api/export/full")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	archive, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	archivePath := filepath.Join(t.TempDir(), "project.zip")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "projects", "shared")
	if err := installFullArchive(archivePath, dest, false); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	firstAtSwap := make(chan struct{})
	releaseFirst := make(chan struct{})
	var renameMu sync.Mutex
	destRenameCalls := 0
	ops := projectDirOps{
		rename: func(oldPath, newPath string) error {
			if oldPath == dest {
				renameMu.Lock()
				destRenameCalls++
				call := destRenameCalls
				renameMu.Unlock()
				if call == 1 {
					close(firstAtSwap)
					<-releaseFirst
				}
			}
			return os.Rename(oldPath, newPath)
		},
		removeAll: os.RemoveAll,
	}
	results := make(chan error, 2)
	go func() { results <- installFullArchiveWithOps(archivePath, dest, true, ops) }()
	<-firstAtSwap
	go func() { results <- installFullArchiveWithOps(archivePath, dest, true, ops) }()
	deadline := time.Now().Add(time.Second)
	for projectImportLocks.references(dest) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("second overwrite did not queue behind destination lock")
		}
		time.Sleep(time.Millisecond)
	}
	renameMu.Lock()
	callsWhileBlocked := destRenameCalls
	renameMu.Unlock()
	if callsWhileBlocked != 1 {
		t.Fatalf("second overwrite entered replacement critical section; rename calls=%d", callsWhileBlocked)
	}
	close(releaseFirst)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent overwrite: %v", err)
		}
	}
	reopened, err := store.Open(dest)
	if err != nil {
		t.Fatalf("open final project: %v", err)
	}
	flows, err := reopened.QueryFlows(10)
	reopened.Close()
	if err != nil || len(flows) != 1 || flows[0].Host != "example.com" {
		t.Fatalf("final project unusable: flows=%+v err=%v", flows, err)
	}
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatalf("read projects dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "-staging-") || strings.Contains(entry.Name(), "-rollback-") {
			t.Fatalf("temporary import directory leaked: %s", entry.Name())
		}
	}
}
