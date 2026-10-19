// desktop-panel-proxy-model.js — pure rules for the desktop History layout
// (docs/desktop-design.md, Panels). No DOM and no imports, so node --test covers
// them; desktop-panel-proxy.js owns the elements and calls these.

// The inspector keeps three views on the header and folds the rest into "More".
export const PRIMARY_VIEWS = ['pretty', 'raw', 'hex'];

// splitViews partitions a segmented control's views into the visible three and
// the ones that move to the More menu. Views the page hid stay hidden.
export function splitViews(views, primary = PRIMARY_VIEWS) {
  const list = (Array.isArray(views) ? views : []).filter((v) => v && v.view && !v.hidden);
  return { primary: list.filter((v) => primary.includes(v.view)), more: list.filter((v) => !primary.includes(v.view)) };
}

// moreLabel is the More button's text: its own name until a folded view is the
// current one, then that view's name, so the header always says what is shown.
export function moreLabel(more, current) {
  const hit = (Array.isArray(more) ? more : []).find((v) => v.view === current);
  return hit ? String(hit.label || hit.view) : 'More';
}

// activeTokenCount: how many filter tokens the search field shows, given each
// chip's data-clear key. Method, status and the search text are not counted:
// they already show in their own controls. A narrow toolbar shows this number
// as one chip instead of the tokens.
export const OWN_CONTROL_CHIPS = ['search', 'method', 'status'];
export function activeTokenCount(keys) {
  return (Array.isArray(keys) ? keys : []).filter((k) => !OWN_CONTROL_CHIPS.includes(k)).length;
}

// toolbarWidthTier: which toolbar tier a History panel of `width` px shows. The
// tokens collapse to a count chip at 880px and the pills drop their chrome at
// 760px. A parked panel measures 0 and keeps the wide layout.
export const TOOLBAR_NARROW = 880;
export const TOOLBAR_COMPACT = 760;
export function toolbarWidthTier(width) {
  const w = Number(width);
  if (!(w > 0)) return { narrow: false, compact: false };
  return { narrow: w <= TOOLBAR_NARROW, compact: w <= TOOLBAR_COMPACT };
}

// isViewHidden: proxy.js hides a view button with [hidden] or with an inline
// style (Render is display:none for a response that is not HTML).
export function isViewHidden(v) {
  return !v || !!v.hidden || v.display === 'none';
}

// notePlaceholder: the compact affordance reads "Add note…"; the open field
// carries the full prompt.
export const NOTE_PLACEHOLDER_SHORT = 'Add note…';
export function notePlaceholder(open, full) {
  return open ? full : NOTE_PLACEHOLDER_SHORT;
}

// sentenceCase turns the web's uppercase pane labels into the desktop's.
export function sentenceCase(text) {
  const t = String(text || '').trim().toLowerCase();
  return t ? t[0].toUpperCase() + t.slice(1) : '';
}

// pressBubbles: should a menu item's synthetic click on `node` bubble? Source
// buttons open their own menu and need it not to (the app-wide click-away
// listener would close the menu at once). Copy-image buttons are the opposite:
// core.js handles them from one delegated document listener, so the click has
// to reach document.
export function pressBubbles(node) {
  return !!(node && node.classList && node.classList.contains('js-copy-image'));
}
