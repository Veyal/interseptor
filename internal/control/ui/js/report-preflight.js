// report-preflight.js — the Report sub-view of Findings (spec 5.5).
//
// Left: the blocker checklist grouped by finding with Fix links. Right: export
// options, the export button and a preview of what the report will contain.
// Readiness is the server's (`GET /api/findings/readiness`); this file renders
// it and gates on it, it never re-derives pass or fail. It is the only export
// surface (`GET /api/findings/report`); the old export modal was removed.
//
// The view mounts into #findReportMount (owned by the Findings region) and adds
// one toggle button to the Findings toolbar. Both are created here, so the
// module is optional: when it fails to load, Findings behaves as before.

import { $, esc, escAttr, api, saveFile, toast, projectStorageKey } from './core.js';
import { handleAppHash, settleFindingsBeforeExport } from './findings.js';
import { setShellApi } from './shell-hooks.js';
import { projectState } from './project-state.js';
import {
  groupBlockers, summarize, exportGate, draftGate, overrideConfirmed, OVERRIDE_PHRASE,
  exportURL, readinessURL, exportFilename, sanitizeOptions, previewRows, STATUS_CHOICES, FORMAT_CHOICES,
} from './report-preflight-model.js';

const OPTIONS_KEY = 'interseptor.report.options';
const icon = (name) => `<svg class="icon" aria-hidden="true" focusable="false"><use href="#${name}"/></svg>`;

const S = { open: false, loading: false, busy: false, error: '', quality: null, options: sanitizeOptions(), confirming: false, typed: '', seq: 0, sig: '' };
let mount = null;
let toggle = null;

function loadOptions() {
  try { S.options = sanitizeOptions(JSON.parse(localStorage.getItem(projectStorageKey(OPTIONS_KEY)) || 'null')); } catch (e) { S.options = sanitizeOptions(); }
}
function saveOptions() {
  try { localStorage.setItem(projectStorageKey(OPTIONS_KEY), JSON.stringify(S.options)); } catch (e) { /* storage may be blocked; options just reset next visit */ }
}

const gateInput = () => ({ quality: S.quality, loading: S.loading, error: S.error, busy: S.busy });

function blockersHTML() {
  if (S.loading && !S.quality) return '<div class="skel skel-row" aria-hidden="true"></div><div class="skel skel-row" aria-hidden="true"></div>';
  const groups = groupBlockers(S.quality);
  if (!groups.length) {
    const s = summarize(S.quality);
    return `<p class="rp-none">${icon('i-check-circle')}<span>${s.total ? 'No blockers. Every finding in this report is ready.' : 'No findings match the selected statuses.'}</span></p>`;
  }
  return '<ol class="rp-groups">' + groups.map((g) => `<li class="rp-group"><div class="rp-group-h"><span class="rp-id">#${g.id}</span><span class="rp-title">${esc(g.title)}</span>${g.severity ? `<span class="rp-sev">${esc(g.severity)}</span>` : ''}</div><ul class="rp-issues">${g.issues.map((i) => `<li class="rp-issue">${icon('i-alert-tri')}<span class="rp-issue-text"><strong>${esc(i.label)}</strong>${i.message ? `<small>${esc(i.message)}</small>` : ''}${i.hint ? `<small class="rp-hint">${esc(i.hint)}</small>` : ''}</span><a class="btn xs" href="${escAttr(i.href)}" data-fix="${escAttr(i.href)}" aria-label="Fix ${escAttr(i.label)} in finding ${g.id}">Fix</a></li>`).join('')}</ul></li>`).join('') + '</ol>';
}

function optionsHTML() {
  const o = S.options;
  const opts = (list, cur) => list.map((c) => `<option value="${escAttr(c.value)}"${c.value === cur ? ' selected' : ''}>${esc(c.label)}</option>`).join('');
  return `<div class="rp-fields"><label for="reportFormat">Format</label><select id="reportFormat" class="btn btn-field">${opts(FORMAT_CHOICES, o.format)}</select>
<label for="reportStatuses">Include</label><select id="reportStatuses" class="btn btn-field">${opts(STATUS_CHOICES, o.statuses)}</select>
<label class="rp-check"><input type="checkbox" id="reportEvidence"${o.includeEvidence ? ' checked' : ''}> Include attached evidence</label>
<label class="rp-check"><input type="checkbox" id="reportGroupTag"${o.groupByTag ? ' checked' : ''}> Group by tag</label></div>`;
}

