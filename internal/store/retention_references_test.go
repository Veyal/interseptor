package store

import (
	"testing"
	"time"
)

// retentionRefCase attaches one old flow to a finding through a single kind of
// reference that is not a body flow block, so finding_flows never sees it.
type retentionRefCase struct {
	name   string
	attach func(t *testing.T, s *Store, flowID int64)
}

func retentionRefCases() []retentionRefCase {
	create := func(t *testing.T, s *Store, f *Finding) int64 {
		t.Helper()
		id, err := s.CreateFinding(f)
		if err != nil {
			t.Fatalf("CreateFinding: %v", err)
		}
		return id
	}
	return []retentionRefCase{
		{"proofReview.evidence", func(t *testing.T, s *Store, fid int64) {
			create(t, s, &Finding{Title: "evidence", ProofReview: FindingProofReview{Evidence: map[string]FindingEvidenceReference{"action": {FlowID: fid}}}})
		}},
		{"targets.flowIds", func(t *testing.T, s *Store, fid int64) {
			create(t, s, &Finding{Title: "targets", Targets: FindingTargets{{URL: "https://example.com/x", FlowIDs: []int64{fid}}}})
		}},
		{"claims.evidence", func(t *testing.T, s *Store, fid int64) {
			create(t, s, &Finding{Title: "claims", Claims: []FindingClaim{{ID: "c1", Statement: "reads data", Verdict: "confirmed", Evidence: []FindingEvidenceReference{{FlowID: fid}}}}})
		}},
		{"proofReview.claims.evidence", func(t *testing.T, s *Store, fid int64) {
			create(t, s, &Finding{Title: "capability", ProofReview: FindingProofReview{Claims: map[string]FindingCapabilityClaim{"account_control": {Note: "n", Evidence: []FindingEvidenceReference{{FlowID: fid}}}}}})
		}},
		{"verification.baseline", func(t *testing.T, s *Store, fid int64) {
			id := create(t, s, &Finding{Title: "verified baseline"})
			if _, err := s.SaveFindingVerification(&FindingVerification{FindingID: id, BaselineFlow: fid}); err != nil {
				t.Fatal(err)
			}
		}},
		{"verification.payload", func(t *testing.T, s *Store, fid int64) {
			id := create(t, s, &Finding{Title: "verified payload"})
			if _, err := s.SaveFindingVerification(&FindingVerification{FindingID: id, PayloadFlow: fid}); err != nil {
				t.Fatal(err)
			}
		}},
	}
}

func insertOldFlow(t *testing.T, s *Store, host string) int64 {
	t.Helper()
	id, err := s.InsertFlow(&Flow{TS: time.Now().Add(-48 * time.Hour), Method: "GET", Scheme: "https", Host: host, Port: 443, Path: "/x", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRetentionPreservesEveryFindingReferenceKind(t *testing.T) {
	deletes := map[string]func(s *Store) (int64, error){
		"olderThan":  func(s *Store) (int64, error) { return s.DeleteFlowsOlderThan(time.Now().Add(-time.Hour).UnixMilli()) },
		"keepNewest": func(s *Store) (int64, error) { return s.DeleteFlowsKeepNewest(1) },
	}
	for _, c := range retentionRefCases() {
		for dname, del := range deletes {
			t.Run(c.name+"/"+dname, func(t *testing.T) {
				s, err := Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				kept := insertOldFlow(t, s, "old.example.com")
				doomed := insertOldFlow(t, s, "old.example.com")
				if _, err := s.InsertFlow(&Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "new.example.com", Port: 443, Path: "/", Status: 200}); err != nil {
					t.Fatal(err)
				} // recent and newest: kept by every pass
				c.attach(t, s, kept)
				n, err := del(s)
				if err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("deleted %d flows, want only the unreferenced one", n)
				}
				if _, err := s.GetFlow(kept); err != nil {
					t.Fatalf("referenced flow %d deleted: %v", kept, err)
				}
				if f, err := s.GetFlow(doomed); err == nil && f != nil {
					t.Fatalf("unreferenced flow %d survived", doomed)
				}
			})
		}
	}
}

// A hash-only evidence reference yields a NULL flow id; NULL inside NOT IN
// must not silently disable retention for every flow.
func TestRetentionIgnoresHashOnlyReferences(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	doomed := insertOldFlow(t, s, "old.example.com")
	if _, err := s.db.Exec(`INSERT INTO findings(ts,updated_ts,title,proof_review) VALUES(1,1,'img',?)`,
		`{"evidence":{"result":{"hash":"`+zeroHash+`"}}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO findings(ts,updated_ts,title,targets) VALUES(1,1,'bad','not json')`); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteFlowsOlderThan(time.Now().Add(-time.Hour).UnixMilli())
	if err != nil || n != 1 {
		t.Fatalf("deleted %d (err %v), want 1", n, err)
	}
	if f, err := s.GetFlow(doomed); err == nil && f != nil {
		t.Fatal("unreferenced flow survived")
	}
}

const zeroHash = "0000000000000000000000000000000000000000000000000000000000000000"
