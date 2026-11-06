// collections-open-model.js — the guard around opening a stored request in the
// Collections editor. Pure: the panel passes its editor state in.
//
// openGuard decides what opening a request does to unsaved edits. Clicking the
// tree row of the request that is already open is not a navigation, so it must
// keep the editor: re-fetching it would throw the edits away with no dialog and
// nothing to undo. A different request is a navigation, so ask first. With
// nothing dirty every open is a plain load.
export function openGuard({ dirty = false, openUid = '', nextUid = '' } = {}) {
  if (!dirty) return 'load';
  if (openUid && openUid === nextUid) return 'keep';
  return openUid ? 'confirm' : 'load';
}

// isOpenItem: the editor is showing this stored request. Callers that act on the
// open request after opening it (the tree's Send menu) must check, because a
// cancelled "discard unsaved changes" prompt leaves the other request open.
export function isOpenItem(ed, uid) { return !!ed && !!uid && ed.uid === uid; }
