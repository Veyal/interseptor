// notes-mode-model.js — the Notes View / Edit mode rules. Pure: no imports, no DOM.

export const NOTES_MODES = ['view', 'edit'];

export const normalizeMode = (m) => (m === 'edit' ? 'edit' : 'view');
export const toggleMode = (m) => (m === 'edit' ? 'view' : 'edit');

// A note with content opens for reading; an empty one opens for writing.
export const initialMode = (text) => (String(text == null ? '' : text).trim() ? 'view' : 'edit');

// A reload (panel revisit, agent nudge) must not flip a mode the user picked.
export function resolveLoadMode({ current, userChosen, text }) {
  return userChosen ? normalizeMode(current) : initialMode(text);
}

// caretForWord maps a word selected in the rendered view to its first position
// in the markdown source, so Edit opens near it. null when it cannot be found.
export function caretForWord(src, word) {
  const w = String(word == null ? '' : word).trim();
  if (!w || w.length > 200) return null;
  const i = String(src == null ? '' : src).indexOf(w);
  return i < 0 ? null : i;
}
