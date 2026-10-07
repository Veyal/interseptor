// collections-sheets.js — environment variables sheet, script review (quarantine approve), import sheet with report, and the collection runner.
import { $, esc, api, toast, toastError, icon, uiConfirm, openFlow, getHook } from './core.js';
import { renderState } from './statepanel.js';
import { openSheet, closeSheet } from './sheet.js';
import * as M from './collections-model.js';
import * as V from './varscope-model.js';
import { S, X, btn, el, itemByUid, jget, jsend, setStatus } from './collections-core.js';
import { paintPane, scheduleResolve } from './collections-editor.js';
import { globalsEnv, reloadEnvs, renderEnvSelect, renderScriptsChip, updateBadge } from './collections-env.js';
import { fmtSize } from './collections-response.js';
import { renderTree } from './collections-tree.js';

export function openEnvSheet() {
  if (!S.collection) { toast('Create or import a collection first'); return; }
  const groups = [];
  const env = S.envs.find((e) => e.uid === S.envUid);
  if (env) groups.push({ kind: 'environment', uid: env.uid, title: 'Environment: ' + env.name, env });
  groups.push({ kind: 'collection', uid: S.colUid, title: 'Collection: ' + (S.collection.name || '') });
  const g = globalsEnv();
  if (g) groups.push({ kind: 'environment', uid: g.uid, title: 'Globals', env: g });
  let active = 0;
  openSheet({ id: 'collEnvSheet', title: 'Variables', detents: ['half', 'full'], detent: 'full', content: (body) => { body.textContent = ''; paintEnvSheet(body, groups, () => active, (i) => { active = i; }); return body; }, opener: $('#collEnvEdit') });
}