function previewHTML() {
  if (S.loading && !S.quality) return '<div class="skel skel-row" aria-hidden="true"></div><div class="skel skel-row" aria-hidden="true"></div><div class="skel skel-row" aria-hidden="true"></div>';
  const rows = previewRows(S.quality);
  if (!rows.length) return '<p class="hint">Nothing to preview.</p>';
  return '<ol class="rp-preview-list">' + rows.map((r) => `<li class="rp-prow${r.ready ? ' is-ready' : ' is-blocked'}">${icon(r.ready ? 'i-check-circle' : 'i-alert-tri')}<span class="rp-title">${esc(r.title)}</span>${r.severity ? `<span class="rp-sev">${esc(r.severity)}</span>` : ''}<span class="rp-state">${r.status}</span></li>`).join('') + '</ol><div class="rp-page" aria-hidden="true"><span class="skel rp-line"></span><span class="skel rp-line rp-line-s"></span><span class="skel rp-line"></span></div>';
}

function actionsHTML() {
  const gate = exportGate(gateInput());
  const draft = draftGate(gateInput());
  const confirm = draft.available && S.confirming;
  const ok = overrideConfirmed(S.typed);
  return `<div class="rp-actions"><button type="button" class="btn btn-primary" id="reportExport" aria-disabled="${gate.allowed ? 'false' : 'true'}" aria-describedby="reportExportReason"${S.busy ? ' aria-busy="true"' : ''}>Download report</button>
${draft.available && !S.confirming ? '<button type="button" class="btn" id="reportDraftOpen" aria-expanded="false" aria-controls="reportDraftConfirm">Export draft anyway</button>' : ''}</div>
<p id="reportExportReason" class="rp-reason${gate.allowed ? ' is-ok' : ''}">${icon(gate.allowed ? 'i-check-circle' : 'i-alert-tri')}<span>${esc(gate.reason)}</span></p>
${confirm ? `<div id="reportDraftConfirm" class="rp-confirm" role="group" aria-labelledby="reportDraftLabel"><label id="reportDraftLabel" for="reportDraftType">Unresolved blockers stay in a draft export. Type ${OVERRIDE_PHRASE} to confirm.</label><div class="rp-confirm-row"><input id="reportDraftType" class="btn btn-field" autocomplete="off" spellcheck="false" value="${escAttr(S.typed)}"><button type="button" class="btn btn-primary" id="reportDraftGo" aria-disabled="${ok ? 'false' : 'true'}">Export draft</button><button type="button" class="btn" id="reportDraftCancel">Cancel</button></div></div>` : ''}`;
}

function summaryText() {
  if (S.loading && !S.quality) return 'Checking readiness…';
  if (S.error && !S.quality) return 'Readiness unavailable';
  const s = summarize(S.quality);
  return s.total ? `${s.ready} of ${s.total} ready · ${s.blocked} blocked` : 'No findings';
}

function render() {
  if (!mount) return;
  const keep = document.activeElement && mount.contains(document.activeElement) ? document.activeElement.id : '';
  mount.innerHTML = `<section class="rp" aria-labelledby="reportTitle" aria-busy="${S.loading ? 'true' : 'false'}">
<header class="rp-head"><button type="button" class="btn" id="reportBack">Back to findings</button><h2 id="reportTitle" tabindex="-1">Report preflight</h2><span id="reportSummary" class="rp-sum" role="status" aria-live="polite">${esc(summaryText())}</span><button type="button" class="btn" id="reportRecheck">Check again</button></header>
${S.error ? `<p class="rp-error" role="alert">${icon('i-alert-tri')}<span>${esc(S.error)}</span></p>` : ''}
<div class="rp-body"><section class="rp-col rp-blockers" aria-labelledby="rpBlkTitle"><h3 id="rpBlkTitle">Blockers</h3>${blockersHTML()}</section>
<div class="rp-col rp-side"><section aria-labelledby="rpOptTitle"><h3 id="rpOptTitle">Export</h3>${optionsHTML()}<div id="reportActions">${actionsHTML()}</div></section>
<section aria-labelledby="rpPrevTitle"><h3 id="rpPrevTitle">Report contents</h3>${previewHTML()}</section></div></div></section>`;
  wire();
  if (keep) document.getElementById(keep)?.focus();
}

function renderActions() {
  const box = $('#reportActions');
  if (!box) return;
  box.innerHTML = actionsHTML();
  wireActions();
}

async function check() {
  const seq = ++S.seq;
  S.loading = true; S.error = '';
  render();
  try {
    const q = await api(readinessURL(S.options.statuses));
    if (seq !== S.seq) return;
    S.quality = q;
  } catch (e) {
    if (seq !== S.seq) return;
    S.error = e && e.message ? e.message : 'Readiness check failed';
  } finally {
    if (seq === S.seq) { S.loading = false; render(); }
  }
}

