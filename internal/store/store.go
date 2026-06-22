// Package store persists flow metadata in SQLite and bodies on disk.
package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store owns the SQLite database and the on-disk body directory.
type Store struct {
	db        *sql.DB
	bodiesDir string
}

// Flow is one captured request/response exchange. Bodies are referenced by
// content hash, never embedded.
type Flow struct {
	ID          int64
	TS          time.Time
	Method      string
	Scheme      string
	Host        string
	Port        int
	Path        string
	HTTPVersion string
	Status      int
	ReqHeaders  map[string][]string
	ResHeaders  map[string][]string
	ReqBodyHash string
	ResBodyHash string
	ReqLen      int64
	ResLen      int64
	Mime        string
	DurationMs  int64
	ClientAddr  string
	Error       string
}

const schema = `
CREATE TABLE IF NOT EXISTS flows (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  method TEXT, scheme TEXT, host TEXT, port INTEGER, path TEXT,
  http_version TEXT, status INTEGER,
  req_headers TEXT, res_headers TEXT,
  req_body_hash TEXT, res_body_hash TEXT,
  req_len INTEGER, res_len INTEGER,
  mime TEXT, duration_ms INTEGER, client_addr TEXT, error TEXT,
  flags INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_flows_host ON flows(host);
CREATE INDEX IF NOT EXISTS idx_flows_status ON flows(status);
CREATE INDEX IF NOT EXISTS idx_flows_method ON flows(method);
CREATE INDEX IF NOT EXISTS idx_flows_ts ON flows(ts);

CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Open creates (or opens) the database and body store under dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	bodiesDir := filepath.Join(dir, "bodies")
	if err := os.MkdirAll(bodiesDir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "interceptor.db"))
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, bodiesDir: bodiesDir}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// InsertFlow stores a new flow and sets f.ID to the assigned row id.
func (s *Store) InsertFlow(f *Flow) (int64, error) {
	rh, _ := json.Marshal(f.ReqHeaders)
	sh, _ := json.Marshal(f.ResHeaders)
	res, err := s.db.Exec(
		`INSERT INTO flows
		 (ts, method, scheme, host, port, path, http_version, status,
		  req_headers, res_headers, req_body_hash, res_body_hash,
		  req_len, res_len, mime, duration_ms, client_addr, error)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.TS.UnixMilli(), f.Method, f.Scheme, f.Host, f.Port, f.Path, f.HTTPVersion, f.Status,
		string(rh), string(sh), f.ReqBodyHash, f.ResBodyHash,
		f.ReqLen, f.ResLen, f.Mime, f.DurationMs, f.ClientAddr, f.Error)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	f.ID = id
	return id, nil
}

// GetFlow loads a single flow by id.
func (s *Store) GetFlow(id int64) (*Flow, error) {
	row := s.db.QueryRow(
		`SELECT id, ts, method, scheme, host, port, path, http_version, status,
		        req_headers, res_headers, req_body_hash, res_body_hash,
		        req_len, res_len, mime, duration_ms, client_addr, error
		 FROM flows WHERE id = ?`, id)
	return scanFlow(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFlow(row scanner) (*Flow, error) {
	var (
		f          Flow
		tsMillis   int64
		reqH, resH string
	)
	if err := row.Scan(
		&f.ID, &tsMillis, &f.Method, &f.Scheme, &f.Host, &f.Port, &f.Path, &f.HTTPVersion, &f.Status,
		&reqH, &resH, &f.ReqBodyHash, &f.ResBodyHash,
		&f.ReqLen, &f.ResLen, &f.Mime, &f.DurationMs, &f.ClientAddr, &f.Error,
	); err != nil {
		return nil, err
	}
	f.TS = time.UnixMilli(tsMillis)
	_ = json.Unmarshal([]byte(reqH), &f.ReqHeaders)
	_ = json.Unmarshal([]byte(resH), &f.ResHeaders)
	return &f, nil
}

// SetSetting upserts a key/value setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetSetting returns the value and whether it was present.
func (s *Store) GetSetting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}
