// runner.js — collection runner view: options, run, per-request results with
// tests/console/variable changes, rerun failed. The view mounts into a host
// element (the Collections panel owns placement), so it needs no sheet id.
//
//   mountRunner(host, { collectionUid, collectionName, folderUid, itemUids, envUid })
//     -> { run(), rerunFailed(), destroy() }
//
// Also registered as the 'collectionRunner' hook. Pure logic lives in
// runner-model.js. All API text is rendered with textContent; secrets arrive
// already masked by the server.

import { api, toast, toastError, registerHook, openFlow, icon, wireRowKey } from './core.js';
import { renderState } from './statepanel.js';
import {
  BAIL_MODES, PERSIST_MODES, buildRunBody, normalizeRows, summarize, summaryText, failedItemUids, filterRows,
  stateIcon,
} from './runner-model.js';

let seq = 0;

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

function labelled(id, text, control) {
  const wrap = el('div', 'field');
  const l = el('label', null, text);
  l.htmlFor = id;
  control.id = id;
  wrap.append(l, control);
  return wrap;
}

function select(options, value) {
  const s = el('select', 'btn');
  for (const [v, t] of options) {
    const o = el('option', null, t);
    o.value = v;
    s.appendChild(o);
  }
  s.value = value;
  return s;
}

function stateBadge(r) {
  const b = el('span', 'chip run-' + r.state);
  const i = el('span');
  i.innerHTML = icon(stateIcon(r.state));
  b.append(i, document.createTextNode(' ' + r.label));
  return b;
}

function detailBlock(title, lines, emptyText) {
  const wrap = el('section', 'run-detail-block');
  wrap.appendChild(el('h3', 'kv-dim', title));
  if (!lines.length) { wrap.appendChild(el('p', 'kv-dim', emptyText)); return wrap; }
  const ul = el('ul');
  for (const t of lines) ul.appendChild(el('li', null, t));
  wrap.appendChild(ul);
  return wrap;
}

function parseResult(row) {
  if (!row || !row.resultJson) return {};
  try { return JSON.parse(row.resultJson) || {}; } catch (e) { return {}; }
}

function renderDetail(host, r, res) {
  host.textContent = '';
  const head = el('div', 'row');
  head.appendChild(el('strong', null, `${r.n}. ${r.name}`));
  host.appendChild(head);
  if (r.error) host.appendChild(el('p', 'field-error', r.error));
  if (r.blockReason) host.appendChild(el('p', 'kv-dim', 'Blocked: ' + r.blockReason));
  if (r.quarantined) host.appendChild(el('p', 'kv-dim', `${r.quarantined} script(s) not approved and skipped`));
  const tests = (res.tests || []).map((t) => `${t.status.toUpperCase()}  ${t.name}${t.message ? ' - ' + t.message : ''}`);
  host.appendChild(detailBlock('Tests', tests, 'No tests ran.'));
  const con = (res.console || []).map((c) => `[${c.level}] ${c.text}`);
  host.appendChild(detailBlock('Console', con, 'No console output.'));
  const vars = (res.varChanges || []).map((v) => `${v.scope}.${v.key} = ${v.unset ? '(unset)' : v.value}`);
  host.appendChild(detailBlock('Variable changes', vars, 'No variable changes.'));
  if (r.flowId) {
    const b = el('button', 'btn', 'Open flow #' + r.flowId);
    b.type = 'button';
    b.addEventListener('click', () => openFlow(r.flowId));
    host.appendChild(b);
  }
}

