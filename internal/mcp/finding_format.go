package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// findingFormatGuide is the REQUIRED finding shape. Surfaced in initialize
// instructions and create_finding / update_finding tool descriptions.
const findingFormatGuide = `REQUIRED FORMAT (evidence-first; blanks OK in a draft, complete before report-ready):
1. Claim — title plus a concise summary of the vulnerable behavior
2. Risk — impact (attacker outcome) and why (failed security boundary / root cause)
3. Affected targets — ordered targets with methods, URL, role, variant, relation, and flow_ids. First entry is primary; preserve every affected endpoint.
   - Link annotated evidence per target; only documented setup/chain targets may use evidenceException
   - Environment: production|staging|development|testing|local (legacy prod is preserved)
   - CVSS:4.0 vector is required for report readiness; the calculated score must match severity
4. Reproduction — ordered typed blocks with role=context|setup|baseline|action|result|control|retest|observation
   - Before → Action → After is only the "Differential proof" preset for authz/state-change cases
   - Outlines organize the narrative; readiness separately requires evidenced action, observed result, and a negative/control case
5. Evidence — every report-ready finding needs a captured flow and/or image, with a short proof statement
   - Set proofReview.visual=true for browser/visual claims; attach a real browser screenshot of the observed result
   - If the request exists in Interseptor, attach its flow so raw evidence remains inspectable
   - Use render_flow_preview for a generated HTTP image; it is labeled as a flow preview, not a browser screenshot
   - Capability checklist before report-ready (readiness reports each separately): ACTION the exact triggering request/flow; RESULT the observed outcome; CONTROL a negative or normal-behavior request that distinguishes vulnerable behavior; VISUAL a real browser/device screenshot for browser findings
   - Operator-uploaded images count as visual proof only after a reviewer classifies them (classify_finding_image); generated previews never qualify
   - Test the claimed impact end to end when safe and in scope; if a step was not executed, set proofReview.execution=not_executed with proofReview.reason, keep status=needs_verification, or narrow the impact
6. Fix and retest — remediation plus the expected secure behavior / negative test in retest
7. Review — confidence=tentative|firm|certain; proofReview.execution=demonstrated only for impact actually observed
   - A permissive response or reachable prerequisite alone does not establish the claimed impact
   - Otherwise use prerequisite_only or not_executed with an explicit proofReview.reason; status stays needs_verification
   - Narrow impact to what evidence establishes. Mark unproven execution as "NOT confirmed". Keep verificationInstructions for the remaining review
   - Keep secrets redacted; a length or digest can establish equality without publishing the value
   - For integrity evidence, describe only the bounded, reversible observed change and preserve its flow

Use structured blocks arrays when available; legacy body JSON remains accepted. Stub create (title only) is allowed.
Do NOT file walls of freeform markdown. Put summary/impact/why/fix/retest in their fields, reproduction in text blocks, and raw proof in flow/image blocks.`

// wallOfTextMin is the minimum narrative length that triggers a hard reject
// when body text looks like an essay without structure.
const wallOfTextMin = 180

func findingBlocksSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "canonical ordered reproduction/evidence blocks; prefer this over legacy body JSON",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":         map[string]any{"type": "string", "description": "text|flow|image"},
				"role":         map[string]any{"type": "string", "description": "context|setup|baseline|action|result|control|retest|observation"},
				"md":           map[string]any{"type": "string", "description": "text block content"},
				"flowId":       map[string]any{"type": "integer", "description": "captured flow id for a flow block"},
				"note":         map[string]any{"type": "string", "description": "short evidence caption"},
				"proof":        map[string]any{"type": "string", "description": "exactly what the evidence establishes"},
				"hash":         map[string]any{"type": "string", "description": "existing content hash; upload new images with add_finding_image"},
				"mime":         map[string]any{"type": "string"},
				"caption":      map[string]any{"type": "string"},
				"source":       map[string]any{"type": "string", "description": "captured_flow|browser_screenshot|flow_preview|generated_image|operator_upload|tool_output|other"},
				"sourceFlowId": map[string]any{"type": "integer", "description": "originating flow for captured flows or generated flow previews"},
			},
			"required": []string{"type"},
		},
	}
}

