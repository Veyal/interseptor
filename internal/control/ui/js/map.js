import { $, esc, escAttr, state, toast, api, copyText, methodColor, statusColor, statusText, fmtSize, fmtDur, renderLoadError, projectStorageKey, openFlow, getHook, openCtxMenu, icon } from './core.js';
import { linkedIndex, endpointLinked, coveragePips, coverageSummary, authState } from './map-coverage.js';
import { renderState } from './statepanel.js';
import { sendToRepeater } from './tools.js';
import { animateOnce, cancelElementAnimations, MOTION } from './motion.js';

// keyClick promotes a click-only element to a keyboard-operable control (role +
// tabindex + Enter/Space) so endpoints/rows/sorts are reachable without a mouse.
function keyClick(el, fn, preserveRole=false){
  if(!el) return;
  // Keep native table row/header semantics when wiring keyboard activation.
  // Non-table divs still receive button semantics for assistive technology.
  if(!preserveRole)el.setAttribute('role','button');
  el.tabIndex=0;
  el.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();fn(e);}});
  el.onclick=fn;
}
// wireRovingGroup turns a set of keyClick-wired sibling rows into a single shared
// Tab stop, with Arrow/Home/End moving focus among them (roving tabindex) —
// dense hosts would otherwise turn every row into its own Tab stop. Call after
// keyClick, which unconditionally sets tabIndex=0 on each row.
function wireRovingGroup(elsLike){
  const els=[...elsLike];
  els.forEach((el,i)=>{
    el.tabIndex=i===0?0:-1;
    el.addEventListener('keydown',e=>{
      if(!['ArrowDown','ArrowUp','Home','End'].includes(e.key))return;
      e.preventDefault();
      const idx=els.indexOf(el);
      const next=e.key==='ArrowDown'?(idx+1)%els.length:e.key==='ArrowUp'?(idx-1+els.length)%els.length:e.key==='Home'?0:els.length-1;
      el.tabIndex=-1;els[next].tabIndex=0;els[next].focus();
    });
  });
}
import { flowPopup } from './flowmodal.js';

function labelMapControls() {
  const labels = {
    '#mapDomain': 'Map domain', '#mapSearch': 'Search site map',
    '#mapSearchScope': 'Map search scope', '#mapMethod': 'Map method',
    '#mapStatus': 'Map status', '#mapTag': 'Map tag',
  };
  Object.entries(labels).forEach(([sel, label]) => {
    const el = $(sel); if (el && !el.getAttribute('aria-label')) el.setAttribute('aria-label', label);
  });
}

const GRAPH_NODE_MAX = 200;
const MAP_TREE_EAGER_MAX = 2500;
const MAP_TABLE_VIRTUAL_MIN = 400;
const MAP_ROW_H = 28;
const MAP_VIEW_KEY = 'mapView';
const MAP_HIDE_NOISE_KEY = 'mapHideNoise';
const MAP_COLLAPSE_IDENTICAL_KEY = 'mapCollapseIdentical';
const MAP_DOMAIN_KEY = 'mapDomain';
let loadEndpointsEpoch = 0;
let loadParamsEpoch = 0;
let mapEndpointDataMode='unknown';
let mapEndpointRequestMode='full';
let loadEndpointsPending=false;

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
    const v = localStorage.getItem(projectStorageKey(MAP_HIDE_NOISE_KEY));
    if(v === '0') return false;
  }catch(e){}
  return true;
}

function restoreMapCollapseIdentical(){
  try{
    if(localStorage.getItem(projectStorageKey(MAP_COLLAPSE_IDENTICAL_KEY)) === '0') return false;
  }catch(e){}
  return true;
}
function restoreMapDomain(){
  try{return localStorage.getItem(projectStorageKey(MAP_DOMAIN_KEY))||'';}catch(e){return '';}
}

/* ---- endpoint map ---- */
function restoreMapView(){
  try{
    const v = localStorage.getItem(projectStorageKey(MAP_VIEW_KEY));
    if(v === 'tree' || v === 'table' || v === 'graph' || v === 'params') return v;
  }catch(e){}
  // Phones default to the list (table) view; the tree needs width to read.
  try{if(window.matchMedia('(max-width: 720px)').matches) return 'table';}catch(e){}
  return 'tree';
}

export const mapState = {
  eps: [], total: 0, truncated: false, domain: restoreMapDomain(), method: '', search: '', searchScope: 'path', searchNote: '', tag: '',
  statusClass: 0, hideNoise: restoreMapHideNoise(), noiseHiddenCount: 0, collapseIdentical: restoreMapCollapseIdentical(), expandAll: false,
  view: restoreMapView(), collapsed: new Set(), expandedClusters: new Set(), searchExpandedClusters: new Set(), zoom: { k: 1, x: 12, y: 12 }, _needFit: true,
  sort: { key: 'path', dir: 1 }, linked: null, onlyUnlinked: false, _linkVersion: 0, _treeHosts: null, _treeSeenHosts: new Set(), _dataVersion: 0, selectedNodeKey: '', _animateNextFit: false,
};

function mapUsesServerSearch(){
  return mapState.searchScope !== 'path' && mapState.search.trim().length > 0;
}

function mapFilterSignature(source=mapState){
  return JSON.stringify([
    source.domain||'',source.tag||'',source.method||'',Number(source.statusClass)||0,
    String(source.search||'').trim(),source.searchScope||'path',!!source.hideNoise,
  ]);
}

function invalidateEndpointLoad(){
  if(!loadEndpointsPending)return;
  loadEndpointsEpoch++;
  loadEndpointsPending=false;
}

function setMapSearchState(search,scope=mapState.searchScope){
  const nextSearch=String(search||'').trim(),nextScope=scope||'path';
  if(nextSearch===mapState.search&&nextScope===mapState.searchScope)return false;
  const wasServer=mapUsesServerSearch();
  mapState.search=nextSearch;
  mapState.searchScope=nextScope;
  const nextServer=mapUsesServerSearch();
  if(loadEndpointsPending&&(wasServer||nextServer))invalidateEndpointLoad();
  return true;
}

export function focusMapSearch(term, scope='body'){
  term=String(term||'').trim();
  if(!term){ toast('nothing to search'); return; }
  document.querySelector('.tab[data-tab="map"]')?.click();
  mapState.domain='';
  setMapSearchState(term,scope||'body');
  mapState.view='table';
  setMapView('table');
  const dom=$('#mapDomain'), sr=$('#mapSearch'), sc=$('#mapSearchScope');
  if(dom) dom.value='';
  if(sr) sr.value=term;
  if(sc) sc.value=mapState.searchScope;
}

