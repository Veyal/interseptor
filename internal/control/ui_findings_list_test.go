package control

import (
	"regexp"
	"strings"
	"testing"
)

// jsFunc returns the source of a top-level function declaration, up to the
// next top-level declaration, so assertions stay scoped to one function.
func jsFunc(t *testing.T, src, signature string) string {
	t.Helper()
	start := strings.Index(src, signature)
	if start < 0 {
		t.Fatalf("findings.js has no %s", signature)
	}
	rest := src[start+len(signature):]
	end := regexp.MustCompile(`\n(function |async function |export |const |let |\{\n|\$\()`).FindStringIndex(rest)
	if end == nil {
		return src[start:]
	}
	return src[start : start+len(signature)+end[0]]
}

// listPane returns the markup of <aside class="find-browser"> only.
func listPane(t *testing.T) string {
	t.Helper()
	region := findingsRegion(t, "findings")
	start := strings.Index(region, `<aside class="find-browser"`)
	end := strings.Index(region, `</aside>`)
	if start < 0 || end < start {
		t.Fatal("find-browser aside not found in the findings region")
	}
	return region[start:end]
}

// A row is two lines (title, then a mono metadata line) with a severity stripe
// and an evidence count; the three readiness circles no longer live in the list.
func TestUIFindingsRowIsTwoLinesWithEvidenceCount(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	row := jsFunc(t, src, "function findingRowHTML(")
	requireUIContains(t, row,
		`class="find-row`, `find-row-sev-`, `data-id="${f.id}"`, `data-finding-id="${f.id}"`,
		`findingsEditable() ? ' data-evidence-drop' : ''`,
		`aria-current="${f.id === selFinding ? 'true' : 'false'}"`,
		`class="find-title"`, `class="find-meta"`, `class="find-ev`, `findingPocCount(f)`, `icon(findingHasScreenshot(f) ? 'image' : 'attach')`)
	if strings.Contains(row, "readinessMeterHTML") || strings.Contains(row, "find-row-meter") || strings.Contains(row, `class="sev `) {
		t.Error("a list row must not render readiness circles or a severity pill; severity is the stripe and group header")
	}
	meta := jsFunc(t, src, "function findingRowMeta(")
	requireUIContains(t, meta, "&middot;", "statusLabel(f.status)", "findingHost(f)", "f.cwe", "statusBadgeClass(f.status)", "findingStatusIcon(")
	if regexp.MustCompile(`[^\x00-\x7f]`).MatchString(executableJS(row + meta)) {
		t.Error("row markup must use entities and sprite icons, never literal non-ASCII glyphs")
	}
	if strings.Contains(src, `style="color`) {
		t.Error("severity and status colours must stay CSS classes")
	}
	css := readUIAsset(t, "findings.css")
	rowRule := regexp.MustCompile(`\.find-browser \.find-row\{[^}]*\}`).FindString(css)
	requireUIContains(t, rowRule, "min-height:50px")
	requireUIContains(t, css,
		".find-browser .find-row .find-title{", "text-overflow:ellipsis", "white-space:nowrap",
		".find-browser .find-row::before{", "width:3px", "background:var(--sv)",
		".find-ev{", "font-variant-numeric:tabular-nums", ".find-ev.is-none{")
	for _, sev := range []string{"critical", "high", "medium", "low", "info"} {
		requireUIContains(t, css, ".find-row-sev-"+sev+"{--sv:var(--sev-"+sev+")}")
	}
}

