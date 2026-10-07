// statepanel.js — one renderer for every empty / loading / error / offline /
// locked state, extending the existing .state-empty and .state-error markup.
// Every state is icon + text (never colour alone). Content is built with
// textContent, so API error strings cannot inject markup.
//
//   renderState(el, kind, opts)
//   kinds: empty-first | empty-filtered | loading | error | offline | locked
//   opts:  title, hint, icon, actionLabel, onAction, filterCount, onClear,
//          rows (loading skeletons, 3-8), status, message, onRetry, details

import { icon, copyText } from './core.js';

export const STATE_KINDS = ['empty-first', 'empty-filtered', 'loading', 'error', 'offline', 'locked'];
const DEFAULT_ICON = { 'empty-first': 'info', 'empty-filtered': 'search', loading: 'search', error: 'alert', offline: 'alert', locked: 'lock' };

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}
function iconWrap(cls, name) {
  const w = el('div', cls);
  w.innerHTML = icon(name); // icon() output is a fixed sprite reference
  return w;
}
function button(label, onClick, cls = 'btn primary') {
  const b = el('button', cls, label);
  b.type = 'button';
  b.addEventListener('click', onClick);
  return b;
}
export const clampSkeletonRows = (n) => Math.min(8, Math.max(3, Number.isFinite(n) ? Math.floor(n) : 5));

function errorDetails(opts) {
  const parts = [];
  if (opts.status) parts.push('HTTP ' + opts.status);
  if (opts.message) parts.push(opts.message);
  if (opts.details) parts.push(opts.details);
  return parts.join('\n');
}

export function renderState(host, kind, opts = {}) {
  if (!host) return null;
  host.textContent = '';
  host.removeAttribute('aria-busy');
  host.dataset.state = kind;
  const iconName = opts.icon || DEFAULT_ICON[kind] || 'search';

  if (kind === 'loading') {
    host.setAttribute('aria-busy', 'true');
    const wrap = el('div', 'state-loading');
    const status = el('p', 'u-sr', opts.title || 'Loading');
    status.setAttribute('role', 'status');
    wrap.appendChild(status);
    for (let i = 0; i < clampSkeletonRows(opts.rows); i++) {
      const row = el('div', 'skel skel-row');
      row.setAttribute('aria-hidden', 'true');
      wrap.appendChild(row);
    }
    host.appendChild(wrap);
    return { focus() {} };
  }

  const wrap = el('div', kind === 'error' || kind === 'offline' ? 'state-error state-panel' : 'state-empty state-panel');
  wrap.dataset.kind = kind;
  wrap.appendChild(iconWrap(kind === 'error' || kind === 'offline' ? 'state-error-icon' : 'state-empty-icon', iconName));
  const heading = el('h3', 'state-empty-title state-panel-title');
  const hint = el('p', 'state-empty-hint');

  if (kind === 'empty-filtered') {
    const n = opts.filterCount || 0;
    heading.textContent = opts.title || (n > 0 ? `No results match ${n} ${n === 1 ? 'filter' : 'filters'}` : 'No results match these filters');
    hint.textContent = opts.hint || '';
    wrap.append(heading);
    if (hint.textContent) wrap.append(hint);
    if (opts.onClear) wrap.append(button(opts.actionLabel || 'Clear filters', opts.onClear, 'btn'));
  } else if (kind === 'error') {
    heading.textContent = opts.title || 'Something went wrong';
    hint.textContent = errorDetails(opts) || opts.hint || '';
    wrap.append(heading);
    if (hint.textContent) { hint.setAttribute('role', 'alert'); wrap.append(hint); }
    const actions = el('div', 'state-actions');
    if (opts.onRetry) actions.append(button('Retry', opts.onRetry, 'btn'));
    const text = errorDetails(opts);
    if (text) actions.append(button('Copy details', () => copyText(text, 'details copied'), 'btn'));
    if (actions.childNodes.length) wrap.append(actions);
    // Focus moves to the heading on error only.
    heading.tabIndex = -1;
    host.appendChild(wrap);
    heading.focus({ preventScroll: true });
    return { focus: () => heading.focus({ preventScroll: true }) };
  } else if (kind === 'offline') {
    heading.textContent = opts.title || 'Offline';
    hint.textContent = opts.hint || 'Live updates are paused. Showing the last data received.';
    wrap.append(heading, hint);
    wrap.dataset.stale = 'true';
  } else if (kind === 'locked') {
    // Only the generic lock card: no hostnames, project names or counts.
    heading.textContent = 'Locked';
    hint.textContent = 'Enter your PIN to continue.';
    wrap.append(heading, hint);
  } else {
    // empty-first: why, and exactly one primary action.
    heading.textContent = opts.title || 'Nothing here yet';
    hint.textContent = opts.hint || '';
    wrap.append(heading);
    if (hint.textContent) wrap.append(hint);
    if (opts.onAction) wrap.append(button(opts.actionLabel || 'Get started', opts.onAction));
  }
  host.appendChild(wrap);
  return { focus: () => heading.focus({ preventScroll: true }) };
}
