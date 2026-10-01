package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestUISSEStaleReconnectDecision exercises the reconnect decision on its own:
// the boot connection never resyncs, a short blip stays on the cheap per-event
// path, and only a gap past the staleness threshold (or an unknown last-event
// time) escalates to one full resync.
func TestUISSEStaleReconnectDecision(t *testing.T) {
	app := readUIAsset(t, "js/app.js")
	gap := regexp.MustCompile(`const STALE_GAP_MS=\d+;`).FindString(app)
	if gap == "" {
		t.Fatal("app.js no longer declares a STALE_GAP_MS threshold")
	}
	script := gap + "\n" + repeaterRenderJS(t, app, "function shouldResyncOnReconnect(lastEventAt,now,reason)") + `
const eq=(got,want,msg)=>{if(got!==want)throw Error(msg+': got '+got+' want '+want);};
const t0=1700000000000;
eq(shouldResyncOnReconnect(0,t0,'boot'),false,'the boot connection must never resync');
eq(shouldResyncOnReconnect(t0-5*60*1000,t0,'boot'),false,'boot must not resync even with an old timestamp');
eq(shouldResyncOnReconnect(t0,t0+250,'reconnect'),false,'a short blip must stay on the per-event path');
eq(shouldResyncOnReconnect(t0,t0+STALE_GAP_MS,'reconnect'),false,'a gap exactly at the threshold is not stale');
eq(shouldResyncOnReconnect(t0,t0+STALE_GAP_MS+1,'reconnect'),true,'a gap past the threshold must resync');
eq(shouldResyncOnReconnect(t0,t0+30*60*1000,'reconnect'),true,'a backgrounded tab must resync on wake');
eq(shouldResyncOnReconnect(0,t0,'reconnect'),true,'an unknown last-event time cannot prove freshness');
if(STALE_GAP_MS<=0)throw Error('the staleness threshold must be positive');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("SSE stale-reconnect decision: %v\n%s", err, out)
	}
}

// TestUISSEStaleReconnectResyncsOnceInsteadOfReplaying pins the wiring around the
// decision: `hello` distinguishes boot from reconnect, every message refreshes the
// freshness clock, and a stale reconnect refetches through the existing reload
// paths rather than replaying per-event catch-up it cannot trust.
func TestUISSEStaleReconnectResyncsOnceInsteadOfReplaying(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, contract := range []string{
		"const reason=sseConnectedOnce?'reconnect':'boot'",
		"if(shouldResyncOnReconnect(lastSSEMsgAt,now,reason))resyncAfterStaleReconnect()",
		"sseConnectedOnce=true",
		"lastSSEMsgAt=now",
		"es.onmessage=e=>{lastSSEMsgAt=Date.now();",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("SSE reconnect wiring missing %q", contract)
		}
	}
	if strings.Contains(app, "gap>STALE_GAP_MS") {
		t.Error("the stale-reconnect decision must live in shouldResyncOnReconnect, not inline in the hello handler")
	}
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
