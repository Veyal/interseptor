// project-picker-model.js — pure rules behind the Projects picker: relative
// times, folder grouping, the Recent group, filter matching and highlighting,
// keyboard index math and new-project validation. No DOM and no core.js, so
// every rule runs under `node --test`. project-picker.js renders the result.

export const RECENT_LIMIT = 5;
// Above this many other projects the list leads with Recent and folders start
// collapsed (except the one holding the current project).
export const RECENT_THRESHOLD = 8;
export const PAGE_STEP = 5;
const MAX_FOLDER_DEPTH = 3;
const MAX_FOLDER_SEGMENT = 48;
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

const secs = (n) => (Number.isFinite(Number(n)) && Number(n) > 0 ? Number(n) : 0);

// relativeTime renders unix seconds as a short age ("5m ago", "3d ago", "4 Mar").
export function relativeTime(sec, nowMs = Date.now()) {
  const n = secs(sec);
  if (!n) return '';
  const min = Math.floor(Math.max(0, nowMs - n * 1000) / 60000);
  if (min < 1) return 'just now';
  if (min < 60) return min + 'm ago';
  const hr = Math.floor(min / 60);
  if (hr < 36) return hr + 'h ago';
  const day = Math.floor(hr / 24);
  if (day < 14) return day + 'd ago';
  const d = new Date(n * 1000);
  const year = d.getUTCFullYear() === new Date(nowMs).getUTCFullYear() ? '' : ' ' + d.getUTCFullYear();
  return d.getUTCDate() + ' ' + MONTHS[d.getUTCMonth()] + year;
}

export function openedLabel(p, nowMs = Date.now()) {
  const opened = relativeTime(p && p.openedAt, nowMs);
  return opened ? 'Opened ' + opened : 'Never opened';
}

export function rowMeta(p, { showFolder = false, nowMs = Date.now() } = {}) {
  const folder = showFolder ? normalizeFolder(p && p.category) : '';
  return openedLabel(p, nowMs) + (folder ? ' · ' + folder : '');
}

export function normalizeFolder(raw) {
  return String(raw == null ? '' : raw).split(/[\\/]/).map((s) => s.trim()).filter(Boolean).join('/');
}

export function folderOptions(projects) {
  const set = new Set();
  for (const p of projects || []) { const f = normalizeFolder(p && p.category); if (f) set.add(f); }
  return [...set].sort((a, b) => a.localeCompare(b));
}

export function projectPathFlavorFor(value) {
  const raw = String(value || '').trim();
  return /^[A-Za-z]:[\\/]/.test(raw) || raw.startsWith('\\\\') ? 'windows' : 'posix';
}

// projectPathKey normalises an absolute folder path for identity comparison.
// Relative paths return '' (they are never a valid project location).
export function projectPathKey(value, flavor = 'posix') {
  const source = String(value || '').trim();
  if (!source) return '';
  const windows = flavor === 'windows';
  const raw = windows ? source.replace(/\\/g, '/') : source;
  const drive = windows ? (raw.match(/^[A-Za-z]:/) || [])[0] || '' : '';
  const unc = windows && !drive && raw.startsWith('//');
  const rooted = windows ? (unc || (drive && raw.slice(drive.length).startsWith('/'))) : raw.startsWith('/');
  if (!rooted) return '';
  const parts = raw.slice(drive.length).split('/'), out = [], rootDepth = unc ? 2 : 0;
  for (const part of parts) {
    if (!part || part === '.') continue;
    if (part === '..') { if (out.length > rootDepth) out.pop(); continue; }
    out.push(part);
  }
  return (unc ? '//' : drive ? drive + '/' : '/') + out.join('/');
}

export function projectKey(p, flavor = 'posix') {
  return p && p.path ? 'path:' + (projectPathKey(p.path, flavor) || p.path) : 'name:' + String(p && p.name || '');
}

