package control

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A full-project archive is a lossless, portable copy of one project: a
// consistent snapshot of interceptor.db plus every content-addressed body blob.
// Unlike the HAR/JSON project bundle (a curated interchange subset), restoring
// it reproduces the project byte-for-byte on another machine. The CA and custom
// checks are global (shared across projects) and deliberately excluded.
const (
	archiveDBName   = "interceptor.db"
	archiveBodyRoot = "bodies"
	maxArchiveBytes = 4 << 30 // 4 GiB import cap — a runaway-upload backstop
)

// buildFullArchive writes a zip of {snapshotPath as interceptor.db, bodiesDir/**
// as bodies/**} to w. snapshotPath is a self-contained DB snapshot (see
// store.BackupTo); bodiesDir may not exist (empty project) — that is fine.
func buildFullArchive(w io.Writer, snapshotPath, bodiesDir string) error {
	zw := zip.NewWriter(w)
	if err := addFileToZip(zw, snapshotPath, archiveDBName); err != nil {
		zw.Close()
		return err
	}
	if err := filepath.WalkDir(bodiesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // skip unreadable entries and directories; blobs are leaves
		}
		if strings.HasPrefix(d.Name(), ".tmp-") {
			return nil // in-flight body captures are not part of the committed set
		}
		rel, rerr := filepath.Rel(bodiesDir, p)
		if rerr != nil {
			return nil
		}
		return addFileToZip(zw, p, archiveBodyRoot+"/"+filepath.ToSlash(rel))
	}); err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}

func addFileToZip(zw *zip.Writer, srcPath, name string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	// Store (no compression) for body blobs — they are already-compressed or
	// binary and gain little; Deflate the DB, which is highly compressible.
	method := zip.Store
	if name == archiveDBName {
		method = zip.Deflate
	}
	hw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		return err
	}
	_, err = io.Copy(hw, f)
	return err
}

// snapshotDB writes a consistent DB snapshot to a fresh temp file and returns
// its path; the caller must remove it. VACUUM INTO requires the target not to
// exist, so the temp file is created then removed before the snapshot.
func (h *Hub) snapshotDB() (string, error) {
	tmp, err := os.CreateTemp("", "interseptor-snap-*.db")
	if err != nil {
		return "", err
	}
	p := tmp.Name()
	tmp.Close()
	os.Remove(p)
	if err := h.st.BackupTo(p); err != nil {
		os.Remove(p)
		return "", err
	}
	return p, nil
}

// unpackFullArchive restores a full-project zip into destDir. It accepts only
// the two known members (interceptor.db, bodies/**) and rejects any entry that
// would escape destDir (zip-slip). It requires interceptor.db to be present.
func unpackFullArchive(zipPath, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("not a valid project archive: %w", err)
	}
	defer zr.Close()

	destAbs, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destAbs, 0o755); err != nil {
		return err
	}
	sawDB := false
	for _, f := range zr.File {
		// Normalize and constrain the entry name to the allowed members.
		rel := strings.TrimPrefix(path.Clean("/"+f.Name), "/")
		if rel != archiveDBName && !strings.HasPrefix(rel, archiveBodyRoot+"/") {
			continue // ignore anything outside the project layout
		}
		dst := filepath.Join(destAbs, filepath.FromSlash(rel))
		// Defence in depth against zip-slip: the resolved path must stay under destAbs.
		if dst != destAbs && !strings.HasPrefix(dst, destAbs+string(os.PathSeparator)) {
			return fmt.Errorf("archive entry escapes target: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if err := extractZipFile(f, dst); err != nil {
			return err
		}
		if rel == archiveDBName {
			sawDB = true
		}
	}
	if !sawDB {
		return fmt.Errorf("archive is missing %s — not a project export", archiveDBName)
	}
	return nil
}

func extractZipFile(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// projectImportDir resolves a plain project name to its target directory under
// GlobalDir/projects, refusing path-like names and requiring GlobalDir.
func (h *Hub) projectImportDir(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !safeProjectTarget(name) {
		return "", fmt.Errorf("invalid project name: use a plain name, not a path")
	}
	if h.GlobalDir == "" {
		return "", fmt.Errorf("project storage location is not configured")
	}
	return filepath.Join(h.GlobalDir, "projects", name), nil
}

// dirHasProject reports whether dir already holds an interseptor project (so an
// import doesn't silently clobber a live engagement without --overwrite).
func dirHasProject(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, archiveDBName))
	return err == nil
}

func archiveFilename(project string) string {
	project = strings.TrimSpace(project)
	if !safeProjectTarget(project) {
		project = "project"
	}
	return "interseptor-" + project + ".zip"
}

// --- HTTP handlers: streaming (UI) ---

// exportFull streams the active project as a downloadable zip archive.
func (h *projectAPI) exportFull(w http.ResponseWriter, r *http.Request) {
	snap, err := h.snapshotDB()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "snapshot: "+err.Error())
		return
	}
	defer os.Remove(snap)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+archiveFilename(h.ProjectName)+`"`)
	if err := buildFullArchive(w, snap, h.st.BodiesDir()); err != nil {
		// Headers are already sent; log-and-abort is all we can do mid-stream.
		log.Printf("control: full export failed: %v", err)
	}
}

// importFull ingests an uploaded project zip as a new named project under
// GlobalDir/projects/<name>, then reports the name so the UI can offer to
// switch. It never overwrites an existing project unless overwrite=1.
func (h *projectAPI) importFull(w http.ResponseWriter, r *http.Request) {
	destDir, err := h.projectImportDir(r.URL.Query().Get("name"))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if dirHasProject(destDir) && r.URL.Query().Get("overwrite") != "1" {
		httpErr(w, http.StatusConflict, "a project with that name already exists (pass overwrite=1 to replace)")
		return
	}
	// archive/zip needs random access, so spool the upload to a temp file first.
	tmp, err := os.CreateTemp("", "interseptor-import-*.zip")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, io.LimitReader(r.Body, maxArchiveBytes)); err != nil {
		tmp.Close()
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tmp.Close()
	if err := unpackFullArchive(tmpPath, destDir); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": filepath.Base(destDir), "dir": destDir})
}

// --- HTTP handlers: server-side file paths (MCP) ---

// exportFullFile writes the archive to an operator-supplied path on the server
// filesystem and returns the path and size. For the local MCP agent, which
// works with paths rather than binary downloads.
func (h *projectAPI) exportFullFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	dest := strings.TrimSpace(in.Path)
	if dest == "" {
		httpErr(w, http.StatusBadRequest, "path required")
		return
	}
	snap, err := h.snapshotDB()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "snapshot: "+err.Error())
		return
	}
	defer os.Remove(snap)
	out, err := os.Create(dest)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "create: "+err.Error())
		return
	}
	if err := buildFullArchive(out, snap, h.st.BodiesDir()); err != nil {
		out.Close()
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := out.Close(); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	fi, _ := os.Stat(dest)
	var size int64
	if fi != nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": dest, "bytes": size})
}

// importFullFile restores a project archive from a server-side path into a new
// named project. Mirrors importFull for the MCP agent.
func (h *projectAPI) importFullFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path      string `json:"path"`
		Name      string `json:"name"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		httpErr(w, http.StatusBadRequest, "path required")
		return
	}
	destDir, err := h.projectImportDir(in.Name)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if dirHasProject(destDir) && !in.Overwrite {
		httpErr(w, http.StatusConflict, "a project with that name already exists (pass overwrite=true to replace)")
		return
	}
	if err := unpackFullArchive(strings.TrimSpace(in.Path), destDir); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": filepath.Base(destDir), "dir": destDir})
}
