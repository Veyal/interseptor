package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// keysSchema is the standalone schema for the global API-keys database.
// Keys are shared across projects (like the CA) so a Tailscale/remote login
// survives project switches.
const keysSchema = `
CREATE TABLE IF NOT EXISTS api_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT,
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL UNIQUE,
  created INTEGER NOT NULL,
  scope TEXT NOT NULL DEFAULT 'full',
  expires INTEGER NOT NULL DEFAULT 0
);
`

// AttachGlobalKeys points API-key CRUD/verify at a SQLite file under globalDir
// (`keys.db`) instead of the project store. Existing project-local keys are
// copied into the global DB once (when the global DB has none yet), so a key
// minted before this change keeps working after a project switch.
func (s *Store) AttachGlobalKeys(globalDir string) error {
	if globalDir == "" {
		return fmt.Errorf("store.AttachGlobalKeys: empty globalDir")
	}
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(globalDir, "keys.db")
	dsn := "file:" + path +
		"?_pragma=busy_timeout(10000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(2)
	if _, err := db.Exec(keysSchema); err != nil {
		db.Close()
		return err
	}
	// Prefer migrating from the current project store first.
	if err := migrateAPIKeysInto(db, s.db); err != nil {
		db.Close()
		return err
	}
	// Named projects keep data under globalDir/projects/<name>; the first remote
	// key is usually minted on "default" (globalDir/interceptor.db). Pull those
	// in when the global keys DB is still empty.
	projectRoot := filepath.Clean(filepath.Join(s.bodiesDir, ".."))
	if filepath.Clean(globalDir) != projectRoot {
		defaultDB := filepath.Join(globalDir, "interceptor.db")
		if _, err := os.Stat(defaultDB); err == nil {
			src, err := sql.Open("sqlite", "file:"+defaultDB+"?mode=ro&_pragma=busy_timeout(5000)")
			if err == nil {
				_ = migrateAPIKeysInto(db, src)
				src.Close()
			}
		}
	}
	if s.keys != nil {
		_ = s.keys.Close()
	}
	s.keys = db
	return nil
}

// migrateAPIKeysInto copies api_keys rows from src into dst when dst is empty.
// Uses INSERT OR IGNORE so re-runs are safe. No-op if src has no api_keys table.
func migrateAPIKeysInto(dst, src *sql.DB) error {
	var n int
	if err := dst.QueryRow(`SELECT COUNT(1) FROM api_keys`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	rows, err := src.Query(`SELECT label, prefix, hash, created, scope, expires FROM api_keys`)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var label, prefix, hash, scope string
		var created, expires int64
		if err := rows.Scan(&label, &prefix, &hash, &created, &scope, &expires); err != nil {
			return err
		}
		if _, err := dst.Exec(
			`INSERT OR IGNORE INTO api_keys (label, prefix, hash, created, scope, expires) VALUES (?,?,?,?,?,?)`,
			label, prefix, hash, created, scope, expires); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) keysDB() *sql.DB {
	if s.keys != nil {
		return s.keys
	}
	return s.db
}
