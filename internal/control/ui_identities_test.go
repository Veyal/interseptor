package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The Identities panel (js/identities*.js, css/identities.css): what agents and
// the operator saved for authorization testing. These contracts pin how the
// panel is registered everywhere a tool panel must be, that its pure rules run
// under node, that secrets stay masked until the operator reveals them, and that
// the existing Authz modal links to it.

var identitiesModules = []string{"js/identities.js", "js/identities-core.js", "js/identities-detail.js", "js/identities-edit.js", "js/identities-model.js"}

func TestUIIdentitiesPanelIsRegisteredLikeEveryToolPanel(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index,
		`id="tab-identities" role="tab" aria-selected="false" aria-controls="panel-identities" data-tab="identities" data-group="Test" title="Identities"`,
		`<use href="#i-identity"/></svg>Identities</button>`,
		`<div class="panel" id="panel-identities" role="tabpanel" aria-labelledby="tab-identities" data-panel="identities">`,
		`<option value="identities">Identities</option>`,
		`<link rel="stylesheet" href="/css/identities.css">`,
		`<script type="module" src="/js/identities.js"></script>`,
		"<!-- region:identities -->", "<!-- /region:identities -->",
	)
	// Rail order: Intruder, then Identities, then Collections.
	intr, ident, coll := strings.Index(index, `id="tab-intruder"`), strings.Index(index, `id="tab-identities"`), strings.Index(index, `id="tab-collections"`)
	if !(intr < ident && ident < coll) {
		t.Error("the Identities rail tab must sit after Intruder")
	}
	region := index[strings.Index(index, "<!-- region:identities -->"):strings.Index(index, "<!-- /region:identities -->")]
	for _, id := range []string{"identFilter", "identNew", "identFromFlow", "identRunTest", "identStatus", "identRoot", "identList", "identListState", "identDetail"} {
		if !strings.Contains(region, `id="`+id+`"`) {
			t.Errorf("the identities region lacks #%s", id)
		}
	}
	// The split's Back button lives directly in .split-detail; the detail body
	// repaints a child so it never wipes that button.
	requireUIContains(t, region, `<section class="split-detail ident-detail" aria-label="Identity detail"><div id="identDetail" class="ident-detail-body"></div></section>`)

	requireUIContains(t, readUIAsset(t, "js/app.js"), "const known=['proxy','intercept','repeater','intruder','identities','collections',")
	requireUIContains(t, readUIAsset(t, "js/desktop-model.js"), "'intruder', 'identities', 'collections'", "identities: '#identFilter',")
	requireUIContains(t, readUIAsset(t, "js/dock-model.js"), "panels: ['repeater', 'intruder', 'identities', 'collections']", "identities: 'Identities'")
	requireUIContains(t, readUIAsset(t, "js/bus.js"), "IDENTITIES_OPEN: 'identities:open',")

	js := executableJS(readUIAsset(t, "js/identities.js"))
	requireUIContains(t, js,
		"registerCommand({ t: 'Open Identities',",
		"registerCommand({ t: 'New identity',",
		"registerCommand({ t: 'Identity from selected flow',",
		"registerSseHandler('authz.update', onAuthzUpdate);",
		"busOn(EVENTS.IDENTITIES_OPEN,",
		"if (e.detail && e.detail.tab === 'identities') onShow();",
		"stackBelow: 720",
	)
}

func TestUIIdentitiesModelUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/identities-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
	model := readUIAsset(t, "js/identities-model.js")
	if staticImportRE.MatchString(model) || strings.Contains(model, "document.") {
		t.Error("identities-model.js must stay pure: no imports and no DOM")
	}
}

