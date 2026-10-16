// ctxmenu-model.js — pure logic for the universal context menu engine: menu-tree
// normalization, cascading-flyout geometry (with edge-collision flipping), the
// keyboard state machine, and the declarative model for the text-field clipboard
// bridge. No DOM and no imports, so every rule is unit-tested under `node --test`
// (internal/control/ui/_js-tests) instead of needing a browser.
//
// core.js owns the rendering and the real actions; this module only decides what
// a menu contains and where a submenu goes.

// A submenu opens (or a stale one closes) after the pointer rests for this long,
// so a diagonal sweep across a parent row on the way to a flyout does not swap
// the flyout out from under the pointer.
export const SUBMENU_HOVER_MS = 120;

// The "7-item rule": a root menu is a map, not a list. Seven roots is the target
// and nine is the hard cap — past that the operator scrolls inside a popup
// instead of reading it, so the extra actions belong in a categorised submenu.
export const ROOT_ITEM_TARGET = 7;
export const ROOT_ITEM_MAX = 9;

const isSep = (it) => !!(it && it.sep);

// normalizeItems drops empty entries, collapses runs of separators and trims
// leading/trailing ones, so a section built with conditional spread syntax never
// renders a stray divider.
export function normalizeItems(items) {
  const out = [];
  for (const it of Array.isArray(items) ? items : []) {
    if (!it) continue;
    if (isSep(it)) {
      if (out.length && !isSep(out[out.length - 1])) out.push({ sep: true });
      continue;
    }
    if (!it.label && !it.val) continue;
    out.push(it);
  }
  while (out.length && isSep(out[out.length - 1])) out.pop();
  return out;
}

// normalizeSections returns the sections that have something to show, each with
// its items normalized. Children are normalized lazily, when the flyout opens.
export function normalizeSections(sections) {
  const out = [];
  for (const sec of Array.isArray(sections) ? sections : []) {
    if (!sec) continue;
    const items = normalizeItems(sec.items);
    if (!items.length) continue;
    out.push({ head: sec.head || '', items });
  }
  return out;
}

// hasChildren reports whether an item opens a flyout. An item may carry both
// `children` and `act`; children win, because a parent row must stay a parent.
export function hasChildren(item) {
  return !!(item && Array.isArray(item.children) ? item.children.filter(Boolean).length : false);
}

// childSections turns an item's `children` into sections. A child list may be a
// flat array of items or an array of {head, items} sections; both are accepted so
// a caller can group a long flyout without a second nesting level.
export function childSections(item) {
  const kids = (item && item.children ? item.children : []).filter(Boolean);
  if (!kids.length) return [];
  const sectioned = kids.every((k) => Array.isArray(k.items));
  return normalizeSections(sectioned ? kids : [{ items: kids }]);
}

// menuRows flattens sections into the ordered rows one menu level renders.
// Separators and headings are not rows: they are never focusable.
export function menuRows(sections) {
  const rows = [];
  for (const sec of normalizeSections(sections)) {
    for (const it of sec.items) {
      if (isSep(it)) continue;
      rows.push(it);
    }
  }
  return rows;
}

// rootItemCount is what the 7-item rule counts: focusable rows at the root.
export const rootItemCount = (sections) => menuRows(sections).length;
export const exceedsRootLimit = (sections) => rootItemCount(sections) > ROOT_ITEM_MAX;

const clamp = (n, lo, hi) => Math.min(hi, Math.max(lo, n));

// submenuPlacement puts a flyout beside its parent panel, overlapping the frame
// by `overlap` so the pointer can cross without leaving both menus. It opens to
// the right; when the flyout would run past the viewport it flips to the left of
// the parent, and when neither side fits it takes the roomier one and is clamped.
export function submenuPlacement({ parent, item, width, height, viewport, overlap = 4, margin = 8 }) {
  const vLeft = viewport.left || 0, vTop = viewport.top || 0;
  const vRight = vLeft + viewport.width, vBottom = vTop + viewport.height;
  const roomRight = vRight - margin - (parent.right - overlap);
  const roomLeft = parent.left + overlap - (vLeft + margin);
  const fitsRight = width <= roomRight;
  const side = fitsRight || roomRight >= roomLeft ? 'right' : 'left';
  const panelWidth = Math.min(width, Math.max(0, viewport.width - margin * 2));
  const wanted = side === 'right' ? parent.right - overlap : parent.left + overlap - panelWidth;
  const left = clamp(wanted, vLeft + margin, Math.max(vLeft + margin, vRight - margin - panelWidth));
  const maxHeight = Math.max(0, viewport.height - margin * 2);
  const panelHeight = Math.min(height, maxHeight);
  // Align the flyout's first row with the row that opened it, then slide it up
  // only as far as needed to keep the whole panel on screen.
  const anchorTop = item && item.top != null ? item.top : parent.top;
  const top = clamp(anchorTop - 5, vTop + margin, Math.max(vTop + margin, vBottom - margin - panelHeight));
  return { left, top, side, width: panelWidth, maxHeight };
}

