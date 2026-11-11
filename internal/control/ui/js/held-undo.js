// held-undo.js — the toast that backs a deferred Intercept drop. It stays up
// for as long as the drop is pending, offers a keyboard-reachable Undo, and
// pauses the countdown while it is hovered or focused (WCAG 2.2.1).

export function showDropToast(message, { onUndo, onPause, onResume }) {
  const host = document.getElementById('toast');
  if (!host) return { dismiss() {} };
  const t = document.createElement('div');
  t.className = 'toast-item info show toast-keep'; // holds an Undo; must outlive newer notices
  t.setAttribute('role', 'status');
  t.append(document.createTextNode(message + ' '));
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn xs toast-action';
  btn.textContent = 'Undo';
  t.append(btn);
  host.appendChild(t);
  const dismiss = () => t.remove();
  // Hover and focus hold the countdown independently: it runs again only when neither is
  // left, so the pointer crossing the toast cannot restart an irreversible drop under a
  // keyboard user who is still on Undo.
  let hovered = false, focused = false;
  const sync = (wasHeld) => {
    const held = hovered || focused;
    if (held && !wasHeld) onPause();
    else if (!held && wasHeld) onResume();
  };
  const hold = (which, on) => () => {
    const was = hovered || focused;
    if (which === 'hover') hovered = on; else focused = on;
    sync(was);
  };
  t.addEventListener('mouseenter', hold('hover', true));
  t.addEventListener('mouseleave', hold('hover', false));
  t.addEventListener('focusin', hold('focus', true));
  t.addEventListener('focusout', hold('focus', false));
  btn.addEventListener('click', () => { dismiss(); onUndo(); });
  return { dismiss, node: t };
}