func TestUIIdentitiesModulesStaySafe(t *testing.T) {
	for _, name := range identitiesModules {
		js := executableJS(readUIAsset(t, name))
		for _, bad := range []string{"window.confirm", "window.alert", "window.prompt", "localStorage.setItem", "style=\"", ".style."} {
			if strings.Contains(js, bad) {
				t.Errorf("%s uses %s; use uiConfirm/uiPrompt, classes and in-memory state", name, bad)
			}
		}
		if m := regexp.MustCompile(`(^|[^A-Za-z.])(confirm|alert|prompt)\(`).FindString(js); m != "" {
			t.Errorf("%s calls a native dialog (%s); use uiConfirm/uiPrompt", name, m)
		}
		// Server strings are set with textContent; the only HTML a module writes
		// is the fixed sprite reference from icon().
		if regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=`).MatchString(js) {
			t.Errorf("%s assigns innerHTML; build nodes with textContent", name)
		}
		for _, m := range regexp.MustCompile(`insertAdjacentHTML\([^)]*\)`).FindAllString(js, -1) {
			if !strings.Contains(m, "icon(") {
				t.Errorf("%s inserts HTML that is not the icon() sprite: %s", name, m)
			}
		}
	}
	// uiConfirm messages are HTML: every interpolated value is escaped.
	for _, name := range []string{"js/identities.js", "js/identities-detail.js"} {
		js := executableJS(readUIAsset(t, name))
		for _, call := range regexp.MustCompile(`uiConfirm\([^;]*`).FindAllString(js, -1) {
			for _, part := range regexp.MustCompile(`\+\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\+`).FindAllStringSubmatch(call, -1) {
				if part[1] != "what" {
					t.Errorf("%s: uiConfirm interpolates %s without esc(): %s", name, part[1], call)
				}
			}
		}
	}
}

// Raw header values render only behind the per-identity Reveal toggle, which
// is an aria-pressed button controlling the raw block; everything else shows
// masked values (name and length) from the server or identities-model.js.
func TestUIIdentitiesSecretsAreMaskedUntilRevealed(t *testing.T) {
	js := executableJS(readUIAsset(t, "js/identities-detail.js"))
	requireUIContains(t, js,
		"const revealed = S.revealed.has(identity.name);",
		"reveal.setAttribute('aria-pressed', revealed ? 'true' : 'false');",
		"reveal.setAttribute('aria-controls', 'identRaw');",
		"raw.hidden = !revealed;",
		"if (revealed) raw.textContent = identity.headers || '(no headers)';",
		"M.maskedHeaders(identity.headers)",
	)
	if n := strings.Count(js, "identity.headers"); n > 6 {
		t.Errorf("identities-detail.js reads identity.headers %d times; raw values belong only to Reveal, Copy, the mask and the summary", n)
	}
	core := executableJS(readUIAsset(t, "js/identities-core.js"))
	requireUIContains(t, core, "revealed: new Set(),")
	if strings.Contains(core, "localStorage") || strings.Contains(executableJS(readUIAsset(t, "js/identities.js")), "localStorage") {
		t.Error("the reveal state and identities must never be written to browser storage")
	}
}

// A live update never replaces an open edit form: it marks it stale and offers
// Reload or Overwrite; detail repaints keep focus by element id.
func TestUIIdentitiesLiveUpdatesDoNotClobberEdits(t *testing.T) {
	edit := executableJS(readUIAsset(t, "js/identities-edit.js"))
	requireUIContains(t, edit,
		"export function checkEditStale() {",
		"if (M.isStale(e.baseline, byName(e.name))) { e.stale = true; paintStale(); }",
		"if (e.stale && !e.overwrite) { paintStale(); return; }",
		"btn('Reload',", "btn('Overwrite',",
		"if (err.status === 409)",
	)
	entry := executableJS(readUIAsset(t, "js/identities.js"))
	requireUIContains(t, entry, "checkEditStale();", "sseTimer = setTimeout(() => {", "if (gen !== S.listGen) return;", "if (gen !== S.detailGen || S.sel !== name) return;")
	detail := executableJS(readUIAsset(t, "js/identities-detail.js"))
	requireUIContains(t, detail, "const keepId = active && host.contains(active) && active.id ? active.id : '';", "if (gen !== S.matrixGen) return;", "if (gen !== S.timelineGen) return;")
}

func TestUIIdentitiesAuthzModalLinksToThePanel(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="authzOpenIdentities"`)
	authz := executableJS(readUIAsset(t, "js/authz.js"))
	requireUIContains(t, authz,
		"busEmit(EVENTS.IDENTITIES_OPEN,{});",
		"busEmit(EVENTS.IDENTITIES_OPEN,{runId:String(d.runId)});",
		"Saved to Access results",
		"'#authzOpenIdentities'",
	)
	// authz.js stays a leaf of identities.js: it reaches the panel over the bus.
	if strings.Contains(authz, "identities") && regexp.MustCompile(`from '\./identities`).MatchString(authz) {
		t.Error("authz.js must not import the identities modules")
	}
}
