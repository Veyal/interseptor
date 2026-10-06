// layout-math.js — pure geometry and state math shared by split.js and sheet.js.
// No DOM and no imports, so every rule is unit-tested under `node --test`
// (internal/control/ui/_js-tests) instead of needing a browser.

export const clamp = (n, lo, hi) => Math.min(hi, Math.max(lo, n));

export const SPLIT_STEP = 8;
export const SPLIT_STEP_LARGE = 64;
export const SPLIT_AUTO_BREAKPOINT = 1100;
export const DETENTS = ['peek', 'half', 'full'];

export const toPercent = (size, total) => (total > 0 ? (size / total) * 100 : 0);
export const fromPercent = (pct, total) => (pct / 100) * total;

// splitBounds returns the allowed size of the primary (list) pane: it can never
// squeeze either pane below its minimum, and a container too small for both
// minimums keeps the list at its minimum.
export function splitBounds(total, mins, gutter = 8) {
  const [minList, minDetail] = mins;
  return { min: minList, max: Math.max(minList, total - minDetail - gutter) };
}

export function clampSplitSize(size, total, mins, gutter = 8) {
  const { min, max } = splitBounds(total, mins, gutter);
  return clamp(size, min, max);
}

// splitKeyAction translates a sash keydown into an action. Arrow keys only apply
// along the split axis; Shift widens the step.
export function splitKeyAction(key, { orientation = 'right', shift = false } = {}) {
  const step = shift ? SPLIT_STEP_LARGE : SPLIT_STEP;
  const keys = orientation === 'bottom' ? { ArrowUp: -1, ArrowDown: 1 } : { ArrowLeft: -1, ArrowRight: 1 };
  if (key in keys) return { delta: keys[key] * step };
  if (key === 'Home') return { to: 'min' };
  if (key === 'End') return { to: 'max' };
  if (key === 'Enter') return { toggle: true };
  return null;
}

// parseStoredPercent reads a persisted split size; anything unexpected is ignored
// so a corrupted or future-versioned entry falls back to the default.
export function parseStoredPercent(raw) {
  if (typeof raw !== 'string') return null;
  try {
    const v = JSON.parse(raw);
    if (v && v.v === 1 && typeof v.pct === 'number' && v.pct >= 1 && v.pct <= 99) return v.pct;
  } catch (e) { /* fall through */ }
  return null;
}

// resolveSplitMode: 'stack' at or below the phone breakpoint, otherwise the
// requested orientation; 'auto' puts the detail beside the list on wide screens
// and below it on tablets.
export function resolveSplitMode(width, { orientation = 'right', stackBelow = 720 } = {}) {
  if (width <= stackBelow) return 'stack';
  if (orientation === 'auto') return width >= SPLIT_AUTO_BREAKPOINT ? 'right' : 'bottom';
  return orientation;
}

export function nextDetent(current, dir, allowed = DETENTS) {
  const list = DETENTS.filter((d) => allowed.includes(d));
  const i = list.indexOf(current);
  if (i < 0) return list[0];
  return list[clamp(i + (dir > 0 ? 1 : -1), 0, list.length - 1)];
}

// sheetOffsets: visible sheet height (px) at each detent for a viewport height.
export function sheetOffsets(viewportHeight) {
  return { peek: 72, half: Math.round(viewportHeight / 2), full: viewportHeight };
}

// resolveSheetDrag decides what a finished handle drag does. dy is the downward
// travel in px (negative = up). A downward swipe from half or peek dismisses the
// sheet; otherwise the sheet snaps to the nearest detent.
export function resolveSheetDrag({ detent, dy, heights }) {
  if (dy > 0 && detent === 'half' && dy >= heights.half * 0.25) return { action: 'close' };
  if (dy > 0 && detent === 'peek' && dy >= 40) return { action: 'close' };
  const target = heights[detent] - dy;
  let best = DETENTS[0];
  for (const d of DETENTS) if (Math.abs(heights[d] - target) < Math.abs(heights[best] - target)) best = d;
  return { action: 'detent', detent: best };
}

// shouldHideDock: a soft keyboard shrinks the visual viewport well below the
// layout viewport; the bottom navigation must get out of its way.
export function shouldHideDock(visualHeight, innerHeight) {
  return typeof visualHeight === 'number' && innerHeight > 0 && visualHeight < 0.75 * innerHeight;
}
