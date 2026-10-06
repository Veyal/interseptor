// intruder.js — Intruder run summary, determinate progress, outlier chips, the
// Positions / Payloads / Results stepper for narrow widths and the result-row
// evidence actions (attach, diff vs baseline, copy as). Pure helpers are exported
// for node tests; the DOM wiring receives its helpers from tools.js (`deps`) so
// this module never imports core.js.
//
// Nothing here changes how an attack is validated or started: it only reads the
// state the existing poll already renders, and it never fabricates results.

import { createKeyRegistry } from './keys.js';
import { buildDiffPanel } from './repeater.js';

export const LARGE_RUN = 1000;
export const STEPS = ['positions', 'payloads', 'results'];
const SLOW_FACTOR = 3;
const SLOW_MIN_MS = 500;

/* ---- pure helpers ---- */

export function resultFlowId(r) {
  const n = Number(r && (r.flowId || r.flowID));
  return Number.isSafeInteger(n) && n > 0 ? n : 0;
}

const median = (xs) => {
  const s = xs.slice().sort((a, b) => a - b);
  return s.length ? s[Math.floor((s.length - 1) / 2)] : 0;
};

// The usable rows for a baseline: sent without error and with a status.
const usable = (rows) => (Array.isArray(rows) ? rows : []).filter((r) => r && !r.error && Number(r.status) > 0);

// baselineOf summarises "normal" as the most common status plus the median
// length and time of the rows that share it. Returns null without usable rows.
export function baselineOf(rows) {
  const ok = usable(rows);
  if (!ok.length) return null;
  const counts = new Map();
  for (const r of ok) counts.set(r.status, (counts.get(r.status) || 0) + 1);
  let status = ok[0].status;
  for (const [s, n] of counts) if (n > counts.get(status) || (n === counts.get(status) && s < status)) status = s;
  const same = ok.filter((r) => r.status === status);
  return { status, length: median(same.map((r) => Number(r.length) || 0)), timeMs: median(same.map((r) => Number(r.timeMs) || 0)) };
}

// baselineRow picks the captured row to diff against: modal status, length
// closest to the baseline, with a stored flow. Null when none has a flow.
export function baselineRow(rows) {
  const base = baselineOf(rows);
  if (!base) return null;
  let best = null;
  for (const r of usable(rows)) {
    if (r.status !== base.status || !resultFlowId(r)) continue;
    const d = Math.abs((Number(r.length) || 0) - base.length);
    if (!best || d < best.d) best = { r, d };
  }
  return best ? best.r : null;
}

// outlierChips returns text-bearing chips (kind is a CSS hook, label is shown).
export function outlierChips(r, baseline) {
  if (!r) return [];
  if (r.error) return [{ kind: 'error', label: 'Error' }];
  const chips = [];
  if (baseline) {
    if (r.status !== baseline.status) chips.push({ kind: 'status', label: 'Status differs' });
    if (r.anomaly) chips.push({ kind: 'length', label: (Number(r.length) || 0) >= baseline.length ? 'Longer' : 'Shorter' });
    const t = Number(r.timeMs) || 0;
    if (t >= SLOW_MIN_MS && t >= Math.max(baseline.timeMs, 1) * SLOW_FACTOR) chips.push({ kind: 'time', label: 'Slower' });
  } else if (r.anomaly) {
    chips.push({ kind: 'length', label: 'Length anomaly' });
  }
  if (r.flagged) chips.push({ kind: 'flag', label: 'Flagged' });
  if (r.matched) chips.push({ kind: 'match', label: 'Matched' });
  return chips;
}

const CHIP_ICON = { error: 'alert-tri', status: 'alert-tri', length: 'diff', time: 'clock', flag: 'flag', match: 'check-circle' };