export function mountRunner(host, opts = {}) {
  const uid = ++seq;
  const st = { rows: [], runUid: '', busy: false, filter: 'all', sel: -1, results: null, itemUids: opts.itemUids || [] };

  host.textContent = '';
  host.classList.add('runner-view');
  const title = el('h2', 'kv-dim', 'Run: ' + (opts.collectionName || 'collection'));

  const form = el('div', 'row');
  const delay = el('input', 'btn');
  delay.type = 'number'; delay.min = '0'; delay.max = '60000'; delay.value = '0';
  const bail = select(BAIL_MODES.map((m) => [m, m === 'none' ? 'Never' : m === 'on-failure' ? 'On failure' : 'On error']), 'none');
  const persist = select(PERSIST_MODES.map((m) => [m, m === 'keep' ? 'Keep variable writes' : 'Discard variable writes']), 'discard');
  const noScripts = el('input'); noScripts.type = 'checkbox';
  form.append(
    labelled(`runDelay${uid}`, 'Delay (ms)', delay),
    labelled(`runBail${uid}`, 'Stop', bail),
    labelled(`runPersist${uid}`, 'Persist', persist),
    labelled(`runNoScripts${uid}`, 'Skip scripts', noScripts),
  );

  const actions = el('div', 'row');
  const runBtn = el('button', 'btn primary', 'Run'); runBtn.type = 'button';
  const rerunBtn = el('button', 'btn', 'Rerun failed'); rerunBtn.type = 'button'; rerunBtn.disabled = true;
  const filterBtn = el('button', 'btn', 'Show problems only'); filterBtn.type = 'button';
  filterBtn.setAttribute('aria-pressed', 'false');
  actions.append(runBtn, rerunBtn, filterBtn);

  const live = el('div', 'statusline'); live.setAttribute('role', 'status'); live.setAttribute('aria-live', 'polite');
  const body = el('div', 'run-body');
  const detail = el('div', 'run-detail');
  host.append(title, form, actions, live, body, detail);

  function renderRows() {
    body.textContent = '';
    body.removeAttribute('aria-busy');
    const shown = filterRows(st.rows, st.filter);
    if (!st.rows.length) { renderState(body, 'empty-first', { title: 'No run yet', hint: 'Choose options and press Run.' }); return; }
    if (!shown.length) { renderState(body, 'empty-filtered', { title: 'No problems', hint: 'Every request passed.', onClear: toggleFilter }); return; }
    const wrap = el('div', 'md-table-wrap');
    const tbl = el('table', 'rules-tbl');
    tbl.setAttribute('aria-label', 'Run results');
    const hr = el('tr');
    for (const h of ['#', 'Request', 'Result', 'Status', 'Tests', 'Flow']) { const th = el('th', null, h); th.scope = 'col'; hr.appendChild(th); }
    const thead = el('thead'); thead.appendChild(hr); tbl.appendChild(thead);
    const tb = el('tbody');
    for (const r of shown) {
      const tr = el('tr');
      tr.tabIndex = 0;
      tr.setAttribute('aria-selected', r.n - 1 === st.sel ? 'true' : 'false');
      const cells = [el('td', null, String(r.n)), el('td', null, r.name), el('td'), el('td', null, r.status ? String(r.status) : '-'),
        el('td', null, r.testsText), el('td', null, r.flowId ? '#' + r.flowId : '-')];
      cells[2].appendChild(stateBadge(r));
      tr.append(...cells);
      const pick = () => selectRow(r);
      tr.addEventListener('click', pick);
      wireRowKey(tr, pick);
      tb.appendChild(tr);
    }
    tbl.appendChild(tb);
    wrap.appendChild(tbl);
    body.appendChild(wrap);
  }

  async function selectRow(r) {
    st.sel = r.n - 1;
    renderRows();
    if (st.results == null && st.runUid) {
      try { st.results = (await api('/api/runs/' + encodeURIComponent(st.runUid))).results || []; } catch (e) { st.results = []; toastError('Run detail', e); }
    }
    const nth = st.rows.slice(0, r.n).filter((x) => x.itemUid === r.itemUid).length;
    const res = parseResult((st.results || []).filter((x) => x.itemUid === r.itemUid)[nth - 1]);
    renderDetail(detail, r, res);
  }

  function toggleFilter() {
    st.filter = st.filter === 'all' ? 'problems' : 'all';
    filterBtn.setAttribute('aria-pressed', String(st.filter !== 'all'));
    filterBtn.textContent = st.filter === 'all' ? 'Show problems only' : 'Show all';
    renderRows();
  }

  function setBusy(on) {
    st.busy = on;
    runBtn.disabled = on;
    rerunBtn.disabled = on || !failedItemUids(st.rows).length;
    if (on) { body.setAttribute('aria-busy', 'true'); live.textContent = 'Running...'; }
  }

  async function run(extra = {}) {
    if (st.busy) return null;
    let payload;
    try {
      payload = buildRunBody({
        collectionUid: opts.collectionUid, folderUid: opts.folderUid, itemUids: st.itemUids, envUid: opts.envUid,
        persist: persist.value, bail: bail.value, delayMs: delay.value, noScripts: noScripts.checked, ...extra,
      });
    } catch (e) { toastError('Run', e); return null; }
    setBusy(true);
    renderState(body, 'loading', { rows: 4 });
    try {
      const out = await api('/api/collections/run', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
      });
      st.runUid = out.runUid || '';
      st.results = null;
      st.sel = -1;
      detail.textContent = '';
      st.rows = normalizeRows(out.results);
      const s = summarize(st.rows);
      live.textContent = `${summaryText(s)}. Status: ${out.status || 'done'}.`;
      if (out.status && out.status !== 'done') toast('Run ' + out.status, 'warn');
      renderRows();
      return out;
    } catch (e) {
      renderState(body, 'error', { message: e && e.message, onRetry: () => run(extra) });
      live.textContent = 'Run failed.';
      return null;
    } finally { setBusy(false); }
  }

  const rerunFailed = () => run({ itemUids: failedItemUids(st.rows), folderUid: '' });

  runBtn.addEventListener('click', () => run());
  rerunBtn.addEventListener('click', rerunFailed);
  filterBtn.addEventListener('click', toggleFilter);
  renderRows();

  return { run, rerunFailed, destroy() { host.textContent = ''; host.classList.remove('runner-view'); } };
}

registerHook('collectionRunner', mountRunner);
