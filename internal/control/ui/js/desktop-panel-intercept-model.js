// desktop-panel-intercept-model.js — pure rules for the desktop Intercept layout.
// No DOM and no imports; desktop-panel-intercept.js owns the elements.

// ACTION_ORDER is the desktop action row, left to right: the one primary, the
// danger action, then the quiet ones. Unknown ids keep their relative order at
// the end, so a button the page adds later is never dropped.
export const ACTION_ORDER = ['forwardBtn', 'dropBtn', 'heldRepeaterBtn', 'forwardRepeaterBtn', 'forwardAllBtn'];

export function orderActions(ids, order = ACTION_ORDER) {
  const list = Array.isArray(ids) ? ids : [];
  const rank = (id) => { const i = order.indexOf(id); return i < 0 ? order.length : i; };
  return list.map((id, i) => ({ id, i })).sort((a, b) => rank(a.id) - rank(b.id) || a.i - b.i).map((x) => x.id);
}

// sameOrder reports whether two id lists match, so the DOM is touched only when
// the row actually needs re-sorting.
export function sameOrder(a, b) {
  return a.length === b.length && a.every((id, i) => id === b[i]);
}

// holdRuleSummary names the "only hold when" rule on one line, for the toolbar's
// tooltip: "Holding everything" or "Holding when URL matches /admin/".
export function holdRuleSummary({ on, target, pattern }) {
  const names = { any: 'anything', url: 'URL', header: 'headers', body: 'body', method: 'method' };
  if (!on) return 'Holding everything that matches the intercept switches';
  const t = names[target] || 'anything';
  return pattern ? 'Holding when ' + t + ' matches ' + pattern : 'Holding when ' + t + ' matches (add a pattern)';
}
