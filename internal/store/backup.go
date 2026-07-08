package store

import (
	"fmt"
	"os"
	"strings"
)

// BodiesDir returns the absolute directory holding content-addressed body blobs
// for this store. Callers that archive a whole project (DB + bodies) need it.
func (s *Store) BodiesDir() string { return s.bodiesDir }

// BackupTo writes a consistent, compacted snapshot of the database to destPath
// using SQLite's `VACUUM INTO`. It is safe to call on a live WAL-mode database:
// the snapshot is a single self-contained file with every committed row folded
// in (no separate -wal/-shm needed) and free pages reclaimed, so it is both
// consistent and typically smaller than a raw file copy.
//
// destPath must not already exist. We check explicitly rather than relying on
// `VACUUM INTO`'s own refusal, since that behavior is driver/platform-specific
// (observed to differ under the pure-Go modernc.org/sqlite driver across OSes).
// The filename is a SQL string literal (not a bound parameter), so single
// quotes in the path are escaped to keep it a single literal.
func (s *Store) BackupTo(destPath string) error {
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	lit := "'" + strings.ReplaceAll(destPath, "'", "''") + "'"
	_, err := s.db.Exec("VACUUM INTO " + lit)
	return err
}