export async function paintEnvSheet(body, groups, getActive, setActive) {
  body.textContent = '';
  const wrap = el('div', 'coll-sheet');
  const seg = el('div', 'seg');
  seg.setAttribute('role', 'group');
  seg.setAttribute('aria-label', 'Variable scope');
  groups.forEach((g, i) => {
    const b = el('button', '', g.title.split(':')[0]);
    b.type = 'button';
    b.setAttribute('aria-pressed', i === getActive() ? 'true' : 'false');
    if (i === getActive()) b.classList.add('on');
    b.addEventListener('click', () => { setActive(i); paintEnvSheet(body, groups, getActive, setActive); });
    seg.append(b);
  });
  wrap.append(seg);
  body.append(wrap);
  const g = groups[getActive()];
  const holder = el('div');
  wrap.append(holder);
  renderState(holder, 'loading', { title: 'Loading variables', rows: 4 });
  let vars;
  try {
    const r = await jget('/api/variables/' + g.kind + '/' + encodeURIComponent(g.uid));
    vars = r.variables || [];
  } catch (e) { renderState(holder, 'error', { title: 'Could not load variables', message: e.message, status: e.status, onRetry: () => paintEnvSheet(body, groups, getActive, setActive) }); return; }
  holder.textContent = '';
  holder.append(el('h3', '', g.title));
  if (g.env) holder.append(envMeta(g.env));
  holder.append(el('p', 'coll-note', 'Initial values are shared when you export. Current values stay on this machine and are what requests use. Secret variables never show or export a value.'));
  const rows = V.sheetRows(vars);
  const currents = new Map();
  const table = el('table', 'coll-kv');
  table.setAttribute('aria-label', g.title + ' variables');
  table.innerHTML = '<thead><tr><th scope="col">On</th><th scope="col">Name</th><th scope="col">Type</th><th scope="col">Initial value</th><th scope="col">Current value</th><th scope="col"><span class="u-sr">Remove</span></th></tr></thead>';
  const tb = el('tbody');
  const addRow = (r) => {
    const tr = el('tr');
    const on = el('td', 'coll-on'); const cb = document.createElement('input'); cb.type = 'checkbox'; cb.checked = r.enabled; cb.setAttribute('aria-label', 'Enabled ' + r.key); cb.addEventListener('change', () => { r.enabled = cb.checked; }); on.append(cb);
    const mkText = (prop, aria, opts = {}) => { const td = el('td'); const i = document.createElement('input'); i.type = 'text'; i.className = 'btn btn-field'; i.value = opts.value != null ? opts.value : r[prop]; i.setAttribute('aria-label', aria + ' ' + (r.key || 'new variable')); i.autocomplete = 'off'; i.spellcheck = false; if (opts.disabled) i.disabled = true; if (opts.placeholder) i.placeholder = opts.placeholder; i.addEventListener('input', () => { if (opts.onInput) opts.onInput(i.value); else r[prop] = i.value; }); td.append(i); return td; };
    const type = el('td'); const ts = document.createElement('select'); ts.className = 'btn btn-field'; ts.setAttribute('aria-label', 'Type of ' + (r.key || 'new variable')); ['default', 'secret'].forEach((t) => ts.append(new Option(t, t))); ts.value = r.type === 'secret' ? 'secret' : 'default'; ts.addEventListener('change', () => { r.type = ts.value; r.secret = ts.value === 'secret'; paintRows(); }); type.append(ts);
    const curPlaceholder = r.secret ? (r.hasCurrent ? 'set (hidden)' : 'not set') : '';
    tr.append(on, mkText('key', 'Name', {}), type,
      mkText('initial', 'Initial value of', { disabled: r.secret, placeholder: r.secret ? 'never stored' : '' }),
      mkText('current', 'Current value of', { value: r.secret ? '' : r.current, placeholder: curPlaceholder, onInput: (v) => { currents.set(r.key, { value: v, secret: r.secret }); } }));
    const del = el('td', 'coll-del'); const x = btn('', () => { rows.splice(rows.indexOf(r), 1); paintRows(); }, 'btn xs'); x.innerHTML = icon('trash'); x.setAttribute('aria-label', 'Remove ' + (r.key || 'variable')); del.append(x); tr.append(del);
    tb.append(tr);
  };
  const paintRows = () => { tb.textContent = ''; rows.forEach(addRow); };
  paintRows();
  table.append(tb);
  const scroll = el('div', 'coll-scroll-x');
  scroll.append(table);
  holder.append(scroll);
  holder.append(btn('Add variable', () => { rows.push({ key: '', type: 'default', enabled: true, initial: '', current: '', hasCurrent: false, secret: false }); paintRows(); tb.querySelector('tr:last-child input[type="text"]')?.focus(); }, 'btn', 'plus'));
  const foot = el('div', 'coll-sheet-foot');
  const status = el('span', 'coll-note');
  status.setAttribute('role', 'status');
  const save = btn('Save variables', async () => {
    save.disabled = true;
    try {
      await jsend('PUT', '/api/variables/' + g.kind + '/' + encodeURIComponent(g.uid), { variables: V.toDeclared(rows) });
      for (const [key, c] of currents) {
        if (!key || (c.secret && c.value === '')) continue;
        await jsend('PUT', '/api/variables/' + g.kind + '/' + encodeURIComponent(g.uid) + '/current', { key, value: c.value, secret: c.secret });
      }
      await reloadEnvs();
      renderEnvSelect();
      scheduleResolve();
      status.textContent = 'Saved';
      toast('Variables saved');
    } catch (e) { status.textContent = ''; toastError('Could not save variables', e); } finally { save.disabled = false; }
  }, 'btn accent', 'save');
  const reset = btn('Reset current values', async () => {
    const ok = await uiConfirm('Reset current values', 'Drop every local current value in <b>' + esc(g.title) + '</b> so requests use the initial values again?', 'Reset', 'btn danger');
    if (!ok) return;
    try { await jsend('POST', '/api/variables/' + g.kind + '/' + encodeURIComponent(g.uid) + '/current/reset'); await reloadEnvs(); paintEnvSheet(body, groups, getActive, setActive); } catch (e) { toastError('Could not reset', e); }
  }, 'btn');
  foot.append(status, reset, save);
  holder.append(foot);
}

