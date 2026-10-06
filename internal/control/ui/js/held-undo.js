// held-undo.js — the toast that backs a deferred Intercept drop. It stays up
// for as long as the drop is pending, offers a keyboard-reachable Undo, and
// pauses the countdown while it is hovered or focused (WCAG 2.2.1).

export function showDropToast(message, { onUndo, onPause, onResume }) {
  const host = document.getElementById('toast');
  if (!host) return { dismiss() {} };
  const t = document.createElement('div');
  t.className = 'toast-item info show';
  t.setAttribute('role', 'status');
  t.append(document.createTextNode(message + ' '));
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn xs toast-action';
  btn.textContent = 'Undo';
  t.append(btn);
  host.appendChild(t);
  const dismiss = () => t.remove();
  t.addEventListener('mouseenter', () => onPause());
  t.addEventListener('mouseleave', () => onResume());
  t.addEventListener('focusin', () => onPause());
  t.addEventListener('focusout', () => onResume());
  btn.addEventListener('click', () => { dismiss(); onUndo(); });
  return { dismiss, node: t };
}
