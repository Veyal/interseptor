// Package store persists flow metadata in SQLite and bodies on disk.
package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	Flags       int64
	Note        string   // free-text annotation an operator (or the AI) attaches to the flow
	Tags        []string // labels attached to the flow (manual or AI); loaded on demand, not a flows column
}

// Flow flag bits, OR'd into Flow.Flags.
const (
	FlagIntercepted  int64 = 1 << iota // request passed through the intercept hold queue
	FlagEdited                         // request was edited before forwarding
	FlagDropped                        // request was dropped by the user (not forwarded)
	FlagCaptureError                   // a body could not be captured; forwarding still succeeded
	FlagTLSFailed                      // TLS interception failed for this flow
	FlagWebSocket                      // a protocol-upgrade (WebSocket) handshake, tunneled transparently
	FlagRepeater                       // a request sent from the Repeater module
	FlagIntruder                       // a request sent from the Intruder module
	FlagImported                       // a flow imported from a HAR file (not proxied)
	FlagActiveScan                     // a probe sent by the active scanner
	FlagAI                             // request originated from the AI assistant (over MCP)
	FlagAuthz                          // a request replayed by the authorization (access-control) tester
	FlagDiscovery                      // an endpoint found by the content-discovery (forced-browse) engine
	FlagTLSBypassed                    // CONNECT tunneled raw (no MITM) — host on the TLS-bypass list
)

// flowColumns is the canonical SELECT column order; scanFlow consumes it.
const flowColumns = `id, ts, method, scheme, host, port, path, http_version, status,
	req_headers, res_headers, req_body_hash, res_body_hash,
	req_len, res_len, mime, duration_ms, client_addr, error, flags, note`

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
  flags INTEGER NOT NULL DEFAULT 0,
  note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_flows_host ON flows(host);
CREATE INDEX IF NOT EXISTS idx_flows_status ON flows(status);
CREATE INDEX IF NOT EXISTS idx_flows_method ON flows(method);
CREATE INDEX IF NOT EXISTS idx_flows_ts ON flows(ts);
-- Composite index backs the Map's GROUP BY host, method, path aggregation and the
-- host=? filter, so a large flows table is walked via the index instead of a full scan.
CREATE INDEX IF NOT EXISTS idx_flows_endpoint ON flows(host, method, path);

CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ord INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  type TEXT NOT NULL,
  match TEXT NOT NULL,
  replace TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scan_issues (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  flow_id INTEGER NOT NULL DEFAULT 0,
  severity TEXT NOT NULL,
  title TEXT NOT NULL,
  target TEXT NOT NULL,
  detail TEXT, evidence TEXT, fix TEXT,
  UNIQUE(title, target)
);

CREATE TABLE IF NOT EXISTS ws_frames (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  flow_id INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  dir TEXT NOT NULL,
  opcode INTEGER NOT NULL,
  length INTEGER NOT NULL,
  preview TEXT
);
CREATE INDEX IF NOT EXISTS idx_ws_flow ON ws_frames(flow_id);

