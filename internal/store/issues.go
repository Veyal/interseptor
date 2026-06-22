package store

// Issue is one scanner finding. Severity is "High" | "Medium" | "Low" | "Info".
type Issue struct {
	ID       int64  `json:"id"`
	FlowID   int64  `json:"flowId"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Target   string `json:"target"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence"`
	Fix      string `json:"fix"`
}

// SaveIssues upserts issues, deduplicated by (title, target).
func (s *Store) SaveIssues(issues []Issue) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(
		`INSERT INTO scan_issues (flow_id, severity, title, target, detail, evidence, fix)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(title, target) DO UPDATE SET
		   flow_id=excluded.flow_id, severity=excluded.severity,
		   detail=excluded.detail, evidence=excluded.evidence, fix=excluded.fix`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, is := range issues {
		if _, err := stmt.Exec(is.FlowID, is.Severity, is.Title, is.Target, is.Detail, is.Evidence, is.Fix); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListIssues returns all issues ordered by severity (High→Info) then id.
func (s *Store) ListIssues() ([]Issue, error) {
	rows, err := s.db.Query(
		`SELECT id, flow_id, severity, title, target, detail, evidence, fix
		 FROM scan_issues
		 ORDER BY CASE severity WHEN 'High' THEN 0 WHEN 'Medium' THEN 1 WHEN 'Low' THEN 2 ELSE 3 END, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var is Issue
		if err := rows.Scan(&is.ID, &is.FlowID, &is.Severity, &is.Title, &is.Target, &is.Detail, &is.Evidence, &is.Fix); err != nil {
			return nil, err
		}
		out = append(out, is)
	}
	return out, rows.Err()
}

// ClearIssues removes all issues.
func (s *Store) ClearIssues() error {
	_, err := s.db.Exec(`DELETE FROM scan_issues`)
	return err
}
