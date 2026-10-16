import { $, api, esc, escAttr, toast, openModal, closeModal } from './core.js';

// Auth timeline: a read-only view of one login attempt's redirect / cookie /
// session / CSRF / MFA chain. Everything here comes from GET
// /api/flows/{id}/auth-timeline over already captured flows. Cookie values are
// never shown (short fingerprints only); open the flow to see raw values.

const modal = $('#authTimelineModal');
const state = { flowId: 0, data: null, requestID: 0 };

function setState(text, error = false) {
  const box = $('#authTimelineState');
  if (!box) return;
  box.hidden = false;
  box.textContent = text;
  box.className = error ? 'session-inspect-state is-error' : 'session-inspect-state hint';
}

function eventHTML(ev) {
  const hyp = ev.confidence === 'hypothesis';
  const attrs = ev.attributes?.length ? ' · ' + ev.attributes.join(' · ') : '';
  const fp = ev.fingerprint ? ` <small>${esc(ev.previousFingerprint ? ev.previousFingerprint + ' → ' : '')}${esc(ev.fingerprint)}</small>` : '';
  const tag = hyp ? '<span class="badge">hypothesis</span> ' : '';
  return `<div class="session-transition ${hyp ? 'candidate' : ''} auth-ev auth-ev-${escAttr(ev.kind)}">${tag}<b>${esc(ev.kind)}</b>${ev.name ? ' · ' + esc(ev.name) : ''}${fp}${esc(attrs)} — ${esc(ev.detail)}</div>`;
}

function stepHTML(step, lostFlowId) {
  const statusClass = step.status >= 400 ? 'is-error' : step.status >= 200 && step.status < 400 ? 'is-ok' : '';
  const lost = step.flowId === lostFlowId ? ' <span class="badge">authenticated state likely lost here (hypothesis)</span>' : '';
  const events = step.events?.length ? `<div class="session-transition-list">${step.events.map(eventHTML).join('')}</div>` : '';
  return `<article class="session-event"><div class="session-event-head"><button type="button" class="btn btn-compact session-event-id" data-auth-open-flow="${escAttr(step.flowId)}" aria-label="Open flow #${escAttr(step.flowId)}">#${esc(step.flowId)}</button><time>${esc(new Date(step.ts).toLocaleString())}</time><span class="session-status ${statusClass}">${esc(String(step.status || '—'))}</span>${lost}</div><div class="session-event-target">${esc(step.method)} ${esc(step.scheme)}://${esc(step.host)}${esc(step.path)}${step.redirect ? ` <span class="hint">→ ${esc(step.redirect)}</span>` : ''}</div>${events}</article>`;
}

function render() {
  const data = state.data;
  if (!data) return;
  const box = $('#authTimelineState');
  if (box) box.hidden = true;
  const summary = $('#authTimelineSummary');
  if (summary) {
    const lost = data.lostAt
      ? `Authenticated state appears lost at flow #${data.lostAt.flowId} (${data.lostAt.confidence}): ${data.lostAt.reason}`
      : 'No loss of authenticated state detected in this chain.';
    summary.textContent = `${lost} · MFA: ${data.mfaState} (${data.mfaConfidence}). ${data.note || ''}`;
  }
  const list = $('#authTimelineList');
  if (!list) return;
  list.innerHTML = data.steps?.length
    ? `<div class="session-timeline-list">${data.steps.map(step => stepHTML(step, data.lostAt?.flowId)).join('')}</div>`
    : '<div class="session-empty">No captured flows in this chain.</div>';
  list.querySelectorAll('[data-auth-open-flow]').forEach(button => {
    button.onclick = () => {
      closeModal(modal);
      document.querySelector('.tab[data-tab="proxy"]')?.click();
      document.dispatchEvent(new CustomEvent('interceptor:session-open-flow', { detail: Number(button.dataset.authOpenFlow) }));
    };
  });
}

async function load() {
  if (!state.flowId) return;
  const requestID = ++state.requestID;
  setState('Reading the login chain…');
  try {
    const data = await api('/api/flows/' + encodeURIComponent(state.flowId) + '/auth-timeline');
    if (requestID !== state.requestID) return;
    state.data = data;
    render();
  } catch (e) {
    if (requestID !== state.requestID) return;
    state.data = null;
    const list = $('#authTimelineList');
    if (list) list.innerHTML = '';
    setState(e.message || 'Auth timeline failed', true);
  }
}

export function openAuthTimeline(flowId) {
  const id = Number(flowId);
  if (!Number.isSafeInteger(id) || id <= 0) {
    toast('select a captured login flow first');
    return;
  }
  state.flowId = id;
  state.data = null;
  state.requestID++;
  const list = $('#authTimelineList');
  if (list) list.innerHTML = '';
  const summary = $('#authTimelineSummary');
  if (summary) summary.textContent = '';
  setState('Reading the login chain…');
  openModal(modal, { initialFocus: $('#authTimelineRefresh'), onEscape: () => closeModal(modal), onDismiss: () => closeModal(modal) });
  load();
}

$('#authTimelineClose')?.addEventListener('click', () => closeModal(modal));
$('#authTimelineRefresh')?.addEventListener('click', load);
