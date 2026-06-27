import { $, esc, escAttr, state, toast, api, methodColor, statusColor, statusText, fmtSize, fmtDur } from './core.js';
import { sendToRepeater } from './tools.js';
import { flowPopup } from './flowmodal.js';

const GRAPH_NODE_CAP = 150;
const MAP_VIEW_KEY = 'mapView';

/* ---- endpoint map ---- */
function restoreMapView(){
  try{
    const v = localStorage.getItem(MAP_VIEW_KEY);
    if(v === 'tree' || v === 'table' || v === 'graph' || v === 'params') return v;
  }catch(e){}
  return 'tree';
}

export const mapState = {
  eps: [], domain: null, method: '', search: '', searchScope: 'path', searchNote: '', tag: '',
  statusClass: 0, expandAll: false,
  view: restoreMapView(), collapsed: new Set(), zoom: { k: 1, x: 12, y: 12 }, _needFit: true,
  sort: { key: 'path', dir: 1 },
};

function mapUsesServerSearch(){
  return mapState.searchScope !== 'path' && mapState.search.trim().length > 0;
}

export function focusMapSearch(term, scope='body'){
  term=String(term||'').trim();
  if(!term){ toast('nothing to search'); return; }
  document.querySelector('.tab[data-tab="map"]')?.click();
  mapState.domain='';
  mapState.search=term;
  mapState.searchScope=scope||'body';
  mapState.view='table';
  setMapView('table');
  const dom=$('#mapDomain'), sr=$('#mapSearch'), sc=$('#mapSearchScope');
  if(dom) dom.value='';
  if(sr) sr.value=term;
  if(sc) sc.value=mapState.searchScope;
  loadEndpoints();
}

export async function loadEndpoints(){
  const warn=$('#mapWarn');
  if(warn&&mapUsesServerSearch()){warn.style.display='block';warn.textContent='Searching bodies…';}
  try{
    const params = new URLSearchParams();
    if(mapState.domain) params.set('host', mapState.domain);
    if(mapState.tag) params.set('tag', mapState.tag);
    if(mapUsesServerSearch()){
      params.set('search', mapState.search.trim());
      params.set('searchScope', mapState.searchScope);
    }
    const q = params.toString();
    const d = await api('/api/endpoints' + (q ? '?' + q : ''));
    mapState.eps = d.endpoints || [];
    mapState.searchNote = d.searchNote || '';
    mapState._needFit = true;
    fillMapDomains();
    fillMapMethods();
    fillMapTags();
    renderMap();
  }catch(e){ toast('map: '+e.message); }
}

export function fillMapDomains(){
  const sel = $('#mapDomain'); if(!sel) return;
  const counts = {};
  mapState.eps.forEach(e => { counts[e.host] = (counts[e.host] || 0) + 1; });
  const hosts = Object.keys(counts).sort((a, b) => counts[b] - counts[a] || a.localeCompare(b));
  if(mapState.domain === null) mapState.domain = hosts[0] || '';
  else if(mapState.domain && !counts[mapState.domain]) mapState.domain = hosts[0] || '';
  sel.innerHTML = `<option value="">All domains (${mapState.eps.length})</option>`
    + hosts.map(h => `<option value="${escAttr(h)}">${esc(h)} (${counts[h]})</option>`).join('');
  sel.value = mapState.domain;
}

// fillMapTags populates the Map tag filter from the project's tags (state.tags),
// keeping the current selection. Hidden when there are no tags.
export function fillMapTags(){
  const sel = $('#mapTag'); if(!sel) return;
  const tags = state.tags || [];
  sel.style.display = tags.length ? '' : 'none';
  if(mapState.tag && !tags.some(t => t.tag === mapState.tag)) mapState.tag = '';
  sel.innerHTML = '<option value="">all tags</option>'
    + tags.map(t => `<option value="${escAttr(t.tag)}">${esc(t.tag)} (${t.count})</option>`).join('');
  sel.value = mapState.tag;
}