export function envMeta(env) {
  const row = el('div', 'coll-sheet-row');
  const mk = (label, prop, ph) => {
    const l = el('label', '', label);
    const id = 'collEnvMeta-' + prop;
    l.htmlFor = id;
    const i = document.createElement('input');
    i.id = id; i.type = 'text'; i.className = 'btn btn-field'; i.placeholder = ph; i.value = env[prop] || '';
    i.addEventListener('change', async () => {
      try { await jsend('PUT', '/api/environments/' + encodeURIComponent(env.uid), { name: env.name, kind: env.kind, collectionUid: env.collectionUid, boundIdentity: prop === 'boundIdentity' ? i.value : env.boundIdentity, baseTargetPin: prop === 'baseTargetPin' ? i.value : env.baseTargetPin, rev: env.rev }); env[prop] = i.value; await reloadEnvs(); toast('Saved'); } catch (e) { toastError('Could not save', e); }
    });
    row.append(l, i);
  };
  if (env.kind === 'env') { mk('Target pin', 'baseTargetPin', 'host the environment is meant for'); }
  return row;
}

/* ------------------------------------------------------------------ scripts sheet */

export async function openScriptsSheet() {
  if (!S.colUid) return;
  openSheet({ id: 'collScriptsSheet', title: 'Scripts', detents: ['half', 'full'], detent: 'full', opener: $('#collScripts'), content: (body) => { body.textContent = ''; paintScriptsSheet(body); return body; } });
}

export async function paintScriptsSheet(body) {
  const holder = el('div', 'coll-sheet');
  body.append(holder);
  renderState(holder, 'loading', { title: 'Loading scripts', rows: 3 });
  let view;
  try { view = await jget('/api/collections/' + encodeURIComponent(S.colUid) + '/scripts'); } catch (e) { renderState(holder, 'error', { title: 'Could not load scripts', message: e.message, status: e.status, onRetry: () => { body.textContent = ''; paintScriptsSheet(body); } }); return; }
  S.scripts = view;
  renderScriptsChip(); updateBadge();
  const rv = M.scriptReview(view, (uid) => (itemByUid(uid) || {}).name || '');
  holder.textContent = '';
  holder.append(el('h3', '', 'Script review'));
  holder.append(el('p', 'coll-note', rv.rows.length
    ? 'Imported scripts are quarantined: they do not run until you approve them here. Approval is bound to the exact source and the capabilities below; any edit resets it. Only this interactive session can approve; AI and MCP callers cannot.'
    : 'This collection has no scripts.'));
  const caps = new Set(rv.capabilities);
  if (rv.grantable.length) {
    const box = el('fieldset', 'coll-caps');
    box.append(el('legend', 'coll-note', 'Capabilities granted to approved scripts. Scripts that read or set variables need vars.read and vars.write; nothing is granted by default.'));
    rv.grantable.forEach((c) => {
      const l = el('label');
      const cb = document.createElement('input');
      cb.type = 'checkbox'; cb.checked = caps.has(c);
      cb.addEventListener('change', () => { if (cb.checked) caps.add(c); else caps.delete(c); });
      l.append(cb, document.createTextNode(c));
      box.append(l);
    });
    holder.append(box);
  }
  const picked = new Set();
  rv.rows.forEach((r) => {
    const card = el('div', 'coll-script');
    const head = el('div', 'coll-sheet-row');
    if (!r.trusted) {
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.setAttribute('aria-label', 'Approve ' + r.listen + ' script ' + r.shortHash);
      cb.addEventListener('change', () => { if (cb.checked) picked.add(r.hash); else picked.delete(r.hash); });
      head.append(cb);
    }
    const tag = el('strong', '', r.listen + ' · ' + r.owners.join(', '));
    head.append(tag, el('span', 'coll-meta', r.lines + ' lines · ' + r.shortHash));
    const state = el('span', r.trusted ? 'coll-trustline' : 'coll-flag-chip', r.trusted ? 'Approved' : 'Quarantined');
    state.dataset.s = r.trusted ? 'trusted' : 'untrusted';
    head.append(state);
    card.append(head);
    const facts = [];
    if (r.status !== 'supported') facts.push('Analyser: ' + r.status);
    if (r.apis.length) facts.push('APIs: ' + r.apis.join(', '));
    if (r.modules.length) facts.push('Modules: ' + r.modules.join(', '));
    if (r.hosts.length) facts.push('Hosts in source: ' + r.hosts.join(', '));
    if (facts.length) card.append(el('p', 'coll-note', facts.join(' · ')));
    if (r.flags.length) { const f = el('div', 'coll-chips'); r.flags.forEach((x) => { const c = el('span', 'coll-flag-chip'); c.innerHTML = icon('alert'); c.append(document.createTextNode(x)); f.append(c); }); card.append(f); }
    if (r.source) { const det = el('details'); const sm = el('summary', '', 'Show source'); det.append(sm, el('pre', '', r.source)); card.append(det); }
    holder.append(card);
  });
  if (rv.rows.length) {
    const foot = el('div', 'coll-sheet-foot');
    const status = el('span', 'coll-note');
    status.setAttribute('role', 'status');
    const approve = async (all) => {
      if (!all && !picked.size) { toast('Select scripts to approve'); return; }
      const ok = await uiConfirm('Approve scripts', 'Approved scripts run with these capabilities: <b>' + esc([...caps].join(', ') || 'none') + '</b>. Read the source first; a trusted script is trusted code with your network position.', 'Approve', 'btn accent');
      if (!ok) return;
      try { await jsend('POST', '/api/collections/' + encodeURIComponent(S.colUid) + '/trust', M.trustBody({ all, hashes: [...picked], capabilities: [...caps] })); toast('Scripts approved'); body.textContent = ''; await paintScriptsSheet(body); renderTree(); if (S.ed) paintPane(S.tab); } catch (e) { toastError('Could not approve', e); }
    };
    const revoke = async () => {
      const ok = await uiConfirm('Revoke approval', 'Quarantine every script in this collection again?', 'Revoke', 'btn danger');
      if (!ok) return;
      try { await jsend('POST', '/api/collections/' + encodeURIComponent(S.colUid) + '/trust/revoke', { all: true }); toast('Approval revoked'); body.textContent = ''; await paintScriptsSheet(body); renderTree(); } catch (e) { toastError('Could not revoke', e); }
    };
    foot.append(status, btn('Revoke all', revoke, 'btn danger'), btn('Approve selected', () => approve(false), 'btn'), btn('Approve all', () => approve(true), 'btn accent'));
    holder.append(foot);
  }
}

