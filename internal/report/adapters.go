package report

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Adapters reshape findings for an external tracker or platform. Every adapter
// obeys the same two rules:
//
//  1. The narrative is rendered by renderFinding, the one function that writes a
//     finding's Markdown for the engagement report. An adapter therefore carries
//     the same evidence roles, proof statements and provenance labels
//     ("generated evidence render … not browser proof") as the report does, and
//     cannot drift from it.
//  2. A field with no honest equivalent in the target system is omitted, not
//     filled in. An adapter never invents an identifier it cannot know — a
//     HackerOne weakness id, a Bugcrowd VRT id, a Jira project key — because a
//     guessed identifier files the report against the wrong thing.
//
// False positives are excluded, exactly as they are from the report body.

// ReportableFindings are the findings an adapter exports: everything except the
// ones reviewed and dismissed, in the report's own severity order.
func ReportableFindings(findings []store.Finding) []store.Finding {
	out := make([]store.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Status != "false_positive" {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Severity), rank(out[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// FindingMarkdown renders one finding exactly as the engagement report renders
// it, minus the report's numbered heading. titles may be nil.
func FindingMarkdown(f store.Finding, titles map[int64]string) string {
	var b strings.Builder
	renderFinding(&b, 1, f, titles)
	md := strings.TrimLeft(b.String(), "\n")
	// renderFinding opens with "### 1. <title>"; an adapter carries the title in
	// its own field, so drop the heading and keep the body.
	if _, rest, ok := strings.Cut(md, "\n"); ok {
		md = strings.TrimLeft(rest, "\n")
	}
	return strings.TrimRight(md, "\n")
}

// evidenceEntry is the provenance-preserving description of one attached
// artifact. It never carries raw bytes — a tracker payload is not the place for
// 64 KiB of captured traffic — only what the artifact is and what it proves.
type evidenceEntry struct {
	Role    string `json:"role,omitempty"`
	Kind    string `json:"kind"` // flow | image | text
	Source  string `json:"source,omitempty"`
	Proof   string `json:"proof,omitempty"`
	FlowID  int64  `json:"flowId,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

func findingEvidence(f store.Finding) []evidenceEntry {
	var out []evidenceEntry
	for _, bl := range f.Blocks {
		if bl.Type == "text" {
			continue
		}
		out = append(out, evidenceEntry{
			Role: bl.Role, Kind: bl.Type, Source: bl.Source, Proof: bl.Proof,
			FlowID: bl.FlowID, Hash: bl.Hash, Missing: bl.Missing,
		})
	}
	for _, fl := range f.Flows {
		if hasFlowEvidence(out, fl.FlowID) {
			continue
		}
		out = append(out, evidenceEntry{
			Kind: "flow", Proof: fl.Note, FlowID: fl.FlowID, Missing: fl.Missing,
		})
	}
	return out
}

func hasFlowEvidence(entries []evidenceEntry, flowID int64) bool {
	for _, e := range entries {
		if e.Kind == "flow" && e.FlowID == flowID {
			return true
		}
	}
	return false
}

// findingTargetURLs lists the affected target URLs, primary first, de-duplicated.
func findingTargetURLs(f store.Finding) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, t := range f.Targets {
		add(t.URL)
	}
	add(f.Target)
	return out
}

// absoluteURI reports whether s is an absolute URI, i.e. whether it can honestly
// be written into a field typed as one.
func absoluteURI(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.IsAbs() && u.Host != ""
}

// adapterTitle is the finding title, single-line and trimmed to max runes so a
// tracker's own length limit does not truncate mid-sentence.
func adapterTitle(f store.Finding, max int) string {
	title := strings.TrimSpace(sanitizeLine(f.Title))
	if title == "" {
		title = fmt.Sprintf("Finding #%d", f.ID)
	}
	return truncateRunes(title, max)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimRight(string(r[:max-1]), " ") + "…"
}
