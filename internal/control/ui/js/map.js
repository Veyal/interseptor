import { $, esc, escAttr, state, toast, api, methodColor, statusColor, statusText, fmtSize, fmtDur, highlightHTTP, prettify, RENDER_CAP, openModal, closeModal, isBinaryMime, bodyMime, headerBlockText } from './core.js';
import { syncControls, renderChips, loadFlows, selectFlow } from './proxy.js';

/* ---- endpoint map ---- */
export const mapState={eps:[],domain:null,method:'',search:'',statusClass:0,expandAll:false,view:'graph',collapsed:new Set(),zoom:{k:1,x:12,y:12},_needFit:true};
export async function loadEndpoints(){
  try{const d=await api('/api/endpoints');mapState.eps=d.endpoints||[];mapState._needFit=true;fillMapDomains();fillMapMethods();renderMap();}
  catch(e){toast('map: '+e.message);}
}
// Populate the domain picker and default to the busiest domain — showing one app
// at a time keeps the graph readable instead of cramming every host on screen.
export function fillMapDomains(){
  const sel=$('#mapDomain');if(!sel)return;
  const counts={};mapState.eps.forEach(e=>{counts[e.host]=(counts[e.host]||0)+1;});
  const hosts=Object.keys(counts).sort((a,b)=>counts[b]-counts[a]||a.localeCompare(b));
  if(mapState.domain===null)mapState.domain=hosts[0]||'';            // first open: biggest domain
  else if(mapState.domain&&!counts[mapState.domain])mapState.domain=hosts[0]||''; // host gone → fall back
  sel.innerHTML=`<option value="">All domains (${mapState.eps.length})</option>`+hosts.map(h=>`<option value="${escAttr(h)}">${esc(h)} (${counts[h]})</option>`).join('');
  sel.value=mapState.domain;
}
// Collapse every host node — used for the "All domains" overview so you see the
// hosts (each with an endpoint count) and drill in by click, not 500 crammed rows.
export function mapCollapseHosts(){
  mapState.collapsed.clear();
  [...new Set(mapState.eps.map(e=>e.host))].forEach(h=>mapState.collapsed.add('/'+h));
}
export function fillMapMethods(){
  const sel=$('#mapMethod');if(!sel)return;
  const methods=[...new Set(mapState.eps.map(e=>e.method))].sort();const cur=sel.value;
  sel.innerHTML='<option value="">method</option>'+methods.map(m=>`<option value="${escAttr(m)}">${esc(m)}</option>`).join('');
  if(methods.includes(cur))sel.value=cur;
}
export function mapFiltered(){
  const q=mapState.search.toLowerCase();
  return mapState.eps.filter(e=>{
    if(mapState.domain&&e.host!==mapState.domain)return false;
    if(mapState.method&&e.method!==mapState.method)return false;
    if(mapState.statusClass&&Math.floor((e.lastStatus||0)/100)!==mapState.statusClass)return false;
    if(q&&!((e.path||'').toLowerCase().includes(q)||(e.host||'').toLowerCase().includes(q)))return false;
    return true;
  });
}
export function mapCount(node){let n=node.eps.length;node.kids.forEach(k=>n+=mapCount(k));return n;}
export function buildMapTree(eps){
  const hosts=new Map();
  eps.forEach(e=>{
    if(!hosts.has(e.host))hosts.set(e.host,{name:e.host,kids:new Map(),eps:[]});
    let node=hosts.get(e.host);
    (e.path||'/').split('?')[0].split('/').filter(Boolean).forEach(seg=>{
      if(!node.kids.has(seg))node.kids.set(seg,{name:seg,kids:new Map(),eps:[]});node=node.kids.get(seg);});
    node.eps.push(e);
  });
  return hosts;
}
export function mapEpRow(e){
  const sts=(e.statuses||[]).map(s=>`<span style="color:${statusColor(s)}">${s}</span>`).join(' ');
  const path=e.path||'/';
  return `<div class="map-ep"${e.lastFlowId?` data-flow="${e.lastFlowId}"`:''} title="${escAttr(e.method+' '+(e.scheme||'http')+'://'+e.host+path)}">
    <span class="map-m" style="color:${methodColor(e.method)}">${esc(e.method)}</span>
    <span class="map-p">${esc(path)}</span><span class="map-sts">${sts}</span>
    <span class="map-hits">${e.hits>1?e.hits+'×':''}</span></div>`;
}
export function mapRenderNode(node,open){
  let html='';
  [...node.kids.values()].sort((a,b)=>a.name.localeCompare(b.name)).forEach(kid=>{
    html+=`<details class="map-folder"${open?' open':''}><summary><span class="map-seg">/${esc(kid.name)}</span><span class="map-c">${mapCount(kid)}</span></summary><div class="map-body">${mapRenderNode(kid,open)}</div></details>`;
  });
  node.eps.slice().sort((a,b)=>a.method.localeCompare(b.method)).forEach(e=>html+=mapEpRow(e));
  return html;
}
export function renderMap(){
  const eps=mapFiltered();
  const hostN=new Set(eps.map(e=>e.host)).size;
  // Always show a count — a blank label after a search leaves the user unsure whether
  // anything matched. Distinguish "filtered to nothing" from "no traffic captured yet".
  const filtered=!!(mapState.search||mapState.method||mapState.statusClass||mapState.domain);
  $('#mapCount').textContent=eps.length
    ?`${eps.length} endpoint${eps.length===1?'':'s'} · ${hostN} host${hostN===1?'':'s'}`
    :(mapState.eps.length?(filtered?'No endpoints match the filters':'No endpoints'):'No endpoints captured yet');
  if(mapState.view==='graph')renderMapGraph(eps); else renderMapTree(eps);
}
export function renderMapTree(eps){
  const box=$('#mapTree');if(!box)return;
  if(!eps.length){box.innerHTML='<div class="hint" style="padding:12px">No endpoints match — capture traffic or relax the filters.</div>';return;}
  const open=mapState.expandAll||!!mapState.search; // a search auto-expands so matches are visible
  const hosts=buildMapTree(eps);
  box.innerHTML=[...hosts.values()].sort((a,b)=>a.name.localeCompare(b.name)).map(h=>
    `<details class="map-host" open><summary>🌐 ${esc(h.name)}<span class="map-c">${mapCount(h)}</span></summary><div class="map-body">${mapRenderNode(h,open)}</div></details>`).join('');
  box.querySelectorAll('.map-ep[data-flow]').forEach(el=>el.onclick=()=>flowPopup(Number(el.dataset.flow)));
}
// Any filter change re-fits the graph so the (possibly smaller/larger) result set is
// always fully visible — the user shouldn't have to manually pan/zoom after a search.
$('#mapSearch').oninput=e=>{mapState.search=e.target.value.trim();mapState._needFit=true;renderMap();};
$('#mapDomain')&&($('#mapDomain').onchange=e=>{mapState.domain=e.target.value;
  // A specific domain shows its tree expanded; "All domains" collapses every host.
  if(mapState.domain)mapState.collapsed.clear();else mapCollapseHosts();
  mapState._needFit=true;renderMap();});
