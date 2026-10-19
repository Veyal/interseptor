import { $, $$, esc, escAttr, state, toast, api, methodColor, beautifyBody, contentTypeFromRaw, highlightHTTP, renderLoadError, uiConfirm, projectStorageKey, compileScopeRules, flowInScope, wireRowKey } from './core.js';
import { sendRawToRepeater } from './tools.js';
import { wireListbox, setListboxSelection, focusOption } from './listbox.js';
import { animateOnce, MOTION } from './motion.js';
import { createSplitPane } from './split.js';
import { heldKey, createDropScheduler, forwardAllPlan, forwardPath, heldForwardRaw, heldIsModified, createPrettyBridge, losslessBeautify, scopeNote, scopeBadgeText, identityText, rulesSummary, dropToastText, forwardAllProgress, isAutoForwarded, autoForwardText, createAutoForwardTally, contentLengthNote, contentLengthText } from './intercept-model.js';
import { applyDelta, snapshotSeq, deltaType, SEQ_UNKNOWN } from './intercept-delta.js';
import { showDropToast } from './held-undo.js';
import { registerSseHandler } from './shell-hooks.js';
import { on as busOn, EVENTS } from './bus.js';

/* ---- intercept ---- */
// One unified hold queue (requests + responses) feeding one editor. state.heldSel
// is {id, side:'req'|'resp'} for the selected item, or null.
const heldRawCache=new Map();
const heldOriginalCache=new Map();
let heldKeysReady=false;
let knownHeldKeys=new Set();
let heldSignalWindowAt=0,heldSignalCount=0;
let heldActionInFlight=null;
let heldActionEpoch=0;
let heldLoadingKey=null;
let heldDecodeEpoch=0;
// Deferred drops (key -> true) and the shared context shown in the held header.
// There is no server-side undo, so a drop waits DROP_DELAY_MS on the client.
const dropMarks=new Set();
let forwardAllBusy=false;
const icptCtx={scope:null,identity:'',pane:null};
let interceptStateEpoch=0;
let interceptSummaryEpoch=0;
// Generation of the hold queue the client currently mirrors (see intercept-delta.js).
// SEQ_UNKNOWN until the first authoritative snapshot, and again after a detected
// gap until the resnapshot rebases it. interceptResnapshotPending dedupes the
// on-demand snapshot fetch so a burst of dropped deltas triggers only one.
let interceptSeq=SEQ_UNKNOWN;
let interceptResnapshotPending=false;
// Toggle mutations can change the held queues and are kept on their own lane.
// Filter persistence is deliberately separate: the filter endpoint does not
// emit an intercept.update event, and a stalled request must never make the
// safety toggles inoperable.
let interceptMutationTail=Promise.resolve();
let interceptFilterMutationTail=Promise.resolve();
let filterMutationEpoch=0;
let filterCommitEpoch=0;
let pendingFilterMutation=null;
let acknowledgedFilterConfig=null;
export function interceptStateGeneration(){return interceptStateEpoch;}
export function interceptFilterGeneration(){return filterCommitEpoch;}
function currentFilterConfig(source=state.intercept||{}){
  return {
    filterEnabled:!!source.filterEnabled,
    filterTarget:source.filterTarget||'any',
    filterPattern:source.filterPattern||'',
  };
}
function commitFilterConfig(config){
  // Filter reconciliation owns only filter fields. It must not invalidate an
  // in-flight authoritative refresh for queue or safety-toggle state: a failed
  // filter save is local rollback, not a newer intercept summary.
  state.intercept={...(state.intercept||{}),...config};
  filterCommitEpoch++;
}
function readFilterControls(){
  const enabled=$('#interceptFilterOn').checked,target=$('#interceptFilterTarget').value,pattern=$('#interceptFilterPattern').value;
  return {
    input:{enabled,target,pattern},
    config:{filterEnabled:enabled&&pattern!=='',filterTarget:target||'any',filterPattern:pattern},
  };
}
function stageInterceptFilter(){
  const {input,config}=readFilterControls();
  const epoch=++filterMutationEpoch;
  pendingFilterMutation={epoch,config,input};
  return pendingFilterMutation;
}
function filterControlsMatch(input){
  const enabled=$('#interceptFilterOn'),target=$('#interceptFilterTarget'),pattern=$('#interceptFilterPattern');
  return !!enabled&&!!target&&!!pattern&&enabled.checked===!!input.enabled&&target.value===input.target&&pattern.value===input.pattern;
}
function syncFilterControls(config){
  const enabled=$('#interceptFilterOn'),target=$('#interceptFilterTarget'),pattern=$('#interceptFilterPattern');
  if(enabled)enabled.checked=!!config?.filterEnabled;
  if(target)target.value=config?.filterTarget||'any';
  if(pattern)pattern.value=config?.filterPattern||'';
}
function mergeIncomingInterceptState(next){
  const incoming=next||{};
  if(!pendingFilterMutation)return incoming;
  // SSE summaries (and toggle responses) can still contain filter A while the
  // user's filter B request is in flight. Merge only the local filter fields;
  // all queue/toggle fields remain authoritative from the newer summary.
  return {...incoming,...pendingFilterMutation.config};
}
export function mergeInterceptFilterSince(next,generation){
  return generation===filterCommitEpoch?next:{...(next||{}),...currentFilterConfig()};
}
function commitInterceptState(next,filterAuthoritative){
  if(filterAuthoritative)acknowledgedFilterConfig=currentFilterConfig(next||{});
  const merged=mergeIncomingInterceptState(next);
  state.intercept=merged;interceptStateEpoch++;interceptSummaryEpoch++;
  // Any authoritative full snapshot (replaceInterceptState) rebases the delta
  // generation; a local optimistic edit (replaceLocalInterceptState) must not.
  if(filterAuthoritative)noteSnapshotSeq(merged);
}
// noteSnapshotSeq rebases the delta generation whenever an authoritative full
// snapshot is applied (SSE intercept.state/intercept.update, a toggle/filter
// response, or the REST resync). An older server that omits seq leaves the
// client on SEQ_UNKNOWN, which keeps it on the full-render path.
function noteSnapshotSeq(snapshot){
  const s=snapshotSeq(snapshot);
  if(s!==SEQ_UNKNOWN){interceptSeq=s;interceptResnapshotPending=false;}
}
export function replaceInterceptState(next){commitInterceptState(next,true);}
function replaceLocalInterceptState(next){commitInterceptState(next,false);}
async function applyInterceptMutation(request){
  const result=interceptMutationTail.then(async()=>{
    const generation=interceptSummaryEpoch;
    const filterGeneration=filterCommitEpoch;
    const s=await request();
    if(generation!==interceptSummaryEpoch)return false;
    replaceInterceptState(mergeInterceptFilterSince(s,filterGeneration));renderIntercept();return true;
  });
  interceptMutationTail=result.catch(()=>{});
  return result;
}
async function applyFilterMutation(request,draft){
  const {epoch,config,input}=draft;
  const result=interceptFilterMutationTail.then(async()=>{
    if(epoch!==filterMutationEpoch)return false;
    const generation=interceptSummaryEpoch;
    try{
      const summary=await request();
      acknowledgedFilterConfig={...config};
      const latest=epoch===filterMutationEpoch;
      const summaryCurrent=generation===interceptSummaryEpoch;
      if(latest)pendingFilterMutation=null;
      if(summaryCurrent){replaceInterceptState(summary);renderIntercept();}
      else if(latest)commitFilterConfig(config);
      return latest;
    }catch(error){
      if(epoch===filterMutationEpoch){
        pendingFilterMutation=null;
        const fallback=acknowledgedFilterConfig||currentFilterConfig();
        commitFilterConfig(fallback);
        if(filterControlsMatch(input))syncFilterControls(fallback);
        throw error;
      }
      return false;
    }
  });
  interceptFilterMutationTail=result.catch(()=>{});
  return result;
}
function allowHeldSignal(){
  const now=performance.now();
  if(now-heldSignalWindowAt>800){heldSignalWindowAt=now;heldSignalCount=0;}
  heldSignalCount++;
  return !document.hidden&&heldSignalCount<=4;
}
function heldItems(){const ic=state.intercept||{};return [...(ic.queue||[]).map(h=>({...h,side:'req'})),...(ic.responseQueue||[]).map(h=>({...h,side:'resp'}))];}
function heldDecodeCurrent(epoch,selectionKey,raw){
  return epoch===heldDecodeEpoch&&!!state.heldSel&&heldKey(state.heldSel.side,state.heldSel.id)===selectionKey&&$('#heldRaw').value===raw;
}
function setHeldDecodeState(show){
  const button=$('#heldDecodeBtn');
  if(!button)return;
  button.textContent=show?'Raw':'Decoded';
  button.title=show?'Show the raw held message':'Show message-codec plaintext for the body';
  button.setAttribute('aria-label',show?'Show raw held message':'Show decoded held message');
  button.setAttribute('aria-pressed',show?'true':'false');
}
function heldOriginal(h){return h.original||h.raw||'';}
function setHeldModified(raw, original){
  const badge=$('#heldModified');
  // A <textarea> value is LF-normalized while the held original is CRLF, so a
  // plain !== would flag every untouched message as MODIFIED.
  const modified=heldIsModified(raw,original);
  if(badge){badge.hidden=!modified;badge.textContent=modified?'MODIFIED':'';}
  // The server rewrites Content-Length from the edited body, so the header in
  // the editor is stale after a body edit: say what will really be sent.
  const cl=$('#heldClNote');
  if(cl){const text=modified?contentLengthText(contentLengthNote(raw)):'';cl.hidden=!text;cl.textContent=text;}
}

