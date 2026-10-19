package control

import (
	"strings"
	"testing"
)

// Round 2 (QA a5): a created collection must be the one that opens.
func TestUINewCollectionIsSelectedAfterCreation(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/collections.js"))
	requireUIContains(t, js,
		"async function loadAll({ keepSelection = true, select = '' } = {})",
		"const want = select || (keepSelection && S.colUid) || saved;",
		"await loadAll({ keepSelection: false, select: c.uid });",
	)
}

// Round 2 (QA a5): the run review resolves {{variable}} URLs through the
// server's resolver and lists what it cannot resolve; it never shows no hosts
// for a collection whose URLs all start with a variable.
func TestUIRunReviewResolvesVariableHosts(t *testing.T) {
	sheets := executableJS(readUIAsset(t, "js/collections-sheets.js"))
	requireUIContains(t, sheets, "resolvePlanUrls", "'/api/variables/resolve'", "resolved, blockCheck })", "Unresolved hosts (")
	matrix := executableJS(readUIAsset(t, "js/collections-matrix.js"))
	requireUIContains(t, matrix, "resolvePlanUrls", "'/api/variables/resolve'", "fact('Unresolved hosts'")
	plan := executableJS(readUIAsset(t, "js/collections-run-plan.js"))
	if strings.Contains(plan, "taken from variables") {
		t.Error("the plan must not claim hosts are unknowable; it resolves them")
	}
}

// Round 2 (QA a5): toolbar wraps instead of pushing Run off-screen; toast hugs
// its text and lets clicks through; the runner results collapse in a narrow sheet.
func TestUICollectionsRound2Layout(t *testing.T) {
	css := readUIAsset(t, "css/collections.css")
	requireUIContains(t, css,
		".coll-bar{gap:var(--sp-2);flex-wrap:wrap;overflow-x:visible",
		"@container (max-width:34rem){\n  .run-results-tbl,.run-results-tbl tbody{display:block}",
		"@media (max-width:1100px) and (max-height:760px)",
	)
	desk := readUIAsset(t, "css/desktop/panel-collections.css")
	requireUIContains(t, desk, "#panel-collections .coll-bar{flex-wrap:wrap;gap:6px;row-gap:4px;overflow:visible}")
	surf := readUIAsset(t, "surfaces.css")
	if strings.Contains(surf, "width: min(560px") {
		t.Error("the toast must hug its text, not span 560px")
	}
	requireUIContains(t, surf, "width: max-content;", ":not(:has(button)){pointer-events:none}")
}

func TestUIRunnerBlockedRowUsesHumanLabel(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/runner.js"))
	requireUIContains(t, js, "'Blocked: ' + blockText(r.blockReason)")
}
