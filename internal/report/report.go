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

var sevRank = map[string]int{"High": 0, "Medium": 1, "Low": 2, "Info": 3}

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
	for _, sev := range []string{"High", "Medium", "Low", "Info"} {
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
