// desktop-panel-activity.js — the Activity panel's desktop layout (docs/desktop-design.md,
// Panels). Runs only inside a desktop host (html.desktop-app); activity.js calls it
// behind that check, so a browser tab keeps the web markup. It:
//   - folds the category filter into the single toolbar row (a segmented filter
//     that lists only categories that have events);
//   - gives every timeline row a small type icon for its category.
// Ids, roles and the row/filter handlers in activity.js are unchanged.
import { icon } from './core.js';
import { activityCategory } from './activity-model.js';
import { isDesktop } from './desktop-panel-kit.js';

export const isDesktopSkin = isDesktop;

let inited = false;
export function initActivityDesktop() {
  if (inited || !isDesktopSkin()) return;
  const chips = document.getElementById('actChips');
  const input = document.getElementById('actIntentFilter');
  if (!chips || !input) return;
  inited = true;
  input.after(chips);
}

const TYPE_ICON = { proxy: 'proxy', agent: 'agent', findings: 'finding', notes: 'notes', scope: 'scope', settings: 'settings' };
const TYPE_LABEL = { proxy: 'Proxy', agent: 'Agent/MCP', findings: 'Findings', notes: 'Notes', scope: 'Scope', settings: 'Settings' };

// The category icon for a timeline row; the label is its tooltip.
export function actTypeIconHTML(tool) {
  const cat = activityCategory(tool);
  return `<span class="act-type" title="${TYPE_LABEL[cat] || 'Agent/MCP'}" aria-hidden="true">${icon(TYPE_ICON[cat] || 'agent')}</span>`;
}
