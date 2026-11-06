package store

import (
	"fmt"
	"strings"
	"time"
)

// Coverage statuses. "considered" is the weakest signal (the agent looked at the
// playbook for this surface), "run" means it sent the probes, "found" produced a
// finding, "dismissed" was checked and ruled out with a reason.
const (
	CoverageConsidered = "considered"
	CoverageRun        = "run"
	CoverageFound      = "found"
	CoverageDismissed  = "dismissed"
)

// PlaybookCoverage is one "we tested this playbook against this surface" record.
type PlaybookCoverage struct {
	PlaybookID string  `json:"playbookId"`
	Host       string  `json:"host"`
	Endpoint   string  `json:"endpoint"`
	Param      string  `json:"param,omitempty"`
	Status     string  `json:"status"`
	FlowIDs    []int64 `json:"flowIds,omitempty"`
	Note       string  `json:"note,omitempty"`
	TS         int64   `json:"ts"`
}

// ValidCoverageStatus reports whether status is one of the four.
func ValidCoverageStatus(s string) bool {
	switch s {
	case CoverageConsidered, CoverageRun, CoverageFound, CoverageDismissed:
		return true
	}
	return false
}

// RecordPlaybookCoverage upserts one coverage row. A later status replaces an
// earlier one for the same surface; flow ids accumulate so the evidence stays
// linked across a considered → run → found progression.
func (s *Store) RecordPlaybookCoverage(c PlaybookCoverage) (PlaybookCoverage, error) {
	c.Host = strings.ToLower(strings.TrimSpace(c.Host))
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Param = strings.TrimSpace(c.Param)
	c.Status = strings.ToLower(strings.TrimSpace(c.Status))
	if strings.TrimSpace(c.PlaybookID) == "" {
		return c, fmt.Errorf("playbookId is required")
	}
	if !ValidCoverageStatus(c.Status) {
		return c, fmt.Errorf("status must be considered, run, found or dismissed (got %q)", c.Status)
	}
	c.TS = time.Now().UnixMilli()
	existing, err := s.ListPlaybookCoverage(PlaybookCoverageFilter{
		PlaybookID: c.PlaybookID, Host: c.Host, Endpoint: c.Endpoint, Param: c.Param,
	})
	if err == nil && len(existing) == 1 {
		seen := map[int64]bool{}
		for _, id := range existing[0].FlowIDs {
			seen[id] = true
		}
		for _, id := range c.FlowIDs {
			if !seen[id] {
				existing[0].FlowIDs = append(existing[0].FlowIDs, id)
			}
		}
		c.FlowIDs = existing[0].FlowIDs
	}
	_, err = s.db.Exec(`INSERT INTO playbook_coverage(playbook_id,host,endpoint,param,status,flow_ids,note,ts)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(playbook_id,host,endpoint,param) DO UPDATE SET
			status=excluded.status, flow_ids=excluded.flow_ids, note=excluded.note, ts=excluded.ts`,
		c.PlaybookID, c.Host, c.Endpoint, c.Param, c.Status, joinIDs(c.FlowIDs), c.Note, c.TS)
	if err != nil {
		return c, fmt.Errorf("store: record playbook coverage: %w", err)
	}
	return c, nil
}

// PlaybookCoverageFilter narrows a coverage listing; empty fields mean all.
type PlaybookCoverageFilter struct {
	PlaybookID string
	Host       string
	Endpoint   string
	Param      string
	Limit      int
}

// ListPlaybookCoverage returns matching rows, newest first.
func (s *Store) ListPlaybookCoverage(f PlaybookCoverageFilter) ([]PlaybookCoverage, error) {
	var where []string
	var args []any
	add := func(col, val string) {
		if val != "" {
			where = append(where, col+" = ?")
			args = append(args, val)
		}
	}
	add("playbook_id", f.PlaybookID)
	add("host", strings.ToLower(f.Host))
	add("endpoint", f.Endpoint)
	add("param", f.Param)
	q := `SELECT playbook_id,host,endpoint,param,status,flow_ids,note,ts FROM playbook_coverage`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY ts DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list playbook coverage: %w", err)
	}
	defer rows.Close()
	var out []PlaybookCoverage
	for rows.Next() {
		var (
			c    PlaybookCoverage
			ids  string
		)
		if err := rows.Scan(&c.PlaybookID, &c.Host, &c.Endpoint, &c.Param, &c.Status, &ids, &c.Note, &c.TS); err != nil {
			return nil, err
		}
		c.FlowIDs = splitIDs(ids)
		out = append(out, c)
	}
	return out, rows.Err()
}

// PlaybookCoverageCounts is one playbook's rollup: how many surfaces at each status.
type PlaybookCoverageCounts struct {
	PlaybookID  string `json:"playbookId"`
	Considered  int64  `json:"considered"`
	Run         int64  `json:"run"`
	Found       int64  `json:"found"`
	Dismissed   int64  `json:"dismissed"`
	LastTS      int64  `json:"lastTs"`
}

// CoverageSummary rolls coverage up per playbook.
func (s *Store) CoverageSummary() ([]PlaybookCoverageCounts, error) {
	rows, err := s.db.Query(`SELECT playbook_id, status, COUNT(*), MAX(ts) FROM playbook_coverage GROUP BY playbook_id, status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index := map[string]*PlaybookCoverageCounts{}
	for rows.Next() {
		var (
			id     string
			status string
			n, ts  int64
		)
		if err := rows.Scan(&id, &status, &n, &ts); err != nil {
			return nil, err
		}
		c, ok := index[id]
		if !ok {
			c = &PlaybookCoverageCounts{PlaybookID: id}
			index[id] = c
		}
		switch status {
		case CoverageConsidered:
			c.Considered += n
		case CoverageRun:
			c.Run += n
		case CoverageFound:
			c.Found += n
		case CoverageDismissed:
			c.Dismissed += n
		}
		if ts > c.LastTS {
			c.LastTS = ts
		}
	}
	out := make([]PlaybookCoverageCounts, 0, len(index))
	for _, c := range index {
		out = append(out, *c)
	}
	return out, rows.Err()
}

// joinIDs renders flow ids as a comma list.
func joinIDs(ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return strings.Join(parts, ",")
}

// splitIDs parses the stored comma list.
func splitIDs(raw string) []int64 {
	if raw == "" {
		return nil
	}
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		var id int64
		if _, err := fmt.Sscan(strings.TrimSpace(part), &id); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}
