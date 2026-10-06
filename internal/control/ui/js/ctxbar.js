// ctxbar.js — the Engagement Strip under the topbar: project, scope, target,
// active identity, evidence counts, report readiness, blockers and the next
// action. It renders projectState and never decides readiness itself. It
// imports only pure modules, so the view-model rules are covered under node and
// the DOM wiring takes everything else through initCtxbar(deps).
//
// deps: {openSettings(section), openProject(), activateTab(tab), toggleScopeFilter(),
//        scopeFilterOn(), openEngagement(), openSheet(opts), copyText?, hasOpenModal(),
//        loadIdentity(), saveIdentity(name), toast(msg, sev)}

import {
  projectState, scopeChipModel, evidenceSummary, readinessValuetext, identityHue, identityInitials,
  createBlockerAnnouncer, blockersByFinding,
} from './project-state.js';

const SEGMENT_CAP = 10;
const POPOVER_ROW_CAP = 50;
const SVG_NS = 'http://www.w3.org/2000/svg';

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
function setUse(svg, name) {
  const use = svg && svg.querySelector('use');
  if (use) use.setAttribute('href', '#i-' + name);
}
const setText = (id, value) => { const n = $(id); if (n && n.textContent !== value) n.textContent = value; };

function renderScope(s) {
  const chip = $('ctxScope');
  if (!chip) return;
  const m = scopeChipModel(s, { filterOn: deps.scopeFilterOn ? deps.scopeFilterOn() : false });
  setText('ctxScopeText', s.loaded ? m.text : '…');
  chip.setAttribute('aria-checked', m.checked ? 'true' : 'false');
  chip.setAttribute('aria-label', s.loaded ? m.label : 'Scope: loading');
  chip.setAttribute('aria-disabled', m.disabled ? 'true' : 'false');
  chip.title = m.title;
  chip.dataset.stale = m.stale ? 'true' : 'false';
  chip.dataset.state = m.disabled ? 'none' : m.checked ? 'filtering' : 'defined';
}

function renderTarget(s) {
  const chip = $('ctxTarget');
  if (!chip) return;
  const has = s.brief.ok;
  setText('ctxTargetText', s.loaded ? (has ? s.brief.target.split('\n')[0] : 'Set target') : '…');
  chip.dataset.empty = s.loaded && !has ? 'true' : 'false';
  chip.dataset.stale = s.stale.brief ? 'true' : 'false';
  chip.setAttribute('aria-label', s.loaded ? (has ? 'Target: ' + s.brief.target + '. Open engagement details.' : 'No target set. Set target.') : 'Target: loading');
  chip.title = s.stale.brief ? 'Could not refresh the engagement brief' : (has ? s.brief.target : 'Set the authorised target');
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
  const e = $('ctxEvidence');
  if (!e) return;
  const t = evidenceSummary(s.evidence);
  setText('ctxEvidenceText', s.loaded ? t : '…');
  e.setAttribute('aria-label', s.loaded ? 'Evidence: ' + t + '. Open Findings.' : 'Evidence: loading');
  e.dataset.stale = s.stale.evidence ? 'true' : 'false';
  e.title = s.stale.evidence ? 'Flow and websocket counts could not be refreshed' : 'Evidence held for this project';
}

function renderReady(s) {
  const meter = $('ctxMeter'), segs = $('ctxSegs');
  if (!meter || !segs) return;
  const total = s.findings.total, ready = s.findings.ready;
  const valuetext = readinessValuetext(s.findings, s.blockers);
  meter.setAttribute('aria-valuemax', String(Math.max(total, 1)));
  meter.setAttribute('aria-valuenow', String(Math.min(ready, Math.max(total, 1))));
  meter.setAttribute('aria-valuetext', valuetext);
  meter.hidden = total === 0;
  const n = Math.min(total, SEGMENT_CAP);
  const filled = total ? Math.round((ready / total) * n) : 0;
  const sig = n + ':' + filled;
  if (segs.dataset.sig !== sig) {
    segs.dataset.sig = sig;
    segs.textContent = '';
    for (let i = 0; i < n; i++) segs.appendChild(el('span', i < filled ? 'ctx-seg is-pass' : 'ctx-seg is-gap'));
  }
  setText('ctxReadyText', s.loaded ? (total ? ready + '/' + total : 'No findings') : '…');
  setUse($('ctxReadyIcon'), total && ready === total ? 'check-circle' : 'ring');
  const wrap = $('ctxReady');
  if (wrap) { wrap.dataset.stale = s.stale.findings ? 'true' : 'false'; wrap.title = s.stale.findings ? 'Could not refresh findings' : valuetext; }
}

function renderBlockers(s) {
  const chip = $('ctxBlockers');
  if (!chip) return;
  const n = s.blockers.length;
  const label = !s.loaded ? '…' : n ? n + (n === 1 ? ' blocker' : ' blockers') : 'No blockers';
  setText('ctxBlockersText', label);
  setUse($('ctxBlockersIcon'), n ? 'alert-tri' : 'check-circle');
  chip.dataset.count = String(n);
  chip.setAttribute('aria-label', s.loaded ? label + '. Show what blocks the report.' : 'Blockers: loading');
  if (announcer && s.loaded) announcer.update(n);
  if (popOpen) fillBlockerList();
}

