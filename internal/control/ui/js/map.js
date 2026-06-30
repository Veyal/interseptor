import { $, esc, escAttr, state, toast, api, apiTry, methodColor, statusColor, statusText, fmtSize, fmtDur } from './core.js';
import { sendToRepeater } from './tools.js';

// keyClick promotes a click-only element to a keyboard-operable control (role +
// tabindex + Enter/Space) so endpoints/rows/sorts are reachable without a mouse.
function keyClick(el, fn){
  if(!el) return;
  el.setAttribute('role','button');
  el.tabIndex=0;
  el.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();fn(e);}});
  el.onclick=fn;
}
import { flowPopup } from './flowmodal.js';

const GRAPH_NODE_MAX = 200;
const MAP_TREE_EAGER_MAX = 2500;
const MAP_TABLE_VIRTUAL_MIN = 400;
const MAP_ROW_H = 28;
const MAP_VIEW_KEY = 'mapView';
const MAP_HIDE_NOISE_KEY = 'mapHideNoise';
const MAP_COLLAPSE_IDENTICAL_KEY = 'mapCollapseIdentical';

// Static media extensions omitted from the node-link graph (images, fonts, AV).
const MAP_MEDIA_EXT = new Set([
  '.png', '.jpg', '.jpeg', '.gif', '.webp', '.svg', '.ico', '.bmp', '.avif',
  '.woff', '.woff2', '.ttf', '.otf', '.eot',
  '.mp4', '.webm', '.mov', '.avi', '.mkv',
  '.mp3', '.wav', '.ogg', '.m4a', '.flac',
]);

function isMapMediaEndpoint(e){
  const path = String(e.path || '/').split('?')[0].split('#')[0].toLowerCase();
  const leaf = path.slice(path.lastIndexOf('/') + 1);
  if(leaf === 'favicon.ico') return true;
  const dot = leaf.lastIndexOf('.');
  if(dot < 0) return false;
  return MAP_MEDIA_EXT.has(leaf.slice(dot));
}

function pruneGraphNode(n){
  if(n.type === 'ep') return true;
  n.children = n.children.filter(c => pruneGraphNode(c));
  return n.children.length > 0;
}

function graphEps(eps){
  return eps.filter(e => !isMapMediaEndpoint(e));
}

function restoreMapHideNoise(){
  try{
    const v = localStorage.getItem(MAP_HIDE_NOISE_KEY);
    if(v === '0') return false;
  }catch(e){}
  return true;
}

function restoreMapCollapseIdentical(){
  try{
    if(localStorage.getItem(MAP_COLLAPSE_IDENTICAL_KEY) === '0') return false;
  }catch(e){}
  return true;
}

/* ---- endpoint map ---- */
function restoreMapView(){
  try{
    const v = localStorage.getItem(MAP_VIEW_KEY);
    if(v === 'tree' || v === 'table' || v === 'graph' || v === 'params') return v;
  }catch(e){}
  return 'tree';
}

export const mapState = {
  eps: [], total: 0, truncated: false, domain: null, method: '', search: '', searchScope: 'path', searchNote: '', tag: '',
  statusClass: 0, hideNoise: restoreMapHideNoise(), collapseIdentical: restoreMapCollapseIdentical(), expandAll: false,
  view: restoreMapView(), collapsed: new Set(), expandedClusters: new Set(), zoom: { k: 1, x: 12, y: 12 }, _needFit: true,
  sort: { key: 'path', dir: 1 }, _treeHosts: null, _dataVersion: 0,
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
  const params = new URLSearchParams();
  if(mapState.domain) params.set('host', mapState.domain);
  if(mapState.tag) params.set('tag', mapState.tag);
  if(!mapState.hideNoise) params.set('hideNoise', '0');
  if(mapUsesServerSearch()){
    params.set('search', mapState.search.trim());
    params.set('searchScope', mapState.searchScope);
  }
  const q = params.toString();
  const d = await apiTry('/api/endpoints' + (q ? '?' + q : ''),{},{label:'Map'});
  if(!d)return;
  mapState.eps = d.endpoints || [];
  mapState.total = d.total != null ? d.total : mapState.eps.length;
  mapState.truncated = !!d.truncated;
  mapState.searchNote = d.searchNote || '';
  mapState._dataVersion++;
  mapState._needFit = true;
  fillMapDomains();
  fillMapMethods();
  fillMapTags();
  renderMap();
}

