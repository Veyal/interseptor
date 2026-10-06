// flow-diff.js — the Proxy "Diff" verb: two selected flows' responses in the
// shared DiffView (hunk navigation with n / N, unified or side by side, ignore
// volatile values), shown in a modal. It reuses repeater.js's buildDiffPanel so
// the Proxy, Repeater and Intruder diffs behave identically. The old word-level
// compare modal stays reachable from the command palette.

import { api, toast, openModal, closeModal, state } from './core.js';
import { buildDiffPanel } from './repeater.js';

const MODAL_ID = 'flowDiffModal';
let modal = null;
let panel = null;
let opener = null;

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
}

// selectedPair returns the two selected flow ids in ascending order, or null.
export function selectedPair(selected) {
  const ids = [...(selected || [])].map(Number).filter((n) => n > 0).sort((a, b) => a - b);
  return ids.length === 2 ? ids : null;
}

export function openFlowDiff() {
  const ids = selectedPair(state.selected);
  if (!ids) { toast('select exactly two flows to diff'); return false; }
  if (!modal || !modal.isConnected) build();
  opener = document.activeElement;
  openModal(modal, { onEscape: () => panel.close(), onDismiss: () => panel.close() });
  panel.open(ids[0], ids[1]);
  return true;
}