function renderNext(s) {
  const chip = $('ctxNext');
  if (!chip) return;
  setText('ctxNextText', s.loaded ? s.nextAction.label : '…');
  chip.dataset.kind = s.nextAction.kind;
  chip.setAttribute('aria-label', s.loaded ? 'Next action: ' + s.nextAction.label : 'Next action: loading');
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
  badge.classList.toggle('u-hidden', !s.loaded || n === 0);
}

export function renderCtxbar(s = projectState.get()) {
  const bar = $('ctxbar');
  if (!bar) return;
  bar.setAttribute('aria-busy', s.loaded ? 'false' : 'true');
  renderScope(s); renderTarget(s); renderIdentity(s); renderEvidence(s);
  renderReady(s); renderBlockers(s); renderNext(s); renderRailBadge(s);
}

export function setCtxProject(name) {
  setText('ctxProjectText', name || 'Project');
  const b = $('ctxProject');
  if (b) b.setAttribute('aria-label', 'Project: ' + (name || 'unknown') + '. Switch or create a project.');
}

/* ---- blockers popover ---- */
function projectRow(b) {
  const li = el('li', 'ctx-pop-row');
  li.appendChild(iconNode('alert-tri'));
  li.appendChild(el('span', 'ctx-pop-label', b.label));
  const fix = el('button', 'btn xs', 'Fix');
  fix.type = 'button';
  fix.setAttribute('aria-label', 'Fix: ' + b.label);
  fix.addEventListener('click', () => { closeBlockerPopover(); if (deps.openSettings) deps.openSettings('scope'); });
  li.appendChild(fix);
  return li;
}
function findingRow(row) {
  const li = el('li', 'ctx-pop-row ctx-pop-finding');
  li.appendChild(iconNode('flag'));
  const body = el('span', 'ctx-pop-label');
  body.appendChild(el('strong', '', 'F-' + row.id));
  for (const g of row.gaps) {
    const a = el('a', 'ctx-gap', g.label);
    a.href = g.href;
    a.addEventListener('click', closeBlockerPopover);
    body.append(' ', a);
  }
  li.appendChild(body);
  const fix = el('a', 'btn xs', 'Fix');
  fix.href = row.gaps[0].href;
  fix.setAttribute('aria-label', 'Fix F-' + row.id + ': ' + row.gaps[0].label);
  fix.addEventListener('click', closeBlockerPopover);
  li.appendChild(fix);
  return li;
}
function fillBlockerList() {
  const list = $('ctxBlockerList');
  if (!list) return;
  const s = projectState.get();
  list.textContent = '';
  const project = s.blockers.filter((b) => b.scope === 'project');
  const findings = blockersByFinding(s.findings.items);
  if (!project.length && !findings.length) {
    list.appendChild(el('li', 'ctx-pop-empty', s.stale.findings ? 'Could not refresh findings. Showing the last known state.' : 'Nothing blocks the report.'));
    return;
  }
  project.forEach((b) => list.appendChild(projectRow(b)));
  findings.slice(0, POPOVER_ROW_CAP).forEach((r) => list.appendChild(findingRow(r)));
  if (findings.length > POPOVER_ROW_CAP) list.appendChild(el('li', 'ctx-pop-empty', findings.length - POPOVER_ROW_CAP + ' more findings. Open Findings to see all.'));
}
function placePopover() {
  const pop = $('ctxBlockerPop'), chip = $('ctxBlockers');
  if (!pop || !chip) return;
  const r = chip.getBoundingClientRect();
  const left = Math.max(8, Math.min(r.left, window.innerWidth - 360));
  pop.style.setProperty('--pop-left', Math.round(left) + 'px');
}
export function openBlockerPopover() {
  const pop = $('ctxBlockerPop'), chip = $('ctxBlockers');
  if (!pop || !chip || popOpen) return;
  popOpen = true;
  fillBlockerList();
  placePopover();
  pop.hidden = false;
  chip.setAttribute('aria-expanded', 'true');
  const first = pop.querySelector('a[href],button');
  if (first) first.focus({ preventScroll: true });
}
export function closeBlockerPopover({ restoreFocus = false } = {}) {
  const pop = $('ctxBlockerPop'), chip = $('ctxBlockers');
  if (!pop || !popOpen) return;
  popOpen = false;
  pop.hidden = true;
  if (chip) chip.setAttribute('aria-expanded', 'false');
  if (restoreFocus && chip) chip.focus({ preventScroll: true });
}

