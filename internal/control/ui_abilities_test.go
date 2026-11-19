package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestUIAbilitiesPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/abilities-model.test.mjs", "_js-tests/abilities-host.test.mjs", "_js-tests/desktop-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("abilities/desktop-model node tests failed: %v\n%s", err, out)
	}
}

func TestUIAbilitiesShellMarkupContract(t *testing.T) {
	index := readUIAsset(t, "index.html")

	cssIdx := strings.Index(index, `<link rel="stylesheet" href="/css/abilities.css">`)
	projIdx := strings.Index(index, `<link rel="stylesheet" href="/css/projects.css">`)
	mobIdx := strings.Index(index, `<link rel="stylesheet" href="/mobile.css"`)
	if cssIdx < 0 {
		t.Fatal("index.html missing /css/abilities.css stylesheet link")
	}
	if projIdx >= 0 && cssIdx < projIdx {
		t.Error("/css/abilities.css must appear after /css/projects.css")
	}
	if mobIdx >= 0 && cssIdx > mobIdx {
		t.Error("/css/abilities.css must appear before /mobile.css")
	}

	if !strings.Contains(index, `<symbol id="i-abilities" viewBox="0 0 24 24">`) {
		t.Error("index.html #iconSprite missing <symbol id=\"i-abilities\" viewBox=\"0 0 24 24\">")
	}

	for _, want := range []string{
		`id="tab-abilities"`,
		`aria-controls="panel-abilities"`,
		`data-tab="abilities"`,
		`data-group="Configure"`,
		`href="#i-abilities"`,
		`<option value="abilities">Abilities</option>`,
		`id="panel-abilities"`,
		`aria-labelledby="tab-abilities"`,
		`data-panel="abilities"`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html missing %q", want)
		}
	}

	tabAbIdx := strings.Index(index, `id="tab-abilities"`)
	tabSetIdx := strings.Index(index, `id="tab-settings"`)
	if tabAbIdx < 0 || tabSetIdx < 0 || tabAbIdx > tabSetIdx {
		t.Error("#tab-abilities must appear before #tab-settings in Configure rail group")
	}
}

func TestUIAbilitiesShellAndInspectorWiring(t *testing.T) {
	for _, mod := range []string{"js/abilities.js", "js/abilities-host.js"} {
		src := readUIAsset(t, mod)
		if lines := strings.Count(src, "\n"); lines > 700 {
			t.Errorf("%s is %d lines (> 700 line module budget)", mod, lines)
		}
		for _, forbidden := range []string{"./app.js", "./proxy.js", "./canvas.js", "./flowdrawer.js"} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s must not import %s (import cycle / coupling risk)", mod, forbidden)
			}
		}
	}

	abilitiesJS := readUIAsset(t, "js/abilities.js")
	for _, sym := range []string{
		"initAbilities",
		"loadAbilities",
		"mountAbilityPanel",
		"abilityFlowContextItems",
		"abilityInspectorTabs",
		"handleAbilitySSE",
	} {
		if !regexp.MustCompile(`(?m)^export\s+(?:async\s+)?(?:function\s+` + sym + `\b|\{[^}]*\b` + sym + `\b[^}]*\})`).MatchString(abilitiesJS) {
			t.Errorf("js/abilities.js must export %s", sym)
		}
	}

	modelJS := readUIAsset(t, "js/abilities-model.js")
	if regexp.MustCompile(`(?m)^import .* from './core\.js'`).MatchString(modelJS) {
		t.Error("js/abilities-model.js must remain pure and not statically import core.js")
	}

	appJS := readUIAsset(t, "js/app.js")
	for _, want := range []string{
		"from './abilities.js'",
		"initAbilities",
		"loadAbilities",
		"mountAbilityPanel",
		"name==='abilities'",
		"name.startsWith('ability:')",
		"Go to Abilities",
		"b:'abilities'",
	} {
		if !strings.Contains(appJS, want) {
			t.Errorf("js/app.js missing abilities wiring %q", want)
		}
	}

	if !strings.Contains(readUIAsset(t, "js/shortcuts-table.js"), `['b', 'Abilities']`) {
		t.Error("js/shortcuts-table.js GO table missing ['b', 'Abilities']")
	}

	deskModel := readUIAsset(t, "js/desktop-model.js")
	if !strings.Contains(deskModel, "'abilities'") || !strings.Contains(deskModel, "abilities: '#abSearch'") {
		t.Error("js/desktop-model.js missing 'abilities' panel or '#abSearch' find target")
	}

	proxyJS := readUIAsset(t, "js/proxy.js")
	for _, want := range []string{
		"from './abilities.js'",
		"abilityFlowContextItems",
		"abilityInspectorTabs",
		"label:'Abilities'",
		"syncAbilityInspectButtons",
	} {
		if !strings.Contains(proxyJS, want) {
			t.Errorf("js/proxy.js missing abilities integration %q", want)
		}
	}

	fdJS := readUIAsset(t, "js/flowdrawer.js")
	for _, want := range []string{
		"from './abilities.js'",
		"abilityInspectorTabs",
		"allFlowTabs",
		"tab.startsWith('ab:')",
		"interseptor:abilitiesupdate",
	} {
		if !strings.Contains(fdJS, want) {
			t.Errorf("js/flowdrawer.js missing abilities integration %q", want)
		}
	}
}
