// desktop-shell.js — the desktop skin's chrome (docs/desktop-design.md, Shell).
// Runs only when js/desktop.js detected a desktop host (html.desktop-app); in a
// browser tab app.js never calls it, so the web DOM is untouched. It:
//   - moves the bar's project chip to the left of the title toolbar,
//   - adds the intercept pill next to the readiness ring,
//   - builds the 24px status bar (proxy, intercept, scope, flows, warnings,
//     connection, project, version), each item a button to its panel/popover,
//   - shows the palette chord as the platform writes it (⌘K on macOS),
//   - registers palette entries for the controls that left the bar.
// Everything app-specific arrives through initDesktopShell, so this module
// imports only the pure model and the desktop kit (the shared DOM helpers), and
// stays free of the feature modules.
import { statusBarModel, interceptPillModel, paletteShortcut } from './desktop-model.js';
import { el, iconNode, isDesktop, registerPopup } from './desktop-panel-kit.js';

const REFRESH_MS = 1000;
const env = { read: () => ({}), actions: {}, items: {}, pill: null, pop: null, last: '', pressOpen: null };

const act = (name, ...args) => () => { const fn = env.actions[name]; if (typeof fn === 'function') fn(...args); };

// item builds one status bar button: [dot|icon] text.
function item(key, { icon, dot, mono, onClick }) {
  const b = el('button', 'dsk-sb-item');
  b.type = 'button';
  b.dataset.sb = key;
  if (dot) { const d = el('span', 'dsk-sb-dot'); d.setAttribute('aria-hidden', 'true'); b.appendChild(d); }
  if (icon) b.appendChild(iconNode(icon));
  b.appendChild(el('span', mono ? 'dsk-sb-text dsk-sb-mono' : 'dsk-sb-text'));
  b.addEventListener('click', onClick);
  env.items[key] = b;
  return b;
}
const sep = () => { const s = el('span', 'dsk-sb-sep'); s.setAttribute('aria-hidden', 'true'); return s; };

function buildStatusBar() {
  if (document.getElementById('dskStatus')) return;
  const bar = el('footer');
  bar.id = 'dskStatus';
  bar.setAttribute('aria-label', 'Status bar');
  bar.append(
    item('proxy', { dot: true, mono: true, onClick: act('openSettings', 'proxy') }),
    item('intercept', { icon: 'intercept', onClick: act('openPanel', 'intercept') }),
    item('scope', { icon: 'scope', onClick: act('openSettings', 'scope') }),
    item('flows', { onClick: act('openPanel', 'proxy') }),
    el('span', 'dsk-sb-spacer'),
    item('warnings', { icon: 'alert', onClick: toggleWarnings }),
    item('connection', { dot: true, onClick: toggleConnection }),
    sep(),
    item('project', { icon: 'folder', onClick: act('openProject') }),
    item('version', { onClick: act('openReleases') }),
  );
  env.items.connection.addEventListener('pointerdown', () => { env.pressOpen = isConnectionOpen(); });
  env.items.connection.setAttribute('aria-haspopup', 'dialog');
  env.items.warnings.setAttribute('aria-haspopup', 'dialog');
  env.items.warnings.setAttribute('aria-expanded', 'false');
  env.items.warnings.dataset.tone = 'warn';
  document.body.appendChild(bar);
}

// The connection popover closes itself on focusout, which a press on this item
// causes before its click, so the open state is read at press time.
function toggleConnection() {
  const open = env.pressOpen !== null ? env.pressOpen : isConnectionOpen();
  env.pressOpen = null;
  act(open ? 'closeConnection' : 'openConnection')();
}
function isConnectionOpen() { const fn = env.actions.connectionOpen; return typeof fn === 'function' && fn() === true; }

// ---- warnings popover: TLS diagnosis and similar global warnings ----
function buildWarningsPopover() {
  const pop = el('div', 'dsk-pop');
  pop.id = 'dskWarnPop';
  pop.setAttribute('role', 'dialog');
  pop.setAttribute('aria-label', 'Warnings');
  pop.hidden = true;
  document.body.appendChild(pop);
  env.pop = pop;
  // Outside press, Escape and resize close it through the kit, like every desktop popup.
  env.popup = registerPopup({
    isOpen: () => !pop.hidden,
    close: ({ focus = false } = {}) => closeWarnings(focus),
    contains: (t) => pop.contains(t) || env.items.warnings.contains(t),
  });
}
function fillWarnings(list) {
  const pop = env.pop;
  pop.textContent = '';
  pop.appendChild(el('div', 'dsk-pop-title', 'Warnings'));
  for (const w of list) {
    const row = el('div', 'dsk-pop-row');
    row.appendChild(iconNode('alert'));
    const body = el('div');
    body.appendChild(el('div', 'dsk-pop-title', w.title));
    if (w.detail) body.appendChild(el('div', '', w.detail));
    row.appendChild(body);
    pop.appendChild(row);
  }
  const actions = el('div', 'dsk-pop-actions');
  const show = el('button', 'btn sm', 'Show details in Proxy');
  show.type = 'button';
  show.addEventListener('click', () => { closeWarnings(); act('showWarnings')(); });
  actions.appendChild(show);
  pop.appendChild(actions);
}
function toggleWarnings() {
  if (!env.pop) return;
  if (!env.pop.hidden) { closeWarnings(true); return; }
  fillWarnings(statusBarModel(env.read()).warnings.items);
  const r = env.items.warnings.getBoundingClientRect();
  env.pop.style.right = Math.max(8, Math.round(window.innerWidth - r.right)) + 'px';
  if (env.popup) env.popup.opened();
  env.pop.hidden = false;
  env.items.warnings.setAttribute('aria-expanded', 'true');
  env.pop.querySelector('button')?.focus({ preventScroll: true });
}
function closeWarnings(restoreFocus = false) {
  if (!env.pop || env.pop.hidden) return;
  env.pop.hidden = true;
  env.items.warnings.setAttribute('aria-expanded', 'false');
  if (restoreFocus) env.items.warnings.focus({ preventScroll: true });
}