export function mapCollapseHosts(){
  mapState.collapsed.clear();
  [...new Set(mapState.eps.map(e => e.host))].forEach(h => mapState.collapsed.add('/'+h));
}

export function fillMapMethods(){
  const sel = $('#mapMethod'); if(!sel) return;
  const methods = [...new Set(mapState.eps.map(e => e.method))].sort();
  const cur = sel.value;
  sel.innerHTML = '<option value="">method</option>' + methods.map(m => `<option value="${escAttr(m)}">${esc(m)}</option>`).join('');
  if(methods.includes(cur)) sel.value = cur;
}

export function epMatchesSearch(e, q){
  if(!q) return true;
  q = q.toLowerCase();
  return (e.path||'').toLowerCase().includes(q)
    || (e.host||'').toLowerCase().includes(q)
    || (e.method||'').toLowerCase().includes(q);
}

export function mapFiltered(){
  const q = mapState.search.toLowerCase();
  const serverFiltered = mapUsesServerSearch();
  return mapState.eps.filter(e => {
    if(mapState.domain && e.host !== mapState.domain) return false;
    if(mapState.method && e.method !== mapState.method) return false;
    if(mapState.statusClass && Math.floor((e.lastStatus || 0) / 100) !== mapState.statusClass) return false;
    if(!serverFiltered && q && !epMatchesSearch(e, q)) return false;
    return true;
  });
}

export function mapCount(node){ let n = node.eps.length; node.kids.forEach(k => n += mapCount(k)); return n; }

export function buildMapTree(eps){
  const hosts = new Map();
  eps.forEach(e => {
    if(!hosts.has(e.host)) hosts.set(e.host, { name: e.host, kids: new Map(), eps: [] });
    let node = hosts.get(e.host);
    (e.path || '/').split('?')[0].split('/').filter(Boolean).forEach(seg => {
      if(!node.kids.has(seg)) node.kids.set(seg, { name: seg, kids: new Map(), eps: [] });
      node = node.kids.get(seg);
    });
    node.eps.push(e);
  });
  return hosts;
}

export function mapEpRow(e, dim){
  const sts = (e.statuses || []).map(s => `<span style="color:${statusColor(s)}">${s}</span>`).join(' ');
  const path = e.path || '/';
  const hit = mapState.search && epMatchesSearch(e, mapState.search);
  return `<div class="map-ep${dim && !hit ? ' map-dim' : ''}${hit ? ' map-hit' : ''}"${e.lastFlowId ? ` data-flow="${e.lastFlowId}"` : ''} title="${escAttr(e.method+' '+(e.scheme||'http')+'://'+e.host+path)}">
    <span class="map-m" style="color:${methodColor(e.method)}">${esc(e.method)}</span>
    <span class="map-p">${esc(path)}</span><span class="map-sts">${sts}</span>
    <span class="map-hits">${e.hits > 1 ? e.hits+'×' : ''}</span></div>`;
}

export function mapRenderNode(node, open, dim){
  let html = '';
  [...node.kids.values()].sort((a, b) => a.name.localeCompare(b.name)).forEach(kid => {
    html += `<details class="map-folder"${open ? ' open' : ''}><summary><span class="map-seg">/${esc(kid.name)}</span><span class="map-c">${mapCount(kid)}</span></summary><div class="map-body">${mapRenderNode(kid, open, dim)}</div></details>`;
  });
  node.eps.slice().sort((a, b) => a.method.localeCompare(b.method)).forEach(e => html += mapEpRow(e, dim));
  return html;
}

function mapHintText(){
  if(mapState.view === 'params') return 'Parameter names mined from captured traffic — click a row to inspect the sample flow';
  if(mapState.view === 'table') return 'Sortable endpoint list · click a row to inspect · → Rep sends to Repeater';
  if(mapState.view === 'graph') return 'Drag to pan · scroll to zoom · click folder to expand · double-click host to focus domain · click endpoint to inspect';
  return 'Hierarchical site map · click an endpoint to inspect';
}