export async function loadEndpoints(){
  const request={
    serverSearch:mapUsesServerSearch(),domain:mapState.domain,search:mapState.search.trim(),searchScope:mapState.searchScope,
    tag:mapState.tag,hideNoise:mapState.hideNoise,method:mapState.method,statusClass:mapState.statusClass,
  };
  const serverSearch=request.serverSearch;
  const requestFilterSignature=mapFilterSignature(request);
  const epoch = ++loadEndpointsEpoch;
  loadEndpointsPending=true;
  mapEndpointRequestMode=serverSearch?'server':'full';
  const warn=$('#mapWarn');
  if(warn&&serverSearch){warn.style.display='block';warn.textContent='Searching bodies…';}
  const params = new URLSearchParams();
  if(request.tag) params.set('tag', request.tag);
  if(!request.hideNoise) params.set('hideNoise', '0');
  if(serverSearch){
    // Scope a body/header search to the selected host — but NEVER host-filter a
    // plain load. The domain dropdown is populated from the fetched endpoints
    // (fillMapDomains), so fetching only one host would collapse the selector to
    // that single domain and hide every other host (leaving the user unable to
    // switch domains). Plain domain filtering is applied client-side in mapFiltered.
    if(request.domain) params.set('host', request.domain);
    params.set('search', request.search);
    params.set('searchScope', request.searchScope);
  }
  const q = params.toString();
  if(warn){warn.style.display='block';warn.textContent='Loading Map…';}
  if(!mapState.eps.length) showMapSkeleton();
  try{
    const d = await api('/api/endpoints' + (q ? '?' + q : ''));
    if (epoch !== loadEndpointsEpoch) return;
    const endpoints=d.endpoints||[];
    let noiseHiddenCount=0;
    // The default noise filter is server-side. When it produces an empty map,
    // make the empty state distinguish "no capture" from "all paths were only
    // 403/404" with one bounded diagnostic request. Only run this when the
    // remaining client-side filters cannot make the count misleading.
    if(request.hideNoise&&!endpoints.length&&!request.search&&!request.method&&!request.statusClass){
      const allQ = new URLSearchParams({hideNoise:'0'});
      if(request.domain) allQ.set('host',request.domain);
      if(request.tag) allQ.set('tag',request.tag);
      try{
        const all=await api('/api/endpoints?'+allQ.toString());
        if (epoch !== loadEndpointsEpoch) return;
        if(requestFilterSignature===mapFilterSignature())
          noiseHiddenCount=all.total!=null?all.total:(all.endpoints||[]).length;
      }catch(e){/* diagnostic only; preserve the primary map result */}
    }
    if (epoch !== loadEndpointsEpoch) return;
    mapState.eps=endpoints;
    mapState.total=d.total!=null?d.total:endpoints.length;
    mapState.truncated=!!d.truncated;
    mapState.searchNote=d.searchNote||'';
    mapState.noiseHiddenCount=noiseHiddenCount;
    mapEndpointDataMode=serverSearch?'server':'full';
    mapState._dataVersion++;
    mapState._needFit = true;
    fillMapDomains(mapState.noiseHiddenCount>0?mapState.domain:'');
    fillMapMethods();
    fillMapTags();
    renderMap();
    void loadMapLinks();
  }catch(e){
    if(epoch === loadEndpointsEpoch){
      // With nothing loaded yet, a StatePanel with Retry replaces the empty tree;
      // with data on screen the shared inline error keeps the last good view.
      if(mapState.eps.length) renderLoadError(warn,'Map',e,loadEndpoints,true);
      else renderMapLoadError(e);
    }
  }finally{
    if(epoch===loadEndpointsEpoch){
      loadEndpointsPending=false;
      if(warn&&warn.textContent==='Loading Map…'){warn.style.display='none';warn.textContent='';}
    }
  }
}

// First load: skeleton rows in the active view's host, so the panel never looks
// empty while the lazy module and the first request are in flight.
function mapViewHost(){
  return mapState.view === 'table' ? $('#mapTable') : mapState.view === 'params' ? $('#mapParams') : $('#mapTree');
}
function showMapSkeleton(){
  const host = mapViewHost();
  if(host && mapState.view !== 'graph') renderState(host, 'loading', { rows: 6, title: 'Loading Map' });
}
function renderMapLoadError(e){
  const host = mapViewHost();
  if(!host) return;
  renderState(host, 'error', { title: 'Could not load the Map', status: e && e.status, message: e && e.message, onRetry: loadEndpoints });
}

