package store

import (
	"strings"
)

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

// ReconcileIssuesForScan replaces issues attached to flows actually scanned and
// removes explicitly stale (for example now-out-of-scope) flow ids. Orphaned
// issues are removed in SQL. Deletes are chunked to stay below SQLite variable
// limits even for the scanner's largest bounded batch.
func (s *Store) ReconcileIssuesForScan(scannedFlowIDs, staleFlowIDs []int64, issues []Issue) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	deleteIDs := append(append(make([]int64, 0, len(scannedFlowIDs)+len(staleFlowIDs)), scannedFlowIDs...), staleFlowIDs...)
	const deleteChunk = 400
	for start := 0; start < len(deleteIDs); start += deleteChunk {
		end := start + deleteChunk
		if end > len(deleteIDs) {
			end = len(deleteIDs)
		}
		args := make([]any, end-start)
		marks := make([]string, end-start)
		for i, id := range deleteIDs[start:end] {
			args[i], marks[i] = id, "?"
		}
		if _, err := tx.Exec(`DELETE FROM scan_issues WHERE flow_id IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM scan_issues WHERE NOT EXISTS (SELECT 1 FROM flows WHERE flows.id = scan_issues.flow_id)`); err != nil {
		return err
	}
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

// IssueFlowsPage returns a bounded page of compact, distinct flow rows
// referenced by passive issues. It omits bodies and headers.
func (s *Store) IssueFlowsPage(afterID int64, limit int) ([]*Flow, error) {
	if limit < 1 || limit > 2000 {
		limit = 500
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT f.id, f.scheme, f.host, f.port, f.path
		 FROM scan_issues i JOIN flows f ON f.id = i.flow_id
		 WHERE f.id > ? ORDER BY f.id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Flow
	for rows.Next() {
		f := new(Flow)
		if err := rows.Scan(&f.ID, &f.Scheme, &f.Host, &f.Port, &f.Path); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ClearIssues() error {
	_, err := s.db.Exec(`DELETE FROM scan_issues`)
	return err
}
