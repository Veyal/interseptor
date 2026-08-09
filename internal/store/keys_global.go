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
CREATE TABLE IF NOT EXISTS ip_allowlist (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  cidr TEXT NOT NULL UNIQUE,
  label TEXT,
  created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
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
		defaultDB := resolveProjectDB(globalDir)
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
// Durable auth state migrates independently, so deleting the final legacy key
// cannot reopen auth. INSERT OR IGNORE and setting upsert make re-runs safe.
func migrateAPIKeysInto(dst, src *sql.DB) error {
	tx, err := dst.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var keyCount int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM api_keys`).Scan(&keyCount); err != nil {
		return err
	}
	armed := keyCount > 0
	var armedValue string
	if err := src.QueryRow(`SELECT value FROM settings WHERE key = ?`, apiKeyAuthArmedSetting).Scan(&armedValue); err == nil {
		armed = armed || armedValue == "1"
	} else if err != sql.ErrNoRows && !strings.Contains(strings.ToLower(err.Error()), "no such table") {
		return err
	}

	if keyCount == 0 {
		rows, err := src.Query(`SELECT label, prefix, hash, created, scope, expires FROM api_keys`)
		if err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "no such table") {
				return err
			}
		} else {
			for rows.Next() {
				var label, prefix, hash, scope string
				var created, expires int64
				if err := rows.Scan(&label, &prefix, &hash, &created, &scope, &expires); err != nil {
					rows.Close()
					return err
				}
				armed = true
				if _, err := tx.Exec(
					`INSERT OR IGNORE INTO api_keys (label, prefix, hash, created, scope, expires) VALUES (?,?,?,?,?,?)`,
					label, prefix, hash, created, scope, expires); err != nil {
					rows.Close()
					return err
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
	}
	if armed {
		if _, err := tx.Exec(
			`INSERT INTO settings(key, value) VALUES(?, '1')
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, apiKeyAuthArmedSetting); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) keysDB() *sql.DB {
	if s.keys != nil {
		return s.keys
	}
	return s.db
}
