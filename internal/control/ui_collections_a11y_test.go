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
}
