package store

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type FindingCapabilityClaim struct {
	Note     string                     `json:"note"`
	Basis    string                     `json:"basis,omitempty"` // state_change: before_after or server_proof
	Evidence []FindingEvidenceReference `json:"evidence"`
}

// FindingQualityCheck is one actionable readiness item. Rule groups the check
// (claim_evidence, completeness, evidence_integrity); Capability names the
// missing evidence capability for claim_evidence checks.
type FindingQualityCheck struct {
	Code       string `json:"code"`
	Field      string `json:"field"`
	Message    string `json:"message"`
	Rule       string `json:"rule,omitempty"`
	Capability string `json:"capability,omitempty"`
}

// sessionClaims need a server-observed flow, not only a screenshot, because the
// claimed capability is a property of the authenticated session.
func requiresFlowEvidence(key string) bool {
	return key == "authenticated_without_required_factor" || key == "account_control"
}

var capabilityClaimPatterns = []struct {
	key     string
	pattern *regexp.Regexp
	message string
}{
	{"authenticated_without_required_factor", regexp.MustCompile(`(?i)\b(?:(?:mfa|2fa|multi.factor|two.factor)\s+bypass|bypass(?:es|ing)?\s+(?:mfa|2fa|multi.factor|two.factor))\b`), "Link an annotated result showing an authenticated session without the required factor; record the reviewer's observation."},
	{"browser_execution", regexp.MustCompile(`(?i)\b(?:xss|cross.site scripting|script execution)\b`), "Link a real browser or device capture of execution. Reflection or an accepted HTTP request does not establish execution."},
	{"account_control", regexp.MustCompile(`(?i)\b(?:account takeover|account control|take over (?:an? |the )?account)\b`), "Link an annotated result demonstrating the specific account-control capability claimed."},
	{"state_change", regexp.MustCompile(`(?i)\b(?:unauth[o]?ri[sz]ed (?:state change|modification|update|deletion)|state.change)\b`), "Link annotated before/control and after evidence of the state. A server-proof label alone is insufficient."},
}

func normalizeCapabilityClaims(f *Finding) error {
	if len(f.ProofReview.Claims) > 4 {
		return fmt.Errorf("%w: at most four capability claims", ErrInvalidFinding)
	}
	for key, c := range f.ProofReview.Claims {
		valid := false
		for _, entry := range capabilityClaimPatterns {
			valid = valid || key == entry.key
		}
		if !valid {
			return fmt.Errorf("%w: unsupported capability claim %q", ErrInvalidFinding, key)
		}
		if len(c.Note) > 8192 || len(c.Evidence) > 16 {
			return fmt.Errorf("%w: capability claim exceeds limits", ErrInvalidFinding)
		}
		if c.Basis != "" && c.Basis != "before_after" && c.Basis != "server_proof" {
			return fmt.Errorf("%w: claim basis must be before_after or server_proof", ErrInvalidFinding)
		}
		for _, ref := range c.Evidence {
			if (ref.FlowID <= 0 && !isContentHash(ref.Hash)) || (ref.FlowID > 0 && ref.Hash != "") {
				return fmt.Errorf("%w: capability evidence requires one flowId or image hash", ErrInvalidFinding)
			}
		}
	}
	return nil
}
func visitClaimReferences(f *Finding, fn func(*FindingEvidenceReference)) {
	for key, c := range f.ProofReview.Claims {
		for i := range c.Evidence {
			fn(&c.Evidence[i])
		}
		f.ProofReview.Claims[key] = c
	}
}
func (f *Finding) capabilityClaimGaps() []string {
	var gaps []string
	text := f.Title + "\n" + f.Summary + "\n" + f.Impact
	for _, entry := range capabilityClaimPatterns {
		claim, declared := f.ProofReview.Claims[entry.key]
		if !declared && !entry.pattern.MatchString(text) {
			continue
		}
		result, baseline, visual, flowResult := false, false, false, false
		for _, ref := range claim.Evidence {
			if ref.Missing {
				continue
			}
			for _, b := range f.Blocks {
				if b.Missing || strings.TrimSpace(b.Proof) == "" {
					continue
				}
				matches := (ref.FlowID > 0 && b.Type == "flow" && b.FlowID == ref.FlowID) || (ref.Hash != "" && b.Type == "image" && b.Hash == ref.Hash && capturedFindingImage(b.Source))
				if !matches {
					continue
				}
				result = result || b.Role == "result"
				flowResult = flowResult || (b.Type == "flow" && b.Role == "result")
				baseline = baseline || b.Role == "baseline" || b.Role == "control"
				visual = visual || (b.Type == "image" && capturedFindingImage(b.Source) && b.Role == "result")
			}
		}
		valid := strings.TrimSpace(claim.Note) != "" && result
		if entry.key == "browser_execution" {
			valid = valid && visual
		}
		if requiresFlowEvidence(entry.key) {
			valid = valid && flowResult
		}
		if entry.key == "state_change" {
			valid = valid && baseline
		}
		if !valid {
			gaps = append(gaps, "capability:"+entry.key)
		}
	}
	return gaps
}
func qualityChecks(gaps []string) []FindingQualityCheck {
	hints := map[string][2]string{
		"title": {"title", "Add a precise finding title."}, "summary": {"summary", "State the observed claim and its limits."}, "target": {"targets", "Add an affected endpoint or application."}, "target_evidence": {"targets", "Link retained evidence for each affected target."},
		"impact": {"impact", "Describe the demonstrated impact."}, "why": {"why", "Explain the failed boundary or root cause."}, "evidence": {"blocks", "Attach a captured flow or screenshot."}, "proof": {"blocks.proof", "Explain what each attached artifact proves."}, "reproduction": {"blocks.role", "Order and label the reproduction steps."},
		"action": {"proofReview.evidence.action", "Identify the recorded action."}, "result": {"proofReview.evidence.result", "Identify the observed result."}, "control": {"proofReview.evidence.control", "Identify the negative or control case."}, "execution": {"proofReview.execution", "Review whether the claimed impact was demonstrated."}, "execution_reason": {"proofReview.reason", "Explain the verification limit."},
		"visual": {"blocks.source", "Classify and annotate a real browser/device result capture."}, "cvss": {"cvss", "Provide a valid CVSS v4.0 vector."}, "severity": {"severity", "Align severity with the calculated CVSS rating."}, "fix": {"fix", "Describe the recommended fix."}, "retest": {"retest", "Describe expected secure behavior, including a negative case."}, "confidence": {"confidence", "Record the evidence confidence."}, "evidence_missing": {"blocks", "Restore or replace missing evidence references or raw message bodies."}, "verification": {"status", "Resolve the outstanding verification status before final reporting."},
	}
	out := []FindingQualityCheck{}
	for _, gap := range gaps {
		hint, ok := hints[gap]
		rule, capability := "completeness", ""
		if gap == "evidence_missing" {
			rule = "evidence_integrity"
		}
		if !ok && strings.HasPrefix(gap, "capability:") {
			key := strings.TrimPrefix(gap, "capability:")
			for _, entry := range capabilityClaimPatterns {
				if key == entry.key {
					hint = [2]string{"proofReview.claims." + key, entry.message}
					rule, capability, ok = "claim_evidence", key, true
				}
			}
		}
		if !ok {
			hint = [2]string{gap, "Review " + gap + "."}
		}
		out = append(out, FindingQualityCheck{Code: gap, Field: hint[0], Message: hint[1], Rule: rule, Capability: capability})
	}
	return out
}
func addUniqueFindingGap(gaps []string, key string) []string {
	if !slices.Contains(gaps, key) {
		return append(gaps, key)
	}
	return gaps
}