$('#mapMethod').onchange=e=>{mapState.method=e.target.value;mapState._needFit=true;renderMap();};
$('#mapRefresh').onclick=loadEndpoints;
$('#mapExpand').onclick=()=>{mapState.expandAll=!mapState.expandAll;$('#mapExpand').textContent=mapState.expandAll?'Collapse all':'Expand all';mapState._needFit=true;renderMap();};
$('#mapStatus')&&($('#mapStatus').onchange=e=>{mapState.statusClass=Number(e.target.value)||0;mapState._needFit=true;renderMap();});

/* ---- map: node-link graph (hierarchical tidy tree) ---- */
export function gTrunc(s,n){return s.length>n?s.slice(0,n-1)+'…':s;}
export function gCount(n){if(n.type==='ep')return 1;let c=0;n.children.forEach(k=>c+=gCount(k));return c;}
export function buildGraphTree(eps){
  const root={key:'',type:'root',children:[],cm:new Map()};
  const child=(p,k,label,type)=>{let c=p.cm.get(k);if(!c){c={key:p.key+'/'+k,label,type,children:[],cm:new Map(),ep:null};p.cm.set(k,c);p.children.push(c);}return c;};
  eps.forEach(e=>{const host=child(root,e.host,e.host,'host');let node=host;
    (e.path||'/').split('?')[0].split('/').filter(Boolean).forEach(seg=>{node=child(node,seg,'/'+seg,'folder');});
    child(node,'ep|'+e.method,e.method,'ep').ep=e;});
  return root;
}
export function graphLayout(hosts){
  const COL=160,ROW=22,PAD=20;let leaf=0,maxD=0;
  function place(n,d){n.depth=d;maxD=Math.max(maxD,d);n._col=mapState.collapsed.has(n.key)&&n.children.length>0;
    if(n._col||!n.children.length){n.row=leaf++;return;}
    n.children.forEach(c=>place(c,d+1));n.row=(n.children[0].row+n.children[n.children.length-1].row)/2;}
  hosts.forEach(h=>place(h,0));
  const nodes=[],edges=[];
  function collect(n){n.px=PAD+n.depth*COL;n.py=PAD+n.row*ROW;nodes.push(n);
    if(!n._col)n.children.forEach(c=>{edges.push([n,c]);collect(c);});}
  hosts.forEach(collect);
  return {nodes,edges,w:PAD*2+maxD*COL+180,h:PAD*2+Math.max(1,leaf)*ROW};
}
export function gNode(n){
  const x=n.px,y=n.py;let mk,lb,title=esc(n.label||''),extra='';
  if(n.type==='host'){mk=`<circle cx="${x}" cy="${y}" r="5" fill="var(--accent)"/>`;lb=`<text class="g-host" x="${x+9}" y="${y}">${esc(gTrunc(n.label,26))}${n._col?` <tspan class="g-dim">+${gCount(n)}</tspan>`:''}</text>`;}
  else if(n.type==='ep'){const e=n.ep,col=statusColor(e.lastStatus);title=esc(e.method+' '+(e.scheme||'http')+'://'+e.host+(e.path||'/'));
    mk=`<rect x="${x-4}" y="${y-4}" width="8" height="8" rx="2" fill="${col}"/>`;
    lb=`<text class="g-ep" x="${x+9}" y="${y}"><tspan fill="${methodColor(e.method)}" font-weight="700">${esc(e.method)}</tspan> <tspan class="g-dim">${esc((e.statuses||[]).join(','))}${e.hits>1?' · '+e.hits+'×':''}</tspan></text>`;
    extra=` data-flow="${e.lastFlowId||''}"`;}
  else{mk=`<circle cx="${x}" cy="${y}" r="4" fill="${n._col?'var(--blue)':'var(--bg3)'}" stroke="var(--blue)" stroke-width="1.4"/>`;
    lb=`<text class="g-folder" x="${x+9}" y="${y}">${esc(gTrunc(n.label,22))}${n._col?` <tspan class="g-dim">+${gCount(n)}</tspan>`:''}</text>`;}
  const click=n.type==='ep'||n.children.length>0;
  return `<g class="g-node${click?' g-click':''}" data-key="${escAttr(n.key)}" data-kind="${n.type}"${extra}><title>${title}</title>${mk}${lb}</g>`;
}
export function renderMapGraph(eps){
  const g=$('#mapGraphG');if(!g)return;
  if(!eps.length){g.removeAttribute('transform');g.innerHTML='<text class="g-dim" x="20" y="28">No endpoints match — relax the filters, clear the search, or Refresh.</text>';return;}
  const lay=graphLayout(buildGraphTree(eps).children);mapState._g=lay;
  let h='';
  lay.edges.forEach(([a,b])=>{const x1=a.px+7,y1=a.py,x2=b.px-3,y2=b.py,mx=(x1+x2)/2;h+=`<path class="g-edge" d="M${x1} ${y1} C ${mx} ${y1} ${mx} ${y2} ${x2} ${y2}"/>`;});
  lay.nodes.forEach(n=>h+=gNode(n));
  g.innerHTML=h;
  g.querySelectorAll('.g-node').forEach(el=>el.onclick=ev=>{ev.stopPropagation();
    if(el.dataset.kind==='ep'){const f=el.dataset.flow;if(f)flowPopup(Number(f));return;}
    const k=el.dataset.key;mapState.collapsed.has(k)?mapState.collapsed.delete(k):mapState.collapsed.add(k);renderMap();});
  if(mapState._needFit){mapState._needFit=false;mapFitNow();}else mapApplyZoom();
}
export function mapApplyZoom(){const z=mapState.zoom;$('#mapGraphG').setAttribute('transform',`translate(${z.x} ${z.y}) scale(${z.k})`);}
export function mapFitNow(){const svg=$('#mapGraphSvg'),gr=mapState._g;if(!gr)return;
  const vw=svg.clientWidth||820,vh=svg.clientHeight||520;
  const k=Math.max(0.45,Math.min(1.4,vw/gr.w,vh/gr.h)); // keep labels legible; pan if it overflows
  mapState.zoom={k,x:14,y:Math.max(10,(vh-gr.h*k)/2)};mapApplyZoom();}
