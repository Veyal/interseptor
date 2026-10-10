// collections-model.js — pure logic for the Collections panel. No imports and no
// DOM, so it runs under node (tests live in ui/_js-tests). The server is the
// source of truth for resolution, scope policy and script trust; this module
// only shapes API data for display and builds request bodies from editor state.
//
// Item JSON follows the Postman-shaped columns the importers write (see
// internal/collexec/model.go): url is a string or {raw, variable[]}, headers
// and params are [{key,value,disabled}], body is {mode, raw, urlencoded[],
// formdata[], graphql, options}, auth is {type, <type>:[{key,value}]} and
// events are [{listen, script:{type, exec:[lines]}}]. Unknown fields are
// preserved on every write so a UI edit never drops imported data.

export const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'];
export const EDITOR_TABS = ['params', 'headers', 'body', 'auth', 'prerequest', 'tests', 'settings'];
export const TAB_LABEL = {
  params: 'Params', headers: 'Headers', body: 'Body', auth: 'Auth', prerequest: 'Pre-request', tests: 'Tests', settings: 'Settings',
};
export const BODY_MODES = ['none', 'raw', 'urlencoded', 'formdata', 'graphql'];
export const AUTH_TYPES = ['inherit', 'none', 'bearer', 'basic', 'apikey'];
export const AUTH_FIELDS = {
  bearer: ['token'], basic: ['username', 'password'], apikey: ['key', 'value', 'in'],
};
export const RAW_LANGS = ['json', 'text', 'xml', 'html', 'javascript'];
export const LISTEN = { prerequest: 'prerequest', tests: 'test' };

// ---- rank (port of store.RankBetween; keep in step) --------------------------

const ALPHA = '0123456789abcdefghijklmnopqrstuvwxyz';
const idx = (ch) => { const i = ALPHA.indexOf(ch); return i < 0 ? 0 : i; };

export function rankBetween(a = '', b = '') {
  if (b !== '' && a >= b) b = '';
  let out = '';
  let bInf = b === '';
  for (let i = 0; ; i++) {
    let da = 0, db = ALPHA.length;
    if (i < a.length) da = idx(a[i]);
    if (!bInf && i < b.length) db = idx(b[i]);
    if (db - da > 1) return out + ALPHA[Math.floor((da + db) / 2)];
    out += ALPHA[da];
    if (db !== da) bInf = true;
  }
}

// ---- tree ----------------------------------------------------------------

export function buildTree(items) {
  const nodes = new Map();
  for (const it of items || []) nodes.set(it.uid, { item: it, children: [] });
  const roots = [];
  for (const it of items || []) {
    const n = nodes.get(it.uid);
    const p = it.parentUid ? nodes.get(it.parentUid) : null;
    if (p && p !== n) p.children.push(n); else roots.push(n);
  }
  const cmp = (a, b) => (a.item.rank < b.item.rank ? -1 : a.item.rank > b.item.rank ? 1 : a.item.uid < b.item.uid ? -1 : 1);
  const sort = (ns) => { ns.sort(cmp); ns.forEach((n) => sort(n.children)); };
  sort(roots);
  return roots;
}

export function itemUrlRaw(item) {
  const u = item && item.url;
  if (typeof u === 'string') return u;
  return (u && u.raw) || '';
}

