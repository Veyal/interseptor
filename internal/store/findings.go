package store

import (
	"encoding/json"
	"strings"
	"time"
)

// Finding is a curated vulnerability write-up for a project. Unlike a scanner
// Issue (auto-generated, ephemeral), a Finding is persistent and human/AI-curated:
// it carries a status the operator manages and has a narrative body — an ordered
// sequence of text blocks (markdown) and flow-reference blocks (clickable PoC
// request/response), freely interleaved.
type Finding struct {
	ID        int64          `json:"id"`
	TS        int64          `json:"ts"`        // created, unix millis
	UpdatedTS int64          `json:"updatedTs"` // last modified, unix millis
	Severity  string         `json:"severity"`  // High | Medium | Low | Info
	Status    string         `json:"status"`    // open | verified | false_positive | wont_fix | fixed
	Source    string         `json:"source"`    // human | ai | scanner
	Title     string         `json:"title"`
	Target    string         `json:"target"`
	Detail    string         `json:"detail"`   // legacy / MCP compat: first text block synced here
	Evidence  string         `json:"evidence"` // legacy only
	Fix       string         `json:"fix"`
	Body      string         `json:"body,omitempty"` // stored JSON blocks (use Blocks for rendering)
	Flows     []FindingFlow  `json:"flows"`           // attached flow metadata (for list sidebar count)
	Blocks    []FindingBlock `json:"blocks"`          // ordered narrative body (source of truth for UI)
}