$('#mapViewSeg')&&$('#mapViewSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  mapState.view=b.dataset.v;$('#mapViewSeg').querySelectorAll('button').forEach(x=>x.classList.toggle('on',x===b));
  $('#mapTree').style.display=mapState.view==='tree'?'block':'none';$('#mapGraphSvg').style.display=mapState.view==='graph'?'block':'none';
  $('#mapExpand').style.display=mapState.view==='tree'?'':'none';$('#mapFit').style.display=mapState.view==='graph'?'':'none';
  mapState._needFit=true;renderMap();});
$('#mapFit')&&($('#mapFit').onclick=mapFitNow);
(function(){const svg=$('#mapGraphSvg');if(!svg)return;let drag=null;
  svg.addEventListener('wheel',e=>{e.preventDefault();const z=mapState.zoom,r=svg.getBoundingClientRect(),mx=e.clientX-r.left,my=e.clientY-r.top,f=e.deltaY<0?1.1:1/1.1,nk=Math.max(0.1,Math.min(4,z.k*f));z.x=mx-(mx-z.x)*(nk/z.k);z.y=my-(my-z.y)*(nk/z.k);z.k=nk;mapApplyZoom();},{passive:false});
  svg.addEventListener('mousedown',e=>{if(e.target.closest('.g-click'))return;drag={x:e.clientX,y:e.clientY,ox:mapState.zoom.x,oy:mapState.zoom.y};svg.style.cursor='grabbing';});
  window.addEventListener('mousemove',e=>{if(!drag)return;mapState.zoom.x=drag.ox+(e.clientX-drag.x);mapState.zoom.y=drag.oy+(e.clientY-drag.y);mapApplyZoom();});
  window.addEventListener('mouseup',()=>{if(drag){drag=null;svg.style.cursor='grab';}});
})();

