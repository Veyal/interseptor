package control

import (
	"strings"
	"testing"
)

// Round 5: a drop waiting on its Undo toast must win over every bulk release.
// The UI marks the item on the server while the toast is up (so another
// client's intercept-off or Forward cannot send it), clears the mark on Undo,
// and commits pending drops before it turns intercept off itself or the window
// goes away.
func TestUIPendingDropWinsOverBulkRelease(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/intercept.js"))
	requireUIContains(t, src,
		"markDropPending({side:sel.side,id:sel.id},true);",
		"onUndo:()=>{dropToasts.delete(key);if(dropScheduler.undo(key)){markDropPending({side:sel.side,id:sel.id},false);",
		"dropScheduler.takeAll(",
		"if(state.intercept.enabled)await commitPendingDrops('req');",
		"if(state.intercept.responseEnabled)await commitPendingDrops('resp');",
		"window.addEventListener('pagehide',()=>{commitPendingDrops('',{keepalive:true});});",
		"keepalive:true",
	)
	toggle := src[strings.Index(src, "export async function toggleIntercept()"):]
	toggle = toggle[:strings.Index(toggle, "$('#interceptToggle').onclick")]
	if strings.Index(toggle, "commitPendingDrops('req')") > strings.Index(toggle, "/api/intercept/toggle") {
		t.Error("pending drops must be committed before intercept is turned off")
	}
}

// A held request whose client hung up is not an operator drop: History shows
// ABRT and "aborted", not DROP / "dropped".
func TestUIClientDisconnectWhileHeldIsAbortedNotDropped(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy,
		"const CLIENT_HELD_ABORT_ERROR='client disconnected while held';",
		"const clientAborted=f=>!f.status&&(f.error===CLIENT_ABORT_ERROR||f.error===CLIENT_HELD_ABORT_ERROR);",
	)
	row := proxy[strings.Index(proxy, "const stHTML="):]
	row = row[:strings.Index(row, "\n")]
	if strings.Index(row, "clientAborted(f)") > strings.Index(row, "FLAG_DROPPED") {
		t.Error("the row must test clientAborted before FLAG_DROPPED")
	}
	insp := proxy[strings.Index(proxy, "}else if(d.flags&FLAG_TLS){"):]
	if a, d := strings.Index(insp, "clientAborted(d)"), strings.Index(insp, "d.flags&FLAG_DROPPED"); a < 0 || a > d {
		t.Error("the inspector must test clientAborted before the dropped branch")
	}
}