export function isCurrentProject(p, current, dir, projects, flavor = 'posix') {
  const dirKey = projectPathKey(dir, flavor);
  if (p.path) return projectPathKey(p.path, flavor) === dirKey;
  const external = (projects || []).some((x) => x.path && projectPathKey(x.path, flavor) === dirKey);
  return !external && p.name === current;
}

function rangesOf(text, term) {
  const out = [];
  if (!term) return out;
  const hay = String(text || '').toLowerCase();
  for (let at = hay.indexOf(term); at >= 0; at = hay.indexOf(term, at + term.length)) out.push([at, at + term.length]);
  return out;
}

// matchProject returns highlight ranges per field when every whitespace-separated
// term matches the name, folder or path; null when any term matches nothing.
export function matchProject(p, query) {
  const terms = String(query || '').toLowerCase().split(/\s+/).filter(Boolean);
  const hit = { name: [], folder: [], path: [] };
  const fields = { name: p.name, folder: normalizeFolder(p.category), path: p.path };
  for (const term of terms) {
    let any = false;
    for (const k of Object.keys(hit)) {
      const r = rangesOf(fields[k], term);
      if (r.length) { any = true; hit[k].push(...r); }
    }
    if (!any) return null;
  }
  return hit;
}

export function highlightHTML(text, ranges, esc) {
  const s = String(text || '');
  if (!s) return '';
  const sorted = [...(ranges || [])].sort((a, b) => a[0] - b[0]);
  const merged = [];
  for (const [a, b] of sorted) {
    const last = merged[merged.length - 1];
    if (last && a <= last[1]) last[1] = Math.max(last[1], b); else merged.push([a, b]);
  }
  let out = '', at = 0;
  for (const [a, b] of merged) {
    const from = Math.max(a, at), to = Math.min(b, s.length);
    if (to <= from) continue;
    out += esc(s.slice(at, from)) + '<mark class="pm-hit">' + esc(s.slice(from, to)) + '</mark>';
    at = to;
  }
  return out + esc(s.slice(at));
}

function byActivity(a, b) {
  return secs(b.openedAt) - secs(a.openedAt) || secs(b.createdAt) - secs(a.createdAt) || String(a.name).localeCompare(String(b.name));
}

// buildPickerModel turns GET /api/project into the rendered structure. The
// current project is returned separately (pinned card) and never listed as a row.
export function buildPickerModel(projects, { query = '', current = '', dir = '', nowMs = Date.now(), flavor, open = {} } = {}) {
  const fl = flavor || projectPathFlavorFor(dir);
  const all = Array.isArray(projects) ? projects : [];
  const q = String(query || '').trim();
  let cur = null;
  const others = [];
  for (const p of all) {
    if (!cur && isCurrentProject(p, current, dir, all, fl)) cur = p;
    else others.push(p);
  }
  const total = others.length;
  const large = total > RECENT_THRESHOLD;
  const curFolder = cur ? normalizeFolder(cur.category) : null;
  const matches = [];
  for (const p of others) {
    const match = matchProject(p, q);
    if (match) matches.push({ project: p, match });
  }
  matches.sort((a, b) => byActivity(a.project, b.project));
  const groups = new Map();
  for (const m of matches) {
    const f = normalizeFolder(m.project.category);
    if (!groups.has(f)) groups.set(f, []);
    groups.get(f).push(m);
  }
  const folders = [...groups.keys()].filter(Boolean).sort((a, b) => a.localeCompare(b));
  const raw = [];
  if (!q && large) {
    const recent = matches.filter((m) => secs(m.project.openedAt)).slice(0, RECENT_LIMIT);
    if (recent.length) raw.push({ id: 'recent', label: 'Recent', folder: null, members: recent, showFolder: true });
  }
  for (const f of folders) raw.push({ id: 'folder:' + f, label: f, folder: f, members: groups.get(f), showFolder: !!q });
  if (groups.has('')) raw.push({ id: 'unfiled', label: folders.length || raw.length ? 'No folder' : 'All projects', folder: '', members: groups.get(''), showFolder: false });
  const items = [];
  const sections = raw.map((s) => {
    const containsCurrent = curFolder !== null && s.folder === curFolder;
    const remembered = Object.prototype.hasOwnProperty.call(open, s.id) ? !!open[s.id] : null;
    const isOpen = q ? true : remembered !== null ? remembered : (s.id === 'recent' || !large || containsCurrent);
    const sectionItems = s.members.map((m) => {
      const item = {
        id: s.id + '|' + projectKey(m.project, fl), key: projectKey(m.project, fl), project: m.project, match: m.match,
        section: s.id, showFolder: s.showFolder, meta: rowMeta(m.project, { showFolder: s.showFolder, nowMs }), index: items.length,
      };
      items.push(item);
      return item;
    });
    return { id: s.id, label: s.label, folder: s.folder, items: sectionItems, open: isOpen, containsCurrent };
  });
  const curMatch = cur ? matchProject(cur, q) : null;
  return {
    query: q, total, matched: matches.length, sections, items,
    current: cur ? { project: cur, key: projectKey(cur, fl), match: curMatch || { name: [], folder: [], path: [] } } : null,
    enterIndex: items.length ? 0 : -1,
  };
}

