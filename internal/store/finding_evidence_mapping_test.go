package store

import (
	"errors"
	"strings"
	"testing"
)

func TestEvidenceMappingErrorsNameFieldAndTrigger(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	valid := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name string
		ev   map[string]FindingEvidenceReference
		want []string
	}{
		{"empty hash", map[string]FindingEvidenceReference{"result": {Hash: ""}}, []string{"proofReview.evidence.result", "empty", "flowId", "64"}},
		{"bad hash", map[string]FindingEvidenceReference{"action": {Hash: "xyz"}}, []string{"proofReview.evidence.action.hash", "64", "xyz"}},
		{"both", map[string]FindingEvidenceReference{"control": {FlowID: 3, Hash: valid}}, []string{"proofReview.evidence.control", "not both"}},
		{"bad role", map[string]FindingEvidenceReference{"proof": {FlowID: 3}}, []string{"proofReview.evidence.proof", "action, result, control"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.CreateFinding(&Finding{Title: "example", ProofReview: FindingProofReview{Evidence: tc.ev}})
			if !errors.Is(err, ErrInvalidFinding) {
				t.Fatalf("want ErrInvalidFinding, got %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("message missing %q: %v", want, err)
				}
			}
		})
	}
}
