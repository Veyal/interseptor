package control

import (
	"strings"
	"testing"
)

// With a flow open in the bottom inspector and the TLS diagnosis banner shown,
// History measured 0px tall at 1280x800. The list keeps a row minimum, the
// inspector may shrink, and the banner keeps its explanation behind Details.
func TestUIHistoryKeepsRowsVisibleWithInspectorAndBanner(t *testing.T) {
	css := readUIAsset(t, "panel-proxy.css")
	requireUIContains(t, css,
		"#panel-proxy #rows{min-height:calc(var(--row-h,30px) * 5)}",
		"#panel-proxy #inspect{flex:0 1 auto;min-height:120px}",
		"#panel-proxy.active #rows{min-height:calc(56px * 4)}",
	)
	proxy := readUIAsset(t, "js/proxy.js")
	if strings.Contains(proxy, "inspect.style.flex='none'") {
		t.Error("the splitter must not pin the inspector with an inline flex:none; it would stop the inspector from shrinking")
	}
	tls := readUIAsset(t, "js/tlsdiag.js")
	requireUIContains(t, tls,
		`data-tls-action="details" aria-expanded="false" aria-controls="tlsDiagMore"`,
		`<div class="tls-diag-more" id="tlsDiagMore" hidden>`,
		"banner.innerHTML = compact;",
	)
}

// The search chip named every non-body search "path" even though the default
// scope searches everywhere.
func TestUIHistorySearchChipNamesTheScope(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"add('search',{body:'body',id:'id',script:'saved search'}[f.searchScope]||'search',f.search);",
		"syncSearchPlaceholder();renderChips();if(state.filters.search)loadFlows();",
	)
}

// The "scope switch moved" tip is for viewers who knew the old layout.
func TestUIHistoryScopeCoachTipOnlyForReturningViewers(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy, "seen=localStorage.getItem(KEY)==='1'||![FLOW_COLS_KEY,FLOW_COLW_KEY,HIDE_TLS_KEY,'proxy.dock','inspect.height'].some(k=>localStorage.getItem(k)!==null);")
}
