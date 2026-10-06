// soft-keyboard.js — marks the document while a soft keyboard is up so the
// bottom navigation and anchored popovers can get out of its way. One shared
// watcher for dock.js and sheet.js: both used to write the same attribute from
// the same visualViewport event with separate guards.

import { shouldHideDock } from './layout-math.js';

let watched = false;

export function watchSoftKeyboard() {
  const vv = typeof window !== 'undefined' ? window.visualViewport : null;
  if (watched || !vv) return;
  watched = true;
  const sync = () => {
    document.documentElement.dataset.softKeyboard = shouldHideDock(vv.height, window.innerHeight) ? 'true' : 'false';
  };
  vv.addEventListener('resize', sync);
  sync();
}
