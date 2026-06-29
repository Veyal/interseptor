// Package report renders scanner findings as a human-readable Markdown report,
// ready to paste into a pentest writeup. It is a pure transform over stored
// issues — no I/O — so it is equally callable from the control API and the AI.
package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Veyal/interceptor/internal/store"
)

var sevRank = map[string]int{"Critical": 0, "High": 1, "Medium": 2, "Low": 3, "Info": 4}

// sevOrder is the canonical severity ordering (highest first) used for summary
// lines and tables. Kept in sync with sevRank.
var sevOrder = []string{"Critical", "High", "Medium", "Low", "Info"}

// Findings renders issues as Markdown, grouped by severity (High→Info) with a
// summary line. Output is deterministic for a given set of issues.
func Findings(issues []store.Issue) string {
	var b strings.Builder
	b.WriteString("# Interceptor — Passive Scan Findings\n\n")
	if len(issues) == 0 {
		b.WriteString("_No findings._\n")
		return b.String()
	}

	counts := map[string]int{}
	for _, is := range issues {
		counts[is.Severity]++
	}
	var parts []string
	for _, sev := range sevOrder {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
		}
	}
	noun := "findings"
	if len(issues) == 1 {
		noun = "finding"
	}
	b.WriteString(fmt.Sprintf("_%d %s: %s_\n", len(issues), noun, strings.Join(parts, ", ")))

	sorted := append([]store.Issue(nil), issues...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := rank(sorted[i].Severity), rank(sorted[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if sorted[i].Title != sorted[j].Title {
			return sorted[i].Title < sorted[j].Title
		}
		return sorted[i].Target < sorted[j].Target
	})

	lastSev := ""
	for n, is := range sorted {
		if is.Severity != lastSev {
			b.WriteString("\n## " + orVal(is.Severity, "Info") + "\n")
			lastSev = is.Severity
		}
		fmt.Fprintf(&b, "\n### %d. %s\n", n+1, is.Title)
		if is.Target != "" {
			b.WriteString("- **Target:** `" + code(is.Target) + "`\n")
		}
		if is.Detail != "" {
			b.WriteString("- **Detail:** " + is.Detail + "\n")
		}
		if is.Evidence != "" {
			b.WriteString("- **Evidence:** `" + code(is.Evidence) + "`\n")
		}
		if is.Fix != "" {
			b.WriteString("- **Remediation:** " + is.Fix + "\n")
		}
	}
	return b.String()
}

// Project renders a full engagement report: an executive summary table, then the
// curated Findings (each with its status and attached PoC request/response flows)
// grouped by severity Critical→Info, then an "Excluded — False Positives" section
// for findings marked false_positive, and finally an appendix of the auto-generated
// passive-scan Issues. Findings marked false_positive are kept out of the main body.
// Deterministic for a given input. This is the human-and-AI-shared writeup the
// operator exports.
func Project(findings []store.Finding, issues []store.Issue) string {
	var b strings.Builder
	b.WriteString("# Interceptor — Engagement Report\n\n")
	if len(findings) == 0 && len(issues) == 0 {
		b.WriteString("_No findings recorded._\n")
		return b.String()
	}

	// Partition: false_positive findings are excluded from the main body and
	// listed in a clearly-labeled appendix instead.
	var active, excluded []store.Finding
	for _, f := range findings {
		if f.Status == "false_positive" {
			excluded = append(excluded, f)
		} else {
			active = append(active, f)
		}
	}

	if len(active) == 0 {
		if len(excluded) > 0 {
			b.WriteString("_No active curated findings (all recorded findings were marked false positives)._\n")
		} else {
			b.WriteString("_No curated findings recorded._\n")
		}
	} else {
		// One-line summary of the active findings.
		counts := map[string]int{}
		statusCounts := map[string]int{}
		for _, f := range active {
			counts[orVal(f.Severity, "Info")]++
			statusCounts[orVal(f.Status, "open")]++
		}
		var parts []string
		for _, sev := range sevOrder {
			if counts[sev] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
			}
		}
		noun := "findings"
		if len(active) == 1 {
			noun = "finding"
		}
		b.WriteString(fmt.Sprintf("_%d %s: %s_\n", len(active), noun, strings.Join(parts, ", ")))

		// Executive summary table: counts by severity and by status.
		b.WriteString(summaryTable(len(active), counts, statusCounts))

		sorted := append([]store.Finding(nil), active...)
		sort.SliceStable(sorted, func(i, j int) bool {
			ri, rj := rank(sorted[i].Severity), rank(sorted[j].Severity)
			if ri != rj {
				return ri < rj
			}
			if sorted[i].Title != sorted[j].Title {
				return sorted[i].Title < sorted[j].Title
			}
			return sorted[i].ID < sorted[j].ID
		})

		lastSev := ""
		for n, f := range sorted {
			if f.Severity != lastSev {
				b.WriteString("\n## " + orVal(f.Severity, "Info") + "\n")
				lastSev = f.Severity
			}
			renderFinding(&b, n+1, f)
		}
	}

	// Excluded / false-positive findings, listed but not part of the headline body.
	if len(excluded) > 0 {
		b.WriteString("\n---\n\n## Excluded — False Positives\n\n")
		b.WriteString("_These were reviewed and dismissed as false positives; they are recorded here for completeness only._\n")
		sortedFP := append([]store.Finding(nil), excluded...)
		sort.SliceStable(sortedFP, func(i, j int) bool {
			ri, rj := rank(sortedFP[i].Severity), rank(sortedFP[j].Severity)
			if ri != rj {
				return ri < rj
			}
			if sortedFP[i].Title != sortedFP[j].Title {
				return sortedFP[i].Title < sortedFP[j].Title
			}
			return sortedFP[i].ID < sortedFP[j].ID
		})
		for n, f := range sortedFP {
			fmt.Fprintf(&b, "\n### %d. %s\n", n+1, f.Title)
			if f.Severity != "" {
				b.WriteString("- **Severity:** " + f.Severity + "\n")
			}
			if f.Target != "" {
				b.WriteString("- **Target:** `" + code(f.Target) + "`\n")
			}
		}
	}

	if len(issues) > 0 {
		b.WriteString("\n---\n\n## Appendix: Passive Scan Issues\n\n")
		b.WriteString(stripTitle(Findings(issues)))
	}
	return b.String()
}

