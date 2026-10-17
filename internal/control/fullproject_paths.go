package control

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// exportsDirName is the folder under the global data dir where server-side
// archive files live when a caller names a bare file, and the only place the
// AI channel may read or write archives (so a prompt-injected agent cannot
// overwrite dotfiles or read arbitrary files through the archive tools).
const exportsDirName = "exports"

// errArchivePath is a caller mistake in a server-side archive path (400/403).
type errArchivePath struct {
	status int
	msg    string
}

func (e *errArchivePath) Error() string { return e.msg }

// exportsDir returns <GlobalDir>/exports, or "" when storage is unconfigured.
func (h *Hub) exportsDir() string {
	if h.GlobalDir == "" {
		return ""
	}
	return filepath.Join(h.GlobalDir, exportsDirName)
}

// resolveArchivePath validates a server-side archive path. It must name a
// .zip file. A relative path (e.g. a bare file name) resolves under the
// exports dir. AI-source and API-key callers are confined to the exports dir. When create
// is set the exports dir is created (0700) if the result lies inside it.
func (h *Hub) resolveArchivePath(r *http.Request, raw string, create bool) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", &errArchivePath{http.StatusBadRequest, "path required"}
	}
	if !strings.EqualFold(filepath.Ext(p), ".zip") {
		return "", &errArchivePath{http.StatusBadRequest, "path must name a .zip file"}
	}
	exports := h.exportsDir()
	if !filepath.IsAbs(p) {
		if exports == "" {
			return "", &errArchivePath{http.StatusBadRequest, "use an absolute path: project storage location is not configured"}
		}
		p = filepath.Join(exports, p)
	}
	p = filepath.Clean(p)
	inside := exports != "" && pathWithin(exports, p)
	if archivePathConfined(r) && !inside {
		where := "the exports folder"
		if exports != "" {
			where = exports
		}
		return "", &errArchivePath{http.StatusForbidden, fmt.Sprintf("AI and API-key callers may only use archive files inside %s: pass a bare file name such as acme.zip", where)}
	}
	if inside {
		if create {
			if err := os.MkdirAll(exports, 0o700); err != nil {
				return "", err
			}
		}
		// Refuse a symlinked parent that leads out of the exports dir.
		if real, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
			realExports, _ := filepath.EvalSymlinks(exports)
			if realExports == "" || !pathWithin(realExports, filepath.Join(real, filepath.Base(p))) {
				return "", &errArchivePath{http.StatusForbidden, "archive path escapes the exports folder"}
			}
		}
	}
	return p, nil
}

// archivePathConfined reports whether r may only use the exports folder: the
// AI channel and any API-key caller (including remote clients over a share
// tunnel). Only the unauthenticated local operator may name other paths.
func archivePathConfined(r *http.Request) bool {
	tok, _ := authToken(r)
	return isAISource(r) || tok != ""
}

// pathWithin reports whether p is dir or below it (both cleaned, absolute).
func pathWithin(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// writeArchivePathErr maps a resolveArchivePath error to a response.
func writeArchivePathErr(w http.ResponseWriter, err error) {
	var pe *errArchivePath
	if errors.As(err, &pe) {
		httpErr(w, pe.status, pe.msg)
		return
	}
	httpInternalErr(w, err)
}
