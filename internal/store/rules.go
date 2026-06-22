package store

import (
	"strconv"
	"strings"
)

// Rule is one ordered match-&-replace transform. Type is one of
// "req-header", "req-body" (response-side types are reserved for a later slice).
type Rule struct {
	ID      int64
	Ord     int
	Enabled bool
	Type    string
	Match   string
	Replace string
}

// ListRules returns all rules ordered by ord then id.
func (s *Store) ListRules() ([]Rule, error) {
	rows, err := s.db.Query(`SELECT id, ord, enabled, type, match, replace FROM rules ORDER BY ord, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.Ord, &r.Enabled, &r.Type, &r.Match, &r.Replace); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRule inserts a rule and returns its id.
func (s *Store) CreateRule(r *Rule) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO rules (ord, enabled, type, match, replace) VALUES (?,?,?,?,?)`,
		r.Ord, r.Enabled, r.Type, r.Match, r.Replace)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	r.ID = id
	return id, nil
}

// UpdateRule overwrites the rule identified by r.ID.
func (s *Store) UpdateRule(r *Rule) error {
	_, err := s.db.Exec(
		`UPDATE rules SET ord=?, enabled=?, type=?, match=?, replace=? WHERE id=?`,
		r.Ord, r.Enabled, r.Type, r.Match, r.Replace, r.ID)
	return err
}

// DeleteRule removes a rule by id.
func (s *Store) DeleteRule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM rules WHERE id=?`, id)
	return err
}

// FlowFilter selects and pages flows. Zero-valued fields are ignored.
type FlowFilter struct {
	Limit       int    // max rows (defaults to 200 when <= 0)
	BeforeID    int64  // cursor: only rows with id < BeforeID (0 = newest)
	Method      string // exact method match
	Host        string // case-insensitive substring of host
	Search      string // case-insensitive substring of path
	Scheme      string // exact scheme match ("http"/"https")
	StatusClass int    // 1..5 → 1xx..5xx; 0 = any
}

// QueryFlowsFilter returns flows matching f, newest first. Filtering and paging
// are pushed down to SQL so large histories never materialize in memory.
func (s *Store) QueryFlowsFilter(f FlowFilter) ([]*Flow, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	var (
		where []string
		args  []any
	)
	if f.BeforeID > 0 {
		where = append(where, "id < ?")
		args = append(args, f.BeforeID)
	}
	if f.Method != "" {
		where = append(where, "method = ?")
		args = append(args, f.Method)
	}
	if f.Scheme != "" {
		where = append(where, "scheme = ?")
		args = append(args, f.Scheme)
	}
	if f.Host != "" {
		where = append(where, "instr(lower(host), lower(?)) > 0")
		args = append(args, f.Host)
	}
	if f.Search != "" {
		where = append(where, "instr(lower(path), lower(?)) > 0")
		args = append(args, f.Search)
	}
	if f.StatusClass >= 1 && f.StatusClass <= 5 {
		lo := f.StatusClass * 100
		where = append(where, "status >= ? AND status < ?")
		args = append(args, lo, lo+100)
	}

	q := "SELECT " + flowColumns + " FROM flows"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC LIMIT " + strconv.Itoa(limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Flow
	for rows.Next() {
		fl, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fl)
	}
	return out, rows.Err()
}
