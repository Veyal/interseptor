// identities-detail.js — the right-hand detail of the Identities panel: the
// Overview (profile, provenance, token, JWT claims, health and actions), Auth
// flow (source flow, events, usage, the inline login timeline) and Access
// results (runs that include the identity and their matrix) tabs. Every server
// string is set with textContent; secrets render only behind the per-identity
// Reveal toggle. Pure shaping lives in identities-model.js.
import { $, api, esc, toast, toastError, copyText, uiConfirm, uiPrompt } from './core.js';
import { renderState } from './statepanel.js';
import { wireListbox } from './listbox.js';
import * as M from './identities-model.js';
import { S, X, jsend, enc, el, btn, byName, selectedFlowId, selectedFlow, flowLink, setStatus } from './identities-core.js';

const TABS = [['overview', 'Overview'], ['flow', 'Auth flow'], ['access', 'Access results']];

/* ------------------------------------------------------------------ shell */

function section(title, cls = '') {
  const s = el('section', 'ident-sec' + (cls ? ' ' + cls : ''));
  const h = el('h4', 'ident-sec-title', title);
  s.append(h);
  return s;
}
function kv(dl, label, value, cls = '') {
  const dt = el('dt', '', label);
  const dd = el('dd', cls, value == null || value === '' ? '—' : String(value));
  dl.append(dt, dd);
  return dd;
}
function statusChip(status) {
  const c = el('span', 'ident-chip ident-status-chip');
  c.dataset.status = status;
  const dot = el('span', 'ident-dot');
  dot.dataset.status = status;
  dot.setAttribute('aria-hidden', 'true');
  c.append(dot, document.createTextNode(M.statusLabel(status)));
  return c;
}
function sourceChip(identity) {
  const b = M.sourceBadge(identity);
  const c = el('span', 'ident-chip ident-source', b.kind === 'agent' ? b.label : 'you');
  c.dataset.kind = b.kind;
  c.title = b.kind === 'agent' ? 'Created by the agent ' + b.label : 'Created by you';
  return c;
}
function httpStatus(code) {
  const s = el('span', 'ident-http', code ? String(code) : '—');
  s.dataset.class = code ? Math.floor(Number(code) / 100) + 'xx' : 'none';
  return s;
}

// renderDetail paints the selected identity. Focus inside the detail survives a
// repaint by element id (a live refresh never moves focus to another object).
export function renderDetail() {
  const host = $('#identDetail');
  if (!host) return;
  const active = document.activeElement;
  const keepId = active && host.contains(active) && active.id ? active.id : '';
  host.textContent = '';
  const identity = byName(S.sel);
  if (!identity) {
    if (S.ids.length) renderState(host, 'empty-first', { icon: 'identity', title: 'Select an identity', hint: 'Its provenance, token health, auth flow and access results appear here.' });
    return;
  }
  const summary = M.authSummary(identity.headers);
  const jwt = S.detailFor === identity.name && S.detail && S.detail.auth ? S.detail.auth.jwt : null;
  const exp = jwt ? M.jwtExpSec(jwt) : summary.jwtExp;
  const status = M.identityStatus(identity, { jwtExp: exp });

  const head = el('div', 'ident-head');
  const title = el('h3', 'ident-title', identity.name || '(unnamed)');
  const chips = el('div', 'ident-chips');
  if (identity.role) chips.append(el('span', 'ident-chip ident-role', identity.role));
  chips.append(statusChip(status), sourceChip(identity));
  head.append(title, chips);

  const tabs = el('div', 'seg ident-tabs');
  tabs.setAttribute('role', 'tablist');
  tabs.setAttribute('aria-label', 'Identity views');
  const panes = el('div', 'ident-panes');
  TABS.forEach(([key, label]) => {
    const t = el('button', key === S.tab ? 'on' : '', label);
    t.type = 'button';
    t.id = 'identTab-' + key;
    t.setAttribute('role', 'tab');
    t.setAttribute('aria-selected', key === S.tab ? 'true' : 'false');
    t.setAttribute('aria-controls', 'identPane-' + key);
    t.tabIndex = key === S.tab ? 0 : -1;
    t.addEventListener('click', () => setTab(key, true));
    tabs.append(t);
  });
  tabs.addEventListener('keydown', (e) => {
    const keys = ['ArrowLeft', 'ArrowRight', 'Home', 'End'];
    if (!keys.includes(e.key)) return;
    e.preventDefault();
    const i = TABS.findIndex(([k]) => k === S.tab);
    const n = e.key === 'Home' ? 0 : e.key === 'End' ? TABS.length - 1 : (i + (e.key === 'ArrowRight' ? 1 : -1) + TABS.length) % TABS.length;
    setTab(TABS[n][0], true);
  });
  const pane = el('div', 'ident-pane');
  pane.id = 'identPane-' + S.tab;
  pane.setAttribute('role', 'tabpanel');
  pane.setAttribute('aria-labelledby', 'identTab-' + S.tab);
  pane.tabIndex = -1;
  if (S.tab === 'overview') renderOverview(pane, identity, { summary, jwt, exp, status });
  else if (S.tab === 'flow') renderFlowTab(pane, identity);
  else renderAccessTab(pane, identity);
  panes.append(pane);
  host.append(head, tabs, panes);
  if (keepId) { const again = document.getElementById(keepId); if (again && host.contains(again)) again.focus({ preventScroll: true }); }
}

