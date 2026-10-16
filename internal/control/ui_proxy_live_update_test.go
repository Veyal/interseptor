package control

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/store"
)

// domShim is a deliberately tiny DOM good enough for History's row window: a
// container whose innerHTML round-trips a flat list of <div> elements, plus the
// node operations the renderer uses (remove/before/replaceWith/focus). It exists
// so the keyed reconcile and the full re-render can be run against the same
// state and their resulting markup compared byte for byte.
const domShim = `
function camel(s){return s.replace(/-([a-z])/g,(_,c)=>c.toUpperCase());}
class El{
  constructor(tag){this.tagName=tag;this.attrs=[];this.children=[];this.parentNode=null;this.inner='';this.dataset={};this.style={};this.scrollTop=0;this.clientHeight=640;}
  addEventListener(){}
  setAttrs(text){
    for(const m of String(text).matchAll(/([a-zA-Z-]+)="([^"]*)"/g)){
      this.attrs.push([m[1],m[2]]);
      if(m[1]==='style'){
        for(const decl of m[2].split(';')){if(!decl)continue;const i=decl.indexOf(':');this.style[decl.slice(0,i)]=decl.slice(i+1);}
      }else if(m[1].startsWith('data-'))this.dataset[camel(m[1].slice(5))]=m[2];
    }
  }
  styleText(){return Object.keys(this.style).map(k=>k+':'+this.style[k]).join(';');}
  get outerHTML(){return '<div'+this.attrs.map(([k,v])=>' '+k+'="'+(k==='style'?this.styleText():v)+'"').join('')+'>'+this.inner+'</div>';}
  set innerHTML(v){this.children=parseHTML(v,this);this.inner='';}
  get innerHTML(){return this.children.length?this.children.map(c=>c.outerHTML).join(''):this.inner;}
  get firstElementChild(){return this.children[0]||null;}
  get lastElementChild(){return this.children[this.children.length-1]||null;}
  querySelectorAll(sel){
    if(sel!=='.trow')throw Error('shim supports only .trow, got '+sel);
    return this.children.filter(c=>c.attrs.some(([k,v])=>k==='class'&&v.split(' ').includes('trow')));
  }
  querySelector(sel){return this.querySelectorAll(sel)[0]||null;}
  index(){return this.parentNode?this.parentNode.children.indexOf(this):-1;}
  remove(){const i=this.index();if(i>=0)this.parentNode.children.splice(i,1);this.parentNode=null;}
  before(node){const i=this.index();if(i<0)throw Error('before() on a detached node');node.parentNode=this.parentNode;this.parentNode.children.splice(i,0,node);}
  replaceWith(node){const i=this.index();if(i<0)throw Error('replaceWith() on a detached node');node.parentNode=this.parentNode;this.parentNode.children[i]=node;this.parentNode=null;}
  contains(node){return node===this;}
  focus(){document.activeElement=this;}
}
function parseHTML(html,parent){
  const out=[];
  for(const m of String(html).matchAll(/<div([^>]*)>([\s\S]*?)<\/div>/g)){
    const el=new El('div');
    el.setAttrs(m[1]);
    el.inner=m[2];
    el.parentNode=parent;
    out.push(el);
  }
  return out;
}
const document={createElement:tag=>new El(tag),activeElement:null,getElementById:()=>null};
`