// chipsHTML renders chips with icon + visible text. `esc` is core's escaper.
export function chipsHTML(r, baseline, esc) {
  return outlierChips(r, baseline)
    .map((c) => `<span class="intr-chip intr-chip-${c.kind}"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-${CHIP_ICON[c.kind] || 'flag'}"/></svg>${esc(c.label)}</span>`)
    .join('');
}

export function progressModel(done, total) {
  const max = Math.max(0, Number(total) || 0);
  if (!max) return { value: 0, max: 1, percent: 0, text: 'No attack running' };
  const value = Math.min(max, Math.max(0, Number(done) || 0));
  const percent = Math.round((value / max) * 100);
  return { value, max, percent, text: `${value} of ${max} requests (${percent}%)` };
}

// createRateTracker reports requests per second since the first sample of a run.
export function createRateTracker(now = Date.now) {
  let t0 = 0;
  return {
    reset() { t0 = 0; },
    update(done, running) {
      if (!t0) { t0 = now(); return 0; }
      const secs = (now() - t0) / 1000;
      return running && secs > 0 ? Math.round((done / secs) * 10) / 10 : 0;
    },
  };
}

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

export function runSummaryText({ done = 0, total = 0, rate = 0, results = [] } = {}) {
  if (!total) return 'No attack running';
  const rows = Array.isArray(results) ? results : [];
  const errors = rows.filter((r) => r && r.error).length;
  const anomalies = rows.filter((r) => r && r.anomaly).length;
  const parts = [`${done} of ${total} requests`];
  if (rate) parts.push(`${rate} req/s`);
  parts.push(plural(errors, 'error', 'errors'), plural(anomalies, 'anomaly', 'anomalies'));
  return parts.join(' · ');
}

export const needsLargeRunConfirm = (n) => Number.isFinite(n) && n > LARGE_RUN;

export function stepperNext(step, delta) {
  const i = STEPS.indexOf(step);
  if (i < 0) return delta > 0 ? 'payloads' : 'positions';
  return STEPS[Math.max(0, Math.min(STEPS.length - 1, i + delta))];
}

/* ---- DOM wiring ---- */

