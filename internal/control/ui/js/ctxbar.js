// ctxbar.js — the engagement half of the Flight Bar: the project and scope pills,
// the active identity, and the readiness ring whose popover holds the Next
// action, the target, the evidence counts and the report blockers. It renders
// projectState and never decides readiness itself. It imports only pure modules,
// so the view-model rules (ctxbar-model.js) are covered under node and the DOM
// wiring takes everything else through initCtxbar(deps).
//
// deps: {openSettings(section), openProject(), activateTab(tab), toggleScopeFilter(),
//        scopeFilterOn(), openEngagement(), refreshSelect?(select), hasOpenModal(),
//        loadIdentity(), saveIdentity(name), toast(msg, sev)}

import {
  projectState, scopeChipModel, evidenceSummary, identityHue, identityInitials,
  createBlockerAnnouncer, shouldShowReadinessSignals,
} from './project-state.js';
import { ringModel, meterModel, blockerChecklist, compactIntercept, POPOVER_ROW_CAP } from './ctxbar-model.js';
import { getShellApi, isReportOpen, onReportOpenChange } from './shell-hooks.js';

const SVG_NS = 'http://www.w3.org/2000/svg';
const POPOVER_WIDTH = 380;
const POPOVER_GUTTER = 8;

let deps = {};
let popOpen = false;
let announcer = null;
const $ = (id) => document.getElementById(id);

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}
function iconNode(name) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = document.createElementNS(SVG_NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  return svg;
}
const setText = (id, value) => { const n = $(id); if (n && n.textContent !== value) n.textContent = value; };

function renderScope(s) {
  const chip = $('ctxScope');
  if (!chip) return;
  const m = scopeChipModel(s, { filterOn: deps.scopeFilterOn ? deps.scopeFilterOn() : false });
  setText('ctxScopeText', s.loaded ? m.text : '…');
  chip.setAttribute('aria-checked', m.checked ? 'true' : 'false');
  chip.setAttribute('aria-label', s.loaded ? 'Scope ' + m.text + '. ' + m.label : 'Scope loading');
  chip.setAttribute('aria-disabled', m.disabled ? 'true' : 'false');
  chip.title = m.title;
  chip.dataset.stale = m.stale ? 'true' : 'false';
  chip.dataset.state = m.disabled ? 'none' : m.checked ? 'filtering' : 'defined';
}

function renderTarget(s) {
  const row = $('ctxTarget');
  if (!row) return;
  const has = s.brief.ok;
  setText('ctxTargetText', s.loaded ? (has ? s.brief.target.split('\n')[0] : 'Set target') : '…');
  row.dataset.empty = s.loaded && !has ? 'true' : 'false';
  row.dataset.stale = s.stale.brief ? 'true' : 'false';
  row.setAttribute('aria-label', s.loaded ? (has ? 'Target ' + s.brief.target + '. Open engagement details.' : 'Target Set target. No target is set; open engagement details.') : 'Target: loading');
  row.title = s.stale.brief ? 'Could not refresh the engagement brief' : (has ? s.brief.target : 'Set the authorised target');
}

function renderIdentity(s) {
  const wrap = $('ctxIdentityWrap'), sel = $('ctxIdentity');
  if (!wrap || !sel) return;
  wrap.hidden = s.identities.length === 0;
  const sig = s.identities.map((i) => i.name).join('\n');
  if (sel.dataset.sig !== sig) {
    sel.dataset.sig = sig;
    sel.textContent = '';
    const none = el('option', '', 'No identity');
    none.value = '';
    sel.appendChild(none);
    for (const i of s.identities) { const o = el('option', '', i.name); o.value = i.name; sel.appendChild(o); }
  }
  sel.value = s.activeIdentity;
  if (deps.refreshSelect) deps.refreshSelect(sel);
  const av = $('ctxAvatar');
  if (av) {
    av.textContent = s.activeIdentity ? identityInitials(s.activeIdentity) : '—';
    av.dataset.hue = String(s.activeIdentity ? identityHue(s.activeIdentity) : 6);
  }
}

function renderEvidence(s) {
  const row = $('ctxEvidence');
  if (!row) return;
  const t = evidenceSummary(s.evidence);
  setText('ctxEvidenceText', s.loaded ? t : '…');
  row.setAttribute('aria-label', s.loaded ? 'Evidence ' + t + '. Open Findings.' : 'Evidence loading');
  row.dataset.stale = s.stale.evidence ? 'true' : 'false';
  row.title = s.stale.evidence ? 'Flow and websocket counts could not be refreshed' : 'Evidence held for this project';
}

