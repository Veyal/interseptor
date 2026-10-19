// desktop-panel-collections.js — desktop skin of the Collections panel
// (docs/desktop-design.md, Panels). The toolbar keeps the collection picker, the
// environment picker and Run; Import, Export, Variables, New environment,
// Environments and Console move into the overflow menu (the ellipsis "More actions" button). The editor's Send shows
// its ⌘↩ hint, and the resting "Matches the stored request" line is kept for
// screen readers only. Every control is the original element, re-parented.
import { sendHint, isCleanDirtyText } from './desktop-panel-tools-model.js';
import { whenReady, observe, createOverflow, menuSeparator, isMac, iconNode } from './desktop-panel-kit.js';

const $ = (sel) => document.querySelector(sel);

function wireToolbar() {
  const bar = $('#panel-collections .coll-bar');
  if (!bar || $('#collMoreTools')) return;
  const more = createOverflow({
    id: 'collMoreTools',
    tooltip: 'Import, Export, Variables, Environments, Console',
    items: [$('#collImport'), $('#collExport'), menuSeparator(), $('#collEnvEdit'), $('#collEnvNew'), $('#collEnvManage'), menuSeparator(), $('#collConsoleBtn')],
  });
  bar.appendChild(more.button);
}

// The editor is rebuilt on every selection, so the hint and the dirty line are
// re-applied after each change.
function wireEditor() {
  const editor = $('#collEditor');
  if (!editor) return;
  observe(editor, () => {
    const send = editor.querySelector('#collSend');
    if (send && !send.querySelector('.dsk-send-hint')) {
      const kbd = document.createElement('kbd');
      kbd.className = 'dsk-send-hint';
      kbd.textContent = sendHint(isMac());
      send.appendChild(kbd);
    }
    editor.querySelectorAll('.coll-actions .btn:not([title])').forEach((b) => { const t = b.textContent.trim(); if (t) b.title = t; });
    const dirty = editor.querySelector('#collDirty');
    if (dirty) {
      const clean = isCleanDirtyText(dirty.textContent);
      if (clean !== (dirty.dataset.clean === 'true')) dirty.dataset.clean = String(clean);
    }
  });
}

// The empty response says "Press Send (Ctrl+Enter)"; desktop writes the chord.
function wireResponse() {
  const res = $('#collResponse');
  if (!res) return;
  observe(res, () => {
    // Hand-offs can drop to icons in a narrow pane: each keeps its name as a tooltip.
    res.querySelectorAll('.coll-actions .btn:not([title])').forEach((b) => { const t = b.textContent.trim(); if (t) b.title = t; });
    res.querySelectorAll('.coll-meta').forEach((m) => {
      if (m.textContent.includes('(Ctrl+Enter)')) m.textContent = m.textContent.replace('(Ctrl+Enter)', '(' + sendHint(isMac()) + ')');
    });
    // Nothing sent yet: the spec's empty state (a muted icon and one line,
    // centred in the pane) instead of a status bar with a sentence in it.
    res.querySelectorAll('.coll-resbar').forEach((bar) => {
      const only = bar.children.length === 1 && bar.firstElementChild.classList.contains('coll-meta') && /^No response yet/.test(bar.textContent.trim());
      const icon = bar.querySelector(':scope > .dsk-empty-icon');
      if (only && !bar.classList.contains('dsk-res-empty')) bar.classList.add('dsk-res-empty');
      if (bar.classList.contains('dsk-res-empty') && !icon) { const i = iconNode('collection'); i.classList.add('dsk-empty-icon'); bar.prepend(i); }
    });
  });
}

export function initCollectionsDesktop() {
  whenReady(() => $('#collImport') && $('#collEnvEdit') && $('#collEnvNew') && $('#collEnvManage') && $('#collConsoleBtn') && $('#collExport'), wireToolbar);
  whenReady(() => $('#collEditor'), wireEditor);
  whenReady(() => $('#collResponse'), wireResponse);
}