/* ------------------------------------------------------------------ import sheet */

export function openImportSheet(initial = '') {
  openSheet({ id: 'collImportSheet', title: 'Import', detents: ['half', 'full'], detent: 'full', opener: $('#collImport'), content: (body) => { body.textContent = ''; paintImport(body, initial); return body; } });
}

export function paintImport(body, initialText) {
  const wrap = el('div', 'coll-sheet');
  body.append(wrap);
  wrap.append(el('h3', '', 'Import a collection'));
  wrap.append(el('p', 'coll-note', 'Postman v2.0/2.1 (collection, environment or globals), OpenAPI 3 / Swagger 2 (JSON or YAML) or a curl command. Parsing never runs code and never sends a request. Scripts arrive quarantined.'));
  const row = el('div', 'coll-sheet-row');
  const file = document.createElement('input');
  file.type = 'file'; file.id = 'collImportFile'; file.accept = '.json,.yaml,.yml,.txt,application/json';
  file.setAttribute('aria-label', 'Choose a collection file');
  row.append(file);
  wrap.append(row);
  const ta = document.createElement('textarea');
  ta.id = 'collImportText';
  ta.className = 'coll-body-raw';
  ta.setAttribute('aria-label', 'Paste collection JSON, an OpenAPI document or a curl command');
  ta.placeholder = 'Or paste JSON, YAML or a curl command here';
  ta.spellcheck = false;
  ta.value = initialText;
  wrap.append(ta);
  const out = el('div', 'coll-report');
  out.setAttribute('aria-live', 'polite');
  const actions = el('div', 'coll-sheet-foot');
  const preview = btn('Preview', () => runPreview(), 'btn accent', 'search');
  const commit = btn('Import', () => runCommit(), 'btn accent', 'download');
  commit.disabled = true;
  actions.append(preview, commit);
  wrap.append(actions, out);
  let data = initialText;
  file.addEventListener('change', async () => {
    const f = file.files && file.files[0];
    if (!f) return;
    if (f.size > 64 * 1024 * 1024) { toast('That file is larger than the 64 MiB import limit', 'error'); return; }
    data = await f.text();
    ta.value = data.length > 200000 ? '' : data;
    ta.placeholder = data.length > 200000 ? f.name + ' loaded (' + fmtSize(f.size) + ')' : ta.placeholder;
    commit.disabled = true;
    runPreview();
  });
  ta.addEventListener('input', () => { data = ta.value; commit.disabled = true; });

  async function post(kind) {
    const text = ta.value || data;
    if (!text.trim()) { toast('Choose a file or paste something to import'); return null; }
    return api('/api/import/collection/' + kind + '?format=auto', { method: 'POST', headers: { 'content-type': 'application/octet-stream' }, body: text });
  }
  async function runPreview() {
    preview.disabled = true;
    renderState(out, 'loading', { title: 'Parsing', rows: 3 });
    try {
      const pv = await post('preview');
      if (!pv) { out.textContent = ''; return; }
      paintReport(out, pv.report, pv);
      commit.disabled = false;
    } catch (e) { renderState(out, 'error', { title: 'Could not read that file', message: e.message, status: e.status }); commit.disabled = true; } finally { preview.disabled = false; }
  }
  async function runCommit() {
    commit.disabled = true;
    try {
      const r = await post('commit');
      if (!r) return;
      toast('Imported. Scripts are quarantined until you approve them.');
      closeSheet('collImportSheet');
      if (r.collectionUid) S.colUid = r.collectionUid;
      S.selUid = '';
      await X.loadAll({ keepSelection: true });
      if (S.scripts && S.scripts.untrusted) setStatus('warn', S.scripts.untrusted + ' imported ' + (S.scripts.untrusted === 1 ? 'script is' : 'scripts are') + ' quarantined and will not run.', [{ label: 'Review scripts', run: () => openScriptsSheet() }]);
    } catch (e) { toastError('Import failed', e); commit.disabled = false; }
  }
  if (initialText.trim()) runPreview();
}

