package control

import (
	"strings"
	"testing"
)

func TestUIStaticEditorsHaveAccessibleNames(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, contract := range []string{
		`<label for="deviceProxyManualHost">Manual host`,
		`id="setSessionHeaders" aria-label="Session headers`,
		`<label for="macroReq">Refresh request`,
		`<label for="loginMacroReq">Login request`,
		`id="checkSrc" class="rep-edit" aria-label="Custom check source`,
		`id="codecSrc" class="rep-edit" aria-label="Message codec source`,
		`id="promptInput" class="btn btn-field" aria-label="Value`,
	} {
		if !strings.Contains(index, contract) {
			t.Errorf("static editor naming contract missing %q", contract)
		}
	}
}

func TestUIDynamicSecurityEditorsHaveAccessibleNames(t *testing.T) {
	assets := map[string][]string{
		"js/settings.js": {
			`aria-label="Host override hostname"`,
			`aria-label="Headers for host override"`,
			`el.setAttribute('role','status')`,
			`el.setAttribute('aria-live','polite')`,
		},
		"js/authz.js": {
			`aria-label="Authorization identity ${i+1} name"`,
			`aria-label="Authorization identity ${i+1} headers"`,
		},
		"js/proxy.js": {
			`aria-label="Enable scope rule ${r.id}"`,
			`aria-label="Scope rule ${r.id} action"`,
			`aria-label="Scope rule ${r.id} host"`,
			`aria-label="Scope rule ${r.id} path"`,
			`aria-label="Scope rule ${r.id} scheme"`,
			`aria-label="Delete scope rule ${r.id}"`,
			`aria-label="WebSocket replay message for`,
		},
		"js/intercept.js": {
			`aria-label="Enable interception rule ${r.id}"`,
			`aria-label="Interception rule ${r.id} type"`,
			`aria-label="Interception rule ${r.id} match"`,
			`aria-label="Interception rule ${r.id} replacement"`,
			`aria-label="Delete interception rule ${r.id}"`,
		},
		"js/apipanel.js": {
			`aria-label="Remove allowlist entry ${escAttr(e.cidr)}"`,
		},
		"js/findings.js": {
			`aria-label="Finding impact"`,
			`aria-label="Why this is a finding"`,
			`aria-label="Affected target"`,
			`aria-label="Finding remediation"`,
			`aria-label="Evidence annotation"`,
			`aria-label="Screenshot caption"`,
		},
	}
	for asset, contracts := range assets {
		body := readUIAsset(t, asset)
		for _, contract := range contracts {
			if !strings.Contains(body, contract) {
				t.Errorf("%s dynamic naming/status contract missing %q", asset, contract)
			}
		}
	}
}

func TestUIMapGraphUsesRovingTabindex(t *testing.T) {
	mapJS := executableJS(readUIAsset(t, "js/map.js"))
	for _, contract := range []string{
		`tabindex="${selected?'0':'-1'}"`,
		`node.tabIndex=on?0:-1`,
		`['ArrowRight','ArrowDown','ArrowLeft','ArrowUp','Home','End']`,
	} {
		if !strings.Contains(mapJS, contract) {
			t.Errorf("Map graph roving-tabindex contract missing %q", contract)
		}
	}
	if strings.Contains(mapJS, `role="option" tabindex="0"`) {
		t.Error("every Map graph option must not enter the document Tab sequence")
	}
}

func TestUIMainNavigationReportsResponsiveOrientation(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, contract := range []string{
		"matchMedia('(max-width:720px)')",
		"function syncMainNavigationOrientation",
		"nav.setAttribute('aria-orientation',mobileNavMedia.matches?'horizontal':'vertical')",
		"mobileNavMedia.addEventListener('change',syncMainNavigationOrientation)",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("responsive main-navigation orientation contract missing %q", contract)
		}
	}
}

func TestUIStatusRegionsAnnounceBoundedUpdates(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, contract := range []string{
		`id="mapWarn" class="map-warn" style="display:none" role="status" aria-live="polite"`,
		`id="actCount" role="status" aria-live="polite" aria-atomic="true"`,
	} {
		if !strings.Contains(index, contract) {
			t.Errorf("bounded live-status contract missing %q", contract)
		}
	}
	if strings.Contains(index, `id="actFeed" class="scroll" aria-live=`) {
		t.Error("the high-volume Activity feed itself must not be a live region")
	}
}