/* ---- flow popup (quick request/response view, e.g. from the Map) ---- */
export async function flowPopup(id){
  let d;
  try{d=await api('/api/flows/'+id);}catch(e){toast('flow: '+e.message);return;}
  state.fm={id,detail:d,pretty:false};
  $('#fmTitle').innerHTML=`<span style="color:${methodColor(d.method)};font-weight:700">${esc(d.method)}</span> <span style="font-family:var(--mono);color:var(--fg2)">${esc((d.scheme||'http')+'://'+d.host+d.path)}</span>`;
  $('#fmStatus').textContent=d.status?`${d.status} ${statusText(d.status)}`+(d.durationMs?` · ${fmtDur(d.durationMs)}`:''):(d.error||'');
  $('#fmStatus').style.color=statusColor(d.status);
  $('#fmSeg').querySelectorAll('button').forEach(b=>b.classList.toggle('on',b.dataset.v==='raw'));
  openModal($('#flowModal'));
  fmRenderSide('req');fmRenderSide('res');
}
export async function fmRenderSide(side){
  const el=side==='req'?$('#fmReq'):$('#fmRes');const d=state.fm.detail;
  const len=side==='req'?d.reqLen:d.resLen;
  const mime=bodyMime(d,side);
  if(isBinaryMime(mime)){el.innerHTML=highlightHTTP(headerBlockText(d,side))+`<div class="hint" style="padding:14px 0 0;line-height:1.7">Body is <b>${esc(mime)}</b>${len?' · '+fmtSize(len):''} — binary, not rendered.<br><a class="btn" style="margin-top:8px;display:inline-block" href="/api/flows/${state.fm.id}/raw?side=${side}" download="flow-${state.fm.id}-${side}">⤓ Download body</a></div>`;return;}
  if(len>RENDER_CAP){el.innerHTML=`<div class="hint" style="padding:14px;line-height:1.7">${side==='req'?'Request':'Response'} body is <b>${fmtSize(len)}</b> — not rendered.<br><a class="btn" style="margin-top:8px;display:inline-block" href="/api/flows/${state.fm.id}/raw?side=${side}" download="flow-${state.fm.id}-${side}.txt">⤓ Download raw</a></div>`;return;}
  el.innerHTML='<span class="hint" style="padding:12px">loading…</span>';
  try{const raw=await api('/api/flows/'+state.fm.id+'/raw?side='+side);
    el.innerHTML=highlightHTTP(state.fm.pretty?prettify(raw):raw,state.fm.pretty);}
  catch(e){el.textContent='(error: '+e.message+')';}
}
$('#fmClose')&&($('#fmClose').onclick=()=>closeModal($('#flowModal')));
$('#fmProxy')&&($('#fmProxy').onclick=()=>{
  const d=state.fm&&state.fm.detail,id=state.fm&&state.fm.id;
  closeModal($('#flowModal'));if(!d)return;
  document.querySelector('.tab[data-tab="proxy"]').click();
  // Filter History to every request to this endpoint (host + method + path).
  state.filters={scheme:'',method:d.method||'',status:'',host:d.host||'',search:(d.path||'').split('?')[0],exclude:[]};
  syncControls();renderChips();loadFlows();
  if(id)selectFlow(id);
});
$('#fmSeg')&&$('#fmSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{state.fm.pretty=b.dataset.v==='pretty';$('#fmSeg').querySelectorAll('button').forEach(x=>x.classList.toggle('on',x===b));fmRenderSide('req');fmRenderSide('res');});