/* ---- Raw | Pretty (editable) ----
   Beautify rewrites the bytes that will be forwarded and marks the message
   MODIFIED. Reviewing a held message must not require that, so Pretty is a
   separate presentation: it indents a copy of the editor's current text into
   its own textarea (#heldPrettyEdit, highlighted by the #heldPrettyHL overlay —
   the Repeater .rep-field technique) and does not touch #heldRaw until the
   operator actually types. Typing there writes the pretty text into #heldRaw,
   so Forward sends it, MODIFIED turns on, and Raw shows it. Merely viewing
   leaves #heldRaw (and so the forwarded original bytes) alone. */
let heldView='raw';
// Pretty only reformats losslessly (its edits forward as shown); the explicit
// Beautify action keeps the full reformat.
const prettyBridge=createPrettyBridge({beautify:raw=>heldBeautified(raw,body=>losslessBeautify(body,beautifyBody))});
function heldPrettyEl(){return $('#heldPretty');}
function heldPrettyEditEl(){return $('#heldPrettyEdit');}
// heldBeautified indents a held message's body. core.js's prettify() splits the
// head from the body on CRLF, but a <textarea>'s API value normalizes CRLF to LF
// (HTML spec), so the editor's text never matches it — this accepts both.
function heldBeautified(raw,format=beautifyBody){
  const sep=/\r?\n\r?\n/.exec(raw);
  if(!sep)return raw;
  const head=raw.slice(0,sep.index).replace(/\r\n/g,'\n');
  const body=raw.slice(sep.index+sep[0].length);
  return body?head+'\n\n'+format(body):raw;
}
// paintHeldPrettyHL colors the overlay from the Pretty textarea's own text. The
// highlighter only wraps spans around the characters (never re-indents), and a
// trailing newline mirrors the textarea's reserved last line, so the two stay
// aligned exactly as in the Repeater panes.
function paintHeldPrettyHL(){
  const ed=heldPrettyEditEl(),hl=$('#heldPrettyHL');
  if(!ed||!hl)return;
  const text=ed.value||'';
  hl.innerHTML=(text?highlightHTTP(text,true,contentTypeFromRaw(text)):'')+'\n';
}
// renderHeldPretty fills the Pretty editor from the current raw text, indented
// and highlighted. It reads #heldRaw and never writes back to it: reading a held
// message must not change the bytes that will be forwarded.
function renderHeldPretty(){
  const ed=heldPrettyEditEl(),ta=$('#heldRaw');
  if(!ed||!ta)return;
  ed.value=prettyBridge.show(ta.value||'');
  paintHeldPrettyHL();
}
function syncHeldViewSeg(){
  const seg=$('#heldViewSeg');if(!seg)return;
  seg.querySelectorAll('[data-held-view]').forEach(b=>{
    const on=b.dataset.heldView===heldView;
    b.classList.toggle('on',on);
    b.setAttribute('aria-pressed',on?'true':'false');
  });
}
// setHeldView switches presentation only. The Decoded pane is a third, mutually
// exclusive view, so entering Pretty leaves it the same way Raw does.
function setHeldView(view,{focus=false}={}){
  const pre=heldPrettyEl(),ta=$('#heldRaw'),dec=$('#heldDecoded');
  heldView=view==='pretty'?'pretty':'raw';
  if(dec&&dec.style.display&&dec.style.display!=='none'){
    heldDecodeEpoch++;
    dec.style.display='none';dec.hidden=true;setHeldDecodeState(false);
  }
  if(heldView==='pretty'){
    renderHeldPretty();
    if(pre){pre.hidden=false;pre.style.display='block';}
    if(ta)ta.style.display='none';
    const ed=heldPrettyEditEl();
    if(focus&&ed)ed.focus({preventScroll:true});
  }else{
    clearHeldPretty();
    if(ta){ta.style.display='block';if(focus)ta.focus({preventScroll:true});}
  }
  syncHeldViewSeg();
}
function clearHeldPretty(){
  const pre=heldPrettyEl(),ed=heldPrettyEditEl(),hl=$('#heldPrettyHL');
  if(pre){pre.hidden=true;pre.style.display='none';}
  if(ed)ed.value='';
  if(hl)hl.innerHTML='';
}
function resetHeldView(){heldView='raw';prettyBridge.reset();clearHeldPretty();syncHeldViewSeg();}

// Intercept must not be a dead end: "Repeater" copies the (edited) held request
// into a new Repeater tab without forwarding or dropping it.
function ensureHeldRepeaterButton(){
  let b=$('#heldRepeaterBtn');if(b)return b;
  const forward=$('#forwardBtn');if(!forward)return null;
  b=document.createElement('button');
  b.type='button';b.className='btn';b.id='heldRepeaterBtn';b.textContent='Repeater';
  b.title='Open this request in Repeater (it stays held)';
  b.onclick=()=>{
    const sel=state.heldSel,h=sel&&sel.side==='req'&&heldItem(sel.id,sel.side);
    if(!h)return;
    sendRawToRepeater({scheme:h.scheme,host:h.host,raw:$('#heldRaw').value});
  };
  forward.parentNode.insertBefore(b,forward);
  return b;
}
function syncHeldRepeaterButton(side){
  const b=ensureHeldRepeaterButton();if(b)b.hidden=side==='resp';const fr=$('#forwardRepeaterBtn');if(fr)fr.hidden=side==='resp';
}
function setHeldControlsDisabled(disabled){
  const dropping=selectionDropping();
  ['#heldRaw','#forwardBtn','#forwardRepeaterBtn','#dropBtn','#heldBeautifyBtn','#heldResetBtn','#heldDecodeBtn','#heldViewRaw','#heldViewPretty','#heldPrettyEdit','#heldRepeaterBtn']
    .map(s=>$(s)).forEach(el=>{if(el)el.disabled=!!disabled||!!heldActionInFlight||dropping;});
  const all=$('#forwardAllBtn');if(all)all.disabled=!!heldActionInFlight||forwardAllBusy||!heldItems().some(h=>!dropMarks.has(heldKey(h.side,h.id)));
}

