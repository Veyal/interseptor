package store

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// FlagCollection marks a flow sent from a collection request. Unlike
// FlagRepeater these flows stay visible in History (COLL badge + chips).
const FlagCollection int64 = 1 << 9

// Collections errors.
var (
	ErrCollNotFound = errors.New("collection object not found")
	ErrCollConflict = errors.New("collection object was changed by someone else")
	ErrCollInvalid  = errors.New("invalid collection object")
)

const collSchema = `
CREATE TABLE IF NOT EXISTS ix_collections(
 uid TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
 auth_json TEXT NOT NULL DEFAULT '', events_json TEXT NOT NULL DEFAULT '', settings_json TEXT NOT NULL DEFAULT '',
 caps_json TEXT NOT NULL DEFAULT '', scope_policy TEXT NOT NULL DEFAULT 'block',
 sidecar_json TEXT NOT NULL DEFAULT '', key_order_json TEXT NOT NULL DEFAULT '', import_report_json TEXT NOT NULL DEFAULT '',
 rev INTEGER NOT NULL DEFAULT 1, ts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ix_items(
 uid TEXT PRIMARY KEY, collection_uid TEXT NOT NULL, parent_uid TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL, rank TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', description_md TEXT NOT NULL DEFAULT '',
 method TEXT NOT NULL DEFAULT '', url_json TEXT NOT NULL DEFAULT '', headers_json TEXT NOT NULL DEFAULT '',
 params_json TEXT NOT NULL DEFAULT '', body_json TEXT NOT NULL DEFAULT '', auth_json TEXT NOT NULL DEFAULT '',
 events_json TEXT NOT NULL DEFAULT '', vars_json TEXT NOT NULL DEFAULT '', assertions_json TEXT NOT NULL DEFAULT '',
 settings_json TEXT NOT NULL DEFAULT '', examples_json TEXT NOT NULL DEFAULT '', tags_json TEXT NOT NULL DEFAULT '',
 sidecar_json TEXT NOT NULL DEFAULT '', key_order_json TEXT NOT NULL DEFAULT '',
 rev INTEGER NOT NULL DEFAULT 1, ts INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS idx_ix_items_coll ON ix_items(collection_uid, parent_uid, rank);
CREATE TABLE IF NOT EXISTS ix_item_revisions(
 id INTEGER PRIMARY KEY AUTOINCREMENT, item_uid TEXT NOT NULL, rev INTEGER NOT NULL, ts INTEGER NOT NULL,
 actor TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '', snapshot TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_ix_item_revisions ON ix_item_revisions(item_uid, id DESC);
CREATE TABLE IF NOT EXISTS ix_environments(
 uid TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT 'env',
 collection_uid TEXT NOT NULL DEFAULT '', bound_identity TEXT NOT NULL DEFAULT '', base_target_pin TEXT NOT NULL DEFAULT '',
 rev INTEGER NOT NULL DEFAULT 1, ts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ix_variables(
 owner_kind TEXT NOT NULL, owner_uid TEXT NOT NULL, key TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'default',
 initial_value TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(owner_kind, owner_uid, key));
CREATE TABLE IF NOT EXISTS ix_var_current(
 owner_kind TEXT NOT NULL, owner_uid TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL DEFAULT '',
 updated_by TEXT NOT NULL DEFAULT '', ts INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(owner_kind, owner_uid, key));
CREATE TABLE IF NOT EXISTS ix_cookies(
 partition TEXT NOT NULL DEFAULT 'default', domain TEXT NOT NULL, path TEXT NOT NULL DEFAULT '/', name TEXT NOT NULL,
 value TEXT NOT NULL DEFAULT '', flags TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(partition, domain, path, name));
CREATE TABLE IF NOT EXISTS ix_tokens(
 uid TEXT PRIMARY KEY, collection_uid TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '',
 token_json TEXT NOT NULL DEFAULT '', expires INTEGER NOT NULL DEFAULT 0, ts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ix_datasets(
 uid TEXT PRIMARY KEY, collection_uid TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '',
 body_hash TEXT NOT NULL DEFAULT '', format TEXT NOT NULL DEFAULT '', rows_n INTEGER NOT NULL DEFAULT 0, ts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS ix_assets(
 hash TEXT NOT NULL, collection_uid TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0,
 ts INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(collection_uid, hash));
CREATE TABLE IF NOT EXISTS ix_runs(
 uid TEXT PRIMARY KEY, collection_uid TEXT NOT NULL DEFAULT '', env_uid TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT '', started_ts INTEGER NOT NULL DEFAULT 0, finished_ts INTEGER NOT NULL DEFAULT 0,
 summary_json TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS ix_run_results(
 id INTEGER PRIMARY KEY AUTOINCREMENT, run_uid TEXT NOT NULL, item_uid TEXT NOT NULL DEFAULT '', iteration INTEGER NOT NULL DEFAULT 0,
 flow_id INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
 result_json TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_ix_run_results ON ix_run_results(run_uid, id);
CREATE TABLE IF NOT EXISTS ix_flow_ctx(
 flow_id INTEGER PRIMARY KEY, run_id TEXT NOT NULL DEFAULT '', item_uid TEXT NOT NULL DEFAULT '', iteration INTEGER NOT NULL DEFAULT 0,
 phase TEXT NOT NULL DEFAULT '', parent_flow_id INTEGER NOT NULL DEFAULT 0, env_uid TEXT NOT NULL DEFAULT '',
 identity TEXT NOT NULL DEFAULT '', template_hash TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS ix_script_trust(
 collection_uid TEXT NOT NULL, script_hash TEXT NOT NULL, ts INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(collection_uid, script_hash));
CREATE TABLE IF NOT EXISTS ix_import_log(
 id INTEGER PRIMARY KEY AUTOINCREMENT, collection_uid TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '',
 ts INTEGER NOT NULL DEFAULT 0, report_json TEXT NOT NULL DEFAULT '');
`

