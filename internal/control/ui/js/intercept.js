import { $, $$, esc, escAttr, state, toast, api, methodColor, wireRowKey, prettify } from './core.js';
import { animateOnce, MOTION } from './motion.js';

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
let interceptStateEpoch=0;
let interceptSummaryEpoch=0;
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
function heldKey(side,id){return side+':'+id;}
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
  const modified=raw!==original;
  if(badge){badge.hidden=!modified;badge.textContent=modified?'MODIFIED':'';}
}

function setHeldControlsDisabled(disabled){
  ['#heldRaw','#forwardBtn','#dropBtn','#heldBeautifyBtn','#heldResetBtn','#heldDecodeBtn']
    .map(s=>$(s)).forEach(el=>{if(el)el.disabled=!!disabled||!!heldActionInFlight;});
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
  el.id='heldLoadState';el.className='hint';el.setAttribute('role',retry?'alert':'status');
  el.style.cssText='padding:10px 14px;border-bottom:1px solid var(--line);color:'+(retry?'var(--red)':'var(--fg2)');
  el.textContent=text;
  if(retry){
    const b=document.createElement('button');b.type='button';b.className='btn';b.style.marginLeft='10px';b.textContent='Retry loading held message';
    b.onclick=()=>selectHeld(h.id,h.side);el.appendChild(b);
  }
  main.insertBefore(el,main.firstChild);
}

function showHeldLoading(h){
  heldLoadingKey=heldKey(h.side,h.id);
  clearHeldLoadMessage();
  const head=$('#heldEditor'),empty=$('#heldEmpty'),ta=$('#heldRaw'),dec=$('#heldDecoded');
  if(head)head.style.display='flex';
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

export function renderIntercept(){
  const ic=state.intercept||{};
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
  const list=$('#heldList');
  if(!total){list.innerHTML='';state.heldSel=null;showEditor(null);return;}
  list.innerHTML=items.map(h=>`<div class="icpt-item${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?' sel':''}" data-id="${h.id}" data-side="${h.side}" aria-current="${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?'true':'false'}">
    <span class="icpt-tag ${h.side}">${h.side==='req'?'REQ':'RESP'}</span>
    ${h.side==='req'?`<span class="m" style="color:${methodColor(h.method)}">${esc(h.method)}</span>`:''}
    <span class="u">${esc(h.host)}${esc(h.path)}</span></div>`).join('');
  $$('#heldList .icpt-item').forEach(el=>{el.onclick=()=>selectHeld(Number(el.dataset.id),el.dataset.side);wireRowKey(el,()=>selectHeld(Number(el.dataset.id),el.dataset.side));});
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
    if(ta){ta.style.display='none';ta.disabled=false;ta.removeAttribute('placeholder');}
    if(empty)empty.style.display='flex';
    const dec=$('#heldDecoded');if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
    setHeldDecodeState(false);
    setHeldControlsDisabled(false);
    return;
  }
  if(head)head.style.display='flex';
  if(empty)empty.style.display='none';
  const dec=$('#heldDecoded');if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
  setHeldDecodeState(false);
  const original=heldOriginal(h);
  if(ta){ta.style.display='block';ta.disabled=false;ta.removeAttribute('placeholder');ta.value=h.raw||'';setHeldModified(ta.value,original);}
  setHeldControlsDisabled(false);
  if(title)title.innerHTML=h.side==='resp'
    ?`<span class="icpt-tag resp" style="margin-right:8px">RESP</span><span class="u">${esc(h.host)}${esc(h.path)}</span>`
    :`<span style="color:${methodColor(h.method)};font-weight:700">${esc(h.method)}</span> ${esc(h.host)}${esc(h.path)}`;
}
export async function selectHeld(id,side,opts={}){
  const previousKey=state.heldSel&&heldKey(state.heldSel.side,state.heldSel.id);
  const nextKey=heldKey(side,id);
  state.heldSel={id,side};
  if(previousKey!==nextKey)heldDecodeEpoch++;
  if(previousKey!==nextKey){
    if(!heldActionInFlight)restoreHeldActionControls();
  }
  $$('#heldList .icpt-item').forEach(el=>{const selected=Number(el.dataset.id)===id&&el.dataset.side===side;el.classList.toggle('sel',selected);el.setAttribute('aria-current',selected?'true':'false');});
   const h=heldItem(id,side);if(!h)return;
   const cacheKey=side+':'+id;
   let raw=heldRawCache.has(cacheKey)?heldRawCache.get(cacheKey):h.raw;
   if(!heldOriginalCache.has(cacheKey))heldOriginalCache.set(cacheKey,heldOriginal(h));

  if(heldLoadingKey===cacheKey&&opts.keepEditor)return;

  if(opts.keepEditor){
    const ta=$('#heldRaw');
    if(ta&&document.activeElement===ta)return;
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
$('#respInterceptToggle').onclick=async()=>{
  try{await applyInterceptMutation(()=>api('/api/intercept/response/toggle',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:!state.intercept.responseEnabled})}));}catch(e){toast(e.message);}
};
export async function toggleIntercept(){
  try{await applyInterceptMutation(()=>api('/api/intercept/toggle',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:!state.intercept.enabled})}));}catch(e){toast(e.message);}
}
$('#interceptToggle').onclick=toggleIntercept;
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
  try{await api(base+sel.id+'/forward',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({raw:$('#heldRaw').value})});
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
$('#heldBeautifyBtn')&&($('#heldBeautifyBtn').onclick=()=>{
  const ta=$('#heldRaw');if(!ta)return;
  ta.value=prettify(ta.value);ta.dispatchEvent(new Event('input'));ta.focus();
});
$('#heldResetBtn')&&($('#heldResetBtn').onclick=()=>{
  const sel=state.heldSel,ta=$('#heldRaw');if(!sel||!ta)return;
  const original=heldOriginalCache.get(heldKey(sel.side,sel.id));if(original==null)return;
  ta.value=original;ta.dispatchEvent(new Event('input'));ta.focus();
});