function clearHeldLoadMessage(){
  const el=$('#heldLoadState');
  if(el)el.remove();
}

function showHeldLoadState(h,text,retry){
  clearHeldLoadMessage();
  const main=document.querySelector('.icpt-main');
  if(!main)return;
  const el=document.createElement('div');
  el.id='heldLoadState';el.setAttribute('role',retry?'alert':'status');
  el.className=retry?'hint held-load-state state-error-msg':'hint held-load-state';
  el.textContent=text;
  if(retry){
    const b=document.createElement('button');b.type='button';b.className='btn';b.textContent='Retry loading held message';
    b.onclick=()=>selectHeld(h.id,h.side);el.appendChild(b);
  }
  main.insertBefore(el,main.firstChild);
}

function showHeldLoading(h){
  heldLoadingKey=heldKey(h.side,h.id);
  clearHeldLoadMessage();
  const head=$('#heldEditor'),empty=$('#heldEmpty'),ta=$('#heldRaw'),dec=$('#heldDecoded');
  resetHeldView();
  if(head)head.style.display='flex';
  setHeldChrome(true);
  if(empty)empty.style.display='none';
  if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
  setHeldDecodeState(false);
  if(ta){ta.style.display='block';ta.value='';ta.placeholder='Loading held message…';ta.disabled=true;}
  setHeldControlsDisabled(true);
  showHeldLoadState(h,'Loading held message…',false);
}

function showHeldLoadError(h,error){
  heldLoadingKey=null;
  const ta=$('#heldRaw');
  if(ta){ta.value='';ta.placeholder='Held message could not be loaded';ta.disabled=true;}
  setHeldControlsDisabled(true);
  showHeldLoadState(h,'Unable to load held message: '+(error?.message||'request failed'),true);
}

// heldRowMarkup is the single source of a hold-queue row's markup, shared by the
// full render and the incremental delta path so the two never drift. h carries a
// `side` ('req'|'resp') on top of the stored heldJSON fields.
function heldRowMarkup(h){
  return `<div class="icpt-item${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?' sel':''}${dropMarks.has(heldKey(h.side,h.id))?' is-dropping':''}" data-id="${h.id}" data-side="${h.side}" aria-selected="${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?'true':'false'}">
    <span class="icpt-tag ${h.side}">${h.side==='req'?'REQ':'RESP'}</span>
    ${h.side==='req'?`<span class="m" style="color:${methodColor(h.method)}">${esc(h.method)}</span>`:''}
    <span class="u">${esc(h.host)}${esc(h.path)}</span>${dropMarks.has(heldKey(h.side,h.id))?'<span class="icpt-dropping-tag">Dropping</span>':''}</div>`;
}
// syncInterceptBanner reflects the current total into the danger banner, the
// nav badge and the queue-total chip. Shared so a single delta updates them in
// O(1) without a full list rebuild.
function syncInterceptBanner(){
  const ic=state.intercept||{};
  const total=(ic.queue||[]).length+(ic.responseQueue||[]).length;
  const danger=$('#interceptWarning');
  const interceptDanger=!!(ic.enabled||ic.responseEnabled||total);
  document.documentElement.classList.toggle('intercept-danger',interceptDanger);
  if(danger){
    danger.style.display=interceptDanger?'block':'none';
    danger.textContent=total
      ?`INTERCEPT ACTIVE — ${total} request/response item${total===1?' is':'s are'} held until you Forward or Drop. Off after restart (session-only).`
      :'INTERCEPT ACTIVE — matching traffic will be held until you Forward or Drop. Off after restart (session-only).';
  }
  const badge=$('#heldBadge');if(badge){badge.style.display=total?'inline-block':'none';badge.textContent=total;}
  const ht=$('#heldTotal');if(ht){ht.style.display=total?'inline-block':'none';ht.textContent=total;}
}
export function renderIntercept(){
  const ic=state.intercept||{};
  autoFwd.sync(!!ic.enabled);
  renderAutoForwarded();
  const hint=$('#heldEmptyHint');
  if(hint)hint.textContent=ic.enabled||ic.responseEnabled?'Waiting for matching '+(ic.enabled&&ic.responseEnabled?'requests or responses':ic.enabled?'requests':'responses')+'. '+scopeNote(icptCtx.scope).text:'Enable Requests or Responses to hold traffic.';
  const rq=ic.queue||[], rrq=ic.responseQueue||[];
  const focusedHeld=document.activeElement?.closest?.('#heldList .icpt-item[data-id][data-side]');
  const heldFocus=focusedHeld?{id:focusedHeld.dataset.id,side:focusedHeld.dataset.side}:null;
  setSwitch('#interceptToggle','#icptReqState',ic.enabled);
  setSwitch('#respInterceptToggle','#icptResState',ic.responseEnabled);
  // conditional-intercept filter (don't clobber fields the user is editing)
  const fo=$('#interceptFilterOn');if(fo&&document.activeElement!==fo)fo.checked=!!ic.filterEnabled;
  const ft=$('#interceptFilterTarget');if(ft&&document.activeElement!==ft)ft.value=ic.filterTarget||'any';
  const fp=$('#interceptFilterPattern');if(fp&&document.activeElement!==fp)fp.value=ic.filterPattern||'';
  // unified queue: requests then responses, each tagged with its side
  const items=[...rq.map(h=>({...h,side:'req'})),...rrq.map(h=>({...h,side:'resp'}))];
  if(heldActionInFlight&&!items.some(h=>heldKey(h.side,h.id)===heldActionInFlight.key)){
    // The SSE update can arrive before fetch() resolves. Keep the acknowledged
    // item's DOM row in place until the action handler starts its exit motion;
    // state still comes from the server and the deferred render is short.
    heldActionInFlight.deferred=true;
    return;
  }
  const nextHeldKeys=new Set(items.map(h=>heldKey(h.side,h.id)));
  const arrivals=heldKeysReady?items.filter(h=>!knownHeldKeys.has(heldKey(h.side,h.id))&&allowHeldSignal()):[];
  knownHeldKeys=nextHeldKeys;
  heldKeysReady=true;
  const total=items.length;
  syncInterceptBanner();
  const list=$('#heldList');
  // Phones show the queue alone (the editor's "Nothing on hold" state is in the
  // hidden detail pane), so the empty queue carries the same message; CSS shows
  // it only in the stacked layout.
  if(!total){list.innerHTML=`<p class="icpt-list-empty hint">Nothing on hold. ${esc(hint?hint.textContent:'')}</p>`;list.removeAttribute('role');list.removeAttribute('aria-label');state.heldSel=null;showEditor(null);return;}
  list.innerHTML=items.map(heldRowMarkup).join('');
  // The hold queue is a single-select list: one listbox, one Tab stop, arrows
  // move the selection because selecting *is* what shows the item in the editor.
  wireListbox(list,list.querySelectorAll('.icpt-item'),{
    label:'Held requests and responses',
    selectionFollowsFocus:true,
    activate:el=>selectHeld(Number(el.dataset.id),el.dataset.side),
  });
  arrivals.forEach(h=>{
    const el=list.querySelector(`.icpt-item[data-id="${h.id}"][data-side="${h.side}"]`);
    animateOnce(el,[
      {opacity:.45,transform:'translateX(-4px)',backgroundColor:'var(--accentDim)'},
      {opacity:1,transform:'translateX(0)',backgroundColor:'transparent'},
    ],{duration:MOTION.base,easing:MOTION.enter});
  });
  const cur=state.heldSel&&items.find(h=>h.id===state.heldSel.id&&h.side===state.heldSel.side);
  if(cur)selectHeld(cur.id,cur.side,{keepEditor:true});
  else selectHeld(items[0].id,items[0].side);
  if(heldFocus){
    const target=list.querySelector(`.icpt-item[data-id="${CSS.escape(heldFocus.id)}"][data-side="${CSS.escape(heldFocus.side)}"]`);
    target?.focus({preventScroll:true});
  }
}
function setSwitch(btnSel,stateSel,on){
  const b=$(btnSel);if(b){b.disabled=false;b.classList.toggle('on',!!on);b.setAttribute('aria-pressed',on?'true':'false');}
  const s=$(stateSel);if(s)s.textContent=on?'On':'Off';
}
function heldItem(id,side){const q=side==='resp'?(state.intercept.responseQueue||[]):(state.intercept.queue||[]);return q.find(x=>x.id===id);}
function showEditor(h){
  const head=$('#heldEditor'),ta=$('#heldRaw'),empty=$('#heldEmpty'),title=$('#heldTitle');
  clearHeldLoadMessage();
  if(!h){
    heldLoadingKey=null;
    if(head)head.style.display='none';
    setHeldChrome(false);
    resetHeldView();
    if(ta){ta.style.display='none';ta.disabled=false;ta.removeAttribute('placeholder');}
    if(empty)empty.style.display='flex';
    const dec=$('#heldDecoded');if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
    setHeldDecodeState(false);
    setHeldControlsDisabled(false);
    return;
  }
  if(head)head.style.display='flex';
  setHeldChrome(true);
  renderHeldContext();
  if(empty)empty.style.display='none';
  const dec=$('#heldDecoded');if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
  setHeldDecodeState(false);
  const original=heldOriginal(h);
  resetHeldView();
  if(ta){ta.style.display='block';ta.disabled=false;ta.removeAttribute('placeholder');ta.value=h.raw||'';setHeldModified(ta.value,original);}
  setHeldControlsDisabled(false);
  syncHeldRepeaterButton(h.side);
  if(title)title.innerHTML=h.side==='resp'
    ?`<span class="icpt-tag resp" style="margin-right:8px">RESP</span><span class="u">${esc(h.host)}${esc(h.path)}</span>`
    // .icpt-edtitle is a flex row, so a plain space between the method and the
    // URL collapses ("POSTexample.com/api"); the gap is a margin instead.
    :`<span style="color:${methodColor(h.method)};font-weight:700;margin-right:8px;flex:none">${esc(h.method)}</span><span class="u">${esc(h.host)}${esc(h.path)}</span>`;
}
export async function selectHeld(id,side,opts={}){
  const previousKey=state.heldSel&&heldKey(state.heldSel.side,state.heldSel.id);
  const nextKey=heldKey(side,id);
  state.heldSel={id,side};
  if(previousKey!==nextKey)heldDecodeEpoch++;
  if(previousKey!==nextKey){
    if(!heldActionInFlight)restoreHeldActionControls();
  }
  $$('#heldList .icpt-item').forEach(el=>{el.classList.toggle('sel',Number(el.dataset.id)===id&&el.dataset.side===side);});
  setListboxSelection($$('#heldList .icpt-item'),el=>Number(el.dataset.id)===id&&el.dataset.side===side);
   const h=heldItem(id,side);if(!h)return;
   const cacheKey=side+':'+id;
   let raw=heldRawCache.has(cacheKey)?heldRawCache.get(cacheKey):h.raw;
   if(!heldOriginalCache.has(cacheKey))heldOriginalCache.set(cacheKey,heldOriginal(h));

  if(heldLoadingKey===cacheKey&&opts.keepEditor)return;

  if(opts.keepEditor){
    const ta=$('#heldRaw');
    if(ta&&document.activeElement===ta)return;
    // A background refresh must not tear down an open Pretty editor either.
    if(heldView==='pretty'&&previousKey===nextKey)return;
  }
  if(!heldRawCache.has(cacheKey)&&!raw&&h.len!=null){
    showHeldLoading({...h,side});
    if(heldRawCache.has(cacheKey))raw=heldRawCache.get(cacheKey);
    else{
      try{
        const d=await api('/api/intercept/held/'+id+'/raw?side='+side);
        // A fast second click on another item can change state.heldSel while this
        // fetch was in flight. Bail (and restore that item's editor) so the editor
        // — and thus #forwardBtn, which reads state.heldSel — never shows item A's
        // body while the selection is item B.
        if(!state.heldSel||state.heldSel.id!==id||state.heldSel.side!==side)return;
        raw=d.raw||'';
        heldRawCache.set(cacheKey,raw);
        if(!heldOriginalCache.get(cacheKey))heldOriginalCache.set(cacheKey,raw);
        h.raw=raw;
      }catch(e){
        if(state.heldSel&&state.heldSel.id===id&&state.heldSel.side===side)showHeldLoadError({...h,side},e);
        return;
      }
    }
  }
  heldLoadingKey=null;
  showEditor({...h,side,raw});
}