// collTables lists every table created by collSchema, in dependency-free order.
var collTables = []string{"ix_collections", "ix_items", "ix_item_revisions", "ix_environments", "ix_variables",
	"ix_var_current", "ix_cookies", "ix_tokens", "ix_datasets", "ix_assets", "ix_runs", "ix_run_results",
	"ix_flow_ctx", "ix_script_trust", "ix_import_log"}

var collEnsured sync.Map // *sql.DB -> struct{}

// ensureCollections creates the collection tables on first use. It is
// additive and idempotent (CREATE IF NOT EXISTS) so old databases need no
// migration step and the main schema is untouched.
func (s *Store) ensureCollections() error {
	if _, ok := collEnsured.Load(s.db); ok {
		return nil
	}
	if _, err := s.db.Exec(collSchema); err != nil {
		return err
	}
	collEnsured.Store(s.db, struct{}{})
	return nil
}

// NewUID returns a new 26-character ULID (time-ordered, Crockford base32).
func NewUID() string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err)
	}
	out := make([]byte, 26)
	// 128 bits -> 26 base32 chars (first char carries 3 bits).
	var acc uint64
	bits := 0
	idx := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && idx >= 0 {
			out[idx] = alphabet[acc&31]
			acc >>= 5
			bits -= 5
			idx--
		}
	}
	if idx >= 0 {
		out[idx] = alphabet[acc&31]
	}
	return string(out)
}

// rawArg validates and flattens an optional JSON column.
func rawArg(name string, r json.RawMessage) (string, error) {
	if len(r) == 0 {
		return "", nil
	}
	if !json.Valid(r) {
		return "", fmt.Errorf("%w: %s is not valid JSON", ErrCollInvalid, name)
	}
	return string(r), nil
}

func rawOut(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func nowMs() int64 { return time.Now().UnixMilli() }
