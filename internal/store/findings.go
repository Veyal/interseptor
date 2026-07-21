package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrFlowNotFound is returned by AttachFlow when the referenced flow id has no
// row in the flows table (typo, purged, or never captured).
var ErrFlowNotFound = errors.New("flow not found")

// NormalizeFindingBody coerces common agent mistakes (type md/markdown → text)
// and rejects unknown block types. Returns the normalized JSON body (or "" for
// empty input). Empty/invalid JSON that is not an array is rejected when non-empty.
func NormalizeFindingBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return "", fmt.Errorf("body must be a JSON array of blocks: %w", err)
	}
	for i := range recs {
		switch strings.ToLower(strings.TrimSpace(recs[i].Type)) {
		case "text":
			recs[i].Type = "text"
		case "md", "markdown":
			recs[i].Type = "text"
		case "flow":
			recs[i].Type = "flow"
		case "image":
			recs[i].Type = "image"
		default:
			return "", fmt.Errorf("body block[%d]: type must be text|flow|image, got %q", i, recs[i].Type)
		}
	}
	j, err := json.Marshal(recs)
	if err != nil {
		return "", err
	}
	return string(j), nil
}

// Finding is a curated vulnerability write-up for a project. Unlike a scanner
// Issue (auto-generated, ephemeral), a Finding is persistent and human/AI-curated:
// it carries a status the operator manages and has a narrative body — an ordered
// sequence of text blocks (markdown) and flow-reference blocks (clickable PoC
// request/response), freely interleaved.
type Finding struct {
	ID        int64  `json:"id"`
	TS        int64  `json:"ts"`        // created, unix millis
	UpdatedTS int64  `json:"updatedTs"` // last modified, unix millis
	Severity  string `json:"severity"`  // Critical | High | Medium | Low | Info
	Status    string `json:"status"`    // open | needs_verification | verified | false_positive | wont_fix | fixed
	Source    string `json:"source"`    // human | ai | scanner
	Title     string `json:"title"`
	Target    string `json:"target"`
	Detail    string `json:"detail"`         // legacy / MCP compat: first text block synced here
	Evidence  string `json:"evidence"`       // legacy only
	Fix       string `json:"fix"`            // back-compat: kept but superseded by Impact
	Impact    string `json:"impact"`         // security impact — what an attacker gains / business consequence
	Why       string `json:"why"`            // why this is a vulnerability (broken security property)
	Cwe       string `json:"cwe,omitempty"`  // CWE id or short class, e.g. CWE-639 / IDOR
	Environment string `json:"environment,omitempty"` // prod | staging | local | ""
	Cvss      string `json:"cvss,omitempty"` // CVSS score or vector string, e.g. "7.5" or "CVSS:3.1/AV:N/..."
	// VerificationInstructions tells a human reviewer exactly what to check when
	// Status is needs_verification (e.g. "download X and run file on it").
	VerificationInstructions string         `json:"verificationInstructions,omitempty"`
	Body                     string         `json:"body,omitempty"` // stored JSON blocks (use Blocks for rendering)
	Flows                    []FindingFlow  `json:"flows"`          // attached flow metadata (for list sidebar count)
	Blocks                   []FindingBlock `json:"blocks"`         // ordered narrative body (source of truth for UI)
	// Tags are report-scoping labels (same slug model as flow tags), e.g. cms / api / out-of-scope.
	Tags []string `json:"tags"`
	// Verification is the Autopilot/machine proof-record when present (not stored on the finding row).
	Verification *FindingVerification `json:"verification,omitempty"`
	// Ready / Missing are computed at read time (not stored) — report-ready checklist.
	Ready   bool     `json:"ready"`
	Missing []string `json:"missing,omitempty"`
}