let _fdKey = -1, _fdHtml = '';
export function fillMapDomains(){
  const sel = $('#mapDomain'); if(!sel) return;
  // Rebuild the (potentially thousands-of-options) host <select> only when the
  // dataset actually changes — successive re-fetches with the same hosts reuse it.
  if(mapState._dataVersion !== _fdKey){
    const counts = {};
    mapState.eps.forEach(e => { counts[e.host] = (counts[e.host] || 0) + 1; });
    const hosts = Object.keys(counts).sort((a, b) => counts[b] - counts[a] || a.localeCompare(b));
    if(mapState.domain === null) mapState.domain = hosts[0] || '';
    else if(mapState.domain && !counts[mapState.domain]) mapState.domain = hosts[0] || '';
    _fdHtml = `<option value="">All domains (${mapState.eps.length})</option>`
      + hosts.map(h => `<option value="${escAttr(h)}">${esc(h)} (${counts[h]})</option>`).join('');
    _fdKey = mapState._dataVersion;
    sel.innerHTML = _fdHtml;
  }
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

let _mfKey = '', _mfCache = null;
export function mapFiltered(){
  const key = mapState._dataVersion + '|' + (mapState.domain || '') + '|' + mapState.method + '|' + mapState.statusClass + '|' + mapState.eps.length;
  if(key === _mfKey && _mfCache) return _mfCache;
  // Client-side search is a marking pass only (dims non-matches) — not a filter —
  // so buildMapTree's memo stays valid while typing.
  const out = mapState.eps.filter(e => {
    if(mapState.domain && e.host !== mapState.domain) return false;
    if(mapState.method && e.method !== mapState.method) return false;
    if(mapState.statusClass && Math.floor((e.lastStatus || 0) / 100) !== mapState.statusClass) return false;
    return true;
  });
  _mfKey = key; _mfCache = out;
  return out;
}

// Per-host clustering: soft-404 endpoints group together; remaining endpoints
// with the same latest resBodyHash collapse into one "+N identical" row.
function mapClusterKey(host, kind, id){ return host + '|' + kind + '|' + id; }

function mapMakeCluster(members, kind, host, hidden, out, hash){
  members.sort((a, b) => (b.hits || 0) - (a.hits || 0) || (a.path || '').localeCompare(b.path || ''));
  const rep = { ...members[0] };
  const id = kind === 'soft404' ? 'soft404' : hash;
  rep._cluster = { kind, key: mapClusterKey(host, kind, id), count: members.length, members };
  out.push(rep);
  for(let i = 1; i < members.length; i++){
    hidden.add(members[i].host + '|' + members[i].method + '|' + members[i].path);
  }
}

function mapAssignClusters(eps){
  if(!mapState.collapseIdentical) return eps.map(e => ({ ...e }));
  const byHost = new Map();
  eps.forEach(e => {
    if(!byHost.has(e.host)) byHost.set(e.host, []);
    byHost.get(e.host).push(e);
  });
  const hidden = new Set();
  const out = [];
  for(const [, list] of byHost){
    const soft = list.filter(e => e.soft404);
    if(soft.length > 1) mapMakeCluster(soft, 'soft404', soft[0].host, hidden, out, '');
    else soft.forEach(e => out.push({ ...e }));
    const rest = list.filter(e => !e.soft404);
    const singletons = [];
    const byHash = new Map();
    rest.forEach(e => {
      const h = e.resBodyHash || '';
      if(!h){ singletons.push(e); return; }
      if(!byHash.has(h)) byHash.set(h, []);
      byHash.get(h).push(e);
    });
    singletons.forEach(e => out.push({ ...e }));
    for(const [hash, members] of byHash){
      if(members.length > 1) mapMakeCluster(members, 'identical', members[0].host, hidden, out, hash);
      else out.push({ ...members[0] });
    }
  }
  return out.filter(e => {
    const k = e.host + '|' + e.method + '|' + e.path;
    return e._cluster || !hidden.has(k);
  });
}

export function mapVisibleEps(eps){
  const clustered = mapAssignClusters(eps);
  const out = [];
  for(const e of clustered){
    out.push(e);
    if(e._cluster && mapState.expandedClusters.has(e._cluster.key)){
      e._cluster.members.slice(1).forEach(m => out.push({ ...m, _clusterChild: true }));
    }
  }
  return out;
}

export function mapCount(node){
  if(node._count != null) return node._count;
  let n = node.eps.length;
  node.kids.forEach(k => { n += mapCount(k); });
  node._count = n;
  return n;
}

// Memoized: tree structure depends on filters + clustering, not the search term.
let _btKey = '', _btCache = null;
export function buildMapTree(eps){
  const key = mapState._dataVersion + '|' + mapState.domain + '|' + mapState.method + '|' + mapState.statusClass + '|' + mapState.collapseIdentical + '|' + eps.length;
  if(key === _btKey && _btCache) return _btCache;
  const hosts = new Map();
  eps.forEach(e => {
    if(!hosts.has(e.host)) hosts.set(e.host, { name: e.host, key: '/'+e.host, kids: new Map(), eps: [], _count: null });
    let node = hosts.get(e.host);
    let pathKey = '/'+e.host;
    (e.path || '/').split('?')[0].split('/').filter(Boolean).forEach(seg => {
      pathKey += '/'+seg;
      if(!node.kids.has(seg)) node.kids.set(seg, { name: seg, key: pathKey, kids: new Map(), eps: [], _count: null });
      node = node.kids.get(seg);
    });
    node.eps.push(e);
  });
  _btKey = key; _btCache = hosts;
  return hosts;
}

export function findMapTreeNode(key){
  if(!key||!mapState._treeHosts) return null;
  const parts = key.replace(/^\//,'').split('/').filter(Boolean);
  if(!parts.length) return null;
  let node = mapState._treeHosts.get(parts[0]);
  for(let i = 1; i < parts.length && node; i++) node = node.kids.get(parts[i]);
  return node;
}

function epOrClusterMatchesSearch(e, q){
  if(epMatchesSearch(e, q)) return true;
  if(e._cluster) return e._cluster.members.some(m => epMatchesSearch(m, q));
  return false;
}

function mapExpandClustersForSearch(eps){
  const q = mapState.search;
  if(!q || !mapState.collapseIdentical) return;
  for(const e of mapAssignClusters(eps)){
    if(e._cluster && epOrClusterMatchesSearch(e, q)) mapState.expandedClusters.add(e._cluster.key);
  }
}

function wireMapEpRows(root){
  root.querySelectorAll('.map-ep[data-flow]').forEach(el => keyClick(el, () => flowPopup(Number(el.dataset.flow))));
  root.querySelectorAll('.map-cluster-badge').forEach(btn => {
    btn.onclick = ev => {
      ev.stopPropagation();
      const k = btn.dataset.cluster;
      if(mapState.expandedClusters.has(k)) mapState.expandedClusters.delete(k);
      else mapState.expandedClusters.add(k);
      renderMap();
    };
  });
}

export function mapEpRow(e, dim){
  const sts = (e.statuses || []).map(s => `<span style="color:${statusColor(s)}">${s}</span>`).join(' ');
  const path = e.path || '/';
  const q = mapState.search;
  const hit = q && epOrClusterMatchesSearch(e, q);
  let clusterBadge = '';
  if(e._cluster && !e._clusterChild){
    const label = e._cluster.kind === 'soft404' ? 'soft-404' : 'identical';
    const extra = e._cluster.count - 1;
    const expanded = mapState.expandedClusters.has(e._cluster.key);
    clusterBadge = `<button type="button" class="map-cluster-badge" data-cluster="${escAttr(e._cluster.key)}" title="${extra} endpoint${extra === 1 ? '' : 's'} with ${label === 'soft-404' ? 'a soft-404 (200 OK but not-found content)' : 'the same response body'} — click to ${expanded ? 'collapse' : 'expand'}">${label === 'soft-404' ? 'soft-404' : '⚡'} +${extra}</button>`;
  }
  const childCls = e._clusterChild ? ' map-cluster-child' : '';
  return `<div class="map-ep${dim && !hit ? ' map-dim' : ''}${hit ? ' map-hit' : ''}${childCls}${e.soft404 && !e._cluster ? ' map-soft404' : ''}"${e.lastFlowId ? ` data-flow="${e.lastFlowId}"` : ''} title="${escAttr(e.method+' '+(e.scheme||'http')+'://'+e.host+path)}">
    <span class="map-m" style="color:${methodColor(e.method)}">${esc(e.method)}</span>
    <span class="map-p">${esc(path)}</span>${clusterBadge}<span class="map-sts">${sts}</span>
    <span class="map-hits">${e.hits > 1 ? e.hits+'×' : ''}</span></div>`;
}

export function mapRenderNode(node, open, dim, lazy=false){
  let html = '';
  [...node.kids.values()].sort((a, b) => a.name.localeCompare(b.name)).forEach(kid => {
    const summary = `<summary><span class="map-seg">/${esc(kid.name)}</span><span class="map-c">${mapCount(kid)}</span></summary>`;
    if(lazy && !open){
      html += `<details class="map-folder">${summary}<div class="map-body" data-lazy-key="${escAttr(kid.key)}"></div></details>`;
    }else{
      html += `<details class="map-folder"${open ? ' open' : ''}>${summary}<div class="map-body">${mapRenderNode(kid, open, dim, lazy && !open)}</div></details>`;
    }
  });
  node.eps.slice().sort((a, b) => a.method.localeCompare(b.method)).forEach(e => html += mapEpRow(e, dim));
  return html;
}

function mapTreeLazyEnabled(eps){
  return eps.length > 350 && !mapState.expandAll && !mapState.search;
}

function hydrateMapTreeNode(body){
  const key = body.dataset.lazyKey;
  if(!key) return;
  const node = findMapTreeNode(key);
  if(!node){ body.innerHTML = ''; body.removeAttribute('data-lazy-key'); return; }
  const open = mapState.expandAll || !!mapState.search;
  body.innerHTML = mapRenderNode(node, open, !!mapState.search, mapTreeLazyEnabled(mapFiltered()));
  body.removeAttribute('data-lazy-key');
  wireMapEpRows(body);
}

function mapHintText(){
  if(mapState.view === 'params') return 'Parameter names mined from captured traffic — click a row to inspect the sample flow';
  if(mapState.view === 'table') return 'Sortable endpoint list · click a row to inspect · → Rep sends to Repeater';
  if(mapState.view === 'graph') return 'Images/fonts/media hidden · drag to pan · scroll to zoom · click folder to expand · double-click host to focus';
  return 'Hierarchical site map · click an endpoint to inspect';
}

function setMapView(v){
  mapState.view = v;
  if(v === 'graph') mapState._forceGraph = false; // re-evaluate the node cap each time Graph is chosen
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
  box.querySelectorAll('.map-param-row[data-flow]').forEach(tr=>keyClick(tr,()=>flowPopup(Number(tr.dataset.flow))));
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

function mapPerfNote(eps){
  const parts = [];
  if(mapState.truncated && mapState.total > mapState.eps.length){
    parts.push(`Showing first ${mapState.eps.length.toLocaleString()} of ${mapState.total.toLocaleString()} endpoints — filter by domain, tag, or search`);
  }
  if(eps.length > MAP_TREE_EAGER_MAX && mapState.view === 'tree'){
    parts.push(`${eps.length.toLocaleString()} endpoints — Tree is slow at this size; try Table view or filter by domain`);
  }
  return parts.join(' · ');
}

export function renderMap(){
  if(mapState.view === 'params') return;
  const filtered = mapFiltered();
  const eps = mapVisibleEps(filtered);
  const hostN = new Set(eps.map(e => e.host)).size;
  const hasFilters = !!(mapState.search || mapState.method || mapState.statusClass || mapState.domain);
  let countText = eps.length
    ? `${eps.length.toLocaleString()} endpoint${eps.length === 1 ? '' : 's'} · ${hostN} host${hostN === 1 ? '' : 's'}`
    : (mapState.eps.length ? (hasFilters ? 'No endpoints match the filters' : 'No endpoints') : 'No endpoints captured yet');
  if(mapState.truncated && mapState.total > mapState.eps.length) countText += ` (${mapState.total.toLocaleString()} total)`;
  $('#mapCount').textContent = countText;
  const warn = $('#mapWarn');
  const perf = mapPerfNote(eps);
  if(warn && mapState.view !== 'graph'){
    if(perf){
      warn.style.display = 'block';
      warn.textContent = perf;
    }else if(mapState.searchNote){
      warn.style.display = 'block';
      warn.textContent = mapState.searchNote;
    }else if(mapUsesServerSearch() && (mapState.searchScope === 'body' || mapState.searchScope === 'all') && mapState.view !== 'graph'){
      warn.style.display = 'block';
      warn.textContent = 'Body search scans stored bodies (content-deduped, latest 8000 flows max). Filter by domain to narrow.';
    }else if(warn && mapState.view !== 'graph'){
      warn.style.display = 'none';
      warn.textContent = '';
    }
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
    mapState._treeHosts = null;
    return;
  }
  if(eps.length > MAP_TREE_EAGER_MAX && (mapState.expandAll || mapState.search)){
    box.innerHTML = `<div class="hint" style="padding:16px;line-height:1.6">Too many endpoints (${eps.length.toLocaleString()}) to render expanded — switch to <b>Table</b> view, filter by domain, or narrow your search.</div>`;
    mapState._treeHosts = null;
    return;
  }
  mapState._treeHosts = buildMapTree(eps);
  const open = mapState.expandAll || !!mapState.search;
  const dim = !!mapState.search;
  const lazy = mapTreeLazyEnabled(eps);
  box.innerHTML = [...mapState._treeHosts.values()].sort((a, b) => a.name.localeCompare(b.name)).map(h => {
    if(lazy){
      return `<details class="map-host"><summary>🌐 ${esc(h.name)}<span class="map-c">${mapCount(h)}</span></summary><div class="map-body" data-lazy-key="${escAttr(h.key)}"></div></details>`;
    }
    return `<details class="map-host" open><summary>🌐 ${esc(h.name)}<span class="map-c">${mapCount(h)}</span></summary><div class="map-body">${mapRenderNode(h, open, dim, false)}</div></details>`;
  }).join('');
  wireMapEpRows(box);
}

function mapSortEps(eps){
  const k = mapState.sort.key, dir = mapState.sort.dir;
  const val = e => k === 'hits' ? (e.hits || 0) : k === 'status' ? (e.lastStatus || 0) : k === 'method' ? e.method : k === 'host' ? e.host : (e.path || '');
  return eps.slice().sort((a, b) => {
    const x = val(a), y = val(b);
    return (x > y ? 1 : x < y ? -1 : 0) * dir;
  });
}

function mapTableRow(e, showHost){
  const path = e.path || '/';
  const sts = (e.statuses || []).map(s => `<span style="color:${statusColor(s)}">${s}</span>`).join(' ');
  const q = mapState.search;
  const hit = q && epOrClusterMatchesSearch(e, q);
  let clusterCell = '';
  if(e._cluster && !e._clusterChild){
    const extra = e._cluster.count - 1;
    const label = e._cluster.kind === 'soft404' ? 'soft-404' : 'identical';
    clusterCell = ` <span class="map-cluster-badge-static" title="${extra} with ${label}">${label === 'soft-404' ? 'soft-404' : '⚡'} +${extra}</span>`;
  }
  return `<tr data-flow="${e.lastFlowId || ''}" class="${hit ? 'map-hit-row' : ''}${e._clusterChild ? ' map-cluster-child' : ''}">
    ${showHost ? `<td style="font-family:var(--mono);font-size:11px">${esc(e.host)}</td>` : ''}
    <td class="map-tbl-m" style="color:${methodColor(e.method)}">${esc(e.method)}</td>
    <td class="map-tbl-p" title="${escAttr(path)}">${esc(path)}${clusterCell}</td>
    <td class="map-tbl-sts">${sts || '—'}</td>
    <td style="text-align:right;color:var(--fg3)">${e.hits > 1 ? e.hits+'×' : ''}</td>
    <td class="map-tbl-act">${e.lastFlowId ? `<button class="btn" data-rep="${e.lastFlowId}" title="Send to Repeater">→ Rep</button>` : ''}</td>
  </tr>`;
}

function wireMapTableRows(box){
  box.querySelectorAll('tr[data-flow]').forEach(tr => {
    const id = Number(tr.dataset.flow);
    if(!id) return;
    keyClick(tr, ev => {
      if(ev.target.closest('[data-rep]')) return;
      flowPopup(id);
    });
  });
  box.querySelectorAll('[data-rep]').forEach(b => b.onclick = ev => {
    ev.stopPropagation();
    sendToRepeater({ id: Number(b.dataset.rep) });
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
  const head = `<thead><tr>
    ${showHost ? th('host', 'Host', '140px') : ''}
    ${th('method', 'Method', '72px')}
    ${th('path', 'Path', '')}
    ${th('status', 'Status', '88px')}
    ${th('hits', 'Hits', '52px')}
    <th style="width:72px"></th>
  </tr></thead>`;

  if(sorted.length >= MAP_TABLE_VIRTUAL_MIN){
    box.innerHTML = `<div class="map-virt"><table class="map-tbl map-virt-head">${head}</table><div class="map-virt-scroll"><div class="map-virt-spacer"></div><table class="map-tbl map-virt-body"><tbody></tbody></table></div></div>`;
    const scrollEl = box.querySelector('.map-virt-scroll');
    const bodyTbl = box.querySelector('.map-virt-body');
    const spacer = box.querySelector('.map-virt-spacer');
    spacer.style.height = (sorted.length * MAP_ROW_H) + 'px';
    let paintQueued = false;
    const paint = () => {
      paintQueued = false;
      const st = scrollEl.scrollTop;
      const vh = scrollEl.clientHeight || 400;
      const start = Math.max(0, Math.floor(st / MAP_ROW_H) - 15);
      const end = Math.min(sorted.length, Math.ceil((st + vh) / MAP_ROW_H) + 15);
      const tbody = bodyTbl.querySelector('tbody');
      tbody.innerHTML = sorted.slice(start, end).map(e => mapTableRow(e, showHost)).join('');
      bodyTbl.style.transform = `translateY(${start * MAP_ROW_H}px)`;
      wireMapTableRows(bodyTbl);
    };
    scrollEl.onscroll = () => { if(!paintQueued){ paintQueued = true; requestAnimationFrame(paint); } };
    paint();
  }else{
    const rows = sorted.map(e => mapTableRow(e, showHost)).join('');
    box.innerHTML = `<table class="map-tbl">${head}<tbody>${rows}</tbody></table>`;
    wireMapTableRows(box);
  }

  box.querySelectorAll('th[data-sort]').forEach(h => keyClick(h, () => {
    const k = h.dataset.sort;
    if(mapState.sort.key === k) mapState.sort.dir *= -1;
    else{ mapState.sort.key = k; mapState.sort.dir = 1; }
    renderMap();
  }));
}

let mapSearchTimer = null;
function mapApplySearch(){
  if(mapUsesServerSearch()){
    loadEndpoints();
    return;
  }
  mapState.searchNote = '';
  const filtered = mapFiltered();
  if(mapState.search){
    mapExpandForSearch(filtered);
    mapExpandClustersForSearch(filtered);
  }
  mapState._needFit = true;
  renderMap();
}
$('#mapSearch') && ($('#mapSearch').oninput = e => {
  mapState.search = e.target.value.trim();
  clearTimeout(mapSearchTimer);
  mapSearchTimer = setTimeout(mapApplySearch, mapUsesServerSearch() ? 350 : 280);
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
  const eps = mapFiltered();
  if(!mapState.expandAll && eps.length > MAP_TREE_EAGER_MAX){
    toast(`Too many endpoints (${eps.length.toLocaleString()}) — filter by domain or search first`);
    return;
  }
  mapState.expandAll = !mapState.expandAll;
  $('#mapExpand').textContent = mapState.expandAll ? 'Collapse all' : 'Expand all';
  mapState._needFit = true;
  renderMap();
};
$('#mapStatus') && ($('#mapStatus').onchange = e => { mapState.statusClass = Number(e.target.value) || 0; mapState._needFit = true; renderMap(); });
function syncMapHideNoise(){
  const b=$('#mapHideNoise'); if(!b) return;
  b.classList.toggle('on',!!mapState.hideNoise);
  b.setAttribute('aria-pressed',mapState.hideNoise?'true':'false');
  b.textContent=mapState.hideNoise?'Hiding 403/404-only':'Showing all statuses';
}
function syncMapCollapseIdentical(){
  const b=$('#mapCollapseIdentical'); if(!b) return;
  b.classList.toggle('on',!!mapState.collapseIdentical);
  b.setAttribute('aria-pressed',mapState.collapseIdentical?'true':'false');
  b.textContent=mapState.collapseIdentical?'Collapsing identical':'Showing every path';
}
syncMapHideNoise();
syncMapCollapseIdentical();
$('#mapHideNoise')&&($('#mapHideNoise').onclick=()=>{
  mapState.hideNoise=!mapState.hideNoise;
  try{localStorage.setItem(MAP_HIDE_NOISE_KEY,mapState.hideNoise?'1':'0');}catch(e){}
  syncMapHideNoise();
  loadEndpoints();
});
$('#mapCollapseIdentical')&&($('#mapCollapseIdentical').onclick=()=>{
  mapState.collapseIdentical=!mapState.collapseIdentical;
  mapState.expandedClusters.clear();
  try{localStorage.setItem(MAP_COLLAPSE_IDENTICAL_KEY,mapState.collapseIdentical?'1':'0');}catch(e){}
  syncMapCollapseIdentical();
  mapState._needFit = true;
  renderMap();
});
// Tag is a server-side filter (changes which endpoints come back) — re-fetch.
$('#mapTag') && ($('#mapTag').onchange = e => { mapState.tag = e.target.value; mapState.domain = null; mapState._needFit = true; loadEndpoints(); });

/* ---- map: node-link graph ---- */
export function gTrunc(s, n){ return s.length > n ? s.slice(0, n - 1) + '…' : s; }
export function gCount(n){
  if(n._gCount != null) return n._gCount;
  if(n.type === 'ep'){ n._gCount = 1; return 1; }
  let c = 0;
  n.children.forEach(k => { c += gCount(k); });
  n._gCount = c;
  return c;
}

let _gtKey = '', _gtCache = null;
export function buildGraphTree(eps){
  eps = graphEps(eps);
  const key = mapState._dataVersion + '|' + mapState.domain + '|' + mapState.method + '|' + mapState.statusClass + '|' + mapState.collapseIdentical + '|' + eps.length;
  if(key === _gtKey && _gtCache) return _gtCache;
  const root = { key: '', type: 'root', children: [], cm: new Map(), _gCount: null };
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
  root.children = root.children.filter(c => pruneGraphNode(c));
  _gtKey = key; _gtCache = root;
  return root;
}

function mapExpandForSearch(eps){
  const q = mapState.search.toLowerCase();
  if(!q) return;
  const root = buildGraphTree(eps);
  // Single bottom-up pass: a node "has a match" if it's a matching endpoint or any
  // descendant matches. Uncollapse every ancestor of a match so it's visible. This
  // is O(N) — the old code recomputed a full subtree search at every node (O(N²)).
  function mark(n){
    let has;
    if(n.type === 'ep'){
      has = epMatchesSearch(n.ep, q);
    } else {
      has = false;
      for(const c of n.children){ if(mark(c)) has = true; }
    }
    if(has) mapState.collapsed.delete(n.key);
    return has;
  }
  root.children.forEach(mark);
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
  if(lay.nodes.length > GRAPH_NODE_MAX && !mapState._forceGraph){
    g.removeAttribute('transform');
    g.innerHTML = `<text class="g-dim" x="20" y="28">Graph has ${lay.nodes.length.toLocaleString()} nodes — too dense to read.</text>`;
    if(warn){
      warn.style.display = 'block';
      warn.innerHTML = `Graph capped at ${GRAPH_NODE_MAX} nodes (${lay.nodes.length.toLocaleString()} match). Filter by domain or use Table view — or <a href="#" id="mapGraphForce" style="color:var(--accent);text-decoration:underline">show graph anyway</a>.`;
      const force = $('#mapGraphForce');
      if(force) force.onclick = ev => { ev.preventDefault(); mapState._forceGraph = true; warn.style.display = 'none'; renderMapGraph(eps); };
    }
    return;
  }
  if(warn){
    if(mapState.searchNote){
      warn.style.display = 'block';
      warn.textContent = mapState.searchNote;
    }else if(lay.nodes.length > GRAPH_NODE_MAX * 0.75){
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

{const tree=$('#mapTree');if(tree)tree.addEventListener('toggle',e=>{
  const det=e.target;
  if(det.tagName!=='DETAILS'||!det.open)return;
  const body=det.querySelector(':scope > .map-body[data-lazy-key]');
  if(body)hydrateMapTreeNode(body);
},true);}
