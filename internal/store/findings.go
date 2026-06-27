package store

import (
	"strings"
	"time"
)

// Finding is a curated vulnerability write-up for a project. Unlike a scanner
// Issue (auto-generated, ephemeral), a Finding is persistent and human/AI-curated:
// it carries a status the operator manages and can have multiple request/response
// PoCs (flows) attached as evidence.
type Finding struct {
	ID        int64         `json:"id"`
	TS        int64         `json:"ts"`        // created, unix millis
	UpdatedTS int64         `json:"updatedTs"` // last modified, unix millis
	Severity  string        `json:"severity"`  // High | Medium | Low | Info
	Status    string        `json:"status"`    // open | verified | false_positive | wont_fix | fixed
	Source    string        `json:"source"`    // human | ai | scanner
	Title     string        `json:"title"`
	Target    string        `json:"target"`
	Detail    string        `json:"detail"`
	Evidence  string        `json:"evidence"`
	Fix       string        `json:"fix"`
	Flows     []FindingFlow `json:"flows"` // attached PoC request/response flows
}

// FindingFlow is one PoC flow attached to a finding, enriched with a compact flow
// summary for display (the human selects request/responses to record here).
type FindingFlow struct {
	FlowID int64  `json:"flowId"`
	Ord    int    `json:"ord"`
	Note   string `json:"note,omitempty"`
	Method string `json:"method,omitempty"`
	Host   string `json:"host,omitempty"`
	Path   string `json:"path,omitempty"`
	Status int    `json:"status,omitempty"`
}

func normalizeFindingSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "high":
		return "High"
	case "low":
		return "Low"
	case "info", "informational":
		return "Info"
	default:
		return "Medium"
	}
}

func normalizeFindingStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "verified":
		return "verified"
	case "false_positive", "false-positive", "fp":
		return "false_positive"
	case "wont_fix", "wontfix", "won't_fix":
		return "wont_fix"
	case "fixed", "remediated":
		return "fixed"
	default:
		return "open"
	}
}

func normalizeFindingSource(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ai":
		return "ai"
	case "scanner":
		return "scanner"
	default:
		return "human"
	}
}

// CreateFinding inserts a finding (normalizing severity/status/source) and sets
// f.ID/f.TS/f.UpdatedTS. Title is required.
func (s *Store) CreateFinding(f *Finding) (int64, error) {
	now := time.Now().UnixMilli()
	f.TS, f.UpdatedTS = now, now
	f.Severity = normalizeFindingSeverity(f.Severity)
	f.Status = normalizeFindingStatus(f.Status)
	f.Source = normalizeFindingSource(f.Source)
	res, err := s.db.Exec(
		`INSERT INTO findings (ts, updated_ts, severity, status, source, title, target, detail, evidence, fix)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		f.TS, f.UpdatedTS, f.Severity, f.Status, f.Source, f.Title, f.Target, f.Detail, f.Evidence, f.Fix)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	f.ID = id
	return id, nil
}

// UpdateFinding applies the non-nil fields to a finding and bumps updated_ts. A
// nil field is left unchanged; severity/status/source are normalized when set.
func (s *Store) UpdateFinding(id int64, severity, status, title, target, detail, evidence, fix *string) error {
	sets := []string{"updated_ts=?"}
	args := []any{time.Now().UnixMilli()}
	if severity != nil {
		sets = append(sets, "severity=?")
		args = append(args, normalizeFindingSeverity(*severity))
	}
	if status != nil {
		sets = append(sets, "status=?")
		args = append(args, normalizeFindingStatus(*status))
	}
	if title != nil {
		sets = append(sets, "title=?")
		args = append(args, *title)
	}
	if target != nil {
		sets = append(sets, "target=?")
		args = append(args, *target)
	}
	if detail != nil {
		sets = append(sets, "detail=?")
		args = append(args, *detail)
	}
	if evidence != nil {
		sets = append(sets, "evidence=?")
		args = append(args, *evidence)
	}
	if fix != nil {
		sets = append(sets, "fix=?")
		args = append(args, *fix)
	}
	args = append(args, id)
	_, err := s.db.Exec(`UPDATE findings SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...)
	return err
}

