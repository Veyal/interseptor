// repeater-tab-model.js — pure helpers for the Repeater tab strip (no DOM), so
// they run under `node --test`.

// renamedTabLabel: the label after a rename prompt. null/undefined is a cancel
// and keeps the current name; an empty entry clears it (the URL title returns).
export function renamedTabLabel(current, input) {
  if (input == null) return String(current || '');
  return String(input).trim().slice(0, 60);
}

// placeAfter moves `tab` (already in `tabs`) to just after `anchor`, in place.
// Dropping the tab out of the list and looking the anchor up again is what makes
// the identity case special: with `anchor === tab` the anchor is gone after the
// splice, so indexOf() returned -1 and the tab landed at the front of the strip
// instead of staying where it already was.
export function placeAfter(tabs, tab, anchor) {
  const from = tabs.indexOf(tab);
  if (from < 0 || tabs.indexOf(anchor) < 0) return tabs;
  if (tab === anchor) return tabs;
  tabs.splice(from, 1);
  tabs.splice(tabs.indexOf(anchor) + 1, 0, tab);
  return tabs;
}

// mergeHistoryNewestFirst merges send-history rows from several sources into one
// newest-first list (highest flow id first), de-duplicated by id.
//
// The order is load-bearing, not cosmetic: the History rail renders the list
// as-is and previousHistoryEntry() resolves "the previous send" *positionally*
// (the next entry). Concatenating a legacy-endpoint snapshot behind the tab's own
// sends therefore put a flow captured after this tab's last send below an older
// one, so "Diff vs previous" compared against a *newer* response and the diff's
// removed/added sides came out reversed.
export function mergeHistoryNewestFirst(...lists) {
  const out = [];
  const seen = new Set();
  for (const list of lists) {
    for (const entry of Array.isArray(list) ? list : []) {
      const id = Number(entry && entry.id);
      if (!Number.isSafeInteger(id) || id <= 0 || seen.has(id)) continue;
      seen.add(id);
      out.push(entry);
    }
  }
  return out.sort((a, b) => Number(b.id) - Number(a.id));
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