// TestUIHistoryVirtualWindowReconcilesInsteadOfRebuilding is the parity gate for
// the keyed reconciliation path: for every live mutation shape (insert, remove,
// in-place update, scrolled window) the DOM the reconcile produces must equal the
// DOM a full renderRows() produces for the same state — while re-wiring only the
// rows that actually changed and leaving surviving rows, their focus, and the
// container's scroll position alone.
func TestUIHistoryVirtualWindowReconcilesInsteadOfRebuilding(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	proxy := readUIAsset(t, "js/proxy.js")
	script := domShim +
		repeaterRenderJS(t, core, "export function createVirtualList({container,itemHeight,threshold,buffer,onScroll})") +
		repeaterRenderJS(t, core, "export function diffVisibleRows(prevIds,nextIds)") + `
const state={flows:[],selId:null,selected:new Set(),inScopeOnly:false};
let wired=0;
const wireFlowRow=r=>{r._wired=(r._wired||0)+1;wired++;};
const flowRowHTML=f=>'<div class="trow" data-id="'+f.id+'" data-status="'+f.status+'">#'+f.id+' '+f.status+'</div>';
const syncInspectorVisibility=()=>{};
const applyFlowGrid=()=>{};
const consumeFlowSignals=()=>{};
const captureFlowListFocus=()=>null;
const restoreFlowListFocus=()=>{};
const anyFilter=()=>false;
let checklistHandle=null;
const renderEmptyHistory=()=>{};
const box=new El('div');
const $=sel=>sel==='#rows'?box:null;
const flowVirt=createVirtualList({container:box,itemHeight:28,threshold:120,buffer:40,onScroll:()=>{}});
` +
		repeaterRenderJS(t, proxy, "function buildFlowRowEl(f)") +
		repeaterRenderJS(t, proxy, "function stampFlowRows(box,html)") +
		repeaterRenderJS(t, proxy, "function reconcileVirtualRows()") +
		repeaterRenderJS(t, proxy, "export function renderRows()") + `
const flow=(id,status)=>({id,status});
const seed=n=>{state.flows=[];for(let i=n;i>=1;i--)state.flows.push(flow(i,200));};
function parity(label){
  wired=0;
  const reconciled=reconcileVirtualRows();
  if(!reconciled)throw Error(label+': reconcile refused a window it should have patched');
  const reconcileWired=wired;
  const after=box.innerHTML;
  const survivors=box.querySelectorAll('.trow');
  wired=0;
  renderRows();
  const full=box.innerHTML;
  if(after!==full)throw Error(label+': reconciled DOM differs from a full re-render\\n  got  '+after.slice(0,400)+'\\n  want '+full.slice(0,400));
  return {survivors,reconcileWired,fullWired:wired};
}

// Baseline: 300 rows, virtualized, scrolled to the live tail.
seed(300);
renderRows();
if(!flowVirt.isActive())throw Error('300 rows must virtualize');
if(box.firstElementChild.dataset.vpad!=='top'||box.lastElementChild.dataset.vpad!=='bottom')throw Error('virtualized window must be padded top and bottom');
const mountedBefore=box.querySelectorAll('.trow').length;
if(mountedBefore>=300)throw Error('virtualized window mounted the whole list');

// 1. Live insert at the head: only the new row may be built and wired.
const keptRow=box.querySelectorAll('.trow')[3];
document.activeElement=keptRow;
state.flows.unshift(flow(301,200));
const insert=parity('insert');
if(!insert.survivors.includes(keptRow))throw Error('insert tore down a surviving row instead of patching around it');
if(keptRow._wired!==1)throw Error('surviving row was re-wired by an insert');
if(document.activeElement!==keptRow)throw Error('insert dropped focus out of a surviving row');
if(insert.reconcileWired!==1)throw Error('an insert must wire exactly one row, wired '+insert.reconcileWired);
if(insert.fullWired<50)throw Error('expected a full re-render to wire the whole window');

// 2. In-place update of a mounted row: patched individually, DOM still identical.
state.flows[5].status=503;
const update=parity('update');
if(update.reconcileWired!==1)throw Error('an update must rebuild exactly one row, rebuilt '+update.reconcileWired);
if(update.survivors.filter(r=>r.dataset.status==='503').length!==1)throw Error('updated row did not repaint');

// 3. Removal of a mounted row.
state.flows.splice(4,1);
const remove=parity('remove');
if(remove.reconcileWired>1)throw Error('a removal must not rebuild the window, wired '+remove.reconcileWired);

// 4. Scrolled window: the reconcile must move the window without resetting scroll.
box.scrollTop=2400;
renderRows();
if(box.firstElementChild.style.height==='0px')throw Error('a scrolled window must carry a top pad');
const scrolledIds=box.querySelectorAll('.trow').map(r=>r.dataset.id);
state.flows.unshift(flow(303,200));
const scrolled=parity('scrolled insert');
if(box.scrollTop!==2400)throw Error('reconcile disturbed scroll position');
if(scrolled.reconcileWired!==1)throw Error('a scrolled insert must wire one row, wired '+scrolled.reconcileWired);
if(scrolled.survivors.map(r=>r.dataset.id).join()===scrolledIds.join())throw Error('the shifted window should have moved by one row');

// 5. A window with no survivors at all (e.g. a re-sort) is still reconciled
// correctly — it just degenerates to remove-all/insert-all.
state.flows.reverse();
parity('full replacement');

// 6. Reordered survivors are not reconcilable — the caller must fall back.
box.scrollTop=0;
seed(300);
renderRows();
const swap=state.flows[10];state.flows[10]=state.flows[11];state.flows[11]=swap;
const before=box.innerHTML;
if(reconcileVirtualRows())throw Error('reconcile claimed a reordered window');
if(box.innerHTML!==before)throw Error('a refused reconcile must leave the DOM untouched');

// 7. A list that fell back below the virtualization threshold is not patchable either.
seed(300);
renderRows();
state.flows=state.flows.slice(0,10);
if(reconcileVirtualRows())throw Error('reconcile claimed a de-virtualized list');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("History keyed reconciliation: %v\n%s", err, out)
	}
}

// TestUIHistoryVisibleRowDiffPlansMinimalPatches unit-tests the pure diff the
// reconcile is built on, including the cases where it must refuse to patch.
func TestUIHistoryVisibleRowDiffPlansMinimalPatches(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	script := repeaterRenderJS(t, core, "export function diffVisibleRows(prevIds,nextIds)") + `
const j=v=>JSON.stringify(v);
const eq=(got,want,msg)=>{if(j(got)!==j(want))throw Error(msg+': got '+j(got)+' want '+j(want));};

// Live tail: one new id at the front, one dropped off the end of the window.
let plan=diffVisibleRows([5,4,3],[6,5,4]);
eq(plan.reusable,true,'head insert must be reconcilable');
eq(plan.remove,[3],'head insert must drop the row that left the window');
eq(plan.insert,[{id:6,before:5}],'head insert must anchor before the first survivor');

// Burst: several new ids arrive in one frame, in order.
plan=diffVisibleRows([5,4],[8,7,6,5,4]);
eq(plan.insert,[{id:8,before:5},{id:7,before:5},{id:6,before:5}],'a burst must insert in render order before the same anchor');
eq(plan.remove,[],'a burst that drops nothing must remove nothing');

// Scrolling down: new ids append at the tail with no survivor after them.
plan=diffVisibleRows([5,4,3],[4,3,2]);
eq(plan.remove,[5],'scroll must drop the row that left the top');
eq(plan.insert,[{id:2,before:null}],'a tail insert has no anchor');

// No change at all.
plan=diffVisibleRows([3,2,1],[3,2,1]);
eq(plan,{reusable:true,remove:[],insert:[],keep:[3,2,1]},'an unchanged window must plan no work');

// First render / cleared window.
eq(diffVisibleRows([],[2,1]).insert,[{id:2,before:null},{id:1,before:null}],'an empty window inserts every row');
eq(diffVisibleRows([2,1],[]).remove,[2,1],'an emptied window removes every row');

// Refusals: survivors reordered, and duplicate keys.
eq(diffVisibleRows([3,2,1],[1,2,3]).reusable,false,'reordered survivors are not patchable');
eq(diffVisibleRows([3,3,1],[3,1]).reusable,false,'duplicate ids are not keyed rows');
eq(diffVisibleRows([4,3,2,1],[4,2,3,1]).reusable,false,'a swap inside the window is not patchable');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("diffVisibleRows behavior: %v\n%s", err, out)
	}
}

