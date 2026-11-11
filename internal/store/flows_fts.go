package store

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// ensureFlowsFTS creates the FTS5 index, backfills it on first run and repairs
// it when it has drifted from the flows table.
func (s *Store) ensureFlowsFTS() error {
	if _, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS flows_fts USING fts5(
		host, path, method, note,
		tokenize='unicode61'
	)`); err != nil {
		return err
	}
	drifted, err := s.flowsFTSDrifted()
	if err != nil || !drifted {
		return err
	}
	return s.repairFlowsFTS()
}

// flowsFTSDrifted reports whether the full-text index and the flows table hold
// different sets of row ids, judged by their row count and their lowest and
// highest id. Every flow write keeps the two in step inside one transaction,
// so they only part when something outside that path interferes: a backfill
// cut short by a crash, rows written by a build without FTS, an entry left
// behind by a row removed some other way. Each of those changes the count or
// an end of the id range. The index side is read from flows_fts_docsize, the
// shadow table holding one row per indexed document, so all six values come
// from b-tree counts and end-point seeks instead of a walk of the index.
func (s *Store) flowsFTSDrifted() (bool, error) {
	// Scalar subqueries, not one aggregate row per table: MIN and MAX are only
	// answered by an end-point seek when each is its query's sole aggregate.
	// One statement, so both tables are read from the same snapshot even when
	// another process is writing flows to this project.
	const stats = `(SELECT COUNT(*) FROM %[2]s),
		COALESCE((SELECT MIN(%[1]s) FROM %[2]s), 0), COALESCE((SELECT MAX(%[1]s) FROM %[2]s), 0)`
	flowStats := fmt.Sprintf(stats, "id", "flows")
	var flows, fts [3]int64
	dest := []any{&flows[0], &flows[1], &flows[2], &fts[0], &fts[1], &fts[2]}
	err := s.db.QueryRow(`SELECT ` + flowStats + `, ` + fmt.Sprintf(stats, "id", "flows_fts_docsize")).Scan(dest...)
	if err != nil {
		// No readable shadow table: ask the index itself.
		err = s.db.QueryRow(`SELECT ` + flowStats + `, ` + fmt.Sprintf(stats, "rowid", "flows_fts")).Scan(dest...)
	}
	if err != nil {
		return false, err
	}
	return flows != fts, nil
}

// repairFlowsFTS brings the index back in step with flows: entries whose flow
// is gone are removed and flows without an entry are indexed, in one
// transaction. The ids are collected first so no statement reads the index it
// is writing.
func (s *Store) repairFlowsFTS() error {
	var indexed int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM flows_fts`).Scan(&indexed); err != nil {
		return err
	}
	if indexed == 0 { // first run, or an index that was never filled
		_, err := s.db.Exec(`INSERT INTO flows_fts(rowid, host, path, method, note)
			SELECT id, host, path, method, note FROM flows`)
		return err
	}
	orphans, err := s.queryIDs(`SELECT rowid FROM flows_fts WHERE rowid NOT IN (SELECT id FROM flows)`)
	if err != nil {
		return err
	}
	missing, err := s.queryIDs(`SELECT id FROM flows WHERE id NOT IN (SELECT rowid FROM flows_fts)`)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := execIDChunks(tx, orphans, `DELETE FROM flows_fts WHERE rowid IN (%s)`); err != nil {
		return err
	}
	if err := execIDChunks(tx, missing, `INSERT INTO flows_fts(rowid, host, path, method, note)
		SELECT id, host, path, method, note FROM flows WHERE id IN (%s)`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(missing) == 0 && len(orphans) == 0 {
		return nil
	}
	log.Printf("store: full-text index was out of step with History: indexed %d missing flow(s), removed %d stale entr(ies)", len(missing), len(orphans))
	return nil
}

// queryIDs runs a query returning one integer column.
func (s *Store) queryIDs(q string) ([]int64, error) {
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// execIDChunks runs format (with one %s for the placeholder list) once per
// chunk of ids, keeping each statement under SQLite's bound-parameter limit.
func execIDChunks(tx *sql.Tx, ids []int64, format string) error {
	const chunk = 400
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		ph := strings.TrimRight(strings.Repeat("?,", len(part)), ",")
		if _, err := tx.Exec(fmt.Sprintf(format, ph), args...); err != nil {
			return err
		}
	}
	return nil
}