async function runExport(mode) {
  if (S.busy) return;
  S.busy = true; S.confirming = false; S.typed = '';
  renderActions();
  try {
    document.activeElement?.blur?.();
    await settleFindingsBeforeExport();
    const res = await fetch(exportURL(S.options, mode), { credentials: 'same-origin' });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      if (body && body.quality) S.quality = body.quality;
      throw new Error((body && body.error) || 'Export failed (' + res.status + ')');
    }
    const blob = await res.blob();
    await saveFile(blob, exportFilename(S.options.format), blob.type);
    toast(mode === 'draft' ? 'draft report exported' : 'report exported');
  } catch (e) {
    if (!e || e.name !== 'AbortError') toast(e && e.message ? e.message : 'Export failed', 'error');
  } finally {
    S.busy = false; render();
  }
}

function wireActions() {
  $('#reportExport')?.addEventListener('click', () => { if (exportGate(gateInput()).allowed) runExport('final'); });
  $('#reportDraftOpen')?.addEventListener('click', () => { S.confirming = true; S.typed = ''; renderActions(); $('#reportDraftType')?.focus(); });
  const type = $('#reportDraftType');
  const go = () => { if (overrideConfirmed(S.typed) && draftGate(gateInput()).available) runExport('draft'); };
  type?.addEventListener('input', () => { S.typed = type.value; $('#reportDraftGo')?.setAttribute('aria-disabled', overrideConfirmed(S.typed) ? 'false' : 'true'); });
  type?.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); go(); }
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); cancelConfirm(); }
  });
  $('#reportDraftGo')?.addEventListener('click', go);
  $('#reportDraftCancel')?.addEventListener('click', cancelConfirm);
}

function cancelConfirm() {
  S.confirming = false; S.typed = '';
  renderActions();
  $('#reportDraftOpen')?.focus();
}

function wire() {
  $('#reportBack')?.addEventListener('click', () => closeView(true));
  $('#reportRecheck')?.addEventListener('click', check);
  $('#reportFormat')?.addEventListener('change', (e) => { S.options = sanitizeOptions({ ...S.options, format: e.target.value }); saveOptions(); });
  $('#reportEvidence')?.addEventListener('change', (e) => { S.options = sanitizeOptions({ ...S.options, includeEvidence: e.target.checked }); saveOptions(); });
  $('#reportGroupTag')?.addEventListener('change', (e) => { S.options = sanitizeOptions({ ...S.options, groupByTag: e.target.checked }); saveOptions(); });
  $('#reportStatuses')?.addEventListener('change', (e) => { S.options = sanitizeOptions({ ...S.options, statuses: e.target.value }); saveOptions(); check(); });
  mount.querySelectorAll('[data-fix]').forEach((a) => a.addEventListener('click', (e) => {
    e.preventDefault();
    const href = a.getAttribute('data-fix');
    closeView(false);
    if (location.hash === href) handleAppHash(); else location.hash = href;
  }));
  wireActions();
}

function setOpen(on) {
  S.open = on;
  if (mount) mount.hidden = !on;
  const list = $('#scanFindingsView');
  if (list) list.hidden = on;
  if (toggle) { toggle.setAttribute('aria-pressed', on ? 'true' : 'false'); toggle.setAttribute('aria-expanded', on ? 'true' : 'false'); }
}

export function openReportView() {
  if (!mount) return false;
  loadOptions();
  setOpen(true);
  S.quality = null;
  check();
  $('#reportTitle')?.focus();
  return true;
}

export function closeView(focusToggle) {
  if (!S.open) return;
  S.seq++;
  S.loading = false; S.confirming = false; S.typed = '';
  setOpen(false);
  if (mount) mount.innerHTML = '';
  if (focusToggle) toggle?.focus();
}

function onProjectState(st) {
  const sig = [st.findings.total, st.findings.ready, st.blockers.length].join(':');
  if (sig === S.sig) return;
  const first = S.sig === '';
  S.sig = sig;
  if (!first && S.open && !S.busy && !S.loading) check();
}

function init() {
  mount = $('#findReportMount');
  const actions = document.querySelector('.findings-toolbar-actions');
  if (!mount || !actions || $('#findReportToggle')) return;
  toggle = document.createElement('button');
  toggle.type = 'button';
  toggle.className = 'btn';
  toggle.id = 'findReportToggle';
  toggle.setAttribute('aria-pressed', 'false');
  toggle.setAttribute('aria-expanded', 'false');
  toggle.setAttribute('aria-controls', 'findReportMount');
  toggle.textContent = 'Report';
  actions.appendChild(toggle);
  // The only export path: the palette, the strip's Next chip and the toolbar all
  // come through here, so the typed DRAFT confirmation cannot be bypassed.
  setShellApi({ openReport: openReportView });
  toggle.addEventListener('click', () => { if (S.open) closeView(true); else openReportView(); });
  mount.setAttribute('role', 'region');
  mount.setAttribute('aria-label', 'Report preflight');
  projectState.subscribe(onProjectState);
  window.addEventListener('hashchange', () => { if (S.open && /^#finding-/.test(location.hash)) closeView(false); });
}

init();
