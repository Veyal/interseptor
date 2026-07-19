package control

import (
	"regexp"
	"strings"
	"testing"
)

func readUIAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func requireUIContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("UI asset missing %q", want)
		}
	}
}

func requireUIRegex(t *testing.T, body, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(body) {
		t.Errorf("UI executable contract missing pattern %q", pattern)
	}
}

func executableJS(src string) string {
	comments := regexp.MustCompile(`(?s:/\*.*?\*/)|(?m:^\s*//.*$)`)
	return comments.ReplaceAllString(src, "")
}

func TestUIFoundationFocusAndReducedMotionContracts(t *testing.T) {
	css := readUIAsset(t, "app.css")
	requireUIContains(t, css,
		":focus-visible",
		":not(#inspectSplitter)",
		"summary",
		`[contenteditable="true"]`,
		"outline:3px solid",
		"@media (prefers-reduced-motion:reduce)",
		"*,*::before,*::after",
		"animation:none!important",
		"transition:none!important",
	)
	lines := strings.Split(css, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "outline:none") || strings.Contains(line, "#inspectSplitter") {
			continue
		}
		// Full-bleed editors intentionally drop the outer ring in favor of an inset accent.
		if i > 0 && (strings.Contains(lines[i-1], ".rep-edit:focus-visible") || strings.Contains(lines[i-1], ".notes-edit:focus-visible")) {
			continue
		}
		t.Errorf("interactive app style still suppresses focus outline: %s", line)
	}
	requireUIContains(t, css, ".rep-edit:focus-visible,.notes-edit:focus-visible", "inset 3px 0 0 var(--accent)")
	if strings.Contains(readUIAsset(t, "js/app.js"), "outline:none") {
		t.Error("command palette still suppresses its inline focus outline")
	}
	login := readUIAsset(t, "login.html")
	if strings.Contains(login, "outline:none") {
		t.Error("login form still suppresses its focus outline")
	}
	requireUIContains(t, login, ":focus-visible", "outline:3px solid")
}

func TestUIFoundationModalRegistryCoversEveryDialog(t *testing.T) {
	index := readUIAsset(t, "index.html")
	core := executableJS(readUIAsset(t, "js/core.js"))

	re := regexp.MustCompile(`<div id="([^"]+(?:Modal|Lightbox))"`)
	dialogs := re.FindAllStringSubmatch(index, -1)
	if len(dialogs) < 20 {
		t.Fatalf("found %d dialogs, want at least 20", len(dialogs))
	}
	for _, dialog := range dialogs {
		if !strings.Contains(core, "'"+dialog[1]+"'") {
			t.Errorf("modal registry does not include dialog overlay %q", dialog[1])
		}
	}
	requireUIContains(t, core,
		"const modalRegistry=new Map()",
		"const modalStack=[]",
		"const MODAL_Z_BASE=",
		"function modalZIndex(position)",
		"function syncModalZOrder()",
		"function restoreModalZIndex(entry)",
		"previousInlineZIndex",
		"previousZIndexPriority",
		"function topModal()",
		"export function hasOpenModal()",
		"function visibleFocusable",
		"window.addEventListener('keydown'",
		"if(e.key==='Escape')",
		"if(e.defaultPrevented)return",
		"entry.onEscape",
	)
	requireUIRegex(t, core, `(?s)function syncModalZOrder\(\)\{.*?modalStack\.forEach.*?setProperty\('z-index'`)
	requireUIRegex(t, core, `(?s)function restoreModalZIndex\(entry\)\{.*?(setProperty|removeProperty)\('z-index'`)
	if strings.Contains(core, `$$('[role="dialog"]').find(`) {
		t.Error("modal focus trap still discovers the first open dialog by DOM order")
	}
	modalKeys := regexp.MustCompile(`(?s)// Escape and Tab.*?MODAL_IDS\.forEach`).FindString(readUIAsset(t, "js/core.js"))
	if strings.Contains(modalKeys, "},true);") {
		t.Error("modal Escape/Tab listener still runs in capture phase")
	}
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy, "hasOpenModal", "if(hasOpenModal())return")
}