// FindingBlock is one element in a finding's narrative body.
type FindingBlock struct {
	Type    string `json:"type"`              // "text", "flow", or "image"
	MD      string `json:"md,omitempty"`      // type=="text": markdown content
	FlowID  int64  `json:"flowId,omitempty"`  // type=="flow": attached flow
	Note    string `json:"note,omitempty"`    // type=="flow": annotation
	Hash    string `json:"hash,omitempty"`    // type=="image": content-addressed sha256
	Mime    string `json:"mime,omitempty"`    // type=="image": sanitized MIME
	Caption string `json:"caption,omitempty"` // type=="image": optional caption

	// Enriched at read time from the flows JOIN — never stored in the body JSON.
	Method string `json:"method,omitempty"`
	Host   string `json:"host,omitempty"`
	Path   string `json:"path,omitempty"`
	Status int    `json:"status,omitempty"`

	// ReqRaw / ResRaw are reconstructed HTTP messages for report export only
	// (same shape as GET /api/flows/{id}/raw). Never stored; omitted from list APIs.
	ReqRaw string `json:"reqRaw,omitempty"`
	ResRaw string `json:"resRaw,omitempty"`

	// URL is set at read time for image blocks (GET /api/findings/images/{hash}).
	URL string `json:"url,omitempty"`

	// Missing is set when referenced evidence is gone: a purged flow (type=="flow")
	// or a missing body blob (type=="image"). The block and annotation/caption are
	// preserved; the UI/report surface that the evidence is gone.
	Missing bool `json:"missing,omitempty"`
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

	// Missing is true when the referenced flow row no longer exists in the flows
	// table (purged via prune_history / GC). The attachment row and note survive.
	Missing bool `json:"missing,omitempty"`

	// ReqRaw / ResRaw are report-export enrichments (not stored).
	ReqRaw string `json:"reqRaw,omitempty"`
	ResRaw string `json:"resRaw,omitempty"`
}