// DeleteFinding removes a finding and its PoC attachments.
func (s *Store) DeleteFinding(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM finding_flows WHERE finding_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM findings WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AttachFlow records a flow as a PoC for a finding (idempotent on re-attach, which
// updates the note/order). The finding's updated_ts is bumped.
func (s *Store) AttachFlow(findingID, flowID int64, note string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var nextOrd int
	_ = tx.QueryRow(`SELECT COALESCE(MAX(ord)+1, 0) FROM finding_flows WHERE finding_id=?`, findingID).Scan(&nextOrd)
	if _, err := tx.Exec(
		`INSERT INTO finding_flows (finding_id, flow_id, ord, note) VALUES (?,?,?,?)
		 ON CONFLICT(finding_id, flow_id) DO UPDATE SET note=excluded.note`,
		findingID, flowID, nextOrd, note); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE findings SET updated_ts=? WHERE id=?`, time.Now().UnixMilli(), findingID); err != nil {
		return err
	}
	return tx.Commit()
}

// DetachFlow removes a PoC flow from a finding.
func (s *Store) DetachFlow(findingID, flowID int64) error {
	if _, err := s.db.Exec(`DELETE FROM finding_flows WHERE finding_id=? AND flow_id=?`, findingID, flowID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE findings SET updated_ts=? WHERE id=?`, time.Now().UnixMilli(), findingID)
	return err
}

// findingFlows loads the PoC flows for a finding, enriched with a compact flow
// summary (method/host/path/status) via a LEFT JOIN so a since-deleted flow still
// lists with its id.
func (s *Store) findingFlows(findingID int64) ([]FindingFlow, error) {
	rows, err := s.db.Query(
		`SELECT ff.flow_id, ff.ord, ff.note, f.method, f.host, f.path, f.status
		 FROM finding_flows ff LEFT JOIN flows f ON f.id = ff.flow_id
		 WHERE ff.finding_id=? ORDER BY ff.ord, ff.flow_id`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindingFlow
	for rows.Next() {
		var ff FindingFlow
		var method, host, path *string
		var status *int
		if err := rows.Scan(&ff.FlowID, &ff.Ord, &ff.Note, &method, &host, &path, &status); err != nil {
			return nil, err
		}
		if method != nil {
			ff.Method = *method
		}
		if host != nil {
			ff.Host = *host
		}
		if path != nil {
			ff.Path = *path
		}
		if status != nil {
			ff.Status = *status
		}
		out = append(out, ff)
	}
	return out, rows.Err()
}

func scanFinding(sc scanner) (*Finding, error) {
	var f Finding
	if err := sc.Scan(&f.ID, &f.TS, &f.UpdatedTS, &f.Severity, &f.Status, &f.Source,
		&f.Title, &f.Target, &f.Detail, &f.Evidence, &f.Fix); err != nil {
		return nil, err
	}
	return &f, nil
}

const findingCols = `id, ts, updated_ts, severity, status, source, title, target, detail, evidence, fix`

// GetFinding loads one finding with its PoC flows.
func (s *Store) GetFinding(id int64) (*Finding, error) {
	f, err := scanFinding(s.db.QueryRow(`SELECT `+findingCols+` FROM findings WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if f.Flows, err = s.findingFlows(id); err != nil {
		return nil, err
	}
	return f, nil
}

// ListFindings returns findings ordered by severity (High→Info) then newest, each
// with its PoC flows. Empty severity/status means "any".
func (s *Store) ListFindings(severity, status string) ([]Finding, error) {
	where := []string{"1=1"}
	args := []any{}
	if severity != "" {
		where = append(where, "severity=?")
		args = append(args, normalizeFindingSeverity(severity))
	}
	if status != "" {
		where = append(where, "status=?")
		args = append(args, normalizeFindingStatus(status))
	}
	rows, err := s.db.Query(
		`SELECT `+findingCols+` FROM findings WHERE `+strings.Join(where, " AND ")+
			` ORDER BY CASE severity WHEN 'High' THEN 0 WHEN 'Medium' THEN 1 WHEN 'Low' THEN 2 ELSE 3 END, id DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Flows, err = s.findingFlows(out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}
