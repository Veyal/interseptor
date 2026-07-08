package autopwn

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/verify"
)

// issueLite is the subset of a scanner issue the collector needs.
type issueLite struct {
	FlowID   int64  `json:"flowId"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Target   string `json:"target"`
	Detail   string `json:"detail"`
}

// parseIssues extracts issues from a list_issues tool result. It tolerates both a
// bare array and an {"issues":[...]} envelope.
func parseIssues(out string) []issueLite {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	if strings.HasPrefix(out, "[") {
		var arr []issueLite
		if json.Unmarshal([]byte(out), &arr) == nil {
			return arr
		}
		return nil
	}
	var env struct {
		Issues []issueLite `json:"issues"`
	}
	if json.Unmarshal([]byte(out), &env) == nil {
		return env.Issues
	}
	return nil
}

// classOfIssue maps a scanner issue title to a verifier vuln-class label. Falls
// back to a slugged title so an unrecognized issue still carries a class.
func classOfIssue(title string) string {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "sql") && strings.Contains(t, "error"):
		return "sqli-error"
	case strings.Contains(t, "sql") && (strings.Contains(t, "bool") || strings.Contains(t, "blind")):
		return "sqli-boolean"
	case strings.Contains(t, "sql") && strings.Contains(t, "time"):
		return "sqli-time"
	case strings.Contains(t, "sql"):
		return "sqli"
	case strings.Contains(t, "xss") || strings.Contains(t, "cross-site scripting"):
		return "xss-reflected"
	case strings.Contains(t, "ssrf"):
		return "ssrf-blind"
	case strings.Contains(t, "command") || strings.Contains(t, "os-cmd") || strings.Contains(t, "rce"):
		return "cmdi-time"
	case strings.Contains(t, "ssti") || strings.Contains(t, "template"):
		return "ssti"
	case strings.Contains(t, "redirect"):
		return "open-redirect"
	case strings.Contains(t, "traversal") || strings.Contains(t, "path"):
		return "path-traversal"
	default:
		return slug(title)
	}
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "observation"
	}
	return out
}

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	return strings.ReplaceAll(s, old, new)
}

func replaceBytes(b []byte, old, new string) []byte {
	if len(b) == 0 || old == "" {
		return b
	}
	return []byte(strings.ReplaceAll(string(b), old, new))
}

// findingTitle renders a concise title for a verified finding.
func findingTitle(c Candidate) string {
	class := strings.TrimSpace(c.VulnClass)
	if class == "" {
		class = "vulnerability"
	}
	target := c.Target
	if i := strings.Index(target, "?"); i >= 0 {
		target = target[:i]
	}
	return fmt.Sprintf("%s at %s", humanClass(class), target)
}

// humanClass renders a class label a bit more readably for a title.
func humanClass(class string) string {
	switch {
	case strings.HasPrefix(class, "sqli"):
		return "SQL injection"
	case strings.HasPrefix(class, "xss"):
		return "Cross-site scripting"
	case strings.HasPrefix(class, "ssrf"):
		return "Server-side request forgery"
	case strings.HasPrefix(class, "cmdi"):
		return "OS command injection"
	case class == "ssti":
		return "Server-side template injection"
	case class == "open-redirect":
		return "Open redirect"
	case class == "path-traversal":
		return "Path traversal"
	default:
		return strings.ReplaceAll(class, "-", " ")
	}
}

// buildFindingNarrative summarizes the proof into a markdown body for the finding.
func buildFindingNarrative(c Candidate, proof verify.Proof) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** confirmed at `%s`", humanClass(c.VulnClass), c.Target)
	if c.Point != "" {
		fmt.Fprintf(&b, " (injection point: %s)", c.Point)
	}
	b.WriteString(".\n\n")
	if c.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", c.Summary)
	}
	b.WriteString("### Verification\n\n")
	fmt.Fprintf(&b, "- Differential reproduction held **%d** consecutive time(s).\n", proof.ReproCount)
	if proof.AgentVerdict.Result != "" {
		fmt.Fprintf(&b, "- Adversarial verifier verdict: **%s**", proof.AgentVerdict.Result)
		if proof.AgentVerdict.Reasoning != "" {
			fmt.Fprintf(&b, " — %s", proof.AgentVerdict.Reasoning)
		}
		b.WriteString(".\n")
	}
	if proof.OOBToken != "" {
		fmt.Fprintf(&b, "- Out-of-band callback received for token `%s` (blind-class ground truth).\n", proof.OOBToken)
	}
	if proof.HumanConfirm.Confirmed {
		b.WriteString("- Human confirmation obtained before filing.\n")
	}
	fmt.Fprintf(&b, "\nMachine confidence: **%d/100**.\n", proof.Confidence)
	return b.String()
}
