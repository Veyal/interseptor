package control

import (
	"regexp"
	"strings"
	"testing"
)

// axe-core nested-interactive (serious): a History row is role="option", and an
// option may not contain focusable content. A negative tabindex does not keep
// assistive technology out, so the row's tag chips and its actions glyph are
// plain spans reached through the row's own handlers and its context menu.
func TestUIA11yR2HistoryRowHasNoInteractiveDescendants(t *testing.T) {
	js := readUIAsset(t, "js/proxy.js")
	start := strings.Index(js, "function flowRowHTML(f){")
	end := strings.Index(js, "let flowSignalWindowAt=0")
	if start < 0 || end < start {
		t.Fatal("flowRowHTML/wireFlowRow not found in proxy.js")
	}
	row := js[start:end]
	for _, bad := range []string{"<button", "<a ", "<input", "tabIndex=", "'role','button'", `role="button"`} {
		if strings.Contains(row, bad) {
			t.Errorf("a History row must not render or wire %q inside role=option", bad)
		}
	}
	if n := strings.Count(row, "tabindex="); n != 1 {
		t.Errorf("only the row itself may carry a tabindex, found %d", n)
	}
	requireUIContains(t, row,
		`<span class="tr-more rowOverflow" data-rowmenu="${f.id}" role="img" aria-label="Actions for flow #${f.id}">`,
		`<span class="flowtag" data-tagchip="${escAttr(t)}"`,
		// Chip right-click and the keyboard menu key stay on the row.
		"if(chip){openTagChipMenu(e.clientX,e.clientY,chip.dataset.tagchip,id);return;}",
		"e.key==='ContextMenu'||(e.shiftKey&&e.key==='F10')",
		// The row's name carries the tags the chips show.
		"`, tags ${flow.tags.join(', ')}`",
	)
	// Mouse paths: chip click filters, the glyph opens the row menu, and the
	// keyboard menu offers the same tag filter.
	requireUIContains(t, js,
		"if(chip){filterByTag(chip.dataset.tagchip);return;}",
		"const more=e.target.closest('.rowOverflow[data-rowmenu]');",
		"children.push({label:'Filter · '+t,icon:'tag',on:state.filters.tag===t,act:()=>filterByTag(t)});",
	)
}

// axe-core aria-required-children (critical): the Repeater/Intruder strip holds
// Close and "+" buttons next to the tabs, so the strip is not the tablist. A
// separate element owns exactly the tab buttons.
func TestUIA11yR2ToolTablistOwnsOnlyTabs(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	start := strings.Index(core, "mgr.render=function(barSel){")
	end := strings.Index(core, "mgr.switchTo=function(")
	if start < 0 || end < start {
		t.Fatal("createTabManager render not found")
	}
	render := core[start:end]
	if strings.Contains(render, "bar.setAttribute('role','tablist')") {
		t.Error("the strip also holds Close and + buttons; it must not be the tablist")
	}
	requireUIContains(t, render,
		"for(const a of ['role','aria-label','aria-orientation'])bar.removeAttribute(a);",
		"list.setAttribute('role','tablist');list.setAttribute('aria-label',tablistLabel);list.setAttribute('aria-orientation','horizontal');",
		"list.setAttribute('aria-owns',mgr.tabs.map(t=>bar.id+'Tab'+t.tid).join(' '));",
		`aria-posinset="${ti+1}" aria-setsize="${mgr.tabs.length}"`,
		// Delete on the focused tab closes it unless its Close button is disabled.
		"else if(e.key==='Delete'){",
		"if(closeBtn&&!closeBtn.disabled){e.preventDefault();mgr.close(tid,true);}",
	)
	css := readUIAsset(t, "app.css")
	rule := regexp.MustCompile(`\.rep-tablist\{([^}]*)\}`).FindStringSubmatch(css)
	if rule == nil {
		t.Fatal(".rep-tablist rule missing")
	}
	// It must stay rendered: display:none or visibility:hidden drops it, and the
	// tabs it owns, from the accessibility tree.
	if strings.Contains(rule[1], "display:none") || strings.Contains(rule[1], "visibility:hidden") || !strings.Contains(rule[1], "position:absolute") {
		t.Errorf(".rep-tablist must be rendered but out of flow, got %q", rule[1])
	}
}

// axe-core landmark-complementary-is-top-level (moderate): the finding rail sits
// inside <main>, so it is a labelled group, not an <aside> landmark.
func TestUIA11yR2FindingRailIsNotANestedComplementaryLandmark(t *testing.T) {
	js := readUIAsset(t, "js/findings.js")
	requireUIContains(t, js, `<div class="find-rail${findRailOpen ? ' is-open' : ''}" id="findRail" role="group" aria-label="Finding properties">`)
	if strings.Contains(js, `<aside class="find-rail`) {
		t.Error("the finding rail must not be an <aside> inside <main>")
	}
}

// WCAG 2.2 SC 2.5.8: 24px targets at phone width without changing desktop density.
func TestUIA11yR2PhoneTargetsReach24px(t *testing.T) {
	app := readUIAsset(t, "app.css")
	requireUIContains(t, app,
		".search input{flex:1;align-self:stretch;",
		".map-crumb a{display:inline-block;position:relative;padding:6px;margin:-6px}",
		".icpt-cond .icpt-chk input{width:24px;height:24px;min-width:24px}",
	)
	proxy := readUIAsset(t, "panel-proxy.css")
	requireUIContains(t, proxy,
		"#panel-proxy #rows .trow .tr-path{grid-area:path;padding-block:5px;margin-block:-5px}",
		`#panel-proxy #rows .trow .flowtag::after{content:"";position:absolute;left:50%;top:50%;width:max(100%,24px);height:24px;transform:translate(-50%,-50%)}`,
	)
	// The chip hit area and the crumb padding are phone-only.
	for _, c := range []struct{ css, rule string }{{proxy, ".flowtag::after{"}, {app, ".map-crumb a{display:inline-block"}, {app, ".icpt-cond .icpt-chk input{"}} {
		i := strings.Index(c.css, c.rule)
		if i < 0 || strings.LastIndex(c.css[:i], "@media (max-width:720px){") < strings.LastIndex(c.css[:i], "\n}\n") {
			t.Errorf("%q must live inside @media (max-width:720px)", c.rule)
		}
	}
}
