package vault

import (
	"archive/zip"
	"fmt"
	"path"
	"strings"
)

const (
	archiveDBName   = "interceptor.db"
	archiveBodyRoot = "bodies"
)

// validateProjectZip ensures the zip looks like a full-project export
// (contains interceptor.db; no zip-slip names). Bodies are optional.
func validateProjectZip(zipPath string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("not a valid project archive: %w", err)
	}
	defer zr.Close()
	sawDB := false
	for _, f := range zr.File {
		rel := strings.TrimPrefix(path.Clean("/"+f.Name), "/")
		if strings.Contains(rel, "..") {
			return fmt.Errorf("archive entry escapes: %q", f.Name)
		}
		if rel == archiveDBName {
			sawDB = true
			continue
		}
		if strings.HasPrefix(rel, archiveBodyRoot+"/") || rel == archiveBodyRoot {
			continue
		}
		// Ignore extra members (same posture as control unpack which skips unknowns).
	}
	if !sawDB {
		return fmt.Errorf("archive is missing %s — not a project export", archiveDBName)
	}
	return nil
}
