package store

import (
	"slices"
	"testing"
)

func TestClaimConsistencyRequiresCapabilityEvidence(t *testing.T) {
	f := completeAssessment()
	f.Title = "Stored XSS execution in comments"
	f.EnrichCompleteness()
	if f.Ready || !slices.Contains(f.Missing, "capability:browser_execution") {
		t.Fatalf("flow acceptance was treated as execution: %+v", f.Readiness)
	}
	f.ProofReview.Claims = map[string]FindingCapabilityClaim{"browser_execution": {Note: "Reviewer observed execution in the browser", Evidence: []FindingEvidenceReference{{FlowID: 2}}}}
	f.EnrichCompleteness()
	if f.Ready {
		t.Fatal("HTTP alone counted as browser execution")
	}
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	f.Blocks = append(f.Blocks, FindingBlock{Type: "image", Hash: hash, Source: "browser_screenshot", Role: "result", Proof: "Observed result"})
	f.ProofReview.Claims["browser_execution"] = FindingCapabilityClaim{Note: "Reviewer-observed result", Evidence: []FindingEvidenceReference{{Hash: hash}}}
	f.EnrichCompleteness()
	if !f.Ready {
		t.Fatalf("declared capture: %+v", f.Readiness)
	}
	f.Blocks[3].Source = "flow_preview"
	f.EnrichCompleteness()
	if f.Ready {
		t.Fatal("generated preview qualified")
	}
	for _, title := range []string{"MFA bypass permits access", "Account takeover", "Unauthorized state change"} {
		f = completeAssessment()
		f.Title = title
		f.EnrichCompleteness()
		if f.Ready || len(f.Readiness.Checks) == 0 {
			t.Fatalf("unsupported %s claim accepted", title)
		}
	}
}
func TestQualityGateDoesNotRewriteClaim(t *testing.T) {
	f := completeAssessment()
	f.Title = "MFA bypass"
	title := f.Title
	impact := f.Impact
	f.EnrichCompleteness()
	if f.Title != title || f.Impact != impact {
		t.Fatal("gate rewrote claim")
	}
	found := false
	for _, c := range f.Readiness.Checks {
		if c.Code == "capability:authenticated_without_required_factor" {
			found = true
			if c.Field != "proofReview.claims.authenticated_without_required_factor" || c.Message == "" {
				t.Fatalf("not actionable %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("missing capability check")
	}
}
func TestClaimChecksNameTheMissingCapability(t *testing.T) {
	cases := map[string]string{
		"Stored XSS execution":      "browser_execution",
		"MFA bypass permits access": "authenticated_without_required_factor",
		"Account takeover":          "account_control",
		"Unauthorized state change": "state_change",
	}
	for title, key := range cases {
		f := completeAssessment()
		f.Title = title
		f.EnrichCompleteness()
		found := false
		for _, c := range f.Readiness.Checks {
			if c.Code == "capability:"+key {
				found = c.Rule == "claim_evidence" && c.Capability == key && c.Field == "proofReview.claims."+key
			}
		}
		if !found {
			t.Fatalf("%s: no capability-tagged check in %+v", title, f.Readiness.Checks)
		}
	}
}

func TestSessionClaimsNeedServerObservedFlow(t *testing.T) {
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	f := completeAssessment()
	f.Title = "2FA bypass"
	f.Blocks = append(f.Blocks, FindingBlock{Type: "image", Hash: hash, Source: "browser_screenshot", Role: "result", Proof: "Dashboard visible"})
	f.ProofReview.Claims = map[string]FindingCapabilityClaim{"authenticated_without_required_factor": {Note: "Reviewer saw dashboard", Evidence: []FindingEvidenceReference{{Hash: hash}}}}
	f.EnrichCompleteness()
	if !slices.Contains(f.Missing, "capability:authenticated_without_required_factor") {
		t.Fatalf("screenshot alone proved an authenticated session: %+v", f.Readiness)
	}
	f.ProofReview.Claims["authenticated_without_required_factor"] = FindingCapabilityClaim{Note: "Authenticated response without factor", Evidence: []FindingEvidenceReference{{FlowID: 2}}}
	f.EnrichCompleteness()
	if slices.Contains(f.Missing, "capability:authenticated_without_required_factor") {
		t.Fatalf("flow result rejected: %+v", f.Readiness)
	}
}

func TestStateChangeLabelCannotReplaceBeforeAfterEvidence(t *testing.T) {
	f := completeAssessment()
	f.Title = "State change"
	f.ProofReview.Claims = map[string]FindingCapabilityClaim{"state_change": {Note: "Declared server proof", Basis: "server_proof", Evidence: []FindingEvidenceReference{{FlowID: 2}}}}
	f.EnrichCompleteness()
	if f.Ready {
		t.Fatal("label alone satisfied state-change proof")
	}
}
