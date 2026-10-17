package control

import (
	"regexp"
	"strings"
	"testing"
)

// At phone width css/collections.css hides every .lbl-long label in the
// Collections toolbar, and js/hints.js strips the title attribute, so each of
// those buttons needs an explicit aria-label or it has no accessible name.
func TestCollectionsToolbarButtonsKeepNameWhenLabelHidden(t *testing.T) {
	index := readUIAsset(t, "index.html")
	start := strings.Index(index, "<!-- region:collections -->")
	end := strings.Index(index, "<!-- /region:collections -->")
	if start < 0 || end < start {
		t.Fatal("collections region markers not found in index.html")
	}
	region := index[start:end]
	btnRe := regexp.MustCompile(`(?s)<button\b[^>]*>.*?</button>`)
	labelRe := regexp.MustCompile(`\baria-label="([^"]+)"`)
	textRe := regexp.MustCompile(`<span class="lbl-long">([^<]*)</span>`)
	n := 0
	for _, b := range btnRe.FindAllString(region, -1) {
		m := textRe.FindStringSubmatch(b)
		if m == nil {
			continue
		}
		n++
		l := labelRe.FindStringSubmatch(b)
		if l == nil {
			t.Errorf("button with hidden-on-phone label %q has no aria-label: %.80s", m[1], b)
			continue
		}
		// WCAG 2.5.3 label in name: the visible word must be part of the name.
		if !strings.Contains(strings.ToLower(l[1]), strings.ToLower(strings.TrimSpace(m[1]))) {
			t.Errorf("aria-label %q does not contain the visible label %q", l[1], m[1])
		}
	}
	if n < 7 {
		t.Errorf("expected at least 7 toolbar buttons with .lbl-long labels, found %d", n)
	}
	// Icon-only on phones, so two toolbar buttons must not share one icon.
	iconRe := regexp.MustCompile(`id="(coll\w+)"[^>]*>\s*<svg[^>]*><use href="#(i-[\w-]+)"`)
	seen := map[string]string{}
	toolbar := region[:strings.Index(region, `id="collStatus"`)]
	for _, m := range iconRe.FindAllStringSubmatch(toolbar, -1) {
		if m[1] == "collEnvNew" { // shown only when there is no environment to manage
			continue
		}
		if prev, ok := seen[m[2]]; ok {
			t.Errorf("%s and %s both use icon %s", prev, m[1], m[2])
		}
		seen[m[2]] = m[1]
	}
}

// A collection could only be created from the UI; renaming or deleting one
// needed the REST API. The toolbar actions menu offers both, and its click
// must not reach the app-wide click-to-close listener that hides the menu.
func TestCollectionsCanRenameAndDeleteTheCollection(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="collMore" aria-label="Collection actions" aria-haspopup="menu"`)
	js := executableJS(readUIAsset(t, "js/collections.js"))
	requireUIContains(t, js,
		"e.stopPropagation(); openCollectionMenu(e.currentTarget)",
		"'Rename collection'", "'Delete collection'",
		"jsend('DELETE', '/api/collections/' + encodeURIComponent(co.uid))",
		"rev: co.rev",
	)
}

// Opening another request while a send is in flight used to paint the first
// request's response under the second (and Save example would store it there).
// sendCurrent pins the item it sent and drops the result once that changes.
func TestCollectionsSendResultStaysWithItsRequest(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/collections-response.js"))
	requireUIContains(t, js,
		"const uid = S.ed.uid;",
		"const current = () => gen === S.sendGen && S.ed && S.ed.uid === uid;",
		"{ itemUid: uid,",
	)
	at := strings.Index(js, "export async function sendCurrent()")
	end := strings.Index(js, "export function setSendBusy")
	body := js[at:end]
	if strings.Index(body, "if (!current())") > strings.Index(body, "S.step = res;") {
		t.Error("sendCurrent must check that the request is still open before it stores the result")
	}
}

// A current value typed into a new variable row before its name was keyed by
// the empty name and silently dropped on save. Values are keyed by row now.
func TestCollectionsVariableSheetKeepsCurrentValueTypedBeforeName(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/collections-sheets.js"))
	requireUIContains(t, js, "currents.set(r, {", "for (const [row, c] of currents)", "const key = (row.key || '').trim();")
	if strings.Contains(js, "currents.set(r.key") {
		t.Error("current values must not be keyed by the name at typing time")
	}
}

// The collection and environment pickers are enhanced into .ui-select
// wrappers; when the toolbar is crowded the wrapper shrinks, so the trigger
// must fill it (and ellipsize) rather than spill over the next button.
func TestCollectionsPickersEllipsizeInsteadOfOverlapping(t *testing.T) {
	css := readUIAsset(t, "css/collections.css")
	requireUIContains(t, css,
		".coll-field .ui-select{max-width:min(240px,40vw);min-width:0",
		".coll-field .ui-select-btn .ui-select-trigger,.coll-env .ui-select-btn .ui-select-trigger{width:100%;max-width:100%}",
		// A readable name wins over button labels: labels fold to icons first.
		".coll-field{display:inline-flex;align-items:center;min-width:min(150px,36vw)}",
		"#collImport .lbl-long,#collExport .lbl-long,#collConsoleBtn .lbl-long{display:none}",
	)
}