CREATE TABLE IF NOT EXISTS api_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT,
  prefix TEXT NOT NULL,
  hash TEXT NOT NULL,
  created INTEGER NOT NULL,
  scope TEXT NOT NULL DEFAULT 'full',
  expires INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS scope_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ord INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  action TEXT NOT NULL,
  host TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  scheme TEXT NOT NULL DEFAULT '',
  port INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS saved_views (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  data TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS activity (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  tool TEXT NOT NULL, summary TEXT, ok INTEGER, result TEXT, ms INTEGER
);

CREATE TABLE IF NOT EXISTS notes_images (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  mime TEXT NOT NULL,
  data BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  updated_ts INTEGER NOT NULL,
  severity TEXT NOT NULL DEFAULT 'Medium',
  status TEXT NOT NULL DEFAULT 'open',
  source TEXT NOT NULL DEFAULT 'human',
  title TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  evidence TEXT NOT NULL DEFAULT '',
  fix TEXT NOT NULL DEFAULT ''
);

-- PoC request/response evidence attached to a finding (many flows per finding).
CREATE TABLE IF NOT EXISTS finding_flows (
  finding_id INTEGER NOT NULL,
  flow_id INTEGER NOT NULL,
  ord INTEGER NOT NULL DEFAULT 0,
  note TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (finding_id, flow_id)
);
CREATE INDEX IF NOT EXISTS idx_finding_flows_finding ON finding_flows(finding_id);

CREATE TABLE IF NOT EXISTS flow_tags (
  flow_id INTEGER NOT NULL,
  tag TEXT NOT NULL,
  PRIMARY KEY (flow_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_flow_tags_tag ON flow_tags(tag);

CREATE TABLE IF NOT EXISTS tag_meta (
  tag TEXT PRIMARY KEY,
  color TEXT NOT NULL DEFAULT ''
);

-- One row per autonomous-pentest ("Autopilot") run. The scope/plan/budget/summary
-- columns hold opaque JSON snapshots the engine owns; the store never inspects them.
CREATE TABLE IF NOT EXISTS pentest_run (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'planning',
  scope TEXT NOT NULL DEFAULT '',
  plan TEXT NOT NULL DEFAULT '',
  budget TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_pentest_run_ts ON pentest_run(ts);

-- The machine proof-record behind a verified finding (1:1 with a finding,
-- keyed by finding_id — one proof-record per finding). Distinguishes a
-- machine-proven finding from an operator's hand-set verified status.
CREATE TABLE IF NOT EXISTS finding_verification (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  finding_id INTEGER NOT NULL UNIQUE,
  run_id INTEGER NOT NULL DEFAULT 0,
  vuln_class TEXT NOT NULL DEFAULT '',
  gates TEXT NOT NULL DEFAULT '',
  repro_count INTEGER NOT NULL DEFAULT 0,
  oob_token TEXT NOT NULL DEFAULT '',
  baseline_flow INTEGER NOT NULL DEFAULT 0,
  payload_flow INTEGER NOT NULL DEFAULT 0,
  confidence INTEGER NOT NULL DEFAULT 0,
  ts INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_finding_verification_run ON finding_verification(run_id);
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
	// Apply pragmas via the DSN so they run on EVERY pooled connection — busy_timeout
	// and synchronous are per-connection, so setting them once via db.Exec would leave
	// the other pooled connections without a busy timeout and they'd fail immediately
	// with SQLITE_BUSY ("database is locked") under write contention. WAL lets readers
	// run concurrently with the single writer; the busy timeout makes contending writers
	// wait their turn instead of erroring.
	dsn := "file:" + filepath.Join(dir, "interceptor.db") +
		"?_pragma=busy_timeout(10000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Additive column migrations: CREATE TABLE IF NOT EXISTS never alters an
	// existing table, so add later columns here. The ADD COLUMN errors with
	// "duplicate column" once present (including on a freshly-created schema),
	// which is the idempotent no-op we want to ignore.
	for _, mig := range []string{
		`ALTER TABLE flows ADD COLUMN note TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE activity ADD COLUMN intent TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE findings ADD COLUMN body TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE findings ADD COLUMN impact TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE findings ADD COLUMN cvss TEXT NOT NULL DEFAULT ''`,
		// Collaboration: scoped/expiring API keys (existing keys → full, never-expire).
		`ALTER TABLE api_keys ADD COLUMN scope TEXT NOT NULL DEFAULT 'full'`,
		`ALTER TABLE api_keys ADD COLUMN expires INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(mig); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db: db, bodiesDir: bodiesDir}
	if err := s.ensureFlowsFTS(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// InsertFlow stores a new flow and sets f.ID to the assigned row id.
func (s *Store) InsertFlow(f *Flow) (int64, error) {
	rh, _ := json.Marshal(f.ReqHeaders)
	sh, _ := json.Marshal(f.ResHeaders)
	// The flow row and its FTS index entry go in one transaction so a crash
	// between them can't leave a flow invisible to full-text search.
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`INSERT INTO flows
		 (ts, method, scheme, host, port, path, http_version, status,
		  req_headers, res_headers, req_body_hash, res_body_hash,
		  req_len, res_len, mime, duration_ms, client_addr, error, flags)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.TS.UnixMilli(), f.Method, f.Scheme, f.Host, f.Port, f.Path, f.HTTPVersion, f.Status,
		string(rh), string(sh), f.ReqBodyHash, f.ResBodyHash,
		f.ReqLen, f.ResLen, f.Mime, f.DurationMs, f.ClientAddr, f.Error, f.Flags)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO flows_fts(rowid, host, path, method, note) VALUES (?,?,?,?,?)`,
		id, f.Host, f.Path, f.Method, f.Note); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	f.ID = id
	return id, nil
}

// UpdateFlow fills in the response-side (and post-send request) fields of a flow
// that was first inserted at request time, keyed by f.ID. The immutable request
// identity (ts, scheme, host, port, version, client) is left untouched.
func (s *Store) UpdateFlow(f *Flow) error {
	rh, _ := json.Marshal(f.ReqHeaders)
	sh, _ := json.Marshal(f.ResHeaders)
	// One transaction; no pre-SELECT round-trip. host is immutable and the note is
	// not touched here, so the FTS index only needs its path/method refreshed (a
	// column UPDATE that leaves the indexed note in place).
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE flows SET
		   method=?, path=?, status=?, req_headers=?, res_headers=?,
		   req_body_hash=?, res_body_hash=?, req_len=?, res_len=?,
		   mime=?, duration_ms=?, error=?, flags=?
		 WHERE id=?`,
		f.Method, f.Path, f.Status, string(rh), string(sh),
		f.ReqBodyHash, f.ResBodyHash, f.ReqLen, f.ResLen,
		f.Mime, f.DurationMs, f.Error, f.Flags, f.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE flows_fts SET path=?, method=? WHERE rowid=?`, f.Path, f.Method, f.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetFlowNote sets (or clears, with "") the free-text note attached to a flow.
func (s *Store) SetFlowNote(id int64, note string) error {
	var host, path, method, oldNote string
	if err := s.db.QueryRow(`SELECT host, path, method, note FROM flows WHERE id=?`, id).Scan(&host, &path, &method, &oldNote); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE flows SET note=? WHERE id=?`, note, id); err != nil {
		return err
	}
	return s.replaceFlowFTS(id, host, path, method, oldNote, host, path, method, note)
}

// DeleteFlows removes the given flows and returns how many rows were deleted.
// An empty id list is a no-op. Content-addressed body files are left in place
// (they are shared/deduplicated across flows); the metadata rows are what go.
func (s *Store) DeleteFlows(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// FTS rows are keyed by rowid (= flow id), so unindex in one batch by id (the
	// content columns aren't needed to delete). Both deletes share one transaction
	// so a partial failure can't leave the full-text index out of sync with flows
	// (a stale FTS row would make a deleted flow appear in search until reused).
	if _, err := tx.Exec(`DELETE FROM flows_fts WHERE rowid IN (`+ph+`)`, args...); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id IN (`+ph+`)`, args...); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM flows WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GetFlow loads a single flow by id.
func (s *Store) GetFlow(id int64) (*Flow, error) {
	row := s.db.QueryRow(`SELECT `+flowColumns+` FROM flows WHERE id = ?`, id)
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
		&f.ReqLen, &f.ResLen, &f.Mime, &f.DurationMs, &f.ClientAddr, &f.Error, &f.Flags, &f.Note,
	); err != nil {
		return nil, err
	}
	f.TS = time.UnixMilli(tsMillis).UTC()
	_ = json.Unmarshal([]byte(reqH), &f.ReqHeaders)
	_ = json.Unmarshal([]byte(resH), &f.ResHeaders)
	return &f, nil
}

// QueryFlows returns up to limit flows, newest first.
func (s *Store) QueryFlows(limit int) ([]*Flow, error) {
	rows, err := s.db.Query(
		`SELECT `+flowColumns+` FROM flows ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Flow
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FlowCount returns the total number of captured flows.
func (s *Store) FlowCount() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM flows`).Scan(&n)
	return n, err
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
