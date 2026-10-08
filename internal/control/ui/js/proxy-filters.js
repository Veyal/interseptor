// proxy-filters.js: pure view-model helpers for the Proxy panel (filter counts,
// empty states, bulk verbs, inspector docking). No imports and no DOM access so
// every function runs under `node --test`.

export const DRAWER_MIN_WIDTH = 1100;

// activeFilterCount mirrors the legacy anyFilter(): each simple filter counts
// once and every exclusion counts individually. Hiding TLS failures is the
// default and is not counted.
export function activeFilterCount(st) {
  const f = st.filters || {};
  let n = 0;
  for (const k of ['scheme', 'method', 'status', 'host', 'search', 'tag']) if (f[k]) n++;
  n += (f.exclude || []).length;
  if (st.notesOnly) n++;
  if (st.inScopeOnly) n++;
  if (st.showManual === false) n++;
  if (st.showAI === false) n++;
  if (st.showCollection === false) n++;
  return n;
}

// popoverFilterCount counts only the controls that live inside the Filters
// popover (the badge on the Filters button).
export function popoverFilterCount(st) {
  const f = st.filters || {};
  let n = 0;
  if (st.notesOnly) n++;
  if (st.showManual === false) n++;
  if (st.showAI === false) n++;
  if (st.showCollection === false) n++;
  if (f.tag) n++;
  if (st.hideTlsFailed === false) n++;
  return n;
}

export function emptyStateModel(st, { proxyAddr = '' } = {}) {
  const n = activeFilterCount(st);
  if (n > 0) {
    return { kind: 'empty-filtered', title: `No flows match ${n} filter${n === 1 ? '' : 's'}`, hint: 'Clear the filters to see everything captured so far.', count: n };
  }
  return {
    kind: 'empty-first',
    title: 'Point your browser or device at the proxy',
    hint: proxyAddr ? `Set the HTTP and HTTPS proxy to ${proxyAddr}. Requests appear here as they arrive.` : 'Requests appear here as they arrive.',
    count: 0,
  };
}

export function attachedLabel(findings) {
  const list = findings || [];
  if (!list.length) return '';
  return `Attached to finding${list.length === 1 ? '' : 's'} ${list.map((f) => '#' + f.id).join(', ')}`;
}

// bulkVerbs lists the bulk-bar verbs and whether each is enabled for a selection
// of `n` flows, with the reason shown when it is not.
export function bulkVerbs(n) {
  const any = n > 0;
  return [
    { id: 'repeater', label: 'Repeater', enabled: any, reason: 'Select at least one flow' },
    { id: 'intruder', label: 'Intruder', enabled: n === 1, reason: 'Intruder takes exactly one flow' },
    { id: 'scanner', label: 'Scanner', enabled: n === 1, reason: 'Scanner takes exactly one flow' },
    { id: 'tag', label: 'Tag', enabled: any, reason: 'Select at least one flow' },
    { id: 'finding', label: 'Add to finding', enabled: any, reason: 'Select at least one flow' },
    { id: 'copyas', label: 'Copy as', enabled: any, reason: 'Select at least one flow' },
    { id: 'diff', label: 'Diff', enabled: n === 2, reason: 'Select exactly two flows to diff' },
    { id: 'render', label: 'Render image', enabled: any, reason: 'Select at least one flow' },
    { id: 'delete', label: 'Delete', enabled: any, reason: 'Select at least one flow' },
  ];
}

export function parseDockPref(raw) { return raw === 'drawer' ? 'drawer' : 'bottom'; }

// resolveDock returns where the inspector lives: the side drawer needs room, so
// narrow viewports always use the bottom inspector.
export function resolveDock(pref, width) {
  return pref === 'drawer' && width >= DRAWER_MIN_WIDTH ? 'drawer' : 'bottom';
}

// middleEllipsis shortens a long path to `max` characters by eliding the middle,
// keeping the start (route) and the end (resource name) visible on phone cards.
export function middleEllipsis(text, max = 44) {
  const t = String(text == null ? '' : text);
  if (max < 5 || t.length <= max) return t;
  const keep = max - 1;
  const head = Math.ceil(keep * 0.6);
  return t.slice(0, head) + '\u2026' + t.slice(t.length - (keep - head));
}
