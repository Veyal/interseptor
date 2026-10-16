// dock-model.js — pure logic for the phone bottom navigation. No imports and no
// DOM, so it runs under node. The five destinations map onto the rail groups;
// the DOM wiring lives in dock.js and switches panels through the shell's
// activateTab, never here.

export const DOCK_DESTINATIONS = [
  { id: 'capture', label: 'Capture', icon: 'proxy', panels: ['proxy', 'intercept'] },
  { id: 'test', label: 'Test', icon: 'repeater', panels: ['repeater', 'intruder'] },
  { id: 'recon', label: 'Recon', icon: 'scanner', panels: ['scanner', 'map'] },
  { id: 'report', label: 'Report', icon: 'report', panels: ['findings', 'notes', 'activity'] },
  { id: 'more', label: 'More', icon: 'more', panels: ['settings'] },
];

export const PANEL_LABELS = {
  proxy: 'Proxy', intercept: 'Intercept', repeater: 'Repeater', intruder: 'Intruder',
  scanner: 'Scanner', map: 'Map', findings: 'Findings', notes: 'Notes', activity: 'Activity', settings: 'Settings',
};

const byId = (id) => DOCK_DESTINATIONS.find((d) => d.id === id) || null;

export function destinationForPanel(panel) {
  const d = DOCK_DESTINATIONS.find((x) => x.panels.includes(panel));
  return d ? d.id : null;
}

// resolvePanel: the panel a destination opens, preferring the last one used.
export function resolvePanel(destId, last) {
  const d = byId(destId);
  if (!d) return null;
  const remembered = last && last[destId];
  return d.panels.includes(remembered) ? remembered : d.panels[0];
}

// rememberPanel returns a new map with panel stored under its destination, or the
// same map when the panel is unknown.
export function rememberPanel(last, panel) {
  const dest = destinationForPanel(panel);
  if (!dest) return last;
  return { ...(last || {}), [dest]: panel };
}

// parseStoredLast reads the persisted map defensively: corrupt JSON, wrong shapes
// and panels that belong to another destination are all dropped.
export function parseStoredLast(raw) {
  let value;
  try { value = JSON.parse(raw); } catch (e) { return {}; }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  const out = {};
  for (const d of DOCK_DESTINATIONS) {
    if (d.panels.includes(value[d.id])) out[d.id] = value[d.id];
  }
  return out;
}

// badgeCount reads a rail badge (text content and hidden state) as a count.
export function badgeCount(text, hidden) {
  if (hidden) return 0;
  const n = Number.parseInt(String(text == null ? '' : text).trim(), 10);
  return Number.isFinite(n) && n > 0 && /^\s*\d+\s*$/.test(String(text)) ? n : 0;
}

export const badgeText = (n) => (n > 99 ? '99+' : n > 0 ? String(n) : '');

const BADGE_WORDS = {
  capture: ['request held', 'requests held'],
  report: ['blocker', 'blockers'],
};
// badgeAnnouncement: the words a screen reader hears for a badge; empty when the
// destination has no badge or the count is zero.
export function badgeAnnouncement(destId, n) {
  const words = BADGE_WORDS[destId];
  if (!words || !(n > 0)) return '';
  return n + ' ' + words[n === 1 ? 0 : 1];
}

// dockKeyTarget: the button index a navigation key moves to, or -1.
export function dockKeyTarget(key, index, length) {
  if (!(length > 0)) return -1;
  if (key === 'Home') return 0;
  if (key === 'End') return length - 1;
  if (key === 'ArrowRight') return (index + 1) % length;
  if (key === 'ArrowLeft') return (index - 1 + length) % length;
  return -1;
}