// Severity groups: sticky header with a count, Critical to Info then id, and a
// persisted toggle for a flat list.
func TestUIFindingsListGroupsBySeverityWithStickyHeaders(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	requireUIContains(t, src, "const SEVERITY_ORDER = ['critical', 'high', 'medium', 'low', 'info']",
		"function orderFindings(", "function findingGroupHeaderHTML(", `class="find-group`,
		"projectStorageKey('findings.groupBySeverity')", "function setFindGroupBySeverity(", "findGroupBySeverity")
	hdr := jsFunc(t, src, "function findingGroupHeaderHTML(")
	requireUIContains(t, hdr, "find-row-sev-", `class="n"`)
	// The grouping choice is part of the keyed render, or toggling it would be skipped.
	requireUIContains(t, src, "findGroupBySeverity ? 'g' : 'f'")
	css := readUIAsset(t, "findings.css")
	group := regexp.MustCompile(`\.find-group\{[^}]*\}`).FindString(css)
	requireUIContains(t, group, "position:sticky", "top:0", "z-index:")
	if strings.Contains(src[:strings.Index(src, "function renderFindings()")], "Writing guide") {
		t.Error("no Writing guide copy belongs in the list code")
	}
}

// Search, Filter and New share one toolbar row; severity, status and tags live in a popover;
// active filters show as removable chips; Writing guide is no longer a second disclosure in the list.
func TestUIFindingsFilterPopoverAndChips(t *testing.T) {
	pane := listPane(t)
	requireUIContains(t, pane,
		`id="findSearch"`, `id="findFilterBtn"`, `aria-haspopup="true"`, `aria-controls="findFilters"`, `aria-expanded="false"`,
		`id="findFilterCount"`, `id="findListNew"`, `id="findFilters"`, `role="group"`,
		`id="findFilterSeverity"`, `id="findFilterStatus"`, `id="findTagFilter"`, `id="findGroupToggle"`, `aria-pressed=`,
		`id="findFilterChips"`)
	filters := pane[strings.Index(pane, `id="findFilters"`):]
	for _, id := range []string{"findFilterSeverity", "findFilterStatus", "findTagFilter", "findGroupToggle"} {
		if !strings.Contains(filters, `id="`+id+`"`) {
			t.Errorf("%s must live inside the Filter popover", id)
		}
	}
	if !regexp.MustCompile(`id="findFilters"[^>]*hidden`).MatchString(pane) {
		t.Error("the Filter popover starts hidden")
	}
	if strings.Contains(pane, "find-browser-filters") || strings.Contains(pane, "findTagsDisclosure") {
		t.Error("the inline severity/status selects and the Tags disclosure must be gone from the list pane")
	}
	if strings.Contains(pane, "<details") || strings.Contains(pane, "<summary>Writing guide") || strings.Contains(pane, `id="findGuideAside"`) {
		t.Error("the duplicate Writing guide disclosure must not remain in the list pane")
	}
	region := findingsRegion(t, "findings")
	requireUIContains(t, region, `id="findGuide"`, `aria-controls="findGuideAside"`, `id="findGuideAside"`, `id="findGuideDialog"`)
	if strings.Count(region, ">Writing guide<") > 0 && strings.Count(region, "<summary>Writing guide</summary>") > 0 {
		t.Error("Writing guide must be reachable only from the toolbar button")
	}

	src := readUIAsset(t, "js/findings.js")
	requireUIContains(t, src,
		"function renderFindFilterChips(", "function activeFindFilters(", "function setFindFiltersOpen(", "function syncFindFilterButton(",
		`data-remove-filter="`, `aria-label="Remove `, "Clear all", "e.key !== 'Escape'", "findFilterCount")
	chips := jsFunc(t, src, "function renderFindFilterChips(")
	requireUIContains(t, chips, "icon('close')", "activeFindFilters()")
	if strings.Contains(chips, `style="`) {
		t.Error("filter chips must not use inline style")
	}
	css := readUIAsset(t, "findings.css")
	requireUIContains(t, css, ".find-filters{", ".find-chip{", ".find-filter-btn:focus-visible", ".find-chip button:focus-visible", "(pointer:coarse)")
	coarse := css[strings.LastIndex(css, "@media(pointer:coarse){\n  .find-filter-btn"):]
	requireUIContains(t, coarse[:400], "min-height:44px")
}

// The list keeps its roving tab stop: one tab stop for the list, not one per row.
func TestUIFindingsListKeepsSingleTabStop(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	row := jsFunc(t, src, "function findingRowHTML(")
	requireUIContains(t, row, `tabindex="${tab ? '0' : '-1'}"`)
}
