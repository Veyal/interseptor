// collections-safety.js — pure helpers behind "do not lose the user's work": in-session delete Undo and the
// pre-send stored version. No DOM, no network; covered by _js-tests/collections-safety.test.mjs.

export const UNDO_WINDOW_MS = 15000;
export const MAX_UNDO_REQUESTS = 200;
const SENT_OVER_CAP = 20;

// orderForRestore returns the root item and its descendants with every parent before its children.
export function orderForRestore(items, rootUid) {
  const kids = new Map();
  const byUid = new Map();
  for (const it of items || []) { byUid.set(it.uid, it); const k = it.parentUid || ''; (kids.get(k) || kids.set(k, []).get(k)).push(it); }
  const out = [];
  const root = byUid.get(rootUid);
  if (!root) return out;
  const queue = [root];
  while (queue.length) {
    const it = queue.shift();
    out.push(it);
    for (const c of kids.get(it.uid) || []) queue.push(c);
  }
  return out;
}

// restoreBody builds the POST /api/collections/{uid}/items body: identity and revision fields are dropped
// (the server assigns them), the original rank is kept so the item lands where it was.
export function restoreBody(item, parentUid, stripEvents) {
  const b = { ...item };
  for (const k of ['uid', 'rev', 'ts', 'collectionUid']) delete b[k];
  b.parentUid = parentUid || '';
  if (stripEvents) delete b.events;
  return b;
}

// needsScriptStrip: creating an item through the UI auto-trusts the scripts it carries, so a restored item
// must not bring back a script that was quarantined (imported, untrusted) when it was deleted.
export function needsScriptStrip(item, scripts) {
  if (!item || !Array.isArray(item.events) || !item.events.length) return false;
  if (!scripts || !Array.isArray(scripts.scripts)) return true;
  return scripts.scripts.some((s) => !s.trusted && (s.owners || []).includes(item.uid));
}

export function undoable(items) {
  return (items || []).filter((i) => i.kind === 'request').length <= MAX_UNDO_REQUESTS;
}

export function undoMessage(name, descendants) {
  const n = descendants | 0;
  return 'Deleted "' + (name || 'Untitled') + '"' + (n ? ' and ' + n + ' item' + (n === 1 ? '' : 's') : '') + '.';
}

// Pre-send stored versions: when Send saves an edit over a stored request, the version that was stored is kept
// (oldest wins, so repeated sends still lead back to what was there before the experiment).
export function rememberSentOver(map, uid, base) {
  if (!map.has(uid)) {
    map.set(uid, base);
    while (map.size > SENT_OVER_CAP) map.delete(map.keys().next().value);
  }
}
export function sentOverFor(map, uid) { return map.get(uid) || null; }
export function forgetSentOver(map, uid) { map.delete(uid); }
