import { $, esc, escAttr, state, toast, toastError, api, methodColor, statusColor, statusText, fmtSize, fmtDur, highlightHTTP, prettify, RENDER_CAP, openModal, closeModal, isBinaryMime, bodyMime, headerBlockText, copyText, flowBodyDownloadName, flowBodyDownloadHref, wireSelectionDecode, openFlow, getHook, icon } from './core.js';
import { syncControls, renderChips, loadFlows, selectFlow } from './proxy.js';
import { sendToRepeater, sendToIntruder } from './tools.js';
/* ---- flow inspect entry points (Map graph/table, Scanner findings, Findings evidence, …)
   flowPopup/closeFlowPopup are a compatibility shim over the Flow Drawer: when the
   drawer module has registered `openFlow` it handles the request. The modal below
   remains only as the fallback for a drawer that failed to load, so a missing
   optional module can never leave "inspect flow" dead. ---- */
let fmOpenEpoch=0;
const fmSideEpoch={req:0,res:0};
export function closeFlowPopup({updateRoute=true}={}){
  const closeDrawer=getHook('closeFlow');
  if(closeDrawer)closeDrawer({updateRoute:false});
  fmOpenEpoch++;fmSideEpoch.req++;fmSideEpoch.res++;
  if($('#flowModal')?.style.display==='flex')closeModal($('#flowModal'));
  const match=location.hash.match(/^#finding-(\d+)\/flow-\d+$/i);
  if(updateRoute&&match)history.replaceState(null,'',`#finding-${match[1]}/evidence`);
}
function setFlowInspectorSide(side){
  $('#flowModal .flow-inspector')?.setAttribute('data-mobile-side',side);
  $('#fmSides')?.querySelectorAll('[data-side]').forEach(button=>{
    const active=button.dataset.side===side;button.classList.toggle('on',active);button.setAttribute('aria-pressed',String(active));
  });
}
$('#fmSides')?.querySelectorAll('[data-side]').forEach(button=>button.onclick=()=>setFlowInspectorSide(button.dataset.side));
function fmFlowUrl(d){
  if(!d) return '';
  const port = d.port && !((d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80)) ? ':'+d.port : '';
  return (d.scheme||'http')+'://'+d.host+port+(d.path||'/');
}

export async function flowPopup(id, opts={}){
  // Findings pass modal so the request and response open in the centered dialog
  // instead of the history sidebar. Other callers keep the drawer.
  if(opts.modal){await openFlowModal(id);return;}
  if(openFlow(id,{source:'popup'}))return;
  await openFlowModal(id);
}
async function openFlowModal(id){
  const epoch=++fmOpenEpoch;
  let d;
  try{d=await api('/api/flows/'+id);}catch(e){if(epoch===fmOpenEpoch)toast('flow: '+e.message);return;}
  if(epoch!==fmOpenEpoch)return;
  state.fm = { id, detail: d, url: fmFlowUrl(d), pretty: true, compare: false };
  setFlowInspectorSide(d.status||d.resLen||d.resBodyHash?'res':'req');
  const compareBtn=$('#fmSeg').querySelector('[data-v="compare"]');
  const hasCompare=!!(d.originalReqBodyHash||d.originalResBodyHash||d.originalReqHeaders||d.originalResHeaders);
  if(compareBtn){compareBtn.hidden=!hasCompare;compareBtn.disabled=!hasCompare;}
  $('#fmTitle').innerHTML = `<span style="color:${methodColor(d.method)};font-weight:700">${esc(d.method)}</span> <span style="font-family:var(--mono);color:var(--fg2)">${esc(state.fm.url)}</span>`;
  $('#fmStatus').textContent = d.status ? `${d.status} ${statusText(d.status)}`+(d.durationMs ? ` · ${fmtDur(d.durationMs)}` : '') : (d.error || '');
  $('#fmStatus').style.color = statusColor(d.status);
  $('#fmSeg').querySelectorAll('button').forEach(b => { const on = b.dataset.v === 'pretty'; b.classList.toggle('on', on); b.setAttribute('aria-pressed', on ? 'true' : 'false'); });
  openModal($('#flowModal'),{onEscape:closeFlowPopup,onDismiss:closeFlowPopup});
  fmRenderSide('req'); fmRenderSide('res');
}

function fmOriginal(d,side){
  const names=side==='req'?['originalReqRaw','originalRequest','originalReq']:['originalResRaw','originalResponse','originalRes'];
  for(const name of names){if(typeof d?.[name]==='string'&&d[name])return d[name];}
  return '';
}
function fmCurrent(d,side){
  const names=side==='req'?['currentReqRaw','modifiedReqRaw','modifiedRequest']:['currentResRaw','modifiedResRaw','modifiedResponse'];
  for(const name of names){if(typeof d?.[name]==='string'&&d[name])return d[name];}
  return '';
}
function fmCompare(raw,original,current){
  if(!original&&!current)return raw;
  const left=original||raw,right=current||raw;
  return `--- original\n${left}\n\n+++ current\n${right}`;
}
document.addEventListener('click', e => {
  const a = e.target.closest && e.target.closest('a.md-flow-link');
  if (!a) return;
  e.preventDefault();
  const id = Number(a.dataset.flow);
  if (id) flowPopup(id);
});

export async function fmRenderSide(side){
  const el = side === 'req' ? $('#fmReq') : $('#fmRes');
  const dec = side === 'req' ? $('#fmReqDecode') : $('#fmResDecode');
  if(dec)dec.hidden=true;
  const renderEpoch=++fmSideEpoch[side];
  const id = state.fm.id; // snapshot: a second flowPopup must not let this render write the wrong flow
  const d = state.fm.detail;
  const pretty=state.fm.pretty,compare=state.fm.compare;
  const current=()=>fmSideEpoch[side]===renderEpoch&&state.fm.id===id&&state.fm.detail===d&&state.fm.pretty===pretty&&state.fm.compare===compare;
  const len = side === 'req' ? d.reqLen : d.resLen;
  const mime = bodyMime(d, side);
  if(isBinaryMime(mime)){
    const dl=flowBodyDownloadName(state.fm.id,side,mime), href=flowBodyDownloadHref(state.fm.id,side);
    el.innerHTML = highlightHTTP(headerBlockText(d, side))+`<div class="hint" style="padding:14px 0 0;line-height:1.7">Body is <b>${esc(mime)}</b>${len ? ' · '+fmtSize(len) : ''} — binary, not rendered.<br><a class="btn" style="margin-top:8px;display:inline-block" href="${href}" download="${escAttr(dl)}"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-download"/></svg> Download body</a></div>`;
    return;
  }
  if(len > RENDER_CAP){
    const dl=flowBodyDownloadName(state.fm.id,side,mime), href=flowBodyDownloadHref(state.fm.id,side);
    el.innerHTML = `<div class="hint" style="padding:14px;line-height:1.7">${side === 'req' ? 'Request' : 'Response'} body is <b>${fmtSize(len)}</b> — not rendered.<br><a class="btn" style="margin-top:8px;display:inline-block" href="${href}" download="${escAttr(dl)}"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-download"/></svg> Download body</a></div>`;
    return;
  }
  el.innerHTML = '<span class="hint" style="padding:12px">loading…</span>';
  try{
    const raw=await api('/api/flows/'+id+'/raw?side='+side+(compare?'&variant=original':''));
    if(!current())return;
    el._rawText=raw;
    el.innerHTML=highlightHTTP(pretty?prettify(raw):raw,pretty,mime);
  }catch(e){if(current()){
    el.innerHTML=`<span role="alert">${esc(e.message)}</span> <button type="button" class="btn" data-fm-retry>Retry</button>`;
    el.querySelector('[data-fm-retry]').onclick=()=>fmRenderSide(side);
  }}
}

$('#fmClose') && ($('#fmClose').onclick = () => closeFlowPopup());
$('#fmCopyUrl') && ($('#fmCopyUrl').onclick = () => {
  const url = state.fm && (state.fm.url || fmFlowUrl(state.fm.detail));
  if(url) copyText(url, 'URL copied');
});
$('#fmCopyLink') && ($('#fmCopyLink').onclick = () => {
  const id = state.fm && state.fm.id;
  if(!id) return;
  copyText(location.origin + '/#flow-' + id, 'Flow link copied');
});
$('#fmRepeater') && ($('#fmRepeater').onclick = () => {
  const id = state.fm && state.fm.id;
  if(!id) return;
  closeModal($('#flowModal'));
  sendToRepeater({ id });
});
// The popup opens from Intruder results, Map and Scanner as well, so it carries
// the same onward actions as History: Intruder and Add to finding. The buttons
// are injected here and close the popup first, as the Repeater action does.
function wireFlowPopupActions(){
  const repeater=$('#fmRepeater');
  if(!repeater||$('#fmIntruder'))return;
  const intruder=document.createElement('button');
  intruder.type='button';intruder.className='btn';intruder.id='fmIntruder';
  intruder.title='Load this flow into Intruder';intruder.innerHTML='Intruder '+icon('external');
  const finding=document.createElement('button');
  finding.type='button';finding.className='btn';finding.id='fmFinding';
  finding.title='Add this flow to a finding';finding.textContent='+ Finding';
  repeater.after(intruder,finding);
  intruder.onclick=()=>{
    const id=state.fm&&state.fm.id;if(!id)return;
    closeModal($('#flowModal'));
    sendToIntruder({id});
  };
  finding.onclick=()=>{
    const id=state.fm&&state.fm.id;if(!id)return;
    closeModal($('#flowModal'));
    import('./findings.js').then(m=>m.addFlowToFinding(id)).catch(e=>toastError('Add to finding failed',e));
  };
}
wireFlowPopupActions();
$('#fmProxy') && ($('#fmProxy').onclick = () => {
  const d = state.fm && state.fm.detail, id = state.fm && state.fm.id;
  closeModal($('#flowModal'));
  if(!d) return;
  document.querySelector('.tab[data-tab="proxy"]').click();
  state.filters = { scheme: '', method: d.method || '', status: '', host: d.host || '', search: (d.path || '').split('?')[0], exclude: [] };
  syncControls(); renderChips(); loadFlows();
  if(id) selectFlow(id);
});
$('#fmSeg') && $('#fmSeg').querySelectorAll('button').forEach(b => b.onclick = () => {
  state.fm.pretty = b.dataset.v === 'pretty';
  state.fm.compare = b.dataset.v === 'compare';
  $('#fmSeg').querySelectorAll('button').forEach(x => { x.classList.toggle('on', x === b); x.setAttribute('aria-pressed', x === b ? 'true' : 'false'); });
  fmRenderSide('req'); fmRenderSide('res');
});
const fmOpenDecoder=s=>import('./scanner.js').then(m=>m.openDecoder(s));
wireSelectionDecode($('#fmReq'),$('#fmReqDecode'),{onDecoder:fmOpenDecoder,getContext:()=>state.fm&&state.fm.id?{flowId:state.fm.id,side:'req'}:null});
wireSelectionDecode($('#fmRes'),$('#fmResDecode'),{onDecoder:fmOpenDecoder,getContext:()=>state.fm&&state.fm.id?{flowId:state.fm.id,side:'res'}:null});
