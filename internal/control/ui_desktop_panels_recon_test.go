package control

import (
	"regexp"
	"strings"
	"testing"
)

// Scanner, Map and Activity on the desktop skin (docs/desktop-design.md, Panels).
// Their stylesheets and scripts only act under html.desktop-app, so a browser tab
// keeps the web look; these contracts pin that and the moves that must not lose a
// control.

var desktopReconSheets = []string{
	"css/desktop/panel-activity.css", "css/desktop/panel-map.css", "css/desktop/panel-scanner.css",
}

func TestUIDesktopReconSheetsAreLinkedAfterTheShellInOrder(t *testing.T) {
	index := readUIAsset(t, "index.html")
	at := strings.Index(index, `<link rel="stylesheet" href="/css/desktop/shell.css">`)
	if at < 0 {
		t.Fatal("index.html lost desktop/shell.css")
	}
	for _, name := range desktopReconSheets {
		link := `<link rel="stylesheet" href="/` + name + `">`
		i := strings.Index(index, link)
		if i < 0 {
			t.Fatalf("index.html does not link %s", name)
		}
		if i < at {
			t.Errorf("%s must load after shell.css and the earlier panel sheets, alphabetically", name)
		}
		at = i
		rec := uiGet(t, "/"+name, map[string]string{"Accept-Encoding": "gzip"})
		if rec.Code != 200 || rec.Header().Get("ETag") == "" {
			t.Errorf("/%s: status %d ETag %q", name, rec.Code, rec.Header().Get("ETag"))
		}
	}
}

func TestUIDesktopReconSheetsAreScopedAndFollowTheRules(t *testing.T) {
	ruleRE := regexp.MustCompile(`([^{}]+)\{[^{}]*\}`)
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	weight := regexp.MustCompile(`font-weight\s*:\s*(\d+)`)
	for _, name := range desktopReconSheets {
		raw := readUIAsset(t, name)
		css := stripCSSComments(raw)
		rules := ruleRE.FindAllStringSubmatch(css, -1)
		if len(rules) == 0 {
			t.Fatalf("%s has no rules", name)
		}
		for _, r := range rules {
			for _, sel := range splitTopLevelCommas(r[1]) {
				if !strings.HasPrefix(strings.TrimSpace(sel), "html.desktop-app") {
					t.Errorf("%s: selector %q is not scoped under html.desktop-app; a browser tab would change", name, strings.TrimSpace(sel))
				}
			}
		}
		// Colours come from the tokens, never literals; hex inside a data: URI is not a colour.
		for _, m := range hex.FindAllString(css, -1) {
			t.Errorf("%s: literal colour %s; use desktop tokens", name, m)
		}
		if strings.Contains(css, "rgba(") || strings.Contains(css, "rgb(") {
			t.Errorf("%s: literal rgb colour; use desktop tokens", name)
		}
		for _, m := range weight.FindAllStringSubmatch(css, -1) {
			if m[1] != "400" && m[1] != "500" && m[1] != "600" {
				t.Errorf("%s: font-weight %s; the desktop skin allows 400, 500 and 600", name, m[1])
			}
		}
		if strings.Contains(css, "@keyframes") || strings.Contains(css, "uppercase") {
			t.Errorf("%s declares keyframes or uppercase text", name)
		}
		for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(css, -1) {
			for _, part := range strings.Split(m[1], ",") {
				if f := strings.Fields(part); len(f) > 0 && f[0] != "transform" && f[0] != "opacity" {
					t.Errorf("%s transitions %q; only transform and opacity may animate", name, f[0])
				}
			}
		}
		// A rule that sets display for a [hidden] element must restate [hidden].
		for _, id := range []string{".scan-progress", ".scan-sev-chips", ".act-chips", ".map-pop"} {
			if strings.Contains(css, id+"{display") && !strings.Contains(css, id+"[hidden]{display:none}") && !strings.Contains(css, id+"[hidden]{display:none") {
				t.Errorf("%s: %s sets display without restating [hidden]", name, id)
			}
		}
	}
}

