package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/redact"
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
4. Proof of concept — every filed finding tells this story, in order, so a non-technical reader and a developer can both follow it. This is the Differential proof. Do not pick a shorter role sequence.
   - baseline: what the application normally does for an ordinary authorized user. Attach that captured request when it exists, and say it in plain language.
   - action: what we changed or sent to trigger the issue. Name the exact difference from the normal request.
   - result: what the response or application did differently, and the practical impact of that change.
   - Put the plain-language explanation in each block's proof field. Do not paste raw HTTP into text.
   - setup, control, observation, and retest may be added after those three. They do not replace them.
   - When the finding lists more than one affected target, repeat this proof for each affected target you tested. Link that proof with the target's flow_ids or image_hashes. One capture may be linked to several targets when it truly proves each of them. A target you did not test may be listed without its own proof; do not invent a request to fill it.
   - Readiness still checks an evidenced action, observed result, and a negative or normal control. The baseline is the normal behavior; a separate control block is still required before report-ready.
5. Evidence — ALWAYS attach evidence. Every finding needs at least one captured flow or image, attached in the same run that files it. A title-only stub is an intermediate step, never a finished finding: create it, then attach, before you move on. A claim with nothing behind it is not a finding.
   - Any form satisfies this: an attached flow (add_finding_poc), a real browser or device screenshot (add_finding_image), or a generated HTTP preview (render_flow_preview). Attaching something is never optional. What each form is worth as proof is a separate question, answered below.
   - If you could not capture anything, say why in proofReview.reason and keep status=needs_verification. Do not file the claim bare and do not invent a request.
   - Set proofReview.visual=true for browser/visual claims; attach a real browser screenshot of the observed result
   - If the request exists in Interseptor, attach its flow so raw evidence remains inspectable
   - Use render_flow_preview for a generated HTTP image; it is labeled as a flow preview, not a browser screenshot
   - Capability checklist before report-ready (readiness reports each separately): ACTION the exact triggering request/flow; RESULT the observed outcome; CONTROL a negative or normal-behavior request that distinguishes vulnerable behavior; VISUAL a real browser/device screenshot for browser findings
   - Operator-uploaded images count as visual proof only after a reviewer classifies them (classify_finding_image); generated previews and evidence renders never qualify
   - Use render_evidence for generated Intruder/authz/diff/waterfall/chain images (source=evidence_render, sourceRef kept); they are drawn from recorded data and are not browser proof, so a real screenshot via add_finding_image is still required for visual claims
   - Test the claimed impact end to end when safe and in scope; if a step was not executed, set proofReview.execution=not_executed with proofReview.reason, keep status=needs_verification, or narrow the impact
   - WebSocket evidence: ws_send records the handshake and every frame as a flow and returns its flowId — cite it as an ordinary flow block (including a rejected-handshake or invalid-token control); annotate frames with set_ws_frame_note
6. Fix and retest — remediation plus the expected secure behavior / negative test in retest
7. Review — confidence=tentative|firm|certain; proofReview.execution=demonstrated only for impact actually observed
   - A permissive response or reachable prerequisite alone does not establish the claimed impact
   - Otherwise use prerequisite_only or not_executed with an explicit proofReview.reason; status stays needs_verification
   - Narrow impact to what evidence establishes. Mark unproven execution as "NOT confirmed". Keep verificationInstructions for the remaining review
   - Record claims[] verdicts (confirmed|partially_confirmed|not_reproduced|refuted), notExecuted[] for authorised requests deliberately not sent, and relatedFindings[] for chains/duplicates instead of burying them in prose
   - Keep secrets redacted; a length or digest can establish equality without publishing the value. Call redact_value to get {len, sha256_prefix, kind} and a "[redacted ...]" form to paste; writes that contain a JWT, AIza key, $2b$ hash or Bearer token produce a warning
   - For integrity evidence, describe only the bounded, reversible observed change and preserve its flow

Use structured blocks arrays when available; legacy body JSON remains accepted. Stub create (title only) is allowed.
Do NOT file walls of freeform markdown. Put summary/impact/why/fix/retest in their fields, reproduction in text blocks, and raw proof in flow/image blocks.