export function paintReport(out, report, pv) {
  out.textContent = '';
  const head = el('p', '', (pv && pv.name ? pv.name + ': ' : '') + M.reportHeadline(report));
  head.append(document.createTextNode(''));
  out.append(head);
  if (pv && (pv.environments || []).length) out.append(el('p', 'coll-note', 'Environments in the file: ' + pv.environments.join(', ')));
  M.groupReport(report).forEach((g) => {
    const d = el('details');
    if (g.level === 'unsupported' || g.level === 'blocked' || g.level === 'needs-review') d.open = true;
    const s = el('summary');
    s.innerHTML = icon(g.level === 'converted' ? 'check' : g.level === 'unsupported' || g.level === 'blocked' ? 'alert-circle' : 'alert');
    s.append(document.createTextNode(g.label + ' (' + g.entries.length + ')'));
    d.append(s);
    const ul = el('ul');
    g.entries.slice(0, 200).forEach((e) => ul.append(el('li', '', [e.path, e.feature, e.message, e.line ? 'line ' + e.line : '', e.suggestion].filter(Boolean).join(' · '))));
    if (g.entries.length > 200) ul.append(el('li', '', '+ ' + (g.entries.length - 200) + ' more'));
    d.append(ul);
    out.append(d);
  });
  const sl = (report && report.scriptList) || [];
  if (sl.length) {
    const d = el('details');
    d.append(el('summary', '', 'Scripts (' + sl.length + ', all quarantined)'));
    const ul = el('ul');
    sl.slice(0, 200).forEach((s) => ul.append(el('li', '', [s.path, s.listen, s.lines + ' lines', s.status, (s.hosts || []).length ? 'hosts: ' + s.hosts.join(', ') : '', (s.flags || []).length ? 'flags: ' + s.flags.join(', ') : ''].filter(Boolean).join(' · '))));
    d.append(ul);
    out.append(d);
  }
}