// nextIndex moves through n visible rows; -1 is the filter field above them.
export function nextIndex(i, n, key) {
  if (n <= 0) return -1;
  switch (key) {
    case 'ArrowDown': return Math.min(i + 1, n - 1);
    case 'ArrowUp': return i <= 0 ? -1 : i - 1;
    case 'Home': return 0;
    case 'End': return n - 1;
    case 'PageDown': return Math.min(Math.max(i, 0) + PAGE_STEP, n - 1);
    case 'PageUp': return Math.max(i - PAGE_STEP, 0);
    default: return i;
  }
}

export function pickerSummary({ total = 0, matched = 0, query = '' } = {}) {
  if (query) return matched ? matched + ' of ' + total + ' match' : 'No matches';
  if (!total) return 'No other projects';
  return total + ' other project' + (total === 1 ? '' : 's');
}

function folderError(raw) {
  const parts = String(raw || '').split(/[\\/]/).map((s) => s.trim());
  if (!String(raw || '').trim()) return '';
  if (parts.filter(Boolean).length > MAX_FOLDER_DEPTH) return 'A folder can be at most 3 levels deep.';
  for (const part of parts.filter(Boolean)) {
    if (part === '.' || part === '..') return 'Folder names cannot be "." or "..".';
    if ([...part].length > MAX_FOLDER_SEGMENT) return 'Each folder name can be at most 48 characters.';
  }
  return '';
}
export { folderError };

// validateNewProject mirrors the server's rules so problems show inline before
// a restart is requested. The server stays authoritative.
export function validateNewProject({ name = '', path = '', folder = '' } = {}, projects = [], flavor = 'posix') {
  const n = String(name || '').trim(), p = String(path || '').trim();
  const fail = (field, error, extra = {}) => ({ ok: false, field, error, ...extra });
  if (!n && !p) return fail('name', 'Enter a project name.');
  if (n && (n === '.' || n === '..' || /[\\/]/.test(n) || n.startsWith('~') || n.startsWith('-'))) {
    return fail('name', 'Use a plain name: no slashes, and not starting with "~" or "-".');
  }
  if (p && !projectPathKey(p, flavor)) return fail('path', 'Save location must be an absolute folder path.');
  if (n && !p) {
    const existing = (projects || []).find((x) => !x.path && x.name === n);
    if (existing) return fail('name', 'A project named "' + n + '" already exists.', { existing });
  }
  const fe = folderError(folder);
  if (fe) return fail('folder', fe);
  return { ok: true, field: '', error: '', name: n, path: p, folder: normalizeFolder(folder) };
}
