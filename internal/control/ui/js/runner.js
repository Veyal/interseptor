// runner.js — collection runner view: options, live progress, per-request
// results with tests/console/variable changes, pause/resume/abort, the
// persist-ask prompt and rerun failed. The view mounts into a host element
// (the Collections panel owns placement), so it needs no sheet id.
//
// A run is owned by the server (POST /api/runner/runs). The view follows it by
// polling /api/runner/runs/{uid}?since=N and by the global {type:'collrun'}
// event stream, which nudges an immediate poll; both feed the same pure
// reducers in runner-model.js. Closing the view never aborts the run.
//
//   mountRunner(host, { collectionUid, collectionName, folderUid, itemUids, envUid })
//     -> { run(), rerunFailed(), abort(), destroy() }
//
// Also registered as the 'collectionRunner' hook. Pure logic lives in
// runner-model.js. All API text is rendered with textContent; secrets arrive
// already masked by the server.

import { api, toast, toastError, registerHook, openFlow, icon, wireRowKey } from './core.js';
import { renderState } from './statepanel.js';
import { registerSseHandler } from './shell-hooks.js';
import {
  BAIL_MODES, PERSIST_MODES, buildRunBody, summarize, summaryText, failedItemUids, filterRows,
  stateIcon, newLive, applyProgress, applyEvent, runControls, phaseLabel,
} from './runner-model.js';

const POLL_MS = 700;

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