/* ------------------------------------------------------------------ run */

export async function runCollection(folderUid = '') {
  if (!S.colUid) { toast('Create or import a collection first'); return; }
  const mount = getHook('collectionRunner');
  if (mount) {
    // The runner view has its own options and an explicit Run button, so no confirm step.
    openSheet({ id: 'collRunSheet', title: 'Run', detents: ['half', 'full'], detent: 'full', opener: $('#collRun'), content: (body) => { body.textContent = ''; const w = el('div', 'coll-sheet'); body.append(w); mount(w, { collectionUid: S.colUid, collectionName: S.collection.name || '', folderUid: folderUid || '', envUid: S.envUid || '' }); return body; } });
    return;
  }
  const ok = await uiConfirm('Run requests', 'Run every request ' + (folderUid ? 'in this folder' : 'in <b>' + esc(S.collection.name || 'the collection') + '</b>') + ' in order? Scope policy is <b>block</b>; quarantined scripts are skipped. Variable changes made by scripts are discarded.', 'Run', 'btn accent');
  if (!ok) return;
  openSheet({ id: 'collRunSheet', title: 'Run', detents: ['half', 'full'], detent: 'full', opener: $('#collRun'), content: (body) => { body.textContent = ''; const w = el('div', 'coll-sheet'); body.append(w); renderState(w, 'loading', { title: 'Running', rows: 5 }); execRun(w, folderUid); return body; } });
}

export async function execRun(holder, folderUid) {
  try {
    const res = await jsend('POST', '/api/collections/run', { collectionUid: S.colUid, folderUid: folderUid || undefined, envUid: S.envUid || undefined, persist: 'discard', bail: 'none' });
    paintRun(holder, res);
  } catch (e) { renderState(holder, 'error', { title: 'Run failed', message: e.message, status: e.status, onRetry: () => { renderState(holder, 'loading', { title: 'Running', rows: 5 }); execRun(holder, folderUid); } }); }
}

export function paintRun(holder, res) {
  holder.textContent = '';
  holder.append(el('h3', '', 'Run results'));
  const rows = res.results || res.steps || [];
  const sum = res.summary || null;
  if (sum) holder.append(el('p', '', Object.entries(sum).filter(([, v]) => typeof v === 'number').map(([k, v]) => k + ': ' + v).join(' · ')));
  if (!rows.length && !sum) holder.append(el('p', 'coll-note', 'The run finished with no results.'));
  const list = el('div', 'coll-run');
  list.setAttribute('role', 'list');
  rows.forEach((r) => {
    const st = r.result || r;
    const item = itemByUid(r.itemUid || st.itemUid);
    const sm = M.stepSummary(st);
    const row = el('div', 'coll-run-row');
    row.setAttribute('role', 'listitem');
    const ic = el('span');
    ic.innerHTML = icon(sm.kind === 'sent' && !(st.tests || []).some((t) => t.status === 'fail' || t.status === 'error') ? 'check' : 'alert');
    row.append(ic, el('span', 'coll-name', (item && item.name) || r.itemUid || 'Request'),
      el('span', 'coll-code', sm.kind === 'sent' ? sm.label : sm.kind.toUpperCase()),
      el('span', 'coll-meta', M.testsLabel(st.tests)),
      (M.firstFlowId(st) ? btn('Flow #' + M.firstFlowId(st), () => openFlow(M.firstFlowId(st), { source: 'collections' }), 'btn xs', 'link') : el('span')));
    list.append(row);
  });
  holder.append(list);
  holder.append(el('p', 'coll-note', 'Every request was captured as a flow in History with a collection flag.'));
}

/* ------------------------------------------------------------------ new collection */