type findingFormatInput struct {
	Severity                 string
	Status                   string
	Title                    string
	Summary                  string
	Target                   string
	Detail                   string
	Impact                   string
	Why                      string
	Fix                      string
	Retest                   string
	Confidence               string
	Body                     string // JSON blocks array string
	VerificationInstructions string
	Partial                  bool // update calls may omit already-populated envelope fields
}

var (
	reHeading         = regexp.MustCompile(`(?m)^#{1,6}\s+\S`)
	reCredMention     = regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|credential|token)\b`)
	reCredBoldOrTable = regexp.MustCompile(`(?i)(\*\*[^*]*(password|passwd|secret|api[_-]?key|credential|token)[^*]*\*\*|\|[^|\n]*(password|passwd|secret|api[_-]?key|credential|token)[^|\n]*\|)`)
)

type findingArtifacts struct {
	text          string
	flowCount     int
	imageCount    int
	proofless     int
	roles         map[string]bool
	validBodyJSON bool
}

type findingBodyBlock struct {
	Type    string `json:"type"`
	MD      string `json:"md,omitempty"`
	FlowID  int64  `json:"flowId,omitempty"`
	Note    string `json:"note,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Caption string `json:"caption,omitempty"`
	Role    string `json:"role,omitempty"`
	Proof   string `json:"proof,omitempty"`
	Source  string `json:"source,omitempty"`
}

// validateFindingFormat enforces the point-first finding template for MCP writes.
// Hard errors reject the tool call; warnings are appended so the agent can self-correct.
func validateFindingFormat(in findingFormatInput) (error, []string) {
	a := narrativeArtifacts(in.Body, in.Detail)
	if !a.validBodyJSON {
		return fmt.Errorf("body must be a JSON array of typed blocks [{type:'text',role,md}|{type:'flow',role,flowId,proof}|{type:'image',role,hash,source,proof}]"), nil
	}
	if confidence := strings.ToLower(strings.TrimSpace(in.Confidence)); confidence != "" && confidence != "tentative" && confidence != "firm" && confidence != "certain" {
		return fmt.Errorf("confidence must be tentative, firm, or certain"), nil
	}

	var warns []string

	// Reject essay dumps in body/detail that ignore the structured fields model.
	if len(strings.TrimSpace(a.text)) >= wallOfTextMin && !reHeading.MatchString(a.text) &&
		strings.TrimSpace(in.Impact) == "" && strings.TrimSpace(in.Why) == "" {
		return fmt.Errorf("finding narrative is a wall of text — set summary + impact + why fields, keep body as typed reproduction/evidence blocks, not a freeform essay"), nil
	}

	hasSummary := strings.TrimSpace(in.Summary) != ""
	hasImpact := strings.TrimSpace(in.Impact) != ""
	hasWhy := strings.TrimSpace(in.Why) != ""
	hasTarget := strings.TrimSpace(in.Target) != ""
	hasFix := strings.TrimSpace(in.Fix) != ""
	hasRetest := strings.TrimSpace(in.Retest) != ""
	hasPoC := a.flowCount+a.imageCount > 0

	// Soft completeness: warn when the write looks substantial but pillars are missing.
	substantial := hasSummary || hasImpact || hasWhy || hasPoC || len(strings.TrimSpace(a.text)) >= 40 ||
		strings.EqualFold(strings.TrimSpace(in.Status), "verified")

	if substantial && !in.Partial {
		if !hasSummary {
			warns = append(warns, "missing summary — state the vulnerable behavior in one or two concise sentences")
		}
		if !hasImpact {
			warns = append(warns, "missing impact — set the impact field (what an attacker gains / CIA consequence)")
		}
		if !hasWhy {
			warns = append(warns, "missing why — set the why field (which security property breaks)")
		}
		if !hasTarget {
			warns = append(warns, "missing target — set the affected host/app/endpoint")
		}
		if !hasPoC {
			warns = append(warns, "missing evidence — attach a captured flow and/or screenshot before report-ready")
		}
		if !hasFix {
			warns = append(warns, "missing fix — describe the control that should be enforced at the failed trust boundary")
		}
		if !hasRetest {
			warns = append(warns, "missing retest — state the expected secure behavior and a negative verification case")
		}
	}

	sev := strings.ToLower(strings.TrimSpace(in.Severity))
	if (sev == "critical" || sev == "high") && a.roles["baseline"] && a.roles["result"] && a.flowCount < 2 {
		warns = append(warns, "this differential Critical/High proof should attach separate baseline and result flows")
	}
	if (sev == "critical" || sev == "high") && substantial && a.flowCount == 0 {
		warns = append(warns, "Critical/High finding has no captured flow — attach one when the behavior occurred in Interseptor so raw proof stays inspectable")
	}
	if hasPoC && a.proofless > 0 {
		warns = append(warns, "evidence is missing a proof annotation — explain exactly what each flow or image establishes")
	}
	if hasPoC && a.imageCount == 0 {
		warns = append(warns, "visual proof recommended — attach a real browser screenshot when it proves the UI result, or a labeled flow preview for HTTP evidence")
	}
	if hasPoC && !hasReproductionRole(a.roles) && len(strings.TrimSpace(a.text)) > 0 {
		warns = append(warns, "reproduction blocks need semantic roles such as observation, action, and result; Before/Action/After is only for differential proof")
	}

	st := strings.ToLower(strings.TrimSpace(in.Status))
	st = strings.ReplaceAll(st, "-", "_")
	if (st == "needs_verification" || st == "needsverification") && strings.TrimSpace(in.VerificationInstructions) == "" {
		warns = append(warns, "status is needs_verification but verificationInstructions is empty — tell the human exactly what to check")
	}

	if reCredMention.MatchString(a.text) && !reCredBoldOrTable.MatchString(a.text) {
		warns = append(warns, "credentials/secrets mentioned — redact values; use a length or digest when it is sufficient evidence")
	}

	return nil, warns
}

func findingTargetsSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 64, "description": "Ordered affected targets; first is primary. Link captured evidence through flow_ids.", "items": obj(map[string]any{
		"url": pt("string"), "methods": map[string]any{"type": "array", "items": pt("string")}, "method": p("string", "single-method input alias"),
		"role": p("string", "identity prerequisite"), "variant": p("string", "parameter, object identifier, or variant"), "relation": p("string", "affected|source|sink|setup|chain"), "note": pt("string"),
		"flow_ids": map[string]any{"type": "array", "items": pt("integer")}, "image_hashes": map[string]any{"type": "array", "items": pt("string")}, "evidenceException": p("string", "documented reason for setup/chain target without its own evidence"),
	}, "url")}
}

func findingProofReviewSchema() map[string]any {
	ref := obj(map[string]any{"flowId": pt("integer"), "hash": pt("string")})
	claims := map[string]any{}
	for _, key := range []string{"authenticated_without_required_factor", "browser_execution", "account_control", "state_change"} {
		claims[key] = obj(map[string]any{"note": p("string", "Reviewer observation, not an automatic verification flag"), "evidence": map[string]any{"type": "array", "maxItems": 16, "items": ref}}, "note", "evidence")
	}
	return obj(map[string]any{"claims": obj(claims), "execution": p("string", "demonstrated|prerequisite_only|not_executed"), "reason": p("string", "required when impact was not demonstrated"), "visual": p("boolean", "true when a real browser screenshot is required to establish the visual claim"), "evidence": obj(map[string]any{"action": ref, "result": ref, "control": ref})})
}