function setMapView(v){
  mapState.view = v;
  try{ localStorage.setItem(MAP_VIEW_KEY, v); }catch(e){}
  const seg = $('#mapViewSeg');
  if(seg) seg.querySelectorAll('button').forEach(x => { const on = x.dataset.v === v; x.classList.toggle('on', on); x.setAttribute('aria-pressed', on ? 'true' : 'false'); });
  const tree = $('#mapTree'), tbl = $('#mapTable'), wrap = $('#mapGraphWrap'), params = $('#mapParams');
  if(tree) tree.style.display = v === 'tree' ? 'block' : 'none';
  if(tbl) tbl.style.display = v === 'table' ? 'block' : 'none';
  if(wrap) wrap.style.display = v === 'graph' ? 'block' : 'none';
  if(params) params.style.display = v === 'params' ? 'block' : 'none';
  const exp = $('#mapExpand'), fit = $('#mapFit');
  if(exp) exp.style.display = v === 'tree' ? '' : 'none';
  if(fit) fit.style.display = v === 'graph' ? '' : 'none';
  const hint = $('#mapHint');
  if(hint) hint.textContent = mapHintText();
  mapState._needFit = true;
  if(v === 'params') loadParams();
  else renderMap();
}

export async function loadParams(){
  const warn=$('#mapWarn');
  if(warn){warn.style.display='block';warn.textContent='Mining parameters…';}
  try{
    const q = new URLSearchParams();
    if(mapState.domain) q.set('host', mapState.domain);
    q.set('inScope', '1');
    const d = await api('/api/params?' + q);
    renderMapParams(d);
    if(warn) warn.style.display='none';
    const c=$('#mapCount');if(c)c.textContent=(d.flowsScanned||0)+' flows · param miner';
  }catch(e){if(warn){warn.style.display='block';warn.textContent='';} toast('params: '+e.message);}
}

function renderMapParams(d){
  const box=$('#mapParams'); if(!box) return;
  const hosts=d.hosts||[];
  if(!hosts.length){box.innerHTML='<div class="hint" style="padding:16px">No parameters found — capture in-scope traffic with query strings or form/JSON bodies.</div>';return;}
  box.innerHTML=hosts.map(h=>`<div style="margin-bottom:16px">
    <div style="font-size:10px;font-weight:700;letter-spacing:.5px;color:var(--accent);margin-bottom:6px">${esc(h.host)}</div>
    <table class="rules-tbl"><thead><tr><th>Name</th><th style="width:70px">Source</th><th style="width:50px">Hits</th><th style="width:90px">Sample</th></tr></thead><tbody>
    ${(h.params||[]).map(p=>`<tr class="map-param-row" data-flow="${p.lastFlowId}" title="${escAttr(p.samplePath||'')}">
      <td style="font-family:var(--mono);color:var(--fg)">${esc(p.name)}</td>
      <td style="color:var(--fg3)">${esc(p.source)}</td>
      <td>${p.hits}</td>
      <td><button type="button" class="btn xs map-param-inspect">#${p.lastFlowId}</button></td>
    </tr>`).join('')}
    </tbody></table></div>`).join('');
  box.querySelectorAll('.map-param-inspect').forEach(b=>{b.onclick=ev=>{ev.stopPropagation();const tr=b.closest('[data-flow]');if(tr)flowPopup(Number(tr.dataset.flow));};});
}

function renderMapCrumb(eps){
  const el = $('#mapCrumb'); if(!el) return;
  const hostN = new Set(eps.map(e => e.host)).size;
  const parts = [];
  parts.push(`<a href="#" data-crumb="all">All</a>`);
  if(mapState.domain){
    parts.push(`<a href="#" data-crumb="domain">${esc(mapState.domain)}</a>`);
  }else{
    parts.push(`<span>${hostN} host${hostN === 1 ? '' : 's'}</span>`);
  }
  if(mapState.search) parts.push(`<span>search (${esc(mapScopeLabel(mapState.searchScope))}): <b style="color:var(--accent)">${esc(mapState.search)}</b></span>`);
  el.innerHTML = parts.join(' <span style="color:var(--fg3)">›</span> ');
  el.style.display = 'block';
  el.querySelectorAll('[data-crumb]').forEach(a => {
    a.onclick = ev => {
      ev.preventDefault();
      if(a.dataset.crumb === 'all'){
        mapState.domain = '';
        $('#mapDomain').value = '';
        mapCollapseHosts();
      }else if(a.dataset.crumb === 'domain'){
        mapState.collapsed.clear();
      }
      mapState._needFit = true;
      renderMap();
    };
  });
}