let _fdKey = -1, _fdPreserved = '', _fdHtml = '';
export function fillMapDomains(preserveMissing=''){
  const sel = $('#mapDomain'); if(!sel) return;
  // Rebuild the (potentially thousands-of-options) host <select> only when the
  // dataset actually changes — successive re-fetches with the same hosts reuse it.
  if(mapState._dataVersion !== _fdKey || preserveMissing !== _fdPreserved){
    const counts = {};
    mapState.eps.forEach(e => { counts[e.host] = (counts[e.host] || 0) + 1; });
    const hosts = Object.keys(counts).sort((a, b) => counts[b] - counts[a] || a.localeCompare(b));
    if(mapState.domain && !counts[mapState.domain] && mapState.domain !== preserveMissing) mapState.domain = '';
    const preserved = preserveMissing && !counts[preserveMissing]
      ? `<option value="${escAttr(preserveMissing)}">${esc(preserveMissing)} (${mapState.noiseHiddenCount} hidden)</option>` : '';
    _fdHtml = `<option value="">All domains (${mapState.eps.length})</option>`
      + preserved + hosts.map(h => `<option value="${escAttr(h)}">${esc(h)} (${counts[h]})</option>`).join('');
    _fdKey = mapState._dataVersion;
    _fdPreserved = preserveMissing;
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
  const key = mapState._dataVersion + '|' + (mapState.domain || '') + '|' + mapState.method + '|' + mapState.statusClass + '|' + mapState.eps.length + '|' + mapState.onlyUnlinked + '|' + mapState._linkVersion;
  if(key === _mfKey && _mfCache) return _mfCache;
  // Client-side search is a marking pass only (dims non-matches) — not a filter —
  // so buildMapTree's memo stays valid while typing.
  const out = mapState.eps.filter(e => {
    if(mapState.domain && e.host !== mapState.domain) return false;
    if(mapState.method && e.method !== mapState.method) return false;
    if(mapState.statusClass && Math.floor((e.lastStatus || 0) / 100) !== mapState.statusClass) return false;
    if(mapState.onlyUnlinked && mapState.linked && endpointLinked(e, mapState.linked)) return false;
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
  mapExpandClustersForSearch(clustered);
  const out = [];
  for(const e of clustered){
    out.push(e);
    if(e._cluster && (mapState.expandedClusters.has(e._cluster.key)||mapState.searchExpandedClusters.has(e._cluster.key))){
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

let _btKey = '', _btCache = null;
function mapTreeExpansionSignature(){
  return JSON.stringify([
    [...mapState.expandedClusters].sort(),
    [...mapState.searchExpandedClusters].sort(),
  ]);
}
export function buildMapTree(eps){
  const key = mapState._dataVersion + '|' + mapState.domain + '|' + mapState.method + '|' + mapState.statusClass + '|' + mapState.collapseIdentical + '|' + mapTreeExpansionSignature() + '|' + eps.length;
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
  mapState.searchExpandedClusters.clear();
  if(!q || !mapState.collapseIdentical) return;
  for(const e of eps){
    if(e._cluster && epOrClusterMatchesSearch(e, q)) mapState.searchExpandedClusters.add(e._cluster.key);
  }
}

function wireMapEpRows(root){
  const rows=root.querySelectorAll('.map-ep[data-flow]');
  rows.forEach(el => keyClick(el, () => openMapFlow(Number(el.dataset.flow))));
  wireRovingGroup(rows);
  root.querySelectorAll('.map-cluster-badge').forEach(btn => {
    btn.onclick = ev => {
      ev.stopPropagation();
      const k = btn.dataset.cluster;
      if(mapState.searchExpandedClusters.has(k))return;
      if(mapState.expandedClusters.has(k)) mapState.expandedClusters.delete(k);
      else mapState.expandedClusters.add(k);
      renderMap();
    };
  });
}

// Evidence-aware row decoration. Every part is derived from fields the API
// already returns (observed statuses, flows attached to findings); with no
// findings data loaded the linkage pip is omitted rather than shown as "none".
function mapCoverageHTML(e){
  const auth = authState(e);
  const authHTML = auth.kind === 'unknown' ? '' : `<span class="map-auth is-${auth.kind}" title="${auth.kind === 'auth' ? 'A 401 or 403 was observed for this endpoint' : 'Only successful or redirect statuses were observed'}">${icon(auth.icon)}<span>${esc(auth.label)}</span></span>`;
  const linked = coveragePips(e, mapState.linked).find(p => p.id === 'linked');
  const pipHTML = !linked ? '' : linked.on
    ? `<span class="map-pip is-on" title="${escAttr(linked.label)}">${icon('paperclip')}<span>Linked</span></span>`
    : `<span class="map-pip" title="${escAttr(linked.label)}">${icon('ring')}<span class="u-sr">${esc(linked.label)}</span></span>`;
  return authHTML || pipHTML ? `<span class="map-cov">${authHTML}${pipHTML}</span>` : '';
}

// One entry for opening an endpoint's latest flow: the Flow Drawer when it is
// registered, the legacy popup otherwise.
function openMapFlow(id){
  if(!id) return;
  if(!openFlow(id, { source: 'map' })) flowPopup(id);
}

function mapCtxFor(trigger){
  const id = Number(trigger.closest('[data-flow]')?.dataset.flow);
  if(!id) return null;
  const items = [
    { label: 'Open in flow view', icon: 'search', act: () => openMapFlow(id) },
    { label: 'Send to Repeater', icon: 'repeat', act: () => sendToRepeater({ id }) },
  ];
  const attach = getHook('attachEvidence');
  if(attach) items.push({ label: 'Attach to finding or create one', icon: 'paperclip', act: () => attach({ kind: 'flow', refs: [id] }, { anchor: trigger }) });
  return { id, sections: [{ head: 'ENDPOINT', items }] };
}

function wireMapContextMenus(){
  ['#mapTree', '#mapTable'].forEach(sel => {
    const box = $(sel);
    if(!box || box._mapCtxWired) return;
    box._mapCtxWired = true;
    box.addEventListener('contextmenu', ev => {
      const row = ev.target.closest && ev.target.closest('.map-ep[data-flow], tr[data-flow]');
      const ctx = row && mapCtxFor(row);
      if(!ctx) return;
      ev.preventDefault();
      ev.stopPropagation();
      const r = row.getBoundingClientRect();
      openCtxMenu(ev.clientX || r.left, ev.clientY || r.bottom, ctx.sections, null);
    });
  });
}

// Which flows are attached to findings (for coverage). Non-blocking: the Map is
// fully usable while this loads, and a failure simply hides the linkage pips.
let mapLinkEpoch = 0;
async function loadMapLinks(){
  const epoch = ++mapLinkEpoch;
  try{
    const d = await api('/api/findings');
    if(epoch !== mapLinkEpoch) return;
    mapState.linked = linkedIndex(d.findings || []);
  }catch(e){
    if(epoch !== mapLinkEpoch) return;
    mapState.linked = null;
  }
  mapState._linkVersion++;
  renderMap();
}

function renderMapCoverage(eps){
  const sum = coverageSummary(eps, mapState.linked);
  const el = $('#mapCoverage'); if(!el) return;
  el.hidden = !sum;
  if(!sum) return;
  el.textContent = sum.text;
  const btn = $('#mapUnlinked');
  if(btn){
    btn.hidden = false;
    btn.setAttribute('aria-pressed', mapState.onlyUnlinked ? 'true' : 'false');
    btn.classList.toggle('on', mapState.onlyUnlinked);
  }
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
    const searchExpanded=mapState.searchExpandedClusters.has(e._cluster.key);
    const expanded = searchExpanded||mapState.expandedClusters.has(e._cluster.key);
    const badgeTitle=searchExpanded?'Expanded to show current search matches':`${extra} endpoint${extra === 1 ? '' : 's'} with ${label === 'soft-404' ? 'a soft-404 (200 OK but not-found content)' : 'the same response body'} — click to ${expanded ? 'collapse' : 'expand'}`;
    const badgeLabel=searchExpanded?`Expanded ${extra} ${label} endpoint${extra === 1 ? '' : 's'} to show search matches`:`${expanded ? 'Collapse' : 'Expand'} ${extra} ${label} endpoint${extra === 1 ? '' : 's'}`;
    clusterBadge = `<button type="button" class="map-cluster-badge" data-cluster="${escAttr(e._cluster.key)}" title="${escAttr(badgeTitle)}" aria-label="${escAttr(badgeLabel)}"${searchExpanded?' disabled aria-disabled="true"':''}>${label === 'soft-404' ? 'soft-404' : '<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-bolt"/></svg>'} +${extra}</button>`;
  }
  const childCls = e._clusterChild ? ' map-cluster-child' : '';
  return `<div class="map-ep${dim && !hit ? ' map-dim' : ''}${hit ? ' map-hit' : ''}${childCls}${e.soft404 && !e._cluster ? ' map-soft404' : ''}"${e.lastFlowId ? ` data-flow="${e.lastFlowId}"` : ''} title="${escAttr(e.method+' '+(e.scheme||'http')+'://'+e.host+path)}">
    <span class="map-m" style="color:${methodColor(e.method)}">${esc(e.method)}</span>
    <span class="map-p">${esc(path)}</span>${clusterBadge}<span class="map-sts">${sts}</span>${mapCoverageHTML(e)}
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

function wireMapHostDetails(box){
  box.querySelectorAll('.map-host[data-host-key]').forEach(detail=>{
    detail.addEventListener('toggle',()=>{
      const key=detail.dataset.hostKey;
      if(!key)return;
      if(detail.open){
        mapState.collapsed.delete(key);
        const body=detail.querySelector(':scope > .map-body[data-lazy-key]');
        if(body)hydrateMapTreeNode(body);
      }else{
        mapState.collapsed.add(key);
        if(mapState.expandAll){
          mapState.expandAll=false;
          const button=$('#mapExpand');if(button)button.textContent='Expand all';
        }
      }
    });
  });
}

function setMapView(v){
  labelMapControls();
  if(v!=='params')loadParamsEpoch++;
  mapState.view = v;
  if(v === 'graph') mapState._forceGraph = false; // re-evaluate the node cap each time Graph is chosen
  try{localStorage.setItem(projectStorageKey(MAP_VIEW_KEY),v);}catch(e){}
  const seg = $('#mapViewSeg');
  if(seg) seg.querySelectorAll('button').forEach(x => { const on = x.dataset.v === v; x.classList.toggle('on', on); x.setAttribute('aria-pressed', on ? 'true' : 'false'); x.setAttribute('aria-label', 'Map view '+(x.dataset.v||'')); });
  const tree = $('#mapTree'), tbl = $('#mapTable'), wrap = $('#mapGraphWrap'), params = $('#mapParams');
  if(tree) tree.style.display = v === 'tree' ? 'block' : 'none';
  if(tbl) tbl.style.display = v === 'table' ? 'block' : 'none';
  if(wrap) wrap.style.display = v === 'graph' ? 'block' : 'none';
  if(params) params.style.display = v === 'params' ? 'block' : 'none';
  const exp = $('#mapExpand'), fit = $('#mapFit');
  if(exp) exp.style.display = v === 'tree' ? '' : 'none';
  if(fit) fit.style.display = v === 'graph' ? '' : 'none';
  mapState._needFit = true;
  if(v === 'params') loadParams();
  else mapApplySearch();
}

export async function loadParams(){
  const epoch = ++loadParamsEpoch;
  const warn=$('#mapWarn');
  if(warn){warn.style.display='block';warn.textContent='Mining parameters…';}
  try{
    const q = new URLSearchParams();
    if(mapState.domain) q.set('host', mapState.domain);
    q.set('inScope', '1');
    const d = await api('/api/params?' + q);
    if (epoch !== loadParamsEpoch) return;
    renderMapParams(d);
    if(warn) warn.style.display='none';
    const c=$('#mapCount');if(c)c.textContent=(d.flowsScanned||0)+' flows · param miner';
  }catch(e){
    // Parameter mining is an explicit user action; leave a retry in the panel
    // instead of reducing a failed request to a transient toast.
    if(epoch === loadParamsEpoch){
      if(warn)warn.setAttribute('aria-live','polite');
      renderLoadError(warn,'Parameters',e,loadParams,false);
      const retry=warn?.querySelector('[data-load-retry]');
      if(retry)retry.setAttribute('data-map-params-retry','');
    }
  }
}

function renderMapParams(d){
  const box=$('#mapParams'); if(!box) return;
  const hosts=d.hosts||[];
  if(!hosts.length){box.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-flask"/></svg></div><div class="state-empty-title">No parameters found</div><p class="state-empty-hint">Capture in-scope traffic with query strings or form/JSON bodies.</p></div>';return;}
  box.innerHTML=hosts.map(h=>`<div style="margin-bottom:16px">
    <div class="micro-label micro-label-accent" style="margin-bottom:6px">${esc(h.host)}</div>
    <table class="rules-tbl"><thead><tr><th>Name</th><th style="width:70px">Source</th><th style="width:50px">Hits</th><th style="width:90px">Sample</th></tr></thead><tbody>
    ${(h.params||[]).map(p=>`<tr class="map-param-row" data-flow="${p.lastFlowId}" title="${escAttr(p.samplePath||'')}">
      <td style="font-family:var(--mono);color:var(--fg)">${esc(p.name)}</td>
      <td style="color:var(--fg3)">${esc(p.source)}</td>
      <td>${p.hits}</td>
      <td><button type="button" class="btn xs map-param-inspect" aria-label="Inspect flow #${p.lastFlowId} for parameter ${escAttr(p.name)}">#${p.lastFlowId}</button></td>
    </tr>`).join('')}
    </tbody></table></div>`).join('');
  box.querySelectorAll('.map-param-inspect').forEach(b=>{b.onclick=ev=>{ev.stopPropagation();const tr=b.closest('[data-flow]');if(tr)flowPopup(Number(tr.dataset.flow));};});
  box.querySelectorAll('tbody').forEach(tbody=>{
    const rows=tbody.querySelectorAll('.map-param-row[data-flow]');
    rows.forEach(tr=>keyClick(tr,()=>flowPopup(Number(tr.dataset.flow)),true));
    wireRovingGroup(rows);
  });
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
        try{localStorage.setItem(projectStorageKey(MAP_DOMAIN_KEY),'');}catch(e){}
        mapCollapseHosts();
      }else if(a.dataset.crumb === 'domain'){
        mapState.collapsed.clear();
      }
      mapState._needFit = true;
      refreshMapDomainSelection();
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
  // A skeleton or error panel from the loading phase must not leave busy state behind.
  ['#mapTree', '#mapTable', '#mapParams'].forEach(sel => { const h = $(sel); if(h){ h.removeAttribute('aria-busy'); delete h.dataset.state; } });
  if(mapState.view === 'params') return;
  const filtered = mapFiltered();
  const visible=mapVisibleEps(filtered);
  const eps=mapState.view==='graph'?mapGraphDisplayEps(visible):visible;
  const hostN = new Set(eps.map(e => e.host)).size;
  const hasFilters = !!(mapState.search || mapState.method || mapState.statusClass || mapState.domain);
  const hiddenByNoise = !eps.length && mapState.noiseHiddenCount > 0;
  let countText = eps.length
    ? `${eps.length.toLocaleString()} endpoint${eps.length === 1 ? '' : 's'} · ${hostN} host${hostN === 1 ? '' : 's'}`
    : hiddenByNoise
      ? `${mapState.noiseHiddenCount.toLocaleString()} endpoint${mapState.noiseHiddenCount === 1 ? '' : 's'} hidden by the 403/404 noise filter`
      : (mapState.eps.length ? (hasFilters ? 'No endpoints match the filters' : 'No endpoints') : 'No endpoints captured yet');
  if(mapState.truncated && mapState.total > mapState.eps.length) countText += ` (${mapState.total.toLocaleString()} total)`;
  $('#mapCount').textContent = countText;
  const warn = $('#mapWarn');
  const perf = mapPerfNote(eps);
  if(warn){
    if(hiddenByNoise){
      warn.style.display = 'block';
      warn.innerHTML = `${mapState.noiseHiddenCount.toLocaleString()} endpoint${mapState.noiseHiddenCount === 1 ? '' : 's'} hidden because they only returned 403/404. <button type="button" class="btn xs" id="mapShowNoise">Show all statuses</button>`;
      const show=$('#mapShowNoise');
      if(show)show.onclick=mapHiddenNoiseAction;
    }else if(mapState.view !== 'graph' && perf){
      warn.style.display = 'block';
      warn.textContent = perf;
    }else if(mapState.view !== 'graph' && mapState.searchNote){
      warn.style.display = 'block';
      warn.textContent = mapState.searchNote;
    }else if(mapState.view !== 'graph' && mapUsesServerSearch() && (mapState.searchScope === 'body' || mapState.searchScope === 'all')){
      warn.style.display = 'block';
      warn.textContent = 'Body search scans stored bodies (content-deduped, latest 8000 flows max). Filter by domain to narrow.';
    }else if(mapState.view !== 'graph'){
      warn.style.display = 'none';
      warn.textContent = '';
    }
  }
  renderMapCrumb(eps);
  renderMapCoverage(mapState.eps);
  if(mapState.view === 'graph') renderMapGraph(eps);
  else if(mapState.view === 'table') renderMapTable(eps);
  else renderMapTree(eps);
}

// Keep the empty-state recovery local: changing this filter should not reset
// the host/search selection or require a second navigation step.
function mapHiddenNoiseAction(){
  mapState.hideNoise=false;
  try{localStorage.setItem(projectStorageKey(MAP_HIDE_NOISE_KEY),'0');}catch(e){}
  syncMapHideNoise();
  loadEndpoints();
}

export function renderMapTree(eps){
  const box = $('#mapTree'); if(!box) return;
  if(!eps.length){
    box.innerHTML = '<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-map"/></svg></div><div class="state-empty-title">No endpoints match</div><p class="state-empty-hint">Capture traffic or relax the filters.</p></div>';
    mapState._treeHosts = null;
    return;
  }
  if(eps.length > MAP_TREE_EAGER_MAX && (mapState.expandAll || mapState.search)){
    box.innerHTML = `<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><div class="state-empty-title">Too many endpoints (${eps.length.toLocaleString()})</div><p class="state-empty-hint">Switch to <b>Table</b> view, filter by domain, or narrow your search to render expanded.</p></div>`;
    mapState._treeHosts = null;
    return;
  }
  mapState._treeHosts = buildMapTree(eps);
  const open = mapState.expandAll || !!mapState.search;
  const dim = !!mapState.search;
  const lazy = mapTreeLazyEnabled(eps);
  const hosts=[...mapState._treeHosts.values()].sort((a, b) => a.name.localeCompare(b.name));
  if(lazy)hosts.forEach(h=>{
    if(!mapState._treeSeenHosts.has(h.key)){
      mapState._treeSeenHosts.add(h.key);
      mapState.collapsed.add(h.key);
    }
  });
  box.innerHTML = hosts.map(h => {
    const hostOpen=open||!mapState.collapsed.has(h.key);
    if(lazy){
      return `<details class="map-host" data-host-key="${escAttr(h.key)}"${hostOpen ? ' open' : ''}><summary><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-globe"/></svg> ${esc(h.name)}<span class="map-c">${mapCount(h)}</span></summary><div class="map-body" data-lazy-key="${escAttr(h.key)}"></div></details>`;
    }
    return `<details class="map-host" data-host-key="${escAttr(h.key)}"${hostOpen ? ' open' : ''}><summary><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-globe"/></svg> ${esc(h.name)}<span class="map-c">${mapCount(h)}</span></summary><div class="map-body">${mapRenderNode(h, open, dim, false)}</div></details>`;
  }).join('');
  wireMapEpRows(box);
  wireMapHostDetails(box);
  box.querySelectorAll('.map-host[open] > .map-body[data-lazy-key]').forEach(hydrateMapTreeNode);
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
    clusterCell = ` <span class="map-cluster-badge-static" title="${extra} with ${label}">${label === 'soft-404' ? 'soft-404' : '<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-bolt"/></svg>'} +${extra}</span>`;
  }
  return `<tr data-flow="${e.lastFlowId || ''}" class="${hit ? 'map-hit-row' : ''}${e._clusterChild ? ' map-cluster-child' : ''}">
    ${showHost ? `<td style="font-family:var(--mono);font-size:var(--fs-xs)">${esc(e.host)}</td>` : ''}
    <td class="map-tbl-m" style="color:${methodColor(e.method)}">${esc(e.method)}</td>
    <td class="map-tbl-p" title="${escAttr(path)}">${esc(path)}${clusterCell}</td>
    <td class="map-tbl-sts">${sts || '—'}</td>
    <td style="text-align:right;color:var(--fg3)">${e.hits > 1 ? e.hits+'×' : ''}</td>
    <td class="map-tbl-cov">${mapCoverageHTML(e)}</td>
    <td class="map-tbl-act">${e.lastFlowId ? `<button class="btn" data-rep="${e.lastFlowId}" title="Send to Repeater">→ Rep</button>` : ''}</td>
  </tr>`;
}

function wireMapTableRows(box){
  const wired=[];
  box.querySelectorAll('tr[data-flow]').forEach(tr => {
    const id = Number(tr.dataset.flow);
    if(!id) return;
    keyClick(tr, ev => {
      if(ev.target.closest('[data-rep]')) return;
      openMapFlow(id);
    }, true);
    wired.push(tr);
  });
  wireRovingGroup(wired);
  box.querySelectorAll('[data-rep]').forEach(b => b.onclick = ev => {
    ev.stopPropagation();
    sendToRepeater({ id: Number(b.dataset.rep) });
  });
}

function renderMapTable(eps){
  const box = $('#mapTable'); if(!box) return;
  if(!eps.length){
    box.innerHTML = '<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-map"/></svg></div><div class="state-empty-title">No endpoints match</div><p class="state-empty-hint">Capture traffic or relax the filters.</p></div>';
    return;
  }
  const sorted = mapSortEps(eps);
  const showHost = !mapState.domain;
  const sk = mapState.sort.key, sd = mapState.sort.dir;
  const th = (k, label, w) => `<th class="${sk === k ? 'sorted' : ''}" data-sort="${k}" aria-sort="${sk === k ? (sd > 0 ? 'ascending' : 'descending') : 'none'}"${w ? ` style="width:${w}"` : ''}>${label}${sk === k ? (sd > 0 ? ' ▲' : ' ▼') : ''}</th>`;
  const head = `<thead><tr>
    ${showHost ? th('host', 'Host', '140px') : ''}
    ${th('method', 'Method', '72px')}
    ${th('path', 'Path', '')}
    ${th('status', 'Status', '88px')}
    ${th('hits', 'Hits', '52px')}
    <th class="map-th-evidence">Evidence</th>
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
  }, true));
}

let mapSearchTimer = null;
function mapApplySearch(){
  if(mapUsesServerSearch()||mapEndpointDataMode!=='full'||mapEndpointRequestMode==='server'){
    loadEndpoints();
    return;
  }
  mapState.searchNote = '';
  const filtered = mapFiltered();
  if(mapState.search){
    mapExpandForSearch(filtered);
  }
  mapState._needFit = true;
  renderMap();
}
$('#mapSearch') && ($('#mapSearch').oninput = e => {
  setMapSearchState(e.target.value,mapState.searchScope);
  clearTimeout(mapSearchTimer);
  mapSearchTimer = setTimeout(mapApplySearch, mapUsesServerSearch() ? 350 : 280);
});
$('#mapSearchScope') && ($('#mapSearchScope').onchange = e => {
  setMapSearchState(mapState.search,e.target.value||'path');
  mapApplySearch();
});
function refreshMapDomainSelection(){
  if(mapState.view==='params'){
    invalidateEndpointLoad();loadParams();return;
  }
  if(mapUsesServerSearch()||mapEndpointDataMode!=='full'||mapEndpointRequestMode==='server')loadEndpoints();
  else renderMap();
}
$('#mapDomain') && ($('#mapDomain').onchange = e => {
  mapState.domain = e.target.value;
  try{localStorage.setItem(projectStorageKey(MAP_DOMAIN_KEY),mapState.domain);}catch(e){}
  if(mapState.domain) mapState.collapsed.clear();
  else mapCollapseHosts();
  mapState._needFit = true;
  if($('#mapDiscoveryPanel')&&!$('#mapDiscoveryPanel').hidden) refreshMapDiscoveryPanel();
  refreshMapDomainSelection();
});
$('#mapMethod') && ($('#mapMethod').onchange = e => { mapState.method = e.target.value; mapState._needFit = true; renderMap(); });
$('#mapRefresh') && ($('#mapRefresh').onclick = loadEndpoints);
$('#mapUnlinked') && ($('#mapUnlinked').onclick = () => { mapState.onlyUnlinked = !mapState.onlyUnlinked; renderMap(); });
wireMapContextMenus();
$('#mapExpand').onclick = () => {
  const eps = mapFiltered();
  if(!mapState.expandAll && eps.length > MAP_TREE_EAGER_MAX){
    toast(`Too many endpoints (${eps.length.toLocaleString()}) — filter by domain or search first`);
    return;
  }
  mapState.expandAll = !mapState.expandAll;
  if(mapState.expandAll)mapState.collapsed.clear();
  else mapCollapseHosts();
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
  try{localStorage.setItem(projectStorageKey(MAP_HIDE_NOISE_KEY),mapState.hideNoise?'1':'0');}catch(e){}
  syncMapHideNoise();
  loadEndpoints();
});
function mapDiscoveryHost(){
  const d=(mapState.domain&&String(mapState.domain).trim())||'';
  if(d) return d;
  // Prefer the first host currently in the map inventory.
  const eps=mapState.eps||[];
  for(const e of eps){ if(e&&e.host) return e.host; }
  return '';
}
function mapDiscoveryCmds(){
  const proxy=state.proxyAddr||'127.0.0.1:8080';
  const host=mapDiscoveryHost();
  if(!host) return {ok:false, reason:'Select a domain in the Map filter (or capture traffic first).'};
  const base=host.includes('://')?host:('https://'+host);
  return {
    ok:true,
    ferox:`feroxbuster -u ${base} -p http://${proxy} --dont-extract-links -t 20`,
    ffuf:`ffuf -u ${base}/FUZZ -w wordlist.txt -x http://${proxy} -mc all -fc 404`,
  };
}
function refreshMapDiscoveryPanel(){
  const cmds=mapDiscoveryCmds();
  const f=$('#mapDscFerox'), u=$('#mapDscFfuf');
  if(!cmds.ok){
    if(f) f.textContent=cmds.reason;
    if(u) u.textContent='';
    return;
  }
  if(f) f.textContent=cmds.ferox;
  if(u) u.textContent=cmds.ffuf;
}
$('#mapDiscoveryHelp')&&($('#mapDiscoveryHelp').onclick=()=>{
  const p=$('#mapDiscoveryPanel'); if(!p) return;
  const show=p.hasAttribute('hidden')||p.style.display==='none';
  const button=$('#mapDiscoveryHelp');
  if(show){ p.hidden=false; p.style.display=''; refreshMapDiscoveryPanel(); button.textContent='Discovery ▾'; }
  else { p.hidden=true; p.style.display='none'; button.textContent='Discovery ▸'; }
  button.setAttribute('aria-expanded',show?'true':'false');
});
function copyMapDiscovery(which){
  const cmds=mapDiscoveryCmds();
  if(!cmds.ok){ toast(cmds.reason); refreshMapDiscoveryPanel(); return; }
  copyText(cmds[which], which==='ferox'?'ferox command copied':'ffuf command copied');
}
$('#mapDscCopyFerox')&&($('#mapDscCopyFerox').onclick=()=>copyMapDiscovery('ferox'));
$('#mapDscCopyFfuf')&&($('#mapDscCopyFfuf').onclick=()=>copyMapDiscovery('ffuf'));
$('#mapCollapseIdentical')&&($('#mapCollapseIdentical').onclick=()=>{
  mapState.collapseIdentical=!mapState.collapseIdentical;
  mapState.expandedClusters.clear();
  mapState.searchExpandedClusters.clear();
  try{localStorage.setItem(projectStorageKey(MAP_COLLAPSE_IDENTICAL_KEY),mapState.collapseIdentical?'1':'0');}catch(e){}
  syncMapCollapseIdentical();
  mapState._needFit = true;
  renderMap();
});
// Tag is a server-side filter (changes which endpoints come back) — re-fetch.
$('#mapTag') && ($('#mapTag').onchange = e => { mapState.tag = e.target.value; mapState._needFit = true; loadEndpoints(); });

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
  const key = mapState._dataVersion + '|' + mapState.domain + '|' + mapState.method + '|' + mapState.statusClass + '|' + mapState.collapseIdentical + '|' + mapState.searchScope + '|' + mapState.search + '|' + mapTreeExpansionSignature() + '|' + eps.length;
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
  const tipWidth=tip.offsetWidth||200,tipHeight=tip.offsetHeight||60;
  const localX=ev.clientX-wrap.left+12,localY=ev.clientY-wrap.top+12;
  const maxLeft=Math.max(8,wrap.width-tipWidth-8),maxTop=Math.max(8,wrap.height-tipHeight-8);
  tip.style.left=Math.max(8,Math.min(maxLeft,localX))+'px';
  tip.style.top=Math.max(8,Math.min(maxTop,localY))+'px';
}

