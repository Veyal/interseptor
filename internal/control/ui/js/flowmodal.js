import { $, esc, escAttr, state, toast, api, methodColor, statusColor, statusText, fmtSize, fmtDur, highlightHTTP, prettify, RENDER_CAP, openModal, closeModal, isBinaryMime, bodyMime, headerBlockText, copyText, flowBodyDownloadName, flowBodyDownloadHref, wireSelectionDecode } from './core.js';
import { syncControls, renderChips, loadFlows, selectFlow } from './proxy.js';
/* ---- flow inspect popup (Map graph/table, Scanner findings, …) ---- */
function fmFlowUrl(d){
  if(!d) return '';
  const port = d.port && !((d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80)) ? ':'+d.port : '';
  return (d.scheme||'http')+'://'+d.host+port+(d.path||'/');
}

export async function flowPopup(id){
  let d;
  try{ d = await api('/api/flows/'+id); }catch(e){ toast('flow: '+e.message); return; }
  state.fm = { id, detail: d, url: fmFlowUrl(d), pretty: true };
  $('#fmTitle').innerHTML = `<span style="color:${methodColor(d.method)};font-weight:700">${esc(d.method)}</span> <span style="font-family:var(--mono);color:var(--fg2)">${esc((d.scheme||'http')+'://'+d.host+d.path)}</span>`;
  $('#fmStatus').textContent = d.status ? `${d.status} ${statusText(d.status)}`+(d.durationMs ? ` · ${fmtDur(d.durationMs)}` : '') : (d.error || '');
  $('#fmStatus').style.color = statusColor(d.status);
  $('#fmSeg').querySelectorAll('button').forEach(b => { const on = b.dataset.v === 'pretty'; b.classList.toggle('on', on); b.setAttribute('aria-pressed', on ? 'true' : 'false'); });
  openModal($('#flowModal'));
  fmRenderSide('req'); fmRenderSide('res');
}

export async function fmRenderSide(side){
  const el = side === 'req' ? $('#fmReq') : $('#fmRes');
  const dec = side === 'req' ? $('#fmReqDecode') : $('#fmResDecode');
  if(dec)dec.hidden=true;
  const id = state.fm.id; // snapshot: a second flowPopup must not let this render write the wrong flow
  const d = state.fm.detail;
  const len = side === 'req' ? d.reqLen : d.resLen;
  const mime = bodyMime(d, side);
  if(isBinaryMime(mime)){
    const dl=flowBodyDownloadName(state.fm.id,side,mime), href=flowBodyDownloadHref(state.fm.id,side);
    el.innerHTML = highlightHTTP(headerBlockText(d, side))+`<div class="hint" style="padding:14px 0 0;line-height:1.7">Body is <b>${esc(mime)}</b>${len ? ' · '+fmtSize(len) : ''} — binary, not rendered.<br><a class="btn" style="margin-top:8px;display:inline-block" href="${href}" download="${escAttr(dl)}">⤓ Download body</a></div>`;
    return;
  }
  if(len > RENDER_CAP){
    const dl=flowBodyDownloadName(state.fm.id,side,mime), href=flowBodyDownloadHref(state.fm.id,side);
    el.innerHTML = `<div class="hint" style="padding:14px;line-height:1.7">${side === 'req' ? 'Request' : 'Response'} body is <b>${fmtSize(len)}</b> — not rendered.<br><a class="btn" style="margin-top:8px;display:inline-block" href="${href}" download="${escAttr(dl)}">⤓ Download body</a></div>`;
    return;
  }
  el.innerHTML = '<span class="hint" style="padding:12px">loading…</span>';
  try{
    const raw = await api('/api/flows/'+id+'/raw?side='+side);
    if(state.fm.id !== id) return; // a newer flowPopup superseded this render
    el._rawText=raw;
    el.innerHTML = highlightHTTP(state.fm.pretty ? prettify(raw) : raw, state.fm.pretty, mime);
  }catch(e){ el.textContent = '(error: '+e.message+')'; }
}

$('#fmClose') && ($('#fmClose').onclick = () => closeModal($('#flowModal')));
$('#fmCopyUrl') && ($('#fmCopyUrl').onclick = () => {
  const url = state.fm && (state.fm.url || fmFlowUrl(state.fm.detail));
  if(url) copyText(url, 'URL copied');
});
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
  $('#fmSeg').querySelectorAll('button').forEach(x => { x.classList.toggle('on', x === b); x.setAttribute('aria-pressed', x === b ? 'true' : 'false'); });
  fmRenderSide('req'); fmRenderSide('res');
});
const fmOpenDecoder=s=>import('./scanner.js').then(m=>m.openDecoder(s));
wireSelectionDecode($('#fmReq'),$('#fmReqDecode'),{onDecoder:fmOpenDecoder});
wireSelectionDecode($('#fmRes'),$('#fmResDecode'),{onDecoder:fmOpenDecoder});
