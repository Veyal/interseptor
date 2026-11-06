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

const CHIP_ICON = { error: 'alert', status: 'alert', length: 'diff', time: 'clock', flag: 'finding', match: 'status-done' };

// chipsHTML renders chips with icon + text. In result rows the text is hidden
// by CSS (icon only, so a chip never covers the payload); the full label stays
// as the tooltip and accessible name. `esc` is core's escaper.
export function chipsHTML(r, baseline, esc) {
  return outlierChips(r, baseline)
    .map((c) => `<span class="intr-chip intr-chip-${c.kind}" title="${esc(c.label)}" aria-label="${esc(c.label)}"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-${CHIP_ICON[c.kind] || 'finding'}"/></svg><span class="intr-chip-text">${esc(c.label)}</span></span>`)
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

// intrStopped: a run that is no longer running but never reached its total was
// stopped (Stop button, or the engine closed) rather than finished.
const intrStopped = (running, done, total) => !running && total > 0 && done < total;

// intrProgressLabel is the short progress text in the Results header.
export function intrProgressLabel({ running = false, done = 0, total = 0, capped = false } = {}) {
  if (running) return `running ${done}/${total}`;
  if (!total) return '';
  if (intrStopped(running, done, total)) return `Stopped at ${done}/${total}`;
  return `done ${done}/${total}${capped ? ' (capped)' : ''}`;
}

export function runSummaryText({ done = 0, total = 0, rate = 0, results = [], running = true } = {}) {
  if (!total) return 'No attack running';
  const rows = Array.isArray(results) ? results : [];
  const errors = rows.filter((r) => r && r.error).length;
  const anomalies = rows.filter((r) => r && r.anomaly).length;
  const parts = [intrStopped(running, done, total) ? `Stopped at ${done} of ${total} requests` : `${done} of ${total} requests`];
  if (rate) parts.push(`${rate} req/s`);
  parts.push(plural(errors, 'error', 'errors'), plural(anomalies, 'anomaly', 'anomalies'));
  return parts.join(' · ');
}

// mergeLiveHead folds the state poll's bounded head into the rows already shown.
// The head is the run's first liveHeadCap results and is rewritten in place when
// the run finishes (derived flags are applied then), so it must replace its
// prefix even when it is the same length; the paged tail past the head is kept.
// Replacing only a *longer* head froze a finished run on the un-flagged rows the
// live stream had, and a plain concat would duplicate rows on every poll.
export function mergeLiveHead(displayed, head, newRun = false) {
  const shown = Array.isArray(displayed) ? displayed : [];
  const live = Array.isArray(head) ? head : [];
  if (newRun) return live.slice();
  if (!live.length) return shown.slice();
  return live.concat(shown.slice(live.length));
}

// pollRebasesRunRows: may a state poll rebase the active run's row snapshot?
// Only the pane that is actually showing that run may — the live owner, with the
// run's own attack tab active. While History owns the pane, or the run belongs to
// another attack tab, the displayed array holds something else (another run's
// attempts, or nothing), so adopting it destroys the run's own rows: "Live /
// current run" then restores foreign rows and the pager continues from a foreign
// offset, appending a page that starts in the middle of the run.
export function pollRebasesRunRows(owner, belongsHere) {
  return owner === 'live' && belongsHere === true;
}

// intrCaptureRows: the attempts a finished run contributes to History, or null
// while the pane is showing something other than that run. History must never
// record another run's rows (and its flagged count) under this run's id, and a
// History view shorter than the run must not block the capture forever — the
// run's own view captures it instead.
export function intrCaptureRows(owner, displayed, runRows) {
  if (owner !== 'live') return null;
  const rows = Array.isArray(displayed) ? displayed : runRows;
  return Array.isArray(rows) ? rows.slice() : [];
}

// intrRowSelection: the attempt the result-row actions (Attach, Diff vs
// baseline, the e/d shortcuts) target after a row was clicked or focused. A row
// the attack captured no flow for has no evidence to act on, so the selection
// clears — keeping the previous attempt armed attached or diffed a different
// attempt's response than the one the operator picked.
export function intrRowSelection(rowFlowId) {
  const id = Number(rowFlowId);
  return Number.isSafeInteger(id) && id > 0 ? id : 0;
}

// presetFromEditor snapshots the editor for "Save attack preset". Payload lists
// longer than the editor preview live only in sniperLines/posLines; the tab
// serializer deliberately drops the largest ones to protect localStorage, but a
// preset is saved once and must keep the list it was saved with — otherwise a
// 1000-line list came back as its 40-line preview and the attack silently sent
// the wrong payloads.
export function presetFromEditor(snapshot, name) {
  const s = snapshot || {};
  const lines = (v) => (Array.isArray(v) ? v.map((item) => String(item)) : null);
  return {
    name: String(name == null ? '' : name),
    target: String(s.target || ''), template: String(s.template || ''), type: s.type || 'sniper',
    sniper: String(s.sniper || ''), pos: Array.isArray(s.pos) ? s.pos.map((item) => String(item)) : [],
    sniperSource: s.sniperSource || 'list', sniperNums: { ...(s.sniperNums || {}) },
    posSources: Array.isArray(s.posSources) ? s.posSources.slice() : [],
    posNums: Array.isArray(s.posNums) ? s.posNums.map((n) => ({ ...(n || {}) })) : [],
    threads: s.threads, delay: s.delay, repeat: s.repeat,
    grep: String(s.grep || ''), extract: String(s.extract || ''), proc: String(s.proc || ''),
    sniperLines: lines(s.sniperLines),
    posLines: Array.isArray(s.posLines) ? s.posLines.map((a) => (Array.isArray(a) ? a.map((item) => String(item)) : null)) : [],
    sniperFile: typeof s.sniperFile === 'string' ? s.sniperFile : null,
    posFiles: Array.isArray(s.posFiles) ? s.posFiles.map((f) => (typeof f === 'string' ? f : null)) : [],
  };
}

// INTR_MAX_DELAY_MS bounds the per-request throttle so one typo cannot hold a
// run for the better part of an hour (the server itself accepts any delay).
export const INTR_MAX_DELAY_MS = 60000;

// intrRequestCount is the number of requests an attack will send. Battering
// ram substitutes one payload at every marker, so it sends one request per
// payload; sniper sends one per payload per marker.
export function intrRequestCount(type, counts, markers) {
  const c = Array.isArray(counts) ? counts : [];
  if (type === 'pitchfork') return c.length ? Math.min(...c) : 0;
  if (type === 'cluster') return c.length ? c.reduce((a, b) => a * b, 1) : 0;
  const n = c[0] || 0;
  return type === 'battering' ? n : n * Math.max(markers || 0, 1);
}

// The Intruder has no request cap: payload lists and repeat counts are bounded
// only by what the machine can actually send. These two constants are sanity
// bounds on a single input field, not on the attack — they exist so a typo
// (1e15 sends) is refused up front instead of running for a century. Anything
// inside them runs in full; the server schedules requests lazily.
export const INTR_MAX_REPEAT = 1000000;
export function intrRepeatError(raw) {
  const text = String(raw ?? '').trim();
  const n = Number(text);
  return text !== '' && Number.isInteger(n) && n >= 1 && n <= INTR_MAX_REPEAT
    ? ''
    : `repeat must be between 1 and ${INTR_MAX_REPEAT.toLocaleString()}`;
}

// intrCountLabel is the attack-bar count for Race / repeat: the number of
// sends, or a validation state when the field holds an unusable value.
export function intrCountLabel(type, { repeat } = {}) {
  if (type !== 'repeat') return '';
  if (intrRepeatError(repeat)) return 'invalid repeat';
  const n = Number(repeat);
  return `${n} send${n === 1 ? '' : 's'}`;
}

// INTR_MAX_NUMBERS mirrors maxNumbersValues in internal/intruder/numbers.go: one
// Numbers payload list is materialised, so its length is bounded — and an
// oversized range is refused with this message rather than silently truncated.
export const INTR_MAX_NUMBERS = 1000000;

// intrNumbersError returns a message for a Numbers payload range that cannot
// produce values (empty string when it can). Step 0 means "auto direction".
export function intrNumbersError(cfg) {
  const c = cfg || {};
  if (!Number.isFinite(Number(c.start)) || !Number.isFinite(Number(c.end)) || c.start === '' || c.end === '') return 'enter a start and end';
  if ((c.mode || 'sequence') === 'random') {
    const n = Number(c.count);
    if (!Number.isInteger(n) || n < 1) return 'how many must be a whole number of 1 or more';
    if (n > INTR_MAX_NUMBERS) return `how many is ${n.toLocaleString()} — the maximum is ${INTR_MAX_NUMBERS.toLocaleString()}`;
    return '';
  }
  const step = Number(c.step) || 0;
  if (!Number.isInteger(step)) return 'step must be a whole number';
  const start = Number(c.start), end = Number(c.end);
  if ((step > 0 && start > end) || (step < 0 && start < end)) return 'step goes the wrong way for start and end';
  const stride = step === 0 ? (start > end ? -1 : 1) : step;
  const count = Math.floor((end - start) / stride) + 1;
  if (count > INTR_MAX_NUMBERS) return `this range produces ${count.toLocaleString()} values — the maximum is ${INTR_MAX_NUMBERS.toLocaleString()}; widen the step or narrow the range`;
  return '';
}

// intrValidateRegex returns an error message for a pattern the browser cannot
// compile (the server rejects or silently ignores the same input), else ''.
export function intrValidateRegex(label, pattern) {
  const p = String(pattern || '').trim();
  if (!p) return '';
  try { new RegExp(p); return ''; } catch (e) { return `${label} is not a valid regular expression: ${e.message}`; }
}

// intrValidateTarget requires scheme://host[:port] with an http(s) scheme.
export function intrValidateTarget(value) {
  const v = String(value || '').trim();
  if (!v) return 'enter a target (scheme://host)';
  let u;
  try { u = new URL(v); } catch (e) { return 'target must look like https://host[:port]'; }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'target must start with http:// or https://';
  if (!u.hostname) return 'target needs a host';
  return '';
}

// intrSortRows orders result rows; the default "# ascending" sorts by attempt
// id so threaded completion order does not leak into the table.
export function intrSortRows(rows, sort) {
  const col = (sort && sort.col) || 'id';
  const dir = sort && sort.dir === 'desc' ? -1 : 1;
  // Ties fall back to the attempt id (always ascending) so equal St/Len/Time
  // values keep a stable, predictable order.
  const byId = (a, b) => (a.id || 0) - (b.id || 0);
  return rows.slice().sort((a, b) => {
    const d = col === 'payload'
      ? String(a.payload || '').localeCompare(String(b.payload || '')) * dir
      : ((a[col] || 0) - (b[col] || 0)) * dir;
    return d || byId(a, b);
  });
}

// intrRowTitle is the result row tooltip: the full payload (the cell truncates
// it), then what a click does.
export function intrRowTitle(r) {
  const id = r.id;
  const fid = resultFlowId(r);
  const payload = r.payload == null || r.payload === '' ? '' : String(r.payload);
  const parts = [`#${id}`];
  if (payload) parts.push(payload);
  if (r.error) parts.push(String(r.error));
  else if (fid) parts.push(`open flow #${fid}`);
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
// toastError, openCtxMenu, getResults, openResult, flowPngMenuItems,
// copyPreviewPng, getRunId, runChartPngURL, sendToRepeater, setResultFilter}. Returns
// {onRender, setStep, confirmLarge}.
export function wireIntruderExtras(deps) {
  const doc = document;
  const { $, api, toast, toastError, openCtxMenu, getResults, openResult, flowPngMenuItems, copyPreviewPng, getRunId, runChartPngURL, sendToRepeater, setResultFilter } = deps;
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
    text.textContent = runSummaryText({ done, total, rate: r, results: st.results, running });
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
  const attach = mkButton(doc, 'intrAttach', 'Attach', 'attach');
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
    if (!el) return;
    // Every row owns the selection once picked, including one whose attempt has
    // no captured flow: leaving the previous attempt armed would make Attach /
    // Diff act on a different attempt than the row the operator selected.
    const id = intrRowSelection(rowFlow(el));
    if (id !== active) { active = id; syncActions(); }
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

  // One categorised menu for a result row: the fourteen copy formats and the
  // four PNG variants live in flyouts, so the root stays readable.
  list.addEventListener('contextmenu', async (e) => {
    const row = e.target.closest && e.target.closest('.intr-row');
    const id = rowFlow(row);
    if (!row || !id) return;
    e.preventDefault();
    setActive(row);
    const { COPY_AS_KINDS, copyAs } = await import('./copyas.js');
    const png = flowPngMenuItems ? flowPngMenuItems(id) : [];
    const runId = getRunId ? String(getRunId() || '') : '';
    const chart = (kind, label) => ({ label, act: () => copyPreviewPng(runChartPngURL(runId, kind), 'intruder-' + runId + '-' + kind) });
    openCtxMenu(e.clientX, e.clientY, [
      { head: 'ATTEMPT · flow #' + id, items: [
        { label: 'Open in inspector', icon: 'external', act: () => openResult(row) },
        sendToRepeater ? { label: 'Send to Repeater', icon: 'repeater', act: () => sendToRepeater({ id }) } : null,
        { label: 'Attach as evidence', icon: 'attach', act: () => attachRow(id, row) },
        { label: 'Diff vs baseline', icon: 'diff', act: () => diffRow(id) },
        { label: 'Copy attempt as', icon: 'copy', children: [
          { head: 'REQUEST FORMAT', items: COPY_AS_KINDS.map((k) => ({ label: k.label, val: k.key, act: () => copyAs(k.kind, [{ id }]) })) },
          png.length ? { head: 'IMAGE', items: png } : null,
        ].filter(Boolean) },
        runId && copyPreviewPng && runChartPngURL ? { label: 'Copy run chart as PNG', icon: 'image', children: [
          chart('timeline', 'Timeline'), chart('distribution', 'Status distribution'), chart('race', 'Race window'), chart('strip', 'Payload strip'),
        ] } : null,
        setResultFilter ? { label: 'Filter results', icon: 'filter', children: [
          { label: 'Interesting only', val: 'flagged · match · anomaly', act: () => setResultFilter('interesting') },
          { label: 'Errors only', act: () => setResultFilter('error') },
          { label: 'All results', act: () => setResultFilter('all') },
        ] } : null,
      ].filter(Boolean) },
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
  return { onRender, setStep, confirmLarge, closeDiff: () => diffPanel.close({ focus: false }) };
}
