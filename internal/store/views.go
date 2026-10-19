package store

import (
	"database/sql"
	"errors"
)

// SavedView is a named history filter (its Data is an opaque JSON blob the UI
// understands: scheme/method/status/search/host/inScope).
type SavedView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Data string `json:"data"`
}

// ListViews returns saved views, newest first.
func (s *Store) ListViews() ([]SavedView, error) {
	rows, err := s.db.Query(`SELECT id, name, data FROM saved_views ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedView
	for rows.Next() {
		var v SavedView
		if err := rows.Scan(&v.ID, &v.Name, &v.Data); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CreateView stores a named view and returns its id. A view's name is its
// identity: saving again under an existing name replaces that view's filters
// and keeps its id, instead of listing the same name twice.
func (s *Store) CreateView(v *SavedView) (int64, error) {
	var existing int64
	err := s.db.QueryRow(`SELECT id FROM saved_views WHERE name = ? ORDER BY id DESC LIMIT 1`, v.Name).Scan(&existing)
	if err == nil {
		if _, err := s.db.Exec(`UPDATE saved_views SET data = ? WHERE id = ?`, v.Data, existing); err != nil {
			return 0, err
		}
		v.ID = existing
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO saved_views (name, data) VALUES (?, ?)`, v.Name, v.Data)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	v.ID = id
	return id, nil
}

// DeleteView removes a view by id.
func (s *Store) DeleteView(id int64) error {
	_, err := s.db.Exec(`DELETE FROM saved_views WHERE id = ?`, id)
	return err
}