// ---- per-item delta application (intercept.held / intercept.released) ----
// A delta mutates one queue and advances the generation; #heldList is updated in
// place (add or remove one row) rather than rebuilt. The incremental path defers
// to the full renderIntercept for the transitions it does not own — an
// empty<->non-empty flip, removing the selected row, or an action in flight — and
// a gap or unknown baseline triggers exactly one snapshot refresh so a dropped or
// out-of-order frame can never leave the queue wrong.
export function applyInterceptDelta(message,resnapshot){
  const kind=deltaType(message);
  if(!kind)return;
  const delta={type:kind,side:message.side==='resp'?'resp':'req',id:message.id,item:message.item,seq:message.seq};
  const res=applyDelta(state.intercept||{},interceptSeq,delta);
  if(res.status==='duplicate')return;
  if(res.status==='gap'){
    interceptSeq=SEQ_UNKNOWN; // stop applying deltas until a snapshot rebases us
    if(!interceptResnapshotPending&&typeof resnapshot==='function'){
      interceptResnapshotPending=true;
      Promise.resolve(resnapshot()).catch(()=>{}).finally(()=>{interceptResnapshotPending=false;});
    }
    return;
  }
  state.intercept=res.state;
  interceptSeq=res.seq;
  renderHeldDelta(kind,delta.side,delta.id,delta.item);
}
function renderHeldDelta(kind,side,id,item){
  const list=$('#heldList');
  if(!list)return;
  if(heldActionInFlight||forwardAllBusy){renderIntercept();return;}
  const total=(state.intercept.queue||[]).length+(state.intercept.responseQueue||[]).length;
  if(kind==='held'){
    if(total<=1){renderIntercept();return;}   // empty -> non-empty: full render selects + opens the editor
    addHeldRow(side,item);
  }else{
    if(total===0){renderIntercept();return;}  // non-empty -> empty: full render clears + hides the editor
    if(state.heldSel&&state.heldSel.id===id&&state.heldSel.side===side){renderIntercept();return;} // selected row removed
    removeHeldRow(side,id);
  }
}
function heldRowEl(h){
  const tmp=document.createElement('div');
  tmp.innerHTML=heldRowMarkup(h);
  return tmp.firstElementChild;
}
// wireHeldRow gives one incrementally-added row the same listbox semantics
// wireListbox gives a rendered row: an option in the roving tab order with click
// and Enter/Space activation. Arrow traversal across rows is refreshed by the
// next full render (every Forward/Drop reconcile triggers one).
function wireHeldRow(el){
  el.setAttribute('role','option');
  if(!el.hasAttribute('aria-selected'))el.setAttribute('aria-selected','false');
  el.tabIndex=-1;
  const go=()=>selectHeld(Number(el.dataset.id),el.dataset.side);
  el.onclick=go;
  wireRowKey(el,go);
}
function heldSelectionPredicate(el){return !!state.heldSel&&Number(el.dataset.id)===state.heldSel.id&&el.dataset.side===state.heldSel.side;}
function addHeldRow(side,item){
  const list=$('#heldList');if(!list)return;
  const h={...item,side};
  if(list.querySelector(`.icpt-item[data-id="${h.id}"][data-side="${side}"]`)){syncInterceptBanner();return;}
  const el=heldRowEl(h);
  wireHeldRow(el);
  // Requests sort before responses: a new request goes before the first response
  // row (insertBefore(null) appends), a new response at the very end.
  if(side==='req')list.insertBefore(el,list.querySelector('.icpt-item[data-side="resp"]'));
  else list.appendChild(el);
  knownHeldKeys.add(heldKey(side,h.id));
  setListboxSelection($$('#heldList .icpt-item'),heldSelectionPredicate);
  if(allowHeldSignal())animateOnce(el,[
    {opacity:.45,transform:'translateX(-4px)',backgroundColor:'var(--accentDim)'},
    {opacity:1,transform:'translateX(0)',backgroundColor:'transparent'},
  ],{duration:MOTION.base,easing:MOTION.enter});
  syncInterceptBanner();
}
function removeHeldRow(side,id){
  const list=$('#heldList');if(!list)return;
  knownHeldKeys.delete(heldKey(side,id));
  const el=list.querySelector(`.icpt-item[data-id="${id}"][data-side="${side}"]`);
  if(el)el.remove();
  setListboxSelection($$('#heldList .icpt-item'),heldSelectionPredicate);
  syncInterceptBanner();
}
export async function toggleResponseIntercept(){
  try{await applyInterceptMutation(()=>api('/api/intercept/response/toggle',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:!state.intercept.responseEnabled})}));}catch(e){toast(e.message);}
}
$('#respInterceptToggle').onclick=toggleResponseIntercept;
export async function toggleIntercept(){
  try{await applyInterceptMutation(()=>api('/api/intercept/toggle',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:!state.intercept.enabled})}));}catch(e){toast(e.message);}
}
$('#interceptToggle').onclick=toggleIntercept;
// Arrow/Home/End over the hold queue. wireListbox binds traversal to the rows of
// the last full render, but held/released deltas add and remove rows in place,
// so a new row was unreachable and a removed one could take focus off-page. The
// keys are handled here instead, in the capture phase (ahead of the per-row
// handlers), against the live rows.
const HELD_NAV_KEYS=['ArrowDown','ArrowUp','Home','End'];
$('#heldList').addEventListener('keydown',e=>{
  if(!HELD_NAV_KEYS.includes(e.key))return;
  const row=e.target.closest?.('.icpt-item');
  if(!row||e.target!==row)return;
  e.preventDefault();e.stopPropagation();
  const els=[...$('#heldList').querySelectorAll('.icpt-item')];
  const from=els.indexOf(row),n=els.length;
  const to=e.key==='ArrowDown'?(from+1)%n:e.key==='ArrowUp'?(from-1+n)%n:e.key==='Home'?0:n-1;
  const el=focusOption(els,to);
  if(el&&to!==from)selectHeld(Number(el.dataset.id),el.dataset.side);
},true);
// Forward / Drop act on the selected item, routing to the request or response API.
function setHeldActionState(button,stateName,label){
  const buttons=[$('#forwardBtn'),$('#dropBtn')];
  buttons.forEach(b=>{if(b)b.disabled=stateName==='pending'||!state.heldSel;});
  button.classList.remove('is-pending','is-success','is-error');
  if(stateName!=='idle')button.classList.add('is-'+stateName);
  button.dataset.state=stateName;
  button.setAttribute('aria-busy',stateName==='pending'?'true':'false');
  button.textContent=label;
}
function heldSelectionOwns(key){
  const sel=state.heldSel;
  return !!sel&&heldKey(sel.side,sel.id)===key;
}
function clearHeldActionState(button,label){
  if(!button)return;
  button.classList.remove('is-pending','is-success','is-error');
  button.dataset.state='idle';
  button.setAttribute('aria-busy','false');
  button.textContent=label;
}
function restoreHeldActionControls(){
  clearHeldActionState($('#forwardBtn'),'Forward');
  clearHeldActionState($('#dropBtn'),'Drop');
  if(state.heldSel&&!heldLoadingKey)setHeldControlsDisabled(false);
}
function setHeldActionResult(button,key,stateName,label){
  if(!button)return false;
  if(!heldSelectionOwns(key))return false;
  setHeldActionState(button,stateName,label);
  return true;
}
function resetHeldAction(button,label,delay,epoch,key){
  setTimeout(()=>{
    if(epoch===heldActionEpoch&&heldSelectionOwns(key))setHeldActionState(button,'idle',label);
  },delay);
}
function reconcileHeldRemoval(sel){
  const queueKey=sel.side==='resp'?'responseQueue':'queue';
  const queue=(state.intercept?.[queueKey]||[]).filter(h=>h.id!==sel.id);
  replaceLocalInterceptState({...(state.intercept||{}),[queueKey]:queue});
  if(state.heldSel&&state.heldSel.id===sel.id&&state.heldSel.side===sel.side)state.heldSel=null;
  heldActionInFlight=null;
  restoreHeldActionControls();
  renderIntercept();
  // Phones show one pane at a time: after Forward or Drop go back to the queue.
  if(icptCtx.pane&&icptCtx.pane.mode()==='stack')icptCtx.pane.showList();
}
function releaseHeldAction(){
  const deferred=!!heldActionInFlight?.deferred;
  heldActionInFlight=null;
  if(deferred)renderIntercept();
  restoreHeldActionControls();
}
$('#forwardBtn').onclick=async()=>{const sel=state.heldSel;if(!sel)return;
  const base=sel.side==='resp'?'/api/intercept/response/':'/api/intercept/';
  const button=$('#forwardBtn');
  const row=document.querySelector(`#heldList .icpt-item[data-id="${sel.id}"][data-side="${sel.side}"]`);
  const epoch=++heldActionEpoch;
  const actionKey=heldKey(sel.side,sel.id);
  heldActionInFlight={key:actionKey,deferred:false};
  setHeldActionState(button,'pending','Forwarding…');
  // An untouched message omits `raw` so the server forwards the held original
  // bytes exactly; viewing Pretty never changes #heldRaw, so it stays untouched.
  const held=heldItem(sel.id,sel.side);
  const original=heldOriginalCache.get(actionKey)??(held?heldOriginal(held):null);
  const rawOut=original==null?$('#heldRaw').value:heldForwardRaw($('#heldRaw').value,original);
  try{await api(base+sel.id+'/forward',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(rawOut==null?{}:{raw:rawOut})});
    await animateOnce(row,[{opacity:1,transform:'translateX(0)'},{opacity:0,transform:'translateX(6px)'}],{duration:MOTION.base,easing:MOTION.exit});
    heldRawCache.delete(sel.side+':'+sel.id);
    heldOriginalCache.delete(heldKey(sel.side,sel.id));
    reconcileHeldRemoval(sel);
    setHeldActionResult(button,actionKey,'success','Forwarded');
    resetHeldAction(button,'Forward',600,epoch,actionKey);
    toast(sel.side==='resp'?'response forwarded':'forwarded');
  }catch(e){releaseHeldAction();setHeldActionResult(button,actionKey,'error','Forward failed');resetHeldAction(button,'Forward',900,epoch,actionKey);toast(e.message);}};
