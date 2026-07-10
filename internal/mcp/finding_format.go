package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// findingFormatGuide is the REQUIRED finding narrative format. Surfaced in
// initialize instructions and create_finding / update_finding tool descriptions
// so agents stop filing walls of text (see GitHub #6).
const findingFormatGuide = `REQUIRED FORMAT for finding body text blocks:
1. ## Summary — 1–2 sentences; FIRST sentence states the IMPACT (what an attacker gains / CIA consequence), not the mechanism
2. ## Evidence — request/response details; credentials/secrets MUST be a markdown table or bold bullet list (e.g. **password**: s3cret)
3. ## Impact — concrete confidentiality/integrity/availability consequence
4. Attach PoC flows via add_finding_poc (or body flow blocks); each finding MUST say which flow shows the proof
5. If JS/XSS execution was NOT confirmed, say "NOT confirmed" explicitly
6. If the human must check something: status=needs_verification + verificationInstructions, and add ## Needs Verification

Write as interleaved notebook blocks (text → flow → text → image → flow), not a wall of prose. Prefer attaching the actual flow/image over pasting raw HTTP into evidence.`

// wallOfTextMin is the minimum narrative length that triggers a hard reject
// when no markdown ## heading is present. Short create_finding openings
// (what/where, PoC next) stay allowed.
const wallOfTextMin = 180

type findingFormatInput struct {
	Severity                 string
	Status                   string
	Detail                   string
	Impact                   string
	Body                     string // JSON blocks array string
	VerificationInstructions string
}

var (
	reHeading     = regexp.MustCompile(`(?m)^#{1,6}\s+\S`)
	reSummary     = regexp.MustCompile(`(?im)^##\s+Summary\b`)
	reEvidence    = regexp.MustCompile(`(?im)^##\s+Evidence\b`)
	reImpactHead  = regexp.MustCompile(`(?im)^##\s+Impact\b`)
	reCredMention = regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|credential|token)\b`)
	reCredBoldOrTable = regexp.MustCompile(`(?i)(\*\*[^*]*(password|passwd|secret|api[_-]?key|credential|token)[^*]*\*\*|\|[^|\n]*(password|passwd|secret|api[_-]?key|credential|token)[^|\n]*\|)`)
)

// validateFindingFormat enforces the structured finding template for MCP writes.
// Hard errors reject the tool call; warnings are appended to a successful response
// so the agent can self-correct with update_finding / add_finding_poc.
func validateFindingFormat(in findingFormatInput) (error, []string) {
	text, hasFlow, ok := narrativeText(in.Body, in.Detail)
	if !ok {
		return fmt.Errorf("body must be a JSON array of blocks [{type:'text',md}|{type:'flow',flowId,note}|{type:'image',...}]"), nil
	}

	var warns []string

	if len(strings.TrimSpace(text)) >= wallOfTextMin && !reHeading.MatchString(text) {
		return fmt.Errorf("finding narrative is a wall of text — use sectioned markdown per REQUIRED FORMAT: ## Summary (impact-first), ## Evidence, ## Impact. Then attach PoC flows with add_finding_poc"), nil
	}

	substantial := len(strings.TrimSpace(text)) >= 80
	if substantial && reHeading.MatchString(text) {
		if !reSummary.MatchString(text) {
			warns = append(warns, "missing ## Summary (first sentence must state the IMPACT, not the mechanism)")
		}
		if !reEvidence.MatchString(text) {
			warns = append(warns, "missing ## Evidence section")
		}
	}

	sev := strings.ToLower(strings.TrimSpace(in.Severity))
	needsImpact := sev == "critical" || sev == "high" || sev == "medium"
	hasImpact := strings.TrimSpace(in.Impact) != "" || reImpactHead.MatchString(text)
	if needsImpact && substantial && !hasImpact {
		warns = append(warns, "missing impact — set the impact field and/or add a ## Impact section with a concrete CIA consequence")
	}

	if (sev == "critical" || sev == "high") && substantial && !hasFlow {
		warns = append(warns, "Critical/High finding has no attached flow — call add_finding_poc (or include a body flow block) so the human can open the proof")
	}

	st := strings.ToLower(strings.TrimSpace(in.Status))
	st = strings.ReplaceAll(st, "-", "_")
	if (st == "needs_verification" || st == "needsverification") && strings.TrimSpace(in.VerificationInstructions) == "" {
		warns = append(warns, "status is needs_verification but verificationInstructions is empty — tell the human exactly what to check")
	}

	if reCredMention.MatchString(text) && !reCredBoldOrTable.MatchString(text) {
		warns = append(warns, "credentials/secrets mentioned but not highlighted — put them in a markdown table or **bold** list under ## Evidence")
	}

	return nil, warns
}

// narrativeText extracts markdown from body JSON blocks (plus legacy detail)
// and whether any flow block is present. ok=false means body was non-empty
// but not valid JSON blocks.
func narrativeText(body, detail string) (text string, hasFlow bool, ok bool) {
	var parts []string
	if d := strings.TrimSpace(detail); d != "" {
		parts = append(parts, d)
	}
	if strings.TrimSpace(body) == "" {
		return strings.Join(parts, "\n\n"), false, true
	}
	var blocks []store.FindingBlock
	if err := json.Unmarshal([]byte(body), &blocks); err != nil {
		return "", false, false
	}
	for _, b := range blocks {
		switch strings.ToLower(b.Type) {
		case "text":
			if strings.TrimSpace(b.MD) != "" {
				parts = append(parts, b.MD)
			}
		case "flow":
			hasFlow = true
		}
	}
	return strings.Join(parts, "\n\n"), hasFlow, true
}

// formatWarningsBlock renders soft validation warnings for the tool response.
func formatWarningsBlock(warns []string) string {
	if len(warns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nFORMAT WARNING — fix with update_finding / add_finding_poc:\n")
	for _, w := range warns {
		b.WriteString("- ")
		b.WriteString(w)
		b.WriteByte('\n')
	}
	return b.String()
}