function mapScopeLabel(scope){
  return ({path:'path/host',headers:'headers',body:'body',all:'all'})[scope] || scope;
}

export function renderMap(){
  if(mapState.view === 'params') return;
  const eps = mapFiltered();
  const hostN = new Set(eps.map(e => e.host)).size;
  const filtered = !!(mapState.search || mapState.method || mapState.statusClass || mapState.domain);
  $('#mapCount').textContent = eps.length
    ? `${eps.length} endpoint${eps.length === 1 ? '' : 's'} · ${hostN} host${hostN === 1 ? '' : 's'}`
    : (mapState.eps.length ? (filtered ? 'No endpoints match the filters' : 'No endpoints') : 'No endpoints captured yet');
  const warn = $('#mapWarn');
  if(warn && mapState.searchNote){
    warn.style.display = 'block';
    warn.textContent = mapState.searchNote;
  }else if(warn && mapUsesServerSearch() && (mapState.searchScope === 'body' || mapState.searchScope === 'all') && mapState.view !== 'graph'){
    warn.style.display = 'block';
    warn.textContent = 'Body search scans stored bodies (content-deduped, latest 8000 flows max). Filter by domain to narrow.';
  }else if(warn && mapState.view !== 'graph'){
    warn.style.display = 'none';
    warn.textContent = '';
  }
  renderMapCrumb(eps);
  if(mapState.view === 'graph') renderMapGraph(eps);
  else if(mapState.view === 'table') renderMapTable(eps);
  else renderMapTree(eps);
}

export function renderMapTree(eps){
  const box = $('#mapTree'); if(!box) return;
  if(!eps.length){
    box.innerHTML = '<div class="hint" style="padding:12px">No endpoints match — capture traffic or relax the filters.</div>';
    return;
  }
  const open = mapState.expandAll || !!mapState.search;
  const dim = !!mapState.search;
  const hosts = buildMapTree(eps);
  box.innerHTML = [...hosts.values()].sort((a, b) => a.name.localeCompare(b.name)).map(h =>
    `<details class="map-host" open><summary>🌐 ${esc(h.name)}<span class="map-c">${mapCount(h)}</span></summary><div class="map-body">${mapRenderNode(h, open, dim)}</div></details>`
  ).join('');
  box.querySelectorAll('.map-ep[data-flow]').forEach(el => el.onclick = () => flowPopup(Number(el.dataset.flow)));
}

function mapSortEps(eps){
  const k = mapState.sort.key, dir = mapState.sort.dir;
  const val = e => k === 'hits' ? (e.hits || 0) : k === 'status' ? (e.lastStatus || 0) : k === 'method' ? e.method : k === 'host' ? e.host : (e.path || '');
  return eps.slice().sort((a, b) => {
    const x = val(a), y = val(b);
    return (x > y ? 1 : x < y ? -1 : 0) * dir;
  });
}