// menuKeyAction maps a keydown to one engine action. Returns '' for keys the
// menu does not own, so the caller can let them through.
export function menuKeyAction(key, { children = false, depth = 0 } = {}) {
  switch (key) {
    case 'ArrowDown': return 'next';
    case 'ArrowUp': return 'prev';
    case 'Home': return 'first';
    case 'End': return 'last';
    case 'ArrowRight': return children ? 'open' : '';
    case 'ArrowLeft': return depth > 0 ? 'close' : '';
    case 'Escape': return depth > 0 ? 'close' : 'dismiss';
    case 'Enter': return children ? 'open' : 'activate';
    case ' ': return children ? 'open' : 'activate';
    case 'Tab': return 'dismiss';
    default: return '';
  }
}

// nextIndex moves the highlight within one level, wrapping at both ends so
// ArrowUp from the first row lands on the last.
export function nextIndex(index, count, action) {
  if (count <= 0) return 0;
  switch (action) {
    case 'next': return (index + 1) % count;
    case 'prev': return (index - 1 + count) % count;
    case 'first': return 0;
    case 'last': return count - 1;
    default: return clamp(index, 0, count - 1);
  }
}

/* ---- text field clipboard bridge ---------------------------------------- */

// Suppressing the native menu app-wide takes Cut/Copy/Paste away from every text
// field, so Interseptor has to give them back. CODEC_OPS are the `/api/decode`
// ops that are useful on a selection in a request editor or a search box.
export const CODEC_OPS = [
  { op: 'urldecode', label: 'URL decode' },
  { op: 'urlencode', label: 'URL encode' },
  { op: 'base64decode', label: 'Base64 decode' },
  { op: 'base64encode', label: 'Base64 encode' },
  { op: 'hexdecode', label: 'Hex decode' },
  { op: 'hexencode', label: 'Hex encode' },
  { op: 'htmldecode', label: 'HTML entity decode' },
  { op: 'htmlencode', label: 'HTML entity encode' },
  { op: 'jwtdecode', label: 'JWT decode' },
];

// PAYLOAD_MARKER is Intruder's position marker; Repeater and Intruder editors
// offer it so a position can be marked without hunting for the character.
export const PAYLOAD_MARKER = '§';

// INSERT_SNIPPETS are the values a tester types over and over. `probe` entries
// are inert strings an operator sends deliberately at an authorized target; they
// are not executed here and nothing is sent automatically.
export const INSERT_SNIPPETS = [
  { id: 'marker', label: 'Payload marker ' + PAYLOAD_MARKER, payload: true },
  { id: 'uuid', label: 'Random UUID' },
  { id: 'timestamp', label: 'ISO timestamp' },
  { id: 'canary-sqli', label: "SQLi canary  ' OR 1=1--", value: "' OR 1=1--", probe: true },
  { id: 'canary-xss', label: 'XSS canary  <script>alert(1)</script>', value: '<script>alert(1)</script>', probe: true },
  { id: 'canary-traversal', label: 'Traversal canary  ../../etc/passwd', value: '../../etc/passwd', probe: true },
];

// textFieldMenuModel is the declarative menu for an input/textarea/contenteditable:
// categorised roots, never a flat wall. Actions are identifiers that core.js maps
// to real behaviour, so the categories and labels are testable without a browser.
export function textFieldMenuModel({ selection = '', editable = true, empty = true, payloadMarker = false, clipboardRead = true } = {}) {
  const sel = String(selection || '');
  const hasSel = sel.length > 0;
  const clip = [
    { label: 'Cut', icon: 'copy', action: 'cut', disabled: !hasSel || !editable },
    { label: 'Copy', icon: 'copy', action: 'copy', disabled: !hasSel },
    { label: 'Paste', icon: 'clipboard', action: 'paste', disabled: !editable || !clipboardRead },
    { label: 'Select all', action: 'select-all', disabled: empty },
  ];
  const sections = [{ head: 'CLIPBOARD', items: clip }];
  const codecs = CODEC_OPS.map((c) => ({ label: c.label, action: 'codec:' + c.op }));
  const transformHead = hasSel ? 'selection' : 'whole field';
  sections.push({
    items: [
      { label: 'Encode / decode', icon: 'codec', hint: transformHead, children: codecs, disabled: !editable || (!hasSel && empty) },
      {
        label: 'Insert',
        icon: 'plus',
        children: INSERT_SNIPPETS.filter((s) => !s.payload || payloadMarker).map((s) => ({ label: s.label, action: 'insert:' + s.id })),
        disabled: !editable,
      },
      { label: 'Open in Decoder', icon: 'decode', action: 'decoder', disabled: !hasSel && empty },
    ],
  });
  sections.push({ items: [{ label: 'Clear field', icon: 'trash', action: 'clear', danger: true, disabled: !editable || empty }] });
  return sections;
}

// selectionMenuModel is the menu over a plain text selection outside a text field
// (highlighted markdown, a rendered response, a table cell).
export function selectionMenuModel({ selection = '' } = {}) {
  const sel = String(selection || '');
  if (!sel) return [];
  return [{
    head: 'SELECTION',
    items: [
      { label: 'Copy', icon: 'copy', action: 'copy' },
      { label: 'Encode / decode', icon: 'codec', children: CODEC_OPS.map((c) => ({ label: c.label, action: 'codec:' + c.op })) },
      { label: 'Open in Decoder', icon: 'decode', action: 'decoder' },
      { label: 'Search in History', icon: 'search', action: 'search' },
    ],
  }];
}

// shortLabel trims a value for use beside a menu label.
export function shortLabel(s, max = 40) {
  const v = String(s == null ? '' : s).replace(/\s+/g, ' ').trim();
  return v.length > max ? v.slice(0, max) + '…' : v;
}
