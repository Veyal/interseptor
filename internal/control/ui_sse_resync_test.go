package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestUISSEReconnectDecision exercises the reconnect decision on its own: the
// clean boot connection never resyncs, but a boot that followed a failed connect
// and every later reconnect (of any gap length) do.
func TestUISSEReconnectDecision(t *testing.T) {
	app := readUIAsset(t, "js/app.js")
	script := repeaterRenderJS(t, app, "function shouldResyncOnReconnect(reason,hadError)") +
		repeaterRenderJS(t, app, "function sseRetryDelay(attempt)") +
		regexp.MustCompile(`const SSE_BACKOFF_MS=\[[^\]]*\];`).FindString(app) + `
const eq=(got,want,msg)=>{if(got!==want)throw Error(msg+': got '+got+' want '+want);};
eq(shouldResyncOnReconnect('boot',false),false,'the clean boot connection must never resync');
eq(shouldResyncOnReconnect('boot',true),true,'a boot that followed a failed connect must resync');
eq(shouldResyncOnReconnect('reconnect',false),true,'every reconnect resyncs, however short the gap');
eq(shouldResyncOnReconnect('reconnect',true),true,'a reconnect after errors resyncs');
const delays=[0,1,2,3,4,50].map(sseRetryDelay);
eq(delays.join(','),'1000,2000,5000,15000,15000,15000','backoff is 1s,2s,5s,15s and capped');
eq(sseRetryDelay(-1),1000,'a negative attempt clamps to the first delay');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("SSE reconnect decision: %v\n%s", err, out)
	}
}

// TestUISSEClosedStreamRecovers pins the wiring: a CLOSED EventSource is rebuilt
// with capped backoff after probing the API, the status exposes a reconnect
// button, and every hello after a gap resyncs through one debounced call.
func TestUISSEClosedStreamRecovers(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, contract := range []string{
		"let sseSource=null",
		"es.readyState===EventSource.CLOSED",
		"scheduleSseReconnect()",
		"await api('/api/version')",
		"const reason=sseConnectedOnce?'reconnect':'boot'",
		"if(shouldResyncOnReconnect(reason,sseHadError))scheduleResync()",
		"sseConnectedOnce=true",
		"sseHadError=true",
		"setSseStatus('offline')",
		"clearTimeout(sseResyncTimer)",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("SSE recovery wiring missing %q", contract)
		}
	}
	if strings.Contains(app, "lastSSEMsgAt") || strings.Contains(app, "STALE_GAP_MS") {
		t.Error("the staleness-gap heuristic was replaced by resync-on-every-reconnect")
	}
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="sseRetry"`)
	requireUIContains(t, readUIAsset(t, "surfaces.css"), ".sse-dot.offline{")
}

// TestUISSEResyncRefetchesEveryStore pins the refetch list.
func TestUISSEResyncRefetchesEveryStore(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	start := strings.Index(app, "function resyncAfterStaleReconnect()")
	if start < 0 {
		t.Fatal("resyncAfterStaleReconnect not found")
	}
	end := strings.Index(app[start:], "\n}")
	if end < 0 {
		t.Fatal("unterminated resyncAfterStaleReconnect")
	}
	body := app[start : start+end]
	for _, refetch := range []string{
		"scheduleReload()",
		"loadScope()",
		"loadRules()",
		"loadTags()",
		"loadViews()",
		"loadSession()",
		"loadSettings()",
		"loadProject()",
		"refreshIntercept()",
		"loadHumanInput()",
	} {
		if !strings.Contains(body, refetch) {
			t.Errorf("stale-reconnect resync must refetch via %q", refetch)
		}
	}
	for _, panel := range []string{"intruder", "scanner", "findings", "notes", "activity", "map"} {
		if !strings.Contains(body, `data-tab="`+panel+`"`) {
			t.Errorf("stale-reconnect resync skips the %s panel", panel)
		}
	}
}