// FindingBlock is one element in a finding's narrative body.
type FindingBlock struct {
	Type   string `json:"type"`             // "text" or "flow"
	MD     string `json:"md,omitempty"`     // type=="text": markdown content
	FlowID int64  `json:"flowId,omitempty"` // type=="flow": attached flow
	Note   string `json:"note,omitempty"`   // type=="flow": annotation

	// Enriched at read time from the flows JOIN — never stored in the body JSON.
	Method string `json:"method,omitempty"`
	Host   string `json:"host,omitempty"`
	Path   string `json:"path,omitempty"`
	Status int    `json:"status,omitempty"`
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

// blockRecord is the minimal form written to the body column (no enriched metadata).
type blockRecord struct {
	Type   string `json:"type"`
	MD     string `json:"md,omitempty"`
	FlowID int64  `json:"flowId,omitempty"`
	Note   string `json:"note,omitempty"`
}

// marshalBody serializes blocks for storage, stripping enriched metadata.
func marshalBody(blocks []FindingBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	recs := make([]blockRecord, len(blocks))
	for i, b := range blocks {
		recs[i] = blockRecord{Type: b.Type, MD: b.MD, FlowID: b.FlowID, Note: b.Note}
	}
	j, _ := json.Marshal(recs)
	return string(j)
}

// buildBlocks parses the stored body JSON and enriches flow blocks with flow
// metadata. If body is empty, synthesizes blocks from legacy detail/evidence/flows.
func buildBlocks(body, detail, evidence string, flows []FindingFlow) []FindingBlock {
	// Build a lookup of flow metadata.
	flowMeta := make(map[int64]FindingFlow, len(flows))
	for _, fl := range flows {
		flowMeta[fl.FlowID] = fl
	}

	if body != "" {
		var recs []blockRecord
		if err := json.Unmarshal([]byte(body), &recs); err == nil && len(recs) > 0 {
			blocks := make([]FindingBlock, len(recs))
			for i, r := range recs {
				blocks[i] = FindingBlock{Type: r.Type, MD: r.MD, FlowID: r.FlowID, Note: r.Note}
				if r.Type == "flow" {
					if fl, ok := flowMeta[r.FlowID]; ok {
						blocks[i].Method = fl.Method
						blocks[i].Host = fl.Host
						blocks[i].Path = fl.Path
						blocks[i].Status = fl.Status
					}
				}
			}
			return blocks
		}
	}

	// Legacy synthesis: detail text + evidence text + flow rows.
	var blocks []FindingBlock
	if detail != "" {
		blocks = append(blocks, FindingBlock{Type: "text", MD: detail})
	}
	if evidence != "" {
		blocks = append(blocks, FindingBlock{Type: "text", MD: evidence})
	}
	for _, fl := range flows {
		blocks = append(blocks, FindingBlock{
			Type: "flow", FlowID: fl.FlowID, Note: fl.Note,
			Method: fl.Method, Host: fl.Host, Path: fl.Path, Status: fl.Status,
		})
	}
	return blocks
}

// initialBody creates the first body JSON from create-time text fields.
func initialBody(detail, evidence string) string {
	var blocks []blockRecord
	if detail != "" {
		blocks = append(blocks, blockRecord{Type: "text", MD: detail})
	}
	if evidence != "" {
		blocks = append(blocks, blockRecord{Type: "text", MD: evidence})
	}
	if len(blocks) == 0 {
		return ""
	}
	j, _ := json.Marshal(blocks)
	return string(j)
}

// appendFlowToBody adds a flow block at the end of the stored body JSON.
// If the flow is already present, its note is updated. Returns the new body JSON.
func appendFlowToBody(bodyJSON string, flowID int64, note string) string {
	var recs []blockRecord
	if bodyJSON != "" {
		_ = json.Unmarshal([]byte(bodyJSON), &recs)
	}
	for i, r := range recs {
		if r.Type == "flow" && r.FlowID == flowID {
			recs[i].Note = note
			j, _ := json.Marshal(recs)
			return string(j)
		}
	}
	recs = append(recs, blockRecord{Type: "flow", FlowID: flowID, Note: note})
	j, _ := json.Marshal(recs)
	return string(j)
}

// removeFlowFromBody removes all flow blocks with the given flowID from the body JSON.
func removeFlowFromBody(bodyJSON string, flowID int64) string {
	if bodyJSON == "" {
		return ""
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(bodyJSON), &recs); err != nil {
		return bodyJSON
	}
	filtered := recs[:0]
	for _, r := range recs {
		if r.Type != "flow" || r.FlowID != flowID {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	j, _ := json.Marshal(filtered)
	return string(j)
}

// firstTextMD returns the markdown content of the first text block in the body JSON.
func firstTextMD(bodyJSON string) string {
	if bodyJSON == "" {
		return ""
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(bodyJSON), &recs); err != nil {
		return ""
	}
	for _, r := range recs {
		if r.Type == "text" && r.MD != "" {
			return r.MD
		}
	}
	return ""
}

// updateFirstTextInBody replaces the first text block's content in body JSON.
// If no text block exists, prepends one.
func updateFirstTextInBody(bodyJSON, md string) string {
	var recs []blockRecord
	if bodyJSON != "" {
		_ = json.Unmarshal([]byte(bodyJSON), &recs)
	}
	for i, r := range recs {
		if r.Type == "text" {
			recs[i].MD = md
			j, _ := json.Marshal(recs)
			return string(j)
		}
	}
	// No text block yet — prepend one.
	recs = append([]blockRecord{{Type: "text", MD: md}}, recs...)
	j, _ := json.Marshal(recs)
	return string(j)
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

// CreateFinding inserts a finding and sets f.ID/f.TS/f.UpdatedTS. Title is required.
// If Body is empty it is synthesized from Detail + Evidence so new findings are
// immediately in the interleaved-body format.
func (s *Store) CreateFinding(f *Finding) (int64, error) {
	now := time.Now().UnixMilli()
	f.TS, f.UpdatedTS = now, now
	f.Severity = normalizeFindingSeverity(f.Severity)
	f.Status = normalizeFindingStatus(f.Status)
	f.Source = normalizeFindingSource(f.Source)
	if f.Body == "" {
		f.Body = initialBody(f.Detail, f.Evidence)
	}
	res, err := s.db.Exec(
		`INSERT INTO findings (ts, updated_ts, severity, status, source, title, target, detail, evidence, fix, body)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		f.TS, f.UpdatedTS, f.Severity, f.Status, f.Source, f.Title, f.Target, f.Detail, f.Evidence, f.Fix, f.Body)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	f.ID = id
	return id, nil
}

// UpdateFinding applies non-nil fields and bumps updated_ts.
// body, when set, is stored as the new narrative body (already-serialized JSON).
// When detail is set but body is nil, the first text block in an existing body
// is updated (MCP backward-compat: AI updates detail → UI sees the change).
// When body is set, detail is synced from its first text block so MCP list_findings
// still shows meaningful text.
func (s *Store) UpdateFinding(id int64, severity, status, title, target, detail, evidence, fix, body *string) error {
	// If detail changes and there is an existing body, sync the first text block.
	if detail != nil && body == nil {
		var existBody string
		_ = s.db.QueryRow(`SELECT body FROM findings WHERE id=?`, id).Scan(&existBody)
		if existBody != "" {
			newBody := updateFirstTextInBody(existBody, *detail)
			body = &newBody
		}
	}
	// If body changes, sync its first text block back to detail for MCP compat.
	if body != nil && *body != "" && detail == nil {
		if md := firstTextMD(*body); md != "" {
			detail = &md
		}
	}

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
	if body != nil {
		sets = append(sets, "body=?")
		args = append(args, *body)
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

// AttachFlow records a flow as a PoC for a finding and appends (or updates) a
// flow block in the finding's narrative body. Idempotent on re-attach — updates
// the note in both tables and in the body block.
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

	// Sync flow block into the body.
	var bodyJSON string
	_ = tx.QueryRow(`SELECT body FROM findings WHERE id=?`, findingID).Scan(&bodyJSON)
	newBody := appendFlowToBody(bodyJSON, flowID, note)
	// Also update detail from first text block if needed.
	detailSync := firstTextMD(newBody)
	if _, err := tx.Exec(
		`UPDATE findings SET body=?, detail=CASE WHEN ?<>'' THEN ? ELSE detail END, updated_ts=? WHERE id=?`,
		newBody, detailSync, detailSync, time.Now().UnixMilli(), findingID); err != nil {
		return err
	}
	return tx.Commit()
}

// DetachFlow removes a PoC flow from a finding's flow table and body.
func (s *Store) DetachFlow(findingID, flowID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM finding_flows WHERE finding_id=? AND flow_id=?`, findingID, flowID); err != nil {
		return err
	}
	var bodyJSON string
	_ = tx.QueryRow(`SELECT body FROM findings WHERE id=?`, findingID).Scan(&bodyJSON)
	newBody := removeFlowFromBody(bodyJSON, flowID)
	if _, err := tx.Exec(`UPDATE findings SET body=?, updated_ts=? WHERE id=?`, newBody, time.Now().UnixMilli(), findingID); err != nil {
		return err
	}
	return tx.Commit()
}

// findingFlows loads the PoC flows for a finding (for the sidebar count and block enrichment).
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
		&f.Title, &f.Target, &f.Detail, &f.Evidence, &f.Fix, &f.Body); err != nil {
		return nil, err
	}
	return &f, nil
}

const findingCols = `id, ts, updated_ts, severity, status, source, title, target, detail, evidence, fix, body`

// GetFinding loads one finding with its narrative body blocks and PoC flow list.
func (s *Store) GetFinding(id int64) (*Finding, error) {
	f, err := scanFinding(s.db.QueryRow(`SELECT `+findingCols+` FROM findings WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if f.Flows, err = s.findingFlows(id); err != nil {
		return nil, err
	}
	f.Blocks = buildBlocks(f.Body, f.Detail, f.Evidence, f.Flows)
	return f, nil
}

// ListFindings returns findings ordered by severity (High→Info) then newest, each
// with its PoC flows and narrative blocks. Empty severity/status means "any".
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
		out[i].Blocks = buildBlocks(out[i].Body, out[i].Detail, out[i].Evidence, out[i].Flows)
	}
	return out, nil
}