$('#heldDecodeBtn')&&($('#heldDecodeBtn').onclick=async()=>{
  const sel=state.heldSel;if(!sel)return;
  const rawEl=$('#heldRaw'),decEl=$('#heldDecoded');
  if(!rawEl||!decEl)return;
  if(heldView==='pretty')setHeldView('raw');
  if(decEl.style.display&&decEl.style.display!=='none'){
    heldDecodeEpoch++;
    decEl.style.display='none';decEl.hidden=true;rawEl.style.display='block';setHeldDecodeState(false);return;
  }
  const raw=rawEl.value||'';
  const decodeEpoch=++heldDecodeEpoch;
  const selectionKey=heldKey(sel.side,sel.id);
  const i=raw.indexOf('\r\n\r\n');const body=i>=0?raw.slice(i+4):raw;
  const hostLine=(raw.match(/^Host:\s*(.+)$/im)||[])[1]||'';
  const side=sel.side==='resp'?'res':'req';
  try{
    const d=await api('/api/codecs/test',{method:'POST',headers:{'content-type':'application/json'},
      body:JSON.stringify({side,rawBody:body,host:hostLine.trim()})});
    if(!heldDecodeCurrent(decodeEpoch,selectionKey,raw))return;
    if(!d.matched){toast('no message codec matched');return;}
    if(d.error){toast(d.error);return;}
    decEl.textContent=(d.title||d.codecId||'decoded')+'\n\n'+(d.plaintext||'');
    decEl.hidden=false;decEl.style.display='block';rawEl.style.display='none';setHeldDecodeState(true);
  }catch(e){if(heldDecodeCurrent(decodeEpoch,selectionKey,raw))toast(e.message);}
});
$('#heldRaw').addEventListener('input',()=>{
  heldDecodeEpoch++;
  const sel=state.heldSel,h=sel&&heldItem(sel.id,sel.side);
  if(h){
    heldRawCache.set(heldKey(sel.side,sel.id),$('#heldRaw').value);
    setHeldModified($('#heldRaw').value,heldOriginalCache.get(heldKey(sel.side,sel.id))||heldOriginal(h));
  }
});
// Typing in Pretty is the one act that turns the pretty text into the message to
// forward: it is stored into #heldRaw and the shared input handler above takes
// over (cache, MODIFIED badge, Decoded invalidation).
{
  const ed=heldPrettyEditEl(),ta=$('#heldRaw');
  if(ed&&ta){
    ed.addEventListener('input',()=>{
      ta.value=prettyBridge.edit(ed.value);
      paintHeldPrettyHL();
      ta.dispatchEvent(new Event('input'));
    });
    // Keep the colored overlay scrolled in lockstep with the editor.
    ed.addEventListener('scroll',()=>{const hl=$('#heldPrettyHL');if(hl){hl.scrollTop=ed.scrollTop;hl.scrollLeft=ed.scrollLeft;}});
  }
}
$('#heldBeautifyBtn')&&($('#heldBeautifyBtn').onclick=()=>{
  const ta=$('#heldRaw');if(!ta)return;
  setHeldView('raw');
  ta.value=heldBeautified(ta.value);ta.dispatchEvent(new Event('input'));ta.focus();
});
// Raw | Pretty: presentation only, so switching never touches the held bytes.
{
  const seg=$('#heldViewSeg');
  if(seg){
    seg.addEventListener('click',e=>{
      const b=e.target.closest&&e.target.closest('[data-held-view]');
      if(!b||b.disabled)return;
      setHeldView(b.dataset.heldView,{focus:true});
    });
    seg.addEventListener('keydown',e=>{
      if(e.key!=='ArrowLeft'&&e.key!=='ArrowRight')return;
      e.preventDefault();
      const next=e.key==='ArrowRight'?'pretty':'raw';
      setHeldView(next);
      seg.querySelector(`[data-held-view="${next}"]`)?.focus();
    });
  }
}
$('#heldResetBtn')&&($('#heldResetBtn').onclick=()=>{
  const sel=state.heldSel,ta=$('#heldRaw');if(!sel||!ta)return;
  setHeldView('raw');prettyBridge.reset();
  const original=heldOriginalCache.get(heldKey(sel.side,sel.id));if(original==null)return;
  ta.value=original;ta.dispatchEvent(new Event('input'));ta.focus();
});

