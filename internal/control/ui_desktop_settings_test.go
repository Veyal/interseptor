package control

import (
	"regexp"
	"strings"
	"testing"
)

// settingsMainActions is the one primary button of each Settings section on desktop.
var settingsMainActions = []string{"saveAddrBtn", "dlCaBtn", "androidSetupAllBtn", "addScopeBtn", "keyCreate", "saveSessionBtn", "exportProject"}

func TestUIDesktopSettingsHasOnePrimaryPerSection(t *testing.T) {
	css := assertDesktopPanelSheetFNS(t, "css/desktop/panel-settings.css")
	if !strings.Contains(css, `.settings-body :is(.btn.btn-primary,.btn.accent){background:var(--surface-2)`) {
		t.Error("every Settings primary button must be demoted to secondary by default")
	}
	promo := regexp.MustCompile(`\.settings-body :is\(([^)]*)\)\{background:var\(--accentSolid\)`).FindStringSubmatch(css)
	if promo == nil {
		t.Fatal("panel-settings.css lost the per-section primary rule")
	}
	index := readUIAsset(t, "index.html")
	panel := index[strings.Index(index, `id="panel-settings"`):strings.Index(index, "<!-- /region:settings -->")]
	// Each section (data-sec) gets at most one promoted id, and every promoted id exists in its section.
	sections := regexp.MustCompile(`<section class="set-sec" data-sec="(\w+)"`).FindAllStringSubmatchIndex(panel, -1)
	perSection := map[string]int{}
	for _, id := range settingsMainActions {
		if !strings.Contains(promo[1], "#"+id) {
			t.Errorf("#%s is not promoted", id)
		}
		if !strings.Contains(panel, `id="`+id+`"`) {
			t.Errorf("index.html lost #%s", id)
		}
	}
	for _, id := range strings.Split(strings.ReplaceAll(promo[1], "#", ""), ",") {
		at := strings.Index(panel, `id="`+strings.TrimSpace(id)+`"`)
		sec := ""
		for _, s := range sections {
			if s[0] < at {
				sec = panel[s[2]:s[3]]
			}
		}
		perSection[sec]++
	}
	for sec, n := range perSection {
		if n > 1 {
			t.Errorf("section %q promotes %d buttons; only one primary per section", sec, n)
		}
	}
}

func TestUIDesktopSettingsKeepsRowsControlsAndSwitches(t *testing.T) {
	css := assertDesktopPanelSheetFNS(t, "css/desktop/panel-settings.css")
	for _, want := range []string{
		// section list: nav-list selected style comes from states.css; here the list chrome and type
		`.settings-nav button{min-height:30px`,
		`.settings-section-heading h2{font-size:var(--text-heading);line-height:var(--lh-heading)`,
		// grouped card per section and label-left / control-right rows
		`.settings-body .section{padding:16px 20px 18px`,
		`grid-template-columns:minmax(120px,190px) minmax(0,1fr)`,
		// one shared control width
		`max-width:320px`,
		// toggles read as switches
		`.settings-body .uisw::before{content:"";flex:none;width:28px;height:16px`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("panel-settings.css lost %q", want)
		}
	}
	// Every setting keeps its id: the CSS-only change must not touch the markup contract.
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"setNav", "setSearch", "saveAddrBtn", "saveProxyAuthBtn", "saveDeviceProxyBtn", "saveControlAddrBtn", "saveUpstreamBtn", "proxyAuthToggle", "sysProxyToggle", "capScopeToggle", "dlCaBtn", "tlsBypassSave", "androidSetupAllBtn", "addScopeBtn", "saveSessionBtn", "keyCreate", "exportProject", "retPolicySave"} {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Errorf("index.html lost #%s", id)
		}
	}
}