function graphTipHide(){ const t = $('#mapGraphTip'); if(t) t.style.display = 'none'; }

const graphSnapshots=new Map();
let mapZoomEpoch=0;
function graphNodeSignature(n){
  const e=n.ep||{};
  return [n.type,n.label,n._col?'1':'0',gCount(n),e.lastStatus||0,e.hits||0,e.lastFlowId||0].join('|');
}
function graphEdgeKey(a,b){return a.key+'>'+b.key;}
function selectGraphNode(el){
  if(!el)return;
  mapState.selectedNodeKey=el.dataset.key||'';
  document.querySelectorAll('#mapGraphG .g-node').forEach(node=>{
    const on=node.dataset.key===mapState.selectedNodeKey;
    node.classList.toggle('g-selected',on);
    node.setAttribute('aria-selected',on?'true':'false');
    node.tabIndex=on?0:-1;
  });
}

export function gNode(n){
  const x = n.px, y = n.py;
  const match = graphNodeMatches(n);
  const dim = mapState.search && !match;
  const selected=n.key===mapState.selectedNodeKey;
  const cls = `g-node g-click${dim ? ' g-dimmed' : ''}${match && mapState.search ? ' g-match' : ''}${selected ? ' g-selected' : ''}`;
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
  const aria=n.type==='host'
    ?`${n.label}, host, ${gCount(n)} endpoints. Enter toggles; F focuses host.`
    :n.type==='ep'
      ?`${n.ep.method} ${n.ep.scheme||'http'}://${n.ep.host}${n.ep.path||'/'}, status ${n.ep.lastStatus||'unknown'}, ${n.ep.hits||0} hit${n.ep.hits===1?'':'s'}. Enter opens the latest flow.`
      :`${n.label}, group, ${gCount(n)} endpoints. Enter toggles.`;
  return `<g class="${cls}" data-key="${escAttr(n.key)}" data-kind="${n.type}" data-host="${n.type === 'host' ? escAttr(n.label) : ''}" role="option" tabindex="${selected?'0':'-1'}" aria-label="${escAttr(aria)}" aria-selected="${selected?'true':'false'}"${extra}><title>${title}</title>${hit}${mk}${lb}</g>`;
}

