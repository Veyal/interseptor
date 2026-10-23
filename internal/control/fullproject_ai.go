package control

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// exportsDir is where AI/MCP-initiated server-side archives may be written:
// GlobalDir/exports (or ProjectDir/exports when no global dir is set).
func (h *Hub) exportsDir() string {
	switch {
	case h.GlobalDir != "":
		return filepath.Join(h.GlobalDir, "exports")
	case h.ProjectDir != "":
		return filepath.Join(h.ProjectDir, "exports")
	}
	return ""
}

// aiExportDest confines an AI-supplied export path to exportsDir. A bare file
// name is placed there; a path is accepted only if its directory is exactly
// exportsDir. Existing files are refused (409). Returns the HTTP status to use
// on error.
func (h *Hub) aiExportDest(raw string) (string, int, error) {
	dir := h.exportsDir()
	if dir == "" {
		return "", http.StatusForbidden, errors.New("no exports directory is configured for AI exports")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	base := filepath.Base(filepath.Clean(raw))
	if strings.ContainsAny(raw, `/\`) && filepath.Dir(filepath.Clean(raw)) != absDir {
		return "", http.StatusForbidden, fmt.Errorf("AI exports may only be written to %s (pass a file name)", absDir)
	}
	if base == "." || base == ".." || strings.HasPrefix(base, ".") || !strings.EqualFold(filepath.Ext(base), ".zip") {
		return "", http.StatusForbidden, errors.New("AI export file name must be a plain name ending in .zip")
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return "", http.StatusInternalServerError, err
	}
	dest := filepath.Join(absDir, base)
	if _, err := os.Lstat(dest); err == nil {
		return "", http.StatusConflict, fmt.Errorf("refusing to overwrite existing file %s", base)
	}
	return dest, 0, nil
}
