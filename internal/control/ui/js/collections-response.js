// collections-response.js — send, the response pane (body, headers, tests, examples), console and Repeater/curl actions.
import { $, esc, api, toast, toastError, icon, getHook, uiPrompt, uiConfirm, copyText, openFlow } from './core.js';
import { getShellApi } from './shell-hooks.js';
import { renderState } from './statepanel.js';
import * as M from './collections-model.js';
import { S, btn, el, jget, jsend, setStatus } from './collections-core.js';
import { saveCurrent } from './collections-editor.js';
import { paintEnvDot, reloadEnvs, setPhone } from './collections-env.js';
import { openEnvSheet, openScriptsSheet } from './collections-sheets.js';

export async function sendCurrent() {
  if (!S.ed) return;
  const verdict = M.decideSend({ url: S.ed.url, busy: S.busy, kind: S.ed.kind });
  if (!verdict.ok) { toast(verdict.reason); return; }
  if (S.dirty && !(await saveCurrent())) return;
  const gen = ++S.sendGen;
  S.busy = true;
  setSendBusy(true);
  S.step = null;
  renderResponse({ loading: true });
  if (S.phone === 'req' && window.matchMedia('(max-width:720px)').matches) setPhone('res');
  try {
    const res = await jsend('POST', '/api/collections/send', { itemUid: S.ed.uid, envUid: S.envUid || undefined, persist: 'keep' });
    if (gen !== S.sendGen) return;
    S.step = res;
    S.consoleLines = (res.console || []).slice(0);
    S.flow = null;
    const id = M.firstFlowId(res);
    if (id) { try { S.flow = await jget('/api/flows/' + id); } catch (e) { S.flow = null; } }
    S.resTab = (res.tests || []).length && M.stepSummary(res).kind !== 'sent' ? 'tests' : S.resTab;
    renderResponse();
    renderConsole();
    if ((res.varChanges || []).length) { await reloadEnvs(); }
    if (M.stepSummary(res).kind === 'blocked') setStatus('warn', M.stepSummary(res).label, M.needsAddHost(res) ? [{ label: 'Add host to scope', run: () => addHostToScope() }] : []);
    else setStatus('', '');
    paintEnvDot((res.unresolved || []).length || null);
  } catch (e) {
    if (gen !== S.sendGen) return;
    S.step = null;
    renderResponse({ error: e });
  } finally {
    if (gen === S.sendGen) { S.busy = false; setSendBusy(false); }
  }
}

export function setSendBusy(on) {
  const b = $('#collSend');
  if (!b) return;
  b.disabled = on;
  b.setAttribute('aria-busy', on ? 'true' : 'false');
}

export async function addHostToScope() {
  const host = M.hostOf(S.step && S.step.url || S.ed.url);
  if (!host) { toast('Could not read the host from the URL'); return; }
  const h = host.replace(/:\d+$/, '');
  const ok = await uiConfirm('Add host to scope', 'Add <b>' + esc(h) + '</b> as an in-scope host for this project? Requests from this collection to it will then be allowed.', 'Add to scope', 'btn accent');
  if (!ok) return;
  try {
    await jsend('POST', '/api/scope', { action: 'include', host: h });
    toast('Added ' + h + ' to scope');
    setStatus('', '');
  } catch (e) { toastError('Could not add the host', e); }
}

/* ------------------------------------------------------------------ response */

export const RES_TABS = [['body', 'Body'], ['headers', 'Headers'], ['tests', 'Tests'], ['examples', 'Examples']];

