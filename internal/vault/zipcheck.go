package vault

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	archiveDBName       = "interceptor.db"
	archiveBodyRoot     = "bodies"
	maxZipEntries       = 1_000_000
	maxZipFileBytes     = 4 << 30
	maxZipExpandedBytes = 32 << 30
)

type zipReadLimits struct {
	entries    int
	fileBytes  int64
	totalBytes int64
}

var defaultZipReadLimits = zipReadLimits{
	entries:    maxZipEntries,
	fileBytes:  maxZipFileBytes,
	totalBytes: maxZipExpandedBytes,
}

// validateProjectZip ensures the zip looks like a full-project export
// (contains interceptor.db; no zip-slip names). Bodies are optional.
func validateProjectZip(zipPath string) error {
	return validateProjectZipWithLimits(zipPath, defaultZipReadLimits)
}

func validateProjectZipWithLimits(zipPath string, limits zipReadLimits) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("not a valid project archive: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > limits.entries {
		return fmt.Errorf("project archive exceeds entry count limit of %d", limits.entries)
	}
	sawDB := false
	var expandedBytes int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > uint64(limits.fileBytes) {
			return fmt.Errorf("archive entry %q exceeds expanded size limit of %d bytes", f.Name, limits.fileBytes)
		}
		if f.UncompressedSize64 > uint64(limits.totalBytes-expandedBytes) {
			return fmt.Errorf("project archive exceeds cumulative expanded size limit of %d bytes", limits.totalBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open archive entry %q: %w", f.Name, err)
		}
		readBytes, readErr := io.Copy(io.Discard, &io.LimitedReader{R: rc, N: limits.fileBytes + 1})
		closeErr := rc.Close()
		if readErr != nil {
			return fmt.Errorf("read archive entry %q: %w", f.Name, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close archive entry %q: %w", f.Name, closeErr)
		}
		if readBytes > limits.fileBytes {
			return fmt.Errorf("archive entry %q exceeds expanded size limit of %d bytes", f.Name, limits.fileBytes)
		}
		expandedBytes += readBytes
		if expandedBytes > limits.totalBytes {
			return fmt.Errorf("project archive exceeds cumulative expanded size limit of %d bytes", limits.totalBytes)
		}
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
