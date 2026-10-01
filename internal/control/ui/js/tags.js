// tags.js — the History tag quick-bar and per-tag colors. Loads the project's tags
// (with counts + colors) from /api/tags, renders a clickable filter strip above the
// flow list, and lets you color a tag via its right-click menu. Tag colors are also
// applied to the per-row tag chips (rendered in proxy.js via state.tagColors).
import { $, esc, escAttr, api, state, toast, openCtxMenu, renderLoadError } from './core.js';
import { filterByTag, renderRows } from './proxy.js';

// Persist stable hex values; render presets with theme colors for contrast.
export const TAG_COLORS = [
  ['red', '#ff5b5b', 'var(--red)'], ['amber', '#ffb02e', 'var(--amber)'], ['green', '#00c389', 'var(--accent)'],
  ['blue', '#4aa8ff', 'var(--blue)'], ['violet', '#c08cff', 'var(--violet)'], ['cyan', '#3fd8d0', 'var(--cyan)'], ['gray', '#8e8e99', 'var(--fg3)'],
];
let tagLoadError=null;
let tagLoadEpoch=0;
let tagReloadPending=false;
const tagColorLanes=new Map();

// tagChipStyle returns the inline style for a chip in a tag's color ('' = default).
// Stored colors are validated (server-side hex rule) before interpolation so a
// value restored from an imported project DB can never break out of the attribute.
export function tagChipStyle(tag) {
  const saved = state.tagColors[tag];
  const preset = TAG_COLORS.find(([, hex]) => hex === saved?.toLowerCase())?.[2];
  const c = preset || (saved && /^#[0-9a-fA-F]{3,8}$/.test(saved) ? saved : '');
  return c ? `color:${c};border-color:${c}` : '';
}

export async function loadTags() {
  const epoch=++tagLoadEpoch;
  try {
    const d = await api('/api/tags');
    if(epoch!==tagLoadEpoch)return;
    if(tagColorLanes.size){tagReloadPending=true;return;}
    tagLoadError=null;
    state.tags = d.tags || [];
    state.tagColors = {};
    state.tags.forEach(t => { if (t.color) state.tagColors[t.tag] = t.color; });
    renderTagBar();
    renderRows(); // recolor the per-row tag chips with any updated colors
  } catch (e) {
    if(epoch!==tagLoadEpoch)return;
    tagLoadError=e;renderTagBar();
  }
}

export function renderTagBar() {
  const bar = $('#tagBar'); if (!bar) return;
  if(tagLoadError){bar.style.display='flex';renderLoadError(bar,'Tags',tagLoadError,loadTags,state.tags.length>0);return;}
  if (!state.tags.length) { bar.style.display = 'none'; bar.innerHTML = ''; return; }
  const focusedTag=bar.contains(document.activeElement)?document.activeElement.dataset.tag:null;
  bar.style.display = 'flex';
  bar.innerHTML = state.tags.map(t => {
    const on = state.filters.tag === t.tag;
    return `<button class="tagchip${on ? ' on' : ''}" data-tag="${escAttr(t.tag)}" style="${tagChipStyle(t.tag)}"
      title="filter by ${escAttr(t.tag)} · right-click to color">${esc(t.tag)} <em>${t.count}</em></button>`;
  }).join('');
  bar.querySelectorAll('.tagchip').forEach(b => {
    b.onclick = () => filterByTag(b.dataset.tag);
    b.oncontextmenu = e => { e.preventDefault(); openColorMenu(e.clientX, e.clientY, b.dataset.tag, b); };
    b.onkeydown = e => {
      if(e.key!=='ContextMenu'&&!(e.shiftKey&&e.key==='F10'))return;
      e.preventDefault();
      const box=b.getBoundingClientRect();
      openColorMenu(box.left, box.bottom, b.dataset.tag, b);
    };
    if(b.dataset.tag===focusedTag)b.focus({preventScroll:true});
  });
}

function openColorMenu(x, y, tag, trigger) {
  openCtxMenu(x, y, [
    {
      head: 'COLOR · ' + tag,
      items: TAG_COLORS.map(([name, hex]) => ({
        label: name, val: hex, on: state.tagColors[tag]?.toLowerCase() === hex, act: () => setTagColor(tag, hex),
      })).concat([{ label: 'Clear color', danger: true, act: () => setTagColor(tag, '') }]),
    },
    { items: [{ label: 'Filter by this tag', act: () => filterByTag(tag) }] },
  ], trigger);
}

async function setTagColor(tag, color) {
  let lane=tagColorLanes.get(tag);
  if(!lane){lane={running:false,next:null,revision:0,promise:null};tagColorLanes.set(tag,lane);}
  lane.next={color,revision:++lane.revision};
  if(lane.running)return lane.promise;
  lane.running=true;
  lane.promise=(async()=>{
    while(lane.next){
      const mutation=lane.next;
      lane.next=null;
      tagLoadEpoch++;
      try{
        await api('/api/tags/' + encodeURIComponent(tag) + '/color', {
          method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ color:mutation.color }),
        });
      }catch(e){if(mutation.revision===lane.revision)toast(e.message);}
    }
    tagColorLanes.delete(tag);
    tagReloadPending=false;
    await loadTags();
  })();
  return lane.promise;
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