export function setTab(key, focus = false) {
  if (!TABS.some(([k]) => k === key)) return;
  S.tab = key;
  renderDetail();
  if (key === 'access' && !S.runs && !S.runsError) X.loadRuns?.();
  if (focus) document.getElementById('identTab-' + key)?.focus({ preventScroll: true });
}

function detailNote(pane) {
  if (S.detailFor === S.sel && S.detailError) {
    const n = el('div', 'ident-callout');
    n.dataset.kind = 'warn';
    n.setAttribute('role', 'alert');
    n.append(el('span', '', 'Token analysis and history could not be loaded: ' + (S.detailError.message || 'error')));
    n.append(btn('Retry', () => X.loadDetail?.(S.sel), 'btn xs'));
    pane.append(n);
  } else if (S.detailFor !== S.sel || !S.detail) {
    pane.append(el('p', 'hint ident-loading', 'Loading token analysis…'));
  }
}

/* ------------------------------------------------------------------ overview */

function renderOverview(pane, identity, { summary, jwt, exp, status }) {
  detailNote(pane);
  const d = S.detailFor === identity.name ? S.detail : null;

  // Profile
  const prof = section('Profile');
  const dl = el('dl', 'ident-kv');
  kv(dl, 'Name', identity.name);
  kv(dl, 'Role', identity.role);
  kv(dl, 'Notes', identity.notes, 'ident-notes');
  prof.append(dl);
  const pa = el('div', 'ident-actions');
  const edit = btn('Edit', () => X.openEdit?.({ mode: 'edit', name: identity.name, opener: edit }), 'btn', 'edit');
  edit.id = 'identEditBtn';
  pa.append(edit);
  prof.append(pa);

  // Provenance
  const prov = section('Provenance');
  const p = M.provenance(identity);
  const line = el('p', 'ident-prov', p.lead + (p.kindText ? ' ' + p.kindText : ''));
  prov.append(line);
  if (p.flowId) {
    const row = el('p', 'ident-prov ident-prov-flow');
    row.append(document.createTextNode('From flow '));
    row.append(flowLink(p.flowId, '#' + p.flowId));
    if (p.target) row.append(el('span', 'ident-mono', ' ' + p.target));
    prov.append(row);
  }
  if (identity.updatedAt) prov.append(el('p', 'hint ident-prov', 'Last written ' + (M.relTime(identity.updatedAt) || identity.updatedAt) + (identity.owner ? ' by ' + identity.owner : '')));

  // Token
  const tok = section('Token');
  const auth = d && d.auth;
  const type = auth && auth.type ? auth.type : summary.type;
  const tdl = el('dl', 'ident-kv');
  const typeDd = kv(tdl, 'Type', M.authTypeLabel(type));
  if (exp) {
    const cd = el('span', 'ident-countdown');
    cd.dataset.exp = String(exp);
    cd.textContent = ' · ' + (M.formatCountdown(exp) === 'expired' ? 'expired' : 'expires in ' + M.formatCountdown(exp));
    typeDd.append(cd);
  }
  tok.append(tdl);
  const headers = auth && Array.isArray(auth.headers) ? auth.headers : M.maskedHeaders(identity.headers);
  if (headers.length) {
    const ul = el('ul', 'ident-hdrs');
    ul.setAttribute('aria-label', 'Headers (values hidden)');
    headers.forEach((h) => {
      const li = el('li', 'ident-hdr');
      li.append(el('span', 'ident-hdr-name', h.name));
      li.append(el('span', 'ident-hdr-val', h.masked || M.maskValue('x'.repeat(Number(h.length) || 0))));
      ul.append(li);
    });
    tok.append(ul);
  } else tok.append(el('p', 'hint', 'No auth headers: requests are sent anonymously.'));
  if (auth && Array.isArray(auth.cookies) && auth.cookies.length) {
    const ul = el('ul', 'ident-hdrs');
    ul.setAttribute('aria-label', 'Cookies (fingerprints only)');
    auth.cookies.forEach((c) => {
      const li = el('li', 'ident-hdr');
      li.append(el('span', 'ident-hdr-name', 'cookie ' + c.name));
      li.append(el('span', 'ident-hdr-val', c.fingerprint ? 'fp ' + c.fingerprint : '—'));
      ul.append(li);
    });
    tok.append(ul);
  }
  const revealed = S.revealed.has(identity.name);
  const ta = el('div', 'ident-actions');
  const reveal = btn(revealed ? 'Hide values' : 'Reveal values', () => {
    if (S.revealed.has(identity.name)) S.revealed.delete(identity.name); else S.revealed.add(identity.name);
    renderDetail();
  }, 'btn', revealed ? 'lock' : 'lock-open');
  reveal.id = 'identReveal';
  reveal.setAttribute('aria-pressed', revealed ? 'true' : 'false');
  reveal.setAttribute('aria-controls', 'identRaw');
  ta.append(reveal);
  if (revealed && identity.headers) {
    const copy = btn('Copy', () => copyText(identity.headers, 'headers copied'), 'btn', 'copy');
    copy.id = 'identCopyRaw';
    copy.setAttribute('aria-label', 'Copy the raw header lines');
    ta.append(copy);
  }
  tok.append(ta);
  const raw = el('pre', 'ident-raw');
  raw.id = 'identRaw';
  raw.hidden = !revealed;
  if (revealed) raw.textContent = identity.headers || '(no headers)';
  tok.append(raw);

  // JWT
  const jwtSec = section('JWT claims');
  if (jwt) {
    if (jwt.expired) { const b = el('span', 'ident-chip ident-expired', 'Expired'); b.dataset.status = 'expired'; jwtSec.append(b); }
    const table = el('table', 'ident-claims');
    const cap = el('caption', 'u-sr', 'JWT claims');
    table.append(cap);
    const tb = el('tbody');
    M.jwtRows(jwt).forEach((r) => {
      const tr = el('tr');
      const th = el('th', '', r.label);
      th.scope = 'row';
      const td = el('td', 'ident-mono', r.value);
      if (r.label === 'Expires' && exp) td.dataset.claimExp = String(exp);
      tr.append(th, td);
      tb.append(tr);
    });
    table.append(tb);
    jwtSec.append(table);
  } else jwtSec.append(el('p', 'hint', d ? 'No JWT in this identity.' : (summary.jwtExp ? 'A JWT is present; claims load with the token analysis.' : 'No JWT detected.')));

  // Health
  const health = section('Health');
  const lc = identity.lastCheck;
  const hdl = el('dl', 'ident-kv');
  const vdd = kv(hdl, 'Status', '');
  vdd.textContent = '';
  vdd.append(statusChip(status));
  if (identity.broken && identity.brokenNote) kv(hdl, 'Broken because', identity.brokenNote);
  if (lc) {
    const sdd = kv(hdl, 'Last check', '');
    sdd.textContent = '';
    sdd.append(httpStatus(lc.status), document.createTextNode(' ' + (M.statusLabel(lc.verdict) || '') + (lc.ts ? ' · ' + M.relTime(lc.ts) : '')));
    if (Number(lc.flowId) > 0) { sdd.append(document.createTextNode(' ')); sdd.append(flowLink(lc.flowId)); }
    if (lc.note) kv(hdl, 'Note', lc.note);
  } else kv(hdl, 'Last check', 'Never checked');
  health.append(hdl);
  const ha = el('div', 'ident-actions');
  const busy = !!S.busy;
  // The running action keeps focus (aria-disabled, not disabled) so a repaint
  // does not drop it to <body>; act() ignores a second press.
  const mk = (id, label, run, icon, cls = 'btn') => { const b = btn(label, run, cls, icon); b.id = id; if (S.busy === id) { b.setAttribute('aria-busy', 'true'); b.setAttribute('aria-disabled', 'true'); } else b.disabled = busy; return b; };
  ha.append(
    mk('identCheck', 'Check now', () => checkNow(identity.name), 'refresh'),
    mk('identRefresh', 'Refresh from flow…', () => refreshFromFlow(identity.name), 'download'),
    mk('identBroken', identity.broken ? 'Unmark broken' : 'Mark broken', () => toggleBroken(identity.name), 'alert'),
    mk('identDup', 'Duplicate', () => duplicate(identity.name), 'copy'),
    mk('identDelete', 'Delete', () => remove(identity.name), 'trash', 'btn danger'),
  );
  health.append(ha);
  const hint = el('p', 'hint ident-hint');
  hint.textContent = sourceFlowId() ? 'Check now replays the source flow as this identity.' : 'Check now and Refresh from flow use the flow selected in History.';
  health.append(hint);

  pane.append(prof, prov, tok, jwtSec, health);
}

