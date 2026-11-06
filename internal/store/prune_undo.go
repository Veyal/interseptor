package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// pruneRow is one snapshot row: the full flows row plus its tags, so a restore
// rebuilds the capture exactly rather than a lossy outline.
type pruneRow struct {
	Flow map[string]any   `json:"flow"`
	Tags []string         `json:"tags,omitempty"`
}

// sqlRow is a positional query result with typed accessors.
type sqlRow []any

func (r sqlRow) str(i int) string {
	switch v := r[i].(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func (r sqlRow) int(i int) int64 {
	switch v := r[i].(type) {
	case nil:
		return 0
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case []byte:
		var n int64
		fmt.Sscan(string(v), &n)
		return n
	case string:
		var n int64
		fmt.Sscan(v, &n)
		return n
	default:
		return 0
	}
}

// queryRowsByIDs runs a query with a %s id-list placeholder, chunked so a large
// prune never exceeds SQLite's parameter limit.
func (s *Store) queryRowsByIDs(ids []int64, format string) ([]sqlRow, error) {
	const idChunk = 400
	var out []sqlRow
	for start := 0; start < len(ids); start += idChunk {
		end := min(start+idChunk, len(ids))
		args := make([]any, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		ph := strings.TrimRight(strings.Repeat("?,", end-start), ",")
		rows, err := s.db.Query(fmt.Sprintf(format, ph), args...)
		if err != nil {
			return nil, fmt.Errorf("store: prune query: %w", err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, vals)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// snapshotPrune writes the rows, tags and body hashes of a batch to the snapshot
// tables so RestorePrune can put them back inside the undo window.
func (s *Store) snapshotPrune(ids []int64, mode string, expiresMS int64) (int64, error) {
	if _, err := s.db.Exec(pruneSnapshotSchema); err != nil {
		return 0, fmt.Errorf("store: prune snapshot schema: %w", err)
	}
	rows, err := s.queryRowsByIDs(ids, `SELECT * FROM flows WHERE id IN (%s)`)
	if err != nil {
		return 0, err
	}
	cols, err := s.flowColumns()
	if err != nil {
		return 0, err
	}
	tags, err := s.tagsByFlowIDs(ids)
	if err != nil {
		return 0, err
	}
	var snap []pruneRow
	hashes := map[string]struct{}{}
	for _, row := range rows {
		flow := make(map[string]any, len(cols))
		for i, name := range cols {
			flow[name] = row[i]
		}
		id := row.int(0) // id is the first column of flows
		snap = append(snap, pruneRow{Flow: flow, Tags: tags[id]})
		for _, name := range []string{"req_body_hash", "res_body_hash", "original_req_body_hash", "original_res_body_hash"} {
			if h, ok := flow[name].(string); ok && h != "" {
				hashes[h] = struct{}{}
			}
		}
	}
	blob, err := json.Marshal(snap)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Expired snapshots are dropped here, so the window is enforced even when
	// nothing calls a maintenance path.
	if _, err := tx.Exec(`DELETE FROM prune_snapshots WHERE expires_ts <= ?`, time.Now().UnixMilli()); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM prune_body_hashes WHERE snapshot_id NOT IN (SELECT id FROM prune_snapshots)`); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO prune_snapshots(ts,expires_ts,mode,flows) VALUES(?,?,?,?)`,
		time.Now().UnixMilli(), expiresMS, mode, string(blob))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for h := range hashes {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO prune_body_hashes(snapshot_id,hash) VALUES(?,?)`, id, h); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// flowColumns lists the flows table columns in declaration order.
func (s *Store) flowColumns() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('flows') ORDER BY cid`)
	if err != nil {
		return nil, fmt.Errorf("store: flow columns: %w", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// tagsByFlowIDs returns each flow's tags.
func (s *Store) tagsByFlowIDs(ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	rows, err := s.queryRowsByIDs(ids, `SELECT flow_id, tag FROM flow_tags WHERE flow_id IN (%s)`)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id := row.int(0)
		out[id] = append(out[id], row.str(1))
	}
	return out, nil
}

// deleteFlowsByIDs removes the flows, their index rows, tags and frames.
func (s *Store) deleteFlowsByIDs(ids []int64) (int64, error) {
	_ = s.FlushWSFrames()
	const chunk = 400
	var deleted int64
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: prune begin: %w", err)
	}
	defer tx.Rollback()
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		args := make([]any, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		ph := strings.TrimRight(strings.Repeat("?,", end-start), ",")
		if _, err := tx.Exec(`DELETE FROM flows_fts WHERE rowid IN (`+ph+`)`, args...); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id IN (`+ph+`)`, args...); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM ws_frames WHERE flow_id IN (`+ph+`)`, args...); err != nil {
			return 0, err
		}
		res, err := tx.Exec(`DELETE FROM flows WHERE id IN (`+ph+`)`, args...)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		deleted += n
	}
	return deleted, tx.Commit()
}

// PendingPrune is one restorable prune batch.
type PendingPrune struct {
	ID          int64  `json:"id"`
	TS          int64  `json:"ts"`
	ExpiresTS   int64  `json:"expiresTs"`
	Mode        string `json:"mode"`
	Flows       int    `json:"flows"`
	RestoresIn  int64  `json:"restoresInMs"`
}

// PendingPrunes lists restore-able batches, newest first.
func (s *Store) PendingPrunes() ([]PendingPrune, error) {
	rows, err := s.db.Query(`SELECT id, ts, expires_ts, mode, flows FROM prune_snapshots WHERE expires_ts > ? ORDER BY id DESC`, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingPrune
	for rows.Next() {
		var (
			p    PendingPrune
			blob string
		)
		if err := rows.Scan(&p.ID, &p.TS, &p.ExpiresTS, &p.Mode, &blob); err != nil {
			return nil, err
		}
		var snap []pruneRow
		if err := json.Unmarshal([]byte(blob), &snap); err == nil {
			p.Flows = len(snap)
		}
		p.RestoresIn = p.ExpiresTS - time.Now().UnixMilli()
		out = append(out, p)
	}
	return out, rows.Err()
}

// RestorePrune puts a pruned batch back and consumes the snapshot. It fails when
// the window has passed, so an expired snapshot cannot silently restore stale
// flows over newer traffic.
func (s *Store) RestorePrune(id int64) (int64, error) {
	var (
		blob    string
		expires int64
	)
	err := s.db.QueryRow(`SELECT flows, expires_ts FROM prune_snapshots WHERE id = ?`, id).Scan(&blob, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("prune %d not found (already restored or expired)", id)
	}
	if err != nil {
		return 0, err
	}
	if expires <= time.Now().UnixMilli() {
		return 0, fmt.Errorf("prune %d expired; restore is no longer possible", id)
	}
	var snap []pruneRow
	if err := json.Unmarshal([]byte(blob), &snap); err != nil {
		return 0, fmt.Errorf("prune %d snapshot is corrupt: %w", id, err)
	}
	cols, err := s.flowColumns()
	if err != nil {
		return 0, err
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(cols)), ",")
	insert := `INSERT OR REPLACE INTO flows (` + strings.Join(cols, ",") + `) VALUES (` + ph + `)`
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var restored int64
	for _, row := range snap {
		args := make([]any, len(cols))
		for i, name := range cols {
			args[i] = normalizeRestoreValue(row.Flow[name])
		}
		if _, err := tx.Exec(insert, args...); err != nil {
			return 0, fmt.Errorf("restore flow: %w", err)
		}
		id := int64(0)
		if v, ok := row.Flow["id"].(float64); ok {
			id = int64(v)
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO flows_fts(rowid, host, path, method, note) VALUES (?,?,?,?,?)`,
			id, row.Flow["host"], row.Flow["path"], row.Flow["method"], row.Flow["note"]); err != nil {
			return 0, err
		}
		for _, tag := range row.Tags {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO flow_tags(flow_id, tag) VALUES(?,?)`, id, tag); err != nil {
				return 0, err
			}
		}
		restored++
	}
	if _, err := tx.Exec(`DELETE FROM prune_snapshots WHERE id = ?`, id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM prune_body_hashes WHERE snapshot_id = ?`, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return restored, nil
}

// normalizeRestoreValue turns a JSON-decoded value back into a driver argument.
func normalizeRestoreValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case float64:
		return int64(x)
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	default:
		return x
	}
}

// protectedPruneHashes returns body hashes a live snapshot needs, so a body GC
// cannot delete the bodies an undo would bring back.
func (s *Store) protectedPruneHashes() (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT DISTINCT h.hash FROM prune_body_hashes h
		JOIN prune_snapshots p ON p.id = h.snapshot_id WHERE p.expires_ts > ?`, time.Now().UnixMilli())
	if err != nil {
		// No snapshot table yet: nothing is protected.
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out[h] = struct{}{}
	}
	return out, rows.Err()
}
