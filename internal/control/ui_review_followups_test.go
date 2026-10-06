package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUIInspectorFilterSignatureIncludesSort(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "sort:(state.sort&&state.sort.key)||'',dir:sortDirParam(),")
}

func TestUITagAndScopeMutationFailuresUseToastError(t *testing.T) {
	tags := executableJS(readUIAsset(t, "js/tags.js"))
	requireUIContains(t, tags,
		"import { $, esc, escAttr, api, state, toast, toastError,",
		"toastError('Tag colour not saved',e)",
		"toastError('Tagging failed',e)",
	)
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy,
		"toastError('Scope rule not saved',e)",
		"toastError('Scope rule not deleted',e)",
	)
}

func TestUIHistoryAuditInlineStylesReplacedByClasses(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, banned := range []string{
		`style="text-align:${c.align}"`,
		`style="display:flex;gap:6px;margin-bottom:10px"`,
		`style="display:flex;gap:10px;padding:3px 0`,
		`style="width:60px;flex:none"`,
		`style="padding:12px;color:var(--fg2);line-height:1.5"`,
		`style="flex:1;font-family:var(--mono)"`,
		`id="wsReplayOut" style=`,
		`style="margin-top:8px`,
		`style="padding:14px`,
	} {
		if strings.Contains(src, banned) {
			t.Errorf("proxy.js still carries inline style %q", banned)
		}
	}
	requireUIContains(t, src, `class="ws-replay-row"`, `class="tls-blocked"`, `class="ws-frame${`, "u-ta-'+c.align")
	css := readUIAsset(t, "app.css") + readUIAsset(t, "surfaces.css")
	requireUIContains(t, css, ".ws-replay-row{", ".tls-blocked{", ".ws-frame{", ".u-ta-right{")
}

func TestUISelDeleteRestoresButtonBeforeSelectionBarUpdate(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	start := strings.Index(src, "$('#selDelete').onclick=async()=>")
	end := strings.Index(src, "const SPLITTER_KEY")
	if start < 0 || end <= start {
		t.Fatal("selDelete handler boundary not found")
	}
	h := src[start:end]
	if strings.Contains(h, "finally{") {
		t.Error("selDelete must not reset the button in an unconditional finally that runs after updateSelBar")
	}
	restore := strings.Index(h, "restoreSelDelete(btn)")
	update := strings.Index(h, "updateSelBar()")
	if restore < 0 || update < 0 || restore > update {
		t.Errorf("selDelete must restore the button before updateSelBar (restore=%d update=%d)", restore, update)
	}
	requireUIContains(t, src, "function restoreSelDelete(btn)")
}

// TestUISSEReconnectAttemptsDoNotOverlap drives scheduleSseReconnect with fake
// timers: a timer that already fired (probe in flight) must not open a second
// stream once a newer attempt has taken over, and a stale failed probe must not
// reschedule on top of the newer attempt.
func TestUISSEReconnectAttemptsDoNotOverlap(t *testing.T) {
	app := readUIAsset(t, "js/app.js")
	script := repeaterRenderJS(t, app, "function sseRetryDelay(attempt)") +
		repeaterRenderJS(t, app, "function scheduleSseReconnect(immediate)") + `
const eq=(got,want,msg)=>{if(got!==want)throw Error(msg+': got '+got+' want '+want);};
`
	prelude := `
const SSE_BACKOFF_MS=[1000,2000,5000,15000];
let sseRetryTimer=null,sseRetryCount=0,sseAttemptToken=0;
const timers=[],probes=[];let connects=0;
const clearTimeout=()=>{};
const setTimeout=fn=>{timers.push(fn);return timers.length;};
const setSseStatus=()=>{};
const connectEvents=()=>{connects++;};
const api=()=>new Promise((res,rej)=>probes.push({res,rej}));
`
	body := `
scheduleSseReconnect();
const stale=timers[0]();
scheduleSseReconnect(true);
const fresh=timers[1]();
probes[1].res();await fresh;
eq(connects,1,'the newest attempt opens one stream');
probes[0].res();await stale;
eq(connects,1,'a stale probe must not open a second stream');
scheduleSseReconnect();
const failing=timers[2]();
scheduleSseReconnect(true);
probes[2].rej(new Error('offline'));await failing;
eq(timers.length,4,'a stale failed probe must not reschedule over the newer attempt');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", prelude+script+body).CombinedOutput(); err != nil {
		t.Fatalf("SSE reconnect overlap: %v\n%s", err, out)
	}
}

func TestUISSEEventSourceCreationIsSingleFlight(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app,
		"let sseAttemptToken=0",
		"const token=++sseAttemptToken",
		"if(token!==sseAttemptToken)return;",
		"sseAttemptToken++;if(sseSource){sseSource.close();sseSource=null;}",
		"es.onerror=()=>{if(sseSource!==es)return;",
	)
}