// performDrop is the real, irreversible drop. It only runs after the 5 s undo
// window (see scheduleDrop), so it must work for an item that is no longer the
// selected one: visual state is applied only while the selection owns the key.
async function performDrop(sel){
  const base=sel.side==='resp'?'/api/intercept/response/':'/api/intercept/';
  const button=$('#dropBtn');
  const row=document.querySelector(`#heldList .icpt-item[data-id="${sel.id}"][data-side="${sel.side}"]`);
  const epoch=++heldActionEpoch;
  const actionKey=heldKey(sel.side,sel.id);
  heldActionInFlight={key:actionKey,deferred:false};
  if(heldSelectionOwns(actionKey))setHeldActionState(button,'pending','Dropping…');
  try{await api(base+sel.id+'/drop',{method:'POST'});
    await animateOnce(row,[{opacity:1,transform:'translateY(0)'},{opacity:0,transform:'translateY(-3px)'}],{duration:MOTION.fast,easing:MOTION.exit});
    heldRawCache.delete(sel.side+':'+sel.id);
    heldOriginalCache.delete(heldKey(sel.side,sel.id));
    reconcileHeldRemoval(sel);
    setHeldActionResult(button,actionKey,'success','Dropped');
    resetHeldAction(button,'Drop',600,epoch,actionKey);
    toast(sel.side==='resp'?'response dropped':'dropped');
  }catch(e){releaseHeldAction();setHeldActionResult(button,actionKey,'error','Drop failed');resetHeldAction(button,'Drop',900,epoch,actionKey);toast(e.message);}}
export async function applyInterceptFilter(draft=pendingFilterMutation||stageInterceptFilter()){
  if(draft.started)return draft.promise;
  draft.started=true;
  const {input}=draft;
  draft.promise=(async()=>{
    try{
      // Typing a pattern while "Only hold when" is unchecked saves it but changes
      // nothing, so it must not toast "filter off" on every pause in typing.
      let wasOn=false;
      const saved=await applyFilterMutation(()=>{wasOn=!!(acknowledgedFilterConfig||currentFilterConfig()).filterEnabled;return api('/api/intercept/filter',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(input)});},draft);
      const on=!!(input.enabled&&input.pattern);
      if(saved&&(on||wasOn))toast(on?'filter applied':'filter off');
      return saved;
    }catch(e){toast(e.message,'error');return false;}
  })();
  return draft.promise;
}
// The conditional filter auto-applies on a debounce (and on Enter), so there is
// no Apply button — keeping one would be a redundant third commit path.
$('#interceptFilterPattern').addEventListener('keydown',e=>{if(e.key==='Enter'){
  e.preventDefault();clearTimeout(icptFilterTimer);
  const draft=pendingFilterMutation&&filterControlsMatch(pendingFilterMutation.input)?pendingFilterMutation:stageInterceptFilter();
  applyInterceptFilter(draft);
}});
let icptFilterTimer=null;
function scheduleInterceptFilter(){
  clearTimeout(icptFilterTimer);
  const draft=stageInterceptFilter();
  icptFilterTimer=setTimeout(()=>applyInterceptFilter(draft),650);
}
['interceptFilterOn','interceptFilterTarget','interceptFilterPattern'].forEach(id=>{
  const el=$('#'+id);if(!el)return;
  el.addEventListener('change',scheduleInterceptFilter);
  if(el.tagName==='INPUT')el.addEventListener('input',scheduleInterceptFilter);
});