const sourceFlowId = () => {
  const d = S.detailFor === S.sel ? S.detail : null;
  const t = d && d.timeline && d.timeline.source;
  if (t && Number(t.flowId) > 0) return Number(t.flowId);
  const i = byName(S.sel);
  return i && i.source && Number(i.source.flowId) > 0 ? Number(i.source.flowId) : 0;
};

/* ------------------------------------------------------------------ actions */

async function act(id, name, run) {
  if (S.busy) return;
  S.busy = id;
  renderDetail();
  try { await run(); } finally {
    S.busy = '';
    if (S.sel === name) renderDetail();
  }
}

function checkNow(name) {
  const body = {};
  if (!sourceFlowId()) {
    const f = selectedFlowId();
    if (!f) { toast('Select a flow in History to check this identity against'); return; }
    body.flowId = f;
  }
  return act('identCheck', name, async () => {
    setStatus('Checking ' + name + '…');
    try {
      const r = await jsend('POST', '/api/authz/identities/' + enc(name) + '/check', body);
      const lc = r && r.lastCheck;
      setStatus(lc ? name + ': ' + M.statusLabel(lc.verdict) + (lc.status ? ' (' + lc.status + ')' : '') : 'Check complete');
      await X.loadIdentities?.();
    } catch (e) {
      setStatus('Check failed: ' + e.message, 'error');
      toastError(e.status === 400 ? 'No flow to check against' : 'Check failed', e);
    }
  });
}