// ---- title toolbar ----
function buildTitleToolbar(mac) {
  const start = document.querySelector('#bar .fb-start');
  const chip = document.getElementById('mobileProjectChip');
  if (start && chip) start.appendChild(chip);
  const ring = document.getElementById('ctxRing');
  if (ring && !document.getElementById('dskIcptPill')) {
    const pill = el('button', 'dsk-icpt-pill');
    pill.id = 'dskIcptPill';
    pill.type = 'button';
    pill.setAttribute('aria-pressed', 'false');
    pill.appendChild(iconNode('intercept'));
    pill.appendChild(el('span', 'dsk-icpt-text', 'Intercept'));
    pill.addEventListener('click', () => { const fn = env.actions.toggleIntercept; if (typeof fn === 'function') fn(); setTimeout(refresh, 150); });
    ring.parentNode.insertBefore(pill, ring);
    env.pill = pill;
  }
  const kbds = document.querySelectorAll('#cmdkBtn kbd');
  const chord = paletteShortcut(mac);
  if (kbds.length === 2) { kbds[0].textContent = chord[0]; kbds[1].textContent = chord[1]; }
  // Shortcut hints written for both platforms ("Ctrl/⌘") name this one's key.
  document.querySelectorAll('kbd').forEach((k) => { if (k.textContent === 'Ctrl/⌘') k.textContent = mac ? '⌘' : 'Ctrl'; });
  const btn = document.getElementById('cmdkBtn');
  if (btn) { const tip = chord.join('') + ' — jump to any panel, flow or finding'; btn.title = tip; if (btn.hasAttribute('data-tooltip')) btn.setAttribute('data-tooltip', tip); }
}

const setText = (node, text) => { if (node && node.textContent !== text) node.textContent = text; };
const setAttr = (node, name, value) => { if (node && node.getAttribute(name) !== value) node.setAttribute(name, value); };

// refresh re-reads the sources and touches the DOM only where something changed.
export function refresh() {
  if (!env.items.proxy) return;
  let snap, m;
  try { snap = env.read(); m = statusBarModel(snap); } catch { return; }
  const key = JSON.stringify(m) + (snap.intercept && snap.intercept.enabled ? 1 : 0);
  if (key === env.last) return;
  env.last = key;
  for (const name of ['proxy', 'intercept', 'scope', 'flows', 'warnings', 'connection', 'project', 'version']) {
    const b = env.items[name], it = m[name];
    setText(b.querySelector('.dsk-sb-text'), it.text);
    setAttr(b, 'aria-label', it.label);
    b.title = it.label.replace(/\. [^.]*\.$/, '');
    if (name !== 'warnings') { if (it.tone) b.dataset.tone = it.tone; else delete b.dataset.tone; }
    b.hidden = !!it.hidden;
  }
  const pdot = env.items.proxy.querySelector('.dsk-sb-dot');
  if (pdot) pdot.dataset.state = m.proxy.tone === 'ok' ? 'ok' : '';
  const cdot = env.items.connection.querySelector('.dsk-sb-dot');
  if (cdot) cdot.dataset.state = m.connection.dot;
  if (m.connection.dot !== 'ok') env.items.connection.dataset.tone = m.connection.dot === 'bad' ? 'bad' : 'warn';
  else delete env.items.connection.dataset.tone;
  if (env.pill) {
    const pill = interceptPillModel(snap.intercept || {});
    setAttr(env.pill, 'aria-pressed', pill.pressed ? 'true' : 'false');
    setText(env.pill.querySelector('.dsk-icpt-text'), pill.text);
    setAttr(env.pill, 'aria-label', pill.label);
    env.pill.title = pill.title;
  }
  if (env.pop && !env.pop.hidden && m.warnings.hidden) closeWarnings();
}

// initDesktopShell builds the desktop chrome once. read() returns the status
// snapshot statusBarModel takes; actions: {openPanel(name), openSettings(sec),
// openConnection(), closeConnection(), connectionOpen(), openProject(),
// openReleases(), toggleIntercept(), showWarnings()};
// commands: palette entries [{t, kw, run}] registered through registerCommand.
export function initDesktopShell({ read = () => ({}), actions = {}, commands = [], registerCommand = null, mac = false } = {}) {
  if (!isDesktop()) return false;
  env.read = read;
  env.actions = actions;
  buildTitleToolbar(mac);
  buildStatusBar();
  buildWarningsPopover();
  if (typeof registerCommand === 'function') commands.forEach((c) => registerCommand({ group: 'Actions', ...c }));
  refresh();
  setInterval(() => { if (!document.hidden) refresh(); }, REFRESH_MS);
  return true;
}
