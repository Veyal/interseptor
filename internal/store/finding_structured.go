package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Structured finding fields. They are persisted together in one JSON column
// (findings.structured) and exposed as three top-level Finding fields.

// FindingClaim records a per-claim verdict so a withdrawn claim is never lost in prose.
type FindingClaim struct {
	ID        string                     `json:"id"`
	Statement string                     `json:"statement"`
	Verdict   string                     `json:"verdict"` // confirmed | partially_confirmed | not_reproduced | refuted
	Evidence  []FindingEvidenceReference `json:"evidence,omitempty"`
	Note      string                     `json:"note,omitempty"`
}

// FindingNotExecuted records a request deliberately not sent ("chose not to", not "could not").
type FindingNotExecuted struct {
	Method                string `json:"method"`
	Target                string `json:"target"`
	Reason                string `json:"reason"`
	Risk                  string `json:"risk,omitempty"`
	RequiresAuthorisation bool   `json:"requiresAuthorisation,omitempty"`
}

// FindingRelation links this finding to another one in the same project.
type FindingRelation struct {
	ID       int64  `json:"id"`
	Relation string `json:"relation"` // enables | enabled_by | chain | duplicate | escalates
}

type findingStructured struct {
	Claims          []FindingClaim       `json:"claims,omitempty"`
	NotExecuted     []FindingNotExecuted `json:"notExecuted,omitempty"`
	RelatedFindings []FindingRelation    `json:"relatedFindings,omitempty"`
}

const maxStructuredItems = 64

var claimVerdicts = []string{"confirmed", "partially_confirmed", "not_reproduced", "refuted"}
var relationKinds = []string{"enables", "enabled_by", "chain", "duplicate", "escalates"}

// InverseFindingRelation returns the relation as seen from the linked finding.
func InverseFindingRelation(relation string) string {
	switch relation {
	case "enables":
		return "enabled_by"
	case "enabled_by":
		return "enables"
	}
	return relation // chain, duplicate and escalates are shown the same from both ends
}

// IsWithdrawnVerdict reports whether the verdict withdraws the claim.
func IsWithdrawnVerdict(verdict string) bool {
	return verdict == "not_reproduced" || verdict == "refuted"
}

// structuredScan adapts the structured column to database/sql.
type structuredScan struct{ f *Finding }

func (s structuredScan) Scan(value any) error {
	var v findingStructured
	if err := scanFindingJSON(value, &v); err != nil {
		return err
	}
	s.f.Claims, s.f.NotExecuted, s.f.RelatedFindings = v.Claims, v.NotExecuted, v.RelatedFindings
	return nil
}

func structuredValue(f Finding) string {
	b, _ := json.Marshal(findingStructured{f.Claims, f.NotExecuted, f.RelatedFindings})
	return string(b)
}