async function refreshFromFlow(name) {
  const fid = selectedFlowId();
  if (!fid) { toast('Select the flow in History whose auth headers should replace these'); return; }
  const f = selectedFlow();
  const what = f ? ' (' + esc(f.method || '') + ' ' + esc((f.host || '') + (f.path || '')) + ')' : '';
  const ok = await uiConfirm('Refresh identity from flow',
    'Replace the auth headers of <b>' + esc(name) + '</b> with the Cookie and Authorization headers captured in flow #' + esc(String(fid)) + what + '?',
    'Refresh', 'btn accent');
  if (!ok) return;
  await act('identRefresh', name, async () => {
    try {
      await jsend('POST', '/api/authz/identities/' + enc(name) + '/refresh-from-flow/' + enc(String(fid)));
      setStatus(name + ' refreshed from flow #' + fid);
      await X.loadIdentities?.();
    } catch (e) { setStatus('Refresh failed: ' + e.message, 'error'); toastError('Refresh failed', e); }
  });
}

async function toggleBroken(name) {
  const identity = byName(name);
  if (!identity) return;
  let body;
  if (identity.broken) body = { broken: false, brokenNote: '' };
  else {
    const note = await uiPrompt({ title: 'Mark ' + name + ' as broken', placeholder: 'Reason (optional), e.g. locked after a rate-limit test', allowEmpty: true });
    if (note === null) return;
    body = { broken: true, brokenNote: note };
  }
  await act('identBroken', name, async () => {
    try {
      await jsend('PATCH', '/api/authz/identities/' + enc(name), body);
      setStatus(name + (body.broken ? ' marked broken; runs skip it' : ' unmarked'));
      await X.loadIdentities?.();
    } catch (e) { setStatus('Update failed: ' + e.message, 'error'); toastError('Update failed', e); }
  });
}

