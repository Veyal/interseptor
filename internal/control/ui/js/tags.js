// tags.js — the History tag quick-bar and per-tag colors. Loads the project's tags
// (with counts + colors) from /api/tags, renders a clickable filter strip above the
// flow list, and lets you color a tag via its right-click menu. Tag colors are also
// applied to the per-row tag chips (rendered in proxy.js via state.tagColors).
import { $, esc, escAttr, api, state, toast, openCtxMenu } from './core.js';
import { filterByTag, renderRows } from './proxy.js';

// A small preset palette using theme CSS variables (follows light/dark).
export const TAG_COLORS = [
  ['red', 'var(--red)'], ['amber', 'var(--amber)'], ['green', 'var(--green)'],
  ['blue', 'var(--blue)'], ['violet', 'var(--violet)'], ['cyan', 'var(--cyan)'], ['gray', 'var(--fg3)'],
];

// tagChipStyle returns the inline style for a chip in a tag's color ('' = default).
export function tagChipStyle(tag) {
  const c = state.tagColors[tag];
  return c ? `color:${c};border-color:${c}` : '';
}

export async function loadTags() {
  try {
    const d = await api('/api/tags');
    state.tags = d.tags || [];
    state.tagColors = {};
    state.tags.forEach(t => { if (t.color) state.tagColors[t.tag] = t.color; });
    renderTagBar();
    renderRows(); // recolor the per-row tag chips with any updated colors
  } catch (e) { /* tags are non-critical; stay quiet */ }
}

export function renderTagBar() {
  const bar = $('#tagBar'); if (!bar) return;
  if (!state.tags.length) { bar.style.display = 'none'; bar.innerHTML = ''; return; }
  bar.style.display = 'flex';
  bar.innerHTML = state.tags.map(t => {
    const on = state.filters.tag === t.tag;
    return `<button class="tagchip${on ? ' on' : ''}" data-tag="${escAttr(t.tag)}" style="${tagChipStyle(t.tag)}"
      title="filter by ${escAttr(t.tag)} · right-click to color">${esc(t.tag)} <em>${t.count}</em></button>`;
  }).join('');
  bar.querySelectorAll('.tagchip').forEach(b => {
    b.onclick = () => filterByTag(b.dataset.tag);
    b.oncontextmenu = e => { e.preventDefault(); openColorMenu(e.clientX, e.clientY, b.dataset.tag); };
  });
}

function openColorMenu(x, y, tag) {
  openCtxMenu(x, y, [
    {
      head: 'COLOR · ' + tag,
      items: TAG_COLORS.map(([name, hex]) => ({
        label: name, val: hex, on: state.tagColors[tag] === hex, act: () => setTagColor(tag, hex),
      })).concat([{ label: 'Clear color', danger: true, act: () => setTagColor(tag, '') }]),
    },
    { items: [{ label: 'Filter by this tag', act: () => filterByTag(tag) }] },
  ]);
}

async function setTagColor(tag, color) {
  try {
    await api('/api/tags/' + encodeURIComponent(tag) + '/color', {
      method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ color }),
    });
    // The server broadcasts tags.update, but refresh locally too for snappiness.
    await loadTags();
  } catch (e) { toast(e.message); }
}

// tagActionTargets returns flow ids for a tag mutation: the whole multi-selection
// when the row is part of it, otherwise just that row.
export function tagActionTargets(flowId) {
  if (state.selected.size && state.selected.has(flowId)) return [...state.selected];
  return [flowId];
}

// mutateFlowTags bulk-adds or bulk-removes tags via POST /api/flows/tags.
export async function mutateFlowTags(flowIds, { add, remove }) {
  if (!flowIds?.length) return;
  const body = { flowIds };
  if (add?.length) body.add = add;
  if (remove?.length) body.remove = remove;
  if (!body.add && !body.remove) return;
  try {
    await api('/api/flows/tags', {
      method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body),
    });
    const n = flowIds.length;
    if (remove?.length) toast('removed from ' + n + ' flow' + (n === 1 ? '' : 's'));
    else if (add?.length) toast('tagged ' + n + ' flow' + (n === 1 ? '' : 's'));
  } catch (e) { toast(e.message); }
}

// openTagChipMenu — right-click a per-row tag chip to filter or remove that tag.
export function openTagChipMenu(x, y, tag, flowId) {
  const targets = tagActionTargets(flowId);
  const n = targets.length;
  openCtxMenu(x, y, [{
    head: 'TAG · ' + tag,
    items: [
      { label: 'Filter by this tag', on: state.filters.tag === tag, act: () => filterByTag(tag) },
      { label: 'Remove from flow', danger: true, val: n > 1 ? n + ' selected' : '', act: () => mutateFlowTags(targets, { remove: [tag] }) },
    ],
  }]);
}
