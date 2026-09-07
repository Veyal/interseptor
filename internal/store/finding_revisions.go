package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// FindingChange identifies the writing boundary, not a verified human identity.
// The caller may supply a reason; transports set their own actor/source labels.
type FindingChange struct {
	ImageIngestion string `json:"-"`
	Actor          string `json:"actor"`
	Source         string `json:"source"`
	Reason         string `json:"reason,omitempty"`
}
type FindingRevision struct {
	ID        int64 `json:"id"`
	FindingID int64 `json:"findingId"`
	TS        int64 `json:"ts"`
	FindingChange
	Action   string   `json:"action"`
	Fields   []string `json:"fields"`
	Snapshot *Finding `json:"snapshot,omitempty"`
}

type FindingRevisionDiff struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

func (s *Store) ensureFindingRevisions() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS finding_image_provenance(hash TEXT PRIMARY KEY,ingestion TEXT NOT NULL,original_source TEXT NOT NULL,ingested_ts INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS finding_revisions (
 id INTEGER PRIMARY KEY AUTOINCREMENT, finding_id INTEGER NOT NULL, ts INTEGER NOT NULL,
 actor TEXT NOT NULL, source TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', action TEXT NOT NULL,
 fields TEXT NOT NULL, snapshot TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_finding_revisions_finding ON finding_revisions(finding_id,id DESC);
 CREATE TRIGGER IF NOT EXISTS finding_revisions_no_update BEFORE UPDATE ON finding_revisions BEGIN SELECT RAISE(ABORT,'finding revisions are immutable'); END;
 CREATE TRIGGER IF NOT EXISTS finding_revisions_no_delete BEFORE DELETE ON finding_revisions BEGIN SELECT RAISE(ABORT,'finding revisions are immutable'); END;`)
	return err
}

func findingSnapshot(tx *sql.Tx, id int64) (*Finding, error) {
	f, err := scanFinding(tx.QueryRow(`SELECT `+findingCols+` FROM findings WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT flow_id,ord,note FROM finding_flows WHERE finding_id=? ORDER BY ord,flow_id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v FindingFlow
		if err = rows.Scan(&v.FlowID, &v.Ord, &v.Note); err != nil {
			rows.Close()
			return nil, err
		}
		f.Flows = append(f.Flows, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(`SELECT tag FROM finding_tags WHERE finding_id=? ORDER BY tag`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tag string
		if err = rows.Scan(&tag); err != nil {
			rows.Close()
			return nil, err
		}
		f.Tags = append(f.Tags, tag)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var v FindingVerification
	err = tx.QueryRow(`SELECT `+findingVerificationCols+` FROM finding_verification WHERE finding_id=?`, id).Scan(&v.ID, &v.FindingID, &v.RunID, &v.VulnClass, &v.Gates, &v.ReproCount, &v.OOBToken, &v.BaselineFlow, &v.PayloadFlow, &v.Confidence, &v.TS)
	if err == nil {
		f.Verification = &v
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return f, nil
}

func findingRevisionFields(before, after *Finding) []FindingRevisionDiff {
	convert := func(f *Finding) map[string]any {
		out := map[string]any{}
		if f != nil {
			b, _ := json.Marshal(f)
			_ = json.Unmarshal(b, &out)
		}
		for _, k := range []string{"id", "ts", "updatedTs", "ready", "missing", "readiness", "cvssScore", "cvssRating", "cvssNomenclature"} {
			delete(out, k)
		}
		return out
	}
	a, b := convert(before), convert(after)
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	out := []FindingRevisionDiff{}
	for k := range keys {
		if !reflect.DeepEqual(a[k], b[k]) {
			out = append(out, FindingRevisionDiff{k, a[k], b[k]})
		}
	}
	slices.SortFunc(out, func(a, b FindingRevisionDiff) int { return strings.Compare(a.Field, b.Field) })
	return out
}

func revisionBefore(tx *sql.Tx, id int64) error {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM finding_revisions WHERE finding_id=?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return appendFindingRevision(tx, id, "baseline", FindingChange{Source: "migration"})
}
func appendFindingRevision(tx *sql.Tx, id int64, action string, change FindingChange) error {
	if len(change.Reason) > 2048 {
		return fmt.Errorf("%w: revision reason exceeds 2048 bytes", ErrInvalidFinding)
	}
	f, err := findingSnapshot(tx, id)
	if err != nil {
		return err
	}
	var previous Finding
	var raw string
	err = tx.QueryRow(`SELECT snapshot FROM finding_revisions WHERE finding_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&raw)
	var before *Finding
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return err
		}
		before = &previous
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	diff := findingRevisionFields(before, f)
	fields := []string{}
	for _, d := range diff {
		fields = append(fields, d.Field)
	}
	if len(fields) == 0 && action == "update" {
		return nil
	}
	if action == "delete" || action == "restore" {
		fields = append(fields, "lifecycle")
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	fieldData, _ := json.Marshal(fields)
	if change.Actor == "" {
		change.Actor = "local writer"
	}
	if change.Source == "" {
		change.Source = "store"
	}
	_, err = tx.Exec(`INSERT INTO finding_revisions(finding_id,ts,actor,source,reason,action,fields,snapshot) VALUES(?,?,?,?,?,?,?,?)`, id, time.Now().UnixMilli(), change.Actor, change.Source, change.Reason, action, string(fieldData), string(data))
	return err
}
func firstFindingChange(changes []FindingChange) FindingChange {
	if len(changes) > 0 {
		return changes[0]
	}
	return FindingChange{}
}

func (s *Store) ListFindingRevisions(id int64, limit int) ([]FindingRevision, error) {
	return s.ListFindingRevisionsPage(id, 0, limit)
}
func (s *Store) ListFindingRevisionsPage(id, before int64, limit int) ([]FindingRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,finding_id,ts,actor,source,reason,action,fields FROM finding_revisions WHERE finding_id=? AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, id, before, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingRevision{}
	for rows.Next() {
		var v FindingRevision
		var fields string
		if err = rows.Scan(&v.ID, &v.FindingID, &v.TS, &v.Actor, &v.Source, &v.Reason, &v.Action, &fields); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(fields), &v.Fields); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) GetFindingRevision(id, revisionID int64) (*FindingRevision, []FindingRevisionDiff, error) {
	var v FindingRevision
	var fields, data string
	err := s.db.QueryRow(`SELECT id,finding_id,ts,actor,source,reason,action,fields,snapshot FROM finding_revisions WHERE finding_id=? AND id=?`, id, revisionID).Scan(&v.ID, &v.FindingID, &v.TS, &v.Actor, &v.Source, &v.Reason, &v.Action, &fields, &data)
	if err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal([]byte(fields), &v.Fields); err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal([]byte(data), &v.Snapshot); err != nil {
		return nil, nil, err
	}
	var before *Finding
	err = s.db.QueryRow(`SELECT snapshot FROM finding_revisions WHERE finding_id=? AND id<? ORDER BY id DESC LIMIT 1`, id, revisionID).Scan(&data)
	if err == nil {
		if err = json.Unmarshal([]byte(data), &before); err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	return &v, findingRevisionFields(before, v.Snapshot), nil
}
func (s *Store) DeletedFindings(limit int) ([]FindingRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT r.id,r.finding_id,r.ts,r.actor,r.source,r.reason,r.action,r.fields,json_extract(r.snapshot,'$.title') FROM finding_revisions r WHERE r.action='delete' AND NOT EXISTS(SELECT 1 FROM findings f WHERE f.id=r.finding_id) AND r.id=(SELECT MAX(id) FROM finding_revisions WHERE finding_id=r.finding_id) ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingRevision{}
	for rows.Next() {
		var v FindingRevision
		var fields, title string
		if err = rows.Scan(&v.ID, &v.FindingID, &v.TS, &v.Actor, &v.Source, &v.Reason, &v.Action, &fields, &title); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(fields), &v.Fields)
		v.Snapshot = &Finding{ID: v.FindingID, Title: title}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RestoreFindingRevision restores report content and retained evidence references.
// Independently purged flows remain explicitly missing; a revision is not a backup of raw traffic.
func (s *Store) RestoreFindingRevision(id, revisionID int64, change FindingChange) error {
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data string
	if err = tx.QueryRow(`SELECT snapshot FROM finding_revisions WHERE finding_id=? AND id=?`, id, revisionID).Scan(&data); err != nil {
		return err
	}
	var f Finding
	if err = json.Unmarshal([]byte(data), &f); err != nil {
		return err
	}
	// Legacy snapshots are restored without silently changing their historical claims.
	if err = markMissingFindingReferences(tx, &f); err != nil {
		return err
	}
	var records []blockRecord
	_ = json.Unmarshal([]byte(f.Body), &records)
	seen := map[int64]bool{}
	for _, record := range records {
		if record.Type == "flow" {
			seen[record.FlowID] = true
		}
	}
	if len(records) == 0 {
		_ = json.Unmarshal([]byte(initialBody(f.Detail, f.Evidence)), &records)
	}
	for _, flow := range f.Flows {
		if !seen[flow.FlowID] {
			records = append(records, blockRecord{Type: "flow", FlowID: flow.FlowID, Note: flow.Note, Role: findingBlockRoleFromNote(flow.Note), Missing: flow.Missing})
			seen[flow.FlowID] = true
		}
	}

	for i := range records {
		if records[i].Type == "flow" {
			var exists int
			if err = tx.QueryRow(`SELECT count(*) FROM flows WHERE id=?`, records[i].FlowID).Scan(&exists); err != nil {
				return err
			}
			records[i].Missing = records[i].Missing || exists == 0
		}
	}
	normalized, err := json.Marshal(records)
	if err != nil {
		return err
	}
	f.Body = string(normalized)
	_, err = tx.Exec(`INSERT INTO findings (`+findingCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET updated_ts=excluded.updated_ts,severity=excluded.severity,status=excluded.status,source=excluded.source,title=excluded.title,summary=excluded.summary,target=excluded.target,confidence=excluded.confidence,detail=excluded.detail,evidence=excluded.evidence,fix=excluded.fix,body=excluded.body,impact=excluded.impact,why=excluded.why,cwe=excluded.cwe,environment=excluded.environment,cvss=excluded.cvss,verification_instructions=excluded.verification_instructions,retest=excluded.retest,targets=excluded.targets,proof_review=excluded.proof_review`, id, f.TS, time.Now().UnixMilli(), f.Severity, f.Status, f.Source, f.Title, f.Summary, f.Target, f.Confidence, f.Detail, f.Evidence, f.Fix, f.Body, f.Impact, f.Why, f.Cwe, f.Environment, f.Cvss, f.VerificationInstructions, f.Retest, f.Targets, f.ProofReview)
	if err != nil {
		return err
	}
	if err = syncFindingFlowsFromBody(tx, id, f.Body); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM finding_tags WHERE finding_id=?`, id); err != nil {
		return err
	}
	for _, tag := range f.Tags {
		if _, err = tx.Exec(`INSERT INTO finding_tags(finding_id,tag) VALUES(?,?)`, id, tag); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM finding_verification WHERE finding_id=?`, id); err != nil {
		return err
	}
	if v := f.Verification; v != nil {
		_, err = tx.Exec(`INSERT INTO finding_verification(finding_id,run_id,vuln_class,gates,repro_count,oob_token,baseline_flow,payload_flow,confidence,ts) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, v.RunID, v.VulnClass, v.Gates, v.ReproCount, v.OOBToken, v.BaselineFlow, v.PayloadFlow, v.Confidence, v.TS)
		if err != nil {
			return err
		}
	}
	if err = appendFindingRevision(tx, id, "restore", change); err != nil {
		return err
	}
	return tx.Commit()
}
