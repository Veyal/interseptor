package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// Rendering for the structured finding fields: claims, deliberately-not-executed
// requests, related-finding chains, and the optional revision audit trail.

func findingTitles(findings []store.Finding) map[int64]string {
	out := make(map[int64]string, len(findings))
	for _, f := range findings {
		out[f.ID] = f.Title
	}
	return out
}

func findingLabel(id int64, titles map[int64]string) string {
	if t, ok := titles[id]; ok && t != "" {
		return fmt.Sprintf("#%d %s", id, sanitizeLine(t))
	}
	return fmt.Sprintf("#%d", id)
}

// withInverseRelations returns copies whose RelatedFindings also carry the
// inverse of every link pointing at them, so a link reads the same from both ends.
func withInverseRelations(findings []store.Finding) []store.Finding {
	out := append([]store.Finding(nil), findings...)
	index := make(map[int64]int, len(out))
	for i, f := range out {
		index[f.ID] = i
	}
	has := func(f store.Finding, r store.FindingRelation) bool {
		for _, existing := range f.RelatedFindings {
			if existing == r {
				return true
			}
		}
		return false
	}
	for _, f := range findings {
		for _, r := range f.RelatedFindings {
			j, ok := index[r.ID]
			if !ok {
				continue
			}
			inverse := store.FindingRelation{ID: f.ID, Relation: store.InverseFindingRelation(r.Relation)}
			if !has(out[j], inverse) {
				out[j].RelatedFindings = append(append([]store.FindingRelation(nil), out[j].RelatedFindings...), inverse)
			}
		}
	}
	return out
}