// blockRecord is the minimal form written to the body column (no enriched metadata).
type blockRecord struct {
	Type    string `json:"type"`
	MD      string `json:"md,omitempty"`
	FlowID  int64  `json:"flowId,omitempty"`
	Note    string `json:"note,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Mime    string `json:"mime,omitempty"`
	Caption string `json:"caption,omitempty"`
}

// marshalBody serializes blocks for storage, stripping enriched metadata.
func marshalBody(blocks []FindingBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	recs := make([]blockRecord, len(blocks))
	for i, b := range blocks {
		recs[i] = blockRecord{
			Type: b.Type, MD: b.MD, FlowID: b.FlowID, Note: b.Note,
			Hash: b.Hash, Mime: b.Mime, Caption: b.Caption,
		}
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
				blocks[i] = FindingBlock{
					Type: r.Type, MD: r.MD, FlowID: r.FlowID, Note: r.Note,
					Hash: r.Hash, Mime: r.Mime, Caption: r.Caption,
				}
				if r.Type == "flow" {
					if fl, ok := flowMeta[r.FlowID]; ok {
						blocks[i].Method = fl.Method
						blocks[i].Host = fl.Host
						blocks[i].Path = fl.Path
						blocks[i].Status = fl.Status
						blocks[i].Missing = fl.Missing
					} else {
						// No attachment row for this flow id at all — the referenced
						// flow is gone (purged). Preserve the block; mark it missing.
						blocks[i].Missing = true
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
			Missing: fl.Missing,
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
	return insertFlowIntoBody(bodyJSON, flowID, note, -1)
}

// insertFlowIntoBody inserts a flow block at position pos (0-based block index)
// in the stored body JSON. pos < 0 or pos >= len means append at end.
// If the flow is already present, its note is updated in-place (position unchanged).
func insertFlowIntoBody(bodyJSON string, flowID int64, note string, pos int) string {
	var recs []blockRecord
	if bodyJSON != "" {
		_ = json.Unmarshal([]byte(bodyJSON), &recs)
	}
	// If already present, update the note in-place — don't change position.
	for i, r := range recs {
		if r.Type == "flow" && r.FlowID == flowID {
			recs[i].Note = note
			j, _ := json.Marshal(recs)
			return string(j)
		}
	}
	newBlock := blockRecord{Type: "flow", FlowID: flowID, Note: note}
	if pos < 0 || pos >= len(recs) {
		recs = append(recs, newBlock)
	} else {
		recs = append(recs, blockRecord{}) // grow by one
		copy(recs[pos+1:], recs[pos:])
		recs[pos] = newBlock
	}
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
	case "critical":
		return "Critical"
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
	case "needs_verification", "needs-verification", "needsverification":
		return "needs_verification"
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

func normalizeFindingEnvironment(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "prod", "production":
		return "prod"
	case "staging", "stage", "stg":
		return "staging"
	case "local", "dev", "development":
		return "local"
	default:
		return strings.TrimSpace(s)
	}
}

// EnrichCompleteness fills Ready/Missing and best-effort migrates Why from old
// "## Why this is a vulnerability" narrative when the why column is empty.
func (f *Finding) EnrichCompleteness() {
	if f == nil {
		return
	}
	if strings.TrimSpace(f.Why) == "" {
		if w := ExtractWhyFromNarrative(f.narrativeText()); w != "" {
			f.Why = w
		}
	}
	f.Missing = f.completenessGaps()
	f.Ready = len(f.Missing) == 0
}

func (f *Finding) narrativeText() string {
	var parts []string
	if d := strings.TrimSpace(f.Detail); d != "" {
		parts = append(parts, d)
	}
	for _, b := range f.Blocks {
		if b.Type == "text" && strings.TrimSpace(b.MD) != "" {
			parts = append(parts, b.MD)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (f *Finding) completenessGaps() []string {
	var miss []string
	if strings.TrimSpace(f.Title) == "" {
		miss = append(miss, "title")
	}
	if strings.TrimSpace(f.Impact) == "" {
		miss = append(miss, "impact")
	}
	if strings.TrimSpace(f.Why) == "" {
		miss = append(miss, "why")
	}
	if strings.TrimSpace(f.Target) == "" {
		miss = append(miss, "target")
	}
	flowN, imgN := 0, 0
	for _, b := range f.Blocks {
		switch b.Type {
		case "flow":
			if !b.Missing {
				flowN++
			}
		case "image":
			if !b.Missing && b.Hash != "" {
				imgN++
			}
		}
	}
	// Also count finding_flows if blocks empty of flows but Flows populated.
	if flowN == 0 {
		for _, fl := range f.Flows {
			if !fl.Missing {
				flowN++
			}
		}
	}
	if flowN+imgN == 0 {
		miss = append(miss, "poc")
	}
	sev := strings.ToLower(strings.TrimSpace(f.Severity))
	if (sev == "critical" || sev == "high") && flowN < 2 {
		miss = append(miss, "poc_before_after")
	}
	return miss
}

// ExtractWhyFromNarrative pulls the body of "## Why this is a vulnerability"
// from legacy markdown (best-effort migration).
func ExtractWhyFromNarrative(text string) string {
	re := regexp.MustCompile(`(?im)^##\s+Why this is a vulnerability\s*\n+([\s\S]*?)(?:\n##\s+|$)`)
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
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
	f.Environment = normalizeFindingEnvironment(f.Environment)
	if f.Body == "" {
		f.Body = initialBody(f.Detail, f.Evidence)
	}
	normBody, err := NormalizeFindingBody(f.Body)
	if err != nil {
		return 0, err
	}
	f.Body = normBody
	if f.Detail == "" && f.Body != "" {
		f.Detail = firstTextMD(f.Body)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`INSERT INTO findings (ts, updated_ts, severity, status, source, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verification_instructions)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.TS, f.UpdatedTS, f.Severity, f.Status, f.Source, f.Title, f.Target, f.Detail, f.Evidence, f.Fix, f.Body, f.Impact, f.Why, f.Cwe, f.Environment, f.Cvss, f.VerificationInstructions)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	f.ID = id
	if err := syncFindingFlowsFromBody(tx, id, f.Body); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if len(f.Tags) > 0 {
		norm, err := s.SetFindingTags(id, f.Tags)
		if err != nil {
			return 0, err
		}
		f.Tags = norm
	} else if f.Tags == nil {
		f.Tags = []string{}
	}
	return id, nil
}

// UpdateFinding applies non-nil fields and bumps updated_ts.
// body, when set, is stored as the new narrative body (already-serialized JSON).
// When detail is set but body is nil, the first text block in an existing body
// is updated (MCP backward-compat: AI updates detail → UI sees the change).
// When body is set, detail is synced from its first text block so MCP list_findings
// still shows meaningful text.
func (s *Store) UpdateFinding(id int64, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions *string) error {
	// If detail changes and there is an existing body, sync the first text block.
	if detail != nil && body == nil {
		var existBody string
		_ = s.db.QueryRow(`SELECT body FROM findings WHERE id=?`, id).Scan(&existBody)
		if existBody != "" {
			newBody := updateFirstTextInBody(existBody, *detail)
			body = &newBody
		}
	}
	// Normalize / coerce body before detail sync so type=md becomes text.
	if body != nil {
		norm, err := NormalizeFindingBody(*body)
		if err != nil {
			return err
		}
		*body = norm
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
	if impact != nil {
		sets = append(sets, "impact=?")
		args = append(args, *impact)
	}
	if why != nil {
		sets = append(sets, "why=?")
		args = append(args, *why)
	}
	if cwe != nil {
		sets = append(sets, "cwe=?")
		args = append(args, *cwe)
	}
	if environment != nil {
		sets = append(sets, "environment=?")
		args = append(args, normalizeFindingEnvironment(*environment))
	}
	if cvss != nil {
		sets = append(sets, "cvss=?")
		args = append(args, *cvss)
	}
	if verificationInstructions != nil {
		sets = append(sets, "verification_instructions=?")
		args = append(args, *verificationInstructions)
	}
	args = append(args, id)

	// Body rewrite must keep finding_flows in sync (UI enrichment joins that table).
	if body != nil {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE findings SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
			return err
		}
		if err := syncFindingFlowsFromBody(tx, id, *body); err != nil {
			return err
		}
		return tx.Commit()
	}

	_, err := s.db.Exec(`UPDATE findings SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...)
	return err
}