/* ---- rules ---- */
// Same wording as the add-rule picker, so a saved rule reads like the one added.
const RULE_TYPES=[['req-header','Request header'],['req-body','Request body'],['res-header','Response header'],['res-body','Response body']];
let rulesLoadEpoch=0,ruleMutationEpoch=0,ruleMutationLanes=new Map(),ruleMutationRevision=new Map(),ruleDrafts=new Map();
export function renderRules(){
  const body=$('#rulesBody');
  syncRulesCount();
  const active=document.activeElement;
  const activeRow=active?.closest?.('#rulesBody tr[data-id]');
  const focus={id:activeRow?.dataset.id||'',key:active?.dataset?.k||'',start:active?.selectionStart,end:active?.selectionEnd};
  const det=document.querySelector('details.icpt-mr');
  if(det&&state.rules.length)det.open=true;
  if(!state.rules.length){body.innerHTML='<tr><td colspan="5" class="hint" style="padding:10px 8px">No rules. Add one below.</td></tr>';return;}
  const rows=state.rules.map(r=>({...r,...(ruleDrafts.get(r.id)||{})}));
  body.innerHTML=rows.map(r=>`<tr data-id="${r.id}">
    <td><input type="checkbox" aria-label="Enable interception rule ${r.id}" ${r.enabled?'checked':''} data-k="enabled"></td>
    <td><select data-k="type" aria-label="Interception rule ${r.id} type">${RULE_TYPES.map(([tp,label])=>`<option value="${tp}" ${r.type===tp?'selected':''}>${label}</option>`).join('')}</select></td>
    <td><input type="text" data-k="match" aria-label="Interception rule ${r.id} match" placeholder="match regex" spellcheck="false" value="${escAttr(r.match)}"></td>
    <td><input type="text" data-k="replace" aria-label="Interception rule ${r.id} replacement" placeholder="replacement" spellcheck="false" value="${escAttr(r.replace)}"></td>
    <td><button class="btn danger" data-del="${r.id}" data-k="delete" aria-label="Delete interception rule ${r.id}">Delete</button></td></tr>`).join('');
  body.querySelectorAll('tr').forEach(tr=>{
    const id=Number(tr.dataset.id);
    tr.querySelectorAll('input[data-k],select[data-k]').forEach(inp=>{
      inp.addEventListener('input',()=>rememberRuleDraft(id,tr));
      inp.addEventListener('change',()=>updateRule(id,tr));
    });
  });
  body.querySelectorAll('[data-del]').forEach(b=>b.onclick=()=>deleteRule(Number(b.dataset.del)));
  if(focus.id&&focus.key)requestAnimationFrame(()=>{const el=body.querySelector(`tr[data-id="${focus.id}"] [data-k="${focus.key}"]`);if(!el)return;el.focus({preventScroll:true});if(typeof focus.start==='number'&&el.setSelectionRange)el.setSelectionRange(focus.start,focus.end);});
}
export async function loadRules(){const epoch=++rulesLoadEpoch,mutationSnapshot=ruleMutationEpoch;try{const d=await api('/api/rules');if(epoch!==rulesLoadEpoch||mutationSnapshot!==ruleMutationEpoch||ruleMutationLanes.size)return;state.rules=d.rules||[];renderRules();}catch(e){if(epoch!==rulesLoadEpoch||ruleMutationLanes.size)return;
  // A bare toast left the table showing stale rows with no way back. Surface the
  // failure in the table itself, with a Retry, and keep the toast for the nudge.
  const body=$('#rulesBody');
  if(body){
    const cell=document.createElement('td');
    cell.colSpan=5;
    // renderLoadError sets display:block on its target, which would pull a <td>
    // out of table layout and void the colspan — give it an inner slot instead.
    const slot=document.createElement('div');
    cell.appendChild(slot);
    const row=document.createElement('tr');
    row.appendChild(cell);
    body.replaceChildren(row);
    renderLoadError(slot,'Interception rules',e,loadRules,state.rules.length>0);
  }
  toast(e.message);}}
function ruleMutation(id,work){
  ruleMutationEpoch++;const rev=(ruleMutationRevision.get(id)||0)+1;ruleMutationRevision.set(id,rev);
  const prior=ruleMutationLanes.get(id)||Promise.resolve();const next=prior.catch(()=>{}).then(work);ruleMutationLanes.set(id,next);
  return next.finally(()=>{if(ruleMutationLanes.get(id)!==next)return;ruleMutationLanes.delete(id);if(!ruleMutationLanes.size)loadRules();});
}
function rememberRuleDraft(id,tr){
  const r=state.rules.find(x=>x.id===id);if(!r)return null;
  const get=k=>tr.querySelector(`[data-k="${k}"]`);
  const draft={id,ord:r.ord,enabled:get('enabled').checked,type:get('type').value,match:get('match').value,replace:get('replace').value,bigBody:!!r.bigBody};
  ruleDrafts.set(id,draft);return draft;
}
export async function updateRule(id,tr){
  const upd=rememberRuleDraft(id,tr);if(!upd)return;
  const pending=ruleMutation(id,()=>api('/api/rules/'+id,{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(upd)}));
  const revision=ruleMutationRevision.get(id);
  try{await pending;if(revision===ruleMutationRevision.get(id)&&ruleDrafts.get(id)===upd){ruleDrafts.delete(id);toast('rule saved');}}
  catch(e){if(revision===ruleMutationRevision.get(id)){if(ruleDrafts.get(id)===upd){ruleDrafts.delete(id);renderRules();}toast(e.message,'error');}}
}
export async function deleteRule(id){
  const hadDraft=ruleDrafts.has(id),draftAtDelete=ruleDrafts.get(id);
  const pending=ruleMutation(id,()=>api('/api/rules/'+id,{method:'DELETE'})),revision=ruleMutationRevision.get(id);
  try{await pending;if(revision===ruleMutationRevision.get(id))ruleDrafts.delete(id);}
  catch(e){
    if(revision===ruleMutationRevision.get(id)&&hadDraft&&ruleDrafts.get(id)===draftAtDelete){ruleDrafts.delete(id);renderRules();}
    if(revision===ruleMutationRevision.get(id))toast(e.message,'error');
  }
}
let ruleAddInFlight=false,ruleAddEpoch=0;
function setRuleAddState(stateName){const b=$('#addRuleBtn');if(!b)return;b.disabled=stateName==='pending';b.setAttribute('aria-busy',stateName==='pending'?'true':'false');b.textContent=stateName==='pending'?'Adding…':stateName==='success'?'Added':'+ Add rule';}
$('#addRuleBtn').onclick=async()=>{
  if(ruleAddInFlight)return;
  const rule={type:$('#newRuleType').value,match:$('#newRuleMatch').value,replace:$('#newRuleReplace').value,enabled:true};
  if(!rule.match){toast('match regex required');return;}
  ruleAddInFlight=true;const addEpoch=++ruleAddEpoch;setRuleAddState('pending');
  try{await ruleMutation(0,()=>api('/api/rules',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(rule)}));
    $('#newRuleMatch').value='';$('#newRuleReplace').value='';toast('rule added');setRuleAddState('success');}catch(e){toast(e.message,'error');setRuleAddState('idle');}
  finally{ruleAddInFlight=false;if($('#addRuleBtn')?.textContent==='Added')setTimeout(()=>{if(addEpoch===ruleAddEpoch)setRuleAddState('idle');},600);}
};

