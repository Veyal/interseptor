package store

import (
	"strings"
	"time"
)

// SetFindingTags replaces a finding's tag set with the normalized `tags` (empty clears).
func (s *Store) SetFindingTags(findingID int64, tags []string) ([]string, error) {
	norm := NormalizeTags(tags)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM findings WHERE id=?`, findingID).Scan(&exists); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM finding_tags WHERE finding_id=?`, findingID); err != nil {
		return nil, err
	}
	for _, t := range norm {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO finding_tags (finding_id, tag) VALUES (?,?)`, findingID, t); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(`UPDATE findings SET updated_ts=? WHERE id=?`, time.Now().UnixMilli(), findingID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return norm, nil
}

// FindingTags returns one finding's tags, sorted.
func (s *Store) FindingTags(findingID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT tag FROM finding_tags WHERE finding_id=? ORDER BY tag`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TagsForFindings batch-loads tags for many findings in one query.
func (s *Store) TagsForFindings(ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT finding_id, tag FROM finding_tags WHERE finding_id IN (`+ph+`) ORDER BY tag`, args...)
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

// DistinctFindingTags lists every finding tag in use with its count, most-used first.
func (s *Store) DistinctFindingTags() ([]TagCount, error) {
	rows, err := s.db.Query(`
		SELECT ft.tag, COUNT(*) AS n, COALESCE(tm.color,'')
		FROM finding_tags ft
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