async function duplicate(name) {
  const identity = byName(name);
  if (!identity) return;
  const copy = M.duplicateName(name, S.ids.map((i) => i.name));
  await act('identDup', name, async () => {
    try {
      await jsend('POST', '/api/authz/identity', { name: copy, headers: identity.headers || '', role: identity.role || '', notes: identity.notes || '' });
      setStatus('Duplicated as ' + copy);
      S.sel = copy;
      await X.loadIdentities?.();
    } catch (e) { setStatus('Duplicate failed: ' + e.message, 'error'); toastError('Duplicate failed', e); }
  });
}

async function remove(name) {
  const ok = await uiConfirm('Delete identity', '<b>' + esc(name) + '</b> and its saved auth headers will be removed. Captured flows and past access results stay.', 'Delete', 'btn danger');
  if (!ok) return;
  await act('identDelete', name, async () => {
    try {
      await jsend('DELETE', '/api/authz/identity/' + enc(name));
      S.revealed.delete(name);
      if (S.sel === name) S.sel = '';
      setStatus(name + ' deleted');
      await X.loadIdentities?.();
    } catch (e) { setStatus('Delete failed: ' + e.message, 'error'); toastError('Delete failed', e); }
  });
}

/* ------------------------------------------------------------------ auth flow */

function renderFlowTab(pane, identity) {
  detailNote(pane);
  const d = S.detailFor === identity.name ? S.detail : null;
  const items = M.timelineItems(d);
  const tl = section('Timeline');
  if (!items.length) tl.append(el('p', 'hint', d ? 'No recorded events for this identity yet.' : ''));
  else {
    const ol = el('ol', 'ident-timeline');
    items.forEach((it) => {
      const li = el('li', 'ident-tl-item');
      li.dataset.kind = it.kind === 'source' ? 'source' : it.event;
      if (it.kind === 'source') {
        li.append(el('div', 'ident-tl-title', 'Login flow'));
        const row = el('div', 'ident-tl-body');
        row.append(flowLink(it.flowId), el('span', 'ident-mono', ' ' + it.method + ' ' + it.host + it.path + ' '), httpStatus(it.status));
        li.append(row);
      } else {
        li.append(el('div', 'ident-tl-title', it.label + (it.actor ? ' by ' + it.actor : '')));
        if (it.detail) li.append(el('div', 'ident-tl-body', it.detail));
      }
      if (it.ts) {
        const t = el('time', 'hint ident-tl-time', M.relTime(it.ts) || String(it.ts));
        t.setAttribute('datetime', String(it.ts));
        t.title = String(it.ts);
        li.append(t);
      }
      ol.append(li);
    });
    tl.append(ol);
  }
  const src = sourceFlowId();
  if (src) {
    const a = el('div', 'ident-actions');
    const open = btn(S.loginTimelineFor === src && S.loginTimeline ? 'Reload login timeline' : 'Open login timeline', () => loadLoginTimeline(src), 'btn', 'timeline');
    open.id = 'identLoginTimelineBtn';
    a.append(open);
    tl.append(a);
    const box = el('div', 'ident-login-timeline');
    box.id = 'identLoginTimeline';
    paintLoginTimeline(box, src);
    tl.append(box);
  }

  const use = section('Usage');
  const u = M.usageSummary(d);
  use.append(el('p', 'ident-usage', d ? u.text : ''));
  if (u.recent.length) {
    const ul = el('ul', 'ident-recent');
    ul.setAttribute('aria-label', 'Recent requests sent as this identity');
    u.recent.forEach((r) => {
      const li = el('li', 'ident-recent-row');
      li.append(flowLink(r.flowId), el('span', 'ident-mono ident-recent-target', ' ' + (r.method || '') + ' ' + (r.host || '') + (r.path || '/') + ' '), httpStatus(r.status));
      if (r.ts) li.append(el('span', 'hint', ' ' + M.relTime(r.ts)));
      ul.append(li);
    });
    use.append(ul);
  }
  pane.append(tl, use);
}