func narrativeArtifacts(body, detail string) findingArtifacts {
	out := findingArtifacts{roles: map[string]bool{}, validBodyJSON: true}
	var parts []string
	if d := strings.TrimSpace(detail); d != "" {
		parts = append(parts, d)
	}
	if strings.TrimSpace(body) == "" {
		out.text = strings.Join(parts, "\n\n")
		return out
	}
	var blocks []findingBodyBlock
	if err := json.Unmarshal([]byte(body), &blocks); err != nil {
		out.validBodyJSON = false
		return out
	}
	for _, b := range blocks {
		role := strings.ToLower(strings.TrimSpace(b.Role))
		if role != "" {
			out.roles[role] = true
		}
		switch strings.ToLower(b.Type) {
		case "text":
			if strings.TrimSpace(b.MD) != "" {
				parts = append(parts, b.MD)
			}
		case "flow":
			out.flowCount++
			if strings.TrimSpace(b.Note) != "" {
				parts = append(parts, b.Note)
			}
			if strings.TrimSpace(b.Proof) != "" {
				parts = append(parts, b.Proof)
			} else {
				out.proofless++
			}
		case "image":
			out.imageCount++
			if strings.TrimSpace(b.Caption) != "" {
				parts = append(parts, b.Caption)
			}
			if strings.TrimSpace(b.Proof) != "" {
				parts = append(parts, b.Proof)
			} else {
				out.proofless++
			}
		}
	}
	out.text = strings.Join(parts, "\n\n")
	return out
}

func hasReproductionRole(roles map[string]bool) bool {
	return roles["observation"] || roles["action"] || roles["result"] || roles["baseline"] || roles["setup"]
}

// formatWarningsBlock renders soft validation warnings for the tool response.
func formatWarningsBlock(warns []string) string {
	if len(warns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nFORMAT WARNING — fix with update_finding / evidence tools (draft OK; fill for report-ready):\n")
	for _, w := range warns {
		b.WriteString("- ")
		b.WriteString(w)
		b.WriteByte('\n')
	}
	return b.String()
}

// prependFindingsSummary adds one-line #id summaries ahead of the raw JSON list.
func prependFindingsSummary(raw string) string {
	var wrap struct {
		Findings []struct {
			store.Finding
			MissingFlowIDs   []int64 `json:"missingFlowIds"`
			MissingFlowCount int     `json:"missingFlowCount"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil || len(wrap.Findings) == 0 {
		return raw
	}
	var b strings.Builder
	b.WriteString("Summary:\n")
	for _, item := range wrap.Findings {
		f := item.Finding
		poc, missing := 0, 0
		seen := map[int64]bool{}
		for _, fl := range f.Flows {
			seen[fl.FlowID] = true
			if fl.Missing {
				missing++
			} else {
				poc++
			}
		}
		for _, bl := range f.Blocks {
			if bl.Type != "flow" || bl.FlowID == 0 || seen[bl.FlowID] {
				continue
			}
			seen[bl.FlowID] = true
			if bl.Missing {
				missing++
			} else {
				poc++
			}
		}
		tags := strings.Join(f.Tags, ",")
		if tags == "" {
			tags = "-"
		}
		stage, images, evidence := "legacy", 0, poc
		if f.Readiness != nil {
			stage = f.Readiness.Stage
			images = f.Readiness.ScreenshotCount
			evidence = f.Readiness.EvidenceCount
			poc = f.Readiness.FlowCount
		}
		if item.MissingFlowCount > 0 {
			missing = item.MissingFlowCount
		} else if len(item.MissingFlowIDs) > 0 {
			missing = len(item.MissingFlowIDs)
		}
		confidence := strings.TrimSpace(f.Confidence)
		if confidence == "" {
			confidence = "-"
		}
		b.WriteString(fmt.Sprintf("#%d · %s · %s · confidence=%s · readiness=%s · tags=%s · evidence=%d (flows=%d visual=%d) · missingFlows=%d · %s\n",
			f.ID, f.Severity, f.Status, confidence, stage, tags, evidence, poc, images, missing, strings.TrimSpace(f.Title)))
	}
	b.WriteByte('\n')
	b.WriteString(raw)
	return b.String()
}