func ftsMatchQuery(term string) string {
	// FTS5 reads a NUL byte as the end of its query string, which leaves the
	// quoted term unterminated and fails the whole list query with a syntax
	// error. A NUL cannot appear in an indexed token, so it separates words.
	term = strings.TrimSpace(strings.ReplaceAll(term, "\x00", " "))
	if term == "" {
		return ""
	}
	parts := strings.Fields(term)
	bits := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ReplaceAll(p, `"`, `""`)
		bits = append(bits, fmt.Sprintf(`"%s"*`, p))
	}
	return strings.Join(bits, " OR ")
}

// flowSearchAsID returns a flow id when term is an id lookup (#123, id:123, or plain
// digits with scope "id"). Otherwise ok is false and callers should use FTS.
func flowSearchAsID(term, scope string) (int64, bool) {
	term = strings.TrimSpace(term)
	if term == "" {
		return 0, false
	}
	lower := strings.ToLower(term)
	var raw string
	switch {
	case strings.HasPrefix(term, "#"):
		raw = term[1:]
		if raw == "" || strings.TrimSpace(raw) != raw {
			return 0, false
		}
	case strings.HasPrefix(lower, "id:"):
		raw = strings.TrimSpace(term[len("id:"):])
	case scope == "id":
		raw = term
	default:
		return 0, false
	}
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// appendFTSSearch adds an FTS MATCH clause when term is non-empty.
func appendFTSSearch(where []string, args []any, term string) ([]string, []any) {
	term = strings.TrimSpace(term)
	if term == "" {
		return where, args
	}
	q := ftsMatchQuery(term)
	if q == "" {
		return where, args
	}
	where = append(where, "id IN (SELECT rowid FROM flows_fts WHERE flows_fts MATCH ?)")
	args = append(args, q)
	return where, args
}

// ftsDrivenListQuery builds an id-ordered History page for a full-text search
// by walking the index in rowid order (a flow's id is its index rowid) and
// looking each match up in flows, so the query stops at a full page. The
// "id IN (SELECT rowid ... MATCH ?)" form appendFTSSearch produces reads every
// match into a temporary list before the first row is returned: for a term
// present in 450,000 of 500,000 flows that was 165 ms a page, and 430 ms for
// a later page, against 34 ms and 40 ms here; a rare term costs the same
// either way. CROSS JOIN fixes the join order, the subquery exposes only the
// rowid so the unqualified column names of the other filters still mean
// flows, and the page cursor and scan bound are applied to the index rowid so
// the walk starts (and ends) there instead of reading matches it will discard.
// ok is false when the search is an id lookup, the list is sorted by another
// column, or the filter names the flows by id (a handful of primary-key
// lookups beats walking every match to find them: 274 ms against 438 ms for
// three ids); those keep the IN form.
func ftsDrivenListQuery(f FlowFilter, columns string, limit int) (string, []any, bool) {
	if _, isID := flowSearchAsID(f.Search, f.SearchScope); isID || len(f.FlowIDs) > 0 {
		return "", nil, false
	}
	match := ftsMatchQuery(f.Search)
	key, dir := normalizedFlowSort(f)
	if match == "" || key != "id" {
		return "", nil, false
	}
	order, past, bound := "DESC", "<", ">="
	if dir > 0 {
		order, past, bound = "ASC", ">", "<="
	}
	sub := "SELECT rowid AS fts_id FROM flows_fts WHERE flows_fts MATCH ?"
	args := []any{match}
	if cursor := flowPageCursorID(f); cursor != 0 {
		sub += " AND rowid " + past + " ?"
		args = append(args, cursor)
	}
	if f.ScanBoundID > 0 {
		sub += " AND rowid " + bound + " ?"
		args = append(args, f.ScanBoundID)
	}
	rest := f
	rest.Search = ""
	where, whereArgs := buildFlowFilterWhere(rest)
	q := "SELECT " + columns + " FROM (" + sub + ") AS fts_match CROSS JOIN flows ON flows.id = fts_match.fts_id"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY fts_match.fts_id " + order + " LIMIT " + strconv.Itoa(limit)
	return q, append(args, whereArgs...), true
}
