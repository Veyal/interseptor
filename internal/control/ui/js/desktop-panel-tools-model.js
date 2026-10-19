// desktop-panel-tools-model.js — pure rules behind the desktop skin of the test
// panels (Repeater, Intruder, Collections; docs/desktop-design.md, Panels). No
// DOM, so it runs under node (_js-tests/desktop-panel-tools-model.test.mjs).

// sentenceCase turns a static ALL-CAPS pane label ("REQUEST TEMPLATE") into the
// sentence-case the desktop skin shows ("Request template"). Text that is not
// all capitals (already written as a sentence, or mixed with code such as §)
// is returned unchanged.
export function sentenceCase(text) {
  const s = String(text == null ? '' : text).trim();
  if (!s || s !== s.toUpperCase() || s === s.toLowerCase()) return s;
  const lower = s.toLowerCase();
  return lower.charAt(0).toUpperCase() + lower.slice(1);
}

// sendHint is the Send shortcut as the platform writes it.
export function sendHint(mac) {
  return mac ? '⌘↩' : 'Ctrl+↩';
}

// stripGlyph removes the decorative trailing arrow web labels carry ("Send" + arrow).
export function stripGlyph(text) {
  return String(text == null ? '' : text).replace(/\s*[\u25B8\u25B6]\s*$/u, '');
}

// The Intruder results header says the same thing in three places (the header
// count, the stats line and the progress summary). runLine merges them into one
// sentence: "15 of 15 requests · 6 flagged · 0 errors · 1 anomaly".
export function runLine(summaryText, statsText) {
  const summary = String(summaryText == null ? '' : summaryText).trim();
  if (!summary) return '';
  const m = /(\d[\d,.]*)\s+flagged/i.exec(String(statsText == null ? '' : statsText));
  if (!m) return summary;
  const parts = summary.split(' · ');
  // Keep "N of M requests" (and a rate, if any) first, then flagged, then the rest.
  const head = parts.filter((p) => /requests?$|req\/s$/.test(p));
  const rest = parts.filter((p) => !head.includes(p));
  return [...head, `${m[1]} flagged`, ...rest].join(' · ');
}

// isCleanDirtyText: Collections shows "Matches the stored request" beside a
// disabled Save. That is the resting state, so the desktop skin keeps it for
// screen readers only and shows the line when there is something to say.
export function isCleanDirtyText(text) {
  return /^\s*matches the stored request\s*$/i.test(String(text == null ? '' : text));
}

// overflowStep moves the focus index inside an overflow menu: arrows wrap,
// Home and End jump. Returns the new index, or -1 for a key it does not handle.
export function overflowStep(key, index, count) {
  if (!(count > 0)) return -1;
  if (key === 'ArrowDown') return (index + 1) % count;
  if (key === 'ArrowUp') return (index - 1 + count) % count;
  if (key === 'Home') return 0;
  if (key === 'End') return count - 1;
  return -1;
}

// placeMenu positions an overflow menu under (or over) its trigger, right
// aligned to the trigger and kept inside the viewport.
export function placeMenu(trigger, size, viewport, gap = 4, margin = 8) {
  let left = trigger.right - size.width;
  left = Math.max(margin, Math.min(left, viewport.width - size.width - margin));
  let top = trigger.bottom + gap;
  if (top + size.height > viewport.height - margin) top = Math.max(margin, trigger.top - gap - size.height);
  return { left: Math.round(left), top: Math.round(top) };
}
