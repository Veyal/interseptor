package store

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	cvsspkg "github.com/Veyal/interseptor/internal/cvss"
	cvss31 "github.com/pandatix/go-cvss/31"
	cvss40 "github.com/pandatix/go-cvss/40"
)

// FindingTargets is ordered: the first entry is the compact primary target.
type FindingTargets []FindingTarget

type FindingTarget struct {
	URL               string   `json:"url"`
	Methods           []string `json:"methods,omitempty"`
	Method            string   `json:"method,omitempty"` // accepted single-method input alias
	Variant           string   `json:"variant,omitempty"`
	Role              string   `json:"role,omitempty"`
	Relation          string   `json:"relation,omitempty"`
	Note              string   `json:"note,omitempty"`
	FlowIDs           []int64  `json:"flow_ids,omitempty"`
	ImageHashes       []string `json:"image_hashes,omitempty"`
	EvidenceException string   `json:"evidenceException,omitempty"`
	// Persist missing peer IDs so an unrelated local flow can never satisfy them.
	MissingFlowIDs []int64 `json:"missingFlowIds,omitempty"`
}

// ProofReview records the operator's assessment, not an automated proof claim.
type FindingProofReview struct {
	Claims    map[string]FindingCapabilityClaim `json:"claims,omitempty"`
	Execution string                            `json:"execution,omitempty"` // demonstrated | prerequisite_only | not_executed
	Reason    string                            `json:"reason,omitempty"`
	Visual    bool                              `json:"visual,omitempty"`
	// SeverityOverride is the documented reason a finding's severity
	// deliberately differs from its calculated CVSS rating. Without it a
	// mismatch is rejected at write time.
	SeverityOverride string                              `json:"severityOverride,omitempty"`
	Evidence         map[string]FindingEvidenceReference `json:"evidence,omitempty"`
}

