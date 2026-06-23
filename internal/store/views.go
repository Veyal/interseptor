package store

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

// CreateView stores a named view and returns its id.
func (s *Store) CreateView(v *SavedView) (int64, error) {
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
