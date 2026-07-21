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
const findingFormatGuide = `REQUIRED FORMAT (point-first; blanks OK on create, fill before report-ready):
1. title — short name (required to create)
2. impact — what an attacker gains / business consequence (field, not a wall of prose)
3. why — why this is a vulnerability (which security property breaks: authz, authn, etc.)
4. target — affected host/app/endpoint
5. PoC / Evidence — ordered body blocks: short step notes + attached flows + screenshots
   - Standard: Before → Action → After (each with a flow and/or image)
   - IDOR/BOLA: our account → other user's id → cross-access with our session
   - Prefer render_flow_preview for HTTP PNG evidence; add_finding_image for real UI shots
   - Critical/High: attach ≥2 flows (Before + After). Never claim success without After proof.
6. Optional: cwe, environment (prod|staging|local), fix/remediation, cvss
7. needs_verification → set verificationInstructions; say "NOT confirmed" when XSS/JS was not proven

Stub create (title only) is allowed. Expand impact/why/target/PoC before considering the finding done.
Do NOT file walls of freeform markdown — put Impact/Why in their fields and keep body as the PoC timeline.`

// wallOfTextMin is the minimum narrative length that triggers a hard reject
// when body text looks like an essay without structure.
const wallOfTextMin = 180

type findingFormatInput struct {
	Severity                 string
	Status                   string
	Title                    string
	Target                   string
	Detail                   string
	Impact                   string
	Why                      string
	Body                     string // JSON blocks array string
	VerificationInstructions string
}