export function renderResponse(opts = {}) {
  const host = $('#collResponse');
  if (!host) return;
  host.textContent = '';
  if (opts.loading) { const h = el('div'); host.append(h); renderState(h, 'loading', { title: 'Sending request', rows: 4 }); return; }
  if (opts.error) { const h = el('div'); host.append(h); renderState(h, 'error', { title: 'Send failed', message: opts.error.message, status: opts.error.status, onRetry: () => sendCurrent() }); return; }
  if (!S.ed) { const h = el('div'); host.append(h); renderState(h, 'empty-first', { title: 'No response yet', hint: 'Open a request and press Send.' }); return; }
  const sum = M.stepSummary(S.step);
  const bar = el('div', 'coll-resbar');
  if (sum.kind === 'none') {
    bar.append(el('span', 'coll-meta', 'No response yet. Press Send (Ctrl+Enter).'));
    host.append(bar);
    appendExamplesOnly(host);
    return;
  }
  if (sum.kind === 'sent') {
    const code = el('span', 'coll-code', sum.label + (sum.statusText ? ' ' + sum.statusText : ''));
    code.dataset.c = String(Math.floor(sum.status / 100));
    bar.append(code, el('span', 'coll-meta', sum.timeMs + ' ms'), el('span', 'coll-meta', fmtSize(sum.size)));
    const id = M.firstFlowId(S.step);
    if (id) {
      const link = btn('Flow #' + id, () => { if (!openFlow(id, { source: 'collections' })) toast('The flow view is not available yet'); }, 'btn xs', 'link');
      bar.append(link);
    }
    const tc = M.testsLabel(S.step.tests);
    if ((S.step.tests || []).length) bar.append(el('span', 'coll-meta', tc));
  } else {
    bar.append(el('span', 'coll-code', sum.kind === 'blocked' ? 'Blocked' : sum.kind === 'skipped' ? 'Skipped' : 'Error'));
  }
  host.append(bar);

  if (sum.kind === 'blocked' || sum.kind === 'error' || sum.kind === 'skipped') {
    const pane = el('div', 'coll-pane');
    const box = el('div', 'coll-blocked');
    box.dataset.k = sum.kind;
    box.append(el('strong', '', sum.label));
    if ((S.step.unresolved || []).length) box.append(el('span', '', 'Unresolved: ' + S.step.unresolved.join(', ') + '. Define them in the active environment, or choose one.'));
    (S.step.warnings || []).forEach((w) => box.append(el('span', '', w)));
    const acts = el('div', 'coll-chips');
    if ((S.step.unresolved || []).length) acts.append(btn('Edit variables', () => openEnvSheet(), 'btn xs'));
    if (M.needsAddHost(S.step)) acts.append(btn('Add host to scope', () => addHostToScope(), 'btn xs'));
    if (sum.reason === 'scripts_quarantined') acts.append(btn('Review scripts', () => openScriptsSheet(), 'btn xs'));
    if (acts.childNodes.length) box.append(acts);
    pane.append(box);
    (S.step.scripts || []).forEach((n) => pane.append(el('p', 'coll-note', 'Script ' + n.listen + ': ' + n.reason)));
    host.append(pane);
    return;
  }

  const tabs = el('div', 'coll-tabs');
  tabs.setAttribute('role', 'tablist');
  tabs.setAttribute('aria-label', 'Response sections');
  RES_TABS.forEach(([k, label]) => {
    const b = el('button', 'coll-tab');
    b.type = 'button';
    b.setAttribute('role', 'tab');
    b.id = 'collRes-' + k;
    b.setAttribute('aria-selected', S.resTab === k ? 'true' : 'false');
    b.setAttribute('aria-controls', 'collResPane');
    b.tabIndex = S.resTab === k ? 0 : -1;
    b.append(document.createTextNode(label));
    if (k === 'tests' && (S.step.tests || []).length) b.append(el('span', 'coll-n', String(S.step.tests.length)));
    b.addEventListener('click', () => { S.resTab = k; renderResponse(); });
    b.addEventListener('keydown', (e) => {
      const i = RES_TABS.findIndex((x) => x[0] === k);
      const to = e.key === 'ArrowRight' ? (i + 1) % RES_TABS.length : e.key === 'ArrowLeft' ? (i + RES_TABS.length - 1) % RES_TABS.length : -1;
      if (to >= 0) { e.preventDefault(); S.resTab = RES_TABS[to][0]; renderResponse(); $('#collRes-' + S.resTab)?.focus(); }
    });
    tabs.append(b);
  });
  host.append(tabs);
  const pane = el('div', 'coll-pane');
  pane.id = 'collResPane';
  pane.setAttribute('role', 'tabpanel');
  pane.setAttribute('aria-labelledby', 'collRes-' + S.resTab);
  host.append(pane);
  if (S.resTab === 'body') paintResBody(pane);
  else if (S.resTab === 'headers') paintResHeaders(pane);
  else if (S.resTab === 'tests') paintResTests(pane);
  else paintExamples(pane);

  const acts = el('div', 'coll-actions');
  const id = M.firstFlowId(S.step);
  if (id) {
    acts.append(
      btn('Save example', () => saveExample(), 'btn', 'save'),
      btn('Repeater', () => import('./tools.js').then((m) => m.sendToRepeater({ id })).catch((e) => toastError('Repeater', e)), 'btn', 'repeater'),
      btn('Intruder', () => import('./tools.js').then((m) => m.sendToIntruder({ id })).catch((e) => toastError('Intruder', e)), 'btn', 'intruder'),
      btn('Add to finding', (ev) => { const attach = getHook('attachEvidence'); if (attach) attach({ kind: 'flow', refs: [id] }, { anchor: ev.currentTarget }); else toast('Evidence attach is not available yet'); }, 'btn', 'attach'),
      btn('History', () => { getShellApi().activateTab ? getShellApi().activateTab('proxy') : document.querySelector('.tab[data-tab="proxy"]')?.click(); }, 'btn', 'list'));
  }
  host.append(acts);
}

