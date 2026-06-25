import { $, $$, esc, escAttr, state, toast, api, methodColor, statusColor, statusText, mimeLabel, fmtSize, fmtBytes, fmtTime, fmtDur, FLAG_WS, FLAG_AI, RENDER_CAP, highlightHTTP, prettify, copyText, uiPrompt, uiConfirm, closeModals, isBinaryMime, bodyMime, headerBlockText } from './core.js';
import { sendToRepeater, sendToIntruder } from './tools.js';
import { retentionStats, loadRetention } from './settings.js';
import { openAi } from './ai.js';
import { openAuthz } from './authz.js';

export function applySort(flows){
  const k=state.sort.key,dir=state.sort.dir;
  const val=f=>k==='size'?(f.resLen||0):k==='time'?f.ts:k==='status'?(f.status||0):k==='method'?f.method:k==='host'?f.host:k==='path'?f.path:k==='mime'?mimeLabel(f.mime):f.id;
  return flows.slice().sort((a,b)=>{const x=val(a),y=val(b);return (x>y?1:x<y?-1:0)*dir;});
}
export function getStartedCard(){
  return `<div style="max-width:640px;margin:26px auto;padding:0 16px">
    <div style="font-size:14px;font-weight:700;color:var(--fg);margin-bottom:4px">No traffic yet — let's capture some</div>
    <div class="hint" style="margin-bottom:14px">Interceptor sits between your client and the internet; point traffic at it and it shows up here live.</div>
    <ol style="color:var(--fg2);line-height:2;font-size:12.5px;padding-left:20px;margin:0">
      <li>Point your browser/client at the proxy <b style="color:var(--accent);font-family:var(--mono)">${esc(state.proxyAddr)}</b></li>
      <li>To intercept <b>HTTPS</b>, <a href="/api/ca.crt" download style="color:var(--accent)">download the CA</a> and trust it (details in Settings)</li>
      <li>Browse — flows stream in here. <b style="color:var(--fg)">Right-click</b> a row to filter, copy as cURL, send to Repeater/Intruder, or ✨ ask AI</li>
      <li>Using an AI assistant? <button id="gsMcp" class="btn accent" style="padding:2px 9px;vertical-align:middle">Connect it via MCP</button></li>
    </ol>
    <div class="hint" style="margin-top:14px">Tip: press <b style="color:var(--fg)">Ctrl/⌘ K</b> for the command palette — jump to any tab, search flows, or run an action.</div></div>`;
}
export function renderRows(){
  const box=$('#rows');
  const flows=applySort(state.flows);
  $('#rowCount').textContent=state.flows.length;
  if(!flows.length){
    if(anyFilter()||state.inScopeOnly){
      // Traffic exists but nothing matches the active filters — don't show the
      // "no traffic yet" onboarding (it reads as if capture is broken).
      box.innerHTML='<div class="empty">No flows match the current filters.<br><button class="btn" id="emptyClear" style="margin-top:12px">Clear filters</button></div>';
      const c=document.getElementById('emptyClear');if(c)c.onclick=()=>{
        if(state.inScopeOnly){state.inScopeOnly=false;const st=$('#scopeToggle');if(st){st.classList.remove('accent');st.textContent='◎ in scope';}}
        clearAllFilters();
      };
    }else{
      box.innerHTML=getStartedCard();
      const b=document.getElementById('gsMcp');if(b)b.onclick=()=>{document.querySelector('.tab[data-tab="api"]').click();document.querySelector('#apiSub button[data-s="mcp"]').click();};
    }
    return;}
  box.innerHTML=flows.map(f=>{
    const intercepted=(f.flags&1)!==0;
    const pending=!f.status&&!f.error;
    const stHTML=f.status?String(f.status):(f.error?'ERR':'<span class="blink" style="color:var(--fg3)" title="waiting for response">•••</span>');
    return `<div class="trow ${f.id===state.selId?'sel':''}${pending?' pending':''}" data-id="${f.id}" title="Left-click to inspect · Right-click to filter / copy">
      <div class="tr-id" data-field="id"><input type="checkbox" class="rowsel" data-id="${f.id}"${state.selected.has(f.id)?' checked':''} style="vertical-align:middle;margin-right:5px">${f.id}</div>
      <div class="tr-m" data-field="method" style="color:${methodColor(f.method)}">${esc(f.method)}</div>
      <div class="tr-host" data-field="host">${esc(f.scheme==='https'?'🔒 ':'')}${esc(f.host)}</div>
      <div class="tr-path" data-field="path">${esc(f.path)}${intercepted?' <span style="color:var(--accent)" title="intercepted">●</span>':''}${(f.flags&FLAG_AI)?'<span class="ai-tag" title="sent by the AI assistant">AI</span>':''}${f.note?' <span title="has a note" style="cursor:help">📝</span>':''}</div>
      <div class="tr-st" data-field="status" style="color:${statusColor(f.status)}">${stHTML}</div>
      <div class="tr-mime" data-field="mime">${esc(mimeLabel(f.mime))}</div>
      <div class="tr-len" data-field="size">${f.status?fmtSize(f.resLen):''}</div>
      <div class="tr-t" data-field="time">${fmtTime(f.ts)}</div>
    </div>`;
  }).join('');
  $$('#rows .trow').forEach(r=>{
    const id=Number(r.dataset.id);
    r.onclick=()=>selectFlow(id);
    r.oncontextmenu=e=>{
      e.preventDefault();
      const f=state.flows.find(x=>x.id===id);
      const cell=e.target.closest('[data-field]');
      showCtx(e.clientX,e.clientY,f,cell?cell.dataset.field:'');
    };
  });
  $$('#rows .rowsel').forEach(cb=>{
    cb.onclick=e=>{
      e.stopPropagation(); // toggle selection without opening the inspector
      const id=Number(cb.dataset.id),list=applySort(state.flows),idx=list.findIndex(f=>f.id===id);
      if(e.shiftKey&&state.lastSelIdx>=0&&state.lastSelIdx<list.length){
        const a=Math.min(state.lastSelIdx,idx),b=Math.max(state.lastSelIdx,idx);
        for(let i=a;i<=b;i++){cb.checked?state.selected.add(list[i].id):state.selected.delete(list[i].id);}
      }else{cb.checked?state.selected.add(id):state.selected.delete(id);}
      state.lastSelIdx=idx;renderRows();updateSelBar();
    };
  });
  const sa=$('#selAll');
  if(sa){const list=applySort(state.flows);sa.checked=list.length>0&&list.every(f=>state.selected.has(f.id));sa.indeterminate=!sa.checked&&list.some(f=>state.selected.has(f.id));}
}
export async function loadFlows(){
  const q=new URLSearchParams();
  const f=state.filters;
  if(f.scheme)q.set('scheme',f.scheme);
  if(f.search)q.set('search',f.search);
  if(f.method)q.set('method',f.method);
  if(f.status)q.set('status',f.status);
  if(f.host)q.set('host',f.host);
  (f.exclude||[]).forEach(e=>{const k={method:'notMethod',host:'notHost',path:'notPath',status:'notStatus'}[e.field];if(k)q.append(k,e.value);});
  if(state.inScopeOnly)q.set('inScope','1');
  if(!state.showAI)q.set('ai','0');
  q.set('limit','500');
  try{const d=await api('/api/flows?'+q.toString());state.flows=d.flows||[];renderRows();refreshMethodFilter();}catch(e){toast('flows: '+e.message);}
}
function refreshMethodFilter(){
  if(state.filters.method)return; // don't shrink the list while filtering by method
  const order=['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS','CONNECT','TRACE'];
  const present=[...new Set(state.flows.map(f=>f.method).filter(Boolean))]
    .sort((a,b)=>{const ia=order.indexOf(a),ib=order.indexOf(b);return (ia<0?99:ia)-(ib<0?99:ib)||a.localeCompare(b);});
  const sel=$('#fMethod');if(!sel)return;const cur=sel.value;
  sel.innerHTML='<option value="">method</option>'+present.map(m=>`<option ${m===cur?'selected':''}>${esc(m)}</option>`).join('');
}
let reloadTimer=null;
export function scheduleReload(){clearTimeout(reloadTimer);reloadTimer=setTimeout(loadFlows,150);}
export async function selectFlow(id){
  state.selId=id;renderRows();
  try{
    state.detail=await api('/api/flows/'+id);
    const d=state.detail;
    $('#noteInput').value=d.note||'';$('#noteBar').style.display='flex';
    await renderSide('req');
    if(d.flags&FLAG_WS){
      $('#resStatus').textContent='WebSocket frames';$('#resStatus').style.color='var(--accent)';
      await renderWSFrames(id);
    }else if(!d.status&&!d.error){
      // In-flight request: response not back yet. The flow.update handler
      // re-selects this flow once it lands, filling the pane in automatically.
      $('#resView').innerHTML='<span class="blink" style="color:var(--fg3)">waiting for response…</span>';
      $('#resStatus').textContent='pending';$('#resStatus').style.color='var(--fg3)';
    }else{
      await renderSide('res');
      $('#resStatus').textContent=(d.status?`${d.status} ${statusText(d.status)}`:(d.error||''))+(d.durationMs?` · ${fmtDur(d.durationMs)}`:'');
      $('#resStatus').style.color=statusColor(d.status);
    }
  }catch(e){toast('flow: '+e.message);}
}
function wsOpcode(o){return {0:'cont',1:'text',2:'bin',8:'close',9:'ping',10:'pong'}[o]||('0x'+o.toString(16));}
function wsFrameRow(dir,opcode,length,text){
  const arrow=dir==='send'?'<span style="color:var(--blue)">▲ send</span>':'<span style="color:var(--accent)">▼ recv</span>';
  return `<div style="display:flex;gap:10px;padding:3px 0;border-bottom:1px solid var(--line)">
    <span style="width:60px;flex:none">${arrow}</span>
    <span style="width:46px;flex:none;color:var(--fg3)">${wsOpcode(opcode)}</span>
    <span style="width:58px;flex:none;color:var(--fg2);text-align:right">${length} B</span>
    <span style="color:var(--fg);overflow-wrap:anywhere">${esc(text)}</span></div>`;
}
function flowWsURL(d){const s=d.scheme==='https'?'wss':'ws';const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);return `${s}://${d.host}${def?'':':'+d.port}${d.path||'/'}`;}
export async function renderWSFrames(id){
  try{
    const d=await api('/api/flows/'+id+'/ws');const frames=d.frames||[];
    const url=flowWsURL(state.detail||{});
    const box=`<div style="display:flex;gap:6px;margin-bottom:10px">
        <input id="wsMsg" placeholder="Replay a frame to ${escAttr(url)}" style="flex:1;font-family:var(--mono)">
        <button class="btn accent" id="wsSendBtn">▲ Send</button></div>
      <div id="wsReplayOut" style="margin-bottom:10px"></div>`;
    const list=frames.length?frames.map(f=>wsFrameRow(f.dir,f.opcode,f.length,f.preview)).join('')
      :'<span style="color:var(--fg3)">No frames captured yet — frames stream in live as the socket exchanges messages.</span>';
    $('#resView').innerHTML=box+list;
    const sb=document.getElementById('wsSendBtn');if(sb)sb.onclick=()=>wsReplay(url);
    const inp=document.getElementById('wsMsg');if(inp)inp.onkeydown=e=>{if(e.key==='Enter')wsReplay(url);};
  }catch(e){$('#resView').textContent='(error: '+e.message+')';}
}
async function wsReplay(url){
  const msg=($('#wsMsg')||{}).value||'';
  const out=$('#wsReplayOut');if(out)out.innerHTML='<span style="color:var(--fg3)">opening socket…</span>';
  try{
    const r=await api('/api/ws/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({url,message:msg})});
    const head=`<div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:4px 0 4px">REPLAY · HTTP ${r.status} · ${(r.frames||[]).length} frame(s)</div>`;
    if(out)out.innerHTML=head+(r.frames||[]).map(f=>wsFrameRow(f.dir,f.opcode,f.len,f.text)).join('');
  }catch(e){if(out)out.innerHTML='<span style="color:var(--red)">'+esc(e.message)+'</span>';}
}
export async function renderSide(side){
  const el=side==='req'?$('#reqView'):$('#resView');
  if(!state.selId){return;}
  const draw=async()=>{
    try{const raw=await api('/api/flows/'+state.selId+'/raw?side='+side);
      el.innerHTML=highlightHTTP(state.view[side]==='pretty'?prettify(raw):raw,state.view[side]==='pretty');
    }catch(e){el.textContent='(error: '+e.message+')';}
  };
  const len=state.detail?(side==='req'?state.detail.reqLen:state.detail.resLen):0;
  // Binary body (image/font/media/archive/…): show only the headers — the bytes
  // aren't readable as text. Built from the detail DTO, so the body isn't fetched.
  const mime=bodyMime(state.detail,side);
  if(isBinaryMime(mime)){
    el.innerHTML=highlightHTTP(headerBlockText(state.detail,side))+
      `<div class="hint" style="padding:14px 0 0;line-height:1.7">Body is <b>${esc(mime)}</b>${len?' · '+fmtSize(len):''} — binary, not rendered.<br>
        <a class="btn" style="margin-top:8px;display:inline-block" href="/api/flows/${state.selId}/raw?side=${side}" download="flow-${state.selId}-${side}">⤓ Download body</a>
        <button class="btn" data-bin="1" style="margin-top:8px;margin-left:6px">Show raw anyway</button></div>`;
    const b=el.querySelector('[data-bin]');
    if(b)b.onclick=()=>{el.innerHTML='<span class="hint" style="padding:16px">rendering…</span>';setTimeout(draw,10);};
    return;
  }
  if(len>RENDER_CAP){
    el.innerHTML=`<div class="hint" style="padding:18px;line-height:1.8">${side==='req'?'Request':'Response'} body is <b>${fmtSize(len)}</b> — not shown, to keep the browser responsive.<br>
      <a class="btn" style="margin-top:8px;display:inline-block" href="/api/flows/${state.selId}/raw?side=${side}" download="flow-${state.selId}-${side}.txt">⤓ Download raw</a>
      <button class="btn" data-bigshow="1" style="margin-top:8px">Show anyway</button></div>`;
    const b=el.querySelector('[data-bigshow]');
    if(b)b.onclick=()=>{el.innerHTML='<span class="hint" style="padding:16px">rendering…</span>';setTimeout(draw,10);};
    return;
  }
  await draw();
}
// Only the inspector's request/response view segs (data-side) — NOT every .seg on the
// page. Other tabs (Intruder, Repeater, AI, Map) own their own seg handlers; a bare
// $$('.seg') here would clobber them since this module loads after them.
$$('.seg[data-side]').forEach(seg=>{const side=seg.dataset.side;seg.querySelectorAll('button').forEach(b=>b.onclick=()=>{
  state.view[side]=b.dataset.view;seg.querySelectorAll('button').forEach(x=>x.classList.toggle('on',x===b));renderSide(side);});});

$$('.thead [data-sort]').forEach(h=>h.onclick=()=>{
  const k=h.dataset.sort;if(state.sort.key===k)state.sort.dir*=-1;else{state.sort.key=k;state.sort.dir=k==='id'||k==='time'?-1:1;}renderRows();});

$('#fScheme').onchange=e=>setFilter('scheme',e.target.value);
$('#fMethod').onchange=e=>setFilter('method',e.target.value);
$('#fStatus').onchange=e=>setFilter('status',e.target.value);
$('#fSearch').oninput=e=>{state.filters.search=e.target.value;renderChips();scheduleReload();};
$('#refreshBtn').onclick=loadFlows;
// Inspector header actions — operate on the currently-selected flow.
function inspectorFlow(){return state.detail||state.flows.find(x=>x.id===state.selId)||null;}
{const b=$('#insRepeater');if(b)b.onclick=()=>{const f=inspectorFlow();if(f)sendToRepeater(f);else toast('select a flow first');};}
{const b=$('#insIntruder');if(b)b.onclick=()=>{const f=inspectorFlow();if(f)sendToIntruder(f);else toast('select a flow first');};}
{const b=$('#insCurl');if(b)b.onclick=()=>{const f=inspectorFlow();if(f)copyCurl(f);else toast('select a flow first');};}
$('#exportHar').onclick=()=>{$('#exportHar').href='/api/export/har'+(state.inScopeOnly?'?inScope=1':'');toast('History exported — .har downloaded');};
$('#importHarBtn').onclick=()=>$('#importHarFile').click();
$('#importHarFile').onchange=async e=>{
  const f=e.target.files[0];if(!f)return;
  try{const text=await f.text();const r=await api('/api/import/har',{method:'POST',headers:{'content-type':'application/json'},body:text});
    toast('imported '+r.imported+' flows');loadFlows();}catch(err){toast('import: '+err.message);}
  e.target.value='';
};
$('#scopeToggle').onclick=()=>{state.inScopeOnly=!state.inScopeOnly;$('#scopeToggle').classList.toggle('accent',state.inScopeOnly);$('#scopeToggle').textContent=(state.inScopeOnly?'◉':'◎')+' in scope';loadFlows();};
$('#aiToggle').onclick=()=>{state.showAI=!state.showAI;$('#aiToggle').classList.toggle('accent',state.showAI);loadFlows();};
export async function saveNote(){
  if(!state.selId)return;
  const note=$('#noteInput').value;
  if(state.detail&&note===(state.detail.note||''))return; // unchanged — skip redundant PUT
  try{
    await api('/api/flows/'+state.selId+'/note',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({note})});
    if(state.detail)state.detail.note=note;
    const s=$('#noteSaved');s.style.opacity='1';setTimeout(()=>{s.style.opacity='0';},1200);
  }catch(e){toast('note: '+e.message);}
}
$('#noteInput').addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#noteInput').blur();}});
$('#noteInput').addEventListener('blur',saveNote);
/* ---- saved views ---- */
export async function loadViews(){try{const d=await api('/api/views');state.views=d.views||[];renderViews();}catch(e){}}
export function renderViews(){
  const sel=$('#viewsSelect'),cur=sel.value;
  sel.innerHTML='<option value="">views…</option>'+state.views.map(v=>`<option value="${v.id}">${esc(v.name)}</option>`).join('');
  if(state.views.find(v=>String(v.id)===cur))sel.value=cur;
  sel.style.display=state.views.length?'':'none';                       // hide the picker until a view is saved
  $('#delViewBtn').style.display=(state.views.length&&sel.value)?'inline-block':'none';
}
$('#viewsSelect').onchange=()=>{
  const id=$('#viewsSelect').value;$('#delViewBtn').style.display=id?'inline-block':'none';
  if(!id)return;const v=state.views.find(x=>String(x.id)===id);if(!v)return;
  let f={};try{f=JSON.parse(v.data||'{}');}catch(e){}
  state.filters={scheme:f.scheme||'',method:f.method||'',status:f.status||'',search:f.search||'',host:f.host||'',exclude:Array.isArray(f.exclude)?f.exclude:[]};
  state.inScopeOnly=!!f.inScope;
  syncControls();$('#scopeToggle').classList.toggle('accent',state.inScopeOnly);$('#scopeToggle').textContent=(state.inScopeOnly?'◉':'◎')+' in scope';
  renderChips();loadFlows();
};
$('#saveViewBtn').onclick=async()=>{
  const name=await uiPrompt({title:'Save current filters as a view',placeholder:'view name'});if(!name)return;
  const data={...state.filters,inScope:state.inScopeOnly};
  try{await api('/api/views',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({name,data})});toast('view saved');loadViews();}catch(e){toast(e.message);}
};
$('#delViewBtn').onclick=async()=>{const id=$('#viewsSelect').value;if(!id)return;
  try{await api('/api/views/'+id,{method:'DELETE'});$('#viewsSelect').value='';$('#delViewBtn').style.display='none';loadViews();toast('view deleted');}catch(e){toast(e.message);}};
