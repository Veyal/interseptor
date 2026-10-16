// flow-diff.js — the Proxy "Diff" verb: two selected flows' responses in the
// shared DiffView (hunk navigation with n / N, unified or side by side, ignore
// volatile values), shown in a modal. It reuses repeater.js's buildDiffPanel so
// the Proxy, Repeater and Intruder diffs behave identically. The old word-level
// compare modal stays reachable from the command palette.

import { api, toast, openModal, closeModal, state, copyPreviewPng, icon } from './core.js';
import { buildDiffPanel } from './repeater.js';

const MODAL_ID = 'flowDiffModal';
let modal = null;
let panel = null;
let opener = null;
let copyPng = null;
let shownIds = null;

function build() {
  modal = document.createElement('div');
  modal.id = MODAL_ID;
  modal.className = 'modal-overlay';
  modal.style.display = 'none';
  const shell = document.createElement('div');
  shell.className = 'modal-shell modal-xl flow-diff-shell';
  shell.setAttribute('role', 'dialog');
  shell.setAttribute('aria-modal', 'true');
  shell.setAttribute('aria-label', 'Diff selected flows');
  modal.appendChild(shell);
  document.body.appendChild(modal);
  panel = buildDiffPanel(document, {
    prefix: 'proxyDiff', title: 'Diff selected flows', host: shell, api, toast,
    returnFocus: () => opener,
    onClose: () => closeModal(modal),
  });
  // The Comparer's own picture: the server already renders a flow-vs-flow diff
  // PNG, so pasting the comparison into a report should not need a screenshot.
  copyPng = document.createElement('button');
  copyPng.type = 'button';
  copyPng.id = 'proxyDiffCopyPng';
  copyPng.className = 'btn xs';
  copyPng.innerHTML = icon('image') + ' Copy diff as PNG';
  copyPng.setAttribute('aria-label', 'Copy this diff as a PNG image');
  copyPng.addEventListener('click', () => {
    if (!shownIds) { toast('open a diff first'); return; }
    copyPreviewPng('/api/render/flow-diff.png?a=' + shownIds[0] + '&b=' + shownIds[1], 'flow-diff-' + shownIds[0] + '-' + shownIds[1]);
  });
  const head = shell.querySelector('.rep-diff-head');
  if (head) head.insertBefore(copyPng, head.querySelector('#proxyDiffClose'));
}

// selectedPair returns the two selected flow ids in ascending order, or null.
export function selectedPair(selected) {
  const ids = [...(selected || [])].map(Number).filter((n) => n > 0).sort((a, b) => a - b);
  return ids.length === 2 ? ids : null;
}

export function openFlowDiff() {
  const ids = selectedPair(state.selected);
  if (!ids) { toast('select exactly two flows to diff'); return false; }
  return openFlowDiffPair(ids[0], ids[1]);
}

// openFlowDiffPair compares two named flows — the Repeater tab menu diffs two
// tabs' responses through the same Comparer.
export function openFlowDiffPair(a, b) {
  const x = Number(a), y = Number(b);
  if (!(x > 0 && y > 0) || x === y) { toast('choose two different flows to diff'); return false; }
  if (!modal || !modal.isConnected) build();
  opener = document.activeElement;
  shownIds = [x, y];
  openModal(modal, { onEscape: () => panel.close(), onDismiss: () => panel.close() });
  panel.open(x, y);
  return true;
}
