package store

import (
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
	norm := NormalizeTags(tags)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
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
func (s *Store) AddFlowTags(flowID int64, tags []string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, t := range NormalizeTags(tags) {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO flow_tags (flow_id, tag) VALUES (?,?)`, flowID, t); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(`SELECT tag FROM flow_tags WHERE flow_id=? ORDER BY tag`, flowID)
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
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveFlowTag detaches a single tag from a flow.
func (s *Store) RemoveFlowTag(flowID int64, tag string) error {
	_, err := s.db.Exec(`DELETE FROM flow_tags WHERE flow_id=? AND tag=?`, flowID, normalizeTag(tag))
	return err
}

// FlowTags returns one flow's tags, sorted.
func (s *Store) FlowTags(flowID int64) ([]string, error) {
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