// splitUrl breaks a request URL into the host (or a leading {{variable}} base)
// and the path, without query or hash. The path is what tells two requests on
// the same host apart. A relative URL has no host.
const URL_HEAD = /^(?:[a-z][a-z0-9+.-]*|\{\{[^{}]*\}\}):\/\/([^/?#]*)|^(\{\{[^{}]*\}\})(?=[/?#]|$)/i;
export function splitUrl(raw) {
  const src = String(raw || '').trim();
  const m = URL_HEAD.exec(src);
  let host = '';
  let rest = src;
  if (m) { host = (m[1] != null ? m[1] : m[2]).replace(/^[^@/]*@/, ''); rest = src.slice(m[0].length); }
  const cut = rest.search(/[?#]/);
  let path = cut < 0 ? rest : rest.slice(0, cut);
  if (host && !path) path = '/';
  return { host, path };
}

// hostKey compares hosts case-insensitively but leaves {{Variable}} names alone.
const hostKey = (h) => (String(h || '').includes('{{') ? String(h) : String(h || '').toLowerCase());

// dominantHost returns the host most requests in a collection share, or ''.
// It must cover more than half of the requests that have a host and at least
// two requests, so a lone request never loses its host.
export function dominantHost(items) {
  const counts = new Map();
  let total = 0;
  for (const it of items || []) {
    if (!it || it.kind === 'folder') continue;
    const h = hostKey(splitUrl(itemUrlRaw(it)).host);
    if (!h) continue;
    total++;
    counts.set(h, (counts.get(h) || 0) + 1);
  }
  let best = '', n = 0;
  for (const [h, c] of counts) if (c > n) { best = h; n = c; }
  return n >= 2 && n * 2 > total ? best : '';
}

// pathParts splits a path so the end stays visible when the middle is cut:
// head may ellipsize, tail never does. The tail starts at a path separator when
// one falls in the window, and never cuts inside a {{variable}}.
export function pathParts(path, tailLen = 24) {
  const p = String(path || '');
  if (p.length <= tailLen) return { head: '', tail: p };
  let cut = p.length - tailLen;
  const slash = p.indexOf('/', cut);
  if (slash >= 0 && slash < p.length - 1) cut = slash;
  const open = p.lastIndexOf('{{', cut - 1);
  if (open >= 0 && p.indexOf('}}', open) >= cut) cut = open;
  return { head: p.slice(0, cut), tail: p.slice(cut) };
}

// rowLine is everything a request row shows under its name. The host is shown
// only when it differs from the collection's dominant host, or when the caller
// says it holds an unresolved variable.
export function rowLine(item, dominant = '', hostUnresolved = false) {
  const { host, path } = splitUrl(itemUrlRaw(item));
  const showHost = !!host && (hostUnresolved || hostKey(host) !== hostKey(dominant));
  return { host, showHost, path, ...pathParts(path) };
}

// rowLabel is the accessible name of a request row: method, name and target.
export function rowLabel(item, line, unresolved = [], quarantined = false) {
  const head = (item.method || 'GET').toUpperCase() + ' ' + (item.name || 'Untitled request');
  const where = (line.showHost ? line.host : '') + line.path;
  let out = head + (where ? ', ' + where : '');
  if (unresolved.length) out += ', unresolved variable' + (unresolved.length === 1 ? '' : 's') + ': ' + unresolved.join(', ');
  if (quarantined) out += ', scripts quarantined';
  return out;
}

// matchesQuery: case-insensitive match against name, method and URL.
export function matchesQuery(item, q) {
  const s = String(q || '').trim().toLowerCase();
  if (!s) return true;
  return [item.name, item.method, itemUrlRaw(item)].some((v) => String(v || '').toLowerCase().includes(s));
}

// filterTree keeps matching nodes and their ancestors; a matching folder keeps
// its whole subtree so a folder name search still shows its requests.
export function filterTree(roots, q) {
  const s = String(q || '').trim();
  if (!s) return roots;
  const walk = (ns, ancestorMatched) => {
    const out = [];
    for (const n of ns) {
      const self = matchesQuery(n.item, s);
      const kids = walk(n.children, ancestorMatched || self);
      if (self || ancestorMatched || kids.length) out.push({ item: n.item, children: self || ancestorMatched ? n.children : kids, matched: self });
    }
    return out;
  };
  return walk(roots, false);
}

// flattenVisible lists rows in display order for keyboard navigation. While a
// search is active every folder is shown open.
export function flattenVisible(roots, expanded, { forceOpen = false } = {}) {
  const rows = [];
  const walk = (ns, depth, parent) => {
    ns.forEach((n, i) => {
      const folder = n.item.kind === 'folder';
      const open = folder && (forceOpen || expanded.has(n.item.uid));
      rows.push({ uid: n.item.uid, item: n.item, depth, folder, open, parent, posinset: i + 1, setsize: ns.length, count: n.children.length });
      if (open) walk(n.children, depth + 1, n.item.uid);
    });
  };
  walk(roots, 0, '');
  return rows;
}

// treeKey maps an arrow key to the next focus / expansion action for a tree row.
export function treeKey(rows, uid, key) {
  const i = rows.findIndex((r) => r.uid === uid);
  if (i < 0) return null;
  const r = rows[i];
  switch (key) {
    case 'ArrowDown': return i + 1 < rows.length ? { focus: rows[i + 1].uid } : null;
    case 'ArrowUp': return i > 0 ? { focus: rows[i - 1].uid } : null;
    case 'Home': return { focus: rows[0].uid };
    case 'End': return { focus: rows[rows.length - 1].uid };
    case 'ArrowRight':
      if (r.folder && !r.open) return { expand: r.uid };
      if (r.folder && r.open && rows[i + 1] && rows[i + 1].parent === r.uid) return { focus: rows[i + 1].uid };
      return null;
    case 'ArrowLeft':
      if (r.folder && r.open) return { collapse: r.uid };
      return r.parent ? { focus: r.parent } : null;
    default: return null;
  }
}

// descendants lists every uid below a folder (to refuse dropping into itself).
export function descendantUids(items, uid) {
  const kids = new Map();
  for (const it of items || []) { const k = it.parentUid || ''; (kids.get(k) || kids.set(k, []).get(k)).push(it.uid); }
  const out = new Set();
  const stack = [uid];
  while (stack.length) for (const c of kids.get(stack.pop()) || []) if (!out.has(c)) { out.add(c); stack.push(c); }
  return out;
}

// planMove computes the {parentUid, rank} for dropping `uid` before / after /
// into a target. Returns null when the drop is invalid (onto itself or into its
// own subtree) or already the current position.
export function planMove(items, uid, targetUid, where) {
  const by = new Map((items || []).map((i) => [i.uid, i]));
  const moving = by.get(uid), target = by.get(targetUid);
  if (!moving || !target || uid === targetUid) return null;
  if (descendantUids(items, uid).has(targetUid)) return null;
  const siblingsOf = (parent) => (items || []).filter((i) => (i.parentUid || '') === parent && i.uid !== uid)
    .sort((a, b) => (a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0));
  let parent, before = '', after = '';
  if (where === 'into' && target.kind === 'folder') {
    parent = target.uid;
    const sibs = siblingsOf(parent);
    before = sibs.length ? sibs[sibs.length - 1].rank : '';
  } else {
    parent = target.parentUid || '';
    const sibs = siblingsOf(parent);
    const at = sibs.findIndex((s) => s.uid === target.uid);
    if (where === 'before') { before = at > 0 ? sibs[at - 1].rank : ''; after = target.rank; }
    else { before = target.rank; after = at + 1 < sibs.length ? sibs[at + 1].rank : ''; }
  }
  const rank = rankBetween(before, after);
  if ((moving.parentUid || '') === parent && moving.rank === rank) return null;
  return { parentUid: parent, rank };
}

// planNudge moves an item one place up or down among its siblings (Alt+Up/Down).
export function planNudge(items, uid, dir) {
  const it = (items || []).find((i) => i.uid === uid);
  if (!it) return null;
  const sibs = (items || []).filter((i) => (i.parentUid || '') === (it.parentUid || ''))
    .sort((a, b) => (a.rank < b.rank ? -1 : a.rank > b.rank ? 1 : 0));
  const at = sibs.findIndex((s) => s.uid === uid);
  const to = at + (dir < 0 ? -1 : 1);
  if (to < 0 || to >= sibs.length) return null;
  return planMove(items, uid, sibs[to].uid, dir < 0 ? 'before' : 'after');
}

// ---- URL <-> params -------------------------------------------------------

export function splitURL(raw) {
  const s = String(raw || '');
  const hash = s.indexOf('#');
  const noHash = hash >= 0 ? s.slice(0, hash) : s;
  const q = noHash.indexOf('?');
  return { base: q >= 0 ? noHash.slice(0, q) : noHash, query: q >= 0 ? noHash.slice(q + 1) : '', hash: hash >= 0 ? s.slice(hash) : '' };
}

const safeDecode = (s) => { try { return decodeURIComponent(s.replace(/\+/g, ' ')); } catch (e) { return s; } };

// parseQuery keeps {{vars}} verbatim: they are not percent-decoded.
export function parseQuery(query) {
  if (!query) return [];
  return query.split('&').filter((p) => p !== '').map((p) => {
    const i = p.indexOf('=');
    const k = i >= 0 ? p.slice(0, i) : p, v = i >= 0 ? p.slice(i + 1) : '';
    const dec = (x) => (x.includes('{{') ? x : safeDecode(x));
    return { key: dec(k), value: dec(v), disabled: false };
  });
}

const enc = (x) => (x.includes('{{') ? x : encodeURIComponent(x).replace(/%24/g, '$'));

export function buildURL(base, params, hash = '') {
  const on = (params || []).filter((p) => !p.disabled && (p.key !== '' || p.value !== ''));
  const q = on.map((p) => (p.value === '' && !String(p.key).includes('=') ? enc(p.key) + '=' : enc(p.key) + '=' + enc(p.value))).join('&');
  return base + (q ? '?' + q : '') + hash;
}

// syncParamsFromURL re-derives enabled params from the URL text and keeps the
// user's disabled rows after them (the URL cannot express a disabled param).
export function syncParamsFromURL(raw, prev) {
  const { query } = splitURL(raw);
  const live = parseQuery(query);
  const off = (prev || []).filter((p) => p.disabled);
  return live.concat(off);
}

// ---- editor model ----------------------------------------------------------

const asList = (v) => (Array.isArray(v) ? v : []);
const kvText = (v) => (v == null ? '' : typeof v === 'string' ? v : typeof v === 'object' ? JSON.stringify(v) : String(v));

function toRows(list) {
  return asList(list).map((k) => ({
    key: k.key || '', value: kvText(k.value), description: typeof k.description === 'string' ? k.description : '',
    disabled: !!k.disabled || k.enabled === false, type: k.type, contentType: k.contentType, src: k.src,
  }));
}
function fromRows(rows, orig) {
  const byKey = new Map(asList(orig).map((o) => [o.key, o]));
  return (rows || []).map((r) => {
    const o = byKey.get(r.key) || {};
    const out = { ...o, key: r.key, value: r.value };
    if (r.description) out.description = r.description; else delete out.description;
    if (r.disabled) out.disabled = true; else delete out.disabled;
    delete out.enabled;
    if (r.type) out.type = r.type;
    if (r.contentType) out.contentType = r.contentType;
    return out;
  });
}

export function eventsToScripts(events) {
  const out = { prerequest: '', tests: '' };
  for (const e of asList(events)) {
    const exec = e && e.script && e.script.exec;
    const text = Array.isArray(exec) ? exec.join('\n') : typeof exec === 'string' ? exec : '';
    if (e.listen === 'prerequest') out.prerequest = out.prerequest ? out.prerequest + '\n' + text : text;
    else if (e.listen === 'test') out.tests = out.tests ? out.tests + '\n' + text : text;
  }
  return out;
}

// scriptsToEvents rewrites the prerequest/test events, preserving other
// listeners and the original script wrapper fields (id, type, packages).
export function scriptsToEvents(events, scripts) {
  const evs = asList(events).map((e) => ({ ...e }));
  const apply = (listen, text) => {
    const at = evs.findIndex((e) => e.listen === listen);
    const lines = String(text || '').split('\n');
    const empty = String(text || '').trim() === '';
    if (at >= 0) {
      if (empty) { evs.splice(at, 1); return; }
      evs[at] = { ...evs[at], script: { ...(evs[at].script || {}), type: (evs[at].script && evs[at].script.type) || 'text/javascript', exec: lines } };
    } else if (!empty) evs.push({ listen, script: { type: 'text/javascript', exec: lines } });
  };
  apply('prerequest', scripts.prerequest);
  apply('test', scripts.tests);
  return evs;
}

export function authToEditor(auth) {
  if (!auth || typeof auth !== 'object' || !auth.type) return { type: 'inherit', fields: {} };
  const type = String(auth.type).toLowerCase() === 'noauth' ? 'none' : String(auth.type).toLowerCase();
  const sub = auth[auth.type];
  const fields = {};
  if (Array.isArray(sub)) for (const kv of sub) fields[kv.key] = kvText(kv.value);
  else if (sub && typeof sub === 'object') for (const k of Object.keys(sub)) fields[k] = kvText(sub[k]);
  return { type, fields };
}

export function editorToAuth(ed, orig) {
  if (!ed || ed.type === 'inherit') return undefined;
  if (ed.type === 'none') return { ...(orig || {}), type: 'noauth' };
  const names = AUTH_FIELDS[ed.type];
  if (!names) return orig;
  const base = { ...(orig || {}), type: ed.type };
  base[ed.type] = names.map((k) => ({ key: k, value: (ed.fields && ed.fields[k]) || '', type: 'string' }));
  return base;
}

export function bodyToEditor(body) {
  const b = body && typeof body === 'object' ? body : {};
  const mode = BODY_MODES.includes(String(b.mode || '').toLowerCase()) ? String(b.mode).toLowerCase() : 'none';
  const lang = (b.options && b.options.raw && b.options.raw.language) || 'text';
  const g = b.graphql || {};
  return {
    mode, raw: b.raw || '', language: lang,
    urlencoded: toRows(b.urlencoded), formdata: toRows(b.formdata),
    gqlQuery: g.query || '', gqlVars: typeof g.variables === 'string' ? g.variables : g.variables ? JSON.stringify(g.variables, null, 2) : '',
  };
}

export function editorToBody(ed, orig) {
  const base = { ...(orig || {}) };
  base.mode = ed.mode;
  if (ed.mode === 'none') return orig && orig.mode === 'none' ? orig : base;
  if (ed.mode === 'raw') {
    base.raw = ed.raw;
    base.options = { ...(base.options || {}), raw: { ...((base.options || {}).raw || {}), language: ed.language } };
  } else if (ed.mode === 'urlencoded') base.urlencoded = fromRows(ed.urlencoded, base.urlencoded);
  else if (ed.mode === 'formdata') base.formdata = fromRows(ed.formdata, base.formdata);
  else if (ed.mode === 'graphql') base.graphql = { ...(base.graphql || {}), query: ed.gqlQuery, variables: ed.gqlVars };
  return base;
}

export function itemToEditor(item) {
  const it = item || {};
  const raw = itemUrlRaw(it);
  const params = it.params && it.params.length ? toRows(it.params) : parseQuery(splitURL(raw).query);
  return {
    uid: it.uid, rev: it.rev, kind: it.kind, name: it.name || '', method: it.method || 'GET', url: raw,
    params, headers: toRows(it.headers), body: bodyToEditor(it.body),
    auth: authToEditor(it.auth), scripts: eventsToScripts(it.events),
    settings: settingsToEditor(it.settings), description: it.descriptionMd || '',
  };
}

export function settingsToEditor(s) {
  const o = s && typeof s === 'object' ? s : {};
  return {
    followRedirects: o.followRedirects == null ? '' : String(o.followRedirects),
    maxRedirects: o.maxRedirects ? String(o.maxRedirects) : '',
    timeoutMs: o.timeoutMs == null ? '' : String(o.timeoutMs),
    verifyTls: o.verifyTls == null ? '' : String(o.verifyTls),
    unresolved: o.unresolved || '', useSession: o.useSession == null ? '' : String(o.useSession),
  };
}

export function editorToSettings(ed, orig) {
  const out = { ...(orig || {}) };
  const tri = (key, v) => { if (v === 'true') out[key] = true; else if (v === 'false') out[key] = false; else delete out[key]; };
  tri('followRedirects', ed.followRedirects); tri('verifyTls', ed.verifyTls); tri('useSession', ed.useSession);
  const n = (key, v) => { const x = Number(v); if (v !== '' && Number.isFinite(x) && x >= 0) out[key] = Math.floor(x); else delete out[key]; };
  n('maxRedirects', ed.maxRedirects); n('timeoutMs', ed.timeoutMs);
  if (ed.unresolved) out.unresolved = ed.unresolved; else delete out.unresolved;
  return out;
}

// editorToItem builds the full PUT body (UpdateItem replaces the row): the
// original item with the edited columns swapped in and `rev` for optimistic
// concurrency.
export function editorToItem(ed, orig) {
  const o = orig || {};
  const urlRaw = buildURL(splitURL(ed.url).base, ed.params, splitURL(ed.url).hash);
  const hasParams = ed.params.some((p) => p.key !== '' || p.value !== '');
  const next = { ...o, name: ed.name, method: ed.method, rev: o.rev };
  next.url = typeof o.url === 'object' && o.url ? { ...o.url, raw: hasParams ? urlRaw : ed.url } : (hasParams ? urlRaw : ed.url);
  next.params = hasParams || (o.params && o.params.length) ? fromRows(ed.params, o.params) : o.params;
  next.headers = fromRows(ed.headers, o.headers);
  next.body = editorToBody(ed.body, o.body);
  const auth = editorToAuth(ed.auth, o.auth);
  if (auth === undefined) delete next.auth; else next.auth = auth;
  next.events = scriptsToEvents(o.events, ed.scripts);
  if (!next.events.length) delete next.events;
  next.settings = editorToSettings(ed.settings, o.settings);
  if (!Object.keys(next.settings).length) delete next.settings;
  next.descriptionMd = ed.description;
  for (const k of Object.keys(next)) if (next[k] === undefined) delete next[k];
  return next;
}

// signature is a stable string used for the dirty check.
export function editorSignature(ed) {
  return JSON.stringify([ed.name, ed.method, ed.url, ed.params, ed.headers, ed.body, ed.auth, ed.scripts, ed.settings, ed.description]);
}

export function newRequestItem(collectionUid, parentUid, name = 'New request') {
  return { kind: 'request', collectionUid, parentUid: parentUid || '', name, method: 'GET', url: '', headers: [], body: { mode: 'none' } };
}

// ---- send / response -------------------------------------------------------

// decideSend turns the editor state into a go/no-go for the Send button. The
// server enforces everything; this only avoids pointless round trips and
// explains why Send is blocked.
export function decideSend({ url, busy, kind } = {}) {
  if (kind === 'folder') return { ok: false, reason: 'Select a request to send' };
  if (busy) return { ok: false, reason: 'Sending…' };
  if (!String(url || '').trim()) return { ok: false, reason: 'Enter a URL' };
  return { ok: true, reason: '' };
}

const BLOCK_TEXT = {
  unresolved_variables: 'Unresolved variables block the send',
  out_of_scope: 'The target host is out of scope',
  base_target_pin: 'The environment target pin does not match this host',
  own_listener: 'Requests to Interseptor itself are never sent',
  scripts_quarantined: 'Scripts are quarantined until you approve them',
};

// stepSummary condenses a StepResult for the response status line.
export function stepSummary(res) {
  if (!res) return { kind: 'none', label: '' };
  if (res.outcome === 'blocked') return { kind: 'blocked', label: BLOCK_TEXT[res.blockReason] || 'Send blocked', reason: res.blockReason || '' };
  if (res.outcome === 'error') return { kind: 'error', label: res.error || 'Request failed' };
  if (res.outcome === 'skipped') return { kind: 'skipped', label: 'Skipped by a pre-request script' };
  const r = res.response || {};
  if (r.error) return { kind: 'error', label: r.error, status: r.status || 0 };
  return { kind: 'sent', label: String(r.status || ''), status: r.status || 0, statusText: r.statusText || '', size: r.size || 0, timeMs: r.timeMs || 0 };
}

// scopeWarnPrompt: an interactive warn-policy send to an out-of-scope host is
// answered by the server with outcome=blocked/out_of_scope or a warning; the UI
// offers a one-click "add host" for that case.
export function needsAddHost(res) { return !!res && res.outcome === 'blocked' && res.blockReason === 'out_of_scope'; }

export function hostOf(rawUrl) {
  const m = /^[a-z][a-z0-9+.-]*:\/\/([^/?#]+)/i.exec(String(rawUrl || '').trim());
  if (!m) return '';
  return m[1].replace(/^[^@]*@/, '');
}

// testCounts aggregates TestResult rows. 'unsupported' stays its own bucket:
// it must never count as a pass.
export function testCounts(tests) {
  const c = { pass: 0, fail: 0, skip: 0, error: 0, unsupported: 0, total: 0 };
  for (const t of tests || []) { c[t.status] = (c[t.status] || 0) + 1; c.total++; }
  return c;
}
export function testsLabel(tests) {
  const c = testCounts(tests);
  if (!c.total) return 'No tests';
  const bad = c.fail + c.error;
  return (c.pass) + '/' + c.total + ' passed' + (bad ? ', ' + bad + ' failed' : '') + (c.unsupported ? ', ' + c.unsupported + ' unsupported' : '');
}
export const TEST_ICON = { pass: 'check', fail: 'close', error: 'alert', skip: 'status-todo', unsupported: 'alert-circle' };
export const TEST_TEXT = { pass: 'PASS', fail: 'FAIL', error: 'ERROR', skip: 'SKIP', unsupported: 'UNSUPPORTED' };

export function consoleCounts(lines) {
  const c = { log: 0, warn: 0, error: 0 };
  for (const l of lines || []) { const k = l.level === 'warn' ? 'warn' : l.level === 'error' ? 'error' : 'log'; c[k]++; }
  return c;
}

// firstFlowId picks the flow to show in the response pane.
export function firstFlowId(res) {
  if (!res) return 0;
  if (res.flowId) return res.flowId;
  const l = res.flowIds || [];
  return l.length ? l[l.length - 1] : 0;
}

// ---- runs ------------------------------------------------------------------

export function parseResultJSON(row) {
  try { return JSON.parse(row.resultJson || '{}'); } catch (e) { return {}; }
}
export function runSummary(rows) {
  const s = { requests: 0, passed: 0, failed: 0, blocked: 0, errors: 0, skipped: 0, tests: { pass: 0, fail: 0, total: 0 } };
  for (const row of rows || []) {
    s.requests++;
    const r = parseResultJSON(row);
    if (r.outcome === 'blocked' || row.status === 'blocked') s.blocked++;
    else if (r.outcome === 'error' || row.status === 'error') s.errors++;
    else if (r.outcome === 'skipped' || row.status === 'skipped') s.skipped++;
    else if (testCounts(r.tests).fail + testCounts(r.tests).error > 0 || row.status === 'failed') s.failed++;
    else s.passed++;
    const tc = testCounts(r.tests);
    s.tests.pass += tc.pass; s.tests.fail += tc.fail + tc.error; s.tests.total += tc.total;
  }
  return s;
}

// ---- import report ---------------------------------------------------------

export const LEVEL_ORDER = ['unsupported', 'blocked', 'needs-review', 'degraded', 'preserved-inert', 'converted'];
export const LEVEL_LABEL = {
  unsupported: 'Unsupported', blocked: 'Blocked', 'needs-review': 'Needs review', degraded: 'Degraded', 'preserved-inert': 'Preserved, inert', converted: 'Converted',
};

export function groupReport(report) {
  const groups = new Map();
  for (const e of (report && report.entries) || []) {
    const k = LEVEL_ORDER.includes(e.level) ? e.level : 'needs-review';
    (groups.get(k) || groups.set(k, []).get(k)).push(e);
  }
  return LEVEL_ORDER.filter((k) => groups.has(k)).map((k) => ({ level: k, label: LEVEL_LABEL[k], entries: groups.get(k) }));
}

export function reportHeadline(report) {
  if (!report) return '';
  return report.headline || '';
}

// scriptReview prepares the review-sheet rows from GET .../scripts. A script is
// approvable only when it is not already trusted.
export function scriptReview(view, ownerName = () => '') {
  const rows = ((view && view.scripts) || []).map((s) => ({
    hash: s.hash, shortHash: String(s.hash || '').slice(0, 12), listen: s.listen === 'test' ? 'Tests' : 'Pre-request',
    trusted: !!s.trusted, status: s.status || 'supported', lines: s.lines || 0,
    apis: s.apis || [], modules: s.modules || [], hosts: s.hosts || [], flags: s.flags || [],
    owners: (s.owners || []).map((o) => (o ? ownerName(o) || o : 'Collection')), source: s.source || '',
  }));
  return { rows, untrusted: rows.filter((r) => !r.trusted).length, capabilities: (view && view.capabilities) || [], grantable: (view && view.grantable) || [] };
}

// trustBody builds the POST .../trust body. confirm is always explicit.
export function trustBody({ hashes, all, capabilities } = {}) {
  const b = { confirm: true };
  if (all) b.all = true; else b.hashes = hashes || [];
  if (capabilities) b.capabilities = capabilities;
  return b;
}

// ---- examples --------------------------------------------------------------

export function exampleFromStep(name, res, flow) {
  const r = (res && res.response) || {};
  return {
    name: name || 'Example', status: r.status || 0, statusText: r.statusText || '', contentType: r.contentType || '',
    headers: Object.entries((flow && flow.resHeaders) || {}).flatMap(([k, vs]) => (Array.isArray(vs) ? vs : [vs]).map((v) => ({ key: k, value: v }))),
    body: (flow && flow.body) || '', flowId: firstFlowId(res) || undefined,
  };
}

// ---- command palette / keyboard --------------------------------------------

export const SHORTCUTS = [
  { keys: 'Ctrl+Enter', label: 'Send the open request' },
  { keys: '/', label: 'Filter the collection tree' },
  { keys: 'Alt+Up / Alt+Down', label: 'Move the selected item among its siblings' },
  { keys: 'Arrow keys', label: 'Move through the tree; Right opens, Left closes a folder' },
  { keys: 'Ctrl+`', label: 'Show or hide the console' },
  { keys: 'Escape', label: 'Close a sheet or the context menu' },
];

export function commandEntries(h = {}) {
  return [
    { t: 'Collections: Import collection', kw: 'postman openapi swagger curl import file', run: h.import },
    { t: 'Collections: Paste cURL', kw: 'curl import request', run: h.pasteCurl },
    { t: 'Collections: New collection', kw: 'create collection', run: h.newCollection },
    { t: 'Collections: Switch environment', kw: 'env environment variables switch', run: h.switchEnv },
    { t: 'Collections: Edit environment variables', kw: 'env environment variables secret', run: h.editEnv },
    { t: 'Collections: Send open request', kw: 'send run request', run: h.send },
    { t: 'Collections: Run collection', kw: 'runner run all requests', run: h.runCollection },
    { t: 'Collections: Review scripts', kw: 'trust approve quarantine scripts', run: h.reviewScripts },
  ].filter((c) => typeof c.run === 'function').map((c) => ({ ...c, group: 'Actions' }));
}
