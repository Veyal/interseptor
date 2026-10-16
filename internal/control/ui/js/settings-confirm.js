// settings-confirm.js — typed confirmation for permanent data deletion. It rides
// on the themed confirm dialog (uiConfirm) rather than adding another modal: the
// OK button stays disabled until the operator types the phrase.
import { $ } from './core.js';
import { typedConfirmMatches, TYPED_CONFIRM_PHRASE } from './settings-model.js';

const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

// confirmTyped(confirmFn, title, htmlMsg, okLabel, okClass, okColor[, phrase]) -> Promise<boolean>
// confirmFn is core's uiConfirm; it builds the dialog synchronously, so the typed
// field can be attached right after the call and removed when the dialog settles.
export function confirmTyped(confirmFn, title, htmlMsg, okLabel, okClass, okColor, phrase = TYPED_CONFIRM_PHRASE) {
  const result = confirmFn(title, htmlMsg, okLabel, okClass, okColor);
  const msg = $('#confirmMsg');
  const ok = $('#confirmOk');
  if (!msg || !ok) return result;
  const wrap = el('div', 'typed-confirm');
  const label = el('label', 'hint', 'Type ' + phrase + ' to confirm');
  label.setAttribute('for', 'confirmTypedInput');
  const input = el('input', 'btn btn-field typed-confirm-input');
  input.id = 'confirmTypedInput';
  input.type = 'text';
  input.autocomplete = 'off';
  input.spellcheck = false;
  input.setAttribute('aria-describedby', 'confirmTypedHint');
  const hint = el('p', 'hint', 'This deletes data permanently and cannot be undone.');
  hint.id = 'confirmTypedHint';
  wrap.append(label, input, hint);
  msg.appendChild(wrap);
  ok.disabled = true;
  ok.setAttribute('aria-describedby', 'confirmTypedHint');
  input.addEventListener('input', () => { ok.disabled = !typedConfirmMatches(input.value, phrase); });
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); if (!ok.disabled) ok.click(); } });
  input.focus();
  const cleanup = () => { ok.disabled = false; ok.removeAttribute('aria-describedby'); wrap.remove(); };
  result.then(cleanup, cleanup);
  return result;
}
