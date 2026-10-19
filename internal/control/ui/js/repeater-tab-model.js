// repeater-tab-model.js — pure helpers for the Repeater tab strip (no DOM), so
// they run under `node --test`.

// renamedTabLabel: the label after a rename prompt. null/undefined is a cancel
// and keeps the current name; an empty entry clears it (the URL title returns).
export function renamedTabLabel(current, input) {
  if (input == null) return String(current || '');
  return String(input).trim().slice(0, 60);
}

// placeAfter moves `tab` (already in `tabs`) to just after `anchor`, in place.
export function placeAfter(tabs, tab, anchor) {
  const from = tabs.indexOf(tab);
  if (from < 0 || tabs.indexOf(anchor) < 0) return tabs;
  tabs.splice(from, 1);
  tabs.splice(tabs.indexOf(anchor) + 1, 0, tab);
  return tabs;
}

// repeaterEscapeAction decides what Esc does in the Repeater, independent of
// where focus is (WebKit leaves it on <main> after a mouse click): close the
// diff panel first, else cancel an in-flight send, else close the History rail. A modal, another panel or a
// handler that already consumed Esc wins.
export function repeaterEscapeAction({ panelActive, modalOpen, defaultPrevented, diffOpen, sendPending, historyOpen } = {}) {
  if (!panelActive || modalOpen || defaultPrevented) return null;
  if (diffOpen) return 'diff';
  if (sendPending) return 'cancel';
  if (historyOpen) return 'history';
  return null;
}