// The popover header meter: role=meter with its value text and one segment per
// finding (capped). The ring on the bar is a purely visual twin of it.
function renderMeter(s) {
  const meter = $('ctxMeter'), segs = $('ctxSegs');
  if (!meter || !segs) return;
  const m = meterModel(s);
  meter.setAttribute('aria-valuemax', String(m.max));
  meter.setAttribute('aria-valuenow', String(m.now));
  meter.setAttribute('aria-valuetext', m.valuetext);
  meter.hidden = m.hidden;
  const sig = m.segments.n + ':' + m.segments.filled;
  if (segs.dataset.sig !== sig) {
    segs.dataset.sig = sig;
    segs.textContent = '';
    for (let i = 0; i < m.segments.n; i++) segs.appendChild(el('span', i < m.segments.filled ? 'ctx-seg is-pass' : 'ctx-seg is-gap'));
  }
  setText('ctxReadyText', s.loaded ? m.valuetext : '…');
}

// The ring is always on the bar; what it says depends on whether Report work is
// on screen. Readiness (the fraction, the arc, the blocker count) is report-time
// information, so outside Report the ring only flags missing project setup.
function renderRing(s) {
  const ring = $('ctxRing');
  if (!ring) return;
  const m = ringModel(s, { reportOpen: isReportOpen() });
  ring.dataset.state = m.state;
  ring.dataset.mark = m.mark;
  ring.dataset.report = m.reportTime ? 'true' : 'false';
  ring.dataset.stale = m.reportTime && s.stale.findings ? 'true' : 'false';
  ring.setAttribute('aria-label', m.label);
  ring.title = m.title;
  const arc = $('ctxRingArc');
  if (arc) arc.setAttribute('stroke-dasharray', m.pct + ' 100');
  const text = $('ctxRingText'), badge = $('ctxRingBadge');
  if (text) { text.textContent = m.text; text.hidden = !m.text; }
  if (badge) { badge.textContent = m.badge; badge.hidden = !m.badge; }
  renderMeter(s);
  if (announcer && s.loaded && m.reportTime) announcer.update(s.blockers.length);
  if (popOpen) fillBlockerList();
}

function renderNext(s) {
  const row = $('ctxNext');
  if (!row) return;
  setText('ctxNextText', s.loaded ? s.nextAction.label : '…');
  row.dataset.kind = s.nextAction.kind;
  row.setAttribute('aria-label', s.loaded ? 'Next ' + s.nextAction.label + '. Do the next action.' : 'Next loading');
}

function renderRailBadge(s) {
  const tab = document.getElementById('tab-findings');
  if (!tab) return;
  let badge = document.getElementById('findBadge');
  if (!badge) {
    badge = el('span', 'badge u-hidden');
    badge.id = 'findBadge';
    badge.appendChild(el('span', '', '0'));
    badge.appendChild(el('span', 'visually-hidden', ' report blockers'));
    tab.appendChild(badge);
  }
  const n = s.blockers.length;
  badge.firstChild.textContent = String(n);
  badge.classList.toggle('u-hidden', !shouldShowReadinessSignals(isReportOpen(), n, s.loaded).badge);
}

export function renderCtxbar(s = projectState.get()) {
  if (!$('ctxRing')) return;
  const group = $('fbEngage');
  if (group) group.setAttribute('aria-busy', s.loaded ? 'false' : 'true');
  renderScope(s); renderTarget(s); renderIdentity(s); renderEvidence(s);
  renderNext(s); renderRing(s); renderRailBadge(s);
}