func TestUIDesktopReconScriptsAreGatedOnTheDesktopSkin(t *testing.T) {
	for _, name := range []string{"js/desktop-panel-scanner.js", "js/desktop-panel-map.js", "js/desktop-panel-activity.js"} {
		js := readUIAsset(t, name)
		// The host check lives once, in the desktop kit.
		requireUIContains(t, js, "from './desktop-panel-kit.js';", "export const isDesktopSkin = isDesktop;")
		if !strings.Contains(js, "if (inited || !isDesktopSkin()) return;") && !strings.Contains(js, "isDesktopSkin()") {
			t.Errorf("%s does not gate on the desktop skin", name)
		}
	}
	scanner := executableJS(readUIAsset(t, "js/scanner.js"))
	requireUIContains(t, scanner,
		"if(isDesktopSkin())return scanTargetRowDesktop(",
		"if(isDesktopSkin()){",
		"scanDetailDesktopHTML({g,first,",
		"setTimeout(initScannerDesktop,0);",
	)
	activity := executableJS(readUIAsset(t, "js/activity.js"))
	requireUIContains(t, activity,
		"isDesktopSkin()?visibleCategories(counts,actFilter.cats):ACTIVITY_CATEGORIES",
		"${isDesktopSkin()?actTypeIconHTML(it.tool):''}",
		"setTimeout(initActivityDesktop,0);",
	)
	mapJS := executableJS(readUIAsset(t, "js/map.js"))
	requireUIContains(t, mapJS, "initMapDesktop();", "setTimeout(initMapDesktop,0);")
}

// Nothing leaves the panels: the Scanner, Map and Activity controls keep their ids, and the
// desktop scripts MOVE existing elements instead of recreating them.
func TestUIDesktopReconKeepsEveryControl(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, id := range []string{
		"scanTarget", "scanFilter", "scanRun", "scanClear", "scanToolsBtn", "checksBtn", "codecsBtn", "scanDecoderBtn", "oobBtn",
		"scanScopeLink", "scanStop", "scanHostFilter", "scanSevChips",
		"mapDomain", "mapSearch", "mapSearchScope", "mapViewSeg", "mapMethod", "mapStatus", "mapTag", "mapHideNoise",
		"mapCollapseIdentical", "mapDiscoveryHelp", "mapUnlinked", "mapExpand", "mapFit", "mapRefresh", "mapCount", "mapCoverage",
		"actIntentFilter", "actChips", "actAppendix", "actClear", "actCount",
	} {
		requireUIContains(t, index, `id="`+id+`"`)
	}
	mapDesk := readUIAsset(t, "js/desktop-panel-map.js")
	for _, id := range []string{"#mapDomain", "#mapSearchScope", "#mapMethod", "#mapStatus", "#mapTag", "#mapHideNoise", "#mapCollapseIdentical", "#mapUnlinked", "#mapExpand", "#mapFit", "#mapRefresh", "#mapDiscoveryHelp", "#mapSearch", "#mapViewSeg", "#mapCount"} {
		requireUIContains(t, mapDesk, "'"+id+"'")
	}
	for _, bad := range []string{"createElement('select'", "getElementById('mapExpand').cloneNode", "cloneNode("} {
		if strings.Contains(mapDesk, bad) {
			t.Errorf("desktop-panel-map.js must move the existing controls, not recreate them (%s)", bad)
		}
	}
	scanDesk := readUIAsset(t, "js/desktop-panel-scanner.js")
	// Every per-target action keeps its data-act hook and an accessible name.
	requireUIContains(t, scanDesk, `data-act="open"`, `data-act="verify"`, `data-act="attach"`, `aria-label="Open flow #${i.flowId}"`, `aria-label="Verify in Repeater"`, `aria-label="Attach as evidence"`, "data-scan-toggle", "aria-expanded")
	requireUIContains(t, scanDesk, `id="scanPromote"`)
}
