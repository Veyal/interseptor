package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUIFindingsDeferredRefreshWaitsForPointerClick(t *testing.T) {
	source := readUIAsset(t, "js/findings.js")
	start := strings.Index(source, "function bindFindingPointerGuard(")
	end := strings.Index(source, "function findingDetailEditPending(")
	if start < 0 || end <= start {
		t.Fatal("finding refresh needs a pointer interaction guard")
	}
	pendingEnd := strings.Index(source[end:], "function refreshDeferredFindingDetail(")
	script := `
let findingDetailPointerActive=false, refreshes=0;
let bodyEditing=false,selFinding=1,bodySavesInFlight=0,findingWritesInFlight=0,findEditMode=true;
const findingDrafts={has:()=>false},bodySaveTimers=new Map(),document={activeElement:null};
const target=()=>({handlers:{},addEventListener(name,fn){this.handlers[name]=fn;},querySelector(){return null;}});
const detail=target(),window=target(),$=()=>detail;
function refreshDeferredFindingDetail(){if(!findingDetailEditPending())refreshes++;}
` + source[start:end+pendingEnd] + `
bindFindingPointerGuard(detail);
detail.handlers.pointerdown({pointerId:1,isPrimary:true});
setTimeout(refreshDeferredFindingDetail,0); // A focused field blurs before link click.
await new Promise(resolve=>setTimeout(resolve,10));
if(refreshes!==0||!findingDetailEditPending())throw Error('refresh replaced the pressed link before click');
window.handlers.pointerup({pointerId:1});
if(refreshes!==0)throw Error('pointerup refreshed before click dispatch');
await new Promise(resolve=>setTimeout(resolve,10));
if(refreshes!==1||findingDetailPointerActive)throw Error('refresh did not resume after click');
detail.handlers.pointerdown({pointerId:1,isPrimary:true});
window.handlers.pointerup({pointerId:1});
detail.handlers.pointerdown({pointerId:1,isPrimary:true});
await new Promise(resolve=>setTimeout(resolve,10));
if(!findingDetailPointerActive||refreshes!==1)throw Error('earlier release stole a newer interaction');
window.handlers.pointercancel({pointerId:1});
await new Promise(resolve=>setTimeout(resolve,10));
if(findingDetailPointerActive||refreshes!==2)throw Error('cancel left refresh blocked');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("finding pointer refresh: %v\n%s", err, out)
	}
}

func TestUIFindingsSerializesAndCoalescesMutationWrites(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"findingWriteQueues",
		"pendingFields",
		"pendingWaiters",
		"latestValues",
		"function pendingFindingValue(id, key, fallback)",
		"function acknowledgedFindingValue(id, key, fallback)",
		"drainFindingWrites",
		"Object.assign(queue.pendingFields, fields)",
		"queue.latestValues[key] = fields[key]",
		"const waiters = queue.pendingWaiters.splice(0)",
		"await api('/api/findings/' + id",
		"const expected = pendingFindingValue(f.id, key, previous)",
		"if (v === expected)",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings writes must serialize/coalesce latest intent: missing %q", want)
		}
	}
}

func TestUIFindingsFailedWritesRetainDraftUntilAcknowledged(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"findingDrafts.stage(id, fields)",
		"findingDrafts.fail(id, waiter.tokens)",
		"findingDrafts.acknowledge(id, waiter.tokens)",
		"...findingDrafts.values(selFinding)",
		"retryFindingSaves",
		"if(findingDrafts.has(f.id))",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("failed edits must be retained and recoverable: missing %q", want)
		}
	}
	if strings.Contains(findings, "el.value = authoritative") {
		t.Error("a rejected save must not discard the typed draft")
	}
}

func TestUIFindingsDeferredRefreshRestoresStableDetailFocus(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"captureFindingFocus",
		"restoreFindingFocus",
		"active.tabIndex",
		"const focus = captureFindingFocus()",
		"restoreFindingFocus(focus)",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("deferred finding refresh must preserve stable detail focus: missing %q", want)
		}
	}
}

func TestUIFindingsDebouncesBodiesPerFinding(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"let bodySaveTimers = new Map()",
		"const previous = bodySaveTimers.get(fid)",
		"bodySaveTimers.set(fid",
		"bodySaveTimers.delete(fid)",
		"bodySaveTimers.has(selFinding)",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("finding body debounce must be entity-scoped: missing %q", want)
		}
	}
	if strings.Contains(findings, "clearTimeout(bodySaveTimer)") {
		t.Error("one finding must not cancel another finding's pending body save")
	}
}

func TestUIFindingsExportWaitsAndBlocksUnresolvedDrafts(t *testing.T) {
	source := readUIAsset(t, "js/findings.js")
	start := strings.Index(source, "async function settleFindingsBeforeExport(")
	end := strings.Index(source, "$('#findExport') &&")
	if start < 0 || end <= start {
		t.Fatal("missing export draft barrier")
	}
	script := `
 let bodyFindingId=1,bodySavesInFlight=0,findingWritesInFlight=0;
 const bodySaveTimers=new Map(),bodySaveSnapshots=new Map(),findingWriteQueues=new Map(),findingAttachPending=new Set();
 let dirty=false,previewDirty=false,requests=0,downloads=0,errors=[];
 const findingDrafts={hasAny:()=>dirty},cvssPreviewDrafts={hasAny:()=>previewDirty};
 const controls={findExport:{disabled:false,setAttribute(){},removeAttribute(){}},findExportMode:{value:'final'}};
 const $=s=>controls[s.slice(1)],captureActiveFindingTextEditor=()=>{},flushPendingBodySave=async id=>{bodySaveSnapshots.delete(id);bodySaveTimers.delete(id);};
 const fetch=async()=>{requests++;return {ok:true,blob:async()=>({type:'text/plain'})}},saveFile=async()=>downloads++,toast=(message,type)=>{if(type==='error')errors.push(message)},closeModal=()=>{};
 ` + source[start:end] + `
 for(const mode of ['draft','final']) {
 controls.findExportMode.value=mode;findingWriteQueues.set(1,{running:true});dirty=true;
 const before=requests;const pending=exportFindingsReport();await new Promise(r=>setTimeout(r,5));
 if(requests!==before||downloads!==before)throw Error('export ran while write pending');
 dirty=false;findingWriteQueues.clear();await pending;
 if(requests!==before+1||downloads!==requests)throw Error('settled write did not export');
 dirty=true;const rejected=requests;await exportFindingsReport();if(requests!==rejected||!errors.at(-1).includes('Save or retry'))throw Error('rejected write exported stale content');
 dirty=false;previewDirty=true;await exportFindingsReport();if(requests!==rejected)throw Error('unapplied preview exported');previewDirty=false;
 }
 `
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("export drafts: %v\n%s", err, out)
	}
}