var (
	reHeading         = regexp.MustCompile(`(?m)^#{1,6}\s+\S`)
	reBefore          = regexp.MustCompile(`(?i)\*\*Before\*\*|(?i)\bbefore\b|(?i)\bour account\b`)
	reAfter           = regexp.MustCompile(`(?i)\*\*After\*\*|(?i)\bafter\b|(?i)\bother user(?:'s)?\b|(?i)\bcross-?access\b`)
	reCredMention     = regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|credential|token)\b`)
	reCredBoldOrTable = regexp.MustCompile(`(?i)(\*\*[^*]*(password|passwd|secret|api[_-]?key|credential|token)[^*]*\*\*|\|[^|\n]*(password|passwd|secret|api[_-]?key|credential|token)[^|\n]*\|)`)
)

// validateFindingFormat enforces the point-first finding template for MCP writes.
// Hard errors reject the tool call; warnings are appended so the agent can self-correct.
func validateFindingFormat(in findingFormatInput) (error, []string) {
	text, hasFlow, flowCount, imgCount, ok := narrativeArtifacts(in.Body, in.Detail)
	if !ok {
		return fmt.Errorf("body must be a JSON array of blocks [{type:'text',md}|{type:'flow',flowId,note}|{type:'image',...}]"), nil
	}

	var warns []string

	// Reject essay dumps in body/detail that ignore the structured fields model.
	if len(strings.TrimSpace(text)) >= wallOfTextMin && !reHeading.MatchString(text) &&
		strings.TrimSpace(in.Impact) == "" && strings.TrimSpace(in.Why) == "" {
		return fmt.Errorf("finding narrative is a wall of text — set impact + why fields, keep body as a short PoC timeline (Before→Action→After with add_finding_poc / render_flow_preview), not a freeform essay"), nil
	}

	hasImpact := strings.TrimSpace(in.Impact) != ""
	hasWhy := strings.TrimSpace(in.Why) != ""
	hasTarget := strings.TrimSpace(in.Target) != ""
	hasPoC := hasFlow || imgCount > 0

	// Soft completeness: warn when the write looks substantial but pillars are missing.
	substantial := hasImpact || hasWhy || hasPoC || len(strings.TrimSpace(text)) >= 40 ||
		strings.EqualFold(strings.TrimSpace(in.Status), "verified")

	if substantial {
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
			warns = append(warns, "missing PoC — attach a flow (add_finding_poc) and/or screenshot (render_flow_preview / add_finding_image)")
		}
	}

	sev := strings.ToLower(strings.TrimSpace(in.Severity))
	if (sev == "critical" || sev == "high") && hasPoC && flowCount < 2 {
		warns = append(warns, "Critical/High PoC should attach ≥2 flows (Before + After) — one flow alone rarely proves the change")
	}
	if (sev == "critical" || sev == "high") && substantial && !hasFlow {
		warns = append(warns, "Critical/High finding has no attached flow — call add_finding_poc so the human can open the proof")
	}

	// When PoC notes exist, nudge Before/After labeling.
	if hasFlow && flowCount >= 1 && text != "" {
		if !reBefore.MatchString(text) || !reAfter.MatchString(text) {
			// Also check flow notes inside body.
			notes := flowNotes(in.Body)
			if !reBefore.MatchString(notes) || !reAfter.MatchString(notes) {
				warns = append(warns, "PoC steps should label Before and After (or IDOR: our account / other user / cross-access) in step notes")
			}
		}
	}

	st := strings.ToLower(strings.TrimSpace(in.Status))
	st = strings.ReplaceAll(st, "-", "_")
	if (st == "needs_verification" || st == "needsverification") && strings.TrimSpace(in.VerificationInstructions) == "" {
		warns = append(warns, "status is needs_verification but verificationInstructions is empty — tell the human exactly what to check")
	}

	if reCredMention.MatchString(text) && !reCredBoldOrTable.MatchString(text) {
		warns = append(warns, "credentials/secrets mentioned but not highlighted — put them in a markdown table or **bold** list in a PoC step note")
	}

	return nil, warns
}

func narrativeArtifacts(body, detail string) (text string, hasFlow bool, flowCount, imgCount int, ok bool) {
	var parts []string
	if d := strings.TrimSpace(detail); d != "" {
		parts = append(parts, d)
	}
	if strings.TrimSpace(body) == "" {
		return strings.Join(parts, "\n\n"), false, 0, 0, true
	}
	var blocks []store.FindingBlock
	if err := json.Unmarshal([]byte(body), &blocks); err != nil {
		return "", false, 0, 0, false
	}
	for _, b := range blocks {
		switch strings.ToLower(b.Type) {
		case "text":
			if strings.TrimSpace(b.MD) != "" {
				parts = append(parts, b.MD)
			}
		case "flow":
			hasFlow = true
			flowCount++
			if strings.TrimSpace(b.Note) != "" {
				parts = append(parts, b.Note)
			}
		case "image":
			imgCount++
			if strings.TrimSpace(b.Caption) != "" {
				parts = append(parts, b.Caption)
			}
		}
	}
	return strings.Join(parts, "\n\n"), hasFlow, flowCount, imgCount, true
}

func flowNotes(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	var blocks []store.FindingBlock
	if err := json.Unmarshal([]byte(body), &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if strings.EqualFold(b.Type, "flow") && strings.TrimSpace(b.Note) != "" {
			parts = append(parts, b.Note)
		}
	}
	return strings.Join(parts, "\n")
}

// formatWarningsBlock renders soft validation warnings for the tool response.
func formatWarningsBlock(warns []string) string {
	if len(warns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nFORMAT WARNING — fix with update_finding / add_finding_poc (draft OK; fill for report-ready):\n")
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
		Findings []store.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(raw), &wrap); err != nil || len(wrap.Findings) == 0 {
		return raw
	}
	var b strings.Builder
	b.WriteString("Summary:\n")
	for _, f := range wrap.Findings {
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
		b.WriteString(fmt.Sprintf("#%d · %s · %s · tags=%s · poc=%d · missingFlows=%d · %s\n",
			f.ID, f.Severity, f.Status, tags, poc, missing, strings.TrimSpace(f.Title)))
	}
	b.WriteByte('\n')
	b.WriteString(raw)
	return b.String()
}
