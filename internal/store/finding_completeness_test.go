package store

import (
	"testing"
)

func TestFindingCompletenessDraftVsReady(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.CreateFinding(&Finding{Title: "stub only"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ready {
		t.Fatalf("stub should not be ready: missing=%v", got.Missing)
	}
	for _, need := range []string{"impact", "why", "target", "poc"} {
		found := false
		for _, m := range got.Missing {
			if m == need {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected missing %q in %v", need, got.Missing)
		}
	}

	why := "broken object-level authorization"
	summary := "A user can access another user's object."
	cwe := "CWE-639"
	env := "staging"
	impact := "attacker reads other users' PII"
	target := "GET api.example.com/users/{id}"
	fix := "Enforce ownership checks."
	confidence := "certain"
	retest := "Repeat the request and confirm denial."
	if err := s.UpdateFindingCanonical(id, nil, nil, nil, &target, nil, nil, &fix, nil, &impact, &why, &cwe, &env, nil, nil, &summary, &confidence, &retest, nil); err != nil {
		t.Fatal(err)
	}
	f1, _ := s.InsertFlow(&Flow{Method: "GET", Host: "api.example.com", Path: "/users/1", Status: 200})
	f2, _ := s.InsertFlow(&Flow{Method: "GET", Host: "api.example.com", Path: "/users/2", Status: 200})
	_ = s.AttachFlowWithMetadata(id, f1, "Before: own account", -1, "baseline", "Establishes authorized access to the current user's record.", "captured_flow", f1)
	_ = s.AttachFlowWithMetadata(id, f2, "After: other user data", -1, "result", "Shows the same session receiving another user's data.", "captured_flow", f2)

	// Still High default Medium — only  need 1 poc for Medium. Make it High to require 2.
	sev := "High"
	_ = s.UpdateFinding(id, &sev, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	got, err = s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Why != why || got.Cwe != cwe || got.Environment != "staging" {
		t.Fatalf("fields: why=%q cwe=%q env=%q", got.Why, got.Cwe, got.Environment)
	}
	if !got.Ready {
		t.Fatalf("expected ready, missing=%v", got.Missing)
	}
}

func TestExtractWhyFromNarrative(t *testing.T) {
	md := "## Summary\nimpact first\n\n## Why this is a vulnerability\nBroken authz on object IDs.\n\n## Impact\nPII leak\n"
	got := ExtractWhyFromNarrative(md)
	if got != "Broken authz on object IDs." {
		t.Fatalf("got %q", got)
	}
}

func TestEnrichCompletenessMigratesWhyFromBody(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := `[{"type":"text","md":"## Summary\nx\n\n## Why this is a vulnerability\nMissing ownership check on /order/{id}."}]`
	id, err := s.CreateFinding(&Finding{
		Title: "legacy why", Impact: "order leak", Target: "example.com", Body: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	f1, _ := s.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/order/1", Status: 200})
	_ = s.AttachFlow(id, f1, "poc", -1)
	got, _ := s.GetFinding(id)
	if got.Why != "Missing ownership check on /order/{id}." {
		t.Fatalf("why migrate = %q", got.Why)
	}
}