func normalizeFindingStructured(f *Finding) error {
	if len(f.Claims) > maxStructuredItems || len(f.NotExecuted) > maxStructuredItems || len(f.RelatedFindings) > maxStructuredItems {
		return fmt.Errorf("%w: at most %d claims, not-executed requests, or related findings", ErrInvalidFinding, maxStructuredItems)
	}
	seen := map[string]bool{}
	for i := range f.Claims {
		c := &f.Claims[i]
		c.ID, c.Statement, c.Note = strings.TrimSpace(c.ID), strings.TrimSpace(c.Statement), strings.TrimSpace(c.Note)
		c.Verdict = strings.ToLower(strings.TrimSpace(c.Verdict))
		switch {
		case c.ID == "" || len(c.ID) > 64:
			return fmt.Errorf("%w: claim %d requires an id (max 64 bytes)", ErrInvalidFinding, i+1)
		case seen[c.ID]:
			return fmt.Errorf("%w: duplicate claim id %q", ErrInvalidFinding, c.ID)
		case c.Statement == "" || len(c.Statement) > 2048:
			return fmt.Errorf("%w: claim %q requires a statement (max 2048 bytes)", ErrInvalidFinding, c.ID)
		case !slices.Contains(claimVerdicts, c.Verdict):
			return fmt.Errorf("%w: claim %q verdict must be one of %s", ErrInvalidFinding, c.ID, strings.Join(claimVerdicts, "|"))
		case len(c.Note) > 8192 || len(c.Evidence) > 16:
			return fmt.Errorf("%w: claim %q exceeds limits", ErrInvalidFinding, c.ID)
		}
		seen[c.ID] = true
		for _, ref := range c.Evidence {
			if (ref.FlowID <= 0 && !isContentHash(ref.Hash)) || (ref.FlowID > 0 && ref.Hash != "") {
				return fmt.Errorf("%w: claim %q evidence requires one flowId or image hash", ErrInvalidFinding, c.ID)
			}
		}
	}
	for i := range f.NotExecuted {
		n := &f.NotExecuted[i]
		n.Method, n.Target = strings.ToUpper(strings.TrimSpace(n.Method)), strings.TrimSpace(n.Target)
		n.Reason, n.Risk = strings.TrimSpace(n.Reason), strings.TrimSpace(n.Risk)
		switch {
		case n.Method == "" || len(n.Method) > 32 || strings.ContainsAny(n.Method, " \t\r\n"):
			return fmt.Errorf("%w: not-executed request %d requires a valid method", ErrInvalidFinding, i+1)
		case n.Target == "" || len(n.Target) > 4096:
			return fmt.Errorf("%w: not-executed request %d requires a target (max 4096 bytes)", ErrInvalidFinding, i+1)
		case n.Reason == "" || len(n.Reason) > 4096:
			return fmt.Errorf("%w: not-executed request %d requires a reason (max 4096 bytes)", ErrInvalidFinding, i+1)
		case len(n.Risk) > 4096:
			return fmt.Errorf("%w: not-executed request %d risk exceeds 4096 bytes", ErrInvalidFinding, i+1)
		}
	}
	seenRel := map[FindingRelation]bool{}
	related := f.RelatedFindings[:0]
	for _, r := range f.RelatedFindings {
		r.Relation = strings.ToLower(strings.TrimSpace(r.Relation))
		if r.ID <= 0 || !slices.Contains(relationKinds, r.Relation) {
			return fmt.Errorf("%w: related finding requires a positive id and relation %s", ErrInvalidFinding, strings.Join(relationKinds, "|"))
		}
		if seenRel[r] {
			continue
		}
		seenRel[r] = true
		related = append(related, r)
	}
	f.RelatedFindings = related
	return nil
}

// validateRelatedFindings checks that every linked finding exists. selfID is 0 on create.
func validateRelatedFindings(tx *sql.Tx, selfID int64, f Finding) error {
	for _, r := range f.RelatedFindings {
		if r.ID == selfID && selfID != 0 {
			return fmt.Errorf("%w: finding cannot be related to itself", ErrInvalidFinding)
		}
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM findings WHERE id=?`, r.ID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: related finding #%d does not exist", ErrInvalidFinding, r.ID)
		}
	}
	return nil
}

// withdrawnClaimIDs lists claims whose verdict withdraws them, in declared order.
func (f *Finding) withdrawnClaimIDs() []string {
	var out []string
	for _, c := range f.Claims {
		if IsWithdrawnVerdict(c.Verdict) {
			out = append(out, c.ID)
		}
	}
	return out
}

func (f *Finding) hasRelation(kind string) bool {
	for _, r := range f.RelatedFindings {
		if r.Relation == kind {
			return true
		}
	}
	return false
}

// relaxLinkedFindingGaps drops evidence demands for a duplicate: its evidence
// lives on the finding it duplicates, so asking for it twice is noise.
func (f *Finding) relaxLinkedFindingGaps(gaps []string) []string {
	if !f.hasRelation("duplicate") {
		return gaps
	}
	drop := []string{"evidence", "proof", "reproduction", "action", "result", "control", "target_evidence", "execution", "visual"}
	return slices.DeleteFunc(gaps, func(g string) bool { return slices.Contains(drop, g) })
}
