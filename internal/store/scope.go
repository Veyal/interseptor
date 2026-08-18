package store

import "database/sql"

// ScopeRule is one target-scope rule. Action is "include" | "exclude". Empty
// host/path/scheme and port 0 mean "any" for that field.
type ScopeRule struct {
	ID      int64  `json:"id"`
	Ord     int    `json:"ord"`
	Enabled bool   `json:"enabled"`
	Action  string `json:"action"`
	Host    string `json:"host"`
	Path    string `json:"path"`
	Scheme  string `json:"scheme"`
	Port    int    `json:"port"`
}

// ListScopeRules returns scope rules ordered by ord then id.
func (s *Store) ListScopeRules() ([]ScopeRule, error) {
	rows, err := s.db.Query(`SELECT id, ord, enabled, action, host, path, scheme, port FROM scope_rules ORDER BY ord, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScopeRule
	for rows.Next() {
		var r ScopeRule
		if err := rows.Scan(&r.ID, &r.Ord, &r.Enabled, &r.Action, &r.Host, &r.Path, &r.Scheme, &r.Port); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateScopeRule inserts a scope rule and returns its id.
func (s *Store) CreateScopeRule(r *ScopeRule) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO scope_rules (ord, enabled, action, host, path, scheme, port) VALUES (?,?,?,?,?,?,?)`,
		r.Ord, r.Enabled, r.Action, r.Host, r.Path, r.Scheme, r.Port)
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

// UpdateScopeRule overwrites the rule identified by r.ID.
func (s *Store) UpdateScopeRule(r *ScopeRule) error {
	res, err := s.db.Exec(
		`UPDATE scope_rules SET ord=?, enabled=?, action=?, host=?, path=?, scheme=?, port=? WHERE id=?`,
		r.Ord, r.Enabled, r.Action, r.Host, r.Path, r.Scheme, r.Port, r.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteScopeRule removes a scope rule by id.
func (s *Store) DeleteScopeRule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM scope_rules WHERE id=?`, id)
	return err
}
