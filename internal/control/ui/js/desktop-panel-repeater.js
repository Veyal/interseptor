// desktop-panel-repeater.js — desktop skin of the Repeater panel (docs/desktop-design.md,
// Panels). One send bar (method, URL, History, Send ⌘↩, the overflow menu with the
// request actions: Copy as, Copy cURL, Send to Intruder, Add to finding, Import
// Postman), one header row per pane (Response keeps its views), and one action
// row under the response header: the status line, then Attach as evidence, Save
// as proof, Diff and a response overflow menu (Copy as PNG). In a narrow pane the
// row's buttons drop to icons (panel-repeater.css). Every button is the original
// element, re-parented: ids, listeners, tooltips and enabled state are the web
// ones. Desktop only (desktop-panel-tools.js starts this after html.desktop-app
// is set).
import { sendHint, stripGlyph } from './desktop-panel-tools-model.js';
import { whenReady, observe, caseLabels, createOverflow, menuSeparator, iconNode, isMac, el, move } from './desktop-panel-kit.js';

const $ = (sel) => document.querySelector(sel);

// The glyphs web labels carry (a trailing arrow, a leading history glyph) read as dated in the
// desktop skin; labels are re-written whenever the app re-writes them.
function plainLabel(node, { prefix = '' } = {}) {
  if (!node) return;
  observe(node, () => {
    let t = node.textContent;
    if (prefix && t.startsWith(prefix)) t = t.slice(prefix.length);
    t = stripGlyph(t);
    if (t !== node.textContent) node.textContent = t;
  });
}

// withIcon gives a menu item its icon when the web button has none, so every
// item in a menu has one.
function withIcon(node, name) {
  if (node && !node.querySelector('.icon')) node.prepend(iconNode(name));
  return node;
}

function wireSendBar() {
  const panel = $('#panel-repeater');
  if (!panel) return;
  caseLabels(panel, '.pane-head .lbl, .rep-sub');
  plainLabel($('#repSendLabel'));
  // This module is the one owner of the Send hint.
  const kbd = $('#repSend .kbd');
  if (kbd) observe(kbd, () => { const want = sendHint(isMac()); if (kbd.textContent !== want) kbd.textContent = want; });
  const hist = $('#repHistToggle');
  if (hist) plainLabel(hist, { prefix: '⟲ ' });
}

// Request actions (they act on the request, not on the response) join Import
// Postman in the send bar's overflow menu.
function wireRequestMenu() {
  const line = $('#panel-repeater .rep-line');
  const group = $('#repActions');
  if (!line || !group || $('#repBarMore')) return;
  const byId = (id) => document.getElementById(id);
  // In a menu the short web labels need their object: "Intruder" is "Send to Intruder".
  const toIntruder = byId('repToIntruder');
  const addFinding = byId('repAddFinding');
  if (toIntruder) { toIntruder.textContent = 'Send to Intruder'; toIntruder.prepend(iconNode('intruder')); }
  if (addFinding) { addFinding.textContent = 'Add to finding'; addFinding.prepend(iconNode('finding')); }
  const more = createOverflow({
    id: 'repBarMore',
    tooltip: 'Copy as, Copy cURL, Send to Intruder, Add to finding, Import Postman',
    items: [
      withIcon(byId('repCopyAs'), 'copy'), withIcon(byId('repCopyCurl'), 'clipboard'),
      menuSeparator(), toIntruder, addFinding,
      menuSeparator(), withIcon(byId('repPostmanImport'), 'folder-open'),
    ],
  });
  const send = $('#repSend');
  const cancel = $('#repCancel');
  line.insertBefore(more.button, cancel && cancel.parentNode === line ? cancel.nextSibling : (send ? send.nextSibling : null));
}

function wireResponseActions() {
  const res = $('#panel-repeater .rep-res');
  const head = res && res.querySelector('.pane-head');
  const group = $('#repActions');
  if (!res || !head || !group || $('#repActionRow')) return;
  const row = el('div', 'dsk-actionrow');
  row.id = 'repActionRow';
  head.insertAdjacentElement('afterend', row);
  // The status line ("200 OK · 12 ms · 403 B") leads the row, so the header
  // keeps room for the title and the views at any pane width.
  const status = $('#repStatus');
  if (status) move(status, row);
  move(group, row);
  const byId = (id) => group.querySelector('#' + id);
  // "Diff vs previous" is "Diff" on the row (its tooltip keeps the full sentence).
  const diff = byId('repDiffBtn');
  if (diff) [...diff.childNodes].forEach((n) => { if (n.nodeType === 3) n.data = ' Diff'; });
  const keep = new Set(['repAttach', 'repProof', 'repDiffBtn', 'repAttachedChip']);
  const more = createOverflow({
    id: 'repMore',
    tooltip: 'Copy as PNG',
    items: [byId('repCopyPng')],
  });
  row.appendChild(more.button);
  // Anything the app adds to the group later lands in the menu rather than
  // crowding the row.
  [...group.children].forEach((c) => { if (!keep.has(c.id) && c.parentNode === group && c.tagName === 'BUTTON') more.add(c); });
}

export function initRepeaterDesktop() {
  whenReady(() => $('#panel-repeater .rep-line') && $('#repSend'), wireSendBar);
  whenReady(() => $('#repActions') && $('#repAttach') && $('#repCopyCurl') && $('#repToIntruder') && $('#repCopyAs') && $('#repPostmanImport'), () => {
    wireRequestMenu();
    wireResponseActions();
  });
}