function renderMapTable(eps){
  const box = $('#mapTable'); if(!box) return;
  if(!eps.length){
    box.innerHTML = '<div class="hint" style="padding:16px">No endpoints match — capture traffic or relax the filters.</div>';
    return;
  }
  const sorted = mapSortEps(eps);
  const showHost = !mapState.domain;
  const sk = mapState.sort.key, sd = mapState.sort.dir;
  const th = (k, label, w) => `<th class="${sk === k ? 'sorted' : ''}" data-sort="${k}"${w ? ` style="width:${w}"` : ''}>${label}${sk === k ? (sd > 0 ? ' ▲' : ' ▼') : ''}</th>`;
  const rows = sorted.map(e => {
    const path = e.path || '/';
    const sts = (e.statuses || []).map(s => `<span style="color:${statusColor(s)}">${s}</span>`).join(' ');
    const hit = mapState.search && epMatchesSearch(e, mapState.search);
    return `<tr data-flow="${e.lastFlowId || ''}" class="${hit ? 'map-hit-row' : ''}">
      ${showHost ? `<td style="font-family:var(--mono);font-size:11px">${esc(e.host)}</td>` : ''}
      <td class="map-tbl-m" style="color:${methodColor(e.method)}">${esc(e.method)}</td>
      <td class="map-tbl-p" title="${escAttr(path)}">${esc(path)}</td>
      <td class="map-tbl-sts">${sts || '—'}</td>
      <td style="text-align:right;color:var(--fg3)">${e.hits > 1 ? e.hits+'×' : ''}</td>
      <td class="map-tbl-act">${e.lastFlowId ? `<button class="btn" data-rep="${e.lastFlowId}" title="Send to Repeater">→ Rep</button>` : ''}</td>
    </tr>`;
  }).join('');
  box.innerHTML = `<table class="map-tbl"><thead><tr>
    ${showHost ? th('host', 'Host', '140px') : ''}
    ${th('method', 'Method', '72px')}
    ${th('path', 'Path', '')}
    ${th('status', 'Status', '88px')}
    ${th('hits', 'Hits', '52px')}
    <th style="width:72px"></th>
  </tr></thead><tbody>${rows}</tbody></table>`;
  box.querySelectorAll('th[data-sort]').forEach(h => h.onclick = () => {
    const k = h.dataset.sort;
    if(mapState.sort.key === k) mapState.sort.dir *= -1;
    else{ mapState.sort.key = k; mapState.sort.dir = 1; }
    renderMap(); // route through renderMap so crumb/count/warn stay consistent
  });
  box.querySelectorAll('tr[data-flow]').forEach(tr => {
    const id = Number(tr.dataset.flow);
    if(!id) return;
    tr.onclick = ev => {
      if(ev.target.closest('[data-rep]')) return;
      flowPopup(id);
    };
  });
  box.querySelectorAll('[data-rep]').forEach(b => b.onclick = ev => {
    ev.stopPropagation();
    sendToRepeater({ id: Number(b.dataset.rep) });
  });
}

let mapSearchTimer = null;
function mapApplySearch(){
  if(mapUsesServerSearch()){
    loadEndpoints();
    return;
  }
  mapState.searchNote = '';
  if(mapState.search) mapExpandForSearch(mapFiltered());
  mapState._needFit = true;
  renderMap();
}
$('#mapSearch') && ($('#mapSearch').oninput = e => {
  mapState.search = e.target.value.trim();
  clearTimeout(mapSearchTimer);
  if(mapUsesServerSearch()) mapSearchTimer = setTimeout(mapApplySearch, 350);
  else mapApplySearch();
});
$('#mapSearchScope') && ($('#mapSearchScope').onchange = e => {
  mapState.searchScope = e.target.value || 'path';
  mapApplySearch();
});
$('#mapDomain') && ($('#mapDomain').onchange = e => {
  mapState.domain = e.target.value;
  if(mapState.domain) mapState.collapsed.clear();
  else mapCollapseHosts();
  mapState._needFit = true;
  if(mapUsesServerSearch()) loadEndpoints();
  else renderMap();
});
$('#mapMethod') && ($('#mapMethod').onchange = e => { mapState.method = e.target.value; mapState._needFit = true; renderMap(); });
$('#mapRefresh') && ($('#mapRefresh').onclick = loadEndpoints);
$('#mapExpand').onclick = () => {
  mapState.expandAll = !mapState.expandAll;
  $('#mapExpand').textContent = mapState.expandAll ? 'Collapse all' : 'Expand all';
  mapState._needFit = true;
  renderMap();
};
$('#mapStatus') && ($('#mapStatus').onchange = e => { mapState.statusClass = Number(e.target.value) || 0; mapState._needFit = true; renderMap(); });
// Tag is a server-side filter (changes which endpoints come back) — re-fetch.
$('#mapTag') && ($('#mapTag').onchange = e => { mapState.tag = e.target.value; mapState.domain = null; mapState._needFit = true; loadEndpoints(); });

