package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestUIShellHasLandmarkSkipLinkAndAccessibleNames(t *testing.T) {
	index := readUIAsset(t, "index.html")
	body := index[strings.Index(index, "<body>"):]
	if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(body, "<body>")), `<a class="skip" id="skipLink" href="#main">`) {
		t.Error("the skip link must be the first child of <body>")
	}
	requireUIContains(t, index,
		`<main id="main" tabindex="-1" aria-label="Workspace">`,
		"</main>",
		`id="capDot" role="img" aria-label="Capture idle"`,
		`id="icptStat" role="status" aria-live="polite"`,
		`id="retSelectAll" aria-label="Select all hosts"`,
	)
	if strings.Contains(index, `<div id="main">`) {
		t.Error("#main must be a <main> landmark")
	}
	if strings.Contains(index, `id="crumb" aria-live`) {
		t.Error("the breadcrumb must not be a bare live region")
	}
	for _, dot := range []string{"intrBadge", "scanBadge", "mapBadge"} {
		if !strings.Contains(index, `id="`+dot+`" class="nav-dot" aria-hidden="true"`) {
			t.Errorf("%s must be decorative; its state is announced through the tab text", dot)
		}
	}
	core := readUIAsset(t, "js/core.js")
	requireUIContains(t, core,
		`aria-label="Copy decoded value"`,
		`aria-label="Dismiss decoded value"`,
		"const UI_SELECT_SKIP='#rows,.intr-virt-body,.map-virt-body'",
		"m.target.closest?.(UI_SELECT_SKIP)",
	)
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"const NAV_DOT_NOTE=' (new updates)'",
		"n.className='visually-hidden nav-dot-note'",
		"e.preventDefault();$('#main')?.focus()",
		"function setCapDot(live)",
	)
}

func TestUIShortcutsAndPaletteCoverWorkflowActions(t *testing.T) {
	index := readUIAsset(t, "index.html")
	sheet := index[strings.Index(index, `id="shortcutsModal"`):]
	sheet = sheet[:strings.Index(sheet, "</div>\n  </div>\n</div>")+1]
	for _, want := range []string{
		"Find inside the inspected request or response",
		"Add the selected Proxy flow(s) to a finding",
		"Start the Intruder attack",
		"<b>New finding</b>",
		"<b>Export findings</b>",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("shortcut sheet missing %q", want)
		}
	}
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, want := range []string{
		"activePanel()==='intruder'&&isModShortcut(e,'Enter')",
		"intrStart()",
		"flowSendShortcutAllowed()&&isPlainShortcut(e,'a')",
		"{t:'New finding'",
		"{t:'Export findings'",
		"{t:'Add selected flow to finding'",
		"{t:'Find inside selected message (Ctrl+F)'",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
	if strings.Contains(app, "intrStart()") && !strings.Contains(app, "activePanel()==='intruder'") {
		t.Error("the Intruder start shortcut must be scoped to the Intruder panel")
	}
}

func TestUIPaletteUsesClassesNotInlineStyles(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/cmdk.js"))
	start := strings.Index(app, "function cmdkBuild()")
	end := strings.Index(app, "function cmdkRun(")
	if start < 0 || end < start {
		t.Fatal("palette functions not found")
	}
	palette := app[start:end]
	if strings.Contains(palette, "style=") || strings.Contains(palette, "cssText") {
		t.Error("the command palette must use class names, not inline styles")
	}
	for _, class := range []string{"cmdk-overlay", "cmdk-shell", "cmdk-input", "cmdk-list", "cmdk-row", "cmdk-foot", "cmdk-empty", "cmdk-text", "cmdk-sub"} {
		if !strings.Contains(palette, class) {
			t.Errorf("palette markup missing class %s", class)
		}
		if !strings.Contains(readUIAsset(t, "surfaces.css"), "."+class) {
			t.Errorf("surfaces.css missing .%s", class)
		}
	}
	for _, file := range []string{"js/activity.js"} {
		if strings.Contains(executableJS(readUIAsset(t, file)), `class="ok" aria-hidden="true" style=`) {
			t.Errorf("%s still writes the activity dot colour inline", file)
		}
	}
	if strings.Contains(app, "el.style.color='var(--accent)'") {
		t.Error("the update badge must use .is-update, not inline colour")
	}
}

