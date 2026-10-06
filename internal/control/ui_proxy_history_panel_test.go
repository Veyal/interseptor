package control

import (
	"strings"
	"testing"
)

func TestUIHistoryReloadKeepsScrollAndCannotStarve(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "if(signatureChanged&&box)box.scrollTop=0;", "RELOAD_MAX_WAIT_MS=1000", "reloadFirstAt")
	if strings.Contains(src, "if(box)box.scrollTop=0;") {
		t.Error("loadFlows must not reset History scroll on a same-filter reload")
	}
}

func TestUIHistoryScrollAndSelectionAvoidFullRebuilds(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src,
		"onScroll:()=>{if(!reconcileVirtualRows())renderRows();}",
		"function syncSelectedRow(prevId)",
		"if(!syncSelectedRow(prevSelId))renderRows();",
	)
	if strings.Contains(src, "onScroll:renderRows") {
		t.Error("virtual scroll must reconcile before falling back to renderRows")
	}
}

func TestUIHistoryWebSocketFramesCoalesceAndKeepReplayInput(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "export function scheduleWSFrames(id)", "WS_FRAME_COALESCE_MS=250", `id="wsFrameList"`, "wsFrameList")
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "scheduleWSFrames(state.selId)")
	if strings.Contains(app, "renderWSFrames(state.selId)") {
		t.Error("ws.frame events must go through the coalescing scheduler")
	}
}

func TestUIHistoryPhoneColumnsFitNarrowViewports(t *testing.T) {
	src := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, src, "(max-width:720px)", "PHONE_FLOW_COLS", "visibleFlowCols()", "flowPhoneQuery.addEventListener")
}

func TestUIHistoryBulkDeleteHasInFlightState(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "if(btn.disabled)return;", "btn.setAttribute('aria-busy','true')", "Deleting…", "toastError('Delete failed',e)")
}

func TestUIHistoryFindInResponseCachesRawBody(t *testing.T) {
	src := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, src, "function fetchRawMessage(flowId,side,detail)", "rawCache")
	if strings.Count(src, "await fetchRawMessage(flowId,side,detail)") != 2 {
		t.Error("renderSide raw/hex fetches must go through the cached helper")
	}
}

func TestUIHistoryAddToFindingWording(t *testing.T) {
	html := readUIAsset(t, "index.html")
	requireUIContains(t, html, `id="selAddFinding"`, "Add to finding", `id="inspectAddFinding"`)
	if strings.Contains(html, "</svg> Finding</button>") {
		t.Error("selection bar must say Add to finding")
	}
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "inspectAddFinding", "toastError(")
	requireUIContains(t, strings.SplitN(readUIAsset(t, "js/proxy.js"), "\n", 2)[0], "toastError")
}