const SVG_NS = 'http://www.w3.org/2000/svg';
function iconNode(doc, name) {
  const svg = doc.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = doc.createElementNS(SVG_NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  return svg;
}
function mkButton(doc, id, text, icon, cls = 'btn xs') {
  const b = doc.createElement('button');
  b.type = 'button';
  b.id = id;
  b.className = cls;
  if (icon) b.appendChild(iconNode(doc, icon));
  b.appendChild(doc.createTextNode((icon ? ' ' : '') + text));
  return b;
}

// wireIntruderExtras(deps) is called once from intrInit. deps: {$, api, toast,
// toastError, openCtxMenu, getResults, openResult}. Returns
// {onRender, setStep, confirmLarge}.
export function wireIntruderExtras(deps) {
  const doc = document;
  const { $, api, toast, toastError, openCtxMenu, getResults, openResult } = deps;
  const work = doc.querySelector('#panel-intruder .intr-work');
  const resultsPane = doc.querySelector('#panel-intruder .intr-results');
  const list = $('#intrResults');
  const head = doc.querySelector('#panel-intruder .intr-head');
  const finding = $('#intrToFinding');
  if (!work || !resultsPane || !list || !head || !finding || $('#intrSummary')) return null;

  /* run summary + determinate progress */
  const summary = doc.createElement('div');
  summary.id = 'intrSummary';
  summary.className = 'intr-summary';
  summary.hidden = true;
  const progress = doc.createElement('progress');
  progress.id = 'intrProgress2';
  progress.className = 'intr-progress';
  progress.setAttribute('aria-label', 'Attack progress');
  progress.max = 1;
  progress.value = 0;
  const text = doc.createElement('span');
  text.id = 'intrSummaryText';
  text.className = 'intr-summary-text';
  const live = doc.createElement('span');
  live.id = 'intrSummaryLive';
  live.className = 'u-sr';
  live.setAttribute('role', 'status');
  live.setAttribute('aria-live', 'polite');
  summary.append(progress, text, live);
  resultsPane.insertBefore(summary, head);
  const rate = createRateTracker();
  let lastRunning = false;

  function onRender(st) {
    const total = Number(st.total) || 0;
    const done = Number(st.done) || 0;
    const running = !!st.running;
    if (running && !lastRunning) rate.reset();
    const r = running ? rate.update(done, true) : 0;
    const p = progressModel(done, total);
    summary.hidden = !total && !running;
    progress.max = p.max;
    progress.value = p.value;
    progress.setAttribute('aria-valuetext', p.text);
    text.textContent = runSummaryText({ done, total, rate: r, results: st.results });
    if (running !== lastRunning) live.textContent = running ? 'Attack started' : (total ? `Attack finished: ${p.text}` : '');
    lastRunning = running;
    syncActions();
  }

  /* stepper (narrow widths) */
  const stepper = doc.createElement('div');
  stepper.id = 'intrStepper';
  stepper.className = 'seg intr-stepper';
  stepper.setAttribute('role', 'group');
  stepper.setAttribute('aria-label', 'Attack step');
  const names = { positions: 'Positions', payloads: 'Payloads', results: 'Results' };
  STEPS.forEach((s, i) => {
    const b = doc.createElement('button');
    b.type = 'button';
    b.dataset.step = s;
    b.textContent = `${i + 1}. ${names[s]}`;
    stepper.appendChild(b);
  });
  work.parentNode.insertBefore(stepper, work);
  function setStep(s) {
    work.dataset.step = s;
    stepper.querySelectorAll('button').forEach((b) => {
      const on = b.dataset.step === s;
      b.classList.toggle('on', on);
      b.setAttribute('aria-pressed', String(on));
    });
  }
  stepper.addEventListener('click', (e) => { const b = e.target.closest && e.target.closest('button[data-step]'); if (b) setStep(b.dataset.step); });
  stepper.addEventListener('keydown', (e) => {
    const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
    if (!d) return;
    e.preventDefault();
    const s = stepperNext(work.dataset.step, d);
    setStep(s);
    const b = stepper.querySelector(`[data-step="${s}"]`);
    if (b) b.focus();
  });
  setStep('positions');

  /* large-run inline confirm (never a modal) */
  const confirmRow = doc.createElement('div');
  confirmRow.id = 'intrConfirmRow';
  confirmRow.className = 'intr-confirm';
  confirmRow.setAttribute('role', 'alert');
  confirmRow.hidden = true;
  const confirmText = doc.createElement('span');
  const confirmGo = mkButton(doc, 'intrConfirmGo', 'Send anyway', null, 'btn xs btn-primary');
  const confirmNo = mkButton(doc, 'intrConfirmCancel', 'Cancel', null);
  confirmRow.append(confirmText, confirmGo, confirmNo);
  const bar = doc.querySelector('#panel-intruder .intr-bar');
  bar.parentNode.insertBefore(confirmRow, bar.nextSibling);
  let pendingGo = null;
  function confirmLarge(count, go) {
    confirmText.textContent = `This attack will send ${count.toLocaleString()} requests (over ${LARGE_RUN.toLocaleString()}).`;
    pendingGo = go;
    confirmRow.hidden = false;
    confirmGo.focus();
  }
  confirmGo.addEventListener('click', () => { const go = pendingGo; pendingGo = null; confirmRow.hidden = true; if (go) go(); });
  confirmNo.addEventListener('click', () => { pendingGo = null; confirmRow.hidden = true; const s = $('#intrStart'); if (s) s.focus(); });

  /* row actions: attach, diff vs baseline, copy as */
  let active = 0;
  const attach = mkButton(doc, 'intrAttach', 'Attach', 'paperclip');
  const diff = mkButton(doc, 'intrDiffBtn', 'Diff vs baseline', 'diff');
  attach.title = 'Attach the selected attempt as evidence';
  diff.title = 'Compare the selected attempt with the baseline response';
  finding.parentNode.insertBefore(attach, finding);
  finding.parentNode.insertBefore(diff, finding);
  const diffPanel = buildDiffPanel(doc, { prefix: 'intrDiff', title: 'Diff vs baseline', host: resultsPane, hide: [head, list], api, toast, returnFocus: () => diff });

  function syncActions() {
    const base = baselineRow(getResults());
    attach.disabled = !active;
    attach.title = active ? 'Attach the selected attempt as evidence' : 'Select a result row first (click it or move focus to it)';
    diff.disabled = !active || !base;
    diff.title = !active ? 'Select a result row first' : !base ? 'No baseline response with a captured flow yet' : 'Compare the selected attempt with the baseline response';
  }
  const rowFlow = (el) => Number(el && el.dataset && el.dataset.flow) || 0;
  function setActive(el) {
    const id = rowFlow(el);
    if (id && id !== active) { active = id; syncActions(); }
  }
  list.addEventListener('focusin', (e) => setActive(e.target.closest && e.target.closest('.intr-row')));
  list.addEventListener('click', (e) => setActive(e.target.closest && e.target.closest('.intr-row')));

  async function attachRow(id, anchor) {
    if (!id) { toast('No captured flow for this attempt', 'warn'); return; }
    try {
      const { attachEvidence } = await import('./evidence-attach.js');
      await attachEvidence({ kind: 'flow', refs: [id] }, { anchor });
    } catch (e) { toastError('Could not attach evidence', e); }
  }
  async function diffRow(id) {
    const base = baselineRow(getResults());
    if (!base) { toast('No baseline response with a captured flow yet', 'warn'); return; }
    const baseId = resultFlowId(base);
    if (!id || id === baseId) { toast(id === baseId ? 'This attempt is the baseline' : 'No captured flow for this attempt', 'warn'); return; }
    await diffPanel.open(baseId, id);
  }
  attach.addEventListener('click', () => attachRow(active, attach));
  diff.addEventListener('click', () => diffRow(active));

  list.addEventListener('contextmenu', async (e) => {
    const row = e.target.closest && e.target.closest('.intr-row');
    const id = rowFlow(row);
    if (!row || !id) return;
    e.preventDefault();
    setActive(row);
    const { COPY_AS_KINDS, copyAs } = await import('./copyas.js');
    openCtxMenu(e.clientX, e.clientY, [
      { items: [
        { label: 'Open flow', act: () => openResult(row) },
        { label: 'Attach as evidence', act: () => attachRow(id, row) },
        { label: 'Diff vs baseline', act: () => diffRow(id) },
      ] },
      { head: 'Copy request as', items: COPY_AS_KINDS.map((k) => ({ label: k.label, act: () => copyAs(k.kind, [{ id }]) })) },
    ], null);
  });

  /* single-key shortcuts (scoped, typing-gated, switchable); every one has a button above */
  const registry = createKeyRegistry({ isModalOpen: () => !!doc.querySelector('.modal-bg.show, [aria-modal="true"]:not([hidden])') });
  registry.register({ id: 'intruder.attach', keys: 'e', scope: 'intruder-results', label: 'Attach attempt as evidence', group: 'Intruder', run: (e) => attachRow(rowFlow(e.target.closest('.intr-row')), e.target.closest('.intr-row')) });
  registry.register({ id: 'intruder.diff', keys: 'd', scope: 'intruder-results', label: 'Diff attempt vs baseline', group: 'Intruder', run: (e) => diffRow(rowFlow(e.target.closest('.intr-row'))) });
  list.addEventListener('keydown', (e) => {
    if (e.defaultPrevented || !e.target.closest || !e.target.closest('.intr-row')) return;
    registry.handle(e, { scopes: ['intruder-results'] });
  });

  setStep('positions');
  syncActions();
  return { onRender, setStep, confirmLarge };
}