/* ---- readiness popover: the blocker checklist ---- */
function projectRow(b) {
  const li = el('li', 'ctx-pop-row');
  li.appendChild(iconNode('alert'));
  li.appendChild(el('span', 'ctx-pop-label', b.label));
  const fix = el('button', 'btn xs', 'Fix');
  fix.type = 'button';
  fix.setAttribute('aria-label', 'Fix: ' + b.label);
  fix.addEventListener('click', () => { closeReadinessPopover({ restoreFocus: true }); if (deps.openSettings) deps.openSettings('scope'); });
  li.appendChild(fix);
  return li;
}
function findingRow(row) {
  const li = el('li', 'ctx-pop-row ctx-pop-finding');
  li.appendChild(iconNode('finding'));
  const body = el('span', 'ctx-pop-label');
  body.appendChild(el('strong', '', 'F-' + row.id));
  for (const g of row.gaps) {
    const a = el('a', 'ctx-gap', g.label);
    a.href = g.href;
    a.addEventListener('click', () => closeReadinessPopover({ restoreFocus: true }));
    body.append(' ', a);
  }
  li.appendChild(body);
  const fix = el('a', 'btn xs', 'Fix');
  fix.href = row.gaps[0].href;
  fix.setAttribute('aria-label', 'Fix F-' + row.id + ': ' + row.gaps[0].label);
  fix.addEventListener('click', () => closeReadinessPopover({ restoreFocus: true }));
  li.appendChild(fix);
  return li;
}
// The list is rebuilt only when its content changed: the store refreshes every
// couple of seconds under live traffic, and rebuilding an unchanged list would
// drop keyboard focus from the Fix link someone is on.
function fillBlockerList() {
  const list = $('ctxBlockerList');
  if (!list) return;
  const model = blockerChecklist(projectState.get(), POPOVER_ROW_CAP);
  const sig = JSON.stringify(model);
  if (list.dataset.sig === sig) return;
  list.dataset.sig = sig;
  // A changed list replaces the row someone may be on; hand focus to the first
  // control left (or Close) instead of dropping it to the page.
  const hadFocus = list.contains(document.activeElement);
  list.textContent = '';
  if (model.empty) list.appendChild(el('li', 'ctx-pop-empty', model.emptyText));
  else {
    model.project.forEach((b) => list.appendChild(projectRow(b)));
    model.findings.forEach((r) => list.appendChild(findingRow(r)));
    if (model.more) list.appendChild(el('li', 'ctx-pop-empty', model.moreText));
  }
  if (hadFocus) {
    const next = list.querySelector('a[href],button') || $('ctxRingClose');
    if (next) next.focus({ preventScroll: true });
  }
}
// The popover hangs from the ring: its right edge lines up with the ring's, kept
// inside the viewport. On a phone the stylesheet pins it to both side gutters.
function placePopover() {
  const pop = $('ctxRingPop'), ring = $('ctxRing');
  if (!pop || !ring) return;
  const width = Math.min(POPOVER_WIDTH, window.innerWidth - POPOVER_GUTTER * 2);
  const right = Math.min(Math.max(POPOVER_GUTTER, window.innerWidth - ring.getBoundingClientRect().right), window.innerWidth - width - POPOVER_GUTTER);
  pop.style.setProperty('--pop-right', Math.round(Math.max(POPOVER_GUTTER, right)) + 'px');
}
export function openReadinessPopover() {
  const pop = $('ctxRingPop'), ring = $('ctxRing');
  if (!pop || !ring || popOpen) return;
  popOpen = true;
  fillBlockerList();
  placePopover();
  pop.hidden = false;
  ring.setAttribute('aria-expanded', 'true');
  const first = pop.querySelector('a[href],button');
  if (first) first.focus({ preventScroll: true });
}
export function closeReadinessPopover({ restoreFocus = false } = {}) {
  const pop = $('ctxRingPop'), ring = $('ctxRing');
  if (!pop || !popOpen) return;
  popOpen = false;
  pop.hidden = true;
  if (ring) ring.setAttribute('aria-expanded', 'false');
  if (restoreFocus && ring) ring.focus({ preventScroll: true });
}

/* ---- actions ---- */
function runNext() {
  const a = projectState.get().nextAction;
  if (a.kind === 'set-target' || a.kind === 'enable-scope') { openEngagement(); return; }
  if (a.kind === 'capture') { if (deps.activateTab) deps.activateTab('proxy'); return; }
  if ((a.kind === 'attach-proof' || a.kind === 'fix-blockers') && a.href) { window.location.hash = a.href; return; }
  if (a.kind === 'export') {
    if (deps.activateTab) deps.activateTab('findings');
    const open = getShellApi().openReport;
    if (open) open();
    return;
  }
  if (a.kind === 'new-finding') {
    if (deps.activateTab) deps.activateTab('findings');
    const btn = document.getElementById('findNew');
    if (btn) btn.click();
  }
}
function openEngagement() {
  if (deps.openEngagement && deps.openEngagement()) return;
  if (deps.openSettings) deps.openSettings('scope');
}
function onScopeClick(e) {
  const chip = $('ctxScope');
  if (e.shiftKey || chip.getAttribute('aria-disabled') === 'true') { if (deps.openSettings) deps.openSettings('scope'); return; }
  if (deps.toggleScopeFilter) deps.toggleScopeFilter();
  renderScope(projectState.get());
}
function onIdentityChange() {
  const sel = $('ctxIdentity');
  if (!projectState.setActiveIdentity(sel.value)) { sel.value = projectState.get().activeIdentity; return; }
  if (deps.saveIdentity) deps.saveIdentity(sel.value);
  if (deps.toast) deps.toast(sel.value ? 'Sending as ' + sel.value + ' by default' : 'Default identity cleared');
}
// app.js writes the intercept status into #icptStat (and toggles its display). On
// narrow tiers the stylesheet keeps that element for assistive technology only and
// shows this short twin instead ("REQ · 2 held"), which this keeps in step.
function watchIntercept() {
  const stat = $('icptStat'), compact = $('icptCompact');
  if (!stat || !compact || typeof MutationObserver !== 'function') return;
  const sync = () => {
    const full = getComputedStyle(stat).display === 'none' ? '' : stat.textContent.trim();
    const text = compactIntercept(full);
    // app.js rewrites the status every second; only touch the DOM when something changed.
    if (compact.textContent !== text) compact.textContent = text;
    if (compact.title !== full) compact.title = full;
    if (compact.hidden === !!text) compact.hidden = !text;
  };
  new MutationObserver(sync).observe(stat, { childList: true, characterData: true, subtree: true, attributes: true, attributeFilter: ['style'] });
  sync();
}