/* ---- map: node-link graph ---- */
export function gTrunc(s, n){ return s.length > n ? s.slice(0, n - 1) + '…' : s; }
export function gCount(n){ if(n.type === 'ep') return 1; let c = 0; n.children.forEach(k => c += gCount(k)); return c; }

export function buildGraphTree(eps){
  const root = { key: '', type: 'root', children: [], cm: new Map() };
  const child = (p, k, label, type) => {
    let c = p.cm.get(k);
    if(!c){ c = { key: p.key+'/'+k, label, type, children: [], cm: new Map(), ep: null }; p.cm.set(k, c); p.children.push(c); }
    return c;
  };
  eps.forEach(e => {
    const host = child(root, e.host, e.host, 'host');
    let node = host;
    (e.path || '/').split('?')[0].split('/').filter(Boolean).forEach(seg => { node = child(node, seg, '/'+seg, 'folder'); });
    child(node, 'ep|'+e.method, e.method, 'ep').ep = e;
  });
  return root;
}

function mapExpandForSearch(eps){
  const q = mapState.search.toLowerCase();
  if(!q) return;
  const root = buildGraphTree(eps);
  function subtreeMatch(n){
    if(n.type === 'ep') return epMatchesSearch(n.ep, q);
    return n.children.some(subtreeMatch);
  }
  function walk(n){
    if(subtreeMatch(n)) mapState.collapsed.delete(n.key);
    n.children.forEach(walk);
  }
  root.children.forEach(walk);
}

export function graphLayout(hosts){
  const COL = 168, ROW = 24, PAD = 20;
  let leaf = 0, maxD = 0;
  function place(n, d){
    n.depth = d; maxD = Math.max(maxD, d);
    n._col = mapState.collapsed.has(n.key) && n.children.length > 0;
    if(n._col || !n.children.length){ n.row = leaf++; return; }
    n.children.forEach(c => place(c, d + 1));
    n.row = (n.children[0].row + n.children[n.children.length - 1].row) / 2;
  }
  hosts.forEach(h => place(h, 0));
  const nodes = [], edges = [];
  function collect(n){
    n.px = PAD + n.depth * COL; n.py = PAD + n.row * ROW;
    nodes.push(n);
    if(!n._col) n.children.forEach(c => { edges.push([n, c]); collect(c); });
  }
  hosts.forEach(collect);
  return { nodes, edges, w: PAD * 2 + maxD * COL + 200, h: PAD * 2 + Math.max(1, leaf) * ROW };
}

function graphNodeMatches(n){
  if(!mapState.search) return true;
  if(n.type === 'ep') return epMatchesSearch(n.ep, mapState.search);
  return n.children.some(graphNodeMatches);
}

function graphTipShow(n, ev){
  const tip = $('#mapGraphTip'); if(!tip) return;
  let html = '';
  if(n.type === 'ep' && n.ep){
    const e = n.ep;
    html = `<div class="tip-m" style="color:${methodColor(e.method)}">${esc(e.method)} <span style="color:${statusColor(e.lastStatus)}">${e.lastStatus || '—'}</span></div>
      <div>${esc((e.scheme||'http')+'://'+e.host+(e.path||'/'))}</div>
      ${e.hits > 1 ? `<div class="hint">${e.hits} hits · ${(e.statuses||[]).join(', ')}</div>` : ''}`;
  }else{
    html = `<div class="tip-m">${esc(n.label)}</div><div class="hint">${gCount(n)} endpoint${gCount(n) === 1 ? '' : 's'} · click to ${mapState.collapsed.has(n.key) ? 'expand' : 'collapse'}</div>`;
  }
  tip.innerHTML = html;
  tip.style.display = 'block';
  const wrap = $('#mapGraphWrap').getBoundingClientRect();
  tip.style.left = Math.min(wrap.width - 200, ev.clientX - wrap.left + 12) + 'px';
  tip.style.top = (ev.clientY - wrap.top + 12) + 'px';
}

