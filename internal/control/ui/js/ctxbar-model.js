// ctxbar-model.js — the pure view-model behind the Flight Bar's readiness ring and
// its popover. No DOM and no core.js: every rule runs under `node --test`.
//
// The server decides readiness; project-state.js folds it. This file only words
// and shapes what is already there for the ring, the popover meter and the
// blocker checklist. It never decides that a finding passed or failed.
//
// Readiness is report-time information: the ready fraction, the arc and the
// blocker count appear only while the Report view is open (the same signal that
// shows the Findings rail badge). Outside Report the ring is a quiet entry to the
// popover and flags only project setup that is still missing (target, scope), and
// it does so with a shape, never with colour alone.

import { shouldShowReadinessSignals, readinessValuetext, blockersByFinding } from './project-state.js';

export const RING_BADGE_CAP = 99;
export const POPOVER_ROW_CAP = 50;
export const METER_SEGMENT_CAP = 10;

const int = (n) => (Number.isFinite(Number(n)) && Number(n) > 0 ? Math.floor(Number(n)) : 0);
const list = (a) => (Array.isArray(a) ? a : []);
const plural = (n, one, many) => n + ' ' + (n === 1 ? one : many);

// ringModel returns everything the ring button draws and says:
//   state  loading | idle | setup | progress | ready | blocked   (data-state)
//   mark   none | bang | check                                   (data-mark, a shape)
//   pct    0..100 arc fill, report time only
//   text   the visible "ready/total", report time only, '' when there are no findings
//   badge  the visible blocker count ("99+" when capped), report time only
//   label  the accessible name; it always contains the visible fraction and badge
//   title  the hover text
export function ringModel(s, { reportOpen = false } = {}) {
  const state = s || {};
  const loaded = !!state.loaded;
  const findings = state.findings || {};
  const blockers = list(state.blockers);
  const total = int(findings.total);
  const ready = Math.min(int(findings.ready), total);
  const n = blockers.length;
  const reportTime = shouldShowReadinessSignals(reportOpen, n, loaded).chip && loaded;
  const next = state.nextAction && state.nextAction.label ? state.nextAction.label : '';

  if (!loaded) {
    return { state: 'loading', mark: 'none', reportTime: false, pct: 0, text: '', badge: '', label: 'Readiness: loading', title: 'Loading engagement state' };
  }

  if (!reportTime) {
    const setup = blockers.some((b) => b && b.scope === 'project');
    const label = 'Engagement status. ' + (setup ? 'Setup incomplete. ' : '') + (next ? 'Next: ' + next + '. ' : '') + 'Show details.';
    return {
      state: setup ? 'setup' : 'idle', mark: setup ? 'bang' : 'none', reportTime: false, pct: 0, text: '', badge: '',
      label, title: next ? 'Next: ' + next : 'Engagement status',
    };
  }

  const text = total ? ready + '/' + total : '';
  const badge = n ? (n > RING_BADGE_CAP ? RING_BADGE_CAP + '+' : String(n)) : '';
  let ringState = 'progress', mark = 'none';
  if (n) { ringState = 'blocked'; mark = 'bang'; }
  else if (total && ready === total) { ringState = 'ready'; mark = 'check'; }
  else if (!total) ringState = 'idle';
  const head = total ? 'Report readiness ' + text : 'Report readiness: no findings yet';
  // The name must contain the badge as drawn (WCAG 2.5.3), so a capped count reads "99+ blockers".
  const tail = n ? (badge.endsWith('+') ? badge + ' blockers' : plural(n, 'blocker', 'blockers')) : 'No blockers';
  return {
    state: ringState, mark, reportTime: true, pct: total ? Math.round((ready / total) * 100) : 0, text, badge,
    label: head + '. ' + tail + '. Show details.',
    title: state.stale && state.stale.findings ? 'Could not refresh findings' : readinessValuetext(findings, blockers),
  };
}

// meterModel is the role=meter in the popover header: the range, the spoken value
// and the segment count (one per finding, capped so ten findings and a hundred
// read the same width).
export function meterModel(s, cap = METER_SEGMENT_CAP) {
  const state = s || {};
  const findings = state.findings || {};
  const total = int(findings.total);
  const ready = Math.min(int(findings.ready), total);
  const max = Math.max(total, 1);
  const n = Math.min(total, cap);
  return {
    max, now: Math.min(ready, max), valuetext: readinessValuetext(findings, state.blockers), hidden: total === 0,
    segments: { n, filled: total ? Math.round((ready / total) * n) : 0 },
  };
}

// compactIntercept shortens the intercept status that app.js writes into #icptStat
// ("· REQ intercept ON · RESP intercept ON · 2 held") for the narrow tiers, where the
// bar has no room for it: "REQ · RESP · 2 held". The full text stays in #icptStat for
// assistive technology and as the compact label's tooltip.
export function compactIntercept(text) {
  const parts = String(text == null ? '' : text).split('\u00b7').map((p) => p.trim()).filter(Boolean);
  return parts.map((p) => { const m = /^(REQ|RESP)\s+intercept\s+ON$/i.exec(p); return m ? m[1].toUpperCase() : p; }).join(' \u00b7 ');
}

// blockerChecklist is the list the readiness popover carries: project blockers
// first (each fixes through Scope settings), then one row per blocked finding with
// a link to every gap, capped so a large project cannot grow the popover without
// bound.
export function blockerChecklist(s, cap = POPOVER_ROW_CAP) {
  const state = s || {};
  const project = list(state.blockers).filter((b) => b && b.scope === 'project').map((b) => ({ code: b.code, label: b.label }));
  const all = blockersByFinding(state.findings && state.findings.items);
  const more = Math.max(0, all.length - cap);
  const stale = !!(state.stale && state.stale.findings);
  return {
    project,
    findings: all.slice(0, cap).map((r) => ({ id: r.id, gaps: r.gaps })),
    more,
    moreText: more ? more + ' more findings. Open Findings to see all.' : '',
    empty: !project.length && !all.length,
    emptyText: !state.loaded ? 'Loading readiness\u2026' : stale ? 'Could not refresh findings. Showing the last known state.' : 'Nothing blocks the report.',
  };
}
