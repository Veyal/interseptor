// keyboard.js — the DOM side of the keyboard model: the shared keys.js registry
// instance, its single document dispatcher, the chord continuation popup, the
// "Single-key shortcuts" switch and the generated shortcut sheet.
//
// The registry is ADDITIVE. The legacy document handlers in app.js, proxy.js and
// tools.js keep running untouched; shortcuts-table.js documents them. New
// features register through getHook('keyRegistry')() with
//   keys.register({id, keys, scope, when, run, label, group})
// where scope is 'global', a panel id ('proxy', 'repeater', ...) or 'drawer'
// while focus is inside the Flow Drawer. A bare-character first step is a
// single-key shortcut: it never fires in editable controls, while a dialog is
// open, or when the switch is off (WCAG 2.1.4). Settings consumes the switch
// through getHook('singleKeyShortcuts') or setSingleKeyShortcuts(), and listens
// for SINGLE_KEY_EVENT.
import { MODAL_IDS, registerHook } from './core.js';
import { createKeyRegistry, readSingleKeyPref, writeSingleKeyPref } from './keys.js';
import { LEGACY_SHORTCUTS, parseDisplayKeys, registryKeysToDisplay } from './shortcuts-table.js';

export const SINGLE_KEY_EVENT = 'interseptor:singlekeychange';

let singleKey = readSingleKeyPref();
export const singleKeyShortcutsOn = () => singleKey;

function syncSwitch() {
  const box = document.getElementById('scSingleKey');
  if (box) box.checked = singleKey;
}
export function setSingleKeyShortcuts(on) {
  singleKey = !!on;
  writeSingleKeyPref(singleKey);
  syncSwitch();
  document.dispatchEvent(new CustomEvent(SINGLE_KEY_EVENT, { detail: { on: singleKey } }));
  return singleKey;
}

const modalOpen = () => MODAL_IDS.some((id) => { const m = document.getElementById(id); return !!m && m.style.display === 'flex'; });
const paletteOpen = () => document.getElementById('cmdk')?.style.display === 'flex';

export function activeScopes() {
  const scopes = ['global'];
  const panel = document.querySelector('.panel.active');
  if (panel && panel.dataset.panel) scopes.push(panel.dataset.panel);
  const active = document.activeElement;
  if (active && active.closest && active.closest('#flowDrawer')) scopes.push('drawer');
  return scopes;
}

// ---- chord continuation popup ----
let hint = null;
function hintEl() {
  if (hint) return hint;
  hint = document.createElement('div');
  hint.id = 'chordHint';
  hint.className = 'chord-hint';
  hint.setAttribute('role', 'status');
  hint.setAttribute('aria-live', 'polite');
  hint.hidden = true;
  document.body.appendChild(hint);
  return hint;
}
function renderChordHint(pending) {
  const el = hintEl();
  el.textContent = '';
  if (!pending) { el.hidden = true; return; }
  const head = document.createElement('span');
  head.className = 'chord-typed';
  const typed = document.createElement('kbd');
  typed.textContent = pending.typed.join(' ');
  head.appendChild(typed);
  head.appendChild(document.createTextNode(' then'));
  el.appendChild(head);
  for (const c of pending.continuations) {
    const row = document.createElement('span');
    row.className = 'chord-next';
    const k = document.createElement('kbd');
    k.textContent = c.next;
    row.appendChild(k);
    row.appendChild(document.createTextNode(' ' + c.label));
    el.appendChild(row);
  }
  const esc = document.createElement('span');
  esc.className = 'chord-cancel';
  esc.textContent = 'Esc cancels';
  el.appendChild(esc);
  el.hidden = false;
}

export const keyRegistry = createKeyRegistry({
  singleKeyEnabled: singleKeyShortcutsOn,
  isModalOpen: modalOpen,
  onPendingChange: renderChordHint,
});
registerHook('keyRegistry', () => keyRegistry);
registerHook('singleKeyShortcuts', (on) => (on === undefined ? singleKey : setSingleKeyShortcuts(on)));

// One dispatcher. It runs before the legacy handlers (module order) and only
// consumes events that match a registered binding; a handler that already
// prevented the event wins, and the palette keeps its own keys.
document.addEventListener('keydown', (e) => {
  if (e.defaultPrevented || paletteOpen()) return;
  try { keyRegistry.handle(e, { scopes: activeScopes() }); } catch (err) { /* a binding must not break typing */ }
});

// ---- cheatsheet ----
export function shortcutSheet() {
  return keyRegistry.cheatsheet(LEGACY_SHORTCUTS.map((l) => ({ group: l.group, keys: l.keys, label: l.label, scope: l.scope })));
}

function keysNode(keys) {
  const wrap = document.createElement('div');
  wrap.className = 'sc-keys';
  parseDisplayKeys(keys).forEach((alt, ai) => {
    if (ai) wrap.appendChild(document.createTextNode(' or '));
    alt.forEach((step, si) => {
      if (si) wrap.appendChild(document.createTextNode(' then '));
      step.forEach((k, ki) => {
        if (ki) { const plus = document.createElement('span'); plus.className = 'sc-plus'; plus.textContent = '+'; wrap.appendChild(plus); }
        const kbd = document.createElement('kbd');
        kbd.textContent = k;
        wrap.appendChild(kbd);
      });
    });
  });
  return wrap;
}

export function renderShortcutSheet(grid) {
  if (!grid) return;
  grid.textContent = '';
  for (const g of shortcutSheet()) {
    const h = document.createElement('h3');
    h.className = 'sc-section';
    h.textContent = g.group;
    grid.appendChild(h);
    for (const it of g.items) {
      const card = document.createElement('div');
      card.className = 'sc-card';
      card.appendChild(keysNode(it.legacy ? it.keys : registryKeysToDisplay(it.keys)));
      const p = document.createElement('p');
      p.className = 'sc-desc';
      p.textContent = it.label + (it.scope && it.scope !== 'global' ? ' (' + it.scope + ')' : '');
      card.appendChild(p);
      grid.appendChild(card);
    }
  }
}

// Replace the static fallback cards with the generated sheet each time the
// dialog opens, and wire the Single-key shortcuts switch.
export function mountShortcutSheet() {
  const modal = document.getElementById('shortcutsModal');
  if (!modal) return;
  const render = () => renderShortcutSheet(modal.querySelector('.sc-grid'));
  new MutationObserver(() => { if (modal.style.display === 'flex') render(); }).observe(modal, { attributes: true, attributeFilter: ['style'] });
  const box = document.getElementById('scSingleKey');
  if (box) { box.checked = singleKey; box.addEventListener('change', () => setSingleKeyShortcuts(box.checked)); }
}