// Tree/table search keeps surrounding context and marks matches. A dense graph
// cannot do that safely: retaining thousands of dimmed nodes defeats both the
// readability cap and the graph's own "search to narrow" recovery guidance.
// Server-side header/body searches are already reduced before they reach here.
function mapGraphFiltered(eps){
  if(!mapState.search||mapUsesServerSearch())return eps;
  return eps.filter(ep=>epMatchesSearch(ep,mapState.search));
}
function mapGraphDisplayEps(eps){
  return graphEps(mapGraphFiltered(eps));
}
function graphCapSignature(){
  return [mapState.domain,mapState.method,mapState.statusClass,mapState.tag,mapState.hideNoise?'1':'0',mapState.collapseIdentical?'1':'0',mapState.search,mapState.searchScope].join('|');
}

export function renderMapGraph(eps){
  eps=mapGraphDisplayEps(eps);
  const g = $('#mapGraphG'); if(!g) return;
  const warn = $('#mapWarn');
  const capSignature=graphCapSignature();
  if(mapState._forceGraphSignature!==capSignature)mapState._forceGraph=false;
  const focusedKey=document.activeElement?.closest?.('#mapGraphG .g-node')?.dataset.key||'';
  graphTipHide();
  if(!eps.length){
    g.removeAttribute('transform');
    g.innerHTML = '<text class="g-dim" x="20" y="28">No endpoints match — relax the filters, clear the search, or Refresh.</text>';
    if(warn && !mapState.noiseHiddenCount) warn.style.display = 'none';
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
      if(force) force.onclick = ev => { ev.preventDefault(); mapState._forceGraph = true;mapState._forceGraphSignature=capSignature; warn.style.display = 'none'; renderMapGraph(eps); };
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
  if(!lay.nodes.some(n=>n.key===mapState.selectedNodeKey)){
    mapState.selectedNodeKey=lay.nodes.some(n=>n.key===focusedKey)?focusedKey:lay.nodes[0].key;
  }
  const snapshotKey=[mapState.domain,mapState.method,mapState.statusClass,mapState.collapseIdentical?'1':'0'].join('|');
  const previousGraph=graphSnapshots.get(snapshotKey);
  const nextNodeSignatures=new Map(lay.nodes.map(n=>[n.key,graphNodeSignature(n)]));
  const nextEdgeKeys=new Set(lay.edges.map(([a,b])=>graphEdgeKey(a,b)));
  const dataChanged=!!previousGraph&&previousGraph.dataVersion!==mapState._dataVersion;
  const changedNodes=new Set();
  if(dataChanged){
    nextNodeSignatures.forEach((signature,key)=>{if(previousGraph.nodes.get(key)!==signature)changedNodes.add(key);});
  }
  const changedEdges=new Set();
  if(dataChanged){
    lay.edges.forEach(([a,b])=>{const key=graphEdgeKey(a,b);if(!previousGraph.edges.has(key)||changedNodes.has(b.key))changedEdges.add(key);});
  }
  const animateGraphDiff=changedNodes.size+changedEdges.size<=24;
  let h = '';
  lay.edges.forEach(([a, b]) => {
    const x1 = a.px + 8, y1 = a.py, x2 = b.px - 4, y2 = b.py, mx = (x1 + x2) / 2;
    h += `<path class="g-edge" data-edge="${escAttr(graphEdgeKey(a,b))}" d="M${x1} ${y1} C ${mx} ${y1} ${mx} ${y2} ${x2} ${y2}"/>`;
  });
  lay.nodes.forEach(n => h += gNode(n));
  g.innerHTML = h;
  graphSnapshots.set(snapshotKey,{nodes:nextNodeSignatures,edges:nextEdgeKeys,dataVersion:mapState._dataVersion});
  if(graphSnapshots.size>12)graphSnapshots.delete(graphSnapshots.keys().next().value);
  if(animateGraphDiff){
    changedEdges.forEach(key=>{
      const edge=[...g.querySelectorAll('.g-edge')].find(el=>el.dataset.edge===key);
      animateOnce(edge,[{opacity:.25,strokeDasharray:'3 5',strokeDashoffset:'16'},{opacity:1,strokeDasharray:'3 5',strokeDashoffset:'0'}],{duration:MOTION.slow,easing:MOTION.enter});
    });
    changedNodes.forEach(key=>{
      const node=[...g.querySelectorAll('.g-node')].find(el=>el.dataset.key===key);
      animateOnce(node,[{opacity:.45,transform:'translateX(-3px)'},{opacity:1,transform:'translateX(0)'}],{duration:MOTION.slow,easing:MOTION.enter});
    });
  }
  const focusHost=el=>{
    const host=el.dataset.host;
    if(!host)return;
    mapState.domain=host;
    try{localStorage.setItem(projectStorageKey(MAP_DOMAIN_KEY),host);}catch(e){}
    const sel=$('#mapDomain');if(sel)sel.value=host;
    mapState.collapsed.clear();
    mapState._needFit=true;
    mapState._animateNextFit=true;
    if($('#mapDiscoveryPanel')&&!$('#mapDiscoveryPanel').hidden)refreshMapDiscoveryPanel();
    refreshMapDomainSelection();
    toast('focused on '+host);
  };
  g.querySelectorAll('.g-node').forEach(el => {
    el.addEventListener('mouseenter', ev => {
      const key = el.dataset.key;
      const n = lay.nodes.find(x => x.key === key);
      if(n) graphTipShow(n, ev);
    });
    el.addEventListener('mouseleave', graphTipHide);
    el.addEventListener('dblclick', ev => {
      ev.stopPropagation();
      focusHost(el);
    });
    el.onclick = ev => {
      ev.stopPropagation();
      selectGraphNode(el);
      if(el.dataset.kind === 'ep'){
        const f = el.dataset.flow;
        if(f) flowPopup(Number(f));
        return;
      }
      const k = el.dataset.key;
      mapState.collapsed.has(k) ? mapState.collapsed.delete(k) : mapState.collapsed.add(k);
      renderMap();
    };
    el.addEventListener('keydown',ev=>{
      const navKeys=['ArrowRight','ArrowDown','ArrowLeft','ArrowUp','Home','End'];
      if(navKeys.includes(ev.key)){
        ev.preventDefault();
        const nodes=[...g.querySelectorAll('.g-node')],i=nodes.indexOf(el);
        const forward=ev.key==='ArrowRight'||ev.key==='ArrowDown';
        const next=ev.key==='Home'?nodes[0]:ev.key==='End'?nodes[nodes.length-1]:nodes[(i+(forward?1:-1)+nodes.length)%nodes.length];
        selectGraphNode(next);next.focus({preventScroll:true});return;
      }
      if((ev.key==='f'||ev.key==='F')&&el.dataset.host){ev.preventDefault();focusHost(el);return;}
      if(ev.key==='Enter'||ev.key===' '){
        ev.preventDefault();
        el.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      }
    });
  });
  if(focusedKey){
    const focused=[...g.querySelectorAll('.g-node')].find(el=>el.dataset.key===focusedKey);
    if(focused)focused.focus({preventScroll:true});
  }
  if(mapState._needFit){
    mapState._needFit=false;
    const animate=mapState._animateNextFit;
    mapState._animateNextFit=false;
    mapFitNow(animate);
  }
  else mapApplyZoom();
}

export function mapApplyZoom(){
  const z = mapState.zoom;
  const g=$('#mapGraphG');if(!g)return;
  mapZoomEpoch++;
  cancelElementAnimations(g);
  g.setAttribute('transform', `translate(${z.x} ${z.y}) scale(${z.k})`);
}

function graphTransform(z){return `translate(${z.x}px,${z.y}px) scale(${z.k})`;}
export async function mapFitNow(animate=false){
  const svg = $('#mapGraphSvg'), gr = mapState._g;
  if(!svg || !gr) return;
  const vw = svg.clientWidth || 820, vh = svg.clientHeight || 520;
  const k = Math.max(0.35, Math.min(1.5, vw / gr.w, vh / gr.h));
  const from={...mapState.zoom};
  const next={k,x:16,y:Math.max(10,(vh-gr.h*k)/2)};
  mapState.zoom=next;
  const g=$('#mapGraphG');
  if(!animate||!g){mapApplyZoom();return;}
  const epoch=++mapZoomEpoch;
  g.removeAttribute('transform');
  await animateOnce(g,[{transform:graphTransform(from)},{transform:graphTransform(next)}],{duration:MOTION.slow,easing:MOTION.standard,fill:'both'});
  if(epoch!==mapZoomEpoch)return;
  g.setAttribute('transform',`translate(${next.x} ${next.y}) scale(${next.k})`);
  cancelElementAnimations(g);
}

$('#mapViewSeg') && $('#mapViewSeg').querySelectorAll('button').forEach(b => b.onclick = () => setMapView(b.dataset.v));
$('#mapFit') && ($('#mapFit').onclick = () => mapFitNow(true));

// Graph wheel: pinch (trackpad) and Ctrl/Cmd+scroll zoom; plain scroll pans.
// Browsers fire trackpad pinch as wheel events with ctrlKey set, so the same
// gate covers both intentional zoom gestures without stealing normal scroll.
export function mapGraphWheel(e, zoom, apply){
  const pinchOrMod = e.ctrlKey || e.metaKey;
  if(pinchOrMod){
    e.preventDefault();
    const r = e.currentTarget.getBoundingClientRect();
    const mx = e.clientX - r.left, my = e.clientY - r.top;
    // ctrlKey pinch often reports larger deltaY; normalize so one notch ≈ 10%.
    const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
    const f = Math.exp(-dy * 0.01);
    const nk = Math.max(0.1, Math.min(4, zoom.k * f));
    zoom.x = mx - (mx - zoom.x) * (nk / zoom.k);
    zoom.y = my - (my - zoom.y) * (nk / zoom.k);
    zoom.k = nk;
    apply();
    return 'zoom';
  }
  e.preventDefault();
  const sx = e.deltaMode === 1 ? 16 : 1;
  zoom.x -= (e.deltaX || 0) * sx;
  zoom.y -= (e.deltaY || 0) * sx;
  apply();
  return 'pan';
}

(function(){
  const svg = $('#mapGraphSvg'); if(!svg) return;
  let drag = null;
  svg.addEventListener('wheel', e => {
    mapGraphWheel(e, mapState.zoom, mapApplyZoom);
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