function graphTipHide(){ const t = $('#mapGraphTip'); if(t) t.style.display = 'none'; }

export function gNode(n){
  const x = n.px, y = n.py;
  const match = graphNodeMatches(n);
  const dim = mapState.search && !match;
  const cls = `g-node g-click${dim ? ' g-dimmed' : ''}${match && mapState.search ? ' g-match' : ''}`;
  let mk, lb, title = esc(n.label || ''), extra = '', hitW = 120;
  if(n.type === 'host'){
    mk = `<circle cx="${x}" cy="${y}" r="6" fill="var(--accent)"/>`;
    lb = `<text class="g-host" x="${x+10}" y="${y}">${esc(gTrunc(n.label, 32))}${n._col ? ` <tspan class="g-dim">+${gCount(n)}</tspan>` : ''}</text>`;
    hitW = Math.min(280, 10 + n.label.length * 6.5);
  }else if(n.type === 'ep'){
    const e = n.ep, col = statusColor(e.lastStatus);
    title = esc(e.method+' '+(e.scheme||'http')+'://'+e.host+(e.path||'/'));
    mk = `<rect x="${x-5}" y="${y-5}" width="10" height="10" rx="2" fill="${col}"/>`;
    lb = `<text class="g-ep" x="${x+10}" y="${y}"><tspan fill="${methodColor(e.method)}" font-weight="700">${esc(e.method)}</tspan> <tspan class="g-dim">${esc(gTrunc((e.path||'/'), 36))}${e.hits > 1 ? ' · '+e.hits+'×' : ''}</tspan></text>`;
    hitW = Math.min(320, 10 + ((e.path||'').length + e.method.length) * 5.5);
    extra = ` data-flow="${e.lastFlowId||''}"`;
  }else{
    mk = `<circle cx="${x}" cy="${y}" r="5" fill="${n._col ? 'var(--blue)' : 'var(--bg3)'}" stroke="var(--blue)" stroke-width="1.4"/>`;
    lb = `<text class="g-folder" x="${x+10}" y="${y}">${esc(gTrunc(n.label, 28))}${n._col ? ` <tspan class="g-dim">+${gCount(n)}</tspan>` : ''}</text>`;
    hitW = Math.min(240, 10 + n.label.length * 6);
  }
  const hit = `<rect class="g-hit" x="${x-8}" y="${y-12}" width="${hitW}" height="24" fill="transparent"/>`;
  return `<g class="${cls}" data-key="${escAttr(n.key)}" data-kind="${n.type}" data-host="${n.type === 'host' ? escAttr(n.label) : ''}"${extra}><title>${title}</title>${hit}${mk}${lb}</g>`;
}

