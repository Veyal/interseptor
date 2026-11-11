package store

import (
	"database/sql"
	"sort"
	"strings"
)

// Tags are short labels attached to flows — by an operator (right-click) or the AI
// (MCP tag_flow) — for triage, filtering, and grouping on the Map. They live in a
// separate flow_tags table (not a flows column) so the hot insert/scan path is
// untouched; the list/map layers batch-load them per page.

const (
	maxTagLen     = 32
	maxTagsPerRow = 30
)

// normalizeTag lowercases, trims, and reduces a tag to a safe slug ([a-z0-9._-]),
// collapsing other runs to a single '-'. Returns "" for an empty/garbage tag.
func normalizeTag(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxTagLen {
		out = strings.Trim(out[:maxTagLen], "-")
	}
	return out
}

// NormalizeTags cleans, de-duplicates and sorts a tag list, dropping empties and
// capping the count. Exposed so callers (API/MCP) can normalize before display.
func NormalizeTags(tags []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range tags {
		n := normalizeTag(t)
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
		if len(out) >= maxTagsPerRow {
			break
		}
	}
	sort.Strings(out)
	return out
}

// SetFlowTags replaces a flow's tag set with the normalized `tags` (empty clears).
func (s *Store) SetFlowTags(flowID int64, tags []string) ([]string, error) {
	s.waitFlow(flowID)
	norm := NormalizeTags(tags)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireFlowForTags(tx, flowID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM flow_tags WHERE flow_id=?`, flowID); err != nil {
		return nil, err
	}
	for _, t := range norm {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO flow_tags (flow_id, tag) VALUES (?,?)`, flowID, t); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return norm, nil
}

// AddFlowTags adds tags to a flow (union) and returns the flow's full tag set.
// Queued capture writes for this flow are drained first so the tag cannot
// land before the row.
func (s *Store) AddFlowTags(flowID int64, tags []string) ([]string, error) {
	s.waitFlow(flowID)
	return s.writeFlowTags(flowID, tags)
}

func (s *Store) writeFlowTags(flowID int64, tags []string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	s.flowTxs.Add(1)
	defer tx.Rollback()
	out, err := writeFlowTagsExec(tx, flowID, tags)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// writeFlowTagsExec unions tags onto an existing flow and returns the row's
// full tag set. The caller owns the transaction boundary.
func writeFlowTagsExec(ex flowExec, flowID int64, tags []string) ([]string, error) {
	if err := requireFlowForTags(ex, flowID); err != nil {
		return nil, err
	}
	for _, t := range NormalizeTags(tags) {
		if _, err := ex.Exec(`INSERT OR IGNORE INTO flow_tags (flow_id, tag) VALUES (?,?)`, flowID, t); err != nil {
			return nil, err
		}
	}
	rows, err := ex.Query(`SELECT tag FROM flow_tags WHERE flow_id=? ORDER BY tag`, flowID)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, tag)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return out, nil
}

// MutateFlowTags applies one add/remove command to every selected flow in one
// transaction. Remove wins when the same normalized tag appears in both sets.
func (s *Store) MutateFlowTags(flowIDs []int64, add, remove []string) error {
	_, err := s.MutateFlowTagsBulk(flowIDs, add, remove)
	return err
}

// tagBulkChunk is how many flow ids one bulk tag statement names. With up to
// maxTagsPerRow tags bound beside them a statement stays far below SQLite's
// bound-parameter limit.
const tagBulkChunk = 400