func TestUIFoundationCustomSelectKeyboardContract(t *testing.T) {
	core := executableJS(readUIAsset(t, "js/core.js"))
	requireUIContains(t, core,
		"trigger.setAttribute('role','combobox')",
		"aria-activedescendant",
		"function uiSelectAccessibleName(sel)",
		"function uiSelectHandlesKey(e,open)",
		"function uiSelectMenuZIndex(inst)",
		"menuPreviousZIndex",
		"menuPreviousZIndexPriority",
		"function wireUiSelectLabels(sel,trigger)",
		"e.preventDefault()",
		"trigger.focus()",
		"trigger.click()",
		"e.key==='Escape'?open",
		":['ArrowDown','ArrowUp','Home','End','Enter',' ']",
		"sel.setAttribute('aria-hidden','true')",
		"e.stopPropagation()",
		"case 'ArrowDown'",
		"case 'ArrowUp'",
		"case 'Home'",
		"case 'End'",
		"case 'Enter'",
		"case ' '",
		"case 'Escape'",
		"typeahead",
		"new Event('change',{bubbles:true})",
		"attributeFilter:['disabled','hidden','class','style']",
	)
	requireUIRegex(t, core, `(?s)trigger\.addEventListener\('keydown',e=>\{.*?if\(uiSelectHandlesKey\(e,open\)\)e\.stopPropagation\(\)`)
	requireUIRegex(t, core, `(?s)function uiSelectMenuZIndex\(inst\)\{.*?modalStack.*?getComputedStyle\(inst\.trigger\).*?return`)
	requireUIRegex(t, core, `(?s)function wireUiSelectLabels\(sel,trigger\)\{.*?sel\.labels.*?preventDefault\(\).*?trigger\.click\(\)`)
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	requireUIContains(t, tools, "syncUiSelectStyles", "syncUiSelectStyles(lm)")
}

func TestUIFoundationCommandPaletteAccessibilityContract(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		`role="dialog"`,
		`aria-modal="true"`,
		`aria-labelledby="cmdkTitle"`,
		`role="listbox"`,
		`role="option"`,
		`aria-activedescendant`,
		"openModal(cmdk.el",
		"closeModal(cmdk.el)",
		"e.preventDefault();e.stopPropagation();cmdkClose()",
	)
}

func TestUIFoundationShortcutContract(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	index := readUIAsset(t, "index.html")
	requireUIContains(t, app,
		"const GO_MNEMONICS=",
		"if(gotoPending)",
		"function exactModifiers(e,",
		"function isModShortcut(e,key)",
		"function isModSpace(e)",
		"function isPlainShortcut(e,key",
		"function isHelpShortcut(e)",
		"isModShortcut(e,'k')",
		"isModShortcut(e,'Enter')",
		"isModSpace(e)",
		"isModShortcut(e,'r')",
		"isModShortcut(e,'i')",
		"isHelpShortcut(e)",
		`closest?.('[role="combobox"],[role="listbox"]')`,
		"if(gotoPending&&(typing||hasAnyModifier(e)))resetGoto()",
		"activePanel()==='intercept'",
		"function workflowShortcutBlocked()",
		"if(workflowShortcutBlocked())return",
	)
	requireUIRegex(t, app, `function exactModifiers\(e,\{mod=false,shift=false,alt=false\}=\{\}\)\{[^}]*ctrlKey\|\|e\.metaKey[^}]*shiftKey[^}]*altKey`)
	requireUIRegex(t, app, `function isHelpShortcut\(e\)\{return e\.key==='\?'&&!e\.ctrlKey&&!e\.metaKey&&!e\.altKey;\}`)
	// Repeater Send (Mod+Space / Mod+Enter) must run before the typing early-return.
	requireUIRegex(t, app, `(?s)if\(activePanel\(\)==='repeater'&&\(isModSpace\(e\)\|\|isModShortcut\(e,'Enter'\)\)\).*?if\(typing\)return`)
	requireUIRegex(t, app, `(?s)if\(gotoPending&&\(typing\|\|hasAnyModifier\(e\)\)\)resetGoto\(\).*?if\(typing\)return`)
	requireUIContains(t, index,
		`<kbd>g</kbd><kbd>p</kbd>`,
		`<kbd>r</kbd><kbd>i</kbd>`,
		`<kbd>Ctrl/⌘</kbd><kbd>R</kbd>`,
		`<kbd>Ctrl/⌘</kbd><kbd>I</kbd>`,
		`<kbd>Ctrl/⌘</kbd><kbd>Space</kbd>`,
		`title="Forward (F)"`,
		`title="Drop (D)"`,
	)
	css := readUIAsset(t, "app.css")
	requireUIContains(t, css,
		".rep-edit:focus-visible,.notes-edit:focus-visible",
		"inset 3px 0 0 var(--accent)",
	)
	for _, removed := range []string{
		"mod&&e.key.toLowerCase()==='b'",
		"mod&&e.key.toLowerCase()==='r'",
		"mod&&e.key.toLowerCase()==='i'",
		"mod&&(e.key===' '||e.code==='Space')",
		"mod&&e.key.toLowerCase()==='k'",
		"!mod&&(e.key==='r'||e.key==='i')",
		"!mod&&(e.key==='f'||e.key==='d')",
		"isPlainShortcut(e,'?',{shift:true})",
		"Ctrl+Shift+F",
		"Ctrl+D",
	} {
		if strings.Contains(app, removed) || strings.Contains(index, removed) {
			t.Errorf("removed shortcut remains: %q", removed)
		}
	}
}
