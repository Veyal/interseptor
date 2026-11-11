package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// r3Dump returns every row of a query as one string per row, so two states of
// a table can be compared exactly (NULL, integer and text stay distinct).
func r3Dump(t *testing.T, s *Store, q string, args ...any) []string {
	t.Helper()
	rows, err := s.db.Query(q, args...)
	if err != nil {
		t.Fatalf("dump %s: %v", q, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for i, v := range vals {
			fmt.Fprintf(&b, "%s=%T:%v|", cols[i], v, v)
		}
		out = append(out, b.String())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func r3Equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, want %d", what, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s row %d differs:\n got %s\nwant %s", what, i, got[i], want[i])
		}
	}
}

// r3SeedPruneProject seeds n flows over three hosts with notes, flags, tags,
// body hashes, a NULL column and an integer too large for a float64.
func r3SeedPruneProject(t *testing.T, n int) *Store {
	t.Helper()
	s := seedReviewDB(t, n)
	if _, err := s.db.Exec(`UPDATE flows SET host = 'prune' || (id % 3) || '.example.com'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE flows SET mime = NULL WHERE id % 7 = 0`); err != nil {
		t.Fatal(err)
	}
	// 2^53+1 does not survive a float64, the type a plain JSON decode gives.
	if _, err := s.db.Exec(`UPDATE flows SET res_len = 9007199254740993, flags = flags | ? WHERE id % 13 = 0`, FlagAI); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE flows SET note = 'quote " backslash \ <tag> ' || char(10) || 'unicode é' WHERE id % 17 = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM flows_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO flows_fts(rowid, host, path, method, note) SELECT id, host, path, method, note FROM flows`); err != nil {
		t.Fatal(err)
	}
	return s
}

const (
	r3FlowsDump = `SELECT * FROM flows ORDER BY id`
	r3TagsDump  = `SELECT flow_id, tag FROM flow_tags ORDER BY flow_id, tag`
	r3FTSDump   = `SELECT rowid, host, path, method, note FROM flows_fts ORDER BY rowid`
)

// A prune that spans several snapshot chunks restores exactly the rows it
// removed: every column (ids, flags, notes, body hashes), the tags and the
// full-text entries.
func TestR3PruneUndoAcrossChunksRestoresExactRows(t *testing.T) {
	const n = 3*pruneChunkFlows + 137
	s := r3SeedPruneProject(t, n)
	flows, tags, fts := r3Dump(t, s, r3FlowsDump), r3Dump(t, s, r3TagsDump), r3Dump(t, s, r3FTSDump)
	if len(tags) == 0 {
		t.Fatal("seed has no tags")
	}

	// Two of the three hosts: more than two chunks, and rows that must stay.
	res, err := s.PruneFlows(PruneOptions{Hosts: []string{"prune0.example.com", "prune1.example.com"}, UndoWindow: time.Hour})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	wantDeleted := int64(0)
	for i := 1; i <= n; i++ {
		if i%3 != 2 {
			wantDeleted++
		}
	}
	if res.Deleted != wantDeleted {
		t.Fatalf("deleted %d, want %d", res.Deleted, wantDeleted)
	}
	var chunks, inChunks, biggest, parentCell int64
	if err := s.db.QueryRow(`SELECT COUNT(*), SUM(n), MAX(n) FROM prune_snapshot_chunks WHERE snapshot_id = ?`, res.PruneID).Scan(&chunks, &inChunks, &biggest); err != nil {
		t.Fatal(err)
	}
	if chunks < 3 || inChunks != wantDeleted || biggest > pruneChunkFlows {
		t.Fatalf("snapshot has %d chunks holding %d flows (largest %d), want >=3 chunks, %d flows, at most %d each",
			chunks, inChunks, biggest, wantDeleted, pruneChunkFlows)
	}
	if err := s.db.QueryRow(`SELECT length(flows) FROM prune_snapshots WHERE id = ?`, res.PruneID).Scan(&parentCell); err != nil || parentCell != 0 {
		t.Fatalf("snapshot parent row holds %d bytes of flows (err %v), want none", parentCell, err)
	}
	if got := r3Dump(t, s, `SELECT id FROM flows WHERE host != 'prune2.example.com'`); len(got) != 0 {
		t.Fatalf("%d pruned flows are still present", len(got))
	}
	pending, err := s.PendingPrunes()
	if err != nil || len(pending) != 1 || int64(pending[0].Flows) != wantDeleted {
		t.Fatalf("pending = %+v, err %v; want one batch of %d flows", pending, err, wantDeleted)
	}

	restored, err := s.RestorePrune(res.PruneID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored != wantDeleted {
		t.Fatalf("restored %d, want %d", restored, wantDeleted)
	}
	r3Equal(t, "flows", r3Dump(t, s, r3FlowsDump), flows)
	r3Equal(t, "flow_tags", r3Dump(t, s, r3TagsDump), tags)
	r3Equal(t, "flows_fts", r3Dump(t, s, r3FTSDump), fts)
	for _, table := range []string{"prune_snapshots", "prune_snapshot_chunks", "prune_body_hashes"} {
		var left int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&left); err != nil || left != 0 {
			t.Fatalf("%s holds %d rows after the undo (err %v), want 0", table, left, err)
		}
	}
	if _, err := s.RestorePrune(res.PruneID); err == nil {
		t.Fatal("a consumed snapshot restored twice")
	}
}

// r3LegacySnapshot removes the given flows the way the released build did: the
// whole batch as one JSON array in prune_snapshots.flows.
func r3LegacySnapshot(t *testing.T, s *Store, where string, expires time.Time) int64 {
	t.Helper()
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS prune_snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, expires_ts INTEGER NOT NULL,
		mode TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '', flows TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS prune_body_hashes (snapshot_id INTEGER NOT NULL, hash TEXT NOT NULL, PRIMARY KEY (snapshot_id, hash))`); err != nil {
		t.Fatal(err)
	}
	cols, err := s.flowColumns()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT * FROM flows WHERE ` + where)
	if err != nil {
		t.Fatal(err)
	}
	var snap []pruneRow
	hashes := map[string]struct{}{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		flow := map[string]any{}
		for i, name := range cols {
			flow[name] = vals[i]
		}
		for _, name := range pruneBodyHashColumns {
			if h, _ := flow[name].(string); h != "" {
				hashes[h] = struct{}{}
			}
		}
		snap = append(snap, pruneRow{Flow: flow})
	}
	rows.Close()
	for i := range snap {
		id := snap[i].Flow["id"].(int64)
		tagRows, err := s.db.Query(`SELECT tag FROM flow_tags WHERE flow_id = ?`, id)
		if err != nil {
			t.Fatal(err)
		}
		for tagRows.Next() {
			var tag string
			_ = tagRows.Scan(&tag)
			snap[i].Tags = append(snap[i].Tags, tag)
		}
		tagRows.Close()
	}
	blob, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO prune_snapshots(ts,expires_ts,mode,flows) VALUES(?,?,?,?)`,
		time.Now().UnixMilli(), expires.UnixMilli(), "delete", string(blob))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	for h := range hashes {
		if _, err := s.db.Exec(`INSERT INTO prune_body_hashes(snapshot_id,hash) VALUES(?,?)`, id, h); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`DELETE FROM flows_fts WHERE rowid IN (SELECT id FROM flows WHERE ` + where + `)`,
		`DELETE FROM flow_tags WHERE flow_id IN (SELECT id FROM flows WHERE ` + where + `)`,
		`DELETE FROM flows WHERE ` + where,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// A database left by the released build, with a pending prune in the
// single-cell format and no chunk table, still lists and undoes that prune
// after it is opened by this build, and its bodies stay protected from GC.
func TestR3LegacySingleCellSnapshotStillRestores(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A body on disk that only the pruned flows reference.
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("only the pruned flows reference this body")); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		f := &Flow{TS: time.UnixMilli(int64(1_700_000_000_000 + i)), Method: "GET", Scheme: "https",
			Host: fmt.Sprintf("legacy%d.example.com", i%2), Port: 443, Path: fmt.Sprintf("/p/%d", i), Status: 200,
			ResLen: 9007199254740993, Note: "note " + fmt.Sprint(i), Flags: FlagRepeater}
		if i%2 == 0 {
			f.ResBodyHash = hash
		}
		if _, err := s.InsertFlow(f); err != nil {
			t.Fatal(err)
		}
		if i%4 == 0 {
			if _, err := s.AddFlowTags(f.ID, []string{"keep-me", "auth"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	flows, tags, fts := r3Dump(t, s, r3FlowsDump), r3Dump(t, s, r3TagsDump), r3Dump(t, s, r3FTSDump)
	pruneID := r3LegacySnapshot(t, s, `host = 'legacy0.example.com'`, time.Now().Add(time.Hour))
	// The released build has no chunk table.
	if _, err := s.db.Exec(`DROP TABLE prune_snapshot_chunks`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	var hasChunks int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='prune_snapshot_chunks'`).Scan(&hasChunks); err != nil || hasChunks != 1 {
		t.Fatalf("Open did not add prune_snapshot_chunks to an existing database (count %d, err %v)", hasChunks, err)
	}
	pending, err := s.PendingPrunes()
	if err != nil || len(pending) != 1 || pending[0].ID != pruneID || pending[0].Flows != 20 {
		t.Fatalf("pending = %+v, err %v; want prune %d with 20 flows", pending, err, pruneID)
	}
	if _, _, err := s.GCBodies(); err != nil {
		t.Fatalf("GCBodies: %v", err)
	}
	if !s.BodyExists(hash) {
		t.Fatal("GC removed a body that a pending single-cell prune snapshot still needs")
	}
	restored, err := s.RestorePrune(pruneID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored != 20 {
		t.Fatalf("restored %d, want 20", restored)
	}
	r3Equal(t, "flows", r3Dump(t, s, r3FlowsDump), flows)
	r3Equal(t, "flow_tags", r3Dump(t, s, r3TagsDump), tags)
	r3Equal(t, "flows_fts", r3Dump(t, s, r3FTSDump), fts)
	if pending, _ := s.PendingPrunes(); len(pending) != 0 {
		t.Fatalf("snapshot still pending after the undo: %+v", pending)
	}
}

// The snapshot and the delete are one transaction: a prune that fails part-way
// removes nothing and leaves no snapshot behind.
func TestR3FailedPruneLeavesNoSnapshotAndNoDeletion(t *testing.T) {
	const n = 2*pruneChunkFlows + 50
	s := r3SeedPruneProject(t, n)
	flows := r3Dump(t, s, r3FlowsDump)
	// Fails in the second chunk, after the first chunk was snapshotted and deleted.
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE TRIGGER r3_block BEFORE DELETE ON flows WHEN old.id = %d
		BEGIN SELECT RAISE(ABORT, 'r3 blocked'); END`, pruneChunkFlows+10)); err != nil {
		t.Fatal(err)
	}
	res, err := s.PruneFlows(PruneOptions{UndoWindow: time.Hour})
	if err == nil {
		t.Fatalf("prune succeeded (%+v) although a delete was rejected", res)
	}
	r3Equal(t, "flows after a failed prune", r3Dump(t, s, r3FlowsDump), flows)
	for _, table := range []string{"prune_snapshots", "prune_snapshot_chunks", "prune_body_hashes"} {
		var left int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&left); err != nil || left != 0 {
			t.Fatalf("%s holds %d rows after a failed prune (err %v), want 0", table, left, err)
		}
	}
}

// An expired snapshot's chunks are dropped by the next prune, and a body-GC
// keeps the bodies of a chunked snapshot that is still pending.
func TestR3ChunkedSnapshotExpiryAndBodyProtection(t *testing.T) {
	s := r3SeedPruneProject(t, pruneChunkFlows+20)
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("body of a pruned flow")); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE flows SET res_body_hash = ? WHERE id = 3`, hash); err != nil {
		t.Fatal(err)
	}
	first, err := s.PruneFlows(PruneOptions{Hosts: []string{"prune0.example.com"}, UndoWindow: time.Hour})
	if err != nil || first.Deleted == 0 {
		t.Fatalf("prune: %+v, %v", first, err)
	}
	if _, _, err := s.GCBodies(); err != nil {
		t.Fatal(err)
	}
	if !s.BodyExists(hash) {
		t.Fatal("GC removed a body a pending chunked snapshot still needs")
	}
	// Protection that cannot be read must stop the sweep, not empty it.
	if _, err := s.db.Exec(`ALTER TABLE prune_body_hashes RENAME COLUMN hash TO h`); err != nil {
		t.Fatal(err)
	}
	if removed, _, err := s.GCBodies(); err == nil {
		t.Fatalf("GCBodies succeeded (removed %d) although prune protection could not be read", removed)
	}
	if !s.BodyExists(hash) {
		t.Fatal("GC removed a protected body while protection was unreadable")
	}
	if _, err := s.db.Exec(`ALTER TABLE prune_body_hashes RENAME COLUMN h TO hash`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`UPDATE prune_snapshots SET expires_ts = 1 WHERE id = ?`, first.PruneID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestorePrune(first.PruneID); err == nil {
		t.Fatal("an expired snapshot restored")
	}
	second, err := s.PruneFlows(PruneOptions{Hosts: []string{"prune1.example.com"}, UndoWindow: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM prune_snapshot_chunks WHERE snapshot_id != ?`, second.PruneID).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("%d chunk rows of the expired snapshot survived the next prune (err %v)", stale, err)
	}
}