async function loadLoginTimeline(flowId) {
  const gen = ++S.timelineGen;
  S.loginTimelineFor = flowId; S.loginTimeline = null; S.loginTimelineError = null;
  const box = $('#identLoginTimeline');
  if (box) paintLoginTimeline(box, flowId, true);
  try {
    const data = await api('/api/flows/' + enc(String(flowId)) + '/auth-timeline');
    if (gen !== S.timelineGen) return;
    S.loginTimeline = data;
  } catch (e) {
    if (gen !== S.timelineGen) return;
    S.loginTimelineError = e;
  }
  if (S.tab === 'flow') renderDetail();
}

function paintLoginTimeline(box, flowId, loading = false) {
  box.textContent = '';
  if (S.loginTimelineFor !== flowId) return;
  if (loading) { renderState(box, 'loading', { title: 'Reading the login chain', rows: 3 }); return; }
  if (S.loginTimelineError) { renderState(box, 'error', { title: 'Could not read the login timeline', message: S.loginTimelineError.message, status: S.loginTimelineError.status, onRetry: () => loadLoginTimeline(flowId) }); return; }
  const data = S.loginTimeline;
  if (!data) return;
  const sum = el('p', 'ident-tl-summary');
  sum.textContent = (data.lostAt ? 'Authenticated state appears lost at flow #' + data.lostAt.flowId + ' (' + data.lostAt.confidence + '): ' + data.lostAt.reason : 'No loss of authenticated state detected in this chain.')
    + ' · MFA: ' + (data.mfaState || 'unknown') + (data.mfaConfidence ? ' (' + data.mfaConfidence + ')' : '') + (data.note ? '. ' + data.note : '');
  box.append(sum);
  const steps = data.steps || [];
  if (!steps.length) { box.append(el('p', 'hint', 'No captured flows in this chain.')); return; }
  const ol = el('ol', 'ident-timeline ident-login-steps');
  steps.forEach((s) => {
    const li = el('li', 'ident-tl-item');
    li.dataset.kind = 'step';
    const row = el('div', 'ident-tl-body');
    row.append(flowLink(s.flowId), el('span', 'ident-mono', ' ' + (s.method || '') + ' ' + (s.scheme ? s.scheme + '://' : '') + (s.host || '') + (s.path || '') + ' '), httpStatus(s.status));
    if (s.redirect) row.append(el('span', 'hint', ' → ' + s.redirect));
    if (data.lostAt && data.lostAt.flowId === s.flowId) row.append(el('span', 'ident-chip', 'state likely lost here (hypothesis)'));
    li.append(row);
    (s.events || []).forEach((ev) => {
      const e = el('div', 'ident-tl-event' + (ev.confidence === 'hypothesis' ? ' is-hypothesis' : ''));
      const fp = ev.fingerprint ? ' [' + (ev.previousFingerprint ? ev.previousFingerprint + ' → ' : '') + ev.fingerprint + ']' : '';
      e.textContent = (ev.confidence === 'hypothesis' ? 'hypothesis · ' : '') + ev.kind + (ev.name ? ' · ' + ev.name : '') + fp + (ev.attributes && ev.attributes.length ? ' · ' + ev.attributes.join(' · ') : '') + ' — ' + (ev.detail || '');
      li.append(e);
    });
    if (s.ts) { const t = el('time', 'hint ident-tl-time', M.relTime(s.ts)); t.setAttribute('datetime', String(s.ts)); li.append(t); }
    ol.append(li);
  });
  box.append(ol);
}

