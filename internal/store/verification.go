package store

// FindingVerification is the machine proof-record behind a `verified` Finding: the
// concrete evidence the 4-gate verifier produced, distinguishing a machine-proven
// finding from an operator's hand-set `verified` status. It is 1:1 with a Finding
// (keyed by finding_id — one proof-record per finding).
type FindingVerification struct {
	ID           int64  `json:"id"`
	FindingID    int64  `json:"findingId"`    // FK findings; also reachable from the run
	RunID        int64  `json:"runId"`        // FK pentest_run
	VulnClass    string `json:"vulnClass"`    // e.g. sqli-boolean, ssrf-blind, xss-reflected
	Gates        string `json:"gates"`        // JSON: which gates ran and passed
	ReproCount   int    `json:"reproCount"`   // how many times the differential held
	OOBToken     string `json:"oobToken"`     // non-empty for blind classes proven via callback
	BaselineFlow int64  `json:"baselineFlow"` // PoC flow ids
	PayloadFlow  int64  `json:"payloadFlow"`
	Confidence   int    `json:"confidence"` // 0-100 derived from which gates passed
	TS           int64  `json:"ts"`         // unix millis
}

const findingVerificationCols = `id, finding_id, run_id, vuln_class, gates, repro_count, oob_token, baseline_flow, payload_flow, confidence, ts`

// SaveFindingVerification upserts the proof-record for a finding, keyed by
// finding_id (one proof-record per finding). On conflict it replaces every proof
// field so re-verifying a finding overwrites the prior record in place; v.ID is
// set to the resulting row id.
func (s *Store) SaveFindingVerification(v *FindingVerification) (int64, error) {
	_, err := s.db.Exec(
		`INSERT INTO finding_verification
		   (finding_id, run_id, vuln_class, gates, repro_count, oob_token, baseline_flow, payload_flow, confidence, ts)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(finding_id) DO UPDATE SET
		   run_id=excluded.run_id,
		   vuln_class=excluded.vuln_class,
		   gates=excluded.gates,
		   repro_count=excluded.repro_count,
		   oob_token=excluded.oob_token,
		   baseline_flow=excluded.baseline_flow,
		   payload_flow=excluded.payload_flow,
		   confidence=excluded.confidence,
		   ts=excluded.ts`,
		v.FindingID, v.RunID, v.VulnClass, v.Gates, v.ReproCount, v.OOBToken,
		v.BaselineFlow, v.PayloadFlow, v.Confidence, v.TS)
	if err != nil {
		return 0, err
	}
	// LastInsertId is unreliable across an upsert that took the UPDATE branch, so
	// read the row's id back by its unique finding_id.
	if err := s.db.QueryRow(`SELECT id FROM finding_verification WHERE finding_id=?`, v.FindingID).Scan(&v.ID); err != nil {
		return 0, err
	}
	return v.ID, nil
}

// GetFindingVerification loads the proof-record for a finding, or sql.ErrNoRows if
// the finding has no machine verification.
func (s *Store) GetFindingVerification(findingID int64) (*FindingVerification, error) {
	var v FindingVerification
	if err := s.db.QueryRow(
		`SELECT `+findingVerificationCols+` FROM finding_verification WHERE finding_id=?`, findingID,
	).Scan(&v.ID, &v.FindingID, &v.RunID, &v.VulnClass, &v.Gates, &v.ReproCount,
		&v.OOBToken, &v.BaselineFlow, &v.PayloadFlow, &v.Confidence, &v.TS); err != nil {
		return nil, err
	}
	return &v, nil
}
