package control

import (
	"strings"
	"testing"
)

func TestUIDisclosureControlsHaveStableExpandedRelationships(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, want := range []string{
		`id="repHistToggle"`, `aria-controls="repHistory"`, `aria-expanded="false"`,
		`id="intrHistToggle"`, `aria-controls="intrHistory"`, `aria-expanded="false"`,
		`id="mapDiscoveryHelp"`, `aria-controls="mapDiscoveryPanel"`, `aria-expanded="false"`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("disclosure accessibility contract missing %q", want)
		}
	}
	for _, want := range []string{
		"setAttribute('aria-expanded',show?'true':'false')",
		"aria-expanded", "repHistToggle", "intrHistToggle", "mapDiscoveryHelp",
	} {
		if !strings.Contains(readUIAsset(t, "js/tools.js")+readUIAsset(t, "js/map.js"), want) {
			t.Errorf("disclosure state synchronization contract missing %q", want)
		}
	}
}

func TestUIRequestTabsAndFiltersHaveUnambiguousSemantics(t *testing.T) {
	index := readUIAsset(t, "index.html")
	core := readUIAsset(t, "js/core.js")
	tools := readUIAsset(t, "js/tools.js")
	if !strings.Contains(core, "tabPanelId") || !strings.Contains(core, `aria-controls="${escAttr(tabPanelId)}"`) || !strings.Contains(core, `tabPanel.setAttribute('aria-labelledby',activeTab.id)`) {
		t.Error("dynamic request tabs must identify their nested tabpanel")
	}
	if strings.Contains(core, "panel-repeater") || strings.Contains(core, "panel-intruder") {
		t.Error("dynamic request tabs must not target the outer main-navigation tabpanels")
	}
	for _, want := range []string{
		`id="repTabPanel" role="tabpanel"`,
		`id="intrTabPanel" role="tabpanel"`,
		`tabPanelId:'repTabPanel'`,
		`tabPanelId:'intrTabPanel'`,
	} {
		if !strings.Contains(index+tools, want) {
			t.Errorf("dynamic tabpanel relationship missing %q", want)
		}
	}
	if !strings.Contains(index, `id="intrType" role="group" aria-label="Intruder mode"`) {
		t.Error("Intruder mode switch must be an explicitly labelled button group")
	}
	if !strings.Contains(index, `id="intrResFilter" role="group" aria-label="Filter results"`) {
		t.Error("Intruder result filters must be an explicitly labelled button group")
	}
	if strings.Contains(index, `id="intrResFilter" role="tablist"`) || strings.Contains(index, `data-f="all" role="tab"`) {
		t.Error("Intruder result filters must not claim incomplete tab semantics")
	}
	if strings.Contains(tools, `x.setAttribute('aria-selected',on?'true':'false')`) {
		t.Error("button-group result filters must not mix aria-selected with aria-pressed")
	}
	if !strings.Contains(tools, "function wireButtonGroupKeys(") {
		t.Error("button groups must retain arrow/Home/End keyboard navigation")
	}
}

func TestUISettingsToolNavUsesCurrentPageState(t *testing.T) {
	index := readUIAsset(t, "index.html")
	settings := readUIAsset(t, "js/settings.js")
	if strings.Contains(index, `data-sec="scanner" aria-pressed=`) {
		t.Error("Settings Scanner & OOB navigation must not expose toggle state")
	}
	if !strings.Contains(settings, "setAttribute('aria-current'") {
		t.Error("Settings navigation must retain aria-current page state")
	}
	start := strings.Index(settings, "$$('#setNav button').forEach(b=>b.onclick")
	if start >= 0 {
		end := strings.Index(settings[start:], "});")
		if end > 0 && strings.Contains(settings[start:start+end], "aria-pressed") {
			t.Error("Settings section navigation must not expose toggle state")
		}
	}
}

func TestUIGenericLoadErrorsUseInnerAlert(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	if !strings.Contains(core, `<span class="state-error-msg" role="alert">`) {
		t.Error("generic load errors must announce their message through an inner alert")
	}
	if strings.Contains(core, `el.setAttribute('aria-live'`) || strings.Contains(core, `el.setAttribute("aria-live"`) {
		t.Error("generic load errors must not turn high-volume containers into live regions")
	}
}

func TestUIDirectLoadErrorsUseScopedAlerts(t *testing.T) {
	for asset, contracts := range map[string][]string{
		"js/apipanel.js": {`role="alert"`, `data-key-list-retry`},
		"js/scanner.js":  {`role="alert"`, `data-check-docs-retry`, `data-checks-list-retry`},
		"js/codecs.js":   {`role="alert"`, `data-codec-docs-retry`, `data-codecs-list-retry`},
	} {
		src := readUIAsset(t, asset)
		for _, want := range contracts {
			if !strings.Contains(src, want) {
				t.Errorf("%s direct load error contract missing %q", asset, want)
			}
		}
	}
}

func TestUICheckAndCodecModesHaveCompleteTabPatterns(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, p := range []struct{ prefix, paneCode, paneDocs string }{
		{"check", "checkPaneCode", "checkPaneDocs"},
		{"codec", "codecPaneCode", "codecPaneDocs"},
	} {
		if !strings.Contains(index, `id="`+p.prefix+`ModeSeg" role="tablist"`) {
			t.Errorf("%s mode switch must be a tablist", p.prefix)
		}
		for _, mode := range []string{"code", "docs"} {
			tab := `id="` + p.prefix + `Mode` + strings.Title(mode) + `"`
			if !strings.Contains(index, tab) || !strings.Contains(index[strings.Index(index, tab):], `role="tab"`) || !strings.Contains(index[strings.Index(index, tab):], `aria-controls="`+p.prefix+`Pane`+strings.Title(mode)+`"`) {
				t.Errorf("%s %s tab is missing its labelled relationship", p.prefix, mode)
			}
		}
		for _, pane := range []string{p.paneCode, p.paneDocs} {
			paneAt := strings.Index(index, `id="`+pane+`"`)
			if paneAt < 0 || !strings.Contains(index[paneAt:], `role="tabpanel"`) || !strings.Contains(index[paneAt:], `aria-labelledby="`) {
				t.Errorf("%s pane %s must be a labelled tabpanel", p.prefix, pane)
			}
		}
	}
	for asset, fn := range map[string]string{"js/scanner.js": "wireCheckModeKeys", "js/codecs.js": "wireCodecModeKeys"} {
		if !strings.Contains(readUIAsset(t, asset), "function "+fn+"(") {
			t.Errorf("%s must retain keyboard navigation for mode tabs", asset)
		}
	}
}

func TestUIHeldDecodeControlExposesSelectedRepresentation(t *testing.T) {
	index := readUIAsset(t, "index.html")
	intercept := readUIAsset(t, "js/intercept.js")
	if !strings.Contains(index, `id="heldDecodeBtn"`) || !strings.Contains(index, `aria-pressed="false"`) || !strings.Contains(index, `aria-label="Show decoded held message"`) {
		t.Error("held decode control needs an explicit initial raw-state label")
	}
	for _, want := range []string{
		"function setHeldDecodeState(show)",
		"setAttribute('aria-pressed',show?'true':'false')",
		"setAttribute('aria-label',show?'Show raw held message':'Show decoded held message')",
		"button.textContent=show?'Raw':'Decoded'",
	} {
		if !strings.Contains(intercept, want) {
			t.Errorf("held decode synchronization contract missing %q", want)
		}
	}
}
