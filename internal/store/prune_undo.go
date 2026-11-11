package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// pruneRow is one snapshot row: the full flows row plus its tags, so a restore
// rebuilds the capture exactly rather than a lossy outline.
type pruneRow struct {
	Flow map[string]any `json:"flow"`
	Tags []string       `json:"tags,omitempty"`
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
		// rows.Close does not report an error that ended the iteration.
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: prune query: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// pruneChunkFlows is how many flows one snapshot chunk holds. A chunk is one
// JSON array in one prune_snapshot_chunks row, so neither the encoder nor a
// single cell grows with the size of the prune; it is also the IN-list size,
// well under SQLite's bound-parameter limit.
const pruneChunkFlows = 400

// pruneBodyHashColumns are the flows columns that name a stored body.
var pruneBodyHashColumns = []string{"req_body_hash", "res_body_hash", "original_req_body_hash", "original_res_body_hash"}

// purgeExpiredPruneSnapshots drops snapshots whose undo window has passed,
// with their chunks and body-hash protection. It runs at the start of every
// prune, so the window is enforced even when nothing calls a maintenance path.
func purgeExpiredPruneSnapshots(tx *sql.Tx, nowMS int64) error {
	if _, err := tx.Exec(`DELETE FROM prune_snapshots WHERE expires_ts <= ?`, nowMS); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM prune_snapshot_chunks WHERE snapshot_id NOT IN (SELECT id FROM prune_snapshots)`); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM prune_body_hashes WHERE snapshot_id NOT IN (SELECT id FROM prune_snapshots)`)
	return err
}

// pruneWithSnapshot removes the flows and, in the same transaction, writes the
// snapshot RestorePrune needs to put them back: either both happen or neither
// does, and the snapshot holds the rows exactly as they were deleted. The
// snapshot is written one chunk of pruneChunkFlows flows at a time, each chunk
// read, encoded, stored and deleted before the next is touched, so memory and
// cell size stay flat however large the prune is.
func (s *Store) pruneWithSnapshot(ids []int64, mode string, expiresMS int64) (pruneID, deleted int64, err error) {
	if _, err := s.db.Exec(pruneSnapshotSchema); err != nil {
		return 0, 0, fmt.Errorf("store: prune snapshot schema: %w", err)
	}
	cols, err := s.flowColumns()
	if err != nil {
		return 0, 0, err
	}
	_ = s.FlushWSFrames()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("store: prune begin: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if err := purgeExpiredPruneSnapshots(tx, now); err != nil {
		return 0, 0, err
	}
	// flows stays empty: the rows live in prune_snapshot_chunks. A non-empty
	// value is a snapshot written by a build that stored one JSON cell.
	res, err := tx.Exec(`INSERT INTO prune_snapshots(ts,expires_ts,mode,flows) VALUES(?,?,?,'')`, now, expiresMS, mode)
	if err != nil {
		return 0, 0, err
	}
	if pruneID, err = res.LastInsertId(); err != nil {
		return 0, 0, err
	}
	w := newPruneChunkWriter(tx, pruneID, cols)
	for start := 0; start < len(ids); start += pruneChunkFlows {
		n, err := w.moveChunk(ids[start:min(start+pruneChunkFlows, len(ids))])
		if err != nil {
			return 0, 0, err
		}
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return pruneID, deleted, nil
}

// pruneChunkWriter moves flows into a snapshot one chunk at a time.
type pruneChunkWriter struct {
	st       *txStmts
	pruneID  int64
	seq      int64
	keys     [][]byte // `"<column>":` per flows column, in column order
	hashCols []int    // positions of pruneBodyHashColumns
	vals     []any
	ptrs     []any
	buf      bytes.Buffer
}

func newPruneChunkWriter(tx *sql.Tx, pruneID int64, cols []string) *pruneChunkWriter {
	w := &pruneChunkWriter{st: &txStmts{tx: tx}, pruneID: pruneID, vals: make([]any, len(cols)), ptrs: make([]any, len(cols))}
	for i, name := range cols {
		key, _ := json.Marshal(name)
		w.keys = append(w.keys, append(key, ':'))
		w.ptrs[i] = &w.vals[i]
		for _, h := range pruneBodyHashColumns {
			if name == h {
				w.hashCols = append(w.hashCols, i)
			}
		}
	}
	return w
}

// moveChunk snapshots the flows with these ids and deletes them, with their
// index rows, tags and frames. It returns how many flows were deleted.
func (w *pruneChunkWriter) moveChunk(ids []int64) (int64, error) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	tags, err := w.chunkTags(ph, args)
	if err != nil {
		return 0, err
	}
	rows, hashes, err := w.encodeChunk(ph, args, tags)
	if err != nil {
		return 0, err
	}
	if rows > 0 {
		if _, err := w.st.Exec(`INSERT INTO prune_snapshot_chunks(snapshot_id,seq,n,flows) VALUES(?,?,?,?)`,
			w.pruneID, w.seq, rows, w.buf.String()); err != nil {
			return 0, err
		}
		w.seq++
	}
	for h := range hashes {
		if _, err := w.st.Exec(`INSERT OR IGNORE INTO prune_body_hashes(snapshot_id,hash) VALUES(?,?)`, w.pruneID, h); err != nil {
			return 0, err
		}
	}
	for _, q := range []string{
		`DELETE FROM flows_fts WHERE rowid IN (` + ph + `)`,
		`DELETE FROM flow_tags WHERE flow_id IN (` + ph + `)`,
		`DELETE FROM ws_frames WHERE flow_id IN (` + ph + `)`,
	} {
		if _, err := w.st.Exec(q, args...); err != nil {
			return 0, err
		}
	}
	res, err := w.st.Exec(`DELETE FROM flows WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (w *pruneChunkWriter) chunkTags(ph string, args []any) (map[int64][]string, error) {
	rows, err := w.st.Query(`SELECT flow_id, tag FROM flow_tags WHERE flow_id IN (`+ph+`) ORDER BY flow_id, tag`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: prune query: %w", err)
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var (
			id  int64
			tag string
		)
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		out[id] = append(out[id], tag)
	}
	return out, rows.Err()
}

// encodeChunk writes the chunk's flows into w.buf as a JSON array of pruneRow
// (the layout of the former single-cell snapshot, so one decoder reads both)
// and returns the row count and the body hashes those rows reference.
func (w *pruneChunkWriter) encodeChunk(ph string, args []any, tags map[int64][]string) (int64, map[string]struct{}, error) {
	rows, err := w.st.Query(`SELECT * FROM flows WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("store: prune query: %w", err)
	}
	defer rows.Close()
	hashes := map[string]struct{}{}
	w.buf.Reset()
	w.buf.WriteByte('[')
	var n int64
	for rows.Next() {
		if err := rows.Scan(w.ptrs...); err != nil {
			return 0, nil, err
		}
		if n > 0 {
			w.buf.WriteByte(',')
		}
		n++
		w.buf.WriteString(`{"flow":{`)
		for i, v := range w.vals {
			if i > 0 {
				w.buf.WriteByte(',')
			}
			w.buf.Write(w.keys[i])
			if err := appendPruneJSON(&w.buf, v); err != nil {
				return 0, nil, err
			}
		}
		w.buf.WriteByte('}')
		if t := tags[sqlRow(w.vals).int(0)]; len(t) > 0 { // id is the first column of flows
			w.buf.WriteString(`,"tags":`)
			if err := appendPruneJSON(&w.buf, t); err != nil {
				return 0, nil, err
			}
		}
		w.buf.WriteByte('}')
		for _, i := range w.hashCols {
			if h, ok := w.vals[i].(string); ok && h != "" {
				hashes[h] = struct{}{}
			}
		}
	}
	// rows.Close does not report an error that ended the iteration; a chunk
	// cut short here would delete flows the snapshot does not hold.
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("store: prune query: %w", err)
	}
	w.buf.WriteByte(']')
	return n, hashes, nil
}

