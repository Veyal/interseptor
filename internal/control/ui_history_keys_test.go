package control

import (
	"strings"
	"testing"
)

// History was 85 Tab stops for 85 flows, ArrowDown on a row did nothing, and j/k
// moved the selection while focus stayed behind. The list is now one listbox
// with a roving stop; keys move selection and focus together.
func TestUIHistoryListboxKeyboardModel(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		`role="option" tabindex="-1" aria-selected="${rowSelected(f.id)?'true':'false'}"`,
		"box.setAttribute('role','listbox');box.setAttribute('aria-label','Captured flows');box.setAttribute('aria-multiselectable','true');",
		"r.setAttribute('aria-posinset',String(start+i+1));",
		"const to=listKeyTarget(e.key,list.findIndex(f=>f.id===from),list.length,page);",
		"revealFlowRow(id,{focus:true});",
		"chip.tabIndex=-1;",
	)
	if strings.Contains(proxy, `aria-pressed="${state.selected.has(f.id)`) {
		t.Error("History rows are options now; multi-selection is aria-selected, not aria-pressed")
	}
	// Pads are resized before any focus() in the keyed reconcile: a focus forces
	// layout, and the briefly shorter content clamped scrollTop (End jumped back).
	start := strings.Index(proxy, "function reconcileVirtualRows(){")
	end := strings.Index(proxy[start:], "\n}\n")
	body := proxy[start : start+end]
	pads := strings.Index(body, "bottom.style.height=win.bottomPad+'px';")
	if pads < 0 || strings.Index(body, ".focus(") < pads {
		t.Error("reconcileVirtualRows must resize the pads before it moves focus")
	}

	app := readUIAsset(t, "js/app.js")
	requireUIContains(t, app,
		`[role="listbox"]:not(#rows)`,
		"const carryFocus=focusInFlowList();",
		"revealFlowRow(id,{focus:carryFocus});",
	)
}

// The sort headers name their state, and keyboard sorting keeps focus on the
// re-rendered header.
func TestUIHistorySortHeadersNameTheirState(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"`${accessible}, sorted ${sd>0?'ascending':'descending'}`",
		"`${accessible}, sortable`",
		"if(hadFocus)$('#flowHead')?.querySelector(`[data-sort=\"${CSS.escape(k)}\"]`)?.focus();",
	)
}