func renderClaims(b *strings.Builder, f store.Finding) {
	if len(f.Claims) == 0 {
		return
	}
	b.WriteString("**Claims:**\n\n")
	for _, c := range f.Claims {
		status := strings.ToUpper(strings.ReplaceAll(c.Verdict, "_", " "))
		if store.IsWithdrawnVerdict(c.Verdict) {
			status = "WITHDRAWN (" + strings.ReplaceAll(c.Verdict, "_", " ") + ")"
		}
		line := fmt.Sprintf("- [%s] %s", status, sanitizeLine(c.Statement))
		var refs []string
		for _, ref := range c.Evidence {
			label := code(ref.Hash)
			if ref.FlowID > 0 {
				label = fmt.Sprintf("flow #%d", ref.FlowID)
			}
			if ref.Missing {
				label += " (missing)"
			}
			refs = append(refs, label)
		}
		if len(refs) > 0 {
			line += " (evidence: " + strings.Join(refs, ", ") + ")"
		}
		if c.Note != "" {
			line += " — " + sanitizeLine(c.Note)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

func notExecutedLine(n store.FindingNotExecuted) string {
	line := fmt.Sprintf("`%s %s` — %s", code(n.Method), code(n.Target), sanitizeLine(n.Reason))
	if n.Risk != "" {
		line += " Risk: " + sanitizeLine(n.Risk)
	}
	if n.RequiresAuthorisation {
		line += " (authorisation required)"
	}
	return line
}

func renderFindingNotExecuted(b *strings.Builder, f store.Finding) {
	if len(f.NotExecuted) == 0 {
		return
	}
	b.WriteString("**Not executed (deliberately):** listed in the Requests Deliberately Not Executed section\n\n")
	for _, n := range f.NotExecuted {
		b.WriteString("- " + notExecutedLine(n) + "\n")
	}
	b.WriteString("\n")
}

func renderRelated(b *strings.Builder, f store.Finding, titles map[int64]string) {
	if len(f.RelatedFindings) == 0 {
		return
	}
	b.WriteString("**Related findings:**\n\n")
	for _, r := range f.RelatedFindings {
		b.WriteString(fmt.Sprintf("- %s %s\n", strings.ReplaceAll(r.Relation, "_", " "), findingLabel(r.ID, titles)))
	}
	b.WriteString("\n")
}

// renderNotExecutedSection lists every deliberately-not-executed request, so a
// reviewer sees the destructive requests that were NOT sent in one place.
func renderNotExecutedSection(b *strings.Builder, findings []store.Finding) {
	var any bool
	for _, f := range findings {
		any = any || len(f.NotExecuted) > 0
	}
	if !any {
		return
	}
	b.WriteString("\n---\n\n## Requests Deliberately Not Executed\n\n")
	b.WriteString("_These requests were authorised or reachable but intentionally not sent. \"Not executed\" is a choice, not a failure to reproduce._\n\n")
	for _, f := range findings {
		for _, n := range f.NotExecuted {
			b.WriteString(fmt.Sprintf("- %s — finding #%d %s\n", notExecutedLine(n), f.ID, sanitizeLine(f.Title)))
		}
	}
}

type chainEdge struct {
	from, to int64
	label    string
}

func canonicalEdges(findings []store.Finding) []chainEdge {
	seen := map[chainEdge]bool{}
	var edges []chainEdge
	add := func(e chainEdge) {
		if !seen[e] {
			seen[e] = true
			edges = append(edges, e)
		}
	}
	for _, f := range findings {
		for _, r := range f.RelatedFindings {
			switch r.Relation {
			case "enables", "escalates":
				add(chainEdge{f.ID, r.ID, r.Relation})
			case "enabled_by":
				add(chainEdge{r.ID, f.ID, "enables"})
			default: // chain, duplicate: symmetric, so order the pair
				a, c := f.ID, r.ID
				if a > c {
					a, c = c, a
				}
				add(chainEdge{a, c, r.Relation})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		if edges[i].to != edges[j].to {
			return edges[i].to < edges[j].to
		}
		return edges[i].label < edges[j].label
	})
	return edges
}

// renderChainsSection renders each linked group of findings as a readable chain.
func renderChainsSection(b *strings.Builder, findings []store.Finding) {
	edges := canonicalEdges(findings)
	if len(edges) == 0 {
		return
	}
	titles := findingTitles(findings)
	parent := map[int64]int64{}
	var find func(int64) int64
	find = func(x int64) int64 {
		if p, ok := parent[x]; ok && p != x {
			parent[x] = find(p)
			return parent[x]
		}
		parent[x] = x
		return x
	}
	for _, e := range edges {
		parent[find(e.from)] = find(e.to)
	}
	groups := map[int64][]chainEdge{}
	for _, e := range edges {
		root := find(e.from)
		groups[root] = append(groups[root], e)
	}
	roots := make([]int64, 0, len(groups))
	for r := range groups {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool { return groups[roots[i]][0].from < groups[roots[j]][0].from })
	b.WriteString("\n---\n\n## Finding Chains\n\n")
	for i, root := range roots {
		fmt.Fprintf(b, "**Chain %d**\n\n", i+1)
		for _, e := range groups[root] {
			b.WriteString(fmt.Sprintf("- %s **%s** %s\n", findingLabel(e.from, titles), e.label, findingLabel(e.to, titles)))
		}
		b.WriteString("\n")
	}
}

// AuditTrail renders an optional appendix of finding revision history. It lists
// who/when/which fields changed and the stated reason — never revision
// snapshots, so values held only in the secured project store are not exported.
func AuditTrail(findings []store.Finding, revisions map[int64][]store.FindingRevision) string {
	var b strings.Builder
	for _, f := range findings {
		revs := revisions[f.ID]
		if len(revs) == 0 {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\n---\n\n## Appendix: Audit Trail\n\n_Field names and stated reasons only; values stay in the project store._\n")
		}
		fmt.Fprintf(&b, "\n### Finding #%d %s\n\n", f.ID, sanitizeLine(f.Title))
		sorted := append([]store.FindingRevision(nil), revs...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
		for _, r := range sorted {
			line := fmt.Sprintf("- %s — %s by %s via %s", time.UnixMilli(r.TS).UTC().Format("2006-01-02 15:04 UTC"), sanitizeLine(r.Action), sanitizeLine(r.Actor), sanitizeLine(r.Source))
			if len(r.Fields) > 0 {
				line += " — fields: " + sanitizeLine(strings.Join(r.Fields, ", "))
			}
			if r.Reason != "" {
				line += " — reason: " + sanitizeLine(r.Reason)
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func withdrawnClaimIDs(f store.Finding) []string {
	var out []string
	for _, c := range f.Claims {
		if store.IsWithdrawnVerdict(c.Verdict) {
			out = append(out, c.ID)
		}
	}
	return out
}
