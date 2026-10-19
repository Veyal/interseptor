package control

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The Findings, Notes and Settings desktop sheets (css/desktop/panel-*.css) restyle
// those panels inside a desktop host only. These contracts keep a browser tab on the
// web look, hold the sheets to the desktop design system, and pin the arrangement
// decisions (one primary per view, severity as a dot, no empty Notes strip).

var desktopFNSSheets = []string{"css/desktop/panel-findings.css", "css/desktop/panel-notes.css", "css/desktop/panel-settings.css"}

// assertDesktopPanelSheetFNS checks one panel sheet: linked after shell.css, served with a
// validator and gzip, every selector under html.desktop-app (also inside @container), no
// literal colours, only transform/opacity animation, weights 400-600, no uppercase labels.
func assertDesktopPanelSheetFNS(t *testing.T, name string) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	link := `<link rel="stylesheet" href="/` + name + `">`
	li, shell := strings.Index(index, link), strings.Index(index, `<link rel="stylesheet" href="/css/desktop/shell.css">`)
	if li < 0 || shell < 0 || li < shell {
		t.Fatalf("index.html must link %s after css/desktop/shell.css", name)
	}
	if strings.Count(index, link) != 1 {
		t.Errorf("%s is linked more than once", name)
	}
	rec := uiGet(t, "/"+name, map[string]string{"Accept-Encoding": "gzip"})
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == "" || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("/%s: status %d ETag %q encoding %q", name, rec.Code, rec.Header().Get("ETag"), rec.Header().Get("Content-Encoding"))
	}
	css := stripCSSComments(readUIAsset(t, name))
	rules := regexp.MustCompile(`([^{}]+)\{[^{}]*\}`).FindAllStringSubmatch(css, -1)
	if len(rules) == 0 {
		t.Fatalf("%s has no rules", name)
	}
	for _, r := range rules {
		for _, sel := range splitTopLevelCommas(r[1]) {
			sel = strings.TrimSpace(sel)
			if i := strings.LastIndex(sel, "}"); i >= 0 { // a rule that follows a closed @container block
				sel = strings.TrimSpace(sel[i+1:])
			}
			if !strings.HasPrefix(sel, "html.desktop-app") {
				t.Errorf("%s: selector %q is not scoped under html.desktop-app; a browser tab would change", name, sel)
			}
		}
	}
	if m := regexp.MustCompile(`(?:^|[^\w-])#[0-9a-fA-F]{3,8}\b(?:\s*[;,)}])`).FindString(css); m != "" {
		t.Errorf("%s: literal colour %q; use desktop tokens", name, m)
	}
	for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(css, -1) {
		for _, part := range strings.Split(m[1], ",") {
			if f := strings.Fields(part); len(f) > 0 && f[0] != "transform" && f[0] != "opacity" && f[0] != "none" {
				t.Errorf("%s transitions %q; only transform and opacity may animate", name, f[0])
			}
		}
	}
	for _, m := range regexp.MustCompile(`font-weight\s*:\s*(\d+)|font\s*:\s*(\d{3})\s`).FindAllStringSubmatch(css, -1) {
		if w := m[1] + m[2]; w != "400" && w != "500" && w != "600" {
			t.Errorf("%s uses font-weight %s; the desktop skin allows 400, 500 and 600", name, w)
		}
	}
	// Sentence case from capitalised source text: lowercase the word, capitalise its first letter only.
	if bare := strings.ReplaceAll(css, "::first-letter{text-transform:uppercase}", ""); strings.Contains(bare, "text-transform:uppercase") {
		t.Errorf("%s uppercases labels; desktop labels are sentence case", name)
	}
	if strings.Contains(css, "@keyframes") || strings.Contains(css, "@import") {
		t.Errorf("%s declares keyframes or imports", name)
	}
	return css
}

func TestUIDesktopPanelSheetsAreLinkedScopedAndOnSystem(t *testing.T) {
	index := readUIAsset(t, "index.html")
	at := -1
	for _, name := range desktopFNSSheets { // alphabetical, after shell.css
		assertDesktopPanelSheetFNS(t, name)
		i := strings.Index(index, `href="/`+name+`"`)
		if i < at {
			t.Errorf("%s is linked out of alphabetical order", name)
		}
		at = i
	}
}

func TestUIDesktopFindingsListHasNoRowBarsAndSeverityIsADot(t *testing.T) {
	css := assertDesktopPanelSheetFNS(t, "css/desktop/panel-findings.css")
	for _, want := range []string{
		// the web's 3px severity bar and coloured dash are replaced
		`.find-browser .find-row:not(.sel)::before{content:none}`,
		`.find-group .bar{width:8px;height:8px;border-radius:50%`,
		// severity = dot plus word, status = quiet tag
		`.find-sev-chip::before{content:"";flex:none;width:8px;height:8px;border-radius:50%`,
		`.find-status-badge{padding:0 6px;border:0;border-radius:var(--radius-xs)`,
		// reading scale: heading 20/28, body 14/21, section title 15/20, evidence cards 8px
		`.find-title-text{font-size:var(--text-heading);line-height:var(--lh-heading)`,
		`.find-stext{flex:1 1 15rem;max-width:72ch;font-size:var(--text-body);line-height:var(--lh-body)`,
		`.find-xch{border:1px solid var(--border-subtle);border-radius:var(--radius-md)`,
		`.find-doc-in{gap:32px;max-width:76ch}`,
		// the folded Properties row is a quiet disclosure, and the rail never collapses its row
		`.find-rail{overflow:clip`,
		`@container findws (max-width:1000px)`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("panel-findings.css lost %q", want)
		}
	}
	// The web's mono capitals and 17px reading size must not come back.
	for _, banned := range []string{"font-size:17px", "calc(var(--fs-xl) + 1px)", "letter-spacing:.09em", "letter-spacing:.1em"} {
		if strings.Contains(css, banned) {
			t.Errorf("panel-findings.css contains %q", banned)
		}
	}
}

func TestUIDesktopFindingsHasOnePrimaryPerView(t *testing.T) {
	css := assertDesktopPanelSheetFNS(t, "css/desktop/panel-findings.css")
	for _, want := range []string{
		`#findListNew{background:transparent`,
		`#panel-findings:has(#findInlineNew:not([hidden])) #findNew`,
		`#panel-findings:has(#findReportMount:not([hidden])) #findNew`,
		`:is(#findToggleEdit,#findAddImage){background:var(--surface-2)`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("panel-findings.css lost the one-primary rule %q", want)
		}
	}
	// The controls those rules rely on keep their ids in the markup.
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"findNew", "findListNew", "findInlineNew", "findInlineCreate", "findReportMount", "findGuide", "findFilterBtn", "findSearch", "findList", "findDetail", "findDeletedOpen"} {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Errorf("index.html lost #%s", id)
		}
	}
	for _, id := range []string{"findToggleEdit", "findAddImage"} {
		if !strings.Contains(readUIAsset(t, "js/findings.js"), `id="`+id+`"`) {
			t.Errorf("js/findings.js lost #%s", id)
		}
	}
	if !strings.Contains(readUIAsset(t, "js/report-preflight.js"), `id="reportExport"`) {
		t.Error("report preflight lost #reportExport")
	}
}
