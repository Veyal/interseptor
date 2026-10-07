package store

import (
	"encoding/json"
	"fmt"
)

func jsonValidString(s string) bool { return json.Valid([]byte(s)) }

// CollRun is a runner execution header (ix_runs placeholder; WP10 fills in
// semantics). SummaryJSON is opaque.
type CollRun struct {
	UID           string `json:"uid"`
	CollectionUID string `json:"collectionUid"`
	EnvUID        string `json:"envUid,omitempty"`
	Source        string `json:"source,omitempty"` // ui | cli | mcp
	Status        string `json:"status,omitempty"`
	StartedTS     int64  `json:"startedTs"`
	FinishedTS    int64  `json:"finishedTs,omitempty"`
	SummaryJSON   string `json:"summaryJson,omitempty"`
}

// CollRunResult is one executed request of a run.
type CollRunResult struct {
	ID         int64  `json:"id"`
	RunUID     string `json:"runUid"`
	ItemUID    string `json:"itemUid"`
	Iteration  int    `json:"iteration"`
	FlowID     int64  `json:"flowId,omitempty"`
	Status     string `json:"status,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	ResultJSON string `json:"resultJson,omitempty"`
}

// FlowCtx links a captured flow to the collection execution that sent it.
// Secret values never belong in these fields; the unresolved template is
// stored by hash only.
type FlowCtx struct {
	FlowID       int64  `json:"flowId"`
	RunID        string `json:"runId,omitempty"`
	ItemUID      string `json:"itemUid,omitempty"`
	Iteration    int    `json:"iteration,omitempty"`
	Phase        string `json:"phase,omitempty"`
	ParentFlowID int64  `json:"parentFlowId,omitempty"`
	EnvUID       string `json:"envUid,omitempty"`
	Identity     string `json:"identity,omitempty"`
	TemplateHash string `json:"templateHash,omitempty"`
}

// PutRun upserts a run header.
func (s *Store) PutRun(r CollRun) (*CollRun, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	if r.UID == "" {
		r.UID = NewUID()
	}
	if r.StartedTS == 0 {
		r.StartedTS = nowMs()
	}
	if r.SummaryJSON != "" && !jsonValidString(r.SummaryJSON) {
		return nil, fmt.Errorf("%w: run summary is not valid JSON", ErrCollInvalid)
	}
	_, err := s.db.Exec(`INSERT INTO ix_runs(uid,collection_uid,env_uid,source,status,started_ts,finished_ts,summary_json) VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(uid) DO UPDATE SET collection_uid=excluded.collection_uid,env_uid=excluded.env_uid,source=excluded.source,status=excluded.status,started_ts=excluded.started_ts,finished_ts=excluded.finished_ts,summary_json=excluded.summary_json`,
		r.UID, r.CollectionUID, r.EnvUID, r.Source, r.Status, r.StartedTS, r.FinishedTS, r.SummaryJSON)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns a collection's runs newest first (limit<=0 means 100).
func (s *Store) ListRuns(collectionUID string, limit int) ([]CollRun, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT uid,collection_uid,env_uid,source,status,started_ts,finished_ts,summary_json FROM ix_runs WHERE collection_uid=? ORDER BY started_ts DESC, uid DESC LIMIT ?`, collectionUID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollRun{}
	for rows.Next() {
		var r CollRun
		if err := rows.Scan(&r.UID, &r.CollectionUID, &r.EnvUID, &r.Source, &r.Status, &r.StartedTS, &r.FinishedTS, &r.SummaryJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddRunResult appends one result row to a run.
func (s *Store) AddRunResult(r CollRunResult) (int64, error) {
	if err := s.ensureCollections(); err != nil {
		return 0, err
	}
	if r.RunUID == "" {
		return 0, fmt.Errorf("%w: run uid required", ErrCollInvalid)
	}
	if r.ResultJSON != "" && !jsonValidString(r.ResultJSON) {
		return 0, fmt.Errorf("%w: result is not valid JSON", ErrCollInvalid)
	}
	res, err := s.db.Exec(`INSERT INTO ix_run_results(run_uid,item_uid,iteration,flow_id,status,duration_ms,result_json) VALUES(?,?,?,?,?,?,?)`,
		r.RunUID, r.ItemUID, r.Iteration, r.FlowID, r.Status, r.DurationMs, r.ResultJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListRunResults returns a run's results in execution order.
func (s *Store) ListRunResults(runUID string) ([]CollRunResult, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,run_uid,item_uid,iteration,flow_id,status,duration_ms,result_json FROM ix_run_results WHERE run_uid=? ORDER BY id`, runUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollRunResult{}
	for rows.Next() {
		var r CollRunResult
		if err := rows.Scan(&r.ID, &r.RunUID, &r.ItemUID, &r.Iteration, &r.FlowID, &r.Status, &r.DurationMs, &r.ResultJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutFlowCtx upserts the collection context of a flow.
func (s *Store) PutFlowCtx(c FlowCtx) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	if c.FlowID <= 0 {
		return fmt.Errorf("%w: flow id required", ErrCollInvalid)
	}
	_, err := s.db.Exec(`INSERT INTO ix_flow_ctx(flow_id,run_id,item_uid,iteration,phase,parent_flow_id,env_uid,identity,template_hash) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(flow_id) DO UPDATE SET run_id=excluded.run_id,item_uid=excluded.item_uid,iteration=excluded.iteration,phase=excluded.phase,parent_flow_id=excluded.parent_flow_id,env_uid=excluded.env_uid,identity=excluded.identity,template_hash=excluded.template_hash`,
		c.FlowID, c.RunID, c.ItemUID, c.Iteration, c.Phase, c.ParentFlowID, c.EnvUID, c.Identity, c.TemplateHash)
	return err
}

// GetFlowCtx returns a flow's collection context, ok=false if none.
func (s *Store) GetFlowCtx(flowID int64) (FlowCtx, bool, error) {
	var c FlowCtx
	if err := s.ensureCollections(); err != nil {
		return c, false, err
	}
	rows, err := s.db.Query(`SELECT flow_id,run_id,item_uid,iteration,phase,parent_flow_id,env_uid,identity,template_hash FROM ix_flow_ctx WHERE flow_id=?`, flowID)
	if err != nil {
		return c, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return c, false, rows.Err()
	}
	err = rows.Scan(&c.FlowID, &c.RunID, &c.ItemUID, &c.Iteration, &c.Phase, &c.ParentFlowID, &c.EnvUID, &c.Identity, &c.TemplateHash)
	return c, err == nil, err
}

// ---- script trust ----

// TrustScript records that scriptHash (sha256 of exact source + libs + caps)
// is trusted for a collection. Callers must only invoke this from an
// interactive UI session; AI/MCP/archive paths must never reach it.
func (s *Store) TrustScript(collectionUID, scriptHash string) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	if collectionUID == "" || scriptHash == "" {
		return fmt.Errorf("%w: collection and hash required", ErrCollInvalid)
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO ix_script_trust(collection_uid,script_hash,ts) VALUES(?,?,?)`, collectionUID, scriptHash, nowMs())
	return err
}

// IsScriptTrusted reports whether the exact script hash is trusted.
func (s *Store) IsScriptTrusted(collectionUID, scriptHash string) (bool, error) {
	if err := s.ensureCollections(); err != nil {
		return false, err
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM ix_script_trust WHERE collection_uid=? AND script_hash=?`, collectionUID, scriptHash).Scan(&n)
	return n > 0, err
}

// RevokeScriptTrust removes one trusted hash, or all of a collection's when
// scriptHash is empty.
func (s *Store) RevokeScriptTrust(collectionUID, scriptHash string) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	if scriptHash == "" {
		_, err := s.db.Exec(`DELETE FROM ix_script_trust WHERE collection_uid=?`, collectionUID)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM ix_script_trust WHERE collection_uid=? AND script_hash=?`, collectionUID, scriptHash)
	return err
}

// AddImportLog records an import report for a collection.
func (s *Store) AddImportLog(collectionUID, source, reportJSON string) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	if reportJSON != "" && !jsonValidString(reportJSON) {
		return fmt.Errorf("%w: report is not valid JSON", ErrCollInvalid)
	}
	_, err := s.db.Exec(`INSERT INTO ix_import_log(collection_uid,source,ts,report_json) VALUES(?,?,?,?)`, collectionUID, source, nowMs(), reportJSON)
	return err
}