/* ------------------------------------------------------------------ access results */

function renderAccessTab(pane, identity) {
  const bar = el('div', 'ident-access-bar');
  const seg = el('div', 'seg');
  seg.setAttribute('role', 'group');
  seg.setAttribute('aria-label', 'Which runs to show');
  [[false, 'This identity'], [true, 'All runs']].forEach(([all, label]) => {
    const b = el('button', S.runsAll === all ? 'on' : '', label);
    b.type = 'button';
    b.id = all ? 'identRunsAll' : 'identRunsMine';
    b.setAttribute('aria-pressed', S.runsAll === all ? 'true' : 'false');
    b.addEventListener('click', () => { S.runsAll = all; renderDetail(); });
    seg.append(b);
  });
  const reload = btn('', () => X.loadRuns?.(), 'btn', 'refresh', 'Reload access results');
  reload.id = 'identRunsReload';
  bar.append(seg, el('div', 'spacer'), reload);
  pane.append(bar);

  const listBox = el('div', 'ident-runs-wrap');
  pane.append(listBox);
  if (S.runsError) { renderState(listBox, 'error', { title: 'Could not load access results', message: S.runsError.message, status: S.runsError.status, onRetry: () => X.loadRuns?.() }); return; }
  if (!S.runs) { renderState(listBox, 'loading', { title: 'Loading access results', rows: 3 }); return; }
  const runs = S.runsAll ? S.runs : M.runsForIdentity(S.runs, identity.name);
  if (!runs.length) {
    renderState(listBox, 'empty-first', {
      icon: 'lock-open', title: S.runsAll ? 'No access tests yet' : 'No access tests include ' + identity.name,
      hint: 'Run an access test from the toolbar (or let an agent run one); each run is kept here with its matrix.',
    });
    return;
  }
  const list = el('div', 'ident-runs');
  list.id = 'identRuns';
  runs.forEach((r) => {
    const row = el('div', 'ident-run-row');
    row.dataset.run = String(r.id);
    row.id = 'identRun-' + String(r.id).replace(/[^A-Za-z0-9_-]/g, '_');
    if (String(r.id) === S.runSel) row.setAttribute('aria-selected', 'true');
    const top = el('div', 'ident-run-top');
    top.append(el('span', 'ident-run-kind', M.runKindLabel(r.kind)), el('span', 'ident-mono ident-run-title', M.runTitle(r)));
    const meta = el('div', 'ident-run-meta hint');
    const n = (r.identities || []).length;
    meta.textContent = [M.relTime(r.ts), r.actor ? 'by ' + r.actor : '', n + ' identit' + (n === 1 ? 'y' : 'ies')].filter(Boolean).join(' · ');
    if (Number(r.suspicious) > 0) {
      const s = el('span', 'ident-chip ident-sus', r.suspicious + ' same as baseline');
      top.append(s);
    }
    row.append(top, meta);
    list.append(row);
  });
  listBox.append(list);
  wireListbox(list, list.querySelectorAll('.ident-run-row'), {
    label: 'Access test runs',
    selected: (r) => r.dataset.run === S.runSel,
    activate: (r) => selectRun(r.dataset.run),
  });
  const mx = el('div', 'ident-matrix-wrap');
  mx.id = 'identMatrix';
  pane.append(mx);
  paintMatrix(mx);
}

export async function selectRun(id) {
  if (!id) return;
  S.runSel = String(id);
  const gen = ++S.matrixGen;
  S.matrixFor = S.runSel; S.matrix = null; S.matrixError = null;
  if (S.tab === 'access') renderDetail();
  try {
    const d = await api('/api/authz/runs/' + enc(S.runSel));
    if (gen !== S.matrixGen) return;
    S.matrix = d;
  } catch (e) {
    if (gen !== S.matrixGen) return;
    S.matrixError = e;
  }
  if (S.tab === 'access') renderDetail();
}