// appendPruneJSON writes one value as JSON. Integers are written directly so
// the common columns cost no allocation.
func appendPruneJSON(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case int64:
		buf.Write(strconv.AppendInt(buf.AvailableBuffer(), x, 10))
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
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

func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// PendingPrune is one restorable prune batch.
type PendingPrune struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	ExpiresTS  int64  `json:"expiresTs"`
	Mode       string `json:"mode"`
	Flows      int    `json:"flows"`
	RestoresIn int64  `json:"restoresInMs"`
}

// pendingPruneFlowsSQL counts a snapshot's flows without loading them: the sum
// of its chunk sizes, or for a snapshot in the former single-cell format the
// length of that cell's JSON array.
const pendingPruneFlowsSQL = `CASE
	WHEN p.flows = '' THEN COALESCE((SELECT SUM(c.n) FROM prune_snapshot_chunks c WHERE c.snapshot_id = p.id), 0)
	WHEN json_valid(p.flows) THEN COALESCE(json_array_length(p.flows), 0)
	ELSE 0 END`

// PendingPrunes lists restore-able batches, newest first.
func (s *Store) PendingPrunes() ([]PendingPrune, error) {
	rows, err := s.db.Query(`SELECT p.id, p.ts, p.expires_ts, p.mode, `+pendingPruneFlowsSQL+`
		FROM prune_snapshots p WHERE p.expires_ts > ? ORDER BY p.id DESC`, time.Now().UnixMilli())
	if err != nil {
		// The snapshot tables are created by the first prune; before that
		// nothing is pending.
		if isNoSuchTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []PendingPrune
	for rows.Next() {
		var p PendingPrune
		if err := rows.Scan(&p.ID, &p.TS, &p.ExpiresTS, &p.Mode, &p.Flows); err != nil {
			return nil, err
		}
		p.RestoresIn = p.ExpiresTS - time.Now().UnixMilli()
		out = append(out, p)
	}
	return out, rows.Err()
}

// RestorePrune puts a pruned batch back and consumes the snapshot. It fails when
// the window has passed, so an expired snapshot cannot silently restore stale
// flows over newer traffic. The snapshot is read inside the restoring
// transaction, one chunk at a time, so two restores of one prune cannot both
// run and memory does not grow with the size of the batch.
func (s *Store) RestorePrune(id int64) (int64, error) {
	cols, err := s.flowColumns()
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var (
		legacy  string // the whole batch, in a snapshot from the single-cell format
		expires int64
	)
	err = tx.QueryRow(`SELECT flows, expires_ts FROM prune_snapshots WHERE id = ?`, id).Scan(&legacy, &expires)
	if errors.Is(err, sql.ErrNoRows) || isNoSuchTable(err) {
		return 0, fmt.Errorf("prune %d not found (already restored or expired)", id)
	}
	if err != nil {
		return 0, err
	}
	if expires <= time.Now().UnixMilli() {
		return 0, fmt.Errorf("prune %d expired; restore is no longer possible", id)
	}
	r := newPruneRestorer(tx, cols)
	if legacy != "" {
		if err := r.restoreChunk(legacy); err != nil {
			return 0, fmt.Errorf("prune %d snapshot is corrupt: %w", id, err)
		}
	} else if err := r.restoreChunks(id); err != nil {
		return 0, err
	}
	for _, q := range []string{
		`DELETE FROM prune_snapshots WHERE id = ?`,
		`DELETE FROM prune_snapshot_chunks WHERE snapshot_id = ?`,
		`DELETE FROM prune_body_hashes WHERE snapshot_id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return r.restored, nil
}

// pruneRestorer re-inserts snapshot rows through statements prepared once per
// restore.
type pruneRestorer struct {
	st       *txStmts
	cols     []string
	insert   string
	args     []any
	restored int64
}

func newPruneRestorer(tx *sql.Tx, cols []string) *pruneRestorer {
	ph := strings.TrimRight(strings.Repeat("?,", len(cols)), ",")
	return &pruneRestorer{
		st:     &txStmts{tx: tx},
		cols:   cols,
		insert: `INSERT OR REPLACE INTO flows (` + strings.Join(cols, ",") + `) VALUES (` + ph + `)`,
		args:   make([]any, len(cols)),
	}
}

// restoreChunks restores every chunk of a snapshot in the order it was
// written. The chunk numbers are listed first so no cursor is open on the
// connection while rows are inserted.
func (r *pruneRestorer) restoreChunks(id int64) error {
	rows, err := r.st.Query(`SELECT seq FROM prune_snapshot_chunks WHERE snapshot_id = ? ORDER BY seq`, id)
	if err != nil {
		return err
	}
	var seqs []int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			rows.Close()
			return err
		}
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, seq := range seqs {
		var blob string
		if err := r.st.QueryRow(`SELECT flows FROM prune_snapshot_chunks WHERE snapshot_id = ? AND seq = ?`, id, seq).Scan(&blob); err != nil {
			return err
		}
		if err := r.restoreChunk(blob); err != nil {
			return fmt.Errorf("prune %d snapshot chunk %d is corrupt: %w", id, seq, err)
		}
	}
	return nil
}

// restoreChunk restores one JSON array of pruneRow, decoding and inserting a
// row at a time. Numbers are kept as written (json.Number) so an integer
// column comes back exactly, not through a float64.
func (r *pruneRestorer) restoreChunk(blob string) error {
	dec := json.NewDecoder(strings.NewReader(blob))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil { // "null": a snapshot of no rows
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return errors.New("snapshot is not a list of flows")
	}
	for dec.More() {
		var row pruneRow
		if err := dec.Decode(&row); err != nil {
			return err
		}
		if err := r.restoreRow(row); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func (r *pruneRestorer) restoreRow(row pruneRow) error {
	for i, name := range r.cols {
		r.args[i] = normalizeRestoreValue(row.Flow[name])
	}
	if _, err := r.st.Exec(r.insert, r.args...); err != nil {
		return fmt.Errorf("restore flow: %w", err)
	}
	id, _ := normalizeRestoreValue(row.Flow["id"]).(int64)
	if _, err := r.st.Exec(`INSERT OR REPLACE INTO flows_fts(rowid, host, path, method, note) VALUES (?,?,?,?,?)`,
		id, row.Flow["host"], row.Flow["path"], row.Flow["method"], row.Flow["note"]); err != nil {
		return err
	}
	for _, tag := range row.Tags {
		if _, err := r.st.Exec(`INSERT OR IGNORE INTO flow_tags(flow_id, tag) VALUES(?,?)`, id, tag); err != nil {
			return err
		}
	}
	r.restored++
	return nil
}

// normalizeRestoreValue turns a JSON-decoded value back into a driver argument.
func normalizeRestoreValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return int64(f)
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
		if isNoSuchTable(err) {
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