// summaryTable renders the executive-summary Markdown table: one row per
// severity present (highest first) with its count, then a status breakdown.
// total is the active finding count; counts/statusCounts are keyed by label.
func summaryTable(total int, counts, statusCounts map[string]int) string {
	var b strings.Builder
	b.WriteString("\n## Summary\n\n")
	b.WriteString("| Severity | Count |\n")
	b.WriteString("| --- | --- |\n")
	for _, sev := range sevOrder {
		if counts[sev] > 0 {
			fmt.Fprintf(&b, "| %s | %d |\n", sev, counts[sev])
		}
	}
	fmt.Fprintf(&b, "| **Total** | **%d** |\n", total)

	// Status breakdown, in a stable, readable order.
	b.WriteString("\n| Status | Count |\n")
	b.WriteString("| --- | --- |\n")
	for _, st := range []string{"verified", "open", "wont_fix", "fixed"} {
		if statusCounts[st] > 0 {
			fmt.Fprintf(&b, "| %s | %d |\n", st, statusCounts[st])
		}
	}
	// Any other (non-standard) statuses, sorted for determinism.
	var extra []string
	for st := range statusCounts {
		switch st {
		case "verified", "open", "wont_fix", "fixed":
		default:
			extra = append(extra, st)
		}
	}
	sort.Strings(extra)
	for _, st := range extra {
		fmt.Fprintf(&b, "| %s | %d |\n", st, statusCounts[st])
	}
	return b.String()
}

// renderFinding writes one finding's section (heading, metadata, narrative body
// or legacy detail/evidence/PoC-flows fallback) to b.
func renderFinding(b *strings.Builder, n int, f store.Finding) {
	fmt.Fprintf(b, "\n### %d. %s\n", n, f.Title)
	if f.Status != "" {
		b.WriteString("- **Status:** " + f.Status + "\n")
	}
	if f.Target != "" {
		b.WriteString("- **Target:** `" + code(f.Target) + "`\n")
	}
	if f.Cvss != "" {
		b.WriteString("- **CVSS:** " + f.Cvss + "\n")
	}
	if f.Impact != "" {
		b.WriteString("- **Impact:** " + f.Impact + "\n")
	}
	b.WriteString("\n")
	// Render interleaved narrative body (text + PoC flows in author's order).
	if len(f.Blocks) > 0 {
		for _, bl := range f.Blocks {
			if bl.Type == "text" && bl.MD != "" {
				b.WriteString(bl.MD + "\n\n")
			} else if bl.Type == "flow" {
				if bl.Missing {
					// PoC flow was purged from history (prune_history / GC) — note
					// that the evidence is gone instead of an empty/broken quote.
					line := fmt.Sprintf("> ⚠ PoC flow #%d — evidence no longer in history", bl.FlowID)
					if bl.Note != "" {
						line += " — " + bl.Note
					}
					b.WriteString(line + "\n>\n")
					continue
				}
				line := fmt.Sprintf("> `%s %s%s`", orVal(bl.Method, "?"), code(bl.Host), code(bl.Path))
				if bl.Status > 0 {
					line += fmt.Sprintf(" → **%d**", bl.Status)
				}
				if bl.Note != "" {
					line += " — " + bl.Note
				}
				b.WriteString(line + "\n>\n")
			}
		}
		return
	}
	// Legacy fallback: separate detail / evidence / flows sections.
	if f.Detail != "" {
		b.WriteString(f.Detail + "\n\n")
	}
	if f.Evidence != "" {
		b.WriteString("**Evidence:** " + f.Evidence + "\n\n")
	}
	if len(f.Flows) > 0 {
		b.WriteString("**PoC flows:**\n")
		for _, fl := range f.Flows {
			if fl.Missing {
				line := fmt.Sprintf("- ⚠ PoC flow #%d — evidence no longer in history", fl.FlowID)
				if fl.Note != "" {
					line += " — " + code(fl.Note)
				}
				b.WriteString(line + "\n")
				continue
			}
			line := fmt.Sprintf("- `%s %s%s`", orVal(fl.Method, "GET"), code(fl.Host), code(fl.Path))
			if fl.Status > 0 {
				line += fmt.Sprintf(" → %d", fl.Status)
			}
			if fl.Note != "" {
				line += " — " + code(fl.Note)
			}
			b.WriteString(line + "\n")
		}
	}
}

// stripTitle drops the leading "# …" heading line from a Findings() report so it
// nests cleanly under the appendix heading.
func stripTitle(md string) string {
	if strings.HasPrefix(md, "# ") {
		if i := strings.IndexByte(md, '\n'); i >= 0 {
			return strings.TrimLeft(md[i+1:], "\n")
		}
	}
	return md
}

func rank(sev string) int {
	if r, ok := sevRank[sev]; ok {
		return r
	}
	return 99
}

// code makes s safe inside an inline-code span: no backticks, single line.
func code(s string) string {
	s = strings.ReplaceAll(s, "`", "'")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", "")
}

func orVal(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