HOUSE STYLE: the reader is a working pentester triaging a list. They know the vulnerability class. Say where it is and what you proved, nothing else.
- title <=70 chars: "<flaw> on <endpoint or parameter>"; no "Vulnerability:" prefix, no severity word
- summary: ONE sentence <=140 chars: "<METHOD path template> <what happens> to <role>."; never restate the title
- impact: ONE sentence <=120 chars, the concrete attacker gain; why <=100 chars, the failed control (omit when the CWE says it)
- blocks[].text <=100 chars, imperative, ~6 blocks at most; fix <=160 chars, imperative; retest: ONE sentence <=100 chars, the observable pass condition
NEVER WRITE: background or theory ("IDOR occurs when..."); restating the title in the summary; hedging ("could potentially", "may be possible", "it appears"), state what the evidence shows; narration ("we then proceeded to"); raw HTTP, headers or payloads in prose, attach the flow; severity adjectives (severity is a field); anything already visible in an attached flow or screenshot; padding an empty field, leave it blank and readiness reports the gap.
These are targets; the hard reject below stays 180.

RESPONSES: a line starting "error:" is a hard rejection and nothing was written; fix the named field and resend.
A "warning:" line (under "FORMAT WARNINGS") is advisory and non-blocking: the write succeeded and a draft may stay as is.
NARRATIVE LIMIT: 180 characters of unstructured text per field (detail, and the text of blocks/body) unless impact/why are set or the text uses headings. detail is DEPRECATED; prefer blocks.`

// narrativeCharLimit is the published per-field narrative budget. Unstructured
// text of this many characters or more in detail, or in the text of blocks/body,
// is rejected unless impact/why are set or the text uses headings.
const narrativeCharLimit = 180

// detailFieldDescription is the schema text for the legacy detail field.
var detailFieldDescription = fmt.Sprintf("DEPRECATED legacy opening text; use summary/impact/why plus blocks. Unstructured text of %d characters or more is rejected unless impact and why are set", narrativeCharLimit)

// errorf builds a hard rejection. The "error:" prefix is the structural marker
// that distinguishes a rejection (nothing written) from a "warning:" (advisory).
func errorf(format string, args ...any) error {
	return fmt.Errorf("error: "+format, args...)
}

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
				"source":       map[string]any{"type": "string", "description": "captured_flow|browser_screenshot|flow_preview|evidence_render|generated_image|operator_upload|tool_output|other"},
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
	Partial                  bool            // update calls may omit already-populated envelope fields
	Targets                  json.RawMessage // ordered targets, when this write includes them
}

var (
	reHeading         = regexp.MustCompile(`(?m)^#{1,6}\s+\S`)
	reCredMention     = regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|credential|token)\b`)
	reCredBoldOrTable = regexp.MustCompile(`(?i)(\*\*[^*]*(password|passwd|secret|api[_-]?key|credential|token)[^*]*\*\*|\|[^|\n]*(password|passwd|secret|api[_-]?key|credential|token)[^|\n]*\|)`)
)

type findingArtifacts struct {
	text          string
	detailText    string
	blocksText    string
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
		return errorf("body (or blocks) must be a JSON array of typed blocks [{type:'text',role,md}|{type:'flow',role,flowId,proof}|{type:'image',role,hash,source,proof}]; the value sent is not valid JSON of that shape"), nil
	}
	if confidence := strings.ToLower(strings.TrimSpace(in.Confidence)); confidence != "" && confidence != "tentative" && confidence != "firm" && confidence != "certain" {
		return errorf("confidence must be one of tentative, firm, certain; got %q", in.Confidence), nil
	}

	var warns []string

	// Reject essay dumps in body/detail that ignore the structured fields model.
	if strings.TrimSpace(in.Impact) == "" && strings.TrimSpace(in.Why) == "" {
		for _, field := range []struct{ name, text string }{{"detail", a.detailText}, {"blocks", a.blocksText}} {
			text := strings.TrimSpace(field.text)
			if len(text) >= narrativeCharLimit && !reHeading.MatchString(text) {
				return errorf("%s is a wall of text (%d characters; limit is %d per field). Set the summary, impact and why fields and keep blocks to short typed reproduction/evidence entries; detail is deprecated", field.name, len(text), narrativeCharLimit), nil
			}
		}
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
		warns = append(warns, "visual proof recommended — attach a real browser screenshot when it proves the UI result, or a labeled flow preview or evidence render for HTTP/recorded data")
	}
	if hasPoC && !hasReproductionRole(a.roles) && len(strings.TrimSpace(a.text)) > 0 {
		warns = append(warns, "reproduction blocks need semantic roles; the proof of concept uses baseline, action, and result")
	}
	if substantial && (!a.roles["baseline"] || !a.roles["action"] || !a.roles["result"]) {
		warns = append(warns, "proof of concept must describe what the application normally does (role baseline), what was changed to trigger the issue (role action), and what the response or impact changed to (role result)")
	}
	warns = append(warns, affectedTargetProofWarnings(in.Targets)...)

	st := strings.ToLower(strings.TrimSpace(in.Status))
	st = strings.ReplaceAll(st, "-", "_")
	if (st == "needs_verification" || st == "needsverification") && strings.TrimSpace(in.VerificationInstructions) == "" {
		warns = append(warns, "status is needs_verification but verificationInstructions is empty — tell the human exactly what to check")
	}

	if reCredMention.MatchString(a.text) && !reCredBoldOrTable.MatchString(a.text) {
		warns = append(warns, "credentials/secrets mentioned — redact values; use a length or digest when it is sufficient evidence")
	}

	warns = append(warns, secretLintWarnings(in, a)...)

	return nil, warns
}

// secretLintWarnings flags probable secrets in finding text. Warnings carry the
// field, kind, length and the redacted form, never the value.
func secretLintWarnings(in findingFormatInput, a findingArtifacts) []string {
	fields := []struct{ name, text string }{
		{"title", in.Title}, {"summary", in.Summary}, {"impact", in.Impact}, {"why", in.Why},
		{"fix", in.Fix}, {"retest", in.Retest}, {"detail", a.detailText}, {"blocks", a.blocksText},
		{"verificationInstructions", in.VerificationInstructions},
	}
	var warns []string
	for _, f := range fields {
		for _, hit := range redact.Scan(f.text) {
			warns = append(warns, fmt.Sprintf("probable secret in %s (%s, %d chars) — do not publish the value; get its length and digest with redact_value and write %s instead", f.name, hit.Kind, hit.Len, hit.Suggest))
		}
	}
	return warns
}

func findingTargetsArg(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

func affectedTargetProofWarnings(raw json.RawMessage) []string {
	if strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return nil
	}
	var targets []struct {
		URL               string   `json:"url"`
		Relation          string   `json:"relation"`
		FlowIDs           []int64  `json:"flow_ids"`
		ImageHashes       []string `json:"image_hashes"`
		EvidenceException string   `json:"evidenceException"`
	}
	if err := json.Unmarshal(raw, &targets); err != nil || len(targets) < 2 {
		return nil
	}
	var warns []string
	for i, t := range targets {
		rel := strings.ToLower(strings.TrimSpace(t.Relation))
		if rel == "" {
			rel = "affected"
		}
		if (rel == "setup" || rel == "chain") && strings.TrimSpace(t.EvidenceException) != "" {
			continue
		}
		if len(t.FlowIDs) > 0 || len(t.ImageHashes) > 0 {
			continue
		}
		url := strings.TrimSpace(t.URL)
		if url == "" {
			url = "unspecified"
		}
		warns = append(warns, fmt.Sprintf("affected target %d (%s) has no linked proof — link its captured flow or screenshot when you tested it; do not invent a request for a target you did not test", i+1, url))
	}
	return warns
}

func findingTargetsSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 64, "description": "Ordered affected targets; first is primary. Link proof for each target you tested through flow_ids or image_hashes. A target you did not test may omit its own proof.", "items": obj(map[string]any{
		"url": pt("string"), "methods": map[string]any{"type": "array", "items": pt("string")}, "method": p("string", "single-method input alias"),
		"role": p("string", "identity prerequisite"), "variant": p("string", "parameter, object identifier, or variant"), "relation": p("string", "affected|source|sink|setup|chain"), "note": pt("string"),
		"flow_ids": map[string]any{"type": "array", "items": pt("integer")}, "image_hashes": map[string]any{"type": "array", "items": pt("string")}, "evidenceException": p("string", "documented reason for setup/chain target without its own evidence"),
	}, "url")}
}

func findingClaimsSchema() map[string]any {
	ref := obj(map[string]any{"flowId": pt("integer"), "hash": pt("string")})
	return map[string]any{"type": "array", "maxItems": 64, "description": "Per-claim verdicts so a withdrawn claim is never lost in prose. Replaces the stored list.", "items": obj(map[string]any{
		"id": p("string", "stable short id, unique within the finding"), "statement": p("string", "the claim being judged"),
		"verdict":  p("string", "confirmed|partially_confirmed|not_reproduced|refuted"),
		"evidence": map[string]any{"type": "array", "maxItems": 16, "items": ref}, "note": pt("string"),
	}, "id", "statement", "verdict")}
}

func findingNotExecutedSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 64, "description": "Authorised requests deliberately NOT sent (chose not to, as opposed to could not). Rendered in their own report section. Replaces the stored list.", "items": obj(map[string]any{
		"method": pt("string"), "target": pt("string"), "reason": p("string", "why it was not sent"),
		"risk": p("string", "what sending it would have affected"), "requiresAuthorisation": p("boolean", "true when explicit authorisation is needed before sending"),
	}, "method", "target", "reason")}
}

func findingRelatedSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 64, "description": "Links to other findings in this project (ids must exist). Shown from both ends; duplicate links do not demand duplicate evidence. Replaces the stored list.", "items": obj(map[string]any{
		"id": pt("integer"), "relation": p("string", "enables|enabled_by|chain|duplicate|escalates"),
	}, "id", "relation")}
}

func findingProofReviewSchema() map[string]any {
	ref := obj(map[string]any{"flowId": pt("integer"), "hash": pt("string")})
	claims := map[string]any{}
	for _, key := range []string{"authenticated_without_required_factor", "browser_execution", "account_control", "state_change"} {
		claims[key] = obj(map[string]any{"note": p("string", "Reviewer observation, not an automatic verification flag"), "evidence": map[string]any{"type": "array", "maxItems": 16, "items": ref}}, "note", "evidence")
	}
	return obj(map[string]any{"claims": obj(claims), "execution": p("string", "demonstrated|prerequisite_only|not_executed"), "reason": p("string", "required when impact was not demonstrated"), "visual": p("boolean", "true when a real browser screenshot is required to establish the visual claim"), "severityOverride": p("string", "documented reason severity deliberately differs from the calculated CVSS rating; without it a mismatch is rejected"), "evidence": obj(map[string]any{"action": ref, "result": ref, "control": ref})})
}

func narrativeArtifacts(body, detail string) findingArtifacts {
	out := findingArtifacts{roles: map[string]bool{}, validBodyJSON: true}
	var parts []string
	var blockParts []string
	if d := strings.TrimSpace(detail); d != "" {
		parts = append(parts, d)
		out.detailText = d
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
				blockParts = append(blockParts, b.MD)
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
	out.blocksText = strings.Join(blockParts, "\n\n")
	return out
}

func hasReproductionRole(roles map[string]bool) bool {
	return roles["observation"] || roles["action"] || roles["result"] || roles["baseline"] || roles["setup"]
}

// formatWarningsBlock renders soft validation warnings for the tool response.
// Warnings never block: the write has already succeeded when they are shown.
func formatWarningsBlock(warns []string) string {
	if len(warns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nFORMAT WARNINGS (non-blocking; the write succeeded; fix with update_finding / evidence tools, draft OK, fill for report-ready):\n")
	for _, w := range warns {
		b.WriteString("- warning: ")
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