// syncFindingFlowsFromBody replaces finding_flows rows for a finding from the
// ordered type=flow blocks in body JSON. Unknown flow ids are rejected.
func syncFindingFlowsFromBody(tx *sql.Tx, findingID int64, body string) error {
	if _, err := tx.Exec(`DELETE FROM finding_flows WHERE finding_id=?`, findingID); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return fmt.Errorf("body must be a JSON array of blocks: %w", err)
	}
	ord := 0
	for _, r := range recs {
		if r.Type != "flow" {
			continue
		}
		if r.FlowID <= 0 {
			return fmt.Errorf("body flow block missing flowId")
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM flows WHERE id=?`, r.FlowID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("%w: %d", ErrFlowNotFound, r.FlowID)
		}
		if _, err := tx.Exec(
			`INSERT INTO finding_flows (finding_id, flow_id, ord, note) VALUES (?,?,?,?)`,
			findingID, r.FlowID, ord, r.Note,
		); err != nil {
			return err
		}
		ord++
	}
	return nil
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
	if _, err := tx.Exec(`DELETE FROM finding_tags WHERE finding_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM findings WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AttachFlow records a flow as a PoC for a finding and inserts (or updates) a
// flow block in the finding's narrative body. pos is the 0-based block index at
// which to insert the flow block; pass -1 to append at the end. Idempotent on
// re-attach — updates the note in both tables and in the body block (position
// unchanged if the block already exists).
//
// Returns ErrFlowNotFound when flowID has no row in flows — callers must not
// create orphan PoC attachments that later render as Missing.
func (s *Store) AttachFlow(findingID, flowID int64, note string, pos int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM flows WHERE id=?`, flowID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrFlowNotFound, flowID)
		}
		return err
	}

	var nextOrd int
	_ = tx.QueryRow(`SELECT COALESCE(MAX(ord)+1, 0) FROM finding_flows WHERE finding_id=?`, findingID).Scan(&nextOrd)
	if _, err := tx.Exec(
		`INSERT INTO finding_flows (finding_id, flow_id, ord, note) VALUES (?,?,?,?)
		 ON CONFLICT(finding_id, flow_id) DO UPDATE SET note=excluded.note`,
		findingID, flowID, nextOrd, note); err != nil {
		return err
	}

	// Sync flow block into the body at the requested position.
	var bodyJSON string
	_ = tx.QueryRow(`SELECT body FROM findings WHERE id=?`, findingID).Scan(&bodyJSON)
	newBody := insertFlowIntoBody(bodyJSON, flowID, note, pos)
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
		`SELECT ff.flow_id, ff.ord, ff.note, f.method, f.host, f.path, f.status, f.id IS NOT NULL
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
		var present bool
		if err := rows.Scan(&ff.FlowID, &ff.Ord, &ff.Note, &method, &host, &path, &status, &present); err != nil {
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
		// A LEFT JOIN miss (flow purged via prune_history / GC) yields a NULL flow
		// id; the attachment row and its note survive but the evidence is gone.
		ff.Missing = !present
		out = append(out, ff)
	}
	return out, rows.Err()
}

func scanFinding(sc scanner) (*Finding, error) {
	var f Finding
	if err := sc.Scan(&f.ID, &f.TS, &f.UpdatedTS, &f.Severity, &f.Status, &f.Source,
		&f.Title, &f.Target, &f.Detail, &f.Evidence, &f.Fix, &f.Body, &f.Impact, &f.Why, &f.Cwe, &f.Environment, &f.Cvss,
		&f.VerificationInstructions); err != nil {
		return nil, err
	}
	return &f, nil
}

const findingCols = `id, ts, updated_ts, severity, status, source, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verification_instructions`

// GetFinding loads one finding with its narrative body blocks and PoC flow list.
func (s *Store) GetFinding(id int64) (*Finding, error) {
	f, err := scanFinding(s.db.QueryRow(`SELECT `+findingCols+` FROM findings WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if f.Flows, err = s.findingFlows(id); err != nil {
		return nil, err
	}
	if f.Flows == nil {
		f.Flows = []FindingFlow{}
	}
	f.Blocks = buildBlocks(f.Body, f.Detail, f.Evidence, f.Flows)
	if f.Blocks == nil {
		f.Blocks = []FindingBlock{}
	}
	s.enrichImageBlocks(f.Blocks)
	if tags, err := s.FindingTags(id); err != nil {
		return nil, err
	} else {
		f.Tags = tags
	}
	if f.Tags == nil {
		f.Tags = []string{}
	}
	if v, err := s.GetFindingVerification(id); err == nil {
		f.Verification = v
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	f.EnrichCompleteness()
	return f, nil
}

// ListFindings returns findings ordered by severity (High→Info) then newest, each
// with its PoC flows and narrative blocks. Empty severity/status/tag means "any".
// When tag is set, only findings carrying that normalized tag are returned.
func (s *Store) ListFindings(severity, status, tag string) ([]Finding, error) {
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
	if t := normalizeTag(tag); t != "" {
		where = append(where, "EXISTS (SELECT 1 FROM finding_tags ft WHERE ft.finding_id = findings.id AND ft.tag = ?)")
		args = append(args, t)
	}
	rows, err := s.db.Query(
		`SELECT `+findingCols+` FROM findings WHERE `+strings.Join(where, " AND ")+
			` ORDER BY CASE severity WHEN 'Critical' THEN 0 WHEN 'High' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Low' THEN 3 ELSE 4 END, id DESC`,
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
	if len(out) > 0 {
		ids := make([]int64, len(out))
		for i := range out {
			ids[i] = out[i].ID
		}
		flowsByID, err := s.findingFlowsForIDs(ids)
		if err != nil {
			return nil, err
		}
		tagsByID, err := s.TagsForFindings(ids)
		if err != nil {
			return nil, err
		}
		verByID, err := s.VerificationsForFindings(ids)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].Flows = flowsByID[out[i].ID]
			if out[i].Flows == nil {
				out[i].Flows = []FindingFlow{}
			}
			out[i].Tags = tagsByID[out[i].ID]
			if out[i].Tags == nil {
				out[i].Tags = []string{}
			}
			if v := verByID[out[i].ID]; v != nil {
				out[i].Verification = v
			}
			out[i].Blocks = buildBlocks(out[i].Body, out[i].Detail, out[i].Evidence, out[i].Flows)
			if out[i].Blocks == nil {
				out[i].Blocks = []FindingBlock{}
			}
			s.enrichImageBlocks(out[i].Blocks)
			out[i].EnrichCompleteness()
		}
	}
	return out, nil
}

// findingFlowsForIDs batch-loads PoC flows for many findings in one query.
func (s *Store) findingFlowsForIDs(findingIDs []int64) (map[int64][]FindingFlow, error) {
	out := make(map[int64][]FindingFlow, len(findingIDs))
	if len(findingIDs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(findingIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(findingIDs))
	for i, id := range findingIDs {
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT ff.finding_id, ff.flow_id, ff.ord, ff.note, f.method, f.host, f.path, f.status, f.id IS NOT NULL
		 FROM finding_flows ff LEFT JOIN flows f ON f.id = ff.flow_id
		 WHERE ff.finding_id IN (`+placeholders+`) ORDER BY ff.finding_id, ff.ord, ff.flow_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var findingID int64
		var ff FindingFlow
		var method, host, path *string
		var status *int
		var present bool
		if err := rows.Scan(&findingID, &ff.FlowID, &ff.Ord, &ff.Note, &method, &host, &path, &status, &present); err != nil {
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
		ff.Missing = !present
		out[findingID] = append(out[findingID], ff)
	}
	return out, rows.Err()
}