/* ---- target scope ---- */
export async function loadScope(){try{const d=await api('/api/scope');state.scope=d.rules||[];renderScope();}catch(e){}}
export async function addHostToScope(host){
  try{await api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({action:'include',host:host,enabled:true})});
    toast('added '+host+' to scope — toggle ◎ in scope to focus');loadScope();}
  catch(e){toast(e.message);}
}
export function renderScope(){
  const body=$('#scopeBody');if(!body)return;
  if(!state.scope.length){body.innerHTML='<tr><td colspan="6" class="hint" style="padding:10px 8px">No scope rules — everything is in scope.</td></tr>';return;}
  body.innerHTML=state.scope.map(r=>`<tr data-id="${r.id}">
    <td><input type="checkbox" ${r.enabled?'checked':''} data-k="enabled"></td>
    <td><select data-k="action"><option value="include" ${r.action==='include'?'selected':''}>include</option><option value="exclude" ${r.action==='exclude'?'selected':''}>exclude</option></select></td>
    <td><input type="text" data-k="host" value="${escAttr(r.host)}" placeholder="*.acme.com"></td>
    <td><input type="text" data-k="path" value="${escAttr(r.path)}" placeholder="/"></td>
    <td><input type="text" data-k="scheme" value="${escAttr(r.scheme)}" placeholder="any"></td>
    <td><button class="btn danger" data-del="${r.id}">Delete</button></td></tr>`).join('');
  body.querySelectorAll('tr').forEach(tr=>{const id=Number(tr.dataset.id);
    tr.querySelectorAll('[data-k]').forEach(inp=>inp.addEventListener('change',()=>updateScope(id,tr)));});
  body.querySelectorAll('[data-del]').forEach(b=>b.onclick=()=>deleteScope(Number(b.dataset.del)));
}
async function updateScope(id,tr){
  const get=k=>tr.querySelector(`[data-k="${k}"]`);
  const upd={id,action:get('action').value,host:get('host').value.trim(),path:get('path').value.trim(),scheme:get('scheme').value.trim(),enabled:get('enabled').checked,port:0};
  try{await api('/api/scope/'+id,{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(upd)});toast('scope saved');}catch(e){toast(e.message);loadScope();}
}
async function deleteScope(id){try{await api('/api/scope/'+id,{method:'DELETE'});loadScope();}catch(e){toast(e.message);}}
$('#addScopeBtn').onclick=async()=>{
  const rule={action:$('#newScopeAction').value,host:$('#newScopeHost').value.trim(),path:$('#newScopePath').value.trim(),scheme:'',enabled:true,port:0};
  if(!rule.host&&!rule.path){toast('host or path required');return;}
  try{await api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(rule)});
    $('#newScopeHost').value='';$('#newScopePath').value='';loadScope();toast('scope rule added');}catch(e){toast(e.message);}
};
/* ---- filters: chips + apply/clear, kept in sync with the toolbar controls ---- */
export function syncControls(){
  $('#fScheme').value=state.filters.scheme;
  $('#fMethod').value=state.filters.method;
  $('#fStatus').value=state.filters.status;
  $('#fSearch').value=state.filters.search;
}
export function setFilter(key,val){state.filters[key]=val;syncControls();renderChips();loadFlows();}
export function clearFilter(key){setFilter(key,'');}
export function clearAllFilters(){state.filters={scheme:'',search:'',method:'',status:'',host:'',exclude:[]};syncControls();renderChips();loadFlows();}
export function anyFilter(){const f=state.filters;return !!(f.scheme||f.method||f.status||f.host||f.search||(f.exclude&&f.exclude.length));}
// Negative filters: exclude rows matching {field,value}. Toggles off if already present.
export function addExclude(field,value){
  if(value==null||value==='')return;
  const ex=state.filters.exclude||(state.filters.exclude=[]);
  const i=ex.findIndex(e=>e.field===field&&String(e.value)===String(value));
  if(i>=0)ex.splice(i,1); else ex.push({field,value:String(value)});
  renderChips();loadFlows();
}
export function removeExclude(i){state.filters.exclude.splice(i,1);renderChips();loadFlows();}
export function renderChips(){
  const f=state.filters,box=$('#chips'),items=[];
  const add=(k,label,val)=>{if(val)items.push(`<span class="chip"><span>${label} <b>${esc(val)}</b></span><span class="x" data-clear="${k}" title="remove">✕</span></span>`);};
  add('scheme','scheme',f.scheme);
  add('method','method',f.method);
  add('status','status',f.status?f.status+'xx':'');
  add('host','host',f.host);
  add('search','path',f.search);
  (f.exclude||[]).forEach((e,i)=>{items.push(`<span class="chip not"><span>${esc(e.field)} ≠ <b>${esc(e.value)}</b></span><span class="x" data-ex="${i}" title="remove">✕</span></span>`);});
  const hasFilters=items.length>0;
  if(hasFilters)items.push(`<button class="chip-clear" id="chipsClear" title="Remove all filters">Clear all ✕</button>`);
  box.innerHTML=items.join('');
  box.classList.toggle('has',hasFilters);
  box.querySelectorAll('[data-clear]').forEach(x=>x.onclick=()=>clearFilter(x.dataset.clear));
  box.querySelectorAll('[data-ex]').forEach(x=>x.onclick=()=>removeExclude(Number(x.dataset.ex)));
  const cc=$('#chipsClear');if(cc)cc.onclick=clearAllFilters;
  // The "save current filters as a view" (＋) only makes sense when something is filtered.
  const sv=$('#saveViewBtn');if(sv)sv.style.display=hasFilters?'':'none';
}
/* ---- right-click context menu ---- */
export const ctx=$('#ctxmenu');
function hideCtx(){ctx.classList.remove('show');ctx._acts=null;}
export function showCtx(x,y,f,field){
  if(!f)return;
  const cls=f.status?Math.floor(f.status/100):0;
  const filters=[
    {field:'host',label:'Filter host',val:f.host,act:()=>setFilter('host',f.host)},
    {field:'method',label:'Filter method',val:f.method,act:()=>setFilter('method',f.method)},
  ];
  if(cls)filters.push({field:'status',label:'Filter status',val:cls+'xx',act:()=>setFilter('status',String(cls))});
  filters.push({field:'scheme',label:'Filter scheme',val:f.scheme,act:()=>setFilter('scheme',f.scheme)});
  if(field==='path')filters.unshift({field:'path',label:'Filter path',val:f.path,act:()=>setFilter('search',f.path)});
  // Put the right-clicked column's filter first and highlight it.
  filters.sort((a,b)=>(a.field===field?-1:0)-(b.field===field?-1:0));

  // Negative counterparts: hide everything matching the clicked value.
  const excludes=[
    {field:'host',label:'Exclude host',val:f.host,act:()=>addExclude('host',f.host)},
    {field:'method',label:'Exclude method',val:f.method,act:()=>addExclude('method',f.method)},
  ];
  if(cls)excludes.push({field:'status',label:'Exclude status',val:String(f.status),act:()=>addExclude('status',String(f.status))});
  if(field==='path')excludes.unshift({field:'path',label:'Exclude path',val:f.path,act:()=>addExclude('path',f.path)});
  excludes.sort((a,b)=>(a.field===field?-1:0)-(b.field===field?-1:0));

  const acts=[];
  let html='<div class="ctx-head">FILTER</div>';
  filters.forEach(it=>{html+=`<div class="ctx-item ${it.field===field?'on':''}" data-i="${acts.length}"><span class="lbl">${it.label}</span><span class="mono">${esc(it.val)}</span></div>`;acts.push(it.act);});
  html+='<div class="ctx-head">EXCLUDE</div>';
  excludes.forEach(it=>{html+=`<div class="ctx-item" data-i="${acts.length}"><span class="lbl">${it.label}</span><span class="mono" style="color:var(--red)">≠ ${esc(it.val)}</span></div>`;acts.push(it.act);});
  html+='<div class="ctx-sep"></div>';
  html+=`<div class="ctx-item" data-i="${acts.length}">Copy URL</div>`;acts.push(()=>copyURL(f));
  html+=`<div class="ctx-item" data-i="${acts.length}">Copy as cURL</div>`;acts.push(()=>copyCurl(f));
  html+='<div class="ctx-sep"></div>';
  html+=`<div class="ctx-item" data-i="${acts.length}"><span class="lbl">Add to scope</span><span class="mono">${esc(f.host)}</span></div>`;acts.push(()=>addHostToScope(f.host));
  html+='<div class="ctx-sep"></div>';
  html+=`<div class="ctx-item" data-i="${acts.length}"><span class="lbl">Send to</span><span class="mono">Repeater</span></div>`;acts.push(()=>sendToRepeater(f));
  html+=`<div class="ctx-item" data-i="${acts.length}"><span class="lbl">Send to</span><span class="mono">Intruder</span></div>`;acts.push(()=>sendToIntruder(f));
  html+='<div class="ctx-sep"></div>';
  html+=`<div class="ctx-item" data-i="${acts.length}"><span class="lbl">✨ Ask AI</span><span class="mono">explain</span></div>`;acts.push(()=>openAi('explain',[f.id]));
  html+=`<div class="ctx-item" data-i="${acts.length}"><span class="lbl">✨ Ask AI</span><span class="mono">payloads</span></div>`;acts.push(()=>openAi('suggest',[f.id]));
  html+=`<div class="ctx-item" data-i="${acts.length}"><span class="lbl">🔓 Authz test</span><span class="mono">roles</span></div>`;acts.push(()=>openAuthz(f.id));
  html+='<div class="ctx-sep"></div>';
  html+=`<div class="ctx-item" data-i="${acts.length}" style="color:var(--red)">🗑 <span class="lbl" style="color:var(--red)">Delete all from</span><span class="mono" style="color:var(--red)">${esc(f.host)}</span></div>`;
  acts.push(async()=>{
    const hstats=retentionStats&&retentionStats.hosts&&retentionStats.hosts.find(x=>x.host===f.host);
    const flowCount=hstats?hstats.flows:'all';
    const confirmed=await uiConfirm('Delete flows from '+esc(f.host),
      'Permanently delete '+flowCount+' flow'+(flowCount===1?'':'s')+' from <b style="color:var(--accent)">'+esc(f.host)+'</b>?<br>This cannot be undone.',
      'Delete','btn danger','var(--red)');
    if(!confirmed)return;
    try{
      const r=await api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts:[f.host],mode:'delete'})});
      toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
      loadRetention();loadFlows();
    }catch(e){toast('purge: '+e.message);}
  });
  if(anyFilter()){html+='<div class="ctx-sep"></div>';html+=`<div class="ctx-item" data-i="${acts.length}">Clear all filters</div>`;acts.push(clearAllFilters);}

  ctx.innerHTML=html;ctx._acts=acts;
  ctx.querySelectorAll('[data-i]').forEach(el=>el.onclick=()=>{const fn=ctx._acts[Number(el.dataset.i)];hideCtx();if(fn)fn();});
  ctx.style.left=x+'px';ctx.style.top=y+'px';ctx.classList.add('show');
  const r=ctx.getBoundingClientRect();
  if(r.right>innerWidth)ctx.style.left=Math.max(4,x-r.width)+'px';
  if(r.bottom>innerHeight)ctx.style.top=Math.max(4,y-r.height)+'px';
}
document.addEventListener('click',e=>{if(!ctx.contains(e.target))hideCtx();});
document.addEventListener('keydown',e=>{if(e.key==='Escape'){if(typeof closeModals==='function'&&closeModals())return;hideCtx();}});
// Suppress the browser's native context menu app-wide, but keep it where it's
// genuinely useful: editable fields (paste/cut) and over a live text selection (copy).
document.addEventListener('contextmenu',e=>{
  const t=e.target,tag=(t.tagName||'').toLowerCase();
  if(tag==='input'||tag==='textarea'||t.isContentEditable)return;
  const sel=window.getSelection&&window.getSelection();
  if(sel&&String(sel).length&&!sel.isCollapsed)return;
  e.preventDefault();
});
$('#rows').addEventListener('scroll',hideCtx,{passive:true});
window.addEventListener('blur',hideCtx);
export function flowURL(f){const def=(f.scheme==='https'&&f.port===443)||(f.scheme==='http'&&f.port===80);return `${f.scheme}://${f.host}${def?'':':'+f.port}${f.path}`;}
export function copyURL(f){copyText(flowURL(f),'URL copied');}
function shq(s){return "'"+String(s).replace(/'/g,"'\\''")+"'";}
export async function copyCurl(f){
  try{
    const d=await api('/api/flows/'+f.id);
    const parts=[`curl -x http://${state.proxyAddr}`];
    if(f.scheme==='https')parts.push('--cacert interceptor-ca.crt');
    parts.push('-X '+f.method);
    const headers=d.reqHeaders||{};
    Object.keys(headers).sort().forEach(k=>{if(k.toLowerCase()==='host')return;(headers[k]||[]).forEach(v=>parts.push('-H '+shq(k+': '+v)));});
    if(f.reqLen>0){const raw=await api('/api/flows/'+f.id+'/raw?side=req');const i=raw.indexOf('\r\n\r\n');const body=i>=0?raw.slice(i+4):'';if(body)parts.push('--data-raw '+shq(body));}
    parts.push(shq(flowURL(f)));
    copyText(parts.join(' \\\n  '),'cURL copied');
  }catch(e){toast('cURL: '+e.message);}
}
// ---- History multi-select actions ----
export function updateSelBar(){const n=state.selected.size;$('#selBar').style.display=n?'flex':'none';$('#selCount').textContent=n+' selected';}
$('#selAll').onclick=e=>{e.stopPropagation();const list=applySort(state.flows);if(e.target.checked)list.forEach(f=>state.selected.add(f.id));else state.selected.clear();renderRows();updateSelBar();};
$('#selClear').onclick=()=>{state.selected.clear();state.lastSelIdx=-1;renderRows();updateSelBar();};
$('#selAsk').onclick=()=>{const ids=[...state.selected];if(ids.length)openAi('summarize',ids);};
$('#selScope').onclick=async()=>{
  const hosts=[...new Set([...state.selected].map(id=>{const f=state.flows.find(x=>x.id===id);return f&&f.host;}).filter(Boolean))];
  if(!hosts.length)return;
  let added=0;
  for(const host of hosts){try{await api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({action:'include',host,enabled:true})});added++;}catch(e){}}
  toast('added '+added+' host'+(added===1?'':'s')+' to scope');loadScope();
};
export let _delArm=false,_delTimer;
$('#selDelete').onclick=async()=>{
  const ids=[...state.selected];if(!ids.length)return;
  if(!_delArm){_delArm=true;$('#selDelete').textContent='🗑 Confirm? ('+ids.length+')';clearTimeout(_delTimer);_delTimer=setTimeout(()=>{_delArm=false;$('#selDelete').textContent='🗑 Delete';},2500);return;}
  clearTimeout(_delTimer);_delArm=false;$('#selDelete').textContent='🗑 Delete';
  try{
    const r=await api('/api/flows/delete',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({ids})});
    if(state.selected.has(state.selId))state.selId=null;
    state.selected.clear();state.lastSelIdx=-1;updateSelBar();loadFlows();
    toast('deleted '+(r.deleted!=null?r.deleted:ids.length)+' flow'+((r.deleted!=null?r.deleted:ids.length)===1?'':'s'));
  }catch(e){toast('delete: '+e.message);}
};
/* ---- inspector splitter ---- */
(function(){
  const SPLITTER_KEY='inspect.height';
  const MIN_H=120, MAX_PCT=0.80;
  const splitter=document.getElementById('inspectSplitter');
  const inspect=document.getElementById('inspect');
  if(!splitter||!inspect)return;

  function clamp(h){
    const proxyPanel=inspect.closest('.panel');
    const maxH=proxyPanel?(proxyPanel.clientHeight*MAX_PCT):600;
    return Math.max(MIN_H,Math.min(maxH,h));
  }
  function applyHeight(h){
    h=clamp(h);
    inspect.style.height=h+'px';
    inspect.style.flex='none';
    try{localStorage.setItem(SPLITTER_KEY,String(h));}catch(e){}
  }

  // Restore persisted height on load.
  try{const saved=localStorage.getItem(SPLITTER_KEY);if(saved){const h=parseInt(saved,10);if(h>=MIN_H)applyHeight(h);}}catch(e){}

  // Pointer drag.
  let dragY=null,dragH=null;
  splitter.addEventListener('pointerdown',e=>{
    e.preventDefault();
    dragY=e.clientY;
    dragH=inspect.offsetHeight;
    splitter.setPointerCapture(e.pointerId);
  });
  splitter.addEventListener('pointermove',e=>{
    if(dragY===null)return;
    // Dragging up (negative delta) increases inspector height.
    applyHeight(dragH-(e.clientY-dragY));
  });
  splitter.addEventListener('pointerup',()=>{dragY=null;dragH=null;});
  splitter.addEventListener('pointercancel',()=>{dragY=null;dragH=null;});

  // Keyboard: Up/Down arrows nudge by 20px.
  splitter.addEventListener('keydown',e=>{
    if(e.key!=='ArrowUp'&&e.key!=='ArrowDown')return;
    e.preventDefault();
    const delta=e.key==='ArrowUp'?20:-20;
    applyHeight(inspect.offsetHeight+delta);
  });
})();