export function fmtSize(n) { return n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(1) + ' KB' : (n / 1048576).toFixed(1) + ' MB'; }

export const BINARY_MIME = /^(image|audio|video|font)\/|octet-stream|zip|pdf|wasm/i;

export function paintResBody(pane) {
  const f = S.flow;
  if (f && f.mime && BINARY_MIME.test(f.mime)) { pane.append(el('p', 'coll-note', 'Binary response (' + f.mime + ', ' + fmtSize(f.resLen || 0) + '). Open the flow in History to inspect it.')); return; }
  if (!f) { pane.append(el('p', 'coll-note', 'The response body is not available. Open the flow in History.')); return; }
  const body = typeof f.resBody === 'string' ? f.resBody : typeof f.body === 'string' ? f.body : '';
  if (!body) {
    const id = M.firstFlowId(S.step);
    const p = el('p', 'coll-note', 'Loading body…');
    pane.append(p);
    if (id) fetchBody(id, pane, p);
    return;
  }
  pane.append(prettyBody(body));
}

export async function fetchBody(id, pane, placeholder) {
  try {
    const raw = await jget('/api/flows/' + id + '/body?side=res');
    if (M.firstFlowId(S.step) !== id || !pane.isConnected) return;
    placeholder.remove();
    const text = typeof raw === 'string' ? raw : JSON.stringify(raw, null, 2);
    if (S.flow) S.flow.resBody = text;
    pane.append(text ? prettyBody(text) : el('p', 'coll-note', 'Empty response body.'));
  } catch (e) { if (placeholder.isConnected) placeholder.textContent = 'Could not load the body: ' + e.message; }
}

export function prettyBody(text) {
  const MAX = 1024 * 1024;
  let shown = text.length > MAX ? text.slice(0, MAX) : text;
  try { shown = JSON.stringify(JSON.parse(shown), null, 2); } catch (e) { /* not JSON */ }
  const pre = el('pre', 'coll-body', shown);
  if (text.length > MAX) { const w = document.createDocumentFragment(); w.append(el('p', 'coll-note', 'Truncated at 1 MB. Open the flow for the full body.'), pre); return w; }
  return pre;
}

export function paintResHeaders(pane) {
  const f = S.flow;
  const h = (f && f.resHeaders) || {};
  const names = Object.keys(h);
  if (!names.length) { pane.append(el('p', 'coll-note', 'No response headers.')); return; }
  const t = el('table', 'coll-hdrs');
  t.setAttribute('aria-label', 'Response headers');
  names.forEach((k) => (Array.isArray(h[k]) ? h[k] : [h[k]]).forEach((v) => { const tr = el('tr'); tr.append(el('td', '', k), el('td', '', v)); t.append(tr); }));
  pane.append(t);
}

export function paintResTests(pane) {
  const tests = (S.step && S.step.tests) || [];
  if (!tests.length) { pane.append(el('p', 'coll-note', 'This request has no tests or assertions.')); return; }
  const ul = el('ul', 'coll-tests');
  ul.setAttribute('aria-label', 'Test results');
  tests.forEach((t) => {
    const li = el('li', 'coll-test');
    li.dataset.s = t.status;
    const tag = el('span', 'coll-tag');
    tag.innerHTML = icon(M.TEST_ICON[t.status] || 'info');
    tag.append(document.createTextNode(M.TEST_TEXT[t.status] || String(t.status).toUpperCase()));
    const body = el('span');
    body.append(el('span', '', t.name));
    const sub = [t.message, t.expected && 'expected ' + t.expected, t.actual && 'got ' + t.actual, t.source && t.source, t.owner && 'in ' + t.owner].filter(Boolean).join(' · ');
    if (sub) body.append(el('span', 'coll-sub', sub));
    li.append(tag, body);
    ul.append(li);
  });
  pane.append(ul);
}

/* ------------------------------------------------------------------ examples */

export function appendExamplesOnly(host) {
  if (!S.ed || !(S.edBase && S.edBase.examples && S.edBase.examples.length)) return;
  const pane = el('div', 'coll-pane');
  paintExamples(pane);
  host.append(pane);
}