function renderDetail(host, r) {
  host.textContent = '';
  const d = r.detail || {};
  const head = el('div', 'row');
  head.appendChild(el('strong', null, `${r.n}. ${r.name}`));
  host.appendChild(head);
  if (r.error) host.appendChild(el('p', 'field-error', r.error));
  if (r.blockReason) host.appendChild(el('p', 'kv-dim', 'Blocked: ' + r.blockReason));
  if (r.quarantined) host.appendChild(el('p', 'kv-dim', `${r.quarantined} script(s) not approved and skipped`));
  const tests = (d.tests || []).map((t) => `${String(t.status).toUpperCase()}  ${t.name}${t.message ? ' - ' + t.message : ''}`);
  host.appendChild(detailBlock('Tests', tests, 'No tests ran.'));
  const con = (d.console || []).map((c) => `[${c.level}] ${c.text}`);
  host.appendChild(detailBlock('Console', con, 'No console output.'));
  const vars = (d.varChanges || []).map((v) => `${v.scope}.${v.key} = ${v.unset ? '(unset)' : v.value}`);
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
  const st = { live: newLive(), busy: false, filter: 'all', sel: -1, itemUids: opts.itemUids || [], timer: 0, polling: false, closed: false };

  host.textContent = '';
  host.classList.add('runner-view');
  const title = el('h2', 'kv-dim', 'Run: ' + (opts.collectionName || 'collection'));

  const form = el('div', 'row');
  const delay = el('input', 'btn');
  delay.type = 'number'; delay.min = '0'; delay.max = '60000'; delay.value = '0';
  const bail = select(BAIL_MODES.map((m) => [m, m === 'none' ? 'Never' : m === 'on-failure' ? 'On failure' : 'On error']), 'none');
  const persist = select(PERSIST_MODES.map((m) => [m, m === 'ask' ? 'Ask when done' : m === 'keep' ? 'Keep variable writes' : 'Discard variable writes']), 'ask');
  const noScripts = el('input'); noScripts.type = 'checkbox';
  form.append(
    labelled(`runDelay${uid}`, 'Delay (ms)', delay),
    labelled(`runBail${uid}`, 'Stop', bail),
    labelled(`runPersist${uid}`, 'Persist', persist),
    labelled(`runNoScripts${uid}`, 'Skip scripts', noScripts),
  );

  const actions = el('div', 'row');
  const mkBtn = (cls, text) => { const b = el('button', cls, text); b.type = 'button'; return b; };
  const runBtn = mkBtn('btn primary', 'Run');
  const pauseBtn = mkBtn('btn', 'Pause');
  const resumeBtn = mkBtn('btn', 'Resume');
  const abortBtn = mkBtn('btn', 'Abort');
  const rerunBtn = mkBtn('btn', 'Rerun failed');
  const filterBtn = mkBtn('btn', 'Show problems only');
  filterBtn.setAttribute('aria-pressed', 'false');
  actions.append(runBtn, pauseBtn, resumeBtn, abortBtn, rerunBtn, filterBtn);

  const live = el('div', 'statusline'); live.setAttribute('role', 'status'); live.setAttribute('aria-live', 'polite');
  const progress = el('progress', 'run-progress'); progress.max = 1; progress.value = 0; progress.hidden = true;
  progress.setAttribute('aria-label', 'Run progress');
  const ask = el('section', 'run-persist-ask'); ask.hidden = true; ask.setAttribute('role', 'group');
  ask.setAttribute('aria-label', 'Variable changes made by scripts');
  const body = el('div', 'run-body');
  const detail = el('div', 'run-detail');
  host.append(title, form, actions, live, progress, ask, body, detail);

  function renderRows() {
    body.textContent = '';
    body.removeAttribute('aria-busy');
    const rows = st.live.rows;
    const shown = filterRows(rows, st.filter);
    if (!rows.length) {
      if (st.busy) { renderState(body, 'loading', { rows: 3 }); body.setAttribute('aria-busy', 'true'); return; }
      renderState(body, 'empty-first', { title: 'No run yet', hint: 'Choose options and press Run.' });
      return;
    }
    if (!shown.length) {
      const sm = summarize(rows);
      const hint = sm.assertions ? 'No failed, blocked or HTTP error requests.' : `No problems found, but nothing was asserted: ${sm.total} request${sm.total === 1 ? ' was' : 's were'} sent without assertions.`;
      renderState(body, 'empty-filtered', { title: 'No problems', hint, onClear: toggleFilter });
      return;
    }
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

  function selectRow(r) {
    st.sel = r.n - 1;
    renderRows();
    renderDetail(detail, r);
  }

  function renderAsk() {
    ask.textContent = '';
    const c = runControls(st.live);
    ask.hidden = !c.decide;
    if (!c.decide) return;
    ask.appendChild(el('p', null, 'Scripts changed variables during this run. Keep the changes as current values, or discard them?'));
    const writes = st.live.pending.map((v) => `${v.scope}.${v.key} = ${v.unset ? '(unset)' : (v.value || '(empty)')}`);
    ask.appendChild(detailBlock('Pending variable changes', writes, 'None.'));
    const row = el('div', 'row');
    const keep = mkBtn('btn primary', 'Keep changes');
    const drop = mkBtn('btn', 'Discard changes');
    keep.addEventListener('click', () => decide(true));
    drop.addEventListener('click', () => decide(false));
    row.append(keep, drop);
    ask.appendChild(row);
  }

  function renderStatus() {
    const l = st.live;
    const s = summarize(l.rows);
    const planned = l.planned ? ` (${l.rows.length}/${l.planned})` : '';
    live.textContent = l.rows.length || l.runUid ? `${phaseLabel(l.status)}${l.finished ? '' : planned}. ${summaryText(s)}.` : (st.busy ? 'Starting...' : '');
    progress.hidden = !(l.planned && !l.finished);
    if (l.planned) { progress.max = l.planned; progress.value = Math.min(l.rows.length, l.planned); }
    const c = runControls(l);
    pauseBtn.hidden = !c.pause;
    resumeBtn.hidden = !c.resume;
    abortBtn.hidden = !c.abort;
    runBtn.disabled = st.busy && !l.finished;
    rerunBtn.disabled = (st.busy && !l.finished) || !failedItemUids(l.rows).length;
  }

  function render() { renderRows(); renderAsk(); renderStatus(); }

  function toggleFilter() {
    st.filter = st.filter === 'all' ? 'problems' : 'all';
    filterBtn.setAttribute('aria-pressed', String(st.filter !== 'all'));
    filterBtn.textContent = st.filter === 'all' ? 'Show problems only' : 'Show all';
    renderRows();
  }

  const runPath = (suffix = '') => '/api/runner/runs/' + encodeURIComponent(st.live.runUid) + suffix;
  const post = (suffix, payload) => api(runPath(suffix), {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload || {}),
  });

  async function poll() {
    if (st.polling || st.closed || !st.live.runUid) return;
    st.polling = true;
    try {
      const p = await api(runPath('?since=' + st.live.since));
      st.live = applyProgress(st.live, p);
      st.error = '';
    } catch (e) {
      // A transient failure keeps the last known state and retries.
      st.error = (e && e.message) || 'poll failed';
    } finally { st.polling = false; }
    render();
    if (st.live.finished) { finish(); return; }
    schedule();
  }

  function schedule() {
    clearTimeout(st.timer);
    if (st.closed || st.live.finished) return;
    st.timer = setTimeout(poll, POLL_MS);
  }

  function finish() {
    st.busy = false;
    clearTimeout(st.timer);
    render();
    const status = st.live.status;
    if (status && status !== 'done') toast('Run ' + phaseLabel(status).toLowerCase(), 'warn');
  }

  // The global event stream nudges an immediate refresh for this run only.
  const offSse = registerSseHandler('collrun', (m) => {
    const e = m && m.event;
    if (!e || !st.live.runUid || e.runUid !== st.live.runUid) return;
    st.live = applyEvent(st.live, e);
    render();
    if (st.live.finished) poll(); // pick up the final report
  });

  async function run(extra = {}) {
    if (st.busy && !st.live.finished) return null;
    let payload;
    try {
      payload = buildRunBody({
        collectionUid: opts.collectionUid, folderUid: opts.folderUid, itemUids: st.itemUids, envUid: opts.envUid,
        persist: persist.value, bail: bail.value, delayMs: delay.value, noScripts: noScripts.checked, ...extra,
      });
    } catch (e) { toastError('Run', e); return null; }
    st.busy = true;
    st.live = newLive();
    st.sel = -1;
    detail.textContent = '';
    render();
    try {
      const out = await api('/api/runner/runs', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload),
      });
      st.live = { ...st.live, runUid: out.runUid || '', planned: out.plannedSteps || 0, status: out.status || 'running' };
      render();
      await poll();
      return out;
    } catch (e) {
      st.busy = false;
      renderState(body, 'error', { message: e && e.message, onRetry: () => run(extra) });
      live.textContent = 'Run failed.';
      return null;
    }
  }

  async function control(suffix, label) {
    try { await post(suffix); await poll(); } catch (e) { toastError(label, e); }
  }
  async function decide(keep) {
    try {
      await post('/persist', { keep });
      toast(keep ? 'Variable changes kept' : 'Variable changes discarded');
      await poll();
    } catch (e) { toastError('Persist decision', e); }
  }

  const abort = () => control('/abort', 'Abort');
  const rerunFailed = () => run({ itemUids: failedItemUids(st.live.rows), folderUid: '' });

  runBtn.addEventListener('click', () => run());
  pauseBtn.addEventListener('click', () => control('/pause', 'Pause'));
  resumeBtn.addEventListener('click', () => control('/resume', 'Resume'));
  abortBtn.addEventListener('click', abort);
  rerunBtn.addEventListener('click', rerunFailed);
  filterBtn.addEventListener('click', toggleFilter);
  render();

  return {
    run, rerunFailed, abort,
    destroy() {
      st.closed = true;
      clearTimeout(st.timer);
      offSse();
      host.textContent = '';
      host.classList.remove('runner-view');
    },
  };
}

registerHook('collectionRunner', mountRunner);
