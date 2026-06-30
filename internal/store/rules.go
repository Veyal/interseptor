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
	Limit        int    // max rows (defaults to 200 when <= 0)
	BeforeID     int64  // legacy cursor: id < BeforeID when sorting id DESC
	CursorID     int64  // keyset cursor flow id (0 = first page)
	CursorVal    string // sort value at the cursor row (required for non-id sorts)
	SortKey      string // id|method|host|path|status|size|time|mime
	SortDir      int    // +1 asc, -1 desc; 0 = default for the key
	Method       string // exact method match
	Host         string // case-insensitive substring of host
	Search       string // FTS on host/path/method/note, or exact id when SearchScope=id / #id / id:N
	SearchScope  string // path (default FTS), body (handled in control), id (exact flow id)
	Scheme       string // exact scheme match ("http"/"https")
	StatusClass  int    // 1..5 → 1xx..5xx; 0 = any
	RequireFlags  int64 // only rows with any of these flag bits set
	ExcludeFlags  int64 // only rows with none of these flag bits set
	IncludeFlags  int64 // rows with any of these bits are kept even if ExcludeFlags also matches
	WithoutFlags  int64 // only rows with none of these flag bits set (independent of ExcludeFlags)

	// Negative filters — each entry excludes matching rows; multiples are ANDed.
	NotMethods  []string // exclude these exact methods
	NotHosts    []string // exclude rows whose host contains any of these
	NotPaths    []string // exclude rows whose path contains any of these
	NotStatuses []int    // exclude these exact status codes

	FlowIDs []int64 // when set, only these ids (used for body search results)
	HasNote bool    // only rows with a non-empty note
	Tag     string  // only rows carrying this exact tag
}

// QueryFlowsFilter returns flows matching f, newest first. Filtering and paging
// are pushed down to SQL so large histories never materialize in memory.
func (s *Store) QueryFlowsFilter(f FlowFilter) ([]*Flow, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	where, args := buildFlowFilterWhere(f)
	where, args = appendFlowPageCursor(f, where, args)

	q := "SELECT " + flowColumns + " FROM flows"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += flowListOrderBy(f) + " LIMIT " + strconv.Itoa(limit)

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
