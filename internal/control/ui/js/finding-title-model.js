// finding-title-model.js — pure rules for the inline "New finding" title (no DOM).
export const TITLE_MAX = 300;

// titleLimitMessage is shown once the field has hit its length cap, so the
// browser's silent truncation of a long paste is never a surprise.
export function titleLimitMessage(len) {
  return len >= TITLE_MAX ? 'Titles are limited to ' + TITLE_MAX + ' characters; the rest was cut.' : '';
}

// carryToFullForm is what the inline row hands the full dialog.
export function carryToFullForm(title, severity) {
  return { title: String(title || '').trim(), severity: severity || 'Medium' };
}
