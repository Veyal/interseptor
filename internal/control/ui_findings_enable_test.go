package control

import "testing"

// With findings editing off by default, every write entry point used to end in
// a toast that only said "Enable it in Settings" and an empty Findings page
// with no action at all. Both now offer the fix in place.
func TestUIFindingsEditingOffOffersEnableInPlace(t *testing.T) {
	core := executableJS(readUIAsset(t, "js/core.js"))
	requireUIRegex(t, core, `export function requireFindingsEditing\(\)\{[\s\S]*?b\.textContent='Enable editing'[\s\S]*?enableFindingsEditing\(\)`)
	requireUIRegex(t, core, `export function enableFindingsEditing\(\)\{[\s\S]*?\$\('#findingsUIEditingToggle'\)[\s\S]*?b\.click\(\)`)

	findings := executableJS(readUIAsset(t, "js/findings.js"))
	requireUIContains(t, findings,
		`id="findEmptyEnable">Enable findings editing</button>`,
		`box.querySelector('#findEmptyEnable')`,
		`enable.onclick = () => { enableFindingsEditing(); }`,
	)
}
