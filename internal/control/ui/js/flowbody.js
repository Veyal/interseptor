// flowbody.js — pure rendering for the Flow Drawer body. Extracted (copied) from
// the History inspector and the legacy flow popup so Proxy's bottom inspector
// and the drawer can share one renderer. No imports and no DOM access: the
// caller injects syntax highlighting and download links, which keeps every
// function runnable under `node --test` and keeps untrusted flow data escaped
// in exactly one place.
//
// flow shape: {id, detail, raw:{req,res}, ws:{frames}, timeline, auth}
// where timeline is GET /api/flows/session-inspect and auth is
// GET /api/flows/{id}/auth-timeline.

export const MAX_RENDER_BYTES = 1024 * 1024;
export const WS_FRAME_CAP = 500;
export const FLAG_WS = 32;
export const DRAWER_WIDTH = { min: 320, max: 640, def: 420 };

export const FLOW_TABS = [
  { id: 'request', label: 'Request' },
  { id: 'response', label: 'Response' },
  { id: 'timeline', label: 'Timeline' },
  { id: 'auth', label: 'Auth' },
  { id: 'ws', label: 'WS' },
];

const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
export const escapeHTML = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ESC[c]);

export const isWebSocketFlow = (detail) => !!detail && ((detail.flags | 0) & FLAG_WS) !== 0;

// The WS tab exists only for WebSocket flows.
export function tabsForFlow(detail) {
  return FLOW_TABS.filter((t) => t.id !== 'ws' || isWebSocketFlow(detail));
}

// Roving tabindex: arrows wrap, Home/End jump, any other key keeps the tab.
export function nextTab(tabs, current, key) {
  const ids = tabs.map((t) => t.id);
  const i = ids.indexOf(current);
  if (i < 0 || !ids.length) return ids[0] || current;
  if (key === 'ArrowRight') return ids[(i + 1) % ids.length];
  if (key === 'ArrowLeft') return ids[(i - 1 + ids.length) % ids.length];
  if (key === 'Home') return ids[0];
  if (key === 'End') return ids[ids.length - 1];
  return current;
}

export function clampDrawerWidth(w, { min = DRAWER_WIDTH.min, max = DRAWER_WIDTH.max } = {}) {
  const n = Number(w);
  if (!Number.isFinite(n)) return Math.min(max, Math.max(min, DRAWER_WIDTH.def));
  return Math.min(max, Math.max(min, Math.round(n)));
}

// stepSibling returns the neighbouring id in the opener's list, or null at the
// ends (no wrap) so j/k never silently jumps across the list.
export function stepSibling(ids, current, delta) {
  if (!Array.isArray(ids)) return null;
  const i = ids.findIndex((x) => Number(x) === Number(current));
  if (i < 0) return null;
  const j = i + delta;
  return j >= 0 && j < ids.length ? ids[j] : null;
}

export function flowUrl(d) {
  if (!d || !d.host) return '';
  const def = (d.scheme === 'https' && d.port === 443) || (d.scheme === 'http' && d.port === 80);
  return (d.scheme || 'http') + '://' + d.host + (d.port && !def ? ':' + d.port : '') + (d.path || '/');
}

const WS_OPCODES = { 0: 'cont', 1: 'text', 2: 'bin', 8: 'close', 9: 'ping', 10: 'pong' };
export const wsOpcodeName = (o) => WS_OPCODES[o] || '0x' + Number(o).toString(16);

export function wsFramesHTML(frames, limit = WS_FRAME_CAP) {
  if (!Array.isArray(frames) || !frames.length) return '<p class="flow-note">No frames captured yet. Frames appear as the socket exchanges messages.</p>';
  const shown = frames.slice(0, limit);
  const rows = shown.map((f) => {
    const send = f.dir === 'send';
    return `<li class="flow-ws-frame"><span class="flow-ws-dir">${send ? '&#9650; send' : '&#9660; recv'}</span><span class="flow-ws-op">${escapeHTML(wsOpcodeName(f.opcode))}</span><span class="flow-ws-len">${escapeHTML(f.length)} B</span><span class="flow-ws-text">${escapeHTML(f.preview)}</span></li>`;
  }).join('');
  const more = frames.length > shown.length ? `<p class="flow-note">Showing ${shown.length} of ${frames.length} frames.</p>` : '';
  return `<ul class="flow-ws-list">${rows}</ul>${more}`;
}

export function timelineHTML(data) {
  const flows = data && Array.isArray(data.flows) ? data.flows : [];
  const transitions = data && Array.isArray(data.transitions) ? data.transitions : [];
  if (!flows.length && !transitions.length) return '<p class="flow-note">No timeline data for this flow.</p>';
  const chain = flows.map((f) => `<li class="flow-tl-step"><span class="flow-tl-id">#${escapeHTML(f.id)}</span> <span class="flow-tl-req">${escapeHTML(f.method)} ${escapeHTML(f.path)}</span> <span class="flow-tl-status">${escapeHTML(f.status || '-')}</span>${f.redirect ? ` <span class="flow-tl-redirect">&rarr; ${escapeHTML(f.redirect)}</span>` : ''}</li>`).join('');
  const trans = transitions.map((t) => `<li class="flow-tl-trans">${t.confidence === 'hypothesis' ? '<span class="flow-chip">hypothesis</span> ' : ''}<b>${escapeHTML(t.kind)}</b>${t.name ? ' ' + escapeHTML(t.name) : ''}: ${escapeHTML(t.message)}</li>`).join('');
  return `<ol class="flow-tl">${chain}</ol>${trans ? `<ul class="flow-tl-transitions">${trans}</ul>` : ''}`;
}