func TestUISSENudgesAreDebouncedAndPanelGated(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, want := range []string{
		"'tags.update':{contract:'always-reload (debounced)',run:debounce(loadTags,SSE_NUDGE_DEBOUNCE_MS)}",
		"'findings.update':{contract:'panel-gated nudge (debounced)',run:debounce(()=>onPanelUpdate('findings',loadFindings),SSE_NUDGE_DEBOUNCE_MS)}",
		"'notes.update':{contract:'panel-gated nudge (debounced)',run:debounce(()=>onPanelUpdate('notes',loadNotes),SSE_NUDGE_DEBOUNCE_MS)}",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("SSE handler contract missing %q", want)
		}
	}
	// Panel activation refetches, so a gated nudge cannot leave the panel stale.
	for _, tab := range []string{"findings", "notes"} {
		if !strings.Contains(app, "if(t.dataset.tab==='"+tab+"')load") {
			t.Errorf("activating the %s tab must refetch", tab)
		}
	}
	script := repeaterRenderJS(t, readUIAsset(t, "js/app.js"), "function debounce(fn,ms)") + `
const timers=[];globalThis.setTimeout=(f,ms)=>{const h={f,live:true};timers.push(h);return h;};
globalThis.clearTimeout=h=>{if(h)h.live=false;};
let calls=0;const d=debounce(x=>{calls+=x;},200);
d(1);d(1);d(1);
for(const h of timers)if(h.live){h.live=false;h.f();}
if(calls!==1)throw Error('burst must coalesce into one call, got '+calls);
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("debounce: %v\n%s", err, out)
	}
	re := regexp.MustCompile(`(?s)function scheduleMapRefresh\(\)\{\s*(?://[^\n]*\n\s*)*if\(mapRefreshT\)return;\s*mapRefreshT=setTimeout\(\(\)=>\{\s*mapRefreshT=null;`)
	if !re.MatchString(readUIAsset(t, "js/app.js")) {
		t.Error("scheduleMapRefresh must be a throttle: return when a timer is pending and clear it inside the callback")
	}
}

func TestUIVersionBadgeAndHumanInputReportFailures(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"el.textContent='v?'",
		"el.dataset.failed='true'",
		"catch(e){if(el)markVersionFailed(el);}",
		"el.classList.add('is-update')",
	)
	hi := executableJS(readUIAsset(t, "js/humaninput.js"))
	requireUIContains(t, hi,
		"function renderHumanInputError(e)",
		`role="alert"`,
		"hi-retry",
		"if(!humanInputShown)renderHumanInputError(e)",
		"toastError('Could not send answer', e)",
	)
	// respond()'s catch must refresh the list after reporting the failure.
	respond := hi[strings.Index(hi, "async function respond("):]
	catchAt := strings.Index(respond, "catch (e)")
	if catchAt < 0 || strings.Index(respond[catchAt:], "loadHumanInput()") < 0 {
		t.Error("a failed answer must reload prompts to clear expired ones")
	}
}

func TestUITagChipColorsNormalizeAndFollowTheme(t *testing.T) {
	tags := readUIAsset(t, "js/tags.js")
	script := repeaterRenderJS(t, tags, "export function normalizeTagHex(value)") +
		repeaterRenderJS(t, tags, "export function tagChipStyle(tag)") + `
const TAG_COLORS=[['red','#ff5b5b','var(--red)'],['blue','#4aa8ff','var(--blue)']];
const state={tagColors:{}};
const eq=(got,want,msg)=>{if(got!==want)throw Error(msg+': got '+got+' want '+want);};
eq(normalizeTagHex(' #FF5B5B '),'#ff5b5b','hex is trimmed and lowercased');
eq(normalizeTagHex('#abc'),'#aabbcc','short hex expands');
eq(normalizeTagHex('red;x:y'),'','non-hex values are rejected');
eq(normalizeTagHex(null),'','null is rejected');
state.tagColors.a='#FF5B5B';
eq(tagChipStyle('a'),'color:var(--red);border-color:var(--red)','mixed-case preset hex maps to the theme token');
state.tagColors.b='#123456';
eq(tagChipStyle('b'),'color:color-mix(in srgb, #123456 55%, var(--fg));border-color:#123456','custom colors mix toward the foreground');
eq(tagChipStyle('missing'),'','no color means default styling');
state.tagColors.c='#fff"onload=';
eq(tagChipStyle('c'),'','unsafe values never reach the style attribute');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("tag chip colors: %v\n%s", err, out)
	}
	requireUIContains(t, tags, "refreshVisibleRows()")
	if strings.Contains(executableJS(tags), "renderRows()") {
		t.Error("loadTags must patch rows via refreshVisibleRows, not rebuild History")
	}
}