/* ---- workbench additions: deferred drop, forward-all, context, split ---- */
function selectionDropping(){const sel=state.heldSel;return !!sel&&dropMarks.has(heldKey(sel.side,sel.id));}
function setHeldChrome(visible){
  ['#heldActions','#heldCtx'].forEach(sel=>{const el=$(sel);if(el)el.style.display=visible?'flex':'none';});
}
// Out-of-scope auto-forward tally (see intercept-model.js). Hidden while the
// client cannot decide scope exactly or interception is off.
const autoFwd=createAutoForwardTally();
let autoFwdSource=null,autoFwdCompiled=null;
function autoFwdScope(){
  if(autoFwdSource!==state.scope){autoFwdSource=state.scope;autoFwdCompiled=compileScopeRules(state.scope);}
  return autoFwdCompiled;
}
function renderAutoForwarded(){
  const el=$('#heldAutoFwd');if(!el)return;
  const c=autoFwdScope();
  const show=!!(state.intercept&&state.intercept.enabled)&&c.hasInclude&&c.evaluable;
  el.hidden=!show;
  if(show)el.textContent=autoForwardText(autoFwd.count());
}
registerSseHandler('flow.new',m=>{
  if(!m||!m.flow)return;
  const on=!!(state.intercept&&state.intercept.enabled);
  if(!isAutoForwarded(m.flow,{interceptOn:on,compiled:autoFwdScope(),flowInScope}))return;
  autoFwd.add();renderAutoForwarded();
});
function renderHeldContext(){
  const sc=$('#heldScopeText'),idn=$('#heldIdentityText'),note=$('#heldScopeNote');
  if(sc)sc.textContent=scopeBadgeText(icptCtx.scope);
  if(idn)idn.textContent=identityText(icptCtx.identity);
  if(note)note.textContent=scopeNote(icptCtx.scope).text;
  const chip=$('#heldIdentity');if(chip)chip.hidden=!icptCtx.identity;
}
function syncRulesCount(){
  const el=$('#rulesCount');if(!el)return;
  const r=rulesSummary(state.rules);
  el.textContent=r.total;
  el.setAttribute('aria-label',r.total?'Match and replace rules: '+r.label:'No match and replace rules');
}
function syncDropMarks(){
  document.querySelectorAll('#heldList .icpt-item').forEach(row=>{
    const on=dropMarks.has(heldKey(row.dataset.side,Number(row.dataset.id)));
    row.classList.toggle('is-dropping',on);
    const tag=row.querySelector('.icpt-dropping-tag');
    if(on&&!tag){const t=document.createElement('span');t.className='icpt-dropping-tag';t.textContent='Dropping';row.appendChild(t);}
    else if(!on&&tag)tag.remove();
  });
  if(!heldActionInFlight)setHeldControlsDisabled(false);
}
const dropToasts=new Map();
const dropScheduler=createDropScheduler({
  // A due drop waits while a Forward or another Drop is in flight: performDrop
  // owns heldActionInFlight/heldActionEpoch and must not clobber the other's.
  busy:()=>!!heldActionInFlight,
  commit:entry=>{
    dropToasts.get(heldKey(entry.side,entry.id))?.dismiss();dropToasts.delete(heldKey(entry.side,entry.id));
    // The server may have released it meanwhile (timeout, another client).
    if(!heldItem(entry.id,entry.side)){dropMarks.delete(heldKey(entry.side,entry.id));syncDropMarks();return;}
    dropMarks.delete(heldKey(entry.side,entry.id));
    performDrop(entry);
  },
  onChange:(key,what)=>{
    if(what==='pending')dropMarks.add(key);
    else if(what==='undone')dropMarks.delete(key);
    syncDropMarks();
  },
});
// Drop never shows a confirm dialog. It is deferred instead: the request stays
// held while the Undo toast is up, and only then does the irreversible API call
// run. Undo therefore just cancels a timer; it never "restores" a sent drop.
function scheduleDrop(){
  const sel=state.heldSel;if(!sel||selectionDropping()||heldActionInFlight)return;
  const key=heldKey(sel.side,sel.id);
  if(!dropScheduler.schedule(key,{side:sel.side,id:sel.id}))return;
  const t=showDropToast(dropToastText(sel.side),{
    onUndo:()=>{dropToasts.delete(key);if(dropScheduler.undo(key))toast(sel.side==='resp'?'response kept on hold':'request kept on hold','success');},
    onPause:()=>dropScheduler.pause(key),
    onResume:()=>dropScheduler.resume(key),
  });
  dropToasts.set(key,t);
}
$('#dropBtn').onclick=scheduleDrop;

$('#forwardRepeaterBtn')&&($('#forwardRepeaterBtn').onclick=async()=>{
  const sel=state.heldSel,h=sel&&sel.side==='req'&&heldItem(sel.id,sel.side);
  if(!h)return;
  const sent={scheme:h.scheme,host:h.host,raw:$('#heldRaw').value};
  await $('#forwardBtn').onclick();
  // Forward reports failure with a toast and leaves the item held: only open
  // Repeater when the request really left the queue.
  if(!heldItem(sel.id,'req'))sendRawToRepeater(sent);
});

async function forwardAll(){
  if(forwardAllBusy||heldActionInFlight)return;
  const plan=forwardAllPlan(heldItems(),{dropping:dropMarks,rawCache:heldRawCache,originalCache:heldOriginalCache});
  if(!plan.length)return;
  if(!await uiConfirm('Forward all held items?','Forward <b>'+plan.length+'</b> held item'+(plan.length===1?'':'s')+' now? Edits you made are kept. Items waiting to be dropped are skipped.','Forward all','btn btn-primary'))return;
  forwardAllBusy=true;setHeldControlsDisabled(false);
  const status=$('#heldActionStatus');
  let done=0,failure='';
  for(const step of plan){
    if(!heldItem(step.id,step.side)){done++;continue;}
    try{
      await api(forwardPath(step.side,step.id),step.raw!=null?{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({raw:step.raw})}:{method:'POST'});
      heldRawCache.delete(heldKey(step.side,step.id));heldOriginalCache.delete(heldKey(step.side,step.id));
      reconcileHeldRemoval({side:step.side,id:step.id});
      done++;
      if(status)status.textContent=forwardAllProgress(done,plan.length);
    }catch(e){failure=e.message||'request failed';break;}
  }
  forwardAllBusy=false;
  if(status)status.textContent=forwardAllProgress(done,plan.length,failure);
  if(failure)toast(forwardAllProgress(done,plan.length,failure),'error');else toast(forwardAllProgress(done,plan.length));
  setHeldControlsDisabled(false);
}
$('#forwardAllBtn')&&($('#forwardAllBtn').onclick=forwardAll);

// Held header context: scope and identity come from the shared project state.
import('./project-state.js').then(m=>{
  const apply=ps=>{icptCtx.scope=ps&&ps.scope;icptCtx.identity=(ps&&ps.activeIdentity)||'';renderHeldContext();};
  m.projectState.subscribe(apply);apply(m.projectState.get());
}).catch(()=>{});

// Hold queue | editor as a SplitPane. On phones it stacks: choosing an item
// opens the editor, Back returns to the queue.
try{
  const root=$('#icptWork');
  if(root){
    icptCtx.pane=createSplitPane({
      root,key:'intercept.split',orientation:'right',min:[220,320],default:30,stackBelow:720,scopeKey:projectStorageKey,
      label:'Resize hold queue and editor',backLabel:'Hold queue',
      onView:view=>{if(view==='list')requestAnimationFrame(()=>($('#heldList .icpt-item.sel')||$('#heldList .icpt-item'))?.focus({preventScroll:true}));},
    });
    const open=e=>{const row=e.target.closest?.('.icpt-item');if(row&&icptCtx.pane.mode()==='stack')icptCtx.pane.showDetail(row);};
    $('#heldList').addEventListener('click',open);
    $('#heldList').addEventListener('keydown',e=>{if(e.key==='Enter')open(e);});
  }
}catch(e){/* the plain two-column layout still works without the split */}

// Settings' import/restore asks the intercept rule list to reload over the bus
// instead of importing it from settings.js (part of breaking the settings cycle).
busOn(EVENTS.INTERCEPT_RELOAD_RULES, () => { loadRules(); });
// Toasts sit just above the viewport bottom, which is where the held action bar
// is: while the bar is on screen, lift them above it so a toast (the drop Undo
// included) never covers Forward or Drop. The panel and the work area are
// observed because tab switches and the Match & Replace drawer move the bar
// without resizing it.
try{
  const bar=$('#heldActions'),host=$('#toast');
  let lifted=false;
  const syncToastLift=()=>{
    const r=bar.getBoundingClientRect();
    const on=r.height>0&&r.top<innerHeight&&r.bottom>0;
    if(on)host.style.bottom=`max(calc(18px + env(safe-area-inset-bottom,0px)),${Math.round(innerHeight-r.top+8)}px)`;
    else if(lifted)host.style.bottom='';
    lifted=on;
  };
  if(bar&&host&&typeof ResizeObserver==='function'){
    const ro=new ResizeObserver(syncToastLift);
    [bar,$('#panel-intercept'),$('#icptWork')].forEach(el=>{if(el)ro.observe(el);});
    addEventListener('resize',syncToastLift);
  }
}catch(e){/* toasts keep their default position */}