export function authHTML(data) {
  const steps = data && Array.isArray(data.steps) ? data.steps : [];
  if (!steps.length) return '<p class="flow-note">No captured flows in this login chain.</p>';
  const lost = data.lostAt ? `<p class="flow-note" role="status">Authenticated state appears lost at flow #${escapeHTML(data.lostAt.flowId)} (${escapeHTML(data.lostAt.confidence)}): ${escapeHTML(data.lostAt.reason)}</p>` : '';
  const mfa = data.mfaState ? `<p class="flow-note">MFA: ${escapeHTML(data.mfaState)} (${escapeHTML(data.mfaConfidence)})</p>` : '';
  const rows = steps.map((s) => {
    const events = (s.events || []).map((e) => `<li>${e.confidence === 'hypothesis' ? '<span class="flow-chip">hypothesis</span> ' : ''}<b>${escapeHTML(e.kind)}</b>${e.name ? ' ' + escapeHTML(e.name) : ''}: ${escapeHTML(e.detail)}</li>`).join('');
    return `<li class="flow-tl-step"><span class="flow-tl-id">#${escapeHTML(s.flowId)}</span> <span class="flow-tl-req">${escapeHTML(s.method)} ${escapeHTML(s.path)}</span> <span class="flow-tl-status">${escapeHTML(s.status || '-')}</span>${s.redirect ? ` <span class="flow-tl-redirect">&rarr; ${escapeHTML(s.redirect)}</span>` : ''}${events ? `<ul class="flow-tl-transitions">${events}</ul>` : ''}</li>`;
  }).join('');
  return `${lost}${mfa}<ol class="flow-tl">${rows}</ol>`;
}

const sideKey = (side) => (side === 'req' ? 'req' : 'res');
const lenOf = (detail, side) => (side === 'req' ? detail.reqLen : detail.resLen) || 0;
const sizeText = (n) => (n >= 1048576 ? (n / 1048576).toFixed(1) + ' MB' : n >= 1024 ? (n / 1024).toFixed(1) + ' KB' : n + ' B');

function downloadLink(deps, id, side) {
  const href = deps.downloadHref ? deps.downloadHref(id, side) : '';
  return href ? ` <a class="btn flow-download" href="${escapeHTML(href)}" download>Download body</a>` : '';
}

// rawState tells the drawer whether raw text is worth fetching: never for
// binary bodies or bodies above the render cap.
export function rawState(detail, side, deps = {}) {
  if (!detail) return 'none';
  const mime = deps.mimeOf ? deps.mimeOf(detail, side) : '';
  if (deps.isBinaryMime && mime && deps.isBinaryMime(mime)) return 'binary';
  if (lenOf(detail, side) > MAX_RENDER_BYTES) return 'huge';
  return 'text';
}

function rawHTML(flow, side, deps) {
  const detail = flow.detail;
  const state = rawState(detail, side, deps);
  const label = side === 'req' ? 'Request' : 'Response';
  if (state === 'binary') {
    const mime = deps.mimeOf(detail, side);
    const head = deps.headerText ? `<pre class="flow-raw">${deps.highlight ? deps.highlight(deps.headerText(detail, side), side) : escapeHTML(deps.headerText(detail, side))}</pre>` : '';
    return `${head}<p class="flow-note">${label} body is ${escapeHTML(mime)} (${sizeText(lenOf(detail, side))}): binary, not rendered.${downloadLink(deps, flow.id, side)}</p>`;
  }
  if (state === 'huge') {
    return `<p class="flow-note">${label} body is ${sizeText(lenOf(detail, side))}: not rendered to keep the browser responsive.${downloadLink(deps, flow.id, side)}</p>`;
  }
  const raw = flow.raw && flow.raw[sideKey(side)];
  if (typeof raw !== 'string') return '<p class="flow-note" role="status">Loading ' + label.toLowerCase() + '...</p>';
  const cut = raw.length > MAX_RENDER_BYTES;
  const text = cut ? raw.slice(0, MAX_RENDER_BYTES) : raw;
  const body = deps.highlight ? deps.highlight(text, side) : escapeHTML(text);
  const notice = cut ? `<p class="flow-note" role="status">Truncated at 1 MB.${downloadLink(deps, flow.id, side)}</p>` : '';
  return `<pre class="flow-raw">${body}</pre>${notice}`;
}

// renderFlowBody(flow, tab, deps) -> HTML string. deps: highlight(raw, side),
// downloadHref(id, side), mimeOf(detail, side), isBinaryMime(mime),
// headerText(detail, side). Unknown tabs and missing flows render nothing.
export function renderFlowBody(flow, tab, deps = {}) {
  if (!flow || !flow.detail) return '';
  switch (tab) {
    case 'request': return rawHTML(flow, 'req', deps);
    case 'response': return rawHTML(flow, 'res', deps);
    case 'timeline': return timelineHTML(flow.timeline);
    case 'auth': return authHTML(flow.auth);
    case 'ws': return wsFramesHTML(flow.ws && flow.ws.frames);
    default: return '';
  }
}

// linkedFindings lists the findings that already reference a flow, either as an
// evidence block or in the legacy flow list.
export function linkedFindings(findings, flowId) {
  const id = Number(flowId);
  if (!Array.isArray(findings) || !Number.isFinite(id)) return [];
  return findings
    .filter((f) => f && ((Array.isArray(f.blocks) && f.blocks.some((b) => b && b.type === 'flow' && Number(b.flowId) === id)) || (Array.isArray(f.flows) && f.flows.some((x) => Number(x && x.id != null ? x.id : x) === id))))
    .map((f) => ({ id: f.id, title: String(f.title || ''), severity: f.severity || '' }));
}