/* ---- actions ---- */
function runNext() {
  const a = projectState.get().nextAction;
  if (a.kind === 'set-target' || a.kind === 'enable-scope') { openEngagement(); return; }
  if (a.kind === 'capture') { if (deps.activateTab) deps.activateTab('proxy'); return; }
  if ((a.kind === 'attach-proof' || a.kind === 'fix-blockers') && a.href) { window.location.hash = a.href; return; }
  if (a.kind === 'new-finding' || a.kind === 'export') {
    if (deps.activateTab) deps.activateTab('findings');
    const btn = document.getElementById(a.kind === 'export' ? 'findExportOpen' : 'findNew');
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

/* ---- phone sheet: everything the one-row strip hides ---- */
function sheetRow(key, valueNode) {
  const row = el('div', 'ctx-sheet-row');
  row.appendChild(el('span', 'ctx-sheet-key', key));
  row.appendChild(valueNode);
  return row;
}
function sheetButton(text, onClick) {
  const b = el('button', 'btn', text);
  b.type = 'button';
  b.addEventListener('click', onClick);
  return b;
}
function openDetailsSheet() {
  if (!deps.openSheet) return;
  const s = projectState.get();
  const m = scopeChipModel(s, { filterOn: deps.scopeFilterOn ? deps.scopeFilterOn() : false });
  const content = (body) => {
    const wrap = el('div', 'ctx-sheet');
    wrap.appendChild(sheetRow('Scope', el('span', 'ctx-sheet-val', s.loaded ? m.text : 'Loading')));
    wrap.appendChild(sheetRow('Target', el('span', 'ctx-sheet-val', s.brief.ok ? s.brief.target : 'Not set')));
    if (s.identities.length) wrap.appendChild(sheetRow('As', el('span', 'ctx-sheet-val', s.activeIdentity || 'No identity')));
    wrap.appendChild(sheetRow('Evidence', el('span', 'ctx-sheet-val', evidenceSummary(s.evidence))));
    wrap.appendChild(sheetRow('Ready', el('span', 'ctx-sheet-val', readinessValuetext(s.findings, s.blockers))));
    wrap.appendChild(sheetRow('Next', el('span', 'ctx-sheet-val', s.nextAction.label)));
    const actions = el('div', 'ctx-sheet-actions');
    actions.appendChild(sheetButton('Do next action', () => { sheetApi.close(); runNext(); }));
    actions.appendChild(sheetButton('Scope settings', () => { sheetApi.close(); if (deps.openSettings) deps.openSettings('scope'); }));
    wrap.appendChild(actions);
    body.appendChild(wrap);
  };
  const sheetApi = deps.openSheet({ id: 'engagementSheet', title: 'Engagement', detents: ['half', 'full'], content });
}

// initCtxbar wires the strip once. Safe to call when the markup is missing.
export function initCtxbar(d = {}) {
  deps = d;
  const bar = $('ctxbar');
  if (!bar) return;
  const live = $('ctxLive');
  announcer = createBlockerAnnouncer({ emit: (msg) => { if (live) live.textContent = msg; } });
  const scope = $('ctxScope');
  scope.addEventListener('click', onScopeClick);
  $('ctxTarget').addEventListener('click', openEngagement);
  $('ctxProject').addEventListener('click', () => { if (deps.openProject) deps.openProject(); });
  $('ctxEvidence').addEventListener('click', () => { if (deps.activateTab) deps.activateTab('findings'); });
  $('ctxNext').addEventListener('click', runNext);
  $('ctxMore').addEventListener('click', openDetailsSheet);
  $('ctxBlockers').addEventListener('click', () => (popOpen ? closeBlockerPopover({ restoreFocus: true }) : openBlockerPopover()));
  $('ctxBlockerClose').addEventListener('click', () => closeBlockerPopover({ restoreFocus: true }));
  $('ctxBlockerOpen').addEventListener('click', () => { closeBlockerPopover(); if (deps.activateTab) deps.activateTab('findings'); });
  $('ctxIdentity').addEventListener('change', onIdentityChange);
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !popOpen) return;
    if (deps.hasOpenModal && deps.hasOpenModal()) return;
    e.preventDefault();
    closeBlockerPopover({ restoreFocus: true });
  });
  document.addEventListener('click', (e) => {
    const pop = $('ctxBlockerPop');
    if (popOpen && pop && !pop.contains(e.target) && !$('ctxBlockers').contains(e.target)) closeBlockerPopover();
  });
  $('ctxBlockerPop').addEventListener('focusout', (e) => {
    const pop = $('ctxBlockerPop');
    if (popOpen && e.relatedTarget && !pop.contains(e.relatedTarget) && e.relatedTarget !== $('ctxBlockers')) closeBlockerPopover();
  });
  window.addEventListener('resize', () => { if (popOpen) placePopover(); });
  let restored = false;
  projectState.subscribe((s) => {
    // The saved default identity applies once, after the first successful load.
    if (!restored && s.loaded) {
      restored = true;
      const saved = deps.loadIdentity ? deps.loadIdentity() : '';
      if (saved) projectState.setActiveIdentity(saved);
    }
    renderCtxbar(s);
  });
  renderCtxbar(projectState.get());
}