// A popover row that leaves for another surface closes the popover first and parks
// focus on the ring, which stays on screen: a dialog the row opens takes focus from
// there and gives it back on close, and a tab or section change leaves focus on the
// ring instead of on a control that has just been hidden.
const leaving = (fn) => () => { closeReadinessPopover({ restoreFocus: true }); fn(); };

// initCtxbar wires the bar once. Safe to call when the markup is missing.
export function initCtxbar(d = {}) {
  deps = d;
  if (!$('ctxRing')) return;
  const live = $('ctxLive');
  announcer = createBlockerAnnouncer({ emit: (msg) => { if (live) live.textContent = msg; } });
  $('ctxScope').addEventListener('click', onScopeClick);
  $('ctxIdentity').addEventListener('change', onIdentityChange);
  $('ctxTarget').addEventListener('click', leaving(openEngagement));
  $('ctxEvidence').addEventListener('click', leaving(() => { if (deps.activateTab) deps.activateTab('findings'); }));
  $('ctxNext').addEventListener('click', leaving(runNext));
  $('ctxScopeSettings').addEventListener('click', leaving(() => { if (deps.openSettings) deps.openSettings('scope'); }));
  $('ctxBlockerOpen').addEventListener('click', leaving(() => { if (deps.activateTab) deps.activateTab('findings'); }));
  $('ctxRing').addEventListener('click', () => (popOpen ? closeReadinessPopover({ restoreFocus: true }) : openReadinessPopover()));
  $('ctxRingClose').addEventListener('click', () => closeReadinessPopover({ restoreFocus: true }));
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !popOpen) return;
    if (deps.hasOpenModal && deps.hasOpenModal()) return;
    e.preventDefault();
    // Focus returns to the ring only from the popover, the ring or the bare page; from
    // any other control the popover just closes and focus stays where it is.
    const at = document.activeElement;
    closeReadinessPopover({ restoreFocus: !at || at === document.body || $('ctxRingPop').contains(at) || at === $('ctxRing') });
  });
  document.addEventListener('click', (e) => {
    const pop = $('ctxRingPop');
    if (popOpen && pop && !pop.contains(e.target) && !$('ctxRing').contains(e.target)) closeReadinessPopover();
  });
  // Tabbing out of either end closes the popover. A null relatedTarget (a click on
  // plain text, a hidden control) leaves it open; the outside-click handler covers clicks.
  const closeOnFocusLeaving = (e) => {
    const to = e.relatedTarget;
    if (popOpen && to && !$('ctxRingPop').contains(to) && to !== $('ctxRing')) closeReadinessPopover();
  };
  $('ctxRingPop').addEventListener('focusout', closeOnFocusLeaving);
  $('ctxRing').addEventListener('focusout', closeOnFocusLeaving);
  window.addEventListener('resize', () => { if (popOpen) placePopover(); });
  watchIntercept();
  onReportOpenChange(() => renderCtxbar(projectState.get()));
  let restored = false;
  projectState.subscribe((s) => {
    // The saved default identity applies once, after a load that lists it (or at once
    // when nothing is saved). The flag is set before the call: setting the identity
    // notifies synchronously, re-entering this callback.
    if (!restored && s.loaded) {
      const saved = deps.loadIdentity ? deps.loadIdentity() : '';
      if (!saved || s.identities.some((i) => i.name === saved)) {
        restored = true;
        if (saved) projectState.setActiveIdentity(saved);
      }
    }
    // Render the store's current state, not this snapshot: the call above replaced it.
    renderCtxbar(projectState.get());
  });
  renderCtxbar(projectState.get());
}
