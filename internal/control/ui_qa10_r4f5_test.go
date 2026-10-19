package control

import "testing"

// Round 4 (a5): contract pins for the Collections fixes the browser checks cover.
func TestUIRound4CollectionsContracts(t *testing.T) {
	// B1: the run-plan block check predicts the IP guard and the badge names it.
	requireUIContains(t, readUIAsset(t, "js/collections-core.js"), "'ip_guard'", "restrictedHost(u.hostname)")
	requireUIContains(t, readUIAsset(t, "js/collections-model.js"), "ip_guard:")
	// B2: the runner detail swaps the button for a state once the host is added.
	requireUIContains(t, readUIAsset(t, "js/runner.js"), "Now in scope. Rerun to send this request.", "st.addedHosts")
	// B3: matrix cells carry the reason.
	requireUIContains(t, readUIAsset(t, "js/collections-matrix.js"), "MM.cellTitle(m.identities[i], cell)")
	// B4: the handoff failure reads as a sentence, without the package prefix.
	requireUIContains(t, readUIAsset(t, "js/collections-matrix.js"), `replace(/^collmatrix:\s*/, '')`)
	// B5: the desktop bar gives the end zone room for "No identity" near 900px.
	requireUIContains(t, readUIAsset(t, "css/desktop/shell.css"), "@media (max-width:960px)", "minmax(180px,280px)")
	requireUIContains(t, readUIAsset(t, "shell.css"), ".ctx-identity .ui-select-trigger{max-width:16ch}")
	// B6: a secret's masked Current value input is as wide as the other rows.
	requireUIContains(t, readUIAsset(t, "css/collections.css"), `.coll-kv :is(input[type="text"],input[type="password"]){width:100%`)
}
