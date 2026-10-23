// desktop-panel-tools.js — entry of the desktop skin for the test panels
// (Repeater, Intruder, Identities, Collections; docs/desktop-design.md, Panels). Loaded as
// its own module script. In a browser tab nothing here runs: app.js marks
// <html class="desktop-app"> only in a desktop host, and every panel module
// starts after that class is present. The shared helpers live in
// desktop-panel-kit.js, the pure rules in desktop-panel-tools-model.js.
import { isDesktop } from './desktop-panel-kit.js';
import { initRepeaterDesktop } from './desktop-panel-repeater.js';
import { initIntruderDesktop } from './desktop-panel-intruder.js';
import { initCollectionsDesktop } from './desktop-panel-collections.js';
import { initIdentitiesDesktop } from './desktop-panel-identities.js';

function boot() {
  if (!isDesktop() || boot.done) return;
  boot.done = true;
  initRepeaterDesktop();
  initIntruderDesktop();
  initCollectionsDesktop();
  initIdentitiesDesktop();
}

if (isDesktop()) boot();
else {
  const obs = new MutationObserver(() => { if (isDesktop()) { obs.disconnect(); boot(); } });
  obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
}
