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

// The collection and environment pickers are enhanced into .ui-select
// wrappers; when the toolbar is crowded the wrapper shrinks, so the trigger
// must fill it (and ellipsize) rather than spill over the next button.
func TestCollectionsPickersEllipsizeInsteadOfOverlapping(t *testing.T) {
	css := readUIAsset(t, "css/collections.css")
	requireUIContains(t, css,
		".coll-field .ui-select{max-width:min(240px,40vw);min-width:0",
		".coll-field .ui-select-btn .ui-select-trigger,.coll-env .ui-select-btn .ui-select-trigger{width:100%;max-width:100%}",
	)
}
