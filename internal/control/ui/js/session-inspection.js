import { $, api, esc, escAttr, toast, icon, openModal, closeModal } from './core.js';

const modal = $('#sessionInspectModal');
const state = { ids: [], roles: new Map(), data: null, loading: false, requestID: 0 };
const roleCycle = ['anonymous', 'user', 'admin', 'unassigned'];

function roleLabel(role) {
  return role === 'unassigned' ? 'Unassigned' : role[0].toUpperCase() + role.slice(1);
}

function setState(text, error = false) {
  const box = $('#sessionInspectState');
  if (!box) return;
  box.hidden = false;
  box.textContent = text;
  box.className = error ? 'session-inspect-state is-error' : 'session-inspect-state hint';
}

function selectedRoles() {
  return state.ids.map(id => state.roles.get(id) || 'unassigned');
}

async function load() {
  if (!state.ids.length) return;
  const requestID = ++state.requestID;
  state.loading = true;
  setState('Reading selected captures…');
  try {
    const query = new URLSearchParams({ ids: state.ids.join(','), roles: selectedRoles().join(',') });
    const data = await api('/api/flows/session-inspect?' + query.toString());
    if (requestID !== state.requestID) return;
    state.data = data;
    render();
  } catch (e) {
    if (requestID !== state.requestID) return;
    state.data = null;
    $('#sessionInspectDiff')?.setAttribute('hidden', '');
    $('#sessionInspectTimeline')?.setAttribute('hidden', '');
    const safety = $('#sessionInspectSafety');
    if (safety) safety.textContent = '';
    setState(e.message || 'Session inspection failed', true);
  } finally {
    if (requestID === state.requestID) state.loading = false;
  }
}

function cookieHTML(cookie) {
  const attrs = cookie.attributes?.length ? ' · ' + cookie.attributes.join(' · ') : '';
  return `<span class="session-cookie"><b>${esc(cookie.name)}</b><small>${esc(cookie.fingerprint)}${esc(attrs)}</small></span>`;
}

function roleButtons(id, role) {
  return `<div class="session-role-group" role="group" aria-label="Identity role for flow ${escAttr(id)}">${roleCycle.map(value => `<button type="button" class="session-role" data-session-role="${escAttr(value)}" data-session-id="${escAttr(id)}" aria-pressed="${value === role ? 'true' : 'false'}">${esc(roleLabel(value))}</button>`).join('')}</div>`;
}

