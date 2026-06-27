package store

// Activity is one recorded AI (MCP) tool call, persisted per-project so the
// glass-box feed survives restarts.
type Activity struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"` // unix millis
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	OK      bool   `json:"ok"`
	Result  string `json:"result"`
	Ms      int64  `json:"ms"`
	Intent  string `json:"intent,omitempty"` // the AI's stated "why" for a consequential action
}

// activityKeep bounds how many activity rows are retained per project.
const activityKeep = 5000

// InsertActivity persists an AI tool-call record (a.TS set by the caller) and
// sets a.ID. Rows older than the most recent activityKeep are pruned so the log
// can't grow without bound.
func (s *Store) InsertActivity(a *Activity) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO activity (ts, tool, summary, ok, result, ms, intent) VALUES (?,?,?,?,?,?,?)`,
		a.TS, a.Tool, a.Summary, a.OK, a.Result, a.Ms, a.Intent)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	a.ID = id
	_, _ = s.db.Exec(`DELETE FROM activity WHERE id <= ?`, id-activityKeep)
	return id, nil
}

// DeleteActivity removes every recorded activity row (the user cleared the feed).
func (s *Store) DeleteActivity() error {
	_, err := s.db.Exec(`DELETE FROM activity`)
	return err
}

// ListActivity returns recorded activity newest-first, capped at limit.
func (s *Store) ListActivity(limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 300
	}
	rows, err := s.db.Query(
		`SELECT id, ts, tool, summary, ok, result, ms, intent FROM activity ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.TS, &a.Tool, &a.Summary, &a.OK, &a.Result, &a.Ms, &a.Intent); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