type FindingEvidenceReference struct {
	FlowID  int64  `json:"flowId,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

type FindingMetadataPatch struct {
	Change      FindingChange
	Targets     *FindingTargets
	ProofReview *FindingProofReview
}

func scanFindingJSON(value any, out any) error {
	var raw []byte
	switch v := value.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	case nil:
		return nil
	default:
		return fmt.Errorf("invalid finding metadata type %T", value)
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
func (t *FindingTargets) Scan(value any) error        { return scanFindingJSON(value, t) }
func (t FindingTargets) Value() (driver.Value, error) { b, e := json.Marshal(t); return string(b), e }
func (r *FindingProofReview) Scan(value any) error    { return scanFindingJSON(value, r) }
func (r FindingProofReview) Value() (driver.Value, error) {
	b, e := json.Marshal(r)
	return string(b), e
}

func normalizeFindingAssessment(f *Finding) error {
	if err := normalizeCapabilityClaims(f); err != nil {
		return err
	}
	if len(f.Targets) > 64 {
		return fmt.Errorf("%w: at most 64 affected targets", ErrInvalidFinding)
	}
	for i := range f.Targets {
		t := &f.Targets[i]
		t.URL = strings.TrimSpace(t.URL)
		if t.URL == "" || len(t.URL) > 4096 {
			return fmt.Errorf("%w: target %d requires a URL or app identifier (max 4096 bytes)", ErrInvalidFinding, i+1)
		}
		if t.Method != "" {
			t.Methods = append([]string{t.Method}, t.Methods...)
			t.Method = ""
		}
		methods := []string{}
		for _, m := range t.Methods {
			m = strings.ToUpper(strings.TrimSpace(m))
			if m == "" {
				continue
			}
			if len(m) > 32 || strings.ContainsAny(m, " \t\r\n") {
				return fmt.Errorf("%w: target %d has an invalid method", ErrInvalidFinding, i+1)
			}
			if !slices.Contains(methods, m) {
				methods = append(methods, m)
			}
		}
		if len(methods) > 16 || len(t.FlowIDs) > 128 || len(t.MissingFlowIDs) > 128 || len(t.ImageHashes) > 128 {
			return fmt.Errorf("%w: target %d has too many methods or evidence references", ErrInvalidFinding, i+1)
		}
		for _, value := range []string{t.Role, t.Variant, t.Relation, t.Note, t.EvidenceException} {
			if len(value) > 8192 {
				return fmt.Errorf("%w: target context exceeds 8192 bytes", ErrInvalidFinding)
			}
		}
		for _, hash := range t.ImageHashes {
			if !isContentHash(hash) {
				return fmt.Errorf("%w: target image hash is invalid", ErrInvalidFinding)
			}
		}
		t.Methods = methods
		ids := []int64{}
		for _, id := range t.FlowIDs {
			if id <= 0 {
				return fmt.Errorf("%w: target flow IDs must be positive", ErrInvalidFinding)
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		t.FlowIDs = ids
		for _, id := range t.MissingFlowIDs {
			if id <= 0 || !slices.Contains(t.FlowIDs, id) {
				return fmt.Errorf("%w: missing target references must belong to flow_ids", ErrInvalidFinding)
			}
		}
		t.Relation = strings.ToLower(strings.TrimSpace(t.Relation))
		if t.Relation == "" {
			t.Relation = "affected"
		}
		if t.EvidenceException != "" && t.Relation != "setup" && t.Relation != "chain" {
			return fmt.Errorf("%w: evidence exceptions apply only to setup or chain targets", ErrInvalidFinding)
		}
	}
	if len(f.Targets) > 0 {
		f.Target = f.Targets[0].URL
	}
	if err := validateEvidenceMapping(f.ProofReview.Evidence); err != nil {
		return err
	}
	r := &f.ProofReview
	r.Execution = strings.TrimSpace(r.Execution)
	r.Reason = strings.TrimSpace(r.Reason)
	switch r.Execution {
	case "", "demonstrated":
	case "prerequisite_only", "not_executed":
		if r.Reason == "" {
			return fmt.Errorf("%w: explain why claimed impact has not been demonstrated", ErrInvalidFinding)
		}
		f.Status = "needs_verification"
	default:
		return fmt.Errorf("%w: execution must be demonstrated, prerequisite_only, or not_executed", ErrInvalidFinding)
	}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(f.Cvss)), "CVSS:3.1") {
		if _, err := cvss31.ParseVector(strings.TrimSpace(f.Cvss)); err != nil {
			return fmt.Errorf("%w: invalid CVSS v3.1 vector: %v", ErrInvalidFinding, err)
		}
	} else if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(f.Cvss)), "CVSS:4.0") {
		if _, err := cvss40.ParseVector(strings.TrimSpace(f.Cvss)); err != nil {
			return fmt.Errorf("%w: invalid CVSS v4.0 vector: %v", ErrInvalidFinding, err)
		}
	}
	return nil
}

// validateEvidenceMapping checks proofReview.evidence and names the exact role
// and the part (role, flowId or hash) that is wrong.
func validateEvidenceMapping(evidence map[string]FindingEvidenceReference) error {
	roles := make([]string, 0, len(evidence))
	for role := range evidence {
		roles = append(roles, role)
	}
	slices.Sort(roles)
	for _, role := range roles {
		ref := evidence[role]
		field := "proofReview.evidence." + role
		switch {
		case !slices.Contains([]string{"action", "result", "control"}, role):
			return fmt.Errorf("%w: %s: role must be one of action, result, control", ErrInvalidFinding, field)
		case ref.FlowID > 0 && ref.Hash != "":
			return fmt.Errorf("%w: %s: give either flowId or hash, not both", ErrInvalidFinding, field)
		case ref.FlowID <= 0 && ref.Hash == "":
			return fmt.Errorf("%w: %s: both flowId and hash are empty; give flowId (a positive captured flow id) or hash (64 hex characters of an uploaded image)", ErrInvalidFinding, field)
		case ref.FlowID <= 0 && !isContentHash(ref.Hash):
			return fmt.Errorf("%w: %s.hash: expected 64 hex characters of an uploaded image hash, got %q", ErrInvalidFinding, field, truncateForError(ref.Hash))
		}
	}
	return nil
}

func truncateForError(s string) string {
	if len(s) > 24 {
		return s[:24] + "..."
	}
	return s
}

// validateCVSSWrite enforces the CVSS:4.0 contract for a vector being written.
func validateCVSSWrite(vector string) error {
	if err := cvsspkg.ValidateForWrite(vector); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFinding, err)
	}
	return nil
}

// validateSeverityMatchesCVSS blocks a severity that disagrees with the
// calculated rating of a valid CVSS v4.0 vector unless the finding documents an
// explicit override. Unparseable or legacy vectors are not judged here.
func validateSeverityMatchesCVSS(f *Finding) error {
	if strings.TrimSpace(f.ProofReview.SeverityOverride) != "" {
		return nil
	}
	ev, err := cvsspkg.Evaluate(f.Cvss)
	if err != nil || ev.Legacy || strings.EqualFold(f.Severity, ev.Severity) {
		return nil
	}
	return fmt.Errorf("%w: severity %q conflicts with the calculated CVSS rating %s (score %.1f); set severity to %s or document a deliberate difference in proofReview.severityOverride", ErrInvalidFinding, f.Severity, ev.Severity, ev.Score, ev.Severity)
}

func (s *Store) UpdateFindingMetadata(id int64, patch FindingMetadataPatch) error {
	return s.updateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, patch)
}

func (f *Finding) enrichAssessment() {
	f.CvssScore = nil
	f.CvssRating = ""
	f.CvssNomenclature = ""
	f.CvssWarning = ""
	raw := strings.TrimSpace(f.Cvss)
	if cvsspkg.IsLegacy(raw) {
		f.CvssWarning = "legacy CVSS 3.1 vector kept as-is; the finding contract requires CVSS:4.0 — re-score it with evaluate_finding_cvss"
	}
	if strings.HasPrefix(strings.ToUpper(raw), "CVSS:3.1") {
		if v, err := cvss31.ParseVector(raw); err == nil {
			score := v.BaseScore()
			rating, _ := cvss31.Rating(score)
			if rating == "NONE" {
				rating = "INFO"
			}
			f.CvssScore = &score
			f.CvssRating = rating
			f.CvssNomenclature = "CVSS-3.1"
		}
	} else if v, err := cvss40.ParseVector(raw); err == nil {
		score := v.Score()
		rating, _ := cvss40.Rating(score)
		if rating == "NONE" {
			rating = "INFO"
		}
		f.CvssScore = &score
		f.CvssRating = rating
		f.CvssNomenclature = v.Nomenclature()
	}
	// Old scalar targets remain lossless. Existing attachments are their evidence.
	if len(f.Targets) == 0 && strings.TrimSpace(f.Target) != "" {
		t := FindingTarget{URL: f.Target, Relation: "affected"}
		for _, b := range f.Blocks {
			if b.Type == "flow" {
				t.FlowIDs = append(t.FlowIDs, b.FlowID)
				if b.Missing {
					t.MissingFlowIDs = append(t.MissingFlowIDs, b.FlowID)
				}
			}
		}
		f.Targets = FindingTargets{t}
	}
}

// capabilityStatus derives the separate action/result/control/visual flags
// from the assessment gaps and the captured result screenshots.
func (f *Finding) capabilityStatus(gaps []string) FindingCapabilityStatus {
	st := FindingCapabilityStatus{
		Action:    !slices.Contains(gaps, "action"),
		Result:    !slices.Contains(gaps, "result"),
		Control:   !slices.Contains(gaps, "control"),
		Execution: f.ProofReview.Execution,
	}
	for _, b := range f.Blocks {
		if b.Type == "image" && !b.Missing && b.Hash != "" && b.Role == "result" && capturedFindingImage(b.Source) && strings.TrimSpace(b.Proof) != "" {
			st.Visual = true
		}
	}
	return st
}

func (f *Finding) assessmentGaps(r *FindingReadiness) []string {
	var gaps []string
	roles := map[string]bool{}
	flows := map[int64]bool{}
	images := map[string]bool{}
	visual := false
	visualResult := false
	for _, b := range f.Blocks {
		if b.Missing || strings.TrimSpace(b.Proof) == "" {
			continue
		}
		if b.Type == "flow" {
			roles[b.Role] = true
			flows[b.FlowID] = true
		}
		if b.Type == "image" && b.Hash != "" && capturedFindingImage(b.Source) {
			roles[b.Role] = true
			visual = true
			images[b.Hash] = true
			if b.Role == "result" {
				visualResult = true
			}
		}
	}
	for role, ref := range f.ProofReview.Evidence {
		if !ref.Missing && ((ref.FlowID > 0 && flows[ref.FlowID]) || (ref.Hash != "" && images[ref.Hash])) {
			roles[role] = true
			if role == "result" && ref.Hash != "" && images[ref.Hash] {
				visualResult = true
			}
		}
	}
	for _, role := range []string{"action", "result", "control"} {
		if !roles[role] {
			gaps = append(gaps, role)
		}
	}
	if f.ProofReview.Execution != "demonstrated" {
		gaps = append(gaps, "execution")
	}
	if (f.ProofReview.Execution == "not_executed" || f.ProofReview.Execution == "prerequisite_only") && strings.TrimSpace(f.ProofReview.Reason) == "" {
		gaps = append(gaps, "execution_reason")
	}
	if f.ProofReview.Visual && !visualResult {
		gaps = append(gaps, "visual")
	}
	rawCvss := strings.TrimSpace(f.Cvss)
	if strings.HasPrefix(strings.ToUpper(rawCvss), "CVSS:3.1") {
		v, err := cvss31.ParseVector(rawCvss)
		if err != nil {
			gaps = append(gaps, "cvss")
		} else {
			rating, _ := cvss31.Rating(v.BaseScore())
			if rating == "NONE" {
				rating = "INFO"
			}
			if !strings.EqualFold(f.Severity, rating) && strings.TrimSpace(f.ProofReview.SeverityOverride) == "" {
				gaps = append(gaps, "severity")
			}
		}
	} else {
		v, err := cvss40.ParseVector(rawCvss)
		if err != nil {
			gaps = append(gaps, "cvss")
		} else {
			rating, _ := cvss40.Rating(v.Score())
			if rating == "NONE" {
				rating = "INFO"
			}
			if !strings.EqualFold(f.Severity, rating) && strings.TrimSpace(f.ProofReview.SeverityOverride) == "" {
				gaps = append(gaps, "severity")
			}
		}
	}
	for i, t := range f.Targets {
		if (t.Relation == "setup" || t.Relation == "chain") && strings.TrimSpace(t.EvidenceException) != "" {
			continue
		}
		ok := len(f.Targets) == 1 && len(t.FlowIDs) == 0 && visual
		for _, id := range t.FlowIDs {
			if flows[id] && !slices.Contains(t.MissingFlowIDs, id) {
				ok = true
				break
			}
		}
		for _, hash := range t.ImageHashes {
			if images[hash] {
				ok = true
			}
		}
		if !ok {
			r.TargetEvidenceGaps = append(r.TargetEvidenceGaps, i)
		}
	}
	if len(r.TargetEvidenceGaps) > 0 {
		gaps = append(gaps, "target_evidence")
	}
	return gaps
}

func remapFindingTargets(f *Finding, mapping map[int64]int64) {
	visitClaimReferences(f, func(ref *FindingEvidenceReference) {
		if ref.FlowID > 0 && !ref.Missing {
			if local, ok := mapping[ref.FlowID]; ok {
				ref.FlowID = local
			} else {
				ref.Missing = true
			}
		}
	})
	for role, ref := range f.ProofReview.Evidence {
		if ref.FlowID > 0 && !ref.Missing {
			if id, ok := mapping[ref.FlowID]; ok {
				ref.FlowID = id
			} else {
				ref.Missing = true
			}
			f.ProofReview.Evidence[role] = ref
		}
	}
	for i := range f.Targets {
		t := &f.Targets[i]
		missing := append([]int64(nil), t.MissingFlowIDs...)
		for j, id := range t.FlowIDs {
			if slices.Contains(missing, id) {
				continue
			}
			if local, ok := mapping[id]; ok {
				t.FlowIDs[j] = local
			} else {
				t.MissingFlowIDs = append(t.MissingFlowIDs, id)
			}
		}
	}
}

func findingTargetsSignature(targets FindingTargets) []byte {
	copyTargets := append(FindingTargets(nil), targets...)
	for i := range copyTargets {
		copyTargets[i].FlowIDs = nil
		copyTargets[i].MissingFlowIDs = nil
	}
	// A migrated scalar target keeps its old dedup identity.
	if len(copyTargets) == 0 {
		return nil
	}
	if len(copyTargets) == 1 {
		t := copyTargets[0]
		if len(t.Methods) == 0 && t.Method == "" && t.Variant == "" && t.Role == "" && (t.Relation == "" || t.Relation == "affected") && t.Note == "" && t.EvidenceException == "" {
			return nil
		}
	}
	b, _ := json.Marshal(copyTargets)
	return b
}

// References point to captured records; a proof annotation still belongs in blocks.
func validateFindingReferences(tx *sql.Tx, f Finding) error {
	present, err := findingReferencePresence(tx, f)
	if err != nil {
		return err
	}
	for _, t := range f.Targets {
		for _, id := range t.FlowIDs {
			if !present[id] && !slices.Contains(t.MissingFlowIDs, id) {
				return fmt.Errorf("%w: evidence flow #%d does not exist", ErrInvalidFinding, id)
			}
		}
	}
	for _, ref := range f.ProofReview.Evidence {
		if ref.FlowID > 0 && !ref.Missing && !present[ref.FlowID] {
			return fmt.Errorf("%w: evidence flow #%d does not exist", ErrInvalidFinding, ref.FlowID)
		}
	}
	var claimErr error
	visitClaimReferences(&f, func(ref *FindingEvidenceReference) {
		if ref.FlowID > 0 && !ref.Missing && !present[ref.FlowID] {
			claimErr = fmt.Errorf("%w: capability evidence flow #%d does not exist", ErrInvalidFinding, ref.FlowID)
		}
	})
	return claimErr
}

func preserveAssessmentMissing(current Finding, next *Finding) {
	visitClaimReferences(next, func(ref *FindingEvidenceReference) {
		visitClaimReferences(&current, func(old *FindingEvidenceReference) {
			if old.Missing && old.FlowID == ref.FlowID && old.Hash == ref.Hash {
				ref.Missing = true
			}
		})
	})
	for i := range next.Targets {
		t := &next.Targets[i]
		for _, old := range current.Targets {
			for _, id := range old.MissingFlowIDs {
				if slices.Contains(t.FlowIDs, id) && !slices.Contains(t.MissingFlowIDs, id) {
					t.MissingFlowIDs = append(t.MissingFlowIDs, id)
				}
			}
		}
	}
	for role, ref := range next.ProofReview.Evidence {
		for _, old := range current.ProofReview.Evidence {
			if old.Missing && old.FlowID == ref.FlowID && old.Hash == ref.Hash {
				ref.Missing = true
				next.ProofReview.Evidence[role] = ref
			}
		}
	}

}

type findingReferenceQuery interface {
	Query(string, ...any) (*sql.Rows, error)
}

// Batch unique references; large multi-target findings must not issue one SQL
// lookup per target/flow pair on every Findings list refresh.
func findingReferencePresence(db findingReferenceQuery, f Finding) (map[int64]bool, error) {
	unique := map[int64]bool{}
	visitClaimReferences(&f, func(ref *FindingEvidenceReference) {
		if ref.FlowID > 0 {
			unique[ref.FlowID] = true
		}
	})
	for _, t := range f.Targets {
		for _, id := range t.FlowIDs {
			unique[id] = true
		}
	}
	for _, ref := range f.ProofReview.Evidence {
		if ref.FlowID > 0 {
			unique[ref.FlowID] = true
		}
	}
	ids := make([]any, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	present := map[int64]bool{}
	for len(ids) > 0 {
		n := min(len(ids), 500)
		rows, err := db.Query(`SELECT id FROM flows WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", n), ",")+`)`, ids[:n]...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			present[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		ids = ids[n:]
	}
	return present, nil
}

func markMissingFindingReferences(db findingReferenceQuery, f *Finding) error {
	present, err := findingReferencePresence(db, *f)
	if err != nil {
		return err
	}
	for i := range f.Targets {
		t := &f.Targets[i]
		for _, id := range t.FlowIDs {
			if !present[id] && !slices.Contains(t.MissingFlowIDs, id) {
				t.MissingFlowIDs = append(t.MissingFlowIDs, id)
			}
		}
	}
	for role, ref := range f.ProofReview.Evidence {
		if ref.FlowID > 0 && !present[ref.FlowID] {
			ref.Missing = true
			f.ProofReview.Evidence[role] = ref
		}
	}
	visitClaimReferences(f, func(ref *FindingEvidenceReference) {
		if ref.FlowID > 0 && !present[ref.FlowID] {
			ref.Missing = true
		}
	})
	return nil
}