function renderDiff() {
  const section = $('#sessionInspectDiff');
  const list = $('#sessionInspectDiffList');
  const diffs = state.data?.differentials || [];
  if (!section || !list) return;
  section.hidden = !diffs.length;
  list.innerHTML = diffs.length ? `<div class="session-diff-list">${diffs.map(diff => {
    const status = diff.statusDifferent ? '<span class="badge">status differs</span>' : '<span class="hint">same status</span>';
    const response = diff.responseDifferent ? '<span class="badge">response differs</span>' : '<span class="hint">same fingerprint</span>';
    const values = diff.observations?.length ? diff.observations.map(observation => `<span class="session-cookie"><b>${esc(roleLabel(observation.role))}</b> #${esc(String(observation.flowId))} · ${esc(String(observation.status || '—'))}</span>`).join('') : diff.roles.map(role => `<span class="session-cookie"><b>${esc(roleLabel(role))}</b> ${esc(String(diff.statuses?.[role] ?? '—'))}</span>`).join('');
    const counts = diff.roles.map(role => `${roleLabel(role)} ×${diff.roleCounts?.[role] || 0}`).join(' · ');
    return `<article class="session-diff-card"><div class="session-diff-title"><code>${esc(diff.endpoint)}</code><div class="spacer"></div><div class="session-diff-badges">${status}${response}</div></div><div class="session-cookie-list">${values}</div><p class="hint">Selected observations: ${esc(counts)} · latest observation per role drives the comparison.</p><p class="hint">Validation order: unknown · observable side effects: unknown</p></article>`;
  }).join('')}</div>` : '';
}

function renderTimeline() {
  const section = $('#sessionInspectTimeline');
  const list = $('#sessionInspectTimelineList');
  const flows = state.data?.flows || [];
  if (!section || !list) return;
  section.hidden = !flows.length;
  list.innerHTML = flows.length ? `<div class="session-timeline-list">${flows.map(flow => {
    const statusClass = flow.status >= 400 ? 'is-error' : flow.status >= 200 && flow.status < 400 ? 'is-ok' : '';
    const requestCookies = flow.requestCookies?.length ? flow.requestCookies.map(cookieHTML).join('') : '<span class="hint">none observed</span>';
    const responseCookies = flow.responseCookies?.length ? flow.responseCookies.map(cookieHTML).join('') : '<span class="hint">none observed</span>';
    const transitions = (state.data?.transitions || []).filter(item => item.flowId === flow.id).map(item => `<div class="session-transition ${item.confidence === 'candidate' ? 'candidate' : ''}">${icon(item.kind === 'redirect' ? 'globe' : item.kind === 'rotation' ? 'recycle' : 'key')} ${esc(item.message)}</div>`).join('');
    const candidates = flow.candidates?.length ? `<div class="session-transition-list">${flow.candidates.map(item => `<div class="session-transition candidate">${icon('flag')} ${esc(item)}</div>`).join('')}</div>` : '';
    const transitionList = transitions ? `<div class="session-transition-list">${transitions}</div>` : '';
    return `<article class="session-event"><div class="session-event-head"><button type="button" class="btn btn-compact session-event-id" data-session-open-flow="${escAttr(flow.id)}">#${esc(flow.id)}</button><time>${esc(new Date(flow.timestamp).toLocaleString())}</time><span class="session-status ${statusClass}">${esc(String(flow.status || '—'))}</span><div class="spacer"></div>${roleButtons(flow.id, flow.role)}</div><div class="session-event-target">${esc(flow.method)} ${esc(flow.scheme)}://${esc(flow.host)}${esc(flow.path)}${flow.redirect ? ` <span class="hint">→ ${esc(flow.redirect)}</span>` : ''}</div><div class="session-observation-grid"><div class="session-observation"><span class="session-observation-label">Request cookies</span><span class="session-cookie-list">${requestCookies}</span></div><div class="session-observation"><span class="session-observation-label">Response cookies</span><span class="session-cookie-list">${responseCookies}</span></div><div class="session-observation"><span class="session-observation-label">Browser decision</span><span class="session-observation-value">${esc(flow.browserDecision)}</span></div><div class="session-observation"><span class="session-observation-label">Evidence fingerprints</span><span class="session-observation-value">headers ${esc(flow.responseHeadersFingerprint || '—')} · body ${esc(flow.responseBodyFingerprint || '—')}</span></div></div>${transitionList}${candidates}</article>`;
  }).join('')}</div>` : '';
}

function render() {
  const stateBox = $('#sessionInspectState');
  if (state.data) {
    if (stateBox) stateBox.hidden = true;
    const safety = $('#sessionInspectSafety');
    if (safety) safety.textContent = state.data.safetyNote || '';
    renderDiff();
    renderTimeline();
  }
}

function wireRendered() {
  $('#sessionInspectTimelineList')?.querySelectorAll('[data-session-role]').forEach(button => {
    button.onclick = () => {
      const id = Number(button.dataset.sessionId);
      state.roles.set(id, button.dataset.sessionRole);
      load();
    };
  });
  $('#sessionInspectTimelineList')?.querySelectorAll('[data-session-open-flow]').forEach(button => {
    button.onclick = () => {
      closeModal(modal);
      document.querySelector('.tab[data-tab="proxy"]')?.click();
      document.dispatchEvent(new CustomEvent('interceptor:session-open-flow', { detail: Number(button.dataset.sessionOpenFlow) }));
    };
  });
}

const originalRender = render;
render = function wrappedRender() { originalRender(); wireRendered(); };

export function openSessionInspector(ids) {
  const unique = [...new Set((ids || []).map(Number).filter(id => Number.isSafeInteger(id) && id > 0))].slice(0, 32);
  if (!unique.length) {
    toast('select at least one captured flow to inspect');
    return;
  }
  state.ids = unique;
  state.data = null;
  state.requestID++;
  state.roles = new Map(unique.map(id => [id, 'unassigned']));
  setState('Reading selected captures…');
  const refresh = $('#sessionInspectRefresh');
  openModal(modal, { initialFocus: refresh, onEscape: () => closeModal(modal), onDismiss: () => closeModal(modal) });
  load();
}

$('#sessionInspectClose')?.addEventListener('click', () => closeModal(modal));
$('#sessionInspectRefresh')?.addEventListener('click', load);