// TestUIHistoryLiveUpdateKeepsKeyedReconciliationWiring pins the wiring that puts
// the reconcile on the live path — a regression here would silently restore the
// per-frame full-window rebuild without failing the parity test above.
func TestUIHistoryLiveUpdateKeepsKeyedReconciliationWiring(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"const fire=()=>{liveRenderQueued=false;if(!reconcileVirtualRows())renderRows();}",
		"const plan=diffVisibleRows(mounted.map(r=>Number(r.dataset.id)),slice.map(f=>f.id))",
		"if(!plan.reusable)return false",
		"if(top.dataset.vpad!=='top'||bottom.dataset.vpad!=='bottom')return false",
		"if(!row||row._flowHTML===flowRowHTML(f))continue",
		"top.style.height=win.topPad+'px'",
		"bottom.style.height=win.bottomPad+'px'",
		"el._flowHTML=html",
		"rows.forEach((r,i)=>{r._flowHTML=html[i];wireFlowRow(r);})",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("History keyed-reconciliation contract missing %q", contract)
		}
	}
	if strings.Contains(proxy, "const fire=()=>{liveRenderQueued=false;renderRows();}") {
		t.Error("the virtualized live path must not rebuild the whole window per frame")
	}
}

// TestUIScopeFilterMatchesServerEngine is the correctness proof behind letting a
// live flow be placed incrementally while "in scope" is on: for every rule set
// the UI claims it can evaluate, core.js's flowInScope must agree with
// scope.Engine.InScope — the exact predicate /api/flows?inScope=1 applies on the
// server — flow for flow. Rule sets the UI cannot reproduce (Go RE2 host/path
// regexes) must be reported as not evaluable so the caller keeps refetching.
func TestUIScopeFilterMatchesServerEngine(t *testing.T) {
	rule := func(action, host, path, scheme string, port int) store.ScopeRule {
		return store.ScopeRule{Enabled: true, Action: action, Host: host, Path: path, Scheme: scheme, Port: port}
	}
	flows := []*store.Flow{
		{Host: "example.com", Port: 443, Path: "/", Scheme: "https"},
		{Host: "api.example.com", Port: 443, Path: "/v1/users", Scheme: "https"},
		{Host: "api.example.com", Port: 8443, Path: "/v2/users", Scheme: "https"},
		{Host: "API.Example.com", Port: 443, Path: "/v1/admin", Scheme: "https"},
		{Host: "static.example.com", Port: 443, Path: "/app.js", Scheme: "https"},
		{Host: "evil-example.com", Port: 80, Path: "/v1/users", Scheme: "http"},
		{Host: "example.com.evil.net", Port: 80, Path: "/", Scheme: "http"},
		{Host: "cdn.other.test", Port: 80, Path: "/v1", Scheme: "http"},
	}
	sets := []struct {
		name      string
		rules     []store.ScopeRule
		evaluable bool
	}{
		{"no rules at all", nil, true},
		{"wildcard include", []store.ScopeRule{rule("include", "*.example.com", "", "", 0)}, true},
		{"exact host include", []store.ScopeRule{rule("include", "api.example.com", "", "", 0)}, true},
		{"host plus path prefix", []store.ScopeRule{rule("include", "*.example.com", "/v1", "", 0)}, true},
		{"path-only include", []store.ScopeRule{rule("include", "", "/v1", "", 0)}, true},
		{"include with an exclude carve-out", []store.ScopeRule{
			rule("include", "*.example.com", "", "", 0),
			rule("exclude", "static.example.com", "", "", 0),
		}, true},
		{"exclude only", []store.ScopeRule{rule("exclude", "cdn.other.test", "", "", 0)}, true},
		{"scheme and port constrained", []store.ScopeRule{rule("include", "api.example.com", "", "https", 8443)}, true},
		{"disabled rules are ignored", []store.ScopeRule{
			{Enabled: false, Action: "include", Host: ".*evil.*"},
			rule("include", "*.example.com", "", "", 0),
		}, true},
		// Deliberate fallbacks: Go compiles these with regexp (RE2), which JS
		// cannot reproduce, so the UI must keep asking the server.
		{"regex host rule", []store.ScopeRule{rule("include", ".*example.*", "", "", 0)}, false},
		{"anchored regex path rule", []store.ScopeRule{rule("include", "*.example.com", "^/v1", "", 0)}, false},
		{"slash-wrapped path rule", []store.ScopeRule{rule("include", "*.example.com", "/v1/", "", 0)}, false},
	}

	type flowCase struct {
		Host   string `json:"host"`
		Port   int    `json:"port"`
		Path   string `json:"path"`
		Scheme string `json:"scheme"`
		Want   bool   `json:"want"`
	}
	type setCase struct {
		Name      string            `json:"name"`
		Rules     []store.ScopeRule `json:"rules"`
		Evaluable bool              `json:"evaluable"`
		Flows     []flowCase        `json:"flows"`
	}
	cases := make([]setCase, 0, len(sets))
	for _, s := range sets {
		eng := scope.New()
		eng.SetRules(s.rules)
		c := setCase{Name: s.name, Rules: s.rules, Evaluable: s.evaluable}
		for _, f := range flows {
			c.Flows = append(c.Flows, flowCase{Host: f.Host, Port: f.Port, Path: f.Path, Scheme: f.Scheme, Want: eng.InScope(f)})
		}
		cases = append(cases, c)
	}
	table, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}

	core := readUIAsset(t, "js/core.js")
	literals := regexp.MustCompile(`const SCOPE_REGEX_LITERALS=\[[^\n]*\];`).FindString(core)
	if literals == "" {
		t.Fatal("core.js no longer declares SCOPE_REGEX_LITERALS")
	}
	script := literals + "\n" +
		repeaterRenderJS(t, core, "export function scopeLooksLikeRegex(s)") +
		repeaterRenderJS(t, core, "export function compileScopeRules(rules)") +
		repeaterRenderJS(t, core, "function scopeHostMatches(r,host)") +
		repeaterRenderJS(t, core, "function scopeRuleMatches(r,f)") +
		repeaterRenderJS(t, core, "export function flowInScope(f,compiled)") + `
const cases=` + string(table) + `;
let checked=0;
for(const c of cases){
  const compiled=compileScopeRules(c.rules);
  if(compiled.evaluable!==c.evaluable)throw Error(c.name+': evaluable='+compiled.evaluable+', want '+c.evaluable);
  if(!compiled.evaluable)continue;
  for(const f of c.flows){
    const got=flowInScope(f,compiled);
    if(got!==f.want)throw Error(c.name+': '+f.scheme+'://'+f.host+':'+f.port+f.path+' -> '+got+', server says '+f.want);
    checked++;
  }
}
if(checked<50)throw Error('scope parity table is too thin: '+checked+' comparisons');
const unevaluable=compileScopeRules([{enabled:true,action:'include',host:'.*x.*'}]);
if(flowInScope({host:'x.test',path:'/',scheme:'http',port:80},unevaluable)!==false)throw Error('an unevaluable rule set must never claim a flow is in scope');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("client-side scope parity: %v\n%s", err, out)
	}
}

// TestUIHistoryIncrementalUnderScopeMode pins that scope mode no longer forces a
// full /api/flows reload per live flow, and that the client-side predicate is
// actually applied when a row is placed incrementally.
func TestUIHistoryIncrementalUnderScopeMode(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"function scopeDecidableHere(){return !state.inScopeOnly||(scopeLoaded&&activeScopeMatcher().evaluable);}",
		"return scopeDecidableHere()&&!(state.filters.search&&state.filters.search.trim())",
		"if(state.inScopeOnly&&(!scopeLoaded||!flowInScope(f,activeScopeMatcher())))return false",
		"if(scopeMatcherSource!==rules){scopeMatcherSource=rules;scopeMatcherCache=compileScopeRules(rules);}",
		// An empty rule set is only "everything in scope" once /api/scope has
		// actually answered; until then incremental placement must stay off.
		"let scopeMatcherSource=null,scopeMatcherCache=null,scopeLoaded=false;",
		"scopeLoaded=true;",
		"if(changed&&state.inScopeOnly)scheduleReload();",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("scope incremental contract missing %q", contract)
		}
	}
	if strings.Contains(proxy, "return !state.inScopeOnly&&!(state.filters.search") {
		t.Error("scope mode must no longer bail unconditionally to a full reload")
	}
}
