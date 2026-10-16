package store

import (
	"errors"
	"strings"
	"testing"
)

func openStructuredStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFindingStructuredFieldsRoundTrip(t *testing.T) {
	s := openStructuredStore(t)
	other, err := s.CreateFinding(&Finding{Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	f := &Finding{
		Title:           "Chain",
		Claims:          []FindingClaim{{ID: "c1", Statement: "Fabricated timestamps accepted", Verdict: "confirmed"}, {ID: "c2", Statement: "KYC approved", Verdict: "not_reproduced", Note: "profile stayed empty"}},
		NotExecuted:     []FindingNotExecuted{{Method: "post", Target: "https://example.com/register", Reason: "would provision an account", Risk: "creates a real account", RequiresAuthorisation: true}},
		RelatedFindings: []FindingRelation{{ID: other, Relation: "escalates"}},
	}
	id, err := s.CreateFinding(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Claims) != 2 || got.Claims[1].Verdict != "not_reproduced" || got.NotExecuted[0].Method != "POST" || got.RelatedFindings[0].ID != other {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Readiness == nil || len(got.Readiness.WithdrawnClaims) != 1 || got.Readiness.WithdrawnClaims[0] != "c2" {
		t.Fatalf("withdrawn claims not surfaced: %+v", got.Readiness)
	}
}

func TestFindingStructuredValidation(t *testing.T) {
	s := openStructuredStore(t)
	cases := map[string]Finding{
		"bad verdict":     {Title: "x", Claims: []FindingClaim{{ID: "c", Statement: "s", Verdict: "maybe"}}},
		"dup claim id":    {Title: "x", Claims: []FindingClaim{{ID: "c", Statement: "s", Verdict: "confirmed"}, {ID: "c", Statement: "t", Verdict: "refuted"}}},
		"empty statement": {Title: "x", Claims: []FindingClaim{{ID: "c", Verdict: "confirmed"}}},
		"no reason":       {Title: "x", NotExecuted: []FindingNotExecuted{{Method: "POST", Target: "https://example.com"}}},
		"bad relation":    {Title: "x", RelatedFindings: []FindingRelation{{ID: 1, Relation: "friends"}}},
		"missing id":      {Title: "x", RelatedFindings: []FindingRelation{{ID: 9999, Relation: "chain"}}},
	}
	for name, f := range cases {
		f := f
		if _, err := s.CreateFinding(&f); !errors.Is(err, ErrInvalidFinding) {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	id, err := s.CreateFinding(&Finding{Title: "self"})
	if err != nil {
		t.Fatal(err)
	}
	rel := []FindingRelation{{ID: id, Relation: "chain"}}
	err = s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{RelatedFindings: &rel})
	if !errors.Is(err, ErrInvalidFinding) || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("self link: %v", err)
	}
}

func TestFindingStructuredPatchPreservesAndRevisions(t *testing.T) {
	s := openStructuredStore(t)
	id, err := s.CreateFinding(&Finding{Title: "T", Claims: []FindingClaim{{ID: "c1", Statement: "s", Verdict: "confirmed"}}})
	if err != nil {
		t.Fatal(err)
	}
	title := "T2"
	if err = s.UpdateFindingCanonical(id, nil, nil, &title, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	if len(got.Claims) != 1 {
		t.Fatalf("unrelated patch dropped claims: %+v", got.Claims)
	}
	ne := []FindingNotExecuted{{Method: "DELETE", Target: "https://example.com/x", Reason: "destructive"}}
	claims := []FindingClaim{{ID: "c1", Statement: "s", Verdict: "refuted"}}
	if err = s.UpdateFindingCanonical(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, FindingMetadataPatch{NotExecuted: &ne, Claims: &claims, Change: FindingChange{Actor: "r", Source: "api"}}); err != nil {
		t.Fatal(err)
	}
	revs, err := s.ListFindingRevisions(id, 10)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Join(revs[0].Fields, ",")
	if !strings.Contains(fields, "claims") || !strings.Contains(fields, "notExecuted") {
		t.Fatalf("revision does not diff new fields: %s", fields)
	}
	if err = s.DeleteFinding(id); err != nil {
		t.Fatal(err)
	}
	if err = s.RestoreFindingRevision(id, revs[0].ID, FindingChange{Actor: "r", Source: "api"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetFinding(id)
	if err != nil || got.Claims[0].Verdict != "refuted" || len(got.NotExecuted) != 1 {
		t.Fatalf("restore lost structured fields: %+v %v", got, err)
	}
	after, _ := s.ListFindingRevisions(id, 10)
	if len(after) != len(revs)+2 { // delete + restore appended, history intact
		t.Fatalf("restore must append a revision: before=%d after=%d", len(revs), len(after))
	}
}

func TestFindingNotExecutedSatisfiesExecutionReason(t *testing.T) {
	s := openStructuredStore(t)
	f := &Finding{Title: "T", ProofReview: FindingProofReview{Execution: "not_executed"}, NotExecuted: []FindingNotExecuted{{Method: "POST", Target: "https://example.com/a", Reason: "destructive"}}}
	id, err := s.CreateFinding(f)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	for _, g := range got.Readiness.Gaps {
		if g == "execution_reason" {
			t.Fatalf("documented not-executed request should satisfy the reason gap: %v", got.Readiness.Gaps)
		}
	}
}

func TestFindingDuplicateLinkDoesNotDemandEvidence(t *testing.T) {
	s := openStructuredStore(t)
	orig, _ := s.CreateFinding(&Finding{Title: "orig"})
	id, err := s.CreateFinding(&Finding{Title: "dup", RelatedFindings: []FindingRelation{{ID: orig, Relation: "duplicate"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFinding(id)
	for _, g := range got.Readiness.Gaps {
		if g == "evidence" || g == "proof" || g == "reproduction" {
			t.Fatalf("duplicate demanded evidence gap %q", g)
		}
	}
}