export function exampleList() { return (S.edBase && Array.isArray(S.edBase.examples) ? S.edBase.examples : []); }

export function paintExamples(pane) {
  const list = exampleList();
  if (!list.length) { pane.append(el('p', 'coll-note', 'No saved examples. Send the request, then use Save example to keep a response for later comparison.')); return; }
  const ul = el('ul', 'coll-examples');
  ul.setAttribute('aria-label', 'Saved examples');
  list.forEach((ex) => {
    const li = el('li', 'coll-example');
    li.append(el('span', 'coll-code', String(ex.status || '')), el('span', 'grow', ex.name || 'Example'));
    if (ex.flowId) li.append(btn('Flow #' + ex.flowId, () => openFlow(ex.flowId, { source: 'collections' }), 'btn xs', 'link'));
    li.append(btn('Show body', () => showExample(pane, ex), 'btn xs'));
    const del = btn('', async () => {
      try { await jsend('DELETE', '/api/items/' + encodeURIComponent(S.ed.uid) + '/examples/' + encodeURIComponent(ex.id)); S.edBase = await jget('/api/items/' + encodeURIComponent(S.ed.uid)); renderResponse(); } catch (e) { toastError('Could not delete the example', e); }
    }, 'btn xs');
    del.innerHTML = icon('trash');
    del.setAttribute('aria-label', 'Delete example ' + (ex.name || ''));
    li.append(del);
    ul.append(li);
  });
  pane.append(ul);
}
export function showExample(pane, ex) {
  pane.querySelector('.coll-body')?.remove();
  pane.append(el('pre', 'coll-body', typeof ex.body === 'string' ? ex.body : JSON.stringify(ex.body || '', null, 2)));
}

export async function saveExample() {
  if (!S.ed || !S.step) return;
  const name = await uiPrompt({ title: 'Save example', placeholder: 'Example name', value: 'Example ' + (exampleList().length + 1) });
  if (!name) return;
  try {
    const ex = M.exampleFromStep(name, S.step, S.flow ? { resHeaders: S.flow.resHeaders, body: S.flow.resBody } : null);
    await jsend('POST', '/api/items/' + encodeURIComponent(S.ed.uid) + '/examples', ex);
    S.edBase = await jget('/api/items/' + encodeURIComponent(S.ed.uid));
    S.resTab = 'examples';
    renderResponse();
    toast('Example saved');
  } catch (e) { toastError('Could not save the example', e); }
}

/* ------------------------------------------------------------------ console */

export function setConsole(open) {
  S.consoleOpen = open;
  const c = $('#collConsole');
  c.hidden = !open;
  $('#collConsoleBtn').setAttribute('aria-pressed', open ? 'true' : 'false');
  if (open) renderConsole();
}

export function renderConsole() {
  const host = $('#collConsole');
  if (!host || host.hidden) { const b = $('#collConsoleBtn'); if (b) b.title = 'Show the console (Ctrl+`)'; return; }
  host.textContent = '';
  const c = M.consoleCounts(S.consoleLines);
  host.append(el('p', 'coll-note', 'Console: ' + c.log + ' log, ' + c.warn + ' warn, ' + c.error + ' error. Secret values are masked.'));
  if (!S.consoleLines.length) { host.append(el('p', 'coll-note', 'Nothing logged yet. console.log() output from scripts appears here.')); return; }
  S.consoleLines.forEach((l) => {
    const row = el('div', 'coll-log');
    row.dataset.l = l.level === 'warn' || l.level === 'error' ? l.level : 'log';
    row.append(el('span', 'coll-lvl', (l.level || 'log').toUpperCase()), el('span', 'coll-own', l.owner || ''), el('span', '', l.text));
    host.append(row);
  });
}

/* ------------------------------------------------------------------ repeater / curl */

export async function openInRepeater() {
  const id = M.firstFlowId(S.step);
  if (!id) { toast('Send the request once, then open its captured flow in Repeater'); return; }
  try { const m = await import('./tools.js'); await m.sendToRepeater({ id }); getShellApi().activateTab ? getShellApi().activateTab('repeater') : document.querySelector('.tab[data-tab="repeater"]')?.click(); } catch (e) { toastError('Repeater', e); }
}

export async function copyCurl() {
  const id = M.firstFlowId(S.step);
  if (!id) { toast('Send the request once to copy its exact curl command'); return; }
  try { const t = await jget('/api/flows/' + id + '/curl'); copyText(t && t.curl ? t.curl : String(t), 'curl copied'); } catch (e) { toastError('Copy as cURL', e); }
}