export function renderMapGraph(eps){
  const g = $('#mapGraphG'); if(!g) return;
  const warn = $('#mapWarn');
  graphTipHide();
  if(!eps.length){
    g.removeAttribute('transform');
    g.innerHTML = '<text class="g-dim" x="20" y="28">No endpoints match — relax the filters, clear the search, or Refresh.</text>';
    if(warn) warn.style.display = 'none';
    return;
  }
  if(mapState.search) mapExpandForSearch(eps);
  const lay = graphLayout(buildGraphTree(eps).children);
  mapState._g = lay;
  if(warn){
    if(mapState.searchNote){
      warn.style.display = 'block';
      warn.textContent = mapState.searchNote;
    }else if(lay.nodes.length > GRAPH_NODE_CAP){
      warn.style.display = 'block';
      warn.textContent = `Graph has ${lay.nodes.length} nodes — hard to read. Filter by domain, use Table view, or search to narrow.`;
    }else if(!mapUsesServerSearch() || (mapState.searchScope !== 'body' && mapState.searchScope !== 'all')){
      warn.style.display = 'none';
    }
  }
  let h = '';
  lay.edges.forEach(([a, b]) => {
    const x1 = a.px + 8, y1 = a.py, x2 = b.px - 4, y2 = b.py, mx = (x1 + x2) / 2;
    h += `<path class="g-edge" d="M${x1} ${y1} C ${mx} ${y1} ${mx} ${y2} ${x2} ${y2}"/>`;
  });
  lay.nodes.forEach(n => h += gNode(n));
  g.innerHTML = h;
  g.querySelectorAll('.g-node').forEach(el => {
    el.addEventListener('mouseenter', ev => {
      const key = el.dataset.key;
      const n = lay.nodes.find(x => x.key === key);
      if(n) graphTipShow(n, ev);
    });
    el.addEventListener('mouseleave', graphTipHide);
    el.addEventListener('dblclick', ev => {
      ev.stopPropagation();
      const host = el.dataset.host;
      if(!host) return;
      mapState.domain = host;
      const sel = $('#mapDomain');
      if(sel) sel.value = host;
      mapState.collapsed.clear();
      mapState._needFit = true;
      renderMap();
      toast('focused on '+host);
    });
    el.onclick = ev => {
      ev.stopPropagation();
      if(el.dataset.kind === 'ep'){
        const f = el.dataset.flow;
        if(f) flowPopup(Number(f));
        return;
      }
      const k = el.dataset.key;
      mapState.collapsed.has(k) ? mapState.collapsed.delete(k) : mapState.collapsed.add(k);
      renderMap();
    };
  });
  if(mapState._needFit){ mapState._needFit = false; mapFitNow(); }
  else mapApplyZoom();
}

export function mapApplyZoom(){
  const z = mapState.zoom;
  $('#mapGraphG').setAttribute('transform', `translate(${z.x} ${z.y}) scale(${z.k})`);
}

export function mapFitNow(){
  const svg = $('#mapGraphSvg'), gr = mapState._g;
  if(!svg || !gr) return;
  const vw = svg.clientWidth || 820, vh = svg.clientHeight || 520;
  const k = Math.max(0.35, Math.min(1.5, vw / gr.w, vh / gr.h));
  mapState.zoom = { k, x: 16, y: Math.max(10, (vh - gr.h * k) / 2) };
  mapApplyZoom();
}

$('#mapViewSeg') && $('#mapViewSeg').querySelectorAll('button').forEach(b => b.onclick = () => setMapView(b.dataset.v));
$('#mapFit') && ($('#mapFit').onclick = mapFitNow);

(function(){
  const svg = $('#mapGraphSvg'); if(!svg) return;
  let drag = null;
  svg.addEventListener('wheel', e => {
    e.preventDefault();
    const z = mapState.zoom, r = svg.getBoundingClientRect(), mx = e.clientX - r.left, my = e.clientY - r.top;
    const f = e.deltaY < 0 ? 1.1 : 1 / 1.1, nk = Math.max(0.1, Math.min(4, z.k * f));
    z.x = mx - (mx - z.x) * (nk / z.k); z.y = my - (my - z.y) * (nk / z.k); z.k = nk;
    mapApplyZoom();
  }, { passive: false });
  svg.addEventListener('mousedown', e => {
    if(e.target.closest('.g-click')) return;
    drag = { x: e.clientX, y: e.clientY, ox: mapState.zoom.x, oy: mapState.zoom.y };
    svg.style.cursor = 'grabbing';
  });
  window.addEventListener('mousemove', e => {
    if(!drag) return;
    mapState.zoom.x = drag.ox + (e.clientX - drag.x);
    mapState.zoom.y = drag.oy + (e.clientY - drag.y);
    mapApplyZoom();
  });
  window.addEventListener('mouseup', () => { if(drag){ drag = null; svg.style.cursor = 'grab'; } });
})();

// Apply saved view on load (DOM ready — this module loads after index.html paints).
setMapView(mapState.view);