// MutateFlowTagsBulk applies one add/remove command to every selected flow in
// one transaction and returns the flows whose tag set changed, in the order
// they were given. Remove wins when the same normalized tag appears in both
// sets. Every flow must exist, or nothing is written. The work is one
// statement per chunk of tagBulkChunk flows (and per added tag), not one per
// flow: tagging 100,000 flows is a few hundred statements instead of several
// hundred thousand. Tags are not part of the full-text index (flows_fts holds
// host, path, method and note), so only flow_tags and its tag index change,
// and SQLite keeps those two in step.
func (s *Store) MutateFlowTagsBulk(flowIDs []int64, add, remove []string) ([]int64, error) {
	ids := uniqueFlowIDs(flowIDs)
	// Queued capture writes are drained first so a tag cannot land before its
	// row. With an idle queue there is nothing to wait for.
	if s.flowsBusy() {
		for _, id := range ids {
			s.waitFlow(id)
		}
	}
	remove = NormalizeTags(remove)
	add = tagsWithout(NormalizeTags(add), remove)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	st := &txStmts{tx: tx}
	changed := make(map[int64]struct{})
	for start := 0; start < len(ids); start += tagBulkChunk {
		if err := mutateTagChunk(st, ids[start:min(start+tagBulkChunk, len(ids))], add, remove, changed); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(changed))
	for _, id := range ids {
		if _, ok := changed[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// mutateTagChunk tags and untags one chunk of distinct flow ids, recording in
// changed every flow that gained or lost a row.
func mutateTagChunk(st *txStmts, ids []int64, add, remove []string, changed map[int64]struct{}) error {
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+len(remove)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	// flow_tags has no foreign key: a stale id would create an orphan tag row.
	var found int
	if err := st.QueryRow(`SELECT COUNT(*) FROM flows WHERE id IN (`+ph+`)`, args...).Scan(&found); err != nil {
		return err
	}
	if found != len(ids) {
		return sql.ErrNoRows
	}
	insert := `INSERT OR IGNORE INTO flow_tags (flow_id, tag) SELECT id, ? FROM flows WHERE id IN (` + ph + `) RETURNING flow_id`
	for _, tag := range add {
		if err := collectChangedFlows(st, changed, insert, append([]any{tag}, args...)...); err != nil {
			return err
		}
	}
	if len(remove) == 0 {
		return nil
	}
	for _, tag := range remove {
		args = append(args, tag)
	}
	tagPH := strings.TrimRight(strings.Repeat("?,", len(remove)), ",")
	return collectChangedFlows(st, changed,
		`DELETE FROM flow_tags WHERE flow_id IN (`+ph+`) AND tag IN (`+tagPH+`) RETURNING flow_id`, args...)
}

// collectChangedFlows runs a statement that returns the flow id of every row
// it wrote and adds those ids to changed.
func collectChangedFlows(st *txStmts, changed map[int64]struct{}, query string, args ...any) error {
	rows, err := st.Query(query, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		changed[id] = struct{}{}
	}
	// A statement that fails while stepping (a constraint, a trigger) reports
	// it here, not from Query.
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

// uniqueFlowIDs drops repeated ids, keeping the first occurrence's position.
func uniqueFlowIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// tagsWithout returns the tags that are not in drop.
func tagsWithout(tags, drop []string) []string {
	if len(drop) == 0 {
		return tags
	}
	skip := make(map[string]struct{}, len(drop))
	for _, t := range drop {
		skip[t] = struct{}{}
	}
	out := tags[:0:0]
	for _, t := range tags {
		if _, ok := skip[t]; !ok {
			out = append(out, t)
		}
	}
	return out
}

func requireFlowForTags(ex flowExec, flowID int64) error {
	var exists int
	return ex.QueryRow(`SELECT 1 FROM flows WHERE id=?`, flowID).Scan(&exists)
}

// RemoveFlowTag detaches a single tag from a flow.
func (s *Store) RemoveFlowTag(flowID int64, tag string) error {
	s.waitFlow(flowID)
	_, err := s.db.Exec(`DELETE FROM flow_tags WHERE flow_id=? AND tag=?`, flowID, normalizeTag(tag))
	return err
}

// FlowTags returns one flow's tags, sorted.
func (s *Store) FlowTags(flowID int64) ([]string, error) {
	s.waitFlow(flowID)
	rows, err := s.db.Query(`SELECT tag FROM flow_tags WHERE flow_id=? ORDER BY tag`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TagsForFlows batch-loads tags for many flows in one query (no N+1), returning a
// map from flow id to its sorted tag list. Ids with no tags are absent from the map.
func (s *Store) TagsForFlows(ids []int64) (map[int64][]string, error) {
	s.syncFlows()
	out := map[int64][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT flow_id, tag FROM flow_tags WHERE flow_id IN (`+ph+`) ORDER BY tag`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		out[id] = append(out[id], tag)
	}
	return out, rows.Err()
}

// AttachTags populates each flow's Tags field from one batch query.
func (s *Store) AttachTags(flows []*Flow) error {
	if len(flows) == 0 {
		return nil
	}
	ids := make([]int64, len(flows))
	for i, f := range flows {
		ids[i] = f.ID
	}
	m, err := s.TagsForFlows(ids)
	if err != nil {
		return err
	}
	for _, f := range flows {
		f.Tags = m[f.ID]
	}
	return nil
}

// TagCount is a tag with how many flows carry it and its (optional) color.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
	Color string `json:"color,omitempty"`
}

// DistinctTags lists every tag in use with its flow count and color, most-used first.
func (s *Store) DistinctTags() ([]TagCount, error) {
	rows, err := s.db.Query(`
		SELECT ft.tag, COUNT(*) AS n, COALESCE(tm.color,'')
		FROM flow_tags ft
		LEFT JOIN tag_meta tm ON tm.tag = ft.tag
		GROUP BY ft.tag
		ORDER BY n DESC, ft.tag ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count, &tc.Color); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// SetTagColor sets (or clears, with "") a tag's display color. The value is stored
// verbatim; callers validate it's a safe CSS color before persisting.
func (s *Store) SetTagColor(tag, color string) error {
	t := normalizeTag(tag)
	if t == "" {
		return nil
	}
	if color == "" {
		_, err := s.db.Exec(`DELETE FROM tag_meta WHERE tag=?`, t)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO tag_meta (tag, color) VALUES (?,?)
		ON CONFLICT(tag) DO UPDATE SET color=excluded.color`, t, color)
	return err
}
