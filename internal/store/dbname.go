package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// dbFilenames are the SQLite filename suffixes that may exist alongside the
// main database under WAL mode (and the legacy rollback-journal form). All are
// renamed together so a running store isn't left pointing at half-moved files.
var dbFilenames = []string{"", "-wal", "-shm", "-journal"}

const (
	legacyDBName = "interceptor.db"
	currentDBName = "interseptor.db"
)

// migrateDBFilename renames the pre-rebrand database file (interceptor.db) and
// its WAL/SHM sidecars to the post-rebrand name (interseptor.db) the first time
// a newer binary opens the project. Idempotent: a no-op once the new name exists
// or the old one is gone.
func migrateDBFilename(dir string) error {
	oldBase := filepath.Join(dir, legacyDBName)
	newBase := filepath.Join(dir, currentDBName)

	if _, err := os.Stat(newBase); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("migrate db filename: stat %s: %w", newBase, err)
	}
	if _, err := os.Stat(oldBase); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("migrate db filename: stat %s: %w", oldBase, err)
	}
	for _, suffix := range dbFilenames {
		old := oldBase + suffix
		if _, err := os.Stat(old); err != nil {
			continue
		}
		if err := os.Rename(old, newBase+suffix); err != nil {
			return fmt.Errorf("migrate db filename %s: %w", old, err)
		}
	}
	return nil
}

// resolveProjectDB returns the path of the project database in dir, preferring
// the current name (interseptor.db) and falling back to the legacy name
// (interceptor.db) for a project that hasn't been Open-ed (and thus migrated)
// yet. Callers that open a peer/default DB read-only need this because such a
// DB may predate the rename.
func resolveProjectDB(dir string) string {
	cur := filepath.Join(dir, currentDBName)
	if _, err := os.Stat(cur); err == nil {
		return cur
	}
	return filepath.Join(dir, legacyDBName)
}