function paintMatrix(box) {
  box.textContent = '';
  if (!S.runSel) { box.append(el('p', 'hint', 'Select a run to see its matrix.')); return; }
  if (S.matrixError) { renderState(box, 'error', { title: 'Could not load this run', message: S.matrixError.message, status: S.matrixError.status, onRetry: () => selectRun(S.runSel) }); return; }
  if (!S.matrix || S.matrixFor !== S.runSel) { renderState(box, 'loading', { title: 'Loading the run matrix', rows: 4 }); return; }
  const run = S.matrix.run || {};
  const view = M.matrixView(S.matrix);
  const head = el('div', 'ident-matrix-head');
  head.append(el('span', 'ident-run-kind', M.runKindLabel(run.kind)), el('span', 'hint', [M.relTime(run.ts), run.actor ? 'by ' + run.actor : '', view.rows.length + ' endpoint' + (view.rows.length === 1 ? '' : 's'), view.suspicious + ' same as baseline'].filter(Boolean).join(' · ')));
  head.append(el('div', 'spacer'));
  const del = btn('Delete run', () => deleteRun(S.runSel), 'btn danger xs', 'trash');
  del.id = 'identRunDelete';
  head.append(del);
  box.append(head);
  if (!view.rows.length) { box.append(el('p', 'hint', 'This run has no comparable responses.')); return; }
  const scroll = el('div', 'ident-matrix-scroll');
  const table = el('table', 'ident-matrix');
  table.append(el('caption', 'u-sr', 'Access matrix: rows are endpoints, columns are identities'));
  const thead = el('thead');
  const hr = el('tr');
  const th0 = el('th', '', 'Endpoint'); th0.scope = 'col';
  const thb = el('th', '', 'Baseline'); thb.scope = 'col';
  hr.append(th0, thb);
  view.columns.forEach((c) => { const th = el('th', c === S.sel ? 'is-current' : '', c); th.scope = 'col'; hr.append(th); });
  thead.append(hr);
  const tb = el('tbody');
  view.rows.forEach((r) => {
    const tr = el('tr', r.suspicious ? 'is-suspicious' : '');
    const th = el('th', 'ident-mono');
    th.scope = 'row';
    th.append(el('span', 'ident-method', r.method + ' '), document.createTextNode(r.host + r.path));
    if (r.flowId) { th.append(document.createTextNode(' ')); th.append(flowLink(r.flowId)); }
    const base = el('td', 'ident-cell');
    base.append(httpStatus(r.baseline.status), el('span', 'hint', ' ' + r.baseline.length + ' B'));
    tr.append(th, base);
    r.cells.forEach((c) => {
      const td = el('td', 'ident-cell');
      td.dataset.tone = c.tone;
      if (c.suspicious) td.classList.add('is-suspicious');
      td.append(el('span', 'ident-verdict', c.label));
      if (!c.missing) {
        const meta = el('span', 'ident-cell-meta');
        meta.append(httpStatus(c.status), document.createTextNode(' ' + c.length + ' B'));
        td.append(meta);
      }
      if (c.note) td.title = c.note;
      if (c.flowId) td.append(flowLink(c.flowId));
      tr.append(td);
    });
    tb.append(tr);
  });
  table.append(thead, tb);
  scroll.append(table);
  box.append(scroll);
}

async function deleteRun(id) {
  const ok = await uiConfirm('Delete access test run', 'This removes the saved run and its matrix. The replayed flows stay in History.', 'Delete', 'btn danger');
  if (!ok) return;
  try {
    await jsend('DELETE', '/api/authz/runs/' + enc(id));
    if (S.runSel === id) { S.runSel = ''; S.matrix = null; S.matrixFor = ''; }
    setStatus('Run deleted');
    await X.loadRuns?.();
  } catch (e) { setStatus('Delete failed: ' + e.message, 'error'); toastError('Delete failed', e); }
}