$('#dropBtn').onclick=async()=>{const sel=state.heldSel;if(!sel)return;
  const base=sel.side==='resp'?'/api/intercept/response/':'/api/intercept/';
  const button=$('#dropBtn');
  const row=document.querySelector(`#heldList .icpt-item[data-id="${sel.id}"][data-side="${sel.side}"]`);
  const epoch=++heldActionEpoch;
  const actionKey=heldKey(sel.side,sel.id);
  heldActionInFlight={key:actionKey,deferred:false};
  setHeldActionState(button,'pending','Dropping…');
  try{await api(base+sel.id+'/drop',{method:'POST'});
    await animateOnce(row,[{opacity:1,transform:'translateY(0)'},{opacity:0,transform:'translateY(-3px)'}],{duration:MOTION.fast,easing:MOTION.exit});
    heldRawCache.delete(sel.side+':'+sel.id);
    heldOriginalCache.delete(heldKey(sel.side,sel.id));
    reconcileHeldRemoval(sel);
    setHeldActionResult(button,actionKey,'success','Dropped');
    resetHeldAction(button,'Drop',600,epoch,actionKey);
    toast(sel.side==='resp'?'response dropped':'dropped');
  }catch(e){releaseHeldAction();setHeldActionResult(button,actionKey,'error','Drop failed');resetHeldAction(button,'Drop',900,epoch,actionKey);toast(e.message);}};
export async function applyInterceptFilter(draft=pendingFilterMutation||stageInterceptFilter()){
  if(draft.started)return draft.promise;
  draft.started=true;
  const {input}=draft;
  draft.promise=(async()=>{
    try{
      const saved=await applyFilterMutation(()=>api('/api/intercept/filter',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(input)}),draft);
      if(saved)toast(input.enabled&&input.pattern?'filter applied':'filter off');
      return saved;
    }catch(e){toast(e.message);return false;}
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
let rulesLoadEpoch=0,ruleMutationEpoch=0,ruleMutationLanes=new Map(),ruleMutationRevision=new Map(),ruleDrafts=new Map();
export function renderRules(){
  const body=$('#rulesBody');
  const active=document.activeElement;
  const activeRow=active?.closest?.('#rulesBody tr[data-id]');
  const focus={id:activeRow?.dataset.id||'',key:active?.dataset?.k||'',start:active?.selectionStart,end:active?.selectionEnd};
  const det=document.querySelector('details.icpt-mr');
  if(det&&state.rules.length)det.open=true;
  if(!state.rules.length){body.innerHTML='<tr><td colspan="5" class="hint" style="padding:10px 8px">No rules. Add one below.</td></tr>';return;}
  const rows=state.rules.map(r=>({...r,...(ruleDrafts.get(r.id)||{})}));
  body.innerHTML=rows.map(r=>`<tr data-id="${r.id}">
    <td><input type="checkbox" aria-label="Enable interception rule ${r.id}" ${r.enabled?'checked':''} data-k="enabled"></td>
    <td><select data-k="type" aria-label="Interception rule ${r.id} type">${['req-header','req-body','res-header','res-body'].map(tp=>`<option value="${tp}" ${r.type===tp?'selected':''}>${tp}</option>`).join('')}</select></td>
    <td><input type="text" data-k="match" aria-label="Interception rule ${r.id} match" value="${escAttr(r.match)}"></td>
    <td><input type="text" data-k="replace" aria-label="Interception rule ${r.id} replacement" value="${escAttr(r.replace)}"></td>
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
export async function loadRules(){const epoch=++rulesLoadEpoch,mutationSnapshot=ruleMutationEpoch;try{const d=await api('/api/rules');if(epoch!==rulesLoadEpoch||mutationSnapshot!==ruleMutationEpoch||ruleMutationLanes.size)return;state.rules=d.rules||[];renderRules();}catch(e){if(epoch===rulesLoadEpoch&&!ruleMutationLanes.size)toast(e.message);}}
function ruleMutation(id,work){
  ruleMutationEpoch++;const rev=(ruleMutationRevision.get(id)||0)+1;ruleMutationRevision.set(id,rev);
  const prior=ruleMutationLanes.get(id)||Promise.resolve();const next=prior.catch(()=>{}).then(work);ruleMutationLanes.set(id,next);
  return next.finally(()=>{if(ruleMutationLanes.get(id)!==next)return;ruleMutationLanes.delete(id);if(!ruleMutationLanes.size)loadRules();});
}
function rememberRuleDraft(id,tr){
  const r=state.rules.find(x=>x.id===id);if(!r)return null;
  const get=k=>tr.querySelector(`[data-k="${k}"]`);
  const draft={id,ord:r.ord,enabled:get('enabled').checked,type:get('type').value,match:get('match').value,replace:get('replace').value};
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
    $('#newRuleMatch').value='';$('#newRuleReplace').value='';toast('rule added');setRuleAddState('success');}catch(e){toast(e.message);setRuleAddState('idle');}
  finally{ruleAddInFlight=false;if($('#addRuleBtn')?.textContent==='Added')setTimeout(()=>{if(addEpoch===ruleAddEpoch)setRuleAddState('idle');},600);}
};
