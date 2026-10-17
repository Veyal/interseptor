// shell-title.js — the browser tab title and the per-panel heading text. Pure
// (no DOM, no core.js) so it runs under node --test.
//
// The title names the focused tool first, so browser history, the tab strip and
// screen readers say which panel is open: "Proxy · demo · Interseptor".

export const APP_NAME = 'Interseptor';

export function documentTitle(panelLabel, project) {
  const parts = [];
  const label = String(panelLabel || '').trim();
  const proj = String(project || '').trim();
  if (label) parts.push(label);
  if (proj) parts.push(proj);
  parts.push(APP_NAME);
  return parts.join(' · ');
}

// tabLabel reads a rail tab's visible name: its text minus the count badge and
// the screen-reader-only "new updates" note.
export function tabLabel(text, note) {
  let s = String(text || '');
  if (note) s = s.split(note).join('');
  return s.replace(/\s+/g, ' ').trim();
}
